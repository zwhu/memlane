package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRawRecordSessionRowsPreserveWholeSessionIdentity(t *testing.T) {
	resp := v2RawRecordsResponse{DatasetID: "ds", CacheReady: true, Items: []v2RawRecordItem{
		{ItemKind: "session", Session: &v2RawSessionEntry{Session: Session{ID: "codex_one", Dataset: "ds"}, MsgCount: 4}},
		{ItemKind: "session", Session: &v2RawSessionEntry{Session: Session{ID: "codex_two", Dataset: "ds"}, MsgCount: 2}},
	}}
	rows, err := v2RawRecordsToItems("ds", resp)
	if err != nil || len(rows) != 2 || rows[0].ID != "codex_one" || rows[1].ID != "codex_two" || rows[0].Key() == rows[1].Key() {
		t.Fatalf("same host session split or different sessions merged: %+v %v", rows, err)
	}
}

func TestSessionSubjectUsesOfficialFirstLabelNotRecentTail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"session_id": "codex_real-session", "dataset_id": "ds", "user_id": "owner", "label": "会话最初的问题", "msg_count": 1, "tool_calls": 0, "history_paging": true, "page_limit": 20, "qas": []QA{{Question: "带有首条label的保存问答", Answer: "saved"}}})
	}))
	defer server.Close()
	s := Session{ID: "codex_real-session", Dataset: "ds", User: "owner"}
	i, err := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}).read(context.Background(), Item{ID: s.ID, Dataset: s.Dataset, Kind: "会话", Session: &s})
	if err != nil || i.Title != "会话最初的问题" {
		t.Fatalf("session subject followed a moving tail: %q %v", i.Title, err)
	}
}
