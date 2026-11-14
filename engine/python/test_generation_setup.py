import json
from pathlib import Path
import tempfile
import unittest

from aiohttp.test_utils import TestClient, TestServer
from generation_config import GenerationStore
from project_control import private_write
from project_control_server import configured_app


class GenerationSetupTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        private_write(self.root / 'manager', b'x' * 40)
        self.config = {'generation_setup_only': True, 'generation_file': str(self.root / 'generation.json'),
                       'service_commit': 'fixed', 'management_token_file': str(self.root / 'manager')}
        self.app = await configured_app(self.config)
        self.client = TestClient(TestServer(self.app))
        await self.client.start_server()
        self.headers = {'Authorization': 'Bearer ' + 'x' * 40}

    async def asyncTearDown(self):
        await self.client.close()
        self.temp.cleanup()

    async def test_configures_without_official_runtime_or_registry_and_preserves_unknown_validation(self):
        # No service URL, owner key, official store, Codex auth or executable.
        response = await self.client.get('/v1/generation', headers=self.headers)
        self.assertEqual(response.status, 200)
        view = await response.json()
        self.assertIsNone(view['active'])
        payload = {'expected_revision': view['revision'], 'operation_id': 'first-save',
                   'profile': {'mode': 'provider', 'provider': 'custom', 'model': 'synthetic',
                               'endpoint': 'https://example.test/v1'}, 'api_key': 'private-test-key'}
        response = await self.client.put('/v1/generation/profiles/api', headers=self.headers, json=payload)
        self.assertEqual(response.status, 200)
        saved = await response.json()
        reference = {k: saved['result'][k] for k in ('id', 'revision')}
        response = await self.client.put('/v1/generation/next', headers=self.headers,
            json={'expected_revision': saved['revision'], 'operation_id': 'first-next', 'profile': reference})
        self.assertEqual(response.status, 409)
        self.assertEqual((await response.json())['error'], 'generation_validation_required')
        view = await (await self.client.get('/v1/generation', headers=self.headers)).json()
        self.assertNotIn('private-test-key', json.dumps(view))
        self.assertIsNone(view['active'])
        self.assertIsNone(view['next'])
        self.assertFalse((self.root / 'registry.json').exists())

    async def test_only_manager_generation_routes_are_available(self):
        self.assertEqual((await self.client.get('/v1/generation')).status, 401)
        self.assertEqual((await self.client.get('/v1/generation', headers={**self.headers, 'Origin': 'https://example.test'})).status, 403)
        for path in ('/v1/projects', '/v1/connections', '/v1/project/policy'):
            self.assertEqual((await self.client.get(path, headers=self.headers)).status, 404)
        self.assertEqual((await self.client.post('/v1/agents', headers=self.headers, json={})).status, 404)

    async def test_local_ollama_saves_without_key_and_cannot_skip_validation(self):
        payload = {'expected_revision': 0, 'operation_id': 'save-local', 'profile': {
            'mode': 'provider', 'provider': 'ollama', 'model': 'llama3.2:3b',
            'endpoint': 'http://127.0.0.1:11434'}}
        response = await self.client.put('/v1/generation/profiles/local', headers=self.headers, json=payload)
        self.assertEqual(response.status, 200, await response.text())
        saved = await response.json()
        response = await self.client.put('/v1/generation/next', headers=self.headers, json={
            'expected_revision': saved['revision'], 'operation_id': 'select-unvalidated',
            'profile': {k: saved['result'][k] for k in ('id', 'revision')}})
        self.assertEqual(response.status, 409)
        view = await (await self.client.get('/v1/generation', headers=self.headers)).json()
        self.assertEqual(view['profiles']['local'][0]['profile'], payload['profile'])
        self.assertIsNone(view['active'])
        self.assertIsNone(view['next'])
        self.assertFalse((self.root / 'generation-credentials').exists())
        self.assertFalse((self.root / 'registry.json').exists())

    async def test_setup_rejects_already_active_and_releases_generation_lock(self):
        await self.client.close()
        store = GenerationStore(self.config['generation_file'], 'fixed')
        row = store.put('existing', {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'old'}, 0, 'save')
        store.state['active'] = {k: row[k] for k in ('id', 'revision')}
        store.save(); store.close()
        with self.assertRaisesRegex(Exception, 'generation_setup_requires_unconfigured_store'):
            await configured_app(self.config)
        store = GenerationStore(self.config['generation_file'], 'fixed')
        self.assertEqual(store.state['active']['id'], 'existing')
        store.close()


if __name__ == '__main__':
    unittest.main()
