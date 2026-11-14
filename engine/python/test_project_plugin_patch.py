"""Exercise the patched official entry points in an isolated host, no service/model."""
import importlib.util
import os
from pathlib import Path
import subprocess
import shutil
import json
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('build_project_plugin', ROOT / 'scripts/build_project_plugin.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
SOURCE = ROOT / '.cache/coding-memory-components/plugin-a176248914fdb32a78354854b300174203b15a5c/integrations/codex/plugins/cognee'


class OfficialPluginPatchTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary.name)
        cls.artifact = cls.root / 'plugin'
        cls.receipt = builder.build(SOURCE, cls.artifact)

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    def run_isolated(self, code, *, pinned=False):
        with tempfile.TemporaryDirectory(dir=self.root) as temporary:
            home = Path(temporary)
            (home / '.cognee').mkdir()
            (home / '.cognee/.env').write_text('')
            token = home / 'control.token'
            token.write_text('c' * 40)
            token.chmod(0o600)
            artifact = self.artifact
            if pinned:
                artifact = home / 'installed-plugin'
                shutil.copytree(self.artifact, artifact)
                environment_file = home / 'managed.env'
                environment_file.write_text('COGNEE_BASE_URL="http://127.0.0.1:18999"\nCOGNEE_API_KEY="pinned-child-key"\nMEMLANE_PROJECT_CONTROL_URL="http://127.0.0.1:18777"\nMEMLANE_PROJECT_CONTROL_TOKEN_FILE=' + json.dumps(str(token)) + '\n')
                environment_file.chmod(0o600)
                marker = artifact / '.memlane-installation.json'
                marker.write_text(json.dumps({'instance_id': 'synthetic', 'env_file': str(environment_file)}))
                marker.chmod(0o600)
            environment = {key: value for key, value in os.environ.items()
                           if not key.startswith(('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_'))}
            environment.update(HOME=str(home), COGNEE_ENV_FILE=str(home / '.cognee/.env'),
                               COGNEE_PLUGIN_IN_VENV='1', COGNEE_SESSION_KEY='synthetic-host',
                               COGNEE_BASE_URL='http://127.0.0.1:18999', COGNEE_API_KEY='synthetic-child-key',
                               COGNEE_CODING_MEMORY='true', MEMLANE_PROJECT_CONTROL_URL='http://127.0.0.1:18777',
                               MEMLANE_PROJECT_CONTROL_TOKEN_FILE=str(token))
            process = subprocess.run([sys.executable, '-c', code], cwd=home, env={**environment, 'PYTHONPATH': str(artifact / 'scripts')},
                                     capture_output=True, text=True, timeout=20)
            self.assertEqual(process.returncode, 0, process.stdout[-2000:] + process.stderr[-4000:])

    def test_code_default_off_blocks_index_scan_and_old_local_opt_in(self):
        self.run_isolated('''
import _code_graph as graph, _memlane_policy as policy
policy.current = lambda *a, **kw: {"ready": True, "code_enabled": False}
def forbidden(*a, **kw):
    raise AssertionError("disabled code graph scanned or sent data")
graph.canonical_spec = forbidden
graph.find_indexed_repo = forbidden
assert graph.index_repo("http://wrong", "owner", "/private/repo")["status"] == 403
assert graph.do_index_repo("http://wrong", "owner", "/private/repo", "own-slug", opener=forbidden)["status"] == 403
assert graph.autoindex_on_session_start("/private/repo", "http://wrong", "owner") == {}
assert graph.reingest_if_changed("/private/repo", "http://wrong", "owner") == {}
assert graph.auto_code_lane("SomeFunction", "/private/repo") == {}
''')

    def test_enabled_code_group_keeps_uuid_root_and_policy_scope(self):
        self.run_isolated('''
import os, _memlane_policy as policy
policy.current = lambda: {"ready": True, "code": {"enabled": True, "ready": True, "dataset_id": "managed-code-uuid", "roots": [os.getcwd()]}}
calls = []
policy.require = lambda *a, **kw: calls.append((a,kw))
assert policy.code_target("write", workspace=os.getcwd(), automatic=True) == "managed-code-uuid"
assert calls[-1] == (("process",), {"automatic": True})
for kwargs in ({"target": "own-slug"}, {"workspace": os.path.dirname(os.getcwd())}):
    try:
        policy.code_target("write", **kwargs)
        raise AssertionError("unregistered target/root accepted")
    except policy.NotReady:
        pass
''')

    def test_shell_binding_failure_never_reaches_inherited_owner_or_curl(self):
        self.run_isolated('''
import json, os, pathlib, subprocess, sys
home=pathlib.Path.home()
(home/".cognee-plugin").mkdir()
(home/".cognee-plugin/api_key.json").write_text(json.dumps({"api_key":"cached-owner-key"}))
bin=home/"bin";bin.mkdir()
curl=bin/"curl";curl.write_text("#!/bin/sh\\ntouch "+str(home/"curl-called")+"\\n");curl.chmod(0o700)
env={k:v for k,v in os.environ.items() if not k.startswith("MEMLANE_")}
env.update(COGNEE_API_KEY="inherited-owner-key",PATH=str(bin)+os.pathsep+os.environ["PATH"])
scripts=pathlib.Path(sys.path[1])
for name,arg in (("cognee-forget.sh","env"),("cognee-index-repo.sh","/unused"),("cognee-search.sh","probe"),("cognee-remember.sh","probe")):
    result=subprocess.run(["bash",str(scripts/name),arg],env=env,capture_output=True,text=True,timeout=10)
    assert result.returncode != 0,(name,result.stdout,result.stderr)
    assert "owner-key" not in result.stdout,(name,result.stdout)
    assert not (home/"curl-called").exists(),name
''')

    def test_installation_pin_overrides_inherited_owner_and_shared_environment(self):
        self.run_isolated('''
import os
os.environ["COGNEE_API_KEY"] = "inherited-owner-key"
os.environ["COGNEE_SHARED_AGENT_MEMORY"] = "true"
import config, _plugin_common as common
loaded = config.load_config()
assert loaded["api_key"] == "pinned-child-key"
assert common._api_key() == "pinned-child-key"
assert common.plugin_identity_mode() == "disabled"
assert common.shared_memory_enabled() is False
assert os.environ["COGNEE_ENV_FILE"].endswith("managed.env")
''', pinned=True)

    def test_prepared_installation_is_private_literal_unicode_and_does_not_edit_host(self):
        from project_installation import prepare
        from project_control import private_write
        with tempfile.TemporaryDirectory(dir=self.root) as temporary:
            root = Path(temporary) / '配置目录'
            root.mkdir()
            home = root / 'host-home'
            home.mkdir()
            private_write(root / 'official.key', b'prepared-child-key')
            private_write(root / 'control.key', b'c' * 40)
            agent = {'key_file': str(root / 'official.key'), 'control_token_file': str(root / 'control.key')}
            installation = prepare(self.artifact, root, 'codex-test', agent, 'http://127.0.0.1:18999', 'http://127.0.0.1:18777', str(home))
            self.assertEqual(Path(installation['env_file']).stat().st_mode & 0o777, 0o600)
            marker = Path(installation['plugin_dir']) / '.memlane-installation.json'
            self.assertEqual(marker.stat().st_mode & 0o777, 0o600)
            self.assertFalse((self.artifact / '.memlane-installation.json').exists())
            self.assertEqual(list(home.iterdir()), [])
            marketplace = Path(installation['marketplace_root'])
            catalog = json.loads((marketplace / '.agents/plugins/marketplace.json').read_text())
            self.assertEqual(catalog['name'], installation['marketplace_name'])
            self.assertEqual((marketplace / catalog['plugins'][0]['source']['path']).resolve(), Path(installation['plugin_dir']).resolve())
            self.assertNotIn('prepared-child-key', json.dumps(catalog))
            other = prepare(self.artifact, root, 'codex-other', agent, 'http://127.0.0.1:18999', 'http://127.0.0.1:18777', str(home))
            self.assertNotEqual(other['marketplace_name'], installation['marketplace_name'])
            self.assertTrue(marker.exists())
            environment = {**os.environ, 'HOME': str(home), 'COGNEE_PLUGIN_IN_VENV': '1',
                           'PYTHONPATH': str(Path(installation['plugin_dir']) / 'scripts'), 'COGNEE_API_KEY': 'old-owner'}
            code = 'import config,_plugin_common; assert config.load_config()["api_key"]=="prepared-child-key"; assert _plugin_common._api_key()=="prepared-child-key"'
            result = subprocess.run([sys.executable, '-c', code], cwd=home, env=environment, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr[-2000:])

    def test_missing_managed_config_cannot_fall_back_to_global_owner_or_cli(self):
        self.run_isolated('''
import os, subprocess
from pathlib import Path
for key in ("MEMLANE_PROJECT_CONTROL_URL", "MEMLANE_PROJECT_CONTROL_TOKEN_FILE"):
    os.environ.pop(key, None)
os.environ["COGNEE_API_KEY"] = "old-owner-key"
import _env_file
try:
    _env_file.load_env_file()
except RuntimeError as error:
    assert "managed_installation_configuration_required" in str(error)
else:
    raise AssertionError("unconfigured artifact inherited owner")
fake = Path("fake-bin").absolute();fake.mkdir()
uv = fake / "uv";uv.write_text("#!/bin/sh\\ntouch CLI_FALLBACK_USED\\n");uv.chmod(0o700)
os.environ["PATH"] = str(fake) + ":" + os.environ["PATH"]
script = Path(_env_file.__file__).with_name("cognee-remember.sh")
result = subprocess.run(["bash", str(script), "synthetic"], env=os.environ, capture_output=True, text=True, timeout=10)
assert result.returncode != 0
assert not Path("CLI_FALLBACK_USED").exists()
''')

    def test_bound_config_disables_shared_roles_and_cached_owner(self):
        self.run_isolated('''
import config, _memlane_policy as policy, _plugin_common as common
policy.dataset = lambda: "11111111-1111-4111-8111-111111111111"
loaded = config.load_config()
assert config.get_dataset(loaded) == policy.dataset()
assert loaded["shared_agent_memory"] is False
assert common.plugin_identity_mode() == "disabled"
assert common.shared_memory_enabled() is False
common.load_cached_api_key = lambda *a: "old-owner-key"
common.load_cached_agent_key = lambda *a: "old-cached-key"
assert common._api_key() == "synthetic-child-key"
''')

    def test_capture_refusal_precedes_any_transcript_open(self):
        self.run_isolated('''
import builtins
from pathlib import Path
import _coding_memory as coding, _memlane_policy as policy
transcript = Path("transcript.jsonl").absolute()
transcript.write_text("disabled private transcript\\n")
policy.permit = lambda *a, **k: False
original = builtins.open
def guarded(path, *a, **k):
    if Path(path) == transcript:
        raise AssertionError("disabled transcript opened")
    return original(path, *a, **k)
builtins.open = guarded
assert coding.capture({"transcript_path": str(transcript)}) == {"skipped": "managed_capture_paused"}
''')

    def test_parser_offset_skips_partial_and_pre_boundary_turn(self):
        self.run_isolated('''
import json
from pathlib import Path
import _coding_memory as coding
def item(kind, text):
    return json.dumps({"type": "event_msg", "timestamp": "2026-11-13T00:00:00Z", "payload": {"type": "item_completed", "item": {"type": kind, "content": text}}}) + "\\n"
prefix = item("UserMessage", "disabled prompt")
tail = 'discard partial suffix\\n' + item("AgentMessage", "old turn answer") + item("UserMessage", "allowed prompt") + item("AgentMessage", "allowed answer")
path = Path("transcript.jsonl")
path.write_text(prefix + tail)
result = coding.parse_codex(str(path), len(prefix.encode()))
assert len(result["units"]) == 1, result
assert result["units"][0]["request"] == "allowed prompt", result
assert "disabled" not in json.dumps(result)
assert "old turn" not in json.dumps(result)
''')

    def test_explicit_save_denied_before_file_read(self):
        self.run_isolated('''
import _remember_http as remember, _memlane_policy as policy
policy.permit = lambda *a, **k: False
result = remember.do_remember("http://127.0.0.1:18999", "child", "", "11111111-1111-4111-8111-111111111111", "test", file_path="does-not-exist.txt")
assert result["status"] == 403, result
assert "写入权限" in result["error"], result
''')

    def test_auto_improve_all_final_triggers_skip_before_network(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy
seen = []
def permit(operation, dataset=None, automatic=False):
    seen.append(automatic)
    return not automatic
policy.permit = permit
common._backend_reachable = lambda *a: (_ for _ in ()).throw(AssertionError("unexpected network"))
for trigger in ("auto", "idle", "final", "exit", "coding_cooldown"):
    result = common.run_session_improve_detailed("11111111-1111-4111-8111-111111111111", "synthetic", trigger=trigger)
    assert result["reason"] == "managed_policy_paused", result
assert all(seen)
''')

    def test_read_revocation_checked_before_cached_delivery(self):
        self.run_isolated('''
import _coding_memory as coding, _memlane_policy as policy
policy.permit = lambda *a, **k: False
coding.read_state = lambda *a: (_ for _ in ()).throw(AssertionError("stale delivery state used"))
coding.reset_delivery = lambda *a: None
assert coding.deliver("synthetic-host", "query", ".") == ""
assert coding.build_block("query", ".") == ""
assert coding.warm_lessons(".") == 0
''')

    def test_common_read_denial_does_not_block_unrelated_project_buffer(self):
        self.run_isolated('''
import urllib.error
import _plugin_common as common, _memlane_policy as policy
policy.current = lambda *a, **k: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": True}}
def denied(*a, **k):
    raise urllib.error.HTTPError("http://127.0.0.1/", 403, "Forbidden", {}, None)
common.urlopen_following_307 = denied
try:
    common._json_http_request("/api/v1/search", {"dataset_ids": ["common"]})
except urllib.error.HTTPError:
    pass
assert policy.permit("drain")
try:
    common._json_http_request("/api/v1/remember/entry", {})
except urllib.error.HTTPError:
    pass
assert not policy.permit("drain")
''')

    def test_official_buffer_403_remains_blocked_without_automatic_replay(self):
        self.run_isolated('''
import urllib.error
import _plugin_common as common, _memlane_policy as policy
dataset = "11111111-1111-4111-8111-111111111111"
policy.current = lambda *a: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": True}}
common.append_warmup_entry(dataset, "synthetic", {"type": "qa", "question": "synthetic", "answer": "legal pending"})
calls = []
def denied(*a, **k):
    calls.append(1)
    raise urllib.error.HTTPError("http://127.0.0.1/", 403, "Forbidden", {}, None)
common.remember_entry_via_http = denied
assert common.drain_warmup_entries(dataset, "synthetic") == (0, 1)
for _ in range(3):
    assert common.drain_warmup_entries(dataset, "synthetic") == (0, 1)
assert len(calls) == 1
assert not policy.permit("process", dataset, automatic=True)
cache = common._load_json_file(common._bridge_file("synthetic"))
assert len(cache[common._bridge_cache_key(dataset, "synthetic")]["pending_entries"]) == 1
''')

    def test_ambiguous_buffer_never_blindly_replayed_on_failed_verification(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy
dataset = "11111111-1111-4111-8111-111111111111"
policy.current = lambda *a: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": True}}
common.append_warmup_entry(dataset, "synthetic", {"type": "qa", "question": "synthetic", "answer": "unknown outcome"}, ambiguous=True)
common.get_session_detail_via_http = lambda *a, **k: None
common.remember_entry_via_http = lambda *a, **k: (_ for _ in ()).throw(AssertionError("unknown write replayed"))
assert common.drain_warmup_entries(dataset, "synthetic") == (0, 1)
assert not policy.permit("drain", dataset)
''')

    def test_process_interruption_after_send_keeps_unknown_outcome_in_official_buffer(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy
dataset = "11111111-1111-4111-8111-111111111111"
policy.current = lambda *a, **k: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": True}}
common.append_warmup_entry(dataset, "synthetic", {"type": "qa", "question": "synthetic", "answer": "lawful pending"})
def interrupted(*a, **k):
    raise SystemExit("simulated host exit after POST")
common.remember_entry_via_http = interrupted
try:
    common.drain_warmup_entries(dataset, "synthetic")
except SystemExit:
    pass
cache = common._load_json_file(common._bridge_file("synthetic"))
pending = cache[common._bridge_cache_key(dataset, "synthetic")]["pending_entries"]
assert len(pending) == 1 and pending[0][common._AMBIGUOUS_KEY] is True
common.get_session_detail_via_http = lambda *a, **k: None
common.remember_entry_via_http = lambda *a, **k: (_ for _ in ()).throw(AssertionError("unknown POST replayed after host exit"))
assert common.drain_warmup_entries(dataset, "synthetic") == (0, 1)
''')

    def test_explicit_buffer_replay_and_discard_bind_reviewed_snapshot(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy
dataset = "11111111-1111-4111-8111-111111111111"
policy.dataset = lambda: dataset
common._read_map_record = lambda *a: {"session_id": "synthetic"}
policy.current = lambda *a: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": True}}
entry = {"type": "qa", "question": "synthetic", "answer": "lawful pending"}
common.append_warmup_entry(dataset, "synthetic", entry)
policy.block(403)
review = policy.pending_materials(dataset, "synthetic")
assert review["pending"] == 1
assert "lawful pending" not in str(review)
common.append_warmup_entry(dataset, "synthetic", {**entry, "answer": "later lawful entry"})
try:
    policy.decide_pending(dataset, "synthetic", "discard", review["signature"])
except policy.NotReady as error:
    assert str(error) == "pending_materials_changed_review_again"
else:
    raise AssertionError("stale consent discarded new materials")
review = policy.pending_materials(dataset, "synthetic")
calls = []
common.remember_entry_via_http = lambda *a, **k: calls.append(1)
assert policy.decide_pending(dataset, "synthetic", "replay", review["signature"])["drained"] == 2
assert len(calls) == 2
common.append_warmup_entry(dataset, "synthetic", entry)
policy.block(403)
review = policy.pending_materials(dataset, "synthetic")
assert policy.decide_pending(dataset, "synthetic", "discard", review["signature"])["discarded"] == 1
assert policy.pending_materials(dataset, "synthetic")["pending"] == 0
assert len(calls) == 2
''')

    def test_pending_action_refuses_inflight_send_and_unknown_write_replay(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy
dataset = "11111111-1111-4111-8111-111111111111"
policy.dataset = lambda: dataset
common._read_map_record = lambda *a: {"session_id": "synthetic"}
policy.current = lambda *a: {"revision": 1, "policy": {"permissions": ["read", "write", "delete"], "can_capture": True, "can_auto_process": True}}
common.append_warmup_entry(dataset, "synthetic", {"type": "qa", "question": "synthetic", "answer": "unknown"}, ambiguous=True)
policy.block(403)
review = policy.pending_materials(dataset, "synthetic")
acquire = common._try_acquire_drain_lock
common._try_acquire_drain_lock = lambda: False
try:
    policy.decide_pending(dataset, "synthetic", "discard", review["signature"])
except policy.NotReady as error:
    assert str(error) == "buffer_send_in_progress"
else:
    raise AssertionError("inflight buffer changed")
common._try_acquire_drain_lock = acquire
common.get_session_detail_via_http = lambda *a, **k: None
try:
    policy.decide_pending(dataset, "synthetic", "replay", review["signature"])
except policy.NotReady as error:
    assert str(error) == "original_write_result_still_unknown"
else:
    raise AssertionError("unknown write replayed")
assert policy.pending_materials(dataset, "synthetic")["signature"] == review["signature"]
''')

    def test_common_candidates_join_the_single_coding_block_with_empty_project(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy, _coding_memory as coding
common_id = "22222222-2222-4222-8222-222222222222"
snapshot = {"revision": 3, "policy": {"permissions": []}, "common": {"dataset_id": common_id, "revision": 3, "ready": True, "readable": True}}
policy.current = lambda *a, **k: snapshot
assert policy.permit("recall")
assert not policy.permit("read")
requests = []
def request(path, payload=None, **kw):
    requests.append(path)
    if path.startswith("/api/v1/datasets/"):
        return [{"id": "one", "datasetId": common_id, "externalMetadata": {"memlane_kind": "common_knowledge", "category": "preference", "source": "user test source"}},
                {"id": "foreign", "datasetId": "wrong", "externalMetadata": {"memlane_kind": "common_knowledge", "category": "preference"}}]
    assert path == "/api/v1/search"
    assert payload["search_type"] == "CHUNKS"
    assert payload["dataset_ids"] == [common_id]
    return [{"dataset_id": common_id, "search_result": [{"document_id": "one"}, {"document_id": "foreign"}]}]
common._json_http_request = request
reads = []
def raw(dataset, key, timeout):
    assert dataset == common_id and key == "one"
    reads.append(key)
    return "# Common preference\\nUse the documented validation command."
coding._raw_text = raw
coding._project_build_block = lambda *a: (_ for _ in ()).throw(AssertionError("unreadable project queried"))
result = coding.build_block("validation", ".")
assert "Common preference" in result and "项目明确约定优先" in result
assert "user test source" in result
assert len(result) <= 6000 and reads == ["one"]
assert requests.count("/api/v1/search") == 1
snapshot["common"]["readable"] = False
assert coding.build_block("validation", ".") == ""
''')

    def test_common_no_query_warmup_limits_candidates_and_policy_change_clears_result(self):
        self.run_isolated('''
import _plugin_common as common, _memlane_policy as policy, _coding_memory as coding
common_id = "22222222-2222-4222-8222-222222222222"
calls = []
snapshot = {"revision": 3, "policy": {"permissions": ["read"]}, "common": {"dataset_id": common_id, "revision": 3, "ready": True, "readable": True}}
policy.current = lambda *a, **k: snapshot
common._json_http_request = lambda *a, **k: [{"id": str(i), "datasetId": common_id, "createdAt": str(i), "externalMetadata": {"memlane_kind": "common_knowledge", "category": "experience"}} for i in range(30)]
def raw(dataset, key, timeout):
    calls.append(key)
    return "content " + key
coding._raw_text = raw
result = policy.merge_block("", ".", lambda *a: "project rules" * 500)
assert len(calls) == 3 and len(result) <= 6000
assert result.startswith("project rules")
generation = [0]
def changed(*a, **k):
    generation[0] += 1
    return {**snapshot, "revision": generation[0]}
policy.current = changed
assert policy.merge_block("", ".", lambda *a: "stale project") == ""
''')


class BacklogWorkerTests(unittest.TestCase):
    setUpClass = classmethod(OfficialPluginPatchTests.setUpClass.__func__)
    tearDownClass = classmethod(OfficialPluginPatchTests.tearDownClass.__func__)
    run_isolated = OfficialPluginPatchTests.run_isolated
    # Reuse the fixed official artifact and its real buffers/locks/journals.
    def backlog_case(self, scenario):
        prefix = '''
import sys, copy
sys.path.insert(0, ENGINE_PYTHON)
import _plugin_common as common, _memlane_policy as policy
from project_backlog_worker import review, save
dataset="11111111-1111-4111-8111-111111111111"
policy.dataset=lambda:dataset
common._read_map_record=lambda *a:{"session_id":"synthetic"}
snapshot={"revision":1,"ready":True,"policy":{"permissions":["read","write","delete"],"can_capture":True,"can_auto_process":False}}
policy.current=lambda *a,**k:snapshot
def append(n,ambiguous=False):
    common.append_warmup_entry(dataset,"synthetic",{"type":"qa","question":"q"+str(n),"answer":"answer"},ambiguous=ambiguous)
def pending():
    return common._load_json_file(common._bridge_file("synthetic"))[common._bridge_cache_key(dataset,"synthetic")]["pending_entries"]
def payload():
    return dict(review(common,policy,dataset,"synthetic-host"),dataset=dataset)
events=[]
'''.replace('ENGINE_PYTHON', repr(str(ROOT / 'engine/python')))
        self.run_isolated(prefix + scenario)

    def test_backlog_continues_all_reviewed_safe_entries_past_unknown_and_old_budget(self):
        self.backlog_case('''
append("unknown",True)
for n in range(35):append(n)
original=copy.deepcopy(pending()[0])
scope=payload()
assert len(scope["tokens"])==35 and scope["unknown"]==1
common._drain_budget_default=lambda:0
sent=[]
def post(dataset,session,entry,**kw):
    sent.append(entry["question"])
    if len(sent)==1:append("new-after-start")
common.remember_entry_via_http=post
save(common,policy,scope,lambda:False,events.append)
assert events[-1]["status"]=="completed" and events[-1]["saved"]==35,events[-1]
assert len(sent)==35 and len(set(sent))==35
assert pending()[0]==original and len(pending())==2
assert pending()[1]["question"]=="qnew-after-start"
assert not common._DRAIN_LOCK.exists()
''')

    def test_backlog_unknown_post_is_never_replayed_and_other_safe_tail_can_continue(self):
        self.backlog_case('''
append(1);append(2)
sent=[]
def fail(*a,**k):
    sent.append(1)
    raise TimeoutError("ambiguous")
common.remember_entry_via_http=fail
save(common,policy,payload(),lambda:False,events.append)
assert events[-1]["status"]=="paused" and events[-1]["reason"]=="request_result_unknown"
assert pending()[0][common._AMBIGUOUS_KEY]
common.remember_entry_via_http=lambda ds,ss,e,**kw:sent.append(e["question"])
scope=payload()
assert scope["unknown"]==1 and len(scope["tokens"])==1
save(common,policy,scope,lambda:False,events.append)
assert sent==[1,"q2"] and len(pending())==1
assert pending()[0][common._AMBIGUOUS_KEY]
''')

    def test_backlog_cancel_and_revoke_wait_for_current_result_without_resending(self):
        self.backlog_case('''
append(1);append(2)
stop=[False]
sent=[]
def post(*a,**k):
    sent.append(1);stop[0]=True
common.remember_entry_via_http=post
save(common,policy,payload(),lambda:stop[0],events.append)
assert len(sent)==1 and events[-1]["status"]=="stopped" and events[-1]["saved"]==1
assert len(pending())==1 and not pending()[0].get(common._AMBIGUOUS_KEY)
append(3)
def revoke(*a,**k):
    sent.append(2);snapshot["policy"]["permissions"]=["read"]
common.remember_entry_via_http=revoke
save(common,policy,payload(),lambda:False,events.append)
assert len(sent)==2 and events[-1]["status"]=="paused" and events[-1]["reason"]=="policy_unconfirmed"
assert len(pending())==1 and not pending()[0].get(common._AMBIGUOUS_KEY)
''')


if __name__ == '__main__':
    unittest.main()
