#!/usr/bin/env python3
"""Installed Cognee task-pipeline adapter contract; loopback fixtures, no model."""
import asyncio
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.metadata
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
from types import SimpleNamespace
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'engine/python'))


def main():
    if os.environ.get('MEMLANE_DISK_TASK') != '1': raise SystemExit('managed_task_required')
    if importlib.metadata.version('cognee') != '1.6.7':raise SystemExit('installed_cognee_version_changed')
    with tempfile.TemporaryDirectory(prefix='memlane-cognee-adapter-contract-') as temporary:
        base=Path(temporary)
        for name in ('system','data','cache','logs','home'):(base/name).mkdir()
        os.environ.update(SYSTEM_ROOT_DIRECTORY=str(base/'system'),DATA_ROOT_DIRECTORY=str(base/'data'),CACHE_ROOT_DIRECTORY=str(base/'cache'),
                          LOGS_ROOT_DIRECTORY=str(base/'logs'),COGNEE_LOGS_DIR=str(base/'logs'),HOME=str(base/'home'),
                          PYTHON_DOTENV_DISABLED='1',HF_HUB_OFFLINE='1',TELEMETRY_DISABLED='true',COGNEE_TRACING_ENABLED='false',
                          STRUCTURED_OUTPUT_FRAMEWORK='litellm_native',LITELLM_LOCAL_MODEL_COST_MAP='true')
        from pydantic import BaseModel
        from cognee.modules.pipelines import Task
        from cognee.modules.pipelines.operations.run_tasks_base import run_tasks_base
        from cognee.infrastructure.llm.config import LLMConfig
        from cognee.context_global_variables import llm_config
        from cognee.infrastructure.llm.LLMGateway import LLMGateway
        from cognee_processing_adapter import install
        install();install()
        class Output(BaseModel):marker:str
        seen=[]
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_POST(self):
                payload=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                auth=self.headers.get('Authorization');seen.append((self.server.marker,auth,payload))
                if self.server.marker=='failure':status,body=503,{'raw':'DO-NOT-PERSIST-SYNTHETIC-PROVIDER-ERROR'}
                else:
                    content = {'text': 'test'} if self.server.marker == 'string' else {'text': 7} if self.server.marker == 'invalid_string' else {'marker': self.server.marker}
                    status,body=200,{'choices':[{'finish_reason':'stop','message':{'role':'assistant','content':json.dumps(content)}}]}
                raw=json.dumps(body).encode();self.send_response(status);self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
        servers=[]
        for marker in ('first','second','failure','string','invalid_string'):
            server=ThreadingHTTPServer(('127.0.0.1',0),Handler);server.marker=marker
            thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();servers.append((server,thread))
        user=SimpleNamespace(id=uuid4(),tenant_id=None,email='synthetic@example.invalid')
        async def extract(data):
            return await LLMGateway.acreate_structured_output(text_input='synthetic',system_prompt='synthetic schema fixture',response_model=Output)
        async def one(index):
            server=servers[index][0]
            config=LLMConfig(_env_prefix='MEMLANE_SUBMISSION_ISOLATED_',_env_file=None,llm_provider='custom',llm_model='openai/memlane-processing',
                             llm_endpoint='http://127.0.0.1:'+str(server.server_port)+'/v1',llm_api_key='contract-secret-'+server.marker,llm_args={})
            token=llm_config.set(config)
            try:
                # Actual installed official Task/handle_task executor invokes
                # LLMGateway, which dynamically selects the Memlane dispatcher.
                results=[result async for result in run_tasks_base([Task(extract)],'synthetic material',user)]
                return results
            finally:llm_config.reset(token)
        async def prove():
            results=await asyncio.gather(one(0),one(1))
            assert results[0][0].marker=='first' and results[1][0].marker=='second', 'context_crossed'
            before=len(seen)
            try:await one(2)
            except Exception as error:
                assert 'contract-secret' not in repr(error) and 'DO-NOT-PERSIST' not in repr(error), 'unsafe_error'
            else:raise AssertionError('provider_failure_accepted')
            assert len(seen)==before+1,'implicit_paid_retry'
            from cognee_processing_adapter import ProcessingAdapter
            from cognee.infrastructure.llm.utils import test_llm_connection
            server=servers[3][0]
            config=LLMConfig(_env_prefix='MEMLANE_STRING_ISOLATED_',_env_file=None,llm_provider='custom',llm_model='openai/memlane-processing',
                            llm_endpoint='http://127.0.0.1:'+str(server.server_port)+'/v1',llm_api_key='contract-secret-string',llm_args={})
            token=llm_config.set(config)
            try:
                await test_llm_connection()
                result=await LLMGateway.acreate_structured_output('synthetic','return test',str)
                assert result == 'test' and type(result) is str,'sdk_string_contract_broken'
            finally:llm_config.reset(token)
            bad=ProcessingAdapter('http://127.0.0.1:'+str(servers[4][0].server_port)+'/v1','contract-secret-invalid_string')
            try:await bad.acreate_structured_output('synthetic','return text',str)
            except ValueError as error:assert str(error)=='material_model_response_schema_invalid'
            else:raise AssertionError('non_string_output_accepted')
            # Real ASGI cancellation waits for cleanup, and a tombstone defeats
            # a delayed/replayed body for that same explicit operation.
            import hashlib
            import httpx
            from fastapi import FastAPI
            import cognee_submission
            from unittest.mock import patch
            app=FastAPI();instance=str(uuid4())
            async def authenticated():return user
            cognee_submission.install(app,instance,authenticated)
            started,stopped=asyncio.Event(),asyncio.Event()
            async def blocked(*args):
                started.set()
                try:await asyncio.sleep(60)
                finally:stopped.set()
            material={k:str(uuid4()) for k in ('library','operation','dataset','project','memory')}
            material.update(instance=instance,revision=1,origin='knowledge',text='synthetic')
            material['digest']=hashlib.sha256(b'synthetic').hexdigest()
            material['processing']={'revision':{'config_digest':'b'*64},'dependency_digest':'a'*64}
            envelope={'material':material,'gateway':{'endpoint':'http://127.0.0.1:12345/v1','token':'synthetic-cancel-capability'}}
            with patch.object(cognee_submission,'submit_official',blocked):
                async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app),base_url='http://synthetic') as client:
                    original=asyncio.create_task(client.post('/api/v1/memlane/material-submission',json=envelope))
                    await asyncio.wait_for(started.wait(),5)
                    response=await client.post('/api/v1/memlane/material-submission/cancel',json={k:material[k] for k in ('instance','library','operation')})
                    assert response.status_code==200 and response.json()['state']=='stopped' and stopped.is_set(),'cancel_ack_before_cleanup'
                    await asyncio.gather(original,return_exceptions=True)
                    replay=await client.post('/api/v1/memlane/material-submission',json=envelope)
                    assert replay.status_code==409,'cancelled_operation_restarted'

        try:asyncio.run(prove())
        finally:
            for server,thread in servers:server.shutdown();server.server_close();thread.join(timeout=5)
        assert len(seen)==6
        for marker,auth,payload in seen:
            assert auth=='Bearer contract-secret-'+marker,'context_token_crossed'
            assert payload['model']=='memlane-processing' and payload['response_format']['type']=='json_schema' and payload['stream'] is False
            assert 'tools' not in payload
        for path in base.rglob('*'):
            if path.is_file() and path.stat().st_size<=8*1024*1024:
                raw=path.read_bytes()
                assert b'contract-secret-' not in raw and b'DO-NOT-PERSIST-SYNTHETIC-PROVIDER-ERROR' not in raw,'capability_persisted'
        print(json.dumps(dict(status='passed',installed_cognee='1.6.7',official_task_pipeline=True,parallel_contexts=2,loopback_calls=len(seen),failure_calls=1,cancellation_ack=True,cancel_tombstone=True,model_called=False,full_graph_extraction=False,temporary_root_removed_on_exit=True)))


if __name__=='__main__':main()
