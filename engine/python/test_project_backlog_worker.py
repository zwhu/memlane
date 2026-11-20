"""Backlog processing delegates policy and submission to the host plugin."""
import types
import unittest
import contextlib
import copy
import json
import math
import os
import urllib.error
import uuid
from unittest.mock import Mock

from project_backlog_worker import process, check, review, save, entry_key
from project_capture_patch import apply as apply_capture_patch
from test_plugin_capture_drain import DATASET, HOST, PYTHON, SESSION, UPSTREAM, DrainFixture, qa
import tempfile
from pathlib import Path
from unittest.mock import patch
import sys


class PendingResultCheckTests(unittest.TestCase):
    def setUp(self):
        self.entries = [{'type':'qa', 'question':'same', '_replay_ambiguous':True}]
        self.common = types.SimpleNamespace(set_session_key=Mock(), _read_map_record=lambda host:{'session_id':'session'},
            _AMBIGUOUS_KEY='_replay_ambiguous', _buffer_lock=contextlib.nullcontext,
            get_session_detail_via_http=Mock(return_value={'session_id':'session','dataset_id':'dataset'}),
            _server_session_fingerprints=lambda detail:{'same'}, _entry_fingerprint=lambda entry:entry['question'])
        self.policy = types.SimpleNamespace(_pending_identity=Mock(), require=Mock(),
            _pending_snapshot=lambda *args:(None,None,None,{},self.entries,None),
            _state=contextlib.nullcontext, NotReady=RuntimeError)
        self.policy._state = lambda:contextlib.nullcontext({})

    def test_mixed_project_review_excludes_explicit_rejection_not_numbered_unknown(self):
        self.entries = [{'type': 'qa', 'question': 'same', '_capture_id': 'original', '_replay_ambiguous': True}]
        before = copy.deepcopy(self.entries)
        with patch('project_backlog_worker.numbered', return_value=True):
            self.policy._state = lambda: contextlib.nullcontext({'blocked': {'reason': 'capture_request_rejected'}})
            denied = review(self.common, self.policy, 'dataset', 'rejected-source')
            self.policy._state = lambda: contextlib.nullcontext({})
            recoverable = review(self.common, self.policy, 'dataset', 'recoverable-source')
        self.assertEqual(denied['tokens'], [])
        self.assertEqual(len(recoverable['tokens']), 1)
        self.assertEqual(self.entries, before)

    def test_saved_tokens_are_rechecked_for_rejection_before_any_send(self):
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / 'drain.lock'; lock.write_text('owned')
            self.common._try_acquire_drain_lock = lambda: True
            self.common._DRAIN_LOCK = lock
            self.common._release_drain_lock = Mock()
            self.common.remember_entry_via_http = Mock()
            self.policy._state = lambda: contextlib.nullcontext({'blocked': {'reason': 'capture_request_rejected'}})
            progress = []
            with patch.dict(sys.modules, {'_capture_policy': types.SimpleNamespace(allow_tool=lambda *a: True, sanitize=lambda e:e)}):
                save(self.common, self.policy, {'dataset':'dataset','session_id':'session','tokens':['old-reviewed-token']}, lambda:False, progress.append)
            self.assertEqual(progress[-1]['reason'], 'capture_request_rejected')
            self.common.remember_entry_via_http.assert_not_called()

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

    def test_confirmed_handoff_uses_official_switch_without_erasing_watermarks(self):
        self.common.improve_throttle_reason.return_value = 'no_new_entries'
        self.assertEqual(process(self.common, self.policy, {**self.payload, 'handoff_operation':'original-switch'}),
                         {'status':'submitted'})
        self.policy.require.assert_called_once_with('process', 'dataset', automatic=True)
        self.common.improve_throttle_reason.assert_not_called()
        self.common.run_session_improve_detailed.assert_called_once_with('dataset', 'session', trigger='switch')

    def test_incremental_admission_uses_official_cooldown_configuration(self):
        observed = []
        self.common.improve_throttle_reason.side_effect = (
            lambda session: observed.append(('throttle', os.environ.get('COGNEE_IMPROVE_COOLDOWN'))) or ''
        )
        self.common.run_session_improve_detailed.side_effect = (
            lambda *args, **kwargs: observed.append(('improve', os.environ.get('COGNEE_IMPROVE_COOLDOWN'))) or {'ok': True}
        )
        with patch.dict(os.environ, {}, clear=True):
            process(self.common, self.policy, {**self.payload, 'incremental_cooldown_seconds': 300})
            self.assertEqual(observed, [('throttle', '300'), ('improve', '300')])
            self.assertNotIn('COGNEE_IMPROVE_COOLDOWN', os.environ)
        self.common.improve_throttle_reason.assert_called_once_with('session')

        # Previous value is restored on both normal return and exception.
        observed.clear()
        with patch.dict(os.environ, {'COGNEE_IMPROVE_COOLDOWN': '42'}, clear=True):
            process(self.common, self.policy, {**self.payload, 'incremental_cooldown_seconds': 300})
            self.assertEqual(observed, [('throttle', '300'), ('improve', '300')])
            self.assertEqual(os.environ['COGNEE_IMPROVE_COOLDOWN'], '42')

            self.common.run_session_improve_detailed.side_effect = RuntimeError('synthetic submit error')
            with self.assertRaisesRegex(RuntimeError, 'synthetic submit error'):
                process(self.common, self.policy, {**self.payload, 'incremental_cooldown_seconds': 300})
            self.assertEqual(os.environ['COGNEE_IMPROVE_COOLDOWN'], '42')

    def test_official_cooldown_backoff_and_no_new_entries_never_submit(self):
        for reason, status in [('cooldown','deferred'), ('backoff','deferred'), ('no_new_entries','skipped')]:
            with self.subTest(reason=reason):
                self.common.improve_throttle_reason.return_value = reason
                self.assertEqual(process(self.common, self.policy, self.payload), {'status':status, 'reason':reason})
        self.common.run_session_improve_detailed.assert_not_called()

    def test_uncertain_submit_is_not_treated_as_safe_to_retry(self):
        for reason in ('failed', ''):
            self.common.run_session_improve_detailed.return_value = {'ok':False, 'reason':reason}
            self.assertEqual(process(self.common, self.policy, self.payload)['status'], 'unknown')
        for reason in ('busy', 'unreachable'):
            self.common.run_session_improve_detailed.return_value = {'ok':False, 'reason':reason}
            self.assertEqual(process(self.common, self.policy, self.payload)['status'], 'deferred')

    def test_permission_failure_never_calls_official_submission(self):
        self.policy.require.side_effect = RuntimeError('permission unconfirmed')
        with self.assertRaises(RuntimeError): process(self.common, self.policy, self.payload)
        self.common.run_session_improve_detailed.assert_not_called()


class WorkerDrainFixture(DrainFixture):
    """Real installed sender overlay, adding controlled original-receipt GETs."""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.http_events = []
        self.receipts = {}
        self.session_detail = {"session_id": SESSION, "dataset_id": DATASET, "qas": [], "traces": []}

    def load_sender(self):
        source = self.access_patch.apply("_plugin_common.py", (UPSTREAM / "_plugin_common.py").read_text())
        self.common = self.load("_plugin_common", UPSTREAM / "_plugin_common.py", apply_capture_patch(source))
        self.common.set_session_key(HOST)
        self.capture = self.load("_memlane_capture", PYTHON / "plugin_capture.py")
        self.stack.enter_context(patch.object(self.common, "_json_http_request", side_effect=self.capture_http))

    def capture_http(self, path, body=None, *, method="POST", **kwargs):
        self.http_events.append((method, path))
        if method == "PUT":
            return super().capture_http(path, body, method=method, **kwargs)
        if method == "GET" and path == "/api/v1/sessions/" + SESSION:
            return copy.deepcopy(self.session_detail)
        if method == "GET" and path.startswith("/api/v1/capture/entries/"):
            message = path.split("?", 1)[0].rsplit("/", 1)[1]
            response = self.receipts.get(message)
            if response is None:
                raise urllib.error.HTTPError("http://fixture" + path, 404, "synthetic missing receipt", {}, None)
            if isinstance(response, Exception):
                raise response
            return copy.deepcopy(response)
        raise AssertionError("unexpected worker HTTP: " + method + " " + path)

    def saved_receipt(self, entry):
        message, ident, outgoing, sha = self.capture.wire(self.common, DATASET, SESSION, entry)
        return {"protocol": 1, "status": "saved", "message_id": message, "dataset_id": ident,
                "session_id": SESSION, "entry_type": outgoing["type"], "payload_sha256": sha,
                "entry_id": "native-" + message}


class WorkerQueueIsolationTests(unittest.TestCase):
    """QI01/QI03 sender cases plus unchanged ownership and receipt boundaries.

    Existing tests above remain intact. Every generated save report must
    conserve its frozen tokens; filtered/rejected/deferred are subsets of
    isolated and must never add to saved or handled.
    """

    def run_save(self, fixture, *, tokens=None, stopped=lambda: False, on_report=None):
        frozen = list(tokens if tokens is not None else review(fixture.common, fixture.policy, DATASET, HOST)["tokens"])
        reports = []

        def report(value):
            reports.append(value)
            self.assertEqual(sum(value[name] for name in ("saved", "handled", "isolated", "remaining")), len(frozen),
                             "save progress lost or double-counted a frozen token")
            for name in ("saved", "handled", "isolated", "remaining", "filtered", "rejected", "deferred"):
                self.assertGreaterEqual(value.get(name, 0), 0)
            self.assertLessEqual(sum(value.get(name, 0) for name in ("filtered", "rejected", "deferred")), value["isolated"])
            if on_report is not None:
                on_report(value)

        save(fixture.common, fixture.policy, {"dataset": DATASET, "session_id": SESSION, "tokens": frozen}, stopped, report)
        self.assertTrue(reports, "save emitted no terminal progress")
        self.assert_isolation_metadata(fixture.read())
        return reports[-1]

    def assert_isolation_metadata(self, state):
        forbidden = {"entry", "pending_entries", "question", "answer", "method_params", "method_return_value"}

        def visit(value):
            if isinstance(value, dict):
                self.assertFalse(forbidden.intersection(value), "isolation state contains a second content copy")
                for item in value.values():
                    visit(item)
            elif isinstance(value, list):
                for item in value:
                    visit(item)

        for name in ("capture_rejections", "capture_retries"):
            visit(state.get(name, {}))

    def assert_retained(self, actual, original):
        self.assertEqual({key: value for key, value in actual.items() if key != "_replay_ambiguous"},
                         {key: value for key, value in original.items() if key != "_replay_ambiguous"})

    def test_save_and_review_isolate_explicit_rejection(self):
        """QI01: definite per-entry rejection survives review, reopen and later save."""
        for status in (400, 409, 413, 422):
            for unknown in (False, True) if status == 409 else (False,):
                with self.subTest(status=status, previously_unknown=unknown), tempfile.TemporaryDirectory() as directory:
                    rejected = qa("rejected", **({"_replay_ambiguous": True} if unknown else {}))
                    healthy, later = qa("healthy"), qa("new healthy")
                    with WorkerDrainFixture(directory, [rejected, healthy]) as fixture:
                        def respond(message, body):
                            if message == rejected["_capture_id"]:
                                raise urllib.error.HTTPError("http://fixture/" + message, status, "synthetic rejection", {}, None)

                        fixture.behavior = respond
                        result = self.run_save(fixture)
                        self.assertEqual((result["status"], result["saved"], result["handled"], result["isolated"], result["remaining"]),
                                         ("completed", 1, 0, 1, 0), "one rejected entry paused the healthy tail")
                        self.assertEqual((result.get("rejected"), result.get("deferred", 0), result["filtered"]), (1, 0, 0))
                        retained = fixture.read()["pending_entries"][0]
                        self.assert_retained(retained, rejected)
                        if unknown:
                            self.assertTrue(retained["_replay_ambiguous"], "409 erased an earlier unknown write")
                        reviewed = review(fixture.common, fixture.policy, DATASET, HOST)
                        self.assertEqual(reviewed["tokens"], [])
                        self.assertEqual((reviewed.get("rejected"), reviewed.get("deferred", 0)), (1, 0))
                        fixture.now += 301
                        fixture.reopen()
                        fixture.append(later)
                        reviewed = review(fixture.common, fixture.policy, DATASET, HOST)
                        self.assertEqual(reviewed["tokens"], [entry_key(later)])
                        self.assertEqual(reviewed.get("rejected"), 1)
                        self.assertEqual(self.run_save(fixture)["saved"], 1)
                        self.assertEqual([message for message, _ in fixture.calls],
                                         [rejected["_capture_id"], healthy["_capture_id"], later["_capture_id"]])
                        self.assert_retained(fixture.read()["pending_entries"][0], rejected)

    def test_save_and_review_keep_unknown_retry_local_and_durable(self):
        """QI01 isolation / original-ID boundary: one unknown does not block new work."""
        for failure in ("timeout", "transport", "response_json", "receipt_mismatch"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                uncertain, healthy, later = qa("uncertain"), qa("healthy"), qa("new healthy")
                with WorkerDrainFixture(directory, [uncertain, healthy]) as fixture:
                    attempted = False

                    def respond(message, body):
                        nonlocal attempted
                        if message != uncertain["_capture_id"] or attempted:
                            return None
                        attempted = True
                        if failure == "timeout":
                            raise TimeoutError("synthetic possibly committed response loss")
                        if failure == "transport":
                            raise urllib.error.URLError("synthetic transport reset")
                        if failure == "response_json":
                            raise json.JSONDecodeError("synthetic invalid reply", "{", 1)
                        return {"protocol": 1, "status": "saved", "message_id": str(uuid.uuid4())}

                    fixture.behavior = respond
                    result = self.run_save(fixture)
                    self.assertEqual((result["status"], result["saved"], result["isolated"], result["remaining"]), ("completed", 1, 1, 0))
                    self.assertEqual((result.get("deferred"), result.get("rejected", 0)), (1, 0))
                    retained = fixture.read()["pending_entries"][0]
                    self.assert_retained(retained, uncertain)
                    self.assertTrue(retained["_replay_ambiguous"])
                    retry = copy.deepcopy(fixture.read()["capture_retries"])
                    fixture.append(later)
                    reviewed = review(fixture.common, fixture.policy, DATASET, HOST)
                    self.assertEqual(reviewed["tokens"], [entry_key(later)], "review admitted a message whose retry is not due")
                    self.assertEqual((reviewed.get("deferred"), reviewed.get("rejected", 0), reviewed["unknown"]), (1, 0, 1))
                    # A previously frozen scope must also recheck the same
                    # deadline, rather than trusting only review's projection.
                    result = self.run_save(fixture, tokens=[entry_key(uncertain), entry_key(later)])
                    self.assertEqual((result["saved"], result["isolated"], result.get("deferred")), (1, 1, 1))
                    fixture.reopen()
                    self.assertEqual(fixture.read()["capture_retries"], retry)
                    self.assertEqual(review(fixture.common, fixture.policy, DATASET, HOST)["tokens"], [])
                    self.assertEqual(len(fixture.calls), 3, "backoff retried the unknown original")
                    fixture.now += 301
                    self.assertEqual(review(fixture.common, fixture.policy, DATASET, HOST)["tokens"], [entry_key(uncertain)])
                    self.assertEqual(self.run_save(fixture)["saved"], 1)
                    self.assertEqual(fixture.calls[0], fixture.calls[-1], "retry changed the original ID or payload")
                    self.assertEqual(fixture.read()["pending_entries"], [])
                    self.assertNotIn(uncertain["_capture_id"], fixture.read().get("capture_retries", {}))

    def test_save_isolates_unserializable_json_before_http(self):
        """QI03: official JSON-readable NaN/Infinity never reach the PUT boundary."""
        for label, value in (("nan", float("nan")), ("infinity", float("inf")), ("negative_infinity", -float("inf"))):
            with self.subTest(payload=label), tempfile.TemporaryDirectory() as directory:
                invalid, healthy = qa("invalid", context={"nested": [value]}), qa("healthy")
                with WorkerDrainFixture(directory, [invalid, healthy]) as fixture:
                    result = self.run_save(fixture)
                    self.assertEqual((result["status"], result["saved"], result["isolated"], result.get("rejected")), ("completed", 1, 1, 1))
                    self.assertEqual([message for message, _ in fixture.calls], [healthy["_capture_id"]])
                    retained = fixture.read()["pending_entries"][0]
                    self.assertEqual(retained["_capture_id"], invalid["_capture_id"])
                    actual = retained["context"]["nested"][0]
                    self.assertTrue(math.isnan(actual) if label == "nan" else actual == value)
                    self.assertFalse(retained.get("_replay_ambiguous"), "pre-HTTP failure was journaled as a possible commit")
                    reviewed = review(fixture.common, fixture.policy, DATASET, HOST)
                    self.assertEqual((reviewed["tokens"], reviewed.get("rejected")), ([], 1))

    def test_review_reports_independent_held_filtered_and_unknown_counts(self):
        """QI01 projection and preserved official Key/.env filtering boundary."""
        for sensitive_path in ("credentials.key", ".env", "config/.env.local"):
            with self.subTest(sensitive_path=sensitive_path), tempfile.TemporaryDirectory() as directory:
                rejected, deferred, healthy = qa("rejected"), qa("deferred", _replay_ambiguous=True), qa("healthy")
                legacy = qa("legacy", numbered=False, _replay_ambiguous=True)
                filtered = {"type": "trace", "origin_function": "Read", "method_params": {"file_path": sensitive_path},
                            "_capture_id": str(uuid.uuid4()), "_buffered_at": 1}
                entries = [rejected, deferred, filtered, legacy, healthy]
                with WorkerDrainFixture(directory, entries) as fixture:
                    state = fixture.read()
                    state["capture_rejections"] = {rejected["_capture_id"]: {"reason": "capture_request_rejected", "http_status": 422, "at": fixture.now}}
                    state["capture_retries"] = {deferred["_capture_id"]: {"reason": "capture_retry_pending", "attempts": 1, "after": fixture.now + 60}}
                    fixture.write(state)
                    before = copy.deepcopy(fixture.read())
                    self.assertFalse(fixture.filters.allow_tool("Read", filtered["method_params"]))
                    reviewed = review(fixture.common, fixture.policy, DATASET, HOST)
                    self.assertEqual(reviewed["tokens"], [entry_key(healthy)])
                    self.assertEqual((reviewed.get("rejected"), reviewed.get("deferred"), reviewed["filtered"], reviewed["unknown"]), (1, 1, 1, 2))
                    self.assertEqual(reviewed["filtered_tokens"], [entry_key(filtered)])
                    self.assertEqual(fixture.read(), before, "review changed the original pending or isolation state")
                    result = self.run_save(fixture, tokens=[entry_key(entry) for entry in entries])
                    self.assertEqual((result["status"], result["saved"], result["handled"], result["isolated"], result["remaining"]), ("completed", 1, 0, 4, 0))
                    self.assertEqual((result["filtered"], result.get("rejected"), result.get("deferred")), (1, 1, 1))
                    self.assertEqual([message for message, _ in fixture.calls], [healthy["_capture_id"]])
                    self.assertEqual(fixture.read()["pending_entries"], entries[:-1])

    def test_save_preserves_whole_session_authorization_and_sender_barriers(self):
        """Preserved boundaries: pause this managed operation with exact remaining tokens."""
        for barrier in ("http_401", "http_403", "policy_unconfirmed", "policy_write_denied", "legacy_rejection",
                        "stopped_before_send", "stopped_while_busy", "stopped_after_first", "lease_changed"):
            with self.subTest(barrier=barrier), tempfile.TemporaryDirectory() as directory:
                first, healthy = qa("first"), qa("healthy")
                entries = [first, healthy]
                with WorkerDrainFixture(directory, entries) as fixture:
                    frozen = [entry_key(entry) for entry in entries]
                    stop = [barrier in ("stopped_before_send", "stopped_while_busy")]
                    if barrier == "legacy_rejection":
                        fixture.policy.hold("capture_request_rejected")
                    if barrier == "stopped_while_busy":
                        fixture.common._DRAIN_LOCK.parent.mkdir(parents=True, exist_ok=True)
                        fixture.common._DRAIN_LOCK.write_text("another sender")

                    def respond(message, body):
                        if barrier.startswith("http_"):
                            raise urllib.error.HTTPError("http://fixture/" + message, int(barrier[-3:]), "synthetic authorization failure", {}, None)
                        if barrier == "stopped_after_first":
                            stop[0] = True
                        if barrier == "lease_changed":
                            fixture.common._DRAIN_LOCK.rename(Path(directory) / "retired-lease")
                            fixture.common._DRAIN_LOCK.write_text("replacement sender")

                    fixture.behavior = respond
                    if barrier.startswith("policy_"):
                        def unavailable(path, payload=None, **kwargs):
                            if barrier == "policy_unconfirmed":
                                raise fixture.policy.NotReady("project_policy_unconfirmed")
                            response = fixture.control_http(path, payload, **kwargs)
                            response["policy"]["permissions"] = ["read"]
                            return response

                        fixture.stack.enter_context(patch.object(fixture.policy, "_request", side_effect=unavailable))
                    result = self.run_save(fixture, tokens=frozen, stopped=lambda: stop[0])
                    if barrier in ("stopped_after_first", "lease_changed"):
                        self.assertEqual((result["saved"], result["isolated"], result["remaining"]), (1, 0, 1))
                        self.assertEqual(fixture.read()["pending_entries"], [healthy])
                        self.assertEqual([message for message, _ in fixture.calls], [first["_capture_id"]])
                    else:
                        self.assertEqual((result["saved"], result["isolated"], result["remaining"]), (0, 0, 2))
                        self.assertEqual(fixture.read()["pending_entries"], entries)
                    self.assertEqual(result["status"], "stopped" if barrier.startswith("stopped_") else "paused")
                    if barrier.startswith("http_"):
                        self.assertEqual(result["reason"], "write_denied")
                        fixture.now += 301
                        fixture.reopen()
                        self.assertEqual(self.run_save(fixture, tokens=frozen)["status"], "paused")
                        self.assertEqual(len(fixture.calls), 1, "the same session ignored its retained authorization block")
                    elif barrier == "lease_changed":
                        self.assertEqual(result["reason"], "sender_lock_changed")
                        self.assertEqual(fixture.common._DRAIN_LOCK.read_text(), "replacement sender")
                    elif barrier == "stopped_while_busy":
                        self.assertEqual(fixture.common._DRAIN_LOCK.read_text(), "another sender")
                    elif barrier == "legacy_rejection":
                        self.assertEqual(review(fixture.common, fixture.policy, DATASET, HOST)["tokens"], [])
                    if barrier not in ("lease_changed", "stopped_while_busy"):
                        self.assertFalse(fixture.common._DRAIN_LOCK.exists())

    def test_save_rechecks_changed_tokens_and_never_sends_a_cached_body(self):
        """Preserved source/version boundary: disappearance is handled, not saved."""
        for change in ("changed_before_save", "changed_after_first", "removed_before_save"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                first, second = qa("first"), qa("second")
                with WorkerDrainFixture(directory, [first, second]) as fixture:
                    frozen = review(fixture.common, fixture.policy, DATASET, HOST)["tokens"]
                    replacement = {**second, "answer": "changed source version"}

                    def mutate():
                        state = fixture.read()
                        state["pending_entries"] = [entry for entry in state["pending_entries"] if entry["_capture_id"] != second["_capture_id"]]
                        if change != "removed_before_save":
                            state["pending_entries"].append(replacement)
                        fixture.write(state)

                    if change != "changed_after_first":
                        mutate()

                    def report(value):
                        if change == "changed_after_first" and value["saved"] == 1 and value["handled"] == 0:
                            mutate()

                    result = self.run_save(fixture, tokens=frozen, on_report=report)
                    self.assertEqual((result["status"], result["saved"], result["handled"], result["isolated"], result["remaining"]), ("completed", 1, 1, 0, 0))
                    self.assertEqual([message for message, _ in fixture.calls], [first["_capture_id"]])
                    self.assertEqual(fixture.read()["pending_entries"], [] if change == "removed_before_save" else [replacement])

    def test_check_consumes_only_verified_original_receipts_without_any_put(self):
        """Preserved receipt boundary: matching content never confirms an original ID."""
        for mismatch in ("absent", "message_id", "dataset_id", "session_id", "payload_sha256"):
            with self.subTest(receipt=mismatch), tempfile.TemporaryDirectory() as directory:
                confirmed = qa("confirmed", _replay_ambiguous=True)
                uncertain = qa("uncertain", _replay_ambiguous=True)
                legacy = qa("legacy", numbered=False, _replay_ambiguous=True)
                unsent = qa("unsent")
                with WorkerDrainFixture(directory, [confirmed, uncertain, legacy, unsent]) as fixture:
                    fixture.session_detail["qas"] = [{key: value for key, value in legacy.items() if not key.startswith("_")}]
                    fixture.receipts[confirmed["_capture_id"]] = fixture.saved_receipt(confirmed)
                    if mismatch != "absent":
                        receipt = fixture.saved_receipt(uncertain)
                        receipt[mismatch] = "different-original-value"
                        fixture.receipts[uncertain["_capture_id"]] = receipt
                    result = check(fixture.common, fixture.policy, DATASET, HOST)
                    self.assertEqual((result["status"], result["unknown"], result["matched"], result["reason"]),
                                     ("unconfirmed", 2, 1, "original_receipt_unavailable"))
                    self.assertEqual(fixture.read()["pending_entries"], [uncertain, legacy, unsent])
                    self.assertEqual(fixture.calls, [], "check attempted to resubmit an original message")
                    self.assertTrue(all(method == "GET" for method, _ in fixture.http_events))
                    lookup_paths = [path for _, path in fixture.http_events if path.startswith("/api/v1/capture/entries/")]
                    self.assertEqual(len(lookup_paths), 2)
                    self.assertTrue(any(confirmed["_capture_id"] in path for path in lookup_paths))
                    self.assertTrue(any(uncertain["_capture_id"] in path for path in lookup_paths))

    def test_check_retains_unknowns_when_original_session_is_unavailable(self):
        """Preserved scope boundary: a failed read is neither saved nor unsaved evidence."""
        for detail in (None, {"session_id": "other", "dataset_id": DATASET}, {"session_id": SESSION, "dataset_id": "other"}):
            with self.subTest(detail=detail), tempfile.TemporaryDirectory() as directory:
                original = qa("original", _replay_ambiguous=True)
                with WorkerDrainFixture(directory, [original]) as fixture:
                    fixture.session_detail = detail
                    with self.assertRaisesRegex(fixture.policy.NotReady, "official_review_unavailable"):
                        check(fixture.common, fixture.policy, DATASET, HOST)
                    self.assertEqual(fixture.read()["pending_entries"], [original])
                    self.assertEqual(fixture.calls, [])
                    self.assertEqual(fixture.http_events, [("GET", "/api/v1/sessions/" + SESSION)])
