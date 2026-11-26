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
	ingestCalls  int
	extractCalls int
	syncCalls    int
	ingestReqs   []engine.IngestRequest
	extractReqs  []engine.ExtractRequest
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

func (m *contractMockEngine) SyncCatalog(context.Context, *domain.Store) (*engine.SyncStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncCalls++
	return &engine.SyncStats{}, nil
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
	extractReqs := slices.Clone(eng.extractReqs)
	eng.mu.Unlock()

	if ingestCalls != 11 {
		t.Fatalf("ingestCalls = %d, want 11", ingestCalls)
	}
	if syncCalls != 11 {
		t.Fatalf("syncCalls = %d, want 11 (traces must still sync catalog)", syncCalls)
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

	// 1. Auth enforcement: anonymous -> 401, agent credential -> 403.
	for _, route := range []string{"/api/completion/health", "/v1/completion/health"} {
		if code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", nil); code != http.StatusUnauthorized || body["error"] != "unauthorized" {
			t.Fatalf("anonymous GET %s = %d %+v, want 401 unauthorized", route, code, body)
		}
		if code, body := doHTTPJSON(t, srv, http.MethodGet, route, "", agentHeaders(agentToken)); code != http.StatusForbidden || body["error"] != "management_required" {
			t.Fatalf("agent GET %s = %d %+v, want 403 management_required", route, code, body)
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
