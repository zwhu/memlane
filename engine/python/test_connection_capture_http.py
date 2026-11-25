import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from aiohttp.test_utils import TestClient, TestServer
from agent_connections import Connections
from connection_capture import ConnectionCapture
from project_codex_connection import CodexConnection
from project_control import Registry
from project_control_server import create_app
from project_policy import PolicyError, mark_connection_mutation


class CaptureHTTPTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.profile = self.root / 'codex'
        self.profile.mkdir()
        self.official = type('OfficialFixture', (), {'url': 'http://127.0.0.1:18999'})()
        self.registry = Registry(self.root/'private/registry.json', self.official)
        self.config = {'python': sys.executable, 'auth_home': str(self.profile), 'codex_executable': sys.executable,
            'agent_hosts': {'codex': {'profile': str(self.profile), 'executable': sys.executable}}}
        self.adapter = CodexConnection(self.registry, self.config)
        self.identifier = self.adapter.id
        self.registry.state['agents'][self.identifier] = {'name': 'Codex', 'host_profile': str(self.profile),
            'principal_id': 'fixture-subject', 'installation': {'manifest_sha256': 'fixed', 'marketplace_name': 'fixed'}}
        self.registry.state['codex_connections'] = {self.identifier: {'status': 'configured_pending_new_session', 'activated_at': 1}}
        self.registry.save()
        self.permission, self.allow_count, self.unknown = 'required', 0, False
        owner = self
        def view(adapter):
            return {'instance_id': adapter.id, 'kind': 'codex', 'detected': True,
                    'status': 'configured_pending_new_session', 'profile': str(owner.profile)}
        async def inspect(capture, row, adapter):
            record = capture.record(adapter.id, 'codex')
            state = ('deferred' if record.get('capture_deferred') else 'needs_permission') if owner.permission == 'required' else 'waiting_session'
            return {'state': state, 'permission': owner.permission, 'review_sha': 'reviewed'}
        async def allow(capture, adapter, reviewed):
            if reviewed != 'reviewed': raise PolicyError('capture_review_changed', 409)
            mark_connection_mutation()
            owner.allow_count += 1
            owner.permission = 'granted'
            capture.record(adapter.id, 'codex')['capture_deferred'] = False
            capture.cache.pop(adapter.id, None)
            if owner.unknown: raise PolicyError('capture_allow_result_unconfirmed', 503)
        self.patches = [patch.object(CodexConnection, 'view', view),
                        patch.object(ConnectionCapture, 'inspect', inspect), patch.object(ConnectionCapture, 'allow', allow)]
        for item in self.patches: item.start()
        self.client = TestClient(TestServer(create_app(self.registry, 'm'*40, {'fixture-agent': 'a'*40}, codex_config=self.config)))
        await self.client.start_server()
        self.headers = {'Authorization': 'Bearer '+'m'*40}

    async def asyncTearDown(self):
        await self.client.close()
        self.registry.close()
        for item in reversed(self.patches): item.stop()
        self.temp.cleanup()

    async def view(self):
        response = await self.client.get('/v1/connections', headers=self.headers)
        self.assertEqual(response.status, 200)
        return next(row for row in (await response.json())['connections'] if row['instance_id'] == self.identifier)

    async def decide(self, action, operation, reviewed='reviewed', headers=None):
        return await self.client.post('/v1/connections/'+self.identifier+'/'+action, headers=headers or self.headers,
            json={'expected_revision': self.registry.state['revision'], 'operation_id': operation, 'proposed_sha256': reviewed})

    async def test_defer_allow_refresh_and_original_operation_idempotency(self):
        self.assertEqual((await self.view())['capture']['state'], 'needs_permission')
        reply = await self.decide('defer_capture', 'decline-once')
        self.assertEqual(reply.status, 200)
        self.assertTrue(json.loads(self.registry.path.read_text())['codex_connections'][self.identifier]['capture_deferred'])
        self.assertEqual((await self.view())['capture']['state'], 'deferred')
        self.assertEqual(self.allow_count, 0)
        reply = await self.decide('allow_capture', 'allow-once')
        self.assertEqual(reply.status, 200)
        self.assertEqual((await self.view())['capture']['state'], 'waiting_session')
        self.assertEqual(self.allow_count, 1)
        reply = await self.decide('allow_capture', 'allow-once')
        self.assertEqual(reply.status, 200)
        self.assertEqual(self.allow_count, 1)

    async def test_only_manager_can_allow_and_changed_review_is_terminal_rejection(self):
        before = self.registry.path.read_bytes()
        reply = await self.decide('allow_capture', 'forbidden', headers={'Authorization': 'Bearer '+'a'*40})
        self.assertEqual(reply.status, 403)
        self.assertEqual(self.registry.path.read_bytes(), before)
        reply = await self.decide('allow_capture', 'old-review', reviewed='obsolete')
        self.assertEqual(reply.status, 409)
        receipt = await self.client.get('/v1/connections/operations/old-review', headers=self.headers)
        self.assertEqual((await receipt.json())['outcome'], 'rejected')
        self.assertEqual(self.allow_count, 0)

    async def test_unknown_allow_is_reconciled_read_only_not_repeated(self):
        self.unknown = True
        reply = await self.decide('allow_capture', 'unknown-original')
        self.assertEqual(reply.status, 503)
        self.assertEqual((await reply.json())['phase'], 'post_mutation')
        before = self.registry.path.read_bytes()
        self.assertEqual(self.registry.state['connection_operations']['unknown-original']['status'], 'unknown')
        receipt = await self.client.get('/v1/connections/operations/unknown-original', headers=self.headers)
        value = await receipt.json()
        self.assertEqual(value['status'], 'complete')
        self.assertTrue(value['reconciled'])
        self.assertEqual(self.allow_count, 1)
        self.assertEqual(self.registry.path.read_bytes(), before)
