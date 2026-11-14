import asyncio
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from aiohttp.test_utils import TestClient, TestServer

from generation_config import GenerationStore
from generation_validation import GenerationValidation, group_exists
from project_control import Registry
from project_control_server import create_app


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
from project_policy import PolicyError


class ValidationTests(unittest.IsolatedAsyncioTestCase):
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
        self.worker('import json,sys\nrequest=json.load(sys.stdin)\nassert request["environment"]["LLM_API_KEY"] == "synthetic-secret"\nprint(json.dumps({"status":"passed","plain_passed":True,"schema_passed":True,"quality_passed":True,"structured_mode":"unknown"}))\n')
        with patch('generation_validation.ROOT', self.root):
            receipt = self.validator.start(self.profile, 1, 'validate')
            self.assertEqual(self.validator.start(self.profile, 1, 'validate'), receipt)
            with self.assertRaisesRegex(PolicyError, 'generation_validation_busy'):
                self.validator.start(self.profile, self.store.state['revision'], 'another')
            await self.validator.task
        self.assertEqual(self.validator.get('validate')['status'], 'passed')
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

    async def test_validation_deadline_is_unknown_and_owned_process_exits_without_replay(self):
        self.worker('import json,sys,time\njson.load(sys.stdin)\ntime.sleep(60)\n')
        with patch('generation_validation.ROOT', self.root), patch('generation_validation.PROBE_TIMEOUT_SECONDS', .1):
            first = self.validator.start(self.profile, 1, 'deadline')
            await self.validator.task
            self.assertEqual(self.validator.start(self.profile, 1, 'deadline'), first)
        row = self.validator.get('deadline')
        self.assertEqual(row['status'], 'result_unknown')
        self.assertEqual(row['error_code'], 'validation_timeout')
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
        self.assertEqual(self.validator.get('unbound')['status'], 'result_unknown')
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
