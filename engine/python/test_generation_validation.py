import asyncio
from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import socket
import sys
import tempfile
import unittest
from unittest.mock import AsyncMock, patch

from aiohttp.test_utils import TestClient, TestServer

from generation_config import GenerationStore
from generation_validation import GenerationValidation, group_exists
from project_control import Registry
from project_control_server import create_app
from project_policy import PolicyError


class ProcessGroupTests(unittest.TestCase):
    def test_permission_denied_checks_kernel_group_table_before_claiming_exit(self):
        with patch('process_groups.os.killpg', side_effect=PermissionError), patch(
                'process_groups.subprocess.check_output', return_value='  123\n  456\n'):
            self.assertTrue(group_exists(123))
            self.assertFalse(group_exists(789))

    def test_unreadable_group_table_cannot_confirm_cleanup(self):
        with patch('process_groups.os.killpg', side_effect=PermissionError), patch(
                'process_groups.subprocess.check_output', side_effect=OSError):
            with self.assertRaises(OSError):
                group_exists(123)


class ValidationTests(unittest.IsolatedAsyncioTestCase):
    async def test_worker_stderr_metrics_survive_and_arbitrary_sdk_errors_are_dropped(self):
        row = {'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
               'request_id': 'a' * 32, 'at': '2026-11-16T00:00:00+00:00', 'elapsed_ms': 12,
               'durations_ms': {'dispatch_to_headers': 11}, 'outcome': 'succeeded',
               'backend_kind': 'custom', 'status': 200}
        self.worker('import json,sys\njson.load(sys.stdin)\n'
                    'print("synthetic-secret"*20000,file=sys.stderr)\n'
                    'print(' + repr(json.dumps(row)) + ',file=sys.stderr)\n'
                    'print(json.dumps({"status":"passed","plain_passed":True,"schema_passed":True,"quality_passed":True,"structured_mode":"unknown"}))\n')
        output = io.StringIO()
        with patch('generation_validation.ROOT', self.root), redirect_stderr(output):
            self.validator.start(self.profile, 1, 'metrics')
            await self.validator.task
        self.assertEqual(self.validator.get('metrics')['status'], 'passed')
        events = [json.loads(line) for line in output.getvalue().splitlines() if line.startswith('{')]
        child = next(row for row in events if row['component'] == 'generation_http')
        owner = next(row for row in events if row['component'] == 'generation_validation')
        self.assertEqual(child['validation_request_id'], owner['request_id'])
        self.assertIn('probe', owner['durations_ms'])
        self.assertIn('cleanup', owner['durations_ms'])
        self.assertNotIn('synthetic-secret', output.getvalue())

    async def test_worker_exit_uses_only_allowlisted_trace_diagnostics(self):
        cases = (
            ('generation_http', 'ConnectTimeout', None, 'provider_timeout', 'ConnectTimeout', None),
            ('generation_http', 'ConnectError', None, 'provider_connection_unconfirmed', 'ConnectError', None),
            ('generation_http', 'failed', 502, 'provider_unavailable', None, None),
            ('pi_bridge', 'pi_timeout', None, 'provider_timeout', None, 'pi_timeout'),
            ('pi_bridge', 'pi_model_failed', None, 'pi_model_failed', None, 'pi_model_failed'),
            ('generation_http', 'secret_provider_response', None, 'probe_process_failed', None, None),
        )
        for index, (component, outcome, status, code, network, bridge_code) in enumerate(cases):
            with self.subTest(component=component, outcome=outcome, status=status):
                event = {'schema_version': 1, 'event': 'request_finished', 'component': component,
                         'request_id': 'a' * 32, 'at': '2026-11-16T00:00:00+00:00', 'elapsed_ms': 12,
                         'durations_ms': {}, 'outcome': outcome, 'status': status,
                         'bridge_error_code': 'secret_provider_response'}
                self.worker('import json,sys\njson.load(sys.stdin)\n'
                            'print("test-secret https://user:passwd@example.test",file=sys.stderr)\n'
                            'print(' + repr(json.dumps(event)) + ',file=sys.stderr)\nsys.exit(7)\n')
                output = io.StringIO()
                operation = f'exit-{index}'
                with patch('generation_validation.ROOT', self.root), redirect_stderr(output):
                    self.validator.start(self.profile, self.store.state['revision'], operation)
                    await self.validator.task
                receipt = self.validator.get(operation)
                self.assertEqual(receipt['status'], 'result_unknown')
                self.assertEqual(receipt['failed_phase'], 'probe')
                self.assertEqual(receipt['diagnostic_stage'], 'probe')
                self.assertEqual(receipt['error_code'], code)
                self.assertEqual(receipt['diagnostic_code'], network or bridge_code or code)
                self.assertEqual(receipt.get('network_error'), network)
                self.assertEqual(receipt.get('http_status'), status)
                self.assertEqual(receipt.get('bridge_error_code'), bridge_code)
                self.assertEqual(receipt['process_exit_code'], 7)
                self.assertGreater(receipt['elapsed_ms'], 0)
                for secret in ('test-secret', 'passwd', 'secret_provider_response'):
                    self.assertNotIn(secret, json.dumps(receipt) + output.getvalue())

    async def test_bridge_start_preserves_dns_and_policy_classifications(self):
        profile = self.store.put('pi', {'mode': 'agent_bridge', 'bridge_kind': 'pi', 'bridge_provider': 'deepseek', 'model': 'synthetic'},
                                 self.store.state['revision'], 'save-pi')
        runtime = {'kind': 'pi', 'home': self.root}
        for name, exc, status, code, network in (
                ('dns', socket.gaierror('test-secret'), 'result_unknown', 'dns_resolution_failed', 'gaierror'),
                ('policy', PolicyError('pi_authentication_required'), 'failed', 'pi_authentication_required', None),
                ('unsafe', PolicyError('https://user:test-secret@example.test'), 'result_unknown', 'probe_result_unconfirmed', None)):
            with self.subTest(name=name):
                output = io.StringIO()
                with (patch('generation_bridge.check_bridge_runtime', return_value=runtime),
                      patch('agent_bridge_runtime.read_agent_defaults', return_value={}),
                      patch('generation_bridge.bridge_runtime_fingerprint', return_value='fingerprint'),
                      patch('generation_bridge.ValidationBridge') as bridge,
                      redirect_stderr(output)):
                    bridge.return_value.start = AsyncMock(side_effect=exc)
                    bridge.return_value.close = AsyncMock()
                    self.validator.start(profile, self.store.state['revision'], name)
                    queued = self.validator.get(name)
                    self.assertEqual(queued['phase'], 'queued')
                    self.assertEqual(queued['backend_kind'], 'pi')
                    self.assertGreater(queued['started_at'], 0)
                    await self.validator.task
                receipt = self.validator.get(name)
                self.assertEqual(receipt['status'], status)
                self.assertEqual(receipt['error_code'], code)
                self.assertEqual(receipt['diagnostic_code'], code)
                self.assertEqual(receipt.get('network_error'), network)
                self.assertEqual(receipt['failed_phase'], 'bridge_start')
                self.assertNotIn('test-secret', json.dumps(receipt) + output.getvalue())
                self.assertIsNone(self.validator.bridge)

    async def test_malformed_stdout_reports_specific_probe_failure(self):
        for index, (expression, expected) in enumerate((
                ('"x" * 16385', 'probe_output_too_large'),
                ('"secret line is not JSON"', 'probe_output_invalid_json'),
                (repr('{"status":"private-status"}'), 'probe_output_invalid_schema'))):
            worker = f'import sys\nsys.stdin.read()\nprint({expression}, flush=True)\n'
            if expected == 'probe_output_too_large':
                # Keep the worker alive so the manager must enforce the
                # output limit and explicitly reap its owned group.
                worker += 'import time\ntime.sleep(60)\n'
            self.worker(worker)
            operation = f'malformed-{index}'
            with patch('generation_validation.ROOT', self.root):
                self.validator.start(self.profile, self.store.state['revision'], operation)
                await self.validator.task
            receipt = self.validator.get(operation)
            self.assertEqual(receipt['status'], 'result_unknown')
            self.assertEqual(receipt['error_code'], 'probe_result_unconfirmed')
            self.assertEqual(receipt['diagnostic_code'], expected)
            self.assertEqual(receipt['failed_phase'], 'probe')
            self.assertNotIn('secret line', json.dumps(receipt))

    async def test_reported_result_keeps_its_status_and_error_code(self):
        event = {'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
                 'request_id': 'a' * 32, 'at': '2026-11-16T00:00:00+00:00', 'elapsed_ms': 12,
                 'durations_ms': {}, 'status': 403, 'outcome': 'failed'}
        self.worker('import sys\nsys.stdin.read()\n'
                    'print(' + repr(json.dumps(event)) + ',file=sys.stderr)\n'
                    'print(' + repr('{"status":"failed","error_code":"provider_bad_request"}') + ')\n')
        with patch('generation_validation.ROOT', self.root):
            self.validator.start(self.profile, self.store.state['revision'], 'reported')
            await self.validator.task
        self.assertEqual(self.validator.get('reported')['status'], 'failed')
        self.assertEqual(self.validator.get('reported')['error_code'], 'provider_bad_request')
        self.assertEqual(self.validator.get('reported')['diagnostic_code'], 'permission_denied')
        self.assertEqual(self.validator.get('reported')['http_status'], 403)

    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.store = GenerationStore(self.root / 'generation.json', 'fixed', legacy_model='old')
        self.profile = self.store.put('api', {'mode': 'provider', 'provider': 'custom', 'model': 'synthetic',
                                           'endpoint': 'https://example.test/v1'}, 0, 'save', api_key='synthetic-secret')
        self.runtime = {'python': sys.executable, 'official_source': '/unused-fixed-source', 'official_archive': '/unused-fixed-archive'}
        self.validator = GenerationValidation(self.store, self.runtime)

    async def asyncTearDown(self):
        await self.validator.close()
        self.store.close()
        self.temp.cleanup()

    def worker(self, source):
        path = self.root / 'engine/python/generation_probe.py'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(source)

    async def test_real_owned_worker_receipt_and_current_configuration_are_separate(self):
        self.worker('import json,sys\nimport validation_fixture\nrequest=json.load(sys.stdin)\nassert request["environment"]["LLM_API_KEY"] == "synthetic-secret"\nprint(json.dumps({"status":"passed","plain_passed":True,"schema_passed":True,"quality_passed":True,"structured_mode":"unknown"}))\n')
        modules = self.root / 'engine/python'
        (modules / 'validation_fixture.py').write_text('fixture = True\n')
        with patch('generation_validation.ROOT', self.root):
            receipt = self.validator.start(self.profile, 1, 'validate')
            self.assertEqual(self.validator.start(self.profile, 1, 'validate'), receipt)
            with self.assertRaisesRegex(PolicyError, 'generation_validation_busy'):
                self.validator.start(self.profile, self.store.state['revision'], 'another')
            await self.validator.task
        self.assertEqual(self.validator.get('validate')['status'], 'passed')
        self.assertFalse((modules / '__pycache__').exists())
        self.assertIsNone(self.validator.process)
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        self.store.select_next(self.profile, self.store.state['revision'], 'select')
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        self.assertEqual(self.store.state['next']['id'], 'api')

    async def test_cancellation_waits_for_process_and_never_claims_model_request_did_not_happen(self):
        self.worker('import json,sys,time\njson.load(sys.stdin)\ntime.sleep(60)\n')
        with patch('generation_validation.ROOT', self.root):
            self.validator.start(self.profile, 1, 'cancelled')
            for _ in range(100):
                if self.validator.process is not None:
                    break
                await asyncio.sleep(.01)
            self.assertIsNotNone(self.validator.process)
            await self.validator.close()
        self.assertIsNone(self.validator.process)
        self.assertEqual(self.validator.get('cancelled')['status'], 'result_unknown')
        self.assertNotIn('schema_passed', self.validator.get('cancelled'))

    async def test_cancel_during_cleanup_confirms_process_exit(self):
        complete = {'status': 'passed', 'plain_passed': True, 'schema_passed': True, 'quality_passed': True}
        self.worker('import sys\nsys.stdin.read()\nprint(' + repr(json.dumps(complete)) + ')\n')
        entered, release = asyncio.Event(), asyncio.Event()
        stop = self.validator.stop_process

        async def delayed_stop():
            entered.set()
            await release.wait()
            await stop()

        with (patch('generation_validation.ROOT', self.root),
              patch.object(self.validator, 'stop_process', side_effect=delayed_stop)):
            self.validator.start(self.profile, 1, 'cleanup')
            await asyncio.wait_for(entered.wait(), 3)
            try:
                running = self.validator.get('cleanup')
                self.assertEqual(running['status'], 'running')
                self.assertEqual(running['phase'], 'cleanup')
                cancel = asyncio.create_task(self.validator.cancel('cleanup', self.store.state['revision'], 'stop'))
                await asyncio.sleep(0)
                self.assertFalse(cancel.done())
            finally:
                release.set()
            result = await cancel
        self.assertTrue(result['local_process_exited'])
        receipt = self.validator.get('cleanup')
        self.assertEqual(receipt['error_code'], 'validation_interrupted')
        self.assertEqual(receipt['failed_phase'], 'cleanup')
        self.assertGreater(receipt['elapsed_ms'], 0)

    async def test_validation_deadline_is_unknown_and_owned_process_exits_without_replay(self):
        self.worker('import json,sys,time\njson.load(sys.stdin)\ntime.sleep(60)\n')
        with patch('generation_validation.ROOT', self.root), patch('generation_validation.PROBE_TIMEOUT_SECONDS', .1):
            first = self.validator.start(self.profile, 1, 'deadline')
            await self.validator.task
            self.assertEqual(self.validator.start(self.profile, 1, 'deadline'), first)
        row = self.validator.get('deadline')
        self.assertEqual(row['status'], 'result_unknown')
        self.assertEqual(row['error_code'], 'validation_timeout')
        self.assertEqual(row['failed_phase'], 'probe')
        self.assertEqual(row['diagnostic_stage'], 'probe')
        self.assertEqual(row['diagnostic_code'], 'validation_timeout')
        self.assertEqual(row['phase'], 'cleanup')
        self.assertGreater(row['elapsed_ms'], 0)
        self.assertIsNone(self.validator.process)
        self.assertFalse(group_exists(row['process_group']))
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        self.assertIsNone(self.store.state['next'])

    async def test_local_model_can_finish_beyond_remote_budget_without_switching_current_configuration(self):
        from generation_ollama import receipt_for
        local = self.store.put('local', {'mode': 'provider', 'provider': 'ollama', 'model': 'synthetic',
                                       'endpoint': 'http://127.0.0.1:11434'}, self.store.state['revision'], 'save-local')
        result = {'status': 'passed', 'plain_passed': True, 'schema_passed': True, 'quality_passed': True,
                  'structured_mode': 'json_schema', 'native_schema_evidence': receipt_for('ollama_chat/synthetic', 'http://127.0.0.1:11434')}
        self.worker('import json,sys,time\njson.load(sys.stdin)\ntime.sleep(.4)\nprint(' + repr(json.dumps(result)) + ')\n')
        with patch('generation_validation.ROOT', self.root), patch('generation_validation.PROBE_TIMEOUT_SECONDS', .25):
            self.validator.start(local, self.store.state['revision'], 'local-slow')
            await self.validator.task
        row = self.validator.get('local-slow')
        self.assertEqual(row['status'], 'passed')
        self.assertFalse(group_exists(row['process_group']))
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        self.assertIsNone(self.store.state['next'])

    async def test_local_budget_still_expires_as_unknown_without_replay_and_reaps_its_child(self):
        local = self.store.put('local', {'mode': 'provider', 'provider': 'ollama', 'model': 'synthetic',
                                       'endpoint': 'http://127.0.0.1:11434'}, self.store.state['revision'], 'save-local')
        self.worker('import json,sys,time\njson.load(sys.stdin)\ntime.sleep(60)\n')
        with patch('generation_validation.ROOT', self.root), patch('generation_validation.PROBE_TIMEOUT_SECONDS', .05):
            first = self.validator.start(local, self.store.state['revision'], 'local-deadline')
            await self.validator.task
            self.assertEqual(self.validator.start(local, 0, 'local-deadline'), first)
        row = self.validator.get('local-deadline')
        self.assertEqual(row['status'], 'result_unknown')
        self.assertEqual(row['error_code'], 'validation_timeout')
        self.assertIsNone(self.validator.process)
        self.assertFalse(group_exists(row['process_group']))
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        self.assertIsNone(self.store.state['next'])

    async def test_agent_cannot_manage_generation_and_get_never_starts_validation(self):
        registry = Registry(self.root / 'registry.json', None)
        client = TestClient(TestServer(create_app(registry, 'm' * 40, {'agent': 'a' * 40},
                                                    generation=self.store, generation_validation=self.validator)))
        await client.start_server()
        try:
            response = await client.get('/v1/generation', headers={'Authorization': 'Bearer ' + 'a' * 40})
            self.assertEqual(response.status, 403)
            response = await client.get('/v1/generation', headers={'Authorization': 'Bearer ' + 'm' * 40})
            self.assertEqual(response.status, 200)
            self.assertNotIn('synthetic-secret', await response.text())
            self.assertIsNone(self.validator.task)
            response = await client.get('/v1/generation/operations/save', headers={'Authorization': 'Bearer ' + 'm' * 40})
            receipt = await response.json()
            self.assertTrue(receipt['recorded'])
            self.assertEqual(receipt['result']['id'], 'api')
            self.assertNotIn('synthetic-secret', json.dumps(receipt))
            response = await client.get('/v1/generation/operations/save', headers={'Authorization': 'Bearer ' + 'a' * 40})
            self.assertEqual(response.status, 403)
            response = await client.put('/v1/generation/next', headers={'Authorization': 'Bearer ' + 'm' * 40},
                                        json={'profile': self.profile, 'expected_revision': 1, 'operation_id': 'unverified'})
            self.assertEqual(response.status, 409)
            self.assertIsNone(self.store.state['next'])
        finally:
            await client.close()
            registry.close()

    async def test_explicit_cancel_of_queued_validation_is_idempotent_and_has_no_implicit_retry(self):
        self.worker('import time\ntime.sleep(60)\n')
        with patch('generation_validation.ROOT', self.root):
            self.validator.start(self.profile, 1, 'queued')
            result = await self.validator.cancel('queued', self.store.state['revision'], 'cancel')
            self.assertEqual(result['status'], 'result_unknown')
            self.assertTrue(result['local_process_exited'])
            again = await self.validator.cancel('queued', 0, 'cancel')
            self.assertEqual(result, again)
        self.assertIsNone(self.validator.process)
        self.assertTrue(self.validator.task.done())

    async def test_local_pass_without_bound_native_schema_receipt_is_not_trusted(self):
        local = self.store.put('local', {'mode': 'provider', 'provider': 'ollama', 'model': 'synthetic',
            'endpoint': 'http://127.0.0.1:43128'}, self.store.state['revision'], 'save-local')
        self.worker('import json,sys\njson.load(sys.stdin)\nprint(json.dumps({"status":"passed","plain_passed":True,"schema_passed":True,"quality_passed":True}))\n')
        with patch('generation_validation.ROOT', self.root):
            self.validator.start(local, self.store.state['revision'], 'unbound')
            await self.validator.task
        receipt = self.validator.get('unbound')
        self.assertEqual(receipt['status'], 'result_unknown')
        self.assertEqual(receipt['diagnostic_code'], 'ollama_native_schema_unverified')
        self.assertEqual(receipt['failed_phase'], 'probe')
        self.assertIsNone(self.store.state['next'])
        self.assertIsNone(self.validator.process)

    async def test_local_bound_native_receipt_survives_worker_and_next_without_activation(self):
        from generation_ollama import receipt_for
        local = self.store.put('local', {'mode': 'provider', 'provider': 'ollama', 'model': 'synthetic',
            'endpoint': 'http://127.0.0.1:43128'}, self.store.state['revision'], 'save-local')
        evidence = receipt_for('ollama_chat/synthetic', 'http://127.0.0.1:43128')
        result = {'status': 'passed', 'plain_passed': True, 'schema_passed': True, 'quality_passed': True,
                  'native_schema_evidence': evidence, 'capability_calls': 1}
        self.worker('import json,sys\njson.load(sys.stdin)\nprint(' + repr(json.dumps(result)) + ')\n')
        with patch('generation_validation.ROOT', self.root):
            self.validator.start(local, self.store.state['revision'], 'bound')
            await self.validator.task
        self.assertEqual(self.validator.get('bound')['status'], 'passed')
        self.assertEqual(self.validator.get('bound')['native_schema_evidence'], evidence)
        self.store.select_next(local, self.store.state['revision'], 'select-local')
        self.assertEqual(self.store.state['next'], local)
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')


if __name__ == '__main__':
    unittest.main()
