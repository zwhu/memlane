"""Managed official-plugin extension. Configuration/cursors only, no content queue.

Installed beside the fixed official scripts as _memlane_policy.py by the patch
builder. Importing this module alone has no filesystem or network side effects.
"""
import contextlib
import contextvars
import fcntl
import hashlib
import json
import os
from pathlib import Path
import stat
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NotReady(RuntimeError):
    pass


class ControlResponseRejected(NotReady):
    """An online control-plane response that cannot authorize an operation."""


def managed():
    return bool(os.environ.get('MEMLANE_PROJECT_CONTROL_URL'))


def prepare_environment(parse_env_file):
    """Pin each installed artifact to its own private env file, before upstream load.

    Explicit exported configuration remains available for isolated dev hosts.
    A patched artifact without either binding refuses to use global owner caches.
    """
    marker = Path(__file__).resolve().parents[1] / '.memlane-installation.json'
    if marker.exists() or marker.is_symlink():
        if marker.is_symlink() or not marker.is_file() or marker.stat().st_mode & 0o077:
            raise NotReady('private_installation_binding_required')
        try:
            binding = json.loads(marker.read_text())
            path = Path(binding['env_file'])
        except (KeyError, ValueError, TypeError):
            raise NotReady('installation_binding_invalid') from None
        if not path.is_absolute() or path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
            raise NotReady('private_installation_environment_required')
        values = parse_env_file(path)
        required = ('COGNEE_BASE_URL', 'COGNEE_API_KEY', 'MEMLANE_PROJECT_CONTROL_URL', 'MEMLANE_PROJECT_CONTROL_TOKEN_FILE')
        if any(not values.get(key) for key in required):
            raise NotReady('installation_environment_incomplete')
        os.environ['COGNEE_ENV_FILE'] = str(path)
        for key in required:
            os.environ[key] = values[key]
        # TraeX's shell tools export the raw thread id, whereas its event
        # adapter namespaces that id per installation. Use the same key for
        # both entry points; cwd is ambiguous when several sessions coexist.
        if binding.get('host_kind') == 'traex' and os.environ.get('TRAECLI_THREAD_ID'):
            thread = str(uuid.UUID(os.environ['TRAECLI_THREAD_ID'].strip()))
            identity = binding['instance_id'] + '\0' + thread
            os.environ['COGNEE_SESSION_KEY'] = 'traex-' + hashlib.sha256(identity.encode()).hexdigest()
    if not managed() or not os.environ.get('COGNEE_API_KEY') or not os.environ.get('COGNEE_BASE_URL'):
        raise NotReady('managed_installation_configuration_required')
    os.environ.update(COGNEE_PLUGIN_IDENTITY='false', COGNEE_SHARED_AGENT_MEMORY='false',
                      COGNEE_MANAGED_ENDPOINT='true', COGNEE_SESSION_COMPANION_DATASET='false',
                      COGNEE_CODING_MEMORY='true', COGNEE_BACKEND='cloud', COGNEE_CODEX_BACKEND='cloud')


def recall_targets(dataset_name, dataset_ids):
    result = current()
    allowed = []
    if 'read' in result['policy']['permissions']:
        allowed.append(result['dataset_id'])
    common = result.get('common') or {}
    if common.get('ready') and common.get('readable') and common.get('dataset_id'):
        allowed.append(common['dataset_id'])
    targets = ([item.strip() for item in dataset_ids.split(',') if item.strip()] if isinstance(dataset_ids, str)
               else list(dataset_ids or []))
    if not targets and dataset_name:
        targets = [dataset_name]
    if not targets:
        targets = allowed
    if not targets or any(target not in allowed for target in targets):
        raise NotReady('project_read_denied')
    return targets, result['dataset_id']


def code_target(operation, target=None, *, automatic=False, workspace=None):
    """Code groups require an explicit manager association, never a slug fallback."""
    result = current()
    code = result.get('code') or {}
    if not result.get('ready') or not code.get('enabled') or not code.get('ready') or not code.get('dataset_id'):
        raise NotReady('project_code_not_enabled')
    if target and target != code['dataset_id']:
        raise NotReady('project_code_target_mismatch')
    if workspace is not None and os.path.realpath(workspace) not in [os.path.realpath(root) for root in code.get('roots', [])]:
        raise NotReady('project_code_workspace_mismatch')
    require(operation)
    if automatic:
        require('process', automatic=True)
    return code['dataset_id']


def _connection():
    origin = os.environ.get('MEMLANE_PROJECT_CONTROL_URL', '')
    parsed = urllib.parse.urlparse(origin)
    if (parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port
            or parsed.path not in ('', '/') or parsed.query or parsed.fragment or parsed.username):
        raise NotReady('control_configuration_invalid')
    path = Path(os.environ.get('MEMLANE_PROJECT_CONTROL_TOKEN_FILE', ''))
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
        raise NotReady('control_credential_unavailable')
    token = path.read_text().strip()
    if len(token) < 32:
        raise NotReady('control_credential_invalid')
    return origin.rstrip('/'), token


def _request(path, payload=None, *, timeout=5):
    origin, token = _connection()
    request = urllib.request.Request(origin + path,
                                     data=json.dumps(payload).encode() if payload is not None else None,
                                     headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    # Refuse redirects, including same-origin redirects; only fixed control routes.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=timeout) as response:
            raw = response.read(64 * 1024 + 1)
            if len(raw) > 64 * 1024:
                raise NotReady('control_response_limit')
            result = json.loads(raw)
        return result
    except urllib.error.HTTPError as error:
        if error.code < 500:
            raise ControlResponseRejected('project_policy_unconfirmed') from None
        raise NotReady('project_policy_unconfirmed') from None
    except (urllib.error.URLError, OSError, ValueError):
        raise NotReady('project_policy_unconfirmed') from None


def _atomic_json(root, path, data):
    fd, temporary = tempfile.mkstemp(dir=root, prefix='.policy-')
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(data, output, sort_keys=True)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@contextlib.contextmanager
def _state(*, writable=True):
    from _plugin_common import _PLUGIN_DIR, get_session_key
    session_key = get_session_key()
    if not session_key:
        from _plugin_common import resolve_host_key_outside_hook, set_session_key
        session_key, _ = resolve_host_key_outside_hook()
        if not session_key:
            raise NotReady('host_session_required')
        set_session_key(session_key)
    origin, token = _connection()
    digest = hashlib.sha256((origin + '\n' + token + '\n' + session_key).encode()).hexdigest()
    root = _PLUGIN_DIR / 'memlane-policy'
    path = root / (digest + '.json')
    if not writable:
        # Explicit tools may run in a workspace sandbox. Reading an existing
        # binding must not create/chmod/rewrite files in the Agent's home.
        with (root / (digest + '.lock')).open('r') as lock:
            fcntl.flock(lock, fcntl.LOCK_SH)
            yield json.loads(path.read_text())
        return
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (root / (digest + '.lock')).open('a+') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        data = json.loads(path.read_text()) if path.exists() else {}
        try:
            yield data
        finally:
            _atomic_json(root, path, data)


def _capture_cache_path(project_id):
    from _plugin_common import _PLUGIN_DIR
    origin, token = _connection()
    name = hashlib.sha256((origin + '\n' + token + '\n' + project_id).encode()).hexdigest()
    return _PLUGIN_DIR / 'memlane-capture-policy' / (name + '.json')


def _cached_capture_policy(project_id):
    try:
        path = _capture_cache_path(project_id)
        if path.parent.is_symlink():
            return {}
        with open(path, 'r', opener=_private_open) as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > 4096:
                return {}
            value = json.load(stream)
        if isinstance(value, dict) and value.get('project_id') == project_id:
            return value
    except (NotReady, OSError, ValueError):
        pass
    return {}


def _save_capture_policy(project_id, result, checked_at):
    policy = result.get('capture_policy')
    if (not isinstance(policy, dict) or type(policy.get('enabled')) is not bool
            or type(policy.get('project_enabled')) is not bool or type(policy.get('version')) is not int
            or policy['version'] < 1 or policy.get('scope') not in ('ok', 'project', 'agent', 'default')):
        return {}
    record = {**{key: policy[key] for key in ('enabled', 'project_enabled', 'version', 'scope')},
              'updated_at': str(policy.get('updated_at') or ''), 'project_id': project_id, 'checked_at': checked_at,
              'ready': result.get('ready') is not False,
              'can_capture': result.get('can_capture', policy['enabled']) is True}
    try:
        path = _capture_cache_path(project_id)
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if path.parent.is_symlink() or stat.S_IMODE(path.parent.stat().st_mode) != 0o700:
            return {}
        with open(path.with_suffix('.lock'), 'a+', opener=_private_open) as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            existing = _cached_capture_policy(project_id)
            if (record['version'], checked_at) < (existing.get('version', 0), existing.get('checked_at', 0)):
                return existing
            _atomic_json(path.parent, path, record)
    except (NotReady, OSError, ValueError, TypeError):
        # Read-only homes can still use the live response and any newer cache.
        existing = _cached_capture_policy(project_id)
        if (record['version'], checked_at) < (existing.get('version', 0), existing.get('checked_at', 0)):
            return existing
    return record


def _private_open(path, flags):
    return os.open(path, flags | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)


def _capture_disabled(policy):
    return policy.get('enabled') is False and policy.get('scope') in ('project', 'agent', 'default')


def bind(workspace, host_session_key):
    """Called before ensure_launch_record; never rebind an existing unmanaged launch."""
    from _plugin_common import _read_map_record
    with _state() as state:
        if not state.get('binding') and _read_map_record(host_session_key).get('dataset'):
            raise NotReady('existing_session_requires_explicit_binding')
        payload = {'workspace': workspace, 'host_session_key': host_session_key}
        marker = Path(__file__).resolve().parents[1] / '.memlane-installation.json'
        if marker.is_file() and not marker.is_symlink():
            binding = json.loads(marker.read_text())
            manifest = Path(__file__).resolve().parents[1] / '.codex-plugin/plugin.json'
            manifest_override = {
                'cursor': '.cursor-plugin/plugin.json',
                'claude_code': '.claude-plugin/plugin.json',
                'antigravity': 'plugin.json',
            }.get(binding.get('host_kind'))
            if manifest_override:
                manifest = Path(__file__).resolve().parents[1] / manifest_override
            payload['host_environment'] = {
                'instance_id': binding['instance_id'],
                'plugin_path': str(Path(__file__).resolve().parents[1]),
                'plugin_version': str(json.loads(manifest.read_text()).get('version', '')) if manifest.is_file() else '',
            }
            variables = {'traex': ('TRAE_HOME', 'TRAECLI_HOME'), 'pi': ('PI_CODING_AGENT_DIR',),
                         'codex': ('CODEX_HOME',), 'claude_code': ('CLAUDE_CONFIG_DIR',)}
            variables.setdefault('cursor', ('CURSOR_CONFIG_DIR', 'MEMLANE_CURSOR_PROFILE'))
            profiles = {str(Path(os.environ[v]).expanduser().absolute())
                        for v in variables.get(binding.get('host_kind'), ()) if os.environ.get(v)}
            if len(profiles) == 1:
                payload['host_environment']['runtime_profile'] = profiles.pop()
            elif (not profiles and binding.get('host_kind') == 'cursor'
                  and isinstance(binding.get('runtime_profile'), str) and binding['runtime_profile']):
                payload['host_environment']['runtime_profile'] = str(Path(binding['runtime_profile']).expanduser().absolute())
        checked_at = time.time()
        try:
            result = _request('/v1/project/resolve', payload)
        except ControlResponseRejected:
            raise
        except NotReady:
            existing = state.get('binding') or {}
            if (existing.get('project_id') and state.get('binding_workspace') == os.path.realpath(workspace)
                    and _capture_disabled(_cached_capture_policy(existing['project_id']))):
                return existing['dataset_id']
            raise
        if result.get('project_id'):
            _save_capture_policy(result['project_id'], result, checked_at)
        if not result.get('ready') or not result.get('dataset_id'):
            existing = state.get('binding') or {}
            if (existing.get('project_id') == result.get('project_id')
                    and existing.get('dataset_id') == result.get('dataset_id')
                    and state.get('binding_workspace') == os.path.realpath(workspace)
                    and _capture_disabled(result.get('capture_policy') or {})):
                return existing['dataset_id']
            raise NotReady('project_policy_unconfirmed')
        uuid.UUID(result['dataset_id'])
        old = state.get('binding')
        binding = {'project_id': result['project_id'], 'dataset_id': result['dataset_id']}
        if old and binding != old:
            raise NotReady('session_binding_conflict')
        state['binding'] = binding
        state['binding_workspace'] = os.path.realpath(workspace)
        return result['dataset_id']


def dataset():
    """Local immutable binding; authorization is rechecked at each operation."""
    with _state(writable=False) as state:
        binding = state.get('binding')
        if not binding or not binding.get('dataset_id'):
            raise NotReady('project_binding_required')
        return str(uuid.UUID(binding['dataset_id']))


def current(dataset=None, *, timeout=5):
    with _state(writable=False) as state:
        binding = state.get('binding')
        if not binding:
            raise NotReady('project_binding_required')
        checked_at = time.time()
        result = _request('/v1/project/policy?project_id=' + urllib.parse.quote(binding['project_id'], safe=''), timeout=timeout)
        if result.get('dataset_id') == binding['dataset_id']:
            _save_capture_policy(binding['project_id'], result, checked_at)
        if not result.get('ready') or result.get('dataset_id') != binding['dataset_id']:
            raise NotReady('project_policy_unconfirmed')
        if dataset and dataset != binding['dataset_id']:
            raise NotReady('managed_dataset_mismatch')
        return result


def capture_decision(dataset=None):
    """Return the capture status and version from the same policy snapshot."""
    if not managed():
        return 'enabled', None
    try:
        with _state(writable=False) as state:
            binding = state.get('binding')
        if not binding:
            return 'unknown', None
        if dataset and dataset != binding['dataset_id']:
            return 'denied', None
        project_id = binding['project_id']
        checked_at = time.time()
        try:
            result = _request('/v1/project/capture-policy?project_id=' + urllib.parse.quote(project_id, safe=''))
        except ControlResponseRejected:
            return 'unknown', None
        except (NotReady, OSError):
            cached = _cached_capture_policy(project_id)
            return ('disabled', cached['version']) if _capture_disabled(cached) else ('unknown', None)
        if result.get('dataset_id') != binding['dataset_id'] or result.get('project_id', project_id) != project_id:
            return 'unknown', None
        rules = _save_capture_policy(project_id, result, checked_at)
        if rules:
            version = rules['version']
            if _capture_disabled(rules):
                return 'disabled', version
            if rules.get('ready') is False:
                return 'unknown', version
            allowed = rules['enabled'] and rules.get('can_capture', rules['enabled'])
            return ('enabled' if allowed else 'denied'), version
        if result.get('ready') is False:
            return 'unknown', None
        # Older control services publish the decision inside policy.
        previous = result.get('policy') or current(dataset)['policy']
        if 'write' not in previous['permissions']:
            return 'denied', None
        return ('enabled' if previous['can_capture'] else 'disabled'), None
    except (NotReady, KeyError, OSError, ValueError, TypeError):
        return 'unknown', None


def capture_status(dataset=None):
    return capture_decision(dataset)[0]


def cached_capture_disabled(workspace):
    """Read a confirmed local stop only for this session's immutable binding."""
    try:
        with _state(writable=False) as state:
            binding = state['binding']
            if state.get('binding_workspace') != os.path.realpath(workspace):
                return False
        return _capture_disabled(_cached_capture_policy(binding['project_id']))
    except (NotReady, KeyError, OSError, ValueError):
        return False


def permit(operation, dataset=None, *, automatic=False):
    if not managed():
        return True
    if operation == 'capture':
        return capture_status(dataset) == 'enabled'
    try:
        result = current(dataset)
        policy = result['policy']
        permission = 'read' if operation in ('read', 'recall') else ('delete' if operation == 'delete' else 'write')
        allowed = permission in policy['permissions']
        if operation == 'recall':
            common = result.get('common') or {}
            allowed = allowed or (common.get('ready') and common.get('readable'))
        if operation == 'process' and automatic:
            allowed = allowed and policy['can_auto_process']
            with _state() as state:
                if state.get('blocked'):
                    return False
        if operation == 'drain':
            with _state() as state:
                if state.get('blocked'):
                    return False
                if not allowed:
                    state['blocked'] = {'reason': 'write_denied', 'at': time.time(), 'revision': result['revision']}
            return allowed and policy['can_capture']
        return allowed
    except (NotReady, KeyError, OSError, ValueError):
        return False


def require(operation, dataset=None, *, automatic=False):
    if operation == 'capture':
        status = capture_status(dataset)
        if status != 'enabled':
            raise NotReady({'disabled': 'capture_disabled', 'denied': 'project_write_denied'}.get(
                status, 'project_policy_unconfirmed'))
        return
    if not permit(operation, dataset, automatic=automatic):
        raise NotReady('当前 Agent 没有此项目的写入权限' if operation != 'read' else 'project_read_denied')


def block(status):
    if managed() and status in (401, 403):
        hold('official_http_' + str(status))


def is_capture_paused(error):
    if getattr(error, 'code', None) != 403:
        return False
    if '_memlane_capture_paused' not in vars(error):
        try:
            stream = vars(error).get('fp')
            value = json.loads(stream.read(16385)) if stream is not None else {}
            error._memlane_capture_paused = value if isinstance(value, dict) and value.get('error') == 'capture_paused' else None
        except (OSError, ValueError, TypeError, AttributeError):
            error._memlane_capture_paused = None
    return bool(error._memlane_capture_paused)


def remember_capture_pause(error, dataset=None):
    if not is_capture_paused(error):
        return False
    try:
        with _state(writable=False) as state:
            binding = state['binding']
        if dataset and dataset != binding['dataset_id']:
            return True
        body = error._memlane_capture_paused
        scope = body.get('capture_scope')
        previous = _cached_capture_policy(binding['project_id'])
        _save_capture_policy(binding['project_id'], {'capture_policy': {
            'enabled': False, 'project_enabled': scope != 'project', 'version': body.get('capture_policy_version'),
            'scope': scope, 'updated_at': previous.get('updated_at', '') if previous.get('version') == body.get('capture_policy_version') else '',
        }}, time.time())
    except (NotReady, KeyError, OSError, ValueError, TypeError):
        pass
    return True


def require_capture_write(dataset, mode):
    if mode not in ('automatic', 'manual'):
        raise NotReady('invalid_capture_mode')
    require('write' if mode == 'manual' else 'capture', dataset)


def hold(reason):
    if managed():
        with _state() as state:
            state['blocked'] = {'reason': reason, 'at': time.time()}


def _position(path):
    stat = Path(path).stat()
    return {'path': str(Path(path).absolute()), 'inode': stat.st_ino, 'device': stat.st_dev,
            'size': stat.st_size, 'at': time.time()}


def capture_boundary(transcript, *, enabled=True, decision=None, new_turn=False):
    """Return parser offset, or None to skip. Disabled periods are stat-only.

    First allowed hook after a pause establishes a fresh boundary and skips that
    hook's complete turn. Rotation/truncation remains paused, never resets to 0.
    """
    status, version = decision if decision is not None else capture_decision()
    if not enabled:
        status = 'disabled'
    if status == 'unknown':
        return None
    with _state() as state:
        if state.get('cursor_unconfirmed'):
            return None
        try:
            position = _position(transcript)
        except OSError:
            state['cursor_unconfirmed'] = True
            return None
        old = state.get('cursor')
        if old and (any(position[key] != old[key] for key in ('path', 'inode', 'device'))
                    or position['size'] < old['size']):
            state['cursor_unconfirmed'] = True
            return None
        state['capture_policy_status'] = status
        if status != 'enabled':
            if version is not None:
                state['capture_policy_version'] = version
            state['cursor'] = position
            state['paused'] = True
            return None
        old_version = state.get('capture_policy_version')
        if version is not None:
            state['capture_policy_version'] = version
        # Two rapid toggles may happen while no host hook is running.
        changed_while_idle = old_version is not None and version is not None and old_version != version
        if state.pop('paused', False) or changed_while_idle:
            state['cursor'] = position
            state['resume_at'] = position['at']
            return position['size'] if new_turn else None
        return state.get('cursor', {}).get('size', 0)


def _merge_block(query, cwd, project_builder):
    """One existing coding injection, combining verified project/common sources."""
    from concurrent.futures import ThreadPoolExecutor, wait
    from _plugin_common import _json_http_request
    from _coding_memory import _raw_text
    deadline = time.monotonic() + 8

    def remaining():
        value = deadline - time.monotonic()
        if value <= 0:
            raise NotReady('recall_budget_exhausted')
        return value

    try:
        initial = current(timeout=min(2, remaining()))
    except (NotReady, OSError, ValueError):
        return ''
    project = ''
    if 'read' in initial['policy']['permissions']:
        try:
            project = project_builder(query, cwd, min(deadline, time.monotonic() + 5))[:4000]
        except Exception:
            project = ''
    common = initial.get('common') or {}
    common_block = ''
    if common.get('ready') and common.get('readable') and common.get('dataset_id'):
        identifier = common['dataset_id']
        try:
            rows = _json_http_request('/api/v1/datasets/' + identifier + '/data?limit=30&offset=0',
                                      None, method='GET', timeout=remaining())
            candidates = {}
            for row in rows if isinstance(rows, list) else []:
                metadata = row.get('externalMetadata') or {}
                if (row.get('datasetId') == identifier and metadata.get('memlane_kind') == 'common_knowledge'
                        and metadata.get('category') in ('preference', 'experience') and row.get('id')):
                    candidates[row['id']] = row
            ordered = sorted(candidates, key=lambda key: candidates[key].get('updatedAt') or candidates[key].get('createdAt') or '', reverse=True)
            if query.strip() and candidates:
                groups = _json_http_request('/api/v1/search', {'query': query, 'search_type': 'CHUNKS',
                                              'dataset_ids': [identifier], 'top_k': 30, 'only_context': False}, timeout=remaining())
                ordered = []
                for group in groups if isinstance(groups, list) else []:
                    if group.get('dataset_id') != identifier:
                        continue
                    for chunk in group.get('search_result') or []:
                        key = chunk.get('document_id')
                        if key in candidates and key not in ordered:
                            ordered.append(key)
            else:
                ordered = ordered[:3]
            ordered = ordered[:8]
            bodies = {}

            def fetch(key):
                return _raw_text(identifier, key, remaining())

            pool = ThreadPoolExecutor(max_workers=4)
            # Common-memory reads have their own bounded fan-out. The project
            # builder retains the official plugin's own candidate/read limits.
            futures = {pool.submit(fetch, key): key for key in ordered[:4]}
            try:
                done, _ = wait(futures, timeout=remaining())
                for future in done:
                    try:
                        bodies[futures[future]] = future.result()
                    except Exception:
                        pass
            finally:
                pool.shutdown(wait=False, cancel_futures=True)
            pieces = []
            for key in ordered:
                if bodies.get(key):
                    category = candidates[key]['externalMetadata']['category']
                    source = candidates[key]['externalMetadata'].get('source') or '用户手动保存'
                    pieces.append('来源：通用记忆／' + ('个人偏好' if category == 'preference' else '通用经验')
                                  + '；' + str(source)[:160] + '\n' + bodies[key].strip())
                    stage_recall(identifier, [(key, pieces[-1])])
            if pieces:
                common_block = ('\n\n## 跨项目参考\n项目明确约定优先于以下通用建议。\n\n' + '\n\n'.join(pieces))[:2000]
        except Exception:
            common_block = ''
    try:
        # Do not inject candidates after a policy generation/read scope change.
        final = current(timeout=min(2, remaining()))
        if final['revision'] != initial['revision']:
            return ''
        if 'read' not in final['policy']['permissions']:
            project = ''
        if final.get('common') != common:
            common_block = ''
    except Exception:
        return ''
    return (project + common_block)[:6000]


def _pending_snapshot(dataset_id, session_id):
    from _plugin_common import _bridge_file, _bridge_cache_key, _load_json_file
    path = _bridge_file(session_id)
    key = _bridge_cache_key(dataset_id, session_id)
    cache = _load_json_file(path)
    session_cache = cache.get(key) or {}
    pending = list(session_cache.get('pending_entries') or [])
    signature = hashlib.sha256(json.dumps(pending, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    return path, key, cache, session_cache, pending, signature


def stamp_pending_entry(dataset_id, session_id, entry, ambiguous):
    """Journal the original official entry before sending, without another queue.

    A process dying after POST but before trimming must leave an unknown outcome,
    not an apparently unsent item that a later hook can blindly replay.
    """
    from _plugin_common import _buffer_lock, _write_json_file, _AMBIGUOUS_KEY
    original = dict(entry)
    replacement = dict(entry)
    if ambiguous:
        replacement[_AMBIGUOUS_KEY] = True
    else:
        replacement.pop(_AMBIGUOUS_KEY, None)
    with _buffer_lock():
        path, key, cache, session_cache, pending, _ = _pending_snapshot(dataset_id, session_id)
        for index, value in enumerate(pending):
            if value == original:
                pending[index] = replacement
                session_cache['pending_entries'] = pending
                cache[key] = session_cache
                _write_json_file(path, cache)
                entry.clear()
                entry.update(replacement)
                return
    raise NotReady('pending_entry_changed_before_send')


def _pending_identity(dataset_id, session_id):
    from _plugin_common import _read_map_record, get_session_key
    if dataset_id != dataset() or _read_map_record(get_session_key()).get('session_id') != session_id:
        raise NotReady('managed_buffer_session_mismatch')


def pending_materials(dataset_id, session_id):
    """Metadata only, for an explicit desktop review of the official buffer."""
    _pending_identity(dataset_id, session_id)
    from _plugin_common import _buffer_lock, _AMBIGUOUS_KEY
    with _buffer_lock():
        _, _, _, _, pending, signature = _pending_snapshot(dataset_id, session_id)
    with _state() as state:
        return {'pending': len(pending), 'signature': signature,
                'unknown_outcomes': sum(bool(entry.get(_AMBIGUOUS_KEY)) for entry in pending),
                'blocked': state.get('blocked')}


def decide_pending(dataset_id, session_id, action, expected_signature):
    """Only an explicit action can unblock old lawful entries; never touch transcript.

    The signature binds consent to exactly the reviewed official buffer. Missing
    an ambiguous write from the server's recent tail does not prove non-commit.
    """
    if action not in ('replay', 'discard'):
        raise NotReady('invalid_pending_action')
    _pending_identity(dataset_id, session_id)
    if action == 'replay':
        require('write', dataset_id)
    from _plugin_common import (_buffer_lock, _write_json_file, _AMBIGUOUS_KEY,
                                _entry_fingerprint, _server_session_fingerprints,
                                get_session_detail_via_http, drain_warmup_entries,
                                _try_acquire_drain_lock, _release_drain_lock)
    if not _try_acquire_drain_lock():
        raise NotReady('buffer_send_in_progress')
    try:
        with _buffer_lock():
            path, key, cache, session_cache, pending, signature = _pending_snapshot(dataset_id, session_id)
            if not isinstance(expected_signature, str) or expected_signature != signature:
                raise NotReady('pending_materials_changed_review_again')
            if action == 'replay' and any(entry.get(_AMBIGUOUS_KEY) for entry in pending):
                try:
                    from _memlane_capture import numbered
                except ImportError:
                    numbered = lambda entry: False
                legacy = [entry for entry in pending if entry.get(_AMBIGUOUS_KEY) and not numbered(entry)]
                if legacy:
                    detail = get_session_detail_via_http(session_id)
                    known = _server_session_fingerprints(detail) if detail is not None else set()
                    if any(_entry_fingerprint(entry) not in known for entry in legacy):
                        raise NotReady('original_write_result_still_unknown')
                    # Authoritatively matched copies are consumed, never sent twice.
                    pending = [entry for entry in pending if entry not in legacy]
            session_cache['pending_entries'] = [] if action == 'discard' else pending
            session_cache.pop('drain_fail_count', None)
            session_cache.pop('drain_fail_at', None)
            cache[key] = session_cache
            _write_json_file(path, cache)
            with _state() as state:
                state.pop('blocked', None)
    finally:
        _release_drain_lock()
    if action == 'discard':
        return {'action': action, 'discarded': len(pending), 'pending': 0}
    drained, remaining = drain_warmup_entries(dataset_id, session_id, capture_mode='manual')
    return {'action': action, 'drained': drained, 'pending': remaining}


if __name__ == '__main__':
    import argparse
    from _env_file import load_env_file
    load_env_file()
    from _plugin_common import set_session_key
    parser = argparse.ArgumentParser()
    parser.add_argument('--session-key', required=True)
    parser.add_argument('--dataset', required=True)
    parser.add_argument('--session-id')
    parser.add_argument('--action', choices=('show', 'replay', 'discard'), required=True)
    parser.add_argument('--expected-signature')
    arguments = parser.parse_args()
    set_session_key(arguments.session_key)
    try:
        from _plugin_common import _read_map_record
        session_id = arguments.session_id or _read_map_record(arguments.session_key).get('session_id')
        if not session_id:
            raise NotReady('official_session_unconfirmed')
        value = (pending_materials(arguments.dataset, session_id) if arguments.action == 'show'
                 else decide_pending(arguments.dataset, session_id, arguments.action, arguments.expected_signature))
        print(json.dumps(value, ensure_ascii=False))
    except NotReady as error:
        print(json.dumps({'error': str(error)}, ensure_ascii=False))
        raise SystemExit(1)


# Per invocation only: no raw text or pending-content queue on disk.
_recall_candidates = contextvars.ContextVar('recall_candidates', default=None)


def stage_recall(dataset_id, documents):
    candidates = _recall_candidates.get()
    if candidates is not None:
        for document_id, rendered in documents:
            if document_id and rendered:
                candidates.append((dataset_id, document_id, rendered))


def report_recall(hits, channel, event_id=None):
    """Best effort receipt after successful delivery, never alter the result."""
    if not managed() or not hits:
        return
    try:
        _request('/v1/knowledge/recalls', {'event_id': event_id or str(uuid.uuid4()),
                 'channel': channel, 'hits': [{'dataset_id': dataset, 'document_id': document}
                                             for dataset, document in sorted(set(hits))]}, timeout=0.5)
    except Exception:
        # Recall remains usable if stats are unavailable. No automatic replay
        # and no duplicate content storage; failed receipts are not counted.
        import sys
        sys.stderr.write('[memlane] recall statistics receipt unavailable\n')


def report_explicit_recall(result):
    """Only explicit official document references, never infer from answer text."""
    hits = set()
    def visit(value, dataset=None):
        if isinstance(value, list):
            for row in value:
                visit(row, dataset)
        elif isinstance(value, dict):
            if value.get('error'):
                return
            dataset = value.get('dataset_id') or dataset
            document = value.get('document_id')
            if dataset and document:
                hits.add((dataset, document))
            for key in ('search_result', 'results', 'context', 'metadata', 'sources'):
                if isinstance(value.get(key), (list, dict)):
                    visit(value[key], dataset)
    visit(result)
    report_recall(hits, 'explicit')


def merge_block(query, cwd, project_builder):
    candidates = []
    token = _recall_candidates.set(candidates)
    try:
        block = _merge_block(query, cwd, project_builder)
        # Budget truncation and final permission checks have already run.
        report_recall([(dataset, document) for dataset, document, text in candidates if text in block], 'automatic')
        return block
    finally:
        _recall_candidates.reset(token)
