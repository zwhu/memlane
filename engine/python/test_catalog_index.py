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
            self.assertNotIn('catalog_sessions', tables)
            cols = {r[1] for r in cat._write_con.execute('PRAGMA table_info(catalog_docs)').fetchall()}
            self.assertFalse(cols & {'body', 'raw_text', 'why_learned', 'metadata_json'})

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


if __name__ == '__main__':
    unittest.main()
