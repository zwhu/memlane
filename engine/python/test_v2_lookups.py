"""Unit tests for /v1/capabilities and P0 O(1) /v2 lookup endpoints."""
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import uuid

from aiohttp.test_utils import AioHTTPTestCase

from knowledge_recall import RecallStore
from project_control import Registry
from project_control_server import create_app
from provenance_store import ProvenanceStore
from sqlite_ro import official_sqlite_ro


class _DummyOfficial:
    def __init__(self, authorized=()):
        self._authorized = set(authorized)

    def authorized_datasets(self):
        return frozenset(self._authorized)


class V2LookupsTests(AioHTTPTestCase):
    async def get_application(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.official_data = root / 'official-store'
        db_dir = self.official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)
        self.cognee_db = db_dir / 'cognee_db'

        self.ds_verified = str(uuid.uuid4())
        self.ds_code = str(uuid.uuid4())
        self.ds_unconfirmed = str(uuid.uuid4())
        self.ds_common = str(uuid.uuid4())
        self.ds_unmanaged = str(uuid.uuid4())

        self.doc_lesson = str(uuid.uuid4())
        self.doc_legacy = str(uuid.uuid4())
        self.doc_common = str(uuid.uuid4())
        self.doc_code = str(uuid.uuid4())
        self.doc_unconfirmed = str(uuid.uuid4())
        self.doc_unmanaged = str(uuid.uuid4())

        self.user_a = str(uuid.uuid4())
        self.user_b = str(uuid.uuid4())
        self.run_completed = str(uuid.uuid4())
        self.run_active = str(uuid.uuid4())
        self.run_old_gen = str(uuid.uuid4())

        con = sqlite3.connect(self.cognee_db)
        con.executescript('''
            CREATE TABLE datasets (
                id CHAR(32) PRIMARY KEY,
                name TEXT,
                created_at DATETIME,
                updated_at DATETIME
            );
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY,
                dataset_id CHAR(32),
                legacy_id CHAR(32),
                name VARCHAR,
                label VARCHAR,
                mime_type VARCHAR,
                raw_data_location VARCHAR,
                external_metadata JSON,
                system_metadata JSON,
                node_set JSON,
                updated_at DATETIME,
                created_at DATETIME,
                content_hash VARCHAR
            );
            CREATE INDEX ix_data_legacy_id ON data (legacy_id);
            CREATE TABLE session_records (
                session_id VARCHAR NOT NULL,
                user_id CHAR(32) NOT NULL,
                dataset_id CHAR(32),
                status VARCHAR NOT NULL,
                started_at DATETIME NOT NULL,
                last_activity_at DATETIME NOT NULL,
                agent_id VARCHAR,
                PRIMARY KEY (session_id, user_id)
            );
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY,
                pipeline_run_id CHAR(32),
                dataset_id CHAR(32),
                pipeline_name VARCHAR,
                operation_name VARCHAR,
                status VARCHAR,
                outcome VARCHAR,
                created_at DATETIME,
                started_at DATETIME,
                ended_at DATETIME
            );
            CREATE INDEX ix_pipeline_runs_dataset_pipeline_created_at
                ON pipeline_runs (dataset_id, pipeline_name, created_at);
        ''')

        con.execute(
            'INSERT INTO datasets VALUES (?,?,?,?)',
            (uuid.UUID(self.ds_code).hex, 'code-ds', '2026-11-20 10:00:00', '2026-11-20 10:00:00'),
        )

        # Lesson doc: system_metadata overrides planted external_metadata, double-encoded node_set
        con.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.doc_lesson).hex,
                uuid.UUID(self.ds_verified).hex,
                uuid.UUID(self.doc_legacy).hex,
                'lesson.md',
                'Auth State',
                'text/markdown',
                '',
                json.dumps({
                    'lesson_type': 'pitfall',
                    'first_source_date': '1999-01-01',
                    'why_learned': 'real reason',
                }),
                json.dumps({
                    'lesson_type': 'state',
                    'state_key': 'footprint:auth',
                    'session_date': '2026-11-18',
                    'session_id': 'sess-auth',
                }),
                json.dumps(json.dumps(['session_learnings', 'session_learnings:sess-auth'])),
                '2026-11-21 09:00:00',
                '2026-11-21 08:00:00',
                'hash-lesson',
            ),
        )
        # Common doc
        con.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.doc_common).hex,
                uuid.UUID(self.ds_common).hex,
                None,
                'pref.md',
                'Go Style',
                'text/markdown',
                '',
                json.dumps({'memlane_kind': 'common_knowledge', 'category': 'preference'}),
                None,
                None,
                '2026-11-21 07:00:00',
                '2026-11-21 07:00:00',
                'hash-common',
            ),
        )
        # Code doc
        con.execute(
            'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(self.doc_code).hex,
                uuid.UUID(self.ds_code).hex,
                None,
                'main.go',
                'main.go',
                'text/plain',
                '',
                json.dumps({}),
                None,
                None,
                '2026-11-21 06:00:00',
                '2026-11-21 06:00:00',
                'hash-code',
            ),
        )
        # Unconfirmed project doc & unmanaged doc
        for doc_id, ds_id in ((self.doc_unconfirmed, self.ds_unconfirmed), (self.doc_unmanaged, self.ds_unmanaged)):
            con.execute(
                'INSERT INTO data VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)',
                (
                    uuid.UUID(doc_id).hex,
                    uuid.UUID(ds_id).hex,
                    None,
                    'hidden.md',
                    None,
                    'text/markdown',
                    '',
                    json.dumps({'node_set': ['session_learnings']}),
                    None,
                    None,
                    '2026-11-21 05:00:00',
                    '2026-11-21 05:00:00',
                    'hash-hidden',
                ),
            )

        # Sessions (including two users sharing the same session_id)
        con.execute(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            ('sess-shared', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_verified).hex, 'active', '2026-11-21 08:00:00', '2026-11-21 09:00:00', 'codex'),
        )
        con.execute(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            ('sess-shared', uuid.UUID(self.user_b).hex, uuid.UUID(self.ds_verified).hex, 'active', '2026-11-21 08:30:00', '2026-11-21 10:00:00', 'claude'),
        )
        con.execute(
            'INSERT INTO session_records VALUES (?,?,?,?,?,?,?)',
            ('sess-unconf', uuid.UUID(self.user_a).hex, uuid.UUID(self.ds_unconfirmed).hex, 'active', '2026-11-21 08:00:00', '2026-11-21 09:00:00', 'codex'),
        )

        # Pipeline runs: old generation active run (must be ignored!) + completed run + unrelated noise
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.UUID(self.run_old_gen).hex, uuid.UUID(self.ds_code).hex,
                'code_graph_pipeline', None, 'DATASET_PROCESSING_STARTED', None,
                '2026-11-19 09:00:00', '2026-11-19 09:00:00', None,
            ),
        )
        # Same run_id has STARTED earlier and COMPLETED later -> dedup sees COMPLETED first -> NOT active!
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.UUID(self.run_completed).hex, uuid.UUID(self.ds_code).hex,
                'code_graph_pipeline', None, 'DATASET_PROCESSING_STARTED', None,
                '2026-11-21 08:00:00', '2026-11-21 08:00:00', None,
            ),
        )
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.UUID(self.run_completed).hex, uuid.UUID(self.ds_code).hex,
                'code_graph_pipeline', None, 'DATASET_PROCESSING_COMPLETED', 'succeeded',
                '2026-11-21 08:05:00', '2026-11-21 08:00:00', '2026-11-21 08:05:00',
            ),
        )
        # 500 unrelated runs on another dataset to test indexed seek
        other_hex = uuid.uuid4().hex
        con.executemany(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?)',
            [
                (
                    uuid.uuid4().hex, uuid.uuid4().hex, other_hex,
                    'cognify_pipeline', None, 'DATASET_PROCESSING_COMPLETED', 'succeeded',
                    f'2026-11-21 01:{i // 60:02d}:{i % 60:02d}', None, None,
                )
                for i in range(500)
            ],
        )
        con.commit()
        con.close()

        # Provenance store locks an earlier date (2026-11-15) for doc_lesson
        prov = ProvenanceStore(root / 'lesson-provenance.sqlite3')
        prov.record_document(
            self.ds_verified,
            self.doc_lesson,
            lesson_type='state',
            state_key='footprint:auth',
            candidate_dates=('2026-11-15',),
        )
        prov.close()

        # Recall store records 2 hits for doc_lesson
        recall = RecallStore(root / 'knowledge-recall.sqlite3')
        for idx in range(2):
            recall.record('agent-1', {
                'event_id': str(uuid.uuid4()),
                'channel': 'automatic',
                'hits': [{'dataset_id': self.ds_verified, 'document_id': self.doc_lesson}],
            })
        recall.close()

        self.registry = Registry(
            root / 'registry.json',
            _DummyOfficial({self.ds_verified, self.ds_code, self.ds_common}),
        )
        self.registry.state['projects'] = {
            'p-verified': {
                'id': 'p-verified',
                'name': 'Verified',
                'dataset_id': self.ds_verified,
                'code_dataset_id': self.ds_code,
                'code_enabled': True,
                'status': 'verified',
            },
            'p-unconfirmed': {
                'id': 'p-unconfirmed',
                'name': 'Unconfirmed',
                'dataset_id': self.ds_unconfirmed,
                'code_dataset_id': self.ds_code,
                'code_enabled': True,
                'status': 'unconfirmed',
            },
            'p-nocode': {
                'id': 'p-nocode',
                'name': 'NoCode',
                'dataset_id': self.ds_verified,
                'code_enabled': False,
                'status': 'verified',
            },
        }
        self.registry.state['common'] = {
            'id': 'common',
            'kind': 'common',
            'dataset_id': self.ds_common,
            'status': 'verified',
        }
        self.manager_headers = {'Authorization': 'Bearer ' + 'm' * 40}
        self.agent_headers = {'Authorization': 'Bearer ' + 'a' * 40}
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

    async def test_capabilities_auth_and_contract(self):
        self.assertEqual((await self.client.get('/v1/capabilities')).status, 401)
        self.assertEqual((await self.client.get('/v1/capabilities', headers=self.agent_headers)).status, 403)
        self.assertEqual((await self.client.get('/v1/capabilities?foo=1', headers=self.manager_headers)).status, 400)

        resp = await self.client.get('/v1/capabilities', headers=self.manager_headers)
        self.assertEqual(resp.status, 200)
        body = await resp.json()
        self.assertEqual(body['api_versions'], ['v1', 'v2'])
        for feat in (
            'document_lookup_v2',
            'session_lookup_v2',
            'code_activity_v2',
            'tool_graph_batched_v2',
            'catalog_index_v2',
            'reading_state_v2',
            'local_body_fastpath_v2',
            'overview_v2',
            'knowledge_cursor_v2',
            'common_knowledge_cursor_v2',
            'raw_records_v2',
            'search_v2',
            'message_summary_v2',
        ):
            self.assertIn(feat, body['features'])

    async def test_v2_document_lookup_acl_unconfirmed_and_trusted_enrichment(self):
        # Unconfirmed group -> Fail-Closed 403 project_unconfirmed
        unconf = await self.client.get(
            f'/v2/datasets/{self.ds_unconfirmed}/documents/{self.doc_unconfirmed}',
            headers=self.manager_headers,
        )
        self.assertEqual(unconf.status, 403)
        self.assertEqual((await unconf.json())['error'], 'project_unconfirmed')

        # Unmanaged dataset -> 403 dataset_access_denied
        unmanaged = await self.client.get(
            f'/v2/datasets/{self.ds_unmanaged}/documents/{self.doc_unmanaged}',
            headers=self.manager_headers,
        )
        self.assertEqual(unmanaged.status, 403)
        self.assertEqual((await unmanaged.json())['error'], 'dataset_access_denied')

        # Cross-dataset doc lookup -> 404 document_not_found
        wrong_ds = await self.client.get(
            f'/v2/datasets/{self.ds_common}/documents/{self.doc_lesson}',
            headers=self.manager_headers,
        )
        self.assertEqual(wrong_ds.status, 404)

        # Verified lesson lookup by primary id
        resp = await self.client.get(
            f'/v2/datasets/{self.ds_verified}/documents/{self.doc_lesson}',
            headers=self.manager_headers,
        )
        self.assertEqual(resp.status, 200)
        doc = await resp.json()
        self.assertEqual(doc['id'], self.doc_lesson)
        self.assertEqual(doc['datasetId'], self.ds_verified)
        self.assertEqual(doc['effective_date'], '2026-11-15')
        self.assertEqual(doc['externalMetadata']['first_source_date'], '2026-11-15')
        self.assertEqual(doc['externalMetadata']['lesson_type'], 'state')
        self.assertEqual(doc['externalMetadata']['why_learned'], 'real reason')
        self.assertEqual(doc['category'], '项目状态')
        self.assertEqual(doc['common_category'], '')
        self.assertEqual(doc['session_id'], 'sess-auth')
        self.assertEqual(doc['recall_count'], 2)

        # Lookup by legacy_id resolves canonical document
        legacy_resp = await self.client.get(
            f'/v2/datasets/{self.ds_verified}/documents/{self.doc_legacy}',
            headers=self.manager_headers,
        )
        self.assertEqual(legacy_resp.status, 200)
        self.assertEqual((await legacy_resp.json())['id'], self.doc_lesson)

        # Verified common & code dataset lookups succeed
        common_resp = await self.client.get(
            f'/v2/datasets/{self.ds_common}/documents/{self.doc_common}',
            headers=self.manager_headers,
        )
        self.assertEqual(common_resp.status, 200)
        c_doc = await common_resp.json()
        self.assertEqual(c_doc['common_category'], 'preference')
        self.assertEqual(c_doc['category'], '')

        code_resp = await self.client.get(
            f'/v2/datasets/{self.ds_code}/documents/{self.doc_code}',
            headers=self.manager_headers,
        )
        self.assertEqual(code_resp.status, 200)
        self.assertEqual((await code_resp.json())['id'], self.doc_code)

    async def test_v2_session_lookup_acl_and_user_disambiguation(self):
        unconf = await self.client.get(
            f'/v2/datasets/{self.ds_unconfirmed}/sessions/sess-unconf',
            headers=self.manager_headers,
        )
        self.assertEqual(unconf.status, 403)
        self.assertEqual((await unconf.json())['error'], 'project_unconfirmed')

        # Without user_id returns most recently active matching session (user_b)
        latest = await self.client.get(
            f'/v2/datasets/{self.ds_verified}/sessions/sess-shared',
            headers=self.manager_headers,
        )
        self.assertEqual(latest.status, 200)
        s_latest = await latest.json()
        self.assertEqual(s_latest['session_id'], 'sess-shared')
        self.assertEqual(s_latest['user_id'], self.user_b)
        self.assertEqual(s_latest['agent_id'], 'claude')
        self.assertEqual(s_latest['status'], 'active')

        # With explicit user_id returns user_a
        scoped = await self.client.get(
            f'/v2/datasets/{self.ds_verified}/sessions/sess-shared?user_id={self.user_a}',
            headers=self.manager_headers,
        )
        self.assertEqual(scoped.status, 200)
        self.assertEqual((await scoped.json())['user_id'], self.user_a)

        missing = await self.client.get(
            f'/v2/datasets/{self.ds_verified}/sessions/does-not-exist',
            headers=self.manager_headers,
        )
        self.assertEqual(missing.status, 404)

    async def test_v2_code_activity_acl_generation_dedup_and_indexed_plan(self):
        # Unconfirmed project rejected Fail-Closed
        unconf = await self.client.get('/v2/projects/p-unconfirmed/code-activity', headers=self.manager_headers)
        self.assertEqual(unconf.status, 403)
        self.assertEqual((await unconf.json())['error'], 'project_unconfirmed')

        # Unconfigured code project -> 409
        nocode = await self.client.get('/v2/projects/p-nocode/code-activity', headers=self.manager_headers)
        self.assertEqual(nocode.status, 409)

        # Verified code project -> ignores pre-generation active run, sees COMPLETED dedup -> active=False
        resp = await self.client.get('/v2/projects/p-verified/code-activity', headers=self.manager_headers)
        self.assertEqual(resp.status, 200)
        act = await resp.json()
        self.assertEqual(act['project_id'], 'p-verified')
        self.assertEqual(act['code_dataset_id'], self.ds_code)
        self.assertFalse(act['active'])
        self.assertEqual(act['status'], 'DATASET_PROCESSING_COMPLETED')
        self.assertEqual(act['run_id'], self.run_completed)

        # Insert an active remember operation (pipeline_name NULL, no outcome/ended_at) -> active=True
        con = sqlite3.connect(self.cognee_db)
        con.execute(
            'INSERT INTO pipeline_runs VALUES (?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.UUID(self.run_active).hex, uuid.UUID(self.ds_code).hex,
                None, 'remember', None, None,
                '2026-11-21 09:30:00', '2026-11-21 09:30:00', None,
            ),
        )
        con.commit()
        con.close()

        resp2 = await self.client.get('/v2/projects/p-verified/code-activity', headers=self.manager_headers)
        self.assertEqual(resp2.status, 200)
        act2 = await resp2.json()
        self.assertTrue(act2['active'])
        self.assertEqual(act2['status'], 'running')
        self.assertEqual(act2['run_id'], self.run_active)

        # Confirm SQLite uses upstream composite index ix_pipeline_runs_dataset_pipeline_created_at
        with official_sqlite_ro(self.cognee_db) as ro:
            plan = [
                row[3]
                for row in ro.execute(
                    '''EXPLAIN QUERY PLAN
                       SELECT id FROM pipeline_runs
                       WHERE dataset_id = ? AND pipeline_name = 'code_graph_pipeline'
                       ORDER BY created_at DESC''',
                    (uuid.UUID(self.ds_code).hex,),
                ).fetchall()
            ]
        self.assertTrue(any('ix_pipeline_runs_dataset_pipeline_created_at' in line for line in plan), plan)

    async def test_v2_lookups_fail_closed_on_official_401_403(self):
        class _FakeResp:
            def __init__(self, status):
                self.status = status

            async def __aenter__(self):
                return self

            async def __aexit__(self, exc_type, exc, tb):
                return False

            async def read(self):
                return b''

        class _FakeSession:
            def __init__(self, status):
                self.status = status
                self.calls = []

            def get(self, url, headers=None, allow_redirects=False, timeout=None):
                del allow_redirects, timeout
                self.calls.append((url, dict(headers or {})))
                return _FakeResp(self.status)

        for err_status in (401, 403):
            fake_sess = _FakeSession(err_status)
            self.registry.official = type(
                '_LiveOfficial',
                (),
                {
                    'url': 'http://127.0.0.1:18765',
                    'session': fake_sess,
                    'token_file': None,
                },
            )()
            doc_resp = await self.client.get(
                f'/v2/datasets/{self.ds_verified}/documents/{self.doc_lesson}',
                headers=self.manager_headers,
            )
            self.assertEqual(doc_resp.status, err_status)
            code_resp = await self.client.get(
                '/v2/projects/p-verified/code-activity',
                headers=self.manager_headers,
            )
            self.assertEqual(code_resp.status, err_status)
            self.assertGreaterEqual(len(fake_sess.calls), 2)
        self.registry.official = _DummyOfficial()


if __name__ == '__main__':
    unittest.main()
