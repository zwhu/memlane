import asyncio
import copy
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import AsyncMock, patch

from aiohttp.test_utils import make_mocked_request
import tomlkit
from project_codex_connection import CodexConnection, digest, proposal, legacy_consumers, stage_install
from project_control import Registry, private_write
from project_control_server import create_app
from project_policy import PolicyError


class RegistryFixture:
    def __init__(self, root):
        self.path = root / 'registry.json'
        self.mutex = asyncio.Lock()
        self.state = {'revision': 4, 'agents': {}, 'sessions': {}, 'codex_connections': {}}
        self.saves = []

    def check_revision(self, value):
        if value != self.state['revision']:
            raise PolicyError('configuration_changed', 409)

    def save(self):
        self.saves.append(copy.deepcopy(self.state))
        private_write(self.path, json.dumps(self.state).encode())


class ConnectionTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.home = self.root / 'codex-home'; self.home.mkdir()
        self.config_file = self.home / 'config.toml'
        self.before = b'# keep my model\nmodel = "existing-model"\n[plugins."unrelated@official"]\nenabled = true\n[plugins."cognee@legacy"]\nenabled = true # old hook\n[features]\nhooks = false\n'
        self.config_file.write_bytes(self.before)
        self.registry = RegistryFixture(self.root)
        self.config = {'auth_home': str(self.home), 'codex_executable': sys.executable}
        self.connection = CodexConnection(self.registry, self.config)
        plugin = self.root / 'plugin'; plugin.mkdir()
        (plugin / '.codex-plugin').mkdir()
        (plugin / '.codex-plugin/plugin.json').write_text('{"version":"1.8.0"}')
        (plugin / 'hook.py').write_text('checked official hook')
        raw = json.dumps({'files': {'hook.py': digest((plugin / 'hook.py').read_bytes())}}).encode()
        (plugin / '.memlane-project-access.json').write_bytes(raw)
        (plugin / '.memlane-installation.json').write_text('private-instance-marker')
        self.installation = {'plugin_dir': str(plugin), 'marketplace_root': str(self.root / 'marketplace'),
                             'marketplace_name': 'memlane-synthetic-fixed-generation', 'manifest_sha256': digest(raw)}
        stage = self.root / 'stage/1.8.0'; shutil.copytree(plugin, stage)
        self.connection.root.mkdir(parents=True)
        proposed, disabled = proposal(self.before, self.installation)
        private_write(self.connection.root / 'config-proposed.toml', proposed)
        self.preview = {'baseline_sha256': digest(self.before), 'proposed_sha256': digest(proposed), 'disabled_plugins': disabled, 'enabled_plugin': 'cognee@' + self.installation['marketplace_name']}
        self.registry.state['agents'][self.connection.id] = {'installation': self.installation}
        self.registry.state['codex_connections'][self.connection.id] = {'status': 'prepared', 'preview': self.preview, 'staged_cache': str(stage)}

    async def test_activate_preserves_unrelated_settings_and_never_touches_auth_or_buffers(self):
        self.registry.state['sessions'][json.dumps([self.connection.id, 'old-host-session'])] = 'old-project'
        (self.home / 'auth.json').write_text('do not touch authentication')
        buffers = self.root / 'buffer.json'; buffers.write_text('old materials must remain')
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            result = await self.connection.activate(4, self.preview['proposed_sha256'])
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertEqual(result['new_sessions'], 0)
        self.registry.state['sessions'][json.dumps([self.connection.id, 'new-host-session'])] = 'new-project'
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            self.assertEqual(self.connection.view()['new_sessions'], 1)
        parsed = tomlkit.parse(self.config_file.read_text())
        self.assertEqual(parsed['model'], 'existing-model')
        self.assertTrue(parsed['plugins']['unrelated@official']['enabled'])
        self.assertFalse(parsed['plugins']['cognee@legacy']['enabled'])
        self.assertTrue(parsed['features']['hooks'])
        self.assertIn('# keep my model', self.config_file.read_text())
        self.assertEqual((self.home / 'auth.json').read_text(), 'do not touch authentication')
        self.assertEqual(buffers.read_text(), 'old materials must remain')
        self.assertEqual(self.registry.saves[0]['codex_connections'][self.connection.id]['status'], 'activation_unknown')
        with self.assertRaises(PolicyError):
            await self.connection.activate(self.registry.state['revision'], self.preview['proposed_sha256'])

    async def test_changed_config_or_active_legacy_consumer_does_not_install_or_overwrite(self):
        with patch('project_codex_connection.legacy_consumers', return_value=[{'pid': 123}]):
            with self.assertRaisesRegex(PolicyError, 'codex_legacy_consumers_still_active'):
                await self.connection.activate(4, self.preview['proposed_sha256'])
        self.config_file.write_bytes(self.before + b'\n# concurrent edit\n')
        with self.assertRaisesRegex(PolicyError, 'codex_configuration_changed'):
            await self.connection.activate(4, self.preview['proposed_sha256'])
        self.assertFalse((self.home / 'plugins').exists())
        self.assertEqual(self.registry.state['revision'], 4)
        self.assertIn('concurrent edit', self.config_file.read_text())

    async def test_tampered_hook_or_preview_never_activates(self):
        source = Path(self.registry.state['codex_connections'][self.connection.id]['staged_cache'])
        (source / 'hook.py').write_text('changed')
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            with self.assertRaisesRegex(PolicyError, 'codex_installed_files_changed'):
                await self.connection.activate(4, self.preview['proposed_sha256'])
        self.assertEqual(self.config_file.read_bytes(), self.before)
        self.assertEqual(self.registry.state['revision'], 4)

    async def test_unknown_prepare_cannot_mutate_or_create_a_replacement(self):
        self.registry.state['codex_connections'][self.connection.id]['status'] = 'activation_unknown'
        with self.assertRaisesRegex(PolicyError, 'codex_connection_result_requires_review'):
            await self.connection.prepare(4)
        self.assertEqual(self.registry.state['revision'], 4)
        self.assertEqual(self.config_file.read_bytes(), self.before)

    async def test_legacy_consumer_scan_is_scoped_to_the_reviewed_host_profile(self):
        rows = ('123 Fri Nov 13 14:00:00 2026 python /other/.codex/plugins/cache/old/cognee/1/scripts/idle-watcher.py\n'
                '456 Fri Nov 13 14:00:01 2026 python ' + str(self.home) + '/plugins/cache/old/cognee/1/scripts/exit-watcher.py\n')
        with patch('project_codex_connection.subprocess.check_output', return_value=rows):
            result = legacy_consumers(self.home)
            self.assertEqual(legacy_consumers(self.home, ['cognee@current']), [])
        self.assertEqual([row['pid'] for row in result], [456])

    async def test_unknown_activation_reviews_original_files_without_reinstall_or_rewrite(self):
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            await self.connection.activate(4, self.preview['proposed_sha256'])
            self.registry.state['codex_connections'][self.connection.id]['status'] = 'activation_unknown'
            configured = self.config_file.read_bytes()
            with patch('project_codex_connection.stage_install', side_effect=AssertionError('must not reinstall')):
                result = await self.connection.review(self.registry.state['revision'])
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertEqual(self.config_file.read_bytes(), configured)

    async def test_unknown_activation_before_config_commit_reviews_same_preview_as_prepared(self):
        self.registry.state['codex_connections'][self.connection.id]['status'] = 'activation_unknown'
        with (patch('project_codex_connection.stage_install', side_effect=AssertionError('must not reinstall')),
              patch('project_codex_connection.legacy_consumers', return_value=[])):
            result = await self.connection.review(4)
        self.assertEqual(result['status'], 'prepared')
        self.assertEqual(self.config_file.read_bytes(), self.before)

    async def test_review_settles_original_prepare_and_activate_receipts_over_http(self):
        registry = Registry(self.registry.path, None)
        self.addCleanup(registry.close)
        registry.state.update(self.registry.state)
        self.connection.registry = registry
        app = create_app(registry, 'm' * 40, {}, codex_config={'agent_hosts': {
            'codex': {'profile': str(self.home), 'executable': sys.executable}}})
        app.freeze()
        self.addAsyncCleanup(app.cleanup)
        identifier = self.connection.id
        record = registry.state['codex_connections'][identifier]
        unrelated = dict(status='unknown', phase='post_mutation', action='activate', instance_id='other')
        complete = dict(status='complete', action='activate', instance_id=identifier, result={'revision': 1})
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            await self.connection.activate(4, self.preview['proposed_sha256'])
            for status, errors in (
                    ('configured_pending_new_session', (None, None)),
                    ('prepared', (None, 'codex_activation_did_not_apply')),
                    ('staging_not_complete', ('codex_preparation_did_not_install', 'codex_activation_did_not_apply'))):
                with self.subTest(status=status):
                    if status == 'prepared':
                        self.config_file.write_bytes(self.before)
                    if status == 'staging_not_complete':
                        shutil.rmtree(record['staged_cache'])
                    record['status'] = 'staging_unknown' if status == 'staging_not_complete' else 'activation_unknown'
                    operations = registry.state.setdefault('connection_operations', {})
                    operations.update(unrelated=unrelated.copy(), complete=copy.deepcopy(complete))
                    for action in ('prepare', 'activate'):
                        operations['original-' + action] = dict(
                            status='unknown', phase='post_mutation', instance_id=identifier, action=action,
                            outcome='unknown', error_code='interrupted')
                    revision = registry.state['revision']
                    row = await self.connection.review(revision)
                    self.assertEqual(row['status'], status)
                    durable = registry.sessions_store.load_all()[1]
                    for action, error in zip(('prepare', 'activate'), errors):
                        operation = 'original-' + action
                        request = make_mocked_request('GET', f'/v1/connections/operations/{operation}',
                                                      headers={'Authorization': 'Bearer ' + 'm' * 40}, app=app)
                        response = await app._handle(request)
                        body = json.loads(response.body)
                        self.assertEqual(response.status, 200)
                        self.assertEqual(body, {
                            'recorded': True, 'operation_id': operation, 'instance_id': identifier, 'action': action,
                            'status': 'complete', 'phase': 'post_mutation', 'error_code': error,
                            'result': {'revision': revision + 1, 'connection': row},
                            **({'outcome': 'rejected'} if error else {}),
                        })
                        self.assertEqual(durable[operation], operations[operation])
                    self.assertEqual(operations['unrelated'], unrelated)
                    self.assertEqual(operations['complete'], complete)

    async def test_review_rolls_back_connection_and_receipts_on_base_exception(self):
        identifier = self.connection.id
        record = self.registry.state['codex_connections'][identifier]
        record['status'] = 'activation_unknown'
        receipt = dict(status='unknown', phase='post_mutation', instance_id=identifier, action='activate')
        self.registry.state['connection_operations'] = {'original': receipt}
        original = copy.deepcopy(self.registry.state)

        def fail_save():
            self.assertEqual(record['status'], 'prepared')
            self.assertEqual(receipt['status'], 'complete')
            self.assertEqual(self.registry.state['revision'], 5)
            raise asyncio.CancelledError()

        with (patch('project_codex_connection.legacy_consumers', return_value=[]),
              patch.object(self.registry, 'save', side_effect=fail_save)):
            with self.assertRaises(asyncio.CancelledError):
                await self.connection.review(4, include_view=False)
        self.assertEqual(self.registry.state, original)
        self.assertIs(self.registry.state['codex_connections'][identifier], record)
        self.assertIs(self.registry.state['connection_operations']['original'], receipt)

    async def test_review_recovers_enrollment_and_rejects_original_prepare_and_activate(self):
        state = self.registry.state
        identifier = self.connection.id
        state['agents'].clear()
        state['codex_connections'].clear()
        state['enrollments'] = {identifier: {'status': 'unknown'}}
        operations = state['connection_operations'] = {
            action: dict(status='unknown', phase='post_mutation', action=action, instance_id=identifier)
            for action in ('prepare', 'activate')}

        async def recover(expected, instance_id):
            self.assertEqual((expected, instance_id), (4, identifier))
            state['agents'][identifier] = {'name': 'Codex'}
            state['enrollments'][identifier]['status'] = 'verified'
            state['revision'] = 5

        with (patch.object(self.registry, 'recover_agent', create=True, new=AsyncMock(side_effect=recover)),
              patch('project_codex_connection.legacy_consumers', return_value=[]),
              patch.object(self.registry, 'save', side_effect=asyncio.CancelledError()) as save):
            with self.assertRaises(asyncio.CancelledError):
                await self.connection.review(4, include_view=False)
            save.assert_called_once()
        self.assertEqual([op['status'] for op in operations.values()], ['unknown', 'unknown'])
        self.assertEqual(state['revision'], 5)
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            self.assertIsNone(await self.connection.review(5, include_view=False))
        for action, error in (('prepare', 'codex_preparation_did_not_install'),
                              ('activate', 'codex_activation_did_not_apply')):
            with self.subTest(action=action):
                receipt = operations[action]
                self.assertEqual((receipt['status'], receipt['outcome'], receipt['error_code']),
                                 ('complete', 'rejected', error))
                self.assertEqual(receipt['result']['revision'], 5)
                self.assertTrue(receipt['result']['connection']['agent_registered'])
        self.assertEqual(self.registry.saves[-1]['connection_operations'], operations)

    async def test_project_level_reenable_of_old_owner_plugin_blocks_global_activation(self):
        project = self.root / 'workspace'; (project / '.codex').mkdir(parents=True)
        (project / '.codex/config.toml').write_text('[plugins."cognee@legacy"]\nenabled = true\n')
        self.before += ('\n[projects.' + json.dumps(str(project)) + ']\ntrust_level = "trusted"\n').encode()
        self.config_file.write_bytes(self.before)
        proposed, _ = proposal(self.before, self.installation)
        private_write(self.connection.root / 'config-proposed.toml', proposed)
        self.preview.update(baseline_sha256=digest(self.before), proposed_sha256=digest(proposed), enabled_plugin='cognee@'+self.installation['marketplace_name'])
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            with self.assertRaisesRegex(PolicyError, 'codex_project_config_requires_review'):
                await self.connection.activate(4, self.preview['proposed_sha256'])
        self.assertEqual(self.config_file.read_bytes(), self.before)

    async def test_trusted_home_workspace_is_the_user_config_not_a_second_override(self):
        self.before += ('\n[projects.' + json.dumps(str(self.home.parent)) + ']\ntrust_level = "trusted"\n').encode()
        self.config_file.write_bytes(self.before)
        proposed, _ = proposal(self.before, self.installation)
        private_write(self.connection.root / 'config-proposed.toml', proposed)
        self.preview.update(baseline_sha256=digest(self.before), proposed_sha256=digest(proposed))
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            result = await self.connection.activate(4, self.preview['proposed_sha256'])
        self.assertEqual(result['status'], 'configured_pending_new_session')

    async def test_post_activation_folder_trust_edit_does_not_invalidate_original_success(self):
        with patch('project_codex_connection.legacy_consumers', return_value=[]):
            await self.connection.activate(4, self.preview['proposed_sha256'])
            self.config_file.write_bytes(self.config_file.read_bytes() + b'\n# trust added by normal CLI\n[projects."/new/repo"]\ntrust_level = "trusted"\n')
            edited = self.config_file.read_bytes()
            result = await self.connection.review(self.registry.state['revision'])
        self.assertEqual(result['status'], 'configured_pending_new_session')
        self.assertEqual(self.config_file.read_bytes(), edited)

    def test_stage_install_detaches_stdin_to_devnull(self):
        installation = {**self.installation, 'home': str(self.home)}
        stage = self.root / 'new-stage'
        with patch('project_codex_connection.subprocess.run', return_value=subprocess.CompletedProcess(['codex'], 0)) as run, \
                patch('project_codex_connection.verify_cache') as verify:
            cache = stage_install(Path(sys.executable), installation, stage)
        self.assertEqual(cache, stage / 'plugins/cache' / installation['marketplace_name'] / 'cognee' / '1.8.0')
        self.assertEqual(run.call_count, 2)
        for call in run.call_args_list:
            self.assertEqual(call.kwargs['stdin'], subprocess.DEVNULL)
        verify.assert_called_once_with(cache, installation)
