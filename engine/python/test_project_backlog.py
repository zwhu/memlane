import asyncio
import copy
import hashlib
import json
import os
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
    async def test_unconfirmed_review_isolated_before_healthy_session_save(self):
        # A legacy host can return a paused record rather than a snapshot.
        # It must not prevent later healthy hosts from reaching the sender.
        self.registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('old-traex', 'healthy-codex')}
        actions = []
        async def worker(row, target, action, update):
            actions.append((target['host'], action))
            if action == 'review':
                if target['host'] == 'old-traex':
                    return {'status': 'paused', 'reason': 'policy_unconfirmed'}
                return {'session_id': 'native-codex', 'tokens': ['b'*64], 'unknown': 20}
            if action == 'process': return {'status': 'disabled'}
            result = {'status': 'completed', 'saved': 1, 'handled': 0, 'isolated': 0, 'remaining': 0}
            update(result)
            return result
        self.backlog.worker = worker
        operation = str(uuid.uuid4())
        self.backlog.start('p', operation)
        await self.backlog.task
        result = self.backlog.get('p')['job']
        self.assertEqual(result['status'], 'completed')
        self.assertEqual(result['review_failed'], 1)
        self.assertEqual(result['saved'], 1)
        self.assertEqual(result['isolated'], 20)
        self.assertNotIn(('old-traex', 'save'), actions)
        self.assertIn(('healthy-codex', 'save'), actions)
        self.assertEqual(self.backlog.jobs[operation]['targets'][0]['review_reason'], 'policy_unconfirmed')

    async def test_review_exception_and_invalid_token_never_send_bad_target(self):
        self.registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('missing-map', 'invalid-token', 'healthy')}
        sent = []
        async def worker(row, target, action, update):
            if action == 'review':
                if target['host'] == 'missing-map': raise PolicyError('official_session_unconfirmed', 503)
                return {'session_id': target['host'], 'tokens': [None] if target['host'] == 'invalid-token' else ['b'*64], 'unknown': 0}
            if action == 'process': return {'status': 'disabled'}
            sent.append(target['host'])
            result = {'status': 'completed', 'saved': 1, 'handled': 0, 'isolated': 0, 'remaining': 0}
            update(result); return result
        self.backlog.worker = worker
        self.backlog.start('p', str(uuid.uuid4()))
        await self.backlog.task
        self.assertEqual(sent, ['healthy'])
        self.assertEqual(self.backlog.get('p')['job']['review_failed'], 2)

    async def test_numbered_retry_backoff_keeps_operation_and_respects_explicit_pause(self):
        self.policy['policy']['can_capture'] = True
        calls = []
        async def worker(row, target, action, update):
            calls.append(action)
            if action == 'review':
                return {'session_id':'native-session','tokens':['b'*64],'unknown':1,'unknown_signature':'numbered'}
            if action == 'save':
                result = {'status':'paused','reason':'capture_retry_pending','saved':0,'handled':0,'isolated':0,'remaining':1}
                update(result)
                return result
            return {'status':'disabled'}
        self.backlog.worker = worker
        await self.backlog.automatic_tick()
        await self.backlog.task
        row = next(iter(self.backlog.jobs.values()))
        original_id = row['operation_id']
        self.assertEqual(row['reason'],'partial_save_deferred')
        self.assertEqual(row['isolated'] if 'isolated' in row else row['targets'][0]['isolated'],0)
        retry_key = json.dumps(['a', 'host'])
        self.assertEqual(row['save_retries'][retry_key]['attempts'],1)
        before = len(calls)
        await self.backlog.automatic_tick()
        self.assertEqual(len(calls),before,'ignored retry backoff')
        row['save_retries'][retry_key]['after'] = 0
        await self.backlog.automatic_tick()
        await self.backlog.task
        row = self.backlog.jobs[original_id]
        self.assertEqual(len(self.backlog.jobs),1,'changed operation to bypass unknown')
        self.assertEqual(row['save_retries'][retry_key]['attempts'],2)
        self.backlog.stop('p',original_id)
        before = len(calls)
        await self.backlog.automatic_tick()
        self.assertEqual(len(calls),before,'explicit user pause resumed automatically')

    async def test_check_api_is_manager_only_and_never_starts_a_save(self):
        app=create_app(self.registry,'m'*40,{'a':'a'*40})
        managed=attach_backlog(app,self.registry,{})
        app.on_startup.clear()
        release=asyncio.Event()
        calls=[]
        async def worker(row,target,action,update):
            calls.append(action)
            await release.wait()
            return {'status':'unconfirmed','unknown':20,'matched':0,'reason':'original_receipt_unavailable'}
        managed.worker=worker
        client=TestClient(TestServer(app));await client.start_server()
        try:
            payload={'agent_id':'a','host':'host'}
            response=await client.post('/v1/projects/p/backlog/check',json=payload,headers={'Authorization':'Bearer '+'a'*40})
            self.assertEqual(response.status,403)
            headers={'Authorization':'Bearer '+'m'*40}
            response=await client.post('/v1/projects/p/backlog/check',json={'agent_id':'a','host':'another-host'},headers=headers)
            self.assertEqual(response.status,409)
            self.assertEqual(calls,[])
            for _ in range(2):
                response=await client.post('/v1/projects/p/backlog/check',json=payload,headers=headers)
                self.assertEqual(response.status,200)
                self.assertEqual((await response.json())['checks'][0]['status'],'checking')
            self.assertEqual(calls,['check'])
            self.assertEqual(managed.jobs,{})
            release.set();await asyncio.gather(*managed.check_tasks.values())
            response=await client.get('/v1/projects/p/backlog',headers=headers)
            self.assertEqual((await response.json())['checks'][0]['unknown'],20)
            self.assertEqual(self.posts,[])
        finally:
            release.set();await client.close()

    async def test_unknown_checks_run_without_ui_and_do_not_hold_new_saves(self):
        self.policy['policy']['can_capture'] = True
        pending = ['a'*64]
        release_check = asyncio.Event()
        actions = []
        async def worker(row,target,action,update):
            actions.append(action)
            if action == 'review': return {'session_id':'native-session','tokens':list(pending),'unknown':20,'unknown_signature':'stable'}
            if action == 'check':
                await release_check.wait()
                return {'status':'unconfirmed','unknown':20,'matched':0,'reason':'original_receipt_unavailable'}
            if action == 'process': return {'status':'submitted'}
            count=len(pending);pending.clear()
            result={'status':'completed','saved':count,'handled':0,'isolated':0,'remaining':0}
            update(result);return result
        self.backlog.worker=worker
        await self.backlog.automatic_check_tick()
        await asyncio.sleep(0)
        self.assertEqual(self.backlog.get('p')['checks'][0]['status'],'checking')
        await self.backlog.automatic_tick()
        await self.backlog.task
        self.assertEqual(pending,[])
        self.assertEqual(self.backlog.get('p')['job']['saved'],1)
        release_check.set()
        await asyncio.gather(*self.backlog.check_tasks.values())
        result=self.backlog.get('p')['checks'][0]
        self.assertEqual((result['status'],result['unknown']),('unconfirmed',20))
        self.assertGreater(result['checked_at'],0)
        await self.backlog.automatic_check_tick()
        self.assertEqual(actions.count('check'),1,'idle polling hot-looped the same unknown set')
        self.backlog.request_check('p','a','host',force=True)
        await asyncio.gather(*self.backlog.check_tasks.values())
        self.assertEqual(actions.count('check'),2)
        self.assertEqual(actions.count('save'),1,'retry check retransmitted contents')
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
        self.processing=[]
        self.gate=asyncio.Event()
        async def worker(row,target,action,update):
            if action=='review':return {'session_id':'native-session','tokens':['a'*64]*32,'unknown':2}
            if action=='process':
                self.processing.append((target['dataset'], target['session_id']))
                return {'status':'submitted'}
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
        self.assertEqual(self.posts, [], 'manager must not bypass the official plugin')
        self.assertEqual(self.processing, [('ds', 'native-session')])
        self.backlog.start('p',operation)
        self.assertEqual(self.count,1)
        self.assertNotIn('targets',result)

    async def test_automatic_save_continues_new_tail_without_ui_and_keeps_unknown_isolated(self):
        self.policy['policy']['can_capture'] = True
        pending = ['a'*64]
        async def worker(row, target, action, update):
            if action == 'review':
                return {'session_id': 'native-session', 'tokens': list(pending), 'unknown': 20}
            if action == 'process':
                self.processing.append((target['dataset'], target['session_id']))
                return {'status': 'submitted'}
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
        await self.backlog.automatic_tick()
        self.assertEqual(self.processing, [('ds', 'native-session')] * 2)
        await self.backlog.automatic_tick()
        self.assertEqual(len(self.processing), 2)
        self.assertEqual(self.posts, [])
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
        original = self.backlog.worker
        async def fail(row, target, action, update):
            if action != 'process': return await original(row, target, action, update)
            self.processing.append(1)
            raise PolicyError('official_result_unknown',503)
        self.backlog.worker = fail
        await self.backlog.automatic_tick()
        self.assertEqual(self.processing, [1])
        for _ in range(2):
            await self.backlog.automatic_tick()
            if self.backlog.task: await self.backlog.task
        self.assertEqual(self.processing, [1], 'unknown model submit was repeated')

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

    async def test_cooling_first_session_does_not_starve_later_session(self):
        self.policy['policy']['can_capture'] = True
        first = {'agent_id':'a', 'host':'host', 'dataset':'ds', 'session_id':'one', 'saved':1, 'processing':'queued'}
        second = {**first, 'host':'other', 'session_id':'two'}
        self.registry.state['sessions'][json.dumps(['a','other'])] = 'p'
        row = {'operation_id':'fair-processing', 'project_id':'p', 'created_at':1,
               'status':'watching', 'automatic':True, 'targets':[],
               'pending_processing':{'first':first, 'second':second}}
        self.backlog.jobs[row['operation_id']] = row
        calls = []
        async def worker(row, target, action, update):
            if action == 'review': return {'session_id':'one', 'tokens':[], 'unknown':0}
            calls.append(target['session_id'])
            return {'status':'deferred', 'reason':'cooldown'} if target['session_id']=='one' else {'status':'submitted'}
        self.backlog.worker = worker
        await self.backlog.automatic_tick()
        await self.backlog.automatic_tick()
        self.assertEqual(calls, ['one','two'])
        self.assertGreater(first['next_processing_at'], 0)
        # The deadline remains durable after restart and must not hot-poll one.
        reopened = Backlog(self.registry, {}, worker=worker)
        try:
            await reopened.automatic_tick()
            self.assertEqual(calls, ['one','two'])
        finally:
            await reopened.close()

    async def test_automatic_processing_passes_incremental_cooldown_to_worker(self):
        target = {'agent_id':'a', 'host':'host', 'dataset':'ds', 'session_id':'one', 'saved':1}
        row = {'operation_id':'cooldown-config', 'project_id':'p', 'automatic':True, 'targets':[]}
        received = []
        async def worker(row, target, action, update):
            received.append(target.get('incremental_cooldown_seconds'))
            return {'status':'submitted'}
        self.backlog.worker = worker
        await self.backlog.process_saved(row,target)
        self.assertEqual(received,[300])

    async def test_official_defer_does_not_block_saving_new_material(self):
        self.policy['policy']['can_capture'] = True
        self.gate.set()
        await self.backlog.automatic_tick()
        await self.backlog.task
        original = self.backlog.worker
        async def deferred(row, target, action, update):
            if action == 'process': return {'status': 'deferred', 'reason': 'cooldown'}
            return await original(row, target, action, update)
        self.backlog.worker = deferred
        await self.backlog.automatic_tick()
        await self.backlog.task
        self.assertEqual(self.backlog.get('p')['job']['saved'], 64)
        self.assertEqual(self.posts, [])

    async def test_transient_policy_pause_rechecks_fresh_buffer_and_retires_stale_remaining(self):
        self.policy['policy']['can_capture'] = True
        self.gate.set()
        await self.backlog.automatic_tick()
        await self.backlog.task
        row = next(iter(self.backlog.jobs.values()))
        target = row['targets'][0]
        target.update(saved=0, handled=0, remaining=32)
        row.update(status='paused', reason='policy_unconfirmed')
        original = self.backlog.worker
        async def empty(row, target, action, update):
            if action == 'review': return {'session_id':'native-session','tokens':[], 'unknown':0, 'blocked':None}
            return await original(row, target, action, update)
        self.backlog.worker = empty
        await self.backlog.automatic_tick()
        result = self.backlog.get('p')['job']
        self.assertEqual((result['status'],result['remaining'],result['handled']), ('watching',0,32))
        self.assertEqual(result['saved'], 0, 'another sender is not our confirmed save')

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
        original = self.backlog.worker
        async def fail(row, target, action, update):
            if action != 'process': return await original(row, target, action, update)
            self.processing.append(1);raise PolicyError('official_result_unknown',503)
        self.backlog.worker=fail
        op=str(uuid.uuid4());self.backlog.start('p',op);await self.backlog.task
        self.assertEqual(self.backlog.get('p')['job']['processing_failed'],1)
        for _ in range(3):self.backlog.get('p');self.backlog.start('p',op)
        self.assertEqual(self.processing,[1])

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

    async def test_session_save_pause_preserves_original_remaining_and_saves_healthy_tail(self):
        """QI02: a known session pause is local, with durable original reasons."""
        self.policy['policy'].update(can_capture=True, can_auto_process=False)
        self.registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('blocked', 'healthy')}
        for reason in ('write_denied', 'policy_unconfirmed', 'capture_request_rejected'):
            with self.subTest(reason=reason):
                actions = []
                held = False
                pending = {'blocked': ['a'*64], 'healthy': ['b'*64]}

                async def worker(row, target, action, update):
                    nonlocal held
                    host = target['host']
                    actions.append((host, action))
                    if action == 'review':
                        return {'session_id': 'native-' + host, 'tokens': list(pending[host]), 'unknown': 0,
                                'blocked': {'reason': reason} if held and host == 'blocked' else None}
                    if action != 'save':
                        self.fail('a paused or disabled processing target reached the submit worker')
                    if host == 'blocked':
                        held = True
                        result = {'status': 'paused', 'reason': reason, 'saved': 0, 'handled': 0, 'isolated': 0, 'remaining': 1}
                    else:
                        pending[host].clear()
                        result = {'status': 'completed', 'saved': 1, 'handled': 0, 'isolated': 0, 'remaining': 0}
                    update(result)
                    return result

                self.backlog.worker = worker
                operation = str(uuid.uuid4())
                self.backlog.start('p', operation)
                await self.backlog.task
                row = self.backlog.jobs[operation]
                blocked, healthy = row['targets']
                self.assertEqual((blocked['saved'], blocked['handled'], blocked['isolated'], blocked['remaining']), (0, 0, 0, 1))
                self.assertEqual(blocked.get('save_status'), 'paused')
                self.assertIn(reason, (blocked.get('save_reason'), blocked.get('reason')))
                self.assertEqual((healthy['saved'], healthy['remaining']), (1, 0), 'one session pause starved the healthy sender')
                self.assertEqual(actions[:3], [('blocked', 'review'), ('healthy', 'review'), ('blocked', 'save')],
                                 'save began before all selected sessions were frozen')
                self.assertIn(('healthy', 'save'), actions)
                view = self.backlog.public(row)
                self.assertEqual((view['saved'], view['remaining']), (1, 1))
                durable = json.loads(self.backlog.path.read_text())[operation]['targets'][0]
                self.assertEqual(durable['remaining'], 1)
                self.assertIn(reason, (durable.get('save_reason'), durable.get('reason')))
                before = list(actions)
                self.backlog.start('p', operation)
                self.assertEqual(actions, before, 'reopening the same operation resent the rejected original')
                await self.backlog.automatic_tick()
                if self.backlog.task and not self.backlog.task.done():
                    await self.backlog.task
                self.assertEqual(actions.count(('blocked', 'save')), 1, 'automatic scanning bypassed the original session hold')
                self.assertEqual(pending['blocked'], ['a'*64])

    async def test_sender_and_progress_unknowns_keep_the_operation_barrier(self):
        """Retained boundary: sender/ownership/statistics uncertainty is not a local skip."""
        self.policy['policy']['can_auto_process'] = False
        self.registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('unknown', 'healthy')}
        cases = ('sender_lock_changed', 'backlog_sender_exit_unconfirmed', 'backlog_worker_unconfirmed',
                 'progress_missing', 'progress_count', 'progress_type', 'progress_classification_count',
                 'progress_classification_type', 'return_malformed', 'return_counts', 'return_running')
        for failure in cases:
            with self.subTest(failure=failure):
                actions = []

                async def worker(row, target, action, update):
                    actions.append((target['host'], action))
                    if action == 'review':
                        return {'session_id': 'native-' + target['host'], 'tokens': ['a'*64], 'unknown': 0}
                    if target['host'] == 'healthy':
                        self.fail('an unknown original sender was bypassed')
                    valid = {'status': 'running', 'saved': 0, 'handled': 0, 'isolated': 0, 'remaining': 1}
                    if failure in ('backlog_sender_exit_unconfirmed', 'backlog_worker_unconfirmed'):
                        update(valid)
                        raise PolicyError(failure, 503)
                    if failure == 'sender_lock_changed':
                        result = {**valid, 'status': 'paused', 'reason': failure}
                        update(result)
                        return result
                    if failure.startswith('progress_'):
                        malformed = dict(valid)
                        if failure == 'progress_missing':
                            malformed.pop('isolated')
                        elif failure == 'progress_count':
                            malformed['saved'] = 1
                        elif failure == 'progress_type':
                            malformed['saved'] = True
                        elif failure == 'progress_classification_count':
                            malformed.update(isolated=1, remaining=0, rejected=1, deferred=1)
                        else:
                            malformed.update(isolated=1, remaining=0, rejected=True, deferred=0)
                        update(malformed)
                        return malformed
                    update(valid)
                    if failure == 'return_malformed':
                        return []
                    if failure == 'return_counts':
                        return {**valid, 'status': 'completed', 'saved': 9, 'remaining': 0}
                    return valid

                self.backlog.worker = worker
                operation = str(uuid.uuid4())
                self.backlog.start('p', operation)
                await self.backlog.task
                row = self.backlog.jobs[operation]
                self.assertEqual(row['status'], 'paused', 'an unconfirmed final sender result was accepted')
                self.assertNotIn(('healthy', 'save'), actions)
                self.assertEqual(row['targets'][1]['remaining'], 1)
                self.assertEqual(self.backlog.public(row)['saved'], 0, 'corrupt progress was exposed as saved content')
                if failure in ('sender_lock_changed', 'backlog_sender_exit_unconfirmed', 'backlog_worker_unconfirmed'):
                    self.assertEqual(row['reason'], failure)
                before = list(actions)
                self.backlog.start('p', operation)
                self.assertEqual(actions, before)

    async def test_public_save_classifications_preserve_completed_isolation_counts(self):
        """Worker QI01/QI03 results are completed scopes, with honest public counts."""
        self.policy['policy']['can_auto_process'] = False
        for rejected, deferred in ((1, 0), (0, 1), (1, 1)):
            with self.subTest(rejected=rejected, deferred=deferred):
                frozen = ['a'*64] * (1 + rejected + deferred)
                submitted = []

                async def worker(row, target, action, update):
                    if action == 'review':
                        return {'session_id': 'native-session', 'tokens': list(frozen), 'unknown': 0}
                    if action != 'save':
                        submitted.append(action)
                        return {'status': 'submitted'}
                    result = {'status': 'completed', 'saved': 1, 'handled': 0, 'isolated': rejected + deferred,
                              'rejected': rejected, 'deferred': deferred, 'filtered': 0, 'remaining': 0}
                    update(result)
                    return result

                self.backlog.worker = worker
                operation = str(uuid.uuid4())
                self.backlog.start('p', operation)
                await self.backlog.task
                row = self.backlog.jobs[operation]
                view = self.backlog.public(row)
                self.assertEqual((view['status'], view['saved'], view['handled'], view['isolated'], view['remaining']),
                                 ('completed', 1, 0, rejected + deferred, 0))
                self.assertEqual((view.get('save_rejected'), view.get('save_deferred')), (rejected, deferred),
                                 'public state hid the original rejection or retry as a successful save')
                target = row['targets'][0]
                self.assertEqual((target.get('rejected'), target.get('deferred')), (rejected, deferred))
                self.assertEqual(sum(target[name] for name in ('saved', 'handled', 'isolated', 'remaining')), len(frozen))
                self.assertEqual(submitted, [], 'isolated material was treated as a processing submission')
                self.assertNotIn('targets', view)

    async def test_automatic_processing_wait_keeps_saving_and_tracks_stop_and_close(self):
        """QI04: one owned in-flight submit does not occupy the capture scheduler."""
        self.policy['policy'].update(can_capture=True, can_auto_process=True)
        await self.backlog.close()
        for ending in ('release', 'user_stop', 'owner_close'):
            with self.subTest(ending=ending):
                registry = types.SimpleNamespace(**vars(self.registry))
                registry.path = self.root / ending / 'registry.json'
                registry.state = copy.deepcopy(self.registry.state)
                registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('host', 'other')}
                entered, release, finished, saved = (asyncio.Event() for _ in range(4))
                process_calls = []
                owner_tasks = set()
                pending = ['c'*64]
                active = 0
                maximum_active = 0

                async def worker(row, target, action, update):
                    nonlocal active, maximum_active
                    if action == 'review':
                        return {'session_id': 'native-' + target['host'],
                                'tokens': list(pending) if target['host'] == 'host' else [], 'unknown': 0}
                    if action == 'save':
                        count = len(pending)
                        pending.clear()
                        result = {'status': 'completed', 'saved': count, 'handled': 0, 'isolated': 0, 'remaining': 0}
                        update(result)
                        saved.set()
                        return result
                    process_calls.append(target['session_id'])
                    owner_tasks.add(asyncio.current_task())
                    active += 1
                    maximum_active = max(maximum_active, active)
                    entered.set()
                    try:
                        await release.wait()
                        return {'status': 'submitted'}
                    finally:
                        active -= 1
                        finished.set()

                self.backlog = Backlog(registry, {}, worker=worker)
                operation = str(uuid.uuid4())
                first = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 'native-host',
                         'saved': 1, 'handled': 0, 'processing': 'queued'}
                second = {**first, 'host': 'other', 'session_id': 'native-other'}
                row = {'operation_id': operation, 'project_id': 'p', 'created_at': 1, 'status': 'watching',
                       'automatic': True, 'targets': [], 'pending_processing': {'first': first, 'second': second}}
                self.backlog.jobs[operation] = row
                self.backlog.persist()
                tick = asyncio.create_task(self.backlog.automatic_tick())
                closing = None
                try:
                    await asyncio.wait_for(entered.wait(), 1)
                    await asyncio.wait_for(saved.wait(), 1)
                    await tick
                    if self.backlog.task:
                        await self.backlog.task
                    self.assertEqual(pending, [], 'a waiting model submit prevented a healthy capture save')
                    current = self.backlog.jobs[operation]
                    self.assertEqual(self.backlog.public(current)['saved'], 1)
                    self.assertEqual(self.backlog.public(current)['processing_active'], 1)
                    self.assertEqual(self.backlog.public(current)['processing_failed'], 0)
                    self.assertFalse(finished.is_set())
                    for _ in range(2):
                        await self.backlog.automatic_tick()
                        if self.backlog.task and not self.backlog.task.done():
                            await self.backlog.task
                    self.assertEqual(process_calls, ['native-host'], 'the pending submit was duplicated or a second submit was admitted')
                    self.assertEqual(maximum_active, 1)
                    if ending == 'user_stop':
                        self.backlog.stop('p', operation)
                        self.assertTrue(self.backlog.cancel_file(current).exists(), 'user stop lost ownership of the in-flight process')
                        before = list(process_calls)
                        await self.backlog.automatic_tick()
                        self.assertEqual(process_calls, before)
                    if ending == 'owner_close':
                        closing = asyncio.create_task(self.backlog.close())
                        observed = asyncio.Event()
                        asyncio.get_running_loop().call_soon(observed.set)
                        await observed.wait()
                        self.assertFalse(closing.done() and not finished.is_set(), 'close returned while its submit task was still unowned')
                        self.assertTrue(self.backlog.cancel_file(current).exists(), 'close did not signal its owned in-flight work')
                    release.set()
                    await asyncio.wait_for(finished.wait(), 1)
                    if closing:
                        await closing
                    else:
                        await self.backlog.close()
                    self.assertEqual(active, 0)
                    self.assertTrue(all(task.done() for task in owner_tasks))
                    self.assertEqual(self.backlog.public(self.backlog.jobs[operation])['processing_active'], 0,
                                     'a finished submit was still presented as an active owned task')
                finally:
                    release.set()
                    await asyncio.gather(tick, *owner_tasks, *([closing] if closing else []), return_exceptions=True)
                    if self.backlog.task:
                        await asyncio.gather(self.backlog.task, return_exceptions=True)
                    await self.backlog.close()

    async def test_execute_subprocess_exit_uncertainty_blocks_later_senders(self):
        """Retained _execute boundary: even valid progress cannot prove sender exit."""
        self.policy['policy']['can_auto_process'] = False
        self.registry.state['sessions'] = {json.dumps(['a', host]): 'p' for host in ('unknown', 'healthy')}
        for failure in ('nonzero_exit', 'no_reply', 'invalid_reply', 'exit_unconfirmed'):
            with self.subTest(failure=failure):
                processes = []
                actions = []

                class Process:
                    def __init__(child):
                        child.payload = None
                        child.lines = None
                        child.returncode = None
                        child.terminated = False
                        child.waits = 0
                        child.stdin = types.SimpleNamespace(write=child.write, drain=child.drain, close=lambda: None)
                        child.stdout = types.SimpleNamespace(readline=child.readline)

                    def write(child, body):
                        child.payload = json.loads(body)
                        actions.append((child.payload['host'], child.payload['action']))

                    async def drain(child):
                        return None

                    async def readline(child):
                        if child.lines is None:
                            if child.payload['action'] == 'review':
                                child.lines = [json.dumps({'session_id': 'native-' + child.payload['host'], 'tokens': ['a'*64], 'unknown': 0}).encode() + b'\n', b'']
                            elif failure == 'no_reply':
                                child.lines = [b'']
                            elif failure == 'invalid_reply':
                                child.lines = [b'{invalid-json\n']
                            else:
                                progress = {'status': 'running', 'saved': 0, 'handled': 0, 'isolated': 0, 'remaining': 1}
                                child.lines = [json.dumps(progress).encode() + b'\n',
                                               asyncio.TimeoutError() if failure == 'exit_unconfirmed' else b'']
                        value = child.lines.pop(0)
                        if isinstance(value, Exception):
                            raise value
                        return value

                    async def wait(child):
                        child.waits += 1
                        if child.payload['action'] != 'review' and failure == 'exit_unconfirmed':
                            raise asyncio.TimeoutError()
                        child.returncode = 0 if child.payload['action'] == 'review' or failure != 'nonzero_exit' else 1
                        return child.returncode

                    def terminate(child):
                        child.terminated = True

                async def launch(*args, **kwargs):
                    child = Process()
                    processes.append(child)
                    return child

                self.backlog.worker = None
                operation = str(uuid.uuid4())
                with patch('project_backlog.asyncio.create_subprocess_exec', side_effect=launch):
                    self.backlog.start('p', operation)
                    await self.backlog.task
                row = self.backlog.jobs[operation]
                self.assertEqual(row['status'], 'paused')
                self.assertNotIn(('healthy', 'save'), actions)
                self.assertEqual(self.backlog.public(row)['saved'], 0)
                self.assertEqual(self.backlog.public(row)['remaining'], 2)
                sender = next(child for child in processes if child.payload['action'] == 'save')
                self.assertTrue(sender.terminated, 'the worker process was abandoned without a stop attempt')
                self.assertTrue(self.backlog.cancel_file(row).exists())
                if failure == 'exit_unconfirmed':
                    self.assertEqual(row['reason'], 'backlog_sender_exit_unconfirmed')
                    self.assertIsNone(sender.returncode, 'unconfirmed exit was rewritten as a known process status')
                elif failure in ('nonzero_exit', 'no_reply'):
                    self.assertEqual(row['reason'], 'backlog_worker_unconfirmed')
                    self.assertIsNotNone(sender.returncode)
    async def test_manual_admission_isolates_denied_agent_without_replaying_after_permission_restore(self):
        """QI05: manual consent covers admitted targets, not later permission changes."""
        self.policy['policy'].update(can_capture=False, can_auto_process=False)
        self.registry.state['agents']['denied-agent'] = copy.deepcopy(self.registry.state['agents']['a'])
        self.registry.state['sessions'] = {
            json.dumps(['denied-agent', 'denied-host']): 'p',
            json.dumps(['a', 'healthy-host']): 'p',
        }
        for refusal in ('write_denied', 'read_denied', 'policy_not_ready'):
            with self.subTest(refusal=refusal):
                restored = False
                actions = []
                original = {'_capture_id': str(uuid.uuid4()), '_replay_ambiguous': True,
                            'type': 'qa', 'question': 'original unknown', 'answer': 'retained'}
                retained = [copy.deepcopy(original)]
                healthy_tokens = ['b'*64]

                def policy(project, agent_id):
                    view = copy.deepcopy(self.policy)
                    if agent_id == 'denied-agent' and not restored:
                        if refusal == 'policy_not_ready':
                            view['ready'] = False
                        elif refusal == 'write_denied':
                            view['policy']['permissions'] = ['read']
                        else:
                            view['policy']['permissions'] = ['write']
                    return view

                self.registry.policy = policy

                async def worker(row, target, action, update):
                    actions.append((target['agent_id'], target['host'], action))
                    if target['agent_id'] == 'denied-agent':
                        if action == 'review' and refusal == 'read_denied' and not restored:
                            raise PolicyError('backlog_read_denied', 403)
                        self.fail('the denied original target entered an unconsented worker action')
                    if action == 'review':
                        return {'session_id': 'healthy-native-session', 'tokens': list(healthy_tokens), 'unknown': 0}
                    if action != 'save':
                        self.fail('processing was disabled for this manual admission scenario')
                    healthy_tokens.clear()
                    result = {'status': 'completed', 'saved': 1, 'handled': 0, 'isolated': 0, 'remaining': 0}
                    update(result)
                    return result

                self.backlog.worker = worker
                operation = str(uuid.uuid4())
                admitted = self.backlog.start('p', operation)
                self.assertEqual(admitted['operation_id'], operation)
                self.assertIsNotNone(self.backlog.task, 'one denied Agent prevented the healthy operation from being admitted')
                await self.backlog.task
                row = self.backlog.jobs[operation]
                denied = next(target for target in row['targets'] if target['agent_id'] == 'denied-agent')
                healthy = next(target for target in row['targets'] if target['agent_id'] == 'a')
                self.assertEqual((denied['agent_id'], denied['host'], denied['dataset']), ('denied-agent', 'denied-host', 'ds'))
                self.assertEqual(denied.get('review_status'), 'unconfirmed')
                reason = denied.get('review_reason') or denied.get('admission_reason') or denied.get('reason')
                self.assertTrue(isinstance(reason, str) and reason, 'the refused target lost its original reason')
                self.assertEqual(denied.get('saved', 0), 0)
                self.assertEqual((healthy['saved'], healthy['remaining']), (1, 0))
                self.assertEqual(self.backlog.public(row)['saved'], 1, 'a retained unknown was counted as a successful save')
                self.assertIn(('a', 'healthy-host', 'review'), actions)
                self.assertIn(('a', 'healthy-host', 'save'), actions)
                self.assertNotIn(('denied-agent', 'denied-host', 'save'), actions)
                self.assertEqual(retained, [original])
                self.assertEqual(healthy_tokens, [])
                before = list(actions)
                restored = True
                # Repeated requests and a new manager view retain the same
                # original operation and refusal, even after policy changes.
                self.backlog.start('p', operation)
                self.assertEqual(actions, before)
                reopened = Backlog(self.registry, {}, worker=worker)
                try:
                    self.assertEqual(reopened.start('p', operation)['operation_id'], operation)
                    self.assertIsNone(reopened.task, 'restoring permission silently expanded the original consent scope')
                    persisted = next(target for target in reopened.jobs[operation]['targets'] if target['agent_id'] == 'denied-agent')
                    self.assertEqual(persisted.get('review_status'), 'unconfirmed')
                    self.assertEqual(persisted.get('review_reason') or persisted.get('admission_reason') or persisted.get('reason'), reason)
                    self.assertEqual(actions, before)
                    self.assertEqual(retained, [original])
                finally:
                    await reopened.close()

    async def test_inactive_session_snapshot_confirmed_without_save_or_cold_watch_persistence(self):
        actions = []

        async def worker(row, target, action, update):
            actions.append(action)
            return {
                'session_id': '',
                'session_inactive': True,
                'cold_watch': {
                    'map_path': str(self.root / 'sessions/host.json'),
                    'bridge_path': str(self.root / 'bridge/host.json'),
                    'bridge_exists': False,
                    'bridge_dev': 0,
                    'bridge_ino': 0,
                    'bridge_size': 0,
                    'bridge_mtime_ns': 0,
                },
                'tokens': [],
                'unknown': 0,
                'signature': hashlib.sha256(b'[]').hexdigest(),
                'filtered': 0,
            }

        self.backlog.worker = worker
        operation = str(uuid.uuid4())
        self.backlog.start('p', operation)
        await self.backlog.task
        summary = self.backlog.get('p')['job']
        self.assertEqual(summary['status'], 'completed')
        self.assertEqual(summary['review_failed'], 0)
        self.assertEqual(summary['saved'], 0)
        self.assertEqual(actions, ['review'])
        persisted = json.loads(self.backlog.path.read_text())[operation]['targets'][0]
        self.assertEqual(persisted['review_status'], 'confirmed')
        self.assertNotIn('cold_watch', persisted)

    async def test_inactive_session_with_nonempty_tokens_or_unknown_is_rejected(self):
        for bad in (
            {'session_id': '', 'session_inactive': True, 'tokens': ['a' * 64], 'unknown': 0},
            {'session_id': '', 'session_inactive': True, 'tokens': [], 'unknown': 1},
        ):
            with self.subTest(bad=bad):
                async def worker(row, target, action, update, payload=bad):
                    return copy.deepcopy(payload)

                self.backlog.worker = worker
                operation = str(uuid.uuid4())
                self.backlog.start('p', operation)
                await self.backlog.task
                summary = self.backlog.get('p')['job']
                self.assertEqual(summary['review_failed'], 1)
                self.assertEqual(summary['reason'], 'partial_review_unconfirmed')

    async def test_cold_review_cache_survives_zero_ttl_and_invalidates_on_fs_change(self):
        plugin_dir = self.root / 'plugin'
        plugin_dir.mkdir()
        manifest_path = plugin_dir / '.memlane-project-access.json'
        manifest_bytes = json.dumps({'files': {}}).encode()
        manifest_path.write_bytes(manifest_bytes)
        map_path = self.root / 'sessions/host.json'
        bridge_path = self.root / 'bridge/host.json'
        map_path.parent.mkdir()
        bridge_path.parent.mkdir()
        self.registry.state['agents']['a']['installation'] = {
            'plugin_dir': str(plugin_dir),
            'env_file': str(self.root / 'env'),
            'home': str(self.root),
            'manifest_sha256': hashlib.sha256(manifest_bytes).hexdigest(),
        }
        calls = []
        mode = ['cold_no_bridge']

        async def worker(row, target, action, update):
            calls.append((target['host'], action))
            if mode[0] == 'resumed':
                return {'session_id': 'resumed-session', 'tokens': ['c' * 64], 'unknown': 0}
            bridge_exists = bridge_path.exists()
            bst = os.lstat(bridge_path) if bridge_exists else None
            return {
                'session_id': '',
                'session_inactive': True,
                'cold_watch': {
                    'map_path': str(map_path),
                    'bridge_path': str(bridge_path),
                    'bridge_exists': bridge_exists,
                    'bridge_dev': bst.st_dev if bst else 0,
                    'bridge_ino': bst.st_ino if bst else 0,
                    'bridge_size': bst.st_size if bst else 0,
                    'bridge_mtime_ns': bst.st_mtime_ns if bst else 0,
                },
                'tokens': [],
                'unknown': 0,
            }

        cold_backlog = Backlog(self.registry, {}, worker=worker, review_cache_ttl=0)
        try:
            row = {'project_id': 'p', 'operation_id': 'op-cold'}
            target = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds'}
            first = await cold_backlog.execute(row, target, 'review')
            self.assertNotIn('cold_watch', first)
            self.assertTrue(first['session_inactive'])
            second = await cold_backlog.execute(row, target, 'review')
            self.assertEqual(second, first)
            self.assertEqual(len(calls), 1, 'cold cache missed even though watched files were unchanged')

            # Resuming session by creating map_path immediately invalidates cold cache.
            map_path.write_text('{"session_id":"resumed-session"}')
            mode[0] = 'resumed'
            third = await cold_backlog.execute(row, target, 'review')
            self.assertEqual(third['session_id'], 'resumed-session')
            self.assertEqual(len(calls), 2)

            # Empty bridge file gets watched by stat; modifying it invalidates immediately.
            map_path.unlink()
            bridge_path.write_text('{}')
            mode[0] = 'cold_empty_bridge'
            fourth = await cold_backlog.execute(row, target, 'review')
            self.assertTrue(fourth['session_inactive'])
            await cold_backlog.execute(row, target, 'review')
            self.assertEqual(len(calls), 3)
            bridge_path.write_text('{"ds:s":{"pending_entries":[{"type":"qa"}]}}')
            mode[0] = 'resumed'
            fifth = await cold_backlog.execute(row, target, 'review')
            self.assertEqual(fifth['session_id'], 'resumed-session')
            self.assertEqual(len(calls), 4)
        finally:
            await cold_backlog.close()

    async def test_fifo_pruning_evicts_finished_jobs_while_protecting_active_and_other_paused(self):
        self.registry.state['projects']['p-paused'] = {'id': 'p-paused', 'dataset_id': 'ds-paused'}
        completed_ids = []
        for idx in range(99):
            op_id = str(uuid.UUID(int=1000 + idx))
            completed_ids.append(op_id)
            row = {
                'operation_id': op_id,
                'project_id': 'p',
                'status': 'completed',
                'reason': '',
                'created_at': 1000.0 + idx,
                'targets': [],
            }
            self.backlog.jobs[op_id] = row
            self.backlog.cancel_file(row).write_bytes(b'stop')

        paused_op = str(uuid.UUID(int=9999))
        self.backlog.jobs[paused_op] = {
            'operation_id': paused_op,
            'project_id': 'p-paused',
            'status': 'paused',
            'reason': 'user_stopped',
            'created_at': 1.0,  # oldest timestamp overall, but latest paused job for p-paused!
            'targets': [],
        }
        self.assertEqual(len(self.backlog.jobs), 100)

        new_op = str(uuid.uuid4())
        started = self.backlog.start('p', new_op)
        self.assertEqual(started['operation_id'], new_op)
        self.gate.set()
        await self.backlog.task

        self.assertEqual(len(self.backlog.jobs), 100)
        self.assertIn(new_op, self.backlog.jobs)
        self.assertIn(paused_op, self.backlog.jobs)
        self.assertNotIn(completed_ids[0], self.backlog.jobs)
        self.assertFalse((self.backlog.root / f'{completed_ids[0]}.stop').exists())
        self.assertIn(completed_ids[-1], self.backlog.jobs)

        # When 100 jobs are all active/watching, backlog_journal_limit (409) still triggers.
        for row in self.backlog.jobs.values():
            row['status'] = 'watching'
        with self.assertRaises(PolicyError) as ctx:
            self.backlog.start('p', str(uuid.uuid4()))
        self.assertEqual((ctx.exception.code, ctx.exception.status), ('backlog_journal_limit', 409))

    async def test_process_saved_cancellation_and_pre_submit_policy_unconfirmed(self):
        target = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's1', 'saved': 1}
        row = {'operation_id': 'op-cancel', 'project_id': 'p', 'automatic': True, 'targets': [], 'pending_processing': {'t': target}}

        async def cancel_worker(r, t, action, update):
            raise asyncio.CancelledError

        self.backlog.worker = cancel_worker
        with self.assertRaises(asyncio.CancelledError):
            await self.backlog.process_saved(row, target)
        self.assertEqual((target['processing'], target['processing_reason']), ('unknown', 'owner_interrupted'))

        async def paused_worker(r, t, action, update):
            return {'status': 'paused', 'reason': 'policy_unconfirmed'}

        self.backlog.worker = paused_worker
        await self.backlog.process_saved(row, target)
        self.assertEqual((target['processing'], target['processing_reason']), ('deferred', 'policy_unconfirmed'))
        self.assertGreater(target.get('next_processing_at', 0), 0)

    async def test_reconcile_processing_target_outcomes_and_bounded_recovery(self):
        from datetime import datetime, timezone

        now = 1_795_275_000.0
        iso_after = datetime.fromtimestamp(now - 20, tz=timezone.utc).strftime('%Y-%m-%dT%H:%M:%S')
        iso_recent = datetime.fromtimestamp(now - 60, tz=timezone.utc).strftime('%Y-%m-%dT%H:%M:%S')

        activity_rows = []
        session_detail = {'session_id': 's1', 'dataset_id': 'ds', 'last_activity_at': iso_recent}

        async def official_req(method, path, body=None, **kw):
            if path.startswith('/api/v1/activity/pipeline-runs'):
                return copy.deepcopy(activity_rows)
            if path.startswith('/api/v1/sessions/'):
                return copy.deepcopy(session_detail)
            raise AssertionError(path)

        self.registry.official.request = official_req

        def make_row(reason='owner_interrupted', attempted_at=now - 120, retry_count=0):
            t = {
                'agent_id': 'a',
                'host': 'host',
                'dataset': 'ds',
                'session_id': 's1',
                'processing': 'unknown',
                'processing_reason': reason,
                'processing_attempted_at': attempted_at,
            }
            if retry_count:
                t['reconcile_retry_count'] = retry_count
            op = str(uuid.uuid4())
            r = {'operation_id': op, 'project_id': 'p', 'created_at': 1, 'status': 'watching', 'automatic': True,
                 'targets': [], 'pending_processing': {json.dumps(['a', 'host']): t}}
            self.backlog.jobs[op] = r
            return r, t

        # 1. Naive UTC improve succeeded after attempted_at -> confirmed submitted.
        activity_rows = [{
            'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
            'dataset_id': 'ds', 'session_ids': ['s1'], 'started_at': iso_after, 'ended_at': iso_after,
        }]
        r1, t1 = make_row()
        await self.backlog.reconcile_processing_target('p', r1, t1, force=True, now=now)
        self.assertEqual((t1['processing'], t1['recovery_reason'], t1['reconcile_status']),
                         ('submitted', 'official_activity_confirmed', 'confirmed'))

        # 2. Active pipeline running -> stays unknown with reconcile_status='running'.
        activity_rows = [{
            'kind': 'pipeline', 'pipeline_name': 'cognify_pipeline', 'pipeline_run_id': 'run-live',
            'status': 'DATASET_PROCESSING_STARTED', 'dataset_id': 'ds', 'session_ids': ['s1'], 'started_at': iso_recent,
        }]
        r2, t2 = make_row()
        await self.backlog.reconcile_processing_target('p', r2, t2, force=True, now=now)
        self.assertEqual((t2['processing'], t2['reconcile_status']), ('unknown', 'running'))

        # 3. owner_interrupted with errored run -> marks definite failed without auto-requeue.
        activity_rows = [{
            'kind': 'pipeline', 'pipeline_name': 'cognify_pipeline', 'pipeline_run_id': 'run-err',
            'status': 'DATASET_PROCESSING_ERRORED', 'dataset_id': 'ds', 'session_ids': ['s1'], 'started_at': iso_recent,
        }]
        r3, t3 = make_row(reason='owner_interrupted', retry_count=0)
        await self.backlog.reconcile_processing_target('p', r3, t3, force=True, now=now)
        self.assertEqual((t3['processing'], t3['reconcile_status']), ('unknown', 'failed'))

        # 4. owner_interrupted missing from bounded activity window -> unconfirmed, NEVER auto-requeues.
        activity_rows = []
        r4, t4 = make_row(reason='owner_interrupted', retry_count=0)
        await self.backlog.reconcile_processing_target('p', r4, t4, force=True, now=now)
        self.assertEqual((t4['processing'], t4['reconcile_status']), ('unknown', 'unconfirmed'))

        # 5. Non-interrupted unknown with confirmed failure never auto-requeues.
        activity_rows = [{
            'kind': 'operation', 'operation_name': 'improve', 'outcome': 'failed',
            'dataset_id': 'ds', 'session_ids': ['s1'], 'started_at': iso_recent, 'ended_at': iso_after,
        }]
        r5, t5 = make_row(reason='official_submission_unconfirmed', retry_count=0)
        await self.backlog.reconcile_processing_target('p', r5, t5, force=True, now=now)
        self.assertEqual((t5['processing'], t5['reconcile_status']), ('unknown', 'failed'))

    async def test_processing_check_and_retry_http_endpoints(self):
        from datetime import datetime, timezone
        import time

        now = time.time()
        iso_after = datetime.fromtimestamp(now - 10, tz=timezone.utc).isoformat()
        self.policy['policy']['can_auto_process'] = True
        process_calls = []

        async def worker(row, target, action, update):
            if action == 'process':
                process_calls.append(target['session_id'])
                return {'status': 'submitted'}
            return {'session_id': target['session_id'], 'tokens': [], 'unknown': 0}

        self.backlog.worker = worker

        confirmed_op = {'kind': 'operation', 'operation_name': 'improve', 'outcome': 'succeeded',
                        'dataset_id': 'ds', 'session_ids': ['s-done'], 'started_at': iso_after, 'ended_at': iso_after}
        failed_op = {'kind': 'operation', 'operation_name': 'improve', 'outcome': 'failed',
                     'dataset_id': 'ds', 'session_ids': ['s-fail'], 'started_at': iso_after, 'ended_at': iso_after}

        async def official_req(method, path, body=None, **kw):
            if 's-done' in path and path.startswith('/api/v1/activity/'):
                return [confirmed_op]
            if 's-done' in path:
                return {'session_id': 's-done', 'dataset_id': 'ds', 'last_activity_at': iso_after}
            if 's-fail' in path and path.startswith('/api/v1/activity/'):
                return [failed_op]
            if 's-fail' in path:
                return {'session_id': 's-fail', 'dataset_id': 'ds', 'last_activity_at': iso_after}
            if 's-unconfirmed' in path and path.startswith('/api/v1/activity/'):
                return []
            if 's-unconfirmed' in path:
                return {'session_id': 's-unconfirmed', 'dataset_id': 'ds', 'last_activity_at': iso_after}
            if 's-unavail' in path:
                raise PolicyError('official_http_503', 503)
            raise AssertionError(path)

        self.registry.official.request = official_req
        self.registry.state['sessions'][json.dumps(['a', 'host-fail'])] = 'p'
        self.registry.state['sessions'][json.dumps(['a', 'host-unconfirmed'])] = 'p'
        self.registry.state['sessions'][json.dumps(['a', 'host-unavail'])] = 'p'
        op = str(uuid.uuid4())
        self.backlog.jobs[op] = {
            'operation_id': op,
            'project_id': 'p',
            'created_at': 10,
            'status': 'watching',
            'automatic': True,
            'targets': [],
            'pending_processing': {
                json.dumps(['a', 'host']): {
                    'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-done',
                    'saved': 1, 'handled': 0,
                    'processing': 'unknown', 'processing_reason': 'official_submission_unconfirmed',
                    'processing_attempted_at': now - 60,
                },
                json.dumps(['a', 'host-fail']): {
                    'agent_id': 'a', 'host': 'host-fail', 'dataset': 'ds', 'session_id': 's-fail',
                    'saved': 1, 'handled': 0,
                    'processing': 'unknown', 'processing_reason': 'official_submission_unconfirmed',
                    'processing_attempted_at': now - 60,
                },
                json.dumps(['a', 'host-unconfirmed']): {
                    'agent_id': 'a', 'host': 'host-unconfirmed', 'dataset': 'ds', 'session_id': 's-unconfirmed',
                    'saved': 1, 'handled': 0,
                    'processing': 'unknown', 'processing_reason': 'owner_interrupted',
                    'processing_attempted_at': now - 60,
                },
                json.dumps(['a', 'host-unavail']): {
                    'agent_id': 'a', 'host': 'host-unavail', 'dataset': 'ds', 'session_id': 's-unavail',
                    'saved': 1, 'handled': 0,
                    'processing': 'unknown', 'processing_reason': 'official_submission_unconfirmed',
                    'processing_attempted_at': now - 60,
                },
            },
        }

        app = create_app(self.registry, 'm' * 40, {'a': 'a' * 40})
        with patch('project_backlog.Backlog', return_value=self.backlog):
            attach_backlog(app, self.registry, {})
        client = TestClient(TestServer(app))
        await client.start_server()
        try:
            manager = {'Authorization': 'Bearer ' + 'm' * 40}
            self.assertEqual((await client.post('/v1/projects/p/backlog/processing/check')).status, 401)
            self.assertEqual((await client.post('/v1/projects/p/backlog/processing/retry',
                                                headers={'Authorization': 'Bearer ' + 'a' * 40})).status, 403)

            check_res = await client.post('/v1/projects/p/backlog/processing/check', headers=manager, json={})
            self.assertEqual(check_res.status, 200)
            check_body = await check_res.json()
            self.assertEqual(check_body['job']['processing_failed'], 3)
            self.assertEqual({i['session_id']: i['reconcile_status'] for i in check_body['processing_issues']},
                             {'s-fail': 'failed', 's-unconfirmed': 'unconfirmed', 's-unavail': 'unavailable'})
            self.assertEqual(process_calls, [])

            # Only confirmed failure (s-fail) may retry; unconfirmed & unavailable remain protected.
            retry_res = await client.post('/v1/projects/p/backlog/processing/retry', headers=manager, json={})
            self.assertEqual(retry_res.status, 200)
            if self.backlog.processing_task:
                await self.backlog.processing_task
            self.assertEqual(process_calls, ['s-fail'])
            final_body = await (await client.get('/v1/projects/p/backlog', headers=manager)).json()
            self.assertEqual(final_body['job']['processing_failed'], 2)
            self.assertEqual({i['session_id'] for i in final_body['processing_issues']}, {'s-unconfirmed', 's-unavail'})
        finally:
            await client.close()

    async def test_retry_isolates_denied_target_clears_historical_failed_and_tick_isolates_errors(self):
        from datetime import datetime, timezone
        import time

        now = time.time()
        iso_after = datetime.fromtimestamp(now - 10, tz=timezone.utc).isoformat()
        self.registry.state['agents']['b'] = copy.deepcopy(self.registry.state['agents']['a'])
        self.registry.state['sessions'][json.dumps(['b', 'host-b'])] = 'p'

        def per_agent_policy(project, agent_id):
            if agent_id == 'a':
                return {'ready': True, 'policy': {'permissions': ['read'], 'can_capture': False, 'can_auto_process': False}}
            return {'ready': True, 'policy': {'permissions': ['read', 'write'], 'can_capture': True, 'can_auto_process': True}}

        self.registry.policy = per_agent_policy

        async def official_req(method, path, body=None, **kw):
            if path.startswith('/api/v1/activity/'):
                sid = 's-a' if 's-a' in path else 's-b'
                return [{'kind': 'operation', 'operation_name': 'improve', 'outcome': 'failed',
                         'dataset_id': 'ds', 'session_ids': [sid], 'started_at': iso_after, 'ended_at': iso_after}]
            sid = 's-a' if 's-a' in path else 's-b'
            return {'session_id': sid, 'dataset_id': 'ds', 'last_activity_at': iso_after}

        self.registry.official.request = official_req
        processed = []

        async def worker(row, target, action, update):
            if action == 'process':
                processed.append((target['agent_id'], target['session_id']))
                return {'status': 'submitted'}
            raise PolicyError('review_unavailable', 503)

        self.backlog.worker = worker

        # Historical older job has confirmed-failed unknown for agent b.
        old_op = str(uuid.uuid4())
        self.backlog.jobs[old_op] = {
            'operation_id': old_op, 'project_id': 'p', 'created_at': 5, 'status': 'watching', 'automatic': True,
            'targets': [], 'pending_processing': {
                json.dumps(['b', 'host-b']): {
                    'agent_id': 'b', 'host': 'host-b', 'dataset': 'ds', 'session_id': 's-b',
                    'saved': 1, 'processing': 'unknown', 'processing_reason': 'official_submission_unconfirmed',
                    'processing_attempted_at': now - 120,
                },
            },
        }
        new_op = str(uuid.uuid4())
        self.backlog.jobs[new_op] = {
            'operation_id': new_op, 'project_id': 'p', 'created_at': 20, 'status': 'watching', 'automatic': True,
            'targets': [], 'pending_processing': {
                json.dumps(['a', 'host']): {
                    'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-a',
                    'saved': 1, 'processing': 'unknown', 'processing_reason': 'official_submission_unconfirmed',
                    'processing_attempted_at': now - 60,
                },
                json.dumps(['b', 'host-b']): {
                    'agent_id': 'b', 'host': 'host-b', 'dataset': 'ds', 'session_id': 's-b',
                    'saved': 1, 'processing': 'unknown', 'reason': 'previous_submission_unconfirmed',
                },
            },
        }

        # Batch retry isolates denied agent 'a' and still submits healthy agent 'b' without historical wedge.
        await self.backlog.retry_project_processing('p')
        if self.backlog.processing_task:
            await self.backlog.processing_task
        self.assertEqual(processed, [('b', 's-b')])
        self.assertEqual(self.backlog.jobs[old_op]['pending_processing'][json.dumps(['b', 'host-b'])]['processing'], 'failed')
        self.assertEqual(self.backlog.jobs[new_op]['pending_processing'][json.dumps(['b', 'host-b'])]['processing'], 'submitted')
        self.assertEqual(self.backlog.jobs[new_op]['pending_processing'][json.dumps(['a', 'host'])]['processing'], 'unknown')

        # Explicit single-target retry on denied agent 'a' still raises 403.
        with self.assertRaises(PolicyError) as ctx:
            await self.backlog.retry_project_processing('p', agent_id='a', host='host')
        self.assertEqual(ctx.exception.status, 403)

        # automatic_check_tick isolates failing review and still invokes reconcile_next_processing_target.
        reconcile_called = []
        async def fake_reconcile():
            reconcile_called.append(True)
        self.backlog.reconcile_next_processing_target = fake_reconcile
        await self.backlog.automatic_check_tick()
        self.assertEqual(reconcile_called, [True])

    async def test_automatic_tick_clears_stale_backlog_write_denied_review_failed(self):
        self.policy['policy']['can_capture'] = True
        op = str(uuid.uuid4())
        self.backlog.jobs[op] = {
            'operation_id': op,
            'project_id': 'p',
            'created_at': 10,
            'status': 'watching',
            'automatic': True,
            'targets': [{
                'agent_id': 'a',
                'host': 'host',
                'dataset': 'ds',
                'admission_blocked': True,
                'review_status': 'unconfirmed',
                'review_reason': 'backlog_write_denied',
                'save_status': 'paused',
                'save_reason': 'backlog_write_denied',
                'tokens': [],
            }],
            'pending_processing': {},
        }
        self.backlog.worker = lambda row, target, action, update: asyncio.sleep(
            0, result={'session_id': 'native-session', 'tokens': [], 'unknown': 0})
        self.assertEqual(self.backlog.get('p')['job']['review_failed'], 1)
        await self.backlog.automatic_tick()
        self.assertEqual(self.backlog.get('p')['job']['review_failed'], 0)

    async def test_unconfirmed_policy_defers_process_saved_and_migrates_legacy_disabled(self):
        # 1. Transient ACL unconfirmed (!ready while agent still has write + auto_process) defers, not disabled.
        self.policy['ready'] = False
        self.policy['policy']['permissions'] = ['read', 'write']
        self.policy['policy']['can_auto_process'] = True
        op = str(uuid.uuid4())
        target = {
            'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-unconfirmed',
            'saved': 1, 'handled': 0, 'processing': 'queued', 'reason': 'backlog_write_denied',
        }
        row = {
            'operation_id': op, 'project_id': 'p', 'created_at': 10, 'status': 'watching',
            'automatic': True, 'targets': [], 'pending_processing': {json.dumps(['a', 'host']): target},
        }
        self.backlog.jobs[op] = row
        await self.backlog.process_saved(row, target)
        self.assertEqual(target['processing'], 'deferred')
        self.assertEqual(target['processing_reason'], 'policy_unconfirmed')
        self.assertGreater(target.get('next_processing_at', 0), 0)
        self.assertNotIn('reason', target)

        # 2. Explicit write revocation (!write or !can_auto_process) still disables via checked_target.
        self.policy['policy']['permissions'] = ['read']
        self.policy['policy']['can_auto_process'] = False
        target['processing'] = 'queued'
        with self.assertRaises(PolicyError) as ctx:
            await self.backlog.process_saved(row, target)
        self.assertEqual(ctx.exception.code, 'backlog_write_denied')

        # 3. Backlog.__init__ migrates historical disabled + backlog_write_denied + pre_submit_policy_unconfirmed.
        legacy_op = str(uuid.uuid4())
        self.backlog.jobs[legacy_op] = {
            'operation_id': legacy_op, 'project_id': 'p', 'created_at': 20, 'status': 'watching',
            'automatic': True, 'targets': [], 'pending_processing': {
                json.dumps(['a', 'host']): {
                    'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-legacy',
                    'saved': 1, 'handled': 0, 'processing': 'disabled',
                    'reason': 'backlog_write_denied',
                    'recovery_reason': 'pre_submit_policy_unconfirmed',
                    'processing_reason': 'policy_unconfirmed',
                },
            },
        }
        self.backlog.persist()
        reloaded = Backlog(self.registry, {})
        migrated = reloaded.jobs[legacy_op]['pending_processing'][json.dumps(['a', 'host'])]
        self.assertEqual(migrated['processing'], 'deferred')
        self.assertEqual(migrated['previous_processing'], 'disabled')
        self.assertEqual(migrated['recovery_reason'], 'pre_submit_policy_unconfirmed')
        self.assertNotIn('reason', migrated)
        self.assertGreater(migrated.get('next_processing_at', 0), 0)

    async def test_prune_finished_jobs_hard_protects_unresolved_unknown_in_automatic_and_manual_jobs(self):
        # Seed 3 oldest jobs with unresolved unknown/submitting in both manual targets
        # and automatic pending_processing, plus 102 resolved jobs beyond MAX_BACKLOG_JOBS.
        manual_unknown_op = str(uuid.uuid4())
        auto_unknown_op = str(uuid.uuid4())
        manual_submitting_op = str(uuid.uuid4())
        self.backlog.jobs[manual_unknown_op] = {
            'operation_id': manual_unknown_op, 'project_id': 'p', 'created_at': 1, 'status': 'completed',
            'targets': [{'agent_id': 'a', 'host': 'h-manual-unk', 'dataset': 'ds', 'session_id': 's1', 'processing': 'unknown'}],
        }
        self.backlog.jobs[auto_unknown_op] = {
            'operation_id': auto_unknown_op, 'project_id': 'p', 'created_at': 2, 'status': 'completed', 'automatic': True,
            'targets': [], 'pending_processing': {
                json.dumps(['a', 'h-auto-unk']): {
                    'agent_id': 'a', 'host': 'h-auto-unk', 'dataset': 'ds', 'session_id': 's2', 'processing': 'unknown',
                },
            },
        }
        self.backlog.jobs[manual_submitting_op] = {
            'operation_id': manual_submitting_op, 'project_id': 'p', 'created_at': 3, 'status': 'interrupted',
            'targets': [{'agent_id': 'a', 'host': 'h-manual-sub', 'dataset': 'ds', 'session_id': 's3', 'processing': 'submitting'}],
        }
        resolved_ops = []
        for idx in range(102):
            op = str(uuid.uuid4())
            resolved_ops.append(op)
            self.backlog.jobs[op] = {
                'operation_id': op, 'project_id': 'p', 'created_at': 10 + idx, 'status': 'completed',
                'targets': [{'agent_id': 'a', 'host': f'h-{idx}', 'dataset': 'ds', 'processing': 'submitted'}],
            }
        self.backlog._prune_finished_jobs('p', max_jobs=100)
        self.assertEqual(len(self.backlog.jobs), 99)
        self.assertIn(manual_unknown_op, self.backlog.jobs)
        self.assertIn(auto_unknown_op, self.backlog.jobs)
        self.assertIn(manual_submitting_op, self.backlog.jobs)
        for evicted in resolved_ops[:6]:
            self.assertNotIn(evicted, self.backlog.jobs)

    async def test_retry_unconfirmed_requires_explicit_single_target_confirm_and_rejects_running_or_unavailable(self):
        from aiohttp.test_utils import TestClient, TestServer
        from datetime import datetime, timezone
        import time
        from project_backlog import attach_backlog
        from project_control_server import create_app

        now = time.time()
        iso_recent = datetime.fromtimestamp(now - 5, tz=timezone.utc).isoformat()
        mode = {'state': 'empty'}

        async def official_req(method, path, body=None, **kw):
            if mode['state'] == 'unavailable':
                raise PolicyError('official_result_unknown', 503)
            if path.startswith('/api/v1/activity/'):
                if mode['state'] == 'running':
                    return [{'kind': 'operation', 'operation_name': 'improve', 'dataset_id': 'ds',
                             'session_ids': ['s-unconf'], 'started_at': iso_recent}]
                return []
            return {'session_id': 's-unconf', 'dataset_id': 'ds', 'last_activity_at': iso_recent}

        self.registry.official.request = official_req
        processed = []

        async def worker(row, target, action, update):
            if action == 'process':
                processed.append(target['session_id'])
                return {'status': 'submitted'}
            return {'session_id': 's-unconf', 'tokens': [], 'unknown': 0}

        self.backlog.worker = worker
        old_op = str(uuid.uuid4())
        new_op = str(uuid.uuid4())
        self.backlog.jobs[old_op] = {
            'operation_id': old_op, 'project_id': 'p', 'created_at': 1, 'status': 'completed',
            'targets': [{
                'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-unconf',
                'saved': 1, 'processing': 'unknown', 'processing_reason': 'owner_interrupted',
                'processing_attempted_at': now - 90,
            }],
        }
        self.backlog.jobs[new_op] = {
            'operation_id': new_op, 'project_id': 'p', 'created_at': 2, 'status': 'watching', 'automatic': True,
            'targets': [], 'pending_processing': {
                json.dumps(['a', 'host']): {
                    'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-unconf',
                    'saved': 1, 'processing': 'unknown', 'processing_reason': 'owner_interrupted',
                    'processing_attempted_at': now - 60,
                },
            },
        }
        # 1. Default retry (batch and single) refuses unconfirmed without explicit confirmation.
        await self.backlog.retry_project_processing('p')
        await self.backlog.retry_project_processing('p', agent_id='a', host='host')
        self.assertEqual(processed, [])
        # 2. Batch confirm_unconfirmed is rejected.
        with self.assertRaises(PolicyError) as ctx:
            await self.backlog.retry_project_processing('p', confirm_unconfirmed=True)
        self.assertEqual(ctx.exception.code, 'invalid_request_fields')
        # 3. Running or unavailable still refuses even with confirm_unconfirmed=True.
        for blocked_mode in ('running', 'unavailable'):
            mode['state'] = blocked_mode
            await self.backlog.retry_project_processing('p', agent_id='a', host='host', confirm_unconfirmed=True)
            self.assertEqual(processed, [], blocked_mode)
        # 4. Online empty window with explicit single-target confirm_unconfirmed=True resolves & retries.
        mode['state'] = 'empty'
        await self.backlog.retry_project_processing('p', agent_id='a', host='host', confirm_unconfirmed=True)
        if self.backlog.processing_task:
            await self.backlog.processing_task
        self.assertEqual(processed, ['s-unconf'])
        self.assertEqual(self.backlog.jobs[old_op]['targets'][0]['processing'], 'failed')
        self.assertEqual(self.backlog.jobs[old_op]['targets'][0]['recovery_reason'], 'operator_confirmed_unsubmitted')
        self.assertEqual(self.backlog.jobs[new_op]['pending_processing'][json.dumps(['a', 'host'])]['processing'], 'submitted')

        # 5. HTTP endpoint validation for confirm_unconfirmed.
        app = create_app(self.registry, 'm' * 40, {})
        http_backlog = attach_backlog(app, self.registry, {})
        http_backlog.jobs[new_op] = copy.deepcopy(self.backlog.jobs[new_op])
        http_backlog.jobs[new_op]['pending_processing'][json.dumps(['a', 'host'])]['processing'] = 'unknown'
        http_backlog.jobs[new_op]['pending_processing'][json.dumps(['a', 'host'])]['processing_attempted_at'] = now - 30
        http_backlog.worker = worker
        client = TestClient(TestServer(app))
        await client.start_server()
        try:
            headers = {'Authorization': 'Bearer ' + 'm' * 40}
            bad_batch = await client.post('/v1/projects/p/backlog/processing/retry', headers=headers,
                                          json={'confirm_unconfirmed': True})
            self.assertEqual(bad_batch.status, 400)
            bad_type = await client.post('/v1/projects/p/backlog/processing/retry', headers=headers,
                                         json={'agent_id': 'a', 'host': 'host', 'confirm_unconfirmed': 'yes'})
            self.assertEqual(bad_type.status, 400)
            ok = await client.post('/v1/projects/p/backlog/processing/retry', headers=headers,
                                   json={'agent_id': 'a', 'host': 'host', 'confirm_unconfirmed': True})
            self.assertEqual(ok.status, 200)
            if http_backlog.processing_task:
                await http_backlog.processing_task
            self.assertEqual(len(processed), 2)
        finally:
            await client.close()

    async def test_rate_limited_session_is_isolated_with_target_backoff_and_manual_shares_unknown_barrier(self):
        from datetime import datetime, timezone
        import time

        # 1. OpenCode-style paused/rate_limited isolates only the rate-limited target
        # without halting sibling targets or wedging automatic_tick on paused.
        self.policy['policy']['can_capture'] = True
        self.registry.state['agents']['b'] = copy.deepcopy(self.registry.state['agents']['a'])
        self.registry.state['sessions'][json.dumps(['b', 'host-b'])] = 'p'
        saved_targets = []

        async def worker(row, target, action, update):
            if action == 'review':
                return {'session_id': 's-' + target['agent_id'], 'tokens': ['a' * 64], 'unknown': 0}
            if action == 'save':
                if target['agent_id'] == 'a':
                    return {'status': 'paused', 'reason': 'rate_limited', 'saved': 0, 'handled': 0, 'isolated': 0, 'remaining': 1}
                saved_targets.append(target['agent_id'])
                return {'status': 'completed', 'reason': '', 'saved': 1, 'handled': 0, 'isolated': 0, 'remaining': 0}
            return {'status': 'submitted'}

        self.backlog.worker = worker
        op = str(uuid.uuid4())
        self.backlog.start('p', op, automatic=True)
        await self.backlog.task
        if self.backlog.processing_task:
            await self.backlog.processing_task
        row = self.backlog.jobs[op]
        self.assertEqual(row['status'], 'watching')
        self.assertEqual(row['reason'], 'partial_save_deferred')
        self.assertEqual(saved_targets, ['b'])
        self.assertIn(json.dumps(['a', 'host']), row.get('save_retries', {}))
        self.assertGreater(row['save_retries'][json.dumps(['a', 'host'])]['after'], time.time())

        # Legacy paused/rate_limited automatic row is also recoverable by automatic_tick.
        row.update(status='paused', reason='rate_limited', capture_retry_at=0)
        row['save_retries'].clear()
        await self.backlog.automatic_tick()
        if self.backlog.task:
            await self.backlog.task
        self.assertEqual(self.backlog.jobs[op]['status'], 'watching')

        # 2. Manual process_saved enforces the same historical submitting/unknown barrier.
        now = time.time()
        iso_recent = datetime.fromtimestamp(now - 5, tz=timezone.utc).isoformat()
        hist_op = str(uuid.uuid4())
        self.backlog.jobs[hist_op] = {
            'operation_id': hist_op, 'project_id': 'p', 'created_at': 1, 'status': 'completed',
            'targets': [{
                'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-hist',
                'saved': 1, 'processing': 'unknown', 'processing_reason': 'owner_interrupted',
                'processing_attempted_at': now - 60,
            }],
        }
        manual_target = {'agent_id': 'a', 'host': 'host', 'dataset': 'ds', 'session_id': 's-hist', 'saved': 1}
        manual_row = {'operation_id': str(uuid.uuid4()), 'project_id': 'p', 'created_at': 50, 'status': 'processing', 'targets': [manual_target]}
        await self.backlog.process_saved(manual_row, manual_target)
        self.assertEqual(manual_target['processing'], 'unknown')
        self.assertEqual(manual_target['reason'], 'previous_submission_unconfirmed')

        # 3. confirm_unconfirmed=True releases a historical-only unknown target absent from latest job.
        latest_other = str(uuid.uuid4())
        self.backlog.jobs[latest_other] = {
            'operation_id': latest_other, 'project_id': 'p', 'created_at': 9999999999.0, 'status': 'completed',
            'targets': [{'agent_id': 'b', 'host': 'host-b', 'dataset': 'ds', 'session_id': 's-b', 'processing': 'submitted'}],
        }
        async def empty_official(method, path, body=None, **kw):
            if path.startswith('/api/v1/activity/'):
                return []
            return {'session_id': 's-hist', 'dataset_id': 'ds', 'last_activity_at': iso_recent}

        self.registry.official.request = empty_official
        submitted_hist = []
        async def hist_worker(row, target, action, update):
            if action == 'process':
                submitted_hist.append(target['session_id'])
                return {'status': 'submitted'}
            return {'session_id': 's-hist', 'tokens': [], 'unknown': 0}

        self.backlog.worker = hist_worker
        await self.backlog.retry_project_processing('p', agent_id='a', host='host', confirm_unconfirmed=True)
        if self.backlog.processing_task:
            await self.backlog.processing_task
        self.assertEqual(submitted_hist, ['s-hist'])
        self.assertEqual(self.backlog.jobs[hist_op]['targets'][0]['processing'], 'submitted')


if __name__=='__main__':unittest.main()
