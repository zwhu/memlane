"""Unit tests for 3-layer service monitor and /v1/runtime/monitor authentication."""
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from aiohttp.test_utils import AioHTTPTestCase, TestClient, TestServer

from project_control import Registry, private_write
from project_control_server import create_app
from service_monitor import ServiceMonitor, read_log_tail


class ServiceMonitorTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.official_data = self.root / 'official-store'
        self.official_data.mkdir()
        self.state_dir = self.root / 'daily'
        self.state_dir.mkdir()
        (self.state_dir / 'runtime-logs').mkdir()
        self.gen_file = self.state_dir / 'generation.json'
        self.gen_file.write_text('{}', encoding='utf-8')
        self.owner_pid = 4242
        self.ref = {'id': 'existing-codex', 'revision': 2}
        private_write(
            self.official_data / '.memlane-dev-owner.json',
            json.dumps({
                'pid': self.owner_pid,
                'phase': 'ready',
                'generation_mode': 'agent_bridge',
                'generation_reference': self.ref,
                'service_children': {'official': 5001, 'project_control': 5002, 'completion': 5003},
            }).encode(),
        )
        private_write(
            self.state_dir / f'.generation-applied-{self.owner_pid}.json',
            json.dumps({'pid': self.owner_pid, 'reference': self.ref, 'digest': 'abc'}).encode(),
        )
        (self.state_dir / 'runtime-logs/project_control_server.log').write_text(
            json.dumps({'event': 'request_finished', 'elapsed_ms': 142.34}) + '\n',
            encoding='utf-8',
        )
        (self.state_dir / 'runtime-logs/official_service_dev.log').write_text(
            'INFO starting official\n'
            'Authorization: Bearer super-secret-token-value\n'
            'X-Api-Key: secret-official-key\n'
            'error api_key=sk-secret-12345 failed\n',
            encoding='utf-8',
        )
        self.registry = Registry(self.state_dir / 'registry.json', object())
        self.registry.state['projects'] = {'p1': {'id': 'p1', 'name': 'memlane'}}
        self.config = {
            'official_data': str(self.official_data),
            'generation_file': str(self.gen_file),
            'service_url': 'http://127.0.0.1:61255',
        }

    def tearDown(self):
        self.registry.close()
        self.temp.cleanup()

    async def test_ready_debounce_degraded_and_recovery(self):
        probe_results = [
            (True, 200, ''),
            (False, None, 'connection_refused'),
            (False, None, 'timeout'),
            (False, 503, 'official_http_503'),
            (True, 200, ''),
        ]
        idx = {'i': 0}

        async def probe(_url):
            res = probe_results[idx['i']]
            idx['i'] += 1
            return res

        def sample(pid):
            return {'pid': pid, 'alive': True, 'rss_bytes': pid * 1024, 'cpu_percent': 1.5}

        monitor = ServiceMonitor(
            self.config,
            registry=self.registry,
            connections=SimpleNamespace(adapters={'agy': SimpleNamespace(last_hook_error=lambda: {'reason': 'x'})}),
            probe_http=probe,
            sample_process=sample,
            disk_usage=lambda _p: SimpleNamespace(free=80 * 1024 ** 3),
            clock=lambda: 1795100000.0,
        )

        ready = await monitor.snapshot()
        self.assertEqual(ready['state'], 'ready')
        self.assertEqual(ready['consecutive_failures'], 0)
        self.assertEqual(ready['processes']['official']['rss_bytes'], 5001 * 1024)
        self.assertEqual(ready['disk']['free_bytes'], 80 * 1024 ** 3)
        self.assertFalse(ready['disk']['low'])
        self.assertTrue(ready['summary']['generation_receipt_verified'])
        self.assertEqual(ready['summary']['projects_count'], 1)
        self.assertEqual(ready['summary']['recent_processing_ms'], 142.3)
        self.assertEqual(ready['summary']['hook_errors_count'], 1)
        self.assertEqual(ready['log_tail'], [])

        warn1 = await monitor.snapshot()
        self.assertEqual(warn1['state'], 'warning')
        self.assertEqual(warn1['consecutive_failures'], 1)
        self.assertEqual(warn1['log_tail'], [])

        warn2 = await monitor.snapshot()
        self.assertEqual(warn2['state'], 'warning')
        self.assertEqual(warn2['consecutive_failures'], 2)

        degraded = await monitor.snapshot()
        self.assertEqual(degraded['state'], 'degraded')
        self.assertEqual(degraded['consecutive_failures'], 3)
        joined = '\n'.join(degraded['log_tail'])
        self.assertIn('[REDACTED]', joined)
        self.assertNotIn('super-secret-token-value', joined)
        self.assertNotIn('secret-official-key', joined)
        self.assertNotIn('sk-secret-12345', joined)

        recovered = await monitor.snapshot()
        self.assertEqual(recovered['state'], 'ready')
        self.assertEqual(recovered['consecutive_failures'], 0)

    async def test_dead_official_pid_marks_exited_and_ignores_symlinks(self):
        async def probe(_url):
            return True, 200, ''

        def sample(pid):
            return {'pid': pid, 'alive': pid != 5001, 'rss_bytes': None, 'cpu_percent': None}

        monitor = ServiceMonitor(
            self.config,
            registry=self.registry,
            probe_http=probe,
            sample_process=sample,
            disk_usage=lambda _p: SimpleNamespace(free=40 * 1024 ** 3),
        )
        snap = await monitor.snapshot()
        self.assertEqual(snap['state'], 'exited')
        self.assertTrue(snap['disk']['low'])
        self.assertTrue(snap['log_tail'])

        # Symlink log and symlink owner receipt must be ignored safely.
        secret_file = self.root / 'outside.txt'
        secret_file.write_text('outside-secret\n', encoding='utf-8')
        log_path = self.state_dir / 'runtime-logs/official_service_dev.log'
        log_path.unlink()
        log_path.symlink_to(secret_file)
        self.assertEqual(read_log_tail(log_path), [])

        owner_path = self.official_data / '.memlane-dev-owner.json'
        owner_path.unlink()
        owner_path.symlink_to(secret_file)
        snap2 = await monitor.snapshot()
        self.assertIsNone(snap2['owner_pid'])
        self.assertEqual(snap2['state'], 'ready')

    async def test_runtime_monitor_endpoint_auth(self):
        async def probe(_url):
            return True, 200, ''

        monitor = ServiceMonitor(
            self.config,
            registry=self.registry,
            probe_http=probe,
            sample_process=lambda pid: {'pid': pid, 'alive': True, 'rss_bytes': 1024, 'cpu_percent': 0.1},
            disk_usage=lambda _p: SimpleNamespace(free=80 * 1024 ** 3),
        )
        manager = 'm' * 40
        agent = 'a' * 40
        app = create_app(self.registry, manager, {'agent-1': agent}, codex_config=self.config, monitor=monitor)
        async with TestClient(TestServer(app)) as client:
            unauth = await client.get('/v1/runtime/monitor')
            self.assertEqual(unauth.status, 401)
            forbidden = await client.get('/v1/runtime/monitor', headers={'Authorization': 'Bearer ' + agent})
            self.assertEqual(forbidden.status, 403)
            ok = await client.get('/v1/runtime/monitor?include_logs=1', headers={'Authorization': 'Bearer ' + manager})
            self.assertEqual(ok.status, 200)
            body = await ok.json()
            self.assertEqual(body['state'], 'ready')
            self.assertTrue(body['log_tail'])


if __name__ == '__main__':
    unittest.main()
