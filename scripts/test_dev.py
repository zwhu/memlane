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
    def test_owner_control_preserves_auth_checks_without_requiring_future_plugin_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary)
            token=root/'manager.token';token.write_text('m'*40);token.chmod(0o600)
            control={'url':'http://127.0.0.1:18777','registry_file':str(root/'registry.json'),
                     'management_token_file':str(token),'plugin_dir':str(root/'old-running-plugin')}
            runtime={'service_url':'http://127.0.0.1:18172'}
            with patch.object(dev,'verify_managed_plugin',side_effect=ValueError('new source differs')) as check:
                dev.validate_project_control(control,runtime,verify_plugin=False)
                check.assert_not_called()
                with self.assertRaises(ValueError):dev.validate_project_control(control,runtime)
                check.assert_called_once()
            token.chmod(0o644)
            with self.assertRaises(ValueError):dev.validate_project_control(control,runtime,verify_plugin=False)

    def test_first_setup_prepares_only_private_runtime_without_seeding_generation(self):
        import json
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            generation = root / 'generation.json'
            config = {'generation_file': str(generation), 'project_control': {'url': 'http://127.0.0.1:18998'},
                      'service_url': 'http://127.0.0.1:18999'}
            effective, path = dev.generation_setup_runtime(config, root / 'runtime.json')
            self.assertTrue(effective['generation_setup_only'])
            self.assertEqual(effective['generation_mode'], 'unconfigured')
            self.assertEqual(json.loads(path.read_text()), effective)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertFalse(generation.exists())
            self.assertNotIn('generation_setup_only', config)

    def test_provider_stack_needs_no_codex_login_and_commits_only_after_applied_digest(self):
        import json
        from generation_config import GenerationStore, runtime_generation
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            generation = root / 'generation.json'
            store = GenerationStore(generation, 'fixed', legacy_model='original')
            profile = store.put('api', {'mode': 'provider', 'provider': 'custom', 'model': 'synthetic',
                                     'endpoint': 'https://example.test/v1'}, 0, 'save', api_key='synthetic-key')
            store.state['validations']['validate'] = {'status': 'passed', 'digest': profile['digest'],
                'plain_passed': True, 'schema_passed': True, 'quality_passed': True}
            store.select_next(profile, 1, 'next')
            store.close()
            config = {'generation_file': str(generation), 'service_commit': 'fixed', 'python': 'unused-python',
                      'service_url': 'http://127.0.0.1:18999'}
            launches = []
            def launch(command):
                launches.append(command)
                effective = json.loads(Path(command[-1]).read_text())
                selected, environment, digest = runtime_generation(effective)
                self.assertEqual(selected['mode'], 'provider')
                self.assertEqual(environment['LLM_MODEL'], 'openai/synthetic')
                self.assertEqual(environment['LLM_API_KEY'], 'synthetic-key')
                Path(effective['generation_applied_file']).write_text(json.dumps({
                    'pid': 42, 'digest': digest, 'reference': effective['generation_reference']}))
                Path(effective['generation_applied_file']).chmod(0o600)
                return Mock(pid=42)
            with patch.object(dev, 'port_free'):
                effective, path, endpoint, service = dev.start_generation_stack(config, root / 'runtime.json',
                    launch, lambda: self.fail('unexpected cleanup'), health=lambda *args: None)
            self.assertEqual(len(launches), 1)
            self.assertIsNone(endpoint)
            self.assertEqual(effective['generation_mode'], 'provider')
            self.assertNotIn('synthetic-key', path.read_text())
            # The controller can now take the same lock; no live second writer.
            store = GenerationStore(generation, 'fixed')
            try:
                self.assertEqual(store.state['active']['id'], 'api')
                self.assertIsNone(store.state['next'])
            finally:
                store.close()

    def test_applied_digest_mismatch_does_not_commit_candidate_or_claim_cleanup(self):
        import json
        import sys
        from generation_config import GenerationStore
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            generation = root / 'generation.json'
            store = GenerationStore(generation, 'fixed', legacy_model='original')
            profile = store.put('api', {'mode': 'provider', 'provider': 'custom', 'model': 'synthetic',
                                     'endpoint': 'https://example.test/v1'}, 0, 'save', api_key='synthetic-key')
            store.state['validations']['v'] = {'status': 'passed', 'digest': profile['digest'],
                'plain_passed': True, 'schema_passed': True, 'quality_passed': True}
            store.select_next(profile, 1, 'next')
            store.close()
            def launch(command):
                config = json.loads(Path(command[-1]).read_text())
                Path(config['generation_applied_file']).write_text(json.dumps({'pid': 42, 'digest': 'wrong'}))
                Path(config['generation_applied_file']).chmod(0o600)
                return Mock(pid=42)
            config = {'generation_file': str(generation), 'service_commit': 'fixed', 'python': sys.executable,
                      'service_url': 'http://127.0.0.1:18999'}
            with patch.object(dev, 'port_free'):
                with self.assertRaisesRegex(Exception, 'generation_cleanup_unconfirmed'):
                    dev.start_generation_stack(config, root / 'runtime.json', launch, lambda: False, health=lambda *args: None)
            store = GenerationStore(generation, 'fixed')
            try:
                self.assertEqual(store.state['active']['id'], 'existing-codex')
                self.assertEqual(store.state['next']['id'], 'api')
            finally:
                store.close()

    def test_project_control_is_not_mistaken_for_ui_watcher(self):
        state = {'children': [10, 11, 12, 13], 'service_children': {'completion': 10, 'official': 11, 'project_control': 12}}
        self.assertEqual(dev.owner_ui_children(state), [13])
        self.assertEqual(dev.owner_ui_children({'children': [10, 11, 13]}), [13])

    def test_project_control_requires_private_independent_tokens_and_port(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            token = root / 'control.token'
            token.write_text('x' * 40)
            token.chmod(0o600)
            control = {'url': 'http://127.0.0.1:18777', 'registry_file': str(root / 'registry.json'),
                       'management_token_file': str(token), 'agent_control_token_files': {}}
            config = {'service_url': 'http://127.0.0.1:61255', 'completion_url': 'http://127.0.0.1:18173'}
            dev.validate_project_control(control, config)
            with self.assertRaises(ValueError):
                dev.validate_project_control({**control, 'url': config['service_url']}, config)
            with self.assertRaises(ValueError):
                dev.validate_project_control({**control, 'agent_control_token_files': {'a': str(token)}}, config)
            token.chmod(0o644)
            with self.assertRaises(ValueError):
                dev.validate_project_control(control, config)

    def test_project_control_readiness_is_authenticated_directory_read(self):
        from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
        import threading
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path != '/v1/projects' or self.headers.get('Authorization') != 'Bearer ' + 'x' * 40:
                    self.send_error(401)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"revision":0,"projects":[]}')
            def log_message(self, *args):
                pass
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as temporary:
                token = Path(temporary) / 'token'
                token.write_text('x' * 40)
                token.chmod(0o600)
                control = {'url': 'http://127.0.0.1:' + str(server.server_port), 'management_token_file': str(token)}
                dev.wait_project_control(control, Mock(poll=Mock(return_value=None)), timeout=2)
                with self.assertRaises(RuntimeError):
                    dev.wait_project_control(control, Mock(poll=Mock(return_value=1)), timeout=2)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(2)

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
            # The managed relocatable macOS Python locates libpython relative
            # to its executable. Copying it alone makes a broken venv fixture;
            # a normal symlink venv preserves both ABI runtime and pyvenv.cfg.
            venv.EnvBuilder(with_pip=False, symlinks=True).create(runtime)
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

    def test_stale_descendant_history_uses_one_process_snapshot(self):
        known = {pid: 'old-start' for pid in range(10000, 12000)}
        with patch.object(dev, 'process_tree', return_value={}) as scan, \
             patch.object(dev.os, 'kill') as kill:
            dev.stop_descendants(known)
        kill.assert_not_called()
        self.assertLessEqual(scan.call_count, 2, 'one full process scan per stale PID delays stop')

    def test_descendant_history_prunes_gone_and_reused_pids(self):
        known = {20: 'gone', 21: 'old-start', 22: 'same-start'}
        table = {10: (1, 10, 'root'), 21: (1, 21, 'new-start'),
                 22: (1, 22, 'same-start'), 23: (10, 10, 'new-child')}
        with patch.object(dev, 'process_tree', return_value=table):
            dev.track_descendants([10], known)
        self.assertEqual(known, {22: 'same-start', 23: 'new-child'})

    def test_descendant_cleanup_stops_real_owned_child_with_stale_history(self):
        import subprocess
        import sys
        import os
        import signal
        process = subprocess.Popen([sys.executable, '-c', 'import time;time.sleep(60)'], start_new_session=True)
        try:
            known = {pid: 'historical-exited' for pid in range(100000, 102000)}
            known[process.pid] = dev.process_tree()[process.pid][2]
            # The service parent normally reaps its child; this fixture is the
            # direct parent, so reap concurrently rather than leaving a zombie.
            import threading
            reaper = threading.Thread(target=process.wait, daemon=True)
            reaper.start()
            with patch.object(dev, 'process_tree', wraps=dev.process_tree) as scan:
                dev.stop_descendants(known)
            reaper.join(timeout=5)
            self.assertFalse(reaper.is_alive())
            self.assertFalse(dev.group_exists(process.pid))
            self.assertLess(scan.call_count, 20)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)

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

    def test_non_codex_connected_agent_bridge_starts_via_unified_startup_gate(self):
        import contextlib
        import json
        import sys
        import types
        from agent_bridge_runtime import mark_connected_bridge_selection
        from generation_config import GenerationStore, runtime_generation
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            # Create synthetic pi bridge executable, auth.json, and settings.json.
            pi_bin = root / 'fake-pi'
            pi_bin.write_text('#!/bin/sh\nexit 0\n')
            pi_bin.chmod(0o700)
            pi_home = root / 'pi-agent'
            pi_home.mkdir()
            (pi_home / 'auth.json').write_text('{"deepseek":{"access":"tok"}}')
            (pi_home / 'auth.json').chmod(0o600)
            (pi_home / 'settings.json').write_text(json.dumps({
                'defaultProvider': 'deepseek',
                'defaultModel': 'deepseek-chat',
            }))
            comp_token = root / 'completion.token'
            comp_token.write_text('t' * 40)
            comp_token.chmod(0o600)

            generation = root / 'generation.json'
            store = GenerationStore(generation, 'fixed', legacy_model='original')
            ref = store.put(
                'connected-pi',
                {'mode': 'agent_bridge', 'bridge_kind': 'pi', 'bridge_provider': 'deepseek', 'model': 'deepseek-chat'},
                0,
                'save-pi',
            )
            row = store.resolve({'id': ref['id'], 'revision': ref['revision']})
            mark_connected_bridge_selection(store.state, row)
            store.state['active'] = {'id': ref['id'], 'revision': ref['revision']}
            store.save()
            store.close()

            config = {
                'generation_file': str(generation),
                'service_commit': 'fixed',
                'python': 'unused-python',
                'service_url': 'http://127.0.0.1:18999',
                'completion_url': 'http://127.0.0.1:18998',
                'completion_token_file': str(comp_token),
                'pi_executable': str(pi_bin),
                'pi_home': str(pi_home),
            }
            launches = []

            def launch(command):
                launches.append(command)
                if 'official_service_dev.py' in command[1]:
                    effective = json.loads(Path(command[-1]).read_text())
                    _, _, digest = runtime_generation(effective)
                    Path(effective['generation_applied_file']).write_text(json.dumps({
                        'pid': 77, 'digest': digest, 'reference': effective['generation_reference'],
                    }))
                    Path(effective['generation_applied_file']).chmod(0o600)
                    return Mock(pid=77)
                return Mock(pid=66)

            # agent_bridge_runtime.read_agent_defaults imports stdlib tomllib (3.11+)
            # even though pi reads settings.json; allow running this suite under host Python 3.9.
            tomllib_guard = (
                patch.dict(sys.modules, {'tomllib': types.ModuleType('tomllib')})
                if importlib.util.find_spec('tomllib') is None
                else contextlib.nullcontext()
            )
            with patch.object(dev, 'port_free'), tomllib_guard:
                effective, _, endpoint, service = dev.start_generation_stack(
                    config, root / 'runtime.json', launch, lambda: self.fail('unexpected cleanup'),
                    health=lambda *args: None,
                )
            self.assertEqual(len(launches), 2)
            self.assertEqual(endpoint.pid, 66)
            self.assertEqual(service.pid, 77)
            self.assertEqual(effective['generation_mode'], 'agent_bridge')
            self.assertIn('--bridge-kind', launches[0])
            self.assertIn('pi', launches[0])
            self.assertIn('--bridge-provider', launches[0])
            self.assertIn('deepseek', launches[0])

    def test_owner_lock_receipt_nofollow_and_dev_plist_min_os(self):
        self.assertIn('<key>LSMinimumSystemVersion</key>\n\t<string>14.0</string>', dev.DEV_APP_PLIST)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / 'outside.txt'
            target.write_text('keep')
            store = root / 'store'
            store.mkdir()

            # acquire() must reject a symlinked .service-owner.
            (store / '.service-owner').symlink_to(target)
            with self.assertRaises(ValueError):
                dev.acquire(store)
            (store / '.service-owner').unlink()

            # write_owner_record() & read_owner_record() must reject symlinked receipts
            # and write 0600 regular files with fsync.
            runfile = store / '.memlane-dev-owner.json'
            runfile.symlink_to(target)
            with self.assertRaises(ValueError):
                dev.write_owner_record(runfile, {'pid': 123})
            with self.assertRaises(ValueError):
                dev.read_owner_record(runfile)
            runfile.unlink()

            dev.write_owner_record(runfile, {'pid': 456, 'phase': 'ready'})
            self.assertEqual(runfile.stat().st_mode & 0o777, 0o600)
            self.assertEqual(dev.read_owner_record(runfile), {'pid': 456, 'phase': 'ready'})

    def test_stop_groups_parallel_stops_stubborn_groups_within_shared_budget(self):
        import subprocess
        import sys
        import time
        stubborn = 'import signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(60)'
        p1 = subprocess.Popen([sys.executable, '-c', stubborn], start_new_session=True)
        p2 = subprocess.Popen([sys.executable, '-c', stubborn], start_new_session=True)
        try:
            started = time.monotonic()
            errors = dev.stop_groups_parallel([p1, p2], grace=0.3, kill_wait=1.0)
            elapsed = time.monotonic() - started
            self.assertEqual(errors, [])
            self.assertLess(elapsed, 2.0)
            self.assertFalse(dev.group_exists(p1.pid))
            self.assertFalse(dev.group_exists(p2.pid))
        finally:
            for proc in (p1, p2):
                if proc.poll() is None:
                    proc.kill()
                    proc.wait(timeout=5)


if __name__ == '__main__': unittest.main()
