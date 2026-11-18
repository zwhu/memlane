from pathlib import Path
import tempfile
import unittest

from generation_config import GenerationStore
from generation_startup import start_generation
from project_policy import PolicyError


class StartupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.store = GenerationStore(Path(self.temp.name) / 'generation.json', 'fixed', legacy_model='old')
        row = self.store.put('candidate', {'mode': 'agent_bridge', 'bridge_kind': 'codex', 'model': 'new'}, 0, 'save')
        self.reference = {'id': row['id'], 'revision': row['revision']}
        self.store.state['validations']['v'] = {'status': 'passed', 'digest': row['digest'],
            'plain_passed': True, 'schema_passed': True, 'quality_passed': True}
        self.store.select_next(self.reference, 1, 'next')

    def tearDown(self):
        self.store.close()
        self.temp.cleanup()

    def test_connected_agent_selection_starts_without_model_validation(self):
        self.store.state['validations'].clear()
        checks=[]
        self.store.connected_bridge_check=lambda profile:checks.append(profile['bridge_kind'])
        self.store.select_next(self.reference,self.store.state['revision'],'connected-next')
        attempts=[]
        start_generation(self.store,lambda ref,digest:attempts.append(ref['id']),lambda:True)
        self.assertEqual(checks,['codex'])
        self.assertEqual(attempts,['candidate'])
        self.assertEqual(self.store.state['active'],self.reference)

    def test_active_commits_only_after_successful_health_and_digest_callback(self):
        def launch(ref, digest):
            self.assertEqual(self.store.state['active']['id'], 'existing-codex')
            self.assertEqual(ref, self.reference)
            self.assertEqual(digest, self.store.resolve(ref)['digest'])
            return 'healthy-owned-processes'
        self.assertEqual(start_generation(self.store, launch, lambda: self.fail('unexpected cleanup')), 'healthy-owned-processes')
        self.assertEqual(self.store.state['active'], self.reference)
        self.assertIsNone(self.store.state['next'])

    def test_failure_recovers_original_once_only_after_confirmed_cleanup(self):
        events = []
        def launch(ref, digest):
            events.append(ref['id'])
            if ref['id'] == 'candidate':
                raise RuntimeError('contains-secret-do-not-copy')
            return 'old-healthy'
        def cleanup():
            events.append('cleaned')
            return True
        self.assertEqual(start_generation(self.store, launch, cleanup), 'old-healthy')
        self.assertEqual(events, ['candidate', 'cleaned', 'existing-codex'])
        self.assertEqual(self.store.state['active']['id'], 'existing-codex')
        self.assertEqual(self.store.state['next'], self.reference)

    def test_unknown_cleanup_or_second_failure_stops_without_more_launches(self):
        attempts = []
        def fail(ref, digest):
            attempts.append(ref['id'])
            raise RuntimeError('unknown')
        with self.assertRaisesRegex(PolicyError, 'generation_cleanup_unconfirmed'):
            start_generation(self.store, fail, lambda: False)
        self.assertEqual(attempts, ['candidate'])
        attempts.clear()
        with self.assertRaisesRegex(PolicyError, 'generation_recovery_failed'):
            start_generation(self.store, fail, lambda: True)
        self.assertEqual(attempts, ['candidate', 'existing-codex'])

    def test_stale_validation_does_not_start_candidate_or_replay_jobs(self):
        self.store.state['validations'].clear()
        attempts = []
        start_generation(self.store, lambda ref, digest: attempts.append(ref['id']), lambda: True)
        self.assertEqual(attempts, ['existing-codex'])
        self.assertEqual(self.store.state['next_error'], 'generation_validation_required')

    def test_first_candidate_has_no_active_until_health_and_digest_match(self):
        self.store.state['active'] = None
        def launch(ref, digest):
            self.assertIsNone(self.store.state['active'])
            self.assertEqual(ref, self.reference)
            return 'first-healthy'
        self.assertEqual(start_generation(self.store, launch, lambda: self.fail('cleanup')), 'first-healthy')
        self.assertEqual(self.store.state['active'], self.reference)
        self.assertIsNone(self.store.state['next'])

    def test_first_candidate_failure_cleans_and_stops_without_fabricated_fallback(self):
        self.store.state['active'] = None
        attempts = []
        cleanups = []
        def fail(ref, digest):
            attempts.append(ref)
            raise RuntimeError('private-upstream-error')
        with self.assertRaisesRegex(PolicyError, '^generation_start_failed$'):
            start_generation(self.store, fail, lambda: cleanups.append(True) or True)
        self.assertEqual(attempts, [self.reference])
        self.assertEqual(len(cleanups), 1)
        self.assertIsNone(self.store.state['active'])
        self.assertEqual(self.store.state['next'], self.reference)

    def test_first_setup_without_validated_selection_never_launches(self):
        self.store.state['active'] = None
        self.store.state['validations'].clear()
        with self.assertRaisesRegex(PolicyError, '^active_generation_required$'):
            start_generation(self.store, lambda *_: self.fail('unvalidated launch'), lambda: self.fail('cleanup'))
        self.assertEqual(self.store.state['next_error'], 'generation_validation_required')


if __name__ == '__main__':
    unittest.main()
