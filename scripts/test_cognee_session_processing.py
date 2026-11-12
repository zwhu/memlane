"""Session processing boundaries; synthetic ASGI and SDK contexts, no models."""
import asyncio
import copy
from contextvars import ContextVar
import os
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import UUID, uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import cognee_session_processing as module
from test_cognee_submission import fake_modules


def fixture():
    p = {k: str(uuid4()) for k in ('instance', 'library', 'operation', 'project', 'dataset')}
    p.update(session='synthetic-session', processing={'revision': {'config_digest': 'a' * 64}, 'dependency_digest': 'b' * 64},
             gateway={'endpoint': 'http://127.0.0.1:12345/v1', 'token': 'synthetic-private-token'})
    return p


def result(status='completed'):
    return {'status': status, 'stages': [{'stage': 'distill_sessions', 'status': status, 'counts': {'sessions': 1}}]}


class Tests(unittest.TestCase):
    def test_coding_scope_uses_authenticated_user_and_reads_no_material(self):
        from fastapi import FastAPI
        import httpx
        p = fixture()
        user = SimpleNamespace(id=uuid4())
        app = FastAPI()
        async def auth(): return user
        module.install(app, p['instance'], auth)
        calls = []
        async def authorized(**kwargs):
            calls.append(kwargs)
            if kwargs['datasets'] != [UUID(p['dataset'])]:
                raise ValueError('SECRET_INTERNAL_ERROR')
            return [SimpleNamespace(id=UUID(p['dataset']))]
        async def existing(datasets, permission_type, principal, strict):
            self.assertEqual(permission_type, 'write')
            self.assertTrue(strict)
            return await authorized(datasets=datasets, user=principal)
        bindings = fake_modules({'cognee.modules.data.methods': {'get_authorized_existing_datasets': existing}})
        async def run():
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://synthetic') as client:
                response = await client.get('/api/v1/memlane/coding-memory/scope', params={'dataset': p['dataset']})
                self.assertEqual(response.status_code, 200)
                self.assertEqual(response.json(), {'instance': p['instance'], 'dataset': p['dataset'], 'user': str(user.id), 'managed': False})
                response = await client.get('/api/v1/memlane/coding-memory/scope', params={'dataset': str(uuid4())})
                self.assertEqual(response.status_code, 403)
                self.assertNotIn('SECRET', response.text)
                self.assertEqual((await client.get('/api/v1/memlane/coding-memory/scope', params={'dataset': 'invalid'})).status_code, 422)
        with patch.dict(sys.modules, bindings):
            asyncio.run(run())
        self.assertEqual(len(calls), 2)
        self.assertTrue(all(call['user'] is user for call in calls))

    def test_scope_and_live_capability_validation(self):
        p = fixture()
        original = module.validate(p, p['instance'])
        replay = copy.deepcopy(p)
        replay['gateway']['token'] = 'another-live-capability'
        self.assertEqual(original, module.validate(replay, p['instance']))
        for change in ('foreign_instance', 'missing_model_digest', 'dependency', 'remote', 'newline_session', 'injected_text'):
            bad = copy.deepcopy(p)
            if change == 'foreign_instance': bad['instance'] = str(uuid4())
            if change == 'missing_model_digest': del bad['processing']['revision']['config_digest']
            if change == 'dependency': bad['processing']['dependency_digest'] = 'invalid'
            if change == 'remote': bad['gateway']['endpoint'] = 'https://example.invalid/v1'
            if change == 'newline_session': bad['session'] = 'bad\nsession'
            if change == 'injected_text': bad['text'] = 'not an ingestion endpoint'
            with self.assertRaises(Exception, msg=change): module.validate(bad, p['instance'])

    def test_result_does_not_export_provider_errors_or_model_payload(self):
        raw = result('errored')
        raw['stages'][0].update(error='SECRET_PROVIDER_BODY', run={'payload': 'SECRET_CONTENT'}, reason='https://secret.invalid/token')
        out = module.safe_result(raw)
        self.assertNotIn('SECRET', repr(out))
        self.assertNotIn('secret.invalid', repr(out))
        self.assertEqual(out['stages'][0]['error_code'], 'official_stage_failed')
        for invalid in ({'status': 'completed', 'stages': []}, {'status': 'running', 'stages': []}):
            with self.assertRaises(ValueError): module.safe_result(invalid)

    def test_parallel_model_contexts_and_cancellation_reset(self):
        context = ContextVar('synthetic_llm_context', default='parent')
        user = SimpleNamespace(id=uuid4())
        p = fixture()
        seen = []
        async def authorized(datasets, permission_type, principal, strict):
            self.assertEqual(datasets, [UUID(p['dataset'])])
            self.assertIs(principal, user)
            self.assertEqual(permission_type, 'write')
            self.assertTrue(strict)
            return [SimpleNamespace(id=UUID(p['dataset']))]
        async def row(**kwargs):
            return SimpleNamespace(user_id=user.id, dataset_id=UUID(p['dataset']))
        async def improve(**kwargs):
            self.assertEqual(kwargs['session_ids'], [p['session']])
            self.assertFalse(kwargs['run_in_background'])
            self.assertTrue(module.owner_execution.get())
            before = context.get()
            if before.llm_api_key == 'automatic':
                self.assertIsNone(kwargs['build_global_context_index'])
                self.assertIsNone(kwargs['review_conflicts'])
            else:
                self.assertFalse(kwargs['build_global_context_index'])
                self.assertFalse(kwargs['review_conflicts'])
            await asyncio.sleep(0)
            self.assertIs(context.get(), before)
            seen.append(before.llm_api_key)
            if before.llm_api_key == 'cancel': raise asyncio.CancelledError()
            return result()
        bindings = fake_modules({
            'cognee.base_config': {'get_base_config': lambda: SimpleNamespace(personalization_enabled=False)},
            'cognee.api.v1.serve.state': {'get_remote_client': lambda: None},
            'cognee.modules.data.methods': {'get_authorized_existing_datasets': authorized},
            'cognee.modules.session_lifecycle.metrics': {'get_session_row': row},
            'cognee.context_global_variables': {'llm_config': context},
            'cognee.api.v1.improve': {'improve': improve},
            'cognee.infrastructure.llm.config': {'LLMConfig': lambda **kwargs: SimpleNamespace(**kwargs)},
        })
        import cognee_processing_adapter
        before_env = dict(os.environ)
        async def run():
            a, b, c, d = (copy.deepcopy(p) for _ in range(4))
            for item, key in zip((a, b, c, d), ('one', 'two', 'cancel', 'automatic')): item['gateway']['token'] = key
            d['automatic'] = True
            results = await asyncio.gather(*(module.improve_official(item, user) for item in (a, b, c, d)), return_exceptions=True)
            self.assertEqual([r['status'] for r in results[:2]], ['completed', 'completed'])
            self.assertIsInstance(results[2], asyncio.CancelledError)
            self.assertEqual(results[3]['status'], 'completed')
            self.assertEqual(context.get(), 'parent')
            self.assertFalse(module.owner_execution.get())
        with patch.dict(sys.modules, bindings), patch.object(cognee_processing_adapter, 'install'):
            asyncio.run(run())
        self.assertEqual(sorted(seen), ['automatic', 'cancel', 'one', 'two'])
        self.assertEqual(dict(os.environ), before_env)

    def test_configured_service_requires_private_owner_not_only_official_identity(self):
        from fastapi import FastAPI
        import httpx
        import cognee_coding_memory
        p = fixture()
        user = SimpleNamespace(id=uuid4())
        app = FastAPI()
        async def auth(): return user
        module.install(app, p['instance'], auth)
        calls = []
        async def runner(payload, principal):
            calls.append(payload['operation'])
            return result()
        expected = ['synthetic-private-owner-capability']
        async def run():
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://synthetic') as client:
                for token in (None, 'foreign-owner'):
                    body = copy.deepcopy(p)
                    if token: body['owner'] = token
                    response = await client.post('/api/v1/memlane/session-improve', json=body)
                    self.assertEqual(response.status_code, 403)
                self.assertEqual(calls, [])
                body = dict(p, owner=expected[0])
                digest = module.validate(body, p['instance'])
                self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=body)).status_code, 200)
                expected[0] = 'rotated-synthetic-owner-capability'
                body['owner'] = expected[0]
                self.assertEqual(module.validate(body, p['instance']), digest)
                self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=body)).status_code, 200)
                self.assertEqual(len(calls), 1)
                cancel = {k: p[k] for k in ('instance', 'library', 'operation')}
                self.assertEqual((await client.post('/api/v1/memlane/session-improve/cancel', json=cancel)).status_code, 403)
                cancel['owner'] = expected[0]
                response = await client.post('/api/v1/memlane/session-improve/cancel', json=cancel)
                self.assertEqual(response.status_code, 200)
                self.assertNotIn(expected[0], response.text)
        with patch.object(module, '_owner_path', '/synthetic/private-owner'), patch.object(cognee_coding_memory, 'owner_configuration', lambda path: {'token': expected[0]}), patch.object(module, 'improve_official', runner):
            asyncio.run(run())

    def test_asgi_replay_conflict_cancel_and_unknown_outcome(self):
        from fastapi import FastAPI
        import httpx
        p = fixture()
        user = SimpleNamespace(id=uuid4())
        app = FastAPI()
        async def auth(): return user
        module.install(app, p['instance'], auth)
        calls = []
        async def runner(payload, principal):
            calls.append(payload['operation'])
            if payload['session'] == 'failure': raise RuntimeError('SECRET_PROVIDER_BODY')
            return result()
        async def run():
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://synthetic') as client:
                for _ in range(2):
                    response = await client.post('/api/v1/memlane/session-improve', json=p)
                    self.assertEqual(response.status_code, 200)
                self.assertEqual(len(calls), 1)
                changed = copy.deepcopy(p);changed['session'] = 'different'
                self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=changed)).status_code, 409)
                cancellation = {k: p[k] for k in ('instance', 'library', 'operation')}
                self.assertEqual((await client.post('/api/v1/memlane/session-improve/cancel', json=cancellation)).status_code, 200)
                self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=p)).status_code, 409)
                failed = copy.deepcopy(p);failed.update(operation=str(uuid4()), session='failure')
                response = await client.post('/api/v1/memlane/session-improve', json=failed)
                self.assertEqual(response.status_code, 503)
                self.assertNotIn('SECRET', response.text)
                self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=failed)).status_code, 409)
                self.assertEqual(calls.count(failed['operation']), 1)
        with patch.object(module, 'improve_official', runner): asyncio.run(run())

    def test_owner_scope_rejected_before_model_context(self):
        for failure in ('owner', 'dataset', 'personalization', 'remote'):
            with self.subTest(failure=failure):
                p = fixture()
                user = SimpleNamespace(id=uuid4())
                async def authorized(*args, **kwargs): return [SimpleNamespace(id=UUID(p['dataset']))]
                async def row(**kwargs):
                    return SimpleNamespace(user_id=uuid4() if failure == 'owner' else user.id,
                                           dataset_id=uuid4() if failure == 'dataset' else UUID(p['dataset']))
                modules = fake_modules({
                    'cognee.base_config': {'get_base_config': lambda: SimpleNamespace(personalization_enabled=failure == 'personalization')},
                    'cognee.api.v1.serve.state': {'get_remote_client': lambda: object() if failure == 'remote' else None},
                    'cognee.modules.data.methods': {'get_authorized_existing_datasets': authorized},
                    'cognee.modules.session_lifecycle.metrics': {'get_session_row': row},
                    'cognee.context_global_variables': {'llm_config': ContextVar('llm_context')},
                    'cognee.api.v1.improve': {'improve': lambda **kwargs: self.fail('unauthorized processing ran')},
                })
                with patch.dict(sys.modules, modules), self.assertRaises(PermissionError):
                    asyncio.run(module.improve_official(p, user))

    def test_active_cancellation_waits_for_processing_cleanup(self):
        from fastapi import FastAPI
        import httpx
        p = fixture()
        user = SimpleNamespace(id=uuid4())
        app = FastAPI()
        async def auth(): return user
        module.install(app, p['instance'], auth)
        async def run():
            started, cleaned = asyncio.Event(), asyncio.Event()
            async def runner(payload, principal):
                started.set()
                try: await asyncio.Future()
                finally: cleaned.set()
            with patch.object(module, 'improve_official', runner):
                async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://synthetic') as client:
                    pending = asyncio.create_task(client.post('/api/v1/memlane/session-improve', json=p))
                    await asyncio.wait_for(started.wait(), 2)
                    self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=p)).status_code, 409)
                    response = await client.post('/api/v1/memlane/session-improve/cancel', json={k: p[k] for k in ('instance', 'library', 'operation')})
                    self.assertEqual(response.status_code, 200)
                    self.assertTrue(cleaned.is_set())
                    terminal = await asyncio.gather(pending, return_exceptions=True)
                    self.assertIsInstance(terminal[0], asyncio.CancelledError)
                    self.assertEqual((await client.post('/api/v1/memlane/session-improve', json=p)).status_code, 409)
        asyncio.run(run())


if __name__ == '__main__': unittest.main()
