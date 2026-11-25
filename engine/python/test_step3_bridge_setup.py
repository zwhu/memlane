"""Step 3 routes and activation with fixture Agents and validation receipts."""
import json
from pathlib import Path
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from aiohttp.test_utils import make_mocked_request

from agent_bridge_runtime import bridge_runtime_fingerprint
from agent_discovery import default_profile
from first_install_runtime import activation_scope
from generation_config import GenerationStore
from generation_handoff import attach_generation_handoff
from generation_validation import GenerationValidation
from project_control import Registry
from project_control_server import create_app
from project_policy import PolicyError
from test_first_install_runtime import FirstRuntimeFixture


class Step3BridgeSetupTests(FirstRuntimeFixture, unittest.IsolatedAsyncioTestCase):
    async def test_validation_next_handoff_and_activation_without_capture_binding(self):
        for kind, binary, defaults, contents in (
            ('codex', 'Applications/Codex.app/Contents/Resources/codex-cli/bin/codex', 'config.toml', 'model = "fixture"'),
            ('pi', '.local/bin/pi', 'settings.json', '{"defaultProvider":"deepseek","defaultModel":"fixture"}'),
            ('traex', '.bun/bin/traex', 'traecli.toml', 'model = "fixture"'),
            ('antigravity', 'Applications/Gemini.app/Contents/Helpers/agy', '../antigravity-cli/settings.json', '{"model":"fixture"}'),
        ):
            with self.subTest(kind=kind):
                await self.exercise(kind, binary, defaults, contents)

    async def exercise(self, kind, binary, defaults, contents):
        host = self.parent / kind
        home = Path(default_profile(kind, home=host))
        home.mkdir(parents=True)
        executable = host / binary
        executable.parent.mkdir(parents=True)
        executable.touch(mode=0o700)
        settings = home / defaults
        settings.parent.mkdir(parents=True, exist_ok=True)
        settings.write_text(contents)
        auth = home / ('cli/auth.json' if kind == 'traex' else 'auth.json')
        auth.parent.mkdir(parents=True, exist_ok=True)
        auth.touch(mode=0o600)
        config = {'generation_file': str(self.root / (kind + '.json')),
                  'official_data': str(self.root), 'service_commit': self.spec['source_commit']}
        # The connection view has no enrollments or installed capture bindings.
        registry = Registry(self.root / (kind + '-projects.json'), None)
        self.addCleanup(registry.close)
        store = GenerationStore(config['generation_file'], config['service_commit'])
        self.addCleanup(store.close)
        validator = GenerationValidation(store, config)
        self.addAsyncCleanup(validator.close)
        incremental = SimpleNamespace(state={'sessions': {}})

        async def receipt(operation, _, profile):
            store.state['validations'][operation].update(
                status='passed', plain_passed=True, schema_passed=True, quality_passed=True,
                bridge_runtime_fingerprint=bridge_runtime_fingerprint(config, profile))
            store.save()

        with patch('agent_discovery.Path.home', return_value=host), \
             patch('agent_discovery.shutil.which', return_value=None), \
             patch.object(Path, 'is_file', autospec=True, side_effect=self.only_fixture_files(host)):
            app = create_app(registry, 'm' * 40, {}, codex_config=config, generation=store, generation_validation=validator)
            control = attach_generation_handoff(app, store, registry, config, incremental, SimpleNamespace())
            self.addAsyncCleanup(control.close)
            app.freeze()

            async def request(method, path, profile, operation):
                payload = {'profile': profile, 'expected_revision': store.state['revision'], 'operation_id': operation}
                request = make_mocked_request(method, '/v1/generation/' + path, app=app, headers={
                    'Authorization': 'Bearer ' + 'm' * 40, 'Content-Type': 'application/json'})
                with patch.object(type(request), 'json', new=AsyncMock(return_value=payload)):
                    response = await app._handle(request)
                return response.status, json.loads(response.body)

            profile = {'mode': 'agent_bridge', 'bridge_kind': kind, 'model': 'fixture'}
            if kind == 'pi':
                profile['bridge_provider'] = 'deepseek'
            status, saved = await request('PUT', 'profiles/candidate', profile, 'save')
            self.assertEqual(status, 200, saved)
            self.assertIsNone(store.state['active'])
            reference = {key: saved['result'][key] for key in ('id', 'revision')}
            digest = store.resolve(reference)['digest']
            store.state['connected_agent_selections'] = {digest: True}
            for method, path in (('PUT', 'next'), ('POST', 'handoffs')):
                status, body = await request(method, path, reference, path + '-unvalidated')
                self.assertEqual((status, body['error']), (409, 'generation_validation_required'))
            activation = {'schema': 1, 'store_id': self.spec['store_id'], 'operation_id': 'fixture-activation',
                          'reference': reference, 'digest': digest}
            store.save()
            with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                activation_scope(config, {**activation, 'revision': store.state['revision']})
            store.state.pop('connected_agent_selections')
            with patch.object(validator, 'run', side_effect=receipt):
                status, body = await request('POST', 'validations', reference, 'validate')
                self.assertEqual(status, 200, body)
                await validator.task
            self.assertIsNone(store.state['active'])
            status, body = await request('PUT', 'next', reference, 'select')
            self.assertEqual(status, 200, body)
            with patch.object(control, 'run', new_callable=AsyncMock) as handoff:
                status, body = await request('POST', 'handoffs', reference, 'handoff')
                self.assertEqual(status, 200, body)
                await control.task
                handoff.assert_awaited_once()
            self.assertIsNone(store.state['active'])
            self.assertFalse(activation_scope(config, {**activation, 'revision': store.state['revision']}))

    @staticmethod
    def only_fixture_files(host):
        is_file = Path.is_file
        return lambda path: (path.is_relative_to(host) or
                             not path.is_relative_to('/Applications') and
                             not path.is_relative_to('/opt/homebrew/bin') and
                             not path.is_relative_to('/usr/local/bin')) and is_file(path)
