#!/usr/bin/env python3
"""Local, non-streaming Cognee completion adapter; no memory business state."""
import argparse
import asyncio
import copy
from collections import OrderedDict
from contextlib import contextmanager
import hmac
import json
import math
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import tempfile
from threading import Event, Lock
import time
import uuid

from aiohttp import web
from jsonschema import validators
from process_groups import group_exists
from performance_trace import RequestTrace, current_trace, phase, HEADER

MAX_BODY = 2 * 1024 * 1024
PROFILE = 'memlane_completion'


class CompletionError(Exception):
    def __init__(self, code, status=502):
        self.code, self.status = code, status
        super().__init__(code)



def remote_error(raw):
    # Interpret only the machine error tag, never expose model/provider prose.
    info = raw.get('codexErrorInfo') if isinstance(raw, dict) else None
    tag = None
    if isinstance(info, str):
        tag = info
    elif isinstance(info, dict) and len(info) == 1:
        key = next(iter(info))
        if isinstance(key, str):
            tag = key
    codes = {'unauthorized': ('codex_authentication_failed', 502),
             'usageLimitExceeded': ('codex_quota_exceeded', 429),
             'sessionBudgetExceeded': ('codex_quota_exceeded', 429),
             'rateLimitExceeded': ('codex_rate_limited', 429),
             'modelNotFound': ('codex_model_unavailable', 502),
             'contextWindowExceeded': ('codex_context_exceeded', 400),
             'badRequest': ('codex_request_invalid', 400),
             'sandboxError': ('codex_isolation_failed', 502),
             'httpConnectionFailed': ('codex_connection_failed', 502),
             'responseStreamDisconnected': ('codex_stream_disconnected', 502),
             'responseTooManyFailedAttempts': ('codex_connection_failed', 502),
             'internalServerError': ('codex_upstream_error', 502)}
    # App-server can wrap a provider's machine error in an `other` message.
    # Read only the JSON machine code; never return arbitrary provider prose.
    if tag == 'other' and isinstance(raw.get('message'), str):
        try:
            detail = json.loads(raw['message'])
            if isinstance(detail, dict) and isinstance(detail.get('error'), dict):
                machine_code = detail['error'].get('code')
                if machine_code == 'invalid_json_schema':
                    return CompletionError('codex_schema_unsupported', 400)
                if detail.get('status') == 400 and detail['error'].get('type') == 'invalid_request_error':
                    return CompletionError('codex_request_invalid', 400)
        except (ValueError, TypeError):
            pass
    code, status = codes.get(tag, ('codex_model_failed', 502)) if isinstance(tag, str) else ('codex_model_failed', 502)
    return CompletionError(code, status)


def strict_schema(source):
    """Strengthen finite Pydantic models; validate against both contracts later."""
    root = copy.deepcopy(source)
    if not isinstance(root, dict):
        raise CompletionError('codex_schema_invalid', 400)

    def walk(node, depth=0):
        if not isinstance(node, dict):
            return
        if depth > 64:
            raise CompletionError('codex_schema_invalid', 400)
        if any(key in node and (not isinstance(node[key], str) or not node[key].startswith('#')) for key in ('$ref', '$dynamicRef', '$recursiveRef')):
            raise CompletionError('codex_schema_unsupported', 400)
        node.pop('default', None)
        if 'oneOf' in node:
            # Pydantic discriminated unions use oneOf, which native strict
            # output rejects. Transport accepts any branch; the original
            # oneOf remains authoritative in execute's second validation.
            if 'anyOf' in node or not isinstance(node.get('discriminator'), dict):
                raise CompletionError('codex_schema_unsupported', 400)
            node['anyOf'] = node.pop('oneOf')
        node.pop('discriminator', None)
        if node.get('type') == 'object' or 'properties' in node:
            props = node.get('properties')
            if props is None:
                if node.get('additionalProperties') is not False:
                    raise CompletionError('codex_schema_unsupported', 400)
                props = node['properties'] = {}
            if not isinstance(props, dict) or node.get('additionalProperties', False) is not False:
                raise CompletionError('codex_schema_unsupported', 400)
            node['additionalProperties'] = False
            required = node.get('required', [])
            if not isinstance(required, list) or any(not isinstance(x, str) or x not in props for x in required) or len(set(required)) != len(required):
                raise CompletionError('codex_schema_invalid', 400)
            node['required'] = required + sorted(set(props) - set(required))
        for key in ('properties', '$defs', 'definitions', 'patternProperties', 'dependentSchemas'):
            for child in node.get(key, {}).values():
                walk(child, depth + 1)
        for key in ('items', 'contains', 'not', 'if', 'then', 'else', 'propertyNames', 'unevaluatedProperties', 'unevaluatedItems'):
            walk(node.get(key), depth + 1)
        for key in ('anyOf', 'allOf', 'oneOf', 'prefixItems'):
            for child in node.get(key, []):
                walk(child, depth + 1)
    try:
        validators.validator_for(root).check_schema(root)
        walk(root)
    except CompletionError:
        raise
    except Exception:
        raise CompletionError('codex_schema_invalid', 400) from None
    return root


def parse_request(body, model):
    if not isinstance(body, dict) or body.get('model') != model:
        raise CompletionError('codex_model_mismatch', 400)
    if body.get('stream') or body.get('tools') or body.get('functions'):
        raise CompletionError('codex_request_unsupported', 400)
    fmt = body.get('response_format')
    if fmt is None or isinstance(fmt, dict) and fmt.get('type') == 'text':
        # Cognee's own connection check (response_model=str) and other plain
        # completions deliberately have no business schema. Keep Codex's
        # native structured transport and validate its string envelope, then
        # unwrap text only for this explicitly unstructured input contract.
        schema = None
        transport = {'type': 'object', 'properties': {'text': {'type': 'string'}},
                     'required': ['text'], 'additionalProperties': False}
    elif isinstance(fmt, dict) and fmt.get('type') == 'json_schema':
        schema = fmt.get('json_schema', {}).get('schema')
        if len(json.dumps(schema)) > 65536:
            raise CompletionError('codex_schema_invalid', 400)
        transport = strict_schema(schema)
    else:
        # Do not downgrade a structured business request into arbitrary JSON
        # or text; the caller must supply its actual JSON schema.
        raise CompletionError('codex_response_format_unsupported', 400)
    messages = body.get('messages')
    if not isinstance(messages, list) or not messages:
        raise CompletionError('codex_messages_invalid', 400)
    system, prompt = [], []
    for message in messages:
        if not isinstance(message, dict) or message.get('role') not in ('system', 'developer', 'user', 'assistant'):
            raise CompletionError('codex_messages_invalid', 400)
        content = message.get('content')
        if isinstance(content, list):
            if any(not isinstance(x, dict) or x.get('type') != 'text' or not isinstance(x.get('text'), str) for x in content):
                raise CompletionError('codex_messages_invalid', 400)
            content = '\n'.join(x['text'] for x in content)
        if not isinstance(content, str):
            raise CompletionError('codex_messages_invalid', 400)
        if message['role'] in ('system', 'developer'):
            system.append(content)
        else:
            prompt.append({'role': message['role'], 'content': content})
    if not prompt:
        raise CompletionError('codex_messages_invalid', 400)
    return '\n\n'.join(system), json.dumps(prompt, ensure_ascii=False), schema, transport


DISABLED_FEATURES = (
    'shell_tool', 'unified_exec', 'apps', 'plugins', 'hooks', 'memories', 'multi_agent', 'multi_agent_v2',
    'browser_use', 'browser_use_external', 'computer_use', 'image_generation', 'skill_search',
    'skill_mcp_dependency_install', 'workspace_dependencies', 'code_mode_host', 'view_image', 'remote_plugin',
)
# Use only the known common TraeX keys when the CLI cannot list its features.
TRAE_FEATURE_FALLBACK = frozenset(DISABLED_FEATURES) - {'view_image', 'remote_plugin'}
_feature_cache = OrderedDict()
_feature_cache_lock = Lock()
_feature_probes_inflight = {}
_MAX_FEATURE_CACHE = 64


def feature_cache_key(executable, provider):
    try:
        info = os.stat(executable)
    except OSError:
        return None
    return str(executable), info.st_ino, info.st_mtime_ns, info.st_size, provider


def _cache_features(key, supported, excluded):
    # Caller holds the lock, including when combining concurrent discoveries.
    _feature_cache[key] = supported, excluded
    _feature_cache.move_to_end(key)
    if len(_feature_cache) > _MAX_FEATURE_CACHE:
        _feature_cache.popitem(last=False)


def probe_supported_features(executable, *, provider='openai', env=None, cwd=None, timeout=5.0):
    key = feature_cache_key(executable, provider)
    if key is None:
        return None
    while True:
        with _feature_cache_lock:
            if key in _feature_cache:
                _feature_cache.move_to_end(key)
                return _feature_cache[key][0]
            inflight = _feature_probes_inflight.get(key)
            if inflight is None:
                inflight = Event()
                _feature_probes_inflight[key] = inflight
                break
        inflight.wait(timeout=timeout + 1.0)

    def run(environment, work):
        proc = subprocess.Popen([str(executable), 'features', 'list'], env=environment, cwd=work,
                                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                encoding='utf-8', errors='replace', start_new_session=True)
        try:
            stdout, _ = proc.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError):
                proc.kill()
            try:
                proc.communicate(timeout=2)
            except subprocess.TimeoutExpired:
                pass
            raise
        features = set()
        if proc.returncode == 0:
            for line in stdout.splitlines():
                tokens = line.split()
                if (len(tokens) >= 3 and re.fullmatch(r'[a-z0-9_]+', tokens[0])
                        and tokens[-1] in ('true', 'false') and ' '.join(tokens[1:-1]).lower() != 'removed'):
                    features.add(tokens[0])
        return frozenset(features) if features else None

    try:
        try:
            if env is None:
                with tempfile.TemporaryDirectory(prefix='memlane-features-') as directory:
                    environment = {'HOME': directory, 'TMPDIR': directory,
                                   'PATH': str(Path(executable).absolute().parent) + ':/usr/bin:/bin'}
                    if provider == 'trae':
                        cli = Path(directory) / 'cli'
                        cli.mkdir(mode=0o700)
                        environment.update(TRAE_HOME=directory, TRAECLI_HOME=str(cli), MEMLANE_PROCESSING_SESSION='1')
                    else:
                        environment['CODEX_HOME'] = directory
                    supported = run(environment, cwd if cwd is not None else directory)
            else:
                supported = run(dict(env), cwd)
        except (OSError, subprocess.TimeoutExpired):
            supported = None

        with _feature_cache_lock:
            previous, excluded = _feature_cache.get(key, (None, frozenset()))
            if supported is None:
                supported = previous
            _cache_features(key, supported, excluded)
        return supported
    finally:
        with _feature_cache_lock:
            _feature_probes_inflight.pop(key, None)
            inflight.set()


def disabled_features(provider, supported_features, excluded_features):
    selected = supported_features
    if selected is None:
        selected = TRAE_FEATURE_FALLBACK if provider == 'trae' else DISABLED_FEATURES
    return [name for name in DISABLED_FEATURES if name in selected and name not in (excluded_features or ())]


def parse_unknown_feature_error(stderr, candidates):
    text = stderr.decode(errors='replace') if isinstance(stderr, bytes) else stderr
    for line in text.splitlines():
        for pattern in (
            r"""(?i)\bunknown (?:configuration )?field\s*[:=]?\s*['"`]features\.([a-z0-9_]+)['"`]""",
            r"""(?i)\b(?:unknown|invalid) feature\s*[:=]?\s*['"`]?(?:features\.)?([a-z0-9_]+)(?![a-z0-9_-])""",
        ):
            for match in re.finditer(pattern, line):
                if match[1] in candidates:
                    return match[1]
        if any(marker in line.lower() for marker in ('features', 'config.toml', 'traecli.toml')):
            for match in re.finditer(
                    r"""(?i)\bunknown (?:configuration )?field\s*[:=]?\s*['"`]([a-z0-9_]+)['"`]""", line):
                if match[1] in candidates:
                    return match[1]
    return None


class UnsupportedStartupFeature(Exception):
    pass


def private_config(model, work, provider='openai', defaults=None, supported_features=None, excluded_features=None):
    features = disabled_features(provider, supported_features, excluded_features)
    options = []
    if defaults and 'reasoning_effort' in defaults:
        options.append('model_reasoning_effort = ' + json.dumps(defaults['reasoning_effort']))
    if defaults and 'service_tier' in defaults:
        options.append('service_tier = ' + json.dumps(defaults['service_tier']))
    return '\n'.join([
        'model = ' + json.dumps(model), 'model_provider = ' + json.dumps(provider),
        *options,
        'default_permissions = "' + PROFILE + '"', 'web_search = "disabled"',
        'project_doc_max_bytes = 0', '[features]',
        *[name + ' = false' for name in features],
        '[memories]', 'extract_model = ' + json.dumps(model), 'consolidation_model = ' + json.dumps(model),
        '[skills]', 'include_instructions = false', '[skills.bundled]', 'enabled = false',
        '[analytics]', 'enabled = false', '[feedback]', 'enabled = false',
        '[permissions.' + PROFILE + '.filesystem]', '"/" = "deny"', '":minimal" = "read"', json.dumps(str(work)) + ' = "read"',
        '[permissions.' + PROFILE + '.network]', 'enabled = false', ''
    ])


class ActivityClock:
    """Monotonic idle-activity clock; resets deadline on child stdout/stderr output."""

    def __init__(self, idle_timeout):
        try:
            threshold = float(idle_timeout)
        except (TypeError, ValueError):
            raise ValueError('idle_timeout must be a positive finite number') from None
        if not math.isfinite(threshold) or threshold <= 0:
            raise ValueError('idle_timeout must be a positive finite number')
        self.idle_timeout = threshold
        self.last_activity = time.monotonic()

    def touch(self):
        self.last_activity = time.monotonic()

    def remaining(self):
        return self.idle_timeout - (time.monotonic() - self.last_activity)


async def await_with_idle(awaitable, clock):
    """Await an operation until completion or idle_timeout of silence on clock."""
    task = asyncio.ensure_future(awaitable)
    try:
        while not task.done():
            remaining = clock.remaining()
            if remaining <= 0:
                raise TimeoutError()
            done, _ = await asyncio.wait({task}, timeout=remaining)
            if task in done:
                break
        return await task
    except BaseException:
        if not task.done():
            task.cancel()
            while not task.done():
                try:
                    await asyncio.shield(task)
                except BaseException:
                    pass
        raise


async def read_line_with_idle(stream, clock):
    """Read one line from stream, keeping the request alive while output flows."""
    line = await await_with_idle(stream.readline(), clock)
    if line:
        clock.touch()
    return line


class RPC:
    def __init__(self, process, clock=None):
        self.process, self.counter, self.received, self.clock = process, 0, 0, clock

    async def send(self, method, params, notification=False):
        message = {'method': method, 'params': params}
        if not notification:
            self.counter += 1
            message['id'] = self.counter
        self.process.stdin.write(json.dumps(message).encode() + b'\n')
        if self.clock is not None:
            await await_with_idle(self.process.stdin.drain(), self.clock)
        else:
            await self.process.stdin.drain()
        return message.get('id')

    async def read(self):
        try:
            if self.clock is not None:
                line = await read_line_with_idle(self.process.stdout, self.clock)
            else:
                line = await self.process.stdout.readline()
            if not line:
                raise CompletionError('codex_transport_closed')
            self.received += len(line)
            if self.received > 64 * 1024 * 1024:
                raise CompletionError('codex_output_limit')
            message = json.loads(line)
            if not isinstance(message, dict):
                raise CompletionError('codex_transport_invalid')
            return message
        except (ValueError, asyncio.LimitOverrunError):
            raise CompletionError('codex_transport_invalid') from None

    async def call(self, method, params):
        request_id = await self.send(method, params)
        while True:
            reply = await self.read()
            if 'id' in reply:
                if reply.get('method') or reply['id'] != request_id or reply.get('error'):
                    raise CompletionError('codex_rpc_rejected')
                return reply.get('result', {})
            if reply.get('method') == 'error':
                raise remote_error(reply.get('params', {}).get('error'))


async def terminate(process):
    """Wait for the owned session leader and all processes in its group."""
    process.stdin.close()
    for sig, seconds in ((signal.SIGTERM, 3), (signal.SIGKILL, 3)):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            pass
        except PermissionError:
            if group_exists(process.pid):
                raise
        try:
            await asyncio.wait_for(process.wait(), seconds)
        except asyncio.TimeoutError:
            pass
        deadline = asyncio.get_running_loop().time() + seconds
        while asyncio.get_running_loop().time() < deadline:
            if not group_exists(process.pid):
                return
            await asyncio.sleep(.05)
    raise CompletionError('codex_process_cleanup_unknown', 503)



async def collect_stderr(stream, on_activity=None):
    # Keep draining after the retained prefix fills: pipe backpressure must not
    # strand a child during failure or cleanup. This is never a public log.
    retained = bytearray()
    while True:
        chunk = await stream.read(4096)
        if not chunk:
            return bytes(retained)
        if on_activity is not None:
            on_activity()
        retained.extend(chunk[:max(0, 16384 - len(retained))])


def record_start_failure(args, process, code, stderr):
    from trae_ide_hook import scrub_diagnostic_secrets

    directory = getattr(args, 'diagnostics_dir', None)
    if directory is None:
        token_file = getattr(args, 'token_file', None)
        directory = token_file.parent / 'diagnostics' if token_file else None
    if directory is None:
        return
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    if directory.is_symlink():
        raise CompletionError('codex_diagnostic_directory_invalid', 503)
    record = {'at': int(time.time()), 'failure_code': code,
              'pid': process.pid, 'exit_code': process.returncode,
              'stderr': scrub_diagnostic_secrets(stderr.decode(errors='replace'))}
    payload = json.dumps(record, ensure_ascii=False).encode()
    while len(payload) > 16384:
        record['stderr'] = record['stderr'][:len(record['stderr']) // 2]
        payload = json.dumps(record, ensure_ascii=False).encode()
    temporary = directory / ('.codex-start-failure-' + uuid.uuid4().hex)
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as handle:
        handle.write(payload)
    os.replace(temporary, directory / 'codex-start-failure.json')


@contextmanager
def completion_workspace(args):
    root = Path(tempfile.mkdtemp(prefix='memlane-completion-', dir=getattr(args, 'workspace_root', None)))
    try:
        yield root
    finally:
        # A validation owner retains its auth/config/workspace if cleanup is
        # unknown. Ordinary endpoint behavior remains independently owned.
        if not getattr(args, 'owned_processes', None):
            shutil.rmtree(root)


def track_process(args, process, owned):
    registry = getattr(args, 'owned_processes', None)
    if registry is None:
        return
    if owned:
        registry[process.pid] = process
    else:
        registry.pop(process.pid, None)
    callback = getattr(args, 'ownership_changed', None)
    if callback:
        callback()


async def execute(args, system, prompt, schema, transport):
    args = default_request_args(args)
    phase('workspace_prepare')
    kind = getattr(args, 'bridge_kind', 'codex')
    if kind not in ('codex', 'traex'):
        raise CompletionError('unsupported_generation_bridge', 400)
    provider = 'trae' if kind == 'traex' else 'openai'
    auth = args.auth_home / ('cli/auth.json' if kind == 'traex' else 'auth.json')
    if not auth.is_file():
        raise CompletionError('codex_authentication_required', 503)
    with completion_workspace(args) as root:
        home, private, work = (root / name for name in ('home', 'codex', 'task'))
        for directory in (home, private, work):
            directory.mkdir(mode=0o700)
        config = private / ('traecli.toml' if kind == 'traex' else 'config.toml')
        environment = {'HOME': str(home), 'CODEX_HOME': str(private), 'TMPDIR': str(root),
                       'PATH': str(args.codex_executable.parent) + ':/usr/bin:/bin'}
        if kind == 'traex':
            environment.pop('CODEX_HOME')
            environment.update(TRAE_HOME=str(private), TRAECLI_HOME=str(private / 'cli'), MEMLANE_PROCESSING_SESSION='1')
        for key in ('LANG', 'LC_ALL', 'HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'NO_PROXY'):
            if key in os.environ:
                environment[key] = os.environ[key]
        cache_key = feature_cache_key(args.codex_executable, provider)
        supported = await asyncio.to_thread(probe_supported_features, args.codex_executable, provider=provider)
        with _feature_cache_lock:
            _, excluded = _feature_cache.get(cache_key, (None, frozenset()))

        def remember_unsupported(feature):
            nonlocal excluded
            excluded |= {feature}
            if cache_key is not None:
                with _feature_cache_lock:
                    cached, previous = _feature_cache.get(cache_key, (supported, frozenset()))
                    excluded |= previous
                    _cache_features(cache_key, cached, excluded)

        # Copy only subscription authentication, never daily config/plugins/history.
        auth_target = private / ('cli/auth.json' if kind == 'traex' else 'auth.json')
        if kind == 'traex':
            auth_target.parent.mkdir(mode=0o700, exist_ok=True)
            auth_target.symlink_to(auth)
        else:
            auth_target.write_bytes(auth.read_bytes())
            auth_target.chmod(0o600)
        for attempt in range(3):
            config.write_text(private_config(args.model, work, provider, args.agent_defaults_snapshot,
                                             supported_features=supported, excluded_features=excluded))
            config.chmod(0o600)
            candidates = disabled_features(provider, supported, excluded)
            try:
                return await execute_attempt(args, system, prompt, schema, transport, kind=kind,
                                             provider=provider, work=work, environment=environment,
                                             candidates=candidates, can_retry=attempt < 2,
                                             remember_unsupported=remember_unsupported)
            except UnsupportedStartupFeature:
                continue


async def execute_attempt(args, system, prompt, schema, transport, *, kind, provider, work, environment,
                          candidates, can_retry, remember_unsupported):
    # The group is owned by this request, never the user's ordinary Codex.
    phase('process_spawn')
    spawn = asyncio.create_task(asyncio.create_subprocess_exec(str(args.codex_executable), 'app-server', '--strict-config', '--stdio',
        cwd=work, env=environment, stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE, start_new_session=True, limit=8 * 1024 * 1024))
    try:
        process = await asyncio.shield(spawn)
    except asyncio.CancelledError:
        process = await spawn
        try:
            track_process(args, process, True)
        finally:
            await terminate(process)
            track_process(args, process, False)
        raise
    clock = ActivityClock(args.timeout)
    stderr_task = asyncio.create_task(collect_stderr(process.stderr, on_activity=clock.touch))
    rpc, thread_id, turn_id, failure = RPC(process, clock), None, None, None
    initialized = False
    try:
        track_process(args, process, True)
        phase('rpc_initialize')
        await rpc.call('initialize', {'clientInfo': {'name': 'memlane_completion', 'version': '1'}, 'capabilities': {'experimentalApi': True}})
        initialized = True
        await rpc.send('initialized', {}, notification=True)
        phase('thread_start')
        instructions = 'Complete only this isolated model request.'
        if kind == 'traex':
            instructions += (' Return only the JSON object constrained by outputSchema, without Markdown. '
                             'The user message describes the answer content, not the transport encoding. '
                             'For a text envelope, put the requested answer in its text field. '
                             'Required output schema: ' + json.dumps(transport))
        started = await rpc.call('thread/start', {'cwd': str(work), 'runtimeWorkspaceRoots': [],
            'permissions': PROFILE, 'model': args.model, 'modelProvider': provider, 'approvalPolicy': 'never',
            'ephemeral': True, 'allowProviderModelFallback': False, 'dynamicTools': [],
            'baseInstructions': system, 'developerInstructions': instructions})
        thread_id = started.get('thread', {}).get('id')
        if not thread_id or started.get('model') != args.model or started.get('modelProvider') != provider or started.get('runtimeWorkspaceRoots') != [] or started.get('activePermissionProfile', {}).get('id') != PROFILE:
            raise CompletionError('codex_model_or_permissions_changed')
        phase('model_turn')
        request_id = await rpc.send('turn/start', {'threadId': thread_id, 'input': [{'type': 'text', 'text': prompt, 'text_elements': []}], 'outputSchema': transport, 'permissions': PROFILE})
        final, announced = None, None
        first_output = False
        while True:
            event = await rpc.read()
            method, params = event.get('method', ''), event.get('params', {})
            if 'id' in event:
                if method or event['id'] != request_id or event.get('error') or turn_id:
                    raise CompletionError('codex_tool_or_rpc_rejected')
                turn_id = event.get('result', {}).get('turn', {}).get('id')
                if not turn_id or announced and announced != turn_id:
                    raise CompletionError('codex_event_identity_invalid')
                continue
            if method == 'error':
                error = remote_error(params.get('error'))
                will_retry = kind in ('codex', 'traex') and params.get('willRetry') is True
                active_turn = turn_id or announced
                if will_retry:
                    if not active_turn or params.get('threadId') != thread_id or params.get('turnId') != active_turn:
                        raise CompletionError('codex_event_identity_invalid')
                else:
                    if (('threadId' in params and params.get('threadId') != thread_id)
                            or ('turnId' in params and active_turn and params.get('turnId') != active_turn)):
                        raise CompletionError('codex_event_identity_invalid')
                trace = current_trace.get()
                if trace and kind in ('codex', 'traex'):
                    trace.emit('codex_turn_error', outcome=error.code, will_retry=will_retry)
                if will_retry:
                    # The App Server owns the retry within this turn. A
                    # result from the interrupted attempt cannot be returned.
                    final = None
                    continue
                raise error
            if method == 'turn/started':
                announced = params.get('turn', {}).get('id')
                if params.get('threadId') != thread_id or not announced or turn_id and announced != turn_id:
                    raise CompletionError('codex_event_identity_invalid')
            if method.startswith('item/'):
                if params.get('threadId') != thread_id or not turn_id or params.get('turnId') != turn_id:
                    raise CompletionError('codex_event_identity_invalid')
                if method in ('item/started', 'item/completed'):
                    item = params.get('item', {})
                    if item.get('type') not in ('agentMessage', 'reasoning', 'userMessage'):
                        raise CompletionError('codex_unexpected_tool_execution')
                    if method == 'item/completed' and item['type'] == 'agentMessage' and item.get('phase') != 'commentary':
                        if final is not None:
                            raise CompletionError('codex_multiple_final_answers')
                        final = item.get('text')
                elif not method.startswith(('item/agentMessage/', 'item/reasoning/')):
                    raise CompletionError('codex_unexpected_tool_execution')
                if not first_output and method.startswith('item/agentMessage/'):
                    first_output = True
                    trace = current_trace.get()
                    if trace:
                        trace.emit('first_output_event')
            if method == 'turn/completed':
                turn = params.get('turn', {})
                if params.get('threadId') != thread_id or not turn_id or turn.get('id') != turn_id:
                    raise CompletionError('codex_event_identity_invalid')
                if turn.get('status') != 'completed' or turn.get('error'):
                    raise remote_error(turn.get('error'))
                break
        phase('schema_validation')
        try:
            if not isinstance(final, str) or len(final) > MAX_BODY:
                raise ValueError()
            value = json.loads(final)
            for contract in ((schema, transport) if schema is not None else (transport,)):
                validators.validator_for(contract)(contract).validate(value)
        except Exception:
            raise CompletionError('codex_schema_failed') from None
        return value['text'] if schema is None else final
    except CompletionError as error:
        failure = error.code
        raise
    except (TimeoutError, asyncio.CancelledError) as error:
        failure = 'codex_timeout' if isinstance(error, TimeoutError) else 'codex_cancelled'
        rpc.clock = None
        if thread_id and turn_id:
            try:
                async with asyncio.timeout(2):
                    await rpc.send('turn/interrupt', {'threadId': thread_id, 'turnId': turn_id})
                    while True:
                        event = await rpc.read()
                        params = event.get('params', {})
                        if event.get('method') == 'turn/completed' and params.get('threadId') == thread_id and params.get('turn', {}).get('id') == turn_id:
                            break
            except Exception:
                pass
        if isinstance(error, TimeoutError):
            raise CompletionError('codex_timeout', 504) from None
        raise
    finally:
        phase('process_cleanup')
        # Shield cleanup even when upstream disconnects or server shuts down.
        async def finish():
            await terminate(process)
            track_process(args, process, False)
            stderr = await stderr_task
            if failure and turn_id is None:
                if (not initialized and failure == 'codex_transport_closed'
                        and process.returncode not in (None, 0)):
                    unsupported = parse_unknown_feature_error(stderr, candidates)
                    if unsupported:
                        remember_unsupported(unsupported)
                        if can_retry:
                            return True
                # Once initialized, the worker can see request instructions.
                record_start_failure(args, process, failure, b'' if initialized else stderr)
            return False
        cleanup = asyncio.create_task(finish())
        try:
            retry_unsupported = await asyncio.shield(cleanup)
        except asyncio.CancelledError:
            await cleanup
            raise
        if retry_unsupported:
            raise UnsupportedStartupFeature from None


def default_request_args(args):
    from agent_bridge_runtime import snapshot_agent_defaults
    from project_policy import PolicyError
    try:
        return snapshot_agent_defaults(args)
    except PolicyError as error:
        raise CompletionError(error.code, error.status) from None


def create_app(args):
    token = args.token_file.read_text().strip()
    if len(token) < 24:
        raise ValueError('completion token must contain at least 24 characters')
    app = web.Application(client_max_size=MAX_BODY)
    from generation_config import AGENT_BRIDGE_MAX_CONCURRENT_REQUESTS
    concurrency = getattr(args, 'max_concurrency', 2)
    inflight = getattr(args, 'max_inflight', AGENT_BRIDGE_MAX_CONCURRENT_REQUESTS)
    if not 1 <= concurrency <= inflight <= AGENT_BRIDGE_MAX_CONCURRENT_REQUESTS:
        raise ValueError(f'completion limits require 1 <= concurrency <= inflight <= {AGENT_BRIDGE_MAX_CONCURRENT_REQUESTS}')
    slot = asyncio.Semaphore(concurrency)
    admitted = 0
    handlers = set()
    async def lifecycle(app):
        from owner_lifecycle import watch_owner
        monitor = watch_owner()
        try:
            yield
        finally:
            monitor.set()

    async def shutdown(app):
        active = list(handlers)
        for task in active:
            task.cancel()
        await asyncio.gather(*active, return_exceptions=True)

    app.cleanup_ctx.append(lifecycle)
    app.on_shutdown.append(shutdown)

    async def health(request):
        try:
            selected = default_request_args(args).agent_defaults_snapshot
        except CompletionError as error:
            return web.json_response({'status':'unavailable', 'error_code':error.code}, status=error.status)
        return web.json_response({'status': 'ready', 'model': args.model, 'agent_default':selected, 'provider': getattr(args, 'bridge_kind', 'codex'), 'active_or_waiting': admitted, 'max_concurrency': concurrency, 'max_inflight': inflight})

    async def complete(request):
        nonlocal admitted
        if not hmac.compare_digest(request.headers.get('Authorization', ''), 'Bearer ' + token):
            return web.json_response({'error': {'code': 'unauthorized', 'message': 'unauthorized'}}, status=401)
        handler = asyncio.current_task()
        handlers.add(handler)
        handler.add_done_callback(handlers.discard)
        kind = getattr(args, 'bridge_kind', 'codex')
        trace = RequestTrace(kind + '_bridge', request.headers.get(HEADER), backend_kind=kind)
        context_token = current_trace.set(trace)
        outcome = 'failed'
        trace.transition('request_validation')
        try:
            body = await request.json()
            parsed = parse_request(body, args.model)
            if admitted >= inflight:
                raise CompletionError('codex_busy', 429)
            admitted += 1
            try:
                trace.transition('bridge_queue')
                async with slot:
                    trace.emit('bridge_admitted', active_or_waiting=admitted, max_concurrency=concurrency)
                    trace.transition('execute')
                    execution_args = default_request_args(args)
                    task = asyncio.create_task(getattr(args, 'execute_request', execute)(execution_args, *parsed))
                    try:
                        while not task.done():
                            if request.transport is None or request.transport.is_closing():
                                task.cancel()
                                try:
                                    await task
                                except asyncio.CancelledError:
                                    pass
                                raise CompletionError('codex_upstream_cancelled', 499)
                            await asyncio.wait({task}, timeout=.1)
                        result = await task
                    finally:
                        if not task.done():
                            task.cancel()
                            try:
                                await task
                            except asyncio.CancelledError:
                                pass
            finally:
                admitted -= 1
            outcome = 'succeeded'
            return web.json_response({'id': 'chatcmpl-' + uuid.uuid4().hex, 'object': 'chat.completion',
                'created': int(time.time()), 'model': execution_args.model,
                'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': result}, 'finish_reason': 'stop'}]})
        except CompletionError as error:
            outcome = error.code
            return web.json_response({'error': {'code': error.code, 'message': error.code, 'type': 'completion_error'}}, status=error.status)
        except (ValueError, TypeError, AttributeError):
            outcome = 'invalid_request'
            return web.json_response({'error': {'code': 'invalid_request', 'message': 'invalid_request'}}, status=400)
        except OSError:
            outcome = 'codex_process_unavailable'
            return web.json_response({'error': {'code': 'codex_process_unavailable', 'message': 'codex_process_unavailable'}}, status=503)
        except asyncio.CancelledError:
            outcome = 'cancelled'
            raise
        finally:
            trace.finish(outcome, input_bytes=request.content_length)
            current_trace.reset(context_token)

    app.router.add_get('/health', health)
    app.router.add_post('/v1/chat/completions', complete)
    return app


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host', choices=('127.0.0.1', '::1'), default='127.0.0.1')
    parser.add_argument('--port', type=int, required=True)
    parser.add_argument('--token-file', type=Path, required=True)
    parser.add_argument('--codex-executable', type=Path, required=True)
    parser.add_argument('--auth-home', type=Path, required=True)
    parser.add_argument('--model', required=True)
    parser.add_argument('--timeout', type=float, default=120,
                        help='Idle silence threshold in seconds (reset on child stdout/stderr output)')
    parser.add_argument('--max-concurrency', type=int, default=2)
    parser.add_argument('--max-inflight', type=int, default=32)
    parser.add_argument('--diagnostics-dir', type=Path, help='Private bounded startup failures; defaults beside the token file')
    args = parser.parse_args()
    if (not args.codex_executable.is_absolute() or not args.codex_executable.is_file()
            or not args.auth_home.is_absolute() or not math.isfinite(args.timeout) or args.timeout <= 0):
        parser.error('absolute existing executable, absolute auth home and positive timeout required')
    web.run_app(create_app(args), host=args.host, port=args.port, access_log=None, print=None,
                handler_cancellation=True, shutdown_timeout=15)


if __name__ == '__main__':
    main()
