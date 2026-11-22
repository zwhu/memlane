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
            self.assertTrue(res_all['unread_count_known'])
            self.assertGreater(res_all['state_rev'], 0)
            self.assertEqual(store.compute_unread_ids(self.identity, self.project_id, self.ds, cat), set())
        finally:
            store.close()
            cat.close()

    def test_cold_start_race_custom_dir_no_precreate_and_late_backdated_doc_after_compaction(self):
        d_old = str(uuid.uuid4())
        d_mid = str(uuid.uuid4())
        d_new = str(uuid.uuid4())
        self._add_knowledge(d_old, '2026-11-20T09:00:00Z')
        self._add_knowledge(d_mid, '2026-11-20T10:00:00Z')
        self._add_knowledge(d_new, '2026-11-20T11:00:00Z')

        cat = CatalogIndex(self.root / 'catalog-index.sqlite3', official_data=self.official_data)
        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3', state_dir=self.root)
        try:
            cat.sync([self.ds])

            # 1. Default-dir cold-start race (CC-P0-01): overview calls compute_unread_ids BEFORE migrate_local_file!
            sh = knowledge_read_scope_hash(self.identity, self.project_id, self.ds)
            legacy_file = self.root / f'knowledge-read-{sh}.json'
            legacy_file.write_text(json.dumps({
                'version': 1,
                'initialized': True,
                'enabled_at': '2026-11-20T09:30:00Z',
                'knowledge_ids': {d_mid: True},
            }), encoding='utf-8')
            # Cold read MUST auto-migrate legacy_file first and preserve d_new as unread!
            unread_cold = store.compute_unread_ids(self.identity, self.project_id, self.ds, cat)
            self.assertEqual(unread_cold, {d_new})
            self.assertFalse(legacy_file.exists())
            # Subsequent migrate_local_file reports already_migrated cleanly
            res_dup = store.migrate_local_file(self.root, self.identity, self.project_id, self.ds, catalog_index=cat)
            self.assertEqual(res_dup['status'], 'already_migrated')

            # 2. Custom-dir migration (PY-P0-04): missing file returns no_legacy_file and MUST NOT pre-create scope!
            proj_custom = 'proj-custom'
            sh_custom = knowledge_read_scope_hash(self.identity, proj_custom, self.ds)
            res_missing = store.migrate_local_file(self.root, self.identity, proj_custom, self.ds, catalog_index=cat)
            self.assertEqual(res_missing['status'], 'no_legacy_file')
            self.assertIsNone(store.get_scope(sh_custom))

            # Two-stage custom-dir import with expected_items check + idempotent retry + verify_project
            b = store.begin_import(self.identity, proj_custom, self.ds, initialized=True, enabled_at='2026-11-20T09:30:00Z')
            store.stage_import_chunk(b['batch_id'], [{'doc_id': d_mid, 'is_read': True}])
            from project_policy import PolicyError
            with self.assertRaises(PolicyError) as mismatch:
                store.commit_import(b['batch_id'], catalog_index=cat, expected_items=2)
            self.assertEqual(mismatch.exception.status, 409)
            c1 = store.commit_import(b['batch_id'], catalog_index=cat, expected_items=1)
            self.assertEqual(c1['unread_count'], 1)
            self.assertGreater(c1['state_rev'], 0)
            c2 = store.commit_import(b['batch_id'], catalog_index=cat, expected_items=1)
            self.assertEqual(c2['committed'], c1['committed'])

            # 3. Late-arriving backdated doc after horizon compaction (PY-P0-03):
            # Mark d_new read -> horizon compacts up to 11:00:00Z and locks horizon_max_rowid.
            mr = store.mark_read(self.identity, proj_custom, self.ds, [d_new], catalog_index=cat)
            self.assertTrue(mr['unread_count_known'])
            self.assertEqual(mr['unread_count'], 0)
            self.assertGreater(mr['state_rev'], c1['state_rev'])

            # Now a late-arriving knowledge doc with created_at = 10:30:00Z (< compacted horizon 11:00Z!) lands in cognee_db
            d_late = str(uuid.uuid4())
            self._add_knowledge(d_late, '2026-11-20T10:30:00Z')
            cat.sync([self.ds])
            # Branch B2 (cognee_rowid > horizon_max_rowid) MUST surface d_late as UNREAD!
            self.assertEqual(store.compute_unread_ids(self.identity, proj_custom, self.ds, cat), {d_late})
            late_docs = cat.fetch_docs_by_ids(self.ds, [d_late, d_new])
            flags = store.page_unread_flags(self.identity, proj_custom, self.ds, late_docs, catalog_index=cat)
            self.assertTrue(flags[d_late])
            self.assertFalse(flags[d_new])

            # 4. Re-upgrade merge with older enabled_at MUST NOT manufacture phantom unreads for compacted docs!
            b_re = store.begin_import(self.identity, proj_custom, self.ds, initialized=True, enabled_at='2026-11-20T09:00:00Z')
            store.commit_import(b_re['batch_id'], catalog_index=cat)
            self.assertEqual(store.compute_unread_ids(self.identity, proj_custom, self.ds, cat), {d_late})

            # 5. mark_read without catalog_index reports unread_count_known=False
            mr_nocat = store.mark_read(self.identity, proj_custom, self.ds, [d_late], catalog_index=None)
            self.assertFalse(mr_nocat['unread_count_known'])
        finally:
            store.close()
            cat.close()

    def test_round2_doc_seq_late_unread_preservation_cas_mark_all_and_commit_guard(self):
        """Regression for R2-PY-P0-02, CC2-P0-01, R2-PY-P1-07, and Contract #2."""
        from project_policy import PolicyError

        d1 = str(uuid.uuid4())
        d2 = str(uuid.uuid4())
        d3 = str(uuid.uuid4())
        self._add_knowledge(d1, '2026-11-20T10:00:00Z')
        self._add_knowledge(d2, '2026-11-20T11:00:00Z')
        self._add_knowledge(d3, '2026-11-20T12:00:00Z')

        cat = CatalogIndex(self.root / 'catalog-index.sqlite3', official_data=self.official_data)
        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3', state_dir=self.root)
        try:
            cat.sync([self.ds])
            # Initialize scope at 09:00:00Z (all 3 unread)
            b0 = store.begin_import(self.identity, 'proj-r2', self.ds, initialized=True, enabled_at='2026-11-20T09:00:00Z')
            c0 = store.commit_import(b0['batch_id'], catalog_index=cat, expected_items=0)
            self.assertEqual(c0['committed'], 0)
            self.assertEqual(c0['unread_count'], 3)
            self.assertGreater(c0['state_rev'], 0)

            # Mark d1 read -> horizon compacts to 10:00:00Z with monotonic doc_seq watermark
            mr1 = store.mark_read(self.identity, 'proj-r2', self.ds, [d1], catalog_index=cat)
            self.assertEqual(mr1['unread_count'], 2)

            # Delete highest-rowid doc (d3) and insert a backdated d_late reusing d3's EXACT cognee_rowid!
            con = sqlite3.connect(self.cognee_db)
            d3_rid = con.execute('SELECT rowid FROM data WHERE id = ?', (uuid.UUID(d3).hex,)).fetchone()[0]
            con.execute('DELETE FROM data WHERE id = ?', (uuid.UUID(d3).hex,))
            d_late = str(uuid.uuid4())
            con.execute(
                '''INSERT INTO data(rowid, id, dataset_id, name, label, raw_data_location,
                   external_metadata, system_metadata, node_set, pipeline_status, updated_at, created_at, content_hash)
                   VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                (
                    d3_rid, uuid.UUID(d_late).hex, uuid.UUID(self.ds).hex, 'late.md', 'Late', '',
                    '{}', json.dumps({'lesson_type': 'state', 'state_key': 'sk-late', 'session_date': '2026-11-20'}),
                    json.dumps(['session_learnings']), '{}', '2026-11-20T09:30:00Z', '2026-11-20T09:30:00Z', 'h-late',
                ),
            )
            con.commit()
            con.close()
            cat.sync([self.ds])

            # Despite reusing d3's exact cognee_rowid, monotonic doc_seq MUST detect d_late as UNREAD!
            u_snap1, rev1 = store.compute_unread_snapshot(self.identity, 'proj-r2', self.ds, cat)
            self.assertEqual(u_snap1, {d2, d_late})
            self.assertGreater(rev1, 0)

            # Now read d2 -> compact_horizon advances horizon from 10:00Z to 11:00Z AND MUST materialize
            # d_late as explicit is_read=0 inside the SAME transaction so d_late stays UNREAD!
            mr2 = store.mark_read(self.identity, 'proj-r2', self.ds, [d2], catalog_index=cat)
            self.assertEqual(mr2['unread_count'], 1)
            u_snap2, rev2 = store.compute_unread_snapshot(self.identity, 'proj-r2', self.ds, cat)
            self.assertEqual(u_snap2, {d_late})
            self.assertEqual(mr2['state_rev'], rev2)
            flags = store.page_unread_flags(
                self.identity, 'proj-r2', self.ds, cat.fetch_docs_by_ids(self.ds, [d1, d2, d_late]), catalog_index=cat
            )
            self.assertTrue(flags[d_late])
            self.assertFalse(flags[d1])
            self.assertFalse(flags[d2])

            # commit_import requiring merge materialization with catalog_index=None MUST fail-closed 503
            # and keep staging rows uncommitted & retryable!
            b_merge = store.begin_import(
                self.identity, 'proj-r2', self.ds, initialized=True, enabled_at='2026-11-20T08:00:00Z'
            )
            store.stage_import_chunk(b_merge['batch_id'], [{'doc_id': d1, 'is_read': True}])
            with self.assertRaises(PolicyError) as err503:
                store.commit_import(b_merge['batch_id'], catalog_index=None, expected_items=1)
            self.assertEqual(err503.exception.status, 503)
            # Retry with catalog_index=cat succeeds cleanly on the SAME batch_id!
            c_retry = store.commit_import(b_merge['batch_id'], catalog_index=cat, expected_items=1)
            self.assertEqual(c_retry['committed'], 1)
            self.assertEqual(c_retry['unread_count'], 1)

            # Single-tx CAS mark_all_read clears all unreads + prunes redundant is_read=1 in one commit
            ma = store.mark_all_read(self.identity, 'proj-r2', self.ds, catalog_index=cat)
            self.assertEqual(ma['unread_count'], 0)
            self.assertGreater(ma['state_rev'], c_retry['state_rev'])
            u_final, rev_final = store.compute_unread_snapshot(self.identity, 'proj-r2', self.ds, cat)
            self.assertEqual(u_final, set())
            self.assertEqual(rev_final, ma['state_rev'])
            # No stale explicit is_read=0 or redundant valid is_read=1 rows remain
            sh = ma['scope_hash']
            leftover = store.db.execute(
                'SELECT COUNT(*) FROM ui_read_items WHERE scope_hash = ?', (sh,)
            ).fetchone()[0]
            self.assertEqual(leftover, 0)
        finally:
            store.close()
            cat.close()

    def test_mark_all_read_cas_exhaustion_raises_409_conflict(self):
        """R3 P0-1: mark_all_read must raise 409 conflict when all 3 CAS attempts roll back (never false 200)."""
        from project_control import PolicyError
        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3', state_dir=self.root)
        try:
            b = store.begin_import(
                self.identity, 'proj-cas', self.ds, initialized=True, enabled_at='2026-11-20T08:00:00Z'
            )
            store.stage_import_chunk(b['batch_id'], [{'doc_id': str(uuid.uuid4()), 'is_read': False}])
            res = store.commit_import(b['batch_id'], expected_items=1)
            scope_hash = res['scope_hash']
            rev_before = res['state_rev']

            class _ContendingCatalog:
                def max_knowledge_created_at_ns(self, _ds):
                    # Simulate a concurrent writer advancing state_rev between read and CAS UPDATE every attempt
                    store.db.execute(
                        'UPDATE ui_read_scopes SET state_rev = state_rev + 1 WHERE scope_hash = ?',
                        (scope_hash,),
                    )
                    return '2026-11-20T12:00:00.000000000Z'

                def max_knowledge_rowid(self, _ds):
                    return 10

                def knowledge_docs_invalid_created(self, _ds):
                    return []

                def doc_created_at_info(self, _ds, _ids):
                    return {}

            with self.assertRaises(PolicyError) as err409:
                store.mark_all_read(
                    self.identity, 'proj-cas', self.ds, catalog_index=_ContendingCatalog()
                )
            self.assertEqual(err409.exception.status, 409)
            self.assertEqual(err409.exception.code, 'reading_state_conflict')
            # Explicit unread row MUST remain untouched and state_rev advanced 3 times by contender
            rem = store.db.execute(
                'SELECT COUNT(*) FROM ui_read_items WHERE scope_hash = ? AND is_read = 0',
                (scope_hash,),
            ).fetchone()[0]
            self.assertEqual(rem, 1)
            rev_after = store.get_scope(scope_hash)['state_rev']
            self.assertEqual(rev_after, rev_before + 3)
        finally:
            store.close()

    def test_mark_all_read_counts_invalid_document_published_after_selection(self):
        from unittest.mock import patch

        cat = CatalogIndex(self.root / 'catalog-index.sqlite3', official_data=self.official_data)
        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3', state_dir=self.root)
        self.addCleanup(cat.close)
        self.addCleanup(store.close)
        cat.sync([self.ds])
        store.ensure_scope(self.identity, self.project_id, self.ds, catalog_index=cat)
        late = str(uuid.uuid4())
        scan_invalid = cat.knowledge_docs_invalid_created

        def publish_after_scan(dataset_id):
            docs = scan_invalid(dataset_id)
            self._add_knowledge(late, 'invalid')
            cat.sync([self.ds])
            return docs

        with patch.object(cat, 'knowledge_docs_invalid_created') as scan:
            def scan_with_publication(dataset_id):
                if scan.call_count == 1:
                    return publish_after_scan(dataset_id)
                return scan_invalid(dataset_id)

            scan.side_effect = scan_with_publication
            result = store.mark_all_read(self.identity, self.project_id, self.ds, catalog_index=cat)
        self.assertEqual(result['unread_count'], 1)
        self.assertEqual(
            store.compute_unread_snapshot(self.identity, self.project_id, self.ds, cat),
            ({late}, result['state_rev']),
        )

    def test_unread_snapshot_retry_exhaustion_fails_closed(self):
        from unittest.mock import patch
        from project_policy import PolicyError

        store = ReadingStateStore(self.root / 'ui-reading-state.sqlite3', state_dir=self.root)
        self.addCleanup(store.close)
        scope = store.ensure_scope(self.identity, self.project_id, self.ds)

        def contend(*_args):
            store.mark_read(self.identity, self.project_id, self.ds, [str(uuid.uuid4())])
            return set()

        with patch.object(store, '_compute_unread_ids_for_scope', side_effect=contend) as compute:
            with self.assertRaises(PolicyError) as error:
                store._compute_unread_snapshot_for_scope(scope['scope_hash'], self.ds, object())
        self.assertEqual(error.exception.status, 409)
        self.assertEqual(compute.call_count, 3)


if __name__ == '__main__':
    unittest.main()
