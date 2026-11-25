package simpledesktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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
		label, tip := knowledgeCardRecall(s.Items[n])
		if n == 2 && tip != "召回次数尚未读到，请稍后重试。" {
			t.Fatal("missing default-value explanation")
		}
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

func TestCatalogCountRefreshMaintainsCurrentRecallSort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/knowledge/recall-counts" {
			t.Error("unexpected endpoint", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"started_at": "2026-11-14T02:00:00Z", "items": []map[string]any{{"document_id": "a", "count": 6}, {"document_id": "b", "count": 7}}})
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600)
	a := New(Config{Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.page, a.kind = "知识", "知识"
	a.syncReadingIdentity()
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Loaded = true
	s.Knowledge.SortByRecalls = true
	s.Knowledge.Catalog = []Item{{ID: "a", Dataset: "ds", Kind: "知识", Body: "retained a", BodyLoaded: true, RecallStats: &KnowledgeRecallStats{Count: 6}}, {ID: "b", Dataset: "ds", Kind: "知识", Body: "retained b", BodyLoaded: true, RecallStats: &KnowledgeRecallStats{Count: 5}}}
	s.Items = append([]Item(nil), s.Knowledge.Catalog...)
	a.loadKnowledgeCatalogRecalls(s)
	drainReading(t, updates, func() bool { return s.Knowledge.RecallPending == 0 })
	if s.Items[0].ID != "b" || s.Knowledge.Catalog[0].ID != "b" || s.Items[1].Body != "retained a" {
		t.Fatal("refreshed order/body lost", s.Items)
	}
}
func TestOverviewStatsBatchByDatasetAndCancelOnLeave(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		calls := atomic.Int32{}
		started := make(chan struct{}, 4)
		ended := make(chan struct{}, 4)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Path != "/v1/knowledge/recall-counts" {
				t.Error("overview used per-document endpoint", r.URL.Path)
			}
			started <- struct{}{}
			if cancelled {
				<-r.Context().Done()
				ended <- struct{}{}
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"started_at": "2026-11-14T02:00:00Z", "items": []any{}})
		}))
		token := filepath.Join(t.TempDir(), "token")
		os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600)
		a := New(Config{Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
		a.page = "总览"
		updates := make(chan func(), 10)
		a.Update = func(fn func()) { updates <- fn }
		a.overviewState.Generation = 1
		for i := 0; i < 80; i++ {
			a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: fmt.Sprint(i), Dataset: "ds", Kind: "知识"})
		}
		a.loadOverviewRecallStats(1)
		if cancelled {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("batch never started")
			}
			a.navigate("图谱", 0)
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("leaving overview did not cancel request")
			}
		} else {
			drainReading(t, updates, func() bool { return a.overviewState.Snapshot.Knowledge[79].RecallStats != nil })
		}
		if calls.Load() != 1 {
			t.Fatal("overview expanded requests with row count", calls.Load())
		}
		if a.overviewState.RecallCancel != nil {
			a.overviewState.RecallCancel()
		}
		server.Close()
	}
}

func TestLeavingOverviewBeforeDirectoryCallbackCannotStartStats(t *testing.T) {
	stats := atomic.Int32{}
	releaseActivity := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/activity/pipeline-runs" {
			<-releaseActivity
			json.NewEncoder(w).Encode([]any{})
			return
		}
		if r.URL.Path == "/v1/knowledge/recall-events" {
			json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "more": false})
			return
		}
		if r.URL.Path == "/v1/knowledge/recall-counts" {
			stats.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"started_at": "2026-11-14T02:00:00Z", "items": []any{}})
			return
		}
		if r.URL.Path == "/api/v1/datasets/ds/data" {
			json.NewEncoder(w).Encode([]Document{{ID: "a", Dataset: "ds", Created: "2026-11-16T23:00:00", Metadata: map[string]any{"node_set": []any{"session_learnings"}}}})
			return
		}
		if r.URL.Path == "/api/v1/sessions" {
			json.NewEncoder(w).Encode(map[string]any{"sessions": []any{}, "has_more": false})
			return
		}
		json.NewEncoder(w).Encode([]any{})
	}))
	defer func() { close(releaseActivity); server.Close() }()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600)
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.page = "总览"
	a.access.Loaded = true
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	var late func()
	select {
	case late = <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("directory callback not queued")
	}
	a.navigate("图谱", 0)
	late()
	time.Sleep(30 * time.Millisecond)
	if stats.Load() != 0 {
		t.Fatal("late overview callback started stats after leaving", stats.Load())
	}
}
