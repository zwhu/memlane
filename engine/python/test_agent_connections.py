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
        with self.assertRaisesRegex(PolicyError, 'operation_id_conflict'):
            await connections.perform(self.adapter.id, 'activate', first['revision'], 'prepare-one', 'wrong')
        self.registry.state['connection_operations']['unknown'] = {
            'signature': sha(json.dumps([self.adapter.id, 'activate', 'digest']).encode()), 'status': 'unknown'}
        with self.assertRaisesRegex(PolicyError, 'connection_operation_requires_review'):
            await connections.perform(self.adapter.id, 'activate', first['revision'], 'unknown', 'digest')

    async def test_helper_dedup_denied_watermark_and_unknown_enqueue_do_not_replay(self):
        await self.adapter.prepare(0)
        installation = self.registry.state['agents'][self.adapter.id]['installation']
        script = r'''
import json, os, pathlib, sys
sys.path.insert(0, sys.argv[1])
import agent_memory_adapter as memory
import _memlane_policy as policy
import _coding_memory as coding
policy.bind = lambda *args: "00000000-0000-0000-0000-000000000001"
policy.current = lambda *args: {"ready": True, "policy":{"permissions":["read","write","delete"]}}
allowed = [True]
policy.permit = lambda *args, **kwargs: allowed[0]
coding._drain = lambda *args: None
delivered = []
coding._deliver = lambda *args: delivered.extend(args[-1])
request = {"operation":"capture", "session_id":"synthetic", "cwd":os.environ["HOME"],
           "event_id":"first", "entry":{"type":"qa","question":"synthetic prompt","answer":"synthetic answer"}}
assert memory.handle(request)["status"] == "buffered"
assert memory.handle(request)["duplicate"]
assert len(delivered) == 1
assert os.environ["COGNEE_API_KEY"] == "synthetic-key-credential"
trace = {**request, "event_id":"tool-one", "entry":{"type":"trace", "origin_function":"bash",
         "method_params":{"command":"printf synthetic"}, "method_return_value":{"text":"synthetic"},
         "status":"success", "time":"2026-11-14T00:00:00Z"}}
assert memory.handle(trace)["status"] == "buffered"
assert delivered[-1]["method_params"] == {"command":"printf synthetic"}
assert delivered[-1]["generate_feedback_with_llm"] is False
assert isinstance(delivered[-1]["method_return_value"], str)
delivered.pop()
allowed[0] = False
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
assert calls[-1]["operation"] == "flush"
hook.handle("PostToolUseFailure", {**payload,"tool_use_id":"native-tool-id","tool_name":"bash","tool_input":{"command":"synthetic"},"tool_response":{"exit_code":1}})
assert calls[-1]["entry"]["status"] == "error"
assert calls[-1]["event_id"] == "tool:native-tool-id"
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
