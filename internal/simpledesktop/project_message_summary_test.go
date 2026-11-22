package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadProjectQueueUsesMessageSummaryAndHandlesPartialErrors(t *testing.T) {
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte("synthetic-management-token-123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	var summaryCalls, legacyCalls int32
	partialErrors := map[string]string{}

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/p/message-summary" {
			atomic.AddInt32(&summaryCalls, 1)
			json.NewEncoder(w).Encode(projectMessageSummaryResponse{
				ProjectID:     "p",
				DatasetID:     "ds",
				AutomaticSave: true,
				Backlog:       &backlogJob{Operation: "op-1", Status: "watching", Automatic: true},
				ProcessingNotices: []processingNotice{
					{Operation: "proc-1", Status: 502, Code: "codex_upstream_error", Backend: "codex", At: time.Now().UTC().Format(time.RFC3339)},
				},
				ResolvedProcessingNotices: []processingNotice{
					{Operation: "resolved-1", Status: 502, Code: "codex_stream_disconnected", Backend: "codex",
						At: "2026-11-21T18:55:28Z", ResolvedAt: "2026-11-21T18:57:18Z"},
				},
				PendingDocuments: []projectMessageSummaryDocument{
					{ID: "d1", Title: "pending-doc.md", Date: "2026-11-21T08:00:00Z", Kind: "文档"},
				},
				Activity: []activityRow{},
				Errors:   partialErrors,
			})
			return
		}
		atomic.AddInt32(&legacyCalls, 1)
		http.NotFound(w, r)
	}))
	defer control.Close()

	cognee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&legacyCalls, 1)
		http.NotFound(w, r)
	}))
	defer cognee.Close()

	a := settingsFixture()
	a.access.Directory.Bindings = nil
	a.Config.ServiceURL = cognee.URL
	a.Client = Client{Config: a.Config}
	a.Config.ProjectControl = &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }

	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })

	if atomic.LoadInt32(&summaryCalls) != 1 || atomic.LoadInt32(&legacyCalls) != 0 {
		t.Fatalf("expected single summary call without legacy fallback, got summary=%d legacy=%d", summaryCalls, legacyCalls)
	}
	if !q.Loaded || !q.BacklogLoaded || !q.AutomaticSave || q.Backlog == nil || q.Backlog.Operation != "op-1" {
		t.Fatalf("summary state not applied: loaded=%v backlogLoaded=%v auto=%v backlog=%+v", q.Loaded, q.BacklogLoaded, q.AutomaticSave, q.Backlog)
	}
	if len(q.ProcessingNotices) != 2 || !q.ProcessingNotices[1].resolved() || len(q.Documents) != 1 || q.Documents[0].ID != "d1" {
		t.Fatalf("summary items not applied: notices=%+v docs=%+v", q.ProcessingNotices, q.Documents)
	}

	// Partial failure in documents & backlog must surface errors honestly.
	partialErrors = map[string]string{"documents": "upstream_unavailable", "backlog": "backlog_read_failed"}
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.DocumentError == "" || q.BacklogError == "" {
		t.Fatalf("partial errors were hidden: docErr=%q backlogErr=%q", q.DocumentError, q.BacklogError)
	}
}

func TestLoadProjectQueueFallsBackOn404MessageSummary(t *testing.T) {
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte("synthetic-management-token-123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	var sawSummary404, sawProcessingNotices, sawProcessingStatus int32

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/p/message-summary" {
			atomic.AddInt32(&sawSummary404, 1)
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/v1/projects/p/processing-notices" {
			calls := atomic.AddInt32(&sawProcessingNotices, 1)
			reply := map[string]any{"items": []processingNotice{{
				Operation: "proc-1", Status: 502, Code: "codex_upstream_error", Backend: "codex",
				At: "2026-11-21T18:55:28Z", RecoveredAt: "2026-11-21T18:57:18Z",
			}}}
			if calls > 1 {
				reply["resolved_items"] = []processingNotice{{
					Operation: "resolved-1", Status: 502, Code: "codex_stream_disconnected", Backend: "codex",
					At: "2026-11-21T18:55:28Z", ResolvedAt: "2026-11-21T18:57:18Z",
				}}
			}
			json.NewEncoder(w).Encode(reply)
			return
		}
		http.NotFound(w, r)
	}))
	defer control.Close()

	cognee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "processing-status"):
			atomic.AddInt32(&sawProcessingStatus, 1)
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "raw-1", "name": "fallback.md", "completed": false}}})
		case strings.Contains(r.URL.Path, "activity/pipeline-runs"):
			json.NewEncoder(w).Encode([]activityRow{})
		default:
			json.NewEncoder(w).Encode([]Document{{ID: "raw-1", Dataset: "ds", Name: "fallback.md"}})
		}
	}))
	defer cognee.Close()

	a := settingsFixture()
	a.access.Directory.Bindings = nil
	a.Config.ServiceURL = cognee.URL
	a.Client = Client{Config: a.Config}
	a.Config.ProjectControl = &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token}
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }

	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })

	if atomic.LoadInt32(&sawSummary404) != 1 || atomic.LoadInt32(&sawProcessingNotices) != 1 || atomic.LoadInt32(&sawProcessingStatus) != 1 {
		t.Fatalf("404 fallback did not run legacy endpoints: summary=%d notices=%d status=%d", sawSummary404, sawProcessingNotices, sawProcessingStatus)
	}
	if !q.Loaded || q.DocumentError != "" || len(q.Documents) != 1 || q.Documents[0].ID != "raw-1" {
		t.Fatalf("fallback did not load documents: loaded=%v err=%q docs=%+v", q.Loaded, q.DocumentError, q.Documents)
	}
	if len(q.ProcessingNotices) != 1 || q.ProcessingNotices[0].resolved() {
		t.Fatalf("legacy standalone payload must retain its unresolved notice: %+v", q.ProcessingNotices)
	}
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if len(q.ProcessingNotices) != 2 || !q.ProcessingNotices[1].resolved() {
		t.Fatalf("standalone payload did not load resolved diagnostics: %+v", q.ProcessingNotices)
	}
}

func TestLoadProjectQueuePreloadsReadStateAndSyncsKnowledgeCatalogDelta(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "manager.token")
	if err := os.WriteFile(token, []byte("synthetic-management-token-123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(dir, "client-state.json")

	// Pre-seed persisted knowledge-read file on disk (1 read, 1 unread).
	seed := settingsFixture()
	seed.Config.ClientStateFile = stateFile
	seed.Client.Config.ClientStateFile = stateFile
	p := seed.access.Directory.Projects[0]
	readPath, err := seed.knowledgeReadPath(p)
	if err != nil {
		t.Fatal(err)
	}
	enabledAt := time.Now().Add(-time.Hour).UTC()
	if err := writeKnowledgeState(readPath, knowledgeReadState{
		Version:     1,
		Initialized: true,
		StartedAt:   enabledAt,
		Read: map[string]bool{
			"k-old-read":   true,
			"k-old-unread": false,
		},
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var queryVersions []string
	var stage int32

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/p/message-summary" {
			http.NotFound(w, r)
			return
		}
		ver := r.URL.Query().Get("knowledge_version")
		mu.Lock()
		queryVersions = append(queryVersions, ver)
		mu.Unlock()
		cur := atomic.LoadInt32(&stage)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		switch cur {
		case 0:
			// First poll: returns 3 knowledge items (k-old-read, k-old-unread, k-new-1) with v1.
			json.NewEncoder(w).Encode(projectMessageSummaryResponse{
				ProjectID: "p",
				DatasetID: "ds",
				KnowledgeCatalog: []projectMessageSummaryKnowledge{
					{ID: "k-old-read", CreatedAt: enabledAt.Add(-time.Minute).Format(time.RFC3339Nano)},
					{ID: "k-old-unread", CreatedAt: enabledAt.Add(time.Minute).Format(time.RFC3339Nano)},
					{ID: "k-new-1", CreatedAt: now},
				},
				KnowledgeCatalogVersion: "v1-abc",
				Errors:                  map[string]string{},
			})
		case 1:
			// Second poll: server sees v1-abc and returns unchanged delta without catalog payload.
			json.NewEncoder(w).Encode(projectMessageSummaryResponse{
				ProjectID:                 "p",
				DatasetID:                 "ds",
				KnowledgeCatalog:          nil,
				KnowledgeCatalogVersion:   "v1-abc",
				KnowledgeCatalogUnchanged: ver == "v1-abc",
				Errors:                    map[string]string{},
			})
		default:
			// Third poll: k-old-unread removed, k-new-2 arrived under v2-def.
			json.NewEncoder(w).Encode(projectMessageSummaryResponse{
				ProjectID: "p",
				DatasetID: "ds",
				KnowledgeCatalog: []projectMessageSummaryKnowledge{
					{ID: "k-old-read", CreatedAt: enabledAt.Add(-time.Minute).Format(time.RFC3339Nano)},
					{ID: "k-new-1", CreatedAt: now},
					{ID: "k-new-2", CreatedAt: now},
				},
				KnowledgeCatalogVersion: "v2-def",
				Errors:                  map[string]string{},
			})
		}
	}))
	defer control.Close()

	a := settingsFixture()
	a.access.Directory.Bindings = nil
	a.Config.ClientStateFile = stateFile
	a.Client.Config.ClientStateFile = stateFile
	a.Config.ProjectControl = &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token}
	// Reset queues to simulate cold start after restart.
	a.access.Queues = nil
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }

	// 1. Cold-start preload restores persisted unread count before any network poll.
	q := a.projectQueueStateFor(p)
	if !q.KnowledgeRead.Initialized || q.unreadCount() != 1 {
		t.Fatalf("expected cold-start preload unread=1, got init=%v unread=%d", q.KnowledgeRead.Initialized, q.unreadCount())
	}

	// 2. First poll syncs catalog and reveals k-new-1 (total unread = 2).
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.KnowledgeReadError != "" || q.unreadCount() != 2 || q.KnowledgeCatalogVersion != "v1-abc" {
		t.Fatalf("first poll mismatch: err=%q unread=%d ver=%q", q.KnowledgeReadError, q.unreadCount(), q.KnowledgeCatalogVersion)
	}

	// 3. Second poll passes ?knowledge_version=v1-abc and preserves unread=2 on unchanged response.
	atomic.StoreInt32(&stage, 1)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.unreadCount() != 2 || q.KnowledgeCatalogVersion != "v1-abc" {
		t.Fatalf("unchanged delta poll broke unread state: unread=%d ver=%q", q.unreadCount(), q.KnowledgeCatalogVersion)
	}

	// 4. Third poll reflects deleted k-old-unread and added k-new-2 (still 2 unread: k-new-1, k-new-2).
	atomic.StoreInt32(&stage, 2)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.unreadCount() != 2 || q.KnowledgeCatalogVersion != "v2-def" || q.KnowledgeRead.Read["k-old-unread"] || !q.KnowledgeRead.Read["k-old-read"] {
		t.Fatalf("updated catalog poll mismatch: unread=%d ver=%q readMap=%+v", q.unreadCount(), q.KnowledgeCatalogVersion, q.KnowledgeRead.Read)
	}

	mu.Lock()
	defer mu.Unlock()
	wantVersions := []string{"", "v1-abc", "v1-abc"}
	if len(queryVersions) != len(wantVersions) {
		t.Fatalf("expected %d polls, got %v", len(wantVersions), queryVersions)
	}
	for i := range wantVersions {
		if queryVersions[i] != wantVersions[i] {
			t.Fatalf("poll %d sent knowledge_version=%q, want %q", i, queryVersions[i], wantVersions[i])
		}
	}
}
