"""Read-only history adapter: official authorization and owner-scoped SDK reads."""
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock

import httpx
from fastapi import FastAPI, Header, HTTPException

from session_history import install_session_history


class SessionHistoryTests(unittest.IsolatedAsyncioTestCase):
    async def test_capture_status_is_authorized_metadata_without_prompt_or_answer(self):
        self.manager.get_session.return_value = [{'qa_id': 'saved', 'question': 'private prompt',
            'answer': 'private answer', 'time': '2026-11-17T15:00:00Z'}]
        response = await self.client.get('/api/v1/sessions/session/capture-status')
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json()['qa_count'], 1)
        self.assertGreater(response.json()['last_qa_at'], 0)
        self.assertNotIn('private', response.text)
        self.assertNotIn('qas', response.json())
        self.assertEqual((await self.client.get('/api/v1/sessions/session/capture-status',
            headers={'X-Api-Key': 'wrong'})).status_code, 401)
        self.manager.is_available = False
        self.assertEqual((await self.client.get('/api/v1/sessions/session/capture-status')).status_code, 503)

    async def test_capture_status_never_uses_session_activity_or_invalid_entry_times_as_saved_evidence(self):
        self.manager.get_session.return_value = [{'qa_id': 'no-time'}, {'qa_id': 'bad', 'time': 'bad'},
            {'qa_id': 'future', 'time': '2999-01-01T00:00:00Z'}]
        response = await self.client.get('/api/v1/sessions/session/capture-status')
        self.assertIsNone(response.json()['last_qa_at'])

    async def test_capture_status_reads_pinned_sdk_entry_objects_without_copying_content(self):
        self.manager.get_session.return_value = [SimpleNamespace(qa_id='sdk-entry', time='2026-11-17T15:00:00Z')]
        response = await self.client.get('/api/v1/sessions/session/capture-status')
        self.assertEqual(response.status_code, 200)
        self.assertIsNotNone(response.json()['last_qa_at'])

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

    async def test_cache_db_fast_path_reads_bounded_pages_without_full_deserialize(self):
        with tempfile.TemporaryDirectory() as tmp:
            cache_db = Path(tmp) / 'cache.db'
            con = sqlite3.connect(cache_db)
            con.executescript('''
                CREATE TABLE cache_qa_entries (
                    seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                    qa_id TEXT NOT NULL, payload JSON NOT NULL,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
                );
                CREATE INDEX idx_cache_qa_expires ON cache_qa_entries (expires_at) WHERE expires_at IS NOT NULL;
                CREATE INDEX idx_cache_qa_session ON cache_qa_entries (user_id, session_id, seq);
                CREATE TABLE cache_trace_entries (
                    seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                    payload JSON NOT NULL,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
                );
                CREATE INDEX idx_cache_trace_expires ON cache_trace_entries (expires_at) WHERE expires_at IS NOT NULL;
                CREATE INDEX idx_cache_trace_session ON cache_trace_entries (user_id, session_id, seq);
            ''')
            con.execute(
                'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
                (1, 'owner', 'session', 'expired-q', json.dumps({'qa_id': 'expired-q', 'question': 'stale'}), '2020-01-01 00:00:00'),
            )
            for idx in range(25):
                con.execute(
                    'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
                    (idx + 2, 'owner', 'session', f'qa-{idx}', json.dumps({'qa_id': f'qa-{idx}', 'question': f'Q{idx}', 'answer': f'A{idx}'}), None),
                )
            for idx in range(30):
                con.execute(
                    'INSERT INTO cache_trace_entries (seq, user_id, session_id, payload, expires_at) VALUES (?,?,?,?,?)',
                    (idx + 1, 'owner', 'session', json.dumps({'trace_id': f'tr-{idx}', 'origin_function': 'Read'}), None),
                )
            con.commit()
            con.close()

            fast_app = FastAPI()
            install_session_history(fast_app, bindings=self.bindings, cache_db_path=cache_db)
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=fast_app),
                base_url='http://test',
                headers={'X-Api-Key': 'synthetic'},
            ) as fast_client:
                resp = await fast_client.get('/api/v1/sessions/session/history?qa_offset=20&trace_offset=20&limit=20')
                self.assertEqual(resp.status_code, 200)
                body = resp.json()
                self.assertEqual((body['msg_count'], body['tool_calls']), (25, 30))
                self.assertEqual(body['label'], 'Q0')
                self.assertEqual([q['qa_id'] for q in body['qas']], [f'qa-{i}' for i in range(20, 25)])
                self.assertEqual([t['trace_id'] for t in body['traces']], [f'tr-{i}' for i in range(20, 30)])
                self.assertFalse(body['qa_more'] or body['trace_more'])
                self.manager.get_session.assert_not_awaited()
                self.manager.get_agent_trace_session.assert_not_awaited()

                self.assertEqual((await fast_client.get('/api/v1/sessions/session/history?qa_offset=99')).status_code, 409)
                # Falls back cleanly to SessionManager if cache_db vanishes
                cache_db.unlink()
                fallback_resp = await fast_client.get('/api/v1/sessions/session/history')
                self.assertEqual(fallback_resp.status_code, 200)
                self.manager.get_session.assert_awaited_once()

    async def test_broken_or_empty_cache_db_falls_back_to_session_manager_and_503_when_unavailable(self):
        with tempfile.TemporaryDirectory() as tmp:
            cache_db = Path(tmp) / 'cache.db'
            # Missing cache_trace_entries table (schema drift)
            con = sqlite3.connect(cache_db)
            con.execute('CREATE TABLE cache_qa_entries (seq INTEGER PRIMARY KEY)')
            con.commit()
            con.close()

            fast_app = FastAPI()
            install_session_history(fast_app, bindings=self.bindings, cache_db_path=cache_db)
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=fast_app),
                base_url='http://test',
                headers={'X-Api-Key': 'synthetic'},
            ) as fast_client:
                # Schema drift falls back to SessionManager rather than returning 200 empty.
                resp = await fast_client.get('/api/v1/sessions/session/history')
                self.assertEqual(resp.status_code, 200)
                self.assertEqual(resp.json()['msg_count'], 43)
                self.manager.get_session.assert_awaited_once()

                # Valid empty tables also confirm through SessionManager instead of trusting 0.
                con = sqlite3.connect(cache_db)
                con.executescript('''
                    DROP TABLE cache_qa_entries;
                    CREATE TABLE cache_qa_entries (
                        seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT,
                        qa_id TEXT, payload JSON, expires_at DATETIME
                    );
                    CREATE TABLE cache_trace_entries (
                        seq INTEGER PRIMARY KEY, user_id TEXT, session_id TEXT,
                        payload JSON, expires_at DATETIME
                    );
                ''')
                con.commit()
                con.close()
                self.manager.get_session.reset_mock()
                resp2 = await fast_client.get('/api/v1/sessions/session/history')
                self.assertEqual(resp2.status_code, 200)
                self.assertEqual(resp2.json()['msg_count'], 43)
                self.manager.get_session.assert_awaited_once()

                # When SessionManager fails, must return 503 (never 200 empty).
                self.manager.get_session.side_effect = RuntimeError('down')
                resp3 = await fast_client.get('/api/v1/sessions/session/history')
                self.assertEqual(resp3.status_code, 503)
