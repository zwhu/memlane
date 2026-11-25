"""Read-only local discovery; never runs host executables or installers."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from agent_connections import Connections, detect_executable
from agent_bridge_runtime import resolve_bridge_runtime
from agent_discovery import default_profile, detect_executable as discover_executable
from project_control import Registry


class LocalDiscoveryTests(unittest.TestCase):
    def test_codex_app_only_discovers_bundled_runtime_without_a_path_command(self):
        for app in ('Codex.app', 'ChatGPT.app'):
            with self.subTest(app=app), tempfile.TemporaryDirectory() as tmp:
                home = Path(tmp)
                runtime = home / 'Applications' / app / 'Contents/Resources/codex-cli/bin/codex'
                runtime.parent.mkdir(parents=True)
                runtime.write_text('# synthetic nonexecuted runtime')
                runtime.chmod(0o700)
                with patch('agent_connections.Path.home', return_value=home), \
                     patch('agent_connections.shutil.which', return_value=None), \
                     patch('pathlib.Path.is_file', autospec=True, side_effect=lambda path: path == runtime):
                    self.assertEqual(detect_executable('codex', {}), str(runtime))
                    runtime.chmod(0o600)
                    self.assertIsNone(detect_executable('codex', {}))

    def test_codex_explicit_missing_runtime_does_not_switch_to_the_app(self):
        with patch('agent_connections.shutil.which', return_value='/synthetic/codex') as lookup:
            self.assertIsNone(detect_executable('codex', {'executable': '/synthetic/missing-codex'}))
            lookup.assert_not_called()

    def test_refresh_detects_install_and_removal_without_writing_host_or_registry(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            registry = Registry(home / 'private/registry.json', object())
            try:
                registry.save()
                before = registry.path.read_bytes()
                with patch('agent_connections.Path.home', return_value=home), patch('agent_connections.shutil.which', return_value=None):
                    connections = Connections(registry, {})
                    self.assertFalse(next(r for r in connections.view()['connections'] if r['kind'] == 'pi')['detected'])
                    executable = home / '.local/bin/pi'
                    executable.parent.mkdir(parents=True)
                    executable.write_text('# synthetic nonexecuted file')
                    executable.chmod(0o700)
                    first = next(r for r in connections.view()['connections'] if r['kind'] == 'pi')
                    self.assertTrue(first['detected'])
                    executable.unlink()
                    last = next(r for r in connections.view()['connections'] if r['kind'] == 'pi')
                    self.assertFalse(last['detected'])
                    self.assertEqual(first['instance_id'], last['instance_id'])
                    self.assertFalse((home / '.pi').exists())
                    self.assertEqual(before, registry.path.read_bytes())
                    self.assertEqual(registry.state['agents'], {})
            finally:
                registry.close()

    def test_explicit_missing_executable_does_not_fall_back_to_other_profile(self):
        with patch('agent_connections.shutil.which', return_value='/synthetic/other-profile') as lookup:
            self.assertIsNone(detect_executable('pi', {'executable': '/synthetic/missing-explicit'}))
            lookup.assert_not_called()

    def test_private_opencode_install_detected_but_binding_remains_unavailable(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            binary = home / '.opencode/bin/opencode'
            binary.parent.mkdir(parents=True)
            binary.write_text('# synthetic nonexecuted file')
            binary.chmod(0o700)
            registry = Registry(home / 'private/registry.json', object())
            try:
                with patch('agent_connections.Path.home', return_value=home), patch('agent_connections.shutil.which', return_value=None):
                    connections = Connections(registry, {})
                    row = next(r for r in connections.view()['connections'] if r['kind'] == 'opencode')
                    self.assertTrue(row['detected'])
                    self.assertEqual(row['status'], 'integration_pending')
                    self.assertIn(row['instance_id'], connections.adapters)
                    self.assertIsNone(connections.adapters[row['instance_id']].artifact)
            finally:
                registry.close()


class AgentDiscoveryTests(unittest.TestCase):
    def test_bundle_and_user_bin_discovery(self):
        for kind, relative in (
            ('codex', 'Applications/Codex.app/Contents/Resources/codex-cli/bin/codex'),
            ('codex', 'Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex'),
            ('codex', '.bun/bin/codex'), ('codex', '.local/bin/codex'),
            ('pi', '.local/bin/pi'), ('traex', '.bun/bin/traex'),
            ('trae_ide', 'Applications/Trae CN.app/Contents/MacOS/Electron'),
            ('trae_ide', 'Applications/TRAE CN.app/Contents/MacOS/Electron'),
            ('antigravity', 'Applications/Gemini.app/Contents/Helpers/agy'),
            ('cursor', 'Applications/Cursor.app/Contents/Resources/app/bin/cursor'),
            ('opencode', '.opencode/bin/opencode'),
        ):
            with self.subTest(kind=kind, relative=relative), tempfile.TemporaryDirectory() as directory:
                home = Path(directory)
                executable = home / relative
                executable.parent.mkdir(parents=True)
                executable.touch(mode=0o700)
                with patch.object(Path, 'is_file', autospec=True, side_effect=lambda path: path == executable):
                    self.assertEqual(discover_executable(kind, home=home, which=lambda _: None), str(executable))
                    if kind in ('codex', 'pi', 'traex', 'antigravity'):
                        with patch('agent_bridge_runtime.Path.home', return_value=home), \
                             patch('agent_bridge_runtime.shutil.which', return_value=None):
                            selected = resolve_bridge_runtime({}, {'bridge_kind': kind})
                        self.assertEqual(selected['executable'], str(executable))
                        self.assertEqual(selected['home'], default_profile(kind, home=home))

    def test_homebrew_and_bundle_precedence(self):
        for paths in (
            ('/opt/homebrew/bin/codex', '/usr/local/bin/codex'),
            ('/profile/Applications/Codex.app/Contents/Resources/codex-cli/bin/codex',
             '/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex'),
        ):
            with self.subTest(paths=paths), patch.object(Path, 'is_file', autospec=True,
                     side_effect=lambda path: str(path) in paths), patch('agent_discovery.os.access', return_value=True):
                self.assertEqual(discover_executable('codex', home='/profile', which=lambda _: None), paths[0])

    def test_path_alias_and_authoritative_explicit_locations(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'traex'
            path.touch(mode=0o700)
            which = Mock(side_effect=lambda command: str(path) if command == 'traex' else None)
            self.assertEqual(discover_executable('traex', home=directory, which=which), str(path))
            self.assertEqual([call.args[0] for call in which.call_args_list], ['traecli', 'traex'])
            which.reset_mock()
            missing = str(path.with_name('not-installed'))
            self.assertIsNone(discover_executable('traex', {'executable': missing}, str(path), which=which))
            self.assertIsNone(discover_executable('traex', legacy=missing, which=which))
            self.assertIsNone(discover_executable('traex', {'executable': 'traex'}, which=which))
            which.assert_not_called()

    def test_default_profiles(self):
        self.assertEqual(default_profile('codex', home='/profile'), '/profile/.codex')
        self.assertEqual(default_profile('opencode', home='/profile', environ={}), '/profile/.config/opencode')
        self.assertEqual(default_profile('opencode', home='/profile', environ={'XDG_CONFIG_HOME': '/xdg'}), '/xdg/opencode')
