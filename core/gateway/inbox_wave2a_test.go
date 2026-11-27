package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func TestIngestPromptPeekAckAndNoFakeInboxID(t *testing.T) {
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.InboxMaxPendingItems = 1
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Fill the single allowed inbox slot.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-cap","message_id":"m-1","kind":"capture_trace",
		"content":{"origin_function":"Read"}
	}`, http.StatusAccepted)

	// Record a pending prompt via /recall, then send /ingest with empty question while inbox is full (507).
	agentRequest(t, s, "/recall", `{
		"project_id":"p","session_id":"s-peek","turn_id":"turn-peek","query":"remember peek question"
	}`, http.StatusOK)
	fullResp := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-peek","turn_id":"turn-peek","message_id":"m-peek","kind":"capture_qa",
		"content":{"question":"","answer":"answer after capacity error"}
	}`, http.StatusInsufficientStorage)
	if fullResp["error"] != "inbox_capacity_exceeded" {
		t.Fatalf("expected inbox_capacity_exceeded, got %+v", fullResp)
	}
	// Capacity failure must leave zero orphan receipts.
	if n := countRows(t, store, "capture_receipts"); n != 1 {
		t.Fatalf("capture_receipts = %d, want 1 (no orphan receipt on 507)", n)
	}

	// Drain the occupying item, then retry the same /ingest: prompt must still be pending and paired!
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected first item to drain")
	}
	retried := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-peek","turn_id":"turn-peek","message_id":"m-peek","kind":"capture_qa",
		"content":{"question":"","answer":"answer after capacity error"}
	}`, http.StatusAccepted)
	if retried["accepted"] != true || retried["status"] != "pending" {
		t.Fatalf("retry after 507 failed: %+v", retried)
	}
	item, err := store.GetInboxItem(ctx, retried["inbox_id"].(string))
	must(t, err)
	if !strings.Contains(item.PayloadJSON, "remember peek question") {
		t.Fatalf("pending prompt was lost on failed ingest retry: %s", item.PayloadJSON)
	}

	// Legacy receipt with no inbox row: duplicate /ingest must NOT fabricate a fake inbox_id.
	var payload inboxPayload
	payload.Content = json.RawMessage(`{"answer":"a","question":"q"}`)
	payload.SourceMeta.SessionID = "s-legacy"
	payload.ExtractMode = engine.ExtractModeDefault
	canonical, err := json.Marshal(payload)
	must(t, err)
	payloadHash := digest(canonical)
	captureKey := tupleDigest("agent", "ds", "agent", "s-legacy", "m-legacy", string(engine.IngestKindCaptureQA))
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: captureKey, UserID: "agent", DatasetID: "ds", SessionID: "s-legacy",
		MessageID: "m-legacy", PayloadSHA256: payloadHash, EntryType: string(engine.IngestKindCaptureQA),
	})
	must(t, err)
	must(t, store.MarkCaptureReceiptSaved(ctx, captureKey, "legacy-entry-1"))

	legacyDup := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-legacy","message_id":"m-legacy","kind":"capture_qa",
		"content":{"question":"q","answer":"a"}
	}`, http.StatusOK)
	if legacyDup["deduplicated"] != true || legacyDup["status"] != "saved" {
		t.Fatalf("legacy duplicate = %+v", legacyDup)
	}
	if _, hasFakeID := legacyDup["inbox_id"]; hasFakeID {
		t.Fatalf("legacy duplicate returned non-existent inbox_id: %+v", legacyDup)
	}
}

func TestInboxRestartRecoveryAndSessionFlushFailureRetry(t *testing.T) {
	s, store, fake := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 1
		cfg.InboxPollInterval = 20 * time.Millisecond
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// 1. Seed a stale 'processing' inbox item whose receipt is already 'saved' (crash between Ingest and Extract).
	enq := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-recover","message_id":"m-rec","kind":"capture_qa",
		"content":{"question":"recover me","answer":"recovered"}
	}`, http.StatusAccepted)
	inboxID := enq["inbox_id"].(string)
	captureKey := enq["capture_key"].(string)
	_, err := store.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, store.MarkCaptureReceiptSaved(ctx, captureKey, "already-ingested-entry"))

	// 2. Also seed a stale 'processing' session_flush_job for another session.
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "ck-flush-rec", UserID: "agent", DatasetID: "ds", SessionID: "s-flush-rec",
		MessageID: "m-f1", PayloadSHA256: "sha-f1", EntryType: "capture_trace",
	})
	must(t, err)
	must(t, store.MarkCaptureReceiptSaved(ctx, "ck-flush-rec", "entry-f1"))
	flushJob, queued, err := store.EnqueueSessionFlushJob(ctx, "p", "ds", "s-flush-rec")
	must(t, err)
	if !queued {
		t.Fatal("expected flush job queued")
	}
	_, err = store.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	must(t, err)

	// Start recovers both stale processing rows; s-recover skips duplicate Ingest and runs Extract; s-flush-rec flushes.
	s.Inbox().Start(ctx)
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stopCtx))

	if len(fake.ingest) != 0 {
		t.Fatalf("recovered item with saved receipt should skip Ingest, got %d ingests", len(fake.ingest))
	}
	if len(fake.extract) != 2 {
		t.Fatalf("expected 2 extracts (1 recovered QA + 1 recovered flush), got %d", len(fake.extract))
	}
	gotInbox, err := store.GetInboxItem(ctx, inboxID)
	must(t, err)
	if gotInbox.Status != "succeeded" {
		t.Fatalf("recovered inbox item status = %q", gotInbox.Status)
	}
	gotFlush, err := store.GetSessionFlushJob(ctx, "ds", "s-flush-rec")
	must(t, err)
	if gotFlush.Status != "succeeded" || gotFlush.ID != flushJob.ID {
		t.Fatalf("recovered flush job = %+v", gotFlush)
	}

	// 3. Session flush failure surfaces in InboxSummary and retries cleanly via POST /api/projects/{id}/retry.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-fail-flush","message_id":"m-ff","kind":"capture_trace",
		"content":{"origin_function":"Bash"}
	}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected trace to process")
	}
	endResp := agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"s-fail-flush"}`, http.StatusAccepted)
	if endResp["queued"] != true || endResp["flush_status"] != "queued" {
		t.Fatalf("session-end = %+v", endResp)
	}
	fake.mu.Lock()
	fake.extractErr = errors.New("llm summary parse failure")
	fake.mu.Unlock()
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected flush job to run and fail")
	}

	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Failed != 1 || len(summary.RecentFailures) != 1 || !strings.HasPrefix(summary.RecentFailures[0].ID, "flush:") {
		t.Fatalf("summary did not expose failed session flush: %+v", summary)
	}

	fake.mu.Lock()
	fake.extractErr = nil
	fake.mu.Unlock()
	retryResp := decodeResponse[map[string]any](t, manage(t, s, "POST", "/api/projects/p/retry", "", http.StatusOK))
	if retryResp["retried_count"] != float64(1) {
		t.Fatalf("retryResp = %+v, want retried_count=1", retryResp)
	}
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected retried flush job to succeed")
	}
	summaryAfter, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summaryAfter.Failed != 0 || summaryAfter.Pending != 0 {
		t.Fatalf("summary after retry = %+v", summaryAfter)
	}
}

type parallelBarrierEngine struct {
	*fakeEngine
	mu              sync.Mutex
	activeByDataset map[string]int
	overlapSameDS   bool
	dsAStarted      chan struct{}
	dsBStarted      chan struct{}
	releaseAll      chan struct{}
	parallelSeen    atomic.Bool
}

func (e *parallelBarrierEngine) Ingest(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	e.mu.Lock()
	if e.activeByDataset == nil {
		e.activeByDataset = make(map[string]int)
	}
	e.activeByDataset[req.Scope.DatasetID]++
	if e.activeByDataset[req.Scope.DatasetID] > 1 {
		e.overlapSameDS = true
	}
	if len(e.activeByDataset) >= 2 && e.activeByDataset["ds"] > 0 && e.activeByDataset["ds-b"] > 0 {
		e.parallelSeen.Store(true)
	}
	e.mu.Unlock()

	switch req.Scope.DatasetID {
	case "ds":
		select {
		case <-e.dsAStarted:
		default:
			close(e.dsAStarted)
		}
	case "ds-b":
		select {
		case <-e.dsBStarted:
		default:
			close(e.dsBStarted)
		}
	}

	select {
	case <-e.releaseAll:
	case <-ctx.Done():
	}

	e.mu.Lock()
	e.activeByDataset[req.Scope.DatasetID]--
	e.mu.Unlock()
	return e.fakeEngine.Ingest(ctx, req)
}

func TestInboxCrossProjectParallelSameProjectSerial(t *testing.T) {
	var barrier *parallelBarrierEngine
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 2
		cfg.InboxPollInterval = 15 * time.Millisecond
		barrier = &parallelBarrierEngine{
			fakeEngine: cfg.Engine.(*fakeEngine),
			dsAStarted: make(chan struct{}),
			dsBStarted: make(chan struct{}),
			releaseAll: make(chan struct{}),
		}
		cfg.Engine = barrier
	})
	ctx := context.Background()
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p-b", Name: "Project B", DatasetID: "ds-b", AutoCapture: true}))
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	manage(t, s, "PUT", "/api/projects/p-b/acl/agent", `{"access":"maintain"}`, 200)

	// Two items in project p (ds) and one item in project p-b (ds-b).
	for _, req := range []string{
		`{"project_id":"p","session_id":"s-a","message_id":"a1","kind":"capture_trace","content":{"origin_function":"Read"}}`,
		`{"project_id":"p","session_id":"s-a","message_id":"a2","kind":"capture_trace","content":{"origin_function":"Edit"}}`,
		`{"project_id":"p-b","session_id":"s-b","message_id":"b1","kind":"capture_trace","content":{"origin_function":"Bash"}}`,
	} {
		agentRequest(t, s, "/ingest", req, http.StatusAccepted)
	}

	s.Inbox().Start(ctx)
	select {
	case <-barrier.dsAStarted:
	case <-time.After(2 * time.Second):
		close(barrier.releaseAll)
		t.Fatal("timed out waiting for ds-a to start")
	}
	select {
	case <-barrier.dsBStarted:
	case <-time.After(2 * time.Second):
		close(barrier.releaseAll)
		t.Fatal("ds-b was blocked by ds-a; expected cross-project parallelism")
	}
	close(barrier.releaseAll)

	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stopCtx))

	if !barrier.parallelSeen.Load() {
		t.Fatal("did not observe ds and ds-b executing in parallel")
	}
	barrier.mu.Lock()
	overlap := barrier.overlapSameDS
	barrier.mu.Unlock()
	if overlap {
		t.Fatal("observed concurrent execution on the same dataset_id")
	}
	summary, err := s.Inbox().Summary(ctx, "")
	must(t, err)
	if summary.Succeeded != 3 || summary.Pending != 0 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestInboxEngineUnavailablePauseBackoffAndUserIDPassthrough(t *testing.T) {
	s, store, fake := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 1
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// Enqueue an item with explicit EngineUserID (simulating trusted Wave 2B MCP remember fallback).
	encoded, err := json.Marshal(inboxPayload{
		Content:      json.RawMessage(`{"question":"mcp q","answer":"mcp a"}`),
		SourceMeta:   engine.SourceMeta{SessionID: "s-mcp"},
		EngineUserID: "mcp-user-42",
		ExtractMode:  engine.ExtractModeDefault,
	})
	must(t, err)
	_, err = store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID: "mcp-item-1", ProjectID: "p", DatasetID: "ds", AgentID: "agent",
		SessionID: "s-mcp", MessageID: "m-mcp-1", Kind: string(engine.IngestKindCaptureQA),
		PayloadJSON: string(encoded),
	})
	must(t, err)

	// Engine returns 503 generation_not_configured -> must pause and requeue to pending (NOT failed).
	fake.mu.Lock()
	fake.ingestErr = &engine.EngineError{
		StatusCode: http.StatusServiceUnavailable,
		Code:       "generation_not_configured",
		Message:    "generation model not configured",
		Err:        engine.ErrUnavailable,
	}
	fake.mu.Unlock()

	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked {
		t.Fatal("expected 503 engine pause to return worked=false")
	}
	item, err := store.GetInboxItem(ctx, "mcp-item-1")
	must(t, err)
	if item.Status != "pending" {
		t.Fatalf("503 engine unavailable must keep item pending, got %+v", item)
	}
	if !s.Inbox().inExtractBackoff() {
		t.Fatal("expected backoff to be active after 503")
	}

	// Recover engine and clear backoff -> item succeeds and passes UserID to BOTH Ingest and Extract.
	fake.mu.Lock()
	fake.ingestErr = nil
	fake.mu.Unlock()
	s.Inbox().resetExtractBackoff()

	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected item to process after engine recovery")
	}
	if len(fake.ingest) != 2 || len(fake.extract) != 1 {
		t.Fatalf("expected 2 ingest calls (1 paused 503 + 1 recovered) and 1 extract, got %d / %d", len(fake.ingest), len(fake.extract))
	}
	if fake.ingest[1].Scope.UserID != "mcp-user-42" || fake.extract[0].Scope.UserID != "mcp-user-42" {
		t.Fatalf("UserID not passed through: ingest=%+v extract=%+v", fake.ingest[1].Scope, fake.extract[0].Scope)
	}

	// Prune succeeded payload trims payload_json while keeping record.
	pruned, err := s.Inbox().PruneSucceededPayloads(ctx, 0)
	must(t, err)
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	prunedItem, err := store.GetInboxItem(ctx, "mcp-item-1")
	must(t, err)
	if prunedItem.PayloadJSON != "{}" || prunedItem.Status != "succeeded" {
		t.Fatalf("pruned item = %+v", prunedItem)
	}
	_ = fmt.Sprint(slices.Clone([]string{prunedItem.ID}))
}

func TestV4PayloadCompatAndHookEngineUserIDTamperResistance(t *testing.T) {
	s, store, fake := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 1
	})
	ctx := context.Background()
	agent, err := store.GetAgent(ctx, "agent")
	must(t, err)
	agent.PrincipalID = "principal-v4-1"
	must(t, store.UpsertAgent(ctx, agent))
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// 1. Real v4 payload JSON (contains user_id=principal-v4-1, no engine_user_id).
	v4Canonical := `{"content":{"answer":"a","question":"q"},"source_meta":{"session_id":"s-v4"},"user_id":"principal-v4-1","extract_mode":"default"}`
	v4Hash := digest([]byte(v4Canonical))
	v4Key := tupleDigest("agent", "ds", "principal-v4-1", "s-v4", "m-v4", string(engine.IngestKindCaptureQA))
	_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: v4Key, UserID: "principal-v4-1", DatasetID: "ds", SessionID: "s-v4",
		MessageID: "m-v4", PayloadSHA256: v4Hash, EntryType: string(engine.IngestKindCaptureQA),
	})
	must(t, err)
	v4ItemID := tupleDigest("agent", "ds", v4Key)
	_, err = store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID: v4ItemID, ProjectID: "p", DatasetID: "ds", AgentID: "agent",
		SessionID: "s-v4", MessageID: "m-v4", CaptureKey: v4Key,
		Kind: string(engine.IngestKindCaptureQA), PayloadJSON: v4Canonical,
	})
	must(t, err)

	// Resending the same v4 request must deduplicate cleanly (200, NOT 409 capture_conflict).
	dup := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-v4","message_id":"m-v4","kind":"capture_qa",
		"content":{"question":"q","answer":"a"}
	}`, http.StatusOK)
	if dup["deduplicated"] != true || dup["inbox_id"] != v4ItemID {
		t.Fatalf("v4 resend = %+v, want deduplicated with %s", dup, v4ItemID)
	}
	// Worker processing v4 row must still execute under empty owner scope (UserID == "").
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.ingest) != 1 || fake.ingest[0].Scope.UserID != "" || fake.extract[0].Scope.UserID != "" {
		t.Fatalf("v4 row did not use owner scope: worked=%v ingest=%+v extract=%+v", worked, fake.ingest, fake.extract)
	}

	// 2. Hook /ingest attempting to spoof user_id and engine_user_id must NOT change engine Scope.UserID.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-spoof","message_id":"m-spoof","kind":"capture_qa",
		"user_id":"spoofed-user","engine_user_id":"spoofed-engine-user",
		"content":{"question":"spoof","answer":"blocked"}
	}`, http.StatusAccepted)
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.ingest) != 2 || fake.ingest[1].Scope.UserID != "" || fake.extract[1].Scope.UserID != "" {
		t.Fatalf("hook request spoofed engine Scope.UserID: ingest=%+v extract=%+v", fake.ingest[1].Scope, fake.extract[1].Scope)
	}
}

func TestSessionFlushFairnessAndInFlightRerun(t *testing.T) {
	var eng *hybridRecallTestEngine
	s, store, fake := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 1
		cfg.InboxPollInterval = 15 * time.Millisecond
		eng = &hybridRecallTestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	// 1. Fairness: Session A has a ready session_flush_job while Session B has 5 pending traces in the same project.
	_, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "ck-fair-a", UserID: "agent", DatasetID: "ds", SessionID: "sess-a",
		MessageID: "ma1", PayloadSHA256: "sha-a1", EntryType: "capture_trace",
	})
	must(t, err)
	must(t, store.MarkCaptureReceiptSaved(ctx, "ck-fair-a", "entry-a1"))
	if !s.Inbox().ScheduleSessionFlush("ds", "sess-a") {
		t.Fatal("failed to schedule sess-a flush")
	}
	for i := range 5 {
		agentRequest(t, s, "/ingest", fmt.Sprintf(`{
			"project_id":"p","session_id":"sess-b","message_id":"mb-%d","kind":"capture_trace",
			"content":{"origin_function":"Read"}
		}`, i), http.StatusAccepted)
	}

	// Within 2 ProcessOnce calls, sess-a flush MUST run without waiting for all 5 sess-b traces to drain.
	for range 2 {
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked {
			t.Fatal("expected work to be processed")
		}
	}
	jobA, err := store.GetSessionFlushJob(ctx, "ds", "sess-a")
	must(t, err)
	if jobA.Status != "succeeded" {
		t.Fatalf("sess-a flush was starved by sess-b traces: %+v", jobA)
	}
	// Drain remaining sess-b traces cleanly.
	for {
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked {
			break
		}
	}

	// 2. In-flight flush + new trace + second /session-end must trigger an automatic second flush after the first completes.
	firstFlushStarted := make(chan struct{})
	releaseFirstFlush := make(chan struct{})
	var flushCalls atomic.Int32
	eng.mu.Lock()
	eng.extractHook = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
		call := flushCalls.Add(1)
		if call == 1 {
			close(firstFlushStarted)
			<-releaseFirstFlush
		}
		return engine.ExtractResult{OK: true}, nil
	}
	eng.mu.Unlock()

	agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"sess-b"}`, http.StatusAccepted)
	s.Inbox().Start(ctx)
	select {
	case <-firstFlushStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first flush to start")
	}

	// While first flush is processing, add a new trace and send a second /session-end.
	agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"sess-b","message_id":"mb-late","kind":"capture_trace",
		"content":{"origin_function":"Edit"}
	}`, http.StatusAccepted)
	agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"sess-b"}`, http.StatusAccepted)

	close(releaseFirstFlush)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if flushCalls.Load() >= 2 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stopCtx))

	if flushCalls.Load() != 2 {
		t.Fatalf("expected 2 flush extracts (initial + rerun), got %d", flushCalls.Load())
	}
	jobB, err := store.GetSessionFlushJob(ctx, "ds", "sess-b")
	must(t, err)
	if jobB.Status != "succeeded" || jobB.RerunRequested {
		t.Fatalf("unexpected final sess-b flush job: %+v", jobB)
	}
	_ = fake
}

func TestInboxAutoPruneSucceededPayloads(t *testing.T) {
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 1
		cfg.InboxPruneInterval = 1
		cfg.InboxPruneRetention = time.Nanosecond
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	enq := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-prune","message_id":"m-prune","kind":"capture_qa",
		"content":{"question":"prune me","answer":"automatically"}
	}`, http.StatusAccepted)
	id := enq["inbox_id"].(string)

	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected item to process")
	}
	// Auto-prune ran at completion; item metadata & receipt remain intact while payload_json is trimmed.
	item, err := store.GetInboxItem(ctx, id)
	must(t, err)
	if item.Status != "succeeded" || item.PayloadJSON != "{}" {
		t.Fatalf("expected auto-pruned succeeded item, got %+v", item)
	}
	// Resending the same /ingest must still deduplicate cleanly via capture_receipts.
	dup := agentRequest(t, s, "/ingest", `{
		"project_id":"p","session_id":"s-prune","message_id":"m-prune","kind":"capture_qa",
		"content":{"question":"prune me","answer":"automatically"}
	}`, http.StatusOK)
	if dup["deduplicated"] != true || dup["inbox_id"] != id {
		t.Fatalf("duplicate after auto-prune = %+v", dup)
	}
}

func TestPerDatasetPreferFlushAndFailedRerunRecovery(t *testing.T) {
	var eng *hybridRecallTestEngine
	s, store, _ := testServer(t, func(cfg *Config) {
		cfg.InboxWorkers = 2
		cfg.InboxPollInterval = 15 * time.Millisecond
		eng = &hybridRecallTestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
	})
	ctx := context.Background()
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p-b", Name: "Project B", DatasetID: "ds-b", AutoCapture: true}))
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)
	manage(t, s, "PUT", "/api/projects/p-b/acl/agent", `{"access":"maintain"}`, 200)

	// Seed ready flush in ds (sess-a) AND ready flush in ds-b (sess-b-flush), plus 3 traces in ds (sess-a-other).
	for _, seed := range []struct {
		key, ds, sess, proj string
	}{
		{"ck-ds-a", "ds", "sess-a", "p"},
		{"ck-ds-b", "ds-b", "sess-b-flush", "p-b"},
	} {
		_, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
			CaptureKey: seed.key, UserID: "agent", DatasetID: seed.ds, SessionID: seed.sess,
			MessageID: "m1", PayloadSHA256: "sha1", EntryType: "capture_trace",
		})
		must(t, err)
		must(t, store.MarkCaptureReceiptSaved(ctx, seed.key, "e1"))
		if !s.Inbox().ScheduleSessionFlush(seed.ds, seed.sess) {
			t.Fatalf("failed to schedule flush for %s", seed.sess)
		}
	}
	for i := range 3 {
		agentRequest(t, s, "/ingest", fmt.Sprintf(`{
			"project_id":"p","session_id":"sess-a-other","message_id":"mo-%d","kind":"capture_trace",
			"content":{"origin_function":"Read"}
		}`, i), http.StatusAccepted)
	}

	// Step 1: claims a trace in ds -> sets preferFlush["ds"] = true.
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected trace in ds")
	}
	// Simulate ds-b clearing its own preference without affecting ds.
	s.Inbox().claimMu.Lock()
	s.Inbox().preferFlush["ds-b"] = true
	s.Inbox().claimMu.Unlock()

	// Next 2 calls: both ds and ds-b flushes MUST run before remaining traces in ds finish.
	for range 2 {
		worked, err = s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked {
			t.Fatal("expected flush to run")
		}
	}
	jobA, err := store.GetSessionFlushJob(ctx, "ds", "sess-a")
	must(t, err)
	jobB, err := store.GetSessionFlushJob(ctx, "ds-b", "sess-b-flush")
	must(t, err)
	if jobA.Status != "succeeded" || jobB.Status != "succeeded" {
		t.Fatalf("per-dataset preferFlush did not run both flushes: jobA=%+v jobB=%+v", jobA, jobB)
	}

	// Drain remaining traces.
	for {
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked {
			break
		}
	}

	// Failed flush with rerun_requested=1 must automatically re-run and succeed on the second attempt.
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var attempts atomic.Int32
	eng.mu.Lock()
	eng.extractHook = func(_ context.Context, _ engine.ExtractRequest) (engine.ExtractResult, error) {
		if attempts.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return engine.ExtractResult{}, errors.New("transient first flush failure")
		}
		return engine.ExtractResult{OK: true}, nil
	}
	eng.mu.Unlock()

	agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"sess-a-other"}`, http.StatusAccepted)
	s.Inbox().Start(ctx)
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first flush")
	}
	// Second /session-end arrives while first flush is in-flight -> sets rerun_requested=1.
	agentRequest(t, s, "/session-end", `{"project_id":"p","session_id":"sess-a-other"}`, http.StatusAccepted)
	close(releaseFirst)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 2 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	must(t, s.Inbox().Stop(stopCtx))

	if attempts.Load() != 2 {
		t.Fatalf("expected failed flush with rerun_requested=1 to re-run, got attempts=%d", attempts.Load())
	}
	finalJob, err := store.GetSessionFlushJob(ctx, "ds", "sess-a-other")
	must(t, err)
	if finalJob.Status != "succeeded" || finalJob.RerunRequested {
		t.Fatalf("unexpected final job after failed-then-rerun: %+v", finalJob)
	}
}

func TestInboxHTTP500AndOfficialExtractFailedMarkFailedInsteadOfInfiniteRetry(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, 200)

	enqueueQA := func(msgID string) string {
		res := agentRequest(t, s, "/ingest", fmt.Sprintf(`{
			"project_id":"p","session_id":"s-500","message_id":%q,"kind":"capture_qa",
			"content":{"question":"q-%s","answer":"a-%s"}
		}`, msgID, msgID, msgID), http.StatusAccepted)
		return res["inbox_id"].(string)
	}

	// 1. HTTP 500 from engine extract must mark item failed immediately instead of requeuing forever,
	// and classify as engine_internal_error rather than "提炼服务暂时无法连接".
	id500 := enqueueQA("m-500")
	fake.mu.Lock()
	fake.extractErr = &engine.EngineError{
		StatusCode: http.StatusInternalServerError,
		Code:       "engine_internal_error",
		Message:    "engine runner internal error (fd_limit_exceeded)",
		Err:        engine.ErrInternal,
	}
	fake.mu.Unlock()
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected HTTP 500 extract error to finalize attempt (worked=true)")
	}
	item500, err := store.GetInboxItem(ctx, id500)
	must(t, err)
	if item500.Status != "failed" || item500.Attempts != 1 {
		t.Fatalf("HTTP 500 must mark failed on attempt 1, got %+v", item500)
	}
	summary500, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if len(summary500.RecentFailures) != 1 ||
		summary500.RecentFailures[0].ErrorCode != "engine_internal_error" ||
		!strings.Contains(summary500.RecentFailures[0].Summary, "文件句柄不足") ||
		strings.Contains(summary500.RecentFailures[0].Summary, "暂时无法连接") {
		t.Fatalf("HTTP 500 failure summary misclassified: %+v", summary500.RecentFailures)
	}

	// 2. HTTP 503 official_extract_failed must also mark item failed (not pause/requeue forever)
	// and classify as engine_internal_error rather than unavailable.
	id503Extract := enqueueQA("m-503-extract")
	fake.mu.Lock()
	fake.extractErr = &engine.EngineError{
		StatusCode: http.StatusServiceUnavailable,
		Code:       "official_extract_failed",
		Message:    "engine runner rejected the request",
		Err:        engine.ErrUnavailable,
	}
	fake.mu.Unlock()
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected 503 official_extract_failed to finalize attempt")
	}
	item503, err := store.GetInboxItem(ctx, id503Extract)
	must(t, err)
	if item503.Status != "failed" || item503.Attempts != 1 {
		t.Fatalf("503 official_extract_failed must mark failed, got %+v", item503)
	}
	summary503, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	found503 := false
	for _, f := range summary503.RecentFailures {
		if f.ID == id503Extract {
			found503 = true
			if f.ErrorCode != "engine_internal_error" || strings.Contains(f.Summary, "暂时无法连接") {
				t.Fatalf("503 official_extract_failed misclassified: %+v", f)
			}
		}
	}
	if !found503 {
		t.Fatalf("503 official_extract_failed missing from RecentFailures: %+v", summary503.RecentFailures)
	}

	// 3. Transport-level unavailable (StatusCode 0) must still pause and keep item pending.
	idDown := enqueueQA("m-down")
	fake.mu.Lock()
	fake.extractErr = &engine.EngineError{
		StatusCode: 0,
		Code:       "unavailable",
		Message:    "contact engine runner",
		Err:        engine.ErrUnavailable,
	}
	fake.mu.Unlock()
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked {
		t.Fatal("expected transport-level unavailable to pause (worked=false)")
	}
	itemDown, err := store.GetInboxItem(ctx, idDown)
	must(t, err)
	if itemDown.Status != "pending" {
		t.Fatalf("transport-level unavailable must keep item pending, got %+v", itemDown)
	}
}
