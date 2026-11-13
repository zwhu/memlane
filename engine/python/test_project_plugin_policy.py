import json
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

import project_plugin_policy as policy


class PluginPolicyTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.token = self.root / 'control.token'
        self.token.write_text('x' * 40)
        self.token.chmod(0o600)
        self.fake_common = types.SimpleNamespace(_PLUGIN_DIR=self.root,
                                                get_session_key=lambda: 'host-one',
                                                _read_map_record=lambda key: {})
        self.environment = patch.dict('os.environ', {'MEMLANE_PROJECT_CONTROL_URL': 'http://127.0.0.1:18777',
                                                     'MEMLANE_PROJECT_CONTROL_TOKEN_FILE': str(self.token)})
        self.module = patch.dict(sys.modules, {'_plugin_common': self.fake_common})
        self.environment.start()
        self.module.start()

    def tearDown(self):
        self.module.stop()
        self.environment.stop()
        self.temporary.cleanup()

    def test_disabled_cursor_uses_stat_without_reading_transcript(self):
        transcript = self.root / 'transcript.jsonl'
        transcript.write_text('private disabled content\n')
        original = Path.open

        def guarded(path, *args, **kwargs):
            if path == transcript:
                raise AssertionError('disabled transcript was opened')
            return original(path, *args, **kwargs)
        with patch.object(policy, 'permit', return_value=False), patch.object(Path, 'open', guarded):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
        with policy._state() as state:
            self.assertEqual(state['cursor']['size'], transcript.stat().st_size)
            self.assertNotIn('private', json.dumps(state))

    def test_restore_establishes_boundary_and_never_backfills(self):
        transcript = self.root / 'transcript.jsonl'
        transcript.write_text('disabled\n')
        with patch.object(policy, 'permit', return_value=False):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
        transcript.write_text('disabled\nstill disabled\nfirst resumed turn\n')
        with patch.object(policy, 'permit', return_value=True):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
            boundary = transcript.stat().st_size
            with transcript.open('a') as output:
                output.write('allowed next turn\n')
            self.assertEqual(policy.capture_boundary(str(transcript)), boundary)

    def test_truncation_does_not_reset_cursor(self):
        transcript = self.root / 'transcript.jsonl'
        transcript.write_text('disabled long turn\n')
        with patch.object(policy, 'permit', return_value=False):
            policy.capture_boundary(str(transcript))
        transcript.write_text('short\n')
        with patch.object(policy, 'permit', return_value=True):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
            transcript.write_text('longer replacement content\n' * 10)
            self.assertIsNone(policy.capture_boundary(str(transcript)))

    def test_blocked_buffer_remains_blocked_after_permission_restored(self):
        allowed = {'ready': True, 'revision': 4,
                   'policy': {'permissions': ['read', 'write', 'delete'], 'can_capture': True, 'can_auto_process': True}}
        with patch.object(policy, 'current', return_value=allowed):
            self.assertTrue(policy.permit('drain'))
            policy.block(403)
            self.assertFalse(policy.permit('drain'))
            self.assertTrue(policy.permit('capture'))
            self.assertTrue(policy.permit('process', automatic=False))

    def test_auto_process_off_does_not_reject_manual(self):
        current = {'revision': 2, 'policy': {'permissions': ['read', 'write', 'delete'],
                                           'can_capture': True, 'can_auto_process': False}}
        with patch.object(policy, 'current', return_value=current):
            self.assertFalse(policy.permit('process', automatic=True))
            self.assertTrue(policy.permit('process', automatic=False))

    def test_existing_unmanaged_launch_is_not_migrated(self):
        self.fake_common._read_map_record = lambda key: {'dataset': 'old-project'}
        with patch.object(policy, '_request') as request:
            with self.assertRaisesRegex(policy.NotReady, 'existing_session'):
                policy.bind(str(self.root), 'host-one')
            request.assert_not_called()


if __name__ == '__main__':
    unittest.main()
