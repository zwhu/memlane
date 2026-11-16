"""Backlog processing delegates policy and submission to the host plugin."""
import types
import unittest
import contextlib
import copy
from unittest.mock import Mock

from project_backlog_worker import process, check


class PendingResultCheckTests(unittest.TestCase):
    def setUp(self):
        self.entries = [{'type':'qa', 'question':'same', '_replay_ambiguous':True}]
        self.common = types.SimpleNamespace(set_session_key=Mock(), _read_map_record=lambda host:{'session_id':'session'},
            _AMBIGUOUS_KEY='_replay_ambiguous', _buffer_lock=contextlib.nullcontext,
            get_session_detail_via_http=Mock(return_value={'session_id':'session','dataset_id':'dataset'}),
            _server_session_fingerprints=lambda detail:{'same'}, _entry_fingerprint=lambda entry:entry['question'])
        self.policy = types.SimpleNamespace(_pending_identity=Mock(), require=Mock(),
            _pending_snapshot=lambda *args:(None,None,None,None,self.entries,None),
            _state=contextlib.nullcontext, NotReady=RuntimeError)
        self.policy._state = lambda:contextlib.nullcontext({})

    def test_legacy_content_match_never_resends_or_deletes_original(self):
        before = copy.deepcopy(self.entries)
        result = check(self.common,self.policy,'dataset','host')
        self.assertEqual((result['status'],result['unknown'],result['matched']),('unconfirmed',1,1))
        self.assertEqual(self.entries,before)
        self.policy.require.assert_called_once_with('read','dataset')

    def test_missing_or_wrong_original_session_is_not_saved_or_unsaved_proof(self):
        for detail in (None, {'session_id':'other','dataset_id':'dataset'}, {'session_id':'session','dataset_id':'other'}):
            self.common.get_session_detail_via_http.return_value=detail
            with self.assertRaisesRegex(RuntimeError,'official_review_unavailable'):
                check(self.common,self.policy,'dataset','host')
        self.assertEqual(len(self.entries),1)


class OfficialProcessingTests(unittest.TestCase):
    def setUp(self):
        self.payload = {'dataset': 'dataset', 'session_id': 'session'}
        self.common = types.SimpleNamespace(improve_throttle_reason=Mock(return_value=''),
            run_session_improve_detailed=Mock(return_value={'ok': True}))
        self.policy = types.SimpleNamespace(_pending_identity=Mock(), require=Mock())

    def test_uses_official_submission_with_current_identity_and_auto_policy(self):
        self.assertEqual(process(self.common, self.policy, self.payload), {'status': 'submitted'})
        self.policy._pending_identity.assert_called_once_with('dataset', 'session')
        self.policy.require.assert_called_once_with('process', 'dataset', automatic=True)
        self.common.run_session_improve_detailed.assert_called_once_with('dataset', 'session', trigger='auto')

    def test_incremental_admission_uses_official_cooldown_configuration(self):
        import os
        from unittest.mock import patch
        with patch.dict(os.environ, {}, clear=True):
            process(self.common, self.policy, {**self.payload, 'incremental_cooldown_seconds': 300})
            self.assertEqual(os.environ['COGNEE_IMPROVE_COOLDOWN'], '300')
        self.common.improve_throttle_reason.assert_called_once_with('session')

    def test_official_cooldown_backoff_and_no_new_entries_never_submit(self):
        for reason, status in [('cooldown','deferred'), ('backoff','deferred'), ('no_new_entries','skipped')]:
            with self.subTest(reason=reason):
                self.common.improve_throttle_reason.return_value = reason
                self.assertEqual(process(self.common, self.policy, self.payload), {'status':status, 'reason':reason})
        self.common.run_session_improve_detailed.assert_not_called()

    def test_uncertain_submit_is_not_treated_as_safe_to_retry(self):
        for reason in ('failed', 'incomplete_drain', ''):
            self.common.run_session_improve_detailed.return_value = {'ok':False, 'reason':reason}
            self.assertEqual(process(self.common, self.policy, self.payload)['status'], 'unknown')
        for reason in ('busy', 'unreachable'):
            self.common.run_session_improve_detailed.return_value = {'ok':False, 'reason':reason}
            self.assertEqual(process(self.common, self.policy, self.payload)['status'], 'deferred')

    def test_permission_failure_never_calls_official_submission(self):
        self.policy.require.side_effect = RuntimeError('permission unconfirmed')
        with self.assertRaises(RuntimeError): process(self.common, self.policy, self.payload)
        self.common.run_session_improve_detailed.assert_not_called()
