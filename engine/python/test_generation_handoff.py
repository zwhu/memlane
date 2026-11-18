import asyncio
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock

from generation_capacity import EndpointCapacity
from generation_config import GenerationStore
from generation_handoff import GenerationHandoff
from generation_handoff_runtime import GenerationHandoffRuntime, scope
from project_policy import PolicyError


class RuntimeHandoffTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.gate = EndpointCapacity('http://127.0.0.1:12345', 1)
        self.pending = set()
        self.candidate = object()
        self.factory = AsyncMock(return_value=(self.candidate,None))
        self.runtime = GenerationHandoffRuntime({'generation_file':str(Path(self.tmp.name)/'generation.json')},
            {'mode':'provider'}, {}, self.gate, factory=self.factory,
            background=lambda:{t for t in self.pending if not t.done()},stop_timeout=.02,
            operation_for=lambda task:SimpleNamespace(operation_id=__import__('uuid').uuid4(),user_id=None),verify_terminal=AsyncMock(return_value=True))
    async def asyncTearDown(self):
        for task in self.pending:
            task.cancel()
        await asyncio.gather(*self.pending,return_exceptions=True)
        await self.runtime.close()
        self.tmp.cleanup()

    async def test_cancel_finalization_before_switch_and_no_duplicate_completed_work(self):
        writes = ['already-completed']
        finalized = asyncio.Event()
        async def work():
            try:
                await asyncio.Event().wait()
            finally:
                # The engine's terminal write must finish before application.
                await asyncio.sleep(0)
                writes.append('cancelled-watermark-retained');finalized.set()
        work.__qualname__ = '_start_background_run.<locals>.run'
        token=scope.set({'digest':'old','sessions':['original-session'],'credential_sha256':'source'})
        task=asyncio.create_task(work());scope.reset(token)
        self.pending.add(task);await asyncio.sleep(0)
        result=await self.runtime.apply('switch',{'id':'new','revision':1},'digest',
            {'mode':'provider','provider':'openai'},{'LLM_ENDPOINT':'https://example.invalid/v1'}, ['source'])
        self.assertEqual(result['status'],'applied')
        self.assertTrue(finalized.is_set())
        self.assertEqual(writes,['already-completed','cancelled-watermark-retained'])
        self.assertEqual(result['cancelled_sessions'],['original-session'])
        self.assertIs(self.runtime.config,self.candidate)
        self.assertEqual(await self.runtime.apply('switch',{'id':'new','revision':1},'digest',{},{}),result)
        self.factory.assert_awaited_once()

    async def test_unowned_background_work_blocks_switch_without_cancelling_it(self):
        task=asyncio.create_task(asyncio.Event().wait());self.pending.add(task)
        result=await self.runtime.apply('blocked',{'id':'new','revision':1},'digest',
            {'mode':'provider'},{'LLM_ENDPOINT':'https://example.invalid/v1'}, ['source'])
        self.assertEqual(result['reason'],'generation_stop_unconfirmed')
        self.assertFalse(task.done())
        self.assertIsNone(self.runtime.config)

    async def test_restart_retains_unknown_operation_instead_of_silently_repeating(self):
        self.runtime.rows['lost']={'status':'stopping','reference':{'id':'new','revision':1},'digest':'d'}
        self.runtime.save()
        restored=GenerationHandoffRuntime(self.runtime.runtime,{'mode':'provider'},{},self.gate,factory=self.factory)
        self.assertEqual(restored.get('lost')['status'],'unknown')
        self.assertEqual((await restored.apply('lost',{'id':'new','revision':1},'d',{},{}))['status'],'unknown')
        self.factory.assert_not_awaited()


class HandoffControllerTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.path=Path(self.tmp.name)
        self.store=GenerationStore(self.path/'generation.json','fixed',legacy_model='old')
        self.ref=self.store.put('new',{'mode':'provider','provider':'ollama','model':'new',
            'endpoint':'http://127.0.0.1:11434'},0,'save')
        self.ref={k:self.ref[k] for k in ('id','revision')}
        row=self.store.resolve(self.ref)
        self.store.state['validations']['test']={'digest':row['digest'],'status':'passed',
            'plain_passed':True,'schema_passed':True,'quality_passed':True}
        self.store.native_schema_evidence=lambda row: None
        self.incremental=SimpleNamespace(state={'sessions':{
            'cancelled':{'session_id':'a','status':'submitted','submitted_counts':[1,0]},
            'unknown':{'session_id':'b','status':'unknown','submitted_counts':[1,0]},
            'completed':{'session_id':'c','status':'submitted','submitted_counts':[1,0]}}},save=lambda:None)
        import hashlib
        from project_control import private_write
        private_write(self.path/'agent-key',b'original-agent-key')
        registry=SimpleNamespace(state={'agents':{'agent':{'key_file':str(self.path/'agent-key'),'installation':{'verified':True}}},
            'sessions':{json.dumps(['agent',host]):'project' for host in ('cancelled','unknown','completed')}})
        self.keys={host:hashlib.sha256(json.dumps(['agent',host]).encode()).hexdigest() for host in ('cancelled','unknown','completed')}
        self.incremental.state['sessions']={self.keys[k]:v for k,v in self.incremental.state['sessions'].items()}
        self.source_hash=hashlib.sha256(b'original-agent-key').hexdigest()
        self.control=GenerationHandoff(self.store,registry,{},incremental=self.incremental)
    async def asyncTearDown(self):
        await self.control.close();self.store.close();self.tmp.cleanup()

    async def test_connected_agent_selection_does_not_require_model_validation(self):
        from unittest.mock import patch
        ref = self.store.put('connected-agent', {'mode':'agent_bridge', 'bridge_kind':'codex', 'model':'native'}, self.store.state['revision'], 'save-agent')
        ref={key:ref[key] for key in ('id','revision')}
        self.store.environment=lambda reference,runtime: {}
        self.control.service=AsyncMock(return_value={'status':'not_applied','reference':ref,
            'digest':self.store.resolve(ref)['digest'],'reason':'test_no_apply'})
        with patch('agent_bridge_runtime.require_connected_bridge') as check:
            self.control.start(ref,self.store.state['revision'],'choose-agent')
            check.assert_called_once_with(self.control.registry,self.control.runtime,self.store.resolve(ref)['profile'])
        await self.control.task
        self.assertEqual(self.store.state['handoffs']['choose-agent']['status'],'not_applied')

    async def test_only_confirmed_cancelled_session_is_resumed_under_new_profile(self):
        self.control.service=AsyncMock(return_value={'status':'applied','reference':self.ref,
            'digest':self.store.resolve(self.ref)['digest'],'cancelled_sources':[{'session_id':'a','credential_sha256':self.source_hash}]})
        self.control.start(self.ref,self.store.state['revision'],'switch')
        await self.control.task
        self.assertEqual(self.store.state['active'],self.ref)
        self.assertEqual(self.incremental.state['sessions'][self.keys['cancelled']]['status'],'watching')
        self.assertNotIn('submitted_counts',self.incremental.state['sessions'][self.keys['cancelled']])
        self.assertEqual(self.incremental.state['sessions'][self.keys['unknown']]['status'],'unknown')
        self.assertEqual(self.incremental.state['sessions'][self.keys['completed']]['submitted_counts'],[1,0])
        self.control.start(self.ref,-1,'switch')
        self.control.service.assert_awaited_once()

    async def test_lost_reply_retains_original_operation_and_readonly_review(self):
        self.control.service=AsyncMock(side_effect=TimeoutError)
        self.control.start(self.ref,self.store.state['revision'],'lost');await self.control.task
        self.assertEqual(self.control.view('lost')['handoff']['status'],'unknown')
        self.assertEqual(self.store.state['active']['id'],'existing-codex')
        with self.assertRaisesRegex(PolicyError,'requires_review'):
            self.control.start(self.ref,self.store.state['revision'],'another')
        self.control.service=AsyncMock(return_value={'status':'applied','reference':self.ref,
            'digest':self.store.resolve(self.ref)['digest'],'cancelled_sessions':[]})
        await self.control.review('lost')
        self.assertEqual(self.control.service.call_args.args,('GET','lost'))
        self.assertEqual(self.store.state['active'],self.ref)


class OfficialSDKContextTests(unittest.TestCase):
    def test_real_background_launcher_inherits_frozen_llm_config_and_finishes_cancelled_run(self):
        import os, subprocess, sys
        root=Path(__file__).resolve().parents[2]
        manifest=json.loads((root/'engine/coding-memory-components.json').read_text())['service']
        sdk=root/'.cache/coding-memory-components'/('service-'+manifest['commit'])
        script=r'''
import asyncio, importlib
from unittest.mock import AsyncMock
from types import SimpleNamespace
from pathlib import Path
from cognee.context_global_variables import llm_config
from cognee.infrastructure.llm.config import LLMConfig, get_llm_context_config
from generation_handoff_runtime import improve_task, scope
module=importlib.import_module('cognee.api.v1.improve.improve')
module.finish_operation=AsyncMock()
async def scenario():
    original=LLMConfig(llm_model='old',llm_api_key='test',llm_endpoint='http://127.0.0.1:18999')
    token=llm_config.set(original); marker=scope.set({'digest':'old','sessions':['session']})
    entered=asyncio.Event(); seen=[]
    async def execute():
        entered.set(); await asyncio.Event().wait()
    task=module._start_background_run(execute,SimpleNamespace(error=None,finished=False),None)
    llm_config.reset(token);scope.reset(marker)
    await entered.wait()
    assert improve_task(task),task.get_coro().__qualname__
    assert task.get_context().get(llm_config) is original
    token=llm_config.set(LLMConfig(llm_model='new',llm_api_key='test',llm_endpoint='http://127.0.0.1:18999'))
    async def next_request():seen.append(get_llm_context_config().llm_model)
    await asyncio.create_task(next_request());llm_config.reset(token)
    assert seen==['new'] and task.get_context().get(llm_config).llm_model=='old'
    task.cancel();await asyncio.gather(task,return_exceptions=True)
    module.finish_operation.assert_awaited_once()
    assert isinstance(module.finish_operation.call_args.kwargs['error'],asyncio.CancelledError)
asyncio.run(scenario())


# Exercise the installed middleware/HTTP boundary with the REAL SDK launcher.
from fastapi import FastAPI
import httpx, os
from generation_handoff_runtime import install_generation_handoff
from cognee.modules.operations import record_operation
from cognee.infrastructure.databases.relational import create_db_and_tables
from generation_capacity import EndpointCapacity
from project_control import private_write
async def http_scenario():
    root=Path.cwd();private_write(root/'manager',b'm'*40)
    await create_db_and_tables()
    # Use the real terminal SQL writer for the HTTP/cancellation scenario.
    module.finish_operation=importlib.import_module('cognee.modules.operations').finish_operation
    app=FastAPI()
    controller=install_generation_handoff(app,{'generation_file':str(root/'generation.json'),
        'generation_reference':{'id':'old','revision':1},'project_control':{'management_token_file':str(root/'manager')}},
        {'mode':'provider'},{'_digest':'old'},EndpointCapacity('http://127.0.0.1:18999',1))
    entered=asyncio.Event()
    @app.post('/api/v1/improve')
    async def begin():
        async def execute():entered.set();await asyncio.Event().wait()
        async with record_operation('improve',session_ids=['real-sdk-session'],background=True) as operation:
            operation.defer_close()
            module._start_background_run(execute,SimpleNamespace(error=None,finished=False),operation)
        return {'status':'running'}
    @app.get('/api/v1/current-model')
    async def current_model():return {'model':get_llm_context_config().llm_model}
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app),base_url='http://fixture') as client:
        assert (await client.post('/api/v1/improve',json={'sessionIds':['real-sdk-session']},headers={'X-Api-Key':'test-agent-key'})).status_code==200
        await entered.wait()
        import hashlib
        body={'source_credentials':[hashlib.sha256(b'test-agent-key').hexdigest()],'reference':{'id':'new','revision':1},'digest':'new-digest',
            'profile':{'mode':'provider','provider':'custom','model':'new','endpoint':'http://127.0.0.1:18998',
                       'credential_ref':'synthetic','credential_version':'one'},
            'environment':{'LLM_PROVIDER':'openai','LLM_MODEL':'openai/new','LLM_ENDPOINT':'http://127.0.0.1:18998',
                           'LLM_API_KEY':'synthetic-private','STRUCTURED_OUTPUT_FRAMEWORK':'litellm_native'}}
        path='/api/v1/memlane/generation/handoffs/original-switch'
        assert (await client.put(path,json=body)).status_code==403
        headers={'X-Memlane-Management':'m'*40}
        response=await client.put(path,json=body,headers=headers)
        assert response.status_code==200,response.text
        for _ in range(100):
            receipt=(await client.get(path,headers=headers)).json()
            if receipt['status'] not in ('preparing','stopping','applying'):break
            await asyncio.sleep(.01)
        assert receipt['status']=='applied',receipt
        assert 'llm_temperature' not in controller.config.model_fields_set
        assert 'temperature' not in (controller.config.llm_args or {})
        assert receipt['cancelled_sessions']==['real-sdk-session'],receipt
        assert (await client.get('/api/v1/current-model')).json()=={'model':'openai/new'}
        assert 'synthetic-private' not in (root/'generation-handoff-runtime.json').read_text()
        # The official writer can swallow persistence failures. A cancelled
        # Python task without its SQL terminal row must block the next model.
        module.finish_operation=AsyncMock()
        entered.clear()
        await client.post('/api/v1/improve',json={'sessionIds':['real-sdk-session']},headers={'X-Api-Key':'test-agent-key'})
        await entered.wait()
        body['reference']={'id':'third','revision':1};body['digest']='third-digest'
        body['environment']['LLM_MODEL']='openai/third'
        blocked_path='/api/v1/memlane/generation/handoffs/missing-terminal'
        await client.put(blocked_path,json=body,headers=headers)
        for _ in range(100):
            blocked=(await client.get(blocked_path,headers=headers)).json()
            if blocked['status'] not in ('preparing','stopping','applying'):break
            await asyncio.sleep(.01)
        assert blocked['reason']=='generation_stop_unconfirmed',blocked
        assert (await client.get('/api/v1/current-model')).json()=={'model':'openai/new'}
        import socket
        with socket.socket() as reserved:
            reserved.bind(('127.0.0.1',0));closed_port=reserved.getsockname()[1]
        body['profile']={'mode':'provider','provider':'ollama','model':'unavailable',
                         'endpoint':'http://127.0.0.1:'+str(closed_port)}
        body['environment']={};body['digest']='unavailable'
        dead='/api/v1/memlane/generation/handoffs/unavailable'
        await client.put(dead,json=body,headers=headers)
        for _ in range(100):
            rejected=(await client.get(dead,headers=headers)).json()
            if rejected['status'] not in ('preparing','stopping','applying'):break
            await asyncio.sleep(.01)
        assert rejected['status']=='not_applied' and rejected['reason']=='generation_provider_unavailable',rejected
        assert (await client.get('/api/v1/current-model')).json()=={'model':'openai/new'}
    await controller.close()
asyncio.run(http_scenario())

'''
        with tempfile.TemporaryDirectory() as temporary:
            home=Path(temporary)
            env={k:v for k,v in os.environ.items() if k in ('PATH','LANG','LC_ALL','TMPDIR')}
            env.update(HOME=str(home),PYTHONPATH=os.pathsep.join([str(sdk),str(root/'engine/python')]),
                SYSTEM_ROOT_DIRECTORY=str(home/'system'),DATA_ROOT_DIRECTORY=str(home/'data'),
                LOGS_ROOT_DIRECTORY=str(home/'logs'),COGNEE_LOGS_DIR=str(home/'logs'),
                TELEMETRY_DISABLED='true',PYTHON_DOTENV_DISABLED='1',LITELLM_LOCAL_MODEL_COST_MAP='true')
            result=subprocess.run([sys.executable,'-c',script],cwd=home,env=env,capture_output=True,text=True,timeout=35)
            self.assertEqual(result.returncode,0,result.stderr[-3500:])
