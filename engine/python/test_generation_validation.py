import asyncio
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from aiohttp.test_utils import TestClient, TestServer

from generation_config import GenerationStore
from generation_validation import GenerationValidation
from project_control import Registry
from project_control_server import create_app
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
            response = await client.put('/v1/generation/next', headers={'Authorization': 'Bearer ' + 'm' * 40},
                                        json={'profile': self.profile, 'expected_revision': 1, 'operation_id': 'unverified'})
            self.assertEqual(response.status, 409)
            self.assertIsNone(self.store.state['next'])
        finally:
            await client.close()
            registry.close()


if __name__ == '__main__':
    unittest.main()
