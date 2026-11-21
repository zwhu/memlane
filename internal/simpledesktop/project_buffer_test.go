package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBufferAdapterVerifiesInputAndUsesExactInstallation(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	common := []byte(`import contextlib
_AMBIGUOUS_KEY = '_replay_ambiguous'
def set_session_key(host): assert host == 'host-session'
def _read_map_record(host): return {'session_id':'session'}
def _buffer_lock(): return contextlib.nullcontext()
`)
	os.WriteFile(filepath.Join(scripts, "_plugin_common.py"), common, 0600)
	helper := []byte(`import json,os,sys
assert os.path.realpath(os.environ["HOME"]) == os.getcwd()
assert os.environ["COGNEE_ENV_FILE"].endswith("managed.env")
class NotReady(Exception): pass
def pending_materials(dataset, session):
    assert dataset == 'dataset' and session == 'session'
    return {'pending':1,'unknown_outcomes':1,'signature':'reviewed-snapshot'}
def _pending_snapshot(dataset, session):
    with open("buffer.json") as f: pending=json.load(f)
    return None,None,None,{},pending,'reviewed-snapshot'
if __name__ == '__main__':
    assert '--session-key' in sys.argv and 'host-session' in sys.argv
    print(json.dumps({'pending':1,'signature':'reviewed-snapshot'}))
`)
	bufferPath := filepath.Join(root, "buffer.json")
	before := []byte(`[{"type":"qa","question":"synthetic local message","timestamp":"stable","_replay_ambiguous":true}]`)
	os.WriteFile(bufferPath, before, 0600)
	path := filepath.Join(scripts, "_memlane_policy.py")
	if err := os.WriteFile(path, helper, 0600); err != nil {
		t.Fatal(err)
	}
	helperSHA := fmt.Sprintf("%x", sha256.Sum256(helper))
	receipt, _ := json.Marshal(map[string]any{"files": map[string]string{"scripts/_memlane_policy.py": helperSHA, "scripts/_plugin_common.py": fmt.Sprintf("%x", sha256.Sum256(common))}})
	if err := os.WriteFile(filepath.Join(root, ".memlane-project-access.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	installation := AgentInstallation{PluginDir: root, EnvFile: filepath.Join(root, "managed.env"), Home: root, HelperSHA: helperSHA, ManifestSHA: fmt.Sprintf("%x", sha256.Sum256(receipt))}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, _ = filepath.Abs(python)
	result, err := runPendingMaterials(context.Background(), python, installation, ProjectBinding{HostSessionKey: "host-session"}, "dataset", "show", "")
	if err != nil || result.Pending != 1 || result.Signature != "reviewed-snapshot" || len(result.UnknownEntryIDs) != 1 || !validPendingHash(result.UnknownEntryIDs[0]) {
		t.Fatalf("wrong installation result: %#v %v", result, err)
	}
	if len(result.Details) != 0 {
		t.Fatal("background polling leaked message excerpts")
	}
	detail, detailErr := runPendingMaterials(context.Background(), python, installation, ProjectBinding{HostSessionKey: "host-session"}, "dataset", "details", "")
	if detailErr != nil || len(detail.Details) != 1 || detail.Details[0].Summary != "synthetic local message" || !detail.Details[0].Unknown || detail.Details[0].ID != result.UnknownEntryIDs[0] {
		t.Fatal("read-only message inspection failed", detailErr)
	}
	after, _ := os.ReadFile(bufferPath)
	if string(after) != string(before) {
		t.Fatal("read projection changed the official buffer")
	}
	if err = os.WriteFile(path, []byte("raise Exception('tampered helper must not run')"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = runPendingMaterials(context.Background(), python, installation, ProjectBinding{HostSessionKey: "host-session"}, "dataset", "replay", "reviewed-snapshot")
	if err == nil || !strings.Contains(err.Error(), "文件发生变化") {
		t.Fatal("modified helper was executed")
	}
}

func TestCommonEditorWithdrawsRevokedBodyWithoutClaimingInflightCancel(t *testing.T) {
	a := New(Config{ServiceURL: "http://127.0.0.1:18999"}, nil)
	d := Document{ID: "document", Dataset: "common"}
	a.commonEdit = commonEditorState{Open: true, Busy: true, Existing: &d, Target: Project{Dataset: "common"}, Draft: CommonDraft{Body: "protected body"}, Receipt: CommonWriteReceipt{PipelineRunID: "original-run"}}
	a.handleReadingFailure(Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识"}, fmt.Errorf("official HTTP 403"))
	if a.commonEdit.Open || a.commonEdit.Draft.Body != "" || a.commonEdit.Existing != nil {
		t.Fatal("revoked editor body remains copyable")
	}
	if !a.commonEdit.Busy || a.commonEdit.Receipt.PipelineRunID != "original-run" {
		t.Fatal("revocation pretended inflight operation was cancelled")
	}
}

func TestBufferAdapterAcceptsManifestVerifiedOfficialHostWithoutLegacyHelperSHA(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	common := []byte(`import contextlib
_AMBIGUOUS_KEY = '_replay_ambiguous'
def set_session_key(host): assert host == 'host-session'
def _read_map_record(host): return {'session_id':'session'}
def _buffer_lock(): return contextlib.nullcontext()
`)
	helper := []byte(`import json
class NotReady(Exception): pass
def pending_materials(dataset, session):
    return {'pending':0,'unknown_outcomes':0,'signature':'legacy-official-snapshot'}
def _pending_snapshot(dataset, session):
    return None,None,None,{},[],'legacy-official-snapshot'
`)
	commonPath := filepath.Join(scripts, "_plugin_common.py")
	helperPath := filepath.Join(scripts, "_memlane_policy.py")
	if err := os.WriteFile(commonPath, common, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, helper, 0600); err != nil {
		t.Fatal(err)
	}
	helperSHA := fmt.Sprintf("%x", sha256.Sum256(helper))
	commonSHA := fmt.Sprintf("%x", sha256.Sum256(common))
	receiptPath := filepath.Join(root, ".memlane-project-access.json")
	receipt, _ := json.Marshal(map[string]any{"files": map[string]string{
		"scripts/_memlane_policy.py": helperSHA,
		"scripts/_plugin_common.py":  commonSHA,
	}})
	if err := os.WriteFile(receiptPath, receipt, 0600); err != nil {
		t.Fatal(err)
	}
	manifestSHA := fmt.Sprintf("%x", sha256.Sum256(receipt))
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, _ = filepath.Abs(python)

	legacy := AgentInstallation{
		HostKind:    "antigravity",
		PluginDir:   root,
		EnvFile:     filepath.Join(root, "managed.env"),
		Home:        root,
		ManifestSHA: manifestSHA,
	}
	result, err := runPendingMaterials(context.Background(), python, legacy, ProjectBinding{HostSessionKey: "host-session"}, "dataset", "show", "")
	if err != nil || result.Signature != "legacy-official-snapshot" {
		t.Fatalf("legacy official-host installation without HelperSHA failed: %+v %v", result, err)
	}

	mismatched := legacy
	mismatched.HelperSHA = strings.Repeat("0", 64)
	if err := verifyInstallation(mismatched); err == nil || !strings.Contains(err.Error(), "受管插件输入未确认") {
		t.Fatalf("mismatched HelperSHA accepted: %v", err)
	}

	missingCommonReceipt, _ := json.Marshal(map[string]any{"files": map[string]string{
		"scripts/_memlane_policy.py": helperSHA,
	}})
	if err := os.WriteFile(receiptPath, missingCommonReceipt, 0600); err != nil {
		t.Fatal(err)
	}
	missingCommon := legacy
	missingCommon.ManifestSHA = fmt.Sprintf("%x", sha256.Sum256(missingCommonReceipt))
	if err := verifyInstallation(missingCommon); err == nil || !strings.Contains(err.Error(), "受管插件输入未确认") {
		t.Fatalf("manifest missing _plugin_common.py accepted: %v", err)
	}

	if err := os.WriteFile(receiptPath, receipt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, []byte("# tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstallation(legacy); err == nil || !strings.Contains(err.Error(), "文件发生变化") {
		t.Fatalf("tampered helper accepted without HelperSHA: %v", err)
	}

	outside := filepath.Join(filepath.Dir(root), "outside-receipt.json")
	if err := os.WriteFile(outside, receipt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstallation(legacy); err == nil || !strings.Contains(err.Error(), "受管插件输入发生变化") {
		t.Fatalf("symlinked manifest accepted: %v", err)
	}
}

func TestColdSessionBufferSnapshotAndCacheInvalidation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scripts := filepath.Join(root, "scripts")
	sessionsDir := filepath.Join(root, "sessions")
	bridgeDir := filepath.Join(root, "bridge")
	for _, dir := range []string{scripts, sessionsDir, bridgeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	common := []byte(`import contextlib, json, os
from pathlib import Path
_AMBIGUOUS_KEY = '_replay_ambiguous'
_HOST_KEY = ''
_ROOT = Path(os.environ['HOME'])
def set_session_key(host):
    global _HOST_KEY
    _HOST_KEY = host
def _session_map_path(host):
    return _ROOT / 'sessions' / f'{host}.json'
def _bridge_file(session_id=''):
    return _ROOT / 'bridge' / f'{_HOST_KEY or session_id}.json'
def _read_map_record(host):
    cpath = _ROOT / 'exec-count.txt'
    prev = int(cpath.read_text()) if cpath.exists() else 0
    cpath.write_text(str(prev + 1))
    mp = _session_map_path(host)
    if not mp.exists():
        return {}
    try:
        data = json.loads(mp.read_text())
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}
def _buffer_lock():
    return contextlib.nullcontext()
`)
	helper := []byte(`import contextlib, hashlib, json, os
from pathlib import Path
class NotReady(Exception): pass
_ROOT = Path(os.environ['HOME'])
@contextlib.contextmanager
def _state(*, writable=True):
    pp = _ROOT / 'policy.json'
    if not writable:
        if not pp.exists():
            raise FileNotFoundError(pp)
        yield json.loads(pp.read_text())
        return
    data = json.loads(pp.read_text()) if pp.exists() else {}
    yield data
    pp.write_text(json.dumps(data))
def pending_materials(dataset, session):
    bp = _ROOT / 'bridge' / f'{os.environ.get("COGNEE_SESSION_KEY", "")}.json'
    pending = json.loads(bp.read_text()).get(f'{dataset}:{session}', {}).get('pending_entries', []) if bp.exists() else []
    sig = hashlib.sha256(json.dumps(pending, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    return {'pending': len(pending), 'unknown_outcomes': 0, 'signature': sig}
def _pending_snapshot(dataset, session):
    bp = _ROOT / 'bridge' / f'{os.environ.get("COGNEE_SESSION_KEY", "")}.json'
    pending = json.loads(bp.read_text()).get(f'{dataset}:{session}', {}).get('pending_entries', []) if bp.exists() else []
    sig = hashlib.sha256(json.dumps(pending, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    return bp, f'{dataset}:{session}', {}, {}, pending, sig
`)
	commonPath := filepath.Join(scripts, "_plugin_common.py")
	helperPath := filepath.Join(scripts, "_memlane_policy.py")
	if err := os.WriteFile(commonPath, common, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, helper, 0600); err != nil {
		t.Fatal(err)
	}
	helperSHA := fmt.Sprintf("%x", sha256.Sum256(helper))
	commonSHA := fmt.Sprintf("%x", sha256.Sum256(common))
	receiptPath := filepath.Join(root, ".memlane-project-access.json")
	receipt, _ := json.Marshal(map[string]any{"files": map[string]string{
		"scripts/_memlane_policy.py": helperSHA,
		"scripts/_plugin_common.py":  commonSHA,
	}})
	if err := os.WriteFile(receiptPath, receipt, 0600); err != nil {
		t.Fatal(err)
	}
	manifestSHA := fmt.Sprintf("%x", sha256.Sum256(receipt))
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, _ = filepath.Abs(python)
	inst := AgentInstallation{
		HostKind:    "codex",
		PluginDir:   root,
		EnvFile:     filepath.Join(root, "managed.env"),
		Home:        root,
		HelperSHA:   helperSHA,
		ManifestSHA: manifestSHA,
	}
	binding := ProjectBinding{AgentID: "codex-agent", ProjectID: "proj", HostSessionKey: "cold-host"}
	countFile := filepath.Join(root, "exec-count.txt")
	readCount := func() int {
		b, err := os.ReadFile(countFile)
		if err != nil {
			return 0
		}
		var n int
		fmt.Sscanf(string(b), "%d", &n)
		return n
	}

	// 1. Missing map and missing bridge -> cold inactive snapshot, cached across repeat show calls.
	res, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", "")
	if err != nil || !res.SessionInactive || res.Pending != 0 || res.Unknown != 0 || res.ColdWatch != nil || res.Signature == "" {
		t.Fatalf("unexpected cold snapshot result: %+v err=%v", res, err)
	}
	if got := readCount(); got != 1 {
		t.Fatalf("expected 1 subprocess execution, got %d", got)
	}
	res2, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", "")
	if err != nil || !res2.SessionInactive || res2.ColdWatch != nil || res2.Signature != res.Signature {
		t.Fatalf("unexpected cached cold snapshot: %+v err=%v", res2, err)
	}
	if got := readCount(); got != 1 {
		t.Fatalf("expected cold cache hit without new subprocess, got count=%d", got)
	}

	// 2. Empty bridge file -> cold inactive snapshot with cache hit; writing pending_entries invalidates and fails closed.
	bridgePath := filepath.Join(bridgeDir, "cold-host.json")
	if err := os.WriteFile(bridgePath, []byte(`{"ds:s":{"pending_entries":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	res3, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", "")
	if err != nil || !res3.SessionInactive || readCount() != 2 {
		t.Fatalf("expected re-eval on new empty bridge: %+v err=%v count=%d", res3, err, readCount())
	}
	if _, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", ""); err != nil || readCount() != 2 {
		t.Fatalf("expected cold cache hit on unchanged empty bridge: err=%v count=%d", err, readCount())
	}
	if err := os.WriteFile(bridgePath, []byte(`{"ds:s":{"pending_entries":[{"type":"qa","question":"unsent"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", ""); err == nil || !strings.Contains(err.Error(), "官方会话尚未确认") || readCount() != 3 {
		t.Fatalf("missing map with pending bridge must fail closed: err=%v count=%d", err, readCount())
	}

	// 3. Resuming session (map_path created) -> invalidates cold cache and returns live pending count.
	if err := os.Remove(bridgePath); err != nil {
		t.Fatal(err)
	}
	if _, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", ""); err != nil || readCount() != 4 {
		t.Fatalf("failed to warm cold cache before resume: err=%v count=%d", err, readCount())
	}
	mapPath := filepath.Join(sessionsDir, "cold-host.json")
	if err := os.WriteFile(mapPath, []byte(`{"session_id":"s-resumed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bridgePath, []byte(`{"ds:s-resumed":{"pending_entries":[{"type":"qa","question":"resumed"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	resumed, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", "")
	if err != nil || resumed.SessionInactive || resumed.Pending != 1 || readCount() != 5 {
		t.Fatalf("resumed session did not invalidate cold cache: %+v err=%v count=%d", resumed, err, readCount())
	}

	// 4. Corrupt map file present -> fails closed.
	if err := os.Remove(bridgePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", ""); err == nil || !strings.Contains(err.Error(), "官方会话尚未确认") {
		t.Fatalf("present empty map must fail closed: err=%v", err)
	}

	// 5. Tampering manifest after warming cold cache invalidates cache and fails verifyInstallation.
	if err := os.Remove(mapPath); err != nil {
		t.Fatal(err)
	}
	if _, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", ""); err != nil {
		t.Fatalf("failed to warm cold cache before tamper test: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receipt, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runPendingMaterials(context.Background(), python, inst, binding, "ds", "show", ""); err == nil || !strings.Contains(err.Error(), "受管插件输入发生变化") {
		t.Fatalf("tampered manifest hit stale cold cache instead of verifyInstallation: err=%v", err)
	}
}
