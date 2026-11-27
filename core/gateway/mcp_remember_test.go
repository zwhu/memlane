package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/mcp"
)

type mcpFastPathEngine struct {
	*fakeEngine
	ingestFn      func(context.Context, engine.IngestRequest) (engine.IngestResult, error)
	extractFn     func(context.Context, engine.ExtractRequest) (engine.ExtractResult, error)
	searchFn      func(context.Context, engine.SearchRequest) (engine.SearchResult, error)
	syncCatalogFn func(context.Context, *domain.Store) (*engine.SyncStats, error)
}

func (e *mcpFastPathEngine) Ingest(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	if e.ingestFn != nil {
		return e.ingestFn(ctx, req)
	}
	return e.fakeEngine.Ingest(ctx, req)
}

func (e *mcpFastPathEngine) Extract(ctx context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
	if e.extractFn != nil {
		return e.extractFn(ctx, req)
	}
	return e.fakeEngine.Extract(ctx, req)
}

func (e *mcpFastPathEngine) Search(ctx context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	if e.searchFn != nil {
		return e.searchFn(ctx, req)
	}
	return e.fakeEngine.Search(ctx, req)
}

func (e *mcpFastPathEngine) SyncCatalog(ctx context.Context, store *domain.Store) (*engine.SyncStats, error) {
	if e.syncCatalogFn != nil {
		return e.syncCatalogFn(ctx, store)
	}
	return &engine.SyncStats{}, nil
}

func setupMCPGateway(t *testing.T, activateModel bool, eng *mcpFastPathEngine) (*Server, *domain.Store, http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := domain.OpenStore(filepath.Join(dir, "memlane.sqlite3"))
	must(t, err)
	t.Cleanup(func() { _ = store.Close() })

	must(t, store.UpsertProject(ctx, domain.Project{
		ID: "p", Name: "Project", DatasetID: "ds", Aliases: []string{dir}, AutoCapture: true,
	}))
	keyFile := filepath.Join(dir, "agent-a.key")
	const agentToken = "mcp-agent-a-secret-token"
	must(t, os.WriteFile(keyFile, []byte(agentToken+"\n"), 0o600))
	must(t, store.UpsertAgent(ctx, domain.Agent{
		ID:            "agent-a",
		Name:          "Agent A",
		PrincipalID:   "11111111-1111-4111-8111-111111111111",
		KeyFile:       keyFile,
		DefaultAccess: domain.AccessNone,
	}))
	maintain := domain.AccessMaintain
	must(t, store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "p", AgentID: "agent-a", Access: &maintain}))

	bridgeFile := filepath.Join(dir, "bridge.token")
	must(t, os.WriteFile(bridgeFile, []byte("mcp-test-bridge-token-private-0123456789abcdef"), 0o600))
	if activateModel {
		prof, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
			ProfileID: "p1", Mode: "agent_bridge", BridgeKind: "codex", Model: "test-model",
		})
		must(t, err)
		must(t, store.SetActiveGeneration(ctx, prof.ProfileID, prof.Revision))
	}

	mcpServer, err := mcp.NewServer(mcp.Config{Store: store, Engine: eng})
	must(t, err)
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: "gateway-test-management-token-24+",
		BridgeEndpoint:  "http://127.0.0.1:18888/v1",
		BridgeTokenFile: bridgeFile,
		MCPHandler:      mcpServer.Handler(),
	})
	must(t, err)
	mcpServer.SetRememberService(srv.MCPRememberService())
	return srv, store, srv.Handler(), agentToken
}

func callMCPTool(t *testing.T, h http.Handler, token, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	must(t, err)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set("X-Api-Key", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &env))
	if env.Error != nil {
		t.Fatalf("RPC error: %+v", env.Error)
	}
	if env.Result.IsError {
		return nil, env.Result.Content[0].Text
	}
	return env.Result.StructuredContent, ""
}

func TestMCPRememberFastPathSyncAndRecallable(t *testing.T) {
	ctx := context.Background()
	var ingested []engine.IngestRequest
	var extracted []engine.ExtractRequest
	syncCalls := 0
	readyDocs := map[string]string{}

	eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
	eng.ingestFn = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		ingested = append(ingested, req)
		return engine.IngestResult{OK: true, EntryID: "doc-fast-1", DocumentID: "doc-fast-1"}, nil
	}
	eng.extractFn = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
		extracted = append(extracted, req)
		for _, id := range req.DocIDs {
			readyDocs[id] = "fast path memory"
		}
		return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
	}
	eng.searchFn = func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
		hits := make([]engine.SearchHit, 0, len(readyDocs))
		for id, text := range readyDocs {
			hits = append(hits, engine.SearchHit{DatasetID: "ds", DocumentID: id, Title: "Memory", Snippet: text})
		}
		return engine.SearchResult{OK: true, Hits: hits}, nil
	}
	eng.syncCatalogFn = func(_ context.Context, _ *domain.Store) (*engine.SyncStats, error) {
		syncCalls++
		return &engine.SyncStats{DocsProjected: 1}, nil
	}

	srv, store, h, token := setupMCPGateway(t, true, eng)
	wantDigest, err := srv.expectedActiveModelDigest(ctx)
	must(t, err)

	res, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
		"project_id": "p",
		"content":    "fast path memory",
	})
	if errMsg != "" {
		t.Fatalf("remember failed: %s", errMsg)
	}
	if res["status"] != "saved" || res["saved"] != true || res["entry_id"] != "doc-fast-1" ||
		res["document_id"] != "doc-fast-1" || res["deduplicated"] != false || res["task_id"] == "" {
		t.Fatalf("unexpected fast path result: %+v", res)
	}
	if len(ingested) != 1 || ingested[0].Scope.UserID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("ingest did not preserve user_id: %+v", ingested)
	}
	if len(extracted) != 1 || extracted[0].ExpectedModelDigest != wantDigest ||
		extracted[0].Scope.UserID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("extract mismatch: got %+v want digest %q", extracted, wantDigest)
	}
	if syncCalls != 1 {
		t.Fatalf("expected SyncCatalog called once, got %d", syncCalls)
	}

	// Immediately recallable.
	recallRes, errMsg := callMCPTool(t, h, token, "recall", map[string]any{"project_id": "p", "query": "fast path"})
	if errMsg != "" {
		t.Fatalf("recall failed: %s", errMsg)
	}
	hits, _ := recallRes["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("expected 1 recall hit immediately after fast path, got %+v", recallRes)
	}

	// Task status lookup reflects succeeded/saved.
	taskID, _ := res["task_id"].(string)
	statusRes, errMsg := callMCPTool(t, h, token, "remember_status", map[string]any{"task_id": taskID})
	if errMsg != "" {
		t.Fatalf("remember_status failed: %s", errMsg)
	}
	if statusRes["status"] != "succeeded" || statusRes["saved"] != true || statusRes["document_id"] != "doc-fast-1" {
		t.Fatalf("unexpected remember_status: %+v", statusRes)
	}

	// Deterministic retry without idempotency_key deduplicates without calling engine again.
	replay, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
		"project_id": "p",
		"content":    "fast path memory",
	})
	if errMsg != "" || replay["deduplicated"] != true || replay["status"] != "saved" || replay["task_id"] != taskID {
		t.Fatalf("unexpected replay result: %+v (%q)", replay, errMsg)
	}
	if len(ingested) != 1 || len(extracted) != 1 {
		t.Fatalf("replay should not re-invoke engine: ingested=%d extracted=%d", len(ingested), len(extracted))
	}
	inboxItem, err := store.GetInboxItem(ctx, taskID)
	must(t, err)
	if inboxItem.Status != "succeeded" {
		t.Fatalf("inbox item status = %q, want succeeded", inboxItem.Status)
	}
}

func TestMCPRememberFallbackQueueWhenModelUnverifiedPreservesUserID(t *testing.T) {
	ctx := context.Background()
	ingestCalls := 0
	eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
	eng.ingestFn = func(_ context.Context, _ engine.IngestRequest) (engine.IngestResult, error) {
		ingestCalls++
		return engine.IngestResult{OK: true, DocumentID: "should-not-run"}, nil
	}

	_, store, h, token := setupMCPGateway(t, false, eng)

	res, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
		"project_id": "p",
		"content":    "queued when model unverified",
	})
	if errMsg != "" {
		t.Fatalf("remember failed: %s", errMsg)
	}
	if res["status"] != "queued" || res["saved"] != false || res["message"] != mcpQueuedMessage || res["task_id"] == "" {
		t.Fatalf("unexpected queued response: %+v", res)
	}
	if ingestCalls != 0 {
		t.Fatalf("engine ingest should not run when model is unverified, got %d", ingestCalls)
	}

	taskID := res["task_id"].(string)
	item, err := store.GetInboxItem(ctx, taskID)
	must(t, err)
	if item.Status != "pending" {
		t.Fatalf("inbox status = %q, want pending", item.Status)
	}
	var payload map[string]any
	must(t, json.Unmarshal([]byte(item.PayloadJSON), &payload))
	if payload["user_id"] != "11111111-1111-4111-8111-111111111111" ||
		payload["engine_user_id"] != "11111111-1111-4111-8111-111111111111" ||
		payload["extract_mode"] != "default" ||
		payload["idempotency_key"] != res["idempotency_key"] {
		t.Fatalf("payload_json contract mismatch: %+v", payload)
	}
	if _, hasContent := payload["content"]; !hasContent {
		t.Fatalf("payload_json missing content: %+v", payload)
	}
	if _, hasSourceMeta := payload["source_meta"]; !hasSourceMeta {
		t.Fatalf("payload_json missing source_meta: %+v", payload)
	}

	// Duplicate retry while queued returns deduplicated queued result without double enqueue.
	replay, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
		"project_id": "p",
		"content":    "queued when model unverified",
	})
	if errMsg != "" || replay["status"] != "queued" || replay["deduplicated"] != true || replay["task_id"] != taskID {
		t.Fatalf("unexpected queued replay: %+v (%q)", replay, errMsg)
	}

	statusRes, errMsg := callMCPTool(t, h, token, "remember_status", map[string]any{"task_id": taskID})
	if errMsg != "" || statusRes["status"] != "pending" || statusRes["saved"] != false {
		t.Fatalf("unexpected queued remember_status: %+v (%q)", statusRes, errMsg)
	}
}

func TestMCPRememberFallbackOnEngineFailureAndResumeWithoutDoubleIngest(t *testing.T) {
	ctx := context.Background()
	ingestCalls := 0
	extractCalls := 0
	failExtract := true

	eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
	eng.ingestFn = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		ingestCalls++
		if req.IdempotencyKey == "bad-4xx" {
			return engine.IngestResult{}, engine.ErrInvalidArgument
		}
		if req.IdempotencyKey == "engine-down" {
			return engine.IngestResult{}, engine.ErrUnavailable
		}
		return engine.IngestResult{OK: true, EntryID: "entry-resume-1", DocumentID: "entry-resume-1"}, nil
	}
	eng.extractFn = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
		extractCalls++
		if failExtract {
			return engine.ExtractResult{}, context.DeadlineExceeded
		}
		return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
	}

	srv, store, h, token := setupMCPGateway(t, true, eng)

	// 1. Engine unavailable during Ingest -> queued in inbox.
	queuedRes, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
		"project_id":      "p",
		"content":         "engine down note",
		"idempotency_key": "engine-down",
	})
	if errMsg != "" || queuedRes["status"] != "queued" || queuedRes["saved"] != false {
		t.Fatalf("expected queued on engine unavailable, got %+v (%q)", queuedRes, errMsg)
	}
	// Mark this first item succeeded so ProcessOnce picks up the second item below.
	downTaskID := queuedRes["task_id"].(string)
	_, err := store.DB().ExecContext(ctx, `UPDATE capture_inbox SET status='succeeded' WHERE id=?`, downTaskID)
	must(t, err)

	// 2. 4xx validation rejection -> NOT queued, receipt cleaned up.
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "remember", "arguments": map[string]any{
			"project_id": "p", "content": "bad", "idempotency_key": "bad-4xx",
		}},
	})
	must(t, err)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set("X-Api-Key", token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if _, err := store.GetCaptureReceipt(ctx, "bad-4xx"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected 4xx rejected receipt aborted, got %v", err)
	}

	// 3. Ingest succeeds, Extract times out -> receipt saved, inbox pending; worker resumes Extract without re-ingesting.
	ingestCalls = 0
	extractCalls = 0
	res, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
		"project_id":      "p",
		"content":         "resume extract note",
		"idempotency_key": "resume-extract",
	})
	if errMsg != "" || res["status"] != "queued" || res["saved"] != false || res["entry_id"] != "entry-resume-1" {
		t.Fatalf("expected queued after extract timeout, got %+v (%q)", res, errMsg)
	}
	if ingestCalls != 1 || extractCalls != 1 {
		t.Fatalf("expected 1 ingest and 1 failed extract, got %d/%d", ingestCalls, extractCalls)
	}
	failExtract = false
	worked, err := srv.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked {
		t.Fatal("expected inbox worker to process queued item")
	}
	if ingestCalls != 1 || extractCalls != 2 {
		t.Fatalf("worker should skip duplicate Ingest and resume Extract: ingest=%d extract=%d", ingestCalls, extractCalls)
	}
	taskID := res["task_id"].(string)
	statusRes, errMsg := callMCPTool(t, h, token, "remember_status", map[string]any{"task_id": taskID})
	if errMsg != "" || statusRes["status"] != "succeeded" || statusRes["saved"] != true || statusRes["entry_id"] != "entry-resume-1" {
		t.Fatalf("expected succeeded after worker resume, got %+v (%q)", statusRes, errMsg)
	}
}

func TestMCPRememberStatusACLIsolation(t *testing.T) {
	ctx := context.Background()
	eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
	_, store, h, tokenA := setupMCPGateway(t, false, eng)

	// Register Agent B with maintain access to the same project.
	dir := t.TempDir()
	keyB := filepath.Join(dir, "agent-b.key")
	const tokenB = "mcp-agent-b-secret-token"
	must(t, os.WriteFile(keyB, []byte(tokenB+"\n"), 0o600))
	must(t, store.UpsertAgent(ctx, domain.Agent{
		ID:            "agent-b",
		Name:          "Agent B",
		KeyFile:       keyB,
		DefaultAccess: domain.AccessMaintain,
	}))

	res, errMsg := callMCPTool(t, h, tokenA, "remember", map[string]any{
		"project_id": "p",
		"content":    "private to agent A",
	})
	if errMsg != "" {
		t.Fatalf("agent A remember failed: %s", errMsg)
	}
	taskID := res["task_id"].(string)

	// Agent B cannot query Agent A's task.
	if _, msg := callMCPTool(t, h, tokenB, "remember_status", map[string]any{"task_id": taskID}); msg != "Access denied" {
		t.Fatalf("agent B must be denied reading agent A task, got %q", msg)
	}
	// Agent A with read-only access can still query its own task.
	read := domain.AccessRead
	must(t, store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "p", AgentID: "agent-a", Access: &read}))
	if statusRes, msg := callMCPTool(t, h, tokenA, "remember_status", map[string]any{"task_id": taskID}); msg != "" || statusRes["task_id"] != taskID {
		t.Fatalf("agent A with read access should succeed: %+v (%q)", statusRes, msg)
	}
	// Agent A with revoked access (none) is denied.
	none := domain.AccessNone
	must(t, store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: "p", AgentID: "agent-a", Access: &none}))
	if _, msg := callMCPTool(t, h, tokenA, "remember_status", map[string]any{"task_id": taskID}); msg != "Access denied" {
		t.Fatalf("agent A with revoked access must be denied, got %q", msg)
	}
}

func TestMCPRememberR1Fixes(t *testing.T) {
	ctx := context.Background()

	t.Run("R1-01_finalize_independent_context_after_slow_extract", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		eng.ingestFn = func(_ context.Context, _ engine.IngestRequest) (engine.IngestResult, error) {
			return engine.IngestResult{OK: true, EntryID: "doc-slow-1", DocumentID: "doc-slow-1"}, nil
		}
		eng.extractFn = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
			time.Sleep(70 * time.Millisecond)
			return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
		}
		srv, store, _, _ := setupMCPGateway(t, true, eng)
		svc := &mcpRememberService{server: srv, fastPathTimeout: time.Second, finalizeTimeout: 40 * time.Millisecond}
		proj, err := store.GetProject(ctx, "p")
		must(t, err)
		agent, err := store.GetAgent(ctx, "agent-a")
		must(t, err)
		res, err := svc.Remember(ctx, mcp.RememberRequest{
			Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
			Kind: engine.IngestKindDocument, Content: "slow extract ok", IdempotencyKey: "r1-01-key",
		})
		must(t, err)
		if !res.Saved || res.Status != "saved" {
			t.Fatalf("expected saved despite extract > finalizeTimeout: %+v", res)
		}
		item, err := store.GetInboxItem(ctx, res.TaskID)
		must(t, err)
		if item.Status != "succeeded" {
			t.Fatalf("inbox status = %q, want succeeded", item.Status)
		}
	})

	t.Run("R1-03_legacy_saved_receipt_compat_and_task_backfill", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		_, store, h, token := setupMCPGateway(t, true, eng)
		legacyReq := engine.IngestRequest{
			Scope:          engine.Scope{DatasetID: "ds", UserID: "11111111-1111-4111-8111-111111111111"},
			Kind:           engine.IngestKindDocument,
			IdempotencyKey: "legacy-key-1",
			Content:        "legacy saved body",
			SourceMeta:     engine.SourceMeta{},
		}
		legacyRaw, err := json.Marshal(legacyReq)
		must(t, err)
		_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
			CaptureKey: "legacy-key-1", UserID: "11111111-1111-4111-8111-111111111111",
			DatasetID: "ds", SessionID: "legacy-key-1", MessageID: "legacy-key-1",
			PayloadSHA256: digest(legacyRaw), EntryType: string(engine.IngestKindDocument),
		})
		must(t, err)
		must(t, store.MarkCaptureReceiptSaved(ctx, "legacy-key-1", "legacy-doc-9"))

		res, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
			"project_id": "p", "content": "legacy saved body", "idempotency_key": "legacy-key-1",
		})
		if errMsg != "" || res["status"] != "saved" || res["saved"] != true || res["deduplicated"] != true || res["entry_id"] != "legacy-doc-9" {
			t.Fatalf("legacy saved replay failed: %+v (%q)", res, errMsg)
		}
		taskID := fmt.Sprint(res["task_id"])
		statusRes, errMsg := callMCPTool(t, h, token, "remember_status", map[string]any{"task_id": taskID})
		if errMsg != "" || statusRes["status"] != "succeeded" || statusRes["saved"] != true || statusRes["entry_id"] != "legacy-doc-9" {
			t.Fatalf("legacy backfilled task_id not queryable: %+v (%q)", statusRes, errMsg)
		}
	})

	t.Run("R1-05_shared_principal_agent_isolation", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		_, store, h, tokenA := setupMCPGateway(t, false, eng)
		dir := t.TempDir()
		keyC := filepath.Join(dir, "agent-c.key")
		const tokenC = "mcp-agent-c-secret-token"
		must(t, os.WriteFile(keyC, []byte(tokenC+"\n"), 0o600))
		must(t, store.UpsertAgent(ctx, domain.Agent{
			ID: "agent-c", Name: "Agent C", PrincipalID: "11111111-1111-4111-8111-111111111111",
			KeyFile: keyC, DefaultAccess: domain.AccessMaintain,
		}))

		// Agent A enqueues a task with explicit key, then mark it failed.
		resA, errMsg := callMCPTool(t, h, tokenA, "remember", map[string]any{
			"project_id": "p", "content": "shared key note", "idempotency_key": "shared-explicit-key",
		})
		if errMsg != "" {
			t.Fatalf("agent A remember failed: %s", errMsg)
		}
		taskA := fmt.Sprint(resA["task_id"])
		_, err := store.DB().ExecContext(ctx, `UPDATE capture_inbox SET status='failed', last_error='boom' WHERE id=?`, taskA)
		must(t, err)

		// Agent C (same principal_id) with the same explicit key must get conflict and NOT reset Agent A's task.
		if _, msg := callMCPTool(t, h, tokenC, "remember", map[string]any{
			"project_id": "p", "content": "shared key note", "idempotency_key": "shared-explicit-key",
		}); !strings.Contains(msg, "conflicts") {
			t.Fatalf("expected conflict for cross-agent explicit key, got %q", msg)
		}
		itemA, err := store.GetInboxItem(ctx, taskA)
		must(t, err)
		if itemA.Status != "failed" {
			t.Fatalf("agent C mutated agent A's inbox item status to %q", itemA.Status)
		}

		// Without explicit key, auto key includes agent.ID so Agent A and Agent C do not collide.
		autoA, msgA := callMCPTool(t, h, tokenA, "remember", map[string]any{"project_id": "p", "content": "same text auto"})
		autoC, msgC := callMCPTool(t, h, tokenC, "remember", map[string]any{"project_id": "p", "content": "same text auto"})
		if msgA != "" || msgC != "" || autoA["idempotency_key"] == autoC["idempotency_key"] || autoA["task_id"] == autoC["task_id"] {
			t.Fatalf("auto keys should isolate agents sharing principal_id: A=%+v (%q) C=%+v (%q)", autoA, msgA, autoC, msgC)
		}
	})

	t.Run("R1-06_fast_path_timeout_budget_degrades_to_queued", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		eng.ingestFn = func(_ context.Context, _ engine.IngestRequest) (engine.IngestResult, error) {
			return engine.IngestResult{OK: true, EntryID: "entry-timeout-1", DocumentID: "entry-timeout-1"}, nil
		}
		eng.extractFn = func(ctx context.Context, _ engine.ExtractRequest) (engine.ExtractResult, error) {
			select {
			case <-ctx.Done():
				return engine.ExtractResult{}, ctx.Err()
			case <-time.After(500 * time.Millisecond):
				return engine.ExtractResult{OK: true}, nil
			}
		}
		srv, store, _, _ := setupMCPGateway(t, true, eng)
		svc := &mcpRememberService{server: srv, fastPathTimeout: 60 * time.Millisecond, finalizeTimeout: time.Second}
		proj, err := store.GetProject(ctx, "p")
		must(t, err)
		agent, err := store.GetAgent(ctx, "agent-a")
		must(t, err)
		start := time.Now()
		res, err := svc.Remember(ctx, mcp.RememberRequest{
			Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
			Kind: engine.IngestKindDocument, Content: "budget test", IdempotencyKey: "r1-06-key",
		})
		must(t, err)
		if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
			t.Fatalf("fast path did not respect server timeout budget: took %v", elapsed)
		}
		if res.Status != "queued" || res.Saved || res.TaskID == "" {
			t.Fatalf("expected queued degradation on budget timeout: %+v", res)
		}
	})

	t.Run("R1-07_sync_catalog_err_not_exist_requeues_and_P2_qa_doc_id", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		eng.ingestFn = func(_ context.Context, _ engine.IngestRequest) (engine.IngestResult, error) {
			return engine.IngestResult{OK: true, EntryID: "qa-entry-1"}, nil
		}
		eng.extractFn = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
			return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
		}
		eng.syncCatalogFn = func(_ context.Context, _ *domain.Store) (*engine.SyncStats, error) {
			return nil, fmt.Errorf("official db missing: %w", os.ErrNotExist)
		}
		_, store, h, token := setupMCPGateway(t, true, eng)
		res, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
			"project_id": "p", "kind": "capture_qa", "content": `{"question":"q","answer":"a"}`, "idempotency_key": "r1-07-key",
		})
		if errMsg != "" || res["status"] != "queued" || res["saved"] != false || res["entry_id"] != "qa-entry-1" {
			t.Fatalf("SyncCatalog os.ErrNotExist must degrade to queued: %+v (%q)", res, errMsg)
		}
		if docID, _ := res["document_id"].(string); docID != "" {
			t.Fatalf("capture_qa with empty DocumentID must not copy entry_id: %+v", res)
		}
		item, err := store.GetInboxItem(ctx, fmt.Sprint(res["task_id"]))
		must(t, err)
		if item.Status != "pending" {
			t.Fatalf("expected inbox pending after SyncCatalog failure, got %q", item.Status)
		}
	})

	t.Run("R2-01_legacy_non_sorted_json_capture_qa_retry_no_conflict", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		_, store, h, token := setupMCPGateway(t, true, eng)
		rawQA := `{"question":"Q","answer":"A"}`
		legacyReq := engine.IngestRequest{
			Scope:          engine.Scope{DatasetID: "ds", UserID: "11111111-1111-4111-8111-111111111111"},
			Kind:           engine.IngestKindCaptureQA,
			IdempotencyKey: "legacy-qa-key",
			Content:        json.RawMessage(rawQA),
			SourceMeta:     engine.SourceMeta{SessionID: "legacy-qa-key"},
		}
		legacyRaw, err := json.Marshal(legacyReq)
		must(t, err)
		_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
			CaptureKey: "legacy-qa-key", UserID: "11111111-1111-4111-8111-111111111111",
			DatasetID: "ds", SessionID: "legacy-qa-key", MessageID: "legacy-qa-key",
			PayloadSHA256: digest(legacyRaw), EntryType: string(engine.IngestKindCaptureQA),
		})
		must(t, err)
		must(t, store.MarkCaptureReceiptSaved(ctx, "legacy-qa-key", "legacy-qa-entry-1"))

		res, errMsg := callMCPTool(t, h, token, "remember", map[string]any{
			"project_id": "p", "kind": "capture_qa", "content": rawQA, "idempotency_key": "legacy-qa-key",
		})
		if errMsg != "" || res["status"] != "saved" || res["saved"] != true || res["deduplicated"] != true || res["entry_id"] != "legacy-qa-entry-1" {
			t.Fatalf("legacy non-sorted JSON QA retry failed: %+v (%q)", res, errMsg)
		}
	})

	t.Run("R2-02_legacy_saved_receipt_shared_principal_mirrors_without_locking_out", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		_, store, h, tokenA := setupMCPGateway(t, true, eng)
		dir := t.TempDir()
		keyB := filepath.Join(dir, "agent-b2.key")
		const tokenB = "mcp-agent-b2-secret-token"
		must(t, os.WriteFile(keyB, []byte(tokenB+"\n"), 0o600))
		must(t, store.UpsertAgent(ctx, domain.Agent{
			ID: "agent-b2", Name: "Agent B2", PrincipalID: "11111111-1111-4111-8111-111111111111",
			KeyFile: keyB, DefaultAccess: domain.AccessMaintain,
		}))

		legacyReq := engine.IngestRequest{
			Scope:          engine.Scope{DatasetID: "ds", UserID: "11111111-1111-4111-8111-111111111111"},
			Kind:           engine.IngestKindDocument,
			IdempotencyKey: "legacy-shared-key",
			Content:        "legacy shared doc",
		}
		legacyRaw, err := json.Marshal(legacyReq)
		must(t, err)
		_, _, err = store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
			CaptureKey: "legacy-shared-key", UserID: "11111111-1111-4111-8111-111111111111",
			DatasetID: "ds", SessionID: "legacy-shared-key", MessageID: "legacy-shared-key",
			PayloadSHA256: digest(legacyRaw), EntryType: string(engine.IngestKindDocument),
		})
		must(t, err)
		must(t, store.MarkCaptureReceiptSaved(ctx, "legacy-shared-key", "legacy-shared-doc-1"))

		// B replays first -> gets B's read-only mirror task_id.
		resB, msgB := callMCPTool(t, h, tokenB, "remember", map[string]any{
			"project_id": "p", "content": "legacy shared doc", "idempotency_key": "legacy-shared-key",
		})
		if msgB != "" || resB["status"] != "saved" || resB["saved"] != true {
			t.Fatalf("agent B legacy replay failed: %+v (%q)", resB, msgB)
		}
		// A replays second -> must NOT conflict with B's synthetic mirror; gets A's own task_id.
		resA, msgA := callMCPTool(t, h, tokenA, "remember", map[string]any{
			"project_id": "p", "content": "legacy shared doc", "idempotency_key": "legacy-shared-key",
		})
		if msgA != "" || resA["status"] != "saved" || resA["saved"] != true {
			t.Fatalf("agent A legacy replay locked out by B: %+v (%q)", resA, msgA)
		}
		taskA, taskB := fmt.Sprint(resA["task_id"]), fmt.Sprint(resB["task_id"])
		if taskA == "" || taskB == "" || taskA == taskB {
			t.Fatalf("expected distinct mirror task_ids: A=%q B=%q", taskA, taskB)
		}
		// Each can query its own task_id, neither can query the other's.
		if stA, m := callMCPTool(t, h, tokenA, "remember_status", map[string]any{"task_id": taskA}); m != "" || stA["status"] != "succeeded" {
			t.Fatalf("A status failed: %+v (%q)", stA, m)
		}
		if _, m := callMCPTool(t, h, tokenA, "remember_status", map[string]any{"task_id": taskB}); m != "Access denied" {
			t.Fatalf("A must not query B's task_id: %q", m)
		}
		if _, m := callMCPTool(t, h, tokenB, "remember_status", map[string]any{"task_id": taskA}); m != "Access denied" {
			t.Fatalf("B must not query A's task_id: %q", m)
		}
	})

	t.Run("R1-06_generationMu_held_and_syncCatalog_blocked_degrade_immediately", func(t *testing.T) {
		eng := &mcpFastPathEngine{fakeEngine: &fakeEngine{}}
		eng.ingestFn = func(_ context.Context, _ engine.IngestRequest) (engine.IngestResult, error) {
			return engine.IngestResult{OK: true, EntryID: "doc-sync-block", DocumentID: "doc-sync-block"}, nil
		}
		eng.extractFn = func(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
			return engine.ExtractResult{OK: true, TouchedDocIDs: req.DocIDs}, nil
		}
		releaseSync := make(chan struct{})
		eng.syncCatalogFn = func(_ context.Context, _ *domain.Store) (*engine.SyncStats, error) {
			// Simulate blocking on p.syncMu.Lock() which ignores ctx until released.
			<-releaseSync
			return &engine.SyncStats{}, nil
		}
		t.Cleanup(func() { close(releaseSync) })

		srv, store, _, _ := setupMCPGateway(t, true, eng)
		svc := &mcpRememberService{server: srv, fastPathTimeout: 60 * time.Millisecond, finalizeTimeout: time.Second}
		proj, err := store.GetProject(ctx, "p")
		must(t, err)
		agent, err := store.GetAgent(ctx, "agent-a")
		must(t, err)

		// Case A: generationMu held by model handoff -> immediately persists pending and returns queued without blocking.
		srv.generationMu.Lock()
		startA := time.Now()
		resA, err := svc.Remember(ctx, mcp.RememberRequest{
			Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
			Kind: engine.IngestKindDocument, Content: "while handoff holds lock", IdempotencyKey: "r2-genlock-key",
		})
		srv.generationMu.Unlock()
		must(t, err)
		if elapsed := time.Since(startA); elapsed > 200*time.Millisecond {
			t.Fatalf("Remember blocked while generationMu held: %v", elapsed)
		}
		if resA.Status != "queued" || resA.Saved || resA.TaskID == "" {
			t.Fatalf("expected immediate queued while generationMu held: %+v", resA)
		}

		// Case B: SyncCatalog blocks ignoring fastCtx -> returns queued as soon as fastCtx budget expires.
		startB := time.Now()
		resB, err := svc.Remember(ctx, mcp.RememberRequest{
			Agent: agent, Project: proj, UserID: agent.PrincipalID, ReceiptUser: agent.PrincipalID,
			Kind: engine.IngestKindDocument, Content: "while sync blocks", IdempotencyKey: "r2-syncblock-key",
		})
		must(t, err)
		if elapsed := time.Since(startB); elapsed > 300*time.Millisecond {
			t.Fatalf("Remember did not respect fastCtx when SyncCatalog blocked: %v", elapsed)
		}
		if resB.Status != "queued" || resB.Saved || resB.EntryID != "doc-sync-block" {
			t.Fatalf("expected queued on SyncCatalog timeout: %+v", resB)
		}
	})
}
