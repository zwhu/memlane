"""Unit tests for catalog_index minimal zero-body projection, 4-tuple sync, and EQP zero TEMP B-TREE."""
import asyncio
import json
import os
from pathlib import Path
import sqlite3
import stat
import tempfile
import threading
import unittest
from unittest.mock import patch
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

    def test_read_sync_blocks_for_cold_start_scope_timezone_and_missing_source(self):
        async def check():
            timezone = ['UTC']
            cat = CatalogIndex(self.cat_path, official_data=self.official_data, tz_provider=lambda: timezone[0])
            self.addCleanup(cat.close)
            cat._FAST_DOC_COUNT_THRESHOLD = 0
            doc = str(uuid.uuid4())
            self._insert_doc(doc, self.ds_proj)
            cat_sync = cat.sync_async
            with patch.object(cat, 'sync_async', wraps=cat_sync) as strong:
                await cat.sync_for_read_async([self.ds_proj])
                self.assertEqual(cat._last_sync_doc_count, 1)
                self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc])), 1)
                strong.assert_awaited_once()

                await cat.sync_for_read_async([self.ds_proj])
                strong.assert_awaited_once()
                await cat.sync_for_read_async([self.ds_proj, self.ds_common])
                self.assertEqual(strong.await_count, 2)

                timezone[0] = 'Asia/Shanghai'
                await cat.sync_for_read_async([self.ds_proj, self.ds_common])
                self.assertEqual(cat.meta()['local_tz_name'], timezone[0])
                self.assertEqual(strong.await_count, 3)

                self.cognee_db.unlink()
                # Also verify fail-closed when a stale non-empty -wal file remains after main DB vanishes.
                Path(str(self.cognee_db) + '-wal').write_bytes(b'stale-wal-frames')
                with self.assertRaises(PolicyError):
                    await cat.sync_for_read_async([self.ds_proj, self.ds_common])
                self.assertEqual(strong.await_count, 4)

        asyncio.run(check())

    def test_failed_temp_rebuild_does_not_pollute_live_chunk_digests(self):
        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        doc1 = str(uuid.uuid4())
        self._insert_doc(doc1, self.ds_proj)
        gen1 = cat.sync([self.ds_proj])

        doc2 = str(uuid.uuid4())
        self._insert_doc(doc2, self.ds_proj)
        orig_invalidate = cat._invalidate_readers
        with patch.object(cat, '_invalidate_readers', side_effect=RuntimeError('swap aborted')):
            with self.assertRaisesRegex(RuntimeError, 'swap aborted'):
                cat.rebuild_atomic([self.ds_proj])

        # Live catalog still only has doc1, and next sync MUST detect and ingest doc2!
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc2])), 0)
        gen2 = cat.sync([self.ds_proj])
        self.assertGreater(gen2, gen1)
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc1, doc2])), 2)

    def test_bg_timer_signature_failure_clears_timer_and_recovers_on_next_read(self):
        async def check():
            fail_tz = [False]

            def flaky_tz():
                if fail_tz[0]:
                    raise RuntimeError('transient tz error')
                return 'UTC'

            cat = CatalogIndex(self.cat_path, official_data=self.official_data, tz_provider=flaky_tz)
            self.addCleanup(cat.close)
            cat._FAST_DOC_COUNT_THRESHOLD = 0
            cat._FAST_SYNC_THRESHOLD_NS = 0
            cat._BG_COALESCE_MIN_NS = cat._BG_COALESCE_MAX_NS = 60_000_000_000
            self._insert_doc(str(uuid.uuid4()), self.ds_proj)
            cat.sync([self.ds_proj])
            doc2 = str(uuid.uuid4())
            self._insert_doc(doc2, self.ds_proj)

            with patch('catalog_index.threading.Timer') as timer_cls:
                await cat.sync_for_read_async([self.ds_proj])
                timer_cls.assert_called_once()
                # Trigger the timer callback when tz_provider raises inside _full_stat_sig
                fail_tz[0] = True
                timer_cls.call_args.args[1]()
                fail_tz[0] = False
                self.assertIsNone(cat._bg_timer)
                self.assertTrue(cat._bg_sync_failed)
                self.assertTrue(cat.is_catchup_pending())

            # Next read takes blocking path, recovers cleanly, and ingests doc2
            await cat.sync_for_read_async([self.ds_proj])
            self.assertFalse(cat._bg_sync_failed)
            self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc2])), 1)

        asyncio.run(check())

    def test_large_read_sync_coalesces_writes_during_background_sync(self):
        async def check():
            cat = CatalogIndex(self.cat_path, official_data=self.official_data)
            self.addCleanup(cat.close)
            cat._FAST_DOC_COUNT_THRESHOLD = 0
            cat._FAST_SYNC_THRESHOLD_NS = 0
            cat._BG_COALESCE_MIN_NS = cat._BG_COALESCE_MAX_NS = 60_000_000_000
            self._insert_doc(str(uuid.uuid4()), self.ds_proj)
            datasets = [self.ds_proj]
            initial_gen = cat.sync(datasets)
            first = str(uuid.uuid4())
            self._insert_doc(first, self.ds_proj)

            pinned = threading.Event()
            release = threading.Event()
            sync_into = cat._sync_into_connection

            def hold_first_sync(*args, **kwargs):
                result = sync_into(*args, **kwargs)
                if not pinned.is_set():
                    pinned.set()
                    if not release.wait(5):
                        raise AssertionError('background writer was not released')
                return result

            with patch('catalog_index.threading.Timer') as timer, patch.object(
                cat, '_sync_into_connection', side_effect=hold_first_sync,
            ) as worker:
                self.assertEqual(await cat.sync_for_read_async(datasets), initial_gen)
                timer.assert_called_once()
                timer.call_args.args[1]()
                try:
                    self.assertTrue(await asyncio.to_thread(pinned.wait, 5))
                    inflight = cat._inflight_sync[1]
                    second = str(uuid.uuid4())
                    self._insert_doc(second, self.ds_proj)
                    reads = await asyncio.wait_for(
                        asyncio.gather(*(cat.sync_for_read_async(datasets) for _ in range(6))), timeout=1,
                    )
                    self.assertEqual(reads, [initial_gen] * 6)
                    worker.assert_called_once()
                    timer.assert_called_once()
                finally:
                    release.set()

                await asyncio.wrap_future(inflight)
                self.assertIsNone(cat._inflight_sync)
                self.assertEqual(timer.call_count, 2)
                self.assertTrue(cat.is_catchup_pending())
                timer.call_args.args[1]()
                await asyncio.to_thread(cat._writer_executor.submit(lambda: None).result, 5)
                self.assertFalse(cat.is_catchup_pending())
                self.assertEqual(worker.call_count, 2)
                self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [first, second])), 2)

        asyncio.run(check())

    def test_timed_out_read_leaves_writer_running_and_fresh_sync_covers_later_write(self):
        async def check():
            cat = CatalogIndex(self.cat_path, official_data=self.official_data)
            self.addCleanup(cat.close)
            cat._READ_FRESH_WAIT_NS = 0
            self._insert_doc(str(uuid.uuid4()), self.ds_proj)
            datasets = [self.ds_proj]
            initial_gen = cat.sync(datasets)
            self._insert_doc(str(uuid.uuid4()), self.ds_proj)

            pinned = threading.Event()
            release = threading.Event()
            sync_into = cat._sync_into_connection

            def hold_first_sync(*args, **kwargs):
                result = sync_into(*args, **kwargs)
                if not pinned.is_set():
                    pinned.set()
                    if not release.wait(5):
                        raise AssertionError('background writer was not released')
                return result

            with patch.object(cat, '_sync_into_connection', side_effect=hold_first_sync) as worker:
                try:
                    self.assertEqual(await cat.sync_for_read_async(datasets), initial_gen)
                    self.assertTrue(await asyncio.to_thread(pinned.wait, 5))
                    doc_id = str(uuid.uuid4())
                    self._insert_doc(doc_id, self.ds_proj)
                    fresh = asyncio.create_task(cat.sync_async(datasets))
                    await asyncio.sleep(0)
                    self.assertFalse(fresh.done())
                finally:
                    release.set()

                self.assertGreater(await asyncio.wait_for(fresh, timeout=5), initial_gen)
                self.assertEqual(worker.call_count, 2)
                self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc_id])), 1)
                self.assertFalse(cat.is_catchup_pending())

        asyncio.run(check())

    def test_failed_background_catchup_is_retried_in_foreground(self):
        async def check():
            cat = CatalogIndex(self.cat_path, official_data=self.official_data)
            self.addCleanup(cat.close)
            cat._FAST_DOC_COUNT_THRESHOLD = 0
            self._insert_doc(str(uuid.uuid4()), self.ds_proj)
            datasets = [self.ds_proj]
            gen = cat.sync(datasets)
            cat._BG_COALESCE_MIN_NS = cat._BG_COALESCE_MAX_NS = 0
            doc_id = str(uuid.uuid4())
            self._insert_doc(doc_id, self.ds_proj)
            with patch.object(cat, '_sync_into_connection', side_effect=PolicyError('source_failed', 503)):
                self.assertEqual(await cat.sync_for_read_async(datasets), gen)
                await asyncio.to_thread(cat._writer_executor.submit(lambda: None).result, 5)
                self.assertIsNone(cat._inflight_sync)
                self.assertIsNone(cat._bg_timer)
                self.assertTrue(cat.is_catchup_pending())
                with self.assertRaisesRegex(PolicyError, 'source_failed'):
                    await cat.sync_for_read_async(datasets)

            self.assertGreater(await cat.sync_for_read_async(datasets), gen)
            self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc_id])), 1)
            self.assertFalse(cat.is_catchup_pending())

        asyncio.run(check())

    def test_foreground_sync_and_close_cancel_deferred_catchup(self):
        async def check():
            cat = CatalogIndex(self.cat_path, official_data=self.official_data)
            self.addCleanup(cat.close)
            cat._FAST_DOC_COUNT_THRESHOLD = 0
            cat._FAST_SYNC_THRESHOLD_NS = 0
            cat._BG_COALESCE_MIN_NS = cat._BG_COALESCE_MAX_NS = 60_000_000_000
            self._insert_doc(str(uuid.uuid4()), self.ds_proj)
            datasets = [self.ds_proj]
            gen = cat.sync(datasets)
            for use_async in (False, True):
                doc = str(uuid.uuid4())
                self._insert_doc(doc, self.ds_proj)
                self.assertEqual(await cat.sync_for_read_async(datasets), gen)
                deferred = cat._bg_timer
                self.assertIsNotNone(deferred)
                gen = await cat.sync_async(datasets) if use_async else cat.sync(datasets)
                self.assertTrue(deferred.finished.is_set())
                self.assertFalse(cat.is_catchup_pending())
                self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc])), 1)

            self._insert_doc(str(uuid.uuid4()), self.ds_proj)
            await cat.sync_for_read_async(datasets)
            deferred = cat._bg_timer
            self.assertIsNotNone(deferred)
            cat.close()
            deferred.join(timeout=5)
            self.assertFalse(deferred.is_alive())
            self.assertFalse(cat.is_catchup_pending())
            with self.assertRaisesRegex(PolicyError, 'catalog_index_closed'):
                await cat.sync_for_read_async(datasets)
            with self.assertRaisesRegex(PolicyError, 'catalog_index_closed'):
                await cat.sync_async(datasets)

        asyncio.run(check())

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

    def test_rebuild_post_replace_failure_clears_digests_and_reconciles(self):
        import os
        from unittest.mock import patch

        d1 = str(uuid.uuid4())
        self._insert_doc(
            d1, self.ds_proj, name='A.md', label='LabelA',
            sys_meta={'lesson_type': 'state', 'session_date': '2026-11-20'},
            node_set=['session_learnings'],
        )
        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])
        self.assertEqual(cat.fetch_docs_by_ids(self.ds_proj, [d1])[0]['doc_name'], 'A.md')

        # Mutate official source to B.md, then inject os.chmod failure right after os.replace in rebuild_atomic
        with sqlite3.connect(self.cognee_db) as con:
            con.execute("UPDATE data SET name = 'B.md', label = 'LabelB' WHERE id = ?", (uuid.UUID(d1).hex,))
        real_chmod = os.chmod
        fail_once = [True]

        def _flaky_chmod(path, mode):
            if fail_once[0] and str(path) == str(self.cat_path):
                fail_once[0] = False
                raise OSError('injected chmod failure after os.replace')
            return real_chmod(path, mode)

        with patch('catalog_index.os.chmod', side_effect=_flaky_chmod):
            with self.assertRaises(OSError):
                cat.rebuild_atomic([self.ds_proj])

        # Disk now holds B.md; revert official source back to A.md and verify sync reconciles to A.md!
        with cat._state_lock:
            self.assertIsNone(cat._last_synced_ds_tuple)
            self.assertFalse(cat._has_synced_in_process)
            self.assertTrue(cat._bg_sync_failed)
        with sqlite3.connect(self.cognee_db) as con:
            con.execute("UPDATE data SET name = 'A.md', label = 'LabelA' WHERE id = ?", (uuid.UUID(d1).hex,))
        cat.sync([self.ds_proj])
        row = cat.fetch_docs_by_ids(self.ds_proj, [d1])[0]
        self.assertEqual(row['doc_name'], 'A.md')
        self.assertEqual(row['doc_label'], 'LabelA')

    def test_cross_scope_inflight_does_not_short_circuit_foreground_sync(self):
        import asyncio
        import threading

        d1 = str(uuid.uuid4())
        d2 = str(uuid.uuid4())
        self._insert_doc(d1, self.ds_proj, name='proj.md', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-20'}, node_set=['session_learnings'])
        self._insert_doc(d2, self.ds_common, name='common.md', ext_meta={'memlane_kind': 'common_knowledge', 'category': 'preference'})

        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])

        entered_b = threading.Event()
        release_b = threading.Event()
        orig_sync_into = cat._sync_into_connection

        def _slow_scope_b(con, managed_datasets, **kwargs):
            if managed_datasets == [self.ds_common]:
                entered_b.set()
                release_b.wait(timeout=5.0)
            return orig_sync_into(con, managed_datasets, **kwargs)

        cat._sync_into_connection = _slow_scope_b

        async def _run():
            task_b = asyncio.create_task(cat.sync_async([self.ds_common]))
            await asyncio.to_thread(entered_b.wait, 5.0)
            self.assertTrue(entered_b.is_set())
            task_a = asyncio.create_task(cat.sync_for_read_async([self.ds_proj]))
            await asyncio.sleep(0.05)
            # A must NOT have short-circuited while incompatible scope B is still in-flight!
            self.assertFalse(task_a.done())
            release_b.set()
            await task_b
            await task_a

        asyncio.run(_run())
        # After A completes, catalog MUST be projected to scope A (ds_proj present, ds_common absent).
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [d1])), 1)
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_common, [d2])), 0)

    def test_displaced_bg_timer_error_does_not_clear_newer_pending_sync(self):
        import threading

        d1 = str(uuid.uuid4())
        self._insert_doc(d1, self.ds_proj, name='t.md', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-20'}, node_set=['session_learnings'])
        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])

        old_timer = object()
        new_timer = object()
        ds_tuple = (self.ds_proj,)
        new_pending = ((self.ds_proj, self.ds_common), None)
        entered_sig = threading.Event()
        release_sig = threading.Event()
        orig_full_sig = cat._full_stat_sig

        def _failing_sig(dt, rr):
            entered_sig.set()
            release_sig.wait(timeout=5.0)
            raise RuntimeError('old timer stat failed')

        cat._full_stat_sig = _failing_sig
        with cat._state_lock:
            cat._bg_timer = old_timer
            cat._pending_bg_sync = (ds_tuple, None)

        th = threading.Thread(target=cat._on_bg_timer_fire, args=(old_timer,))
        th.start()
        self.assertTrue(entered_sig.wait(timeout=5.0))
        with cat._state_lock:
            cat._bg_timer = new_timer
            cat._pending_bg_sync = new_pending
        release_sig.set()
        th.join(timeout=5.0)
        cat._full_stat_sig = orig_full_sig

        with cat._state_lock:
            self.assertIs(cat._bg_timer, new_timer)
            self.assertEqual(cat._pending_bg_sync, new_pending)
            self.assertFalse(cat._bg_sync_failed)
            cat._bg_timer = None
            cat._pending_bg_sync = None

    def test_children_preview_cache_tracks_committed_generation_not_post_commit_counter(self):
        u1 = str(uuid.uuid4())
        with sqlite3.connect(self.cognee_db) as con:
            con.execute('''
                CREATE TABLE IF NOT EXISTS session_records (
                    session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                    status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                    PRIMARY KEY (session_id, user_id)
                );
            ''')
            con.execute(
                'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
                ('parent-1', uuid.UUID(u1).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:00:00', '2026-11-20 10:00:00', 'codex'),
            )
            con.execute(
                'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
                ('child-1', uuid.UUID(u1).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-20 08:01:00', '2026-11-20 10:01:00', 'codex'),
            )
        rels = {'child-1': {'parent_session_id': 'parent-1', 'subagent_role': 'worker', 'nesting_depth': 1}}
        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        gen1 = cat.sync([self.ds_proj], relations_resolver=lambda _ds, _sids: rels)
        p1 = cat.query_top_level_children_previews(self.ds_proj, [('parent-1', u1)], limit=5, expected_gen=gen1)
        self.assertEqual([r['session_id'] for r in p1[('parent-1', u1)][0]], ['child-1'])

        # Simulate SQLite COMMIT of a new child + generation increment BEFORE any in-memory counter updates!
        with cat._write_lock:
            cat._write_con.execute(
                '''INSERT INTO catalog_sessions(
                     dataset_id, session_id, user_id, agent_id, started_at, last_activity_at,
                     sort_ts_desc_key, is_top_level, parent_session_id, root_session_id,
                     subagent_role, nesting_depth, child_count
                   ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                (
                    self.ds_proj, 'child-2', u1, 'codex', '2026-11-20 08:02:00', '2026-11-20 10:02:00',
                    -1795168920000000000, 0, 'parent-1', 'parent-1', 'worker', 1, 0,
                ),
            )
            cat._write_con.execute('UPDATE catalog_meta SET generation = generation + 1 WHERE singleton = 1')
            cat._write_con.commit()

        gen2 = cat.generation()
        self.assertEqual(gen2, gen1 + 1)
        p2 = cat.query_top_level_children_previews(self.ds_proj, [('parent-1', u1)], limit=5, expected_gen=gen2)
        self.assertEqual({r['session_id'] for r in p2[('parent-1', u1)][0]}, {'child-1', 'child-2'})
        p2_no_hint = cat.query_top_level_children_previews(self.ds_proj, [('parent-1', u1)], limit=5)
        self.assertEqual({r['session_id'] for r in p2_no_hint[('parent-1', u1)][0]}, {'child-1', 'child-2'})

    def test_third_caller_same_scope_waits_behind_executing_other_scope(self):
        import asyncio
        import threading

        d1 = str(uuid.uuid4())
        d2 = str(uuid.uuid4())
        self._insert_doc(d1, self.ds_proj, name='proj.md', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-20'}, node_set=['session_learnings'])
        self._insert_doc(d2, self.ds_common, name='common.md', ext_meta={'memlane_kind': 'common_knowledge', 'category': 'preference'})

        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])

        entered_b = threading.Event()
        release_b = threading.Event()
        orig_sync_into = cat._sync_into_connection

        def _slow_scope_b(con, managed_datasets, **kwargs):
            if managed_datasets == [self.ds_common]:
                entered_b.set()
                release_b.wait(timeout=5.0)
            return orig_sync_into(con, managed_datasets, **kwargs)

        cat._sync_into_connection = _slow_scope_b

        async def _run():
            task_b = asyncio.create_task(cat.sync_async([self.ds_common]))
            await asyncio.to_thread(entered_b.wait, 5.0)
            # First A request queues behind executing B (overwriting _inflight_sync)
            task_a1 = asyncio.create_task(cat.sync_for_read_async([self.ds_proj]))
            await asyncio.sleep(0.03)
            # Second A request arrives while B is STILL executing and A1 is queued!
            task_a2 = asyncio.create_task(cat.sync_for_read_async([self.ds_proj]))
            await asyncio.sleep(0.03)
            self.assertFalse(task_a1.done())
            self.assertFalse(task_a2.done())
            release_b.set()
            await asyncio.gather(task_b, task_a1, task_a2)

        asyncio.run(_run())
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [d1])), 1)
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_common, [d2])), 0)

    def test_pending_raw_and_category_counts_caches_use_committed_snapshot_generation(self):
        d_raw1 = str(uuid.uuid4())
        d_raw2 = str(uuid.uuid4())
        d_k1 = str(uuid.uuid4())
        d_k2 = str(uuid.uuid4())
        self._insert_doc(d_raw1, self.ds_proj, name='raw1.md')
        self._insert_doc(d_k1, self.ds_proj, name='k1.md', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-20'}, node_set=['session_learnings'])

        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        gen1 = cat.sync([self.ds_proj])
        rows1, total1 = cat.query_pending_raw_docs(self.ds_proj, limit=20, expected_gen=gen1)
        self.assertEqual(total1, 1)
        self.assertEqual([r['doc_id'] for r in rows1], [d_raw1])

        # Simulate SQLite COMMIT of a new pending raw doc + generation bump BEFORE _last_generation updates!
        with cat._write_lock:
            cat._write_con.execute(
                '''INSERT INTO catalog_docs(
                     dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                     common_category, lesson_type, state_key, category, session_id,
                     doc_name, doc_label, raw_data_location, content_hash,
                     created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                     effective_date, effective_date_source, day_desc_key, sort_ts_desc_key,
                     recall_count, recall_desc_key, pipeline_status, source_updated_at, doc_seq
                   ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                (
                    self.ds_proj, d_raw2, 9999, 'raw_doc', 0,
                    '', '', '', '', '',
                    'raw2.md', '', '', 'h-raw2',
                    '2026-11-21 10:00:00', '2026-11-21T10:00:00.000000000Z', 0, '2026-11-21T10:00:00.000000000Z',
                    '2026-11-21', 'created_at', -20261121, -1795255200000000000,
                    0, 0, '', '2026-11-21 10:00:00', 9999,
                ),
            )
            cat._write_con.execute('UPDATE catalog_meta SET generation = generation + 1 WHERE singleton = 1')
            cat._write_con.commit()

        self.assertEqual(cat._last_generation, gen1)
        gen2 = cat.generation()
        self.assertEqual(gen2, gen1 + 1)
        rows2, total2 = cat.query_pending_raw_docs(self.ds_proj, limit=20, expected_gen=gen2)
        self.assertEqual(total2, 2)
        self.assertEqual({r['doc_id'] for r in rows2}, {d_raw1, d_raw2})
        rows2_no_hint, total2_no_hint = cat.query_pending_raw_docs(self.ds_proj, limit=20)
        self.assertEqual(total2_no_hint, 2)
        self.assertEqual({r['doc_id'] for r in rows2_no_hint}, {d_raw1, d_raw2})

        # Verify query_project_knowledge holds ro_snapshot so category_counts cannot mix G and G+1!
        self._insert_doc(d_k2, self.ds_proj, name='k2.md', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-21'}, node_set=['session_learnings'])
        with cat._state_lock:
            cat._cat_counts_cache.clear()
        res_k = cat.query_project_knowledge(self.ds_proj, category='项目状态')
        self.assertEqual(res_k['category_counts'].get('项目状态'), 1)

    def test_same_timer_with_newer_pending_sync_preserves_newer_request_on_old_error(self):
        import threading

        d1 = str(uuid.uuid4())
        self._insert_doc(d1, self.ds_proj, name='t2.md', sys_meta={'lesson_type': 'state', 'session_date': '2026-11-20'}, node_set=['session_learnings'])
        cat = CatalogIndex(self.cat_path, official_data=self.official_data)
        self.addCleanup(cat.close)
        cat.sync([self.ds_proj])

        same_timer = object()
        old_pending = ((self.ds_proj,), None)
        # Same value as old_pending, distinct tuple object (must be distinguished by identity!)
        new_pending = ((self.ds_proj,), None)
        self.assertIsNot(old_pending, new_pending)
        entered_sig = threading.Event()
        release_sig = threading.Event()
        orig_full_sig = cat._full_stat_sig

        def _failing_old_sig(dt, rr):
            entered_sig.set()
            release_sig.wait(timeout=5.0)
            raise RuntimeError('old pending stat failed')

        cat._full_stat_sig = _failing_old_sig
        with cat._state_lock:
            cat._bg_timer = same_timer
            cat._pending_bg_sync = old_pending

        th = threading.Thread(target=cat._on_bg_timer_fire, args=(same_timer,))
        th.start()
        self.assertTrue(entered_sig.wait(timeout=5.0))
        # Newer read overwrote _pending_bg_sync while same_timer was still firing
        with cat._state_lock:
            cat._pending_bg_sync = new_pending
        release_sig.set()
        th.join(timeout=5.0)
        cat._full_stat_sig = orig_full_sig

        with cat._state_lock:
            self.assertEqual(cat._pending_bg_sync, new_pending)
            self.assertFalse(cat._bg_sync_failed)
            self.assertIsNotNone(cat._bg_timer)
            self.assertIsNot(cat._bg_timer, same_timer)
            cat._cancel_bg_timer_locked()
            cat._pending_bg_sync = None


if __name__ == '__main__':
    unittest.main()
