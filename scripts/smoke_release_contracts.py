#!/usr/bin/env python3
"""Level 0 local no-mock contract smoke test run in an isolated $TMPDIR before UTM."""
from __future__ import annotations

import argparse
import fcntl
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

if __package__:
    from . import utm_guard
    from .verify_setup_cli import VerificationError, assert_trae_hooks_4_event_schema, flock_held
else:
    import utm_guard
    from verify_setup_cli import VerificationError, assert_trae_hooks_4_event_schema, flock_held

ROOT = Path(__file__).resolve().parents[1]

EXIT_OK = 0
EXIT_CONTRACT_FAILED = 1
EXIT_ENV_UNAVAILABLE = 2
EXIT_ISOLATION_VIOLATION = 3

DEFAULT_TOTAL_TIMEOUT_S = 30.0
DEFAULT_CLI_TIMEOUT_S = 10.0

OWNER_HOLDER_SCRIPT = r'''
import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

cfg_path = Path(sys.argv[1])
store_root = Path(sys.argv[2])
control_script = Path(sys.argv[3])
python_exe = sys.argv[4]
ready_marker = Path(sys.argv[5])
resources_dir = Path(sys.argv[6])
resources_hash = sys.argv[7]
stderr_log_path = Path(sys.argv[8])

def lstart(pid):
    out = subprocess.check_output(['/bin/ps', '-p', str(pid), '-o', 'lstart='], encoding='utf-8', timeout=5)
    return out.strip()

lock_path = store_root / '.service-owner'
receipt_path = store_root / '.memlane-dev-owner.json'
lock_fd = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)

child_env = dict(os.environ)
child_env.pop('MEMLANE_DEV_RUNTIME_CONFIG', None)
stderr_fh = open(stderr_log_path, 'wb')
child = subprocess.Popen(
    [python_exe, '-E', '-s', '-B', str(control_script), '--config', str(cfg_path)],
    cwd=str(resources_dir),
    env=child_env,
    stdin=subprocess.DEVNULL,
    stdout=subprocess.DEVNULL,
    stderr=stderr_fh,
)

cfg = json.loads(cfg_path.read_text(encoding='utf-8'))
my_pid = os.getpid()
rec = {
    'pid': my_pid,
    'started': lstart(my_pid),
    'phase': 'first_install_setup',
    'config': str(cfg_path),
    'official_data': str(store_root),
    'service_url': cfg['service_url'],
    'completion_url': cfg['completion_url'],
    'project_control_url': cfg['project_control']['url'],
    'resources_dir': str(resources_dir),
    'resources_hash': resources_hash,
    'runtime_config': str(cfg_path),
    'generation_mode': 'unconfigured',
    'children': [child.pid],
    'service_children': {'project_control': child.pid},
    'child_start_times': {str(child.pid): lstart(child.pid)},
}
tmp_rec = receipt_path.with_suffix('.tmp')
fd = os.open(tmp_rec, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
with os.fdopen(fd, 'w', encoding='utf-8') as handle:
    json.dump(rec, handle)
os.replace(tmp_rec, receipt_path)

stopping = False
def handle_term(signum, frame):
    global stopping
    stopping = True

signal.signal(signal.SIGTERM, handle_term)
signal.signal(signal.SIGINT, handle_term)

ready_marker.write_text(json.dumps({'owner_pid': my_pid, 'control_pid': child.pid}), encoding='utf-8')

try:
    while not stopping:
        if child.poll() is not None:
            break
        time.sleep(0.05)
finally:
    if child.poll() is None:
        child.terminate()
        try:
            child.wait(timeout=3)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=2)
    try:
        receipt_path.unlink()
    except FileNotFoundError:
        pass
    try:
        fcntl.flock(lock_fd, fcntl.LOCK_UN)
    except OSError:
        pass
    os.close(lock_fd)
    try:
        stderr_fh.close()
    except OSError:
        pass
'''

WORKER_LOCK_HOLDER_SCRIPT = r'''
import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import time

inst_dir = Path(sys.argv[1])
op_id = sys.argv[2]
plan_digest = sys.argv[3]
cfg_path = sys.argv[4]
runtime_hash = sys.argv[5]
ready_flag = Path(sys.argv[6])
fail_flag = Path(sys.argv[7])
done_flag = Path(sys.argv[8])

inst_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
lock_path = inst_dir / 'installation.lock'
state_path = inst_dir / 'worker-state.json'
started = subprocess.check_output(['/bin/ps', '-p', str(os.getpid()), '-o', 'lstart='], encoding='utf-8', timeout=5).strip()

def write_state(state_name, stage_name, error_code=''):
    payload = {
        'schema': 1,
        'pid': os.getpid(),
        'started': started,
        'operation_id': op_id,
        'plan_digest': plan_digest,
        'config_path': cfg_path,
        'start_runtime': True,
        'runtime_hash': runtime_hash,
        'download_source': 'china',
        'state': state_name,
        'stage': stage_name,
        'error_code': error_code,
        'heartbeat_at': '2026-11-25T10:00:00Z',
    }
    tmp = state_path.with_suffix('.tmp')
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as handle:
        json.dump(payload, handle)
    os.replace(tmp, state_path)

fd = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
fcntl.flock(fd, fcntl.LOCK_EX)
try:
    write_state('running', 'starting_runtime')
    ready_flag.write_text('ready', encoding='utf-8')
    deadline = time.monotonic() + 20.0
    while not fail_flag.exists() and time.monotonic() < deadline:
        time.sleep(0.02)
    write_state('failed', 'starting_runtime', 'owner_busy')
finally:
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)
done_flag.write_text('done', encoding='utf-8')
'''


class ContractFailure(RuntimeError):
    """Raised when a product contract assertion fails (exit 1)."""


class EnvironmentUnavailable(RuntimeError):
    """Raised when a local test prerequisite is missing (exit 2)."""


class IsolationViolation(RuntimeError):
    """Raised when sandbox containment, lsof, pycache, or privacy guard fails (exit 3)."""


def _require_contract(condition: bool, message: str) -> None:
    if not condition:
        raise ContractFailure(message)


def _require_isolation(condition: bool, message: str) -> None:
    if not condition:
        raise IsolationViolation(message)


def _write_private_bytes(path: Path, data: bytes) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    tmp = path.parent / f'.{path.name}.{os.getpid()}.tmp'
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'wb') as handle:
            fd = -1
            handle.write(data)
            handle.flush()
        os.replace(tmp, path)
    finally:
        if fd >= 0:
            os.close(fd)
        if tmp.exists():
            try:
                tmp.unlink()
            except OSError:
                pass


def _write_private_json(path: Path, payload: dict) -> None:
    _write_private_bytes(path, (json.dumps(payload, indent=2, ensure_ascii=False) + '\n').encode('utf-8'))


def _free_ports(count: int) -> list[int]:
    sockets: list[socket.socket] = []
    try:
        for _ in range(count):
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.bind(('127.0.0.1', 0))
            sockets.append(sock)
        return [sock.getsockname()[1] for sock in sockets]
    finally:
        for sock in sockets:
            sock.close()


def _python_supports_control_server(python_exe: Path) -> bool:
    if not python_exe.is_file() or not os.access(python_exe, os.X_OK):
        return False
    try:
        probe = subprocess.run(
            [str(python_exe), '-E', '-s', '-B', '-c', 'import aiohttp, tomllib, tomlkit'],
            capture_output=True, encoding='utf-8', timeout=5, check=False,
        )
        return probe.returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


def resolve_smoke_python(explicit: str = '', *, repo_root: Path = ROOT) -> Path:
    """Resolve a Python 3.12+ interpreter capable of running project_control_server.py."""
    candidates: list[Path] = []
    if explicit:
        candidates.append(Path(explicit).expanduser().absolute())
    else:
        candidates.append(repo_root / '.local' / 'cognee-python' / 'bin' / 'python')
        candidates.append(Path(sys.executable))
    for candidate in candidates:
        if _python_supports_control_server(candidate):
            return candidate
    raise EnvironmentUnavailable(
        'no Python 3.12+ interpreter with aiohttp/tomllib/tomlkit found; pass --python /path/to/python'
    )


class _OfficialStubServer:
    """Minimal loopback Cognee stub allowing real project_control_server.py to boot without downloads."""

    def __init__(self, owner_id: str, owner_token: str, *, agent_identities: dict[str, str] | None = None):
        self.owner_id = owner_id
        self.owner_token = owner_token
        self.agent_identities = dict(agent_identities or {})
        self.datasets: list[dict] = []
        self._lock = threading.Lock()
        stub = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, format, *args):  # noqa: A002
                return

            def _send_json(self, code: int, payload):
                raw = json.dumps(payload).encode('utf-8')
                self.send_response(code)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def _caller_identity(self) -> tuple[bool, str | None]:
                key = self.headers.get('X-Api-Key', '')
                if key == stub.owner_token:
                    return True, None
                if key in stub.agent_identities:
                    return True, stub.agent_identities[key]
                return False, None

            def do_GET(self):  # noqa: N802
                ok, agent_principal = self._caller_identity()
                if not ok:
                    self._send_json(401, {'detail': 'unauthorized'})
                    return
                path = self.path.split('?', 1)[0]
                if path == '/api/v1/users/me':
                    if agent_principal is not None:
                        self._send_json(200, {'id': agent_principal, 'parent_user_id': stub.owner_id})
                    else:
                        self._send_json(200, {'id': stub.owner_id, 'email': 'memlane-local@example.com'})
                elif path == '/api/v1/datasets':
                    with stub._lock:
                        self._send_json(200, list(stub.datasets))
                elif path.startswith('/api/v1/permissions/'):
                    self._send_json(200, [])
                else:
                    self._send_json(404, {'detail': 'not_found'})

            def do_POST(self):  # noqa: N802
                ok, _ = self._caller_identity()
                if not ok:
                    self._send_json(401, {'detail': 'unauthorized'})
                    return
                length = int(self.headers.get('Content-Length') or 0)
                body = json.loads(self.rfile.read(length).decode('utf-8')) if length else {}
                path = self.path.split('?', 1)[0]
                if path == '/api/v1/datasets':
                    row = {
                        'id': str(uuid.uuid4()),
                        'name': str(body.get('name', 'dataset')),
                        'ownerId': stub.owner_id,
                    }
                    with stub._lock:
                        stub.datasets.append(row)
                    self._send_json(200, row)
                else:
                    self._send_json(200, {'ok': True})

            def do_DELETE(self):  # noqa: N802
                ok, _ = self._caller_identity()
                if not ok:
                    self._send_json(401, {'detail': 'unauthorized'})
                    return
                length = int(self.headers.get('Content-Length') or 0)
                if length:
                    self.rfile.read(length)
                self._send_json(200, {'ok': True})

        self._server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.port = self._server.server_address[1]
        self.url = f'http://127.0.0.1:{self.port}'
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    def start(self) -> None:
        self._thread.start()

    def stop(self) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=2)


def _clean_smoke_env(root: Path, *, extra: dict[str, str] | None = None) -> dict[str, str]:
    home = root / 'home'
    tmp = root / 'tmp'
    bin_dir = root / 'bin'
    for d in (home, tmp, bin_dir):
        d.mkdir(mode=0o700, parents=True, exist_ok=True)
    env = {
        'HOME': str(home),
        'TMPDIR': str(tmp),
        'PATH': f'{bin_dir}:{os.defpath}',
        'LANG': 'en_US.UTF-8',
        'LC_ALL': 'en_US.UTF-8',
    }
    if extra:
        env.update(extra)
    return env


def assert_env_isolated(env: dict[str, str], root: Path) -> None:
    root_real = Path(os.path.realpath(str(root)))
    _require_isolation('MEMLANE_DEV_RUNTIME_CONFIG' not in env, 'MEMLANE_DEV_RUNTIME_CONFIG leaked into smoke env')
    for key in ('HOME', 'TMPDIR'):
        val = env.get(key, '')
        _require_isolation(bool(val), f'{key} missing in smoke env')
        val_real = Path(os.path.realpath(val))
        try:
            val_real.relative_to(root_real)
        except ValueError:
            raise IsolationViolation(f'{key} escapes isolated root') from None


def assert_config_isolated(config: dict, root: Path, *, real_home: Path | None = None) -> None:
    """Verify every data/config/token/profile path in runtime.json is inside resolved $TMPDIR."""
    root_real = Path(os.path.realpath(str(root)))
    user_home = Path(os.path.realpath(str(real_home or Path.home())))
    forbidden_prefixes = (
        '.local/daily',
        str(user_home / 'Library' / 'Application Support' / 'Memlane'),
    )
    path_keys = {
        'official_data', 'token_file', 'completion_token_file', 'generation_file',
        'client_state_file', 'registry_file', 'management_token_file', 'plugin_dir', 'profile',
    }

    def walk(node, key_name=''):
        if isinstance(node, dict):
            for k, v in node.items():
                walk(v, k)
        elif isinstance(node, list):
            for item in node:
                walk(item, key_name)
        elif isinstance(node, str):
            for marker in forbidden_prefixes:
                _require_isolation(marker not in node, f'config field {key_name} references forbidden path')
            if key_name in path_keys and node:
                p_real = Path(os.path.realpath(node))
                try:
                    p_real.relative_to(root_real)
                except ValueError:
                    raise IsolationViolation(f'config field {key_name} escapes isolated root') from None

    walk(config)


def parse_lsof_paths(output: str) -> list[str]:
    paths: list[str] = []
    for line in output.splitlines():
        if line.startswith('n') and len(line) > 1 and line[1] == '/':
            paths.append(line[1:])
    return paths


def assert_lsof_isolated(
    pids: list[int],
    *,
    repo_root: Path = ROOT,
    real_home: Path | None = None,
    lsof_runner=None,
) -> dict:
    """Assert live smoke processes hold zero file descriptors to .local/daily or real user host state."""
    valid_pids = [pid for pid in pids if isinstance(pid, int) and pid > 1]
    if not valid_pids:
        return {'checked': False, 'reason': 'no_pids'}
    pid_arg = ','.join(str(p) for p in valid_pids)
    if lsof_runner is None:
        try:
            proc = subprocess.run(
                ['/usr/sbin/lsof', '-n', '-P', '-F', 'n', '-p', pid_arg],
                capture_output=True, encoding='utf-8', timeout=5, check=False,
            )
        except (OSError, subprocess.SubprocessError):
            return {'checked': False, 'reason': 'lsof_unavailable'}
        if proc.returncode != 0 and not proc.stdout:
            return {'checked': False, 'reason': f'lsof_exit_{proc.returncode}'}
        raw_out = proc.stdout
    else:
        raw_out = lsof_runner(valid_pids)

    home = Path(os.path.realpath(str(real_home or Path.home())))
    daily_dir = Path(os.path.realpath(str(repo_root / '.local' / 'daily')))
    forbidden_roots = (
        daily_dir,
        home / 'Library' / 'Application Support' / 'Memlane',
        home / 'Library' / 'Application Support' / 'Trae CN',
        home / 'Library' / 'Application Support' / 'TRAE CN',
        home / '.trae-cn',
        home / '.codex',
        home / '.pi',
        home / '.trae',
        home / '.traecli',
        home / '.gemini',
    )
    opened = parse_lsof_paths(raw_out)
    for raw_path in opened:
        fd_real = Path(os.path.realpath(raw_path))
        for bad in forbidden_roots:
            try:
                fd_real.relative_to(bad)
                raise IsolationViolation(f'smoke process opened forbidden FD under {bad.name}')
            except ValueError:
                continue
    return {'checked': True, 'fd_count': len(opened), 'violations': 0}


def assert_no_pycache(root: Path) -> None:
    for entry in root.rglob('*'):
        if entry.name == '__pycache__' or entry.suffix in ('.pyc', '.pyo'):
            raise IsolationViolation(f'unexpected python bytecode artifact written: {entry.name}')


def _stage_runtime_python(repo_root: Path, dest_runtime_dir: Path) -> Path:
    """Copy engine runtime resources into isolated runtime-<hash> matching RuntimeAssets."""
    src_engine = repo_root / 'engine'
    dst_engine = dest_runtime_dir / 'engine'
    dst_engine.mkdir(mode=0o700, parents=True, exist_ok=True)
    for manifest in src_engine.glob('*.json'):
        if manifest.is_file() and not manifest.is_symlink():
            shutil.copy2(manifest, dst_engine / manifest.name)
    for subdir in ('python', 'prompts', 'patches'):
        src_sub = src_engine / subdir
        if src_sub.is_dir() and not src_sub.is_symlink():
            shutil.copytree(
                src_sub,
                dst_engine / subdir,
                ignore=shutil.ignore_patterns('__pycache__', '*.pyc', '*.pyo', 'test_*.py'),
                dirs_exist_ok=True,
            )
    return dst_engine / 'python' / 'project_control_server.py'


def _build_active_beta2_trae_fixture(
    *,
    root: Path,
    store_root: Path,
    trae_profile: Path,
    python_exe: Path,
    control_origin: str,
) -> tuple[str, dict, dict, dict]:
    """Build an authentic active beta.2 TRAE CN hooks + registry state inside $TMPDIR."""
    trae_profile.mkdir(mode=0o700, parents=True, exist_ok=True)
    trae_id = 'trae_ide-' + hashlib.sha256(str(trae_profile.absolute()).encode('utf-8')).hexdigest()[:24]
    plugin_dir = store_root / 'managed-marketplaces' / trae_id / ('a' * 20) / 'plugins' / 'cognee'
    script_bytes = b'# Synthetic smoke TRAE hook\n'
    script_path = plugin_dir / 'scripts' / 'trae_ide_hook.py'
    _write_private_bytes(script_path, script_bytes)
    manifest_bytes = json.dumps({'files': {'scripts/trae_ide_hook.py': hashlib.sha256(script_bytes).hexdigest()}}).encode('utf-8')
    _write_private_bytes(plugin_dir / '.memlane-project-access.json', manifest_bytes)
    env_file = store_root / 'trae-agent.env'
    _write_private_bytes(env_file, b'SMOKE=1\n')
    ctrl_token_file = store_root / 'trae-agent.token'
    _write_private_bytes(ctrl_token_file, (uuid.uuid4().hex + uuid.uuid4().hex).encode('utf-8'))
    agent_api_key = 'agent-key-' + uuid.uuid4().hex + uuid.uuid4().hex
    agent_key_file = store_root / 'trae-agent-official.key'
    _write_private_bytes(agent_key_file, (agent_api_key + '\n').encode('utf-8'))
    agent_principal_id = '33333333-3333-4333-8333-333333333333'

    installation = {
        'plugin_dir': str(plugin_dir),
        'home': str(root / 'home'),
        'env_file': str(env_file),
        'manifest_sha256': hashlib.sha256(manifest_bytes).hexdigest(),
    }
    _write_private_json(plugin_dir / '.memlane-installation.json', {
        'instance_id': trae_id,
        'host_kind': 'trae_ide',
        'env_file': str(env_file),
        'runtime_profile': str(trae_profile),
        'hook_origin': control_origin,
        'hook_token_file': str(ctrl_token_file),
    })

    beta2_events = ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd')
    owned_hooks = {
        event: {'hooks': [{'type': 'command',
                           'command': shlex.join([str(python_exe), '-B', str(script_path), event]),
                           'timeout': 25}]}
        for event in beta2_events
    }
    tp_stop = {'matcher': 'smoke-stop', 'hooks': [{'type': 'command', 'command': '/usr/bin/true'}]}
    tp_pre = {'matcher': 'smoke-pre', 'hooks': [{'type': 'command', 'command': '/usr/bin/true'}]}
    hooks_doc = {
        'version': 1,
        'hooks': {
            **{ev: [owned_hooks[ev]] for ev in beta2_events},
            'Stop': [owned_hooks['Stop'], tp_stop],
            'PreToolUse': [tp_pre],
        },
    }
    hooks_raw = (json.dumps(hooks_doc, indent=2, ensure_ascii=False) + '\n').encode('utf-8')
    hooks_path = trae_profile / 'hooks.json'
    _write_private_bytes(hooks_path, hooks_raw)
    hooks_sha = hashlib.sha256(hooks_raw).hexdigest()

    agent_entry = {
        'name': 'TRAE CN',
        'kind': 'trae_ide',
        'principal_id': agent_principal_id,
        'key_file': str(agent_key_file),
        'default_access': 'none',
        'installation': installation,
        'control_token_file': str(ctrl_token_file),
    }
    connection_entry = {
        'status': 'configured_pending_new_session',
        'activated': True,
        'trae_ide_hooks': owned_hooks,
        'trae_ide_next_hooks': owned_hooks,
        'trae_ide_proposal_sha256': hooks_sha,
    }
    expected_third_party = {'Stop': [tp_stop], 'PreToolUse': [tp_pre]}
    return trae_id, agent_entry, connection_entry, expected_third_party, (agent_api_key, agent_principal_id)


def _http_get_json(url: str, token: str, timeout: float = 5.0) -> tuple[int, dict]:
    req = urllib.request.Request(url, headers={'Authorization': f'Bearer {token}'})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(req, timeout=timeout) as resp:
        return int(resp.status), json.loads(resp.read().decode('utf-8'))


def _wait_control_ready(control_url: str, mgmt_token: str, deadline: float, stderr_path: Path | None = None) -> None:
    while time.monotonic() < deadline:
        try:
            status, payload = _http_get_json(control_url + '/v1/projects', mgmt_token, timeout=1.0)
            if status == 200 and 'revision' in payload:
                return
        except (urllib.error.URLError, OSError, ValueError):
            pass
        time.sleep(0.05)
    detail = ''
    if stderr_path is not None and stderr_path.is_file():
        detail = ': ' + stderr_path.read_text(encoding='utf-8', errors='replace')[-400:].strip()
    raise ContractFailure('isolated project_control_server did not become ready before deadline' + detail)


def _kill_process_group(proc: subprocess.Popen | None) -> None:
    if proc is None:
        return
    if proc.poll() is None:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=2)


def run_local_contract_smoke(
    executable: Path,
    *,
    expected_version: str = '',
    python_exe: str = '',
    timeout: float = DEFAULT_TOTAL_TIMEOUT_S,
    repo_root: Path = ROOT,
) -> tuple[int, dict]:
    """Run Level 0 local no-mock contract smoke (C1-C6) in an isolated $TMPDIR."""
    started = time.monotonic()
    deadline = started + timeout
    exe = Path(executable).expanduser().absolute()
    if not exe.is_file() or not os.access(exe, os.X_OK):
        return EXIT_ENV_UNAVAILABLE, {
            'schema_version': 1,
            'status': 'env_unavailable',
            'returncode': EXIT_ENV_UNAVAILABLE,
            'error': 'executable missing or not executable',
            'cases': [],
        }

    try:
        py312 = resolve_smoke_python(python_exe, repo_root=repo_root)
    except EnvironmentUnavailable as exc:
        return EXIT_ENV_UNAVAILABLE, {
            'schema_version': 1,
            'status': 'env_unavailable',
            'returncode': EXIT_ENV_UNAVAILABLE,
            'error': str(exc),
            'cases': [],
        }

    exe_sha = utm_guard.sha256_file(exe)
    components_meta = json.loads((repo_root / 'engine' / 'coding-memory-components.json').read_text(encoding='utf-8'))
    service_commit = components_meta['service']['commit']

    raw_tmp = tempfile.mkdtemp(prefix='memlane-contract-smoke-')
    root = Path(raw_tmp).resolve()
    os.chmod(root, 0o700)

    user = os.environ.get('USER', '')
    base_privacy_markers = tuple(m for m in (f'/Users/{user}' if user else '', '.local/') if m)
    strict_tmp_privacy_markers = tuple(m for m in (raw_tmp, str(root), *base_privacy_markers) if m)
    cases: list[dict] = []
    isolation_guard: dict = {'config_isolated': False, 'env_clean': False, 'lsof': {}, 'pycache_clean': False}
    app_version = ''
    bundled_runtime_hash = ''

    stub: _OfficialStubServer | None = None
    owner_proc: subprocess.Popen | None = None
    worker_proc: subprocess.Popen | None = None

    def run_bin(args: list[str], *, extra_env: dict[str, str] | None = None,
                strict_tmp_privacy: bool = False, cli_timeout: float = DEFAULT_CLI_TIMEOUT_S) -> tuple[int, dict]:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise ContractFailure('global contract smoke deadline exceeded')
        env = _clean_smoke_env(root, extra=extra_env)
        assert_env_isolated(env, root)
        isolation_guard['env_clean'] = True
        try:
            proc = subprocess.run(
                [str(exe), *args],
                cwd=str(root / 'tmp'),
                env=env,
                capture_output=True,
                encoding='utf-8',
                timeout=min(cli_timeout, remaining),
                check=False,
            )
        except subprocess.TimeoutExpired:
            raise ContractFailure(f'CLI timed out for args={args[:2]}') from None
        except (OSError, UnicodeError) as exc:
            raise ContractFailure(f'CLI execution failed: {exc}') from None
        _require_contract(proc.stderr == '', f'CLI stderr non-empty for {args[:2]}')
        payload = utm_guard.json_object(proc.stdout, error_cls=ContractFailure)
        _require_contract(type(payload.get('schema_version')) is int and payload['schema_version'] == 1,
                          'schema_version must be 1')
        _require_contract(type(payload.get('exit_code')) is int and payload['exit_code'] == proc.returncode,
                          'JSON exit_code differs from process exit code')
        markers = strict_tmp_privacy_markers if strict_tmp_privacy else base_privacy_markers
        utm_guard.check_output_privacy(proc.stdout, payload, markers, error_cls=IsolationViolation)
        return proc.returncode, payload

    def record_case(case_id: str, fn):
        t0 = time.monotonic()
        entry = {'id': case_id, 'status': 'failed', 'elapsed_s': 0.0}
        cases.append(entry)
        try:
            detail = fn()
        except Exception as exc:
            entry['elapsed_s'] = round(time.monotonic() - t0, 3)
            entry['error'] = str(exc)
            raise
        entry['elapsed_s'] = round(time.monotonic() - t0, 3)
        entry['status'] = 'passed'
        if detail:
            entry['detail'] = detail

    try:
        # C1: Cold baseline, CLI schema, secret input guard, and external read-only protection.
        def case_c1():
            nonlocal app_version, bundled_runtime_hash
            code, ver = run_bin(['version', '--json'])
            _require_contract(code == 0 and isinstance(ver.get('capabilities'), dict), 'version --json failed')
            app_meta = ver.get('app') or {}
            app_version = str(app_meta.get('version') or '')
            bundled_runtime_hash = str(app_meta.get('bundled_runtime_hash') or '')
            _require_contract(len(bundled_runtime_hash) == 16, 'missing 16-hex bundled_runtime_hash')
            if expected_version:
                _require_contract(app_version == expected_version, f'version {app_version!r} != {expected_version!r}')

            c1_cfg = root / 'c1' / 'runtime.json'
            c1_comp = root / 'c1' / 'components'
            c1_cfg.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            # Default-HOME cold doctor must not disclose even the temporary HOME/root path.
            code, default_doc = run_bin(['doctor', '--json'], strict_tmp_privacy=True)
            _require_contract(code == 10 and default_doc.get('status') == 'action_required',
                              'default-HOME cold doctor expected exit 10')
            code, doc = run_bin(['doctor', '--config', str(c1_cfg), '--component-root', str(c1_comp), '--json'])
            _require_contract(code == 10 and doc.get('status') == 'action_required', 'cold doctor expected exit 10')

            code, sec_arg = run_bin(['doctor', '--api-key', 'sk-smoke-secret', '--json'])
            _require_contract(
                code == 50 and any(b.get('code') == 'secret_input_forbidden' for b in sec_arg.get('blockers', [])),
                'CLI must reject secret CLI argument with exit 50',
            )
            code, sec_env = run_bin(['doctor', '--json'], extra_env={'MEMLANE_TOKEN': 'smoke-forbidden'})
            _require_contract(
                code == 50 and any(b.get('code') == 'secret_input_forbidden' for b in sec_env.get('blockers', [])),
                'CLI must reject secret env var with exit 50',
            )

            ext_cfg = root / 'c1' / 'external-runtime.json'
            _write_private_json(ext_cfg, {'mode': 'daily', 'official_data': str(root / 'c1' / 'ext-store')})
            before_sha = utm_guard.sha256_file(ext_cfg)
            code, ro = run_bin([
                'install', '--apply', '--plan-digest', 'a' * 64,
                '--config', str(ext_cfg), '--component-root', str(c1_comp), '--json',
            ])
            _require_contract(
                code == 50 and any(b.get('code') == 'non_first_install_config_read_only' for b in ro.get('blockers', [])),
                'external runtime config must be read-only on install --apply',
            )
            _require_contract(utm_guard.sha256_file(ext_cfg) == before_sha, 'external runtime config was modified')
            return {'version': app_version, 'bundled_runtime_hash': bundled_runtime_hash}

        record_case('C1_cold_baseline_and_security', case_c1)

        # C2: install --plan, wrong digest rejection, active starting_runtime --wait 1 -> exit 20, non-looping owner_busy.
        def case_c2():
            nonlocal worker_proc
            c2_cfg = root / 'c2' / 'runtime.json'
            c2_comp = root / 'c2' / 'components'
            c2_cfg.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            scope = ['--config', str(c2_cfg), '--component-root', str(c2_comp), '--json']

            code, plan = run_bin(['install', '--plan', *scope])
            digest = (plan.get('artifacts') or {}).get('plan_digest') or ''
            _require_contract(code == 10 and len(digest) == 64, 'install --plan must return exit 10 and 64-hex digest')

            code, bad = run_bin(['install', '--apply', '--plan-digest', '0' * 64, *scope])
            _require_contract(
                code == 30 and any(b.get('code') == 'plan_digest_mismatch' for b in bad.get('blockers', [])),
                'wrong plan_digest must return exit 30 plan_digest_mismatch',
            )

            holder_script = root / 'c2' / 'worker_holder.py'
            holder_script.write_text(WORKER_LOCK_HOLDER_SCRIPT, encoding='utf-8')
            ready_flag = root / 'c2' / 'worker-ready'
            fail_flag = root / 'c2' / 'worker-fail'
            done_flag = root / 'c2' / 'worker-done'
            worker_proc = subprocess.Popen(
                [
                    str(py312), '-E', '-s', '-B', str(holder_script),
                    str(c2_comp / 'installation'), 'first-install', digest,
                    str(c2_cfg), bundled_runtime_hash,
                    str(ready_flag), str(fail_flag), str(done_flag),
                ],
                env=_clean_smoke_env(root),
                start_new_session=True,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            wait_until = time.monotonic() + 5.0
            while not ready_flag.exists() and time.monotonic() < wait_until:
                time.sleep(0.02)
            _require_contract(ready_flag.exists(), 'worker lock holder did not become ready')

            t_wait = time.monotonic()
            code, busy = run_bin(['install', '--apply', '--plan-digest', digest, '--wait', '1', *scope])
            wait_elapsed = time.monotonic() - t_wait
            comp = busy.get('components') or {}
            prog = comp.get('progress') or {}
            _require_contract(code == 20, f'active worker --wait 1 expected exit 20, got {code}')
            _require_contract(comp.get('worker_active') is True, 'expected worker_active=true')
            _require_contract(prog.get('stage') == 'starting_runtime', f'expected starting_runtime, got {prog}')
            _require_contract(busy.get('retry_after_ms') == 10000, 'expected retry_after_ms=10000')
            _require_contract(busy.get('operation_id') == 'first-install', 'expected operation_id=first-install')
            _require_contract(
                any(a.get('args', [])[:3] == ['doctor', '--operation-id', 'first-install'] for a in busy.get('next_actions', [])),
                'exit 20 must advise doctor --operation-id first-install',
            )

            code, doc_busy = run_bin(['doctor', '--operation-id', 'first-install', *scope])
            _require_contract(
                code == 20 and (doc_busy.get('components') or {}).get('worker_active') is True,
                'doctor --operation-id must report active worker with exit 20',
            )
            _require_contract(
                not any(b.get('code') == 'daemon_stopped' for b in doc_busy.get('blockers', [])),
                'active worker must not emit conflicting daemon_stopped blocker',
            )

            fail_flag.write_text('fail', encoding='utf-8')
            wait_until = time.monotonic() + 5.0
            while not done_flag.exists() and time.monotonic() < wait_until:
                time.sleep(0.02)
            _require_contract(done_flag.exists(), 'worker lock holder did not transition to owner_busy')
            worker_proc.wait(timeout=3)
            worker_proc = None

            code, doc_cont = run_bin(['doctor', '--operation-id', 'first-install', *scope])
            _require_contract(
                code == 20 and (doc_cont.get('components') or {}).get('worker_active') is False,
                'retryable owner_busy contention must return exit 20 with worker_active=false',
            )
            _require_contract(
                any(a.get('args', [])[:2] == ['install', '--plan'] for a in doc_cont.get('next_actions', [])),
                'owner_busy terminal worker must advise install --plan (non-looping)',
            )
            return {'wait_1_elapsed_s': round(wait_elapsed, 3)}

        record_case('C2_install_wait_and_contention', case_c2)

        # Prepare shared live isolated runtime + real project_control_server.py for C3-C6.
        live_runtime = root / 'live'
        live_store = live_runtime / 'library'
        live_comp = live_runtime / 'components'
        live_cfg_path = live_runtime / 'runtime.json'
        staged_runtime_dir = live_comp / 'installation' / f'runtime-{bundled_runtime_hash}'
        staged_control_server = _stage_runtime_python(repo_root, staged_runtime_dir)

        store_id = 'a' * 32
        owner_id = '11111111-1111-4111-8111-111111111111'
        owner_token = 'owner-' + uuid.uuid4().hex + uuid.uuid4().hex
        mgmt_token = 'mgmt-' + uuid.uuid4().hex + uuid.uuid4().hex
        comp_token = 'comp-' + uuid.uuid4().hex + uuid.uuid4().hex

        control_port, completion_port = _free_ports(2)
        control_url = f'http://127.0.0.1:{control_port}'
        completion_url = f'http://127.0.0.1:{completion_port}'

        _write_private_bytes(live_store / 'owner.token', (owner_token + '\n').encode('utf-8'))
        _write_private_bytes(live_store / 'management.token', (mgmt_token + '\n').encode('utf-8'))
        _write_private_bytes(live_store / 'completion.token', (comp_token + '\n').encode('utf-8'))
        _write_private_json(live_store / '.memlane-first-store.json', {
            'schema': 1,
            'store_id': store_id,
            'source_commit': service_commit,
            'model_digest': 'b' * 64,
        })
        _write_private_json(live_store / 'store-identity.json', {
            'schema': 1,
            'store_id': store_id,
            'owner_id': owner_id,
            'key_id': 'smoke-key-1',
            'source_commit': service_commit,
            'storage_initialized': True,
        })

        # Create isolated installed-but-unconfigured traecli executable and TRAE CN profile.
        home_dir = root / 'home'
        traecli_bin = home_dir / '.local' / 'bin' / 'traecli'
        traecli_bin.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        traecli_bin.write_text('#!/bin/sh\nexit 0\n', encoding='utf-8')
        traecli_bin.chmod(0o700)
        traex_profile = home_dir / '.trae'
        traex_profile.mkdir(mode=0o700, parents=True, exist_ok=True)
        trae_profile = home_dir / '.trae-cn'

        trae_id, trae_agent, trae_conn, expected_tp, (agent_api_key, agent_principal_id) = _build_active_beta2_trae_fixture(
            root=root,
            store_root=live_store,
            trae_profile=trae_profile,
            python_exe=py312,
            control_origin=control_url,
        )

        stub = _OfficialStubServer(owner_id, owner_token, agent_identities={agent_api_key: agent_principal_id})
        stub.start()

        # Pre-seed projects.json with active beta.2 TRAE connection + one ambiguous post_mutation op for C5.
        _write_private_json(live_store / 'projects.json', {
            'schema_version': 2,
            'revision': 5,
            'service_namespace': '22222222-2222-4222-8222-222222222222',
            'defaults': {'access': 'maintain', 'capture': True, 'auto_process': True},
            'projects': {},
            'agents': {trae_id: trae_agent},
            'connections': {trae_id: trae_conn},
            'sessions': {},
            'connection_operations': {
                'ui-smoke-ambiguous': {
                    'status': 'unknown',
                    'phase': 'post_mutation',
                    'instance_id': trae_id,
                    'action': 'activate',
                    'signature': 'smoke-ambiguous-sig',
                },
            },
            'connection_receipts': {},
            'enrollments': {},
        })

        absent_dir = root / 'absent'
        live_config = {
            'python': str(py312),
            'official_data': str(live_store),
            'service_commit': service_commit,
            'token_file': str(live_store / 'owner.token'),
            'service_url': stub.url,
            'completion_url': completion_url,
            'completion_token_file': str(live_store / 'completion.token'),
            'generation_file': str(live_store / 'generation.json'),
            'generation_mode': 'unconfigured',
            'model': '',
            'client_state_file': str(live_store / 'client-state.json'),
            'projects': [],
            'common_datasets': [],
            'first_install': {'schema': 1, 'store_id': store_id, 'setup': True},
            'project_control': {
                'url': control_url,
                'registry_file': str(live_store / 'projects.json'),
                'management_token_file': str(live_store / 'management.token'),
                'plugin_dir': str(live_store / 'plugin-source'),
                'agent_control_token_files': {},
            },
            'agent_hosts': {
                'traex': {'executable': str(traecli_bin), 'profile': str(traex_profile)},
                'trae_ide': {'executable': str(traecli_bin), 'profile': str(trae_profile)},
                'codex': {'executable': str(absent_dir / 'codex'), 'profile': str(home_dir / '.codex')},
                'pi': {'executable': str(absent_dir / 'pi'), 'profile': str(home_dir / '.pi' / 'agent')},
                'antigravity': {'executable': str(absent_dir / 'agy'), 'profile': str(home_dir / '.gemini' / 'config')},
                'claude_code': {'executable': str(absent_dir / 'claude'), 'profile': str(home_dir / '.claude')},
                'cursor': {'executable': str(absent_dir / 'cursor'), 'profile': str(home_dir / '.cursor')},
                'opencode': {'executable': str(absent_dir / 'opencode'), 'profile': str(home_dir / '.config' / 'opencode')},
            },
        }
        assert_config_isolated(live_config, root)
        isolation_guard['config_isolated'] = True
        _write_private_json(live_cfg_path, live_config)

        holder_path = live_runtime / 'owner_holder.py'
        holder_path.write_text(OWNER_HOLDER_SCRIPT, encoding='utf-8')
        owner_ready = live_runtime / 'owner-ready.json'
        ctrl_stderr = live_runtime / 'control-stderr.log'
        owner_env = _clean_smoke_env(root)
        assert_env_isolated(owner_env, root)
        owner_proc = subprocess.Popen(
            [
                str(py312), '-E', '-s', '-B', str(holder_path),
                str(live_cfg_path), str(live_store), str(staged_control_server),
                str(py312), str(owner_ready), str(staged_runtime_dir), bundled_runtime_hash,
                str(ctrl_stderr),
            ],
            cwd=str(staged_runtime_dir),
            env=owner_env,
            start_new_session=True,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
        )
        # Reap owner_proc asynchronously so `stop --apply` never sees a defunct zombie in /bin/ps.
        owner_reaper = threading.Thread(target=owner_proc.wait, daemon=True)
        owner_reaper.start()
        wait_until = min(deadline, time.monotonic() + 8.0)
        while not owner_ready.exists() and time.monotonic() < wait_until:
            if owner_proc.poll() is not None:
                err_tail = (owner_proc.stderr.read().decode('utf-8', errors='replace') if owner_proc.stderr else '')[-400:]
                raise ContractFailure(f'owner holder exited early ({owner_proc.returncode}): {err_tail}')
            time.sleep(0.03)
        _require_contract(owner_ready.exists(), 'owner holder did not publish owner-ready marker')
        pids_meta = json.loads(owner_ready.read_text(encoding='utf-8'))
        _wait_control_ready(control_url, mgmt_token, min(deadline, time.monotonic() + 8.0), stderr_path=ctrl_stderr)

        # Live lsof isolation check while real project_control_server.py is serving.
        isolation_guard['lsof'] = assert_lsof_isolated(
            [pids_meta['owner_pid'], pids_meta['control_pid']],
            repo_root=repo_root,
        )

        live_scope = ['--config', str(live_cfg_path), '--component-root', str(live_comp), '--json']

        # C3: Installed-but-unconfigured CLI detection across real Python /v1/generation + real Go doctor.
        def case_c3():
            status, gen_body = _http_get_json(control_url + '/v1/generation', mgmt_token)
            _require_contract(status == 200, f'/v1/generation returned HTTP {status}')
            _require_contract(
                gen_body.get('detected_processing_clis') == ['traex'],
                f'expected detected_processing_clis=["traex"], got {gen_body.get("detected_processing_clis")}',
            )
            _require_contract(
                gen_body.get('agent_bridges') == {},
                f'expected empty agent_bridges before config, got {gen_body.get("agent_bridges")}',
            )

            _, doc = run_bin(['doctor', *live_scope])
            gen = doc.get('generation') or {}
            _require_contract(
                'traex' in (gen.get('detected_unconfigured_clis') or []),
                f'doctor missing traex in detected_unconfigured_clis: {gen}',
            )
            _require_contract(
                not (gen.get('available_bridges') or []),
                f'expected empty available_bridges before config, got {gen}',
            )
            hint = str(gen.get('missing_processing_cli_hint') or '')
            _require_contract('尚未完成登录或未配置默认模型' in hint and '尚未检测到' not in hint,
                              f'doctor misreported installed-unconfigured CLI: {hint!r}')
            traex_rec = next((r for r in gen.get('recommended_processing_clis', []) if r.get('kind') == 'traex'), None)
            _require_contract(traex_rec is not None and traex_rec.get('install_command', '') == '',
                              f'installed-unconfigured traex must omit install_command: {traex_rec}')

            # Now configure traecli.toml and confirm traex promotes to available_bridges.
            (traex_profile / 'traecli.toml').write_text('model = "smoke-fixture"\n', encoding='utf-8')
            _, doc_after = run_bin(['doctor', *live_scope])
            gen_after = doc_after.get('generation') or {}
            _require_contract(
                'traex' in (gen_after.get('available_bridges') or []),
                f'configured traex did not promote to available_bridges: {gen_after}',
            )
            _require_contract(
                'traex' not in (gen_after.get('detected_unconfigured_clis') or []),
                f'configured traex remained in detected_unconfigured_clis: {gen_after}',
            )
            return {'detected_then_promoted': 'traex'}

        record_case('C3_installed_unconfigured_cli_detection', case_c3)

        # C4: TRAE hooks 6->4 migration & preserved_third_party_groups across dry-run, apply, and replay.
        def case_c4():
            hooks_path = trae_profile / 'hooks.json'
            before_sha = utm_guard.sha256_file(hooks_path)

            code, dry = run_bin(['repair', 'trae-hooks', '--instance-id', trae_id, '--dry-run', *live_scope])
            _require_contract(code == 10, f'repair trae-hooks --dry-run expected exit 10, got {code}')
            _require_contract(utm_guard.sha256_file(hooks_path) == before_sha, 'dry-run modified hooks.json')
            dry_repair = (dry.get('artifacts') or {}).get('hook_repair') or {}
            reviewed_sha = (dry.get('artifacts') or {}).get('reviewed_sha') or ''
            _require_contract(len(reviewed_sha) == 64, 'dry-run missing 64-hex reviewed_sha')
            _require_contract(
                dry_repair.get('preserved_third_party_groups') == 2,
                f'dry-run expected preserved_third_party_groups==2, got {dry_repair}',
            )

            code, bad_sha = run_bin([
                'repair', 'trae-hooks', '--instance-id', trae_id, '--apply', '--reviewed-sha', '0' * 64, *live_scope,
            ])
            _require_contract(code == 30, f'wrong reviewed_sha expected exit 30, got {code}')
            _require_contract(utm_guard.sha256_file(hooks_path) == before_sha, 'wrong reviewed_sha modified hooks.json')

            code, applied = run_bin([
                'repair', 'trae-hooks', '--instance-id', trae_id, '--apply', '--reviewed-sha', reviewed_sha, *live_scope,
            ])
            _require_contract(code == 0, f'repair trae-hooks --apply expected exit 0, got {code}')
            app_repair = (applied.get('artifacts') or {}).get('hook_repair') or {}
            _require_contract(app_repair.get('repaired') is True, f'apply missing repaired=true: {app_repair}')
            _require_contract(
                app_repair.get('preserved_third_party_groups') == 2,
                f'apply lost preserved_third_party_groups (beta.3 regression): {app_repair}',
            )
            try:
                assert_trae_hooks_4_event_schema(hooks_path, expected_tp)
            except VerificationError as exc:
                raise ContractFailure(str(exc)) from None
            backups = list(trae_profile.glob('hooks.json.memlane-repair-backup-*'))
            _require_contract(bool(backups), 'repair did not write hooks backup file')

            op_id = applied.get('operation_id') or ''
            _require_contract(bool(op_id), 'repair apply missing operation_id')
            _, replay = run_bin(['doctor', '--operation-id', op_id, *live_scope])
            rep_repair = (replay.get('artifacts') or {}).get('hook_repair') or {}
            _require_contract(
                rep_repair.get('preserved_third_party_groups') == 2,
                f'doctor --operation-id replay lost preserved_third_party_groups: {rep_repair}',
            )
            return {'preserved_third_party_groups': 2, 'backups': len(backups)}

        record_case('C4_trae_hooks_repair_counts', case_c4)

        # C5: pending-ops safe unlock vs ambiguous retention + connection receipt exit 11 gate.
        def case_c5():
            ops_dir = live_store / 'generation.json.ui-operations'
            ops_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
            safe_file = ops_dir / 'ui-smoke-safe.json'
            ambig_file = ops_dir / 'ui-smoke-ambiguous.json'
            _write_private_json(safe_file, {
                'operation_id': 'ui-smoke-safe',
                'kind': 'connection_prepare',
                'profile_id': trae_id,
                'profile_revision': 6,
            })
            _write_private_json(ambig_file, {
                'operation_id': 'ui-smoke-ambiguous',
                'kind': 'connection_activate',
                'profile_id': trae_id,
                'profile_revision': 6,
            })

            code, _ = run_bin(['repair', 'pending-ops', '--operation-id', 'ui-smoke-safe', '--dry-run', *live_scope])
            _require_contract(code == 10, f'safe pending-op dry-run expected exit 10, got {code}')
            code, _ = run_bin(['repair', 'pending-ops', '--operation-id', 'ui-smoke-safe', '--apply', *live_scope])
            _require_contract(code == 0 and not safe_file.exists(), 'safe pending-op apply must exit 0 and clear file')

            code, _ = run_bin(['repair', 'pending-ops', '--operation-id', 'ui-smoke-ambiguous', '--dry-run', *live_scope])
            _require_contract(code == 40, f'ambiguous pending-op dry-run expected exit 40, got {code}')
            code, _ = run_bin(['repair', 'pending-ops', '--operation-id', 'ui-smoke-ambiguous', '--apply', *live_scope])
            _require_contract(code == 40 and ambig_file.exists(), 'ambiguous pending-op apply must exit 40 and retain file')

            code, conn = run_bin(['verify', '--stage', 'connection', '--agent', 'trae_ide', *live_scope])
            _require_contract(
                code == 11 and conn.get('status') == 'human_required' and bool(conn.get('human_steps')),
                f'verify --stage connection before real session must return exit 11, got code={code}',
            )
            return {'safe_unlocked': True, 'ambiguous_retained': True, 'connection_exit': 11}

        record_case('C5_pending_ops_and_connection_gate', case_c5)

        # C6: Graceful stop + uninstall lock-free check + zero bytecode artifacts.
        def case_c6():
            nonlocal owner_proc
            code, dry_stop = run_bin(['stop', '--dry-run', *live_scope])
            fp = (dry_stop.get('artifacts') or {}).get('owner_fingerprint') or ''
            _require_contract(code == 10 and len(fp) == 64, f'stop --dry-run expected exit 10 + 64-hex fp, got {code}')

            code, _ = run_bin(['stop', '--apply', '--owner-fingerprint', '0' * 64, *live_scope])
            _require_contract(code == 30, f'wrong owner_fingerprint stop expected exit 30, got {code}')

            code, stopped = run_bin(['stop', '--apply', '--owner-fingerprint', fp, *live_scope])
            _require_contract(
                code == 0 and (stopped.get('daemon') or {}).get('owner_locked') is False,
                f'stop --apply failed: code={code} daemon={stopped.get("daemon")}',
            )
            _require_contract(not flock_held(live_store / '.service-owner'), '.service-owner lock still held after stop')
            owner_reaper.join(timeout=5)
            owner_proc = None

            uninstall_script = repo_root / 'scripts' / 'uninstall.py'
            un = subprocess.run(
                [
                    sys.executable, '-E', '-s', '-B', str(uninstall_script), '--json',
                    '--home', str(home_dir), '--runtime-dir', str(live_runtime), '--components-dir', str(live_comp),
                ],
                env=_clean_smoke_env(root),
                capture_output=True, encoding='utf-8', timeout=15, check=False,
            )
            _require_contract(un.returncode == 0 and un.stderr == '', f'uninstall dry-run failed: {un.returncode}')
            un_payload = utm_guard.json_object(un.stdout, error_cls=ContractFailure)
            _require_contract(
                un_payload.get('locked_owners') == [] and un_payload.get('errors') == [],
                f'uninstall reported locked owners or errors: {un_payload}',
            )

            assert_no_pycache(root)
            isolation_guard['pycache_clean'] = True
            return {'uninstall_locked_owners': 0, 'pycache_clean': True}

        record_case('C6_stop_uninstall_and_hygiene', case_c6)

    except IsolationViolation as exc:
        return EXIT_ISOLATION_VIOLATION, {
            'schema_version': 1,
            'status': 'isolation_violation',
            'returncode': EXIT_ISOLATION_VIOLATION,
            'executable_sha256': exe_sha,
            'app_version': app_version,
            'bundled_runtime_hash': bundled_runtime_hash,
            'elapsed_s': round(time.monotonic() - started, 3),
            'error': str(exc),
            'cases': cases,
            'isolation_guard': isolation_guard,
        }
    except EnvironmentUnavailable as exc:
        return EXIT_ENV_UNAVAILABLE, {
            'schema_version': 1,
            'status': 'env_unavailable',
            'returncode': EXIT_ENV_UNAVAILABLE,
            'executable_sha256': exe_sha,
            'elapsed_s': round(time.monotonic() - started, 3),
            'error': str(exc),
            'cases': cases,
            'isolation_guard': isolation_guard,
        }
    except ContractFailure as exc:
        return EXIT_CONTRACT_FAILED, {
            'schema_version': 1,
            'status': 'failed',
            'returncode': EXIT_CONTRACT_FAILED,
            'executable_sha256': exe_sha,
            'app_version': app_version,
            'bundled_runtime_hash': bundled_runtime_hash,
            'elapsed_s': round(time.monotonic() - started, 3),
            'error': str(exc),
            'cases': cases,
            'isolation_guard': isolation_guard,
        }
    finally:
        _kill_process_group(worker_proc)
        _kill_process_group(owner_proc)
        if stub is not None:
            stub.stop()
        shutil.rmtree(root, ignore_errors=True)

    return EXIT_OK, {
        'schema_version': 1,
        'status': 'passed',
        'returncode': EXIT_OK,
        'executable_sha256': exe_sha,
        'app_version': app_version,
        'bundled_runtime_hash': bundled_runtime_hash,
        'elapsed_s': round(time.monotonic() - started, 3),
        'cases': cases,
        'isolation_guard': isolation_guard,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--executable', required=True, help='Path to compiled Memlane binary.')
    parser.add_argument('--expected-version', default='', help='Expected app.version in CLI output.')
    parser.add_argument('--python', default='', help='Python 3.12+ interpreter with aiohttp/tomllib/tomlkit.')
    parser.add_argument('--timeout', type=float, default=DEFAULT_TOTAL_TIMEOUT_S, help='Global smoke budget in seconds.')
    parser.add_argument('--json', action='store_true', help='Emit machine-readable JSON report.')
    args = parser.parse_args(argv)

    code, report = run_local_contract_smoke(
        Path(args.executable),
        expected_version=args.expected_version,
        python_exe=args.python,
        timeout=args.timeout,
    )
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print(f"Level 0 contract smoke [{report['status']}] (exit {code}, {report.get('elapsed_s', 0)}s)")
        for case in report.get('cases', []):
            suffix = f" — {case['error']}" if 'error' in case else ''
            print(f"  {case['status']}: {case['id']} ({case['elapsed_s']}s){suffix}")
        if report.get('error') and not report.get('cases'):
            print(f"  error: {report['error']}", file=sys.stderr)
    return code


if __name__ == '__main__':
    sys.exit(main())
