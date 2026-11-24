"""Computer-level OpenCode entry; only the owned global loader is changed."""
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import stat
import threading
import time

from project_control import loopback_origin, private_write
from project_policy import PolicyError
from opencode_installation import loader, prepare as prepare_binding, verify, pending_runtime


NODE_HASH_CACHE_TTL_SECONDS = 300.0
NODE_HASH_CACHE_MAX_ENTRIES = 256
NODE_HASH_CHUNK_BYTES = 1024 * 1024
MAX_NODE_BINARY_BYTES = 512 * 1024 * 1024
_node_hash_lock = threading.Lock()
_node_hash_cache = {}


def clear_node_hash_cache():
    with _node_hash_lock:
        _node_hash_cache.clear()


def _node_stat_key(node, st):
    return (str(node), st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, st.st_ctime_ns)


def _stream_fd_sha256(handle, *, max_bytes=MAX_NODE_BINARY_BYTES):
    digest = hashlib.sha256()
    total = 0
    while True:
        chunk = handle.read(NODE_HASH_CHUNK_BYTES)
        if not chunk:
            break
        total += len(chunk)
        if total > max_bytes:
            raise ValueError('node exceeds size limit')
        digest.update(chunk)
    return digest.hexdigest()


def _node_digest(node, st, *, force):
    key = _node_stat_key(node, st)
    now = time.monotonic()
    if not force:
        with _node_hash_lock:
            cached = _node_hash_cache.get(key)
            if cached is not None and cached[0] > now:
                return cached[1]
    flags = os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_NONBLOCK', 0)
    fd = os.open(node, flags)
    try:
        fst = os.fstat(fd)
        if (not stat.S_ISREG(fst.st_mode)
                or fst.st_size > MAX_NODE_BINARY_BYTES
                or (fst.st_dev, fst.st_ino) != (st.st_dev, st.st_ino)):
            raise ValueError('node changed before hashing')
        if node.resolve(strict=True) != node or not os.access(node, os.X_OK):
            raise ValueError('node path invalid')
        with os.fdopen(fd, 'rb') as handle:
            fd = -1
            digest = _stream_fd_sha256(handle, max_bytes=MAX_NODE_BINARY_BYTES)
            post_fst = os.fstat(handle.fileno())
    finally:
        if fd >= 0:
            os.close(fd)
    final_key = _node_stat_key(node, post_fst)
    if final_key != key or _node_stat_key(node, os.lstat(node)) != key:
        raise ValueError('node changed while hashing')
    now = time.monotonic()
    with _node_hash_lock:
        expired = [k for k, v in _node_hash_cache.items() if v[0] <= now]
        for k in expired:
            _node_hash_cache.pop(k, None)
        _node_hash_cache.pop(key, None)
        while len(_node_hash_cache) >= NODE_HASH_CACHE_MAX_ENTRIES:
            del _node_hash_cache[next(iter(_node_hash_cache))]
        _node_hash_cache[key] = (now + NODE_HASH_CACHE_TTL_SECONDS, digest)
    return digest


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def safe_file(path):
    if any(item.is_symlink() for item in (path, *path.parents)):
        raise PolicyError('opencode_configuration_requires_review', 409)
    if not path.exists():
        return b''
    if not path.is_file() or path.stat().st_size > 1024 * 1024:
        raise PolicyError('opencode_configuration_requires_review', 409)
    return path.read_bytes()


def jsonc(raw):
    """Read JSONC without editing it. Strings and duplicate-key ambiguity matter."""
    text = raw.decode('utf-8-sig')
    output, i, quoted, escaped = [], 0, False, False
    while i < len(text):
        char = text[i]
        if quoted:
            output.append(char)
            if escaped:
                escaped = False
            elif char == '\\':
                escaped = True
            elif char == '"':
                quoted = False
            i += 1
            continue
        if char == '"':
            quoted = True
        elif text.startswith('//', i):
            end = text.find('\n', i)
            i = len(text) if end < 0 else end
            output.append('\n')
            continue
        elif text.startswith('/*', i):
            end = text.find('*/', i + 2)
            if end < 0:
                raise ValueError('unclosed JSONC comment')
            output.append(' ')
            i = end + 2
            continue
        output.append(char)
        i += 1
    cleaned = ''.join(output)
    output, quoted, escaped = [], False, False
    for i, char in enumerate(cleaned):
        if not quoted and char == ',' and cleaned[i + 1:].lstrip().startswith(('}', ']')):
            continue
        output.append(char)
        if escaped:
            escaped = False
        elif quoted and char == '\\':
            escaped = True
        elif char == '"':
            quoted = not quoted
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError('duplicate configuration key')
            result[key] = value
        return result
    def invalid(value):
        raise ValueError('invalid JSON number')
    return json.loads(''.join(output), object_pairs_hook=pairs, parse_constant=invalid)


class OpenCodeConnection:
    kind = 'opencode'

    def __init__(self, registry, config, profile, executable):
        self.registry, self.config, self.executable = registry, config, executable
        self.profile = Path(profile).expanduser().absolute()
        self.id = 'opencode-' + sha(str(self.profile).encode())[:24]
        self.root = registry.path.parent / 'connections' / self.id
        self.target = self.profile / 'plugins/memlane-cognee.ts'
        self.artifact = config.get('opencode_plugin_dir')
        self.artifact_sha = config.get('opencode_manifest_sha256')

    def record(self):
        return self.registry.state.get('connections', {}).get(self.id, {})

    def view(self, *, force_verify=False):
        record = self.record()
        status = record.get('status', 'not_connected' if self.artifact and self.artifact_sha else 'integration_pending')
        if self.profile.exists() and not self.profile.is_dir():
            raise PolicyError('opencode_configuration_requires_review', 409)
        if status == 'configured_pending_new_session':
            self.installed(record, force=force_verify)
        if self.id in self.registry.state.get('enrollments', {}) and self.id not in self.registry.state['agents']:
            status = 'agent_creation_unconfirmed'
        return {'instance_id': self.id, 'kind': self.kind, 'profile': str(self.profile),
                'detected': bool(self.executable and Path(self.executable).is_file() and os.access(self.executable, os.X_OK)),
                'status': status, 'requires_reload': True, 'entrypoints': ['cli'],
                'agent_registered': self.id in self.registry.state['agents'],
                'preview': copy.deepcopy(record.get('preview')),
                'capabilities': {key: {'available': True, 'verified': False} for key in
                                 ('explicit_memory', 'auto_capture', 'auto_recall', 'project_policy')},
                'new_sessions': sum(json.loads(key)[0] == self.id and key not in record.get('sessions_before_activation', [])
                                    for key in self.registry.state['sessions']) if record.get('activated') else 0}

    def preview(self):
        if self.profile.resolve() != self.profile or (self.profile.exists() and not self.profile.is_dir()):
            raise PolicyError('opencode_configuration_requires_review', 409)
        raw = safe_file(self.target)
        record = self.record()
        if self.target.exists() and sha(raw) != record.get('active_loader_sha256'):
            raise PolicyError('existing_opencode_loader_requires_review', 409)
        configs = {}
        for name in ('opencode.json', 'opencode.jsonc', 'config.json'):
            body = safe_file(self.profile / name)
            configs[name] = sha(body)
            if not body:
                continue
            try:
                parsed = jsonc(body)
                if not isinstance(parsed, dict) or not isinstance(parsed.get('plugin', []), list):
                    raise ValueError()
                for entry in parsed.get('plugin', []):
                    identifier = entry[0] if isinstance(entry, list) and entry else entry
                    if not isinstance(identifier, str):
                        raise ValueError()
                    if 'cognee' in identifier.lower():
                        raise PolicyError('duplicate_opencode_memory_plugin_requires_review', 409)
            except (ValueError, UnicodeError, TypeError):
                raise PolicyError('opencode_configuration_requires_review', 409) from None
        inventory = []
        for directory in (self.profile / 'plugins', self.profile / 'plugin'):
            if directory.is_symlink():
                raise PolicyError('opencode_configuration_requires_review', 409)
            if not directory.exists():
                continue
            if not directory.is_dir():
                raise PolicyError('opencode_configuration_requires_review', 409)
            for path in directory.rglob('*'):
                if len(inventory) > 1000 or path.is_symlink():
                    raise PolicyError('opencode_configuration_requires_review', 409)
                inventory.append(str(path.relative_to(self.profile)))
                if path != self.target and 'cognee' in path.name.lower():
                    raise PolicyError('duplicate_opencode_memory_plugin_requires_review', 409)
        inventory = [name for name in inventory if name != str(self.target.relative_to(self.profile))]
        return {'instance_id': self.id, 'kind': self.kind, 'target': str(self.target),
                'baseline_sha256': sha(raw), 'configuration_sha256': sha(json.dumps([configs, sorted(inventory)], sort_keys=True).encode()),
                'preserve_other_plugins': True, 'requires_reload': True, 'requires_host_trust': True}

    def verify_installation(self, installation, *, force=False):
        try:
            if installation['instance_id'] != self.id or installation['profile'] != str(self.profile):
                raise ValueError()
            manifest = verify(installation['plugin_dir'], installation['manifest_sha256'])
            if 'dist/src/managed-pending-cli.js' in manifest['files']:
                node = Path(installation['node_executable'])
                if not node.is_absolute():
                    raise ValueError()
                st = os.lstat(node)
                expected = installation['node_sha256']
                if (stat.S_ISLNK(st.st_mode) or not stat.S_ISREG(st.st_mode) or node.resolve(strict=True) != node
                        or not os.access(node, os.X_OK) or st.st_size > MAX_NODE_BINARY_BYTES
                        or not isinstance(expected, str) or not expected
                        or _node_digest(node, st, force=force) != expected
                        or installation['helper_sha256'] != manifest['files']['dist/src/managed-pending-cli.js']):
                    raise ValueError()
            binding = Path(installation['binding_file'])
            raw = safe_file(binding)
            if binding.stat().st_mode & 0o077 or sha(raw) != installation['binding_sha256']:
                raise ValueError()
            data = json.loads(raw)
            if data['instance_id'] != self.id or data['profile'] != str(self.profile):
                raise ValueError()
            for secret in data['secrets']:
                path = Path(secret['path'])
                if path.stat().st_mode & 0o077 or sha(safe_file(path)) != secret['sha256']:
                    raise ValueError()
            if sha(loader(installation)) != installation['loader_sha256']:
                raise ValueError()
        except (OSError, KeyError, ValueError, TypeError):
            raise PolicyError('opencode_installation_requires_review', 409) from None

    def installed(self, record, *, force=False):
        installation = record.get('active_installation') or record.get('installation')
        if not installation:
            raise PolicyError('opencode_installation_requires_review', 409)
        self.verify_installation(installation, force=force)
        if sha(safe_file(self.target)) != record.get('active_loader_sha256', record.get('proposed_sha256')):
            raise PolicyError('opencode_loader_changed_requires_review', 409)

    async def prepare(self, expected, operation_id=None):
        r = self.registry
        r.check_revision(expected)
        if not self.executable or not Path(self.executable).is_file() or not os.access(self.executable, os.X_OK):
            raise PolicyError('host_executable_unavailable', 503)
        if not self.artifact or not self.artifact_sha:
            raise PolicyError('opencode_component_required', 503)
        preview = self.preview()
        before = self.record()
        if before.get('status') in ('preparation_unknown', 'activation_unknown'):
            raise PolicyError('connection_result_requires_review', 409)
        try:
            manifest = verify(self.artifact, self.artifact_sha)
        except (OSError, ValueError):
            raise PolicyError('opencode_artifact_requires_review', 409) from None
        await asyncio.to_thread(pending_runtime, manifest, self.config.get('opencode_node_executable', 'node'))
        if self.id not in r.state['agents']:
            await r.provision_agent(expected, self.id, '本机 OpenCode')
        if self.id not in r.state['agents']:
            raise PolicyError('agent_creation_result_unknown', 503)
        async with r.mutex:
            record = {'status': 'preparation_unknown', 'preview': preview, 'prepare_operation_id': operation_id,
                      'active_installation': copy.deepcopy(before.get('active_installation')),
                      'active_loader_sha256': before.get('active_loader_sha256')}
            r.state.setdefault('connections', {})[self.id] = record
            r.state['revision'] += 1
            r.save()
            agent = copy.deepcopy(r.state['agents'][self.id])
        control = self.config.get('project_control', {})
        control_url = control.get('url') or self.config['project_control_url']
        gateway_url = loopback_origin(control_url) + '/cognee'
        try:
            installation = await asyncio.to_thread(prepare_binding, self.artifact, r.path.parent, self.id, agent,
                gateway_url, control_url, self.profile,
                artifact_sha256=self.artifact_sha, node=self.config.get('opencode_node_executable', 'node'))
        except (OSError, ValueError, KeyError):
            raise PolicyError('opencode_artifact_requires_review', 409) from None
        async with r.mutex:
            stub = loader(installation)
            private_write(self.root / 'opencode-proposed.ts', stub)
            record.update(installation=installation, proposed_sha256=sha(stub), status='prepared')
            record['preview']['proposed_sha256'] = record['proposed_sha256']
            r.state['agents'][self.id].update(kind=self.kind, host_profile_fingerprint=sha(str(self.profile).encode()),
                entrypoints=['cli'], installation=installation, capabilities=self.view()['capabilities'])
            r.save()
            return self.view()

    async def activate(self, expected, reviewed_sha, operation_id=None):
        r = self.registry
        async with r.mutex:
            r.check_revision(expected)
            record = self.record()
            if record.get('status') != 'prepared' or reviewed_sha != record.get('proposed_sha256'):
                raise PolicyError('reviewed_prepared_connection_required', 409)
            current = self.preview()
            if any(current[key] != record['preview'][key] for key in ('baseline_sha256', 'configuration_sha256')):
                raise PolicyError('host_configuration_changed', 409)
            installation = record['installation']
            self.verify_installation(installation, force=True)
            stub = safe_file(self.root / 'opencode-proposed.ts')
            if sha(stub) != reviewed_sha or stub != loader(installation):
                raise PolicyError('connection_preview_changed', 409)
            record.update(status='activation_unknown', activate_operation_id=operation_id,
                          sessions_before_activation=[key for key in r.state['sessions'] if json.loads(key)[0] == self.id])
            r.state['revision'] += 1
            r.save()
            if sha(safe_file(self.target)) != record['preview']['baseline_sha256']:
                raise PolicyError('host_configuration_changed', 409)
            private_write(self.target, stub)
            self.installed({**record, 'active_installation': installation, 'active_loader_sha256': reviewed_sha}, force=True)
            record.update(status='configured_pending_new_session', activated=True,
                          active_installation=copy.deepcopy(installation), active_loader_sha256=reviewed_sha)
            r.save()
            return self.view()

    async def review(self, expected):
        async with self.registry.mutex:
            self.registry.check_revision(expected)
            record = self.record()
            if record.get('status') == 'activation_unknown':
                self.installed({**record, 'active_installation': record['installation'],
                                'active_loader_sha256': record['proposed_sha256']}, force=True)
                record.update(status='configured_pending_new_session', activated=True,
                              active_installation=copy.deepcopy(record['installation']), active_loader_sha256=record['proposed_sha256'])
                self.registry.state['revision'] += 1
                self.registry.save()
            return self.view(force_verify=True)

    def original_result(self, operation_id):
        record = self.record()
        if operation_id == record.get('prepare_operation_id') and record.get('status') == 'prepared':
            self.verify_installation(record['installation'])
            if sha(safe_file(self.root / 'opencode-proposed.ts')) != record['proposed_sha256']:
                return None
        elif operation_id == record.get('activate_operation_id') and record.get('status') in ('activation_unknown', 'configured_pending_new_session'):
            self.installed({**record, 'active_installation': record['installation'], 'active_loader_sha256': record['proposed_sha256']})
        else:
            return None
        return {'revision': self.registry.state['revision'], 'connection': self.view()}

    def safely_rejected(self, operation_id, action):
        record = self.record()
        if action == 'activate':
            return record.get('activate_operation_id') != operation_id
        if action == 'prepare':
            enrollment = self.registry.state.get('enrollments', {}).get(self.id, {})
            return record.get('prepare_operation_id') != operation_id and enrollment.get('status') != 'unconfirmed'
        return False
