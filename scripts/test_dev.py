import importlib.util
import multiprocessing
from pathlib import Path
import tempfile
import unittest
import socket
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('daily_dev', Path(__file__).with_name('dev.py'))
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)


def try_lock(root, queue):
    try:
        lock = dev.acquire(root)
        lock.close()
        queue.put(True)
    except BlockingIOError:
        queue.put(False)


class OwnershipTests(unittest.TestCase):
    def test_symlink_and_other_process_share_store_lock(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / 'store'
            root.mkdir()
            alias = Path(temporary) / 'alias'
            alias.symlink_to(root, target_is_directory=True)
            lock = dev.acquire(root)
            ctx = multiprocessing.get_context('spawn')
            queue = ctx.Queue()
            process = ctx.Process(target=try_lock, args=(alias, queue))
            process.start()
            self.assertFalse(queue.get(timeout=10))
            process.join(10)
            self.assertEqual(process.exitcode, 0)
            lock.close()
            lock = dev.acquire(alias)
            lock.close()

    def test_config_keeps_virtual_environment_python(self):
        import json
        import subprocess
        import venv
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            runtime = root / 'venv'
            venv.EnvBuilder(with_pip=False).create(runtime)
            store = root / 'store'
            (store / 'system').mkdir(parents=True)
            (store / 'data').mkdir()
            private = root / 'secret'
            private.write_text('test-only')
            private.chmod(0o600)
            manifest = json.loads((dev.ROOT / 'engine/coding-memory-components.json').read_text())
            config = {key: str(private) for key in ('token_file', 'completion_token_file', 'service_environment_file', 'official_archive', 'codex_executable')}
            config.update(official_data=str(store), official_source=str(root), auth_home=str(root),
                          python=str(runtime / 'bin/python'), service_commit=manifest['service']['commit'],
                          service_url='http://127.0.0.1:61255', completion_url='http://127.0.0.1:18173')
            path = root / 'runtime.json'
            path.write_text(json.dumps(config))
            loaded = dev.load_config(path)
            prefix = subprocess.check_output([loaded['python'], '-c', 'import sys;print(sys.prefix)'], text=True).strip()
            self.assertEqual(Path(prefix).resolve(), runtime.resolve())

    def test_stop_does_not_claim_cleanup_from_released_lock_alone(self):
        import json
        import os
        import subprocess
        import sys
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            store = root / 'store'
            (store / 'system').mkdir(parents=True)
            (store / 'data').mkdir()
            private = root / 'secret'
            private.write_text('synthetic-only')
            private.chmod(0o600)
            manifest = json.loads((dev.ROOT / 'engine/coding-memory-components.json').read_text())
            config = {key: str(private) for key in ('token_file', 'completion_token_file', 'service_environment_file', 'official_archive', 'codex_executable')}
            config.update(official_data=str(store), official_source=str(root), auth_home=str(root), python=sys.executable,
                          service_commit=manifest['service']['commit'], service_url='http://127.0.0.1:61255', completion_url='http://127.0.0.1:18173')
            path = root / 'runtime.json'
            path.write_text(json.dumps(config))
            source = "import fcntl,json,os,signal,subprocess,sys;from pathlib import Path;store=Path(sys.argv[1]);lock=(store/'.service-owner').open('a+');fcntl.flock(lock.fileno(),fcntl.LOCK_EX);state={'pid':os.getpid(),'started':subprocess.check_output(['ps','-p',str(os.getpid()),'-o','lstart='],text=True).strip(),'config':sys.argv[2]};(store/'.memlane-dev-owner.json').write_text(json.dumps(state));print('ready',flush=True);signal.pause()"
            owner = subprocess.Popen([sys.executable, '-c', source, str(store), str(path.resolve())], stdout=subprocess.PIPE, text=True)
            try:
                self.assertEqual(owner.stdout.readline().strip(), 'ready')
                result = subprocess.run([sys.executable, str(dev.ROOT / 'scripts/dev.py'), 'stop', '--config', str(path)],
                                        env=dict(os.environ, MEMLANE_DISK_TASK='1'), text=True, capture_output=True, timeout=10)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('cleanup is unconfirmed', result.stderr)
                self.assertNotIn('Owned service groups exited', result.stdout)
            finally:
                if owner.poll() is None: owner.terminate()
                owner.wait(timeout=5)
                owner.stdout.close()

    def test_stop_reaps_child_group(self):
        import subprocess
        import sys
        process = subprocess.Popen([sys.executable, '-c', 'import time;time.sleep(60)'], start_new_session=True)
        dev.stop_group(process)
        self.assertIsNotNone(process.returncode)
        self.assertFalse(dev.group_exists(process.pid))

    def test_exited_group_eperm_race_is_confirmed_before_success(self):
        process = Mock(pid=12345)
        with patch.object(dev, 'group_exists', side_effect=[True, False, False]), \
             patch.object(dev.os, 'killpg', side_effect=PermissionError('exited group')):
            dev.stop_group(process)
        process.wait.assert_called_once_with(timeout=5)

    def test_live_group_permission_failure_is_not_hidden(self):
        process = Mock(pid=12345)
        with patch.object(dev, 'group_exists', return_value=True), \
             patch.object(dev.os, 'killpg', side_effect=PermissionError('live group')):
            with self.assertRaises(PermissionError):
                dev.stop_group(process)
        process.wait.assert_not_called()

    def test_reopen_failure_reserves_watcher_without_stopping_services(self):
        endpoint, service, watcher = Mock(), Mock(), Mock()
        processes = [endpoint, service, watcher]
        launch = Mock()
        with patch.object(dev, 'stop_group', side_effect=PermissionError('live watcher')) as stop, \
             patch('builtins.print') as output:
            current = dev.reopen_ui(watcher, processes, launch, Path('/synthetic/config.json'))
        self.assertIs(current, watcher)
        self.assertEqual(processes, [endpoint, service, watcher])
        stop.assert_called_once_with(watcher)
        launch.assert_not_called()
        self.assertIn('daily services retained', output.call_args.args[0])

    def test_port_check_rejects_an_active_listener(self):
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(('127.0.0.1', 0))
            listener.listen()
            with self.assertRaises(OSError):
                dev.port_free('http://127.0.0.1:' + str(listener.getsockname()[1]))

    def test_port_check_allows_restart_after_server_closes_connection(self):
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(('127.0.0.1', 0))
            address = listener.getsockname()
            listener.listen()
            with socket.create_connection(address) as client:
                connection, _ = listener.accept()
                with connection:
                    connection.shutdown(socket.SHUT_WR)
                    self.assertEqual(client.recv(1), b'')
        dev.port_free('http://127.0.0.1:' + str(address[1]))


if __name__ == '__main__': unittest.main()
