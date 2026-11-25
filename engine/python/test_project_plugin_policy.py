import json
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

import project_plugin_policy as policy
from project_access_hooks import pending_capture_allowed


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

    def test_empty_common_skips_search_and_preserves_project_with_final_policy_check(self):
        dataset = '11111111-1111-4111-8111-111111111111'
        initial = {'revision': 4, 'policy': {'permissions': ['read']},
                   'common': {'ready': True, 'readable': True, 'dataset_id': dataset}}
        for rows, final, expected in (([], initial, 'project context'),
                                      ([], {**initial, 'revision': 5}, ''),
                                      ([{'id': 'raw', 'datasetId': dataset, 'externalMetadata': {}}], initial, 'project context')):
            with self.subTest(rows=rows, revision=final['revision']):
                calls = []
                def request(path, payload=None, **options):
                    calls.append(path)
                    if path.startswith('/api/v1/datasets/'):
                        return rows
                    raise AssertionError('empty common dataset reached search')
                self.fake_common._json_http_request = request
                with patch.dict(sys.modules, {'_coding_memory': types.SimpleNamespace(_raw_text=lambda *a: 'unused')}),                         patch.object(policy, 'current', side_effect=[initial, final]) as current:
                    self.assertEqual(policy._merge_block('question', '.', lambda *a: 'project context'), expected)
                self.assertEqual(current.call_count, 2)
                self.assertEqual(calls, ['/api/v1/datasets/' + dataset + '/data?limit=30&offset=0'])

    def test_common_candidates_still_use_search_and_keep_failure_visible_upstream(self):
        dataset = '11111111-1111-4111-8111-111111111111'
        initial = {'revision': 4, 'policy': {'permissions': ['read']},
                   'common': {'ready': True, 'readable': True, 'dataset_id': dataset}}
        calls = []
        def request(path, payload=None, **options):
            calls.append(path)
            if path.startswith('/api/v1/datasets/'):
                return [{'id': 'knowledge', 'datasetId': dataset,
                         'externalMetadata': {'memlane_kind': 'common_knowledge', 'category': 'preference'}}]
            raise OSError('existing candidate retrieval failed')
        self.fake_common._json_http_request = request
        with patch.dict(sys.modules, {'_coding_memory': types.SimpleNamespace(_raw_text=lambda *a: 'unused')}),                 patch.object(policy, 'current', side_effect=[initial, initial]):
            self.assertEqual(policy._merge_block('question', '.', lambda *a: 'project context'), 'project context')
        self.assertEqual(calls[-1], '/api/v1/search')

    def test_disabled_cursor_uses_stat_without_reading_transcript(self):
        transcript = self.root / 'transcript.jsonl'
        transcript.write_text('private disabled content\n')
        original = Path.open

        def guarded(path, *args, **kwargs):
            if path == transcript:
                raise AssertionError('disabled transcript was opened')
            return original(path, *args, **kwargs)
        with patch.object(policy, 'capture_decision', return_value=('disabled', 1)), patch.object(Path, 'open', guarded):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
        with policy._state() as state:
            self.assertEqual(state['cursor']['size'], transcript.stat().st_size)
            self.assertNotIn('private', json.dumps(state))

    def test_explicit_reads_do_not_require_writable_policy_files(self):
        dataset = '11111111-1111-4111-8111-111111111111'
        with policy._state() as state:
            state['binding'] = {'project_id': 'fixture', 'dataset_id': dataset}
        files = {p: p.read_bytes() for p in (self.root / 'memlane-policy').iterdir()}
        original = Path.open
        def read_only(path, mode='r', *args, **kwargs):
            if any(flag in mode for flag in ('w', 'a', '+')):
                raise PermissionError('fixture: sandbox denies writes outside workspace')
            return original(path, mode, *args, **kwargs)
        with patch.object(Path, 'open', read_only), patch.object(Path, 'mkdir', side_effect=PermissionError('read only')), \
                patch.object(policy, '_request', return_value={'ready': True, 'dataset_id': dataset, 'policy': {'permissions': ['read']}}):
            self.assertEqual(policy.dataset(), dataset)
            self.assertTrue(policy.permit('read'))
        self.assertEqual({p: p.read_bytes() for p in files}, files)

    def test_restore_establishes_boundary_and_never_backfills(self):
        transcript = self.root / 'transcript.jsonl'
        transcript.write_text('disabled\n')
        with patch.object(policy, 'capture_decision', return_value=('disabled', 1)):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
        transcript.write_text('disabled\nstill disabled\nfirst resumed turn\n')
        with patch.object(policy, 'capture_decision', return_value=('enabled', 1)):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
            boundary = transcript.stat().st_size
            with transcript.open('a') as output:
                output.write('allowed next turn\n')
            self.assertEqual(policy.capture_boundary(str(transcript)), boundary)

    def test_truncation_does_not_reset_cursor(self):
        transcript = self.root / 'transcript.jsonl'
        transcript.write_text('disabled long turn\n')
        with patch.object(policy, 'capture_decision', return_value=('disabled', 1)):
            policy.capture_boundary(str(transcript))
        transcript.write_text('short\n')
        with patch.object(policy, 'capture_decision', return_value=('enabled', 1)):
            self.assertIsNone(policy.capture_boundary(str(transcript)))
            transcript.write_text('longer replacement content\n' * 10)
            self.assertIsNone(policy.capture_boundary(str(transcript)))

    def test_blocked_buffer_remains_blocked_after_permission_restored(self):
        allowed = {'ready': True, 'revision': 4,
                   'policy': {'permissions': ['read', 'write', 'delete'], 'can_capture': True, 'can_auto_process': True}}
        with patch.object(policy, 'current', return_value=allowed), patch.object(policy, 'capture_status', return_value='enabled'):
            self.assertTrue(policy.permit('drain'))
            policy.block(403)
            self.assertFalse(policy.permit('drain'))
            self.assertTrue(policy.permit('capture'))
            self.assertTrue(policy.permit('process', automatic=False))

    def test_project_policy_cache_controls_offline_cursor_and_binding(self):
        dataset = '11111111-1111-4111-8111-111111111111'
        transcript = self.root / 'long-session.jsonl'
        transcript.write_text('first turn\n')
        decision = {'ready': True, 'project_id': 'fixture', 'dataset_id': dataset}

        def snapshot(enabled, version):
            return {**decision, 'can_capture': enabled, 'capture_policy': {
                'enabled': enabled, 'project_enabled': enabled, 'version': version,
                'scope': 'ok' if enabled else 'project', 'updated_at': '2026-11-25T10:00:00+00:00'}}

        with patch.object(policy, '_request', return_value=snapshot(True, 1)):
            self.assertEqual(policy.bind(str(self.root), 'host-one'), dataset)
            self.assertEqual(policy.capture_boundary(transcript), 0)
        with patch.object(policy, '_request', side_effect=policy.NotReady('offline')):
            self.assertIsNone(policy.capture_boundary(transcript))
            self.assertFalse(policy.cached_capture_disabled(str(self.root)))
            with policy._state(writable=False) as state:
                self.assertNotIn('cursor', state)
                self.assertNotIn('paused', state)

        with patch.object(policy, '_request', return_value=snapshot(False, 2)):
            self.assertIsNone(policy.capture_boundary(transcript))
        cache = policy._capture_cache_path('fixture')
        self.assertEqual(cache.stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads(cache.read_text())['version'], 2)
        transcript.write_text('first turn\nprivate closed turn\n')
        with patch.object(policy, '_request', side_effect=policy.NotReady('offline')):
            self.assertEqual(policy.bind(str(self.root), 'host-one'), dataset)
            with self.assertRaises(policy.NotReady):
                policy.bind(str(self.root / 'other-workspace'), 'host-one')
            self.assertIsNone(policy.capture_boundary(transcript))
            with policy._state(writable=False) as state:
                self.assertTrue(state['paused'])
                self.assertEqual(state['cursor']['size'], transcript.stat().st_size)
                self.assertNotIn('blocked', state)
        with patch.object(policy, '_request', return_value=snapshot(True, 3)):
            self.assertIsNone(policy.capture_boundary(transcript))
            offset = transcript.stat().st_size
            with transcript.open('a') as output:
                output.write('allowed again\n')
            self.assertEqual(policy.capture_boundary(transcript), offset)
        with patch.object(policy, '_request', return_value=snapshot(True, 5)):
            # A full close/reopen may complete while the host is idle.
            self.assertIsNone(policy.capture_boundary(transcript))
        with patch.object(policy, '_request', return_value=snapshot(False, 4)):
            self.assertEqual(policy.capture_decision(), ('enabled', 5))
            self.assertEqual(policy.capture_boundary(transcript), transcript.stat().st_size)
        self.assertEqual(json.loads(cache.read_text())['version'], 5)

    def test_late_close_cannot_replace_reopened_decision_or_advance_cursor(self):
        dataset = '11111111-1111-4111-8111-111111111111'
        transcript = self.root / 'reopened.jsonl'
        transcript.write_text('prompt written after reopening')
        with policy._state() as state:
            state['binding'] = {'project_id': 'fixture', 'dataset_id': dataset}

        def response(enabled, version):
            return {'ready': True, 'dataset_id': dataset, 'can_capture': enabled,
                    'capture_policy': {'enabled': enabled, 'project_enabled': enabled,
                                       'version': version, 'scope': 'ok' if enabled else 'project'}}

        with patch.object(policy, '_request', return_value=response(True, 3)):
            self.assertEqual(policy.capture_decision(), ('enabled', 3))
            self.assertEqual(policy.capture_boundary(transcript), 0)
        with patch.object(policy, '_request', return_value=response(False, 2)):
            self.assertEqual(policy.capture_decision(), ('enabled', 3))
            self.assertEqual(policy.capture_boundary(transcript), 0)
        with policy._state(writable=False) as state:
            self.assertFalse(state.get('paused'))
            self.assertNotIn('cursor', state)
            self.assertEqual(state['capture_policy_version'], 3)

    def test_denied_transcript_advances_without_backfilling_at_same_policy_version(self):
        transcript = self.root / 'rollout.jsonl'
        transcript.write_text('before denial')
        dataset = '11111111-1111-4111-8111-111111111111'
        with policy._state() as state:
            state['binding'] = {'project_id': 'fixture', 'dataset_id': dataset}
        snapshot = {'ready': True, 'dataset_id': dataset, 'can_capture': False,
                    'capture_policy': {'enabled': False, 'project_enabled': True, 'version': 1, 'scope': 'ok'}}
        with patch.object(policy, '_request', return_value=snapshot):
            self.assertIsNone(policy.capture_boundary(transcript))
            with policy._state(writable=False) as state:
                self.assertTrue(state.get('paused'))
                self.assertEqual(state['cursor']['size'], transcript.stat().st_size)
            transcript.write_text('before denial, denied and interrupted')
            snapshot['can_capture'] = snapshot['capture_policy']['enabled'] = True
            self.assertIsNone(policy.capture_boundary(transcript))
            offset = transcript.stat().st_size
            with transcript.open('a') as out:
                out.write('restored')
            self.assertEqual(policy.capture_boundary(transcript), offset)

    def test_pending_prompt_cleanup_is_session_scoped_and_requires_confirmed_pause(self):
        path = self.root / 'pending.json'
        pending = {'host:': {'prompt': 'old alias'}, 'host:old': {'prompt': 'old turn'},
                   'other:': {'prompt': 'other session'}}
        path.write_text(json.dumps(pending))
        common = types.SimpleNamespace(_pending_file=lambda _: path, _pending_keys=lambda *_: ('host:old', 'host:'),
                                       _load_json_file=lambda path: json.loads(path.read_text()),
                                       _write_json_file=lambda path, value: path.write_text(json.dumps(value)))
        with patch.object(policy, 'capture_decision') as status:
            status.return_value = ('unknown', None)
            self.assertFalse(pending_capture_allowed('session', policy_mod=policy, common_mod=common))
            self.assertEqual(json.loads(path.read_text()), pending)
            status.return_value = ('disabled', 2)
            self.assertFalse(pending_capture_allowed('session', policy_mod=policy, common_mod=common))
            self.assertEqual(json.loads(path.read_text()), {'other:': {'prompt': 'other session'}})

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

    def test_cursor_bind_reports_cursor_manifest_version_and_runtime_profile(self):
        plugin_root = self.root / 'cursor-plugin'
        scripts = plugin_root / 'scripts'
        scripts.mkdir(parents=True)
        (plugin_root / '.cursor-plugin').mkdir()
        (plugin_root / '.cursor-plugin/plugin.json').write_text(json.dumps({'name': 'cognee', 'version': '1.9.0'}))
        cursor_profile = self.root / 'cursor-profile'
        cursor_profile.mkdir()
        (plugin_root / '.memlane-installation.json').write_text(json.dumps({
            'instance_id': 'cursor-test',
            'host_kind': 'cursor',
            'env_file': str(self.root / 'cursor.env'),
            'runtime_profile': str(cursor_profile),
        }))
        captured = {}
        dataset = '11111111-1111-4111-8111-111111111111'

        def fake_request(path, payload=None, **kwargs):
            captured['path'] = path
            captured['payload'] = payload
            return {'ready': True, 'project_id': 'proj-1', 'dataset_id': dataset}

        with patch.object(policy, '__file__', str(scripts / '_memlane_policy.py')), \
                patch.object(policy, '_request', side_effect=fake_request):
            self.assertEqual(policy.bind(str(self.root), 'cursor-host-1'), dataset)
        env = captured['payload']['host_environment']
        self.assertEqual(env['plugin_version'], '1.9.0')
        self.assertEqual(Path(env['runtime_profile']).resolve(), cursor_profile.resolve())

        # Explicit CURSOR_CONFIG_DIR overrides binding runtime_profile fallback.
        override_profile = self.root / 'cursor-env-profile'
        override_profile.mkdir()
        with patch.dict('os.environ', {'CURSOR_CONFIG_DIR': str(override_profile)}), \
                patch.object(policy, '__file__', str(scripts / '_memlane_policy.py')), \
                patch.object(policy, '_request', side_effect=fake_request):
            self.assertEqual(policy.bind(str(self.root), 'cursor-host-2'), dataset)
        self.assertEqual(Path(captured['payload']['host_environment']['runtime_profile']).resolve(), override_profile.resolve())

    def test_merge_block_uses_common_memory_terminology(self):
        dataset = '11111111-1111-4111-8111-111111111111'
        initial = {'revision': 4, 'policy': {'permissions': ['read']},
                   'common': {'ready': True, 'readable': True, 'dataset_id': dataset}}

        def request(path, payload=None, **options):
            if path.startswith('/api/v1/datasets/'):
                return [{'id': 'doc-1', 'datasetId': dataset,
                         'externalMetadata': {'memlane_kind': 'common_knowledge', 'category': 'preference', 'source': 'manual'}}]
            return [{'dataset_id': dataset, 'search_result': [{'document_id': 'doc-1'}]}]

        self.fake_common._json_http_request = request
        with patch.dict(sys.modules, {'_coding_memory': types.SimpleNamespace(_raw_text=lambda *a: 'prefer concise answers')}), \
                patch.object(policy, 'current', side_effect=[initial, initial]):
            block = policy._merge_block('question', '.', lambda *a: 'project context')
        self.assertIn('来源：通用记忆／个人偏好', block)
        self.assertNotIn('来源：通用知识／', block)


if __name__ == '__main__':
    unittest.main()
