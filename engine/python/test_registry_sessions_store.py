import asyncio
import json
from pathlib import Path
import tempfile
import unittest
import uuid

from project_control import Registry
from registry_sessions_store import RegistrySessionsStore


class _FakeOfficial:
    def __init__(self):
        self.owner_id = str(uuid.uuid4())
        self.datasets = {}

    async def request(self, method, path, body=None, token_file=None, *, adapter_token=None):
        if path == '/api/v1/users/me':
            return {'id': self.owner_id}
        if path == '/api/v1/datasets' and method == 'GET':
            return list(self.datasets.values())
        if path == '/api/v1/datasets' and method == 'POST':
            ds_id = str(uuid.uuid4())
            row = {'id': ds_id, 'name': body['name'], 'ownerId': self.owner_id}
            self.datasets[ds_id] = row
            return row
        return []

    async def owned_dataset(self, name, dataset_id=None):
        if dataset_id and dataset_id in self.datasets:
            return self.datasets[dataset_id]
        for row in self.datasets.values():
            if row['name'] == name:
                return row
        ds_id = str(uuid.uuid4())
        row = {'id': ds_id, 'name': name, 'ownerId': self.owner_id}
        self.datasets[ds_id] = row
        return row

    async def sync_acl(self, agent, dataset, permissions):
        return None

    async def effective_permissions(self, agent, dataset):
        return set()


class RegistrySessionsStoreTests(unittest.IsolatedAsyncioTestCase):
    def test_store_roundtrip_and_permissions(self):
        with tempfile.TemporaryDirectory() as folder:
            db_path = Path(folder) / 'registry-sessions.sqlite3'
            store = RegistrySessionsStore(db_path)
            try:
                self.assertEqual(db_path.stat().st_mode & 0o777, 0o600)
                store.upsert_session('["a","h1"]', 'p1')
                store.upsert_session('["a","h2"]', 'p1')
                store.upsert_session('["a","h3"]', 'p2')
                store.upsert_operation('op-1', {'status': 'complete', 'signature': 'sig'})
                store.upsert_receipt('a', {'received_at': 123.0, 'project_id': 'p1'})
                self.assertEqual(store.sessions_for_project('p1'), ['["a","h1"]', '["a","h2"]'])
                sessions, ops, receipts = store.load_all()
                self.assertEqual(sessions, {'["a","h1"]': 'p1', '["a","h2"]': 'p1', '["a","h3"]': 'p2'})
                self.assertEqual(ops['op-1']['status'], 'complete')
                self.assertEqual(receipts['a']['project_id'], 'p1')
            finally:
                store.close()

    async def test_registry_migrates_legacy_json_and_keeps_json_slim(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            reg_path = root / 'project-registry.json'
            repo = root / 'repo'
            repo.mkdir()
            (repo / '.git').mkdir()

            # Write a legacy v2 JSON containing sessions, connection_operations, and connection_receipts
            legacy = {
                'schema_version': 2,
                'service_namespace': str(uuid.uuid4()),
                'revision': 1,
                'defaults': {'access': 'read', 'capture': False, 'auto_process': False},
                'agents': {'a': {'name': 'Agent A', 'principal_id': str(uuid.uuid4()), 'key_file': str(root / 'k')}},
                'projects': {},
                'sessions': {json.dumps(['a', 'legacy-host']): 'legacy-proj'},
                'connection_operations': {'op-legacy': {'status': 'complete', 'signature': 's'}},
                'connection_receipts': {'a': {'received_at': 1.0, 'project_id': 'legacy-proj', 'host_session_key': 'legacy-host'}},
                'connections': {},
            }
            reg_path.write_text(json.dumps(legacy), encoding='utf-8')
            reg_path.chmod(0o600)

            official = _FakeOfficial()
            reg = Registry(reg_path, official)
            try:
                # Legacy entries seamlessly loaded in memory
                self.assertEqual(reg.state['sessions'][json.dumps(['a', 'legacy-host'])], 'legacy-proj')
                self.assertEqual(reg.state['connection_operations']['op-legacy']['status'], 'complete')
                self.assertEqual(reg.state['connection_receipts']['a']['project_id'], 'legacy-proj')

                # Slim JSON strips high-churn dynamic keys
                on_disk = json.loads(reg_path.read_text(encoding='utf-8'))
                self.assertNotIn('sessions', on_disk)
                self.assertNotIn('connection_operations', on_disk)
                self.assertNotIn('connection_receipts', on_disk)

                # Adding 100 new sessions via resolve() does O(1) SQLite upsert and keeps JSON size tiny
                await reg.resolve('a', str(repo), 'sess-0', explicit=True)
                json_size_before = reg_path.stat().st_size
                for i in range(1, 100):
                    await reg.resolve('a', str(repo), f'sess-{i}', explicit=True)
                json_size_after = reg_path.stat().st_size
                self.assertEqual(json_size_before, json_size_after)
                self.assertLess(json_size_after, 8192)
            finally:
                reg.close()

            # Reopen verifies all 101 sessions survived cleanly in registry-sessions.sqlite3
            reg2 = Registry(reg_path, official)
            try:
                self.assertEqual(len(reg2.state['sessions']), 101)
                self.assertIn(json.dumps(['a', 'sess-99']), reg2.state['sessions'])
                self.assertIn(json.dumps(['a', 'legacy-host']), reg2.state['sessions'])
            finally:
                reg2.close()


if __name__ == '__main__':
    unittest.main()
