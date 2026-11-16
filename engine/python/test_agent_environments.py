import tempfile
import unittest
from pathlib import Path

from agent_environments import environment, validate_daily_environments, ROOT


class EnvironmentTests(unittest.TestCase):
    def test_explicit_test_profile_is_rejected_for_daily_use(self):
        config = {'agent_hosts': {'traex': {'profile': str(ROOT / '.local/multi-agent-ollama-20261114/native-setup/traex-profile')}}}
        self.assertEqual(environment(config, {}, 'traex', environ={})['environment_issue'], 'verification_environment')
        with self.assertRaisesRegex(ValueError, 'verification environment'):
            validate_daily_environments(config)

    def test_default_custom_and_conflicting_environment_are_distinct(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            default, custom = root / '.trae', root / 'my custom profile'
            default.mkdir(); custom.mkdir()
            row = environment({}, {}, 'traex', home=root, environ={})
            self.assertEqual(row['profile'], str(default))
            row = environment({'agent_hosts': {'traex': {'profile': str(custom)}}}, {}, 'traex', home=root, environ={})
            self.assertEqual(row['profile'], str(custom))
            self.assertEqual(row['environment_issue'], '')
            config = {'agent_hosts': {'traex': {'profile': str(custom)}}}
            row = environment(config, {}, 'traex', home=root, environ={'TRAE_HOME': str(default)})
            self.assertEqual(row['environment_issue'], 'environment_ambiguous')
            row = environment(config, {'host_targets': {'traex': str(custom)}}, 'traex', home=root, environ={'TRAE_HOME': str(default)})
            self.assertEqual(row['environment_issue'], '')
            self.assertEqual(len(row['environments']), 2)

    def test_missing_directory_is_not_created_and_generic_test_name_is_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            profile = root / 'test user profile'
            config = {'agent_hosts': {'pi': {'profile': str(profile)}}}
            self.assertEqual(environment(config, {}, 'pi', home=root, environ={})['environment_issue'], 'environment_missing')
            self.assertFalse(profile.exists())
            profile.mkdir()
            self.assertEqual(environment(config, {}, 'pi', home=root, environ={})['environment_issue'], '')
