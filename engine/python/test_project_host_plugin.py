import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from project_control import private_write
from project_host_plugin import files_for
import project_host_plugin
from project_installation import prepare
from agent_connections import NativeConnection, verify_artifact
from project_control import Registry
from project_policy import PolicyError
from unittest.mock import patch
import shutil

_FILES_FOR_PATCH = None


def setUpModule():
    global _FILES_FOR_PATCH
    real_files_for = project_host_plugin.files_for
    cache = {}

    def _cached_files_for(kind, archive=None, *, source=None):
        resolved = Path(archive) if archive is not None else project_host_plugin.resolve_official_archive(source)
        try:
            st = os.lstat(resolved)
            key = (
                kind,
                str(resolved),
                st.st_dev,
                st.st_ino,
                st.st_size,
                st.st_mtime_ns,
                project_host_plugin.CLAUDE_FRESH_BOUNDARY,
            )
        except OSError:
            return real_files_for(kind, archive=archive, source=source)
        hit = cache.get(key)
        if hit is None:
            files, upstream = real_files_for(kind, archive=archive, source=source)
            hit = (dict(files), copy.deepcopy(upstream))
            cache[key] = hit
        return dict(hit[0]), copy.deepcopy(hit[1])

    _FILES_FOR_PATCH = patch.object(project_host_plugin, 'files_for', side_effect=_cached_files_for)
    _FILES_FOR_PATCH.start()


def tearDownModule():
    global _FILES_FOR_PATCH
    if _FILES_FOR_PATCH is not None:
        _FILES_FOR_PATCH.stop()
        _FILES_FOR_PATCH = None


class HostPluginTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Shared read-only prepared artifacts for tests that only read scripts/ with per-test HOME.
        cls._shared_temp = tempfile.TemporaryDirectory()
        cls._shared_root = Path(cls._shared_temp.name).resolve()
        private_write(cls._shared_root / 'child.key', b'synthetic-child-key')
        private_write(cls._shared_root / 'control.key', b'synthetic-control-key-' * 3)
        shared_agent = {
            'key_file': str(cls._shared_root / 'child.key'),
            'control_token_file': str(cls._shared_root / 'control.key'),
        }
        cls._shared_artifacts = {}
        for kind in ('claude_code', 'cursor'):
            installation = prepare(
                None, cls._shared_root / 'private', kind + '-synthetic', shared_agent,
                'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(cls._shared_root),
                host_kind=kind, python=sys.executable,
            )
            verify_artifact(installation)
            cls._shared_artifacts[kind] = installation

    @classmethod
    def tearDownClass(cls):
        cls._shared_temp.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        private_write(self.root / 'child.key', b'synthetic-child-key')
        private_write(self.root / 'control.key', b'synthetic-control-key-' * 3)
        self.agent = {'key_file': str(self.root / 'child.key'), 'control_token_file': str(self.root / 'control.key')}

    def tearDown(self):
        self.temp.cleanup()

    def artifact(self, kind):
        return prepare(None, self.root / 'private', kind + '-synthetic', self.agent,
                       'http://127.0.0.1:18999', 'http://127.0.0.1:18998', str(self.root),
                       host_kind=kind, python=sys.executable)

    def isolated(self, installation, source):
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
        env.update(
            HOME=str(self.root),
            COGNEE_PLUGIN_IN_VENV='1',
            COGNEE_SESSION_KEY='synthetic-host',
            PYTHONDONTWRITEBYTECODE='1',
        )
        result = subprocess.run([sys.executable, '-c', source, str(Path(installation['plugin_dir']) / 'scripts')],
                                env=env, cwd=self.root, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr[-3000:])

    def test_independent_pinned_host_artifacts_are_immutable_and_private(self):
        for kind in ('claude_code', 'cursor'):
            installation = self.artifact(kind)
            verify_artifact(installation)
            root = Path(installation['plugin_dir'])
            self.assertEqual((root / '.memlane-installation.json').stat().st_mode & 0o777, 0o600)
            self.assertEqual(Path(installation['env_file']).stat().st_mode & 0o777, 0o600)
            self.assertIn(kind + '-' + kind + '-synthetic', (root / 'scripts/_plugin_common.py').read_text())
            self.assertEqual(self.artifact(kind)['plugin_dir'], str(root))
            self.assertFalse((self.root / '.claude/settings.json').exists())
            self.assertFalse((self.root / '.cursor/plugins').exists())
        bad = self.root / 'wrong-archive'
        bad.write_bytes(b'not the pinned archive')
        with self.assertRaisesRegex(ValueError, 'fixed official host archive mismatch'):
            files_for('claude_code', bad)

    def test_claude_parser_respects_boundary_and_never_scans_sidechain_history(self):
        installation = self._shared_artifacts['claude_code']
        self.isolated(installation, r'''
import json, pathlib, sys
sys.path.insert(0, sys.argv[1])
import _coding_memory as coding
import _memlane_policy as policy
policy.managed = lambda: True
path = pathlib.Path.home() / "synthetic.jsonl"
old = json.dumps({"type":"user","uuid":"old","message":{"content":"forbidden old prompt"}}) + "\n"
partial = '{"type":"user","message":{"content":"disabled partial'
new = [
 {"type":"user","uuid":"new","message":{"content":"allowed new prompt"}},
 {"type":"assistant","uuid":"a","message":{"content":[{"type":"text","text":"allowed new answer"}]}},
]
path.write_text(old + partial + ' record"}}\n' + ''.join(json.dumps(row)+"\n" for row in new))
def forbidden(*args): raise AssertionError("scanned unrelated transcript history")
coding._claude_files = forbidden
parsed = coding.parse_claude(str(path), len((old + partial).encode()))
assert parsed["request"] == "allowed new prompt", parsed
assert parsed["answer"] == "allowed new answer", parsed
assert "forbidden" not in json.dumps(parsed)
assert "disabled" not in json.dumps(parsed)
''')

    def test_cursor_deferred_injection_rebuilds_and_missing_answer_never_reads_history(self):
        installation = self._shared_artifacts['cursor']
        self.isolated(installation, r'''
import pathlib, sys
sys.path.insert(0, sys.argv[1])
import cursor_hook as cursor
import _coding_memory as coding
import _memlane_policy as policy
policy.current = lambda **kwargs: {"revision":1,"policy":{"permissions":[]},"common":{}}
assert coding.build_block("query", str(pathlib.Path.home())) == ""
root = pathlib.Path.home() / "held-context"
coding.build_block = lambda query,cwd: "fresh-authorized:" + query
cursor.hold_prompt_context("host", "turn1", "session-context-lookup.py", "stale-block", root, query="new query", cwd=str(pathlib.Path.home()))
assert cursor.take_prompt_context("host", "turn1", root) == "fresh-authorized:new query"
cursor.hold_prompt_context("host", "turn2", "session-context-lookup.py", "must not leak", root, query="denied query", cwd=str(pathlib.Path.home()))
coding.build_block = lambda *args: ""
assert cursor.take_prompt_context("host", "turn2", root) == ""
assert cursor.read_transcript_tail("/never-read") == []
''')

    def test_claude_fresh_start_captures_first_turn_without_replaying_existing_history(self):
        self.isolated(self._shared_artifacts['claude_code'], r'''
import json, os, pathlib, sys, uuid
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
import _coding_memory as coding
import _cloud_worker as cloud
assert cloud.enabled() is False
assert coding._cloud_spool() is False
profile = pathlib.Path.home() / "claude-profile"
parent = profile / "projects" / "synthetic"
parent.mkdir(parents=True)
os.environ["CLAUDE_CONFIG_DIR"] = str(profile)
decision = {"revision": 7, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True}}
policy.current = lambda *args, **kwargs: decision
policy.capture_decision = lambda *args: ("enabled", decision.get("capture_version", 1))
session = str(uuid.uuid4())
common.set_session_key(session)
path = parent / (session + ".jsonl")
policy.begin_capture_boundary(str(path), source="startup", session_key=session)
assert not path.exists(), "the adapter must never create the host transcript"
assert policy.capture_boundary(str(path)) is None
rows = [{"type":"user", "uuid":"first", "message":{"content":"first new coding task"}},
        {"type":"assistant", "uuid":"answer", "message":{"content":[{"type":"text", "text":"first new answer"}]}}]
path.write_text(''.join(json.dumps(row) + "\n" for row in rows))
offset = policy.capture_boundary(str(path))
assert offset == 0
parsed = coding.parse_claude(str(path), offset)
assert parsed["request"] == "first new coding task" and parsed["answer"] == "first new answer"
with policy._state() as state:
    assert "fresh_host_transcript" not in state
session = str(uuid.uuid4())
common.set_session_key(session)
existing = parent / (session + ".jsonl")
existing.write_text(''.join(json.dumps(row) + "\n" for row in rows))
boundary = existing.stat().st_size
policy.begin_capture_boundary(str(existing), source="startup", session_key=session)
assert policy.capture_boundary(str(existing)) == boundary
assert coding.parse_claude(str(existing), boundary).get("request", "") == ""
''')

    def test_cursor_and_antigravity_require_a_matching_live_pending_turn(self):
        for kind in ('cursor', 'antigravity'):
            with self.subTest(host=kind):
                installation = self._shared_artifacts[kind] if kind == 'cursor' else self.artifact(kind)
                self.isolated(installation, r'''
import asyncio, importlib.util, pathlib, sys
scripts = pathlib.Path(sys.argv[1])
sys.path.insert(0, str(scripts))
import _plugin_common as common, _memlane_policy as policy
session, dataset = "synthetic-session", "11111111-1111-4111-8111-111111111111"
common._read_map_record = lambda *a: {"session_id": session}
policy.require = lambda *a, **kw: None
status, version = "enabled", 1
policy.capture_decision = lambda *a: (status, version)
spec = importlib.util.spec_from_file_location("store", scripts / "store-to-session.py")
store = importlib.util.module_from_spec(spec)
spec.loader.exec_module(store)
store._load_session = lambda: (session, dataset, "")
store.resolve_runtime_mode = lambda: {"mode": "http", "base_url": "http://127.0.0.1:18999"}
store.load_config = lambda: {"backend": "cloud"}
store.server_usable = lambda *a, **kw: True
saved = []
store.remember_entry_via_http = lambda _dataset, _session, entry, **kw: saved.append(entry) or {}

def tool(turn):
    asyncio.run(store._store_tool_call({"turn_id": turn, "tool_name": "Read",
                 "tool_input": {"file_path": "/tmp/synthetic.py"}, "tool_response": "synthetic output"}))

def stop(turn):
    asyncio.run(store._store_assistant_stop({"turn_id": turn, "assistant_message": "synthetic reply"}))

for blocked in ("denied", "disabled"):
    common.remember_pending_prompt(session, "in-flight prompt", turn_id="before")
    status = blocked
    if blocked == "disabled": version += 1
    common.remember_pending_prompt(session, "blocked prompt", turn_id="blocked")
    assert not common._load_json_file(common._pending_file(session))
    status = "enabled"
    if blocked == "disabled": version += 1
    count = len(saved)
    tool("blocked")
    stop("before")
    assert len(saved) == count
    common.remember_pending_prompt(session, "new prompt", turn_id="new")
    tool("blocked")
    assert len(saved) == count
    tool("new")
    stop("new")
    assert [entry["type"] for entry in saved[count:]] == ["trace", "qa"], saved
    assert saved[-1]["question"] == "new prompt"

# A complete close/reopen while the host is idle also invalidates its pending turn.
common.remember_pending_prompt(session, "must leave behind", turn_id="idle")
version += 2
count = len(saved)
tool("idle")
stop("idle")
assert len(saved) == count

# Short prompts (<5 chars like "继续") skip pending prompt persistence in the fixed hook,
# yet must still capture tool calls for that authorized turn without creating a bogus QA entry.
import _memlane_access as access
assert access.pending_capture_allowed(enabled=True, turn_id="short-turn", new_turn=True)
count = len(saved)
tool("short-turn")
stop("short-turn")
assert [entry["type"] for entry in saved[count:]] == ["trace"], saved[count:]
''')

    def test_claude_fresh_start_keeps_version_across_unobserved_close_and_reopen(self):
        self.isolated(self._shared_artifacts['claude_code'], r'''
import os, pathlib, sys, uuid
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
profile = pathlib.Path.home() / "claude-profile"
parent = profile / "projects" / "synthetic"
parent.mkdir(parents=True)
os.environ["CLAUDE_CONFIG_DIR"] = str(profile)
version = 1
policy.current = lambda *a: {"revision": version, "policy": {"permissions": ["write"], "can_capture": True}}
policy.capture_decision = lambda *a: ("enabled", version)
session = str(uuid.uuid4())
common.set_session_key(session)
path = parent / (session + ".jsonl")
policy.begin_capture_boundary(str(path), source="startup", session_key=session)
with policy._state() as state:
    assert state.pop("capture_policy_version") == 1
path.write_text("first allowed turn\n")
assert policy.capture_boundary(str(path)) == 0
with policy._state(writable=False) as state:
    assert state["capture_policy_version"] == 1
version = 3
with path.open("a") as out:
    out.write("closed turn, with no hook for version 2\n")
assert policy.capture_boundary(str(path)) is None
boundary = path.stat().st_size
with path.open("a") as out:
    out.write("new allowed turn\n")
assert policy.capture_boundary(str(path)) == boundary
''')

    def test_claude_denied_transcript_is_excluded_when_write_permission_returns(self):
        self.isolated(self._shared_artifacts['claude_code'], r'''
import json, pathlib, sys
sys.path.insert(0, sys.argv[1])
import _coding_memory as coding, _memlane_policy as policy
path = pathlib.Path("claude.jsonl").absolute()
path.touch()
status = "enabled"
policy.capture_decision = lambda *a: (status, 1)
policy.begin_capture_boundary(str(path))
def turn(question):
    with path.open("a") as out:
        for kind, content in (("user", question), ("assistant", "answer")):
            out.write(json.dumps({"type": kind, "uuid": question + kind, "message": {"content": content}}) + "\n")
status = "denied"
turn("denied Claude question")
assert policy.capture_boundary(str(path)) is None
status = "enabled"
assert policy.capture_boundary(str(path)) is None
turn("authorized Claude question")
offset = policy.capture_boundary(str(path))
assert coding.parse_claude(str(path), offset)["request"] == "authorized Claude question"
''')

    def test_claude_absent_resume_changed_policy_and_wrong_path_skip_first_turn(self):
        self.isolated(self._shared_artifacts['claude_code'], r'''
import os, pathlib, sys, uuid
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
profile = pathlib.Path.home() / "claude-profile"
parent = profile / "projects" / "synthetic"
parent.mkdir(parents=True)
os.environ["CLAUDE_CONFIG_DIR"] = str(profile)
decision = {"revision": 7, "policy": {"permissions": ["read", "write"], "can_capture": True}}
policy.current = lambda *args, **kwargs: decision
policy.capture_decision = lambda *args: ("enabled", decision.get("capture_version", 1))
for source in ("resume", "fork", "clear", "compact", "startup"):
    session = str(uuid.uuid4())
    common.set_session_key(session)
    path = parent / (session + ".jsonl")
    policy.begin_capture_boundary(str(path), source=source, session_key=session)
    if source == "startup":
        decision["revision"] += 2  # revoke/restore must not turn into backfill
    path.write_text("material that must not be replayed\n")
    assert policy.capture_boundary(str(path)) is None, source
    size = path.stat().st_size
    with path.open("a") as out: out.write("next allowed turn\n")
    assert policy.capture_boundary(str(path)) == size
session = str(uuid.uuid4())
common.set_session_key(session)
path = parent / (session + ".jsonl")
policy.begin_capture_boundary(str(path), source="startup", session_key=session)
wrong = parent / "other-session.jsonl"
wrong.write_text("unrelated old material\n")
assert policy.capture_boundary(str(wrong)) is None
''')

    def test_claude_and_cursor_bundle_capture_patch_for_qi01_qi03_parity(self):
        for kind in ('claude_code', 'cursor'):
            with self.subTest(kind=kind):
                installation = self._shared_artifacts[kind]
                root = Path(installation['plugin_dir'])
                self.assertTrue((root / 'scripts/_memlane_capture.py').is_file())
                receipt = json.loads((root / '.memlane-project-access.json').read_text())
                self.assertTrue(receipt.get('capture_patch_sha256'))
                self.isolated(installation, r'''
import hashlib, json, sys, urllib.error
sys.path.insert(0, sys.argv[1])
import _plugin_common as common
import _memlane_policy as policy
import _project_memory as projects
dataset = "11111111-1111-4111-8111-111111111111"
session = "synthetic-qi01"
policy.current = lambda *a, **k: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": False}}
policy.capture_status = lambda *a: "enabled"
policy._pending_identity = lambda *a, **k: None
projects.route = lambda d, s: {"write": d}
seen = []
def fake_json_http(path, payload=None, method="POST", **kw):
    assert method == "PUT" and path.startswith("/api/v1/capture/entries/"), (method, path)
    msg_id = path.rsplit("/", 1)[-1]
    entry = payload["entry"]
    seen.append((msg_id, entry["question"]))
    if entry["question"] == "bad-timeout":
        raise TimeoutError("synthetic timeout")
    if entry["question"] == "bad-413":
        raise urllib.error.HTTPError("http://127.0.0.1/", 413, "Payload Too Large", {}, None)
    sha = hashlib.sha256(json.dumps(entry, sort_keys=True, ensure_ascii=False, allow_nan=False).encode()).hexdigest()
    return {
        "protocol": 1, "status": "saved", "message_id": msg_id,
        "dataset_id": payload["dataset_id"], "session_id": payload["session_id"],
        "entry_type": entry["type"], "payload_sha256": sha, "entry_id": "entry-ok",
    }
common._json_http_request = fake_json_http
res1 = common.remember_entry_via_http(dataset, session, {"type": "qa", "question": "bad-timeout", "answer": "a1"})
res2 = common.remember_entry_via_http(dataset, session, {"type": "qa", "question": "bad-413", "answer": "a2"})
res3 = common.remember_entry_via_http(dataset, session, {"type": "qa", "question": "good-tail", "answer": "a3"})
assert res1 == {"drained": 0, "buffered": 1}, res1
assert res2 == {"drained": 0, "buffered": 2}, res2
assert res3 == {"drained": 1, "buffered": 2}, res3
assert [q for _, q in seen] == ["bad-timeout", "bad-413", "good-tail"], seen
assert len({mid for mid, _ in seen}) == 3
cache = common._load_json_file(common._bridge_file(session))
state = cache[common._bridge_cache_key(dataset, session)]
pending = state["pending_entries"]
assert [p["question"] for p in pending] == ["bad-timeout", "bad-413"], pending
assert pending[0][common._AMBIGUOUS_KEY] is True
assert len(state["capture_retries"]) == 1 and len(state["capture_rejections"]) == 1
''')

    def test_prepare_official_host_prefers_plugin_dir_sibling_archive(self):
        manifest = json.loads((project_host_plugin.ENGINE / 'coding-memory-components.json').read_text())['plugin']
        real_archive = project_host_plugin.ROOT / '.cache/coding-memory-components' / (manifest['commit'] + '.tar.gz')
        if not real_archive.is_file():
            self.skipTest('pinned integration archive not cached locally')
        empty_repo = self.root / 'empty-repo'
        empty_repo.mkdir()
        bundle_dir = self.root / 'bundle'
        plugin_source = bundle_dir / 'managed-plugin'
        plugin_source.mkdir(parents=True)
        sibling = bundle_dir / 'integrations-upstream' / 'service.tar.gz'
        sibling.parent.mkdir(parents=True)
        shutil.copyfile(real_archive, sibling)
        with patch.object(project_host_plugin, 'ROOT', empty_repo):
            project_host_plugin._ARCHIVE_DIGEST_CACHE.clear()
            self.assertFalse(project_host_plugin.archive_available(None))
            self.assertTrue(project_host_plugin.archive_available(plugin_source))
            installation = prepare(plugin_source, self.root / 'private-sibling', 'cursor-sibling', self.agent,
                                   'http://127.0.0.1:18998/cognee', 'http://127.0.0.1:18998', str(self.root),
                                   host_kind='cursor', python=sys.executable)
            verify_artifact(installation)


class HostInstallationTests(unittest.IsolatedAsyncioTestCase):
    async def test_cursor_prepare_writes_runtime_profile_gateway_url_and_verifies_environment(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            registry = Registry(root / 'private/registry.json', type('Official', (), {'url': 'http://127.0.0.1:18999'})())
            try:
                for name in ('key', 'control'):
                    private_write(root / name, ('synthetic-' + name + '-credential-value').encode())
                cursor_profile = root / 'cursor-profile'
                cursor_profile.mkdir()
                config = {'python': sys.executable, 'agent_host_home': str(root),
                          'project_control': {'url': 'http://127.0.0.1:18998'},
                          'agent_hosts': {'claude_code': {'profile': str(root / 'empty-claude')}}}
                connection = NativeConnection(registry, config, 'cursor', cursor_profile, sys.executable)
                registry.state['agents'][connection.id] = {
                    'name': 'Cursor', 'key_file': str(root / 'key'), 'control_token_file': str(root / 'control'),
                }
                await connection.prepare(0)
                installation = registry.state['agents'][connection.id]['installation']
                self.assertIn('COGNEE_BASE_URL="http://127.0.0.1:18998/cognee"',
                              Path(installation['env_file']).read_text())
                marker = json.loads((Path(installation['plugin_dir']) / '.memlane-installation.json').read_text())
                self.assertEqual(marker['runtime_profile'], str(cursor_profile))
                env = {k: v for k, v in os.environ.items()
                       if not k.startswith(('COGNEE_', 'MEMLANE_', 'CURSOR_', 'CLAUDE_', 'CODEX_', 'LLM_', 'EMBEDDING_'))}
                env.update(HOME=str(root), COGNEE_PLUGIN_IN_VENV='1', COGNEE_SESSION_KEY='cursor-host-1')
                script = (
                    'import json, sys\n'
                    'sys.path.insert(0, sys.argv[1])\n'
                    'import _memlane_policy as p, _plugin_common as c\n'
                    'c._read_map_record = lambda *a: {}\n'
                    'captured = {}\n'
                    'def fake_req(path, payload=None, **kw):\n'
                    '    captured.update(payload or {})\n'
                    '    return {"ready": True, "project_id": "p1", "dataset_id": "11111111-1111-4111-8111-111111111111"}\n'
                    'p._request = fake_req\n'
                    'p.bind(sys.argv[2], "cursor-host-1")\n'
                    'print(json.dumps(captured["host_environment"]))\n'
                )
                proc = subprocess.run([sys.executable, '-c', script, str(Path(installation['plugin_dir']) / 'scripts'), str(root)],
                                      env=env, cwd=root, capture_output=True, text=True, timeout=15)
                self.assertEqual(proc.returncode, 0, proc.stderr)
                host_env = json.loads(proc.stdout)
                self.assertTrue(host_env['plugin_version'])
                self.assertEqual(Path(host_env['runtime_profile']).resolve(), cursor_profile.resolve())
                checked = registry.check_host_environment(connection.id, host_env)
                self.assertTrue(checked['environment_verified'], checked)
                self.assertTrue(checked['artifact_verified'], checked)
                self.assertEqual(checked['profile'], str(cursor_profile))
            finally:
                registry.close()
    async def test_cursor_prepare_activate_upgrade_keeps_immutable_paths_and_other_hooks(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            registry = Registry(root / 'private/registry.json', type('Official', (), {'url': 'http://127.0.0.1:18999'})())
            try:
                for name in ('key', 'control'):
                    private_write(root / name, ('synthetic-' + name).encode())
                config = {'python': sys.executable, 'agent_host_home': str(root),
                          'plugin_dir': 'unused-codex', 'project_control_url': 'http://127.0.0.1:18998',
                          'agent_hosts': {'claude_code': {'profile': str(root / 'empty-claude')}}}
                connection = NativeConnection(registry, config, 'cursor', root / 'cursor', sys.executable)
                registry.state['agents'][connection.id] = {'name': 'Cursor', 'key_file': str(root / 'key'), 'control_token_file': str(root / 'control')}
                original = {'version': 1, 'hooks': {'workspaceOpen': [{'command': 'echo unrelated', 'timeout': 3}],
                                                   'stop': [{'command': 'echo another'}]}, 'custom': 'preserve'}
                private_write(connection.target, json.dumps(original).encode())
                baseline = connection.target.read_bytes()
                result = await connection.prepare(0)
                self.assertEqual(connection.target.read_bytes(), baseline)
                result = await connection.activate(registry.state['revision'], result['preview']['proposed_sha256'])
                self.assertEqual(result['status'], 'configured_pending_new_session')
                self.assertEqual(result['new_sessions'], 0)
                first = dict(registry.state['agents'][connection.id]['installation'])
                self.assertTrue((Path(first['plugin_dir']) / '.cursor-plugin/plugin.json').is_file())
                self.assertFalse((connection.profile / 'plugins/local').exists())
                first_files = {str(p.relative_to(Path(first['plugin_dir']))): p.read_bytes()
                               for p in Path(first['plugin_dir']).rglob('*') if p.is_file()}
                first_hooks = json.loads(connection.target.read_text())
                first_loader = first_hooks['hooks']['workspaceOpen'][-1]
                loaded = subprocess.run(first_loader['command'], shell=True, capture_output=True, text=True, check=True)
                self.assertEqual(json.loads(loaded.stdout), {'pluginPaths': [first['plugin_dir']]})
                self.assertEqual(connection.preview()['instance_id'], connection.id)
                connection.config = {**config, 'python': '/usr/bin/python3'}
                newer = await connection.prepare(registry.state['revision'])
                self.assertEqual(json.loads(connection.target.read_text()), first_hooks, 'prepare changed active loader')
                self.assertEqual(registry.state['connections'][connection.id]['cursor_active_installation'], first)
                # Candidate and concurrent host edits fail before mutation.
                proposed_path = connection.root / 'cursor-hooks-proposed.json'
                proposed = proposed_path.read_bytes()
                proposed_path.write_bytes(proposed + b' ')
                with self.assertRaisesRegex(PolicyError, 'connection_preview_changed'):
                    await connection.activate(registry.state['revision'], newer['preview']['proposed_sha256'])
                self.assertEqual(json.loads(connection.target.read_text()), first_hooks)
                proposed_path.write_bytes(proposed)
                active = connection.target.read_bytes()
                connection.target.write_bytes(active + b' ')
                with self.assertRaisesRegex(PolicyError, 'host_configuration_changed'):
                    await connection.activate(registry.state['revision'], newer['preview']['proposed_sha256'])
                connection.target.write_bytes(active)
                imported = root / 'empty-claude/settings.json'
                private_write(imported, json.dumps({'enabledPlugins': {'cognee@other': True}}).encode())
                with self.assertRaisesRegex(PolicyError, 'cursor_third_party_import_requires_review'):
                    await connection.activate(registry.state['revision'], newer['preview']['proposed_sha256'])
                self.assertEqual(connection.target.read_bytes(), active)
                imported.unlink()
                await connection.activate(registry.state['revision'], newer['preview']['proposed_sha256'])
                second = registry.state['agents'][connection.id]['installation']
                self.assertNotEqual(first['manifest_sha256'], second['manifest_sha256'])
                hooks = json.loads(connection.target.read_text())
                self.assertEqual(hooks['custom'], original['custom'])
                self.assertEqual(hooks['hooks']['stop'], original['hooks']['stop'])
                self.assertEqual(hooks['hooks']['workspaceOpen'][:-1], original['hooks']['workspaceOpen'])
                self.assertNotEqual(hooks['hooks']['workspaceOpen'][-1], first_loader)
                loaded = subprocess.run(hooks['hooks']['workspaceOpen'][-1]['command'], shell=True, capture_output=True, text=True, check=True)
                self.assertEqual(json.loads(loaded.stdout), {'pluginPaths': [second['plugin_dir']]})
                loaded = subprocess.run(first_loader['command'], shell=True, capture_output=True, text=True, check=True)
                self.assertEqual(json.loads(loaded.stdout), {'pluginPaths': [first['plugin_dir']]})
                self.assertEqual(first_files, {str(p.relative_to(Path(first['plugin_dir']))): p.read_bytes()
                                             for p in Path(first['plugin_dir']).rglob('*') if p.is_file()})
                verify_artifact(first)
                # A crash after the host write may be reconciled read-only.
                record = registry.state['connections'][connection.id]
                record['status'] = 'activation_unknown'
                before = connection.target.read_bytes()
                registry.save()
                registry.close()
                registry = Registry(root / 'private/registry.json', type('Official', (), {'url': 'http://127.0.0.1:18999'})())
                connection = NativeConnection(registry, connection.config, 'cursor', root / 'cursor', sys.executable)
                with patch('agent_connections.private_write', side_effect=AssertionError('review wrote host')):
                    reviewed = await connection.review(registry.state['revision'])
                self.assertEqual(reviewed['status'], 'configured_pending_new_session')
                self.assertEqual(connection.target.read_bytes(), before)
                connection.target.write_text('{}')
                with self.assertRaisesRegex(PolicyError, 'cursor_loader_changed_requires_review'):
                    connection.view()
                connection.target.write_bytes(before)
                legacy = connection.profile / 'plugins/local/memlane-cognee'
                legacy.mkdir(parents=True)
                (legacy / 'keep.txt').write_text('old runtime dependency')
                with self.assertRaisesRegex(PolicyError, 'duplicate_cursor_memory_plugin_requires_review'):
                    connection.preview()
                self.assertEqual((legacy / 'keep.txt').read_text(), 'old runtime dependency')
                self.assertEqual(connection.target.read_bytes(), before)
            finally:
                registry.close()

    async def test_real_claude_native_installer_only_updates_isolated_profile(self):
        executable = shutil.which('claude')
        if not executable:
            self.skipTest('Claude Code not installed')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            registry = Registry(root / 'private/registry.json', type('Official', (), {'url': 'http://127.0.0.1:18999'})())
            try:
                for name in ('key', 'control'):
                    private_write(root / name, ('synthetic-' + name).encode())
                config = {'python': sys.executable, 'agent_host_home': str(root),
                          'plugin_dir': 'unused-codex', 'project_control_url': 'http://127.0.0.1:18998'}
                connection = NativeConnection(registry, config, 'claude_code', root / 'claude-profile', executable)
                registry.state['agents'][connection.id] = {'name': 'Claude', 'key_file': str(root / 'key'), 'control_token_file': str(root / 'control')}
                prepared = await connection.prepare(0)
                self.assertFalse(connection.target.exists())
                ambient = root / 'unrelated-home'
                ambient.mkdir()
                with patch.dict(os.environ, {'HOME': str(ambient)}), patch(
                        'agent_connections.subprocess.run', wraps=subprocess.run) as installer:
                    result = await connection.activate(registry.state['revision'], prepared['preview']['proposed_sha256'])
                self.assertEqual(len(installer.call_args_list), 2)
                self.assertTrue(all(call.kwargs['env']['HOME'] == str(root)
                                    for call in installer.call_args_list))
                self.assertEqual(list(ambient.iterdir()), [])
                self.assertEqual(result['status'], 'configured_pending_new_session')
                settings = json.loads(connection.target.read_text())
                self.assertTrue(any(name.startswith('cognee@memlane-') and enabled for name, enabled in settings['enabledPlugins'].items()))
                self.assertFalse((root / '.claude/settings.json').exists())
                old_plugin = registry.state['connections'][connection.id]['enabled_plugin']
                old_installation = dict(registry.state['agents'][connection.id]['installation'])
                with patch('project_host_plugin.CLAUDE_FRESH_BOUNDARY',
                           project_host_plugin.CLAUDE_FRESH_BOUNDARY + '\n# isolated update fixture\n'):
                    updated = await connection.prepare(registry.state['revision'])
                with patch.dict(os.environ, {'HOME': str(ambient)}), patch(
                        'agent_connections.subprocess.run', wraps=subprocess.run) as installer:
                    await connection.activate(registry.state['revision'], updated['preview']['proposed_sha256'])
                self.assertEqual(len(installer.call_args_list), 3)
                settings = json.loads(connection.target.read_text())['enabledPlugins']
                new_plugin = registry.state['connections'][connection.id]['enabled_plugin']
                self.assertNotEqual(new_plugin, old_plugin)
                self.assertFalse(settings[old_plugin])
                self.assertTrue(settings[new_plugin])
                self.assertEqual(sum(bool(enabled) for name, enabled in settings.items()
                                     if name.startswith('cognee@')), 1)
                verify_artifact(old_installation)
                self.assertEqual(list(ambient.iterdir()), [])
                changed = json.loads(connection.target.read_text())
                changed['enabledPlugins'][old_plugin] = True
                private_write(connection.target, json.dumps(changed).encode())
                with self.assertRaisesRegex(PolicyError, 'claude_installation_requires_review'):
                    connection.view()
                private_write(connection.target, b'[]')
                with self.assertRaisesRegex(PolicyError, 'claude_installation_requires_review'):
                    connection.view()
            finally:
                registry.close()


if __name__ == '__main__':
    unittest.main()
