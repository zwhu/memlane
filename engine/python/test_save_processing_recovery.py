"""Continuous regressions for retained filtered entries and partial improve."""
import contextlib
import copy
import json
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import Mock, patch

from project_backlog import Backlog
from project_backlog_worker import entry_key, process, review, save


class SaveProcessingRecoveryTests(unittest.TestCase):
    def test_filtered_record_retained_while_later_allowed_message_is_saved(self):
        excluded = {"type": "trace", "origin_function": "private_tool", "method_params": {}}
        healthy = {"type": "qa", "question": "allowed", "answer": "kept"}
        entries = [copy.deepcopy(excluded), copy.deepcopy(healthy)]
        original = copy.deepcopy(entries)
        state = {"pending_entries": entries}
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / "drain.lock"
            lock.write_text("owned")
            common = types.SimpleNamespace(set_session_key=Mock(), _read_map_record=lambda host: {"session_id": "s"},
                _AMBIGUOUS_KEY="_replay_ambiguous", _BUFFER_META_KEYS=set(), _buffer_lock=contextlib.nullcontext,
                _try_acquire_drain_lock=lambda: True, _DRAIN_LOCK=lock, _release_drain_lock=Mock(),
                remember_entry_via_http=Mock(), _write_json_file=Mock())
            policy = types.SimpleNamespace(_pending_identity=Mock(), require=Mock(), stamp_pending_entry=Mock(),
                _state=lambda: contextlib.nullcontext({}), NotReady=RuntimeError,
                _pending_snapshot=lambda *args: (Path(directory)/"buffer", "s", {"s": state}, state, entries, "snapshot"))
            capture = types.SimpleNamespace(allow_tool=lambda name, params: name != "private_tool", sanitize=lambda e: e)
            with patch.dict(sys.modules, {"_capture_policy": capture}):
                snapshot = review(common, policy, "ds", "host")
                self.assertEqual(snapshot["tokens"], [entry_key(healthy)], "filtered head still joined send scope")
                # Simulate filtering changing after an older snapshot was frozen.
                progress = []
                save(common, policy, {"dataset": "ds", "session_id": "s", "tokens": [entry_key(e) for e in original]}, lambda: False, progress.append)
            self.assertEqual(progress[-1]["status"], "completed", "one excluded entry stopped the whole queue")
            self.assertEqual(progress[-1]["saved"], 1)
            self.assertEqual(progress[-1]["filtered"], 1)
            self.assertEqual(entries, [excluded], "excluded material was deleted or allowed material remained")
            common.remember_entry_via_http.assert_called_once_with("ds", "s", healthy, timeout=12)

    def test_partial_submit_is_distinct_from_unconfirmed_request(self):
        common = types.SimpleNamespace(improve_throttle_reason=Mock(return_value=""),
            run_session_improve_detailed=Mock(return_value={"ok": False, "reason": "incomplete_drain"}))
        policy = types.SimpleNamespace(_pending_identity=Mock(), require=Mock())
        self.assertEqual(process(common, policy, {"dataset": "ds", "session_id": "s"}),
                         {"status": "partial", "reason": "incomplete_drain"})
        common.run_session_improve_detailed.return_value = {"ok": False, "reason": "failed"}
        self.assertEqual(process(common, policy, {"dataset": "ds", "session_id": "s"})["status"], "unknown")

    def test_submitting_is_activity_not_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            registry = types.SimpleNamespace(path=Path(directory)/"registry.json", state={"projects": {}})
            backlog = Backlog(registry, {})
            row = {"targets": [], "automatic": True, "pending_processing": {
                "active": {"processing": "submitting"}, "partial": {"processing": "partial"}}}
            public = backlog.public(row)
            self.assertEqual(public["processing_failed"], 0, "in-flight submit was counted as failed")
            self.assertEqual(public["processing_active"], 1)
            self.assertEqual(public["processing_partial"], 1)


class AutomaticRecoveryTests(unittest.IsolatedAsyncioTestCase):
    async def test_partial_tail_waits_and_reopens_without_retrying_unknown_submit(self):
        with tempfile.TemporaryDirectory() as directory:
            policy = {"ready": True, "policy": {"permissions": ["read", "write"], "can_capture": True, "can_auto_process": True}}
            registry = types.SimpleNamespace(path=Path(directory)/"registry.json", state={
                "projects": {"p": {"dataset_id": "ds"}}, "agents": {"a": {"installation": {"status": "configured"}}},
                "sessions": {json.dumps(["a", "host"]): "p"}}, policy=lambda *args: policy)
            backlog = Backlog(registry, {})
            target = {"agent_id": "a", "host": "host", "dataset": "ds", "session_id": "s", "saved": 1, "processing": "queued"}
            row = {"operation_id": "original", "project_id": "p", "created_at": 1, "status": "watching", "automatic": True,
                   "targets": [], "pending_processing": {"source": target}}
            backlog.jobs["original"] = row
            calls = []
            async def execute(row, target, action, update=None):
                if action == "review":
                    return {"session_id": "s", "tokens": [], "unknown": 0, "signature": "tail", "filtered": 0}
                calls.append(action)
                return {"status": "partial", "reason": "incomplete_drain"} if len(calls) == 1 else {"status": "submitted"}
            backlog.execute = execute
            await backlog.automatic_tick()
            self.assertEqual(target["processing"], "partial")
            self.assertEqual(backlog.public(row)["processing_failed"], 0)
            await backlog.automatic_tick()
            self.assertEqual(calls, ["process"], "partial submit was hot-looped")
            reopened = Backlog(registry, {})
            reopened.execute = execute
            await reopened.automatic_tick()
            self.assertEqual(calls, ["process"], "restart discarded continuation deadline")
            reopened.jobs["original"]["pending_processing"]["source"]["next_processing_at"] = 0
            await reopened.automatic_tick()
            self.assertEqual(calls, ["process", "process"])
            self.assertEqual(reopened.public(reopened.jobs["original"])["processing_partial"], 0)
            reopened.jobs["original"]["pending_processing"]["source"]["processing"] = "unknown"
            reopened.persist()
            uncertain = Backlog(registry, {})
            uncertain.execute = execute
            await uncertain.automatic_tick()
            self.assertEqual(calls, ["process", "process"], "unknown submission was retried")

    async def test_only_official_partial_reason_is_reclassified_with_history_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            registry = types.SimpleNamespace(path=Path(directory)/"registry.json", state={"projects": {}})
            backlog = Backlog(registry, {})
            backlog.jobs["old"] = {"operation_id": "old", "status": "completed", "targets": [
                {"processing": "unknown", "processing_reason": "incomplete_drain", "original_receipt": "kept"},
                {"processing": "unknown", "processing_reason": "failed"}, {"processing": "submitting"}]}
            backlog.persist()
            reopened = Backlog(registry, {})
            targets = reopened.jobs["old"]["targets"]
            self.assertEqual((targets[0]["processing"], targets[0]["previous_processing"], targets[0]["original_receipt"]),
                             ("partial", "unknown", "kept"))
            self.assertEqual([t["processing"] for t in targets[1:]], ["unknown", "submitting"])

    async def test_old_filter_pause_rechecks_same_operation_without_user_pause_bypass(self):
        with tempfile.TemporaryDirectory() as directory:
            policy = {"ready": True, "policy": {"permissions": ["read", "write"], "can_capture": True, "can_auto_process": False}}
            registry = types.SimpleNamespace(path=Path(directory)/"registry.json", state={
                "projects": {"p": {"dataset_id": "ds"}}, "agents": {"a": {"installation": {"status": "configured"}}},
                "sessions": {json.dumps(["a", "host"]): "p"}}, policy=lambda *args: policy)
            backlog = Backlog(registry, {})
            # Override only installation verification, retain actual scheduling.
            backlog.checked_target = lambda *args, **kwargs: (registry.state["agents"]["a"], policy)
            row = {"operation_id": "old", "project_id": "p", "created_at": 1, "status": "paused",
                   "automatic": True, "reason": "capture_filter_changed", "targets": [{"agent_id": "a", "host": "host", "tokens": []}], "pending_processing": {}}
            backlog.jobs["old"] = row
            calls = []
            async def execute(row, target, action, update=None):
                calls.append(action)
                return {"session_id": "s", "tokens": [], "unknown": 0, "filtered": 1, "filtered_tokens": ["f"*64], "signature": "current"}
            backlog.execute = execute
            await backlog.automatic_tick()
            self.assertEqual(backlog.jobs["old"]["status"], "watching")
            self.assertEqual(calls, ["review"])
            row.update(status="paused", reason="capture_filter_changed", automatic_user_paused=True)
            await backlog.automatic_tick()
            self.assertEqual(calls, ["review"], "explicit pause was bypassed")
