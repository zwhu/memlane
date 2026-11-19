import json
from pathlib import Path
import tempfile
import types
import unittest
from datetime import datetime, timezone
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
