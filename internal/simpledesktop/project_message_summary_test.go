package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		if r.URL.Path == "/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
			return
		}
		if r.URL.Path == "/v1/reading-state/migrate-local" {
			_ = json.NewEncoder(w).Encode(v2ReadingStateMigrateResponse{
				Migrated: []v2ReadingStateMigratedScope{{ProjectID: "p", DatasetID: "ds", Status: "no_legacy_file"}},
			})
			return
		}
		if r.URL.Path == "/v1/projects/p/message-summary" {
			atomic.AddInt32(&summaryCalls, 1)
			json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID:     "p",
				DatasetID:     "ds",
				AutomaticSave: true,
				Backlog:       &backlogJob{Operation: "op-1", Status: "watching", Automatic: true},
				Notices: []processingNotice{
					{Operation: "proc-1", Status: 502, Code: "codex_upstream_error", Backend: "codex", At: time.Now().UTC().Format(time.RFC3339)},
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

func TestReadingContractRejectsOldBackendAndRecoversAfterRestart(t *testing.T) {
	token := p1TestTokenFile(t)
	var restarted atomic.Bool
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			caps := ControlCapabilities{APIVersions: []string{"v1", "v2"}}
			if restarted.Load() {
				caps.APIVersions = []string{"v1"}
				caps.ReadingContract = readingContract
			}
			_ = json.NewEncoder(w).Encode(caps)
		case "/v1/overview/bootstrap":
			if r.Header.Get("X-Memlane-Reading-Contract") != readingContract {
				http.Error(w, "missing reading contract", 409)
				return
			}
			reads.Add(1)
			if !restarted.Load() {
				_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{Generation: 42})
		case "/v1/projects/p1/message-summary":
			if r.Header.Get("X-Memlane-Reading-Contract") != readingContract {
				http.Error(w, "missing reading contract", 409)
				return
			}
			reads.Add(1)
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{ProjectID: "p1", UnreadCount: 3})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := ProjectControlClient{Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}
	ctx := context.Background()
	if _, err := client.overviewBootstrap(ctx, "reader", 365, 20, 50, 50); !isControlErrorCode(err, 503, "reading_contract_mismatch") {
		t.Fatalf("old backend bootstrap: %v", err)
	}
	if _, err := client.projectMessageSummaryV2(ctx, "reader", "p1"); !isControlErrorCode(err, 503, "reading_contract_mismatch") {
		t.Fatalf("old backend summary: %v", err)
	}
	if reads.Load() != 0 {
		t.Fatalf("received %d incompatible requests", reads.Load())
	}

	restarted.Store(true)
	if summary, err := client.projectMessageSummaryV2(ctx, "reader", "p1"); err != nil || summary.UnreadCount != 3 {
		t.Fatalf("summary after restart: %+v, %v", summary, err)
	}
	if overview, err := client.overviewBootstrap(ctx, "reader", 365, 20, 50, 50); err != nil || overview.Generation != 42 {
		t.Fatalf("overview after restart: %+v, %v", overview, err)
	}

	// A cached successful probe does not make a legacy response compatible.
	restarted.Store(false)
	if _, err := client.overviewBootstrap(ctx, "reader", 365, 20, 50, 50); !isControlErrorCode(err, 503, "reading_contract_mismatch") {
		t.Fatalf("backend replacement after successful probe: %v", err)
	}
}
