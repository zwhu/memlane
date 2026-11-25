import tempfile
from pathlib import Path
import unittest
from unittest.mock import Mock

from generation_config import GenerationStore
from project_policy import PolicyError


class ConnectedBridgeSelectionTests(unittest.TestCase):
    def test_selection_checks_runtime_only_after_validation(self):
        with tempfile.TemporaryDirectory() as directory:
            store = GenerationStore(Path(directory) / 'generation.json', 'fixture')
            self.addCleanup(store.close)
            profile = {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'fixture'}
            saved = store.put('bridge', profile, 0, 'save')
            ref = {key: saved[key] for key in ('id', 'revision')}
            store.bridge_runtime_check = Mock(side_effect=PolicyError('generation_bridge_authentication_required', 409))
            store.state['connected_agent_selections'] = {saved['digest']: True}
            with self.assertRaisesRegex(PolicyError, 'generation_validation_required'):
                store.select_next(ref, store.state['revision'], 'select-unvalidated')
            store.bridge_runtime_check.assert_not_called()

            store.state['validations']['fixture'] = {
                'digest': saved['digest'], 'status': 'passed', 'plain_passed': True,
                'schema_passed': True, 'quality_passed': True,
            }
            with self.assertRaisesRegex(PolicyError, 'generation_bridge_authentication_required'):
                store.select_next(ref, store.state['revision'], 'select-missing-auth')
            store.bridge_runtime_check.assert_called_once_with(profile)
            self.assertIsNone(store.state['next'])
            self.assertIsNone(store.state['active'])
