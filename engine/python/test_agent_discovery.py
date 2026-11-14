"""Read-only local discovery; never runs host executables or installers."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from agent_connections import Connections, detect_executable
from project_control import Registry


class LocalDiscoveryTests(unittest.TestCase):
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
                    self.assertNotIn(row['instance_id'], connections.adapters)
            finally:
                registry.close()
