"""Unit tests for ui-reading-state.sqlite3: double-slash hash, nanosecond precision, atomic migration & compaction."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import stat
import tempfile
import unittest
import uuid

from catalog_index import CatalogIndex
from reading_state_store import ReadingStateStore, knowledge_read_scope_hash


class ReadingStateStoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.official_data = self.root / 'official-store'
        self.db_dir = self.official_data / 'system' / 'databases'
        self.db_dir.mkdir(parents=True)
        self.cognee_db = self.db_dir / 'cognee_db'
        self.ds = str(uuid.uuid4())
        self.project_id = 'proj-alpha'
        # Matches Go readingIdentityPair: "<url>/<sha256(token)>/" (trailing slash -> double slash "//"!)
        token_sha = hashlib.sha256(b'synthetic-token').hexdigest()
        self.identity = f'http://127.0.0.1:61255/{token_sha}/'

        con = sqlite3.connect(self.cognee_db)
        con.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), name VARCHAR, label VARCHAR,
                raw_data_location VARCHAR, external_metadata JSON, system_metadata JSON,
                node_set JSON, pipeline_status JSON, updated_at DATETIME, created_at DATETIME,
                content_hash VARCHAR
            );
        ''')
        con.commit()
        con.close()

    def tearDown(self):
        self.tmp.cleanup()

    def _add_knowledge(self, doc_id: str, created_at: str):
        con = sqlite3.connect(self.cognee_db)
        con.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(doc_id).hex, uuid.UUID(self.ds).hex, 'k.md', None, '',
                '{}', json.dumps({'lesson_type': 'state', 'session_date': '2026-11-20'}),
                json.dumps(['session_learnings']), '{}', created_at, created_at, 'h',
            ),
        )
        con.commit()
        con.close()

    def test_double_slash_scope_hash_golden_compatibility(self):
        # Exact Go formula: sha256(identity + "/" + p.ID + "/" + p.Dataset)
        expected = hashlib.sha256(f'{self.identity}/{self.project_id}/{self.ds}'.encode('utf-8')).hexdigest()
        actual = knowledge_read_scope_hash(self.identity, self.project_id, self.ds)
        self.assertEqual(actual, expected)
        # Verify stripping trailing slash would NOT match (double-slash is load-bearing!)
        wrong_single_slash = hashlib.sha256(f"{self.identity.rstrip('/')}/{self.project_id}/{self.ds}".encode('utf-8')).hexdigest()
        self.assertNotEqual(actual, wrong_single_slash)

    def test_local_atomic_migration_zero_enabled_at_mtime_and_reupgrade_without_phantom_unreads(self):
        d1 = str(uuid.uuid4())
        d2 = str(uuid.uuid4())
        d3 = str(uuid.uuid4())
        d_orphan = str(uuid.uuid4())
        d_invalid_ts = str(uuid.uuid4())

        # Exact 1-nanosecond boundary around enabled_at
        self._add_knowledge(d1, '2026-11-20T10:00:00.123456789Z')
        self._add_knowledge(d2, '2026-11-20T10:00:00.123456790Z')  # +1 ns after enabled_at!
        self._add_knowledge(d3, '2026-11-20T12:00:00.000000000Z')
        self._add_knowledge(d_invalid_ts, '')  # empty/invalid created_at -> must be unread until marked read!

        cat = CatalogIndex(self.root / 'catalog-index.sqlite3', official_data=self.official_data)
        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3')
        try:
            self.assertEqual(stat.S_IMODE(os.stat(store.path).st_mode), 0o600)
            self.assertEqual(store.db.execute('PRAGMA journal_size_limit').fetchone()[0], 4194304)
            cat.sync([self.ds])

            scope_hash = knowledge_read_scope_hash(self.identity, self.project_id, self.ds)
            legacy_file = self.root / f'knowledge-read-{scope_hash}.json'
            legacy_file.write_text(
                json.dumps({
                    'version': 1,
                    'initialized': True,
                    'enabled_at': '2026-11-20T10:00:00.123456789Z',
                    'knowledge_ids': {
                        d1: True,
                        d_orphan: True,  # Not in catalog_docs -> must NEVER be dropped!
                        d3: False,
                    },
                }),
                encoding='utf-8',
            )

            res = store.migrate_local_file(self.root, self.identity, self.project_id, self.ds, catalog_index=cat)
            self.assertEqual(res['status'], 'migrated')
            self.assertEqual(res['imported_items'], 3)
            self.assertFalse(legacy_file.exists())
            self.assertTrue((self.root / f'knowledge-read-{scope_hash}.json.migrated.bak').is_file())

            # Nanosecond boundary: d1 (== enabled_at) is READ; d2 (+1ns) is UNREAD; d3 (explicit False) is UNREAD;
            # d_invalid_ts (empty created_at) is UNREAD!
            unread = store.compute_unread_ids(self.identity, self.project_id, self.ds, cat)
            self.assertEqual(unread, {d2, d3, d_invalid_ts})

            # Orphan explicit is_read=1 preserved in ui_read_items & export
            exported = store.export_scope(scope_hash)
            self.assertTrue(exported['knowledge_ids'].get(d_orphan))

            # Simulate rollback to old app -> user reads d2 -> old app writes a NEW legacy file with later enabled_at!
            legacy_file.write_text(
                json.dumps({
                    'version': 1,
                    'initialized': True,
                    'enabled_at': '2026-11-20T11:00:00.000000000Z',  # > T_old (d2 is implicitly read here!)
                    'knowledge_ids': {d3: True},  # user also read d3 in old version
                }),
                encoding='utf-8',
            )
            res2 = store.migrate_local_file(self.root, self.identity, self.project_id, self.ds, catalog_index=cat)
            self.assertEqual(res2['status'], 'migrated')
            # Conflict merge materialized d2 as read (0 phantom unreads!) and explicit True won for d3!
            unread_after = store.compute_unread_ids(self.identity, self.project_id, self.ds, cat)
            self.assertEqual(unread_after, {d_invalid_ts})

            # Legacy file with initialized=True and zero enabled_at falls back to file mtime UTC
            proj2 = 'proj-zero-ts'
            sh2 = knowledge_read_scope_hash(self.identity, proj2, self.ds)
            f2 = self.root / f'knowledge-read-{sh2}.json'
            f2.write_text(json.dumps({
                'version': 1, 'initialized': True, 'enabled_at': '0001-01-01T00:00:00Z', 'knowledge_ids': {},
            }), encoding='utf-8')
            os.utime(f2, ns=(1700000000_123456000, 1700000000_123456000))
            store.migrate_local_file(self.root, self.identity, proj2, self.ds)
            sc2 = store.get_scope(sh2)
            self.assertTrue(sc2['enabled_at_ns'].startswith('2023-11-14T22:13:20.123456'))
        finally:
            store.close()
            cat.close()

    def test_two_stage_staging_import_and_horizon_compaction(self):
        # Populate 50 docs after enabled_at + 1 invalid-ts doc
        docs = [str(uuid.uuid4()) for _ in range(50)]
        for idx, did in enumerate(docs):
            self._add_knowledge(did, f'2026-11-21T10:{idx // 60:02d}:{idx % 60:02d}.000000000Z')
        d_inv = str(uuid.uuid4())
        self._add_knowledge(d_inv, 'not-a-timestamp')

        cat = CatalogIndex(self.root / 'catalog-index.sqlite3', official_data=self.official_data)
        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3')
        try:
            cat.sync([self.ds])

            # Two-stage staging import: nothing visible in ui_read_scopes before commit
            b = store.begin_import(
                self.identity, self.project_id, self.ds,
                initialized=True, enabled_at='2026-11-21T09:00:00.000000000Z',
            )
            store.stage_import_chunk(b['batch_id'], [{'doc_id': did, 'is_read': True} for did in docs[:20]])
            store.stage_import_chunk(b['batch_id'], [{'doc_id': did, 'is_read': True} for did in docs[20:45]])
            self.assertIsNone(store.get_scope(b['scope_hash']))

            committed = store.commit_import(b['batch_id'], catalog_index=cat)
            self.assertEqual(committed['committed'], 45)

            # Horizon compacted past docs[:45]! Only docs[45:] + d_inv are unread!
            unread = store.compute_unread_ids(self.identity, self.project_id, self.ds, cat)
            self.assertEqual(unread, set(docs[45:]) | {d_inv})

            # Redundant is_read=1 rows below horizon were pruned!
            stored_rows = store.db.execute(
                'SELECT COUNT(*) FROM ui_read_items WHERE scope_hash = ?', (b['scope_hash'],)
            ).fetchone()[0]
            self.assertEqual(stored_rows, 0)

            # Mark invalid-ts doc read -> must stay in ui_read_items even after compaction/mark_all_read!
            store.mark_read(self.identity, self.project_id, self.ds, [d_inv], catalog_index=cat)
            self.assertEqual(store.compute_unread_ids(self.identity, self.project_id, self.ds, cat), set(docs[45:]))

            # Mark all read -> 0 unread, d_inv still stays read!
            res_all = store.mark_all_read(self.identity, self.project_id, self.ds, catalog_index=cat)
            self.assertEqual(res_all['unread_count'], 0)
            self.assertEqual(store.compute_unread_ids(self.identity, self.project_id, self.ds, cat), set())
        finally:
            store.close()
            cat.close()


if __name__ == '__main__':
    unittest.main()
