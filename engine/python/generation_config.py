"""Private generation profiles. No SDK imports, model calls or runtime switching.

The controller owns this lock while serving; the launcher owns it during startup.
Profile revisions are immutable so editing a candidate cannot alter active jobs.
"""
import copy
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import re
import uuid
from urllib.parse import urlparse

from project_control import private_write, read_secret
from project_policy import PolicyError

PROVIDERS = {'openai': 'openai', 'anthropic': 'anthropic', 'gemini': 'gemini', 'custom': 'openai',
             'ollama': 'ollama_chat'}
IDENTIFIER = re.compile(r'[a-zA-Z0-9_-]{1,64}')


def requires_credential(profile):
    return profile.get('mode') == 'provider' and profile.get('provider') != 'ollama'


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()).hexdigest()


def validate_profile(value):
    if not isinstance(value, dict):
        raise PolicyError('invalid_generation_profile')
    allowed = {'mode', 'provider', 'bridge_kind', 'bridge_provider', 'model', 'endpoint', 'credential_ref', 'credential_version',
               'temperature', 'max_output_tokens', 'max_concurrent_requests'}
    if set(value) - allowed:
        raise PolicyError('invalid_generation_fields')
    model = value.get('model')
    if not isinstance(model, str) or not model.strip() or len(model) > 200 or any(ord(c) < 32 for c in model):
        raise PolicyError('invalid_generation_model')
    mode = value.get('mode')
    if 'max_concurrent_requests' in value:
        capacity = value['max_concurrent_requests']
        if type(capacity) is not int or not 1 <= capacity <= 256:
            raise PolicyError('invalid_generation_capacity')
    if mode == 'agent_bridge':
        if value.get('bridge_kind') not in ('codex', 'pi', 'traex', 'antigravity') or set(value) - {'mode', 'bridge_kind', 'bridge_provider', 'model', 'max_concurrent_requests'}:
            raise PolicyError('unsupported_generation_bridge')
        if value['bridge_kind'] == 'pi':
            if value.get('bridge_provider') not in ('deepseek', 'ollama'):
                raise PolicyError('unsupported_pi_generation_provider')
        elif 'bridge_provider' in value:
            raise PolicyError('invalid_generation_bridge_provider')
    elif mode == 'provider':
        if value.get('provider') not in PROVIDERS or 'bridge_kind' in value or 'bridge_provider' in value:
            raise PolicyError('unsupported_generation_provider')
        if 'temperature' in value:
            temperature = value['temperature']
            if type(temperature) not in (int, float) or not 0 <= temperature <= 2 or not math.isfinite(temperature):
                raise PolicyError('invalid_generation_temperature')
        if 'max_output_tokens' in value:
            tokens = value['max_output_tokens']
            if type(tokens) is not int or not 1 <= tokens <= 131072:
                raise PolicyError('invalid_generation_output_limit')
        endpoint = value.get('endpoint', '')
        if not isinstance(endpoint, str):
            raise PolicyError('invalid_generation_endpoint')
        parsed = urlparse(endpoint)
        if (not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment
                or (parsed.scheme != 'https' and not (parsed.scheme == 'http' and parsed.hostname in ('127.0.0.1', '::1', 'localhost')))):
            raise PolicyError('invalid_generation_endpoint')
        if value['provider'] == 'ollama':
            if (parsed.hostname not in ('127.0.0.1', '::1', 'localhost') or parsed.path not in ('', '/')
                    or 'credential_ref' in value or 'credential_version' in value):
                raise PolicyError('invalid_local_ollama_profile')
            try:
                parsed.port
            except ValueError:
                raise PolicyError('invalid_generation_endpoint') from None
        elif not isinstance(value.get('credential_ref'), str) or not IDENTIFIER.fullmatch(value['credential_ref']):
            raise PolicyError('private_generation_credential_required')
        if requires_credential(value) and (not isinstance(value.get('credential_version'), str) or not IDENTIFIER.fullmatch(value['credential_version'])):
            raise PolicyError('generation_credential_version_required')
    else:
        raise PolicyError('unsupported_generation_mode')
    result = copy.deepcopy(value)
    if 'temperature' in result:
        result['temperature'] = float(result['temperature'])
    return result


def compile_environment(profile, runtime, credential=None):
    """Map a validated profile onto the fixed official adapter's variables."""
    p = validate_profile(profile)
    result = {'STRUCTURED_OUTPUT_FRAMEWORK': 'litellm_native', 'LITELLM_LOCAL_MODEL_COST_MAP': 'true'}
    if p['mode'] == 'agent_bridge':
        provider, prefix = 'openai', 'openai'
        endpoint = runtime['completion_url'].rstrip('/') + '/v1'
        key = read_secret(runtime['completion_token_file'])
        # Reuse upstream chunk batching for independent extraction. The shared
        # endpoint transport still bounds all downstream requests to its total
        # capacity; two chunks do not create two whole processing jobs.
        result['CHUNKS_PER_BATCH'] = '2'
    else:
        provider, prefix = p['provider'], PROVIDERS[p['provider']]
        endpoint, key = p['endpoint'], credential
        if provider == 'ollama':
            # The fixed Cognee adapter requires a nonempty key even for local
            # Ollama. This public SDK placeholder is never stored as a secret.
            key = 'ollama'
            # LiteLLM's capability/token lookups omit api_base and consult this
            # variable. Keep their /api/show on the selected local service too.
            result['OLLAMA_API_BASE'] = endpoint
        if not isinstance(key, str) or not key.strip():
            raise PolicyError('generation_credential_unavailable', 503)
    model = p['model']
    # Keep organization/model IDs for compatible endpoints. Only strip the
    # explicitly selected protocol prefix, never guess from a model name.
    qualified = model if model.startswith(prefix + '/') else prefix + '/' + model
    result.update(LLM_PROVIDER=provider, LLM_MODEL=qualified, LLM_ENDPOINT=endpoint, LLM_API_KEY=key)
    if 'temperature' in p:
        result['LLM_TEMPERATURE'] = str(p['temperature'])
    if 'max_output_tokens' in p:
        result['LLM_MAX_COMPLETION_TOKENS'] = str(p['max_output_tokens'])
        # The pinned native adapter forwards llm_args, but does not inject its
        # stored max_completion_tokens into ordinary requests itself.
        result['LLM_ARGS'] = json.dumps({'max_tokens': p['max_output_tokens']}, separators=(',', ':'))
    return result


def native_llm_kwargs(environment):
    """One explicit-only boundary for startup, validation and live handoff.

    Never synthesize sampling/output values. In particular, an omitted option
    is different from an explicit zero. The isolated prefix also prevents the
    previous profile's process environment from becoming the new profile.
    """
    fields = {'LLM_PROVIDER':'llm_provider', 'LLM_MODEL':'llm_model',
              'LLM_ENDPOINT':'llm_endpoint', 'LLM_API_KEY':'llm_api_key',
              'LLM_TEMPERATURE':'llm_temperature',
              'LLM_MAX_COMPLETION_TOKENS':'llm_max_completion_tokens',
              'LLM_ARGS':'llm_args', 'STRUCTURED_OUTPUT_FRAMEWORK':'structured_output_framework'}
    result = {'_env_file':None, '_env_prefix':'MEMLANE_HANDOFF_ISOLATED_'}
    for key, field in fields.items():
        if key in environment:
            result[field] = json.loads(environment[key]) if key == 'LLM_ARGS' else environment[key]
    return result


def native_llm_configuration(environment):
    # Import only after the caller has selected the verified SDK. Keeping the
    # builder pure above lets all modes enforce the same omission contract.
    from cognee.infrastructure.llm.config import LLMConfig
    return LLMConfig(**native_llm_kwargs(environment))


def validation_passed(validations, digest):
    matches = [row for row in validations.values() if row.get('digest') == digest]
    if not matches:
        return False
    revision = max(row.get('registered_revision', 0) for row in matches)
    # Old receipts have no reliable ordering. A conflicting old receipt must
    # not be guessed newer/older from its operation ID or JSON key order.
    latest = [row for row in matches if row.get('registered_revision', 0) == revision]
    return all(row.get('status') == 'passed' and row.get('plain_passed') is True
               and row.get('schema_passed') is True and row.get('quality_passed') is True for row in latest)


class GenerationStore:
    def __init__(self, path, service_commit, *, legacy_model=None):
        self.path, self.service_commit = Path(path), service_commit
        self.path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.lock = os.open(str(self.path) + '.lock', os.O_CREAT | os.O_RDWR, 0o600)
        try:
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if self.path.exists():
                if self.path.is_symlink() or self.path.stat().st_mode & 0o077:
                    raise PolicyError('private_generation_config_required', 503)
                self.state = json.loads(self.path.read_text())
                if self.state.get('schema_version') != 2:
                    raise PolicyError('unsupported_generation_config', 503)
            else:
                self.state = {'schema_version': 2, 'revision': 0, 'profiles': {}, 'active': None,
                              'next': None, 'validations': {}, 'operations': {}}
                if legacy_model:
                    profile = {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': legacy_model}
                    self.state['profiles']['existing-codex'] = [self.record(profile, 1)]
                    self.state['active'] = {'id': 'existing-codex', 'revision': 1}
                self.save()
        except BaseException:
            os.close(self.lock)
            raise

    def close(self):
        os.close(self.lock)

    def save(self):
        private_write(self.path, json.dumps(self.state, ensure_ascii=False, sort_keys=True).encode())

    def record(self, profile, revision):
        validated = validate_profile(profile)
        credential_digest = (hashlib.sha256(self.secret(validated).encode()).hexdigest()
                             if requires_credential(validated) else None)
        return {'revision': revision, 'profile': validated,
                'digest': fingerprint({'profile': validated, 'service_commit': self.service_commit,
                                       'credential_digest': credential_digest})}

    def checked(self, expected, operation_id, request):
        if not isinstance(operation_id, str) or not IDENTIFIER.fullmatch(operation_id):
            raise PolicyError('invalid_operation_id')
        old = self.state['operations'].get(operation_id)
        signature = fingerprint(request)
        if old:
            if old['signature'] != signature:
                raise PolicyError('operation_id_conflict', 409)
            return copy.deepcopy(old['result'])
        if type(expected) is not int or expected != self.state['revision']:
            raise PolicyError('revision_conflict', 409)
        return None

    def commit(self, operation_id, request, result):
        self.state['revision'] += 1
        self.state['operations'][operation_id] = {'signature': fingerprint(request), 'result': copy.deepcopy(result)}
        self.save()
        return result

    def credential_path(self, ref, version):
        if not all(isinstance(item, str) and IDENTIFIER.fullmatch(item) for item in (ref, version)):
            raise PolicyError('invalid_generation_credential_reference')
        directory = self.path.parent / 'generation-credentials'
        if directory.is_symlink() or (directory.exists() and (not directory.is_dir() or directory.stat().st_mode & 0o077)):
            raise PolicyError('private_generation_credentials_required', 503)
        return directory / (ref + '-' + version)

    def secret(self, profile):
        try:
            return read_secret(self.credential_path(profile['credential_ref'], profile['credential_version']))
        except OSError:
            raise PolicyError('generation_credential_unavailable', 503) from None

    def put(self, identifier, profile, expected, operation_id, *, api_key=None):
        if not isinstance(identifier, str) or not IDENTIFIER.fullmatch(identifier):
            raise PolicyError('invalid_generation_profile_id')
        request = {'action': 'put', 'id': identifier, 'profile': profile,
                   'key_digest': fingerprint(api_key) if api_key is not None else None}
        old = self.checked(expected, operation_id, request)
        if old is not None:
            return old
        if identifier in self.state.get('removed_profiles', {}):
            raise PolicyError('generation_profile_removed', 409)
        profile = copy.deepcopy(profile)
        if profile.get('provider') == 'ollama' and api_key is not None:
            raise PolicyError('local_ollama_credential_not_required')
        if api_key is not None:
            if (profile.get('mode') != 'provider' or not isinstance(api_key, str) or not api_key.strip()
                    or len(api_key) > 8192 or any(c in api_key for c in '\r\n')):
                raise PolicyError('invalid_generation_credential')
            profile['credential_ref'], profile['credential_version'] = identifier, uuid.uuid4().hex
        elif requires_credential(profile) and not profile.get('credential_ref'):
            rows = self.state['profiles'].get(identifier, [])
            previous = rows[-1]['profile'] if rows else {}
            if profile.get('credential_version') == previous.get('credential_version') and previous.get('credential_ref'):
                profile['credential_ref'] = previous['credential_ref']
        validated = validate_profile(profile)
        if requires_credential(validated):
            path = self.credential_path(validated['credential_ref'], validated['credential_version'])
            if api_key is not None:
                if path.exists():
                    raise PolicyError('immutable_generation_credential', 409)
                private_write(path, api_key.encode())
            else:
                self.secret(validated)
        rows = self.state['profiles'].setdefault(identifier, [])
        row = self.record(validated, len(rows) + 1)
        rows.append(row)
        return self.commit(operation_id, request, {'id': identifier, 'revision': row['revision'], 'digest': row['digest']})

    def resolve(self, reference):
        try:
            if reference['id'] in self.state.get('removed_profiles', {}):
                raise KeyError()
            if type(reference['revision']) is not int or reference['revision'] < 1:
                raise KeyError()
            row = self.state['profiles'][reference['id']][reference['revision'] - 1]
        except (KeyError, IndexError, TypeError):
            raise PolicyError('generation_profile_not_found', 404) from None
        if row['digest'] != self.record(row['profile'], row['revision'])['digest']:
            raise PolicyError('generation_validation_stale', 409)
        return copy.deepcopy(row)

    def remove(self, identifier, revision, expected, operation_id):
        request = {'action': 'remove', 'id': identifier, 'revision': revision}
        old = self.checked(expected, operation_id, request)
        if old is not None:
            return old
        rows = self.state['profiles'].get(identifier, [])
        if (not rows or identifier in self.state.get('removed_profiles', {}) or type(revision) is not int
                or rows[-1]['revision'] != revision or rows[-1]['profile']['mode'] != 'provider'):
            raise PolicyError('generation_profile_changed', 409)
        if any((self.state.get(key) or {}).get('id') == identifier for key in ('active', 'next')):
            raise PolicyError('generation_profile_in_use', 409)
        if any(row.get('reference', {}).get('id') == identifier and row.get('status') in
               ('queued','preparing','stopping','applying','unknown') for row in self.state.get('handoffs', {}).values()):
            raise PolicyError('generation_profile_operation_pending', 409)
        if any(value.get('reference', {}).get('id') == identifier
               and value.get('status') in ('queued', 'running', 'result_unknown')
               for value in self.state['validations'].values()):
            raise PolicyError('generation_profile_operation_pending', 409)
        # Retain immutable history and credentials for original operation receipts,
        # but withdraw the configuration from all new selection/validation paths.
        self.state.setdefault('removed_profiles', {})[identifier] = {'revision': revision}
        return self.commit(operation_id, request, {'id': identifier, 'deleted': True})

    def select_next(self, reference, expected, operation_id):
        request = {'action': 'next', 'reference': reference}
        old = self.checked(expected, operation_id, request)
        if old is not None:
            return old
        row = self.resolve(reference)
        if row['profile']['mode'] == 'agent_bridge' and getattr(self, 'connected_bridge_check', None):
            self.connected_bridge_check(row['profile'])
            self.state.setdefault('connected_agent_selections', {})[row['digest']] = True
        elif not validation_passed(self.state['validations'], row['digest']):
            raise PolicyError('generation_validation_required', 409)
        self.native_schema_evidence(row)
        self.state['next'] = copy.deepcopy(reference)
        return self.commit(operation_id, request, {'next': copy.deepcopy(reference)})

    def environment(self, reference, runtime):
        profile = self.resolve(reference)['profile']
        credential = (self.secret(profile)
                      if requires_credential(profile) else None)
        return compile_environment(profile, runtime, credential)

    def native_schema_evidence(self, row):
        if row['profile'].get('provider') != 'ollama':
            return None
        from generation_ollama import evidence_matches
        matches = [v for v in self.state['validations'].values() if v.get('digest') == row['digest']]
        if not matches or not validation_passed(self.state['validations'], row['digest']):
            raise PolicyError('generation_native_schema_evidence_required', 409)
        revision = max(v.get('registered_revision', 0) for v in matches)
        latest = [v for v in matches if v.get('registered_revision', 0) == revision]
        environment = compile_environment(row['profile'], {})
        if any(not evidence_matches(v.get('native_schema_evidence'), environment['LLM_MODEL'], environment['LLM_ENDPOINT']) for v in latest):
            raise PolicyError('generation_native_schema_evidence_required', 409)
        return copy.deepcopy(latest[0]['native_schema_evidence'])

    def view(self):
        result = copy.deepcopy(self.state)
        result['handoff_available'] = bool(getattr(self, 'runtime_handoff', False))
        result.pop('operations', None)
        for identifier in result.pop('removed_profiles', {}):
            result['profiles'].pop(identifier, None)
        for rows in result['profiles'].values():
            for row in rows:
                profile = row['profile']
                if requires_credential(profile):
                    profile['credential_configured'] = self.credential_path(profile['credential_ref'], profile['credential_version']).is_file()
                    profile.pop('credential_ref', None)
        return result


def runtime_generation(runtime):
    """Read one immutable revision while the launcher/controller owns the lock.

    This reader never writes or selects `next`; services cannot switch profiles
    because the manager saves another candidate during an in-flight request.
    """
    reference = runtime.get('generation_reference')
    if reference is None:
        profile = {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': runtime['model']}
        return profile, compile_environment(profile, runtime), None
    path = Path(runtime['generation_file'])
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise PolicyError('private_generation_config_required', 503)
    reader = object.__new__(GenerationStore)
    reader.path, reader.service_commit = path, runtime['service_commit']
    reader.state = json.loads(path.read_text())
    if reader.state.get('schema_version') != 2:
        raise PolicyError('unsupported_generation_config', 503)
    row = reader.resolve(reference)
    environment = reader.environment(reference, runtime)
    evidence = reader.native_schema_evidence(row)
    if evidence is not None:
        environment['MEMLANE_OLLAMA_SCHEMA_EVIDENCE'] = json.dumps(evidence, sort_keys=True)
    return row['profile'], environment, row['digest']
