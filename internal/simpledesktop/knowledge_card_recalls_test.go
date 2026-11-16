package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestKnowledgeCardRecallsUseExistingReceiptsAndDistinguishUnknown(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/knowledge/recalls" {
			t.Error("card statistics invoked a write or retrieval")
		}
		if r.URL.Query().Get("document_id") == "failed" {
			w.WriteHeader(503)
			return
		}
		count := int64(7)
		if r.URL.Query().Get("document_id") == "zero" {
			count = 0
		}
		json.NewEncoder(w).Encode(KnowledgeRecallStats{Count: count, StartedAt: "2026-11-14T02:00:00Z"})
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.page, a.kind = "知识", "知识"
	a.syncReadingIdentity()
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Loaded = true
	s.Items = []Item{{ID: "hit", Dataset: "ds", Kind: "知识"}, {ID: "zero", Dataset: "ds", Kind: "知识"}, {ID: "failed", Dataset: "ds", Kind: "知识"}}
	a.loadKnowledgeCardRecalls(s)
	drainReading(t, updates, func() bool { return s.Knowledge.RecallPending == 0 })
	for n, want := range []string{"召回 7 次", "召回 0 次", "召回 —"} {
		label, _ := knowledgeCardRecall(s.Items[n])
		if label != want {
			t.Fatalf("card %d: %s, want %s", n, label, want)
		}
	}
	a.loadKnowledgeCardRecalls(s)
	if calls.Load() != 3 {
		t.Fatal("repaint retried every statistic")
	}
	s.Items = append(s.Items, Item{ID: "new-card", Dataset: "ds", Kind: "知识"})
	a.loadKnowledgeCardRecalls(s)
	drainReading(t, updates, func() bool { return s.Knowledge.RecallPending == 0 })
	if calls.Load() != 4 {
		t.Fatal("new batch reloaded previously checked statistics")
	}
	a.InvalidateReading("ds")
	if s.Items[0].RecallStats != nil {
		t.Fatal("invalidated card retained statistics")
	}
}
