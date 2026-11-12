"""Real ASGI and synthetic official dataset/graph engines; no source scan."""
from contextlib import asynccontextmanager
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import cognee_code_scope as module
from test_cognee_submission import fake_modules


class Tests(unittest.TestCase):
    def test_strict_existing_write_scope_and_empty_observation(self):
        from fastapi import FastAPI
        from fastapi.testclient import TestClient
        for case in ('empty', 'data', 'nodes', 'edges', 'unknown_counts', 'unknown_data', 'denied', 'wrong_dataset'):
            with self.subTest(case=case):
                instance, dataset, user_id, owner_id = uuid4(), uuid4(), uuid4(), uuid4()
                user = SimpleNamespace(id=user_id)
                calls = []
                async def authorized(ids, permission, principal, strict):
                    self.assertEqual(ids, [dataset]); self.assertEqual(permission, 'write')
                    self.assertIs(principal, user); self.assertTrue(strict)
                    calls.append('authorize')
                    if case == 'denied':
                        raise PermissionError('PRIVATE_STORAGE_BODY')
                    return [SimpleNamespace(id=uuid4() if case == 'wrong_dataset' else dataset, owner_id=owner_id)]
                async def data(value, limit):
                    self.assertEqual(value, dataset); self.assertEqual(limit, 1); calls.append('data')
                    return None if case == 'unknown_data' else [object()] if case == 'data' else []
                @asynccontextmanager
                async def scoped(value, owner):
                    self.assertEqual(value, dataset); self.assertEqual(owner, owner_id)
                    calls.append('context'); yield
                async def counts():
                    calls.append('counts')
                    return None if case == 'unknown_counts' else (1, 0) if case == 'nodes' else (0, 1) if case == 'edges' else (0, 0)
                async def graph():
                    return SimpleNamespace(get_graph_counts=counts)
                sdk = fake_modules({
                    'cognee.modules.users.methods': {'get_authenticated_user': lambda: user},
                    'cognee.modules.data.methods': {'get_authorized_existing_datasets': authorized, 'get_dataset_data': data},
                    'cognee.context_global_variables': {'set_database_global_context_variables': scoped},
                    'cognee.infrastructure.databases.graph': {'get_graph_engine': graph},
                })
                with patch.dict(sys.modules, sdk):
                    app = FastAPI(); module.install(app, str(instance))
                    with TestClient(app) as client:
                        response = client.get('/api/v1/memlane/code/scope', params={'dataset': str(dataset), 'expected_instance': str(instance)})
                        if case in ('denied', 'wrong_dataset', 'unknown_counts', 'unknown_data'):
                            self.assertEqual(response.status_code, 409); self.assertNotIn('PRIVATE', response.text)
                        else:
                            self.assertEqual(response.status_code, 200)
                            self.assertEqual(response.json(), {'instance': str(instance), 'dataset': str(dataset), 'user': str(user_id), 'empty': case == 'empty'})
                        if case in ('denied', 'wrong_dataset'):
                            self.assertEqual(calls, ['authorize'])


if __name__ == '__main__':
    unittest.main()
