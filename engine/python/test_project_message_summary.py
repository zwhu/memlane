import asyncio
import copy
from datetime import datetime, timezone
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import uuid

from aiohttp.test_utils import TestClient, TestServer

from project_control import Registry
from project_control_server import create_app
from project_message_summary import attach_project_message_summary
from project_policy import PolicyError


class OfficialStub:
    def __init__(self, responses=None):
        self.responses = dict(responses or {})
        self.calls = []

    async def request(self, method, path, body=None, token_file=None, *, adapter_token=None):
        self.calls.append((method, path, body))
        if path not in self.responses:
            raise AssertionError(f'unexpected official path: {path}')
        value = self.responses[path]
        if isinstance(value, BaseException):
            raise value
        if callable(value):
            result = value(method, path)
            return await result if asyncio.iscoroutine(result) else result
        return copy.deepcopy(value)


class BacklogStub:
    def __init__(self, payload=None, *, error=None):
        self.payload = payload
        self.error = error
        self.calls = []
        self.mutations = 0

    def get(self, project_id):
        self.calls.append(project_id)
        if self.error is not None:
            raise self.error
        return copy.deepcopy(self.payload)


class AsyncStatusBacklogStub:
    def __init__(self, payload):
        self.payload = payload
        self.calls = []

    async def status(self, project_id):
        self.calls.append(project_id)
        await asyncio.sleep(0)
        return copy.deepcopy(self.payload)


class ProjectMessageSummaryTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name).resolve()
        self.project_id = 'proj-alpha'
        self.dataset_id = str(uuid.uuid4())
        self.official = OfficialStub()
        self.registry = Registry(self.root / 'registry.json', self.official)
        self.registry.state['projects'][self.project_id] = {
            'id': self.project_id,
            'name': 'Alpha',
            'dataset_id': self.dataset_id,
            'aliases': [],
            'status': 'verified',
        }
        self.registry.save()
        self.manager_headers = {'Authorization': 'Bearer ' + 'm' * 40}
        self.agent_headers = {'Authorization': 'Bearer ' + 'a' * 40}

    async def asyncTearDown(self):
        self.registry.close()
        self.tmp.cleanup()

    def _write_notice_log(self, dataset_id):
        log_dir = self.root / 'runtime-logs'
        log_dir.mkdir(parents=True, exist_ok=True)
        now = datetime.now(timezone.utc).isoformat()
        req_id = 'a' * 32
        op_id = 'b' * 32
        (log_dir / 'official_service_dev.log').write_text(
            json.dumps({
                'schema_version': 1,
                'event': 'request_finished',
                'request_id': req_id,
                'operation_id': op_id,
                'component': 'generation_http',
                'dataset_id': dataset_id,
                'at': now,
                'status': 503,
                'backend_kind': 'provider',
            }) + '\n',
            encoding='utf-8',
        )
        return req_id, op_id

    async def _client(self, backlog=None, codex_config=None):
        app = create_app(self.registry, 'm' * 40, {'a': 'a' * 40})
        attach_project_message_summary(app, self.registry, backlog=backlog, codex_config=codex_config)
        client = TestClient(TestServer(app))
        await client.start_server()
        return client

    async def test_requires_manager_auth_and_known_project(self):
        client = await self._client()
        try:
            unauth = await client.get(f'/v1/projects/{self.project_id}/message-summary')
            self.assertEqual(unauth.status, 401)

            non_manager = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.agent_headers,
            )
            self.assertEqual(non_manager.status, 403)
            self.assertEqual((await non_manager.json())['error'], 'management_required')

            missing = await client.get(
                '/v1/projects/unknown-project/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(missing.status, 404)
            self.assertEqual((await missing.json())['error'], 'project_not_found')
        finally:
            await client.close()

    async def test_full_aggregation_filters_docs_and_is_read_only(self):
        req_id, op_id = self._write_notice_log(self.dataset_id)
        completed_at = datetime.now(timezone.utc).isoformat()
        raw_doc = {
            'id': 'doc-raw-1',
            'datasetId': self.dataset_id,
            'name': 'architecture-notes.md',
            'label': 'Architecture Notes',
            'updatedAt': '2026-11-21T08:00:00Z',
            'createdAt': '2026-11-20T08:00:00Z',
            'externalMetadata': {},
        }
        raw_machine_doc = {
            'id': 'doc-raw-2',
            'datasetId': self.dataset_id,
            'name': 'text_0123456789abcdef0123456789abcdef.txt',
            'label': None,
            'createdAt': '2026-11-21T07:00:00Z',
            'externalMetadata': {},
        }
        knowledge_doc = {
            'id': 'doc-knowledge',
            'datasetId': self.dataset_id,
            'name': 'lesson.md',
            'externalMetadata': {'node_set': ['session_learnings']},
        }
        common_doc = {
            'id': 'doc-common',
            'datasetId': self.dataset_id,
            'name': 'common.md',
            'externalMetadata': {'common_category': 'preferences'},
        }
        derived_doc = {
            'id': 'doc-derived',
            'datasetId': self.dataset_id,
            'name': 'cache.txt',
            'externalMetadata': {'node_set': ['user_sessions_from_cache']},
        }
        completed_doc = {
            'id': 'doc-done',
            'datasetId': self.dataset_id,
            'name': 'done.md',
            'externalMetadata': {},
        }
        self.official.responses = {
            f'/api/v1/datasets/{self.dataset_id}/processing-status?pipeline=cognify_pipeline': {
                'items': [
                    {'id': 'doc-raw-1', 'name': 'architecture-notes.md', 'completed': False},
                    {'id': 'doc-raw-2', 'name': 'text_0123456789abcdef0123456789abcdef.txt', 'completed': False},
                    {'id': 'doc-knowledge', 'name': 'lesson.md', 'completed': False},
                    {'id': 'doc-common', 'name': 'common.md', 'completed': False},
                    {'id': 'doc-derived', 'name': 'cache.txt', 'completed': False},
                    {'id': 'doc-done', 'name': 'done.md', 'completed': True},
                ],
            },
            f'/api/v1/datasets/{self.dataset_id}/data?limit=500': [
                raw_doc, raw_machine_doc, knowledge_doc, common_doc, derived_doc, completed_doc,
            ],
            f'/api/v1/activity/pipeline-runs?dataset_id={self.dataset_id}&limit=20': [
                {'id': 'run-1', 'dataset_id': self.dataset_id, 'status': 'completed'},
                {'id': 'run-other', 'dataset_id': 'other-dataset', 'status': 'completed'},
            ],
            f'/api/v1/activity/pipeline-runs?dataset_id={self.dataset_id}&limit=100': [{
                'kind': 'operation', 'dataset_id': self.dataset_id, 'pipeline_run_id': str(uuid.UUID(op_id)),
                'operation_name': 'improve', 'background': True, 'outcome': 'succeeded', 'ended_at': completed_at,
            }],
        }
        backlog = BacklogStub({
            'job': {'operation_id': 'op-1', 'status': 'watching', 'remaining': 2},
            'automatic_enabled': True,
            'buffer_reviews': [],
            'checks': [],
        })
        registry_before = (self.root / 'registry.json').read_bytes()

        client = await self._client(backlog=backlog)
        try:
            response = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(response.status, 200)
            body = await response.json()
        finally:
            await client.close()

        self.assertEqual(body['project_id'], self.project_id)
        self.assertEqual(body['dataset_id'], self.dataset_id)
        self.assertTrue(body['automatic_save'])
        self.assertEqual(body['backlog'], {'operation_id': 'op-1', 'status': 'watching', 'remaining': 2})
        self.assertEqual(body['processing_notices'], [])
        self.assertEqual(len(body['resolved_processing_notices']), 1)
        self.assertEqual(body['resolved_processing_notices'][0]['request_id'], req_id)
        self.assertEqual(body['resolved_processing_notices'][0]['operation_id'], op_id)
        self.assertEqual(body['resolved_processing_notices'][0]['resolved_at'], completed_at)
        self.assertEqual(
            body['pending_documents'],
            [
                {
                    'id': 'doc-raw-1',
                    'title': 'Architecture Notes',
                    'date': '2026-11-21T08:00:00Z',
                    'kind': '文档',
                    'document': raw_doc,
                },
                {
                    'id': 'doc-raw-2',
                    'title': 'text_0123456789abcdef0123456789abcdef.txt',
                    'date': '2026-11-21T07:00:00Z',
                    'kind': '文档',
                    'document': raw_machine_doc,
                },
            ],
        )
        self.assertEqual(body['activity'], [{'id': 'run-1', 'dataset_id': self.dataset_id, 'status': 'completed'}])
        self.assertEqual(
            body['knowledge_catalog'],
            [
                {'id': 'doc-knowledge', 'created_at': ''},
                {'id': 'doc-common', 'created_at': ''},
            ],
        )
        self.assertTrue(body['knowledge_catalog_version'])
        self.assertFalse(body['knowledge_catalog_unchanged'])
        self.assertEqual(body['errors'], {})
        # Read-only guarantee: only GET calls to official API and registry file untouched.
        self.assertTrue(all(method == 'GET' for method, _, _ in self.official.calls))
        self.assertEqual((self.root / 'registry.json').read_bytes(), registry_before)
        self.assertEqual(backlog.calls, [self.project_id])

    async def test_partial_branch_failures_report_errors_without_hiding_as_empty(self):
        # Symlinked log causes OSError in processing_notices.
        log_dir = self.root / 'runtime-logs'
        log_dir.mkdir(parents=True, exist_ok=True)
        target = self.root / 'outside.log'
        target.write_text('', encoding='utf-8')
        (log_dir / 'official_service_dev.log').symlink_to(target)

        # Processing status has missing `completed` boolean -> must record errors['documents'].
        self.official.responses = {
            f'/api/v1/datasets/{self.dataset_id}/processing-status?pipeline=cognify_pipeline': {
                'items': [{'id': 'doc-1', 'name': 'a.md', 'completed': None}],
            },
            f'/api/v1/datasets/{self.dataset_id}/data?limit=500': [
                {'id': 'doc-1', 'datasetId': self.dataset_id, 'name': 'a.md', 'externalMetadata': {}},
            ],
            f'/api/v1/activity/pipeline-runs?dataset_id={self.dataset_id}&limit=20': PolicyError('official_http_503', 503),
        }
        backlog = BacklogStub(error=RuntimeError('backlog boom'))

        client = await self._client(backlog=backlog)
        try:
            response = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(response.status, 200)
            body = await response.json()
        finally:
            await client.close()

        self.assertIsNone(body['backlog'])
        self.assertFalse(body['automatic_save'])
        self.assertEqual(body['processing_notices'], [])
        self.assertEqual(body['resolved_processing_notices'], [])
        self.assertEqual(body['pending_documents'], [])
        self.assertEqual(body['activity'], [])
        self.assertEqual(body['errors']['backlog'], 'backlog_unavailable')
        self.assertEqual(body['errors']['processing_notices'], 'processing_diagnostics_unavailable')
        self.assertEqual(body['errors']['documents'], 'incomplete_processing_status')
        self.assertEqual(body['errors']['activity'], 'official_http_503')

    async def test_supports_async_backlog_status_and_none_backlog(self):
        self.official.responses = {
            f'/api/v1/datasets/{self.dataset_id}/processing-status?pipeline=cognify_pipeline': {'items': []},
            f'/api/v1/datasets/{self.dataset_id}/data?limit=500': [],
            f'/api/v1/activity/pipeline-runs?dataset_id={self.dataset_id}&limit=20': [],
        }

        async_backlog = AsyncStatusBacklogStub({'job': {'status': 'running'}, 'automatic_save': True})
        client = await self._client(backlog=async_backlog)
        try:
            res = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(res.status, 200)
            body = await res.json()
            self.assertEqual(body['backlog'], {'status': 'running'})
            self.assertTrue(body['automatic_save'])
            self.assertEqual(body['errors'], {})
        finally:
            await client.close()

        client_none = await self._client(backlog=None)
        try:
            res = await client_none.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(res.status, 200)
            body = await res.json()
            self.assertIsNone(body['backlog'])
            self.assertFalse(body['automatic_save'])
            self.assertEqual(body['errors'], {})
        finally:
            await client_none.close()

    async def test_knowledge_catalog_version_delta_and_query_validation(self):
        k_doc = {
            'id': 'k-1',
            'datasetId': self.dataset_id,
            'name': 'lesson.md',
            'createdAt': '2026-11-21T08:00:00Z',
            'externalMetadata': {'node_set': ['session_learnings']},
        }
        self.official.responses = {
            f'/api/v1/datasets/{self.dataset_id}/processing-status?pipeline=cognify_pipeline': {
                'items': [{'id': 'k-1', 'name': 'lesson.md', 'completed': True}],
            },
            f'/api/v1/datasets/{self.dataset_id}/data?limit=500': [k_doc],
            f'/api/v1/activity/pipeline-runs?dataset_id={self.dataset_id}&limit=20': [],
        }

        client = await self._client()
        try:
            first = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(first.status, 200)
            b1 = await first.json()
            ver = b1['knowledge_catalog_version']
            self.assertTrue(ver)
            self.assertFalse(b1['knowledge_catalog_unchanged'])
            self.assertEqual(b1['knowledge_catalog'], [{'id': 'k-1', 'created_at': '2026-11-21T08:00:00Z'}])

            unchanged = await client.get(
                f'/v1/projects/{self.project_id}/message-summary?knowledge_version={ver}',
                headers=self.manager_headers,
            )
            self.assertEqual(unchanged.status, 200)
            b2 = await unchanged.json()
            self.assertEqual(b2['knowledge_catalog_version'], ver)
            self.assertTrue(b2['knowledge_catalog_unchanged'])
            self.assertIsNone(b2['knowledge_catalog'])

            stale = await client.get(
                f'/v1/projects/{self.project_id}/message-summary?knowledge_version=stale-ver',
                headers=self.manager_headers,
            )
            self.assertEqual(stale.status, 200)
            b3 = await stale.json()
            self.assertEqual(b3['knowledge_catalog_version'], ver)
            self.assertFalse(b3['knowledge_catalog_unchanged'])
            self.assertEqual(b3['knowledge_catalog'], [{'id': 'k-1', 'created_at': '2026-11-21T08:00:00Z'}])

            bad = await client.get(
                f'/v1/projects/{self.project_id}/message-summary?extra=1',
                headers=self.manager_headers,
            )
            self.assertEqual(bad.status, 400)
            self.assertEqual((await bad.json())['error'], 'invalid_summary_query')
        finally:
            await client.close()

    async def test_http_fallback_paginates_past_500_items(self):
        page1 = [
            {
                'id': f'done-{i}',
                'datasetId': self.dataset_id,
                'name': f'done-{i}.md',
                'externalMetadata': {},
            }
            for i in range(499)
        ]
        page1.append({
            'id': 'k-page1',
            'datasetId': self.dataset_id,
            'name': 'k1.md',
            'createdAt': '2026-11-20T01:00:00Z',
            'externalMetadata': {'node_set': ['session_learnings']},
        })
        page2 = [
            {
                'id': 'raw-page2',
                'datasetId': self.dataset_id,
                'name': 'late-pending.md',
                'createdAt': '2026-11-21T09:00:00Z',
                'externalMetadata': {},
            },
            {
                'id': 'k-page2',
                'datasetId': self.dataset_id,
                'name': 'k2.md',
                'createdAt': '2026-11-21T10:00:00Z',
                'externalMetadata': {'node_set': ['session_learnings']},
            },
        ]
        status_items = [{'id': d['id'], 'name': d['name'], 'completed': True} for d in page1]
        status_items.append({'id': 'raw-page2', 'name': 'late-pending.md', 'completed': False})
        status_items.append({'id': 'k-page2', 'name': 'k2.md', 'completed': True})

        self.official.responses = {
            f'/api/v1/datasets/{self.dataset_id}/processing-status?pipeline=cognify_pipeline': {'items': status_items},
            f'/api/v1/datasets/{self.dataset_id}/data?limit=500': page1,
            f'/api/v1/datasets/{self.dataset_id}/data?limit=500&offset=500': page2,
            f'/api/v1/activity/pipeline-runs?dataset_id={self.dataset_id}&limit=20': [],
        }

        client = await self._client()
        try:
            res = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(res.status, 200)
            body = await res.json()
        finally:
            await client.close()

        self.assertEqual([d['id'] for d in body['pending_documents']], ['raw-page2'])
        self.assertEqual(
            body['knowledge_catalog'],
            [
                {'id': 'k-page1', 'created_at': '2026-11-20T01:00:00Z'},
                {'id': 'k-page2', 'created_at': '2026-11-21T10:00:00Z'},
            ],
        )
        self.assertEqual(body['errors'], {})

    async def test_sqlite_fast_path_reads_store_without_official_http(self):
        official_data = self.root / 'official-store'
        db_dir = official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)

        ds_hex = uuid.UUID(self.dataset_id).hex
        other_ds_hex = uuid.uuid4().hex
        doc_pending = str(uuid.uuid4())
        doc_done = str(uuid.uuid4())
        doc_knowledge = str(uuid.uuid4())
        doc_common = str(uuid.uuid4())
        doc_derived = str(uuid.uuid4())
        doc_other = str(uuid.uuid4())
        run_id = str(uuid.uuid4())

        con = sqlite3.connect(db_dir / 'cognee_db')
        con.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), name VARCHAR, label VARCHAR,
                mime_type VARCHAR, raw_data_location VARCHAR, external_metadata JSON,
                node_set JSON, pipeline_status JSON, updated_at DATETIME, created_at DATETIME,
                content_hash VARCHAR
            );
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY, pipeline_run_id CHAR(32), parent_operation_id CHAR(32),
                dataset_id CHAR(32), pipeline_name VARCHAR, operation_name VARCHAR,
                status VARCHAR, outcome VARCHAR, background BOOLEAN, error_class VARCHAR,
                origin VARCHAR, created_at DATETIME, started_at DATETIME, ended_at DATETIME
            );
        ''')
        rows = [
            (
                uuid.UUID(doc_pending).hex, ds_hex, 'notes.md', 'Notes', 'text/markdown',
                '', '{}', None, '{}', '2026-11-21 08:00:00', '2026-11-21 07:00:00', 'h1',
            ),
            (
                uuid.UUID(doc_done).hex, ds_hex, 'done.md', None, 'text/markdown',
                '', '{}', None,
                json.dumps({'cognify_pipeline': {self.dataset_id: {'status': 'DATA_ITEM_PROCESSING_COMPLETED'}}}),
                '2026-11-21 06:00:00', '2026-11-21 06:00:00', 'h2',
            ),
            # Double-encoded node_set column (matches SQLite column storage) without external_metadata.node_set
            (
                uuid.UUID(doc_knowledge).hex, ds_hex, 'lesson.md', None, 'text/markdown',
                '', '{}', json.dumps(json.dumps(['session_learnings'])),
                json.dumps({'cognify_pipeline': {self.dataset_id: 'DATA_ITEM_PROCESSING_COMPLETED'}}),
                '2026-11-21 09:00:00', '2026-11-21 09:00:00', 'h3',
            ),
            (
                uuid.UUID(doc_common).hex, ds_hex, 'common.md', None, 'text/markdown',
                '', json.dumps({'common_category': 'preferences'}), None, '{}',
                '2026-11-21 08:30:00', '2026-11-21 08:30:00', 'h4',
            ),
            (
                uuid.UUID(doc_derived).hex, ds_hex, 'cache.txt', None, 'text/plain',
                '', json.dumps({'node_set': ['user_sessions_from_cache']}), None, '{}',
                '2026-11-21 08:15:00', '2026-11-21 08:15:00', 'h5',
            ),
            (
                uuid.UUID(doc_other).hex, other_ds_hex, 'foreign.md', None, 'text/markdown',
                '', json.dumps({'node_set': ['session_learnings']}), None, '{}',
                '2026-11-21 09:30:00', '2026-11-21 09:30:00', 'h6',
            ),
        ]
        con.executemany('INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?,?)', rows)
        con.execute(
            'INSERT INTO pipeline_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.UUID(run_id).hex, None, ds_hex,
                'cognify_pipeline', None, 'DATASET_PROCESSING_COMPLETED', 'succeeded', 1, None,
                'api', '2026-11-21 09:10:00', '2026-11-21 09:09:00', '2026-11-21 09:10:00',
            ),
        )
        con.execute(
            'INSERT INTO pipeline_runs VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.uuid4().hex, uuid.uuid4().hex, None, other_ds_hex,
                'cognify_pipeline', None, 'DATASET_PROCESSING_COMPLETED', 'succeeded', 1, None,
                'api', '2026-11-21 09:20:00', '2026-11-21 09:19:00', '2026-11-21 09:20:00',
            ),
        )
        con.commit()
        con.close()

        client = await self._client(codex_config={'official_data': str(official_data)})
        try:
            res = await client.get(
                f'/v1/projects/{self.project_id}/message-summary',
                headers=self.manager_headers,
            )
            self.assertEqual(res.status, 200)
            body = await res.json()

            self.assertEqual(self.official.calls, [])
            self.assertEqual(body['errors'], {})
            self.assertEqual([d['id'] for d in body['pending_documents']], [doc_pending])
            self.assertEqual(body['pending_documents'][0]['title'], 'Notes')
            self.assertEqual(body['pending_documents'][0]['date'], '2026-11-21T08:00:00')
            self.assertEqual(
                body['knowledge_catalog'],
                [
                    {'id': doc_knowledge, 'created_at': '2026-11-21T09:00:00'},
                    {'id': doc_common, 'created_at': '2026-11-21T08:30:00'},
                ],
            )
            self.assertEqual(len(body['activity']), 1)
            self.assertEqual(body['activity'][0]['pipeline_run_id'], run_id)
            self.assertEqual(body['activity'][0]['dataset_id'], self.dataset_id)

            ver = body['knowledge_catalog_version']
            res2 = await client.get(
                f'/v1/projects/{self.project_id}/message-summary?knowledge_version={ver}',
                headers=self.manager_headers,
            )
            self.assertEqual(res2.status, 200)
            body2 = await res2.json()
            self.assertTrue(body2['knowledge_catalog_unchanged'])
            self.assertIsNone(body2['knowledge_catalog'])
            self.assertEqual(self.official.calls, [])
        finally:
            await client.close()

    async def test_provenance_date_and_sqlite_schema_guard(self):
        from provenance_store import ProvenanceStore

        official_data = self.root / 'official-store'
        db_dir = official_data / 'system' / 'databases'
        db_dir.mkdir(parents=True)
        ds_hex = uuid.UUID(self.dataset_id).hex
        doc_lesson = str(uuid.uuid4())

        prov = ProvenanceStore(self.root / 'lesson-provenance.sqlite3')
        prov.record_document(
            self.dataset_id,
            doc_lesson,
            lesson_type='state',
            state_key='k-msg',
            candidate_dates=('2026-11-12',),
            source_qa_ids=('qa-1',),
        )
        prov.close()

        con = sqlite3.connect(db_dir / 'cognee_db')
        con.executescript('''
            CREATE TABLE data (
                id CHAR(32) PRIMARY KEY, dataset_id CHAR(32), name VARCHAR, label VARCHAR,
                mime_type VARCHAR, raw_data_location VARCHAR, external_metadata JSON,
                system_metadata JSON, node_set JSON, pipeline_status JSON,
                updated_at DATETIME, created_at DATETIME, content_hash VARCHAR
            );
            CREATE TABLE pipeline_runs (
                id CHAR(32) PRIMARY KEY, pipeline_run_id CHAR(32), parent_operation_id CHAR(32),
                dataset_id CHAR(32), pipeline_name VARCHAR, operation_name VARCHAR,
                status VARCHAR, outcome VARCHAR, background BOOLEAN, error_class VARCHAR,
                origin VARCHAR, created_at DATETIME, started_at DATETIME, ended_at DATETIME
            );
        ''')
        con.execute(
            'INSERT INTO data VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)',
            (
                uuid.UUID(doc_lesson).hex, ds_hex, 'lesson.md', None, 'text/markdown', '',
                json.dumps({'lesson_type': 'state', 'state_key': 'k-msg', 'first_source_date': '1999-01-01', 'node_set': ['session_learnings']}),
                json.dumps({'lesson_type': 'state', 'state_key': 'k-msg', 'session_date': '2026-11-16', 'node_set': ['session_learnings']}),
                json.dumps(['session_learnings']), '{}',
                '2026-11-21 09:00:00', '2026-11-21 09:00:00', 'h-lesson',
            ),
        )
        con.commit()
        con.close()

        client = await self._client(codex_config={'official_data': str(official_data)})
        try:
            res = await client.get(f'/v1/projects/{self.project_id}/message-summary', headers=self.manager_headers)
            self.assertEqual(res.status, 200)
            body = await res.json()
            self.assertEqual(body['knowledge_catalog'][0]['first_source_date'], '2026-11-12')

            # Break schema: drop pipeline_runs; with no official fallback responses, must fail closed 503.
            con = sqlite3.connect(db_dir / 'cognee_db')
            con.execute('DROP TABLE pipeline_runs')
            con.commit()
            con.close()
            self.registry.official = None
            broken = await client.get(f'/v1/projects/{self.project_id}/message-summary', headers=self.manager_headers)
            self.assertEqual(broken.status, 503)
            self.assertEqual((await broken.json())['error'], 'message_summary_store_unavailable')
        finally:
            await client.close()


if __name__ == '__main__':
    unittest.main()
