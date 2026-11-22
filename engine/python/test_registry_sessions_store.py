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

    async def test_rollback_and_reupgrade_preserves_newer_json_same_key_records(self):
        import os
        import time

        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            reg_path = root / 'project-registry.json'
            repo1 = root / 'repo1'
            repo2 = root / 'repo2'
            repo1.mkdir(); (repo1 / '.git').mkdir()
            repo2.mkdir(); (repo2 / '.git').mkdir()

            official = _FakeOfficial()
            reg = Registry(reg_path, official)
            try:
                reg.state['agents'] = {'a': {'name': 'Agent A', 'principal_id': str(uuid.uuid4()), 'key_file': str(root / 'k')}}
                reg.save()
                r1 = await reg.resolve('a', str(repo1), 'shared-host', explicit=True)
                r2 = await reg.resolve('a', str(repo2), 'sqlite-only-host', explicit=True)
                p1, p2 = r1['project_id'], r2['project_id']
                reg.state['connection_operations'] = {'op-1': {'status': 'unknown', 'updated_at': 100.0}}
                reg.state['connection_receipts'] = {'a': {'received_at': 100.0, 'project_id': p1, 'host_session_key': 'shared-host'}}
                reg.save()
                rev_before = reg.state['revision']
            finally:
                reg.close()

            # Simulate rollback to legacy JSON-only version that updated the SAME keys with newer values.
            slim = json.loads(reg_path.read_text(encoding='utf-8'))
            slim.pop('_sessions_store_sync', None)
            slim['revision'] = rev_before + 1
            slim['sessions'] = {
                json.dumps(['a', 'shared-host']): p2,
                json.dumps(['a', 'json-only-host']): p2,
            }
            slim['connection_operations'] = {'op-1': {'status': 'complete', 'updated_at': 500.0}}
            slim['connection_receipts'] = {'a': {'received_at': 500.0, 'project_id': p2, 'host_session_key': 'shared-host'}}
            reg_path.write_text(json.dumps(slim), encoding='utf-8')
            reg_path.chmod(0o600)
            future_ts = time.time() + 10.0
            os.utime(reg_path, (future_ts, future_ts))

            # Re-upgrade must keep newer JSON values for conflicting keys AND preserve sqlite-only keys.
            reg2 = Registry(reg_path, official)
            try:
                self.assertEqual(reg2.state['sessions'][json.dumps(['a', 'shared-host'])], p2)
                self.assertEqual(reg2.state['sessions'][json.dumps(['a', 'json-only-host'])], p2)
                self.assertEqual(reg2.state['sessions'][json.dumps(['a', 'sqlite-only-host'])], p2)
                self.assertEqual(reg2.state['connection_operations']['op-1']['status'], 'complete')
                self.assertEqual(reg2.state['connection_receipts']['a']['project_id'], p2)
                self.assertEqual(reg2.state['connection_receipts']['a']['received_at'], 500.0)
                # Same-revision rollback with newer mtime also lets JSON win over stale SQLite.
                rev_mid = reg2.state['revision']
            finally:
                reg2.close()

            slim2 = json.loads(reg_path.read_text(encoding='utf-8'))
            slim2.pop('_sessions_store_sync', None)
            slim2['revision'] = rev_mid
            slim2['sessions'] = {json.dumps(['a', 'shared-host']): p1}
            reg_path.write_text(json.dumps(slim2), encoding='utf-8')
            reg_path.chmod(0o600)
            future_ts2 = time.time() + 20.0
            os.utime(reg_path, (future_ts2, future_ts2))

            reg3 = Registry(reg_path, official)
            try:
                self.assertEqual(reg3.state['sessions'][json.dumps(['a', 'shared-host'])], p1)
            finally:
                reg3.close()

    def test_sessions_for_project_uses_covering_index_and_keyset_pagination(self):
        with tempfile.TemporaryDirectory() as folder:
            db_path = Path(folder) / 'registry-sessions.sqlite3'
            store = RegistrySessionsStore(db_path)
            try:
                for i in range(20):
                    store.upsert_session(json.dumps(['a', f'h-{i:02d}']), 'p1')
                for i in range(30):
                    store.upsert_session(json.dumps(['a', f'other-{i:02d}']), 'p2')
                eqp = store.db.execute(
                    '''EXPLAIN QUERY PLAN
                       SELECT session_key FROM registry_sessions
                       WHERE project_id = ? AND session_key > ?
                       ORDER BY session_key ASC LIMIT ?''',
                    ('p1', '', 5),
                ).fetchall()
                plan_text = ' '.join(str(r[-1]) for r in eqp)
                self.assertIn('idx_registry_sessions_project', plan_text)
                self.assertNotIn('TEMP B-TREE', plan_text)

                collected = []
                cursor = ''
                while True:
                    page = store.sessions_for_project_page('p1', after_key=cursor, limit=6)
                    if not page:
                        break
                    collected.extend(page)
                    cursor = page[-1]
                self.assertEqual(collected, store.sessions_for_project('p1'))
                self.assertEqual(len(collected), 20)
            finally:
                store.close()


if __name__ == '__main__':
    unittest.main()
