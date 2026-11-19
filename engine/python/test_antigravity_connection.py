"""Pinned native host compatibility and isolation, without a model or daily data."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import shutil
import asyncio
import types
from unittest.mock import patch

from agent_connections import detect_executable, verify_artifact
from antigravity_plugin import files_for
from antigravity_connection import AntigravityConnection
from project_control import private_write, Registry
from project_installation import prepare
from project_policy import PolicyError


class AntigravityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        private_write(self.root / 'child.key', b'synthetic-child-key')
        private_write(self.root / 'control.key', b'synthetic-control-key-' * 3)

    def tearDown(self):
        self.temp.cleanup()

    def artifact(self):
        agent = {'key_file': str(self.root / 'child.key'), 'control_token_file': str(self.root / 'control.key')}
        value = prepare(None, self.root / 'private', 'antigravity-synthetic', agent,
                        'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(self.root),
                        host_kind='antigravity', python=sys.executable)
        marker = Path(value['plugin_dir']) / '.memlane-installation.json'
        data = json.loads(marker.read_text())
        data.update(runtime_profile=str(self.root / '.gemini/config'), activated_at=1)
        private_write(marker, json.dumps(data).encode())
        return value

    def isolated(self, source):
        installation = self.artifact()
        verify_artifact(installation)
        env = {k: v for k, v in os.environ.items() if not k.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(HOME=str(self.root), COGNEE_SESSION_KEY='synthetic-host', COGNEE_PLUGIN_IN_VENV='1')
        result = subprocess.run([sys.executable, '-c', source, str(Path(installation['plugin_dir']) / 'scripts')],
                                env=env, cwd=self.root, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr[-3000:])

    def test_pinned_archive_and_official_hooks_remain_native(self):
        files, upstream = files_for()
        self.assertEqual(upstream['host_kind'], 'antigravity')
        hooks = json.loads(files['hooks.json'])
        self.assertEqual(set(hooks), {'cognee-bootstrap', 'cognee-recall', 'cognee-capture', 'cognee-stop'})
        self.assertIn('Stop', hooks['cognee-stop'])
        self.assertIn('PostToolUse', hooks['cognee-capture'])
        bad = self.root / 'bad-archive'
        bad.write_bytes(b'wrong-source')
        with self.assertRaisesRegex(ValueError, 'archive mismatch'):
            files_for(bad)

    def test_official_shared_modules_import_with_private_environment(self):
        self.isolated('''
import sys, os
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _coding_memory as coding
import _memlane_policy as policy
from config import load_config
value = load_config()
assert value['base_url'] == 'http://127.0.0.1:18999', value
assert value['api_key'] == 'synthetic-child-key'
assert common.plugin_identity_mode(value) == 'disabled'
policy.permit = lambda *a, **kw: False
assert coding.build_block('denied query', '/') == ''
assert common.run_session_improve('synthetic', 'synthetic') is False
''')

    def test_actual_activation_publishes_same_boundary_to_source_and_native(self):
        installation = self.artifact()
        marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
        value = json.loads(marker.read_text()); value['activated_at'] = 0
        private_write(marker, json.dumps(value).encode())
        profile = self.root / '.gemini/config'; profile.mkdir(parents=True)
        private_write(profile / 'config.json', json.dumps({'plugins': {'other': {'enabled': True}}}).encode())
        registry = Registry(self.root / 'registry.json', object())
        connection = AntigravityConnection(registry, {}, profile, '/synthetic/agy')
        registry.state['agents'][connection.id] = {'installation': installation}
        native = profile / 'plugins/memlane-cognee'
        with patch('antigravity_connection.Path.home', return_value=self.root):
            preview = connection.preview()
        registry.state.setdefault('connections', {})[connection.id] = {'status': 'prepared', 'preview': preview,
            'proposed_sha256': installation['manifest_sha256']}
        def command(args, **kwargs):
            if args[2] == 'install':
                self.assertEqual(json.loads(marker.read_text())['activated_at'], 0)
                native.parent.mkdir(); shutil.copytree(installation['plugin_dir'], native)
            if args[2] == 'enable':
                self.assertEqual(json.loads((native / '.memlane-installation.json').read_text())['activated_at'], 0)
                private_write(profile / 'config.json', json.dumps({'plugins': {'other': {'enabled': True}, native.name: {'enabled': True}}}).encode())
            return types.SimpleNamespace(returncode=0)
        try:
            with patch('antigravity_connection.Path.home', return_value=self.root),                     patch('antigravity_connection.subprocess.run', side_effect=command),                     patch('antigravity_connection.time.time', return_value=222):
                asyncio.run(connection.activate(registry.state['revision'], installation['manifest_sha256']))
            self.assertEqual(json.loads(marker.read_text())['activated_at'], 222)
            self.assertEqual(marker.read_bytes(), (native / '.memlane-installation.json').read_bytes())
            self.assertEqual(registry.state['connections'][connection.id]['activated_at'], 222)
        finally:
            registry.close()

    def test_partial_activation_marker_write_recovers_without_reinstall(self):
        installation = self.artifact()
        marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
        value = json.loads(marker.read_text()); value['activated_at'] = 0
        private_write(marker, json.dumps(value).encode())
        profile = self.root / '.gemini/config'; profile.mkdir(parents=True)
        private_write(profile / 'config.json', json.dumps({'plugins': {'other': {'enabled': True}}}).encode())
        registry = Registry(self.root / 'registry.json', object())
        connection = AntigravityConnection(registry, {}, profile, '/synthetic/agy')
        registry.state['agents'][connection.id] = {'installation': installation}
        native = profile / 'plugins/memlane-cognee'
        with patch('antigravity_connection.Path.home', return_value=self.root):
            preview = connection.preview()
        registry.state.setdefault('connections', {})[connection.id] = {'status': 'prepared', 'preview': preview,
            'proposed_sha256': installation['manifest_sha256']}
        def command(args, **kwargs):
            if args[2] == 'install':
                self.assertEqual(json.loads(marker.read_text())['activated_at'], 0)
                native.parent.mkdir(); shutil.copytree(installation['plugin_dir'], native)
            if args[2] == 'enable':
                self.assertEqual(json.loads((native / '.memlane-installation.json').read_text())['activated_at'], 0)
                private_write(profile / 'config.json', json.dumps({'plugins': {'other': {'enabled': True}, native.name: {'enabled': True}}}).encode())
            return types.SimpleNamespace(returncode=0)
        try:
            with patch('antigravity_connection.Path.home', return_value=self.root),                     patch('antigravity_connection.subprocess.run', side_effect=command),                     patch('antigravity_connection.time.time', return_value=222):
                failed = [False]
                def interrupted(path, raw):
                    if Path(path) == native / '.memlane-installation.json' and not failed[0]:
                        failed[0] = True
                        raise OSError('synthetic binding write interrupted')
                    private_write(path, raw)
                with patch('antigravity_connection.private_write', side_effect=interrupted):
                    with self.assertRaisesRegex(OSError, 'binding write interrupted'):
                        asyncio.run(connection.activate(registry.state['revision'], installation['manifest_sha256']))
                self.assertNotEqual(marker.read_bytes(), (native / '.memlane-installation.json').read_bytes())
                asyncio.run(connection.review(registry.state['revision']))
            self.assertEqual(json.loads(marker.read_text())['activated_at'], 222)
            self.assertEqual(marker.read_bytes(), (native / '.memlane-installation.json').read_bytes())
            self.assertEqual(registry.state['connections'][connection.id]['activated_at'], 222)
        finally:
            registry.close()

    def test_prepared_marker_does_not_admit_new_conversations(self):
        self.isolated('''
import sys, json, pathlib, uuid
sys.path.insert(0, sys.argv[1])
import _memlane_antigravity as managed
from _memlane_policy import NotReady
marker = pathlib.Path(sys.argv[1]).parent / '.memlane-installation.json'
value = json.loads(marker.read_text()); value['activated_at'] = 0; marker.write_text(json.dumps(value))
try:
    managed.prepare_payload({'conversationId':str(uuid.uuid4())}, 'store-user-prompt.py')
except NotReady as error:
    assert str(error) == 'installation_not_activated', error
else:
    raise AssertionError('prepared but inactive plugin admitted a conversation')
''')

    def test_old_transcript_is_rejected_before_normalization_or_network(self):
        self.isolated('''
import sys, json, pathlib, time, uuid
sys.path.insert(0, sys.argv[1])
import _memlane_antigravity as managed
from _memlane_policy import NotReady
native = str(uuid.uuid4())
path = pathlib.Path.home() / native / '.system_generated/logs/transcript.jsonl'
path.parent.mkdir(parents=True)
path.write_text('old material must not be read')
marker = pathlib.Path(sys.argv[1]).parent / '.memlane-installation.json'
value = json.loads(marker.read_text())
value['activated_at'] = time.time() + 60
marker.write_text(json.dumps(value))
try:
    managed.prepare_payload({'conversationId':native, 'transcriptPath':str(path), 'workspacePaths':[str(pathlib.Path.home())]}, 'store-user-prompt.py')
except NotReady as error:
    assert str(error) == 'existing_conversation_not_captured', error
else:
    raise AssertionError('accepted old material')
''')

    def test_app_only_runtime_discovery_and_explicit_path_precedence(self):
        runtime = self.root / 'Applications/Gemini.app/Contents/Helpers/agy'
        runtime.parent.mkdir(parents=True)
        runtime.write_text('# nonexecuted synthetic runtime')
        runtime.chmod(0o700)
        with patch('agent_connections.Path.home', return_value=self.root), \
             patch('agent_connections.shutil.which', return_value=None), \
             patch('pathlib.Path.is_file', autospec=True, side_effect=lambda p: p == runtime):
            self.assertEqual(detect_executable('antigravity', {}), str(runtime))
            self.assertIsNone(detect_executable('antigravity', {'executable': str(self.root / 'missing')}))

    def test_native_installation_receipt_accepts_only_registered_artifact(self):
        installation = self.artifact()
        profile = self.root / '.gemini/config'
        native = profile / 'plugins/memlane-cognee'
        shutil.copytree(installation['plugin_dir'], native)
        registry = Registry(self.root / 'registry.json', object())
        try:
            identifier = 'antigravity-synthetic'
            registry.state['agents'][identifier] = {'kind': 'antigravity', 'host_profile': str(profile),
                                                     'installation': installation}
            registry.state.setdefault('connections', {})[identifier] = {'native_plugin_dir': str(native)}
            payload = {'instance_id': identifier, 'plugin_path': str(native), 'plugin_version': '1.6.8',
                       'runtime_profile': str(profile)}
            result = registry.check_host_environment(identifier, payload)
            self.assertTrue(result['environment_verified'])
            self.assertTrue(result['artifact_verified'])
            with self.assertRaises(PolicyError):
                registry.check_host_environment(identifier, {**payload, 'plugin_path': str(profile / 'plugins/other')})
        finally:
            registry.close()


if __name__ == '__main__':
    unittest.main()
