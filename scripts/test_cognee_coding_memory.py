"""Synthetic SDK/Unix contracts; no model, engine, project data or credentials."""
import asyncio
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from uuid import uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'engine/python'))
import cognee_coding_memory as module
import cognee_session_processing as processing
from cognee_session_processing import owner_execution
from test_cognee_submission import fake_modules


class Result:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)
        self.stages, self.error = [], None
    def attach_background_task(self, task):
        self.task = task
        return self
    async def wait(self):
        await self.task
        return self


class Tests(unittest.TestCase):
    def test_private_unix_wire_identity_and_safe_transport_failure(self):
        async def run(root):
            path, sock = root / 'owner.json', root / 'service.sock'
            seen = []
            trigger = {k: str(uuid4()) for k in ('operation', 'instance', 'dataset', 'user')}
            trigger['session'] = 'synthetic-session'
            config = {'protocol': 1, 'socket': str(sock), 'token': 'synthetic-private-capability-not-a-real-secret'}
            path.write_text(json.dumps(config)); path.chmod(0o600)
            async def handle(reader, writer):
                request = json.loads(await reader.readline())
                seen.append(request)
                body = {'protocol': 1, 'id': request['id'], 'ok': True,
                    'result': {'request': dict(trigger, automatic_grant={'generation': 1}), 'state': 'prepared'}}
                if request['op'] == 'bad-identity': body['id'] = 'foreign'
                if request['op'] == 'failure':
                    body.update(ok=False, code='scope_denied', error='SECRET_PROVIDER_BODY')
                writer.write(json.dumps(body).encode() + b'\n'); await writer.drain()
                writer.close(); await writer.wait_closed()
            server = await asyncio.start_unix_server(handle, path=str(sock))
            async with server:
                task = await module.owner_call(path, 'coding_memory_trigger', trigger)
                self.assertEqual(task['state'], 'prepared')
                self.assertEqual(seen[0]['coding_memory_capability'], config['token'])
                self.assertNotIn('user_confirmed', seen[0])
                for op, code in [('bad-identity', 'coding_memory_owner_response_invalid'), ('failure', 'coding_memory_scope_denied')]:
                    with self.assertRaisesRegex(ValueError, '^' + code + '$'):
                        await module.owner_call(path, op, trigger)
                path.chmod(0o644)
                with self.assertRaisesRegex(ValueError, '^coding_memory_owner_configuration_invalid$'):
                    await module.owner_call(path, 'coding_memory_trigger', trigger)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); root.chmod(0o700)
            asyncio.run(run(root))

    def test_official_entrypoints_delegate_once_and_internal_execution_does_not_recurse(self):
        user, dataset = SimpleNamespace(id=uuid4()), SimpleNamespace(id=uuid4())
        original_calls, requests, registered = [], [], []
        async def original(**kwargs):
            original_calls.append(kwargs)
            return 'unchanged-official-pipeline'
        async def scoped(value, principal):
            self.assertIs(principal, user)
            self.assertEqual(value, 'synthetic-dataset')
            return dataset
        async def call(path, operation, trigger):
            self.assertEqual(path, '/synthetic/owner.json')
            requests.append((operation, dict(trigger)))
            self.assertEqual(trigger['user'], str(user.id))
            self.assertEqual(trigger['dataset'], str(dataset.id))
            return {'state': 'completed', 'receipt': {'status': 'completed', 'stages': [
                {'stage': 'distill_sessions', 'status': 'completed', 'counts': {'lessons': 1}}]}}
        modules = fake_modules({
            'cognee': {'improve': original},
            'cognee.api.v1.improve': {'improve': original},
            'cognee.api.v1.improve.improve': {'improve': original},
            'cognee.modules.improve': {'ImproveResult': Result, 'StageResult': lambda **v: SimpleNamespace(**v)},
            'cognee.infrastructure.background_tasks': {'register_background_task': registered.append},
        })
        async def run():
            module.install('/synthetic/owner.json')
            managed = modules['cognee'].improve
            self.assertIs(managed, modules['cognee.api.v1.improve'].improve)
            self.assertIs(managed, modules['cognee.api.v1.improve.improve'].improve)
            module.install('/synthetic/owner.json')
            result = await managed(dataset='synthetic-dataset', session_ids=['synthetic'], user=user)
            self.assertTrue(result.finished)
            self.assertIsNone(result.error)
            self.assertEqual(result.stages[0].counts, {'lessons': 1})
            self.assertEqual(len(requests), 1)
            result = await managed(dataset='synthetic-dataset', session_ids=['synthetic'], user=user, run_in_background=True)
            self.assertFalse(result.finished)
            self.assertEqual(len(registered), 1)
            self.assertIs(await result.wait(), result)
            self.assertTrue(result.finished)
            before = len(requests)
            token = owner_execution.set(True)
            try:
                self.assertEqual(await managed(dataset='synthetic-dataset', session_ids=['synthetic'], user=user, review_conflicts=False), 'unchanged-official-pipeline')
            finally:
                owner_execution.reset(token)
            self.assertEqual(len(requests), before)
            self.assertEqual(len(original_calls), 1)
            self.assertFalse(original_calls[0]['review_conflicts'])
            for invalid in ({'session_ids': []}, {'session_ids': ['one', 'two']}, {'session_ids': ['one'], 'review_conflicts': True}, {'session_ids': ['one'], 'feedback_alpha': 0.2}):
                with self.assertRaises(ValueError):
                    await managed(dataset='synthetic-dataset', user=user, **invalid)
            self.assertEqual(len(requests), before)
        with patch.dict(sys.modules, modules), patch.object(processing, '_owner_path', None), patch.object(module, 'authorized_existing_dataset', scoped), patch.object(module, 'owner_call', call), patch.object(module, 'service_instance', lambda: str(uuid4())):
            asyncio.run(run())

    def test_observation_failure_never_restarts_work_or_invents_success(self):
        async def run():
            result = Result(finished=False)
            failure = {'state': 'failed', 'error_code': 'scope_denied', 'receipt': {'stages': [
                {'stage': 'persist_session_qa', 'status': 'completed', 'counts': {'qas': 1}}]}}
            await module.observe('/unused', {}, failure, result)
            self.assertEqual(result.error, 'coding_memory_scope_denied')
            self.assertEqual(result.stages[0].counts, {'qas': 1})
            calls = []
            async def unavailable(*args):
                calls.append(args[1])
                raise OSError('SECRET_PRIVATE_PATH')
            with patch.object(module, 'owner_call', unavailable):
                result = Result(finished=False)
                await module.observe('/unused', {}, {'state': 'running'}, result)
            self.assertEqual(calls, ['coding_memory_task'])
            self.assertEqual(result.error, 'coding_memory_observation_unconfirmed')
            self.assertTrue(result.finished)
        modules = fake_modules({'cognee.modules.improve': {'StageResult': lambda **v: SimpleNamespace(**v)}})
        with patch.dict(sys.modules, modules): asyncio.run(run())


if __name__ == '__main__':
    unittest.main()
