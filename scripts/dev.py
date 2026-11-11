#!/usr/bin/env python3
"""Run Native UI and a foreground daemon without installing a system helper."""
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

ROOT = Path(__file__).resolve().parents[1]
GO = ROOT / '.local/toolchains/go-1.27.3/go/bin/go'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Verify the real foreground IPC, then exit without opening UI')
    parser.add_argument('--daily-agents', action='store_true', help='Use existing daily Agent configuration paths; connection and collection still require explicit App actions')
    parser.add_argument('--worker-config', type=Path, help='Reuse installed worker/model binaries from a service JSON; index data always stays in the development root')
    args = parser.parse_args()
    dev = ROOT / '.local/dev'
    dev.mkdir(parents=True, exist_ok=True, mode=0o700)
    runner_lock = (dev / 'runner.lock').open('a')
    os.chmod(dev / 'runner.lock', 0o600)
    try:
        fcntl.flock(runner_lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise SystemExit('development runner already active; stop it before starting another')
    client = dev / 'client'
    client.mkdir(exist_ok=True, mode=0o700)
    data = client / 'data'
    data.mkdir(exist_ok=True, mode=0o700)
    service = dev / 'service.json'
    runtime = dev / 'runtime.json'
    worker = {}
    source = {}
    if args.worker_config:
        source = json.loads(args.worker_config.read_text())
        source = source.get('worker', source)
    elif service.exists():
        source = json.loads(service.read_text()).get('worker') or {}
    if source:
        allowed = {'Executable', 'ModelDir', 'Library', 'ExpectedVersion', 'ExpectedGeneration', 'DenyNetwork'}
        worker = {key: value for key, value in source.items() if key in allowed}
        for key in ('Executable', 'ModelDir', 'Library'):
            path = Path(worker.get(key, ''))
            if not path.is_absolute() or not path.exists():
                raise ValueError('installed worker path unavailable: ' + key)
        worker['DataDir'] = str(data / 'index')
    service.write_text(json.dumps({'root': str(data), 'client_root': str(client), 'worker': worker}))
    runtime.write_text(json.dumps({'root': str(client), 'reuse_running_service': True,
                                 'component_source': 'development-unused', 'worker_id': 'development-unused',
                                 'model_id': 'development-unused', 'service_plist': 'io.github.zwhu.memlane.service.plist'}))
    for path in (service, runtime): path.chmod(0o600)
    env = dict(os.environ, GOPATH=str(ROOT / '.local/gopath'), GOCACHE=str(ROOT / '.cache/go-build'),
               GOTOOLCHAIN='local', CGO_ENABLED='0', MEMLANE_DEV_RUNTIME_CONFIG=str(runtime),
               MEMLANE_DEV_BRIDGE=str(dev / 'memlane'))
    if args.daily_agents:
        print('Using daily Agent paths; this option does not itself install or authorize capture.', flush=True)
    else:
        env.update(CODEX_HOME=str(dev / 'codex'), PI_CODING_AGENT_DIR=str(dev / 'pi'))
        for name in ('codex', 'pi'): (dev / name).mkdir(exist_ok=True, mode=0o700)
    env['PATH'] = str(GO.parent) + ':' + env.get('PATH', '')
    processes = []
    def stop(signum, frame): raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, stop)
    try:
        for package, name in (('./cmd/memlaned', 'memlaned'), ('./cmd/memlane', 'memlane')):
            subprocess.run([str(GO), 'build', '-o', str(dev / name), package], cwd=ROOT, env=env, check=True)
        daemon = subprocess.Popen([str(dev / 'memlaned'), '--config', str(service)], cwd=ROOT, env=env, start_new_session=True)
        processes.append(daemon)
        # The socket name follows serviceipc.SocketPath; ask the real service,
        # rather than treating a file's presence as readiness.
        import hashlib
        ipc = Path('/tmp') / ('memlane-' + str(os.getuid()) + '-' + hashlib.sha256(str(data).encode()).hexdigest()[:12]) / 'service.sock'
        deadline = time.monotonic() + 20
        ready = False
        while time.monotonic() < deadline:
            if daemon.poll() is not None: raise RuntimeError('development daemon exited')
            try:
                with socket.socket(socket.AF_UNIX) as connection:
                    connection.settimeout(1)
                    connection.connect(str(ipc))
                    connection.sendall(b'{"protocol":1,"id":"dev-health","op":"health"}\n')
                    response = json.loads(connection.makefile('rb').readline())
                    ready = response.get('ok', False)
                    if worker:
                        ready = ready and response.get('result', {}).get('state') == 'ready'
                    if ready: break
            except (OSError, ValueError): pass
            time.sleep(0.1)
        if not ready: raise RuntimeError('development IPC did not become ready')
        print(('Development IPC and index ready. Data: ' if worker else 'Development IPC ready. Data: ') + str(data), flush=True)
        if not args.check:
            ui = subprocess.Popen([str(GO), 'tool', 'mygo', 'dev'], cwd=ROOT, env=env, start_new_session=True)
            processes.append(ui)
            while ui.poll() is None:
                if daemon.poll() is not None: raise RuntimeError('development daemon exited')
                time.sleep(0.25)
            if ui.returncode: raise subprocess.CalledProcessError(ui.returncode, ui.args)
    except KeyboardInterrupt:
        pass
    finally:
        for process in reversed(processes):
            # go tool is a launcher: the watcher/UI can outlive that process.
            # Each child owns a fresh group; never signal the real service or
            # another terminal's process group.
            try: os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError: pass
            try: process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                try: os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError: pass
                process.wait()


if __name__ == '__main__':
    if os.environ.get('MEMLANE_DISK_TASK') != '1':
        raise SystemExit(subprocess.call([sys.executable, str(ROOT / 'scripts/task.py'), '--', sys.executable, __file__, *sys.argv[1:]]))
    main()
