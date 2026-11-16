import asyncio
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch
import uuid

from project_policy import PolicyError
from tool_graph_control import ToolGraphControl


class ToolGraphControlTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.directory = tempfile.TemporaryDirectory()
        root = Path(self.directory.name)
        token = root / 'manager-token'
        token.write_text('synthetic-management-secret')
        token.chmod(0o600)
        self.actor = {'key_file': '/original/agent.key', 'principal_id': 'original-user'}
        self.official = SimpleNamespace(request=AsyncMock(side_effect=self.request))
        self.registry = SimpleNamespace(state={'revision': 0, 'service_namespace': str(uuid.uuid4()),
            'projects': {'p': {'id': 'p', 'status': 'verified', 'dataset_id': 'project-dataset'}},
            'agents': {'a': self.actor}}, official=self.official,
            checked_policy=AsyncMock(return_value={'ready': True, 'policy': {'permissions': ['read', 'write', 'delete']}}))
        self.control = ToolGraphControl(self.registry, {'project_control': {
            'registry_file': str(root / 'registry.json'), 'management_token_file': str(token)}})

    async def asyncTearDown(self):
        await self.control.close()
        self.directory.cleanup()

    async def request(self, method, path, body=None, **kwargs):
        if path.startswith('/api/v1/sessions'):
            return {'sessions': [
                {'session_id': 'selected', 'dataset_id': 'project-dataset', 'user_id': 'original-user', 'tool_calls': 4},
                {'session_id': 'other-project', 'dataset_id': 'other', 'user_id': 'original-user', 'tool_calls': 100},
                {'session_id': 'other-user', 'dataset_id': 'project-dataset', 'user_id': 'other-user', 'tool_calls': 100}], 'has_more': False}
        self.assertEqual(kwargs['token_file'], '/original/agent.key')
        self.assertEqual(kwargs['adapter_token'], 'synthetic-management-secret')
        if path.endswith('/preview'):
            return {'limits': {'selected': 7}, 'pending': {'selected': 4}}
        return {'status': 'completed'}

    async def test_preview_and_manual_start_are_bound_to_actor_project_and_reviewed_count(self):
        preview = await self.control.preview('p')
        self.assertEqual(preview['session_count'], 1)
        self.assertEqual(preview['tool_steps'], 4)
        operation = str(uuid.uuid4())
        await self.control.start('p', operation, preview['review_token'])
        await self.control.tasks[operation]
        self.assertEqual(self.control.latest('p')['status'], 'completed')
        calls = [call for call in self.official.request.call_args_list if call.args[0] == 'POST' and call.args[1].endswith('/tool-graphs')]
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].args[2]['limits'], {'selected': 7})
        await self.control.start('p', operation, preview['review_token'])
        self.assertEqual(len([call for call in self.official.request.call_args_list if call.args[0] == 'POST' and call.args[1].endswith('/tool-graphs')]), 1)

    async def test_changed_review_or_revoked_write_does_not_dispatch(self):
        preview = await self.control.preview('p')
        self.registry.state['revision'] += 1
        with self.assertRaises(PolicyError):
            await self.control.start('p', str(uuid.uuid4()), preview['review_token'])
        self.registry.checked_policy.return_value = {'ready': True, 'policy': {'permissions': ['read']}}
        with self.assertRaises(PolicyError):
            await self.control.actor('p', 'a')
        self.assertFalse(any(call.args[0] == 'POST' and call.args[1].endswith('/tool-graphs') for call in self.official.request.call_args_list))

    async def test_uncertain_submission_is_not_repeated_under_a_new_id(self):
        preview = await self.control.preview('p')
        original = self.official.request.side_effect
        async def request(method, path, *args, **kwargs):
            if method == 'POST' and path.endswith('/tool-graphs'):
                raise PolicyError('official_result_unknown', 503)
            return await original(method, path, *args, **kwargs)
        self.official.request.side_effect = request
        operation = str(uuid.uuid4())
        await self.control.start('p', operation, preview['review_token'])
        await self.control.tasks[operation]
        self.assertEqual(self.control.latest('p')['status'], 'result_unknown')
        with self.assertRaises(PolicyError):
            await self.control.start('p', str(uuid.uuid4()), preview['review_token'])

    async def test_automatic_graph_respects_project_date_and_auto_processing_permission(self):
        self.registry.state['tool_graph'] = {'enabled': True, 'changed_at': '2026-11-16T00:00:00+00:00'}
        activity = [
            {'id': 'old', 'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
             'dataset_id': 'project-dataset', 'started_at': '2026-11-15T23:00:00+00:00',
             'user_id': 'original-user', 'session_ids': ['selected']},
            {'id': 'new', 'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
             'dataset_id': 'project-dataset', 'started_at': '2026-11-16T01:00:00+00:00',
             'user_id': 'original-user', 'session_ids': ['selected']}]
        original = self.official.request.side_effect
        async def request(method, path, *args, **kwargs):
            if '/activity/' in path:
                return activity
            return await original(method, path, *args, **kwargs)
        self.official.request.side_effect = request
        self.control.start = AsyncMock()
        for enabled in (False, True):
            self.registry.checked_policy.return_value = {'ready': True, 'policy': {
                'permissions': ['read', 'write'], 'can_auto_process': enabled}}
            with patch('tool_graph_control.asyncio.sleep', AsyncMock(side_effect=asyncio.CancelledError)):
                with self.assertRaises(asyncio.CancelledError):
                    await self.control.automatic_loop()
            self.assertEqual(self.control.start.await_count, int(enabled))
        call = self.control.start.await_args
        self.assertTrue(call.kwargs['automatic'])
        self.assertEqual(call.kwargs['plan']['plan'][0]['session_id'], 'selected')
