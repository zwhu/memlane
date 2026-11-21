"""Manager-owned protocol adapter for an immutable OpenCode outbox worker.

Only counts, IDs and process ownership enter this adapter. Bodies and the send
journal remain in the fixed official outbox; Node rechecks the full installation
and authenticates the manager before accessing it.
"""
import asyncio
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import threading
import time
from urllib.parse import urlparse

from project_control import private_write
from project_policy import PolicyError

LIMIT = 16 * 1024 * 1024
HASH_CHUNK_SIZE = 1024 * 1024
VERIFY_CACHE_TTL_SECONDS = 300.0
VERIFY_CACHE_MAX_ENTRIES = 256

_VERIFY_LOCK = threading.Lock()
_FILE_DIGEST_CACHE = {}
_ARTIFACT_VERIFY_CACHE = {}


def clear_verification_caches():
    with _VERIFY_LOCK:
        _FILE_DIGEST_CACHE.clear()
        _ARTIFACT_VERIFY_CACHE.clear()


def _prune_cache(cache, now, max_entries=VERIFY_CACHE_MAX_ENTRIES):
    expired = [key for key, value in cache.items() if (value[0] if isinstance(value, tuple) else value) <= now]
    for key in expired:
        cache.pop(key, None)
    while len(cache) >= max_entries:
        cache.pop(next(iter(cache)))


def _sha256_file(path, *, chunk_size=HASH_CHUNK_SIZE):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        while True:
            chunk = handle.read(chunk_size)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def _stat_signature(st):
    return (st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns)


def _verify_file_digest(path, expected_sha256, *, max_size=None, executable=False,
                        ttl=VERIFY_CACHE_TTL_SECONDS, force=False, clock=time.monotonic):
    path = Path(path)
    if not isinstance(expected_sha256, str) or not expected_sha256:
        raise ValueError('expected digest required')
    if not path.is_absolute() or path.resolve() != path:
        raise ValueError('canonical file required')
    st = os.lstat(path)
    if stat.S_ISLNK(st.st_mode) or not stat.S_ISREG(st.st_mode):
        raise ValueError('regular file required')
    if max_size is not None and st.st_size > max_size:
        raise ValueError('file exceeds size limit')
    if executable and not os.access(path, os.X_OK):
        raise ValueError('executable file required')
    key = (str(path), st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, expected_sha256)
    now = clock()
    if not force and ttl > 0:
        with _VERIFY_LOCK:
            expires_at = _FILE_DIGEST_CACHE.get(key)
            if expires_at is not None and expires_at > now:
                return
    if _sha256_file(path) != expected_sha256:
        raise ValueError('file digest changed')
    if ttl > 0:
        with _VERIFY_LOCK:
            _prune_cache(_FILE_DIGEST_CACHE, now)
            _FILE_DIGEST_CACHE[key] = now + ttl


def _scan_artifact_tree_stats(root):
    root_str = str(root)
    manifest_name = '.memlane-opencode-artifact.json'
    stats = {}
    stack = [root_str]
    while stack:
        current = stack.pop()
        with os.scandir(current) as entries:
            for entry in entries:
                if entry.is_symlink():
                    raise ValueError('artifact symlink rejected')
                if entry.is_dir(follow_symlinks=False):
                    stack.append(entry.path)
                elif entry.is_file(follow_symlinks=False):
                    rel = os.path.relpath(entry.path, root_str).replace(os.sep, '/')
                    if rel == manifest_name:
                        continue
                    stats[rel] = _stat_signature(entry.stat(follow_symlinks=False))
                else:
                    raise ValueError('artifact tree changed')
    return stats


def _verify_artifact_cached(root, expected_manifest, *, ttl=VERIFY_CACHE_TTL_SECONDS,
                            force=False, clock=time.monotonic):
    from opencode_installation import verify as verify_artifact
    manifest_path = root / '.memlane-opencode-artifact.json'
    manifest_st = os.lstat(manifest_path)
    if stat.S_ISLNK(manifest_st.st_mode) or not stat.S_ISREG(manifest_st.st_mode):
        raise ValueError('artifact receipt symlink rejected')
    manifest_sig = _stat_signature(manifest_st)
    cache_key = (str(root), expected_manifest)
    now = clock()
    if not force and ttl > 0:
        with _VERIFY_LOCK:
            cached = _ARTIFACT_VERIFY_CACHE.get(cache_key)
        if cached is not None and cached[0] > now and cached[1] == manifest_sig:
            if _scan_artifact_tree_stats(root) == cached[2]:
                return copy.deepcopy(cached[3])
    manifest = verify_artifact(root, expected_manifest)
    if ttl > 0:
        refreshed_manifest_st = os.lstat(manifest_path)
        if stat.S_ISLNK(refreshed_manifest_st.st_mode) or not stat.S_ISREG(refreshed_manifest_st.st_mode):
            raise ValueError('artifact receipt symlink rejected')
        tree_stats = _scan_artifact_tree_stats(root)
        with _VERIFY_LOCK:
            _prune_cache(_ARTIFACT_VERIFY_CACHE, now)
            _ARTIFACT_VERIFY_CACHE[cache_key] = (
                now + ttl, _stat_signature(refreshed_manifest_st), tree_stats, copy.deepcopy(manifest))
    return manifest


def _process_start_time(pid):
    return subprocess.check_output(['ps', '-p', str(pid), '-o', 'lstart='], text=True).strip()


def private_file(path, limit=65536):
    path = Path(path)
    if (not path.is_absolute() or path.resolve() != path or path.is_symlink()
            or not path.is_file() or path.stat().st_mode & 0o077 or path.stat().st_size > limit):
        raise ValueError('private file required')
    return path.read_bytes()


def verify(installation, runtime, agent_id, *, force=False):
    try:
        if installation.get('host_kind') != 'opencode' or installation.get('instance_id') != agent_id:
            raise ValueError('wrong host')
        root = Path(installation['plugin_dir'])
        if not root.is_absolute() or root.resolve() != root:
            raise ValueError('canonical artifact required')
        manifest = _verify_artifact_cached(root, installation['manifest_sha256'], force=force)
        if 'dist/src/managed-backlog.js' not in manifest['files']:
            raise PolicyError('backlog_host_unsupported', 409)
        helper = root / 'dist/src/managed-pending-cli.js'
        if manifest['files'][str(helper.relative_to(root))] != installation['helper_sha256']:
            raise ValueError('worker changed')
        raw = private_file(installation['binding_file'])
        if hashlib.sha256(raw).hexdigest() != installation['binding_sha256']:
            raise ValueError('binding changed')
        binding = json.loads(raw)
        control = runtime['project_control']
        for value in (control['url'], runtime['service_url']):
            parsed = urlparse(value)
            if (parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port
                    or parsed.path not in ('', '/') or parsed.username or parsed.query or parsed.fragment):
                raise ValueError('explicit original loopback required')
        if (binding.get('version') != 1 or binding.get('instance_id') != agent_id
                or binding.get('plugin_root') != str(root)
                or binding.get('profile') != installation['profile']
                or binding['memlane']['origin'] != control['url'].rstrip('/')
                or binding['cognee']['baseUrl'] != runtime['service_url'].rstrip('/')):
            raise ValueError('original target changed')
        if not isinstance(binding.get('secrets'), list) or len(binding['secrets']) != 2:
            raise ValueError('binding secrets changed')
        for secret in binding['secrets']:
            if hashlib.sha256(private_file(secret['path'])).hexdigest() != secret['sha256']:
                raise ValueError('bound credential changed')
        private_file(control['management_token_file'])
        node = Path(installation['node_executable'])
        _verify_file_digest(
            node,
            installation['node_sha256'],
            max_size=512 * 1024 * 1024,
            executable=True,
            force=force,
        )
        profile = Path(installation['profile'])
        if not profile.is_absolute() or profile.resolve() != profile or not profile.is_dir():
            raise ValueError('profile changed')
        return node, helper, profile
    except PolicyError:
        raise
    except (OSError, ValueError, TypeError, KeyError):
        raise PolicyError('backlog_installation_changed', 409) from None


async def execute(runtime, installation, project_id, target, action, cancel_file, update=None):
    if action not in ('review', 'save', 'process', 'check'):
        raise PolicyError('backlog_worker_unconfirmed', 503)
    read_only = action in ('review', 'check')
    node, helper, profile = await asyncio.to_thread(
        verify, installation, runtime, target['agent_id'], force=not read_only)
    command = {'action': 'backlog_' + action, 'project_id': project_id,
               'dataset_id': target['dataset'], 'host_session_key': target['host']}
    if action == 'check':
        command['action'] = 'check'
    if not read_only:
        command.update(session_id=target['session_id'], native_session=target['native'], cancel_file=str(cancel_file))
    if action == 'save':
        command['tokens'] = target['tokens']
    allowed = ('plugin_dir', 'manifest_sha256', 'binding_file', 'binding_sha256', 'node_executable', 'node_sha256')
    payload = {'installation': {key: installation[key] for key in allowed}, 'agent_id': target['agent_id'],
               'operator': {'token_file': runtime['project_control']['management_token_file'],
                            'control_url': runtime['project_control']['url'], 'service_url': runtime['service_url']},
               'command': command}
    raw = json.dumps(payload).encode()
    if len(raw) > LIMIT:
        raise PolicyError('backlog_snapshot_unconfirmed', 503)
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith(
        ('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_', 'NODE_', 'OPENAI_', 'ANTHROPIC_', 'GEMINI_', 'GOOGLE_'))}
    env['HOME'] = str(profile)
    process = await asyncio.create_subprocess_exec(str(node), str(helper), cwd=profile, env=env,
        stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL, limit=LIMIT)
    owner_path = Path(str(cancel_file) + '.opencode-worker.json')
    owner = {'pid': process.pid, 'phase': 'starting', 'action': action,
             'agent_id': target['agent_id'], 'host': target['host']}
    try:
        if not read_only:
            private_write(owner_path, json.dumps(owner).encode())
            started = await asyncio.to_thread(_process_start_time, process.pid)
            owner.update(started=started, phase='running')
            private_write(owner_path, json.dumps(owner).encode())
        else:
            owner['phase'] = 'running'
        process.stdin.write(raw)
        await process.stdin.drain()
        process.stdin.close()
        last = None
        while True:
            line = await asyncio.wait_for(process.stdout.readline(), 45)
            if not line:
                break
            value = json.loads(line)
            if not isinstance(value, dict) or value.get('error'):
                code = value.get('error', '') if isinstance(value, dict) else ''
                if not re.fullmatch(r'(pending|original|project)_[a-z_]{1,80}', code):
                    code = 'backlog_worker_unconfirmed'
                raise PolicyError(code, 503)
            if update:
                update(value)
            last = value
        await asyncio.wait_for(process.wait(), 10)
        if process.returncode != 0 or last is None:
            raise PolicyError('backlog_worker_unconfirmed', 503)
        if action == 'check':
            unknown = last.get('unknown_outcomes')
            if type(unknown) is not int or unknown < 0 or last.get('checked') is not True:
                raise PolicyError('backlog_worker_unconfirmed', 503)
            return {'status': 'unconfirmed' if unknown else 'confirmed', 'unknown': unknown,
                    'matched': last.get('matched_unknown', 0),
                    'reason': 'original_receipt_unavailable' if unknown else ''}
        return last
    except BaseException:
        if not read_only:
            private_write(cancel_file, b'stop')
        try:
            process.terminate()
        except ProcessLookupError:
            pass
        try:
            await asyncio.wait_for(process.wait(), 35)
        except asyncio.TimeoutError:
            owner['phase'] = 'exit_unconfirmed'
            raise PolicyError('backlog_sender_exit_unconfirmed', 503) from None
        raise
    finally:
        if process.returncode is not None:
            owner.update(phase='confirmed_exited', exit_code=process.returncode)
        if read_only and process.returncode is not None:
            owner_path.unlink(missing_ok=True)
        else:
            private_write(owner_path, json.dumps(owner).encode())
