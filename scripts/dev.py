#!/usr/bin/env python3
"""Own the daily official Cognee service, completion endpoint and MyGo watcher."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import signal
import stat
import socket
import subprocess
import sys
import time
import urllib.request
from datetime import datetime, timezone
import tempfile
import hashlib

ROOT = Path(__file__).resolve().parents[1]
GO = ROOT / '.local/toolchains/go-1.27.3/go/bin/go'
DEFAULT_CONFIG = ROOT / '.local/daily/runtime.json'


def complete_initialization(config, config_path, now=None):
    """Commit the first successful setup once, including existing ready installs."""
    request = urllib.request.Request(config['service_url'].rstrip('/') + '/api/v1/datasets',
                                     headers={'X-Api-Key': Path(config['token_file']).read_text().strip()})
    with urllib.request.urlopen(request, timeout=10) as response:
        if response.status != 200:
            raise RuntimeError('authenticated content readiness failed')
    with urllib.request.urlopen(config['completion_url'].rstrip('/') + '/health', timeout=10) as response:
        if response.status != 200:
            raise RuntimeError('completion readiness failed')
    path = Path(config.get('client_state_file') or Path(config_path).parent / 'client-state.json')
    if not path.is_absolute():
        raise ValueError('client_state_file must be absolute')
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.with_suffix('.lock').open('a+') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        if path.exists():
            state = json.loads(path.read_text())
            if state.get('schema_version') != 1:
                raise ValueError('unsupported client state; retained unchanged')
            datetime.strptime(state['first_used_date'], '%Y-%m-%d')
            completed = datetime.fromisoformat(state['initialization_completed_at'].replace('Z', '+00:00'))
            if completed.tzinfo is None or not state.get('first_used_timezone'):
                raise ValueError('invalid initialization state; retained unchanged')
            return state
        instant = now or datetime.now().astimezone()
        if instant.tzinfo is None:
            raise ValueError('initialization time must include timezone')
        state = {'schema_version': 1,
                 'initialization_completed_at': instant.astimezone(timezone.utc).isoformat(),
                 'first_used_date': instant.date().isoformat(),
                 'first_used_timezone': str(instant.tzinfo)}
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, prefix='.client-state-', delete=False) as stream:
                temporary = Path(stream.name)
                os.chmod(temporary, 0o600)
                json.dump(state, stream, indent=2)
                stream.write('\n')
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, path)
            directory = os.open(path.parent, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
        return state


def load_config(path):
    config = json.loads(path.read_text())
    for key in ('official_data', 'official_source', 'python', 'token_file', 'completion_token_file', 'codex_executable', 'auth_home', 'official_archive', 'service_environment_file'):
        target = Path(config[key]).expanduser().resolve(strict=True)
        # Resolving a venv's Python symlink bypasses its pyvenv.cfg and dependencies.
        config[key] = str(Path(config[key]).expanduser().absolute()) if key == 'python' else str(target)
    for key in ('token_file', 'completion_token_file', 'service_environment_file'):
        if Path(config[key]).stat().st_mode & 0o077:
            raise ValueError(key + ' must be private (0600)')
    data = Path(config['official_data'])
    if not data.is_dir() or not (data / 'system').is_dir() or not (data / 'data').is_dir():
        raise ValueError('official_data must name the existing daily store; no empty database will be created')
    expected = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())['service']['commit']
    if config.get('service_commit') != expected:
        raise ValueError('explicit fixed official service commit required')
    for key in ('service_url', 'completion_url'):
        from urllib.parse import urlparse
        url = urlparse(config[key])
        if url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port or url.path not in ('', '/'):
            raise ValueError(key + ' must be an explicit loopback HTTP origin')
    control = config.get('project_control')
    if control is not None:
        validate_project_control(control, config)
    return config


def validate_project_control(control, config):
    from urllib.parse import urlparse
    if not isinstance(control, dict) or set(control) - {'url', 'registry_file', 'management_token_file', 'agent_control_token_files', 'plugin_dir'}:
        raise ValueError('invalid project_control configuration')
    parsed = urlparse(control['url'])
    if (parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port
            or parsed.path not in ('', '/') or parsed.query or parsed.fragment or parsed.username):
        raise ValueError('project_control.url must be an explicit loopback origin')
    if parsed.port in {urlparse(config[key]).port for key in ('service_url', 'completion_url')}:
        raise ValueError('project control requires its own explicit port')
    for key in ('registry_file', 'management_token_file'):
        if not Path(control[key]).is_absolute():
            raise ValueError('project control configuration paths must be absolute')
    credentials = [control['management_token_file'], *control.get('agent_control_token_files', {}).values()]
    values = []
    for credential in credentials:
        target = Path(credential)
        if not target.is_absolute() or target.is_symlink() or target.stat().st_mode & 0o077:
            raise ValueError('project control credential must be a private regular file')
        value = target.read_text().strip()
        if len(value) < 32 or value in values:
            raise ValueError('project control credentials must be independent and at least 32 characters')
        values.append(value)
    if control.get('plugin_dir'):
        verify_managed_plugin(Path(control['plugin_dir']))


def verify_managed_plugin(directory):
    if not directory.is_absolute():
        raise ValueError('managed plugin directory must be absolute')
    expected = json.loads((ROOT / 'engine/managed-project-plugin.json').read_text())
    receipt = json.loads((directory / '.memlane-project-access.json').read_text())
    if receipt != expected:
        raise ValueError('managed plugin receipt does not match fixed component input')
    for path, key in (('engine/patches/project_access.py', 'patch_sha256'),
                      ('engine/python/project_plugin_policy.py', 'extension_sha256'),
                      ('engine/patches/project-access-upstream.json', 'upstream_lock_sha256')):
        if hashlib.sha256((ROOT / path).read_bytes()).hexdigest() != expected[key]:
            raise ValueError('managed plugin patch source changed; rebuild and review the fixed input')
    files = {str(path.relative_to(directory)): hashlib.sha256(path.read_bytes()).hexdigest()
             for path in directory.rglob('*') if path.is_file() and '__pycache__' not in path.parts
             and path.suffix != '.pyc' and path.name not in ('.memlane-project-access.json', '.memlane-installation.json')}
    if files != expected['files']:
        raise ValueError('managed plugin files do not match fixed input')
    marker = directory / '.memlane-installation.json'
    if marker.exists() or marker.is_symlink():
        if marker.is_symlink() or marker.stat().st_mode & 0o077:
            raise ValueError('managed installation binding must be private')
        environment = Path(json.loads(marker.read_text())['env_file'])
        if not environment.is_absolute() or environment.is_symlink() or environment.stat().st_mode & 0o077:
            raise ValueError('managed installation environment must be private')


def wait_project_control(control, process, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('project control exited before readiness')
        request = urllib.request.Request(control['url'].rstrip('/') + '/v1/projects',
                                         headers={'Authorization': 'Bearer ' + Path(control['management_token_file']).read_text().strip()})
        try:
            with urllib.request.urlopen(request, timeout=1) as response:
                if response.status == 200 and isinstance(json.load(response).get('revision'), int):
                    return
        except Exception:
            pass
        time.sleep(.2)
    raise RuntimeError('project control readiness timed out')


def owner_ui_children(state):
    # Old records had exactly two services; new records explicitly name service
    # roles so the project control process is never mistaken for a UI watcher.
    if 'service_children' in state:
        services = set(state['service_children'].values())
        return [pid for pid in state.get('children', []) if pid not in services]
    return state.get('children', [])[2:]


def launch_owned(command, *, cwd=ROOT, env=None, log_file=None):
    if log_file is None:
        raise ValueError('owned process requires an independent log file')
    log_file = Path(log_file)
    log_file.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(log_file, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise ValueError('owned process log must be a regular file')
        os.fchmod(fd, 0o600)
        return subprocess.Popen(command, cwd=cwd, env=env, start_new_session=True,
                                stdout=fd, stderr=subprocess.STDOUT)
    finally:
        os.close(fd)


def trim_service_log(path, limit=20 * 1024 * 1024):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('service log cannot be a symlink')
    if not path.exists() or path.stat().st_size <= limit:
        return
    # Keep a bounded tail and truncate the same live inode. Child processes
    # retain an O_APPEND regular-file fd, never a pipe or replaced log inode.
    with path.open('rb') as source:
        source.seek(-limit, os.SEEK_END)
        tail = source.read(limit)
    fd = os.open(str(path) + '.previous', os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as previous:
        previous.write(tail)
    fd = os.open(path, os.O_WRONLY | os.O_NOFOLLOW)
    try:
        os.ftruncate(fd, 0)
    finally:
        os.close(fd)


def report_notice(message):
    # A terminal observer is optional; losing it must never stop owned services.
    try:
        print(message, flush=True)
    except BrokenPipeError:
        pass


def acquire(data):
    # Canonical store ownership is shared across checkouts and symlink aliases.
    lock = (Path(data).resolve(strict=True) / '.service-owner').open('a+')
    os.chmod(lock.name, 0o600)
    fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    return lock


def group_exists(pid):
    try:
        os.killpg(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # macOS may deny killpg(0) during exit; inspect the kernel process table.
        rows = subprocess.check_output(['ps', '-axo', 'pgid='], text=True)
        return any(row.strip() == str(pid) for row in rows.splitlines())


def stop_group(process):
    for sig, seconds in ((signal.SIGTERM, 15), (signal.SIGKILL, 5)):
        process.poll()
        if not group_exists(process.pid):
            process.wait(timeout=5)
            return
        try: os.killpg(process.pid, sig)
        except ProcessLookupError: pass
        except PermissionError:
            # macOS can return EPERM when the child exits between the check
            # and signal. Confirm absence; a live denied group still fails.
            process.poll()
            if group_exists(process.pid):
                raise
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            process.poll()
            if not group_exists(process.pid):
                process.wait(timeout=5)
                return
            time.sleep(.1)
    raise RuntimeError('owned process group exit unconfirmed: ' + str(process.pid))


def reopen_ui(current, processes, launch, config_path):
    try:
        if current is not None:
            stop_group(current)
            processes.remove(current)
            current = None
        return launch([str(GO), 'tool', 'mygo', 'dev'], env=ui_env(config_path))
    except Exception as error:
        # A window-only failure must not tear down the daily data services.
        # Keep the old watcher reserved if its exit could not be confirmed.
        report_notice('UI reopen failed; daily services retained: ' + str(error))
        return current


def process_tree():
    rows = subprocess.check_output(['ps', '-axo', 'pid=,ppid=,pgid=,lstart='], text=True)
    result = {}
    for row in rows.splitlines():
        fields = row.split(None, 3)
        if len(fields) == 4:
            result[int(fields[0])] = (int(fields[1]), int(fields[2]), fields[3])
    return result


def track_descendants(roots, known):
    table = process_tree()
    parents = set(roots)
    while True:
        children = {pid for pid, value in table.items() if value[0] in parents}
        new = children - parents
        if not new: break
        parents.update(new)
    for pid in parents - set(roots):
        if pid in table: known[pid] = table[pid][2]


def stop_descendants(known):
    # Codex owns request sessions distinct from the endpoint group. Only signal
    # previously observed descendants whose kernel start identity still matches.
    for sig in (signal.SIGTERM, signal.SIGKILL):
        for pid, started in known.items():
            current = process_tree().get(pid)
            if current is not None and current[2] == started:
                try: os.kill(pid, sig)
                except ProcessLookupError: pass
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            table = process_tree()
            if not any(pid in table and table[pid][2] == started for pid, started in known.items()): return
            time.sleep(.1)
    raise RuntimeError('owned descendant exit remains unconfirmed')


def wait_http(url, process, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None: raise RuntimeError('owned service exited before readiness')
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status == 200: return
        except Exception: pass
        time.sleep(.2)
    raise RuntimeError('service readiness timed out: ' + url)


def port_free(url):
    from urllib.parse import urlparse
    parsed = urlparse(url)
    with socket.socket() as sock:
        # Match the servers' bind semantics: TIME_WAIT is restartable, an
        # existing listener on the same address is still a conflict.
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(('127.0.0.1', parsed.port))


def ui_env(config_path):
    from mygo_sdk import with_patched_sdk
    env = dict(os.environ, GOPATH=str(ROOT / '.local/gopath'), GOCACHE=str(ROOT / '.cache/go-build'),
               GOTOOLCHAIN='local', CGO_ENABLED='0', MEMLANE_DEV_RUNTIME_CONFIG=str(config_path))
    env['PATH'] = str(GO.parent) + ':' + env.get('PATH', '')
    return with_patched_sdk(env)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', nargs='?', choices=('start', 'stop', 'open', 'status'), default='start')
    parser.add_argument('--config', type=Path, default=DEFAULT_CONFIG)
    parser.add_argument('--no-ui', action='store_true')
    parser.add_argument('--watch-ui', action='store_true', help='open: foreground UI watcher attached to the verified existing service')
    args = parser.parse_args()
    if args.watch_ui and args.action != 'open':
        parser.error('--watch-ui is only valid with open')
    path = args.config.expanduser().resolve(strict=True)
    config = load_config(path)
    data = Path(config['official_data'])
    runfile = data / '.memlane-dev-owner.json'
    if args.action in ('stop', 'status', 'open'):
        state = json.loads(runfile.read_text())
        # Verify the live owner via a locked store and kernel PID, not a stale file.
        try:
            lock = acquire(data)
        except BlockingIOError:
            lock = None
        if lock is not None:
            lock.close()
            raise RuntimeError('no active launcher owns this store')
        pid = state['pid']
        os.kill(pid, 0)
        actual = subprocess.check_output(['ps', '-p', str(pid), '-o', 'lstart='], text=True).strip()
        if actual != state['started'] or state['config'] != str(path):
            raise RuntimeError('owner identity mismatch; do not signal an unknown process')
        if args.action == 'status':
            print(json.dumps(state, indent=2)); return
        if args.action == 'open':
            complete_initialization(config, path)
            if args.watch_ui:
                # An older detached launcher may have lost its output reader.
                # Never attach a second watcher while its owned watcher is live.
                for child in owner_ui_children(state):
                    status = subprocess.run(['ps', '-p', str(child), '-o', 'stat='], capture_output=True, text=True)
                    if status.returncode == 0 and status.stdout.strip() and not status.stdout.strip().startswith('Z'):
                        raise RuntimeError('existing UI watcher is live; use ordinary open')
                with path.with_name('.ui-watch.lock').open('a+') as ui_lock:
                    fcntl.flock(ui_lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    def stop_ui(_signal, _frame):
                        raise KeyboardInterrupt
                    signal.signal(signal.SIGTERM, stop_ui)
                    watcher = launch_owned([str(GO), 'tool', 'mygo', 'dev'], cwd=ROOT,
                                           env=ui_env(path), log_file=path.parent / 'runtime-logs/ui-attached.log')
                    try:
                        while watcher.poll() is None:
                            trim_service_log(path.parent / 'runtime-logs/ui-attached.log')
                            time.sleep(.25)
                    except KeyboardInterrupt:
                        pass
                    finally:
                        stop_group(watcher)
                return
            with path.with_name('.ui-watch.lock').open('a+') as ui_lock:
                try:
                    fcntl.flock(ui_lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    print('Foreground UI watcher already attached; use the current window.')
                    return
        os.kill(pid, signal.SIGTERM if args.action == 'stop' else signal.SIGUSR1)
        if args.action == 'stop':
            deadline = time.monotonic() + 35
            while time.monotonic() < deadline:
                try:
                    lock = acquire(data)
                    lock.close()
                    if runfile.exists():
                        raise RuntimeError('launcher exited but cleanup is unconfirmed; ownership record retained')
                    print('Owned service groups exited; daily store released.'); return
                except BlockingIOError: time.sleep(.2)
            raise RuntimeError('stop not confirmed; ownership remains reserved')
        return
    lock = acquire(data)
    if runfile.exists():
        lock.close()
        raise RuntimeError('previous ownership record needs confirmed recovery before restart')
    processes = []
    descendants = {}
    ui = None
    reopen = False
    state = {'pid': os.getpid(), 'started': subprocess.check_output(['ps', '-p', str(os.getpid()), '-o', 'lstart='], text=True).strip(),
             'phase': 'starting', 'config': str(path), 'official_data': str(data),
             'service_url': config['service_url'], 'completion_url': config['completion_url'], 'children': [], 'service_children': {}}
    def save_owner():
        state['children'] = [proc.pid for proc in processes if proc.poll() is None]
        temporary = runfile.with_name(runfile.name + '.' + str(os.getpid()) + '.tmp')
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(state, stream)
        os.replace(temporary, runfile)
    def stop(_signal, _frame): raise KeyboardInterrupt
    def open_window(_signal, _frame):
        nonlocal reopen
        reopen = True
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGUSR1, open_window)
    def launch(command, env=None, cwd=ROOT):
        entry = Path(command[1]).stem if len(command)>1 and command[1].endswith('.py') else 'ui'
        log_file = path.parent / 'runtime-logs' / (entry + '.log')
        trim_service_log(log_file)
        proc = launch_owned(command, cwd=cwd, env=env, log_file=log_file)
        processes.append(proc)
        save_owner()
        return proc
    try:
        save_owner()
        port_free(config['service_url'])
        port_free(config['completion_url'])
        from urllib.parse import urlparse
        endpoint = launch([config['python'], str(ROOT / 'engine/python/codex_completion_endpoint.py'),
                           '--port', str(urlparse(config['completion_url']).port),
                           '--token-file', config['completion_token_file'], '--codex-executable', config['codex_executable'],
                           '--auth-home', config['auth_home'], '--model', config['model']])
        state['service_children']['completion'] = endpoint.pid
        wait_http(config['completion_url'] + '/health', endpoint)
        service = launch([config['python'], str(ROOT / 'engine/python/official_service_dev.py'), '--config', str(path)])
        state['service_children']['official'] = service.pid
        wait_http(config['service_url'] + '/health', service)
        control = None
        if config.get('project_control'):
            port_free(config['project_control']['url'])
            control = launch([config['python'], str(ROOT / 'engine/python/project_control_server.py'), '--config', str(path)])
            state['service_children']['project_control'] = control.pid
            state['project_control_url'] = config['project_control']['url']
            wait_project_control(config['project_control'], control)
        complete_initialization(config, path)
        state['phase'] = 'ready'
        save_owner()
        report_notice('Daily official services ready; closing UI keeps them running. Ctrl+C explicitly stops owned services.')
        reopen = not args.no_ui
        while True:
            for log_file in (path.parent / 'runtime-logs').glob('*.log'):
                trim_service_log(log_file)
            track_descendants([p.pid for p in processes], descendants)
            if endpoint.poll() is not None or service.poll() is not None:
                raise RuntimeError('official service or completion endpoint exited; no automatic restart')
            if control is not None and control.poll() is not None:
                raise RuntimeError('project control exited; no automatic restart or owner fallback')
            if reopen:
                reopen = False
                ui = reopen_ui(ui, processes, launch, path)
            time.sleep(.25)
    except KeyboardInterrupt: pass
    finally:
        errors = []
        track_descendants([p.pid for p in processes], descendants)
        for proc in reversed(processes):
            try: stop_group(proc)
            except Exception as error: errors.append(str(error))
        try: stop_descendants(descendants)
        except Exception as error: errors.append(str(error))
        if errors:
            # Preserve state; do not claim a successful shutdown.
            raise RuntimeError('; '.join(errors))
        runfile.unlink(missing_ok=True)
        lock.close()


if __name__ == '__main__':
    if os.environ.get('MEMLANE_DISK_TASK') != '1':
        raise SystemExit(subprocess.call([sys.executable, str(ROOT / 'scripts/task.py'), '--', sys.executable, __file__, *sys.argv[1:]]))
    main()
