package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestQueueKeepsVisibleActionReceipt(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions[0].View.Message = "本次已保存 20 条，剩余 95 条。"
	q.Sessions[1].View.Message = "已核对：20 条结果仍未知，未重复发送。"
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText(q.Sessions[0].View.Message) || !tt.HasText(q.Sessions[1].View.Message) {
		t.Fatal("save and check receipts disappeared from project queue")
	}
}

func TestPendingObservationSeparatesNewEntriesAndKeepsReceipt(t *testing.T) {
	v := pendingBufferView{Message: "已保存20条"}
	v.observe(PendingMaterials{Pending: 95, Signature: "a"})
	v.observe(PendingMaterials{Pending: 107, Signature: "b"})
	if v.Added != 12 || v.Message != "已保存20条" {
		t.Fatal("new material hid prior save receipt", v)
	}
	v.observe(PendingMaterials{Pending: 107, Signature: "b", Checked: true, Matched: 2, Unknown: 2})
	v.observe(PendingMaterials{Pending: 107, Signature: "b", Unknown: 2})
	if !v.Status.Checked || v.Status.Matched != 2 {
		t.Fatal("refresh erased reviewed identical snapshot")
	}
	v.observe(PendingMaterials{Pending: 108, Signature: "c", Unknown: 2})
	if v.Status.Checked {
		t.Fatal("new snapshot reused an obsolete check")
	}
}

func TestSessionCatalogDeduplicatesWholeSessionsNotQuestions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sessions": []Session{{ID: "codex_one", Dataset: "ds"}, {ID: "codex_one", Dataset: "ds"}, {ID: "codex_two", Dataset: "ds"}}, "has_more": false})
	}))
	defer server.Close()
	rows, _, err := (Client{Config: Config{ServiceURL: server.URL}}).projectRecords(context.Background(), Project{Dataset: "ds"}, 0, "会话")
	if err != nil || len(rows) != 2 {
		t.Fatalf("same host session split or different sessions merged: %d %v", len(rows), err)
	}
}

func TestPendingReviewQueriesOfficialAndPreservesUnknown(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("review submitted data")
		}
		json.NewEncoder(w).Encode(map[string]any{"session_id": "official-session", "dataset_id": "ds", "known": []string{"matched"}})
	}))
	defer server.Close()
	scripts := filepath.Join(root, "scripts")
	os.Mkdir(scripts, 0700)
	common := `import contextlib,json,os,urllib.request
_AMBIGUOUS_KEY='_replay_ambiguous'
def set_session_key(key): pass
def _read_map_record(key): return {'session_id':'official-session'}
def _buffer_lock(): return contextlib.nullcontext()
def get_session_detail_via_http(session, timeout=12):
    return json.load(urllib.request.urlopen(os.environ['REVIEW_TEST_URL'],timeout=timeout))
def _server_session_fingerprints(detail): return set(detail['known'])
def _entry_fingerprint(entry): return entry['value']
`
	helper := `import json,os
class NotReady(Exception): pass
def require(operation,dataset):
    assert operation=='read' and dataset=='ds'
def snapshot():
    return json.load(open(os.path.join(os.environ['HOME'],'pending.json')))
def pending_materials(dataset, session):
    p=snapshot()
    return {'pending':len(p),'unknown_outcomes':len(p),'signature':'original'}
def _pending_snapshot(dataset,session): return None,None,None,None,snapshot(),'original'
`
	os.WriteFile(filepath.Join(scripts, "_plugin_common.py"), []byte(common), 0600)
	os.WriteFile(filepath.Join(scripts, "_memlane_policy.py"), []byte(helper), 0600)
	original := []byte(`[{"value":"matched","_replay_ambiguous":true},{"value":"not-in-recent-tail","_replay_ambiguous":true}]`)
	os.WriteFile(filepath.Join(root, "pending.json"), original, 0600)
	files := map[string]string{}
	for name, value := range map[string]string{"scripts/_plugin_common.py": common, "scripts/_memlane_policy.py": helper} {
		files[name] = fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	}
	receipt, _ := json.Marshal(map[string]any{"files": files})
	os.WriteFile(filepath.Join(root, ".memlane-project-access.json"), receipt, 0600)
	installation := AgentInstallation{PluginDir: root, Home: root, EnvFile: filepath.Join(root, "env"), HelperSHA: files["scripts/_memlane_policy.py"], ManifestSHA: fmt.Sprintf("%x", sha256.Sum256(receipt))}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, _ = filepath.Abs(python)
	t.Setenv("REVIEW_TEST_URL", server.URL)
	result, err := runPendingMaterials(context.Background(), python, installation, ProjectBinding{HostSessionKey: "real-host"}, "ds", "check", "")
	after, _ := os.ReadFile(filepath.Join(root, "pending.json"))
	if err != nil || !result.Checked || result.Matched != 1 || result.Unknown != 2 || result.Pending != 2 || string(after) != string(original) {
		t.Fatalf("review changed unknown state or skipped server: %+v %v", result, err)
	}
}

func TestSessionSubjectUsesOfficialFirstLabelNotRecentTail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"session_id": "codex_real-session", "dataset_id": "ds", "user_id": "owner", "label": "会话最初的问题", "msg_count": 1, "tool_calls": 0, "history_paging": true, "page_limit": 20, "qas": []QA{{Question: "带有首条label的保存问答", Answer: "saved"}}})
	}))
	defer server.Close()
	s := Session{ID: "codex_real-session", Dataset: "ds", User: "owner"}
	i, err := (Client{Config: Config{ServiceURL: server.URL}}).read(context.Background(), Item{ID: s.ID, Dataset: s.Dataset, Kind: "会话", Session: &s})
	if err != nil || i.Title != "会话最初的问题" {
		t.Fatalf("session subject followed a moving tail: %q %v", i.Title, err)
	}
}
