#!/usr/bin/env python3
"""Own the daily official Cognee service, completion endpoint and MyGo watcher."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
GO = ROOT / '.local/toolchains/go-1.27.3/go/bin/go'
DEFAULT_CONFIG = ROOT / '.local/daily/runtime.json'


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
    return config


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
        print('UI reopen failed; daily services retained: ' + str(error), flush=True)
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
    env = dict(os.environ, GOPATH=str(ROOT / '.local/gopath'), GOCACHE=str(ROOT / '.cache/go-build'),
               GOTOOLCHAIN='local', CGO_ENABLED='0', MEMLANE_DEV_RUNTIME_CONFIG=str(config_path))
    env['PATH'] = str(GO.parent) + ':' + env.get('PATH', '')
    return env


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', nargs='?', choices=('start', 'stop', 'open', 'status'), default='start')
    parser.add_argument('--config', type=Path, default=DEFAULT_CONFIG)
    parser.add_argument('--no-ui', action='store_true')
    args = parser.parse_args()
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
             'service_url': config['service_url'], 'completion_url': config['completion_url'], 'children': []}
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
        proc = subprocess.Popen(command, cwd=cwd, env=env, start_new_session=True)
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
        wait_http(config['completion_url'] + '/health', endpoint)
        service = launch([config['python'], str(ROOT / 'engine/python/official_service_dev.py'), '--config', str(path)])
        wait_http(config['service_url'] + '/health', service)
        state['phase'] = 'ready'
        save_owner()
        print('Daily official services ready; closing UI keeps them running. Ctrl+C explicitly stops owned services.', flush=True)
        reopen = not args.no_ui
        while True:
            track_descendants([p.pid for p in processes], descendants)
            if endpoint.poll() is not None or service.poll() is not None:
                raise RuntimeError('official service or completion endpoint exited; no automatic restart')
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
