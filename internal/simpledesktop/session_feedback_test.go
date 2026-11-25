package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/ui"
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

func TestRawRecordSessionRowsPreserveWholeSessionIdentity(t *testing.T) {
	resp := v2RawRecordsResponse{DatasetID: "ds", CacheReady: true, Items: []v2RawRecordItem{
		{ItemKind: "session", Session: &v2RawSessionEntry{Session: Session{ID: "codex_one", Dataset: "ds"}, MsgCount: 4}},
		{ItemKind: "session", Session: &v2RawSessionEntry{Session: Session{ID: "codex_two", Dataset: "ds"}, MsgCount: 2}},
	}}
	rows, err := v2RawRecordsToItems("ds", resp)
	if err != nil || len(rows) != 2 || rows[0].ID != "codex_one" || rows[1].ID != "codex_two" || rows[0].key() == rows[1].key() {
		t.Fatalf("same host session split or different sessions merged: %+v %v", rows, err)
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
