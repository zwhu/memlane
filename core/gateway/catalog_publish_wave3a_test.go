package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/mcp"
)

type wave3aRecordingProjector struct {
	err       error
	calls     []wave3aSnapshotCall
	onCall    func(targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error)
}

type wave3aSnapshotCall struct {
	targets      []engine.CatalogDatasetTarget
	sessionsOnly bool
}

func (p *wave3aRecordingProjector) SnapshotCatalog(_ context.Context, targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
	p.calls = append(p.calls, wave3aSnapshotCall{targets: append([]engine.CatalogDatasetTarget(nil), targets...), sessionsOnly: opts.SessionsOnly})
	if p.onCall != nil {
		return p.onCall(targets, opts)
	}
	if p.err != nil {
		return nil, p.err
	}
	return &engine.CatalogSnapshot{}, nil
}

func (p *wave3aRecordingProjector) ReadLocalBody(context.Context, engine.LocalBodyRef) (string, error) {
	return "", engine.ErrNotFound
}

func TestCatalogPublishFailureRetriesWithZeroLLMCalls(t *testing.T) {
	proj := &wave3aRecordingProjector{err: errors.New("cognee sqlite temporarily busy")}
	s, store, fake := testServer(t, func(cfg *Config) {
		cfg.Projector = proj
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Enqueue a capture_qa item.
	resp := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"sess-pub-fail","message_id":"m-pub-1","kind":"capture_qa",
		"content":{"question":"why did publish fail?","answer":"sqlite busy"}
	}`, http.StatusAccepted)
	inboxID := resp["inbox_id"].(string)

	// ProcessOnce: Ingest + Extract succeed, catalog publish fails.
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected QA item to be processed")
	}
	if len(fake.ingest) != 1 || len(fake.extract) != 1 {
		t.Fatalf("expected 1 ingest + 1 extract, got ingest=%d extract=%d", len(fake.ingest), len(fake.extract))
	}

	// Inbox item must be marked succeeded (NOT failed) so LLM is never re-invoked.
	item, err := store.GetInboxItem(ctx, inboxID)
	must(t, err)
	if item.Status != "succeeded" {
		t.Fatalf("inbox item status = %q, want succeeded", item.Status)
	}

	// InboxSummary must expose independent catalog publish failure.
	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Failed != 0 || summary.CatalogPublishFailed != 1 || summary.CatalogPublishError == "" || summary.CatalogPublishErrorAt == "" {
		t.Fatalf("summary = %+v, want failed=0 catalog_publish_failed=1", summary)
	}
	globalSummary, err := s.Inbox().Summary(ctx, "")
	must(t, err)
	if globalSummary.CatalogPublishFailed != 1 {
		t.Fatalf("global summary = %+v, want catalog_publish_failed=1", globalSummary)
	}

	// Clear projector error and retry via POST /api/projects/p/retry -> ZERO LLM calls!
	proj.err = nil
	retryResp := decodeResponse[map[string]any](t, manage(t, s, "POST", "/api/projects/p/retry", "", http.StatusOK))
	if retryResp["retried_count"] != float64(1) {
		t.Fatalf("retryResp = %+v, want retried_count=1", retryResp)
	}
	if len(fake.ingest) != 1 || len(fake.extract) != 1 {
		t.Fatalf("catalog publish retry must NOT re-run Ingest or Extract: ingest=%d extract=%d", len(fake.ingest), len(fake.extract))
	}
	summaryAfter, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summaryAfter.CatalogPublishFailed != 0 || summaryAfter.CatalogPublishError != "" {
		t.Fatalf("summary after retry = %+v, want catalog_publish_failed=0", summaryAfter)
	}
}

func TestSyncDatasetCatalogAndSessionsPreventGhostCatalogResurrection(t *testing.T) {
	ctx := context.Background()
	var s *Server
	var store *domain.Store
	removeDuringSnapshot := false
	proj := &wave3aRecordingProjector{
		onCall: func(targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
			if removeDuringSnapshot {
				if err := store.DeleteProject(ctx, "p"); err != nil {
					return nil, err
				}
			}
			ds := targets[0].DatasetID
			return &engine.CatalogSnapshot{
				Datasets: []engine.CatalogDatasetSnapshot{{
					DatasetID: ds,
					Exists:    true,
					Docs: []engine.CatalogSnapshotDoc{{
						DatasetID: ds, DocID: "ghost-doc", Kind: "knowledge", IsKnowledge: true, DocLabel: "Ghost",
					}},
					Sessions: []engine.CatalogSnapshotSession{{
						DatasetID: ds, UserID: "u1", SessionID: "ghost-sess", Status: "active",
					}},
				}},
			}, nil
		},
	}
	s, store, _ = testServer(t, func(cfg *Config) {
		cfg.Projector = proj
	})

	// Seed existing catalog rows for project p ("ds").
	seedDoc(t, store, "ds", "existing-doc", "knowledge", "项目状态", time.Now(), -20261127)
	must(t, store.MarkCatalogPublishFailed(ctx, "ds", "p", "prior error"))

	// Case 1: Project removed mid-flight during SnapshotCatalog -> must clean up and NOT resurrect ghost rows.
	removeDuringSnapshot = true
	_, err := s.SyncDatasetCatalog(ctx, "ds")
	must(t, err)
	if n := countRows(t, store, "catalog_docs"); n != 0 {
		t.Fatalf("ghost catalog_docs resurrected: count=%d", n)
	}
	if n := countRows(t, store, "catalog_sessions"); n != 0 {
		t.Fatalf("ghost catalog_sessions resurrected: count=%d", n)
	}
	if n := countRows(t, store, "catalog_sync_state"); n != 0 {
		t.Fatalf("ghost catalog_sync_state resurrected: count=%d", n)
	}
	if n := countRows(t, store, "catalog_publish_state"); n != 0 {
		t.Fatalf("ghost catalog_publish_state left behind: count=%d", n)
	}

	// Case 2: SyncDatasetSessions on already-removed project skips SnapshotCatalog and leaves zero rows.
	removeDuringSnapshot = false
	proj.calls = nil
	_, err = s.SyncDatasetSessions(ctx, "ds")
	must(t, err)
	if len(proj.calls) != 0 {
		t.Fatalf("expected SyncDatasetSessions on removed project to skip SnapshotCatalog, got %d calls", len(proj.calls))
	}
}

func TestCaptureTraceSessionsOnlyDoesNotRewriteCatalogDocs(t *testing.T) {
	ctx := context.Background()
	proj := &wave3aRecordingProjector{
		onCall: func(targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
			return &engine.CatalogSnapshot{
				Datasets: []engine.CatalogDatasetSnapshot{{
					DatasetID: targets[0].DatasetID,
					Exists:    true,
					Sessions: []engine.CatalogSnapshotSession{{
						DatasetID: targets[0].DatasetID, UserID: "u1", SessionID: "s-trace", Status: "active",
					}},
				}},
			}, nil
		},
	}
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.Projector = proj
	})
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Pre-seed a knowledge doc in catalog_docs.
	seedDoc(t, store, "ds", "keep-me", "knowledge", "项目约定", time.Now(), -20261127)

	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-trace","message_id":"m-t1","kind":"capture_trace",
		"content":{"origin_function":"read_file"}
	}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected trace item to be processed")
	}
	if len(proj.calls) != 1 || !proj.calls[0].sessionsOnly || len(proj.calls[0].targets) != 1 || proj.calls[0].targets[0].DatasetID != "ds" {
		t.Fatalf("unexpected projector calls for capture_trace: %+v", proj.calls)
	}
	docs, err := store.ListCatalogDocs(ctx, "ds", "knowledge", 10)
	must(t, err)
	if len(docs) != 1 || docs[0].DocID != "keep-me" {
		t.Fatalf("capture_trace rewrote catalog_docs: %+v", docs)
	}
	sessions, err := store.ListCatalogSessions(ctx, "ds", 10)
	must(t, err)
	if len(sessions) != 1 || sessions[0].SessionID != "s-trace" {
		t.Fatalf("expected s-trace in catalog_sessions, got %+v", sessions)
	}
}

func TestMCPRememberRejectsRemovedProjectWithForbidden(t *testing.T) {
	ctx := context.Background()
	eng := &mcpFastPathEngine{fakeEngine: newFakeEngine()}
	srv, store, _, _ := setupMCPGateway(t, true, eng)
	proj, err := store.GetProject(ctx, "p")
	must(t, err)
	agent, err := store.GetAgent(ctx, "agent-a")
	must(t, err)

	// Remove the project before calling Remember.
	must(t, store.DeleteProject(ctx, "p"))

	svc := srv.MCPRememberService()
	_, err = svc.Remember(ctx, mcp.RememberRequest{
		Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
		Kind: engine.IngestKindDocument, Content: "late remember", IdempotencyKey: "late-key",
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Remember on removed project err = %v, want ErrForbidden", err)
	}
}

func TestGatewayEndToEndSessionRelationFoldingInRawRecords(t *testing.T) {
	ctx := context.Background()
	proj := &wave3aRecordingProjector{
		onCall: func(targets []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
			ds := targets[0].DatasetID
			return &engine.CatalogSnapshot{
				Datasets: []engine.CatalogDatasetSnapshot{{
					DatasetID: ds,
					Exists:    true,
					Sessions: []engine.CatalogSnapshotSession{
						{DatasetID: ds, UserID: "u1", SessionID: "parent-1", Status: "active", StartedAt: "2026-11-26T10:00:00Z", LastActivityAt: "2026-11-26T10:10:00Z", SortTSDescKey: -100},
						{DatasetID: ds, UserID: "u1", SessionID: "child-1", Status: "active", StartedAt: "2026-11-26T10:01:00Z", LastActivityAt: "2026-11-26T10:08:00Z", SortTSDescKey: -90},
						{DatasetID: ds, UserID: "u1", SessionID: "orphan-1", Status: "active", StartedAt: "2026-11-26T10:02:00Z", LastActivityAt: "2026-11-26T10:05:00Z", SortTSDescKey: -80},
					},
				}},
			}, nil
		},
	}
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.Projector = proj
	})
	must(t, store.UpsertSessionRelation(ctx, domain.SessionRelation{
		DatasetID: "ds", SessionID: "child-1", ParentSessionID: "parent-1", SubagentRole: "verifier",
	}))
	must(t, store.UpsertSessionRelation(ctx, domain.SessionRelation{
		DatasetID: "ds", SessionID: "orphan-1", ParentSessionID: "missing-parent", SubagentRole: "explorer",
	}))
	_, err := s.SyncDatasetCatalog(ctx, "ds")
	must(t, err)

	w := manage(t, s, "GET", "/api/projects/p/raw-records", "", http.StatusOK)
	var payload struct {
		Items []struct {
			SessionID    string `json:"session_id"`
			ChildCount   int    `json:"child_count"`
			IsSubagent   bool   `json:"is_subagent"`
			SubagentRole string `json:"subagent_role"`
		} `json:"items"`
	}
	must(t, json.Unmarshal(w.Body.Bytes(), &payload))
	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 top-level sessions (parent-1 + orphan-1), got %+v", payload.Items)
	}
	for _, it := range payload.Items {
		if it.SessionID == "child-1" {
			t.Fatalf("nested child-1 leaked into top-level raw-records: %+v", payload.Items)
		}
		if it.SessionID == "parent-1" && it.ChildCount != 1 {
			t.Fatalf("parent-1 child_count = %d, want 1", it.ChildCount)
		}
		if it.SessionID == "orphan-1" && (!it.IsSubagent || it.SubagentRole != "explorer") {
			t.Fatalf("orphan-1 = %+v", it)
		}
	}
}

func TestR101ConcurrentSameDatasetSnapshotAndPublishSerialized(t *testing.T) {
	ctx := context.Background()
	var active atomic.Int32
	var overlap atomic.Bool
	var callNum atomic.Int32
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})

	proj := &wave3aRecordingProjector{
		onCall: func(targets []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
			if active.Add(1) > 1 {
				overlap.Store(true)
			}
			defer active.Add(-1)
			n := callNum.Add(1)
			ds := targets[0].DatasetID
			if n == 1 {
				close(firstEntered)
				<-releaseFirst
				return &engine.CatalogSnapshot{
					Datasets: []engine.CatalogDatasetSnapshot{{
						DatasetID: ds, Exists: true,
						Docs: []engine.CatalogSnapshotDoc{{DatasetID: ds, DocID: "v1-doc", Kind: "knowledge", IsKnowledge: true, DocLabel: "V1"}},
					}},
				}, nil
			}
			return &engine.CatalogSnapshot{
				Datasets: []engine.CatalogDatasetSnapshot{{
					DatasetID: ds, Exists: true,
					Docs: []engine.CatalogSnapshotDoc{
						{DatasetID: ds, DocID: "v1-doc", Kind: "knowledge", IsKnowledge: true, DocLabel: "V1"},
						{DatasetID: ds, DocID: "v2-doc", Kind: "knowledge", IsKnowledge: true, DocLabel: "V2"},
					},
				}},
			}, nil
		},
	}
	s, store, _ := testServer(t, func(cfg *Config) { cfg.Projector = proj })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = s.SyncDatasetCatalog(ctx, "ds")
	}()
	<-firstEntered
	go func() {
		defer wg.Done()
		_, _ = s.SyncDatasetCatalog(ctx, "ds")
	}()
	time.Sleep(30 * time.Millisecond)
	close(releaseFirst)
	wg.Wait()

	if overlap.Load() {
		t.Fatal("same-dataset SnapshotCatalog + Publish overlapped concurrently")
	}
	docs, err := store.ListCatalogDocs(ctx, "ds", "knowledge", 10)
	must(t, err)
	if len(docs) != 2 {
		t.Fatalf("stale snapshot overwrote newer catalog: got %d docs (%+v), want 2", len(docs), docs)
	}
}

func TestR10304PendingCatalogPublishDrainsWithZeroLLMCallsAndShutdownCancel(t *testing.T) {
	ctx := context.Background()
	eng := &mcpFastPathEngine{fakeEngine: newFakeEngine()}
	var extractCalls atomic.Int32
	eng.extractFn = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
		extractCalls.Add(1)
		return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
	}
	var failSnapshot atomic.Bool
	failSnapshot.Store(true)
	eng.snapshotCatalogFn = func(_ context.Context, targets []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
		if failSnapshot.Load() {
			return nil, errors.New("temporary snapshot error")
		}
		ds := targets[0].DatasetID
		return &engine.CatalogSnapshot{
			Datasets: []engine.CatalogDatasetSnapshot{{
				DatasetID: ds, Exists: true,
				Docs: []engine.CatalogSnapshotDoc{{DatasetID: ds, DocID: "published-doc", Kind: "knowledge", IsKnowledge: true, DocLabel: "Published"}},
			}},
		}, nil
	}

	srv, store, _, _ := setupMCPGateway(t, true, eng)
	proj, _ := store.GetProject(ctx, "p")
	agent, _ := store.GetAgent(ctx, "agent-a")

	// 1. MCP fast-path Extract succeeds, SnapshotCatalog fails -> inbox=succeeded, catalog_publish_state=pending.
	res, err := srv.MCPRememberService().Remember(ctx, mcp.RememberRequest{
		Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
		Kind: engine.IngestKindDocument, Content: "hello", IdempotencyKey: "r1-03-key",
	})
	must(t, err)
	if res.Status != "queued" || res.Saved {
		t.Fatalf("unexpected result: %+v", res)
	}
	item, err := store.GetInboxItem(ctx, res.TaskID)
	must(t, err)
	if item.Status != "succeeded" {
		t.Fatalf("inbox status = %q, want succeeded", item.Status)
	}
	pubBefore, err := store.GetCatalogPublishState(ctx, "ds")
	must(t, err)
	if pubBefore.Status != "pending" {
		t.Fatalf("catalog_publish_state = %+v, want pending", pubBefore)
	}

	// 2. Background ProcessOnce drains pending catalog publish with ZERO additional Extract calls!
	failSnapshot.Store(false)
	worked, err := srv.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected ProcessOnce to drain pending catalog publish")
	}
	if extractCalls.Load() != 1 {
		t.Fatalf("pending catalog publish re-invoked Extract: count=%d, want 1", extractCalls.Load())
	}
	pubAfter, err := store.GetCatalogPublishState(ctx, "ds")
	must(t, err)
	if pubAfter.Status != "ok" {
		t.Fatalf("catalog_publish_state after drain = %+v, want ok", pubAfter)
	}

	// 3. Shutdown cancellation during catalog publish writes pending + completes inbox without re-running Extract.
	eng.snapshotCatalogFn = func(_ context.Context, _ []engine.CatalogDatasetTarget, _ engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
		return nil, context.Canceled
	}
	srv.Inbox().mu.Lock()
	srv.Inbox().run = &inboxRun{stopping: true, done: make(chan struct{})}
	srv.Inbox().mu.Unlock()
	defer func() {
		srv.Inbox().mu.Lock()
		srv.Inbox().run = nil
		srv.Inbox().mu.Unlock()
	}()
	enq := domain.CaptureInboxItem{
		ID: "shutdown-item", ProjectID: "p", DatasetID: "ds", AgentID: "agent-a",
		SessionID: "s-shut", MessageID: "m-shut", CaptureKey: "ck-shut", Kind: string(engine.IngestKindCaptureQA),
		PayloadJSON: `{"content":{"question":"q","answer":"a"},"extract_mode":"default"}`,
	}
	_, err = store.ReserveAndEnqueueCapture(ctx, domain.EnqueueCaptureInput{
		Receipt: domain.CaptureReceipt{
			CaptureKey: "ck-shut", UserID: "u", DatasetID: "ds", SessionID: "s-shut", MessageID: "m-shut",
			PayloadSHA256: "sha", EntryType: string(engine.IngestKindCaptureQA),
		},
		Inbox:  enq,
		Limits: domain.CaptureLimits{MaxPendingItems: 100, MaxPendingBytes: 1 << 20},
	})
	must(t, err)
	_, err = srv.Inbox().ProcessOnce(ctx)
	must(t, err)
	shutItem, err := store.GetInboxItem(ctx, "shutdown-item")
	must(t, err)
	if shutItem.Status != "succeeded" {
		t.Fatalf("shutdown-canceled publish must NOT requeue inbox item: status=%q", shutItem.Status)
	}
	shutPub, err := store.GetCatalogPublishState(ctx, "ds")
	must(t, err)
	if shutPub.Status != "pending" {
		t.Fatalf("shutdown-canceled publish should set pending, got %+v", shutPub)
	}
}

func TestR1SQLPrecedenceInInboxSummary(t *testing.T) {
	ctx := context.Background()
	s, store, _ := testServer(t, nil)
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p2", Name: "Project 2", DatasetID: "ds2", AutoCapture: true}))

	// p has status='ok' with stale error text manually injected (simulated), p2 has status='failed'.
	must(t, store.MarkCatalogPublishOK(ctx, "ds", "p"))
	must(t, store.MarkCatalogPublishFailed(ctx, "ds2", "p2", "p2 real failure"))
	// Inject a non-empty last_error on an 'ok' row to prove SQL precedence filters by status='failed' AND (project_id OR dataset_id).
	_, err := store.DB().ExecContext(ctx, `UPDATE catalog_publish_state SET last_error='stale-ok-err' WHERE dataset_id='ds'`)
	must(t, err)

	sumP, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if sumP.CatalogPublishFailed != 0 || sumP.CatalogPublishError != "" {
		t.Fatalf("summary(p) leaked non-failed row: %+v", sumP)
	}
	sumP2, err := s.Inbox().Summary(ctx, "p2")
	must(t, err)
	if sumP2.CatalogPublishFailed != 1 || sumP2.CatalogPublishError != "p2 real failure" {
		t.Fatalf("summary(p2) = %+v, want failed=1 error='p2 real failure'", sumP2)
	}
}

type discoverableProjector struct {
	wave3aRecordingProjector
	datasets []engine.DiscoveredDataset
}

func (d *discoverableProjector) DiscoverDatasets(context.Context) ([]engine.DiscoveredDataset, error) {
	return d.datasets, nil
}

func TestR1LinkableDatasetsEndpointAfterProjectRemoval(t *testing.T) {
	ctx := context.Background()
	dsActive := "11111111-1111-4111-8111-111111111111"
	dsRemoved := "22222222-2222-4222-8222-222222222222"
	dsProtected := "33333333-3333-4333-8333-333333333333"
	proj := &discoverableProjector{
		datasets: []engine.DiscoveredDataset{
			{ID: dsActive, Name: "Active"},
			{ID: dsRemoved, Name: "OldProject"},
			{ID: dsProtected, Name: "ml-user-profiles", Protected: true},
		},
	}
	s, store, _ := testServer(t, func(cfg *Config) { cfg.Projector = proj })
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p-active", Name: "Active", DatasetID: dsActive}))
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p-old", Name: "OldProject", DatasetID: dsRemoved}))

	// Before removal: neither occupied dataset nor protected dataset is linkable.
	for _, route := range []string{"/api/linkable-datasets", "/v1/linkable-datasets"} {
		items := decodeResponse[[]linkableDatasetView](t, manage(t, s, "GET", route, "", http.StatusOK))
		if len(items) != 0 {
			t.Fatalf("GET %s before removal = %+v, want []", route, items)
		}
	}

	// After removing p-old: dsRemoved becomes linkable!
	must(t, store.DeleteProject(ctx, "p-old"))
	for _, route := range []string{"/api/linkable-datasets", "/v1/linkable-datasets"} {
		items := decodeResponse[[]linkableDatasetView](t, manage(t, s, "GET", route, "", http.StatusOK))
		if len(items) != 1 || items[0].ID != dsRemoved || items[0].Name != "OldProject" {
			t.Fatalf("GET %s after removal = %+v, want [%s]", route, items, dsRemoved)
		}
	}
}

func TestR1MCPRememberRejectsDowngradedACLInLock(t *testing.T) {
	ctx := context.Background()
	eng := &mcpFastPathEngine{fakeEngine: newFakeEngine()}
	srv, store, _, _ := setupMCPGateway(t, true, eng)
	proj, err := store.GetProject(ctx, "p")
	must(t, err)
	agent, err := store.GetAgent(ctx, "agent-a")
	must(t, err)

	// Downgrade agent-a from maintain to read.
	read := domain.AccessRead
	must(t, store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "p", AgentID: "agent-a", Access: &read}))

	_, err = srv.MCPRememberService().Remember(ctx, mcp.RememberRequest{
		Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
		Kind: engine.IngestKindDocument, Content: "denied after downgrade", IdempotencyKey: "downgraded-key",
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Remember after ACL downgrade err = %v, want ErrForbidden", err)
	}
}

func TestR204PendingCatalogPublishNotBlockedByUnverifiedModelAndRetryable(t *testing.T) {
	ctx := context.Background()
	proj := &wave3aRecordingProjector{}
	s, store, _ := testServer(t, func(cfg *Config) { cfg.Projector = proj })

	// Force model unverified (startup error) so LLM extraction is paused.
	_, err := store.DB().ExecContext(ctx, `UPDATE generation_state SET startup_error='model_unverified' WHERE singleton=1`)
	must(t, err)
	must(t, store.MarkCatalogPublishPending(ctx, "ds", "p"))

	// ProcessOnce must STILL drain pending catalog publish despite unverified model!
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(proj.calls) != 1 {
		t.Fatalf("pending catalog publish was blocked by model gate: worked=%v calls=%d", worked, len(proj.calls))
	}
	st, err := store.GetCatalogPublishState(ctx, "ds")
	must(t, err)
	if st.Status != "ok" {
		t.Fatalf("catalog_publish_state = %+v, want ok", st)
	}

	// RetryFailed also picks up status='pending' catalog publish items.
	must(t, store.MarkCatalogPublishPending(ctx, "ds", "p"))
	retried, err := s.Inbox().RetryFailed(ctx, "p")
	must(t, err)
	if len(retried) != 1 || retried[0] != "catalog:ds" {
		t.Fatalf("RetryFailed did not retry pending catalog publish: %v", retried)
	}
}
