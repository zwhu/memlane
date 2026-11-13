import asyncio
import copy
import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

import tomlkit
from project_codex_connection import CodexConnection, digest, proposal, legacy_consumers
from project_control import private_write
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
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
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

    async def asyncTearDown(self):
        self.temporary.cleanup()

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
        with patch('project_codex_connection.stage_install', side_effect=AssertionError('must not reinstall')):
            result = await self.connection.review(4)
        self.assertEqual(result['status'], 'prepared')
        self.assertEqual(self.config_file.read_bytes(), self.before)

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
