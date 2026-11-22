"""Unit tests for catalog_index minimal zero-body projection, 4-tuple sync, and EQP zero TEMP B-TREE."""
import json
import os
from pathlib import Path
import sqlite3
import stat
import tempfile
import unittest
import uuid

from catalog_index import CatalogIndex
from knowledge_recall import RecallStore
from project_policy import PolicyError
from provenance_store import ProvenanceStore


class CatalogIndexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.official_data = self.root / 'official-store'
        self.db_dir = self.official_data / 'system' / 'databases'
        self.db_dir.mkdir(parents=True)
        self.cognee_db = self.db_dir / 'cognee_db'
        self.prov_path = self.root / 'lesson-provenance.sqlite3'
        self.recall_path = self.root / 'knowledge-recall.sqlite3'
        self.cat_path = self.root / 'catalog-index.sqlite3'

        self.ds_proj = str(uuid.uuid4())
        self.ds_common = str(uuid.uuid4())

        con = sqlite3.connect(self.cognee_db)
        con.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY,
                dataset_id CHAR(32),
                name VARCHAR,
                label VARCHAR,
                raw_data_location VARCHAR,
                external_metadata JSON,
                system_metadata JSON,
                node_set JSON,
                pipeline_status JSON,
                updated_at DATETIME,
                created_at DATETIME,
                content_hash VARCHAR
            );
        ''')
        con.commit()
        con.close()

    def tearDown(self):
        self.tmp.cleanup()

    def _insert_doc(
        self,
        doc_id,
        ds_id,
        *,
        name='doc.md',
        label=None,
        ext_meta=None,
        sys_meta=None,
        node_set=None,
        pipeline_status=None,
        created_at='2026-11-20 10:00:00',
        updated_at='2026-11-20 10:00:00',
        content_hash='h1',
    ):
        con = sqlite3.connect(self.cognee_db)
        con.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(doc_id).hex,
                uuid.UUID(ds_id).hex,
                name,
                label,
                '',
                json.dumps(ext_meta or {}),
                json.dumps(sys_meta) if sys_meta is not None else None,
                json.dumps(node_set) if node_set is not None else None,
                json.dumps(pipeline_status or {}),
                updated_at,
                created_at,
                content_hash,
            ),
        )
        con.commit()
        con.close()

    def test_zero_body_schema_wal_pragmas_and_golden_categories(self):
        d_state = str(uuid.uuid4())
        d_pitfall = str(uuid.uuid4())
        d_future = str(uuid.uuid4())
        d_direct = str(uuid.uuid4())
        d_missing = str(uuid.uuid4())
        d_common_old = str(uuid.uuid4())
        d_common_new = str(uuid.uuid4())
        d_raw = str(uuid.uuid4())
        d_derived = str(uuid.uuid4())

        # Matches knowledge_basis_test.go + common_knowledge.go + system_metadata > external_metadata
        self._insert_doc(
            d_state, self.ds_proj,
            sys_meta={'lesson_type': 'state', 'state_key': 'test-baseline', 'session_date': '2026-11-18'},
            ext_meta={'lesson_type': 'pitfall', 'first_source_date': '1999-01-01', 'why_learned': 'SECRET_WHY'},
            node_set=json.dumps(['session_learnings']),  # double-encoded!
        )
        self._insert_doc(
            d_pitfall, self.ds_proj,
            sys_meta={'lesson_type': 'pitfall', 'session_date': '2026-11-19'},
            node_set=['session_learnings'],
        )
        self._insert_doc(
            d_future, self.ds_proj,
            sys_meta={'lesson_type': 'future-kind', 'session_date': '2026-11-19'},
            node_set=['session_learnings'],
        )
        self._insert_doc(
            d_direct, self.ds_proj,
            ext_meta={'node_set': ['user_context']},
            created_at='2026-11-20 08:00:00',
        )
        self._insert_doc(
            d_missing, self.ds_proj,
            ext_meta={'node_set': ['session_learnings']},
            created_at='2026-11-20 09:00:00',
        )
        self._insert_doc(
            d_common_old, self.ds_common,
            ext_meta={'memlane_kind': 'common_knowledge', 'category': 'preference'},
            created_at='2026-11-18 08:00:00',
            updated_at='2026-11-21 12:00:00',  # edited recently -> must float to top!
        )
        self._insert_doc(
            d_common_new, self.ds_common,
            ext_meta={'memlane_kind': 'common_knowledge', 'category': 'experience'},
            created_at='2026-11-20 08:00:00',
            updated_at='2026-11-20 08:00:00',
        )
        self._insert_doc(d_raw, self.ds_proj, name='spec.md', ext_meta={'node_set': ['project_docs']})
        self._insert_doc(d_derived, self.ds_proj, name='derived.txt', ext_meta={'node_set': ['agent_trace_feedbacks']})

        cat = CatalogIndex(
            self.cat_path,
            official_data=self.official_data,
            provenance_path=self.prov_path,
            recall_path=self.recall_path,
            tz_provider=lambda: 'Asia/Shanghai',
        )
        try:
            # Verify 0600 permissions, WAL + journal_size_limit, zero body/why_learned/metadata_json/sessions
            self.assertEqual(stat.S_IMODE(os.stat(self.cat_path).st_mode), 0o600)
            self.assertEqual(cat._write_con.execute('PRAGMA journal_mode').fetchone()[0].lower(), 'wal')
            self.assertEqual(cat._write_con.execute('PRAGMA journal_size_limit').fetchone()[0], 4194304)
            tables = {r[0] for r in cat._write_con.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()}
            self.assertEqual(
                tables,
                {'catalog_meta', 'dataset_sync_state', 'catalog_docs', 'catalog_day_buckets', 'catalog_sessions'},
            )
            cols = {r[1] for r in cat._write_con.execute('PRAGMA table_info(catalog_docs)').fetchall()}
            self.assertFalse(cols & {'body', 'raw_text', 'why_learned', 'metadata_json'})
            sess_cols = {r[1] for r in cat._write_con.execute('PRAGMA table_info(catalog_sessions)').fetchall()}
            self.assertFalse(sess_cols & {'body', 'qa_payload', 'trace_payload', 'messages', 'transcript'})

            gen = cat.sync([self.ds_proj, self.ds_common])
            self.assertEqual(gen, 1)

            # Verify categories & counts
            proj_res = cat.query_project_knowledge(self.ds_proj, limit=20)
            self.assertEqual(proj_res['total_filtered'], 5)
            self.assertEqual(
                proj_res['category_counts'],
                {
                    '项目状态': 1,
                    '踩坑经验': 1,
                    '未识别分类（future-kind）': 1,
                    '直接保存': 1,
                    '未记录': 1,
                },
            )
            by_id = {row['doc_id']: row for row in proj_res['items']}
            self.assertEqual(by_id[d_state]['effective_date'], '2026-11-18')
            self.assertEqual(by_id[d_state]['category'], '项目状态')
            self.assertNotIn(d_derived, by_id)

            # Common knowledge sorted by updatedAt DESC -> edited older doc floats first!
            com_res = cat.query_common_knowledge(self.ds_common, limit=10)
            self.assertEqual([r['doc_id'] for r in com_res['items']], [d_common_old, d_common_new])
            self.assertEqual(com_res['category_counts'], {'preference': 1, 'experience': 1})

            # O(1) stat short-circuit does not bump generation when nothing changed
            self.assertEqual(cat.sync([self.ds_proj, self.ds_common]), 1)
            self.assertTrue(cat.passive_checkpoint())
        finally:
            cat.close()

    def test_four_tuple_fingerprint_catches_rowid_reuse_replacement_inplace_update_and_delete(self):
        d1 = str(uuid.uuid4())
        d2 = str(uuid.uuid4())
        d3 = str(uuid.uuid4())
        self._insert_doc(d1, self.ds_proj, label='v1', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-15'}, node_set=['session_learnings'])
        self._insert_doc(d2, self.ds_proj, label='keep', sys_meta={'lesson_type': 'pitfall', 'session_date': '2026-11-16'}, node_set=['session_learnings'])

        tz_box = {'tz': 'UTC'}
        cat = CatalogIndex(
            self.cat_path,
            official_data=self.official_data,
            provenance_path=self.prov_path,
            recall_path=self.recall_path,
            tz_provider=lambda: tz_box['tz'],
        )
        try:
            gen1 = cat.sync([self.ds_proj])
            self.assertEqual(cat.overview_day_buckets([self.ds_proj]), [
                {'day': '2026-11-16', 'count': 1},
                {'day': '2026-11-15', 'count': 1},
            ])

            # 1. Silent max_rowid delete + immediate insert (1:1 replacement, COUNT(*) stays 2!)
            con = sqlite3.connect(self.cognee_db)
            con.execute('DELETE FROM data WHERE id = ?', (uuid.UUID(d2).hex,))
            con.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(d3).hex, uuid.UUID(self.ds_proj).hex, 'new.md', 'replaced', '',
                    '{}', json.dumps({'lesson_type': 'convention', 'session_date': '2026-11-17'}),
                    json.dumps(['session_learnings']), '{}', '2026-11-20 11:00:00', '2026-11-20 11:00:00', 'h3',
                ),
            )
            con.commit()
            con.close()

            gen2 = cat.sync([self.ds_proj])
            self.assertGreater(gen2, gen1)
            ids = {r['doc_id'] for r in cat.query_project_knowledge(self.ds_proj, limit=10)['items']}
            self.assertEqual(ids, {d1, d3})
            self.assertEqual(cat.overview_day_buckets([self.ds_proj]), [
                {'day': '2026-11-17', 'count': 1},
                {'day': '2026-11-15', 'count': 1},
            ])

            # 2. In-place UPDATE label + updated_at (no pipeline_runs row!)
            con = sqlite3.connect(self.cognee_db)
            con.execute(
                "UPDATE data SET label = 'v2-renamed', updated_at = '2026-11-21 15:00:00' WHERE id = ?",
                (uuid.UUID(d1).hex,),
            )
            con.commit()
            con.close()
            cat.sync([self.ds_proj])
            d1_row = {r['doc_id']: r for r in cat.query_project_knowledge(self.ds_proj, limit=10)['items']}[d1]
            self.assertEqual(d1_row['doc_label'], 'v2-renamed')

            # 3. Incremental provenance sync_seq update shifts effective_date & rebuilds day buckets
            prov = ProvenanceStore(self.prov_path)
            prov.record_document(self.ds_proj, d1, lesson_type='state', state_key='k1', candidate_dates=('2026-11-10',))
            prov.close()
            cat.sync([self.ds_proj])
            self.assertEqual(cat.overview_day_buckets([self.ds_proj]), [
                {'day': '2026-11-17', 'count': 1},
                {'day': '2026-11-10', 'count': 1},
            ])

            # 4. Recall count sync updates recall_count & recall_desc_key
            rec = RecallStore(self.recall_path)
            rec.record('a1', {'event_id': str(uuid.uuid4()), 'channel': 'automatic', 'hits': [{'dataset_id': self.ds_proj, 'document_id': d3}]})
            rec.close()
            cat.sync([self.ds_proj])
            by_recall = cat.query_project_knowledge(self.ds_proj, sort='recall', limit=10)['items']
            self.assertEqual(by_recall[0]['doc_id'], d3)
            self.assertEqual(by_recall[0]['recall_count'], 1)

            # 5. Timezone change recomputes unlocked createdAt-fallback doc's effective_date!
            d_tz = str(uuid.uuid4())
            self._insert_doc(
                d_tz, self.ds_proj,
                ext_meta={'node_set': ['user_context']},
                created_at='2026-11-20T22:00:00Z',  # 2026-11-20 in UTC, 2026-11-21 in Asia/Shanghai!
            )
            cat.sync([self.ds_proj])
            row_utc = {r['doc_id']: r for r in cat.query_project_knowledge(self.ds_proj, limit=10)['items']}[d_tz]
            self.assertEqual(row_utc['effective_date'], '2026-11-20')

            tz_box['tz'] = 'Asia/Shanghai'
            cat.sync([self.ds_proj])
            row_sh = {r['doc_id']: r for r in cat.query_project_knowledge(self.ds_proj, limit=10)['items']}[d_tz]
            self.assertEqual(row_sh['effective_date'], '2026-11-21')

            # 6. Atomic self-healing rebuild + 503 catalog_index_rebuilding guard
            cat._rebuilding = True
            with self.assertRaises(PolicyError) as err:
                cat.query_project_knowledge(self.ds_proj)
            self.assertEqual((err.exception.code, err.exception.status), ('catalog_index_rebuilding', 503))
            cat._rebuilding = False
            rebuilt_gen = cat.rebuild_atomic([self.ds_proj])
            self.assertGreaterEqual(rebuilt_gen, 1)
            self.assertEqual(cat.query_project_knowledge(self.ds_proj, limit=10)['total_filtered'], 3)
        finally:
            cat.close()

    def test_eqp_zero_temp_btree_on_all_same_direction_tuple_keyset_queries(self):
        # Populate 120 same-day zero-recall clustered rows + 20 common docs + 20 raw docs
        con = sqlite3.connect(self.cognee_db)
        rows = []
        for i in range(120):
            rows.append((
                uuid.uuid4().hex, uuid.UUID(self.ds_proj).hex, f'k-{i}.md', None, '',
                '{}', json.dumps({'lesson_type': 'state', 'session_date': '2026-11-21'}),
                json.dumps(['session_learnings']), '{}', '2026-11-21 10:00:00', f'2026-11-21 10:00:{i % 60:02d}', f'hk-{i}',
            ))
        for i in range(20):
            rows.append((
                uuid.uuid4().hex, uuid.UUID(self.ds_common).hex, f'c-{i}.md', None, '',
                json.dumps({'memlane_kind': 'common_knowledge', 'category': 'preference'}), None,
                None, '{}', f'2026-11-21 10:00:{i:02d}', f'2026-11-21 10:00:{i:02d}', f'hc-{i}',
            ))
        for i in range(20):
            rows.append((
                uuid.uuid4().hex, uuid.UUID(self.ds_proj).hex, f'r-{i}.md', None, '',
                json.dumps({'node_set': ['project_docs']}), None,
                None, '{}', f'2026-11-21 09:00:{i:02d}', f'2026-11-21 09:00:{i:02d}', f'hr-{i}',
            ))
        con.executemany('INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)', rows)
        con.commit()
        con.close()

        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        try:
            cat.sync([self.ds_proj, self.ds_common])
            ro = cat._get_reader()
            queries = [
                (
                    'proj_day',
                    '''EXPLAIN QUERY PLAN
                       SELECT doc_id FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1 AND (day_desc_key, doc_id) > (?, ?)
                       ORDER BY day_desc_key ASC, doc_id ASC LIMIT 30''',
                    (self.ds_proj, -20261121, '5555'),
                    'idx_cdocs_proj_day',
                ),
                (
                    'proj_recall',
                    '''EXPLAIN QUERY PLAN
                       SELECT doc_id FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1 AND (recall_desc_key, day_desc_key, doc_id) > (?, ?, ?)
                       ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC LIMIT 30''',
                    (self.ds_proj, 0, -20261121, '5555'),
                    'idx_cdocs_proj_recall',
                ),
                (
                    'proj_cat_day',
                    '''EXPLAIN QUERY PLAN
                       SELECT doc_id FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1 AND category = ? AND (day_desc_key, doc_id) > (?, ?)
                       ORDER BY day_desc_key ASC, doc_id ASC LIMIT 30''',
                    (self.ds_proj, '项目状态', -20261121, '5555'),
                    'idx_cdocs_proj_cat_day',
                ),
                (
                    'common_ts',
                    '''EXPLAIN QUERY PLAN
                       SELECT doc_id FROM catalog_docs
                       WHERE dataset_id = ? AND kind = 'common' AND common_category = ? AND (sort_ts_desc_key, doc_id) > (?, ?)
                       ORDER BY sort_ts_desc_key ASC, doc_id ASC LIMIT 30''',
                    (self.ds_common, 'preference', -1700000000000000000, '5555'),
                    'idx_cdocs_common_ts',
                ),
                (
                    'raw_ts',
                    '''EXPLAIN QUERY PLAN
                       SELECT doc_id FROM catalog_docs
                       WHERE dataset_id = ? AND kind = 'raw_doc' AND (sort_ts_desc_key, doc_id) > (?, ?)
                       ORDER BY sort_ts_desc_key ASC, doc_id ASC LIMIT 30''',
                    (self.ds_proj, -1700000000000000000, '5555'),
                    'idx_cdocs_raw_ts',
                ),
                (
                    'sparse_created',
                    '''EXPLAIN QUERY PLAN
                       SELECT doc_id FROM catalog_docs
                       WHERE dataset_id = ? AND is_knowledge = 1 AND created_at_ns > ?
                       ORDER BY created_at_ns DESC, doc_id ASC''',
                    (self.ds_proj, '2026-11-21T00:00:00.000000000Z'),
                    'idx_cdocs_proj_created',
                ),
            ]
            for name, sql, params, expected_idx in queries:
                with self.subTest(query=name):
                    plan = [r[3] for r in ro.execute(sql, params).fetchall()]
                    joined = ' | '.join(plan)
                    self.assertIn(expected_idx, joined)
                    self.assertNotIn('TEMP B-TREE', joined)
                    self.assertNotIn('SCAN catalog_docs', joined)
        finally:
            cat.close()

    def test_adversarial_mutations_force_full_provenance_siblings_tz_and_anchor_prev(self):
        d1 = str(uuid.uuid4())
        d2 = str(uuid.uuid4())
        d3 = str(uuid.uuid4())
        d4 = str(uuid.uuid4())
        self._insert_doc(
            d1, self.ds_proj, label='AlphaTitle',
            sys_meta={'lesson_type': 'state', 'state_key': 'shared-key', 'session_date': '2026-11-18'},
            node_set=['session_learnings'], created_at='2026-11-18T22:00:00Z', updated_at='2026-11-18T22:00:00Z',
        )
        self._insert_doc(
            d2, self.ds_proj, label='SiblingDoc',
            sys_meta={'lesson_type': 'state', 'state_key': 'shared-key', 'session_date': '2026-11-19'},
            node_set=['session_learnings'], created_at='2026-11-19T10:00:00Z', updated_at='2026-11-19T10:00:00Z',
        )
        self._insert_doc(
            d3, self.ds_proj, label='FallbackTZ',
            ext_meta={'node_set': ['user_context']},
            created_at='2026-11-20T22:00:00Z', updated_at='2026-11-20T22:00:00Z',
        )
        tz_box = {'tz': 'UTC'}
        cat = CatalogIndex(
            self.cat_path,
            official_data=self.official_data,
            provenance_path=self.prov_path,
            recall_path=self.recall_path,
            tz_provider=lambda: tz_box['tz'],
        )
        try:
            cat.sync([self.ds_proj])
            # 1. Same-length rename with UNCHANGED updated_at + mixed append in same txn (PY-P0-01)
            con = sqlite3.connect(self.cognee_db)
            con.execute('UPDATE data SET label = ? WHERE id = ?', ('OmegaTitle', uuid.UUID(d1).hex))
            con.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(d4).hex, uuid.UUID(self.ds_proj).hex, 'd4.md', 'Appended', '',
                    '{}', json.dumps({'lesson_type': 'pitfall', 'state_key': 'k4', 'session_date': '2026-11-21'}),
                    json.dumps(['session_learnings']), '{}', '2026-11-21T01:00:00Z', '2026-11-21T01:00:00Z', 'h4',
                ),
            )
            con.commit()
            con.close()
            cat.sync([self.ds_proj])
            by_id = {r['doc_id']: r for r in cat.query_project_knowledge(self.ds_proj, limit=20)['items']}
            self.assertEqual(by_id[d1]['doc_label'], 'OmegaTitle')
            self.assertIn(d4, by_id)

            # 2. Metadata category change with NULL updated_at -> category_counts MUST NOT drift (PY-P0-01 + PY-P1-05)
            con = sqlite3.connect(self.cognee_db)
            con.execute(
                'UPDATE data SET system_metadata = ?, updated_at = NULL WHERE id = ?',
                (json.dumps({'lesson_type': 'pitfall', 'state_key': 'shared-key', 'session_date': '2026-11-18'}), uuid.UUID(d1).hex),
            )
            con.commit()
            con.close()
            cat.sync([self.ds_proj])
            res_after = cat.query_project_knowledge(self.ds_proj, limit=20)
            self.assertEqual(res_after['category_counts'], {'踩坑经验': 2, '项目状态': 1, '直接保存': 1})

            # 3. Simultaneous cognee append + provenance delta updates BOTH doc AND sibling (PY-P0-02 + CC-P0-03)
            con = sqlite3.connect(self.cognee_db)
            con.execute(
                'UPDATE data SET system_metadata = ? WHERE id = ?',
                (json.dumps({'lesson_type': 'state', 'state_key': 'shared-key', 'session_date': '2026-11-18'}), uuid.UUID(d1).hex),
            )
            con.commit()
            con.close()
            cat.sync([self.ds_proj])

            prov = ProvenanceStore(self.prov_path)
            prov.record_document(self.ds_proj, d1, lesson_type='state', state_key='shared-key', candidate_dates=('2026-11-05',))
            prov.close()
            d5 = str(uuid.uuid4())
            self._insert_doc(
                d5, self.ds_proj, label='ConcurrentAppend',
                sys_meta={'lesson_type': 'convention', 'session_date': '2026-11-22'},
                node_set=['session_learnings'],
            )
            cat.sync([self.ds_proj])
            by_id = {r['doc_id']: r for r in cat.query_project_knowledge(self.ds_proj, limit=20)['items']}
            self.assertEqual(by_id[d1]['effective_date'], '2026-11-05')
            self.assertEqual(by_id[d2]['effective_date'], '2026-11-05')  # Sibling propagated!

            # 4. Timezone change ONLY updates created_at fallback (d3), NEVER overwrites meta session_date (d4/d5) or provenance (d1/d2) (PY-P1-06)
            tz_box['tz'] = 'Asia/Shanghai'
            cat.sync([self.ds_proj])
            by_id = {r['doc_id']: r for r in cat.query_project_knowledge(self.ds_proj, limit=20)['items']}
            self.assertEqual(by_id[d3]['effective_date'], '2026-11-21')  # 22:00Z -> +08 next day
            self.assertEqual(by_id[d4]['effective_date'], '2026-11-21')  # meta preserved
            self.assertEqual(by_id[d1]['effective_date'], '2026-11-05')  # provenance preserved

            # 5. Anchor jump + bidirectional before_key pagination + missing anchor returns None (CC-P0-04)
            ordered_all = cat.query_project_knowledge(self.ds_proj, limit=20)['items']
            mid_doc = ordered_all[2]['doc_id']
            anchored = cat.query_project_knowledge(self.ds_proj, limit=2, anchor_doc_id=mid_doc)
            self.assertIsNotNone(anchored)
            self.assertIn(mid_doc, [r['doc_id'] for r in anchored['items']])
            self.assertTrue(anchored['has_prev'])
            self.assertTrue(anchored['has_more'])
            first_item = anchored['items'][0]
            prev_page = cat.query_project_knowledge(
                self.ds_proj, limit=2, before_key=(first_item['day_desc_key'], first_item['doc_id'])
            )
            self.assertGreaterEqual(len(prev_page['items']), 1)
            self.assertIsNone(cat.query_project_knowledge(self.ds_proj, limit=2, anchor_doc_id=str(uuid.uuid4())))

            # 6. force_full=True purges source-deleted rows (PY-P0-01)
            con = sqlite3.connect(self.cognee_db)
            con.execute('DELETE FROM data WHERE id = ?', (uuid.UUID(d5).hex,))
            con.commit()
            con.close()
            cat.sync([self.ds_proj], force=True)
            self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [d5])), 0)
        finally:
            cat.close()

    def test_round2_startup_self_heal_full_prov_recovery_session_blake2b_and_multi_user_sql(self):
        """Regression for R2-PY-P0-03, R2-PY-P1-02, CC2-P0-02, CC2-P1-01, and R2-GO-P0-04."""
        # 1. Startup corruption self-heal (R2-PY-P1-02): garbage catalog DB must self-heal in __init__
        corrupt_path = self.root / 'corrupt-catalog.sqlite3'
        corrupt_path.write_bytes(b'NOT A VALID SQLITE DATABASE HEADER' * 64)
        cat_healed = CatalogIndex(corrupt_path, official_data=self.official_data)
        try:
            mode = cat_healed._write_con.execute('PRAGMA journal_mode').fetchone()[0]
            self.assertEqual(str(mode).lower(), 'wal')
        finally:
            cat_healed.close()

        # 2. Full provenance recovery after initial prov_ok=False (R2-PY-P0-03 / CC2-P1-01)
        d1 = str(uuid.uuid4())
        self._insert_doc(
            d1, self.ds_proj, label='ProvRecovery',
            sys_meta={'lesson_type': 'state', 'state_key': 'sk-rec', 'session_date': '2026-11-20'},
            node_set=['session_learnings'],
            created_at='2026-11-20 10:00:00',
        )
        # Plant unreadable/corrupt provenance file on sync 1 so prov_ok=False
        self.prov_path.write_bytes(b'corrupted provenance file')
        cat = CatalogIndex(
            self.root / 'cat-r2.sqlite3',
            official_data=self.official_data,
            provenance_path=self.prov_path,
        )
        try:
            cat.sync([self.ds_proj])
            # Live DB stays strictly WAL after cold build!
            mode = cat._write_con.execute('PRAGMA journal_mode').fetchone()[0]
            self.assertEqual(str(mode).lower(), 'wal')
            doc_before = cat.fetch_docs_by_ids(self.ds_proj, [d1])[0]
            self.assertEqual(doc_before['effective_date'], '2026-11-20')

            # Now replace with valid legacy provenance DB (even without sync_seq!) without touching cognee_db
            self.prov_path.unlink()
            pcon = sqlite3.connect(self.prov_path)
            pcon.executescript('''
                CREATE TABLE document_provenance (
                    dataset_id TEXT NOT NULL, document_id TEXT NOT NULL,
                    lesson_type TEXT NOT NULL DEFAULT '', state_key TEXT NOT NULL DEFAULT '',
                    first_source_date TEXT NOT NULL,
                    PRIMARY KEY (dataset_id, document_id)
                );
                CREATE TABLE keyed_provenance (
                    dataset_id TEXT NOT NULL, lesson_type TEXT NOT NULL,
                    state_key TEXT NOT NULL, first_source_date TEXT NOT NULL,
                    PRIMARY KEY (dataset_id, lesson_type, state_key)
                );
            ''')
            pcon.execute(
                'INSERT INTO document_provenance VALUES (?,?,?,?,?)',
                (self.ds_proj, d1, 'state', 'sk-rec', '2026-10-01'),
            )
            pcon.commit()
            pcon.close()

            cat.sync([self.ds_proj])
            doc_after = cat.fetch_docs_by_ids(self.ds_proj, [d1])[0]
            self.assertEqual(doc_after['effective_date'], '2026-10-01')
            self.assertEqual(doc_after['effective_date_source'], 'provenance')
            buckets = cat.overview_day_buckets([self.ds_proj])
            self.assertEqual(buckets, [{'day': '2026-10-01', 'count': 1}])

            # 3. Session in-place UPDATE detection (CC2-P0-02): same-length status/agent_id/non-max timestamp
            con = sqlite3.connect(self.cognee_db)
            con.executescript('''
                CREATE TABLE IF NOT EXISTS session_records (
                    session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                    status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                    PRIMARY KEY (session_id, user_id)
                );
            ''')
            u1, u2 = str(uuid.uuid4()), str(uuid.uuid4())
            con.executemany(
                'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
                [
                    ('shared-root', uuid.UUID(u1).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:00:00', '2026-11-20 10:00:00', 'codex'),
                    ('shared-root', uuid.UUID(u2).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:00:00', '2026-11-20 12:00:00', 'claude'),
                    ('child-u1-a', uuid.UUID(u1).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:10:00', '2026-11-20 09:00:00', 'codex'),
                    ('child-u1-b', uuid.UUID(u1).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:15:00', '2026-11-20 09:05:00', 'codex'),
                    ('child-u2-a', uuid.UUID(u2).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:20:00', '2026-11-20 09:10:00', 'claude'),
                ],
            )
            con.commit()
            con.close()

            rels = {
                'child-u1-a': {'parent_session_id': 'shared-root', 'subagent_role': 'w', 'nesting_depth': 1},
                'child-u1-b': {'parent_session_id': 'shared-root', 'subagent_role': 'w', 'nesting_depth': 1},
                'child-u2-a': {'parent_session_id': 'shared-root', 'subagent_role': 'w', 'nesting_depth': 1},
            }
            cat.sync([self.ds_proj], relations_resolver=lambda _ds, _sids: rels)

            # Same-length in-place UPDATE: 'active' -> 'closed' (both 6 chars), 'codex' -> 'other' (5 chars),
            # and non-max last_activity_at 09:00:00 -> 09:01:00 (same length, < max 12:00:00)!
            con = sqlite3.connect(self.cognee_db)
            con.execute(
                "UPDATE session_records SET status='closed', agent_id='other', last_activity_at='2026-11-20 09:01:00' WHERE session_id='child-u1-a'"
            )
            con.commit()
            con.close()
            cat.sync([self.ds_proj], relations_resolver=lambda _ds, _sids: rels)

            # Multi-user scoped children page & previews + zero TEMP B-TREE EQP verification (R2-GO-P0-04)
            cpage = cat.query_session_children_page(self.ds_proj, 'shared-root', user_id=u1, limit=10)
            self.assertEqual(cpage['total_count'], 2)
            by_sid = {r['session_id']: r for r in cpage['items']}
            self.assertEqual(set(by_sid), {'child-u1-a', 'child-u1-b'})
            self.assertEqual(by_sid['child-u1-a']['status'], 'closed')
            self.assertEqual(by_sid['child-u1-a']['agent_id'], 'other')
            self.assertIn('09:01:00', by_sid['child-u1-a']['last_activity_at'])

            previews = cat.query_top_level_children_previews(self.ds_proj, [('shared-root', u1)], limit=1)
            self.assertEqual(len(previews[('shared-root', u1)][0]), 1)
            self.assertTrue(previews[('shared-root', u1)][1])
            self.assertEqual(previews[('shared-root', u1)][0][0]['user_id'], u1)

            # Verify EXPLAIN QUERY PLAN uses idx_csess_root_user_ts with ZERO TEMP B-TREE
            rcon = cat._get_reader()
            eqp_rows = rcon.execute(
                '''EXPLAIN QUERY PLAN
                   SELECT session_id FROM catalog_sessions
                   WHERE dataset_id = ? AND user_id = ? AND is_top_level = 0 AND root_session_id = ?
                   ORDER BY sort_ts_desc_key ASC, session_id ASC
                   LIMIT 5''',
                (self.ds_proj, u1, 'shared-root'),
            ).fetchall()
            plan_txt = ' | '.join(str(r[3]) for r in eqp_rows)
            self.assertNotIn('TEMP B-TREE', plan_txt.upper(), plan_txt)
            self.assertIn('idx_csess_root_user_ts', plan_txt)
        finally:
            cat.close()

    def test_r3_corruption_heal_floor_recent_sessions_eqp_and_provenance_siblings(self):
        """R3 P0-2, P1-3, P1-4: corruption self-heal preserves reading-state floor, per-dataset recent sessions EQP, and full provenance sibling propagation."""
        from reading_state_store import ReadingStateStore

        # 1. P0-2: Corruption self-heal must sequence ambiguous rows above the reading horizon.
        d_old = str(uuid.uuid4())
        d_late = str(uuid.uuid4())
        con = sqlite3.connect(self.cognee_db)
        con.execute('DELETE FROM data')
        con.execute(
            '''INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)''',
            (
                uuid.UUID(d_old).hex, uuid.UUID(self.ds_proj).hex, 'old.md', 'Old', '',
                '{}', json.dumps({'lesson_type': 'state', 'state_key': 'sk-old', 'session_date': '2026-11-20'}),
                json.dumps(['session_learnings']), '{}', '2026-11-20T09:00:00Z', '2026-11-20T09:00:00Z', 'h-old',
            ),
        )
        con.commit()
        con.close()

        rs_path = self.root / 'ui-reading-state.sqlite3'
        rstore = ReadingStateStore(rs_path, state_dir=self.root)
        cat_path = self.cat_path
        try:
            # Create scope with horizon_max_rowid=100, enabled=08:00Z, horizon=11:00Z (d_old at 09:00Z is READ)
            identity = 'http://127.0.0.1:18765/r3-test/'
            b = rstore.begin_import(identity, 'p-heal', self.ds_proj, initialized=True, enabled_at='2026-11-20T08:00:00Z')
            rstore.commit_import(b['batch_id'], expected_items=0)
            sh = b['scope_hash']
            rstore.db.execute(
                '''UPDATE ui_read_scopes
                   SET read_horizon_at_ns = '2026-11-20T11:00:00.000000000Z', horizon_max_rowid = 100
                   WHERE scope_hash = ?''',
                (sh,),
            )

            # Corrupt catalog-index.sqlite3 on disk before opening CatalogIndex
            cat_path.write_bytes(b'CORRUPTED_CATALOG_HEADER_XYZ' * 128)
            cat = CatalogIndex(
                cat_path,
                official_data=self.official_data,
                provenance_path=self.prov_path,
                recall_path=self.recall_path,
            )
            try:
                # The surviving reading-state DB still proves this explicit read.
                rstore.mark_read(identity, 'p-heal', self.ds_proj, [d_old])
                # Insert a late arrival before the corrupt catalog has been rebuilt.
                con = sqlite3.connect(self.cognee_db)
                con.execute(
                    '''INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)''',
                    (
                        uuid.UUID(d_late).hex, uuid.UUID(self.ds_proj).hex, 'late.md', 'Late', '',
                        '{}', json.dumps({'lesson_type': 'state', 'state_key': 'sk-late', 'session_date': '2026-11-20'}),
                        json.dumps(['session_learnings']), '{}', '2026-11-20T09:30:00Z', '2026-11-20T09:30:00Z', 'h-late',
                    ),
                )
                con.commit()
                con.close()
                cat.sync([self.ds_proj])
                self.assertGreater(cat.doc_created_at_info(self.ds_proj, [d_old])[d_old][2], 100)
                late_info = cat.doc_created_at_info(self.ds_proj, [d_late])
                self.assertGreater(late_info[d_late][2], 100)
                # d_late MUST be detected as UNREAD (not swallowed by horizon_max_rowid=100)!
                self.assertEqual(rstore.compute_unread_ids(identity, 'p-heal', self.ds_proj, cat), {d_late})
                flags = rstore.page_unread_flags(
                    identity, 'p-heal', self.ds_proj, cat.fetch_docs_by_ids(self.ds_proj, [d_old, d_late]), catalog_index=cat
                )
                self.assertFalse(flags[d_old])
                self.assertTrue(flags[d_late])

                # 2. P1-3: query_recent_sessions uses idx_csess_ds_recent_ts (0 TEMP B-TREE) & isolates empty dataset
                con = sqlite3.connect(self.cognee_db)
                con.execute('''
                    CREATE TABLE IF NOT EXISTS session_records (
                        session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                        status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                        PRIMARY KEY (session_id, user_id)
                    );
                ''')
                u1 = str(uuid.uuid4())
                con.execute(
                    'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
                    ('proj-sess-1', uuid.UUID(u1).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:00:00', '2026-11-20 10:00:00', 'codex'),
                )
                con.commit()
                con.close()
                cat.sync([self.ds_proj, self.ds_common])
                self.assertEqual(cat.query_recent_sessions([self.ds_common], limit=10), [])
                self.assertEqual(len(cat.query_recent_sessions([self.ds_proj], limit=10)), 1)
                eqp = cat._get_reader().execute(
                    '''EXPLAIN QUERY PLAN
                       SELECT session_id FROM catalog_sessions
                       WHERE dataset_id = ?
                       ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC
                       LIMIT 20''',
                    (self.ds_common,),
                ).fetchall()
                plan_txt = ' | '.join(str(r[3]) for r in eqp)
                self.assertNotIn('TEMP B-TREE', plan_txt.upper(), plan_txt)
                self.assertIn('idx_csess_ds_recent_ts', plan_txt)

                # 3. P1-4: Full provenance recovery propagates earliest date from document_provenance to same-key sibling
                #    even when keyed_provenance is completely empty!
                d_sib1, d_sib2 = str(uuid.uuid4()), str(uuid.uuid4())
                con = sqlite3.connect(self.cognee_db)
                con.executemany(
                    'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?)',
                    [
                        (
                            uuid.UUID(d_sib1).hex, uuid.UUID(self.ds_proj).hex, 'sib1.md', 'Sib1', '',
                            '{}', json.dumps({'lesson_type': 'state', 'state_key': 'sk-shared', 'session_date': '2026-11-20'}),
                            json.dumps(['session_learnings']), '{}', '2026-11-20T10:00:00Z', '2026-11-20T10:00:00Z', 'h-s1',
                        ),
                        (
                            uuid.UUID(d_sib2).hex, uuid.UUID(self.ds_proj).hex, 'sib2.md', 'Sib2', '',
                            '{}', json.dumps({'lesson_type': 'state', 'state_key': 'sk-shared', 'session_date': '2026-11-21'}),
                            json.dumps(['session_learnings']), '{}', '2026-11-21T10:00:00Z', '2026-11-21T10:00:00Z', 'h-s2',
                        ),
                    ],
                )
                con.commit()
                con.close()
                # Simulate transient provenance outage during first sync, then recover with document_provenance ONLY (empty keyed_provenance)
                self.prov_path.write_bytes(b'corrupt')
                cat.sync([self.ds_proj])
                self.prov_path.unlink()
                pcon = sqlite3.connect(self.prov_path)
                pcon.executescript('''
                    CREATE TABLE document_provenance (
                        dataset_id TEXT NOT NULL, document_id TEXT NOT NULL,
                        lesson_type TEXT NOT NULL DEFAULT '', state_key TEXT NOT NULL DEFAULT '',
                        first_source_date TEXT NOT NULL, sync_seq INTEGER NOT NULL DEFAULT 1,
                        PRIMARY KEY (dataset_id, document_id)
                    );
                    CREATE TABLE keyed_provenance (
                        dataset_id TEXT NOT NULL, lesson_type TEXT NOT NULL, state_key TEXT NOT NULL,
                        first_source_date TEXT NOT NULL,
                        PRIMARY KEY (dataset_id, lesson_type, state_key)
                    );
                ''')
                pcon.execute(
                    'INSERT INTO document_provenance VALUES (?,?,?,?,?,?)',
                    (self.ds_proj, d_sib1, '', '', '2024-03-01', 1),
                )
                pcon.commit()
                pcon.close()
                cat.sync([self.ds_proj])
                fetched = {r['doc_id']: r for r in cat.fetch_docs_by_ids(self.ds_proj, [d_sib1, d_sib2])}
                self.assertEqual(fetched[d_sib1]['effective_date'], '2024-03-01')
                self.assertEqual(fetched[d_sib2]['effective_date'], '2024-03-01')
                buckets = {b['day']: b['count'] for b in cat.overview_day_buckets([self.ds_proj], days=3650)}
                self.assertEqual(buckets.get('2024-03-01'), 2)

                cat.rebuild_atomic([self.ds_proj])
                rebuilt = cat.fetch_docs_by_ids(self.ds_proj, [d_sib1, d_sib2])
                self.assertEqual([r['effective_date'] for r in rebuilt], ['2024-03-01'] * 2)
                rebuilt_buckets = {b['day']: b['count'] for b in cat.overview_day_buckets([self.ds_proj], days=3650)}
                self.assertEqual(rebuilt_buckets.get('2024-03-01'), 2)
            finally:
                cat.close()
        finally:
            rstore.close()

    def test_user_profile_remains_raw_but_excluded_from_pending_refinement(self):
        profile, external_profile, raw = (str(uuid.uuid4()) for _ in range(3))
        self._insert_doc(profile, self.ds_proj, sys_meta={'memlane_kind': 'user_profile'})
        self._insert_doc(
            external_profile, self.ds_proj,
            sys_meta={'source': 'official'}, ext_meta={'memlane_kind': 'user_profile'},
        )
        self._insert_doc(raw, self.ds_proj)
        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])
        rows, count = cat.query_pending_raw_docs(self.ds_proj)
        self.assertEqual(count, 1)
        self.assertEqual([row['doc_id'] for row in rows], [raw])
        docs = cat.fetch_docs_by_ids(self.ds_proj, [profile, external_profile])
        self.assertEqual(len(docs), 2)
        self.assertTrue(all(d['kind'] == 'raw_doc' and d['pipeline_status'] == 'not_applicable' for d in docs))

    def test_profile_exclusion_is_reprojected_on_catalog_upgrade(self):
        from unittest.mock import patch

        profile = str(uuid.uuid4())
        self._insert_doc(
            profile, self.ds_proj,
            sys_meta={'source': 'official'}, ext_meta={'memlane_kind': 'user_profile'},
        )
        with patch('catalog_index.CATALOG_SCHEMA_VERSION', 4):
            old = CatalogIndex(self.cat_path, official_data=self.official_data)
            try:
                old.sync([self.ds_proj])
            finally:
                old.close()
        with sqlite3.connect(self.cat_path) as con:
            con.execute("UPDATE catalog_docs SET pipeline_status = '' WHERE doc_id = ?", (profile,))
            seq = con.execute('SELECT doc_seq FROM catalog_docs WHERE doc_id = ?', (profile,)).fetchone()[0]

        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])
        self.assertEqual(cat.query_pending_raw_docs(self.ds_proj)[1], 0)
        doc = cat.fetch_docs_by_ids(self.ds_proj, [profile])[0]
        self.assertEqual(doc['pipeline_status'], 'not_applicable')
        self.assertEqual(doc['doc_seq'], seq)

    def test_corruption_heal_fails_closed_when_reading_state_is_unreadable(self):
        from project_policy import PolicyError

        corrupt_catalog = b'corrupt catalog'
        self.cat_path.write_bytes(corrupt_catalog)
        (self.root / 'ui-reading-state.sqlite3').write_bytes(b'corrupt reading state')
        with self.assertRaises(PolicyError) as error:
            CatalogIndex(self.cat_path, official_data=self.official_data)
        self.assertEqual(error.exception.code, 'reading_state_unavailable')
        self.assertEqual(self.cat_path.read_bytes(), corrupt_catalog)


if __name__ == '__main__':
    unittest.main()
