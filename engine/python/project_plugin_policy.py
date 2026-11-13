"""Managed official-plugin extension. Configuration/cursors only, no content queue.

Installed beside the fixed official scripts as _memlane_policy.py by the patch
builder. Importing this module alone has no filesystem or network side effects.
"""
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NotReady(RuntimeError):
    pass


def managed():
    return bool(os.environ.get('MEMLANE_PROJECT_CONTROL_URL'))


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


def _request(path, payload=None):
    origin, token = _connection()
    request = urllib.request.Request(origin + path,
                                     data=json.dumps(payload).encode() if payload is not None else None,
                                     headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    # Refuse redirects, including same-origin redirects; only fixed control routes.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=5) as response:
            raw = response.read(64 * 1024 + 1)
            if len(raw) > 64 * 1024:
                raise NotReady('control_response_limit')
            result = json.loads(raw)
        return result
    except (urllib.error.URLError, OSError, ValueError):
        raise NotReady('project_policy_unconfirmed') from None


@contextlib.contextmanager
def _state():
    from _plugin_common import _PLUGIN_DIR, get_session_key
    session_key = get_session_key()
    if not session_key:
        raise NotReady('host_session_required')
    origin, token = _connection()
    digest = hashlib.sha256((origin + '\n' + token + '\n' + session_key).encode()).hexdigest()
    root = _PLUGIN_DIR / 'memlane-policy'
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    path = root / (digest + '.json')
    with (root / (digest + '.lock')).open('a+') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        data = json.loads(path.read_text()) if path.exists() else {}
        try:
            yield data
        finally:
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


def bind(workspace, host_session_key):
    """Called before ensure_launch_record; never rebind an existing unmanaged launch."""
    from _plugin_common import _read_map_record
    with _state() as state:
        if not state.get('binding') and _read_map_record(host_session_key).get('dataset'):
            raise NotReady('existing_session_requires_explicit_binding')
        result = _request('/v1/project/resolve', {'workspace': workspace, 'host_session_key': host_session_key})
        if not result.get('ready') or not result.get('dataset_id'):
            raise NotReady('project_policy_unconfirmed')
        uuid.UUID(result['dataset_id'])
        old = state.get('binding')
        binding = {'project_id': result['project_id'], 'dataset_id': result['dataset_id']}
        if old and binding != old:
            raise NotReady('session_binding_conflict')
        state['binding'] = binding
        return result['dataset_id']


def current(dataset=None):
    with _state() as state:
        binding = state.get('binding')
        if not binding:
            raise NotReady('project_binding_required')
        result = _request('/v1/project/policy?project_id=' + urllib.parse.quote(binding['project_id'], safe=''))
        if not result.get('ready') or result.get('dataset_id') != binding['dataset_id']:
            raise NotReady('project_policy_unconfirmed')
        if dataset and dataset != binding['dataset_id']:
            raise NotReady('managed_dataset_mismatch')
        return result


def permit(operation, dataset=None, *, automatic=False):
    if not managed():
        return True
    try:
        result = current(dataset)
        policy = result['policy']
        permission = 'read' if operation == 'read' else 'write'
        allowed = permission in policy['permissions']
        if operation == 'capture':
            allowed = allowed and policy['can_capture']
        if operation == 'process' and automatic:
            allowed = allowed and policy['can_auto_process']
        if operation == 'drain':
            with _state() as state:
                if state.get('blocked'):
                    return False
                if not allowed:
                    state['blocked'] = {'reason': 'write_denied', 'at': time.time(), 'revision': result['revision']}
        return allowed
    except (NotReady, KeyError, OSError, ValueError):
        return False


def require(operation, dataset=None, *, automatic=False):
    if not permit(operation, dataset, automatic=automatic):
        raise NotReady('当前 Agent 没有此项目的写入权限' if operation != 'read' else 'project_read_denied')


def block(status):
    if managed() and status in (401, 403):
        with _state() as state:
            state['blocked'] = {'reason': 'official_http_' + str(status), 'at': time.time()}


def _position(path):
    stat = Path(path).stat()
    return {'path': str(Path(path).absolute()), 'inode': stat.st_ino, 'device': stat.st_dev,
            'size': stat.st_size, 'at': time.time()}


def capture_boundary(transcript):
    """Return parser offset, or None to skip. Disabled periods are stat-only.

    First allowed hook after a pause establishes a fresh boundary and skips that
    hook's complete turn. Rotation/truncation remains paused, never resets to 0.
    """
    allowed = permit('capture')
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
        if not allowed:
            state['cursor'] = position
            state['paused'] = True
            return None
        if state.pop('paused', False):
            state['cursor'] = position
            state['resume_at'] = position['at']
            return None
        return state.get('cursor', {}).get('size', 0)
