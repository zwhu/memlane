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

    async def test_switching_away_from_ollama_clears_ollama_api_base(self):
        import os
        from unittest.mock import patch
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop('OLLAMA_API_BASE', None)
            ollama = await self.runtime.apply(
                'to-ollama', {'id': 'ollama-1', 'revision': 1}, 'digest-ollama',
                {'mode': 'provider', 'provider': 'ollama'},
                {'LLM_ENDPOINT': 'http://127.0.0.1:11434'}, ['source'])
            self.assertEqual(ollama['status'], 'applied')
            self.assertEqual(os.environ.get('OLLAMA_API_BASE'), 'http://127.0.0.1:11434')
            openai = await self.runtime.apply(
                'to-openai', {'id': 'openai-1', 'revision': 1}, 'digest-openai',
                {'mode': 'provider', 'provider': 'openai'},
                {'LLM_ENDPOINT': 'https://example.invalid/v1'}, ['source'])
            self.assertEqual(openai['status'], 'applied')
            self.assertNotIn('OLLAMA_API_BASE', os.environ)

    async def test_handoff_restores_litellm_model_cost_and_chunks_per_batch_across_channels(self):
        import os
        import litellm
        from unittest.mock import MagicMock, patch
        from generation_ollama import receipt_for

        bridge_model = 'openai/memlane-handoff-isolation-test'
        ollama_model = 'ollama_chat/memlane-ollama-isolation-test'
        ollama_endpoint = 'http://127.0.0.1:11434'
        litellm.model_cost.pop(bridge_model, None)
        litellm.model_cost.pop('memlane-handoff-isolation-test', None)
        litellm.model_cost.pop(ollama_model, None)
        litellm.model_cost.pop('memlane-ollama-isolation-test', None)

        clear_mock = MagicMock()
        with patch.dict(os.environ, {}, clear=False), \
             patch('generation_handoff_runtime._clear_cognify_config_cache', clear_mock):
            os.environ.pop('CHUNKS_PER_BATCH', None)
            # 1. Switch into agent_bridge -> registers model_cost & sets CHUNKS_PER_BATCH=2.
            fake_bridge = SimpleNamespace(close=AsyncMock())
            self.factory.return_value = (SimpleNamespace(llm_model=bridge_model, llm_endpoint='http://127.0.0.1:18173/v1'), fake_bridge)
            r1 = await self.runtime.apply(
                'to-bridge', {'id': 'bridge-1', 'revision': 1}, 'digest-bridge',
                {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'memlane-handoff-isolation-test'},
                {'LLM_MODEL': bridge_model, 'LLM_ENDPOINT': 'http://127.0.0.1:18173/v1', 'CHUNKS_PER_BATCH': '2'}, ['source'])
            self.assertEqual(r1['status'], 'applied')
            self.assertEqual(os.environ.get('CHUNKS_PER_BATCH'), '2')
            self.assertTrue(litellm.supports_response_schema(model=bridge_model))
            self.assertEqual(litellm.model_cost[bridge_model].get('supports_function_calling'), False)
            self.assertEqual(clear_mock.call_count, 1)

            # 2. Switch from agent_bridge to direct provider with same model ID -> restores model_cost & clears CHUNKS_PER_BATCH.
            self.factory.return_value = (SimpleNamespace(llm_model=bridge_model, llm_endpoint='https://example.invalid/v1'), None)
            r2 = await self.runtime.apply(
                'to-provider', {'id': 'prov-1', 'revision': 1}, 'digest-prov',
                {'mode': 'provider', 'provider': 'openai', 'model': 'memlane-handoff-isolation-test'},
                {'LLM_MODEL': bridge_model, 'LLM_ENDPOINT': 'https://example.invalid/v1'}, ['source'])
            self.assertEqual(r2['status'], 'applied', r2)
            fake_bridge.close.assert_awaited_once()
            self.assertNotIn('CHUNKS_PER_BATCH', os.environ)
            self.assertNotIn(bridge_model, litellm.model_cost)
            self.assertFalse(litellm.supports_response_schema(model=bridge_model))
            self.assertEqual(clear_mock.call_count, 2)

            # 3. Switch into ollama -> registers schema capability; leaving ollama invokes restore().
            evidence = receipt_for(ollama_model, ollama_endpoint)
            self.factory.return_value = (SimpleNamespace(llm_model=ollama_model, llm_endpoint=ollama_endpoint), None)
            r3 = await self.runtime.apply(
                'to-ollama-schema', {'id': 'ollama-s', 'revision': 1}, 'digest-ollama-s',
                {'mode': 'provider', 'provider': 'ollama', 'model': 'memlane-ollama-isolation-test'},
                {'LLM_MODEL': ollama_model, 'LLM_ENDPOINT': ollama_endpoint,
                 'MEMLANE_OLLAMA_SCHEMA_EVIDENCE': json.dumps(evidence)}, ['source'])
            self.assertEqual(r3['status'], 'applied', r3)
            self.assertTrue(litellm.supports_response_schema(model=ollama_model))

            self.factory.return_value = (SimpleNamespace(llm_model=bridge_model, llm_endpoint='https://example.invalid/v1'), None)
            r4 = await self.runtime.apply(
                'leave-ollama', {'id': 'prov-2', 'revision': 1}, 'digest-prov-2',
                {'mode': 'provider', 'provider': 'openai', 'model': 'memlane-handoff-isolation-test'},
                {'LLM_MODEL': bridge_model, 'LLM_ENDPOINT': 'https://example.invalid/v1'}, ['source'])
            self.assertEqual(r4['status'], 'applied', r4)
            self.assertFalse(litellm.supports_response_schema(model=ollama_model))
            self.assertNotIn(ollama_model, litellm.model_cost)
            self.assertNotIn('memlane-ollama-isolation-test', litellm.model_cost)
            self.assertNotIn('OLLAMA_API_BASE', os.environ)
            self.assertEqual(clear_mock.call_count, 4)

            # 4. Blocked switch never mutates litellm.model_cost or CHUNKS_PER_BATCH.
            blocker = asyncio.create_task(asyncio.Event().wait())
            self.pending.add(blocker)
            self.factory.return_value = (SimpleNamespace(llm_model=bridge_model, llm_endpoint='http://127.0.0.1:18173/v1'), None)
            blocked = await self.runtime.apply(
                'blocked-bridge', {'id': 'bridge-2', 'revision': 1}, 'digest-bridge-2',
                {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'memlane-handoff-isolation-test'},
                {'LLM_MODEL': bridge_model, 'LLM_ENDPOINT': 'http://127.0.0.1:18173/v1', 'CHUNKS_PER_BATCH': '2'}, ['source'])
            self.assertEqual(blocked['status'], 'blocked')
            self.assertNotIn(bridge_model, litellm.model_cost)
            self.assertNotIn('CHUNKS_PER_BATCH', os.environ)
            blocker.cancel()
            await asyncio.gather(blocker, return_exceptions=True)


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
            'digest':self.store.resolve(self.ref)['digest'],
            'runtime_active':{'reference':self.ref,'digest':self.store.resolve(self.ref)['digest']},
            'cancelled_sources':[{'session_id':'a','credential_sha256':self.source_hash}]})
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
            'digest':self.store.resolve(self.ref)['digest'],
            'runtime_active':{'reference':self.ref,'digest':self.store.resolve(self.ref)['digest']},'cancelled_sessions':[]})
        await self.control.review('lost')
        self.assertEqual(self.control.service.call_args.args,('GET','lost'))
        self.assertEqual(self.store.state['active'],self.ref)

    async def test_connected_non_codex_handoff_records_selection_for_restart(self):
        from unittest.mock import patch
        ref = self.store.put('pi-agent', {'mode': 'agent_bridge', 'bridge_kind': 'pi',
            'bridge_provider': 'deepseek', 'model': 'native'}, self.store.state['revision'], 'save-pi')
        ref = {key: ref[key] for key in ('id', 'revision')}
        digest = self.store.resolve(ref)['digest']
        self.store.environment = lambda reference, runtime: {}
        self.control.service = AsyncMock(return_value={'status': 'applied', 'reference': ref,
            'digest': digest, 'runtime_active': {'reference': ref, 'digest': digest}, 'cancelled_sources': []})
        with patch('agent_bridge_runtime.require_connected_bridge') as check:
            self.control.start(ref, self.store.state['revision'], 'switch-pi')
            check.assert_called_once()
        await self.control.task
        self.assertEqual(self.store.state['active'], ref)
        self.assertTrue(self.store.state.get('connected_agent_selections', {}).get(digest))

    async def test_revoked_legacy_project_403_never_blocks_active_persistence_or_review(self):
        import hashlib
        # Add two unobserved hook-bound sessions: one revoked (403) and one healthy.
        revoked_key = hashlib.sha256(json.dumps(['agent', 'revoked-host']).encode()).hexdigest()
        healthy_key = hashlib.sha256(json.dumps(['agent', 'healthy-host']).encode()).hexdigest()
        self.control.registry.state['projects'] = {
            'revoked-project': {'dataset_id': 'ds-revoked'},
            'healthy-project': {'dataset_id': 'ds-healthy'},
        }
        self.control.registry.state['sessions'][json.dumps(['agent', 'revoked-host'])] = 'revoked-project'
        self.control.registry.state['sessions'][json.dumps(['agent', 'healthy-host'])] = 'healthy-project'
        checked_calls = []
        def checked_target(project_id, target, *, write=True):
            checked_calls.append((project_id, write))
            if project_id == 'revoked-project':
                raise PolicyError('backlog_write_denied', 403)
            return ({'installation': {'verified': True}}, {'ready': True, 'policy': {'permissions': ['read']}})
        async def execute(job, target, action):
            self.assertEqual(action, 'review')
            return {'session_id': 'hook-healthy-session'}
        backlog = SimpleNamespace(
            checked_target=checked_target,
            execute=execute,
            jobs={},
            persist=lambda: None,
        )
        self.control.backlog = backlog
        digest = self.store.resolve(self.ref)['digest']
        applied_reply = {
            'status': 'applied',
            'reference': self.ref,
            'digest': digest,
            'runtime_active': {'reference': self.ref, 'digest': digest},
            'cancelled_sources': [
                {'session_id': 'hook-revoked-session', 'credential_sha256': self.source_hash},
                {'session_id': 'hook-healthy-session', 'credential_sha256': self.source_hash},
            ],
        }
        self.control.service = AsyncMock(return_value=applied_reply)
        self.control.start(self.ref, self.store.state['revision'], 'switch-with-revoked')
        await self.control.task
        # Engine-applied active MUST be persisted despite revoked legacy project 403.
        self.assertEqual(self.store.state['active'], self.ref)
        self.assertIsNone(self.store.state['next'])
        handoff_row = self.store.state['handoffs']['switch-with-revoked']
        self.assertEqual(handoff_row['status'], 'applied')
        self.assertTrue(any(w.get('project_id') == 'revoked-project' and w.get('error_code') == 'backlog_write_denied'
                            for w in handoff_row.get('reconcile_warnings', [])))
        # checked_target must use read-only (write=False) for review.
        self.assertTrue(all(write is False for _, write in checked_calls))
        # Healthy unobserved session still got scheduled.
        self.assertEqual(self.incremental.state['sessions'][healthy_key]['status'], 'watching')
        self.assertNotIn(revoked_key, self.incremental.state['sessions'])

        # Also verify review() recovers an unknown handoff without getting trapped in 403.
        self.store.state['active'] = {'id': 'existing-codex', 'revision': 1}
        self.store.state['handoffs']['lost-with-revoked'] = {
            'operation_id': 'lost-with-revoked',
            'reference': self.ref,
            'digest': digest,
            'previous': {'id': 'existing-codex', 'revision': 1},
            'status': 'unknown',
            'reason': 'generation_handoff_result_unknown',
            'started_at': 1.0,
        }
        self.store.save()
        reviewed = await self.control.review('lost-with-revoked')
        self.assertEqual(reviewed['handoff']['status'], 'applied')
        self.assertEqual(self.store.state['active'], self.ref)

    async def test_concurrent_review_is_serialized_and_increments_revision_once(self):
        digest = self.store.resolve(self.ref)['digest']
        self.store.state['handoffs']['concurrent-op'] = {
            'operation_id': 'concurrent-op',
            'reference': self.ref,
            'digest': digest,
            'previous': {'id': 'existing-codex', 'revision': 1},
            'status': 'unknown',
            'reason': 'controller_interrupted',
            'started_at': 1.0,
        }
        self.store.save()
        initial_revision = self.store.state['revision']
        service_calls = 0
        async def slow_service(method, op, body=None):
            nonlocal service_calls
            service_calls += 1
            await asyncio.sleep(0.05)
            return {
                'status': 'applied',
                'reference': self.ref,
                'digest': digest,
                'runtime_active': {'reference': self.ref, 'digest': digest},
                'cancelled_sources': [{'session_id': 'a', 'credential_sha256': self.source_hash}],
            }
        self.control.service = slow_service
        r1, r2 = await asyncio.gather(
            self.control.review('concurrent-op'),
            self.control.review('concurrent-op'),
        )
        self.assertEqual(r1['handoff']['status'], 'applied')
        self.assertEqual(r2['handoff']['status'], 'applied')
        self.assertEqual(service_calls, 1)
        self.assertEqual(self.store.state['active'], self.ref)
        self.assertEqual(self.store.state['revision'], initial_revision + 1)
        self.assertTrue(self.store.state['handoffs']['concurrent-op']['active_committed'])
        self.assertEqual(self.incremental.state['sessions'][self.keys['cancelled']]['status'], 'watching')

    async def test_crash_during_reconciling_preserves_active_and_resumes_without_double_revision(self):
        import hashlib
        from generation_startup import interrupted_applied_candidate
        from _runtime_common import private_write

        hook_key = hashlib.sha256(json.dumps(['agent', 'hook-host']).encode()).hexdigest()
        self.control.registry.state['projects'] = {'p1': {'dataset_id': 'ds-1'}}
        self.control.registry.state['sessions'][json.dumps(['agent', 'hook-host'])] = 'p1'
        digest = self.store.resolve(self.ref)['digest']
        self.store.state['next'] = self.ref
        self.store.save()
        initial_revision = self.store.state['revision']

        crash_once = {'triggered': False}
        observed_mid_disk = {}
        async def execute(job, target, action):
            self.assertEqual(action, 'review')
            if not crash_once['triggered']:
                crash_once['triggered'] = True
                # Verify phase 1 already persisted active + reconciling + active_committed to disk.
                disk = json.loads(self.store.path.read_text())
                observed_mid_disk.update(disk)
                raise asyncio.CancelledError('simulated crash mid-reconciliation')
            return {'session_id': 'hook-session-1'}

        backlog = SimpleNamespace(
            checked_target=lambda project_id, target, *, write=True: (
                {'installation': {'verified': True}}, {'ready': True, 'policy': {'permissions': ['read']}}
            ),
            execute=execute,
            jobs={'j1': {'pending_processing': {'t1': {'session_id': 'hook-session-1', 'agent_id': 'agent', 'processing': 'submitted'}}}},
            persist=lambda: None,
        )
        self.control.backlog = backlog
        applied_reply = {
            'status': 'applied',
            'reference': self.ref,
            'digest': digest,
            'runtime_active': {'reference': self.ref, 'digest': digest},
            'cancelled_sources': [{'session_id': 'hook-session-1', 'credential_sha256': self.source_hash}],
        }
        self.control.service = AsyncMock(return_value=applied_reply)
        self.control.start(self.ref, self.store.state['revision'], 'crash-mid-reconcile')
        await self.control.task

        # Phase 1 persisted active & reconciling before the crash.
        self.assertEqual(observed_mid_disk['active'], self.ref)
        self.assertIsNone(observed_mid_disk['next'])
        mid_row = observed_mid_disk['handoffs']['crash-mid-reconcile']
        self.assertEqual(mid_row['status'], 'reconciling')
        self.assertTrue(mid_row['active_committed'])
        # start() committed +1, Phase 1 committed +1.
        revision_after_commit = initial_revision + 2
        self.assertEqual(observed_mid_disk['revision'], revision_after_commit)
        # Crash left handoff resumable (unknown), NOT prematurely terminal applied, while active stays new.
        self.assertEqual(self.store.state['active'], self.ref)
        self.assertEqual(self.store.state['handoffs']['crash-mid-reconcile']['status'], 'unknown')
        self.assertNotIn(hook_key, self.incremental.state['sessions'])
        self.assertEqual(backlog.jobs['j1']['pending_processing']['t1']['processing'], 'submitted')

        # Startup recovery must recognize active_committed and not re-apply/double-increment revision.
        private_write(
            self.path / 'generation-handoff-runtime.json',
            json.dumps({'crash-mid-reconcile': {'status': 'applied', 'reference': self.ref, 'digest': digest}}).encode(),
        )
        self.assertIsNone(interrupted_applied_candidate(self.store))

        # Simulate controller restart while status was still 'reconciling' on disk.
        self.store.state['handoffs']['crash-mid-reconcile']['status'] = 'reconciling'
        self.store.save()
        restarted = GenerationHandoff(self.store, self.control.registry, {}, incremental=self.incremental, backlog=backlog)
        try:
            self.assertEqual(self.store.state['handoffs']['crash-mid-reconcile']['status'], 'unknown')
            self.assertTrue(self.store.state['handoffs']['crash-mid-reconcile']['active_committed'])
            restarted.service = AsyncMock(return_value=applied_reply)
            resumed = await restarted.review('crash-mid-reconcile')
            self.assertEqual(resumed['handoff']['status'], 'applied')
            # Reconciliation resumed cleanly and did NOT increment revision a second time.
            self.assertEqual(self.store.state['revision'], revision_after_commit)
            self.assertEqual(self.store.state['active'], self.ref)
            self.assertEqual(self.incremental.state['sessions'][hook_key]['status'], 'watching')
            self.assertEqual(backlog.jobs['j1']['pending_processing']['t1']['processing'], 'queued')
        finally:
            await restarted.close()

    async def test_missing_unrelated_agent_credential_never_blocks_active_persistence(self):
        # Register an unrelated agent whose key file is missing.
        self.control.registry.state['agents']['broken-agent'] = {
            'key_file': str(self.path / 'missing-agent-key'),
            'installation': {'verified': True},
        }
        digest = self.store.resolve(self.ref)['digest']
        self.store.state['handoffs']['missing-cred-op'] = {
            'operation_id': 'missing-cred-op',
            'reference': self.ref,
            'digest': digest,
            'previous': {'id': 'existing-codex', 'revision': 1},
            'status': 'unknown',
            'reason': 'controller_interrupted',
            'started_at': 1.0,
        }
        self.store.save()
        initial_revision = self.store.state['revision']
        self.control.service = AsyncMock(return_value={
            'status': 'applied',
            'reference': self.ref,
            'digest': digest,
            'runtime_active': {'reference': self.ref, 'digest': digest},
            'cancelled_sources': [{'session_id': 'a', 'credential_sha256': self.source_hash}],
        })
        reviewed = await self.control.review('missing-cred-op')
        self.assertEqual(reviewed['handoff']['status'], 'applied')
        self.assertEqual(self.store.state['active'], self.ref)
        self.assertEqual(self.store.state['revision'], initial_revision + 1)
        self.assertTrue(self.store.state['handoffs']['missing-cred-op']['active_committed'])
        self.assertEqual(self.incremental.state['sessions'][self.keys['cancelled']]['status'], 'watching')
        self.assertTrue(any(
            w.get('scope') == 'agent_credential' and w.get('agent_id') == 'broken-agent'
            for w in self.store.state['handoffs']['missing-cred-op'].get('reconcile_warnings', [])
        ))

    async def test_reconcile_persist_failure_stays_resumable_until_save_succeeds(self):
        digest = self.store.resolve(self.ref)['digest']
        self.store.state['handoffs']['persist-fail-op'] = {
            'operation_id': 'persist-fail-op',
            'reference': self.ref,
            'digest': digest,
            'previous': {'id': 'existing-codex', 'revision': 1},
            'status': 'unknown',
            'reason': 'controller_interrupted',
            'started_at': 1.0,
        }
        self.store.save()
        initial_revision = self.store.state['revision']
        fail_state = {'fail_inc': True, 'fail_backlog': True}
        def inc_save():
            if fail_state['fail_inc']:
                raise OSError('disk full')
        def backlog_persist():
            if fail_state['fail_backlog']:
                raise OSError('backlog disk full')
        self.incremental.save = inc_save
        backlog = SimpleNamespace(
            checked_target=lambda *a, **k: ({}, {'policy': {'permissions': ['read']}}),
            execute=AsyncMock(return_value={}),
            jobs={'j1': {'pending_processing': {'t1': {'session_id': 'a', 'agent_id': 'agent', 'processing': 'submitted'}}}},
            persist=backlog_persist,
        )
        self.control.backlog = backlog
        self.control.service = AsyncMock(return_value={
            'status': 'applied',
            'reference': self.ref,
            'digest': digest,
            'runtime_active': {'reference': self.ref, 'digest': digest},
            'cancelled_sources': [{'session_id': 'a', 'credential_sha256': self.source_hash}],
        })
        first = await self.control.review('persist-fail-op')
        # Active was committed atomically, but terminal 'applied' must NOT be set while save/persist failed.
        self.assertEqual(self.store.state['active'], self.ref)
        self.assertEqual(self.store.state['revision'], initial_revision + 1)
        self.assertTrue(self.store.state['handoffs']['persist-fail-op']['active_committed'])
        self.assertEqual(first['handoff']['status'], 'unknown')
        self.assertEqual(first['handoff']['reason'], 'handoff_reconcile_persist_failed')
        self.assertTrue(any(w.get('scope') == 'incremental' for w in first['handoff'].get('reconcile_warnings', [])))
        self.assertTrue(any(w.get('scope') == 'backlog' for w in first['handoff'].get('reconcile_warnings', [])))

        # Once persistence recovers, review() finishes cleanly without incrementing revision again.
        fail_state['fail_inc'] = False
        fail_state['fail_backlog'] = False
        second = await self.control.review('persist-fail-op')
        self.assertEqual(second['handoff']['status'], 'applied')
        self.assertEqual(second['handoff']['reason'], '')
        self.assertNotIn('reconcile_warnings', second['handoff'])
        self.assertEqual(self.store.state['revision'], initial_revision + 1)
        self.assertEqual(backlog.jobs['j1']['pending_processing']['t1']['processing'], 'queued')


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
from cognee.api.v1.improve.routers.get_improve_router import ImprovePayloadDTO
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
    controller.stop_timeout = .1
    entered=asyncio.Event()
    @app.post('/api/v1/improve')
    async def begin(payload: ImprovePayloadDTO):
        async def execute():entered.set();await asyncio.Event().wait()
        async with record_operation('improve',session_ids=payload.session_ids,background=True) as operation:
            operation.defer_close()
            module._start_background_run(execute,SimpleNamespace(error=None,finished=False),operation)
        return {'status':'running'}
    @app.get('/api/v1/current-model')
    async def current_model():return {'model':get_llm_context_config().llm_model}
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app),base_url='http://fixture') as client:
        for invalid in ({'session_ids':'wrong'}, {'sessionIds':[{}]}):
            assert (await client.post('/api/v1/improve',json=invalid,headers={'X-Api-Key':'test-agent-key'})).status_code==422
            assert not controller.tasks() and not controller.requests
        assert (await client.post('/api/v1/improve',json={'session_ids':['real-sdk-session']},headers={'X-Api-Key':'test-agent-key'})).status_code==200
        await entered.wait()
        assert controller.tasks() and all(task.get_context().get(scope)['sessions'] == ['real-sdk-session'] for task in controller.tasks())
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
        await client.post('/api/v1/improve',json={'session_ids':['ignored-snake-value'],
            'sessionIds':['real-sdk-session']},headers={'X-Api-Key':'test-agent-key'})
        await entered.wait()
        assert all(task.get_context().get(scope)['sessions'] == ['real-sdk-session'] for task in controller.tasks())
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
