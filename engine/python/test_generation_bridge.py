"""Owned bridge lifetime checks with real HTTP and synthetic stdio, no model."""
import asyncio
import copy
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

from generation_config import GenerationStore
from generation_validation import GenerationValidation
from process_groups import group_exists
from project_policy import PolicyError
from test_codex_completion_endpoint import FAKE

WORKER = '''import json,sys,urllib.request
request=json.load(sys.stdin)
env=request['environment']
body={'model':env['LLM_MODEL'].removeprefix('openai/'), 'messages':[{'role':'user','content':'synthetic'}], 'response_format':{'type':'json_schema','json_schema':{'name':'test','schema':{'type':'object','properties':{'answer':{'type':'string'}},'required':['answer']}}}}
req=urllib.request.Request(env['LLM_ENDPOINT']+'/chat/completions',data=json.dumps(body).encode(),headers={'Content-Type':'application/json','Authorization':'Bearer '+env['LLM_API_KEY']})
with urllib.request.urlopen(req,timeout=60) as response: result=json.load(response)
assert json.loads(result['choices'][0]['message']['content'])=={'answer':'ok'}
print(json.dumps({'status':'passed','plain_passed':True,'schema_passed':True,'quality_passed':True,'structured_mode':'unknown','boundary':'synthetic_http_stdio_fixture'}))
'''


class BridgeValidationTests(unittest.IsolatedAsyncioTestCase):

    async def test_changed_pi_program_or_provider_settings_require_new_validation(self):
        from types import SimpleNamespace
        from agent_bridge_runtime import bridge_runtime_fingerprint, require_bridge_validation
        home = Path(self.runtime['auth_home'])
        runtime = {'agent_hosts': {'pi': {'executable':str(self.executable),'profile':str(home)}}}
        profile = {'mode':'agent_bridge','bridge_kind':'pi','bridge_provider':'deepseek','model':'fixture'}
        row = {'profile':profile,'digest':'fixed-candidate'}
        evidence = {'status':'passed','digest':row['digest'],'registered_revision':1,
                    'bridge_runtime_fingerprint':bridge_runtime_fingerprint(runtime, profile)}
        store = SimpleNamespace(state={'validations':{'original':evidence}})
        require_bridge_validation(store, runtime, row)
        (home/'models.json').write_text('{"providers":{}}')
        with self.assertRaisesRegex(PolicyError, 'generation_bridge_changed_revalidate'):
            require_bridge_validation(store, runtime, row)
        (home/'models.json').unlink()
        self.executable.write_text(self.executable.read_text()+'\n# changed')
        with self.assertRaisesRegex(PolicyError, 'generation_bridge_changed_revalidate'):
            require_bridge_validation(store, runtime, row)

    async def test_connected_non_codex_bridges_pass_startup_validation_without_probe_receipt(self):
        from types import SimpleNamespace
        from agent_bridge_runtime import require_bridge_startup, require_bridge_validation
        for kind, setup in (
            ('pi', lambda home: (
                (home / 'settings.json').write_text('{"defaultProvider":"deepseek","defaultModel":"pi-native"}'),
                (home / 'auth.json').write_text('{}'),
                (home / 'auth.json').chmod(0o600),
            )),
            ('traex', lambda home: (
                (home / 'traecli.toml').write_text('model = "traex-native"\n'),
                (home / 'cli').mkdir(mode=0o700, exist_ok=True),
                (home / 'cli/auth.json').write_text('{}'),
                (home / 'cli/auth.json').chmod(0o600),
            )),
            ('antigravity', lambda home: (
                (home.parent / 'antigravity-cli').mkdir(mode=0o700, exist_ok=True),
                (home.parent / 'antigravity-cli/settings.json').write_text('{"model":"agy-native"}'),
            )),
        ):
            with self.subTest(kind=kind):
                home = (self.root / (kind + '-connected') / '.gemini/config') if kind == 'antigravity' else (self.root / (kind + '-connected'))
                home.mkdir(parents=True, mode=0o700, exist_ok=True)
                setup(home)
                runtime = {'agent_hosts': {kind: {'executable': str(self.executable), 'profile': str(home)}}}
                profile = {'mode': 'agent_bridge', 'bridge_kind': kind, 'model': 'saved-model'}
                if kind == 'pi':
                    profile['bridge_provider'] = 'deepseek'
                row = {'profile': profile, 'digest': 'digest-' + kind}
                # Unconnected and unvalidated non-Codex bridge is still rejected.
                with self.assertRaisesRegex(PolicyError, 'generation_bridge_changed_revalidate'):
                    require_bridge_validation(SimpleNamespace(state={'validations': {}}), runtime, row)
                # Authorized via connected_agent_selections passes startup without manual validation.
                selected = require_bridge_startup(
                    SimpleNamespace(state={'validations': {}, 'connected_agent_selections': {row['digest']: True}}),
                    runtime, row)
                self.assertEqual(selected['kind'], kind)
                # Legacy applied handoff without connected_agent_selections also passes restart.
                selected_legacy = require_bridge_startup(
                    SimpleNamespace(state={'validations': {}, 'handoffs': {'h1': {'status': 'applied', 'digest': row['digest']}}}),
                    runtime, row)
                self.assertEqual(selected_legacy['home'], home)
                # Broken native defaults still fail explicitly rather than falling back.
                if kind == 'pi':
                    (home / 'settings.json').write_text('{}')
                elif kind == 'traex':
                    (home / 'traecli.toml').write_text('')
                else:
                    (home.parent / 'antigravity-cli/settings.json').write_text('{}')
                with self.assertRaisesRegex(PolicyError, 'agent_default_model_unavailable'):
                    require_bridge_startup(
                        SimpleNamespace(state={'validations': {}, 'connected_agent_selections': {row['digest']: True}}),
                        runtime, row)

    async def test_host_selection_and_model_discovery_do_not_read_authentication(self):
        from generation_bridge import bridge_choices, check_bridge_runtime
        for kind, configuration in [('pi', '{"defaultProvider":"deepseek","defaultModel":"fixture-model"}'),
                                    ('traex', 'model = "fixture-model"')]:
            home = self.root / kind
            home.mkdir(mode=0o700)
            path = home / ('settings.json' if kind == 'pi' else 'traecli.toml')
            path.write_text(configuration)
            auth = home / ('auth.json' if kind == 'pi' else 'cli/auth.json')
            auth.parent.mkdir(mode=0o700, exist_ok=True)
            auth.write_text('deliberately not JSON: authentication belongs to Agent')
            auth.chmod(0o600)
            runtime = {'agent_hosts': {kind: {'executable':str(self.executable),'profile':str(home)}}}
            profile = {'mode':'agent_bridge','bridge_kind':kind,'model':'fixture-model'}
            if kind == 'pi':
                profile['bridge_provider'] = 'deepseek'
            self.assertEqual(bridge_choices(runtime)[kind], profile)
            self.assertEqual(check_bridge_runtime(runtime, profile)['home'], home)
            runtime['agent_hosts'][kind]['executable'] = str(self.root / 'missing-explicit-program')
            with self.assertRaises(PolicyError):
                check_bridge_runtime(runtime, profile)
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.store = GenerationStore(self.root / 'generation.json', 'fixed', legacy_model='fixture-model')
        self.bridge_ref = copy.deepcopy(self.store.state['active'])
        api = self.store.put('api', {'mode':'provider','provider':'custom','model':'synthetic',
                                   'endpoint':'https://example.test/v1'}, 0, 'api-save', api_key='synthetic-key')
        self.store.state['active'] = api
        self.store.save()
        self.active = copy.deepcopy(api)
        auth = self.root / 'daily-codex'
        auth.mkdir(mode=0o700)
        (auth/'auth.json').write_text('{"synthetic":true}')
        (auth/'auth.json').chmod(0o600)
        (auth/'daily-marker').write_text('must not be copied')
        (auth/'config.toml').write_text('model = "fixture-model"\n')
        self.executable = self.root / 'fake-codex'
        self.fake('success')
        self.runtime = {'python':sys.executable, 'official_source':'unused-fixed-source',
                        'official_archive':'unused-fixed-archive','generation_mode':'provider',
                        'codex_executable':str(self.executable),'auth_home':str(auth)}
        worker = self.root / 'engine/python/generation_probe.py'
        worker.parent.mkdir(parents=True)
        worker.write_text(WORKER)
        self.validator = GenerationValidation(self.store, self.runtime)
        self.patcher = patch('generation_validation.ROOT', self.root)
        self.patcher.start()

    def fake(self, mode):
        self.executable.write_text(FAKE.format(python=sys.executable,mode=mode))
        self.executable.chmod(0o700)

    async def asyncTearDown(self):
        await self.validator.close()
        self.patcher.stop()
        self.store.close()
        self.temp.cleanup()

    async def test_api_mode_without_completion_url_uses_owned_endpoint_and_preserves_active(self):
        self.validator.start(self.bridge_ref,self.store.state['revision'],'bridge-test')
        await self.validator.task
        row=self.validator.get('bridge-test')
        self.assertEqual(row['status'],'passed',row)
        self.assertEqual(row['bridge_process_groups'],[])
        self.assertIsNone(self.validator.bridge)
        self.assertIsNone(self.validator.process)
        self.assertEqual(self.store.state['active'],self.active)
        self.assertEqual((Path(self.runtime['auth_home'])/'auth.json').read_text(),'{"synthetic":true}')
        self.assertNotIn('completion_url',self.runtime)
        self.assertNotIn('synthetic-key',json.dumps(row))

    async def test_default_changed_during_validation_cannot_produce_a_passed_receipt(self):
        self.fake('slow-success')
        self.validator.start(self.bridge_ref,self.store.state['revision'],'changing-default')
        for _ in range(500):
            row=self.validator.get('changing-default')
            if row.get('bridge_process_groups'):break
            await asyncio.sleep(.01)
        self.assertTrue(row.get('bridge_process_groups'))
        (Path(self.runtime['auth_home'])/'config.toml').write_text('model="next-native-default"\n')
        await self.validator.task
        row=self.validator.get('changing-default')
        self.assertEqual((row['status'],row['error_code']),('failed','generation_bridge_changed_revalidate'))
        self.assertEqual(row['agent_defaults']['model'],'fixture-model')

    async def test_cancel_confirms_probe_and_separate_app_server_group_exit(self):
        self.fake('hang')
        self.validator.start(self.bridge_ref,self.store.state['revision'],'bridge-cancel')
        for _ in range(500):
            row=self.validator.get('bridge-cancel')
            if row.get('bridge_process_groups'):break
            await asyncio.sleep(.01)
        groups=row.get('bridge_process_groups')
        self.assertTrue(groups,'bridge did not start an owned app-server')
        self.assertNotIn(row['process_group'],groups)
        result=await self.validator.cancel('bridge-cancel',self.store.state['revision'],'cancel-owned')
        self.assertEqual(result['status'],'result_unknown')
        self.assertTrue(result['local_process_exited'])
        for pid in [row['process_group'],*groups]:self.assertFalse(group_exists(pid))
        self.assertIsNone(self.validator.bridge)
        self.assertEqual(self.store.state['active'],self.active)

    async def test_missing_login_rejects_before_registering_a_model_operation(self):
        self.runtime['auth_home'] = str(self.root / 'missing-auth-home')
        with self.assertRaisesRegex(PolicyError,'generation_bridge_authentication_required'):
            self.validator.start(self.bridge_ref,self.store.state['revision'],'no-auth')
        self.assertNotIn('no-auth',self.store.state['validations'])
        self.assertIsNone(self.validator.task)

    async def test_interrupted_foreign_group_is_not_signalled_or_retried(self):
        self.store.state['validations']['old']={'status':'result_unknown','bridge_process_groups':[os.getpgrp()]}
        with self.assertRaisesRegex(PolicyError,'generation_validation_owner_requires_review'):
            self.validator.start(self.bridge_ref,self.store.state['revision'],'no-retry')
        result=await self.validator.cancel('old',self.store.state['revision'],'review-old')
        self.assertFalse(result['local_process_exited'])
        self.assertIsNone(self.validator.process)
        self.assertNotIn('no-retry',self.store.state['validations'])

    async def test_unreadable_group_state_never_reports_local_exit(self):
        self.store.state['validations']['unknown']={'status':'result_unknown','bridge_process_groups':[123]}
        with patch('generation_validation.group_exists',side_effect=OSError('synthetic kernel read failure')):
            with self.assertRaisesRegex(PolicyError,'generation_validation_owner_requires_review'):
                self.validator.start(self.bridge_ref,self.store.state['revision'],'blocked-read')
            result=await self.validator.cancel('unknown',self.store.state['revision'],'review-unreadable')
        self.assertFalse(result['local_process_exited'])

    async def test_unconfirmed_bridge_cleanup_retains_resources_and_blocks_next_validation(self):
        from generation_bridge import ValidationBridge
        with patch.object(ValidationBridge,'close',side_effect=RuntimeError('synthetic unconfirmed cleanup')):
            self.validator.start(self.bridge_ref,self.store.state['revision'],'retained')
            await self.validator.task
        row=self.validator.get('retained')
        self.assertEqual(row['status'],'result_unknown')
        self.assertTrue(row['resources_retained'])
        retained=Path(row['temporary_root'])
        self.assertTrue((retained/'completion-token').exists())
        with self.assertRaisesRegex(PolicyError,'generation_validation_busy'):
            self.validator.start(self.bridge_ref,self.store.state['revision'],'blocked')
        await self.validator.bridge.close()
        self.validator.bridge=None
        shutil.rmtree(retained)

    async def test_validation_bridge_idle_timeout_allows_slow_active_and_reaps_silent_hang(self):
        import time
        import aiohttp
        from generation_bridge import ValidationBridge
        profile = {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'fixture-model'}
        bridge_root = self.root / 'idle-bridge'
        bridge_root.mkdir(mode=0o700)
        for bad in (0, -2, float('nan'), float('inf')):
            with self.assertRaises(ValueError):
                ValidationBridge(self.runtime, profile, bridge_root, lambda: None, timeout=bad)
        script = f'''#!{sys.executable}
import json, os, sys, time
def send(x): print(json.dumps(x), flush=True)
for line in sys.stdin:
    m = json.loads(line); method = m['method']
    if method == 'initialize': send({{'id': m['id'], 'result': {{}}}})
    elif method == 'thread/start':
        send({{'id': m['id'], 'result': {{'thread': {{'id': 't'}}, 'model': 'fixture-model', 'modelProvider': 'openai', 'runtimeWorkspaceRoots': [], 'activePermissionProfile': {{'id': 'memlane_completion'}}}}}})
    elif method == 'turn/start':
        send({{'id': m['id'], 'result': {{'turn': {{'id': 'u'}}}}}})
        if 'hang' in m['params']['input'][0]['text']:
            continue
        for idx in range(7):
            time.sleep(0.2)
            sys.stderr.write(f'bridge heartbeat {{idx}}\\n')
            sys.stderr.flush()
        send({{'method': 'item/completed', 'params': {{'threadId': 't', 'turnId': 'u', 'item': {{'type': 'agentMessage', 'text': json.dumps({{'answer': 'ok'}})}}}}}})
        send({{'method': 'turn/completed', 'params': {{'threadId': 't', 'turn': {{'id': 'u', 'status': 'completed', 'error': None}}}}}})
    elif method == 'turn/interrupt':
        send({{'id': m['id'], 'result': {{}}}})
        send({{'method': 'turn/completed', 'params': {{'threadId': 't', 'turn': {{'id': 'u', 'status': 'interrupted'}}}}}})
'''
        self.executable.write_text(script)
        self.executable.chmod(0o700)
        bridge = ValidationBridge(self.runtime, profile, bridge_root, lambda: None, timeout=0.9)
        env = await bridge.start()
        schema = {'type': 'object', 'properties': {'answer': {'type': 'string'}}, 'required': ['answer']}
        headers = {'Authorization': 'Bearer ' + env['LLM_API_KEY']}
        url = env['LLM_ENDPOINT'] + '/chat/completions'
        try:
            async with aiohttp.ClientSession() as session:
                started = time.monotonic()
                async with session.post(url, headers=headers, json={
                    'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'active'}],
                    'response_format': {'type': 'json_schema', 'json_schema': {'schema': schema}},
                }) as response:
                    elapsed = time.monotonic() - started
                    payload = await response.json()
                    self.assertEqual(response.status, 200, payload)
                    self.assertGreater(elapsed, 0.9)
                    self.assertEqual(json.loads(payload['choices'][0]['message']['content']), {'answer': 'ok'})
                async with session.post(url, headers=headers, json={
                    'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'hang'}],
                    'response_format': {'type': 'json_schema', 'json_schema': {'schema': schema}},
                }) as response:
                    payload = await response.json()
                    self.assertEqual(response.status, 504, payload)
                    self.assertEqual(payload['error']['code'], 'codex_timeout')
            self.assertEqual(bridge.processes, {})
        finally:
            await bridge.close()
