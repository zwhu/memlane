import asyncio
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch
import uuid

from project_policy import PolicyError
from tool_graph_service import ToolGraphJobs


class ToolGraphJobTests(unittest.IsolatedAsyncioTestCase):
    async def test_http_adapter_uses_native_auth_and_management_gate(self):
        import sys
        import json
        import httpx
        from fastapi import FastAPI
        from tool_graph_service import install_tool_graph_routes
        root = Path(__file__).resolve().parents[2]
        manifest = json.loads((root / 'engine/coding-memory-components.json').read_text())['service']
        source = str(root / '.cache/coding-memory-components' / ('service-' + manifest['commit']))
        if source not in sys.path:
            sys.path.insert(0, source)
        from cognee.modules.users.methods import get_authenticated_user
        token = Path(self.directory.name) / 'manager-token'
        token.write_text('synthetic-only-private-manager-token')
        token.chmod(0o600)
        app = FastAPI()
        jobs = install_tool_graph_routes(app, {'project_control': {
            'registry_file': str(Path(self.directory.name) / 'adapter-registry.json'),
            'management_token_file': str(token)}})
        jobs.authorize = AsyncMock()
        jobs.build = self.build
        app.dependency_overrides[get_authenticated_user] = lambda: self.user
        body = {'operation_id': str(uuid.uuid4()), 'dataset_id': self.dataset, 'session_ids': ['original-session'],
                'limits': {'original-session': 3}}
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://synthetic') as client:
            from cognee.infrastructure.background_tasks import register_background_task
            release = asyncio.Event()
            pending = register_background_task(asyncio.create_task(release.wait()))
            try:
                response = await client.get('/api/v1/memlane/tool-graphs/capacity')
                self.assertEqual(response.status_code, 403)
                response = await client.get('/api/v1/memlane/tool-graphs/capacity',
                    headers={'x-memlane-management': token.read_text()})
                self.assertEqual(response.status_code, 200)
                self.assertGreaterEqual(response.json()['pending_background_tasks'], 1)
            finally:
                release.set()
                await pending
            response = await client.post('/api/v1/memlane/tool-graphs', json=body)
            self.assertEqual(response.status_code, 403)
            response = await client.post('/api/v1/memlane/tool-graphs', json=body,
                headers={'x-memlane-management': token.read_text()})
            self.assertEqual(response.status_code, 200, response.text)
            operation = body['operation_id']
            await jobs.tasks[operation]
            self.assertEqual(jobs.get(self.user, operation)['status'], 'completed')
            jobs.build.assert_awaited_once_with(self.user, self.dataset, ['original-session'], {'original-session': 3})
        await jobs.close()

    async def asyncSetUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.path = Path(self.directory.name) / 'jobs.json'
        self.authorize = AsyncMock()
        self.build = AsyncMock(return_value={'new_steps': 3, 'pipeline_run_ids': []})
        self.jobs = ToolGraphJobs(self.path, self.build, self.authorize)
        self.user = SimpleNamespace(id=uuid.uuid4())
        self.dataset = str(uuid.uuid4())

    async def asyncTearDown(self):
        await self.jobs.close()
        self.directory.cleanup()

    async def test_original_actor_scope_idempotence_and_successful_result(self):
        operation = str(uuid.uuid4())
        first = await self.jobs.start(self.user, self.dataset, ['source-session'], operation)
        await self.jobs.tasks[operation]
        result = await self.jobs.start(self.user, self.dataset, ['source-session'], operation)
        self.assertEqual(first['operation_id'], result['operation_id'])
        self.assertEqual(result['status'], 'completed')
        self.build.assert_awaited_once_with(self.user, self.dataset, ['source-session'], None)
        with self.assertRaises(PolicyError):
            self.jobs.get(SimpleNamespace(id=uuid.uuid4()), operation)

    async def test_failure_is_unknown_and_not_retried_on_restart(self):
        self.build.side_effect = RuntimeError('private-model-output')
        operation = str(uuid.uuid4())
        await self.jobs.start(self.user, self.dataset, ['source'], operation)
        await self.jobs.tasks[operation]
        reopened = ToolGraphJobs(self.path, self.build, self.authorize)
        self.assertEqual(reopened.get(self.user, operation)['status'], 'result_unknown')
        self.assertNotIn('private-model-output', self.path.read_text())
        with self.assertRaises(PolicyError):
            await reopened.start(self.user, self.dataset, ['source'], str(uuid.uuid4()))
        self.assertEqual(self.build.await_count, 1)

    async def test_cancel_waits_for_owned_pipeline_cleanup(self):
        entered, exited = asyncio.Event(), asyncio.Event()
        async def build(*_args):
            entered.set()
            try:
                await asyncio.Event().wait()
            finally:
                exited.set()
        self.jobs.build = build
        operation = str(uuid.uuid4())
        await self.jobs.start(self.user, self.dataset, ['source'], operation)
        await entered.wait()
        result = await self.jobs.cancel(self.user, operation)
        self.assertTrue(exited.is_set())
        self.assertEqual(result['status'], 'cancelled')

    async def test_denied_request_cannot_create_a_job(self):
        self.authorize.side_effect = PolicyError('write_required', 403)
        with self.assertRaises(PolicyError):
            await self.jobs.start(self.user, self.dataset, ['source'], str(uuid.uuid4()))
        self.assertFalse(self.jobs.rows)
        self.build.assert_not_awaited()

    async def test_sdk_build_passes_dataset_uuid_for_shared_agent_access(self):
        import sys, json
        root = Path(__file__).resolve().parents[2]
        commit = json.loads((root / 'engine/coding-memory-components.json').read_text())['service']['commit']
        sys.path.insert(0, str(root / '.cache/coding-memory-components' / ('service-' + commit)))
        from tool_graph_service import official_build
        manager = SimpleNamespace(get_agent_trace_count=AsyncMock(return_value=1))
        async def pipeline(**kwargs):
            self.assertIsInstance(kwargs['dataset'], uuid.UUID,
                'SDK treats string dataset IDs as names and denies shared Agent access')
            return {}
        with patch('cognee.infrastructure.session.get_session_manager.get_session_manager', return_value=manager), \
             patch('cognee.infrastructure.session.session_persist_watermark.TRACE_PERSIST_WATERMARK', SimpleNamespace(read_count=AsyncMock(return_value=0))), \
             patch('cognee.memify_pipelines.persist_agent_trace_feedbacks_in_knowledge_graph.persist_agent_trace_feedbacks_in_knowledge_graph_pipeline', pipeline):
            await official_build(self.user, str(self.dataset), ['source'], {'source': 1})

    async def test_uncapped_over_128_sessions_and_terminal_job_pruning(self):
        sessions = [f's-{i:03d}' for i in range(200)]
        op = str(uuid.uuid4())
        res = await self.jobs.start(self.user, self.dataset, sessions, op)
        await self.jobs.tasks[op]
        self.assertEqual(self.jobs.get(self.user, op)['status'], 'completed')
        self.assertEqual(len(self.jobs.get(self.user, op)['session_ids']), 200)

        # Populate 210 terminal jobs + 1 result_unknown job -> prunes terminal to 200, keeps result_unknown
        unknown_id = str(uuid.uuid4())
        self.jobs.rows[unknown_id] = {
            'operation_id': unknown_id,
            'user_id': str(self.user.id),
            'dataset_id': self.dataset,
            'session_ids': ['s-000'],
            'signature': 'sig',
            'status': 'result_unknown',
            'created_at': '2020-01-01T00:00:00+00:00',
        }
        for i in range(210):
            tid = str(uuid.uuid4())
            self.jobs.rows[tid] = {
                'operation_id': tid,
                'user_id': str(self.user.id),
                'dataset_id': self.dataset,
                'session_ids': ['s-000'],
                'signature': f'sig-{i}',
                'status': 'completed',
                'created_at': f'2026-11-21T00:{i // 60:02d}:{i % 60:02d}+00:00',
                'ended_at': f'2026-11-21T00:{i // 60:02d}:{i % 60:02d}+00:00',
            }
        self.jobs.save()
        terminal_remaining = [r for r in self.jobs.rows.values() if r['status'] in ('completed', 'cancelled')]
        self.assertEqual(len(terminal_remaining), 200)
        self.assertIn(unknown_id, self.jobs.rows)
