"""Tests for the Memlane Cognee HTTP Extension Gateway mounted at /cognee."""
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
import uuid

from aiohttp import FormData, web
from aiohttp.test_utils import TestClient, TestServer

sys.path.insert(0, str(Path(__file__).resolve().parent))
from _runtime_common import private_write
from project_control import Official, Registry
from project_control_server import create_app


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
        self.doc_proj = str(uuid.uuid4())
        self.doc_common = str(uuid.uuid4())
        self.upstream_calls = []
        self.sse_next = False

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
            if request.path == '/api/v1/recall':
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
                'principal_id': str(uuid.uuid4()),
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


if __name__ == '__main__':
    unittest.main()
