import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from project_control import private_write
from project_host_plugin import files_for
from project_installation import prepare
from agent_connections import NativeConnection, verify_artifact
from project_control import Registry
from unittest.mock import patch
import shutil


class HostPluginTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        private_write(self.root / 'child.key', b'synthetic-child-key')
        private_write(self.root / 'control.key', b'synthetic-control-key')
        self.agent = {'key_file': str(self.root / 'child.key'), 'control_token_file': str(self.root / 'control.key')}

    def tearDown(self):
        self.temp.cleanup()

    def artifact(self, kind):
        return prepare(None, self.root / 'private', kind + '-synthetic', self.agent,
                       'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(self.root),
                       host_kind=kind, python=sys.executable)

    def isolated(self, installation, source):
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(HOME=str(self.root), COGNEE_PLUGIN_IN_VENV='1', COGNEE_SESSION_KEY='synthetic-host')
        result = subprocess.run([sys.executable, '-c', source, str(Path(installation['plugin_dir']) / 'scripts')],
                                env=env, cwd=self.root, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr[-3000:])

    def test_independent_pinned_host_artifacts_are_immutable_and_private(self):
        for kind in ('claude_code', 'cursor'):
            installation = self.artifact(kind)
            verify_artifact(installation)
            root = Path(installation['plugin_dir'])
            self.assertEqual((root / '.memlane-installation.json').stat().st_mode & 0o777, 0o600)
            self.assertEqual(Path(installation['env_file']).stat().st_mode & 0o777, 0o600)
            self.assertIn(kind + '-' + kind + '-synthetic', (root / 'scripts/_plugin_common.py').read_text())
            self.assertEqual(self.artifact(kind)['plugin_dir'], str(root))
            self.assertFalse((self.root / '.claude/settings.json').exists())
            self.assertFalse((self.root / '.cursor/plugins').exists())
        bad = self.root / 'wrong-archive'
        bad.write_bytes(b'not the pinned archive')
        with self.assertRaisesRegex(ValueError, 'fixed official host archive mismatch'):
            files_for('claude_code', bad)

    def test_claude_parser_respects_boundary_and_never_scans_sidechain_history(self):
        installation = self.artifact('claude_code')
        self.isolated(installation, r'''
import json, pathlib, sys
sys.path.insert(0, sys.argv[1])
import _coding_memory as coding
import _memlane_policy as policy
policy.managed = lambda: True
path = pathlib.Path.home() / "synthetic.jsonl"
old = json.dumps({"type":"user","uuid":"old","message":{"content":"forbidden old prompt"}}) + "\n"
partial = '{"type":"user","message":{"content":"disabled partial'
new = [
 {"type":"user","uuid":"new","message":{"content":"allowed new prompt"}},
 {"type":"assistant","uuid":"a","message":{"content":[{"type":"text","text":"allowed new answer"}]}},
]
path.write_text(old + partial + ' record"}}\n' + ''.join(json.dumps(row)+"\n" for row in new))
def forbidden(*args): raise AssertionError("scanned unrelated transcript history")
coding._claude_files = forbidden
parsed = coding.parse_claude(str(path), len((old + partial).encode()))
assert parsed["request"] == "allowed new prompt", parsed
assert parsed["answer"] == "allowed new answer", parsed
assert "forbidden" not in json.dumps(parsed)
assert "disabled" not in json.dumps(parsed)
''')

    def test_cursor_deferred_injection_rebuilds_and_missing_answer_never_reads_history(self):
        installation = self.artifact('cursor')
        self.isolated(installation, r'''
import pathlib, sys
sys.path.insert(0, sys.argv[1])
import cursor_hook as cursor
import _coding_memory as coding
import _memlane_policy as policy
policy.current = lambda **kwargs: {"revision":1,"policy":{"permissions":[]},"common":{}}
assert coding.build_block("query", str(pathlib.Path.home())) == ""
root = pathlib.Path.home() / "held-context"
coding.build_block = lambda query,cwd: "fresh-authorized:" + query
cursor.hold_prompt_context("host", "turn1", "session-context-lookup.py", "stale-block", root, query="new query", cwd=str(pathlib.Path.home()))
assert cursor.take_prompt_context("host", "turn1", root) == "fresh-authorized:new query"
cursor.hold_prompt_context("host", "turn2", "session-context-lookup.py", "must not leak", root, query="denied query", cwd=str(pathlib.Path.home()))
coding.build_block = lambda *args: ""
assert cursor.take_prompt_context("host", "turn2", root) == ""
assert cursor.read_transcript_tail("/never-read") == []
''')


class HostInstallationTests(unittest.IsolatedAsyncioTestCase):
    async def test_cursor_prepare_is_readonly_then_explicit_activation_copies_verified_plugin(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            registry = Registry(root / 'private/registry.json', type('Official', (), {'url': 'http://127.0.0.1:18999'})())
            try:
                for name in ('key', 'control'):
                    private_write(root / name, ('synthetic-' + name).encode())
                config = {'python': sys.executable, 'agent_host_home': str(root),
                          'plugin_dir': 'unused-codex', 'project_control_url': 'http://127.0.0.1:18998',
                          'agent_hosts': {'claude_code': {'profile': str(root / 'empty-claude')}}}
                connection = NativeConnection(registry, config, 'cursor', root / 'cursor', sys.executable)
                registry.state['agents'][connection.id] = {'name': 'Cursor', 'key_file': str(root / 'key'), 'control_token_file': str(root / 'control')}
                result = await connection.prepare(0)
                self.assertFalse(connection.target.exists())
                result = await connection.activate(registry.state['revision'], result['preview']['proposed_sha256'])
                self.assertEqual(result['status'], 'configured_pending_new_session')
                self.assertEqual(result['new_sessions'], 0)
                self.assertTrue((connection.target.parent / '.cursor-plugin/plugin.json').is_file())
                self.assertEqual(connection.preview()['instance_id'], connection.id)
            finally:
                registry.close()

    async def test_real_claude_native_installer_only_updates_isolated_profile(self):
        executable = shutil.which('claude')
        if not executable:
            self.skipTest('Claude Code not installed')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            registry = Registry(root / 'private/registry.json', type('Official', (), {'url': 'http://127.0.0.1:18999'})())
            try:
                for name in ('key', 'control'):
                    private_write(root / name, ('synthetic-' + name).encode())
                config = {'python': sys.executable, 'agent_host_home': str(root),
                          'plugin_dir': 'unused-codex', 'project_control_url': 'http://127.0.0.1:18998'}
                connection = NativeConnection(registry, config, 'claude_code', root / 'claude-profile', executable)
                registry.state['agents'][connection.id] = {'name': 'Claude', 'key_file': str(root / 'key'), 'control_token_file': str(root / 'control')}
                prepared = await connection.prepare(0)
                self.assertFalse(connection.target.exists())
                with patch.dict(os.environ, {'HOME': str(root)}):
                    result = await connection.activate(registry.state['revision'], prepared['preview']['proposed_sha256'])
                self.assertEqual(result['status'], 'configured_pending_new_session')
                settings = json.loads(connection.target.read_text())
                self.assertTrue(any(name.startswith('cognee@memlane-') and enabled for name, enabled in settings['enabledPlugins'].items()))
                self.assertFalse((root / '.claude/settings.json').exists())
            finally:
                registry.close()


if __name__ == '__main__':
    unittest.main()
