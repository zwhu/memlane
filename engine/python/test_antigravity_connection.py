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

from agent_connections import detect_executable, verify_artifact, sha
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

    def test_native_transcript_names_preserve_identity_and_old_session_guards(self):
        self.isolated('''
import sys, os, json, pathlib, time, uuid, hashlib
sys.path.insert(0, sys.argv[1])
import _memlane_antigravity as managed
import _memlane_policy as policy
import _plugin_common as common
import _project_memory as memory
marker = pathlib.Path(sys.argv[1]).parent / '.memlane-installation.json'
binding = json.loads(marker.read_text())
calls = []
policy.bind = lambda cwd, host: calls.append((cwd, host)) or 'dataset'
common.ensure_launch_record = lambda *a, **k: ('session', {})
common.mark_server_ready = lambda *a, **k: None
memory.begin = lambda *a, **k: None
memory.prepare = lambda *a, **k: None
for case in ('standard', 'full', 'other_name', 'wrong_uuid', 'relative', 'file_symlink', 'parent_symlink', 'old_standard', 'old_full', 'no_workspace'):
    native = str(uuid.uuid4())
    home = pathlib.Path.home()
    directory = home / native / '.system_generated/logs'
    directory.mkdir(parents=True)
    name = 'transcript_full.jsonl' if case in ('full', 'old_full') else 'transcript.jsonl'
    path = directory / name
    path.write_text('metadata-only fixture')
    binding['activated_at'] = time.time() + 60 if case.startswith('old_') else 1
    marker.write_text(json.dumps(binding))
    expected = None
    if case == 'other_name':
        path = directory / 'arbitrary.jsonl'; path.write_text('metadata-only fixture')
        expected = 'native_transcript_identity_required'
    elif case == 'wrong_uuid':
        native = str(uuid.uuid4()); expected = 'native_transcript_identity_required'
    elif case == 'relative':
        path = path.relative_to(home); expected = 'native_transcript_identity_required'
    elif case == 'file_symlink':
        path.unlink(); target = directory / 'actual'; target.write_text('metadata-only fixture'); path.symlink_to(target)
        expected = 'native_transcript_identity_required'
    elif case == 'parent_symlink':
        directory = home / native / 'linked'; directory.symlink_to(path.parent, target_is_directory=True); path = directory / name
        expected = 'native_transcript_identity_required'
    elif case.startswith('old_'):
        expected = 'existing_conversation_not_captured'
    elif case == 'no_workspace':
        expected = 'host_workspace_required'
    calls.clear()
    payload = {'conversationId': native, 'transcriptPath': str(path), 'workspacePaths': [] if case == 'no_workspace' else [str(home)]}
    if expected:
        try: managed.prepare_payload(payload, 'store-user-prompt.py')
        except policy.NotReady as error: assert str(error) == expected, (case, str(error))
        else: raise AssertionError(('admitted invalid input', case))
        assert not calls, (case, calls)
    else:
        result = managed.prepare_payload(payload, 'store-user-prompt.py')
        host = 'antigravity-' + hashlib.sha256((binding['instance_id'] + '\\0' + native).encode()).hexdigest()
        assert calls == [(str(home), host)], (case, calls)
        assert result['conversationId'] == host and result['session_id'] == host
        assert result['transcriptPath'] == str(path)
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

    def active_connection(self):
        profile = self.root / '.gemini/config'; profile.mkdir(parents=True)
        runtime = self.root / 'agy'; runtime.write_text('# synthetic native installer'); runtime.chmod(0o700)
        registry = Registry(self.root / 'registry.json', types.SimpleNamespace(url='http://127.0.0.1:18999'))
        connection = AntigravityConnection(registry, {'python': sys.executable, 'project_control': {
            'url': 'http://127.0.0.1:18998'}}, profile, str(runtime))
        agent = {'key_file': str(self.root / 'child.key'), 'control_token_file': str(self.root / 'control.key'),
                 'principal_id': 'synthetic-principal'}
        installation = prepare(None, self.root / 'private', connection.id, agent,
                               'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(self.root),
                               host_kind='antigravity', python=sys.executable)
        source = Path(installation['plugin_dir'])
        policy = source / 'scripts/_memlane_antigravity.py'
        policy.write_text(policy.read_text().replace("transcript.name not in ('transcript.jsonl', 'transcript_full.jsonl')", "transcript.name != 'transcript.jsonl'"))
        manifest = source / '.memlane-project-access.json'
        value = json.loads(manifest.read_text()); value['files']['scripts/_memlane_antigravity.py'] = sha(policy.read_bytes())
        private_write(manifest, json.dumps(value).encode()); installation['manifest_sha256'] = sha(manifest.read_bytes())
        marker = source / '.memlane-installation.json'; value = json.loads(marker.read_text())
        value.update(runtime_profile=str(profile), activated_at=111)
        private_write(marker, json.dumps(value).encode())
        native = profile / 'plugins/memlane-cognee'; shutil.copytree(source, native)
        private_write(profile / 'config.json', json.dumps({'plugins': {'other': {'enabled': True}, 'memlane-cognee': {'enabled': True}}}).encode())
        agent.update(kind='antigravity', host_profile=str(profile), installation=installation)
        registry.state['agents'][connection.id] = agent
        registry.state.setdefault('connections', {})[connection.id] = {
            'status': 'configured_pending_new_session', 'activated': True, 'activated_at': 222,
            'native_plugin_dir': str(native), 'enabled_plugin': native.name}
        connection.config['project_control']['plugin_dir'] = str(source)
        return registry, connection, installation, native

    def test_reviewed_upgrade_preserves_active_installation_and_first_activation(self):
        registry, connection, original, native = self.active_connection()
        baseline = (connection.profile / 'config.json').read_bytes()
        original_body = (native / 'scripts/_memlane_antigravity.py').read_bytes()
        calls = []
        def command(args, **kwargs):
            calls.append(args[2])
            if args[2] == 'install':
                shutil.rmtree(native); shutil.copytree(args[3], native)
            return types.SimpleNamespace(returncode=0)
        try:
            with patch('antigravity_connection.Path.home', return_value=self.root):
                with self.assertRaisesRegex(PolicyError, 'active_antigravity_upgrade_requires_review'):
                    asyncio.run(connection.prepare(registry.state['revision']))
                asyncio.run(connection.review(registry.state['revision']))
                asyncio.run(connection.prepare(registry.state['revision']))
                record = registry.state['connections'][connection.id]
                candidate = record['prepared_installation']
                self.assertEqual(registry.state['agents'][connection.id]['installation'], original)
                self.assertEqual((native / 'scripts/_memlane_antigravity.py').read_bytes(), original_body)
                self.assertEqual((connection.profile / 'config.json').read_bytes(), baseline)
                self.assertNotEqual(candidate['manifest_sha256'], original['manifest_sha256'])
                asyncio.run(connection.review(registry.state['revision']))
                self.assertEqual(registry.state['connections'][connection.id]['status'], 'prepared')
                self.assertEqual(registry.state['agents'][connection.id]['installation'], original)
                with self.assertRaisesRegex(PolicyError, 'reviewed_prepared_connection_required'):
                    asyncio.run(connection.activate(registry.state['revision'], 'different-candidate'))
                with patch('antigravity_connection.subprocess.run', side_effect=command), \
                     patch('antigravity_connection.time.time', return_value=333):
                    asyncio.run(connection.activate(registry.state['revision'], candidate['manifest_sha256']))
            self.assertEqual(calls, ['validate', 'install', 'enable'])
            self.assertEqual(registry.state['agents'][connection.id]['installation'], candidate)
            self.assertEqual(registry.state['agents'][connection.id]['principal_id'], 'synthetic-principal')
            self.assertEqual((connection.profile / 'config.json').read_bytes(), baseline)
            self.assertEqual(json.loads((native / '.memlane-installation.json').read_text())['activated_at'], 111)
            self.assertEqual(registry.state['connections'][connection.id]['activated_at'], 111)
            self.assertNotIn('prepared_installation', registry.state['connections'][connection.id])
            verify_artifact({**candidate, 'plugin_dir': str(native)})
        finally:
            registry.close()

    def test_upgrade_review_requires_unchanged_configuration(self):
        for changed in ('settings', 'artifact'):
            with self.subTest(changed=changed):
                registry, connection, original, native = self.active_connection()
                try:
                    with patch('antigravity_connection.Path.home', return_value=self.root):
                        asyncio.run(connection.review(registry.state['revision']))
                        if changed == 'settings':
                            private_write(connection.target, json.dumps({'plugins': {'memlane-cognee': {'enabled': True}, 'other': {'enabled': False}}}).encode())
                        else:
                            private_write(native / 'scripts/agy_hook.py', b'# changed native script')
                        with self.assertRaises(PolicyError):
                            asyncio.run(connection.prepare(registry.state['revision']))
                    self.assertEqual(registry.state['agents'][connection.id]['installation'], original)
                finally:
                    registry.close()
                    shutil.rmtree(self.root / '.gemini'); shutil.rmtree(self.root / 'private')
                    shutil.rmtree(self.root / 'connections', ignore_errors=True)

    def test_interrupted_upgrade_recovers_original_boundary_without_reinstall(self):
        for interrupted in ('journal', 'native_marker'):
            with self.subTest(interrupted=interrupted):
                registry, connection, original, native = self.active_connection()
                calls = []
                def command(args, **kwargs):
                    calls.append(args[2])
                    if args[2] == 'install':
                        shutil.rmtree(native); shutil.copytree(args[3], native)
                    return types.SimpleNamespace(returncode=0)
                try:
                    with patch('antigravity_connection.Path.home', return_value=self.root):
                        asyncio.run(connection.review(registry.state['revision']))
                        asyncio.run(connection.prepare(registry.state['revision']))
                        candidate = registry.state['connections'][connection.id]['prepared_installation']
                        failed = [False]
                        failing_path = connection.root / 'activation-boundary.json' if interrupted == 'journal' else native / '.memlane-installation.json'
                        def write(path, raw):
                            if Path(path) == failing_path and not failed[0]:
                                failed[0] = True
                                raise OSError('synthetic upgrade write interrupted')
                            private_write(path, raw)
                        with patch('antigravity_connection.subprocess.run', side_effect=command), \
                             patch('antigravity_connection.private_write', side_effect=write):
                            with self.assertRaisesRegex(OSError, 'upgrade write interrupted'):
                                asyncio.run(connection.activate(registry.state['revision'], candidate['manifest_sha256']))
                            self.assertEqual(registry.state['connections'][connection.id]['status'], 'activation_unknown')
                            asyncio.run(connection.review(registry.state['revision']))
                    self.assertEqual(calls.count('install'), 1)
                    self.assertEqual(registry.state['agents'][connection.id]['installation'], candidate)
                    self.assertEqual(json.loads((native / '.memlane-installation.json').read_text())['activated_at'], 111)
                    self.assertEqual((native / '.memlane-installation.json').read_bytes(), (Path(candidate['plugin_dir']) / '.memlane-installation.json').read_bytes())
                    self.assertEqual(registry.state['connections'][connection.id]['status'], 'configured_pending_new_session')
                finally:
                    registry.close()
                    shutil.rmtree(self.root / '.gemini'); shutil.rmtree(self.root / 'private')
                    shutil.rmtree(self.root / 'connections', ignore_errors=True)


if __name__ == '__main__':
    unittest.main()
