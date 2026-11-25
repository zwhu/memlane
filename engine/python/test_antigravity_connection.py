"""Pinned native host compatibility and isolation, without a model or daily data."""
import copy
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

from aiohttp.test_utils import make_mocked_request

from agent_connections import detect_executable, verify_artifact, sha
import antigravity_plugin
from antigravity_plugin import files_for
from antigravity_connection import AntigravityConnection
from project_control import private_write, Registry
from project_control_server import create_app
from project_installation import prepare
from project_policy import PolicyError


class AntigravityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Cache immutable pinned archive extraction/AST patching by (resolved_archive, lstat)
        # so real antigravity_plugin.files_for() runs once on the pinned archive and always
        # runs uncached on missing/modified/bad archives.
        real_files_for = antigravity_plugin.files_for
        cache = {}
        manifest = json.loads((antigravity_plugin.ENGINE / 'coding-memory-components.json').read_text())['plugin']
        default_archive = antigravity_plugin.ENGINE.parent / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz')

        def _cached_files_for(archive=None):
            resolved = Path(archive or default_archive)
            try:
                st = os.lstat(resolved)
                key = (str(resolved), st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns)
            except OSError:
                return real_files_for(archive)
            hit = cache.get(key)
            if hit is None:
                files, upstream = real_files_for(archive)
                hit = (dict(files), copy.deepcopy(upstream))
                cache[key] = hit
            return dict(hit[0]), copy.deepcopy(hit[1])

        cls._files_for_patch = patch.object(antigravity_plugin, 'files_for', side_effect=_cached_files_for)
        cls._files_for_patch.start()
        # Shared read-only prepared artifact for tests that only read scripts/ with per-test HOME.
        cls._shared_temp = tempfile.TemporaryDirectory()
        cls._shared_root = Path(cls._shared_temp.name).resolve()
        private_write(cls._shared_root / 'child.key', b'synthetic-child-key')
        private_write(cls._shared_root / 'control.key', b'synthetic-control-key-' * 3)
        shared_agent = {
            'key_file': str(cls._shared_root / 'child.key'),
            'control_token_file': str(cls._shared_root / 'control.key'),
        }
        cls._shared_installation = prepare(
            None, cls._shared_root / 'private', 'antigravity-synthetic', shared_agent,
            'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(cls._shared_root),
            host_kind='antigravity', python=sys.executable,
        )
        shared_marker = Path(cls._shared_installation['plugin_dir']) / '.memlane-installation.json'
        shared_data = json.loads(shared_marker.read_text())
        shared_data.update(runtime_profile=str(cls._shared_root / '.gemini/config'), activated_at=1)
        private_write(shared_marker, json.dumps(shared_data).encode())
        verify_artifact(cls._shared_installation)

    @classmethod
    def tearDownClass(cls):
        cls._shared_temp.cleanup()
        cls._files_for_patch.stop()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        private_write(self.root / 'child.key', b'synthetic-child-key')
        private_write(self.root / 'control.key', b'synthetic-control-key-' * 3)

    def tearDown(self):
        self.temp.cleanup()

    async def read_operation_receipt(self, registry, profile, operation):
        app = create_app(registry, 'm' * 40, {}, codex_config={'agent_hosts': {
            'antigravity': {'profile': str(profile), 'executable': sys.executable}}})
        app.freeze()
        try:
            request = make_mocked_request('GET', f'/v1/connections/operations/{operation}',
                                          headers={'Authorization': 'Bearer ' + 'm' * 40}, app=app)
            response = await app._handle(request)
            self.assertEqual(response.status, 200)
            return json.loads(response.body)
        finally:
            await app.cleanup()

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

    def isolated(self, source, *, readonly=False):
        if readonly:
            installation = self._shared_installation
        else:
            installation = self.artifact()
            verify_artifact(installation)
        env = {k: v for k, v in os.environ.items() if not k.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(
            HOME=str(self.root),
            COGNEE_SESSION_KEY='synthetic-host',
            COGNEE_PLUGIN_IN_VENV='1',
            PYTHONDONTWRITEBYTECODE='1',
        )
        result = subprocess.run([sys.executable, '-c', source, str(Path(installation['plugin_dir']) / 'scripts')],
                                env=env, cwd=self.root, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr[-3000:])

    def test_pinned_archive_and_official_hooks_remain_native(self):
        files, upstream = antigravity_plugin.files_for()
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
''', readonly=True)

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
                operation = 'partial-binding-activate'
                receipt = dict(status='unknown', phase='post_mutation', instance_id=connection.id, action='activate',
                               outcome='unknown', error_code='interrupted')
                registry.state.setdefault('connection_operations', {})[operation] = receipt
                revision = registry.state['revision']
                record = registry.state['connections'][connection.id]
                previous = copy.deepcopy(registry.state)

                def fail_save():
                    self.assertEqual(record['status'], 'configured_pending_new_session')
                    self.assertEqual(receipt['status'], 'complete')
                    self.assertEqual(registry.state['revision'], revision + 1)
                    raise asyncio.CancelledError()

                with patch.object(registry, 'save', side_effect=fail_save):
                    with self.assertRaises(asyncio.CancelledError):
                        asyncio.run(connection.review(revision))
                self.assertEqual(registry.state, previous)
                self.assertIs(registry.state['connection_operations'][operation], receipt)
                self.assertIs(registry.state['connections'][connection.id], record)
                row = asyncio.run(connection.review(revision))
                body = asyncio.run(self.read_operation_receipt(registry, profile, operation))
                self.assertEqual(body, {
                    'recorded': True, 'operation_id': operation, 'instance_id': connection.id, 'action': 'activate',
                    'status': 'complete', 'phase': 'post_mutation', 'error_code': None,
                    'result': {'revision': revision + 1, 'connection': row},
                })
                self.assertEqual(registry.sessions_store.load_all()[1][operation], receipt)
            self.assertEqual(json.loads(marker.read_text())['activated_at'], 222)
            self.assertEqual(marker.read_bytes(), (native / '.memlane-installation.json').read_bytes())
            self.assertEqual(registry.state['connections'][connection.id]['activated_at'], 222)
        finally:
            registry.close()

    def test_review_fresh_preparation_unknown_settles_original_prepare_receipt(self):
        profile = self.root / '.gemini/config'
        profile.mkdir(parents=True)
        registry = Registry(self.root / 'registry.json', None)
        self.addCleanup(registry.close)
        connection = AntigravityConnection(registry, {}, profile, sys.executable)
        operation = 'original-prepare'
        with patch('antigravity_connection.Path.home', return_value=self.root):
            registry.state['connections'][connection.id] = {
                'status': 'preparation_unknown', 'preview': connection.preview()}
            receipt = dict(status='unknown', phase='post_mutation', instance_id=connection.id, action='prepare')
            registry.state.setdefault('connection_operations', {})[operation] = receipt
            row = asyncio.run(connection.review(0))
            body = asyncio.run(self.read_operation_receipt(registry, profile, operation))
        self.assertEqual(row['status'], 'not_connected')
        self.assertEqual(body, {
            'recorded': True, 'operation_id': operation, 'instance_id': connection.id, 'action': 'prepare',
            'status': 'complete', 'phase': 'post_mutation', 'outcome': 'rejected',
            'error_code': 'antigravity_preparation_did_not_install',
            'result': {'revision': 1, 'connection': row},
        })
        self.assertEqual(registry.sessions_store.load_all()[1][operation], receipt)

    def test_review_fresh_prepared_validates_baseline_and_settles_prepare_receipt(self):
        profile = self.root / '.gemini/config'
        profile.mkdir(parents=True)
        registry = Registry(self.root / 'registry.json', None)
        self.addCleanup(registry.close)
        connection = AntigravityConnection(registry, {'python': sys.executable, 'project_control': {
            'url': 'http://127.0.0.1:18998'}, 'agent_host_home': str(self.root)}, profile, sys.executable)
        agent = registry.state['agents'][connection.id] = {
            'key_file': str(self.root / 'child.key'), 'control_token_file': str(self.root / 'control.key')}
        with patch('antigravity_connection.Path.home', return_value=self.root):
            asyncio.run(connection.prepare(0))
        record = registry.state['connections'][connection.id]
        installation = copy.deepcopy(agent['installation'])
        self.assertNotIn('native_plugin_dir', record)
        self.assertNotIn('prepared_installation', record)
        receipt = dict(status='in_progress', instance_id=connection.id, action='prepare')
        registry.state.setdefault('connection_operations', {})['original-prepare'] = receipt
        revision = registry.state['revision']

        private_write(connection.target, b'{"plugins": {}}')
        with patch('antigravity_connection.Path.home', return_value=self.root):
            with self.assertRaisesRegex(PolicyError, 'host_configuration_changed'):
                asyncio.run(connection.review(revision))
            self.assertEqual(receipt['status'], 'in_progress')
            self.assertEqual(registry.state['revision'], revision)
            connection.target.unlink()
            row = asyncio.run(connection.review(revision))
        self.assertEqual((row['status'], record['status']), ('prepared', 'prepared'))
        self.assertEqual(agent['installation'], installation)
        self.assertEqual(receipt, {
            'status': 'complete', 'instance_id': connection.id, 'action': 'prepare',
            'result': {'revision': revision + 1, 'connection': row},
        })
        self.assertEqual(registry.sessions_store.load_all()[1]['original-prepare'], receipt)

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
orig_ensure = common.ensure_launch_record
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
        assert os.environ.get('COGNEE_SESSION_PREFIX') == 'antigravity'
        if case == 'standard':
            launched_sid, _ = orig_ensure(host, str(home), dataset='dataset')
            assert launched_sid == host, launched_sid
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
            with (patch('antigravity_connection.Path.home', return_value=self.root),
                  patch('project_installation.prepare') as stage):
                asyncio.run(connection.review(registry.state['revision']))
                active_marker = (native / '.memlane-installation.json').read_bytes()
                asyncio.run(connection.prepare(registry.state['revision']))
            stage.assert_not_called()
            record = registry.state['connections'][connection.id]
            self.assertEqual(record['prepared_installation']['plugin_dir'], candidate['plugin_dir'])
            self.assertEqual(record['active_record']['status'], 'configured_pending_new_session')
            self.assertEqual((Path(candidate['plugin_dir']) / '.memlane-installation.json').read_bytes(), active_marker)
            self.assertEqual(connection.installed(), native)
        finally:
            registry.close()

    def test_review_interrupted_upgrade_restores_active_snapshot_and_rejects_prepare_receipt(self):
        registry, connection, original, native = self.active_connection()
        self.addCleanup(registry.close)
        asyncio.run(connection.review(registry.state['revision']))
        active_record = copy.deepcopy(registry.state['connections'][connection.id])
        receipt = dict(status='unknown', phase='post_mutation', instance_id=connection.id, action='prepare')
        registry.state.setdefault('connection_operations', {})['original-prepare'] = receipt
        with (patch('antigravity_connection.Path.home', return_value=self.root),
              patch('project_installation.prepare', side_effect=asyncio.CancelledError)):
            with self.assertRaises(asyncio.CancelledError):
                asyncio.run(connection.prepare(registry.state['revision']))
        on_disk = json.loads(registry.path.read_text())
        interrupted = on_disk['connections'][connection.id]
        self.assertEqual(interrupted['status'], 'preparation_unknown')
        self.assertTrue(interrupted['activated'])
        self.assertEqual(interrupted['native_plugin_dir'], str(native))
        self.assertEqual(interrupted['active_record'], active_record)
        self.assertEqual(interrupted['active_installation'], original)
        self.assertEqual(on_disk['agents'][connection.id]['installation'], original)
        self.assertEqual(registry.sessions_store.load_all()[1]['original-prepare'], receipt)

        row = asyncio.run(connection.review(on_disk['revision']))
        self.assertEqual(row['status'], 'configured_pending_new_session')
        restored = json.loads(registry.path.read_text())
        self.assertEqual(restored['connections'][connection.id], active_record)
        self.assertEqual(restored['agents'][connection.id]['installation'], original)
        self.assertEqual(restored['revision'], on_disk['revision'] + 1)
        self.assertEqual(receipt, {
            'status': 'complete', 'phase': 'post_mutation', 'instance_id': connection.id, 'action': 'prepare',
            'outcome': 'rejected', 'error_code': 'antigravity_preparation_did_not_install',
            'result': {'revision': restored['revision'], 'connection': row},
        })
        self.assertEqual(registry.sessions_store.load_all()[1]['original-prepare'], receipt)

    def test_review_prepared_upgrade_verifies_both_artifacts_and_settles_only_prepare_receipt(self):
        registry, connection, original, native = self.active_connection()
        self.addCleanup(registry.close)
        asyncio.run(connection.review(registry.state['revision']))
        with patch('antigravity_connection.Path.home', return_value=self.root):
            asyncio.run(connection.prepare(registry.state['revision']))
        record = registry.state['connections'][connection.id]
        receipt = dict(status='unknown', phase='post_mutation', instance_id=connection.id, action='prepare',
                       outcome='rejected', error_code='old_error')
        activate = dict(status='unknown', instance_id=connection.id, action='activate')
        unrelated = dict(status='unknown', instance_id='other', action='prepare')
        complete = dict(status='complete', instance_id=connection.id, action='prepare')
        registry.state['connection_operations'] = operations = {
            'original-prepare': receipt, 'activation': activate, 'unrelated': unrelated, 'settled': complete}
        before = copy.deepcopy(operations)
        revision = registry.state['revision']
        for script in (Path(record['prepared_installation']['plugin_dir']) / 'scripts/agy_hook.py',
                       native / 'scripts/agy_hook.py'):
            with self.subTest(script=script):
                data = script.read_bytes()
                try:
                    script.write_bytes(data + b'\n# tampered')
                    with self.assertRaisesRegex(PolicyError, 'connection_artifact_changed'):
                        asyncio.run(connection.review(revision))
                finally:
                    script.write_bytes(data)
                self.assertEqual(operations, before)
                self.assertEqual(registry.state['revision'], revision)

        def interrupt_save():
            self.assertEqual(receipt['status'], 'complete')
            raise asyncio.CancelledError

        with patch.object(registry, 'save', side_effect=interrupt_save):
            with self.assertRaises(asyncio.CancelledError):
                asyncio.run(connection.review(revision))
        self.assertEqual(operations, before)
        self.assertEqual(registry.state['revision'], revision)
        row = asyncio.run(connection.review(revision))
        self.assertEqual((row['status'], record['status']), ('prepared', 'prepared'))
        self.assertEqual(registry.state['agents'][connection.id]['installation'], original)
        self.assertEqual(receipt, {
            'status': 'complete', 'phase': 'post_mutation', 'instance_id': connection.id, 'action': 'prepare',
            'result': {'revision': revision + 1, 'connection': row},
        })
        self.assertEqual(operations, {**before, 'original-prepare': receipt})
        self.assertEqual(registry.sessions_store.load_all()[1], operations)
        with patch.object(registry, 'save') as save:
            self.assertEqual(asyncio.run(connection.review(revision + 1)), row)
        save.assert_not_called()

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

    def test_hook_error_observability_records_reason_without_secrets_and_exposes_via_view(self):
        profile = self.root / '.gemini/config'
        profile.mkdir(parents=True)
        registry = Registry(self.root / 'registry.json', types.SimpleNamespace(url='http://127.0.0.1:18999'))
        connection = AntigravityConnection(registry, {'python': sys.executable, 'project_control': {
            'url': 'http://127.0.0.1:18998'}}, profile, '/synthetic/agy')
        agent = {'key_file': str(self.root / 'child.key'), 'control_token_file': str(self.root / 'control.key')}
        installation = prepare(None, self.root / 'private', connection.id, agent,
                               'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(self.root),
                               host_kind='antigravity', python=sys.executable)
        marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
        marker_data = json.loads(marker.read_text())
        marker_data.update(runtime_profile=str(profile), activated_at=1)
        private_write(marker, json.dumps(marker_data).encode())
        native = profile / 'plugins/memlane-cognee'
        shutil.copytree(installation['plugin_dir'], native)
        private_write(profile / 'config.json', json.dumps({'plugins': {'memlane-cognee': {'enabled': True}}}).encode())
        agent.update(kind='antigravity', host_profile=str(profile), installation=installation)
        registry.state['agents'][connection.id] = agent
        registry.state.setdefault('connections', {})[connection.id] = {
            'status': 'configured_pending_new_session', 'activated': True, 'activated_at': 1,
            'native_plugin_dir': str(native), 'enabled_plugin': native.name}
        try:
            import uuid as _uuid
            env = {k: v for k, v in os.environ.items() if not k.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
            env.update(HOME=str(self.root), COGNEE_PLUGIN_IN_VENV='1')
            hook_script = str(native / 'scripts/agy_hook.py')
            error_file = self.root / '.cognee-plugin' / ('antigravity-' + connection.id) / 'last-hook-error.json'
            for idx in range(7):
                cid = str(_uuid.uuid4())
                bad_path = self.root / cid / 'arbitrary.jsonl'
                payload = {
                    'conversationId': cid,
                    'transcriptPath': str(bad_path),
                    'workspacePaths': [str(self.root)],
                    'lastUserInput': 'secret-prompt-never-log-' + str(idx),
                }
                result = subprocess.run(
                    [sys.executable, hook_script, 'store-user-prompt.py'],
                    input=json.dumps(payload), env=env, cwd=self.root,
                    capture_output=True, text=True, timeout=20)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), '{}')
            self.assertTrue(error_file.exists())
            self.assertEqual(error_file.stat().st_mode & 0o777, 0o600)
            raw_text = error_file.read_text(encoding='utf-8')
            self.assertNotIn('secret-prompt-never-log', raw_text)
            entries = json.loads(raw_text)['entries']
            self.assertEqual(len(entries), 5)
            latest = entries[-1]
            self.assertEqual(latest['script'], 'store-user-prompt.py')
            self.assertEqual(latest['error_type'], 'NotReady')
            self.assertEqual(latest['reason'], 'native_transcript_identity_required')
            self.assertIn('conversationId', latest['payload_keys'])
            with patch('antigravity_connection.Path.home', return_value=self.root):
                row = connection.view()
                self.assertEqual(row.get('last_hook_error'), latest)
                # Verified receipt newer than the hook error clears stale warnings.
                registry.state.setdefault('connection_receipts', {})[connection.id] = {
                    'artifact_verified': True,
                    'environment_verified': True,
                    'received_at': latest['at'] + 10.0,
                }
                self.assertNotIn('last_hook_error', connection.view())
                # A newer hook error at/after the verified receipt still surfaces.
                entries_data = json.loads(error_file.read_text(encoding='utf-8'))
                newer = dict(latest, at=latest['at'] + 20.0)
                entries_data['entries'] = entries_data['entries'][-4:] + [newer]
                error_file.write_text(json.dumps(entries_data), encoding='utf-8')
                self.assertEqual(connection.view().get('last_hook_error'), newer)
                # Symlink diagnostic file must be ignored both for write and read.
                target = self.root / 'outside-error.json'
                target.write_text('keep-intact', encoding='utf-8')
                error_file.unlink()
                error_file.symlink_to(target)
                subprocess.run(
                    [sys.executable, hook_script, 'store-user-prompt.py'],
                    input=json.dumps({'conversationId': str(_uuid.uuid4()), 'lastUserInput': 'secret-prompt-never-log-sym'}),
                    env=env, cwd=self.root, capture_output=True, text=True, timeout=20)
                self.assertEqual(target.read_text(encoding='utf-8'), 'keep-intact')
                self.assertNotIn('last_hook_error', connection.view())
        finally:
            registry.close()

    def test_official_host_prepare_and_legacy_directory_include_helper_sha256(self):
        installation = self.artifact()
        manifest = json.loads((Path(installation['plugin_dir']) / '.memlane-project-access.json').read_text())
        expected_helper = manifest['files']['scripts/_memlane_policy.py']
        self.assertEqual(installation.get('helper_sha256'), expected_helper)

        registry = Registry(self.root / 'private/registry.json', object())
        try:
            legacy = dict(installation)
            legacy.pop('helper_sha256', None)
            registry.state['agents']['antigravity-legacy'] = {
                'name': 'Gemini App',
                'kind': 'antigravity',
                'installation': legacy,
            }
            agents = {row['id']: row for row in registry.directory()['agents']}
            self.assertEqual(agents['antigravity-legacy']['installation'].get('helper_sha256'), expected_helper)

            # Tampered or symlinked manifest must not backfill helper_sha256.
            bad_manifest = Path(installation['plugin_dir']) / '.memlane-project-access.json'
            outside = self.root / 'outside-manifest.json'
            outside.write_text(bad_manifest.read_text(), encoding='utf-8')
            bad_manifest.unlink()
            bad_manifest.symlink_to(outside)
            agents_after = {row['id']: row for row in registry.directory()['agents']}
            self.assertIsNone(agents_after['antigravity-legacy']['installation'].get('helper_sha256'))
        finally:
            registry.close()

    def test_processing_session_short_circuits_agy_hook_without_error_record(self):
        installation = self._shared_installation
        hook_script = Path(installation['plugin_dir']) / 'scripts/agy_hook.py'
        env = {k: v for k, v in os.environ.items() if not k.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(
            HOME=str(self.root),
            COGNEE_PLUGIN_IN_VENV='1',
            MEMLANE_PROCESSING_SESSION='1',
            PYTHONDONTWRITEBYTECODE='1',
        )
        result = subprocess.run(
            [sys.executable, str(hook_script), 'store-user-prompt.py'],
            input='not-even-json', env=env, cwd=self.root,
            capture_output=True, text=True, timeout=20,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, '{}\n')
        self.assertFalse((self.root / '.cognee-plugin').exists())

    def test_prompt_context_includes_calendar_date(self):
        self.isolated('''
import importlib.util, json, pathlib, re, sys
path = pathlib.Path(sys.argv[1]) / 'store-user-prompt.py'
spec = importlib.util.spec_from_file_location('store_user_prompt', path)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
encoded = mod._prompt_context({'cwd': '/tmp/repo', 'model': 'gemini', 'turn_id': '1'})
parsed = json.loads(encoded)
assert re.match(r'^\\d{4}-\\d{2}-\\d{2}$', parsed['date']), parsed
assert parsed['cwd'] == '/tmp/repo', parsed
''', readonly=True)

    def test_write_denied_buffers_numbered_entry_without_ambiguous_duplicate(self):
        self.isolated('''
import asyncio, contextlib, importlib.util, json, os, pathlib, sys
scripts = pathlib.Path(sys.argv[1])
sys.path.insert(0, str(scripts))
os.environ['COGNEE_CAPTURE_TOOLS'] = 'Read'
import _plugin_common as common, _memlane_policy as policy, _memlane_capture as capture
dataset = '11111111-1111-4111-8111-111111111111'
session_id = 'synthetic-session'
policy.permit = lambda op, *a, **k: op == 'capture'
policy.capture_decision = lambda *a: ('enabled', 1)
policy.require = lambda op, *a, **k: None if op == 'capture' else (_ for _ in ()).throw(policy.NotReady('当前 Agent 没有此项目的写入权限'))
policy._pending_identity = lambda *a, **k: None
policy.dataset = lambda: dataset
policy_state = {}
policy._state = lambda *a, **k: contextlib.nullcontext(policy_state)
common._read_map_record = lambda *a: {'session_id': session_id}
common.remember_pending_prompt(session_id, 'synthetic user prompt')
spec = importlib.util.spec_from_file_location('store_to_session', scripts / 'store-to-session.py')
store_mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(store_mod)
store_mod._load_session = lambda: (session_id, dataset, '')
store_mod.resolve_runtime_mode = lambda: {'mode': 'http', 'base_url': 'http://127.0.0.1:18999'}
store_mod.load_config = lambda: {'backend': 'cloud'}
store_mod.server_usable = lambda *a, **k: True
store_mod.pop_pending_prompt = lambda *a, **k: {'prompt': 'synthetic user prompt'}
asyncio.run(store_mod._store_tool_call({'tool_name': 'Read', 'tool_input': {'file_path': '/tmp/a.py'}, 'tool_response': 'ok'}))
bridge_file = common._bridge_file(session_id)
cache = json.loads(bridge_file.read_text(encoding='utf-8'))
pending = cache[common._bridge_cache_key(dataset, session_id)]['pending_entries']
assert len(pending) == 1, pending
assert capture.numbered(pending[0]), pending
assert not pending[0].get('_replay_ambiguous'), pending
bridge_file.unlink()
policy.require = lambda op, *a, **k: (_ for _ in ()).throw(policy.NotReady('capture_denied'))
asyncio.run(store_mod._store_tool_call({'tool_name': 'Read', 'tool_input': {'file_path': '/tmp/b.py'}, 'tool_response': 'ok'}))
asyncio.run(store_mod._store_assistant_stop({'assistant_message': 'synthetic answer', 'turn_id': '1'}))
assert not bridge_file.exists(), bridge_file.read_text(encoding='utf-8')
''', readonly=True)

    def test_run_session_improve_detailed_returns_structured_reasons(self):
        self.isolated('''
import contextlib, sys, time
sys.path.insert(0, sys.argv[1])
import _plugin_common as common, _memlane_policy as policy
dataset = '11111111-1111-4111-8111-111111111111'
session_id = 'synthetic-session'
time.sleep = lambda *_: None
common.hook_log = lambda *_: None
common.ensure_dataset_via_http = lambda *_: None
common.record_improve_success = lambda *_: None
common.refresh_credits = lambda *_: None
common.clear_payment_required = lambda: None
common.record_payment_required = lambda *_: None

policy.permit = lambda *a, **k: False
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res['ok'] is False and res['reason'] == 'managed_policy_paused', res

policy.permit = lambda *a, **k: True
common.improve_session_lock = lambda *a, **k: contextlib.nullcontext(False)
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res == {'ok': False, 'reason': 'busy', 'error': ''}, res

common.improve_session_lock = lambda *a, **k: contextlib.nullcontext(True)
common._backend_reachable = lambda *_: False
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res['ok'] is False and res['reason'] == 'unreachable', res

common._backend_reachable = lambda *_: True
common.drain_warmup_entries = lambda *a, **k: (1, 1)
common.improve_session_via_http = lambda *a, **k: {'ok': True}
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res == {'ok': False, 'reason': 'incomplete_drain', 'error': ''}, res

common.drain_warmup_entries = lambda *a, **k: (1, 0)
common.improve_session_via_http = lambda *a, **k: {'ok': False, 'busy': True}
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res['ok'] is False and res['reason'] == 'busy', res

common.improve_session_via_http = lambda *a, **k: {'ok': False, 'error': 'boom'}
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res == {'ok': False, 'reason': 'failed', 'error': 'boom'}, res

common.improve_session_via_http = lambda *a, **k: {'ok': True}
res = common.run_session_improve_detailed(dataset, session_id, trigger='auto')
assert res == {'ok': True, 'reason': '', 'error': ''}, res
''', readonly=True)


if __name__ == '__main__':
    unittest.main()
