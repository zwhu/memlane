import json
from pathlib import Path
import tempfile
import types
import unittest
from datetime import datetime, timedelta, timezone
from aiohttp.test_utils import TestClient, TestServer
from project_control_server import create_app
from processing_notices import processing_notices


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
                self.assertEqual((await response.json())['items'],items)

    def test_reads_agent_completion_log_and_attaches_recovery_time(self):
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
            with_recovery = processing_notices(logs, 'dataset', now=now)
            self.assertEqual(len(with_recovery), 1)
            self.assertEqual(with_recovery[0]['code'], 'codex_connection_failed')
            self.assertEqual(with_recovery[0]['recovered_at'], ok_after)

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
