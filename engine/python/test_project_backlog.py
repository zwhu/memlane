import asyncio
import copy
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch
import uuid

from aiohttp.test_utils import TestClient, TestServer
from project_backlog import Backlog, attach_backlog
from project_control_server import create_app
from project_policy import PolicyError


class BacklogTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.policy = {'ready': True, 'policy': {'permissions': ['read','write','delete'], 'can_auto_process': True}}
        self.posts=[]
        async def request(method,path,body=None,**kw):
            self.posts.append((method,path,body,kw))
            return {'status':'running'}
        self.registry=types.SimpleNamespace(path=self.root/'registry.json',
            state={'agents':{'a':{'installation':{},'key_file':'child-private-key'}},
                   'projects':{'p':{'id':'p','dataset_id':'ds'}},
                   'sessions':{json.dumps(['a','host']):'p'}},
            official=types.SimpleNamespace(request=request),policy=lambda *a:copy.deepcopy(self.policy))
        self.registry.state['agents']['a']['installation']={'plugin_dir':'/synthetic','env_file':'/synthetic/env','home':'/synthetic'}
        self.verify=patch('project_backlog.verify_installation')
        self.verify.start()
        self.count=0
        self.gate=asyncio.Event()
        async def worker(row,target,action,update):
            if action=='review':return {'session_id':'native-session','tokens':['a'*64]*32,'unknown':2}
            await self.gate.wait()
            self.count+=1
            result={'status':'completed','saved':32,'handled':0,'isolated':0,'remaining':0}
            update(result)
            return result
        self.backlog=Backlog(self.registry,{},worker=worker)

    async def asyncTearDown(self):
        self.gate.set()
        await self.backlog.close()
        self.verify.stop()
        self.tmp.cleanup()

    async def test_owned_job_survives_frontend_and_repeated_start_does_not_duplicate(self):
        operation=str(uuid.uuid4())
        original=self.backlog.start('p',operation)
        self.assertEqual(self.backlog.start('p',operation)['operation_id'],operation)
        # A new view with a new request also discovers the existing running job.
        self.assertEqual(self.backlog.start('p',str(uuid.uuid4()))['operation_id'],operation)
        await asyncio.sleep(.01)
        self.assertEqual(self.backlog.get('p')['job']['status'],'reviewing')
        self.gate.set();await self.backlog.task
        result=self.backlog.get('p')['job']
        self.assertEqual((result['status'],result['saved'],result['isolated']),('completed',32,2))
        self.assertEqual(self.count,1)
        self.assertEqual(len(self.posts),1)
        self.assertEqual(self.posts[0][2],{'dataset_id':'ds','session_ids':['native-session'],'run_in_background':True})
        self.backlog.start('p',operation)
        self.assertEqual(self.count,1)
        self.assertNotIn('targets',result)

    async def test_automatic_save_continues_new_tail_without_ui_and_keeps_unknown_isolated(self):
        self.policy['policy']['can_capture'] = True
        pending = ['a'*64]
        async def worker(row, target, action, update):
            if action == 'review':
                return {'session_id': 'native-session', 'tokens': list(pending), 'unknown': 20}
            saved = len(pending)
            pending.clear()
            result = {'status':'completed','saved':saved,'handled':0,'isolated':0,'remaining':0}
            update(result)
            return result
        self.backlog.worker = worker
        await self.backlog.automatic_tick()
        await self.backlog.task
        first = self.backlog.get('p')['job']
        self.assertEqual((first['status'], first['saved'], first['isolated']), ('watching', 1, 20))
        self.assertEqual(self.posts, [], 'saving a tiny tail must not run a model immediately')
        pending.extend(['b'*64, 'c'*64])
        await self.backlog.automatic_tick()
        await self.backlog.task
        second = self.backlog.get('p')['job']
        self.assertEqual(second['operation_id'], first['operation_id'])
        self.assertEqual(second['saved'], 3)
        self.assertEqual(second['isolated'], 20)
        for target in self.backlog.jobs[first['operation_id']]['pending_processing'].values():
            target['due_at'] = 0
        await self.backlog.automatic_tick()
        self.assertEqual(len(self.posts), 1)
        await self.backlog.automatic_tick()
        self.assertEqual(len(self.posts), 1)
        self.backlog.stop('p', first['operation_id'])
        pending.append('d'*64)
        await self.backlog.automatic_tick()
        self.assertEqual(pending, ['d'*64], 'explicit stop must persist')

    async def test_automatic_save_respects_disabled_capture_retirement_and_unknown_processing(self):
        self.gate.set()
        self.policy['policy']['can_capture'] = False
        await self.backlog.automatic_tick()
        self.assertIsNone(self.backlog.task)
        self.policy['policy']['can_capture'] = True
        self.registry.state['agent_retirements'] = {'a': {'status':'unconfirmed'}}
        await self.backlog.automatic_tick()
        self.assertIsNone(self.backlog.task)
        self.registry.state['agent_retirements'] = {}
        await self.backlog.automatic_tick()
        await self.backlog.task
        row = next(r for r in self.backlog.jobs.values() if r.get('automatic'))
        async def fail(*args, **kwargs):
            self.posts.append(1)
            raise PolicyError('official_result_unknown',503)
        self.registry.official.request = fail
        for target in row['pending_processing'].values(): target['due_at'] = 0
        await self.backlog.automatic_tick()
        self.assertEqual(self.posts, [1])
        for _ in range(2):
            await self.backlog.automatic_tick()
            if self.backlog.task: await self.backlog.task
        self.assertEqual(self.posts, [1], 'unknown model submit was repeated')

    async def test_automatic_restart_reviews_fresh_buffer_but_user_pause_persists(self):
        self.policy['policy']['can_capture'] = True
        self.gate.set()
        await self.backlog.automatic_tick()
        await self.backlog.task
        row = next(r for r in self.backlog.jobs.values() if r.get('automatic'))
        row.update(status='running', owner_shutdown=True)
        self.backlog.persist()
        restarted = Backlog(self.registry, {}, worker=self.backlog.worker)
        self.assertIsNone(restarted.task, 'constructor replayed an old token list')
        self.assertEqual(restarted.jobs[row['operation_id']]['status'], 'watching')
        await restarted.automatic_tick()
        await restarted.task
        restarted.stop('p', row['operation_id'])
        paused = Backlog(self.registry, {}, worker=self.backlog.worker)
        await paused.automatic_tick()
        self.assertIsNone(paused.task, 'restart bypassed user pause')
        await restarted.close()
        await paused.close()

    async def test_restart_does_not_replay_and_completed_lookup_is_chronological(self):
        op=str(uuid.uuid4());self.backlog.start('p',op)
        await asyncio.sleep(.01)
        # Simulate a durable in-progress journal in a fresh owner, no auto task.
        restarted=Backlog(self.registry,{},worker=self.backlog.worker)
        self.assertEqual(restarted.get('p')['job']['status'],'interrupted')
        self.assertIsNone(restarted.task)
        self.assertTrue(restarted.cancel_file(restarted.jobs[op]).exists())
        self.gate.set();await self.backlog.task
        await restarted.close()

    async def test_disabled_processing_and_unknown_submission_never_retry(self):
        self.policy['policy']['can_auto_process']=False
        self.gate.set();op=str(uuid.uuid4());self.backlog.start('p',op);await self.backlog.task
        self.assertEqual(self.posts,[])
        self.assertEqual(self.backlog.get('p')['job']['processing_disabled'],1)
        self.policy['policy']['can_auto_process']=True
        async def fail(*a,**kw):
            self.posts.append(1);raise PolicyError('official_result_unknown',503)
        self.registry.official.request=fail
        op=str(uuid.uuid4());self.backlog.start('p',op);await self.backlog.task
        self.assertEqual(self.backlog.get('p')['job']['processing_failed'],1)
        for _ in range(3):self.backlog.get('p');self.backlog.start('p',op)
        self.assertEqual(self.posts,[1])

    async def test_manager_only_http_and_stop_does_not_claim_inflight_cancelled(self):
        app=create_app(self.registry,'m'*40,{'a':'a'*40})
        with patch('project_backlog.Backlog',return_value=self.backlog):
            attach_backlog(app,self.registry,{})
        client=TestClient(TestServer(app));await client.start_server()
        path='/v1/projects/p/backlog'
        manager={'Authorization':'Bearer '+'m'*40}
        try:
            self.assertEqual((await client.get(path)).status,401)
            self.assertEqual((await client.post(path,headers={'Authorization':'Bearer '+'a'*40},json={'operation_id':str(uuid.uuid4())})).status,403)
            self.assertEqual((await client.post(path,headers=manager,json={'operation_id':'../not-a-job'})).status,400)
            operation=str(uuid.uuid4())
            result=await client.post(path,headers=manager,json={'operation_id':operation})
            self.assertEqual(result.status,200)
            await asyncio.sleep(.01)
            result=await client.post(path+'/stop',headers=manager,json={'operation_id':operation})
            self.assertEqual((await result.json())['job']['status'],'stopping')
            self.assertFalse(self.backlog.task.done())
            self.gate.set();await self.backlog.task
            result=await client.get(path,headers=manager)
            self.assertEqual((await result.json())['job']['status'],'stopped')
            self.assertEqual(self.posts,[])
        finally:
            self.gate.set();await client.close()


if __name__=='__main__':unittest.main()
