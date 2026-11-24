"""Tests for the Memlane Cognee HTTP Extension Gateway mounted at /cognee."""
import asyncio
from contextlib import redirect_stderr
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import AsyncMock, patch
import uuid

from aiohttp import FormData, web
from aiohttp.test_utils import TestClient, TestServer

sys.path.insert(0, str(Path(__file__).resolve().parent))
from _runtime_common import private_write
from knowledge_recall import RecallStore
from project_control import Official, Registry
from project_control_server import create_app
from project_policy import PolicyError


class _FakeRegistry(Registry):
    def __init__(self, path, official):
        super().__init__(path, official)


class GatewayTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name)
        self.workspace = self.root / 'repo'
        self.workspace.mkdir()

        self.owner_key = 'owner-secret-key'
        self.agent_key = 'agent-a-secret-key'
        self.retired_key = 'agent-r-secret-key'
        private_write(self.root / 'owner.key', self.owner_key.encode())
        private_write(self.root / 'agent_a.key', self.agent_key.encode())
        private_write(self.root / 'agent_r.key', self.retired_key.encode())
        private_write(self.root / 'control_a.tok', b'c' * 40)

        self.project_ds = str(uuid.uuid4())
        self.code_ds = str(uuid.uuid4())
        self.common_ds = str(uuid.uuid4())
        self.other_ds = str(uuid.uuid4())
        self.agent_principal_id = str(uuid.uuid4())
        self.other_user_id = str(uuid.uuid4())
        self.doc_proj = str(uuid.uuid4())
        self.doc_common = str(uuid.uuid4())
        self.upstream_calls = []
        self.sse_next = False
        self.recall_override = None
        self.recall_delay = 0.0
        self.connections_me_override = None

        async def upstream_handler(request):
            body = await request.read()
            self.upstream_calls.append({
                'method': request.method,
                'path': request.path,
                'query': request.rel_url.query_string,
                'headers': dict(request.headers),
                'body': body,
            })
            if request.path == '/api/v1/datasets':
                return web.json_response([
                    {'id': self.project_ds, 'name': 'proj-1'},
                    {'id': self.common_ds, 'name': 'ml-common'},
                    {'id': self.other_ds, 'name': 'unmanaged'},
                ])
            if request.path in ('/api/v1/datasets/status', '/api/v1/datasets/status/progress'):
                return web.json_response({
                    self.project_ds: 'DATASET_PROCESSING_COMPLETED',
                    self.common_ds: 'DATASET_PROCESSING_COMPLETED',
                    self.other_ds: 'DATASET_PROCESSING_STARTED',
                })
            if request.path == '/api/v1/sessions':
                return web.json_response({
                    'sessions': [
                        {'session_id': 'sess-ok', 'dataset_id': self.project_ds},
                        {'session_id': 'sess-secret', 'dataset_id': self.other_ds},
                        {'session_id': 'sess-nods', 'dataset_id': None},
                    ],
                    'total': 3,
                    'limit': 50,
                    'offset': 0,
                    'has_more': False,
                })
            if request.path == '/api/v1/activity/pipeline-runs':
                return web.json_response([
                    {'id': 'r1', 'dataset_id': self.project_ds, 'operation_name': 'improve'},
                    {'id': 'r2', 'dataset_id': self.other_ds, 'operation_name': 'improve'},
                    {'id': 'r3', 'dataset_id': None, 'operation_name': 'recall'},
                ])
            if request.path in ('/api/v1/sessions/sess-ok', '/api/v1/sessions/sess-ok/capture-status', '/api/v1/sessions/sess-ok/history'):
                return web.json_response({
                    'session_id': 'sess-ok',
                    'dataset_id': self.project_ds,
                    'user_id': self.agent_principal_id,
                    'qas': [],
                    'traces': [],
                })
            if request.path in ('/api/v1/sessions/sess-secret', '/api/v1/sessions/sess-secret/capture-status', '/api/v1/sessions/sess-secret/history'):
                return web.json_response({
                    'session_id': 'sess-secret',
                    'dataset_id': self.other_ds,
                    'user_id': self.agent_principal_id,
                    'qas': [{'question': 'secret'}],
                    'traces': [],
                })
            if request.path == '/api/v1/sessions/sess-other-owner':
                return web.json_response({
                    'session_id': 'sess-other-owner',
                    'dataset_id': self.project_ds,
                    'user_id': self.other_user_id,
                    'qas': [],
                    'traces': [],
                })
            if request.path == '/api/v1/sessions/sess-no-owner':
                return web.json_response({
                    'session_id': 'sess-no-owner',
                    'dataset_id': self.project_ds,
                    'qas': [],
                    'traces': [],
                })
            if request.path == '/api/v1/sessions/sess-null':
                return web.json_response({
                    'session_id': 'sess-null',
                    'dataset_id': None,
                    'user_id': self.agent_principal_id,
                    'qas': [],
                    'traces': [],
                })
            if request.path == '/api/v1/sessions/sess-fresh':
                return web.json_response({'detail': 'session not found'}, status=404)
            if request.path == '/api/v1/sessions/sess-err':
                return web.json_response({'detail': 'upstream error'}, status=500)
            if request.path == '/api/v1/sessions/sess-bad-ctype':
                return web.Response(status=200, text='plain text', content_type='text/plain')
            if request.path == '/api/v1/agents/connections/me':
                if self.connections_me_override is not None:
                    return web.json_response(self.connections_me_override)
                return web.json_response({
                    'agent': {'id': 'a1', 'dataset_ids': [self.project_ds, self.other_ds]},
                    'memory_sources': [
                        {'dataset_id': self.project_ds, 'name': 'proj-1'},
                        {'dataset_id': self.other_ds, 'name': 'secret'},
                    ],
                    'recent_sessions': [
                        {'session_id': 'sess-ok', 'dataset_id': self.project_ds},
                        {'session_id': 'sess-secret', 'dataset_id': self.other_ds},
                    ],
                    'recent_qas': [{'question': 'maybe secret'}],
                    'recent_traces': [{'origin_function': 'Read'}],
                })
            if request.path in ('/api/v1/agents/register', '/api/v1/agents/unregister'):
                parsed = json.loads(body) if body else None
                return web.json_response({'ok': True, 'received': parsed}, status=201 if request.path.endswith('/register') else 200)
            if request.path in ('/health', '/api/v1/health'):
                return web.json_response({'status': 'ready', 'saw_api_key': 'X-Api-Key' in request.headers})
            if request.path == '/api/v1/recall':
                if self.recall_delay > 0:
                    await asyncio.sleep(self.recall_delay)
                if self.recall_override is not None:
                    override = self.recall_override
                    self.recall_override = None
                    return web.json_response(override)
                if self.sse_next:
                    self.sse_next = False
                    resp = web.StreamResponse(status=200, headers={'Content-Type': 'text/event-stream'})
                    await resp.prepare(request)
                    await resp.write(b'data: {"chunk":"hello"}\n\n')
                    await resp.write_eof()
                    return resp
                return web.json_response([
                    {
                        'source': 'graph',
                        'text': 'project lesson',
                        'dataset_id': self.project_ds,
                        'metadata': {'document_id': self.doc_proj},
                    },
                    {
                        'source': 'graph',
                        'text': 'common lesson',
                        'dataset_id': self.common_ds,
                        'raw': {'doc_id': self.doc_common},
                    },
                ])
            if request.path == '/api/v1/search':
                return web.json_response([
                    {'dataset_id': self.project_ds, 'metadata': {'doc_id': self.doc_proj}, 'text': 'hit'},
                ])
            if request.path.startswith('/api/v1/capture/entries/') or request.path in (
                '/api/v1/remember/entry', '/api/v1/remember', '/api/v1/improve',
                '/api/v1/cognify', '/api/v1/memify', '/api/v1/forget',
            ):
                parsed = None
                if request.headers.get('Content-Type', '').startswith('application/json') and body:
                    parsed = json.loads(body)
                return web.json_response({'ok': True, 'received': parsed, 'raw_has_dataset_id': self.project_ds.encode() in body})
            return web.json_response({'ok': True, 'path': request.path})

        upstream_app = web.Application()
        upstream_app.router.add_route('*', '/{tail:.*}', upstream_handler)
        self.upstream = TestClient(TestServer(upstream_app))
        await self.upstream.start_server()
        upstream_url = f'http://127.0.0.1:{self.upstream.server.port}'

        self.registry = _FakeRegistry(self.root / 'registry.json', Official(self.upstream.session, upstream_url, str(self.root / 'owner.key')))
        self.registry.state['agents'] = {
            'agent-a': {
                'name': 'Agent A',
                'principal_id': self.agent_principal_id,
                'key_file': str(self.root / 'agent_a.key'),
                'control_token_file': str(self.root / 'control_a.tok'),
            },
            'agent-retired': {
                'name': 'Agent R',
                'principal_id': str(uuid.uuid4()),
                'key_file': str(self.root / 'agent_r.key'),
            },
        }
        self.registry.state['agent_retirements'] = {'agent-retired': {'status': 'revoking'}}
        self.registry.state['projects'] = {
            'proj-1': {
                'id': 'proj-1',
                'name': 'Repo',
                'dataset_id': self.project_ds,
                'dataset_name': 'proj-ds',
                'code_dataset_id': self.code_ds,
                'code_enabled': True,
                'code_configured': True,
                'aliases': [str(self.workspace)],
                'overrides': {},
                'desired_revision': 1,
                'status': 'verified',
                'applied': {'agent-a': {'revision': 1, 'status': 'verified'}},
            },
        }
        self.registry.state['common'] = {
            'id': 'common-1',
            'kind': 'common',
            'name': '通用知识',
            'dataset_id': self.common_ds,
            'dataset_name': 'ml-common-1',
            'overrides': {},
            'desired_revision': 1,
            'status': 'verified',
            'applied': {'agent-a': {'revision': 1, 'status': 'verified'}},
            'code_enabled': False,
        }
        self.registry.save()

        self.app = create_app(self.registry, 'm' * 40, {})
        self.client = TestClient(TestServer(self.app))
        await self.client.start_server()

    async def asyncTearDown(self):
        await self.client.close()
        self.registry.close()
        await self.upstream.close()
        self.tempdir.cleanup()

    def _headers(self, key=None, extra=None):
        hdrs = {'X-Api-Key': key or self.agent_key}
        if extra:
            hdrs.update(extra)
        return hdrs

    async def test_auth_origin_and_allowlist(self):
        r = await self.client.get('/cognee/api/v1/datasets', headers={**self._headers(), 'Origin': 'https://evil.example'})
        self.assertEqual(r.status, 403)
        self.assertEqual((await r.json())['error'], 'browser_origin_rejected')

        r = await self.client.get('/cognee/api/v1/datasets')
        self.assertEqual(r.status, 401)

        r = await self.client.get('/cognee/api/v1/datasets', headers=self._headers('wrong-key'))
        self.assertEqual(r.status, 401)

        r = await self.client.get('/cognee/api/v1/datasets', headers=self._headers(self.retired_key))
        self.assertEqual(r.status, 403)
        self.assertEqual((await r.json())['error'], 'agent_retired')

        for bad_path in (
            '/cognee/api/v1/permissions/datasets/123',
            '/cognee/api/v1/agents/create',
        ):
            r = await self.client.get(bad_path, headers=self._headers())
            self.assertEqual(r.status, 403, bad_path)
        import asyncio as _asyncio
        for wire_path in (
            '/cognee/api/v1/datasets/../users/me',
            '/cognee/api/v1/datasets/%2e%2e/users/me',
        ):
            reader, writer = await _asyncio.open_connection('127.0.0.1', self.client.server.port)
            writer.write(
                f'GET {wire_path} HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Api-Key: {self.agent_key}\r\nConnection: close\r\n\r\n'.encode()
            )
            await writer.drain()
            raw_head = (await reader.readline()).decode()
            writer.close()
            await writer.wait_closed()
            self.assertIn('403', raw_head, wire_path)
        self.assertEqual(self.upstream_calls, [])

    async def test_datasets_filter_and_key_passthrough(self):
        r = await self.client.get('/cognee/api/v1/datasets', headers=self._headers())
        self.assertEqual(r.status, 200)
        rows = await r.json()
        self.assertEqual({row['id'] for row in rows}, {self.project_ds, self.common_ds})
        self.assertEqual(self.upstream_calls[-1]['headers']['X-Api-Key'], self.agent_key)
        self.assertNotEqual(self.upstream_calls[-1]['headers']['X-Api-Key'], self.owner_key)

    async def test_recall_merges_common_records_hits_and_streams_sse(self):
        r = await self.client.post(
            '/cognee/api/v1/recall',
            headers=self._headers(extra={'X-Memlane-Recall-Channel': 'automatic'}),
            json={'query': 'how does retry work', 'dataset_ids': [self.project_ds]},
        )
        self.assertEqual(r.status, 200)
        forwarded = json.loads(self.upstream_calls[-1]['body'])
        self.assertEqual(forwarded['dataset_ids'], [self.project_ds, self.common_ds])

        stats_proj = await self.client.get(
            f'/v1/knowledge/recalls?dataset_id={self.project_ds}&document_id={self.doc_proj}',
            headers={'Authorization': 'Bearer ' + 'm' * 40},
        )
        self.assertEqual(stats_proj.status, 200)
        self.assertEqual((await stats_proj.json())['count'], 1)

        stats_common = await self.client.get(
            f'/v1/knowledge/recalls?dataset_id={self.common_ds}&document_id={self.doc_common}',
            headers={'Authorization': 'Bearer ' + 'm' * 40},
        )
        self.assertEqual((await stats_common.json())['count'], 1)

        # Code-only recall must not merge common_ds.
        r = await self.client.post(
            '/cognee/api/v1/search',
            headers=self._headers(),
            json={'query': 'find func', 'search_type': 'CODE', 'dataset_ids': [self.code_ds]},
        )
        self.assertEqual(r.status, 200)
        forwarded_code = json.loads(self.upstream_calls[-1]['body'])
        self.assertEqual(forwarded_code['dataset_ids'], [self.code_ds])

        # Streaming SSE passthrough
        self.sse_next = True
        r = await self.client.post('/cognee/api/v1/recall', headers=self._headers(), json={'query': 'stream test'})
        self.assertEqual(r.status, 200)
        self.assertEqual(await r.read(), b'data: {"chunk":"hello"}\n\n')

    async def test_paused_recall_writer_leaves_registry_and_event_loop_available(self):
        import threading
        loop = asyncio.get_running_loop()
        loop_thread = threading.get_ident()
        started, release = asyncio.Event(), threading.Event()
        original_record_query = RecallStore._record_query_on_writer

        def slow_writer(store, channel, hit_count):
            self.assertNotEqual(threading.get_ident(), loop_thread)
            loop.call_soon_threadsafe(started.set)
            if not release.wait(3):
                raise TimeoutError('test did not release recall writer')
            return original_record_query(store, channel, hit_count)

        with patch.object(RecallStore, '_record_query_on_writer', slow_writer):
            request = asyncio.create_task(self.client.post('/cognee/api/v1/recall', headers=self._headers(),
                                                           json={'query': 'test', 'dataset_ids': [self.project_ds]}))
            try:
                await asyncio.wait_for(started.wait(), 2)
                self.assertFalse(self.registry.mutex.locked())
                async with asyncio.timeout(2):
                    async with self.registry.mutex:
                        await asyncio.sleep(0)
                    health = await self.client.get('/cognee/health', headers=self._headers())
                self.assertEqual(health.status, 200)
                self.assertFalse(request.done())
            finally:
                release.set()
                response = await request
            self.assertEqual(response.status, 200)

    async def test_recall_write_failures_log_metadata_and_preserve_full_response(self):
        query = 'private query text'
        payload = [{'dataset_id': self.project_ds, 'text': 'private lesson content',
                    'metadata': {'document_id': self.doc_proj}}]
        self.recall_override = payload
        sensitive = [query, self.agent_key, self.project_ds, self.doc_proj, payload[0]['text']]
        failed_query = AsyncMock(side_effect=RuntimeError(' '.join(sensitive)))
        failed_record = AsyncMock(side_effect=PolicyError('invalid_recall_event', 409))
        with (patch.object(RecallStore, 'record_query_async', failed_query),
              patch.object(RecallStore, 'record_async', failed_record),
              self.assertLogs('cognee_extension_gateway', level='WARNING') as logs):
            response = await self.client.post('/cognee/api/v1/recall', headers=self._headers(),
                                              json={'query': query, 'dataset_ids': [self.project_ds]})
        self.assertEqual(response.status, 200)
        self.assertEqual(await response.json(), payload)
        failed_query.assert_awaited_once_with('explicit', 1)
        failed_record.assert_awaited_once()
        fields = {'event': 'recall_record_failed', 'component': 'cognee_gateway', 'channel': 'explicit', 'hit_count': 1}
        self.assertEqual([json.loads(record.getMessage()) for record in logs.records], [
            {**fields, 'stage': 'record_query', 'error_type': 'RuntimeError'},
            {**fields, 'stage': 'record', 'error_type': 'PolicyError', 'error_code': 'invalid_recall_event'},
        ])
        for value in sensitive:
            self.assertNotIn(value, '\n'.join(logs.output))

    async def test_codex_edit_diff_enrichment_and_gates(self):
        diff = '@@ -1 +1 @@\n-old\n+new\n'
        payload = {
            'dataset_id': self.project_ds,
            'session_id': 'sess-1',
            'entry': {
                'type': 'trace',
                'origin_function': 'Edit',
                'status': 'success',
                'method_params': {'file_path': 'a.py', 'change': 'update', 'diff': diff},
                'method_return_value': 'completed',
            },
        }
        r = await self.client.post('/cognee/api/v1/remember/entry', headers=self._headers(), json=payload)
        self.assertEqual(r.status, 200)
        sent = (await r.json())['received']
        self.assertEqual(sent['entry']['method_return_value'], {'status': 'completed', 'unified_diff': diff})

        msg_id = str(uuid.uuid4())
        r = await self.client.put(f'/cognee/api/v1/capture/entries/{msg_id}', headers=self._headers(), json=payload)
        self.assertEqual(r.status, 200)
        sent_cap = (await r.json())['received']
        self.assertEqual(sent_cap['entry']['method_return_value'], {'status': 'completed', 'unified_diff': diff})

        # Common dataset is strictly read-only.
        r = await self.client.post(
            '/cognee/api/v1/remember/entry',
            headers=self._headers(),
            json={**payload, 'dataset_id': self.common_ds},
        )
        self.assertEqual(r.status, 403)
        self.assertEqual((await r.json())['error'], 'common_dataset_read_only')

        # Pause capture -> blocks remember/capture but not read or improve.
        self.registry.state['projects']['proj-1']['overrides'] = {'agent-a': {'capture': False, 'auto_process': True}}
        self.registry.save()
        r = await self.client.post('/cognee/api/v1/remember/entry', headers=self._headers(), json=payload)
        self.assertEqual(r.status, 403)
        self.assertEqual((await r.json())['error'], 'capture_paused')

        # Pause auto_process -> blocks improve/cognify/memify.
        self.registry.state['projects']['proj-1']['overrides'] = {'agent-a': {'capture': True, 'auto_process': False}}
        self.registry.save()
        for p in ('/cognee/api/v1/improve', '/cognee/api/v1/cognify', '/cognee/api/v1/memify'):
            r = await self.client.post(p, headers=self._headers(), json={'dataset_id': self.project_ds})
            self.assertEqual(r.status, 403, p)
            self.assertEqual((await r.json())['error'], 'auto_process_paused')

        # Read-only access blocks delete & forget, allows GET data.
        self.registry.state['projects']['proj-1']['overrides'] = {'agent-a': {'access': 'read'}}
        self.registry.save()
        r = await self.client.get(f'/cognee/api/v1/datasets/{self.project_ds}/data', headers=self._headers())
        self.assertEqual(r.status, 200)
        r = await self.client.post('/cognee/api/v1/forget', headers=self._headers(), json={'dataset_id': self.project_ds})
        self.assertEqual(r.status, 403)
        self.assertEqual((await r.json())['error'], 'dataset_delete_denied')

        # forget everything=True always rejected.
        self.registry.state['projects']['proj-1']['overrides'] = {}
        self.registry.save()
        r = await self.client.post('/cognee/api/v1/forget', headers=self._headers(), json={'everything': True})
        self.assertEqual(r.status, 403)

    async def test_remember_multipart_and_urlencoded_dataset_name_resolution(self):
        form = FormData()
        form.add_field('datasetName', 'proj-ds')
        form.add_field('raw_data', 'hello world')
        r = await self.client.post('/cognee/api/v1/remember', headers=self._headers(), data=form)
        self.assertEqual(r.status, 200)
        self.assertTrue((await r.json())['raw_has_dataset_id'])

        mp = FormData()
        mp.add_field('datasetName', 'proj-ds')
        mp.add_field('data', b'file body', filename='note.txt', content_type='text/plain')
        r = await self.client.post('/cognee/api/v1/remember', headers=self._headers(), data=mp)
        self.assertEqual(r.status, 200)
        self.assertTrue((await r.json())['raw_has_dataset_id'])

    async def test_remember_entry_populates_missing_qa_context_date(self):
        from datetime import datetime
        missing = {
            'dataset_id': self.project_ds,
            'session_id': 'sess-qa-date',
            'entry': {
                'type': 'qa',
                'question': 'hello',
                'answer': 'world',
                'context': '{"cwd":"/repo"}',
                'prompt_time': '2026-11-19T08:00:00+00:00',
            },
        }
        expected_day = datetime.fromisoformat(missing['entry']['prompt_time']).astimezone().strftime('%Y-%m-%d')
        r1 = await self.client.post('/cognee/api/v1/remember/entry', headers=self._headers(), json=missing)
        self.assertEqual(r1.status, 200)
        forwarded1 = (await r1.json())['received']['entry']
        self.assertEqual(json.loads(forwarded1['context']), {'cwd': '/repo', 'date': expected_day})

        explicit = {
            **missing,
            'entry': {**missing['entry'], 'context': '{"cwd":"/repo","date":"2026-11-12"}'},
        }
        r2 = await self.client.post('/cognee/api/v1/remember/entry', headers=self._headers(), json=explicit)
        self.assertEqual(r2.status, 200)
        forwarded2 = (await r2.json())['received']['entry']
        self.assertEqual(json.loads(forwarded2['context']), {'cwd': '/repo', 'date': '2026-11-12'})

    async def test_recall_zero_hit_denominator_and_content_free_gateway_trace(self):
        output = io.StringIO()
        with redirect_stderr(output):
            self.recall_override = []
            r_miss = await self.client.post(
                '/cognee/api/v1/recall',
                headers=self._headers(extra={'X-Memlane-Recall-Channel': 'automatic'}),
                json={'query': 'private-miss-query', 'dataset_ids': [self.project_ds]},
            )
            self.assertEqual(r_miss.status, 200)
            r_hit = await self.client.post(
                '/cognee/api/v1/search',
                headers=self._headers(),
                json={'query': 'private-hit-query', 'dataset_ids': [self.project_ds]},
            )
            self.assertEqual(r_hit.status, 200)
            msg_id = str(uuid.uuid4())
            r_put = await self.client.put(
                f'/cognee/api/v1/capture/entries/{msg_id}',
                headers=self._headers(),
                json={'dataset_id': self.project_ds, 'session_id': 'sess-trace', 'entry': {'type': 'qa', 'question': 'q', 'answer': 'a'}},
            )
            self.assertEqual(r_put.status, 200)

        # Summary includes both 0-hit miss and 1-hit search -> 50% hit_rate.
        summary_resp = await self.client.get(
            '/v1/knowledge/recall-summary',
            headers={'Authorization': 'Bearer ' + 'm' * 40},
        )
        self.assertEqual(summary_resp.status, 200)
        summary = await summary_resp.json()
        self.assertEqual(summary['total_queries'], 2)
        self.assertEqual(summary['hit_queries'], 1)
        self.assertEqual(summary['empty_queries'], 1)
        self.assertEqual(summary['hit_rate'], 0.5)

        traces = [json.loads(line) for line in output.getvalue().splitlines() if line.startswith('{')]
        gw = [t for t in traces if t.get('component') == 'cognee_gateway' and t.get('event') == 'request_finished']
        self.assertEqual(len(gw), 3)
        self.assertEqual((gw[0]['route'], gw[0]['hit_count'], gw[0]['status']), ('POST /api/v1/recall', 0, 200))
        self.assertEqual((gw[1]['route'], gw[1]['hit_count'], gw[1]['status']), ('POST /api/v1/search', 1, 200))
        self.assertEqual((gw[2]['route'], gw[2]['status']), ('PUT /api/v1/capture/entries/{id}', 200))
        for secret in ('private-miss-query', 'private-hit-query', self.project_ds, msg_id, self.agent_key):
            self.assertNotIn(secret, output.getvalue())

    async def test_recall_query_uses_short_timeout_and_emits_timeout_trace(self):
        import cognee_extension_gateway as gw_mod
        self.recall_delay = 0.25
        output = io.StringIO()
        with patch.object(gw_mod, 'GATEWAY_QUERY_TIMEOUT_SECONDS', 0.05), redirect_stderr(output):
            r = await self.client.post(
                '/cognee/api/v1/recall',
                headers=self._headers(),
                json={'query': 'slow-query', 'dataset_ids': [self.project_ds]},
            )
        self.assertEqual(r.status, 503)
        self.assertEqual((await r.json())['error'], 'official_result_unknown')
        traces = [json.loads(line) for line in output.getvalue().splitlines() if line.startswith('{')]
        gw_traces = [t for t in traces if t.get('component') == 'cognee_gateway']
        self.assertEqual(len(gw_traces), 1)
        self.assertEqual((gw_traces[0]['outcome'], gw_traces[0]['route'], gw_traces[0]['status']),
                         ('timeout', 'POST /api/v1/recall', 503))

    async def test_read_routes_enforce_acl_filtering_and_transition_isolation(self):
        # 1. GET /api/v1/datasets/status filters unmanaged and rejects restricted query params.
        r = await self.client.get('/cognee/api/v1/datasets/status', headers=self._headers())
        self.assertEqual(r.status, 200)
        self.assertEqual(set((await r.json()).keys()), {self.project_ds, self.common_ds})

        r_bad_q = await self.client.get(f'/cognee/api/v1/datasets/status?dataset={self.other_ds}', headers=self._headers())
        self.assertEqual(r_bad_q.status, 403)

        # 2. GET /api/v1/sessions filters out unmanaged/untagged sessions and adjusts total.
        r = await self.client.get('/cognee/api/v1/sessions', headers=self._headers())
        self.assertEqual(r.status, 200)
        body = await r.json()
        self.assertEqual([s['session_id'] for s in body['sessions']], ['sess-ok'])
        self.assertEqual(body['total'], 1)

        # 3. GET /api/v1/activity/pipeline-runs filters unmanaged/untagged and rejects restricted dataset_id query.
        r = await self.client.get('/cognee/api/v1/activity/pipeline-runs', headers=self._headers())
        self.assertEqual(r.status, 200)
        self.assertEqual([row['id'] for row in await r.json()], ['r1'])

        r_bad_act = await self.client.get(
            f'/cognee/api/v1/activity/pipeline-runs?dataset_id={self.other_ds}',
            headers=self._headers(),
        )
        self.assertEqual(r_bad_act.status, 403)

        # 4. GET /api/v1/sessions/{sid} (+ /capture-status, /history) enforces dataset_id ACL and blocks reserved subpaths.
        for suffix in ('', '/capture-status', '/history'):
            ok = await self.client.get(f'/cognee/api/v1/sessions/sess-ok{suffix}', headers=self._headers())
            self.assertEqual(ok.status, 200, suffix)
            denied = await self.client.get(f'/cognee/api/v1/sessions/sess-secret{suffix}', headers=self._headers())
            self.assertEqual(denied.status, 403, suffix)
            self.assertEqual((await denied.json())['error'], 'dataset_read_denied')

        for reserved in ('stats', 'cost-by-model', 'with-agent-info', 'cost-by-user-agent'):
            blocked = await self.client.get(f'/cognee/api/v1/sessions/{reserved}', headers=self._headers())
            self.assertEqual(blocked.status, 403, reserved)
            self.assertEqual((await blocked.json())['error'], 'gateway_route_not_allowed')

        # Non-JSON 200 on an ACL-filtered read route fails closed with 503.
        bad_ctype = await self.client.get('/cognee/api/v1/sessions/sess-bad-ctype', headers=self._headers())
        self.assertEqual(bad_ctype.status, 503)

        # 5. Project in transition/unconfirmed (applied revision stale) must not leak through any of the 4 read routes.
        self.registry.state['projects']['proj-1']['desired_revision'] = 2
        self.registry.save()
        r_stat = await self.client.get('/cognee/api/v1/datasets/status', headers=self._headers())
        self.assertEqual(set((await r_stat.json()).keys()), {self.common_ds})
        r_stat_explicit = await self.client.get(
            f'/cognee/api/v1/datasets/status?dataset={self.project_ds}', headers=self._headers()
        )
        self.assertEqual(r_stat_explicit.status, 403)
        r_sess = await self.client.get('/cognee/api/v1/sessions', headers=self._headers())
        self.assertEqual((await r_sess.json())['sessions'], [])
        r_runs = await self.client.get('/cognee/api/v1/activity/pipeline-runs', headers=self._headers())
        self.assertEqual(await r_runs.json(), [])
        r_sid = await self.client.get('/cognee/api/v1/sessions/sess-ok', headers=self._headers())
        self.assertEqual(r_sid.status, 403)

    async def test_host_routes_unauth_health_lifecycle_and_capture_pause_gate(self):
        # Unauthenticated /health works for host probes and never forwards owner key.
        r_health = await self.client.get('/cognee/health')
        self.assertEqual(r_health.status, 200)
        self.assertEqual(await r_health.json(), {'status': 'ready', 'saw_api_key': False})
        r_v1_health = await self.client.get('/cognee/api/v1/health', headers=self._headers())
        self.assertEqual(r_v1_health.status, 200)
        self.assertEqual(await r_v1_health.json(), {'status': 'ready', 'saw_api_key': True})
        r_retired_health = await self.client.get('/cognee/health', headers=self._headers(self.retired_key))
        self.assertEqual(r_retired_health.status, 403)
        r_origin_health = await self.client.get('/cognee/health', headers={'Origin': 'https://evil.example'})
        self.assertEqual(r_origin_health.status, 403)

        # Dataset processing-status and data/count routes allowed for read; DELETE data/count forbidden.
        r_ps = await self.client.get(f'/cognee/api/v1/datasets/{self.project_ds}/processing-status', headers=self._headers())
        self.assertEqual(r_ps.status, 200)
        r_cnt = await self.client.get(f'/cognee/api/v1/datasets/{self.project_ds}/data/count', headers=self._headers())
        self.assertEqual(r_cnt.status, 200)
        r_del_cnt = await self.client.delete(f'/cognee/api/v1/datasets/{self.project_ds}/data/count', headers=self._headers())
        self.assertEqual(r_del_cnt.status, 403)

        # Agent lifecycle register/unregister + connections/me filtering.
        r_reg = await self.client.post(
            '/cognee/api/v1/agents/register',
            headers=self._headers(),
            json={'agent_session_name': 'codex-1', 'dataset_names': ['proj-ds']},
        )
        self.assertEqual(r_reg.status, 201)
        self.assertEqual((await r_reg.json())['received']['dataset_ids'], [self.project_ds])

        r_reg_denied = await self.client.post(
            '/cognee/api/v1/agents/register',
            headers=self._headers(),
            json={'agent_session_name': 'codex-1', 'dataset_ids': [self.other_ds]},
        )
        self.assertEqual(r_reg_denied.status, 403)

        r_unreg = await self.client.post(
            '/cognee/api/v1/agents/unregister',
            headers=self._headers(),
            json={'agent_session_name': 'codex-1'},
        )
        self.assertEqual(r_unreg.status, 200)

        r_me = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
        self.assertEqual(r_me.status, 200)
        me_body = await r_me.json()
        self.assertEqual(me_body['agent']['dataset_ids'], [self.project_ds])
        self.assertEqual([m['dataset_id'] for m in me_body['memory_sources']], [self.project_ds])
        self.assertEqual([s['session_id'] for s in me_body['recent_sessions']], ['sess-ok'])
        self.assertEqual(me_body['recent_qas'], [])

        # capture=False blocks PUT /capture/entries/{msg}, POST /remember/entry, and POST /remember.
        self.registry.state['projects']['proj-1']['overrides'] = {'agent-a': {'capture': False, 'auto_process': True}}
        self.registry.save()
        msg_id = str(uuid.uuid4())
        r_cap = await self.client.put(
            f'/cognee/api/v1/capture/entries/{msg_id}',
            headers=self._headers(),
            json={'dataset_id': self.project_ds, 'session_id': 's1', 'entry': {'type': 'qa', 'question': 'q', 'answer': 'a'}},
        )
        self.assertEqual(r_cap.status, 403)
        self.assertEqual((await r_cap.json())['error'], 'capture_paused')

        r_rem = await self.client.post(
            '/cognee/api/v1/remember',
            headers=self._headers(),
            json={'dataset_id': self.project_ds, 'raw_data': 'fact'},
        )
        self.assertEqual(r_rem.status, 403)
        self.assertEqual((await r_rem.json())['error'], 'capture_paused')

    async def test_connections_me_real_cognee_167_schema_and_acl_enforcement(self):
        user_id = self.registry.state['agents']['agent-a']['principal_id']

        # 1. Real Cognee 1.6.7 AgentDetailResponse: recent_sessions has ONLY {session_id, user_id} (no dataset_id),
        #    memory_sources uses {id, name}, and agent.datasets uses AgentDatasetRef dicts.
        self.connections_me_override = {
            'agent': {
                'id': 'conn-1',
                'agent_session_name': 'codex-1',
                'type': 'codex',
                'memory_mode': 'hybrid',
                'session_id': 'sess-ok',
                'user_id': user_id,
                'status': 'active',
                'datasets': [
                    {'id': self.project_ds, 'name': None, 'role': 'read_write', 'type': 'dataset'},
                    {'id': None, 'name': 'proj-ds', 'role': 'read_write', 'type': 'project_dataset'},
                    {'id': self.other_ds, 'name': None, 'role': 'read_write', 'type': 'dataset'},
                ],
            },
            'memory_sources': [
                {'id': self.project_ds, 'name': 'proj-ds', 'type': 'dataset', 'status': 'active'},
                {'id': self.other_ds, 'name': 'secret', 'type': 'dataset', 'status': 'active'},
            ],
            'recent_sessions': [{'session_id': 'sess-ok', 'user_id': user_id}],
            'recent_traces': [{'origin_function': 'Read', 'status': 'success'}],
            'recent_qas': [{'question': 'how to build', 'answer': 'use scripts/go.sh'}],
        }
        r = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
        self.assertEqual(r.status, 200)
        body = await r.json()
        self.assertEqual([m['id'] for m in body['memory_sources']], [self.project_ds])
        self.assertEqual(len(body['agent']['datasets']), 2)
        self.assertEqual(body['recent_sessions'], [{'session_id': 'sess-ok', 'user_id': user_id}])
        self.assertEqual(body['recent_traces'], [{'origin_function': 'Read', 'status': 'success'}])
        self.assertEqual(body['recent_qas'], [{'question': 'how to build', 'answer': 'use scripts/go.sh'}])

        # 2. Real Cognee 1.6.7 response pointing at an unauthorized session -> stripped + traces/qas cleared.
        self.connections_me_override = {
            **self.connections_me_override,
            'agent': {**self.connections_me_override['agent'], 'session_id': 'sess-secret'},
            'recent_sessions': [{'session_id': 'sess-secret', 'user_id': user_id}],
            'recent_traces': [{'origin_function': 'Edit'}],
            'recent_qas': [{'question': 'top secret', 'answer': 'nope'}],
        }
        r_sec = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
        self.assertEqual(r_sec.status, 200)
        sec_body = await r_sec.json()
        self.assertEqual(sec_body['recent_sessions'], [])
        self.assertEqual(sec_body['recent_traces'], [])
        self.assertEqual(sec_body['recent_qas'], [])

        # 3. Unbound session (dataset_id=None), mismatched/missing owner user_id, and upstream 500 error fail closed.
        for bad_sid in ('sess-null', 'sess-other-owner', 'sess-no-owner', 'sess-err'):
            self.connections_me_override = {
                **self.connections_me_override,
                'agent': {**self.connections_me_override['agent'], 'session_id': bad_sid},
                'recent_sessions': [{'session_id': bad_sid, 'user_id': user_id}],
                'recent_traces': [{'origin_function': 'Read'}],
                'recent_qas': [{'question': 'q', 'answer': 'a'}],
            }
            r_bad = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
            self.assertEqual(r_bad.status, 200)
            bad_body = await r_bad.json()
            self.assertEqual(bad_body['recent_sessions'], [], bad_sid)
            self.assertEqual(bad_body['recent_traces'], [], bad_sid)
            self.assertEqual(bad_body['recent_qas'], [], bad_sid)

        # 4. Freshly registered connection (404 on /sessions/{sid}): allowed ONLY when all agent datasets
        #    are readable and there are no unattributed QAs/traces.
        self.connections_me_override = {
            'agent': {
                'id': 'conn-fresh',
                'agent_session_name': 'codex-fresh',
                'session_id': 'sess-fresh',
                'user_id': user_id,
                'status': 'active',
                'datasets': [{'id': self.project_ds, 'name': None, 'role': 'read_write', 'type': 'dataset'}],
            },
            'memory_sources': [{'id': self.project_ds, 'name': 'proj-ds'}],
            'recent_sessions': [{'session_id': 'sess-fresh', 'user_id': user_id}],
            'recent_traces': [],
            'recent_qas': [],
        }
        r_fresh = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
        self.assertEqual(r_fresh.status, 200)
        self.assertEqual((await r_fresh.json())['recent_sessions'], [{'session_id': 'sess-fresh', 'user_id': user_id}])

        # 404 with non-empty QAs/traces fails closed (cannot prove dataset ownership of cached entries).
        self.connections_me_override = {
            **self.connections_me_override,
            'recent_qas': [{'question': 'unattributed', 'answer': 'must drop'}],
        }
        r_fresh_qa = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
        self.assertEqual(r_fresh_qa.status, 200)
        fresh_qa_body = await r_fresh_qa.json()
        self.assertEqual(fresh_qa_body['recent_sessions'], [])
        self.assertEqual(fresh_qa_body['recent_qas'], [])

        # 5. Project entering transition state immediately hides sess-ok, traces, qas, datasets, and memory_sources.
        self.connections_me_override = {
            'agent': {
                'id': 'conn-1',
                'agent_session_name': 'codex-1',
                'session_id': 'sess-ok',
                'user_id': user_id,
                'status': 'active',
                'datasets': [{'id': self.project_ds, 'name': None, 'role': 'read_write', 'type': 'dataset'}],
            },
            'memory_sources': [{'id': self.project_ds, 'name': 'proj-ds'}],
            'recent_sessions': [{'session_id': 'sess-ok', 'user_id': user_id}],
            'recent_traces': [{'origin_function': 'Read'}],
            'recent_qas': [{'question': 'hi', 'answer': 'yo'}],
        }
        self.registry.state['projects']['proj-1']['status'] = 'transition'
        self.registry.save()
        r_trans = await self.client.get('/cognee/api/v1/agents/connections/me', headers=self._headers())
        self.assertEqual(r_trans.status, 200)
        trans_body = await r_trans.json()
        self.assertEqual(trans_body['memory_sources'], [])
        self.assertEqual(trans_body['agent']['datasets'], [])
        self.assertEqual(trans_body['recent_sessions'], [])
        self.assertEqual(trans_body['recent_traces'], [])
        self.assertEqual(trans_body['recent_qas'], [])


if __name__ == '__main__':
    unittest.main()
