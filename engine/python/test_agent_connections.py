import asyncio
import importlib.util
import json
from pathlib import Path
import shutil
import sys
import subprocess
import os
import tempfile
import unittest

from agent_connections import NativeConnection, Connections, sha, verify_artifact
from project_control import Registry, private_write
from project_policy import PolicyError

ROOT = Path(__file__).resolve().parents[2]


class ConnectionTests(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name).resolve()
        spec = importlib.util.spec_from_file_location('managed_builder', ROOT / 'scripts/build_project_plugin.py')
        builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(builder)
        cls.plugin = cls.root / 'fixed-plugin'
        builder.build(ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee', cls.plugin)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    async def asyncSetUp(self):
        self.workspace = Path(tempfile.mkdtemp(dir=self.root))
        official = type('OfficialFixture', (), {'url': 'http://127.0.0.1:18999'})()
        self.registry = Registry(self.workspace / 'private/registry.json', official)
        self.config = {'python': sys.executable, 'agent_host_home': str(self.workspace),
                       'project_control': {'plugin_dir': str(self.plugin), 'url': 'http://127.0.0.1:18998'}}
        self.profile = self.workspace / 'pi-profile'
        self.profile.mkdir()
        self.adapter = NativeConnection(self.registry, self.config, 'pi', self.profile, sys.executable)
        for suffix in ('key', 'control'):
            private_write(self.workspace / suffix, ('synthetic-' + suffix + '-credential').encode())
        self.registry.state['agents'][self.adapter.id] = {
            'name': 'pi', 'key_file': str(self.workspace / 'key'), 'control_token_file': str(self.workspace / 'control')}
        self.registry.save()

    async def asyncTearDown(self):
        self.registry.close()
        shutil.rmtree(self.workspace)

    async def test_pi_traex_missing_component_refuses_before_provision_or_state_write(self):
        from unittest.mock import AsyncMock, patch
        empty = self.workspace / 'empty-component'
        empty.mkdir()
        for kind in ('pi', 'traex'):
            for source in (None, str(self.workspace / 'absent'), str(empty), 'relative-component'):
                with self.subTest(kind=kind, source=source):
                    control = {'url': 'http://127.0.0.1:18998'}
                    if source is not None:
                        control['plugin_dir'] = source
                    config = {**self.config, 'project_control': control}
                    adapter = NativeConnection(self.registry, config, kind, self.workspace / ('new-' + kind), sys.executable)
                    before = self.registry.path.read_bytes()
                    provision = AsyncMock(side_effect=AssertionError('must not provision without component'))
                    with patch.object(self.registry, 'provision_agent', provision):
                        self.assertEqual(adapter.view()['status'], 'component_unavailable')
                        with self.assertRaises(PolicyError) as error:
                            await adapter.prepare(self.registry.state['revision'])
                        self.assertEqual(error.exception.code, 'managed_plugin_input_mismatch')
                        provision.assert_not_awaited()
                    self.assertEqual(self.registry.path.read_bytes(), before)
                    self.assertFalse(adapter.profile.exists())

    async def test_missing_source_does_not_hide_previously_prepared_installation(self):
        self.registry.state['connections'] = {self.adapter.id: {'status': 'prepared', 'preview': {'proposed_sha256': 'fixed'}}}
        self.adapter.config = {**self.config, 'project_control': {}}
        self.assertEqual(self.adapter.view()['status'], 'prepared')
        self.assertEqual(self.adapter.view()['preview']['proposed_sha256'], 'fixed')

    async def test_pi_reprepare_preserves_existing_buffer_home_without_runtime_override(self):
        await self.adapter.prepare(self.registry.state['revision'])
        original_home = self.registry.state['agents'][self.adapter.id]['installation']['home']
        pending = Path(original_home) / '.cognee-plugin' / ('pi-' + self.adapter.id) / 'pending.json'
        private_write(pending, b'{"pending":"synthetic-original-record"}')
        self.adapter.config = {key: value for key, value in self.config.items() if key != 'agent_host_home'}
        await self.adapter.prepare(self.registry.state['revision'])
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        self.assertEqual(installation['home'], original_home)
        self.assertEqual(pending.read_bytes(), b'{"pending":"synthetic-original-record"}')

    async def test_pi_reprepare_honors_explicit_buffer_home_override(self):
        await self.adapter.prepare(self.registry.state['revision'])
        replacement = self.workspace / 'explicit-home'
        replacement.mkdir()
        self.adapter.config = {**self.config, 'agent_host_home': str(replacement)}
        await self.adapter.prepare(self.registry.state['revision'])
        self.assertEqual(self.registry.state['agents'][self.adapter.id]['installation']['home'], str(replacement))

    async def test_pi_prepare_activate_preserves_profile_and_waits_for_real_receipt(self):
        settings = self.profile / 'settings.json'
        settings.write_text('{"model":"unchanged","extensions":["other.ts"]}')
        before = settings.read_bytes()
        preview = self.adapter.preview()
        self.assertFalse(self.adapter.target.exists())
        prepared = await self.adapter.prepare(self.registry.state['revision'])
        self.assertFalse(self.adapter.target.exists())
        self.assertEqual(prepared['status'], 'prepared')
        self.assertEqual(prepared['preview']['baseline_sha256'], preview['baseline_sha256'])
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        verify_artifact(installation)
        root = Path(installation['plugin_dir'])
        common = (root / 'scripts/_plugin_common.py').read_text()
        self.assertIn('pi-' + self.adapter.id, common)
        self.assertNotIn('".cognee-plugin" / "codex"', common)
        self.assertEqual((self.plugin / 'scripts/_plugin_common.py').read_bytes().find(b'".cognee-plugin" / "codex"') >= 0, True)
        result = await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(settings.read_bytes(), before)
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertEqual(result['new_sessions'], 0)
        self.assertIn(str(root / 'extension/index.ts'), self.adapter.target.read_text())
        self.registry.state['sessions'][json.dumps([self.adapter.id, 'real-host-key'])] = 'project'
        self.assertEqual(self.adapter.view()['new_sessions'], 1)
        with self.assertRaises(PolicyError):
            await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])

    async def test_tampered_artifact_and_concurrent_config_edit_never_activate(self):
        prepared = await self.adapter.prepare(0)
        self.adapter.target.parent.mkdir(parents=True)
        self.adapter.target.write_text('unrelated-user-extension')
        with self.assertRaisesRegex(PolicyError, 'host_configuration_changed'):
            await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(self.adapter.target.read_text(), 'unrelated-user-extension')
        self.adapter.target.unlink()
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        (Path(installation['plugin_dir']) / 'scripts/agent_memory_adapter.py').write_text('tampered')
        with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
            await self.adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        # Updating the artifact's self-reported receipt cannot repair it.
        receipt = Path(installation['plugin_dir']) / '.memlane-project-access.json'
        data = json.loads(receipt.read_text())
        data['files']['scripts/agent_memory_adapter.py'] = sha(b'tampered')
        receipt.write_text(json.dumps(data))
        with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
            verify_artifact(installation)

    async def test_idempotent_management_operation_and_unknown_never_reexecutes(self):
        config = {**self.config, 'agent_hosts': {'pi': {'profile': str(self.profile), 'executable': sys.executable}}}
        connections = Connections(self.registry, config)
        first = await connections.perform(self.adapter.id, 'prepare', 0, 'prepare-one')
        second = await connections.perform(self.adapter.id, 'prepare', 0, 'prepare-one')
        self.assertEqual(first, second)
        self.assertEqual(self.registry.state['revision'], first['revision'])
        before = self.registry.path.read_bytes()
        self.assertEqual(connections.operation('prepare-one'), {'recorded': True, 'status': 'complete', 'result': first, 'error_code': None})
        self.assertEqual(connections.operation('not-registered'), {'recorded': False})
        self.assertEqual(self.registry.path.read_bytes(), before, 'reading original receipt changed registry')
        receipt = connections.operation('prepare-one')
        receipt['result']['connection']['status'] = 'tampered-copy'
        self.assertEqual(connections.operation('prepare-one')['result'], first)
        with self.assertRaisesRegex(PolicyError, 'operation_id_conflict'):
            await connections.perform(self.adapter.id, 'activate', first['revision'], 'prepare-one', 'wrong')
        self.registry.state['connection_operations']['unknown'] = {
            'signature': sha(json.dumps([self.adapter.id, 'activate', 'digest']).encode()), 'status': 'unknown'}
        with self.assertRaisesRegex(PolicyError, 'connection_operation_requires_review'):
            await connections.perform(self.adapter.id, 'activate', first['revision'], 'unknown', 'digest')
        self.assertEqual(connections.operation('unknown')['status'], 'unknown')

    async def test_original_connection_receipt_http_is_management_only_and_read_only(self):
        from aiohttp.test_utils import TestClient, TestServer
        from project_control_server import create_app
        config = {**self.config, 'agent_hosts': {'pi': {'profile': str(self.profile), 'executable': sys.executable}}}
        self.registry.state['connection_operations'] = {'prior-operation': {'status': 'unknown', 'signature': 'private-internal'}}
        self.registry.save()
        before = self.registry.path.read_bytes()
        client = TestClient(TestServer(create_app(self.registry, 'm'*40, {'a': 'a'*40}, codex_config=config)))
        await client.start_server()
        try:
            path = '/v1/connections/operations/prior-operation'
            self.assertEqual((await client.get(path)).status, 401)
            self.assertEqual((await client.get(path, headers={'Authorization': 'Bearer '+'a'*40})).status, 403)
            response = await client.get(path, headers={'Authorization': 'Bearer '+'m'*40})
            self.assertEqual(response.status, 200)
            data = await response.json()
            self.assertTrue(data['recorded'])
            self.assertEqual(data['status'], 'unknown')
            self.assertNotIn('private-internal', json.dumps(data))
            response = await client.get('/v1/connections/operations/absent', headers={'Authorization': 'Bearer '+'m'*40})
            self.assertEqual(await response.json(), {'recorded': False})
            self.assertEqual(self.registry.path.read_bytes(), before)
        finally:
            await client.close()

    async def test_codex_discovery_is_independent_of_provider_bridge_login_and_does_not_write_profile(self):
        from project_codex_connection import CodexConnection
        profile = self.workspace / 'not-yet-created-codex'
        config = {**self.config, 'generation_mode': 'provider', 'agent_hosts': {
            'codex': {'profile': str(profile), 'executable': sys.executable}}}
        before = json.loads(self.registry.path.read_text())
        connections = Connections(self.registry, config)
        row = next(r for r in connections.view()['connections'] if r['kind'] == 'codex')
        self.assertTrue(row['detected'])
        self.assertEqual(row['status'], 'environment_missing')
        self.assertEqual(row['codex_home'], str(profile.resolve()))
        self.assertFalse(profile.exists(), 'discovery created host configuration')
        self.assertNotIn('auth_home', config, 'Agent detection mutated bridge authentication config')
        self.assertNotIn('codex_executable', config)
        self.assertEqual(json.loads(self.registry.path.read_text()), before)
        legacy = CodexConnection(self.registry, {**self.config, 'auth_home': str(profile), 'codex_executable': sys.executable})
        self.assertEqual(row['instance_id'], legacy.id, 'same home changed the legacy Codex identity')
        missing = Connections(self.registry, {**config, 'agent_hosts': {'codex': {
            'profile': str(profile), 'executable': '/synthetic/unavailable-codex'}}})
        self.assertFalse(next(r for r in missing.view()['connections'] if r['kind']=='codex')['detected'])
        profile.write_text('not a profile directory')
        broken = Connections(self.registry, config).view()['connections']
        self.assertEqual(next(r for r in broken if r['kind']=='codex')['status'], 'environment_unavailable')
        self.assertTrue(any(r['kind']=='pi' for r in broken), 'bad Codex target hid independent Agent connections')

    async def test_select_environment_persists_without_installing_or_retaining_wrong_adapter(self):
        target = self.workspace / 'daily traex'
        target.mkdir()
        original = target / 'traecli.toml'
        original.write_text('[plugins.other]\nenabled = true\n')
        config = {**self.config, 'agent_hosts': {'traex': {
            'profile': str(self.workspace / 'missing verification'), 'executable': sys.executable}}}
        connections = Connections(self.registry, config)
        old = next(r for r in connections.view()['connections'] if r['kind'] == 'traex')
        result = await connections.select_environment('traex', str(target), self.registry.state['revision'])
        row = next(r for r in result['connections'] if r['kind'] == 'traex')
        self.assertNotEqual(row['instance_id'], old['instance_id'])
        self.assertEqual(row['profile'], str(target))
        self.assertEqual(row['status'], 'not_connected')
        self.assertEqual(original.read_text(), '[plugins.other]\nenabled = true\n')
        self.assertNotIn(row['instance_id'], self.registry.state['agents'])
        self.assertNotIn(old['instance_id'], connections.adapters)
        reopened = Connections(self.registry, config)
        row2 = next(r for r in reopened.view()['connections'] if r['kind'] == 'traex')
        self.assertEqual(row2['instance_id'], row['instance_id'])
        before = self.registry.path.read_bytes()
        with self.assertRaises(PolicyError):
            await connections.select_environment('traex', str(target), result['revision'] - 1)
        self.assertEqual(self.registry.path.read_bytes(), before)

    async def test_environment_selection_http_requires_manager_and_does_not_install(self):
        from aiohttp.test_utils import TestClient, TestServer
        from project_control_server import create_app
        target = self.workspace / 'real-profile'
        target.mkdir()
        client = TestClient(TestServer(create_app(self.registry, 'm'*40, {'a': 'a'*40}, codex_config=self.config)))
        await client.start_server()
        try:
            body = {'expected_revision': self.registry.state['revision'], 'kind': 'traex', 'profile': str(target)}
            denied = await client.post('/v1/connections/environment', json=body, headers={'Authorization': 'Bearer '+'a'*40})
            self.assertEqual(denied.status, 403)
            self.assertNotIn('host_targets', self.registry.state)
            response = await client.post('/v1/connections/environment', json=body, headers={'Authorization': 'Bearer '+'m'*40})
            self.assertEqual(response.status, 200)
            self.assertEqual(self.registry.state['host_targets']['traex'], str(target))
            self.assertFalse((target / 'traecli.toml').exists())
        finally:
            await client.close()

    async def test_waiting_receipt_explains_timeout_without_inventing_connection(self):
        connections = Connections(self.registry, {**self.config, 'agent_hosts': {'pi': {
            'profile': str(self.profile), 'executable': sys.executable}}})
        self.registry.state['connections'][self.adapter.id] = {
            'status': 'configured_pending_new_session', 'activated': True,
            'proposed_sha256': sha(b''), 'sessions_before_activation': [], 'activated_at': 1}
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertEqual(row['new_sessions'], 0)
        self.assertTrue(row['receipt_overdue'])
        self.assertEqual(row['last_receipt_at'], 0)

    async def test_host_upgrade_review_requires_fresh_receipt_without_reinstalling(self):
        from unittest.mock import patch
        connections = Connections(self.registry, {**self.config, 'agent_hosts': {'pi': {
            'profile': str(self.profile), 'executable': sys.executable}}})
        prepared = await connections.perform(self.adapter.id, 'prepare', 0, 'upgrade-prepare')
        await connections.perform(self.adapter.id, 'activate', self.registry.state['revision'],
            'upgrade-activate', prepared['connection']['preview']['proposed_sha256'])
        record = self.registry.state['connections'][self.adapter.id]
        record['executable_fingerprint'] = [0, 0, 0]
        record['activated_at'] = 1
        self.registry.state['sessions'][json.dumps([self.adapter.id, 'old-session'])] = 'project'
        self.registry.state['connection_receipts'] = {self.adapter.id: {
            'received_at': 2, 'environment_verified': True}}
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertEqual(row['error_code'], 'host_executable_changed')
        original = self.adapter.target.read_bytes()
        with patch.object(NativeConnection, 'prepare', side_effect=AssertionError('must not install')):
            await connections.perform(self.adapter.id, 'review', self.registry.state['revision'], 'upgrade-review')
        self.assertEqual(self.adapter.target.read_bytes(), original)
        row = next(r for r in connections.view()['connections'] if r['instance_id'] == self.adapter.id)
        self.assertFalse(row['executable_changed'])
        self.assertEqual(row['status'], 'environment_receipt_unconfirmed')

    async def test_helper_dedup_denied_watermark_and_unknown_enqueue_do_not_replay(self):
        await self.adapter.prepare(0)
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        script = r'''
import json, os, pathlib, sys, subprocess
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _coding_memory as coding
import _project_memory as projects
projects.begin = lambda *args: None
projects.prepare = lambda *args, **kwargs: {}
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy.current = lambda *args: {"ready": True, "policy":{"permissions":["read","write","delete"]}}
allowed = [True]
policy.permit = lambda *args, **kwargs: allowed[0]
coding._drain = lambda *args: None
delivered = []
coding._deliver = lambda *args: delivered.extend(args[-1])
request = {"operation":"capture", "session_id":"synthetic", "cwd":os.environ["HOME"],
           "event_id":"first", "entry":{"type":"qa","question":"synthetic prompt","answer":"synthetic answer"}}
os.environ["MEMLANE_PROCESSING_SESSION"] = "1"
assert memory.handle(request)["status"] == "skipped_processing_session"
os.environ.pop("MEMLANE_PROCESSING_SESSION")
def git(*args):
    return subprocess.check_output(["git", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.com", *args], cwd=os.environ["HOME"], stderr=subprocess.DEVNULL, text=True).strip()
git("init", "-q")
git("commit", "--allow-empty", "-qm", "before")
before = git("rev-parse", "HEAD")
memory.handle({**request,"operation":"resolve","event_id":"turn-one"})
git("commit", "--allow-empty", "-qm", "after")
after = git("rev-parse", "HEAD")
assert memory.handle(request)["status"] == "buffered"
context = json.loads(delivered[0]["context"])
assert context["start_commit"] == before and context["end_commit"] == after and before != after
assert context["cwd"] == os.environ["HOME"] and context["session_cwd"] == os.environ["HOME"]
assert "transcript_path" not in context
assert memory.handle(request)["duplicate"]
assert len(delivered) == 1
assert os.environ["COGNEE_API_KEY"] == "synthetic-key-credential"
original_policy = policy.current
policy.current = lambda *args, **kwargs: {"ready": True, "policy":{"permissions":["read"]}}
for operation, extra, permission in [("remember", {"content":"denied synthetic"}, "write"), ("forget", {"data_id":"00000000-0000-0000-0000-000000000002"}, "delete")]:
    assert memory.execute({**request, "operation":operation, **extra}) == {"status":"denied", "http_status":403, "error":"project_"+permission+"_denied"}
def unavailable(*args, **kwargs): raise policy.NotReady("synthetic policy unavailable")
policy.current = unavailable
assert memory.execute({**request,"operation":"remember","content":"must remain unknown"}) == {"status":"unavailable","error":"memory_operation_unconfirmed"}
policy.current = original_policy
trace = {**request, "event_id":"tool-one", "entry":{"type":"trace", "origin_function":"bash",
         "method_params":{"command":"printf synthetic"}, "method_return_value":{"text":"synthetic"},
         "status":"success", "time":"2026-11-14T00:00:00Z"}}
assert memory.handle(trace)["status"] == "buffered"
assert delivered[-1]["method_params"] == {"command":"printf synthetic"}
assert delivered[-1]["generate_feedback_with_llm"] is False
assert isinstance(delivered[-1]["method_return_value"], str)
delivered.pop()
allowed[0] = False
memory.handle({**request,"operation":"resolve","event_id":"turn-denied"})
assert "host_turn_context" not in coding.read_state(memory.setup(request)[0])
request["event_id"] = "denied"
assert memory.handle(request)["status"] == "skipped_policy"
allowed[0] = True
assert memory.handle(request)["status"] == "skipped_policy"
assert len(delivered) == 1
def unknown(*args): raise RuntimeError("synthetic enqueue outcome unknown")
coding._deliver = unknown
request["event_id"] = "unknown"
try: memory.handle(request)
except RuntimeError: pass
else: raise AssertionError("enqueue should be unknown")
assert memory.handle(request)["status"] == "enqueue_unknown"
key, _, _ = memory.setup(request)
state = coding.read_state(key)
assert "synthetic answer" not in json.dumps(state)
assert "synthetic prompt" not in json.dumps(state)
assert len(state["host_events"]) == 4
import _plugin_common as common
submitted = []
common.run_session_improve_detailed = lambda *args, **kwargs: submitted.append(kwargs["trigger"]) or {"ok":True}
flush = {"operation":"flush", "session_id":"synthetic", "cwd":os.environ["HOME"], "final":True}
assert memory.handle(flush)["status"] == "processing_submitted"
assert memory.handle(flush)["status"] == "no_new_final_material"
assert submitted == ["final"]
print("helper metadata watermark and private binding passed")
'''
        environment = {key: value for key, value in __import__('os').environ.items()
                       if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        environment.update(HOME=str(self.workspace), COGNEE_API_KEY='must-not-use-ambient-owner')
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                             str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])
        self.assertNotIn('must-not-use-ambient-owner', process.stdout)

    async def test_real_traex_installer_uses_isolated_user_profile(self):
        executable = shutil.which('traecli')
        if not executable:
            self.skipTest('TraeX host not installed')
        from unittest.mock import patch
        profile = self.workspace / 'traex-profile'
        profile.mkdir()
        adapter = NativeConnection(self.registry, self.config, 'traex', profile, executable)
        self.registry.state['agents'][adapter.id] = {
            'name': 'TraeX', 'key_file': str(self.workspace / 'key'), 'control_token_file': str(self.workspace / 'control')}
        prepared = await adapter.prepare(0)
        with patch.dict(os.environ, {'HOME': str(self.workspace)}):
            result = await adapter.activate(self.registry.state['revision'], prepared['preview']['proposed_sha256'])
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertTrue((profile / 'traecli.toml').is_file(), 'host ignored configured profile')
        text = (profile / 'traecli.toml').read_text()
        self.assertIn('memlane-cognee', text)
        self.assertNotIn('bypass', text)
        self.assertFalse((self.workspace / '.trae/traecli.toml').exists(), 'host wrote to an unexpected default profile')
        installation = self.registry.state['agents'][adapter.id]['installation']
        cache = adapter.traex_installed(installation)
        verify_artifact({**installation, 'plugin_dir': str(cache)})
        self.assertEqual(json.loads((cache / '.codex-plugin/plugin.json').read_text())['name'], 'memlane-cognee')
        self.assertIn('+memlane.', cache.name)
        # An interrupted manager can establish the original result by reading
        # the complete native receipt; review must never run the installer.
        self.registry.state['connections'][adapter.id]['status'] = 'activation_unknown'
        with patch('agent_connections.subprocess.run', side_effect=AssertionError('review replayed installation')):
            reviewed = await adapter.review(self.registry.state['revision'])
        self.assertEqual(reviewed['status'], 'configured_pending_new_session')
        original = (cache / 'hooks.json').read_bytes()
        (cache / 'hooks.json').write_bytes(b'{}')
        with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
            adapter.view()
        (cache / 'hooks.json').write_bytes(original)
        # Same instance, explicitly activated new generation: native versioned
        # cache must preserve the old files for an already-running host.
        adapter.config = {**self.config, 'python': '/usr/bin/python3'}
        second = await adapter.prepare(self.registry.state['revision'])
        with patch.dict(os.environ, {'HOME': str(self.workspace)}):
            await adapter.activate(self.registry.state['revision'], second['preview']['proposed_sha256'])
        newer = self.registry.state['agents'][adapter.id]['installation']
        self.assertNotEqual(adapter.traex_installed(newer), cache)
        verify_artifact({**installation, 'plugin_dir': str(cache)})
        adapter.target.write_text(text.replace('enabled = true', 'enabled = false'))
        with self.assertRaisesRegex(PolicyError, 'traex_installation_requires_review'):
            adapter.traex_installed(installation)

    async def test_real_import_order_initializes_matching_official_write_and_recall_scope(self):
        await self.adapter.prepare(0)
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        script = r'''
import json, os, sys
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _project_memory as projects
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy.permit = lambda *args, **kwargs: True
# Only the server capability answer is synthetic; official begin/route and
# production import order stay intact, unlike prematurely importing coding.
def prepare(dataset, session, **kwargs):
    from _plugin_common import _load_json_file, _write_json_file
    path = projects._path(dataset, session)
    state = _load_json_file(path)
    assert state["node_set"]
    state.update(pending=False, identity=projects._identity())
    _write_json_file(path, state)
    return state
projects.prepare = prepare
key, session, dataset = memory.setup({"operation":"status", "session_id":"scope-fixture", "cwd":os.environ["HOME"]})
import _coding_memory as coding
import _plugin_common as common
expected = [projects.project_tag(os.environ["HOME"], "auto")]
assert coding.project_scope(dataset, session, os.environ["HOME"]) == expected
requests = []
common.require_typed_dataset_id_support = lambda: None
common._json_http_request = lambda path, payload, **kwargs: requests.append(payload) or {}
common.remember_entry_via_http(dataset, session, {"type":"qa", "question":"synthetic", "answer":"synthetic"})
assert requests[-1]["entry"]["node_set"] == expected
assert coding._project_ok({"project_node_sets":expected}, expected)
assert not coding._project_ok({"project_node_sets":[]}, expected)
print("official capture/recall project scope matches; old unscoped rows unchanged")
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])

    async def test_traex_explicit_skill_resolves_hook_binding_with_shared_cwd(self):
        private_write(self.workspace / 'control', b'fixture-control-token-with-valid-length-0001')
        from project_installation import prepare
        installation = prepare(self.plugin, self.registry.path.parent, 'synthetic-traex',
            self.registry.state['agents'][self.adapter.id], 'http://127.0.0.1:18999', 'http://127.0.0.1:18998',
            str(self.workspace), host_kind='traex', python=sys.executable)
        script = r'''
import os, sys
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _project_memory as projects
dataset = "00000000-0000-0000-0000-000000000001"
decision = {"ready":True, "revision":1, "project_id":"fixture-project", "dataset_id":dataset,
            "policy":{"permissions":["read","write","delete"], "can_capture":True, "can_auto_process":False}}
policy._request = lambda *a, **kw: decision
projects.prepare = lambda *a, **kw: {}
key, session, _ = memory.setup({"operation":"status", "session_id":"11111111-1111-4111-8111-111111111111", "cwd":os.environ["HOME"]})
import _plugin_common as common
# Several hook launches at one cwd must never make a shell skill guess a session.
common.ensure_launch_record("other-host", os.environ["HOME"], dataset=dataset)
os.environ.pop("COGNEE_SESSION_KEY", None)
os.environ["TRAECLI_THREAD_ID"] = "11111111-1111-4111-8111-111111111111"
import _env_file
_env_file._loaded = False
_env_file.load_env_file()
resolved, reason = common.resolve_host_key_outside_hook()
assert resolved == key, (resolved, reason)
assert policy.dataset() == dataset
assert policy.permit("read")
print("explicit skill and hooks resolve one binding without cwd fallback")
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(
            ('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_', 'TRAECLI_', 'CODEX_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])

    async def test_traex_payloads_pair_prompt_answer_and_never_read_transcript(self):
        from project_installation import prepare
        installation = prepare(self.plugin, self.registry.path.parent, 'synthetic-traex',
            self.registry.state['agents'][self.adapter.id], 'http://127.0.0.1:18999', 'http://127.0.0.1:18998',
            str(self.workspace), host_kind='traex', python=sys.executable)
        script = r'''
import json, os, sys
sys.path.insert(0, sys.argv[1])
import traex_hook as hook
import agent_memory_adapter as memory
import _memlane_policy as policy
import _project_memory as projects
projects.begin = lambda *args: None
projects.prepare = lambda *args, **kwargs: {}
os.environ["MEMLANE_PROCESSING_SESSION"] = "1"
assert hook.handle("SessionStart", {}) == {}
os.environ.pop("MEMLANE_PROCESSING_SESSION")
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy.permit = lambda *args, **kwargs: True
calls = []
def operation(request):
    calls.append(request)
    return {"context":"synthetic authorized context", "status":"buffered"}
memory.handle = operation
payload = {"session_id":"host-session", "cwd":os.environ["HOME"], "turn_id":"host-turn", "prompt":"synthetic prompt"}
assert hook.handle("UserPromptSubmit", payload)["hookSpecificOutput"] == {"hookEventName":"UserPromptSubmit", "additionalContext":"synthetic authorized context"}
assert hook.handle("Stop", {**payload,"transcript_path":"/never-open-this"})["status"] == "missing_answer"
assert len(calls) == 1
hook.handle("Stop", {**payload,"last_assistant_message":"synthetic answer"})
qa = next(c for c in calls if c["operation"] == "capture")
assert qa["entry"]["question"] == "synthetic prompt"
assert qa["entry"]["answer"] == "synthetic answer"
assert qa["entry"]["time"] and qa["entry"]["prompt_time"]
assert calls[-1]["operation"] == "flush"
hook.handle("PostToolUseFailure", {**payload,"tool_use_id":"native-tool-id","tool_name":"bash","tool_input":{"command":"synthetic"},"tool_response":{"exit_code":1}})
assert calls[-1]["entry"]["status"] == "error"
assert calls[-1]["event_id"] == "tool:native-tool-id"
assert calls[-1]["entry"]["time"]
for index in range(2):
    no_turn = {"session_id":"host-session", "cwd":os.environ["HOME"], "prompt":"another synthetic prompt"}
    hook.handle("UserPromptSubmit", no_turn)
    hook.handle("Stop", {**no_turn,"last_assistant_message":"another synthetic answer"})
qas = [c for c in calls if c["operation"] == "capture" and c["entry"]["type"] == "qa"]
assert len({c["event_id"] for c in qas}) == 3
from datetime import datetime
assert all(datetime.fromisoformat(c["entry"]["prompt_time"]).tzinfo for c in qas)
assert all("transcript_path" not in c for c in calls)
print("TraeX event adaptation passed; no transcript access")
'''
        environment = {key: value for key, value in os.environ.items() if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        environment['HOME'] = str(self.workspace)
        process = await asyncio.to_thread(subprocess.run, [sys.executable, '-c', script,
                                          str(Path(installation['plugin_dir']) / 'scripts')],
                                          env=environment, cwd=self.workspace, capture_output=True, text=True, timeout=20)
        self.assertEqual(process.returncode, 0, process.stderr[-3000:])


if __name__ == '__main__':
    unittest.main()
