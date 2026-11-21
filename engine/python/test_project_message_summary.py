import asyncio
import copy
from datetime import datetime, timezone
import json
from pathlib import Path
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

    async def _client(self, backlog=None):
        app = create_app(self.registry, 'm' * 40, {'a': 'a' * 40})
        attach_project_message_summary(app, self.registry, backlog=backlog)
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
        self.assertEqual(len(body['processing_notices']), 1)
        self.assertEqual(body['processing_notices'][0]['request_id'], req_id)
        self.assertEqual(body['processing_notices'][0]['operation_id'], op_id)
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


if __name__ == '__main__':
    unittest.main()
