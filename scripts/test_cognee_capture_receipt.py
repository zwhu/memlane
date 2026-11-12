import asyncio
import json
from pathlib import Path
import sys
import types
import unittest
from unittest.mock import AsyncMock, patch
from uuid import uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import cognee_capture_receipt as adapter


class ReceiptSelection(unittest.TestCase):
    def entry(self, kind='qa'):
        marker = json.dumps({'origin': 'memlane_pi_capture_v1', 'operation': 'a' * 64, 'digest': 'b' * 64})
        if kind == 'qa':
            return {'qa_id': str(uuid4()), 'question': 'synthetic question', 'answer': 'synthetic answer', 'context': marker}
        return {'trace_id': str(uuid4()), 'origin_function': 'bash', 'status': 'success', 'method_params': {'command': 'synthetic'}, 'method_return_value': 'done', 'error_message': '', 'memory_context': marker}

    def test_actual_fields_and_stable_identity(self):
        for kind in ('qa', 'trace'):
            entry = self.entry(kind)
            result = adapter.select_receipt([entry], kind, 'a' * 64)
            self.assertEqual(result['state'], 'saved')
            self.assertEqual(result['entry_id'], entry[kind + '_id'])
            self.assertEqual(result['entry']['type'], kind)
            if kind == 'trace':
                self.assertIs(result['entry']['generate_feedback_with_llm'], False)
                self.assertEqual(result['entry']['method_return_value'], 'done')

    def test_missing_and_evicted_are_unknown(self):
        self.assertEqual(adapter.select_receipt([], 'qa', 'a' * 64), {'state': 'unknown'})
        entry = self.entry()
        entry['context'] = 'not a capture marker'
        self.assertEqual(adapter.select_receipt([entry], 'qa', 'a' * 64), {'state': 'unknown'})

    def test_duplicates_malformed_ids_and_over_budget_rejected(self):
        entry = self.entry()
        with self.assertRaises(ValueError):
            adapter.select_receipt([entry, dict(entry)], 'qa', 'a' * 64)
        entry['qa_id'] = 'not-an-official-id'
        with self.assertRaises(ValueError):
            adapter.select_receipt([entry], 'qa', 'a' * 64)
        with self.assertRaises(ValueError):
            adapter.select_receipt([{}] * 257, 'qa', 'a' * 64)


class ReceiptAuthorization(unittest.IsolatedAsyncioTestCase):
    async def test_owner_scope_and_unavailable_cache(self):
        from fastapi import HTTPException

        class App:
            def get(self, path):
                def register(handler):
                    self.handler = handler
                    return handler
                return register

        for mode in ('saved', 'missing', 'foreign_owner', 'foreign_dataset', 'no_dataset', 'cache_unavailable', 'wrong_instance'):
            with self.subTest(mode=mode):
                instance, dataset, user_id = uuid4(), uuid4(), uuid4()
                user = types.SimpleNamespace(id=user_id)
                entry = ReceiptSelection().entry()
                row = types.SimpleNamespace(user_id=user_id, dataset_id=dataset)
                if mode == 'missing': row = None
                if mode == 'foreign_owner': row.user_id = uuid4()
                if mode == 'foreign_dataset': row.dataset_id = uuid4()
                authorize = AsyncMock(return_value=None if mode == 'no_dataset' else object())
                session_row = AsyncMock(return_value=row)
                manager = types.SimpleNamespace(is_available=mode != 'cache_unavailable', get_session=AsyncMock(return_value=[entry]), get_agent_trace_session=AsyncMock(return_value=[]))
                modules = {
                    'cognee.modules.data.methods': types.SimpleNamespace(get_authorized_dataset=authorize),
                    'cognee.modules.session_lifecycle.metrics': types.SimpleNamespace(get_session_row=session_row),
                    'cognee.infrastructure.session.get_session_manager': types.SimpleNamespace(get_session_manager=lambda: manager),
                }
                app = App()
                adapter.install(app, str(instance), lambda: user)
                with patch.dict(sys.modules, modules):
                    call = app.handler(dataset, 'pi-' + 'c' * 48, 'qa', 'a' * 64, uuid4() if mode == 'wrong_instance' else instance, user)
                    if mode in ('saved', 'missing'):
                        result = await call
                        self.assertEqual(result['state'], 'saved' if mode == 'saved' else 'unknown')
                        self.assertEqual(result['user'], str(user_id))
                    else:
                        with self.assertRaises(HTTPException): await call
                if mode == 'wrong_instance':
                    authorize.assert_not_awaited()
                elif mode != 'no_dataset':
                    session_row.assert_awaited_once_with(session_id='pi-' + 'c' * 48, user_id=user_id)
                if mode == 'saved':
                    manager.get_session.assert_awaited_once_with(user_id=str(user_id), session_id='pi-' + 'c' * 48, formatted=False, last_n=257)
                else:
                    manager.get_session.assert_not_awaited()
                manager.get_agent_trace_session.assert_not_awaited()


class ReceiptHTTP(unittest.TestCase):
    def test_actual_fastapi_route_auth_and_query_contract(self):
        from fastapi import FastAPI, Header, HTTPException
        from fastapi.testclient import TestClient
        instance, dataset, user_id = uuid4(), uuid4(), uuid4()
        user = types.SimpleNamespace(id=user_id)

        async def authenticated(authorization: str | None = Header(default=None)):
            if authorization != 'Bearer synthetic-token':
                raise HTTPException(401, 'authentication_required')
            return user

        row = types.SimpleNamespace(user_id=user_id, dataset_id=dataset)
        authorize = AsyncMock(return_value=object())
        session_row = AsyncMock(return_value=row)
        manager = types.SimpleNamespace(is_available=True, get_session=AsyncMock(return_value=[ReceiptSelection().entry()]))
        modules = {
            'cognee.modules.data.methods': types.SimpleNamespace(get_authorized_dataset=authorize),
            'cognee.modules.session_lifecycle.metrics': types.SimpleNamespace(get_session_row=session_row),
            'cognee.infrastructure.session.get_session_manager': types.SimpleNamespace(get_session_manager=lambda: manager),
        }
        app = FastAPI()
        adapter.install(app, str(instance), authenticated)
        path = f'/api/v1/memlane/datasets/{dataset}/capture-receipts/pi-' + 'c' * 48 + '/qa/' + 'a' * 64
        with patch.dict(sys.modules, modules), TestClient(app) as client:
            denied = client.get(path, params={'expected_instance': str(instance)})
            self.assertEqual(denied.status_code, 401)
            authorize.assert_not_awaited()
            response = client.get(path, params={'expected_instance': str(instance)}, headers={'Authorization': 'Bearer synthetic-token'})
            self.assertEqual(response.status_code, 200)
            self.assertEqual(response.json()['state'], 'saved')
            self.assertEqual(response.json()['user'], str(user_id))
            self.assertNotIn('question', response.json())
        session_row.assert_awaited_once_with(session_id='pi-' + 'c' * 48, user_id=user_id)


if __name__ == '__main__':
    unittest.main()
