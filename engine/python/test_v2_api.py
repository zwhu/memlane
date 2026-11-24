"""End-to-end HTTP integration tests for all Phase 1 /v1/* endpoints."""
import hashlib
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import uuid

from aiohttp.test_utils import AioHTTPTestCase, TestClient, TestServer

from knowledge_recall import RecallStore
from project_control import Registry
from project_control_server import create_app
from reading_state_store import knowledge_read_scope_hash


class _DummyOfficial:
    def __init__(self, authorized=()):
        self.authorized = set(authorized)

    def authorized_datasets(self):
        return frozenset(self.authorized)


class LegacyProvenanceArchiveTests(unittest.TestCase):
    def test_archive_skips_lock_holder_and_rechecks_atomic_replacement(self):
        import fcntl
        import os
        from v2_api import _archive_legacy_dates_if_unchanged

        with tempfile.TemporaryDirectory() as tmp:
            legacy = Path(tmp) / 'knowledge-source-dates-instance.json'
            legacy.write_text('previous', encoding='utf-8')
            snapshot = legacy.stat()
            expected = (snapshot.st_ino, snapshot.st_size, snapshot.st_mtime_ns)
            lock = legacy.with_suffix('.json.lock')

            with lock.open('wb') as writer:
                fcntl.flock(writer, fcntl.LOCK_EX)
                self.assertEqual(_archive_legacy_dates_if_unchanged(legacy, expected), ('', False))
                replacement = legacy.with_suffix('.new')
                replacement.write_text('new file', encoding='utf-8')
                os.replace(replacement, legacy)

            self.assertEqual(_archive_legacy_dates_if_unchanged(legacy, expected), ('', False))
            self.assertEqual(legacy.read_text(encoding='utf-8'), 'new file')
            current = legacy.stat()
            backup, archived = _archive_legacy_dates_if_unchanged(
                legacy, (current.st_ino, current.st_size, current.st_mtime_ns),
            )
            self.assertTrue(archived)
            self.assertEqual(Path(backup).read_text(encoding='utf-8'), 'new file')


class V2APITests(AioHTTPTestCase):
    async def get_application(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.official_data = self.root / 'official-store'
        self.db_dir = self.official_data / 'system' / 'databases'
        self.data_dir = self.official_data / 'data'
        self.db_dir.mkdir(parents=True)
        self.data_dir.mkdir(parents=True)
        self.cognee_db = self.db_dir / 'cognee_db'
        self.cache_db = self.db_dir / 'cache.db'

        self.ds_proj = str(uuid.uuid4())
        self.ds_common = str(uuid.uuid4())
        self.ds_unconf = str(uuid.uuid4())
        self.user_a = str(uuid.uuid4())
        self.user_b = str(uuid.uuid4())

        self.identity = f"http://127.0.0.1:61255/{hashlib.sha256(b'tok').hexdigest()}/"
        self.manager_headers = {
            'Authorization': 'Bearer ' + 'm' * 40,
            'X-Memlane-Reading-Identity': self.identity,
            'X-Memlane-Reading-Contract': 'catalog-cursor-v1',
        }
        self.agent_headers = {'Authorization': 'Bearer ' + 'a' * 40}

        # Populate cognee_db
        c1 = sqlite3.connect(self.cognee_db)
        c1.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), legacy_id CHAR(32),
                name VARCHAR, label VARCHAR, mime_type VARCHAR, raw_data_location VARCHAR,
                external_metadata JSON, system_metadata JSON, node_set JSON,
                pipeline_status JSON, updated_at DATETIME, created_at DATETIME, content_hash VARCHAR
            );
            CREATE TABLE session_records (
                session_id VARCHAR NOT NULL, user_id CHAR(32) NOT NULL, dataset_id CHAR(32),
                status VARCHAR, started_at DATETIME, last_activity_at DATETIME, agent_id VARCHAR,
                PRIMARY KEY (session_id, user_id)
            );
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY, pipeline_run_id CHAR(32), parent_operation_id CHAR(32),
                dataset_id CHAR(32), pipeline_name VARCHAR, operation_name VARCHAR,
                status VARCHAR, outcome VARCHAR, background BOOLEAN, error_class VARCHAR,
                origin VARCHAR, created_at DATETIME, started_at DATETIME, ended_at DATETIME
            );
        ''')

        # 15 project knowledge docs (10 state, 5 pitfall) + 1 embedded common + 3 raw docs
        self.k_docs = []
        for i in range(15):
            did = str(uuid.uuid4())
            self.k_docs.append(did)
            fp = self.data_dir / f'k-{i}.md'
            fp.write_text(f'# Session learning (session s-{i})\n\nKnowledge body number {i}. ' + ('说明文字' * 20), encoding='utf-8')
            lt = 'state' if i < 10 else 'pitfall'
            day = '2026-11-21' if i < 8 else '2026-11-20'
            c1.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(did).hex, uuid.UUID(self.ds_proj).hex, None,
                    f'text_{i:032x}.md', f'Topic {i}' if i == 0 else None, 'text/markdown',
                    f'file://{fp}',
                    json.dumps({'why_learned': 'context'}),
                    json.dumps({'lesson_type': lt, 'state_key': f'sk:{i}', 'session_date': day}),
                    json.dumps(json.dumps(['session_learnings', f'session_learnings:s-{i}'])),
                    json.dumps({'cognify_pipeline': {self.ds_proj: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
                    f'2026-11-21 10:00:{i:02d}', f'2026-11-21 10:00:{i:02d}', f'hk-{i}',
                ),
            )

        # Embedded common doc inside project dataset (must be excluded from category_counts!)
        self.embedded_common = str(uuid.uuid4())
        embed_fp = self.data_dir / 'embed-common.md'
        embed_fp.write_text('# Embedded Common\n\nCommon preference note stored in project dataset.', encoding='utf-8')
        c1.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.embedded_common).hex, uuid.UUID(self.ds_proj).hex, None,
                'embed-common.md', 'Embedded Common', 'text/markdown', f'file://{embed_fp}',
                json.dumps({'memlane_kind': 'common_knowledge', 'category': 'preference'}), None, None,
                '{}', '2026-11-21 09:00:00', '2026-11-21 09:00:00', 'he-c',
            ),
        )

        # Common dataset docs (2 preference, 1 experience)
        self.common_docs = [str(uuid.uuid4()) for _ in range(3)]
        for idx, (did, cat_val, upd) in enumerate((
            (self.common_docs[0], 'preference', '2026-11-21 12:00:00'),
            (self.common_docs[1], 'experience', '2026-11-21 11:00:00'),
            (self.common_docs[2], 'preference', '2026-11-21 10:00:00'),
        )):
            c1.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(did).hex, uuid.UUID(self.ds_common).hex, None,
                    f'common-{idx}.md', f'Common {idx}', 'text/markdown', '',
                    json.dumps({'memlane_kind': 'common_knowledge', 'category': cat_val}), None, None,
                    '{}', upd, '2026-11-20 08:00:00', f'hc-{idx}',
                ),
            )

        # 3 raw docs (1 pending, 2 completed) interleaved with session timestamps
        self.raw_docs = [str(uuid.uuid4()) for _ in range(3)]
        for idx, (did, ts, status_obj) in enumerate((
            (self.raw_docs[0], '2026-11-21 10:00:00', {}),
            (self.raw_docs[1], '2026-11-21 09:30:00', {'cognify_pipeline': {self.ds_proj: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
            (self.raw_docs[2], '2026-11-21 09:00:00', {'cognify_pipeline': {self.ds_proj: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
        )):
            c1.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(did).hex, uuid.UUID(self.ds_proj).hex, None,
                    f'raw-{idx}.md', f'Raw Doc {idx}', 'text/markdown', '',
                    json.dumps({'node_set': ['project_docs']}), None, None,
                    json.dumps(status_obj), ts, ts, f'hr-{idx}',
                ),
            )

        # Unconfirmed project doc
        self.unconf_doc = str(uuid.uuid4())
        c1.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.unconf_doc).hex, uuid.UUID(self.ds_unconf).hex, None,
                'secret.md', 'Secret', 'text/markdown', '',
                json.dumps({'node_set': ['session_learnings']}), None, None,
                '{}', '2026-11-21 08:00:00', '2026-11-21 08:00:00', 'hu',
            ),
        )

        # Sessions: 2 users sharing IDENTICAL session_id + timestamp as raw_docs[0], plus 1 parent with 8 children!
        c1.executemany(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            [
                ('sess-tie', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 09:00:00', '2026-11-21 10:00:00', 'codex'),
                ('sess-tie', uuid.UUID(self.user_b).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 09:00:00', '2026-11-21 10:00:00', 'claude'),
                ('sess-parent', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 08:00:00', '2026-11-21 09:45:00', 'codex'),
                *[
                    (f'sess-child-{i}', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_proj).hex, 'active', '2026-11-21 08:05:00', f'2026-11-21 09:40:{i:02d}', 'codex')
                    for i in range(8)
                ],
            ],
        )
        c1.commit()
        c1.close()

        # Populate cache.db with 1 expired row on sess-tie/user_a + valid QAs on sess-parent & children
        c2 = sqlite3.connect(self.cache_db)
        c2.executescript('''
            CREATE TABLE cache_qa_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                qa_id TEXT NOT NULL, payload JSON NOT NULL,
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
            );
            CREATE INDEX idx_cache_qa_session ON cache_qa_entries (user_id, session_id, seq);
            CREATE TABLE cache_trace_entries (
                seq INTEGER PRIMARY KEY, user_id TEXT NOT NULL, session_id TEXT NOT NULL,
                payload JSON NOT NULL,
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL, expires_at DATETIME
            );
            CREATE INDEX idx_cache_trace_session ON cache_trace_entries (user_id, session_id, seq);
        ''')
        c2.executemany(
            'INSERT INTO cache_qa_entries (seq, user_id, session_id, qa_id, payload, expires_at) VALUES (?,?,?,?,?,?)',
            [
                (1, self.user_a, 'sess-tie', 'q-exp', json.dumps({'question': 'EXPIRED'}), '2020-01-01 00:00:00'),
                (2, self.user_a, 'sess-tie', 'q-empty', json.dumps({'question': ''}), '2099-01-01 00:00:00'),
                (3, uuid.UUID(self.user_a).hex, 'sess-tie', 'q-ok', json.dumps({'question': 'Tie session question A'}), '2099-01-01 00:00:00'),
                (4, self.user_b, 'sess-tie', 'q-b', json.dumps({'question': 'Tie session question B'}), '2099-01-01 00:00:00'),
                (5, self.user_a, 'sess-parent', 'q-p', json.dumps({'question': 'Parent task question'}), '2099-01-01 00:00:00'),
            ],
        )
        c2.execute(
            'INSERT INTO cache_trace_entries (seq, user_id, session_id, payload, expires_at) VALUES (?,?,?,?,?)',
            (1, self.user_a, 'sess-parent', json.dumps({'origin_function': 'Read'}), '2099-01-01 00:00:00'),
        )
        c2.commit()
        c2.close()

        # Seed recall events for composite (agent_id, event_id) filter testing
        self.recall_event_a = str(uuid.uuid4())
        rec = RecallStore(self.root / 'knowledge-recall.sqlite3')
        rec.record('agent-1', {
            'event_id': self.recall_event_a,
            'channel': 'automatic',
            'hits': [
                {'dataset_id': self.ds_proj, 'document_id': self.k_docs[0]},
                {'dataset_id': self.ds_proj, 'document_id': self.k_docs[10]},
            ],
        })
        rec.record('agent-2', {
            'event_id': self.recall_event_a,  # SAME event_id, DIFFERENT agent_id!
            'channel': 'explicit',
            'hits': [{'dataset_id': self.ds_proj, 'document_id': self.k_docs[5]}],
        })
        rec.close()

        # Pre-seed a legacy knowledge-read-<hash>.json so all 15 k_docs are unread (> enabled_at)
        sh = knowledge_read_scope_hash(self.identity, 'p1', self.ds_proj)
        (self.root / f'knowledge-read-{sh}.json').write_text(
            json.dumps({
                'version': 1,
                'initialized': True,
                'enabled_at': '2026-11-21T09:59:00.000000000Z',
                'knowledge_ids': {},
            }),
            encoding='utf-8',
        )

        self.dummy_official = _DummyOfficial({self.ds_proj, self.ds_common})
        self.registry = Registry(self.root / 'registry.json', self.dummy_official)
        self.registry.state['projects'] = {
            'p1': {'id': 'p1', 'name': 'Alpha', 'dataset_id': self.ds_proj, 'status': 'verified'},
            'p-unconf': {'id': 'p-unconf', 'name': 'Unconfirmed', 'dataset_id': self.ds_unconf, 'status': 'unconfirmed'},
        }
        self.registry.state['common'] = {
            'id': 'common', 'kind': 'common', 'dataset_id': self.ds_common, 'status': 'verified',
        }
        self.registry.state['session_relations'] = {
            f'sess-child-{i}': {'parent_session_id': 'sess-parent', 'subagent_role': 'worker', 'nesting_depth': 1}
            for i in range(8)
        }
        return create_app(
            self.registry,
            'm' * 40,
            {'agent-1': 'a' * 40},
            codex_config={'official_data': str(self.official_data)},
        )

    async def tearDownAsync(self):
        await super().tearDownAsync()
        self.registry.close()
        self.tmp.cleanup()

    async def _warm_large_catalog(self):
        cat = self.app['v2_api_handles'].catalog_index
        # Exercise the large-catalog policy on this small fixture.
        cat._FAST_DOC_COUNT_THRESHOLD = 0
        cat._FAST_SYNC_THRESHOLD_NS = 0
        cat._BG_COALESCE_MIN_NS = cat._BG_COALESCE_MAX_NS = 60_000_000_000
        response = await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)
        self.assertEqual(response.status, 200)
        await response.read()
        return cat

    def _append_knowledge_doc(self, source_id):
        doc_id = uuid.uuid4()
        with sqlite3.connect(self.cognee_db) as db:
            row = list(db.execute('SELECT * FROM data WHERE id = ?', (uuid.UUID(source_id).hex,)).fetchone())
            row[0] = doc_id.hex
            db.execute('INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)', row)
        return str(doc_id)

    async def test_stale_catalog_retries_new_knowledge_anchors_and_session_children(self):
        cat = await self._warm_large_catalog()
        for source_id, dataset_id, path in (
            (self.k_docs[0], self.ds_proj, '/v1/projects/p1/knowledge'),
            (self.common_docs[0], self.ds_common, '/v1/common/knowledge'),
        ):
            with self.subTest(path=path):
                doc_id = self._append_knowledge_doc(source_id)
                self.assertFalse(cat.fetch_docs_by_ids(dataset_id, [doc_id]))
                response = await self.client.get(
                    f'{path}?anchor_doc_id={doc_id}', headers=self.manager_headers,
                )
                self.assertEqual(response.status, 200)
                self.assertIn(doc_id, {row['doc_id'] for row in (await response.json())['items']})
                self.assertFalse(cat.is_catchup_pending())

        with sqlite3.connect(self.cognee_db) as db:
            db.execute(
                '''INSERT INTO session_records
                   SELECT 'sess-fresh', user_id, dataset_id, status, started_at, last_activity_at, agent_id
                   FROM session_records WHERE session_id = 'sess-parent' ''',
            )
        response = await self.client.get(
            '/v1/projects/p1/raw-records/sessions/sess-fresh/children', headers=self.manager_headers,
        )
        self.assertEqual(response.status, 200)
        self.assertEqual((await response.json())['items'], [])
        self.assertFalse(cat.is_catchup_pending())

    async def test_mark_read_and_mark_all_read_sync_through_coalesce_window(self):
        cat = await self._warm_large_catalog()
        body = {'reading_identity': self.identity, 'project_id': 'p1', 'dataset_id': self.ds_proj}
        doc_id = self._append_knowledge_doc(self.k_docs[0])
        response = await self.client.post(
            '/v1/reading-state/mark-read', headers=self.manager_headers, json={**body, 'doc_ids': [doc_id]},
        )
        self.assertEqual(response.status, 200)
        result = await response.json()
        self.assertTrue(result['unread_count_known'])
        self.assertEqual(result['unread_count'], 15)
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc_id])), 1)

        doc_id = self._append_knowledge_doc(self.k_docs[1])
        response = await self.client.post(
            '/v1/reading-state/mark-all-read', headers=self.manager_headers, json=body,
        )
        self.assertEqual(response.status, 200)
        result = await response.json()
        self.assertTrue(result['unread_count_known'])
        self.assertEqual(result['unread_count'], 0)
        self.assertEqual(len(cat.fetch_docs_by_ids(self.ds_proj, [doc_id])), 1)

    async def test_stale_catalog_does_not_serve_revoked_dataset(self):
        cat = await self._warm_large_catalog()
        new_id = self._append_knowledge_doc(self.k_docs[0])
        path = '/v1/projects/p1/knowledge'
        response = await self.client.get(path, headers=self.manager_headers)
        self.assertEqual(response.status, 200)
        self.assertNotIn(new_id, {row['doc_id'] for row in (await response.json())['items']})
        self.assertTrue(cat.is_catchup_pending())

        self.dummy_official.authorized.discard(self.ds_proj)
        response = await self.client.get(
            f'{path}?anchor_doc_id={self.k_docs[0]}', headers=self.manager_headers,
        )
        self.assertEqual(response.status, 403)
        self.assertTrue(cat.fetch_docs_by_ids(self.ds_proj, [self.k_docs[0]]))
        self.assertTrue(cat.is_catchup_pending())

    async def test_unified_reading_contract_and_legacy_lesson_date(self):
        caps = await self.client.get('/v1/capabilities', headers=self.manager_headers)
        self.assertEqual(caps.status, 200)
        self.assertEqual((await caps.json())['reading_contract'], 'catalog-cursor-v1')
        old_version = await self.client.get('/v2/overview/bootstrap', headers=self.manager_headers)
        self.assertEqual(old_version.status, 404)

        old_client = await self.client.get(
            '/v1/overview/bootstrap', headers={'Authorization': self.manager_headers['Authorization']},
        )
        self.assertEqual(old_client.status, 409)
        self.assertEqual((await old_client.json())['error'], 'reading_contract_mismatch')

        doc_id = self.k_docs[0]
        with sqlite3.connect(self.cognee_db) as db:
            db.execute(
                "UPDATE data SET system_metadata = NULL, external_metadata = ? WHERE id = ?",
                (json.dumps({'lesson_type': 'state', 'session_date': '2026-11-19',
                             'first_source_date': '1999-01-01'}), uuid.UUID(doc_id).hex),
            )
        document = await self.client.get(
            f'/v1/datasets/{self.ds_proj}/documents/{doc_id}', headers=self.manager_headers,
        )
        self.assertEqual(document.status, 200)
        self.assertEqual((await document.json())['effective_date'], '2026-11-19')
        overview = await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)
        self.assertEqual(overview.status, 200)
        items = (await overview.json())['recent_items']
        item = next(row for row in items if row['doc_id'] == doc_id)
        self.assertEqual(item['effective_date'], '2026-11-19')

    async def test_common_recall_filter_uses_bounded_event_pages(self):
        event_id = str(uuid.uuid4())
        store = RecallStore(self.root / 'knowledge-recall.sqlite3')
        try:
            store.record('agent-1', {
                'event_id': event_id, 'channel': 'explicit',
                'hits': [
                    {'dataset_id': self.ds_common, 'document_id': self.common_docs[0]},
                    {'dataset_id': self.ds_common, 'document_id': self.common_docs[1]},
                    {'dataset_id': self.ds_proj, 'document_id': self.k_docs[0]},
                ],
            })
        finally:
            store.close()
        filter_query = f'&recall_agent_id=agent-1&recall_event_id={event_id}'
        path = '/v1/common/knowledge?limit=1'
        first = await self.client.get(path + filter_query, headers=self.manager_headers)
        self.assertEqual(first.status, 200)
        page = await first.json()
        self.assertEqual(page['total_filtered'], 2)
        self.assertEqual(page['items'][0]['doc_id'], self.common_docs[0])
        cursor = page['next_cursor']
        second = await self.client.get(path + filter_query + '&cursor=' + cursor, headers=self.manager_headers)
        self.assertEqual(second.status, 200)
        self.assertEqual((await second.json())['items'][0]['doc_id'], self.common_docs[1])
        crossed = await self.client.get(path + '&cursor=' + cursor, headers=self.manager_headers)
        self.assertEqual(crossed.status, 400)

    async def test_migrate_local_skips_unconfirmed_project_in_batch(self):
        response = await self.client.post(
            '/v1/reading-state/migrate-local',
            headers=self.manager_headers,
            json={'reading_identity': self.identity, 'projects': [
                {'project_id': 'p-unconf', 'dataset_id': self.ds_unconf},
                {'project_id': 'p1', 'dataset_id': self.ds_proj},
            ]},
        )
        self.assertEqual(response.status, 200)
        rows = {row['project_id']: row for row in (await response.json())['migrated']}
        self.assertEqual(rows['p-unconf']['status'], 'unconfirmed')
        self.assertEqual(rows['p1']['status'], 'migrated')
        self.assertEqual(rows['p1']['unread_count'], 15)

    async def test_project_reads_retry_when_catalog_publishes_after_unread_snapshot(self):
        from unittest.mock import patch

        handles = self.app['v2_api_handles']
        cat, store = handles.catalog_index, handles.reading_state
        summary_path = '/v1/projects/p1/message-summary'
        initial = await self.client.get(summary_path, headers=self.manager_headers)
        self.assertEqual(initial.status, 200)
        await initial.read()
        snapshot = store.compute_unread_snapshot
        for path in ('/v1/projects/p1/knowledge?limit=50', summary_path):
            with self.subTest(path=path):
                store.mark_all_read(self.identity, 'p1', self.ds_proj, catalog_index=cat)
                late = uuid.uuid4()
                published = False

                def snapshot_then_publish(*args):
                    nonlocal published
                    result = snapshot(*args)
                    if not published:
                        with sqlite3.connect(self.cognee_db) as con:
                            con.execute(
                                """INSERT INTO data
                                   SELECT ?, dataset_id, legacy_id, name, label, mime_type, raw_data_location,
                                          external_metadata, system_metadata, node_set, pipeline_status,
                                          updated_at, 'invalid', content_hash FROM data WHERE id = ?""",
                                (late.hex, uuid.UUID(self.k_docs[0]).hex),
                            )
                        cat.sync([self.ds_proj, self.ds_common])
                        published = True
                    return result

                with patch.object(store, 'compute_unread_snapshot', side_effect=snapshot_then_publish):
                    response = await self.client.get(path, headers=self.manager_headers)
                    self.assertEqual(response.status, 200)
                    body = await response.json()
                self.assertEqual(body['unread_count'], 1)
                version = cat.dataset_sync_info(self.ds_proj)['catalog_version']
                if path == summary_path:
                    self.assertEqual(body['knowledge_catalog_version'], version)
                    self.assertEqual([row['doc_id'] for row in body['unread_preview']], [str(late)])
                else:
                    self.assertEqual(body['catalog_version'], version)
                    self.assertEqual([row['doc_id'] for row in body['items'] if row['is_unread']], [str(late)])

    async def test_project_reads_fail_closed_after_catalog_retry_exhaustion(self):
        from itertools import count
        from unittest.mock import patch

        handles = self.app['v2_api_handles']
        paths = ('/v1/projects/p1/knowledge', '/v1/projects/p1/message-summary')
        for path in paths:
            with self.subTest(path=path):
                with (
                    patch.object(handles.catalog_index, 'generation', side_effect=count()),
                    patch.object(handles.reading_state, 'compute_unread_snapshot', return_value=(set(), 1)) as snapshot,
                ):
                    response = await self.client.get(path, headers=self.manager_headers)
                    self.assertEqual(response.status, 409)
                    self.assertEqual((await response.json())['error'], 'catalog_generation_conflict')
                    self.assertEqual(snapshot.call_count, 3)

    async def test_overview_body_knowledge_common_search_and_reading_state_flow(self):
        # 1. Migrate local reading state via POST /v1/reading-state/migrate-local
        mig = await self.client.post(
            '/v1/reading-state/migrate-local',
            headers=self.manager_headers,
            json={'reading_identity': self.identity},
        )
        self.assertEqual(mig.status, 200)
        mig_body = await mig.json()
        self.assertEqual(mig_body['migrated'][0]['status'], 'migrated')
        self.assertEqual(mig_body['migrated'][0]['unread_count'], 15)

        # 2. GET /v1/overview/bootstrap (<= 128 KiB, excludes unconfirmed project)
        ov_resp = await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)
        self.assertEqual(ov_resp.status, 200)
        raw_ov = await ov_resp.read()
        self.assertLessEqual(len(raw_ov), 128 * 1024)
        ov = json.loads(raw_ov)
        self.assertEqual([p['project_id'] for p in ov['projects']], ['p1'])
        self.assertEqual(ov['projects'][0]['unread_count'], 15)
        self.assertEqual(ov['common']['knowledge_count'], 3)
        self.assertGreater(len(ov['day_buckets']), 0)
        self.assertTrue(ov['recent_items'][0]['preview_text'])

        # 3. GET /v1/overview/day cursor pagination + tamper rejection
        day1 = await (await self.client.get('/v1/overview/day?day=2026-11-21&limit=4', headers=self.manager_headers)).json()
        self.assertEqual(len(day1['items']), 4)
        self.assertTrue(day1['has_more'])
        day2 = await (await self.client.get(f"/v1/overview/day?day=2026-11-21&limit=4&cursor={day1['next_cursor']}", headers=self.manager_headers)).json()
        self.assertEqual(len(day2['items']), 4)
        self.assertFalse(set(i['doc_id'] for i in day1['items']) & set(i['doc_id'] for i in day2['items']))
        bad_cur = await self.client.get(f"/v1/overview/day?day=2026-11-20&cursor={day1['next_cursor']}", headers=self.manager_headers)
        self.assertEqual(bad_cur.status, 400)
        self.assertEqual((await bad_cur.json())['error'], 'invalid_cursor')

        # 4. GET /v1/projects/p1/knowledge: unconfirmed rejected, category_counts semantics, composite recall key
        self.assertEqual((await self.client.get('/v1/projects/p-unconf/knowledge', headers=self.manager_headers)).status, 403)
        self.assertEqual((await self.client.get(f'/v1/projects/p1/knowledge?recall_event_id={self.recall_event_a}', headers=self.manager_headers)).status, 400)

        # Composite (agent-1, event) returns 2 hits (1 项目状态 + 1 踩坑经验); filtering category keeps BOTH in category_counts!
        rec_filtered = await (await self.client.get(
            f'/v1/projects/p1/knowledge?recall_agent_id=agent-1&recall_event_id={self.recall_event_a}&category=项目状态',
            headers=self.manager_headers,
        )).json()
        self.assertEqual(rec_filtered['category_counts'], {'项目状态': 1, '踩坑经验': 1})
        self.assertEqual([i['doc_id'] for i in rec_filtered['items']], [self.k_docs[0]])

        # Different agent_id with SAME event_id gets its own distinct hit!
        rec_a2 = await (await self.client.get(
            f'/v1/projects/p1/knowledge?recall_agent_id=agent-2&recall_event_id={self.recall_event_a}',
            headers=self.manager_headers,
        )).json()
        self.assertEqual([i['doc_id'] for i in rec_a2['items']], [self.k_docs[5]])

        # Full pagination through all 16 project knowledge items (15 lessons + 1 embedded common, 0 dupes)
        seen_k = []
        cursor = ''
        while True:
            q = f'/v1/projects/p1/knowledge?limit=5' + (f'&cursor={cursor}' if cursor else '')
            page = await (await self.client.get(q, headers=self.manager_headers)).json()
            seen_k.extend(i['doc_id'] for i in page['items'])
            if not page['has_more']:
                break
            cursor = page['next_cursor']
        self.assertEqual(len(seen_k), 16)
        self.assertEqual(len(set(seen_k)), 16)

        # 5. GET /v1/common/knowledge ordering + filter
        com_all = await (await self.client.get('/v1/common/knowledge?limit=10', headers=self.manager_headers)).json()
        self.assertEqual([i['doc_id'] for i in com_all['items']], self.common_docs)
        com_pref = await (await self.client.get('/v1/common/knowledge?common_category=preference', headers=self.manager_headers)).json()
        self.assertEqual([i['doc_id'] for i in com_pref['items']], [self.common_docs[0], self.common_docs[2]])

        # 6. GET /v1/datasets/{ds}/documents/{doc}/body fast-path
        body_resp = await self.client.get(f'/v1/datasets/{self.ds_proj}/documents/{self.k_docs[0]}/body', headers=self.manager_headers)
        self.assertEqual(body_resp.status, 200)
        b_json = await body_resp.json()
        self.assertEqual(b_json['source'], 'local_fastpath')
        self.assertIn('Knowledge body number 0', b_json['body'])

        # 7. GET /v1/search/recent & /v1/search/metadata
        recent = await (await self.client.get('/v1/search/recent?limit=5', headers=self.manager_headers)).json()
        self.assertEqual(len(recent['knowledge_items']), 5)
        self.assertGreaterEqual(len(recent['session_items']), 1)

        meta_s = await (await self.client.get('/v1/search/metadata?q=Topic%200&filter=knowledge', headers=self.manager_headers)).json()
        self.assertEqual([i['doc_id'] for i in meta_s['items']], [self.k_docs[0]])
        self.assertEqual(meta_s['items'][0]['match_field'], 'doc_label')

        # 8. GET /v1/projects/p1/message-summary (slim < 16 KiB, unread_count=15, preview=10, unread_has_more=True)
        ms_resp = await self.client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)
        self.assertEqual(ms_resp.status, 200)
        ms_raw = await ms_resp.read()
        self.assertLessEqual(len(ms_raw), 16 * 1024)
        ms = json.loads(ms_raw)
        self.assertNotIn('knowledge_catalog', ms)
        self.assertEqual(ms['unread_count'], 15)
        self.assertEqual(len(ms['unread_preview']), 10)
        self.assertTrue(ms['unread_has_more'])
        self.assertEqual(ms['pending_documents_count'], 1)

        # 9. POST /v1/reading-state/mark-read & mark-all-read (Contract #1)
        mr = await (await self.client.post(
            '/v1/reading-state/mark-read',
            headers=self.manager_headers,
            json={'reading_identity': self.identity, 'project_id': 'p1', 'dataset_id': self.ds_proj, 'doc_ids': [self.k_docs[0]]},
        )).json()
        self.assertEqual(mr['unread_count'], 14)
        self.assertTrue(mr['unread_count_known'])
        self.assertGreater(mr['state_rev'], 0)

        mar = await (await self.client.post(
            '/v1/reading-state/mark-all-read',
            headers=self.manager_headers,
            json={'reading_identity': self.identity, 'project_id': 'p1', 'dataset_id': self.ds_proj},
        )).json()
        self.assertEqual(mar['unread_count'], 0)
        self.assertTrue(mar['unread_count_known'])
        self.assertGreater(mar['state_rev'], mr['state_rev'])

        # Reopen the same source and derived stores behind a fresh HTTP server.
        await self.client.close()
        restarted = create_app(
            self.registry, 'm' * 40, {'agent-1': 'a' * 40},
            codex_config={'official_data': str(self.official_data)},
        )
        async with TestClient(TestServer(restarted)) as client:
            response = await client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)
            self.assertEqual(response.status, 200)
            summary = await response.json()
            self.assertEqual(summary['unread_count'], 0)
            self.assertEqual(summary['unread_state_rev'], mar['state_rev'])
            response = await client.get(
                f'/v1/datasets/{self.ds_proj}/documents/{self.k_docs[0]}/body', headers=self.manager_headers,
            )
            self.assertEqual(response.status, 200)
            self.assertIn('Knowledge body number 0', (await response.json())['body'])

    async def test_contracts_overview_kpi_anchor_prev_user_children_pending_docs_and_utf8(self):
        # Insert pipeline_run for unconfirmed dataset -> MUST NOT appear in /v1/overview/bootstrap!
        con = sqlite3.connect(self.cognee_db)
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.uuid4().hex, None, uuid.UUID(self.ds_unconf).hex,
                'cognify_pipeline', '', 'DATASET_PROCESSING_COMPLETED', 'success',
                1, '', 'auto', '2099-01-01 00:00:00', '2099-01-01 00:00:00', '2099-01-01 00:00:00',
            ),
        )
        con.commit()
        con.close()

        # Contract #2 & PY-P0-05: overview/bootstrap session_count + common.raw_doc_count + total_raw_count + no unconfirmed runs
        ov_resp = await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)
        self.assertEqual(ov_resp.status, 200)
        ov_raw = await ov_resp.read()
        self.assertNotIn(b'\\u9879\\u76ee', ov_raw)  # UTF-8 wire encoding, no ASCII escaping!
        ov = json.loads(ov_raw)
        self.assertGreaterEqual(ov['projects'][0]['session_count'], 10)
        self.assertEqual(ov['common']['raw_doc_count'], 0)
        self.assertEqual(
            ov['total_raw_count'],
            ov['projects'][0]['session_count'] + ov['projects'][0]['raw_doc_count'] + ov['common']['raw_doc_count'],
        )
        self.assertTrue(all(r['dataset_id'] != self.ds_unconf for r in ov['pipeline_runs']))

        # Contract #3: anchor_doc_id + direction=prev + 404 for unknown anchor
        # k_docs[10] is on day 2026-11-20 (strictly after all 8 docs on 2026-11-21), so has_prev is deterministically True.
        target_mid = self.k_docs[10]
        anc_resp = await self.client.get(
            f'/v1/projects/p1/knowledge?limit=5&anchor_doc_id={target_mid}',
            headers=self.manager_headers,
        )
        self.assertEqual(anc_resp.status, 200)
        anc = await anc_resp.json()
        self.assertIn(target_mid, [i['doc_id'] for i in anc['items']])
        self.assertTrue(anc['has_prev'])
        self.assertTrue(anc['prev_cursor'])
        prev_resp = await self.client.get(
            f"/v1/projects/p1/knowledge?limit=5&direction=prev&cursor={anc['prev_cursor']}",
            headers=self.manager_headers,
        )
        self.assertEqual(prev_resp.status, 200)
        prev_body = await prev_resp.json()
        self.assertGreater(len(prev_body['items']), 0)
        self.assertFalse({i['doc_id'] for i in prev_body['items']} & {i['doc_id'] for i in anc['items']})
        missing_anc = await self.client.get(
            f'/v1/projects/p1/knowledge?anchor_doc_id={uuid.uuid4()}',
            headers=self.manager_headers,
        )
        self.assertEqual(missing_anc.status, 404)

        # Common knowledge anchor_doc_id + 404
        c_anc = await self.client.get(
            f'/v1/common/knowledge?limit=2&anchor_doc_id={self.common_docs[1]}',
            headers=self.manager_headers,
        )
        self.assertEqual(c_anc.status, 200)
        self.assertIn(self.common_docs[1], [i['doc_id'] for i in (await c_anc.json())['items']])

        # Contract #4: ?user_id=<uuid> on /children validates UUID & scopes owner
        bad_uid = await self.client.get(
            '/v1/projects/p1/raw-records/sessions/sess-parent/children?user_id=not-a-uuid',
            headers=self.manager_headers,
        )
        self.assertEqual(bad_uid.status, 400)
        wrong_owner = await self.client.get(
            f'/v1/projects/p1/raw-records/sessions/sess-parent/children?user_id={self.user_b}',
            headers=self.manager_headers,
        )
        self.assertEqual(wrong_owner.status, 404)
        right_owner = await self.client.get(
            f'/v1/projects/p1/raw-records/sessions/sess-parent/children?user_id={self.user_a}&limit=5',
            headers=self.manager_headers,
        )
        self.assertEqual(right_owner.status, 200)
        self.assertEqual((await right_owner.json())['total_count'], 8)

        # Contract #5: message-summary pending_documents carries full minimal Document fields
        ms = await (await self.client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)).json()
        self.assertEqual(len(ms['pending_documents']), 1)
        pd = ms['pending_documents'][0]
        for k in ('id', 'dataset_id', 'name', 'label', 'title', 'date', 'kind', 'created_at'):
            self.assertIn(k, pd)
        self.assertEqual(pd['dataset_id'], self.ds_proj)

        # Contract #1: migrate-local with unknown reading_identity returns no_legacy_file without pre-creating scope
        other_id = 'http://127.0.0.1:18765/custom-dir-id/'
        m_resp = await self.client.post(
            '/v1/reading-state/migrate-local',
            headers=self.manager_headers,
            json={'reading_identity': other_id},
        )
        self.assertEqual(m_resp.status, 200)
        self.assertEqual((await m_resp.json())['migrated'][0]['status'], 'no_legacy_file')
        handles = self.app['v2_api_handles']
        from reading_state_store import knowledge_read_scope_hash
        self.assertIsNone(handles.reading_state.get_scope(knowledge_read_scope_hash(other_id, 'p1', self.ds_proj)))

        # Contract #6: backfill with unconfirmed project in registry MUST return has_unconfirmed_projects=True and NOT rename file!
        id_hash = hashlib.sha256(other_id.encode('utf-8')).hexdigest()
        dates_file = self.root / f'knowledge-source-dates-{id_hash}.json'
        dates_file.write_text(json.dumps({'schema_version': 1, 'days': {}}), encoding='utf-8')
        dates_file.chmod(0o600)
        bf = await self.client.post(
            '/v1/provenance/backfill-legacy-dates',
            headers=self.manager_headers,
            json={'reading_identity': other_id},
        )
        self.assertEqual(bf.status, 200)
        bf_body = await bf.json()
        self.assertTrue(bf_body['has_unconfirmed_projects'])
        self.assertEqual(bf_body['backup_path'], '')
        self.assertTrue(dates_file.exists())

    async def test_raw_records_v2_dual_branch_merge_tie_breaker_and_children_paging(self):
        # Traverse ALL top-level raw records with page size 2 -> crosses exact timestamp tie between
        # (sess-tie, user_a), (sess-tie, user_b), and raw_docs[0] with ZERO dropped or duplicated rows!
        collected = []
        cursor = ''
        while True:
            q = '/v1/projects/p1/raw-records?kind=all&limit=2' + (f'&cursor={cursor}' if cursor else '')
            resp = await self.client.get(q, headers=self.manager_headers)
            self.assertEqual(resp.status, 200)
            page = await resp.json()
            for entry in page['items']:
                if entry['item_kind'] == 'session':
                    s = entry['session']
                    collected.append(('session', s['user_id'], s['session_id'], s))
                else:
                    d = entry['document']
                    collected.append(('document', '', d['id'], d))
            if not page['has_more']:
                break
            cursor = page['next_cursor']

        # Expect exactly 3 top-level sessions (2 sess-tie users + 1 sess-parent) + 3 raw docs = 6 items
        identities = [(k, u, i) for k, u, i, _ in collected]
        self.assertEqual(len(identities), 6)
        self.assertEqual(len(set(identities)), 6)

        # Verify expired row excluded & empty question fallback worked on sess-tie/user_a
        by_key = {(k, u, i): obj for k, u, i, obj in collected}
        s_a = by_key[('session', self.user_a, 'sess-tie')]
        self.assertEqual(s_a['msg_count'], 2)
        self.assertEqual(s_a['label'], 'Tie session question A')
        s_b = by_key[('session', self.user_b, 'sess-tie')]
        self.assertEqual(s_b['msg_count'], 1)
        self.assertEqual(s_b['label'], 'Tie session question B')

        # Parent session shows child_count=8, 5 preview children, children_has_more=True
        s_parent = by_key[('session', self.user_a, 'sess-parent')]
        self.assertEqual(s_parent['child_count'], 8)
        self.assertEqual(len(s_parent['children_preview']), 5)
        self.assertTrue(s_parent['children_has_more'])
        self.assertEqual(s_parent['tool_calls'], 1)

        # Paginate /children with limit=3 -> reaches all 8 subagent sessions with 0 dupes!
        children_seen = []
        ccursor = ''
        while True:
            q = '/v1/projects/p1/raw-records/sessions/sess-parent/children?limit=3' + (f'&cursor={ccursor}' if ccursor else '')
            cpage = await (await self.client.get(q, headers=self.manager_headers)).json()
            self.assertEqual(cpage['total_count'], 8)
            children_seen.extend(row['session_id'] for row in cpage['items'])
            if not cpage['has_more']:
                break
            ccursor = cpage['next_cursor']
        self.assertEqual(len(children_seen), 8)
        self.assertEqual(len(set(children_seen)), 8)

    async def test_round2_contracts_live_auth_relation_sig_cache_update_and_summary_budget(self):
        """Regression for #55 Contracts #1/#2/#3, live auth, relation_sig>256, cache QA UPDATE, and 16KiB budget."""
        # 1. Contract #1: direction=prev&anchor_doc_id on BOTH /v1/projects/{id}/knowledge and /v1/common/knowledge
        all_pk = await (await self.client.get(
            '/v1/projects/p1/knowledge?limit=50', headers=self.manager_headers
        )).json()
        all_ids = [it['doc_id'] for it in all_pk['items']]
        self.assertGreaterEqual(len(all_ids), 10)
        anchor_id = all_ids[5]

        # direction=prev&anchor_doc_id returns items strictly BEFORE anchor_id (no cursor needed!)
        prev_resp = await self.client.get(
            f'/v1/projects/p1/knowledge?direction=prev&anchor_doc_id={anchor_id}&limit=3',
            headers=self.manager_headers,
        )
        self.assertEqual(prev_resp.status, 200)
        prev_body = await prev_resp.json()
        self.assertEqual([it['doc_id'] for it in prev_body['items']], all_ids[2:5])
        self.assertTrue(prev_body['has_prev'])
        self.assertTrue(prev_body['has_more'])
        # Mutual exclusion of cursor + anchor_doc_id still returns 400
        bad_both = await self.client.get(
            f"/v1/projects/p1/knowledge?direction=prev&anchor_doc_id={anchor_id}&cursor={prev_body['prev_cursor']}",
            headers=self.manager_headers,
        )
        self.assertEqual(bad_both.status, 400)

        # Common knowledge direction=prev&anchor_doc_id
        all_ck = await (await self.client.get('/v1/common/knowledge?limit=50', headers=self.manager_headers)).json()
        ck_ids = [it['doc_id'] for it in all_ck['items']]
        self.assertEqual(len(ck_ids), 3)
        ck_prev = await self.client.get(
            f'/v1/common/knowledge?direction=prev&anchor_doc_id={ck_ids[2]}&limit=2',
            headers=self.manager_headers,
        )
        self.assertEqual(ck_prev.status, 200)
        ck_prev_body = await ck_prev.json()
        self.assertEqual([it['doc_id'] for it in ck_prev_body['items']], ck_ids[0:2])
        self.assertFalse(ck_prev_body['has_prev'])
        self.assertTrue(ck_prev_body['has_more'])

        # 2. Contract #2: unread_state_rev returned & synced across bootstrap, knowledge, message-summary, and mutations
        ov = await (await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)).json()
        self.assertIn('unread_state_rev', ov['projects'][0])
        rev_ov = ov['projects'][0]['unread_state_rev']
        self.assertGreater(rev_ov, 0)
        pk = await (await self.client.get('/v1/projects/p1/knowledge?limit=5', headers=self.manager_headers)).json()
        self.assertEqual(pk['unread_state_rev'], rev_ov)
        ms = await (await self.client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)).json()
        self.assertEqual(ms['unread_state_rev'], rev_ov)

        mr = await (await self.client.post(
            '/v1/reading-state/mark-read',
            headers=self.manager_headers,
            json={
                'reading_identity': self.identity,
                'project_id': 'p1',
                'dataset_id': self.ds_proj,
                'doc_ids': [all_ids[0]],
            },
        )).json()
        self.assertGreater(mr['state_rev'], rev_ov)
        pk2 = await (await self.client.get('/v1/projects/p1/knowledge?limit=5', headers=self.manager_headers)).json()
        self.assertEqual(pk2['unread_state_rev'], mr['state_rev'])
        self.assertEqual(pk2['unread_count'], mr['unread_count'])

        # import/commit explicitly accepts expected_items: 0 and returns state_rev
        b_zero = await (await self.client.post(
            '/v1/reading-state/import/begin',
            headers=self.manager_headers,
            json={
                'reading_identity': 'http://127.0.0.1:61255/zero-items/',
                'project_id': 'p1',
                'dataset_id': self.ds_proj,
                'initialized': True,
                'enabled_at': '2026-11-21T09:59:00Z',
            },
        )).json()
        c_zero = await self.client.post(
            '/v1/reading-state/import/commit',
            headers=self.manager_headers,
            json={'batch_id': b_zero['batch_id'], 'expected_items': 0},
        )
        self.assertEqual(c_zero.status, 200)
        c_zero_body = await c_zero.json()
        self.assertEqual(c_zero_body['committed'], 0)
        self.assertGreater(c_zero_body['state_rev'], 0)

        # 3. Contract #3: ?dataset_id=<uuid> on /v1/search/recent and /v1/search/metadata filters in SQL before LIMIT
        sr_common = await self.client.get(
            f'/v1/search/recent?dataset_id={self.ds_common}&limit=10',
            headers=self.manager_headers,
        )
        self.assertEqual(sr_common.status, 200)
        sr_body = await sr_common.json()
        self.assertEqual(len(sr_body['knowledge_items']), 3)
        self.assertTrue(all(it['dataset_id'] == self.ds_common for it in sr_body['knowledge_items']))

        sm_common = await self.client.get(
            f'/v1/search/metadata?q=Common&dataset_id={self.ds_common}&limit=10',
            headers=self.manager_headers,
        )
        self.assertEqual(sm_common.status, 200)
        sm_body = await sm_common.json()
        self.assertEqual(len(sm_body['items']), 3)
        self.assertTrue(all(it['dataset_id'] == self.ds_common for it in sm_body['items']))

        # Reject project_id + dataset_id together (400) and unconfirmed dataset_id (403)
        self.assertEqual(
            (await self.client.get(
                f'/v1/search/recent?project_id=p1&dataset_id={self.ds_common}', headers=self.manager_headers
            )).status,
            400,
        )
        self.assertEqual(
            (await self.client.get(
                f'/v1/search/metadata?q=Secret&dataset_id={self.ds_unconf}', headers=self.manager_headers
            )).status,
            403,
        )

        # 4. Live official auth intersection & Fail-Closed 503 when official=None (R2-PY-P0-01)
        self.dummy_official.authorized.discard(self.ds_common)
        ov_revoked = await (await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)).json()
        self.assertIsNone(ov_revoked['common'])
        self.assertEqual(
            (await self.client.get(
                f'/v1/search/recent?dataset_id={self.ds_common}', headers=self.manager_headers
            )).status,
            403,
        )
        self.dummy_official.authorized.add(self.ds_common)

        saved_official = self.registry.official
        self.registry.official = None
        try:
            self.assertEqual(
                (await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)).status,
                503,
            )
        finally:
            self.registry.official = saved_official

        # 5. CacheSessionHydrator detects in-place QA payload UPDATE immediately (R2-PY-P1-05 / CC2-P1-02)
        rr1 = await (await self.client.get('/v1/projects/p1/raw-records?kind=session&limit=10', headers=self.manager_headers)).json()
        parent_before = next(e['session'] for e in rr1['items'] if e['session']['session_id'] == 'sess-parent')
        self.assertEqual(parent_before['label'], 'Parent task question')

        c2 = sqlite3.connect(self.cache_db)
        c2.execute(
            "UPDATE cache_qa_entries SET payload = ? WHERE session_id = 'sess-parent'",
            (json.dumps({'question': 'Updated QA title in-place!'}),),
        )
        c2.commit()
        c2.close()
        rr2 = await (await self.client.get('/v1/projects/p1/raw-records?kind=session&limit=10', headers=self.manager_headers)).json()
        parent_after = next(e['session'] for e in rr2['items'] if e['session']['session_id'] == 'sess-parent')
        self.assertEqual(parent_after['label'], 'Updated QA title in-place!')

        # 6. _SessionRelationsResolver.relation_sig detects middle-session mutation even when >256 entries (R2-PY-P1-04)
        #    BOTH on raw plain dict AND on TrackedRelationsDict (outer replace + nested in-place mutation)!
        from session_relations import TrackedRelationsDict
        handles = self.app['v2_api_handles']
        big_rels = {
            f's-{i:04d}': {'parent_session_id': 'root-a', 'subagent_role': 'worker', 'nesting_depth': 1}
            for i in range(300)
        }
        self.registry.state['session_relations'] = big_rels
        sig1 = handles.relations_resolver.relation_sig(self.ds_proj)
        big_rels['s-0150'] = {'parent_session_id': 'root-b', 'subagent_role': 'worker', 'nesting_depth': 1}
        sig2 = handles.relations_resolver.relation_sig(self.ds_proj)
        self.assertNotEqual(sig1, sig2)

        tracked_rels = TrackedRelationsDict(big_rels)
        self.registry.state['session_relations'] = tracked_rels
        sig_t1 = handles.relations_resolver.relation_sig(self.ds_proj)
        tracked_rels['s-0150']['parent_session_id'] = 'root-c'
        sig_t2 = handles.relations_resolver.relation_sig(self.ds_proj)
        self.assertNotEqual(sig_t1, sig_t2)

        # 7. message-summary enforces hard <= 16 KiB UTF-8 byte cap even with massive activity/notices
        c1 = sqlite3.connect(self.cognee_db)
        for i in range(30):
            c1.execute(
                'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.uuid4().hex, uuid.uuid4().hex, None, uuid.UUID(self.ds_proj).hex,
                    'cognify_pipeline', 'op_' + ('x' * 600), 'COMPLETED', 'ok', 0, 'err_' + ('y' * 400),
                    'manual', f'2026-11-21 12:00:{i:02d}', f'2026-11-21 12:00:{i:02d}', f'2026-11-21 12:00:{i:02d}',
                ),
            )
        c1.commit()
        c1.close()
        ms_resp = await self.client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)
        self.assertEqual(ms_resp.status, 200)
        ms_raw = await ms_resp.read()
        self.assertLessEqual(len(ms_raw), 16 * 1024)

    async def test_r3_backfill_stat_guard_and_snapshot_consistency(self):
        """R3 P1-5 & P1-6: atomic unread snapshot (no second page_unread_flags read) + legacy dates stat guard before archive."""
        import hashlib
        import os
        handles = self.app['v2_api_handles']
        orig_page_flags = handles.reading_state.page_unread_flags

        def _forbid_second_read(*_a, **_kw):
            raise AssertionError('page_unread_flags must NOT be called separately on bootstrap/knowledge!')

        handles.reading_state.page_unread_flags = _forbid_second_read
        try:
            bs = await (await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)).json()
            self.assertEqual(bs['projects'][0]['unread_count'], len([it for it in bs['recent_items'] if it['project_id'] == 'p1' and it['is_unread']]))
            kn = await (await self.client.get('/v1/projects/p1/knowledge?limit=10', headers=self.manager_headers)).json()
            self.assertIn('unread_state_rev', kn)
            self.assertGreaterEqual(kn['unread_state_rev'], 0)
        finally:
            handles.reading_state.page_unread_flags = orig_page_flags

        # P1-6: backfill-legacy-dates must NOT rename .migrated.bak if the default file changed mid-request!
        identity = self.manager_headers['X-Memlane-Reading-Identity']
        digest = hashlib.sha256(identity.encode('utf-8')).hexdigest()
        legacy_file = self.registry.path.parent / f'knowledge-source-dates-{digest}.json'
        key_d1 = hashlib.sha256(f'{self.ds_proj}/state/sk:0'.encode('utf-8')).hexdigest()
        legacy_file.write_text(json.dumps({'schema_version': 1, 'days': {key_d1: '2025-02-01'}}), encoding='utf-8')
        os.chmod(legacy_file, 0o600)

        mutated_once = {'done': False}
        had_req = hasattr(self.dummy_official, 'request')
        orig_req = getattr(self.dummy_official, 'request', None)

        async def _mutating_request(method, path, payload=None, adapter_token=None):
            if not mutated_once['done']:
                mutated_once['done'] = True
                # Concurrent modification while backfill request is in flight!
                legacy_file.write_text(
                    json.dumps({'schema_version': 1, 'days': {key_d1: '2025-02-01', 'a' * 64: '2025-01-01'}}),
                    encoding='utf-8',
                )
            return {'imported': len((payload or {}).get('entries') or ()), 'skipped': 0}

        saved_unconf = self.registry.state['projects'].pop('p-unconf', None)
        self.dummy_official.request = _mutating_request
        try:
            r_changed = await self.client.post(
                '/v1/provenance/backfill-legacy-dates',
                headers=self.manager_headers,
                json={'reading_identity': identity},
            )
            self.assertEqual(r_changed.status, 200)
            body_changed = await r_changed.json()
            self.assertFalse(body_changed['has_unconfirmed_projects'])
            self.assertEqual(body_changed['status'], 'partial')
            self.assertEqual(body_changed['backup_path'], '')
            self.assertTrue(legacy_file.exists())
            self.assertFalse(legacy_file.with_name(legacy_file.name + '.migrated.bak').exists())

            # Now write only the matched key and run unchanged -> archives cleanly to .migrated.bak
            legacy_file.write_text(json.dumps({'schema_version': 1, 'days': {key_d1: '2025-02-01'}}), encoding='utf-8')
            r_ok = await self.client.post(
                '/v1/provenance/backfill-legacy-dates',
                headers=self.manager_headers,
                json={'reading_identity': identity},
            )
            self.assertEqual(r_ok.status, 200)
            body_ok = await r_ok.json()
            self.assertEqual(body_ok['status'], 'migrated')
            self.assertTrue(legacy_file.with_name(legacy_file.name + '.migrated.bak').exists())
        finally:
            if saved_unconf is not None:
                self.registry.state['projects']['p-unconf'] = saved_unconf
            if had_req:
                self.dummy_official.request = orig_req
            else:
                delattr(self.dummy_official, 'request')

    async def test_message_summary_preview_error_isolates_reading_state_and_404_retry_checks_acl_first(self):
        # 1. Malformed raw_data_location on an unread doc without readable label/name must only set
        #    errors['reading_state'] without wiping activity or pending_documents!
        bad_doc = str(uuid.uuid4())
        with sqlite3.connect(self.cognee_db) as con:
            con.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(bad_doc).hex, uuid.UUID(self.ds_proj).hex, None,
                    f'text_{"f" * 32}.md', None, 'text/markdown', 'file://[broken',
                    '{}', json.dumps({'lesson_type': 'state', 'state_key': 'sk:bad-uri', 'session_date': '2026-11-22'}),
                    json.dumps(['session_learnings']), '{}',
                    '2026-11-22 12:00:00', '2026-11-22 12:00:00', 'h-bad-uri',
                ),
            )
        ms = await self.client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)
        self.assertEqual(ms.status, 200)
        ms_body = await ms.json()
        self.assertEqual(ms_body['pending_documents_count'], 1)
        self.assertEqual(ms_body['errors'].get('reading_state'), 'reading_state_unavailable')
        self.assertNotIn('documents', ms_body['errors'])

        # 2. On 404 anchor retry, if permission is revoked before retry, 403 must win even if sync would fail!
        handles = self.app['v2_api_handles']
        cat = handles.catalog_index
        missing_anchor = str(uuid.uuid4())
        orig_query = cat.query_project_knowledge

        def _revoke_during_first_query(*args, **kwargs):
            self.dummy_official.authorized.discard(self.ds_proj)
            return orig_query(*args, **kwargs)

        orig_sync_async = cat.sync_async
        async def _broken_sync(*args, **kwargs):
            raise PolicyError('official_store_unavailable', 503)

        cat.query_project_knowledge = _revoke_during_first_query
        cat.sync_async = _broken_sync
        try:
            resp = await self.client.get(
                f'/v1/projects/p1/knowledge?anchor_doc_id={missing_anchor}',
                headers=self.manager_headers,
            )
            self.assertEqual(resp.status, 403)
        finally:
            cat.query_project_knowledge = orig_query
            cat.sync_async = orig_sync_async
            self.dummy_official.authorized.add(self.ds_proj)

    async def test_rebuild_retry_revalidates_acl_on_overview_search_and_message_summary(self):
        from project_policy import PolicyError

        handles = self.app['v2_api_handles']
        cat = handles.catalog_index

        # 1. /v1/overview/bootstrap: mid-rebuild revocation of ds_common must exclude ds_common on retry
        orig_meta = cat.meta
        tripped_ov = [False]

        def _rebuild_then_revoke_common():
            if not tripped_ov[0]:
                tripped_ov[0] = True
                self.dummy_official.authorized.discard(self.ds_common)
                raise PolicyError('catalog_index_rebuilding', 503)
            return orig_meta()

        cat.meta = _rebuild_then_revoke_common
        try:
            resp = await self.client.get('/v1/overview/bootstrap', headers=self.manager_headers)
            self.assertEqual(resp.status, 200)
            body = await resp.json()
            self.assertIsNone(body['common'])
            self.assertTrue(all(it['dataset_id'] != self.ds_common for it in body['recent_items']))
        finally:
            cat.meta = orig_meta
            self.dummy_official.authorized.add(self.ds_common)

        # 2. /v1/search/metadata?dataset_id=<ds_common>: mid-rebuild revocation must fail closed with 403
        orig_search = cat.query_search_metadata
        tripped_sm = [False]

        def _rebuild_then_revoke_search(*args, **kwargs):
            if not tripped_sm[0]:
                tripped_sm[0] = True
                self.dummy_official.authorized.discard(self.ds_common)
                raise PolicyError('catalog_index_rebuilding', 503)
            return orig_search(*args, **kwargs)

        cat.query_search_metadata = _rebuild_then_revoke_search
        try:
            resp = await self.client.get(
                f'/v1/search/metadata?q=Common&dataset_id={self.ds_common}',
                headers=self.manager_headers,
            )
            self.assertEqual(resp.status, 403)
        finally:
            cat.query_search_metadata = orig_search
            self.dummy_official.authorized.add(self.ds_common)

        # 3. /v1/projects/p1/message-summary: mid-rebuild revocation must fail closed with 403 (NOT 200!)
        orig_ds_info = cat.dataset_sync_info
        tripped_ms = [False]

        def _rebuild_then_revoke_ms(*args, **kwargs):
            if not tripped_ms[0]:
                tripped_ms[0] = True
                self.dummy_official.authorized.discard(self.ds_proj)
                raise PolicyError('catalog_index_rebuilding', 503)
            return orig_ds_info(*args, **kwargs)

        cat.dataset_sync_info = _rebuild_then_revoke_ms
        try:
            resp = await self.client.get('/v1/projects/p1/message-summary', headers=self.manager_headers)
            self.assertEqual(resp.status, 403)
        finally:
            cat.dataset_sync_info = orig_ds_info
            self.dummy_official.authorized.add(self.ds_proj)

        # 4. Generation-conflict retry must revalidate ACL before reading the newer generation!
        orig_gen = cat.generation
        gen_calls = [0]

        def _conflict_then_revoke():
            gen_calls[0] += 1
            if gen_calls[0] == 1:
                return orig_gen()
            if gen_calls[0] == 2:
                self.dummy_official.authorized.discard(self.ds_proj)
                return orig_gen() + 1
            return orig_gen() + 1

        cat.generation = _conflict_then_revoke
        try:
            resp = await self.client.get('/v1/projects/p1/knowledge?limit=10', headers=self.manager_headers)
            self.assertEqual(resp.status, 403)
        finally:
            cat.generation = orig_gen
            self.dummy_official.authorized.add(self.ds_proj)

        # 5. Revalidator must re-check local registry binding AFTER await verify_official_read_access!
        orig_auth_ds = self.dummy_official.authorized_datasets
        orig_query = cat.query_project_knowledge
        tripped_rebind = [False]
        rebound_once = [False]

        def _rebuild_once_for_rebind(*args, **kwargs):
            if not tripped_rebind[0]:
                tripped_rebind[0] = True
                raise PolicyError('catalog_index_rebuilding', 503)
            return orig_query(*args, **kwargs)

        def _rebind_during_official_check():
            res = orig_auth_ds()
            if tripped_rebind[0] and not rebound_once[0]:
                rebound_once[0] = True
                # Local project binding is unconfirmed/removed during the official ACL check!
                self.registry.state['projects']['p1']['status'] = 'pending_confirmation'
            return res

        cat.query_project_knowledge = _rebuild_once_for_rebind
        self.dummy_official.authorized_datasets = _rebind_during_official_check
        try:
            resp = await self.client.get('/v1/projects/p1/knowledge?limit=10', headers=self.manager_headers)
            self.assertEqual(resp.status, 403)
        finally:
            cat.query_project_knowledge = orig_query
            self.dummy_official.authorized_datasets = orig_auth_ds
            self.registry.state['projects']['p1']['status'] = 'verified'

        # 6. Initial read must re-check local binding AFTER _ensure_synced() before querying catalog!
        orig_sync_read = cat.sync_for_read_async

        async def _unconfirm_during_initial_sync(*args, **kwargs):
            res = await orig_sync_read(*args, **kwargs)
            self.registry.state['projects']['p1']['status'] = 'pending_confirmation'
            return res

        cat.sync_for_read_async = _unconfirm_during_initial_sync
        try:
            resp = await self.client.get('/v1/projects/p1/knowledge?limit=10', headers=self.manager_headers)
            self.assertEqual(resp.status, 403)
        finally:
            cat.sync_for_read_async = orig_sync_read
            self.registry.state['projects']['p1']['status'] = 'verified'


class V2ApiExtractionTests(unittest.TestCase):
    def test_compatibility_exports_resolve_to_extracted_symbols(self):
        import catalog_index
        import v2_api
        import v2_api_context
        import v2_api_mutations
        import v2_api_raw
        import v2_lookups

        for module, names in (
            (catalog_index, ('_file_stat_sig',)),
            (v2_lookups, ('READING_CONTRACT', 'READING_CONTRACT_HEADER')),
            (v2_api_context, ('V2APIHandles', 'CursorCodec', '_filter_digest', '_SessionRelationsResolver')),
            (v2_api_mutations, ('_archive_legacy_dates_if_unchanged',)),
            (v2_api_raw, ('_query_raw_records_v2', '_query_session_children_v2')),
        ):
            for name in names:
                with self.subTest(symbol=name):
                    self.assertIs(getattr(v2_api, name), getattr(module, name))

    def test_ml020_discovers_all_list_handlers_and_their_direct_budget_calls(self):
        import ast
        import importlib.util

        engine_dir = Path(__file__).resolve().parent
        lint_path = engine_dir.parents[1] / 'scripts' / 'lint.py'
        spec = importlib.util.spec_from_file_location('memlane_lint', lint_path)
        lint = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(lint)

        tree = ast.parse((engine_dir / 'v2_api.py').read_text(encoding='utf-8'))
        index = lint._build_ast_index(tree)
        discovered = lint._collect_v2_list_handlers(tree, index['all_nodes'], index['functions'])
        self.assertEqual(
            {route: fn.name for route, fn in discovered},
            {
                '/v1/overview/bootstrap': 'overview_bootstrap_v2',
                '/v1/overview/day': 'overview_day_v2',
                '/v1/projects/{id}/knowledge': 'project_knowledge_v2',
                '/v1/common/knowledge': 'common_knowledge_v2',
                '/v1/search/recent': 'search_recent_v2',
                '/v1/search/metadata': 'search_metadata_v2',
                '/v1/projects/{id}/raw-records': 'raw_records_v2',
                '/v1/projects/{id}/raw-records/sessions/{sid}/children': 'raw_records_session_children_v2',
            },
        )
        self.assertEqual(len(discovered), 8)
        for route, handler in discovered:
            with self.subTest(route=route):
                calls = {
                    node.func.id for node in ast.walk(handler)
                    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                }
                self.assertTrue({'_parse_limit', 'finalize_budgeted_payload'} <= calls)
                self.assertEqual(ast.unparse(handler.body[-1]), 'return json_utf8_response(payload, raw_bytes=raw_bytes)')

    def test_extracted_v2_domain_modules_obey_ml020_utf8_and_no_http_response_bypass(self):
        import ast
        import importlib.util

        engine_dir = Path(__file__).resolve().parent
        lint_path = engine_dir.parents[1] / 'scripts' / 'lint.py'
        spec = importlib.util.spec_from_file_location('memlane_lint_v2_domains', lint_path)
        lint = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(lint)

        domain_modules = (
            'v2_api_context.py',
            'v2_api_overview.py',
            'v2_api_knowledge.py',
            'v2_api_raw.py',
            'v2_api_mutations.py',
        )
        for mod_name in domain_modules:
            with self.subTest(module=mod_name):
                mod_path = engine_dir / mod_name
                src = mod_path.read_text(encoding='utf-8')
                tree = ast.parse(src, filename=str(mod_path))
                idx = lint._build_ast_index(tree)
                # All extracted domain modules must pass general lint and unconditional ML020 UTF-8 check
                self.assertEqual(lint.check_source(src, str(mod_path)), [])
                self.assertEqual(
                    lint.check_v2_utf8_response(tree, idx['functions'], require_unconditional=True),
                    [],
                )
                # Domain modules must not construct HTTP responses directly or hardcode limit > 50
                for node in idx['all_nodes']:
                    if isinstance(node, ast.ImportFrom) and (node.module or '').startswith('aiohttp'):
                        self.fail(f'{mod_name} must not import from aiohttp directly')
                    if isinstance(node, ast.Call):
                        attr = node.func.attr if isinstance(node.func, ast.Attribute) else (
                            node.func.id if isinstance(node.func, ast.Name) else ''
                        )
                        self.assertNotEqual(attr, 'json_response')
                        for kw in node.keywords:
                            if kw.arg == 'limit' and isinstance(kw.value, ast.Constant) and isinstance(kw.value.value, int):
                                self.assertLessEqual(kw.value.value, 50)
        # Negative control: bare web.json_response in a domain module must be caught by ML020
        bad_src = (engine_dir / 'v2_api_knowledge.py').read_text(encoding='utf-8') + (
            "\nfrom aiohttp import web\ndef _bad_resp(p):\n    return web.json_response(p)\n"
        )
        bad_tree = ast.parse(bad_src)
        bad_idx = lint._build_ast_index(bad_tree)
        bad_codes = {
            code for _, code, _ in lint.check_v2_utf8_response(
                bad_tree, bad_idx['functions'], require_unconditional=True,
            )
        }
        self.assertIn('ML020', bad_codes)


class V2ApiContextTests(unittest.IsolatedAsyncioTestCase):
    async def test_falsy_injected_dependencies_are_preserved_and_not_closed_on_cleanup(self):
        from types import SimpleNamespace
        from v2_api_context import V2ApiContext

        class _FalsyStore:
            def __init__(self):
                self.closed = False

            def __bool__(self):
                return False

            def close(self):
                self.closed = True

        registry = SimpleNamespace(
            path=Path('/unused/project-registry.json'),
            state={'projects': {}, 'common': None},
            official=None,
        )
        fake_cat = _FalsyStore()
        fake_read = _FalsyStore()
        ctx = V2ApiContext(registry, catalog_index=fake_cat, reading_state=fake_read)
        self.assertIs(ctx.cat, fake_cat)
        self.assertIs(ctx.read_store, fake_read)
        self.assertFalse(ctx.owns_catalog)
        self.assertFalse(ctx.owns_reading_state)
        await ctx.cleanup(None)
        self.assertFalse(fake_cat.closed)
        self.assertFalse(fake_read.closed)

    async def test_search_scope_revalidates_live_access_and_refreshes_unfiltered_scope(self):
        from types import SimpleNamespace
        from unittest.mock import AsyncMock
        from project_policy import PolicyError
        from v2_api_context import V2ApiContext

        project_ds, common_ds = str(uuid.uuid4()), str(uuid.uuid4())
        project = {'id': 'example', 'dataset_id': project_ds, 'status': 'verified'}
        common = {'dataset_id': common_ds, 'status': 'verified'}
        official = _DummyOfficial({project_ds, common_ds})
        registry = SimpleNamespace(
            path=Path('/unused/project-registry.json'),
            state={'projects': {'example': project}, 'common': common},
            managed_groups=lambda: [project, common],
            official=official,
        )
        cat = SimpleNamespace(sync_for_read_async=AsyncMock())
        ctx = V2ApiContext(registry, codex_config={'official_data': '/unused'}, catalog_index=cat, reading_state=object())
        self.addAsyncCleanup(ctx.cleanup, None)

        scope, revalidate = await ctx.resolve_search_scope('', project_ds.replace('-', ''))
        self.assertEqual(scope['allowed_ds'], [project_ds])
        self.assertEqual(scope['ds_to_proj'], {project_ds: 'example', common_ds: ''})
        cat.sync_for_read_async.assert_awaited_once_with([project_ds, common_ds], relations_resolver=ctx.rel_resolver)

        unfiltered, refresh = await ctx.resolve_search_scope('', '')
        official.authorized = {common_ds}
        await refresh()
        self.assertEqual(unfiltered['allowed_ds'], [common_ds])
        with self.assertRaises(PolicyError) as exc:
            await revalidate()
        self.assertEqual((exc.exception.code, exc.exception.status), ('dataset_access_denied', 403))


if __name__ == '__main__':
    unittest.main()
