package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway"
	"github.com/zwhu/memlane/core/gateway/completion"
	"github.com/zwhu/memlane/kit/agents"
)

const contractMgmtToken = "contract-regression-management-token-2026"

type contractMockEngine struct {
	mu           sync.Mutex
	modelDigest  string
	healthErr    error
	ingestErr    error
	extractErr   error
	searchErr    error
	ingestCalls      int
	extractCalls     int
	syncCalls        int
	sessionSyncCalls int
	datasetSyncCalls int
	fullSyncCalls    int
	ingestReqs       []engine.IngestRequest
	extractReqs      []engine.ExtractRequest
	ingestFunc   func(call int, req engine.IngestRequest) (engine.IngestResult, error)
}

func newContractMockEngine() *contractMockEngine {
	return &contractMockEngine{modelDigest: "contract-model-digest"}
}

func (m *contractMockEngine) Health(context.Context) (engine.HealthStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.healthErr != nil {
		return engine.HealthStatus{}, m.healthErr
	}
	return engine.HealthStatus{
		OK:          true,
		Engine:      "contract-mock",
		Version:     "1",
		Mode:        engine.HealthModeReady,
		ModelDigest: m.modelDigest,
		Capabilities: engine.Capabilities{
			ToolGraph:      true,
			LanguageEgress: true,
			ModelHandoff:   true,
		},
	}, nil
}

func (m *contractMockEngine) Ingest(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ingestCalls++
	call := m.ingestCalls
	m.ingestReqs = append(m.ingestReqs, req)
	if m.ingestFunc != nil {
		return m.ingestFunc(call, req)
	}
	if m.ingestErr != nil {
		return engine.IngestResult{}, m.ingestErr
	}
	return engine.IngestResult{
		OK:      true,
		EntryID: fmt.Sprintf("e%d", call),
	}, nil
}

func (m *contractMockEngine) Extract(_ context.Context, req engine.ExtractRequest) (engine.ExtractResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extractCalls++
	m.extractReqs = append(m.extractReqs, req)
	if m.extractErr != nil {
		return engine.ExtractResult{}, m.extractErr
	}
	return engine.ExtractResult{OK: true, ModelDigest: m.modelDigest}, nil
}

func (m *contractMockEngine) Search(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.searchErr != nil {
		return engine.SearchResult{}, m.searchErr
	}
	var hits []engine.SearchHit
	for _, scope := range req.Scopes {
		hits = append(hits, engine.SearchHit{
			DatasetID:  scope.DatasetID,
			DocumentID: "doc-" + scope.DatasetID,
			Title:      "Contract Memory",
			Snippet:    "Verified contract hit for " + scope.DatasetID,
			Score:      0.95,
		})
	}
	return engine.SearchResult{OK: true, Hits: hits}, nil
}

func (m *contractMockEngine) ListItems(context.Context, engine.ListRequest) (engine.ListResult, error) {
	return engine.ListResult{OK: true}, nil
}

func (m *contractMockEngine) GetItem(context.Context, engine.Scope, string) (engine.Item, error) {
	return engine.Item{}, engine.ErrNotFound
}

func (m *contractMockEngine) DeleteItem(context.Context, engine.Scope, string) error { return nil }

func (m *contractMockEngine) Graph(context.Context, engine.GraphRequest) (engine.GraphPayload, error) {
	return engine.GraphPayload{OK: true}, nil
}

func (m *contractMockEngine) SnapshotCatalog(_ context.Context, targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncCalls++
	switch {
	case opts.SessionsOnly:
		m.sessionSyncCalls++
	case len(targets) == 1:
		m.datasetSyncCalls++
	default:
		m.fullSyncCalls++
	}
	return &engine.CatalogSnapshot{}, nil
}

func (m *contractMockEngine) ReadLocalBody(context.Context, engine.LocalBodyRef) (string, error) {
	return "", engine.ErrNotFound
}

func openContractStoreAndServer(t *testing.T, eng *contractMockEngine, agentTokens map[string]string) (*domain.Store, *gateway.Server) {
	t.Helper()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	p, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID:  "contract-bridge",
		Mode:       "agent_bridge",
		BridgeKind: "codex",
		Model:      "contract-model",
	})
	if err != nil {
		t.Fatalf("PutGenerationProfile: %v", err)
	}
	if err := store.SetActiveGeneration(ctx, p.ProfileID, p.Revision); err != nil {
		t.Fatalf("SetActiveGeneration: %v", err)
	}

	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		AgentTokens:     agentTokens,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return store, srv
}

func doHTTPJSON(t *testing.T, srv *gateway.Server, method, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s returned non-JSON (%d): %s", method, path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

func mgmtHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + contractMgmtToken}
}

func agentHeaders(token string) map[string]string {
	return map[string]string{"X-Api-Key": token}
}

func TestContractLegacyUpgradeACLAndDefaultsParity(t *testing.T) {
	ctx := context.Background()
	eng := newContractMockEngine()
	tokens := map[string]string{
		"antigravity": "tok-antigravity-contract",
		"trae_ide":    "tok-trae-ide-contract",
		"codex":       "tok-codex-contract",
	}
	store, srv := openContractStoreAndServer(t, eng, tokens)

	if err := store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access:      domain.AccessMaintain,
		Capture:     true,
		AutoProcess: true,
		Revision:    5,
	}); err != nil {
		t.Fatalf("UpsertRegistryDefaults: %v", err)
	}
	if err := store.UpsertProject(ctx, domain.Project{
		ID:          "proj-main",
		Name:        "demo-app",
		DatasetID:   "ds-main",
		DatasetName: "demo-app",
		AutoCapture: true,
		Aliases:     []string{"/synthetic/demo-app"},
	}); err != nil {
		t.Fatalf("UpsertProject main: %v", err)
	}
	if err := store.UpsertProject(ctx, domain.Project{
		ID:          "proj-profiles",
		Name:        "ml-user-profiles",
		DatasetID:   "ds-profiles",
		DatasetName: "ml-user-profiles",
		AutoCapture: true,
	}); err != nil {
		t.Fatalf("UpsertProject protected: %v", err)
	}

	for _, ag := range []domain.Agent{
		{ID: "antigravity", Kind: "antigravity", Name: "Antigravity", PrincipalID: "u-agy", DefaultAccess: domain.AccessMaintain},
		{ID: "trae_ide", Kind: "trae_ide", Name: "TRAE IDE", PrincipalID: "u-trae", DefaultAccess: domain.AccessNone},
		{ID: "codex", Kind: "codex", Name: "Codex", PrincipalID: "u-codex", DefaultAccess: domain.AccessMaintain},
	} {
		if err := store.UpsertAgent(ctx, ag); err != nil {
			t.Fatalf("UpsertAgent %s: %v", ag.ID, err)
		}
	}
	readLevel := domain.AccessRead
	if err := store.SetACLOverride(ctx, domain.ACLOverride{
		TargetKind: "project",
		TargetID:   "proj-main",
		AgentID:    "codex",
		Access:     &readLevel,
	}); err != nil {
		t.Fatalf("SetACLOverride codex: %v", err)
	}

	// 1. GET /api/projects exposes real defaults, agents[].default_access, overrides, and protected flag.
	code, resp := doHTTPJSON(t, srv, http.MethodGet, "/api/projects", "", mgmtHeaders())
	if code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d, want 200: %+v", code, resp)
	}
	defaults, _ := resp["defaults"].(map[string]any)
	if defaults["access"] != "maintain" || defaults["capture"] != true || defaults["auto_process"] != true {
		t.Fatalf("unexpected defaults in GET /api/projects: %+v", defaults)
	}

	agentDefaults := map[string]string{}
	for _, raw := range resp["agents"].([]any) {
		row := raw.(map[string]any)
		agentDefaults[row["id"].(string)], _ = row["default_access"].(string)
	}
	if agentDefaults["antigravity"] != "maintain" || agentDefaults["trae_ide"] != "none" || agentDefaults["codex"] != "maintain" {
		t.Fatalf("unexpected agents[].default_access: %+v", agentDefaults)
	}

	var sawProtected, sawCodexReadOverride bool
	for _, raw := range resp["projects"].([]any) {
		p := raw.(map[string]any)
		if p["project_id"] == "proj-profiles" && p["protected"] == true {
			sawProtected = true
		}
		if p["project_id"] == "proj-main" {
			overrides, _ := p["overrides"].(map[string]any)
			if codexOv, ok := overrides["codex"].(map[string]any); ok && codexOv["access"] == "read" {
				sawCodexReadOverride = true
			}
		}
	}
	if !sawProtected || !sawCodexReadOverride {
		t.Fatalf("missing protected=%v or codex read override=%v in projects response: %+v", sawProtected, sawCodexReadOverride, resp["projects"])
	}

	// 2. Real POST /ingest & POST /recall enforce exact EvaluateProjectACL decisions.
	ingestMainBody := `{"project_id":"proj-main","session_id":"s-acl","message_id":"m-acl-1","kind":"capture_qa","content":{"question":"acl check","answer":"ok"}}`
	recallMainBody := `{"project_id":"proj-main","query":"acl check"}`

	// antigravity (default_access=maintain, inherits defaults): both ingest and recall allowed.
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/ingest", ingestMainBody, agentHeaders(tokens["antigravity"])); status != http.StatusAccepted {
		t.Fatalf("antigravity POST /ingest = %d, want 202: %+v", status, body)
	}
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/recall", recallMainBody, agentHeaders(tokens["antigravity"])); status != http.StatusOK {
		t.Fatalf("antigravity POST /recall = %d, want 200: %+v", status, body)
	}

	// trae_ide (default_access=none): both ingest and recall denied (403).
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/ingest", ingestMainBody, agentHeaders(tokens["trae_ide"])); status != http.StatusForbidden {
		t.Fatalf("trae_ide POST /ingest = %d, want 403: %+v", status, body)
	}
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/recall", recallMainBody, agentHeaders(tokens["trae_ide"])); status != http.StatusForbidden {
		t.Fatalf("trae_ide POST /recall = %d, want 403: %+v", status, body)
	}

	// codex (explicit read override): recall allowed (200), ingest denied (403).
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/recall", recallMainBody, agentHeaders(tokens["codex"])); status != http.StatusOK {
		t.Fatalf("codex POST /recall = %d, want 200: %+v", status, body)
	}
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/ingest", ingestMainBody, agentHeaders(tokens["codex"])); status != http.StatusForbidden {
		t.Fatalf("codex POST /ingest = %d, want 403: %+v", status, body)
	}

	// Protected ml-user-profiles: antigravity denied for both ingest (project_protected) and recall (dataset_read_denied).
	ingestProtected := `{"project_id":"proj-profiles","session_id":"s-prof","message_id":"m-prof","kind":"capture_qa","content":{"question":"profile","answer":"no"}}`
	recallProtected := `{"project_id":"proj-profiles","query":"profile"}`
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/ingest", ingestProtected, agentHeaders(tokens["antigravity"])); status != http.StatusForbidden || body["error"] != "project_protected" {
		t.Fatalf("protected POST /ingest = %d %+v, want 403 project_protected", status, body)
	}
	if status, body := doHTTPJSON(t, srv, http.MethodPost, "/recall", recallProtected, agentHeaders(tokens["antigravity"])); status != http.StatusForbidden {
		t.Fatalf("protected POST /recall = %d %+v, want 403", status, body)
	}
}

func TestContractHalfFailedRetryZeroReceiptConflict(t *testing.T) {
	ctx := context.Background()
	eng := newContractMockEngine()
	eng.ingestFunc = func(call int, _ engine.IngestRequest) (engine.IngestResult, error) {
		if call > 1 {
			return engine.IngestResult{}, engine.ErrConflict
		}
		return engine.IngestResult{OK: true, EntryID: "e1"}, nil
	}
	eng.extractErr = &engine.EngineError{
		Code:    "extract_timeout",
		Message: "engine request timed out",
		Err:     errors.Join(engine.ErrUnavailable, context.DeadlineExceeded),
	}

	agentToken := "tok-retry-agent"
	store, srv := openContractStoreAndServer(t, eng, map[string]string{"agent-1": agentToken})
	if err := store.UpsertProject(ctx, domain.Project{
		ID: "proj-retry", Name: "retry-app", DatasetID: "ds-retry", DatasetName: "retry-app", AutoCapture: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAgent(ctx, domain.Agent{
		ID: "agent-1", Kind: "antigravity", Name: "Antigravity", PrincipalID: "u-1", DefaultAccess: domain.AccessMaintain,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access: domain.AccessMaintain, Capture: true, AutoProcess: true, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Enqueue capture_qa.
	status, _ := doHTTPJSON(t, srv, http.MethodPost, "/ingest", `{
		"project_id":"proj-retry","session_id":"sess-half","message_id":"msg-half",
		"kind":"capture_qa","content":{"question":"retry question","answer":"retry answer"}
	}`, agentHeaders(agentToken))
	if status != http.StatusAccepted {
		t.Fatalf("POST /ingest = %d, want 202", status)
	}

	// Attempt 1: Ingest succeeds (e1, receipt saved), Extract fails with extract_timeout.
	worked, err := srv.Inbox().ProcessOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("attempt 1 ProcessOnce = (%v, %v), want (true, nil)", worked, err)
	}
	status, projBefore := doHTTPJSON(t, srv, http.MethodGet, "/api/projects/proj-retry", "", mgmtHeaders())
	if status != http.StatusOK || projBefore["inbox_failed"] != float64(1) {
		t.Fatalf("expected 1 failed inbox item before retry, got (%d): %+v", status, projBefore)
	}

	// Retry via public HTTP endpoint after Extract recovers.
	eng.mu.Lock()
	eng.extractErr = nil
	eng.mu.Unlock()

	status, retryResp := doHTTPJSON(t, srv, http.MethodPost, "/api/projects/proj-retry/retry", "", mgmtHeaders())
	if status != http.StatusOK || retryResp["retried_count"] != float64(1) {
		t.Fatalf("POST /api/projects/proj-retry/retry = (%d) %+v, want 200 retried_count=1", status, retryResp)
	}

	// Attempt 2: must skip duplicate Ingest (ingestCalls remains 1), call Extract, and succeed with zero conflict.
	worked, err = srv.Inbox().ProcessOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("attempt 2 ProcessOnce = (%v, %v), want (true, nil)", worked, err)
	}

	eng.mu.Lock()
	ingestCalls, extractCalls := eng.ingestCalls, eng.extractCalls
	retryExtractReqs := slices.Clone(eng.extractReqs)
	eng.mu.Unlock()
	if ingestCalls != 1 {
		t.Fatalf("retry called Ingest again: ingestCalls=%d, want 1", ingestCalls)
	}
	if extractCalls != 2 || len(retryExtractReqs) != 2 {
		t.Fatalf("extractCalls=%d, want 2", extractCalls)
	}
	if !slices.Equal(retryExtractReqs[1].DocIDs, []string{"sess-half"}) {
		t.Fatalf("attempt 2 extract DocIDs = %v, want [sess-half] (must not fall back to receipt EntryID)", retryExtractReqs[1].DocIDs)
	}

	status, projAfter := doHTTPJSON(t, srv, http.MethodGet, "/api/projects/proj-retry", "", mgmtHeaders())
	if status != http.StatusOK {
		t.Fatalf("GET /api/projects/proj-retry = %d", status)
	}
	inboxAfter, _ := projAfter["inbox"].(map[string]any)
	if inboxAfter["failed"] != float64(0) || inboxAfter["succeeded"] != float64(1) {
		t.Fatalf("expected failed=0 succeeded=1 after retry, got: %+v", inboxAfter)
	}
	if lastErr, _ := projAfter["last_error"].(string); lastErr != "" {
		t.Fatalf("expected empty last_error after retry, got: %q", lastErr)
	}
}

func TestContractBatchCaptureTraceFastPathAndQASessionExtract(t *testing.T) {
	ctx := context.Background()
	eng := newContractMockEngine()
	agentToken := "tok-batch-agent"
	store, srv := openContractStoreAndServer(t, eng, map[string]string{"agent-batch": agentToken})

	if err := store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access: domain.AccessMaintain, Capture: true, AutoProcess: true, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(ctx, domain.Project{
		ID: "proj-batch", Name: "batch-app", DatasetID: "ds-batch", DatasetName: "batch-app", AutoCapture: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAgent(ctx, domain.Agent{
		ID: "agent-batch", Kind: "antigravity", Name: "Antigravity", PrincipalID: "u-batch", DefaultAccess: domain.AccessMaintain,
	}); err != nil {
		t.Fatal(err)
	}

	// Enqueue 10 capture_trace + 1 capture_qa for the same session.
	for i := 1; i <= 10; i++ {
		body := fmt.Sprintf(`{
			"project_id":"proj-batch","session_id":"sess-batch","message_id":"tool:call-%02d",
			"kind":"capture_trace","content":{"origin_function":"read_file","method_params":{"step":%d},"status":"success"}
		}`, i, i)
		if status, resp := doHTTPJSON(t, srv, http.MethodPost, "/ingest", body, agentHeaders(agentToken)); status != http.StatusAccepted {
			t.Fatalf("trace %d POST /ingest = %d: %+v", i, status, resp)
		}
	}
	qaBody := `{
		"project_id":"proj-batch","session_id":"sess-batch","message_id":"turn:sess-batch:1:12",
		"kind":"capture_qa","content":{"question":"Summarize batch run","answer":"All 10 traces captured cleanly."}
	}`
	if status, resp := doHTTPJSON(t, srv, http.MethodPost, "/ingest", qaBody, agentHeaders(agentToken)); status != http.StatusAccepted {
		t.Fatalf("qa POST /ingest = %d: %+v", status, resp)
	}

	// Drain all 11 items.
	processed := 0
	for {
		worked, err := srv.Inbox().ProcessOnce(ctx)
		if err != nil {
			t.Fatalf("ProcessOnce #%d failed: %v", processed+1, err)
		}
		if !worked {
			break
		}
		processed++
	}
	if processed != 11 {
		t.Fatalf("processed %d inbox items, want 11", processed)
	}

	eng.mu.Lock()
	ingestCalls, extractCalls, syncCalls := eng.ingestCalls, eng.extractCalls, eng.syncCalls
	sessionSyncCalls, datasetSyncCalls, fullSyncCalls := eng.sessionSyncCalls, eng.datasetSyncCalls, eng.fullSyncCalls
	extractReqs := slices.Clone(eng.extractReqs)
	eng.mu.Unlock()

	if ingestCalls != 11 {
		t.Fatalf("ingestCalls = %d, want 11", ingestCalls)
	}
	if syncCalls != 11 || sessionSyncCalls != 10 || datasetSyncCalls != 1 || fullSyncCalls != 0 {
		t.Fatalf("sync breakdown = total:%d session:%d dataset:%d full:%d, want 11/10/1/0",
			syncCalls, sessionSyncCalls, datasetSyncCalls, fullSyncCalls)
	}
	if extractCalls != 1 {
		t.Fatalf("extractCalls = %d, want 1 (only capture_qa should trigger Extract)", extractCalls)
	}
	if !slices.Equal(extractReqs[0].DocIDs, []string{"sess-batch"}) {
		t.Fatalf("capture_qa extract DocIDs = %v, want [sess-batch]", extractReqs[0].DocIDs)
	}

	status, proj := doHTTPJSON(t, srv, http.MethodGet, "/api/projects/proj-batch", "", mgmtHeaders())
	if status != http.StatusOK {
		t.Fatalf("GET /api/projects/proj-batch = %d", status)
	}
	inbox, _ := proj["inbox"].(map[string]any)
	if inbox["succeeded"] != float64(11) || inbox["failed"] != float64(0) || inbox["pending"] != float64(0) {
		t.Fatalf("expected 11 succeeded / 0 failed / 0 pending, got: %+v", inbox)
	}
}

func TestContractTimeoutClassificationSeparation(t *testing.T) {
	ctx := context.Background()
	eng := newContractMockEngine()
	agentToken := "tok-timeout-agent"
	store, srv := openContractStoreAndServer(t, eng, map[string]string{"agent-to": agentToken})

	if err := store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access: domain.AccessMaintain, Capture: true, AutoProcess: true, Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(ctx, domain.Project{
		ID: "proj-to", Name: "timeout-app", DatasetID: "ds-to", DatasetName: "timeout-app", AutoCapture: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAgent(ctx, domain.Agent{
		ID: "agent-to", Kind: "antigravity", Name: "Antigravity", PrincipalID: "u-to", DefaultAccess: domain.AccessMaintain,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. /extract timeout -> error_code == "extract_timeout" & summary contains "记忆提炼超时".
	eng.mu.Lock()
	eng.extractErr = &engine.EngineError{
		Code:    "extract_timeout",
		Message: "engine request timed out",
		Err:     errors.Join(engine.ErrUnavailable, context.DeadlineExceeded),
	}
	eng.mu.Unlock()

	if status, _ := doHTTPJSON(t, srv, http.MethodPost, "/ingest", `{
		"project_id":"proj-to","session_id":"s-to-1","message_id":"m-extract-to",
		"kind":"capture_qa","content":{"question":"slow extract","answer":"still working"}
	}`, agentHeaders(agentToken)); status != http.StatusAccepted {
		t.Fatalf("enqueue extract-timeout item = %d, want 202", status)
	}
	if worked, err := srv.Inbox().ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("ProcessOnce extract-timeout = (%v, %v)", worked, err)
	}

	// 2. /ingest timeout -> error_code == "engine_timeout" (NEVER "extract_timeout").
	eng.mu.Lock()
	eng.extractErr = nil
	eng.ingestErr = &engine.EngineError{
		Code:    "engine_timeout",
		Message: "engine request timed out",
		Err:     errors.Join(engine.ErrUnavailable, context.DeadlineExceeded),
	}
	eng.mu.Unlock()

	if status, _ := doHTTPJSON(t, srv, http.MethodPost, "/ingest", `{
		"project_id":"proj-to","session_id":"s-to-2","message_id":"m-ingest-to",
		"kind":"capture_qa","content":{"question":"slow ingest","answer":"timed out"}
	}`, agentHeaders(agentToken)); status != http.StatusAccepted {
		t.Fatalf("enqueue ingest-timeout item = %d, want 202", status)
	}
	if worked, err := srv.Inbox().ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("ProcessOnce ingest-timeout = (%v, %v)", worked, err)
	}

	status, proj := doHTTPJSON(t, srv, http.MethodGet, "/api/projects/proj-to", "", mgmtHeaders())
	if status != http.StatusOK {
		t.Fatalf("GET /api/projects/proj-to = %d", status)
	}
	failures := proj["inbox"].(map[string]any)["recent_failures"].([]any)
	if len(failures) != 2 {
		t.Fatalf("expected 2 failures (extract_timeout + engine_timeout), got: %+v", failures)
	}
	byCode := map[string]string{}
	for _, raw := range failures {
		f := raw.(map[string]any)
		byCode[f["error_code"].(string)] = f["summary"].(string)
	}
	if summary, ok := byCode["extract_timeout"]; !ok || !strings.Contains(summary, "记忆提炼超时") {
		t.Fatalf("extract timeout misclassified: %+v", byCode)
	}
	if summary, ok := byCode["engine_timeout"]; !ok || strings.Contains(summary, "记忆提炼超时") || !strings.Contains(summary, "超时") {
		t.Fatalf("ingest timeout misclassified: %+v", byCode)
	}

	// 3. /health timeout via GET /api/runtime/monitor -> degraded with engine_timeout summary (never extract_timeout).
	eng.mu.Lock()
	eng.ingestErr = nil
	eng.healthErr = &engine.EngineError{
		Code:    "engine_timeout",
		Message: "engine request timed out",
		Err:     errors.Join(engine.ErrUnavailable, context.DeadlineExceeded),
	}
	eng.mu.Unlock()

	status, mon := doHTTPJSON(t, srv, http.MethodGet, "/api/runtime/monitor", "", mgmtHeaders())
	if status != http.StatusOK {
		t.Fatalf("GET /api/runtime/monitor = %d", status)
	}
	if mon["state"] != "degraded" {
		t.Fatalf("expected degraded state on health timeout, got: %+v", mon)
	}
	monSummary, _ := mon["status_summary"].(string)
	if strings.Contains(monSummary, "记忆提炼超时") || monSummary != "提炼服务连接超时" {
		t.Fatalf("health timeout misclassified as %q, want 提炼服务连接超时", monSummary)
	}
}

type contractGateFunc func(context.Context) (*completion.ActiveProfile, error)

func (f contractGateFunc) ResolveActive(ctx context.Context) (*completion.ActiveProfile, error) {
	return f(ctx)
}

func TestContractCompletionHealthEndpoint(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cliPath := filepath.Join(dir, "codex-cli")
	for file, data := range map[string]string{
		cliPath:                        "#!/bin/sh\nexit 0\n",
		filepath.Join(dir, "auth.json"):   "{}",
		filepath.Join(dir, "config.toml"): `model = "gpt-test"`,
	} {
		mode := os.FileMode(0o600)
		if file == cliPath {
			mode = 0o700
		}
		if err := os.WriteFile(file, []byte(data), mode); err != nil {
			t.Fatalf("write fixture %s: %v", file, err)
		}
	}

	readyProfile := &completion.ActiveProfile{
		Mode:       "agent_bridge",
		BridgeKind: "codex",
		Model:      "gpt-test",
		Executable: cliPath,
		AuthHome:   dir,
		Defaults:   &agents.ModelDefaults{Model: "gpt-test", Provider: "openai", Source: "agent_config"},
	}

	var active atomic.Pointer[completion.ActiveProfile]
	active.Store(readyProfile)

	compSrv, err := completion.NewServer(completion.Config{
		Token: "contract-completion-bearer-token-2026",
		Gate:  contractGateFunc(func(context.Context) (*completion.ActiveProfile, error) { return active.Load(), nil }),
	})
	if err != nil {
		t.Fatalf("completion.NewServer: %v", err)
	}

	eng := newContractMockEngine()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const agentToken = "tok-contract-comp-health-agent"
	if err := store.UpsertAgent(ctx, domain.Agent{
		ID: "agent-ch", Kind: "codex", Name: "Codex", PrincipalID: "u-ch", DefaultAccess: domain.AccessMaintain,
	}); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	srv, err := gateway.NewServer(gateway.Config{
		Store:             store,
		Engine:            eng,
		Projector:         eng,
		CompletionHandler: compSrv.Handler(),
		ManagementToken:   contractMgmtToken,
		AgentTokens:       map[string]string{"agent-ch": agentToken},
	})
	if err != nil {
		t.Fatalf("gateway.NewServer: %v", err)
	}

	// 1. Auth & browser Origin enforcement: anonymous -> 401, agent credential -> 403, browser Origin -> 403.
	for _, route := range []string{"/api/completion/health", "/v1/completion/health"} {
		if code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", nil); code != http.StatusUnauthorized || body["error"] != "unauthorized" {
			t.Fatalf("anonymous GET %s = %d %+v, want 401 unauthorized", route, code, body)
		}
		if code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", agentHeaders(agentToken)); code != http.StatusForbidden || body["error"] != "management_required" {
			t.Fatalf("agent GET %s = %d %+v, want 403 management_required", route, code, body)
		}
		originHeaders := mgmtHeaders()
		originHeaders["Origin"] = "https://example.invalid"
		if code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", originHeaders); code != http.StatusForbidden || body["error"] != "browser_origin_rejected" {
			t.Fatalf("origin GET %s = %d %+v, want 403 browser_origin_rejected", route, code, body)
		}
	}

	// 2. Active agent_bridge -> 200 ready.
	for _, route := range []string{"/api/completion/health", "/v1/completion/health"} {
		code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", mgmtHeaders())
		if code != http.StatusOK || body["status"] != "ready" || body["provider"] != "codex" || body["model"] != "gpt-test" {
			t.Fatalf("ready GET %s = %d %+v, want 200 ready", route, code, body)
		}
	}

	// 3. Direct provider (bridge_inactive) -> 503 bridge_inactive.
	providerProfile := *readyProfile
	providerProfile.Mode = "provider"
	active.Store(&providerProfile)
	for _, route := range []string{"/api/completion/health", "/v1/completion/health"} {
		code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", mgmtHeaders())
		errObj, _ := body["error"].(map[string]any)
		if code != http.StatusServiceUnavailable || errObj["code"] != "bridge_inactive" {
			t.Fatalf("inactive GET %s = %d %+v, want 503 bridge_inactive", route, code, body)
		}
	}

	// 4. Model mismatch -> 409 bridge_profile_mismatch.
	mismatchProfile := *readyProfile
	mismatchProfile.Model = "different-model"
	active.Store(&mismatchProfile)
	for _, route := range []string{"/api/completion/health", "/v1/completion/health"} {
		code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", mgmtHeaders())
		errObj, _ := body["error"].(map[string]any)
		if code != http.StatusConflict || errObj["code"] != "bridge_profile_mismatch" {
			t.Fatalf("mismatch GET %s = %d %+v, want 409 bridge_profile_mismatch", route, code, body)
		}
	}

	// 5. Fingerprint drift -> 409 generation_bridge_changed_revalidate.
	driftedProfile := *readyProfile
	driftedProfile.ExpectedFingerprint = strings.Repeat("0", 64)
	active.Store(&driftedProfile)
	for _, route := range []string{"/api/completion/health", "/v1/completion/health"} {
		code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", mgmtHeaders())
		errObj, _ := body["error"].(map[string]any)
		if code != http.StatusConflict || errObj["code"] != "generation_bridge_changed_revalidate" {
			t.Fatalf("drifted GET %s = %d %+v, want 409 generation_bridge_changed_revalidate", route, code, body)
		}
	}
}

func writeSyntheticExecutable(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
	return path
}

type contractBridgeModels struct{}

func (contractBridgeModels) ModelStatus(context.Context) (engine.ModelStatusResult, error) {
	return engine.ModelStatusResult{OK: true, Mode: "ready", ModelDigest: "bridge-live-digest", OutputLanguage: "zh-CN"}, nil
}

func (contractBridgeModels) ProbeModel(context.Context, engine.ProbeRequest) (engine.ProbeResult, error) {
	return engine.ProbeResult{
		Status:         "passed",
		PlainPassed:    true,
		SchemaPassed:   true,
		QualityPassed:  true,
		StructuredMode: "json_schema",
	}, nil
}

func (contractBridgeModels) ApplyModel(_ context.Context, req engine.ApplyModelRequest) (engine.ApplyModelResult, error) {
	return engine.ApplyModelResult{OK: true, Applied: true, OperationID: req.OperationID, ModelDigest: "bridge-live-digest"}, nil
}

func TestContractGenerationReturnsDetectedBridgesAndDefaults(t *testing.T) {
	// Ensure inherited session env vars never trigger state_*.sqlite copies or override defaults.
	t.Setenv("CODEX_THREAD_ID", "inherited-daemon-session-should-be-ignored")
	t.Setenv("TRAE_THREAD_ID", "inherited-trae-session-should-be-ignored")

	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	emptyApps := filepath.Join(home, "apps")
	if err := os.MkdirAll(emptyApps, 0o700); err != nil {
		t.Fatal(err)
	}

	codexBin := writeSyntheticExecutable(t, filepath.Join(binDir, "codex"))
	piBin := writeSyntheticExecutable(t, filepath.Join(binDir, "pi"))
	traexBin := writeSyntheticExecutable(t, filepath.Join(binDir, "traecli"))
	agyBin := writeSyntheticExecutable(t, filepath.Join(binDir, "agy"))

	fixtures := map[string]string{
		filepath.Join(home, ".codex", "auth.json"):                        `{"token":"synthetic-secret-codex-never-leak"}`,
		filepath.Join(home, ".codex", "config.toml"):                      "model = \"gpt-5.4\"\nmodel_reasoning_effort = \"high\"\nservice_tier = \"fast\"\n",
		filepath.Join(home, ".codex", "state_1.sqlite"):                   "not-a-valid-sqlite-file-if-read-this-fails",
		filepath.Join(home, ".pi", "agent", "auth.json"):                  `{"token":"synthetic-secret-pi-never-leak"}`,
		filepath.Join(home, ".pi", "agent", "settings.json"):              `{"defaultProvider":"deepseek","defaultModel":"deepseek-reasoner","defaultThinkingLevel":"medium"}`,
		filepath.Join(home, ".trae", "cli", "auth.json"):                  `{"token":"synthetic-secret-traex-never-leak"}`,
		filepath.Join(home, ".trae", "traecli.toml"):                      "model = \"doubao-seed-code\"\n",
		filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"): `{"model":"gemini-2.5-pro"}`,
	}
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range fixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	// Intentionally leave DiscoveryOptions.Environ == nil to prove detectGenerationBridges
	// isolates session env vars even in default production configuration.
	discovery := agents.DiscoveryOptions{
		Home:    home,
		AppDirs: []string{emptyApps},
		Kinds:   []string{agents.HostCodex, agents.HostPi, agents.HostTraeX, agents.HostAntigravity},
		LookPath: func(name string) (string, error) {
			candidate := filepath.Join(binDir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			return "", errors.New("not found")
		},
		RuntimeOverrides: map[string]agents.HostOverride{
			agents.HostCodex:       {Executable: codexBin, ProfileDir: filepath.Join(home, ".codex")},
			agents.HostPi:          {Executable: piBin, ProfileDir: filepath.Join(home, ".pi", "agent")},
			agents.HostTraeX:       {Executable: traexBin, ProfileDir: filepath.Join(home, ".trae")},
			agents.HostAntigravity: {Executable: agyBin, ProfileDir: filepath.Join(home, ".gemini", "config")},
		},
	}

	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	eng := newContractMockEngine()
	srv, err := gateway.NewServer(gateway.Config{
		Store:            store,
		Engine:           eng,
		Models:           contractBridgeModels{},
		Projector:        eng,
		ManagementToken:  contractMgmtToken,
		DiscoveryOptions: discovery,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	for _, route := range []string{"/api/generation", "/v1/generation"} {
		code, resp := doHTTPJSON(t, srv, http.MethodGet, route, "", mgmtHeaders())
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d: %+v", route, code, resp)
		}
		raw, _ := json.Marshal(resp)
		if strings.Contains(string(raw), "never-leak") {
			t.Fatalf("GET %s leaked auth secret: %s", route, string(raw))
		}

		detectedRaw, ok := resp["detected_processing_clis"].([]any)
		if !ok || len(detectedRaw) != 4 {
			t.Fatalf("GET %s detected_processing_clis = %#v, want 4 entries", route, resp["detected_processing_clis"])
		}
		var detected []string
		for _, v := range detectedRaw {
			detected = append(detected, v.(string))
		}
		for _, kind := range []string{"codex", "pi", "traex", "antigravity"} {
			if !slices.Contains(detected, kind) {
				t.Fatalf("GET %s missing %s in detected_processing_clis: %v", route, kind, detected)
			}
		}

		defaultsMap, ok := resp["agent_defaults"].(map[string]any)
		if !ok || len(defaultsMap) != 4 {
			t.Fatalf("GET %s agent_defaults = %#v, want 4 entries", route, resp["agent_defaults"])
		}
		bridgesMap, ok := resp["agent_bridges"].(map[string]any)
		if !ok || len(bridgesMap) != 4 {
			t.Fatalf("GET %s agent_bridges = %#v, want 4 entries", route, resp["agent_bridges"])
		}

		codexDef := defaultsMap["codex"].(map[string]any)
		if codexDef["model"] != "gpt-5.4" || codexDef["provider"] != "openai" || codexDef["source"] != "agent_config" ||
			codexDef["reasoning_effort"] != "high" || codexDef["service_tier"] != "fast" {
			t.Fatalf("unexpected codex agent_defaults: %+v", codexDef)
		}
		piDef := defaultsMap["pi"].(map[string]any)
		if piDef["model"] != "deepseek-reasoner" || piDef["provider"] != "deepseek" || piDef["thinking_level"] != "medium" {
			t.Fatalf("unexpected pi agent_defaults: %+v", piDef)
		}
		traexDef := defaultsMap["traex"].(map[string]any)
		if traexDef["model"] != "doubao-seed-code" || traexDef["provider"] != "trae" {
			t.Fatalf("unexpected traex agent_defaults: %+v", traexDef)
		}
		agyDef := defaultsMap["antigravity"].(map[string]any)
		if agyDef["model"] != "gemini-2.5-pro" || agyDef["provider"] != "google" || agyDef["configuration_source"] != "agy_cli_settings" {
			t.Fatalf("unexpected antigravity agent_defaults: %+v", agyDef)
		}

		codexBridge := bridgesMap["codex"].(map[string]any)
		if codexBridge["mode"] != "agent_bridge" || codexBridge["bridge_kind"] != "codex" || codexBridge["model"] != "gpt-5.4" {
			t.Fatalf("unexpected codex agent_bridges: %+v", codexBridge)
		}
		piBridge := bridgesMap["pi"].(map[string]any)
		if piBridge["mode"] != "agent_bridge" || piBridge["bridge_kind"] != "pi" || piBridge["bridge_provider"] != "deepseek" || piBridge["model"] != "deepseek-reasoner" {
			t.Fatalf("unexpected pi agent_bridges: %+v", piBridge)
		}

		// Verify returned bridge profiles round-trip cleanly through PUT /api/generation/profiles/{id},
		// and POST /v1/generation/validations persists & returns agent_defaults on GET /api/generation.
		if route == "/api/generation" {
			for _, kind := range []string{"codex", "pi", "traex", "antigravity"} {
				profileBytes, err := json.Marshal(bridgesMap[kind])
				if err != nil {
					t.Fatal(err)
				}
				saveCode, saveResp := doHTTPJSON(t, srv, http.MethodPut, "/api/generation/profiles/agent-"+kind, string(profileBytes), mgmtHeaders())
				if saveCode != http.StatusOK {
					t.Fatalf("PUT bridge profile %s = %d: %+v (payload=%s)", kind, saveCode, saveResp, string(profileBytes))
				}
			}
			valCode, valResp := doHTTPJSON(t, srv, http.MethodPost, "/v1/generation/validations",
				`{"profile":{"id":"agent-codex","revision":1},"operation_id":"op-codex-val"}`, mgmtHeaders())
			if valCode != http.StatusOK || valResp["status"] != "passed" {
				t.Fatalf("POST /v1/generation/validations = %d %+v", valCode, valResp)
			}
			valDefaults, _ := valResp["agent_defaults"].(map[string]any)
			if valDefaults["model"] != "gpt-5.4" || valDefaults["provider"] != "openai" || valDefaults["reasoning_effort"] != "high" {
				t.Fatalf("POST validation missing agent_defaults: %+v", valResp)
			}
			getValCode, getValResp := doHTTPJSON(t, srv, http.MethodGet, "/v1/generation/validations/op-codex-val", "", mgmtHeaders())
			if getValCode != http.StatusOK {
				t.Fatalf("GET /v1/generation/validations/op-codex-val = %d %+v", getValCode, getValResp)
			}
			getValDefaults, _ := getValResp["agent_defaults"].(map[string]any)
			if getValDefaults["model"] != "gpt-5.4" || getValDefaults["provider"] != "openai" || getValDefaults["service_tier"] != "fast" {
				t.Fatalf("GET validation missing persisted agent_defaults: %+v", getValResp)
			}
		}
	}

	// Verify GET /api/generation exposes validations["op-codex-val"].agent_defaults matching top-level agent_defaults["codex"].
	code, afterVal := doHTTPJSON(t, srv, http.MethodGet, "/api/generation", "", mgmtHeaders())
	if code != http.StatusOK {
		t.Fatalf("GET /api/generation after validation = %d: %+v", code, afterVal)
	}
	validationsMap, _ := afterVal["validations"].(map[string]any)
	codexVal, _ := validationsMap["op-codex-val"].(map[string]any)
	codexValDef, _ := codexVal["agent_defaults"].(map[string]any)
	if codexValDef["model"] != "gpt-5.4" || codexValDef["provider"] != "openai" ||
		codexValDef["source"] != "agent_config" || codexValDef["reasoning_effort"] != "high" || codexValDef["service_tier"] != "fast" {
		t.Fatalf("GET /api/generation validation agent_defaults mismatch: %+v", codexVal)
	}

	// Unauthenticated / unconfigured CLI must be skipped silently and return non-nil empty maps.
	emptyHome := t.TempDir()
	unconfiguredBin := writeSyntheticExecutable(t, filepath.Join(emptyHome, "bin", "codex"))
	srvEmpty, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		DiscoveryOptions: agents.DiscoveryOptions{
			Home:     emptyHome,
			AppDirs:  []string{emptyApps},
			Kinds:    []string{agents.HostCodex},
			LookPath: func(string) (string, error) { return "", errors.New("not found") },
			Environ:  func(string) string { return "" },
			RuntimeOverrides: map[string]agents.HostOverride{
				agents.HostCodex: {Executable: unconfiguredBin, ProfileDir: filepath.Join(emptyHome, ".codex")},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewServer empty: %v", err)
	}
	code, emptyResp := doHTTPJSON(t, srvEmpty, http.MethodGet, "/api/generation", "", mgmtHeaders())
	if code != http.StatusOK {
		t.Fatalf("GET /api/generation unconfigured = %d: %+v", code, emptyResp)
	}
	if defMap, ok := emptyResp["agent_defaults"].(map[string]any); !ok || len(defMap) != 0 {
		t.Fatalf("expected empty agent_defaults object, got %#v", emptyResp["agent_defaults"])
	}
	if brMap, ok := emptyResp["agent_bridges"].(map[string]any); !ok || len(brMap) != 0 {
		t.Fatalf("expected empty agent_bridges object, got %#v", emptyResp["agent_bridges"])
	}
}

func TestContractConnectionsReturnsRichCaptureAndProfileState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	appsDir := filepath.Join(home, "Applications")

	codexBin := writeSyntheticExecutable(t, filepath.Join(binDir, "codex"))
	piBin := writeSyntheticExecutable(t, filepath.Join(binDir, "pi"))
	traexBin := writeSyntheticExecutable(t, filepath.Join(binDir, "traecli"))
	claudeBin := writeSyntheticExecutable(t, filepath.Join(binDir, "claude"))

	// Create synthetic Trae CN.app bundle with Info.plist version and executable.
	traeApp := filepath.Join(appsDir, "Trae CN.app")
	writeSyntheticExecutable(t, filepath.Join(traeApp, "Contents", "MacOS", "Electron"))
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleShortVersionString</key><string>1.9.8</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(traeApp, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".pi", "agent", "extensions", "memlane-cognee"),
		filepath.Join(home, ".trae"),
		filepath.Join(home, ".trae-cn"),
		filepath.Join(home, ".claude"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// Install managed hooks for codex (healthy) and pi (waiting_session), legacy hooks for trae_ide (needs_hook_repair),
	// and NO hooks for traex (must remain registered).
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte(`{
		"hooks": {"Stop": [{"_memlane_managed": "memlane-hook", "hooks": [{"type": "command", "command": "/bin/memlane hook codex Stop --gateway-url http://127.0.0.1:18174 --key-file /tmp/k", "_memlane_managed": "memlane-hook"}]}]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "extensions", "memlane-cognee", "host-binding.json"),
		[]byte(`{"managed_by":"memlane","gateway_url":"http://127.0.0.1:18174","token_file":"/tmp/k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".trae-cn", "hooks.json"), []byte(`{
		"version": 1,
		"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "python3 '/home/.trae/managed-marketplaces/trae_ide-0123456789abcdef01234567/0123456789abcdef0123/plugins/cognee/scripts/trae_ide_hook.py' UserPromptSubmit"}]}]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	eng := newContractMockEngine()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpsertRegistryDefaults(ctx, domain.RegistryDefaults{
		Access: domain.AccessMaintain, Capture: true, AutoProcess: true, Revision: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(ctx, domain.Project{
		ID: "proj-conn", Name: "conn-app", DatasetID: "ds-conn", DatasetName: "conn-app", AutoCapture: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, ag := range []domain.Agent{
		{ID: "agent-codex", Kind: agents.HostCodex, Name: "Codex", PrincipalID: "u-codex", DefaultAccess: domain.AccessMaintain},
		{ID: "agent-trae", Kind: agents.HostTraeIDE, Name: "TRAE CN", PrincipalID: "u-trae", DefaultAccess: domain.AccessMaintain},
		{ID: "agent-pi", Kind: agents.HostPi, Name: "pi", PrincipalID: "u-pi", DefaultAccess: domain.AccessMaintain},
		{ID: "agent-traex", Kind: agents.HostTraeX, Name: "TRAE CLI", PrincipalID: "u-traex", DefaultAccess: domain.AccessMaintain},
		{ID: "agent-claude", Kind: agents.HostClaudeCode, Name: "Claude Code", PrincipalID: "u-claude", DefaultAccess: domain.AccessMaintain},
	} {
		if err := store.UpsertAgent(ctx, ag); err != nil {
			t.Fatalf("UpsertAgent %s: %v", ag.ID, err)
		}
	}
	activeProfile, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "conn-bridge", Mode: "agent_bridge", BridgeKind: agents.HostCodex, Model: "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("PutGenerationProfile: %v", err)
	}
	if err := store.SetActiveGeneration(ctx, activeProfile.ProfileID, activeProfile.Revision); err != nil {
		t.Fatalf("SetActiveGeneration: %v", err)
	}

	const codexTok = "tok-conn-codex"
	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		AgentTokens:     map[string]string{"agent-codex": codexTok},
		DiscoveryOptions: agents.DiscoveryOptions{
			Home:    home,
			AppDirs: []string{appsDir},
			Kinds:   []string{agents.HostCodex, agents.HostTraeIDE, agents.HostPi, agents.HostTraeX, agents.HostClaudeCode, agents.HostCursor},
			LookPath: func(name string) (string, error) {
				candidate := filepath.Join(binDir, name)
				if _, err := os.Stat(candidate); err == nil {
					return candidate, nil
				}
				return "", errors.New("not found")
			},
			Environ: func(string) string { return "" },
			RuntimeOverrides: map[string]agents.HostOverride{
				agents.HostCodex:      {Executable: codexBin, ProfileDir: filepath.Join(home, ".codex")},
				agents.HostPi:         {Executable: piBin, ProfileDir: filepath.Join(home, ".pi", "agent")},
				agents.HostTraeX:      {Executable: traexBin, ProfileDir: filepath.Join(home, ".trae")},
				agents.HostClaudeCode: {Executable: claudeBin, ProfileDir: filepath.Join(home, ".claude")},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Seed a real succeeded capture for agent-codex via POST /ingest + InboxWorker.ProcessOnce.
	status, _ := doHTTPJSON(t, srv, http.MethodPost, "/ingest", `{
		"project_id":"proj-conn","session_id":"sess-codex-1","message_id":"msg-1",
		"kind":"capture_qa","content":{"question":"connection check","answer":"captured"}
	}`, agentHeaders(codexTok))
	if status != http.StatusAccepted {
		t.Fatalf("POST /ingest = %d, want 202", status)
	}
	if worked, err := srv.Inbox().ProcessOnce(ctx); err != nil || !worked {
		t.Fatalf("ProcessOnce = (%v, %v), want (true, nil)", worked, err)
	}

	// 2. Seed a saved capture_receipt directly for agent-trae (legacy-imported receipt pattern).
	if _, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "trae-key-1", UserID: "u-trae", DatasetID: "ds-conn", SessionID: "sess-trae-1",
		MessageID: "m-trae-1", PayloadSHA256: "sha-trae-1", EntryType: "capture_qa",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCaptureReceiptSaved(ctx, "trae-key-1", "entry-trae-1"); err != nil {
		t.Fatal(err)
	}

	// Verify GET /api/connections is strictly read-only.
	var changesBefore, changesAfter int
	if err := store.DB().QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	code, resp := doHTTPJSON(t, srv, http.MethodGet, "/api/connections", "", mgmtHeaders())
	if code != http.StatusOK {
		t.Fatalf("GET /api/connections = %d: %+v", code, resp)
	}
	if err := store.DB().QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if changesAfter != changesBefore {
		t.Fatalf("GET /api/connections mutated SQLite: changes %d -> %d", changesBefore, changesAfter)
	}
	if resp["revision"] != float64(3) {
		t.Fatalf("revision = %v, want 3", resp["revision"])
	}

	byID := map[string]map[string]any{}
	for _, raw := range resp["connections"].([]any) {
		row := raw.(map[string]any)
		byID[row["instance_id"].(string)] = row
	}

	// agent-codex: managed hook + capture receipt -> healthy & environment_verified.
	codexRow := byID["agent-codex"]
	codexCap, _ := codexRow["capture"].(map[string]any)
	if codexRow["status"] != "configured_pending_new_session" || codexRow["detected"] != true ||
		codexRow["agent_registered"] != true || codexRow["profile"] != filepath.Join(home, ".codex") ||
		codexRow["new_sessions"] != float64(1) || codexRow["needs_hook_repair"] != false ||
		codexRow["environment_verified"] != true || codexRow["plugin_version"] != "memlane-hook" ||
		codexCap == nil || codexCap["state"] != "healthy" || codexCap["saved_count"] != float64(1) ||
		codexCap["last_saved_at"].(float64) <= 0 {
		t.Fatalf("unexpected agent-codex connection row: %+v", codexRow)
	}

	// agent-trae: legacy trae_ide_hook.py + saved receipt -> healthy, environment_verified, needs_hook_repair=true.
	traeRow := byID["agent-trae"]
	traeCap, _ := traeRow["capture"].(map[string]any)
	if traeRow["status"] != "configured_pending_new_session" || traeRow["detected"] != true ||
		traeRow["host_version"] != "1.9.8" || traeRow["profile"] != filepath.Join(home, ".trae-cn") ||
		traeRow["new_sessions"] != float64(1) || traeRow["needs_hook_repair"] != true ||
		traeRow["environment_verified"] != true || traeCap == nil || traeCap["state"] != "healthy" ||
		traeCap["last_saved_at"].(float64) <= 0 {
		t.Fatalf("unexpected agent-trae connection row: %+v", traeRow)
	}

	// agent-pi: managed host-binding.json, no saved receipts -> waiting_session.
	piRow := byID["agent-pi"]
	piCap, _ := piRow["capture"].(map[string]any)
	if piRow["status"] != "configured_pending_new_session" || piRow["detected"] != true ||
		piRow["profile"] != filepath.Join(home, ".pi", "agent") || piRow["new_sessions"] != float64(0) ||
		piRow["environment_verified"] != nil || piCap == nil || piCap["state"] != "waiting_session" {
		t.Fatalf("unexpected agent-pi connection row: %+v", piRow)
	}

	// agent-traex: registered & detected, but NO managed hook and NO capture receipts -> stays registered.
	traexRow := byID["agent-traex"]
	if traexRow["status"] != "registered" || traexRow["detected"] != true ||
		traexRow["profile"] != filepath.Join(home, ".trae") || traexRow["capture"] != nil ||
		traexRow["needs_hook_repair"] != false {
		t.Fatalf("unexpected agent-traex connection row: %+v", traexRow)
	}

	// agent-claude: registered & detected, non-capture-managed kind -> stays registered with profile.
	claudeRow := byID["agent-claude"]
	if claudeRow["status"] != "registered" || claudeRow["detected"] != true ||
		claudeRow["profile"] != filepath.Join(home, ".claude") || claudeRow["capture"] != nil {
		t.Fatalf("unexpected agent-claude connection row: %+v", claudeRow)
	}
}

func TestContractConnectionsTraeXCLIHooksDetection(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	traexBin := writeSyntheticExecutable(t, filepath.Join(binDir, "traecli"))
	profileDir := filepath.Join(home, ".trae")
	if err := os.MkdirAll(filepath.Join(profileDir, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}

	eng := newContractMockEngine()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertAgent(ctx, domain.Agent{
		ID: "agent-traex", Kind: agents.HostTraeX, Name: "TRAE CLI", PrincipalID: "u-traex", DefaultAccess: domain.AccessMaintain,
	}); err != nil {
		t.Fatal(err)
	}

	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		DiscoveryOptions: agents.DiscoveryOptions{
			Home:  home,
			Kinds: []string{agents.HostTraeX},
			LookPath: func(name string) (string, error) {
				if name == "traecli" {
					return traexBin, nil
				}
				return "", errors.New("not found")
			},
			Environ: func(string) string { return "" },
			RuntimeOverrides: map[string]agents.HostOverride{
				agents.HostTraeX: {Executable: traexBin, ProfileDir: profileDir},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	fetchTraeX := func() map[string]any {
		t.Helper()
		code, resp := doHTTPJSON(t, srv, http.MethodGet, "/api/connections", "", mgmtHeaders())
		if code != http.StatusOK {
			t.Fatalf("GET /api/connections = %d: %+v", code, resp)
		}
		for _, raw := range resp["connections"].([]any) {
			row := raw.(map[string]any)
			if row["instance_id"] == "agent-traex" {
				return row
			}
		}
		t.Fatalf("agent-traex missing from connections: %+v", resp)
		return nil
	}

	const memlaneHooks = `{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`
	const thirdPartyHooks = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/opt/flux/flux-hook stop"}]}]}}`
	legacyPath := filepath.Join(profileDir, "hooks.json")
	cliPath := filepath.Join(profileDir, "cli", "hooks.json")

	// 1. Third-party hooks only in both files -> registered, needs_hook_repair=false.
	if err := os.WriteFile(legacyPath, []byte(thirdPartyHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliPath, []byte(thirdPartyHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	row := fetchTraeX()
	if row["status"] != "registered" || row["needs_hook_repair"] != false || row["capture"] != nil || row["environment_verified"] != nil {
		t.Fatalf("third-party-only traex row mismatch: %+v", row)
	}

	// 2. Memlane hook ONLY in deprecated ~/.trae/hooks.json (not in cli/hooks.json) -> NOT active, needs_hook_repair=true.
	if err := os.WriteFile(legacyPath, []byte(memlaneHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	row = fetchTraeX()
	if row["status"] != "registered" || row["needs_hook_repair"] != true || row["capture"] != nil || row["environment_verified"] != nil {
		t.Fatalf("deprecated-only traex hook must not report active and must request repair: %+v", row)
	}

	// 3. Memlane hook in BOTH cli/hooks.json and deprecated hooks.json -> configured, still needs_hook_repair=true to clean deprecated file.
	if err := os.WriteFile(cliPath, []byte(memlaneHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	row = fetchTraeX()
	capState, _ := row["capture"].(map[string]any)
	if row["status"] != "configured_pending_new_session" || row["needs_hook_repair"] != true || capState == nil || capState["state"] != "waiting_session" {
		t.Fatalf("dual cli+deprecated traex hook row mismatch: %+v", row)
	}

	// 4. Memlane hook in cli/hooks.json and deprecated hooks.json cleaned (third-party only) -> configured, needs_hook_repair=false.
	if err := os.WriteFile(legacyPath, []byte(thirdPartyHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	row = fetchTraeX()
	capState, _ = row["capture"].(map[string]any)
	if row["status"] != "configured_pending_new_session" || row["needs_hook_repair"] != false || capState == nil || capState["state"] != "waiting_session" {
		t.Fatalf("clean cli/hooks.json traex row mismatch: %+v", row)
	}

	// 5. With a saved capture receipt: cli/hooks.json verifies environment; reverting to deprecated-only drops environment_verified and flags repair.
	if _, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "traex-key-1", UserID: "u-traex", DatasetID: "ds-traex", SessionID: "sess-traex-1",
		MessageID: "m-traex-1", PayloadSHA256: "sha-traex-1", EntryType: "capture_qa",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCaptureReceiptSaved(ctx, "traex-key-1", "entry-traex-1"); err != nil {
		t.Fatal(err)
	}
	row = fetchTraeX()
	capState, _ = row["capture"].(map[string]any)
	if row["status"] != "configured_pending_new_session" || row["needs_hook_repair"] != false ||
		row["environment_verified"] != true || row["plugin_version"] != "memlane-hook" ||
		capState == nil || capState["state"] != "healthy" {
		t.Fatalf("healthy cli/hooks.json traex row mismatch: %+v", row)
	}

	if err := os.WriteFile(cliPath, []byte(thirdPartyHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(memlaneHooks), 0o600); err != nil {
		t.Fatal(err)
	}
	row = fetchTraeX()
	if row["needs_hook_repair"] != true || row["environment_verified"] != nil || row["plugin_version"] != nil {
		t.Fatalf("deprecated-only traex with old receipt must not verify environment and must flag repair: %+v", row)
	}
}
