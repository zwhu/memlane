package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func TestVerifyAuthorizedSessionIgnoresRemovedProjectResidualsAndStillBlocksActiveConflict(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()

	// Seed a capture receipt + inbox row on original project "p" (dataset "ds") for session "sess-reused".
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain","capture":true}`, 200)
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"sess-reused","message_id":"m1","kind":"capture_qa",
		"content":{"question":"old question","answer":"old answer"}
	}`, http.StatusAccepted)

	// Remove project "p" (leaves historical capture_receipts & cancelled capture_inbox for ds).
	manage(t, s, "DELETE", "/api/projects/p", "", http.StatusNoContent)

	// Recreate project with the same name and a brand-new dataset_id.
	created := decodeResponse[projectView](t, manage(t, s, "POST", "/api/projects", `{"id":"p-new","name":"Project","dataset_id":"ds-new"}`, http.StatusCreated))
	if created.DatasetID != "ds-new" {
		t.Fatalf("unexpected recreated dataset_id: %+v", created)
	}
	manage(t, s, "PUT", "/api/projects/p-new/acl/agent", `{"access":"maintain","capture":true}`, 200)

	// Record a new session binding for "sess-reused" on p-new and verify:
	// 1. verifyAuthorizedSession degrades to sessionOwnershipUnknown (NOT Conflict, NOT Authorized)
	//    so normal recall succeeds without leaking old dataset session context from cache.db.
	must(t, store.UpsertSessionBinding(ctx, sessionBindingKey("agent", "sess-reused"), "p-new", 200))
	fake.hits = []engine.SearchHit{{DatasetID: "ds-new", DocumentID: "doc-new", Title: "New"}}

	ownership, _ := s.verifyAuthorizedSession(ctx, domain.Agent{ID: "agent"}, "sess-reused", []engine.Scope{{DatasetID: "ds-new"}}, "common")
	if ownership != sessionOwnershipUnknown {
		t.Fatalf("recreated project reusing old session_id must degrade to sessionOwnershipUnknown (not Conflict or Authorized); got %v", ownership)
	}
	recallResp := agentRequest(t, s, "/recall", `{"project_id":"p-new","session_id":"sess-reused","query":"new","include_session_context":true}`, http.StatusOK)
	if recallResp["ok"] != true {
		t.Fatalf("ordinary recall after project recreate failed: %+v", recallResp)
	}
	if len(fake.search) == 0 || fake.search[len(fake.search)-1].Filters.SessionID != "" || fake.search[len(fake.search)-1].Filters.IncludeSessionContext {
		t.Fatalf("recreated project must not pass ambiguous SessionID/IncludeSessionContext to engine: %+v", fake.search)
	}

	// Reconnecting with the ORIGINAL dataset_id "ds" (on a clean session without a conflicting active binding)
	// restores sessionOwnershipAuthorized.
	_, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "ck-reconn", UserID: "agent", DatasetID: "ds", SessionID: "sess-reconn",
		MessageID: "m-reconn", PayloadSHA256: "sha-reconn", EntryType: "capture_qa",
	})
	must(t, err)
	reconnOwnership, _ := s.verifyAuthorizedSession(ctx, domain.Agent{ID: "agent"}, "sess-reconn", []engine.Scope{{DatasetID: "ds"}}, "common")
	if reconnOwnership != sessionOwnershipAuthorized {
		t.Fatalf("reconnected original dataset must restore sessionOwnershipAuthorized; got %v", reconnOwnership)
	}

	// Active unauthorized project's dataset_id AND code_dataset_id must both trigger sessionOwnershipConflict.
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p-other", Name: "Other", DatasetID: "ds-other", CodeDatasetID: "ds-other-code"}))
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "ck-other", UserID: "agent", DatasetID: "ds-other", SessionID: "sess-conflict",
		MessageID: "m-other", PayloadSHA256: "sha-other", EntryType: "capture_qa",
	})
	must(t, err)
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "ck-other-code", UserID: "agent", DatasetID: "ds-other-code", SessionID: "sess-code-conflict",
		MessageID: "m-code", PayloadSHA256: "sha-code", EntryType: "capture_qa",
	})
	must(t, err)

	conflictOwnership, _ := s.verifyAuthorizedSession(ctx, domain.Agent{ID: "agent"}, "sess-conflict", []engine.Scope{{DatasetID: "ds-new"}}, "common")
	if conflictOwnership != sessionOwnershipConflict {
		t.Fatalf("active unauthorized project session must still return sessionOwnershipConflict; got %v", conflictOwnership)
	}
	codeConflictOwnership, _ := s.verifyAuthorizedSession(ctx, domain.Agent{ID: "agent"}, "sess-code-conflict", []engine.Scope{{DatasetID: "ds-new"}}, "common")
	if codeConflictOwnership != sessionOwnershipConflict {
		t.Fatalf("active unauthorized project code_dataset_id session must return sessionOwnershipConflict; got %v", codeConflictOwnership)
	}
}

func TestCreateProjectNormalizesUUIDAndRejectsAlternateFormatDuplicate(t *testing.T) {
	s, _, _ := testServer(t, nil)
	created := decodeResponse[projectView](t, manage(t, s, "POST", "/api/projects",
		`{"id":"p-uuid","name":"UUID","dataset_id":"{11111111-2222-3333-4444-555566667777}","code_dataset_id":"urn:uuid:AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"}`,
		http.StatusCreated))
	if created.DatasetID != "11111111-2222-3333-4444-555566667777" || created.CodeDatasetID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("UUIDs were not normalized: dataset=%q code=%q", created.DatasetID, created.CodeDatasetID)
	}
	manage(t, s, "POST", "/api/projects",
		`{"id":"p-uuid-dup","name":"Dup","dataset_id":"11111111222233334444555566667777"}`,
		http.StatusConflict)
}

func TestRemovedCogneeImportedSessionTombstoneIsolation(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()

	// Seed an imported catalog_session on project "p" ("ds") with NO capture_receipts / capture_inbox rows.
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", UserID: "u1", SessionID: "sess-imported", Status: "active",
	}))
	// Remove project "p" -> must tombstone ("ds", "sess-imported") in removed_session_owners.
	manage(t, s, "DELETE", "/api/projects/p", "", http.StatusNoContent)

	// Recreate with a NEW dataset_id "ds-new" and bind "sess-imported" -> must degrade to sessionOwnershipUnknown.
	manage(t, s, "POST", "/api/projects", `{"id":"p-new","name":"Project","dataset_id":"ds-new"}`, http.StatusCreated)
	must(t, store.UpsertSessionBinding(ctx, sessionBindingKey("agent", "sess-imported"), "p-new", 100))
	ownership, _ := s.verifyAuthorizedSession(ctx, domain.Agent{ID: "agent"}, "sess-imported", []engine.Scope{{DatasetID: "ds-new"}}, "common")
	if ownership != sessionOwnershipUnknown {
		t.Fatalf("imported session reused in recreated project must degrade to sessionOwnershipUnknown, got %v", ownership)
	}

	// Reconnect original dataset "ds" -> tombstone cleared, and session in "ds" restores Authorized.
	manage(t, s, "DELETE", "/api/projects/p-new", "", http.StatusNoContent)
	manage(t, s, "POST", "/api/projects", `{"id":"p-reconn","name":"Project","dataset_id":"ds"}`, http.StatusCreated)
	must(t, store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: "ds", UserID: "u1", SessionID: "sess-imported", Status: "active",
	}))
	reconnOwnership, _ := s.verifyAuthorizedSession(ctx, domain.Agent{ID: "agent"}, "sess-imported", []engine.Scope{{DatasetID: "ds"}}, "common")
	if reconnOwnership != sessionOwnershipAuthorized {
		t.Fatalf("reconnected original dataset must restore sessionOwnershipAuthorized, got %v", reconnOwnership)
	}
}

func TestDeleteProjectDatasetIDGuardPreventsStaleRemoval(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()

	// Mismatched dataset_id query param returns 409 Conflict and preserves project "p".
	manage(t, s, "DELETE", "/api/projects/p?dataset_id=other-ds", "", http.StatusConflict)
	if _, err := store.GetProject(ctx, "p"); err != nil {
		t.Fatalf("project p should remain intact after 409 conflict: %v", err)
	}
	// Matching dataset_id succeeds with 204.
	manage(t, s, "DELETE", "/api/projects/p?dataset_id=ds", "", http.StatusNoContent)
	if _, err := store.GetProject(ctx, "p"); err == nil {
		t.Fatal("project p should be deleted after matching dataset_id")
	}
}

func TestConcurrentSessionEndAndDeleteProjectNoOrphanFlush(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain","capture":true}`, 200)

	// Seed a saved trace receipt so sessionNeedsConsolidation returns true.
	_, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "ck-trace", UserID: "agent", DatasetID: "ds", SessionID: "sess-race",
		MessageID: "m1", PayloadSHA256: "sha1", EntryType: "capture_trace",
	})
	must(t, err)
	must(t, store.MarkCaptureReceiptSaved(ctx, "ck-trace", "entry-1"))

	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		req := httptest.NewRequest(http.MethodPost, "/session-end", strings.NewReader(`{"project_id":"p","session_id":"sess-race"}`))
		req.Header.Set("Authorization", "Bearer "+testAgent)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted && rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("unexpected /session-end status %d: %s", rec.Code, rec.Body.String())
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		manage(t, s, "DELETE", "/api/projects/p", "", http.StatusNoContent)
	}()
	close(start)
	wg.Wait()

	// After DeleteProject completes, there must be ZERO pending session_flush_jobs for project "p".
	pendingFlushes, err := store.ListSessionFlushJobs(ctx, "p", "pending", 10)
	must(t, err)
	if len(pendingFlushes) != 0 {
		t.Fatalf("orphan pending flush jobs remained after DeleteProject: %+v", pendingFlushes)
	}
}

type reconnectProjector struct {
	calls atomic.Int32
}

func (p *reconnectProjector) SnapshotCatalog(_ context.Context, targets []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
	p.calls.Add(1)
	datasets := make([]engine.CatalogDatasetSnapshot, 0, len(targets))
	for _, target := range targets {
		if target.DatasetID == "ds-reconnected" {
			datasets = append(datasets, engine.CatalogDatasetSnapshot{
				DatasetID: target.DatasetID,
				Exists:    true,
				Docs: []engine.CatalogSnapshotDoc{{
					DatasetID:     target.DatasetID,
					DocID:         "restored-doc",
					Kind:          "knowledge",
					IsKnowledge:   true,
					DocName:       "Restored",
					EffectiveDate: "2026-11-27",
				}},
			})
		}
	}
	return &engine.CatalogSnapshot{Datasets: datasets}, nil
}

func (p *reconnectProjector) ReadLocalBody(_ context.Context, _ engine.LocalBodyRef) (string, error) {
	return "", engine.ErrNotFound
}

func TestProjectReconnectTriggersCatalogSync(t *testing.T) {
	proj := &reconnectProjector{}
	s, _, _ := testServer(t, func(c *Config) {
		c.Projector = proj
	})

	// Creating a brand-new project (empty dataset_id) does NOT need reconnect sync.
	manage(t, s, "POST", "/api/projects", `{"id":"fresh","name":"Fresh"}`, http.StatusCreated)
	if got := proj.calls.Load(); got != 0 {
		t.Fatalf("fresh project creation should not call SyncCatalog, got %d", got)
	}

	// Reconnecting an existing dataset_id triggers best-effort SyncCatalog and surfaces restored docs immediately.
	view := decodeResponse[projectView](t, manage(t, s, "POST", "/api/projects", `{"id":"reconn","name":"Reconnected","dataset_id":"ds-reconnected"}`, http.StatusCreated))
	if got := proj.calls.Load(); got != 1 {
		t.Fatalf("expected 1 SyncCatalog call on reconnect, got %d", got)
	}
	if view.KnowledgeCount != 1 {
		t.Fatalf("expected restored KnowledgeCount=1 immediately after reconnect, got %+v", view)
	}
}

func TestInboxProcessingPropagatedInProjectAndOverview(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()

	// Seed capture_inbox: 1 pending, 1 processing, 1 failed.
	for _, id := range []string{"in-1", "in-2", "in-3"} {
		_, err := store.ReserveAndEnqueueCapture(ctx, domain.EnqueueCaptureInput{
			Receipt: domain.CaptureReceipt{CaptureKey: "ck-" + id, UserID: "agent", DatasetID: "ds", SessionID: "s-" + id, MessageID: id, PayloadSHA256: "sha-" + id, EntryType: "capture_qa"},
			Inbox:   domain.CaptureInboxItem{ID: id, ProjectID: "p", DatasetID: "ds", AgentID: "agent", SessionID: "s-" + id, MessageID: id, CaptureKey: "ck-" + id, Kind: "capture_qa", PayloadJSON: `{}`},
		})
		must(t, err)
	}
	c1, err := store.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, store.MarkInboxFailed(ctx, c1.ID, "inbox err"))
	_, err = store.ClaimNextInboxItem(ctx) // stays processing
	must(t, err)

	// Also seed session_flush_jobs: 1 processing, 1 pending.
	_, _, err = store.EnqueueSessionFlushJob(ctx, "p", "ds", "sf-1")
	must(t, err)
	_, err = store.ClaimNextSessionFlushInDatasets(ctx, []string{"ds"}) // stays processing
	must(t, err)
	_, _, err = store.EnqueueSessionFlushJob(ctx, "p", "ds", "sf-2") // stays pending
	must(t, err)

	// Total expected: pending=2 (1 inbox + 1 flush), processing=2 (1 inbox + 1 flush), failed=1.
	view := decodeResponse[projectView](t, manage(t, s, "GET", "/api/projects/p", "", http.StatusOK))
	if view.InboxPending != 2 || view.InboxProcessing != 2 || view.InboxFailed != 1 || view.Inbox.Processing != 2 {
		t.Fatalf("GET /api/projects/p inbox counts mismatch: pending=%d processing=%d failed=%d inbox=%+v",
			view.InboxPending, view.InboxProcessing, view.InboxFailed, view.Inbox)
	}

	overview := decodeResponse[overviewSnapshot](t, manage(t, s, "GET", "/api/overview/bootstrap", "", http.StatusOK))
	if len(overview.Projects) != 1 {
		t.Fatalf("unexpected overview projects: %+v", overview.Projects)
	}
	op := overview.Projects[0]
	if op.InboxPending != 2 || op.InboxProcessing != 2 || op.InboxFailed != 1 {
		t.Fatalf("GET /api/overview/bootstrap project inbox counts mismatch: %+v", op)
	}
}
