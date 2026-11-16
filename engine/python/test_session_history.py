"""Read-only history adapter: official authorization and owner-scoped SDK reads."""
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock

import httpx
from fastapi import FastAPI, Header, HTTPException

from session_history import install_session_history


class SessionHistoryTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        async def authenticated(x_api_key: str = Header(default='')):
            if x_api_key != 'synthetic':
                raise HTTPException(401)
            return SimpleNamespace(id='reader')
        self.manager = SimpleNamespace(is_available=True,
            get_session=AsyncMock(return_value=[{'qa_id': str(i), 'question': str(i), 'answer': 'a'} for i in range(43)]),
            get_agent_trace_session=AsyncMock(return_value=[{'trace_id': str(i)} for i in range(997)]))
        row = SimpleNamespace(user_id='owner', to_dict=lambda: {
            'session_id': 'session', 'dataset_id': 'dataset', 'user_id': 'owner'})
        self.bindings = SimpleNamespace(get_authenticated_user=authenticated,
            get_permitted_dataset_ids=AsyncMock(return_value=['dataset']),
            get_visible_user_ids=AsyncMock(return_value=['reader']),
            get_session_row=AsyncMock(return_value=row), get_session_manager=lambda: self.manager)
        self.app = FastAPI()
        install_session_history(self.app, bindings=self.bindings)
        self.client = httpx.AsyncClient(transport=httpx.ASGITransport(app=self.app), base_url='http://test',
                                      headers={'X-Api-Key': 'synthetic'})

    async def asyncTearDown(self):
        await self.client.aclose()

    async def test_every_saved_entry_is_reachable_in_bounded_pages(self):
        first = (await self.client.get('/api/v1/sessions/session/history')).json()
        self.assertEqual((first['msg_count'], first['tool_calls']), (43, 997))
        self.assertEqual(first['qas'][0]['qa_id'], '0')
        self.assertEqual(len(first['traces']), 20)
        result = await self.client.get('/api/v1/sessions/session/history?qa_offset=40&trace_offset=980')
        self.assertEqual(result.status_code, 200)
        last = result.json()
        self.assertEqual([q['qa_id'] for q in last['qas']], ['40', '41', '42'])
        self.assertEqual(last['traces'][-1]['trace_id'], '996')
        self.assertFalse(last['qa_more'] or last['trace_more'])
        self.manager.get_session.assert_awaited_with(user_id='owner', session_id='session', formatted=False)
        self.bindings.get_session_row.assert_awaited_with(session_id='session', user_id='reader',
            user_ids=['reader'], permitted_dataset_ids=['dataset'])

        qa_ids, trace_ids = [], []
        for offset in range(0, 1000, 20):
            page = (await self.client.get('/api/v1/sessions/session/history',
                params={'qa_offset': min(offset, 43), 'trace_offset': min(offset, 997)})).json()
            qa_ids.extend(q['qa_id'] for q in page['qas'])
            trace_ids.extend(t['trace_id'] for t in page['traces'])
        self.assertEqual(qa_ids, [str(i) for i in range(43)])
        self.assertEqual(trace_ids, [str(i) for i in range(997)])

    async def test_denied_and_unavailable_are_not_empty_history(self):
        self.assertEqual((await self.client.get('/api/v1/sessions/session/history',
            headers={'X-Api-Key': 'wrong'})).status_code, 401)
        self.bindings.get_session_row.return_value = None
        self.assertEqual((await self.client.get('/api/v1/sessions/session/history')).status_code, 404)
        self.manager.get_session.assert_not_awaited()

    async def test_cache_failure_and_invalid_offsets_fail_closed(self):
        self.manager.is_available = False
        self.assertEqual((await self.client.get('/api/v1/sessions/session/history')).status_code, 503)
        self.assertEqual((await self.client.get('/api/v1/sessions/session/history?limit=1000')).status_code, 422)
        self.assertEqual((await self.client.get('/api/v1/sessions/session/history?qa_offset=-1')).status_code, 422)

    async def test_read_failure_and_oversize_do_not_report_success(self):
        self.manager.get_session.side_effect = RuntimeError('private backend detail')
        reply = await self.client.get('/api/v1/sessions/session/history')
        self.assertEqual(reply.status_code, 503)
        self.assertNotIn('private backend detail', reply.text)
        self.manager.get_session.side_effect = None
        self.manager.get_session.return_value = [{'qa_id': 'large', 'answer': 'x' * (8 * 1024 * 1024)}]
        self.assertEqual((await self.client.get('/api/v1/sessions/session/history')).status_code, 413)
