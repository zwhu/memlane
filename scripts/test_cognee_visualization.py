"""Synthetic SDK boundary/ASGI checks; never import a real database engine."""
import asyncio
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import cognee_visualization as module
from test_cognee_submission import fake_modules


class Tests(unittest.TestCase):
    def test_scope_probe_checks_existing_read_acl_without_graph_fetch(self):
        from fastapi import FastAPI
        from fastapi.testclient import TestClient
        instance, dataset = str(uuid4()), uuid4()
        user = SimpleNamespace(id=uuid4())
        state = {'denied': False, 'calls': 0}
        async def authorized(ids, permission, principal, strict):
            self.assertEqual(ids, [dataset]); self.assertEqual(permission, 'read')
            self.assertIs(principal, user); self.assertTrue(strict)
            state['calls'] += 1
            if state['denied']:
                raise PermissionError('PRIVATE_SCOPE_FAILURE')
            return [SimpleNamespace(id=dataset)]
        sdk = fake_modules({
            'cognee.modules.users.methods': {'get_authenticated_user': lambda: user},
            'cognee.modules.data.methods': {'get_authorized_existing_datasets': authorized},
        })
        with patch.dict(sys.modules, sdk):
            app = FastAPI(); module.install(app, instance)
            with TestClient(app) as client:
                path = '/api/v1/memlane/datasets/' + str(dataset) + '/visualization-scope'
                response = client.get(path, params={'expected_instance': instance})
                self.assertEqual(response.json(), {'instance': instance, 'dataset': str(dataset)})
                self.assertEqual(response.status_code, 200)
                state['denied'] = True
                denied = client.get(path, params={'expected_instance': instance})
                self.assertEqual(denied.status_code, 409)
                self.assertNotIn('PRIVATE', denied.text)
                self.assertEqual(state['calls'], 2)

    def test_exact_dataset_no_unscoped_fallback_and_post_read_revocation(self):
        for case in ('ok', 'absent', 'wrong_dataset', 'revoked', 'owner_changed'):
            with self.subTest(case=case):
                dataset = SimpleNamespace(id=uuid4(), owner_id=uuid4())
                user = SimpleNamespace(id=uuid4())
                scopes, fetches = [], []
                payload = {'nodes': [], 'links': [], 'color_maps': {}, 'schema_graph': {},
                           'provenance_index': {'original': {'id': 'unchanged'}}}
                async def authorized(ids, permission, principal, strict):
                    self.assertEqual(ids, [dataset.id]); self.assertEqual(permission, 'read')
                    self.assertIs(principal, user); self.assertTrue(strict)
                    scopes.append(ids)
                    if case == 'absent' or case == 'revoked' and len(scopes) == 2:
                        return []
                    if case == 'wrong_dataset':
                        return [SimpleNamespace(id=uuid4(), owner_id=dataset.owner_id)]
                    if case == 'owner_changed' and len(scopes) == 2:
                        return [SimpleNamespace(id=dataset.id, owner_id=uuid4())]
                    return [dataset]
                async def fetch(selected, **kwargs):
                    self.assertIs(selected, dataset)
                    self.assertEqual(kwargs, dict(full=False, seed_node_ids=['actual-node'],
                        neighborhood_depth=2, neighborhood_seed_top_k=16, max_nodes=1000))
                    fetches.append(selected)
                    return 'official-graph-data'
                def build(data, search_events):
                    self.assertEqual(data, 'official-graph-data'); self.assertEqual(search_events, [])
                    return payload
                sdk = fake_modules({
                    'cognee.modules.data.methods': {'get_authorized_existing_datasets': authorized},
                    'cognee.api.v1.visualize.visualize': {'fetch_dataset_graph_data': fetch},
                    'cognee.modules.visualization.cognee_network_visualization': {'build_visualization_payload': build},
                })
                with patch.dict(sys.modules, sdk):
                    if case == 'ok':
                        self.assertIs(asyncio.run(module.read_visualization(dataset.id, user, 1000, ['actual-node'])), payload)
                    else:
                        with self.assertRaises(PermissionError):
                            asyncio.run(module.read_visualization(dataset.id, user, 1000, ['actual-node']))
                self.assertEqual(len(fetches), 0 if case in ('absent', 'wrong_dataset') else 1)

    def test_asgi_scope_and_errors_do_not_echo_sdk_data(self):
        from fastapi import FastAPI
        from fastapi.testclient import TestClient
        instance, dataset = str(uuid4()), str(uuid4())
        user = SimpleNamespace(id=uuid4())
        sdk = fake_modules({'cognee.modules.users.methods': {'get_authenticated_user': lambda: user}})
        calls = []
        async def read(selected, principal, limit, seeds):
            calls.append(selected)
            self.assertEqual(str(selected), dataset); self.assertIs(principal, user)
            if seeds == ['failure']:
                raise RuntimeError('PRIVATE_STORAGE_BODY')
            return {'nodes': [], 'links': [], 'color_maps': {}}
        with patch.dict(sys.modules, sdk), patch.object(module, 'read_visualization', read):
            app = FastAPI(); module.install(app, instance)
            with TestClient(app) as client:
                path = '/api/v1/memlane/datasets/' + dataset + '/visualization'
                response = client.get(path, params={'expected_instance': instance})
                self.assertEqual(response.status_code, 200)
                self.assertEqual(response.json()['scope'], 'bounded_subgraph')
                self.assertEqual(response.json()['dataset'], dataset)
                self.assertEqual(client.get(path, params={'expected_instance': str(uuid4())}).status_code, 409)
                self.assertEqual(client.get(path, params={'expected_instance': instance, 'max_nodes': 1001}).status_code, 422)
                failed = client.get(path, params={'expected_instance': instance, 'seed_node_ids': 'failure'})
                self.assertEqual(failed.status_code, 409); self.assertNotIn('PRIVATE', failed.text)
                self.assertEqual(len(calls), 2)


if __name__ == '__main__':
    unittest.main()
