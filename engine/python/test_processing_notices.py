import json
from pathlib import Path
import sqlite3
import tempfile
import types
import unittest
from datetime import datetime, timedelta, timezone
from aiohttp.test_utils import TestClient, TestServer
from project_control_server import create_app
from processing_notices import ACTIVITY_PAGE_SIZE, MAX_RESOLVED_NOTICES, processing_notices, read_processing_notices


class ProcessingNoticesTests(unittest.IsolatedAsyncioTestCase):
    async def test_project_scoped_bounded_projection_and_manager_auth(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);logs=root/'runtime-logs';logs.mkdir()
            stamp=datetime.now(timezone.utc).isoformat();request='a'*32;operation='12345678-1234-1234-1234-123456789abc'
            row={'schema_version':1,'event':'request_finished','component':'generation_http',
                 'request_id':request,'operation_id':operation,'dataset_id':'dataset','at':stamp,
                 'status':502,'backend_kind':'codex','outcome':'succeeded','private':'SECRET'}
            (logs/'official_service_dev.log').write_text('PRIVATE LOG\n'+json.dumps(row)+'\n'+json.dumps(row)+'\n'+json.dumps({**row,'dataset_id':'other'})+'\n')
            (logs/'codex_completion_endpoint.log').write_text(json.dumps({**row,'component':'codex_bridge','outcome':'codex_model_failed'})+'\n')
            items=processing_notices(logs,'dataset')
            self.assertEqual(len(items),1)
            self.assertEqual((items[0]['http_status'],items[0]['code']),(502,'codex_model_failed'))
            self.assertNotIn('SECRET',json.dumps(items))
            self.assertEqual(processing_notices(logs,'not-this-project'),[])
            registry=types.SimpleNamespace(path=root/'registry.json',state={'agents':{},'projects':{'p':{'dataset_id':'dataset'}}})
            app=create_app(registry,'m'*40,{'a':'a'*40})
            async with TestClient(TestServer(app)) as client:
                denied=await client.get('/v1/projects/p/processing-notices',headers={'Authorization':'Bearer '+'a'*40})
                self.assertEqual(denied.status,403)
                response=await client.get('/v1/projects/p/processing-notices',headers={'Authorization':'Bearer '+'m'*40})
                self.assertEqual(response.status,200)
                self.assertEqual(await response.json(), {'items': items, 'resolved_items': []})

    def test_reads_agent_completion_log_and_ignores_successful_requests(self):
        with tempfile.TemporaryDirectory() as directory:
            logs = Path(directory) / 'runtime-logs'
            logs.mkdir()
            now = datetime(2026, 11, 21, 1, 0, 0, tzinfo=timezone.utc)
            fail_at = (now - timedelta(minutes=10)).isoformat()
            ok_before = (now - timedelta(minutes=20)).isoformat()
            ok_after = (now - timedelta(minutes=3)).isoformat()
            req_id = 'b' * 32
            op_id = '12345678-1234-1234-1234-123456789abc'
            base = {'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
                    'dataset_id': 'dataset', 'backend_kind': 'codex', 'outcome': 'succeeded'}
            (logs / 'official_service_dev.log').write_text('\n'.join([
                json.dumps({**base, 'request_id': 'c' * 32, 'operation_id': op_id, 'at': ok_before, 'status': 200}),
                json.dumps({**base, 'request_id': req_id, 'operation_id': op_id, 'at': fail_at, 'status': 502}),
            ]) + '\n')
            # Legacy log lacks this entry; current agent_completion_endpoint.log supplies codex_connection_failed.
            (logs / 'codex_completion_endpoint.log').write_text(
                json.dumps({'schema_version': 1, 'event': 'request_finished', 'component': 'codex_bridge',
                            'request_id': req_id, 'outcome': 'codex_model_failed'}) + '\n'
            )
            (logs / 'agent_completion_endpoint.log').write_text(
                json.dumps({'schema_version': 1, 'event': 'request_finished', 'component': 'codex_bridge',
                            'request_id': req_id, 'outcome': 'codex_connection_failed'}) + '\n'
            )
            without_recovery = processing_notices(logs, 'dataset', now=now)
            self.assertEqual(len(without_recovery), 1)
            self.assertEqual(without_recovery[0]['code'], 'codex_connection_failed')
            self.assertNotIn('recovered_at', without_recovery[0])

            with (logs / 'official_service_dev.log').open('a') as handle:
                handle.write(json.dumps({**base, 'request_id': 'd' * 32, 'operation_id': '99999999-1234-1234-1234-123456789abc',
                                         'at': ok_after, 'status': 200}) + '\n')
                handle.write(json.dumps({**base, 'request_id': 'e' * 32, 'operation_id': op_id,
                                         'at': ok_after, 'status': 200}) + '\n')
            self.assertEqual(processing_notices(logs, 'dataset', now=now), without_recovery)

    def _write_project_failures(self, root, dataset, now, operations):
        logs = root / 'runtime-logs'
        logs.mkdir(exist_ok=True)
        with (logs / 'official_service_dev.log').open('w') as handle:
            for index, operation in enumerate(operations):
                row = {
                    'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
                    'dataset_id': dataset, 'backend_kind': 'codex', 'status': 502,
                    'at': (now - timedelta(minutes=40, seconds=index)).isoformat(),
                    'request_id': f'{index:032x}', 'operation_id': operation,
                }
                handle.write(json.dumps(row) + '\n')

    async def test_sqlite_exact_receipt_beyond_recent_activity_and_bounded_history(self):
        dataset = 'aaaaaaaa-1234-1234-1234-123456789abc'
        resolved_ops = [f'{i:08x}-1234-1234-1234-123456789abc' for i in range(1, 24)]
        unresolved_op = 'bbbbbbbb-1234-1234-1234-123456789abc'
        older_unresolved_op = 'cccccccc-1234-1234-1234-123456789abc'
        now = datetime.now(timezone.utc)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            official_root = root / 'official'
            db_path = official_root / 'system' / 'databases' / 'cognee_db'
            db_path.parent.mkdir(parents=True)
            with sqlite3.connect(db_path) as con:
                con.execute("""CREATE TABLE pipeline_runs (
                    pipeline_run_id TEXT, dataset_id TEXT, pipeline_name TEXT,
                    parent_operation_id TEXT, operation_name TEXT, outcome TEXT, ended_at TEXT,
                    background BOOLEAN, created_at TEXT
                )""")
                con.execute("CREATE INDEX run_id_fixture_index ON pipeline_runs (pipeline_run_id)")
                ended = (now - timedelta(minutes=39)).replace(tzinfo=None).isoformat(sep=' ')
                con.executemany("INSERT INTO pipeline_runs VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", [
                    (operation.replace('-', ''), dataset.replace('-', ''), None, None, 'improve', 'succeeded', ended,
                     True, ended) for operation in resolved_ops
                ])
                con.executemany("INSERT INTO pipeline_runs VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", [
                    (f'other-{i}', dataset, None, None, 'improve', 'succeeded', now.isoformat(), True, now.isoformat())
                    for i in range(25)
                ])
                # A completed child or a different dataset sharing an ID cannot settle this operation.
                con.executemany("INSERT INTO pipeline_runs VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", [
                    (unresolved_op, 'other', None, None, 'improve', 'succeeded', ended, True, ended),
                    (unresolved_op, dataset, 'memify_pipeline', unresolved_op, 'memify_pipeline', 'succeeded', ended,
                     True, ended),
                ])
            self._write_project_failures(root, dataset, now, resolved_ops + [unresolved_op, older_unresolved_op])
            registry = types.SimpleNamespace(path=root / 'registry.json')
            items, history = await read_processing_notices(registry, dataset, official_data=official_root)
            self.assertEqual(len(items), 1)
            self.assertEqual(len(history), MAX_RESOLVED_NOTICES)
            self.assertEqual(items[0]['operation_id'], unresolved_op)
            self.assertNotIn('resolved_at', items[0])
            self.assertEqual(history[0]['operation_id'], resolved_ops[0])
            self.assertEqual(history[0]['resolved_at'], (now - timedelta(minutes=39)).isoformat())

    async def test_http_fallback_paginates_and_requires_final_improve_success(self):
        dataset = 'aaaaaaaa-1234-1234-1234-123456789abc'
        operations = [f'{i:08x}-1234-1234-1234-123456789abc' for i in range(1, 7)]
        recovered, failed, running, noop, stale, other_kind = operations
        now = datetime.now(timezone.utc)
        ended = (now - timedelta(minutes=3)).isoformat()
        base = {'dataset_id': dataset, 'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
                'ended_at': ended, 'background': True}
        calls = []

        async def request(method, url):
            calls.append((method, url))
            if 'offset=' not in url:
                return [{**base, 'pipeline_run_id': 'different'}] * ACTIVITY_PAGE_SIZE
            return [
                {**base, 'pipeline_run_id': recovered},
                {**base, 'pipeline_run_id': failed, 'outcome': 'failed'},
                {**base, 'pipeline_run_id': running, 'ended_at': None},
                {**base, 'pipeline_run_id': noop, 'outcome': 'noop'},
                {**base, 'pipeline_run_id': stale, 'ended_at': (now - timedelta(hours=1)).isoformat()},
                {**base, 'pipeline_run_id': other_kind, 'operation_name': 'cognify'},
            ]

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            registry = types.SimpleNamespace(path=root / 'registry.json', official=types.SimpleNamespace(request=request))
            for operation in operations[1:]:
                with self.subTest(operation=operation):
                    self._write_project_failures(root, dataset, now, [recovered, operation])
                    items, history = await read_processing_notices(registry, dataset, official_data=root / 'missing')
                    self.assertEqual([item['operation_id'] for item in items], [operation])
                    self.assertEqual([item['operation_id'] for item in history], [recovered])
                    self.assertNotIn('resolved_at', items[0])
                    self.assertEqual(items[0].get('failed_at'), ended if operation == failed else None)
                    self.assertEqual(history[0]['resolved_at'], ended)
            path = f'/api/v1/activity/pipeline-runs?dataset_id={dataset}&limit={ACTIVITY_PAGE_SIZE}'
            self.assertEqual(calls, [('GET', path), ('GET', f'{path}&offset={ACTIVITY_PAGE_SIZE}')] * 5)
            registry.state = {'agents': {}, 'projects': {'p': {'dataset_id': dataset}}}
            app = create_app(registry, 'm' * 40, {'a': 'a' * 40})
            async with TestClient(TestServer(app)) as client:
                response = await client.get('/v1/projects/p/processing-notices',
                                            headers={'Authorization': 'Bearer ' + 'm' * 40})
                self.assertEqual(response.status, 200)
                payload = await response.json()
                self.assertEqual([item['operation_id'] for item in payload['items']], [other_kind])
                self.assertEqual([item['operation_id'] for item in payload['resolved_items']], [recovered])

    def test_supports_pi_antigravity_traex_bridges_and_provider_mapping(self):
        with tempfile.TemporaryDirectory() as directory:
            logs = Path(directory) / 'runtime-logs'
            logs.mkdir()
            now = datetime(2026, 11, 21, 2, 0, 0, tzinfo=timezone.utc)
            cases = [
                ('pi', 'pi_bridge', 'pi_authentication_required', 'pi'),
                ('antigravity', 'antigravity_bridge', 'antigravity_account_location_unavailable', 'antigravity'),
                ('traex', 'traex_bridge', 'traex_quota_exceeded', 'traex'),
            ]
            for idx, (backend_kind, bridge_comp, bridge_code, expected_backend) in enumerate(cases):
                req_id = hex(10 + idx)[2:] * 32
                at = (now - timedelta(minutes=10 - idx)).isoformat()
                (logs / 'official_service_dev.log').write_text(json.dumps({
                    'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
                    'request_id': req_id, 'operation_id': f'11111111-1234-1234-1234-123456789ab{idx}',
                    'dataset_id': 'dataset', 'at': at, 'status': 409,
                    'backend_kind': backend_kind, 'outcome': 'failed',
                }) + '\n')
                (logs / 'agent_completion_endpoint.log').write_text(json.dumps({
                    'schema_version': 1, 'event': 'request_finished', 'component': bridge_comp,
                    'request_id': req_id, 'backend_kind': backend_kind, 'outcome': bridge_code,
                }) + '\n')
                items = processing_notices(logs, 'dataset', now=now)
                self.assertEqual(len(items), 1)
                self.assertEqual(items[0]['code'], bridge_code)
                self.assertEqual(items[0]['backend'], expected_backend)

            # Direct OpenAI/Anthropic provider stays backend='provider'.
            (logs / 'official_service_dev.log').write_text(json.dumps({
                'schema_version': 1, 'event': 'request_finished', 'component': 'generation_http',
                'request_id': 'e' * 32, 'operation_id': '22222222-1234-1234-1234-123456789abc',
                'dataset_id': 'dataset', 'at': (now - timedelta(minutes=1)).isoformat(),
                'status': 502, 'backend_kind': 'openai', 'outcome': 'failed',
            }) + '\n')
            items = processing_notices(logs, 'dataset', now=now)
            self.assertEqual((items[0]['code'], items[0]['backend']), ('model_request_failed', 'provider'))
