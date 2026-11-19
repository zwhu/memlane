"""Continuous controller/service restart coverage without model or data access."""
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from generation_config import GenerationStore
from generation_handoff import GenerationHandoff
from generation_handoff_runtime import GenerationHandoffRuntime
from generation_startup import start_generation


class GenerationRestartTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = Path(self.tmp.name) / 'generation.json'
        self.store = GenerationStore(self.path, 'fixed')
        self.refs = {}
        for name in ('old', 'new'):
            row = self.store.put(name, {'mode': 'provider', 'provider': 'ollama',
                'model': name, 'endpoint': 'http://127.0.0.1:11434'}, self.store.state['revision'], 'save-' + name)
            self.refs[name] = {k: row[k] for k in ('id', 'revision')}
        self.store.state['active'] = self.refs['old']
        self.store.state['validations']['fixture'] = {'digest': self.store.resolve(self.refs['new'])['digest'],
            'status': 'passed', 'plain_passed': True, 'schema_passed': True, 'quality_passed': True}
        self.store.save()
        self.store.native_schema_evidence = lambda row: None
        self.store.environment = lambda reference, runtime: {'LLM_ENDPOINT': 'http://127.0.0.1:11434'}
        self.registry = SimpleNamespace(state={'agents': {}, 'sessions': {}})
        self.engines, self.controls = [], []

    async def asyncTearDown(self):
        for control in self.controls:
            await control.close()
        for engine in self.engines:
            await engine.close()
        self.store.close()
        self.tmp.cleanup()

    def engine(self, reference, digest):
        async def factory(profile, environment):
            return SimpleNamespace(llm_model=profile['model']), None
        async def terminal(operations):
            return True
        engine = GenerationHandoffRuntime({'generation_file': str(self.path), 'generation_reference': reference},
            self.store.resolve(reference)['profile'], {'_digest': digest},
            SimpleNamespace(active=0, reconfigure=lambda *args: None), factory=factory,
            background=lambda: set(), verify_terminal=terminal,
            initial_configuration=SimpleNamespace(llm_model=reference['id']))
        self.engines.append(engine)
        return engine

    def controller(self):
        control = GenerationHandoff(self.store, self.registry, {})
        self.controls.append(control)
        return control

    async def lose_applied_reply(self):
        engine = self.engine(self.refs['old'], self.store.resolve(self.refs['old'])['digest'])
        control = self.controller()
        async def service(method, operation, body=None):
            self.assertEqual(method, 'PUT')
            result = await engine.apply(operation, body['reference'], body['digest'], body['profile'],
                body['environment'], body['source_credentials'])
            self.assertEqual(result['status'], 'applied')
            raise TimeoutError('reply lost after applied journal was committed')
        control.service = service
        control.start(self.refs['new'], self.store.state['revision'], 'switch')
        await control.task
        self.assertEqual(control.view('switch')['handoff']['status'], 'unknown')
        self.assertEqual(self.store.state['active'], self.refs['old'])
        self.assertEqual(engine.active['reference'], self.refs['new'])
        return engine

    async def test_lost_applied_reply_then_full_restart_preserves_actual_selection(self):
        await self.lose_applied_reply()
        self.store.close()
        self.store = GenerationStore(self.path, 'fixed')
        restarted = start_generation(self.store, self.engine, lambda: True)
        control = self.controller()
        async def read(method, operation, body=None):
            self.assertEqual((method, body), ('GET', None))
            return await restarted.inspect(operation)
        control.service = read
        await control.review('switch')
        self.assertEqual(self.store.state['active'], self.refs['new'])
        self.assertEqual(restarted.active['reference'], self.store.state['active'])
        self.assertEqual(restarted.config.llm_model, 'new')
        self.assertEqual(control.view('switch')['handoff']['status'], 'applied')

    async def test_historical_applied_receipt_cannot_override_current_runtime(self):
        await self.lose_applied_reply()
        restarted = self.engine(self.refs['old'], self.store.resolve(self.refs['old'])['digest'])
        control = self.controller()
        async def read(method, operation, body=None):
            return await restarted.inspect(operation)
        control.service = read
        await control.review('switch')
        self.assertEqual(self.store.state['active'], self.refs['old'])
        self.assertEqual(control.view('switch')['handoff']['status'], 'unknown')
        self.assertEqual(json.loads(restarted.path.read_text())['switch']['status'], 'applied')

    async def test_completed_old_handoff_does_not_override_later_active_configuration(self):
        engine = await self.lose_applied_reply()
        row = self.store.state['handoffs']['switch']
        row['status'] = 'applied'
        self.store.state['active'] = self.refs['old']
        self.store.save()
        restarted = start_generation(self.store, self.engine, lambda: True)
        self.assertEqual(restarted.active['reference'], self.refs['old'])
        self.assertEqual(engine.get('switch')['status'], 'applied')
