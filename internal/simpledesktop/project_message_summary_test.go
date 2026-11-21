package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if len(q.ProcessingNotices) != 1 || len(q.Documents) != 1 || q.Documents[0].ID != "d1" {
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
			atomic.AddInt32(&sawProcessingNotices, 1)
			json.NewEncoder(w).Encode(map[string]any{"items": []processingNotice{}})
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
}
