"""Real numbered drain over the official temporary buffer, with HTTP controlled.

Scenario mapping (docs/engineering/queue-isolation-20261119.md):
* message rejection / retry isolation: QI01 and original-ID unknown boundary;
* non-finite JSON before transport: QI03;
* authorization, filtering, lease, stop, legacy unknown and trim: retained
  boundaries, not additional BUG_MAP findings.

No running service, daily store, model, installed plugin or production file is
changed. The fixed upstream common module uses the production patch in memory;
the target, policy, buffer, routing and capture filters remain real functions.
"""
import contextlib
import copy
import hashlib
import importlib
import importlib.util
import json
import math
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
import urllib.error
import uuid
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
PYTHON = ROOT / "engine/python"
COMMIT = json.loads((ROOT / "engine/coding-memory-components.json").read_text())["plugin"]["commit"]
UPSTREAM = ROOT / ".cache/coding-memory-components" / ("plugin-" + COMMIT) / "integrations/codex/plugins/cognee/scripts"
DATASET = "11111111-1111-4111-8111-111111111111"
SESSION = "synthetic-drain-session"
HOST = "synthetic-drain-host"


def qa(label, *, numbered=True, **fields):
    entry = {"type": "qa", "question": label, "answer": "answer for " + label,
             "_buffered_at": 1, **fields}
    if numbered:
        entry["_capture_id"] = str(uuid.uuid4())
    return entry


class DrainFixture:
    """Only HTTP and wall-clock time are replaced; all queue IO is real."""

    def __init__(self, root, entries, **state):
        self.root = Path(root)
        self.entries = entries
        self.initial_state = state
        self.now = time.time()
        self.calls = []
        self.behavior = None
        self.stack = contextlib.ExitStack()

    def load(self, name, path, source=None):
        spec = importlib.util.spec_from_file_location(name, path)
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        if source is None:
            spec.loader.exec_module(module)
        else:
            exec(compile(source, str(path), "exec"), module.__dict__)
        return module

    def __enter__(self):
        try:
            return self.enter()
        except BaseException:
            self.stack.close()
            raise

    def enter(self):
        # Import fresh official modules so their home paths and one-time env
        # loading cannot refer to the developer's real Agent installation.
        names = {path.stem for path in UPSTREAM.glob("*.py")}
        names.update(("_memlane_capture", "_memlane_policy", "_memlane_access"))
        old = {name: sys.modules[name] for name in names if name in sys.modules}
        for name in names:
            sys.modules.pop(name, None)

        def restore_modules():
            for name in names:
                sys.modules.pop(name, None)
            sys.modules.update(old)

        self.stack.callback(restore_modules)
        environment = {key: value for key, value in os.environ.items()
                       if not key.startswith(("COGNEE_", "MEMLANE_", "LLM_", "EMBEDDING_"))}
        token = self.root / "control.token"
        token.write_text("c" * 40)
        token.chmod(0o600)
        env_file = self.root / "empty.env"
        env_file.write_text("")
        environment.update(HOME=str(self.root), COGNEE_ENV_FILE=str(env_file),
                           COGNEE_PLUGIN_IN_VENV="1", COGNEE_SESSION_KEY=HOST,
                           COGNEE_BASE_URL="http://127.0.0.1:18999", COGNEE_API_KEY="synthetic-child-key",
                           COGNEE_BACKEND="cloud", COGNEE_CODEX_BACKEND="cloud",
                           COGNEE_PROJECT_NODE_SET="", COGNEE_SESSION_COMPANION_DATASET="false",
                           MEMLANE_PROJECT_CONTROL_URL="http://127.0.0.1:18777",
                           MEMLANE_PROJECT_CONTROL_TOKEN_FILE=str(token))
        self.stack.enter_context(patch.dict(os.environ, environment, clear=True))
        self.stack.enter_context(patch.object(sys, "path", [str(UPSTREAM), *sys.path]))
        self.access_patch = self.load("drain_fixture_project_access", ROOT / "engine/patches/project_access.py")
        self.stack.callback(sys.modules.pop, "drain_fixture_project_access", None)
        self.policy = self.load("_memlane_policy", PYTHON / "project_plugin_policy.py")
        self.access = self.load("_memlane_access", PYTHON / "project_access_hooks.py")
        self.load("_env_file", UPSTREAM / "_env_file.py",
                  self.access_patch.apply("_env_file.py", (UPSTREAM / "_env_file.py").read_text()))
        self.load_sender()
        self.filters = importlib.import_module("_capture_policy")
        self.common._write_map_record(HOST, {"session_id": SESSION, "dataset": DATASET, "host_key": HOST})
        with self.policy._state() as state:
            state["binding"] = {"project_id": "synthetic-project", "dataset_id": DATASET}
        self.stack.enter_context(patch.object(self.policy, "_request", side_effect=self.control_http))
        self.stack.enter_context(patch.object(time, "time", side_effect=lambda: self.now))
        self.write({"pending_entries": copy.deepcopy(self.entries), **self.initial_state})
        return self

    def __exit__(self, *exc):
        return self.stack.__exit__(*exc)

    def load_sender(self):
        self.common = self.load("_plugin_common", UPSTREAM / "_plugin_common.py",
                                self.access_patch.apply("_plugin_common.py", (UPSTREAM / "_plugin_common.py").read_text()))
        self.common.set_session_key(HOST)
        self.capture = self.load("_memlane_capture", PYTHON / "plugin_capture.py")
        self.stack.enter_context(patch.object(self.common, "_json_http_request", side_effect=self.capture_http))

    def reopen(self):
        # Reload both sender and common from source, retaining only their
        # actual on-disk buffer, lease and policy files.
        self.load_sender()

    def control_http(self, path, payload=None, **kwargs):
        if not path.startswith("/v1/project/policy?"):
            raise AssertionError("unexpected control HTTP: " + path)
        return {"ready": True, "dataset_id": DATASET, "revision": 1,
                "policy": {"permissions": ["read", "write"], "can_capture": True, "can_auto_process": False}}

    def capture_http(self, path, body=None, *, method="POST", **kwargs):
        if method != "PUT" or not path.startswith("/api/v1/capture/entries/"):
            raise AssertionError("unexpected capture HTTP: " + method + " " + path)
        message = path.rsplit("/", 1)[1]
        self.calls.append((message, copy.deepcopy(body)))
        if self.behavior is not None:
            response = self.behavior(message, body)
            if response is not None:
                return response
        outgoing = body["entry"]
        sha = hashlib.sha256(json.dumps(outgoing, sort_keys=True, ensure_ascii=False, allow_nan=False).encode()).hexdigest()
        return {"protocol": 1, "status": "saved", "message_id": message,
                "dataset_id": body["dataset_id"], "session_id": body["session_id"],
                "entry_type": outgoing["type"], "payload_sha256": sha, "entry_id": "native-" + message}

    def read(self):
        _, _, _, state, _, _ = self.policy._pending_snapshot(DATASET, SESSION)
        return state

    def write(self, state):
        path = self.common._bridge_file(SESSION)
        key = self.common._bridge_cache_key(DATASET, SESSION)
        with self.common._buffer_lock():
            cache = self.common._load_json_file(path)
            cache[key] = state
            self.common._write_json_file(path, cache)

    def append(self, entry):
        with self.common._buffer_lock():
            path, key, cache, state, pending, _ = self.policy._pending_snapshot(DATASET, SESSION)
            state["pending_entries"] = [*pending, copy.deepcopy(entry)]
            cache[key] = state
            self.common._write_json_file(path, cache)

    def drain(self, **kwargs):
        return self.capture.drain(self.common, DATASET, SESSION, budget_seconds=12, **kwargs)


class PluginCaptureDrainTests(unittest.TestCase):
    def test_legacy_sender_cannot_replay_new_entry_holds(self):
        for failure in ("rejected", "unknown"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                first, healthy = qa("held"), qa("healthy")
                with DrainFixture(directory, [first, healthy]) as fixture:
                    attempted = False
                    def respond(message, body):
                        nonlocal attempted
                        if message == first["_capture_id"] and not attempted:
                            attempted = True
                            if failure == "rejected":
                                raise urllib.error.HTTPError("http://fixture", 422, "rejected", {}, None)
                            raise TimeoutError("possible commit")
                    fixture.behavior = respond
                    self.assertEqual(fixture.drain(), (1, 1))
                    before = len(fixture.calls)
                    legacy_path = PYTHON / "fixtures/plugin_capture_before_entry_isolation.py"
                    legacy = fixture.load("legacy_capture_sender", legacy_path)
                    fixture.stack.callback(sys.modules.pop, "legacy_capture_sender", None)
                    fixture.now += 301
                    self.assertEqual(legacy.drain(fixture.common, DATASET, SESSION, budget_seconds=12), (0, 1))
                    self.assertEqual(len(fixture.calls), before, "an old sender ignored the entry isolation state")
                    if failure == "unknown":
                        self.assertEqual(fixture.drain(), (1, 0))
                        with fixture.policy._state() as state:
                            self.assertNotIn("blocked", state, "resolved entry hold kept the old sender permanently blocked")

    def assert_original(self, actual, original):
        # An actual attempted send may journal ambiguity; content, number and
        # original source metadata must survive every failure unchanged.
        self.assertEqual({key: value for key, value in actual.items() if key != "_replay_ambiguous"},
                         {key: value for key, value in original.items() if key != "_replay_ambiguous"})

    def assert_metadata_only(self, state):
        forbidden = {"entry", "pending_entries", "question", "answer", "method_params", "method_return_value"}

        def visit(value):
            if isinstance(value, dict):
                self.assertFalse(forbidden.intersection(value), "isolation metadata contains a second content copy")
                for item in value.values():
                    visit(item)
            elif isinstance(value, list):
                for item in value:
                    visit(item)

        for key in ("capture_rejections", "capture_retries"):
            visit(state.get(key, {}))

    def test_drain_isolates_rejected_messages_and_keeps_original_facts(self):
        """QI01: each definite rejection is local, durable and never hot-retried."""
        for status in (400, 409, 413, 422):
            for uncertain in (False, True) if status == 409 else (False,):
                with self.subTest(status=status, previously_unknown=uncertain), tempfile.TemporaryDirectory() as directory:
                    rejected = qa("rejected", **({"_replay_ambiguous": True} if uncertain else {}))
                    healthy = qa("healthy")
                    with DrainFixture(directory, [rejected, healthy]) as fixture:
                        def respond(message, body):
                            if message == rejected["_capture_id"]:
                                raise urllib.error.HTTPError("http://fixture/" + message, status, "synthetic rejection", {}, None)

                        fixture.behavior = respond
                        self.assertEqual(fixture.drain(), (1, 1), "one explicit rejection stopped the healthy tail")
                        state = fixture.read()
                        self.assert_original(state["pending_entries"][0], rejected)
                        self.assert_metadata_only(state)
                        if uncertain:
                            self.assertTrue(state["pending_entries"][0].get("_replay_ambiguous"),
                                            "409 erased a previously unconfirmed write fact")
                        fixture.now += 301
                        fixture.reopen()
                        later = qa("new healthy")
                        fixture.append(later)
                        self.assertEqual(fixture.drain(), (1, 1))
                        self.assertEqual([message for message, _ in fixture.calls],
                                         [rejected["_capture_id"], healthy["_capture_id"], later["_capture_id"]],
                                         "a rejected original number was automatically replayed")
                        self.assert_original(fixture.read()["pending_entries"][0], rejected)

    def test_drain_rejects_non_finite_json_before_transport(self):
        """QI03: JSON-readable but protocol-unserializable entries stay local.

        NaN/Infinity are accepted by the actual official json.loads buffer;
        arbitrary Python objects cannot be produced by that persisted path.
        """
        for name, value in (("nan", float("nan")), ("infinity", float("inf")), ("negative_infinity", -float("inf"))):
            with self.subTest(value=name), tempfile.TemporaryDirectory() as directory:
                invalid = qa("invalid " + name, context={"nested": [value]})
                healthy = qa("healthy")
                with DrainFixture(directory, [invalid, healthy]) as fixture:
                    self.assertEqual(fixture.drain(), (1, 1), "a pre-HTTP serialization error stopped later messages")
                    self.assertEqual([message for message, _ in fixture.calls], [healthy["_capture_id"]])
                    state = fixture.read()
                    retained = state["pending_entries"][0]
                    self.assertEqual(retained["_capture_id"], invalid["_capture_id"])
                    actual = retained["context"]["nested"][0]
                    self.assertTrue(math.isnan(actual) if name == "nan" else actual == value)
                    self.assertFalse(retained.get("_replay_ambiguous"), "preflight failure was recorded as an attempted HTTP write")
                    self.assert_metadata_only(state)
                    fixture.now += 301
                    fixture.reopen()
                    later = qa("new healthy")
                    fixture.append(later)
                    self.assertEqual(fixture.drain(), (1, 1))
                    self.assertEqual([message for message, _ in fixture.calls], [healthy["_capture_id"], later["_capture_id"]])

    def test_drain_retries_only_the_original_number_after_durable_backoff(self):
        """QI01 isolation and retained boundary: real unknown writes keep their ID."""
        cases = ("timeout", "transport", "response_json", "receipt_mismatch")
        for failure in cases:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                uncertain, healthy, later = qa("uncertain"), qa("healthy"), qa("arrived during backoff")
                with DrainFixture(directory, [uncertain, healthy]) as fixture:
                    attempted = False

                    def respond(message, body):
                        nonlocal attempted
                        if message != uncertain["_capture_id"] or attempted:
                            return None
                        attempted = True
                        if failure == "timeout":
                            raise TimeoutError("synthetic response lost after possible commit")
                        if failure == "transport":
                            raise urllib.error.URLError("synthetic reset")
                        if failure == "response_json":
                            raise json.JSONDecodeError("synthetic response parse failure", "{", 1)
                        return {"protocol": 1, "status": "saved", "message_id": str(uuid.uuid4())}

                    fixture.behavior = respond
                    self.assertEqual(fixture.drain(), (1, 1), "one unknown result stopped the healthy tail")
                    state = fixture.read()
                    self.assert_original(state["pending_entries"][0], uncertain)
                    self.assertTrue(state["pending_entries"][0]["_replay_ambiguous"])
                    self.assertIn(uncertain["_capture_id"], state.get("capture_retries", {}),
                                  "retry deadline was not persisted for the original message")
                    retry = copy.deepcopy(state["capture_retries"])
                    self.assert_metadata_only(state)
                    self.assertEqual(fixture.drain(), (0, 1))
                    self.assertEqual(len(fixture.calls), 2, "unknown message was immediately retried")
                    fixture.append(later)
                    self.assertEqual(fixture.drain(), (1, 1), "message-local backoff blocked a new healthy append")
                    fixture.reopen()
                    self.assertEqual(fixture.read()["capture_retries"], retry, "reopening reset the durable deadline")
                    self.assertEqual(fixture.drain(), (0, 1))
                    self.assertEqual(len(fixture.calls), 3)
                    fixture.now += 301
                    self.assertEqual(fixture.drain(), (1, 0))
                    self.assertEqual([message for message, _ in fixture.calls],
                                     [uncertain["_capture_id"], healthy["_capture_id"], later["_capture_id"], uncertain["_capture_id"]])
                    self.assertEqual(fixture.calls[0], fixture.calls[-1], "retry changed the original ID or outgoing payload")
                    self.assertNotIn(uncertain["_capture_id"], fixture.read().get("capture_retries", {}))

    def test_drain_preserves_session_barriers_and_official_filters(self):
        """Retained boundaries: no widening policy or guessing legacy provenance."""
        for status in (401, 403):
            with self.subTest(boundary="authorization", status=status), tempfile.TemporaryDirectory() as directory:
                denied, healthy = qa("denied"), qa("healthy")
                with DrainFixture(directory, [denied, healthy]) as fixture:
                    def respond(message, body):
                        raise urllib.error.HTTPError("http://fixture/" + message, status, "synthetic permission denied", {}, None)

                    fixture.behavior = respond
                    self.assertEqual(fixture.drain(), (0, 2))
                    self.assert_original(fixture.read()["pending_entries"][0], denied)
                    with fixture.policy._state() as state:
                        self.assertEqual(state["blocked"]["reason"], "official_http_" + str(status))
                    fixture.now += 301
                    fixture.reopen()
                    self.assertEqual(fixture.drain(), (0, 2))
                    self.assertEqual(len(fixture.calls), 1, "a session permission block was bypassed")

        for reason in ("user_stopped", "capture_request_rejected"):
            with self.subTest(boundary="existing_unattributed_block", reason=reason), tempfile.TemporaryDirectory() as directory:
                entries = [qa("original"), qa("healthy")]
                with DrainFixture(directory, entries) as fixture:
                    fixture.policy.hold(reason)
                    self.assertEqual(fixture.drain(), (0, 2))
                    self.assertEqual(fixture.calls, [])
                    self.assertEqual(fixture.read()["pending_entries"], entries,
                                     "an old block without message attribution was silently reassigned")

        for exclusion in ("sensitive_path", "tool_rule"):
            with self.subTest(boundary="official_filter", exclusion=exclusion), tempfile.TemporaryDirectory() as directory:
                excluded = {"type": "trace", "origin_function": "Read", "method_params": {"file_path": ".env"},
                            "_capture_id": str(uuid.uuid4()), "_buffered_at": 1}
                if exclusion == "tool_rule":
                    excluded.update(origin_function="PrivateTool", method_params={"file_path": "safe.py"})
                healthy = qa("healthy")
                with DrainFixture(directory, [excluded, healthy]) as fixture:
                    with patch.dict(os.environ, {"COGNEE_CAPTURE_DENY_TOOLS": "PrivateTool"}):
                        self.assertFalse(fixture.filters.allow_tool(excluded["origin_function"], excluded["method_params"]))
                        self.assertEqual(fixture.drain(), (1, 1))
                        self.assertEqual(fixture.read()["pending_entries"], [excluded])
                        self.assertEqual(fixture.drain(), (0, 1))
                    self.assertEqual([message for message, _ in fixture.calls], [healthy["_capture_id"]])

        with self.subTest(boundary="legacy_unknown"), tempfile.TemporaryDirectory() as directory:
            legacy, healthy = qa("legacy", numbered=False, _replay_ambiguous=True), qa("healthy")
            with DrainFixture(directory, [legacy, healthy]) as fixture:
                self.assertEqual(fixture.drain(), (1, 1))
                self.assertEqual(fixture.read()["pending_entries"], [legacy])
                fixture.reopen()
                fixture.now += 301
                self.assertEqual(fixture.drain(), (0, 1))
                self.assertEqual([message for message, _ in fixture.calls], [healthy["_capture_id"]])

    def test_drain_obeys_lease_budget_and_trims_only_confirmed_originals(self):
        """Retained ownership and positive-path boundaries over real files."""
        for boundary in ("empty", "zero_budget", "lease_busy", "lease_changed", "append_during_send", "same_content_different_ids"):
            with self.subTest(boundary=boundary), tempfile.TemporaryDirectory() as directory:
                first, second = qa("first"), qa("second")
                entries = [] if boundary == "empty" else [first]
                if boundary == "lease_changed":
                    entries.append(second)
                if boundary == "same_content_different_ids":
                    second = {**first, "_capture_id": str(uuid.uuid4())}
                    entries.append(second)
                with DrainFixture(directory, entries) as fixture:
                    if boundary == "lease_busy":
                        fixture.common._DRAIN_LOCK.parent.mkdir(parents=True, exist_ok=True)
                        fixture.common._DRAIN_LOCK.write_text("another sender")
                        self.assertEqual(fixture.drain(), (0, 1))
                        self.assertEqual(fixture.common._DRAIN_LOCK.read_text(), "another sender")
                    elif boundary == "zero_budget":
                        self.assertEqual(fixture.capture.drain(fixture.common, DATASET, SESSION, budget_seconds=0), (0, 1))
                    elif boundary == "empty":
                        self.assertEqual(fixture.drain(), (0, 0))
                    else:
                        def respond(message, body):
                            if message == first["_capture_id"] and boundary == "lease_changed":
                                # Rename keeps the old inode live, guaranteeing
                                # a different lease rather than inode reuse.
                                fixture.common._DRAIN_LOCK.rename(Path(directory) / "retired-lease")
                                fixture.common._DRAIN_LOCK.write_text("replacement sender")
                            elif message == first["_capture_id"] and boundary == "append_during_send":
                                fixture.append(second)

                        fixture.behavior = respond
                        expected = (2, 0) if boundary == "same_content_different_ids" else (1, 1)
                        self.assertEqual(fixture.drain(), expected)
                        if boundary == "same_content_different_ids":
                            self.assertEqual(fixture.read()["pending_entries"], [])
                            self.assertEqual([message for message, _ in fixture.calls], [first["_capture_id"], second["_capture_id"]])
                        else:
                            self.assertEqual(fixture.read()["pending_entries"], [second])
                            self.assertEqual([message for message, _ in fixture.calls], [first["_capture_id"]])
                        if boundary == "lease_changed":
                            self.assertEqual(fixture.common._DRAIN_LOCK.read_text(), "replacement sender",
                                             "old sender released another owner's replacement lease")
                        else:
                            self.assertFalse(fixture.common._DRAIN_LOCK.exists())
                    if boundary in ("empty", "zero_budget", "lease_busy"):
                        self.assertEqual(fixture.calls, [])
                        self.assertEqual(fixture.read()["pending_entries"], entries)


if __name__ == "__main__":
    unittest.main()
