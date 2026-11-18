import asyncio
import json
from pathlib import Path
import tempfile
import unittest

from first_install_mode import first_install_setup, SetupOnlyMiddleware
from first_install_store import write_private


class FirstInstallModeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.root.chmod(0o700)
        marker = {'schema': 1, 'store_id': 'a' * 32, 'source_commit': 'b' * 40,
                  'model_digest': 'c' * 64}
        write_private(self.root / '.memlane-first-store.json', json.dumps(marker))
        self.receipt = {'schema': 1, 'store_id': marker['store_id'],
                        'source_commit': marker['source_commit'], 'storage_initialized': True,
                        'owner_id': 'fixture-owner', 'key_id': 'fixture-key'}
        write_private(self.root / 'store-identity.json', json.dumps(self.receipt))
        write_private(self.root / 'owner.token', 'synthetic-owner-token')
        self.config = {'first_install': {'schema': 1, 'setup': True, 'store_id': marker['store_id']},
                       'official_data': str(self.root), 'service_commit': marker['source_commit'],
                       'token_file': str(self.root / 'owner.token'), 'model': '',
                       'generation_mode': 'unconfigured', 'generation_file': str(self.root / 'generation.json')}

    def test_daily_config_never_falls_back_to_first_installation(self):
        self.assertFalse(first_install_setup({}))
        self.assertFalse(first_install_setup({'first_install': {'schema': 1, 'setup': False}}))
        self.assertTrue(first_install_setup(self.config))
        (self.root / 'store-identity.json').unlink()
        with self.assertRaises(FileNotFoundError):
            first_install_setup(self.config)

    def test_existing_or_active_generation_cannot_be_replaced(self):
        for changes in ({'model': 'configured-model'}, {'generation_reference': {'id': 'existing'}},
                        {'generation_setup_only': True}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                first_install_setup({**self.config, **changes})
        write_private(self.root / 'generation.json', json.dumps({'schema_version': 2, 'active': {'id': 'existing'}}))
        with self.assertRaises(ValueError):
            first_install_setup(self.config)

    def test_wrong_store_and_foreign_or_linked_credentials_are_rejected(self):
        wrong = {**self.config, 'first_install': {'schema': 1, 'setup': True, 'store_id': 'other'}}
        with self.assertRaises(ValueError):
            first_install_setup(wrong)
        with self.assertRaises(ValueError):
            first_install_setup({**self.config, 'token_file': str(self.root / 'other.token')})
        (self.root / 'owner.token').rename(self.root / 'other.token')
        (self.root / 'owner.token').symlink_to('other.token')
        with self.assertRaises(OSError):
            first_install_setup(self.config)

    def test_private_complete_store_can_resume_with_pending_generation(self):
        write_private(self.root / 'generation.json', json.dumps({'schema_version': 2, 'active': None,
                                                              'next': {'id': 'validated'}}))
        self.assertTrue(first_install_setup(self.config))
        (self.root / 'store-identity.json').chmod(0o644)
        with self.assertRaises(RuntimeError):
            first_install_setup(self.config)

    def test_setup_keeps_authentication_and_blocks_content_before_model_calls(self):
        calls = []
        async def backend(scope, receive, send):
            calls.append(scope['path'])
            await send({'type': 'http.response.start', 'status': 401, 'headers': []})
            await send({'type': 'http.response.body', 'body': b'authentication required'})

        gate = SetupOnlyMiddleware(backend)
        async def request(path, method='POST', kind='http'):
            sent = []
            async def receive():
                return {'type': 'http.request', 'body': b''}
            async def send(message):
                sent.append(message)
            await gate({'type': kind, 'method': method, 'path': path}, receive, send)
            return sent

        for path in ('/api/v1/remember', '/api/v1/cognify', '/api/v1/search',
                     '/api/v1/sessions', '/api/v1/datasets/../remember'):
            self.assertEqual(asyncio.run(request(path))[0]['status'], 503)
        self.assertEqual(calls, [])
        health = asyncio.run(request('/health', 'GET'))
        self.assertEqual(health[0]['status'], 200)
        self.assertEqual(json.loads(health[1]['body']), {
            'status': 'setup_ready', 'generation': 'unconfigured', 'checks': 'liveness_only'})
        self.assertEqual(calls, [], 'setup health initialized the content engine')
        for path, method in (('/api/v1/users/me', 'GET'), ('/api/v1/agents/create', 'POST'),
                             ('/api/v1/datasets', 'POST'), ('/api/v1/permissions/datasets/principal', 'DELETE')):
            self.assertEqual(asyncio.run(request(path, method))[0]['status'], 401)
        self.assertEqual(len(calls), 4)
        self.assertEqual(asyncio.run(request('/api/v1/sessions', kind='websocket'))[0]['type'], 'websocket.close')
