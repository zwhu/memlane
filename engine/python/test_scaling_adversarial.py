"""Phase 2 scaling tests: recall materialization, incremental SQLite fast paths,
backlog batching, capture pruning, provenance single-writer backfill, and
isolated adversarial 1k + WAL concurrency verification.
"""
import asyncio
import copy
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys
import tempfile
import types
import unittest
from unittest.mock import AsyncMock
import uuid

from aiohttp.test_utils import TestClient, TestServer
from catalog_index import CatalogIndex
from external_capture_store import ExternalCaptureStore
from incremental_processing import IncrementalProcessing
from knowledge_recall import RecallStore
from project_backlog import Backlog
import project_backlog_worker as worker
from project_control_server import create_app
from provenance_store import ProvenanceStore
from sqlite_ro import uuid_hex

SCRIPTS_DIR = Path(__file__).resolve().parents[2] / 'scripts'
if str(SCRIPTS_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPTS_DIR))


class RecallMaterializationAndCatalogDeltaTests(unittest.TestCase):
    def test_wal_backfill_from_legacy_db_and_incremental_rev_sync(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            db_path = root / 'knowledge-recall.sqlite3'
            ds = str(uuid.uuid4())
            doc1, doc2 = str(uuid.uuid4()), str(uuid.uuid4())
            ev1 = str(uuid.uuid4())

            # Simulate legacy recall DB without recall_doc_counts
            raw = sqlite3.connect(db_path)
            raw.executescript('''
                CREATE TABLE recall_meta (singleton INTEGER PRIMARY KEY CHECK(singleton=1), schema_version INTEGER NOT NULL CHECK(schema_version=1), started_at TEXT NOT NULL);
                CREATE TABLE recall_event (agent_id TEXT NOT NULL, event_id TEXT NOT NULL, channel TEXT NOT NULL, received_at TEXT NOT NULL, payload_hash TEXT NOT NULL, PRIMARY KEY(agent_id,event_id));
                CREATE TABLE recall_hit (agent_id TEXT NOT NULL, event_id TEXT NOT NULL, dataset_id TEXT NOT NULL, document_id TEXT NOT NULL, PRIMARY KEY(agent_id,event_id,dataset_id,document_id));
                CREATE TABLE recall_query_stats (channel TEXT PRIMARY KEY, total_queries INTEGER NOT NULL DEFAULT 0, hit_queries INTEGER NOT NULL DEFAULT 0, empty_queries INTEGER NOT NULL DEFAULT 0, total_hits INTEGER NOT NULL DEFAULT 0, last_query_at TEXT);
            ''')
            raw.execute("INSERT INTO recall_meta VALUES(1,1,'2026-01-01T00:00:00Z')")
            raw.execute("INSERT INTO recall_event VALUES('a', ?, 'automatic', '2026-05-01T00:00:00Z', 'h')", (ev1,))
            raw.execute("INSERT INTO recall_hit VALUES('a', ?, ?, ?)", (ev1, ds, doc1))
            raw.commit()
            raw.close()
            db_path.chmod(0o600)

            store = RecallStore(db_path)
            try:
                mode = store.db.execute('PRAGMA journal_mode').fetchone()[0]
                self.assertEqual(str(mode).lower(), 'wal')
                # Legacy row was auto-backfilled into recall_doc_counts
                self.assertEqual(store.stats(ds, doc1)['count'], 1)
                rev_after_backfill = store.last_rev()
                self.assertGreaterEqual(rev_after_backfill, 1)

                # New event increments rev monotonically and updates materialized table
                store.record('a', {
                    'event_id': str(uuid.uuid4()),
                    'channel': 'explicit',
                    'hits': [{'dataset_id': ds, 'document_id': doc1}, {'dataset_id': ds, 'document_id': doc2}],
                })
                changes, new_rev = store.doc_counts_since(rev_after_backfill)
                self.assertGreater(new_rev, rev_after_backfill)
                self.assertEqual(dict(((d, doc), cnt) for d, doc, cnt in changes), {(ds, doc1): 2, (ds, doc2): 1})
                self.assertEqual(store.stats(ds, doc1)['count'], 2)
                self.assertEqual(store.stats(ds, doc2)['count'], 1)
                self.assertEqual(store.doc_counts_since(new_rev)[0], [])
            finally:
                store.close()


class ExternalCapturePruneTests(unittest.TestCase):
    def test_prune_expired_respects_batch_limit_and_preserves_pending(self):
        with tempfile.TemporaryDirectory() as folder:
            store = ExternalCaptureStore(Path(folder) / 'capture-receipts.sqlite3')
            try:
                old_ts = (datetime.now(timezone.utc) - timedelta(days=45)).isoformat(timespec='microseconds')
                fresh_ts = datetime.now(timezone.utc).isoformat(timespec='microseconds')
                ds = str(uuid.uuid4())
                uid = str(uuid.uuid4())
                # Insert 15 old saved, 3 old pending, 2 fresh saved
                store.db.execute('BEGIN IMMEDIATE')
                for i in range(15):
                    store.db.execute(
                        '''INSERT INTO capture_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)''',
                        (f'old-saved-{i}', uid, ds, 's1', f'm-{i}', 'sha', 'qa', f'e-{i}', None, 'saved', old_ts),
                    )
                for i in range(3):
                    store.db.execute(
                        '''INSERT INTO capture_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)''',
                        (f'old-pending-{i}', uid, ds, 's1', f'mp-{i}', 'sha', 'qa', None, 0, 'pending', old_ts),
                    )
                for i in range(2):
                    store.db.execute(
                        '''INSERT INTO capture_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)''',
                        (f'fresh-saved-{i}', uid, ds, 's1', f'mf-{i}', 'sha', 'qa', f'ef-{i}', None, 'saved', fresh_ts),
                    )
                store.db.execute('COMMIT')

                deleted1 = store.prune_expired(max_age_seconds=30 * 86400, batch_limit=10)
                self.assertEqual(deleted1, 10)
                deleted2 = store.prune_expired(max_age_seconds=30 * 86400, batch_limit=10)
                self.assertEqual(deleted2, 5)
                deleted3 = store.prune_expired(max_age_seconds=30 * 86400, batch_limit=10)
                self.assertEqual(deleted3, 0)

                # Pending and fresh saved rows are untouched
                remaining = {r[0]: r[1] for r in store.db.execute('SELECT capture_key, status FROM capture_receipts').fetchall()}
                self.assertEqual(len(remaining), 5)
                self.assertEqual(sum(1 for v in remaining.values() if v == 'pending'), 3)
                self.assertEqual(sum(1 for v in remaining.values() if v == 'saved'), 2)
            finally:
                store.close()


class IncrementalSqliteFastPathTests(unittest.IsolatedAsyncioTestCase):
    async def test_sqlite_capacity_and_over_2mb_session_bypass(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            official_data = root / 'official-store'
            db_dir = official_data / 'system' / 'databases'
            db_dir.mkdir(parents=True)
            cognee_db = db_dir / 'cognee_db'
            cache_db = db_dir / 'cache.db'

            ds = str(uuid.uuid4())
            actor = str(uuid.uuid4())
            sid = 'sess-huge-3mb'

            import bench_scaling
            bench_scaling._create_official_schemas(cognee_db, cache_db)
            con = sqlite3.connect(cognee_db)
            con.execute(
                '''INSERT INTO session_records VALUES(?,?,?,?,?,?,?)''',
                (sid, uuid_hex(actor), uuid_hex(ds), 'a', '1970-01-01 00:16:41.000000', '1970-01-01 00:00:00.000000', 'active'),
            )
            # Active pipeline run makes capacity unavailable without HTTP
            run_id = uuid_hex(str(uuid.uuid4()))
            con.execute(
                '''INSERT INTO pipeline_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                (run_id, run_id, None, uuid_hex(ds), 'improve_pipeline', '', 'DATASET_PROCESSING_STARTED', '', 1, '', 'auto', '2026-10-01 00:00:00', '2026-10-01 00:00:00', None),
            )
            con.commit()
            con.close()

            # Write > 2.5 MiB total QA payloads in cache.db (would blow 2 MiB HTTP limit!)
            ccon = sqlite3.connect(cache_db)
            big_answer = 'X' * (300 * 1024)
            future_exp = (datetime.now(timezone.utc) + timedelta(days=10)).strftime('%Y-%m-%d %H:%M:%S.%f')
            for i in range(9):
                ccon.execute(
                    'INSERT INTO cache_qa_entries(user_id, session_id, payload, expires_at) VALUES(?,?,?,?)',
                    (actor, sid, json.dumps({'question': f'Q{i}', 'answer': big_answer, 'time': '1970-01-01T00:16:41Z'}), future_exp),
                )
            ccon.commit()
            ccon.close()

            http_mock = AsyncMock(side_effect=AssertionError('HTTP must not be called when SQLite fast-path is available'))
            registry = types.SimpleNamespace(
                path=root / 'registry.json',
                state={'sessions': {json.dumps(['a', 'host']): 'p'}, 'projects': {'p': {'dataset_id': ds}}, 'agents': {}},
                official=types.SimpleNamespace(request=http_mock),
            )
            calls = []
            async def execute(row, target, action):
                calls.append(action)
                return {'session_id': sid, 'tokens': [], 'unknown': 0} if action == 'review' else {'status': 'submitted'}

            backlog = types.SimpleNamespace(
                registry=registry,
                jobs={},
                runtime={'official_data': str(official_data)},
                execute=execute,
                checked_target=lambda *a, **k: (
                    {'installation': {'verified': True}, 'principal_id': actor, 'key_file': '/k'},
                    {'policy': {'permissions': ['read', 'write'], 'can_auto_process': True}},
                ),
            )
            now = [1000.0]
            proc = IncrementalProcessing(backlog, clock=lambda: now[0])
            try:
                # Active pipeline run detected via SQLite (zero HTTP calls)
                self.assertFalse(await proc.capacity_available())
                self.assertEqual(http_mock.await_count, 0)

                # Mark run completed in cognee_db -> capacity becomes available
                con = sqlite3.connect(cognee_db)
                done_id = uuid_hex(str(uuid.uuid4()))
                con.execute(
                    '''INSERT INTO pipeline_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                    (done_id, run_id, None, uuid_hex(ds), 'improve_pipeline', '', 'DATASET_PROCESSING_COMPLETED', 'success', 1, '', 'auto', '2026-10-01 00:01:00', '2026-10-01 00:00:00', '2026-10-01 00:01:00'),
                )
                con.commit()
                con.close()
                self.assertTrue(await proc.capacity_available())

                # Tick reads >2.5 MiB session via cache.db SQLite without hitting HTTP 2 MiB cap
                await proc.tick()
                now[0] += 31
                await proc.tick()
                self.assertIn('process', calls)
                self.assertEqual(http_mock.await_count, 0)
            finally:
                await proc.close()


class BacklogBoundedAndBatchFlushTests(unittest.IsolatedAsyncioTestCase):
    async def test_automatic_tick_caps_reviews_per_tick_and_rotates(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            sessions = {json.dumps(['a', f'h-{i:02d}']): 'p' for i in range(20)}
            registry = types.SimpleNamespace(
                path=root / 'registry.json',
                state={
                    'projects': {'p': {'id': 'p', 'dataset_id': str(uuid.uuid4()), 'status': 'verified'}},
                    'agents': {'a': {'installation': {'verified': True}, 'principal_id': str(uuid.uuid4()), 'key_file': '/k'}},
                    'sessions': sessions,
                },
                policy=lambda project, agent_id: {'ready': True, 'policy': {'permissions': ['read', 'write'], 'can_capture': True, 'can_auto_process': False}},
            )
            backlog = Backlog(registry, {})
            backlog.MAX_AUTO_REVIEWS_PER_TICK = 5
            reviewed_hosts = []
            async def fake_execute(row, target, action, update=None):
                reviewed_hosts.append(target['host'])
                return {'session_id': target['host'], 'tokens': [], 'unknown': 0}
            backlog.execute = fake_execute

            await backlog.automatic_tick()
            self.assertEqual(len(reviewed_hosts), 5)
            await backlog.automatic_tick()
            self.assertEqual(len(reviewed_hosts), 10)
            # Round-robin covered 10 distinct hosts across 2 ticks
            self.assertEqual(len(set(reviewed_hosts)), 10)

    def test_worker_save_batches_buffer_json_writes(self):
        import contextlib
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            lock_path = root / 'drain.lock'
            lock_path.write_text('lock')
            buf_path = root / 'buffer.json'

            entries = [{'type': 'qa', 'question': f'q{i}', 'answer': f'a{i}', 'message_id': f'm-{i}'} for i in range(25)]
            tokens = [worker.entry_key(e) for e in entries]
            store_cache = {'k': {'pending_entries': copy.deepcopy(entries), 'capture_retries': {}}}
            write_calls = [0]

            class _FakeCommon:
                _DRAIN_LOCK = lock_path
                _AMBIGUOUS_KEY = '_ambiguous'
                _BUFFER_META_KEYS = {'_ambiguous'}
                def _try_acquire_drain_lock(self):
                    return True
                def _release_drain_lock(self):
                    pass
                @contextlib.contextmanager
                def _buffer_lock(self):
                    yield
                def _write_json_file(self, path, data):
                    write_calls[0] += 1
                    store_cache.clear()
                    store_cache.update(copy.deepcopy(data))
                def remember_entry_via_http(self, dataset, session, outgoing, timeout=12, capture_mode='automatic'):
                    return {'ok': True}

            class _FakePolicy:
                class NotReady(Exception):
                    pass
                def _pending_identity(self, ds, sess):
                    pass
                def require(self, action, ds, **kw):
                    pass
                @contextlib.contextmanager
                def _state(self):
                    yield {}
                def _pending_snapshot(self, ds, sess):
                    st = copy.deepcopy(store_cache['k'])
                    return buf_path, 'k', copy.deepcopy(store_cache), st, list(st['pending_entries']), None
                def stamp_pending_entry(self, ds, sess, entry, flag):
                    pass

            # Patch optional plugin helpers so test runs hermetically
            import types as _t
            fake_cp = _t.ModuleType('_capture_policy')
            fake_cp.allow_tool = lambda fn, params: True
            fake_cp.sanitize = lambda raw: raw
            fake_pc = _t.ModuleType('plugin_capture')
            fake_pc.capture_entry_key = lambda e: e.get('message_id', '')
            fake_pc.capture_payload = lambda raw, ambiguous=False: raw
            fake_pc.persist_capture_summary = lambda common, policy, ds, sess, entry, summary: entry
            fake_pc.entry_held = lambda st, e: False
            fake_pc.retain_failure = lambda *a, **k: None
            fake_pc.release_entry_hold = lambda *a, **k: None

            reports = []
            prev_cp = sys.modules.get('_capture_policy')
            prev_pc = sys.modules.get('plugin_capture')
            sys.modules['_capture_policy'] = fake_cp
            sys.modules['plugin_capture'] = fake_pc
            try:
                worker.save(
                    _FakeCommon(),
                    _FakePolicy(),
                    {'dataset': 'ds', 'session_id': 's1', 'tokens': tokens},
                    stopped=lambda: False,
                    report=reports.append,
                )
            finally:
                if prev_cp is None:
                    sys.modules.pop('_capture_policy', None)
                else:
                    sys.modules['_capture_policy'] = prev_cp
                if prev_pc is None:
                    sys.modules.pop('plugin_capture', None)
                else:
                    sys.modules['plugin_capture'] = prev_pc

            self.assertEqual(reports[-1]['status'], 'completed')
            self.assertEqual(reports[-1]['saved'], 25)
            self.assertEqual(store_cache['k']['pending_entries'], [])
            # 25 entries with FLUSH_BATCH_SIZE=10 -> exactly 3 JSON writes (10 + 10 + 5), NOT 25!
            self.assertEqual(write_calls[0], 3)


class ProvenanceBackfillV2Tests(unittest.IsolatedAsyncioTestCase):
    async def test_single_writer_backfill_resolves_hashes_renames_bak_and_respects_rank(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            official_data = root / 'official-store'
            db_dir = official_data / 'system' / 'databases'
            db_dir.mkdir(parents=True)
            state_dir = root / 'state'
            state_dir.mkdir(parents=True)

            cognee_db = db_dir / 'cognee_db'
            cache_db = db_dir / 'cache.db'
            import bench_scaling
            bench_scaling._create_official_schemas(cognee_db, cache_db)

            ds = str(uuid.uuid4())
            doc_keyed = str(uuid.uuid4())
            doc_locked_qa = str(uuid.uuid4())
            con = sqlite3.connect(cognee_db)
            con.executemany(
                '''INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                [
                    (
                        uuid_hex(doc_keyed), None, uuid_hex(ds), 'a.md', 'Lesson A', '', 'text/markdown',
                        '{}', json.dumps({'lesson_type': 'lesson', 'state_key': 'sk-a'}),
                        json.dumps(['session_learnings']), '{}',
                        '2026-10-20 00:00:00.000000', '2026-10-20 00:00:00.000000', 'h1',
                    ),
                    (
                        uuid_hex(doc_locked_qa), None, uuid_hex(ds), 'b.md', 'Lesson B', '', 'text/markdown',
                        '{}', json.dumps({'lesson_type': 'lesson', 'state_key': 'sk-b', 'first_source_date': '2026-08-10'}),
                        json.dumps(['session_learnings']), '{}',
                        '2026-10-20 00:00:00.000000', '2026-10-20 00:00:00.000000', 'h2',
                    ),
                ],
            )
            con.commit()
            con.close()

            # Official single-writer ProvenanceStore already has linked_qa for doc_locked_qa
            prov = ProvenanceStore(state_dir / 'lesson-provenance.sqlite3', cognee_db_path=cognee_db)
            prov.record_documents_batch([{
                'dataset_id': ds,
                'document_id': doc_locked_qa,
                'lesson_type': 'lesson',
                'state_key': 'sk-b',
                'candidate_dates': ['2026-08-10'],
                'explicit_status': 'linked_qa',
            }])

            identity = 'http://127.0.0.1:18765/tokhash/'
            other_identity = 'http://127.0.0.1:18765/otherhash/'
            id_hash = hashlib.sha256(identity.encode('utf-8')).hexdigest()
            other_hash = hashlib.sha256(other_identity.encode('utf-8')).hexdigest()

            key_a = hashlib.sha256(f'{ds}/lesson/sk-a'.encode('utf-8')).hexdigest()
            key_b = hashlib.sha256(f'{ds}/lesson/sk-b'.encode('utf-8')).hexdigest()
            orphan_key = hashlib.sha256(f'{ds}/orphan'.encode('utf-8')).hexdigest()

            legacy_file = state_dir / f'knowledge-source-dates-{id_hash}.json'
            legacy_file.write_text(json.dumps({
                'schema_version': 1,
                'days': {key_a: '2026-07-04', key_b: '2026-09-01', orphan_key: '2026-01-01'},
            }), encoding='utf-8')
            legacy_file.chmod(0o600)

            # Another identity's file must NEVER be touched or scanned
            other_file = state_dir / f'knowledge-source-dates-{other_hash}.json'
            other_file.write_text(json.dumps({'schema_version': 1, 'days': {key_a: '2020-01-01'}}), encoding='utf-8')
            other_file.chmod(0o600)

            manager_token = 'm' * 40
            forwarded_calls = []

            class _FakeOfficialForwarder:
                async def request(self, method, path, body=None, token_file=None, *, adapter_token=None):
                    self.last_token = adapter_token
                    forwarded_calls.append((method, path, body))
                    return prov.backfill_legacy_dates(body['entries'])

            official = _FakeOfficialForwarder()
            registry = types.SimpleNamespace(
                path=state_dir / 'project-registry.json',
                state={
                    'projects': {
                        'p1': {'id': 'p1', 'name': 'P1', 'dataset_id': ds, 'status': 'verified', 'aliases': []},
                    },
                    'agents': {},
                    'sessions': {},
                },
                mutex=asyncio.Lock(),
                official=official,
            )
            registry.managed_groups = lambda: [registry.state['projects']['p1']]
            app = create_app(registry, manager_token, {'a': 'a' * 40}, codex_config={'official_data': str(official_data)})
            client = TestClient(TestServer(app))
            await client.start_server()
            try:
                headers = {'Authorization': 'Bearer ' + manager_token}
                # Capabilities advertises provenance_backfill_v2
                cap_resp = await client.get('/v1/capabilities', headers=headers)
                self.assertEqual(cap_resp.status, 200)
                self.assertIn('provenance_backfill_v2', (await cap_resp.json())['features'])

                # Execute backfill with an unmapped orphan key: imports mapped entries but MUST NOT rename to .migrated.bak!
                resp = await client.post(
                    '/v1/provenance/backfill-legacy-dates',
                    json={'reading_identity': identity},
                    headers=headers,
                )
                self.assertEqual(resp.status, 200, await resp.text())
                body = await resp.json()
                self.assertEqual(body['status'], 'partial')
                self.assertEqual(body['matched'], 2)
                self.assertEqual(body['imported'], 2)
                self.assertEqual(body['skipped'], 1)
                self.assertEqual(body['unmapped'], 1)
                self.assertFalse(body['has_unconfirmed_projects'])
                self.assertEqual(body['backup_path'], '')
                self.assertTrue(legacy_file.exists())
                # Other identity file untouched
                self.assertTrue(other_file.is_file())
                self.assertEqual(official.last_token, manager_token)
                self.assertEqual(forwarded_calls[0][1], '/api/v1/memlane/provenance/backfill-legacy-dates')

                # Check ProvenanceStore evidence ranks & dates:
                # doc_keyed got legacy_json_locked (2026-07-04), NEVER linked_qa!
                # doc_locked_qa kept earlier linked_qa (2026-08-10)!
                row_a = prov.db.execute(
                    'SELECT first_source_date, source_date_status FROM document_provenance WHERE dataset_id=? AND document_id=?',
                    (ds, doc_keyed),
                ).fetchone()
                self.assertEqual(row_a, ('2026-07-04', 'legacy_json_locked'))
                row_b = prov.db.execute(
                    'SELECT first_source_date, source_date_status FROM document_provenance WHERE dataset_id=? AND document_id=?',
                    (ds, doc_locked_qa),
                ).fetchone()
                self.assertEqual(row_b, ('2026-08-10', 'linked_qa'))

                # Once all keys map and no unconfirmed projects remain, backfill completes and renames to .migrated.bak
                legacy_file.write_text(json.dumps({
                    'schema_version': 1,
                    'days': {key_a: '2026-07-04', key_b: '2026-09-01'},
                }), encoding='utf-8')
                legacy_file.chmod(0o600)
                resp_full = await client.post(
                    '/v1/provenance/backfill-legacy-dates',
                    json={'reading_identity': identity},
                    headers=headers,
                )
                self.assertEqual(resp_full.status, 200, await resp_full.text())
                body_full = await resp_full.json()
                self.assertEqual(body_full['status'], 'migrated')
                self.assertEqual(body_full['unmapped'], 0)
                self.assertFalse(body_full['has_unconfirmed_projects'])
                self.assertTrue(body_full['backup_path'].endswith('.migrated.bak'))
                self.assertFalse(legacy_file.exists())
                self.assertTrue(Path(body_full['backup_path']).is_file())

                # Subsequent call returns absent cleanly (idempotent)
                resp2 = await client.post(
                    '/v1/provenance/backfill-legacy-dates',
                    json={'reading_identity': identity},
                    headers=headers,
                )
                self.assertEqual(resp2.status, 200)
                self.assertEqual((await resp2.json())['status'], 'absent')
            finally:
                await client.close()
                prov.close()


class AdversarialScaling1kTests(unittest.IsolatedAsyncioTestCase):
    async def test_1k_adversarial_distributions_eqp_and_wal_concurrency(self):
        import bench_scaling
        report = await bench_scaling.run_benchmark(scale='1k', concurrency_seconds=1.5)
        failures = bench_scaling.check_sla(report)
        self.assertEqual(failures, [], f'1k adversarial SLA failures: {failures}\nreport={json.dumps(report, indent=2)}')
        self.assertGreater(report['wal_concurrency']['writes_committed'], 0)
        self.assertGreater(report['wal_concurrency']['reads_completed'], 20)


if __name__ == '__main__':
    unittest.main()
