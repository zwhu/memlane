package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/mcp"
)

type codeActivityTestEngine struct {
	*fakeEngine
	activity      map[string]engine.CodeActivitySnapshot
	activityErr   map[string]error
	pipelineRuns  []engine.PipelineRun
	ensured       engine.DatasetRecord
	ensureCalls   int
	indexResult   engine.CodeIndexResult
	indexCalls    []engine.CodeIndexRequest
	indexHook     func(engine.CodeIndexRequest)
	searchByScope func(engine.Scope) (engine.SearchResult, error)
}

func (e *codeActivityTestEngine) Search(ctx context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	if e.searchByScope != nil && len(req.Scopes) == 1 {
		return e.searchByScope(req.Scopes[0])
	}
	return e.fakeEngine.Search(ctx, req)
}

func (e *codeActivityTestEngine) EnsureDataset(_ context.Context, name string) (engine.DatasetRecord, error) {
	e.ensureCalls++
	if e.ensured.ID != "" {
		return e.ensured, nil
	}
	return engine.DatasetRecord{ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Name: name}, nil
}

func (e *codeActivityTestEngine) IndexCode(_ context.Context, req engine.CodeIndexRequest) (engine.CodeIndexResult, error) {
	e.indexCalls = append(e.indexCalls, req)
	if e.indexHook != nil {
		e.indexHook(req)
	}
	if e.indexResult.Status != "" {
		return e.indexResult, nil
	}
	return engine.CodeIndexResult{
		OK:             true,
		Status:         "completed",
		DatasetID:      req.DatasetID,
		PipelineRunID:  "run-code-1",
		ItemsProcessed: 5,
	}, nil
}

func (e *codeActivityTestEngine) ProjectCodeActivity(_ context.Context, datasetID string) (engine.CodeActivitySnapshot, error) {
	if err, ok := e.activityErr[datasetID]; ok {
		return engine.CodeActivitySnapshot{}, err
	}
	if snap, ok := e.activity[datasetID]; ok {
		return snap, nil
	}
	return engine.CodeActivitySnapshot{}, engine.ErrNotFound
}

func (e *codeActivityTestEngine) ListPipelineRuns(_ context.Context, q engine.PipelineRunsQuery) ([]engine.PipelineRun, error) {
	out := make([]engine.PipelineRun, 0)
	for _, r := range e.pipelineRuns {
		if q.DatasetID != "" && r.DatasetID != q.DatasetID {
			continue
		}
		if q.DatasetID == "" && q.AllowedDatasetIDs != nil {
			matched := false
			for _, allowed := range q.AllowedDatasetIDs {
				if allowed == r.DatasetID {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, r)
	}
	if q.Offset >= len(out) {
		return []engine.PipelineRun{}, nil
	}
	out = out[q.Offset:]
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func decodeRecMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	must(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestCodeActivityAndPipelineRunsRoutes(t *testing.T) {
	var eng *codeActivityTestEngine
	s, store, _ := testServer(t, func(cfg *Config) {
		eng = &codeActivityTestEngine{
			fakeEngine: cfg.Engine.(*fakeEngine),
			activity:   map[string]engine.CodeActivitySnapshot{},
			activityErr: map[string]error{},
			pipelineRuns: []engine.PipelineRun{
				{ID: "r1", PipelineRunID: "pr1", DatasetID: "ds", OperationName: "remember", Outcome: "succeeded"},
				{ID: "r2", PipelineRunID: "pr2", DatasetID: "unmanaged-ds", OperationName: "remember", Outcome: "succeeded"},
				{ID: "r3", PipelineRunID: "pr3", DatasetID: "common", OperationName: "cognify", Outcome: "succeeded"},
			},
		}
		cfg.Engine = eng
	})
	ctx := t.Context()
	repoDir := t.TempDir()
	resolvedRepoDir, err := filepath.EvalSymlinks(repoDir)
	must(t, err)
	symlinkAlias := filepath.Join(t.TempDir(), "sym-repo")
	must(t, os.Symlink(repoDir, symlinkAlias))
	p, err := store.GetProject(ctx, "p")
	must(t, err)
	p.Aliases = []string{repoDir, symlinkAlias}
	must(t, store.UpsertProject(ctx, p))

	// 1. code-activity before code dataset is configured -> 409 code_dataset_not_configured.
	for _, prefix := range []string{"/api", "/v1"} {
		out := decodeRecMap(t, manage(t, s, "GET", prefix+"/projects/p/code-activity", "", http.StatusConflict))
		if out["error"] != "code_dataset_not_configured" {
			t.Fatalf("%s code-activity error = %+v", prefix, out)
		}
	}

	// 2. POST code-dataset creates and persists CodeDatasetID + CodeConfigured=true;
	// second call preserves existing binding without calling EnsureDataset again.
	dsOut := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-dataset", "", http.StatusOK))
	codeDS := dsOut["id"].(string)
	if codeDS != "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" || dsOut["name"] != "p-code" || dsOut["project_id"] != "p" {
		t.Fatalf("unexpected code-dataset response: %+v", dsOut)
	}
	dsOut2 := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-dataset", "", http.StatusOK))
	if dsOut2["id"] != codeDS || eng.ensureCalls != 1 {
		t.Fatalf("second code-dataset should preserve existing binding: out=%+v calls=%d", dsOut2, eng.ensureCalls)
	}
	updated, err := store.GetProject(ctx, "p")
	must(t, err)
	if updated.CodeDatasetID != codeDS || !updated.CodeConfigured {
		t.Fatalf("project not persisted with code dataset: %+v", updated)
	}

	// 3. code-activity when dataset not yet in official store -> 404 code_dataset_not_found.
	notFoundOut := decodeRecMap(t, manage(t, s, "GET", "/v1/projects/p/code-activity", "", http.StatusNotFound))
	if notFoundOut["error"] != "code_dataset_not_found" {
		t.Fatalf("expected code_dataset_not_found, got %+v", notFoundOut)
	}

	// 4. code-activity returns exact contract when present (even with CodeEnabled=false),
	// while POST code-index rejects when CodeEnabled=false (409 code_indexing_disabled).
	eng.activity[codeDS] = engine.CodeActivitySnapshot{
		CodeDatasetID:    codeDS,
		DatasetCreatedAt: "2026-11-27T10:00:00Z",
		Active:           false,
		Status:           "DATASET_PROCESSING_COMPLETED",
		RunID:            "run-prev",
		LatestCreatedAt:  "2026-11-27T10:05:00Z",
	}
	actOut := decodeRecMap(t, manage(t, s, "GET", "/v1/projects/p/code-activity", "", http.StatusOK))
	if actOut["project_id"] != "p" || actOut["code_dataset_id"] != codeDS || actOut["active"] != false || actOut["run_id"] != "run-prev" {
		t.Fatalf("unexpected code-activity response: %+v", actOut)
	}
	disabledIdx := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-index", `{}`, http.StatusConflict))
	if disabledIdx["error"] != "code_indexing_disabled" {
		t.Fatalf("expected code_indexing_disabled, got %+v", disabledIdx)
	}
	updated.CodeEnabled = true
	must(t, store.UpsertProject(ctx, updated))

	// 5. POST code-index rejects unregistered & symlink paths (403), succeeds with default alias (200),
	// rejects concurrent in-flight request (409), and rejects when active (409).
	deniedIdx := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-index", `{"repo_path":"/etc/passwd"}`, http.StatusForbidden))
	if deniedIdx["error"] != "repo_path_not_allowed" {
		t.Fatalf("expected repo_path_not_allowed, got %+v", deniedIdx)
	}
	symBody, _ := json.Marshal(map[string]string{"repo_path": symlinkAlias})
	symIdx := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-index", string(symBody), http.StatusForbidden))
	if symIdx["error"] != "repo_path_not_allowed" {
		t.Fatalf("expected symlink alias rejected with repo_path_not_allowed, got %+v", symIdx)
	}
	okIdx := decodeRecMap(t, manage(t, s, "POST", "/v1/projects/p/code-index", `{}`, http.StatusOK))
	if okIdx["status"] != "completed" || okIdx["dataset_id"] != codeDS || len(eng.indexCalls) != 1 || eng.indexCalls[0].RepoPath != resolvedRepoDir {
		t.Fatalf("unexpected code-index response: %+v calls=%+v", okIdx, eng.indexCalls)
	}

	// Concurrent re-entrancy guard returns 409 code_index_already_active,
	// AND stays held even if the original HTTP client disconnects early!
	started := make(chan struct{})
	release := make(chan struct{})
	eng.indexHook = func(engine.CodeIndexRequest) {
		close(started)
		<-release
	}
	reqCtx, cancelReq := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/api/projects/p/code-index", strings.NewReader(`{}`)).WithContext(reqCtx)
		req.Header.Set("Authorization", "Bearer "+testManagement)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
	}()
	<-started
	// Disconnect original client while engine IndexCode is still running.
	cancelReq()
	wg.Wait()
	// Retry must still see 409 code_index_already_active until engine finishes.
	dupIdx := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-index", `{}`, http.StatusConflict))
	if dupIdx["error"] != "code_index_already_active" {
		t.Fatalf("post-disconnect retry code-index = %+v, want code_index_already_active", dupIdx)
	}
	close(release)
	// Wait briefly for detached goroutine to release in-flight lock.
	for i := 0; i < 50; i++ {
		s.codeIndexMu.Lock()
		_, busy := s.codeIndexInFlight[codeDS]
		s.codeIndexMu.Unlock()
		if !busy {
			break
		}
		os.Stat(repoDir)
	}
	eng.indexHook = nil

	// Parent directory replaced by a symlink pointing elsewhere must be rejected (403).
	parentBase := t.TempDir()
	realTarget := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(realTarget, "sub"), 0o700))
	parentLink := filepath.Join(parentBase, "parent-link")
	must(t, os.Symlink(realTarget, parentLink))
	escapedAlias := filepath.Join(parentLink, "sub")
	updated.Aliases = append(updated.Aliases, escapedAlias)
	must(t, store.UpsertProject(ctx, updated))
	escBody, _ := json.Marshal(map[string]string{"repo_path": escapedAlias})
	escIdx := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-index", string(escBody), http.StatusForbidden))
	if escIdx["error"] != "repo_path_not_allowed" {
		t.Fatalf("parent-symlink alias = %+v, want 403 repo_path_not_allowed", escIdx)
	}

	eng.activity[codeDS] = engine.CodeActivitySnapshot{CodeDatasetID: codeDS, Active: true, Status: "running", RunID: "run-active"}
	activeIdx := decodeRecMap(t, manage(t, s, "POST", "/api/projects/p/code-index", `{}`, http.StatusConflict))
	if activeIdx["error"] != "code_index_already_active" {
		t.Fatalf("expected code_index_already_active, got %+v", activeIdx)
	}

	// 6. GET activity/pipeline-runs filters out unmanaged datasets and rejects unmanaged dataset_id.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/activity/pipeline-runs?limit=50&offset=0", nil)
	req.Header.Set("Authorization", "Bearer "+testManagement)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/activity/pipeline-runs = %d: %s", rec.Code, rec.Body.String())
	}
	var runs []engine.PipelineRun
	must(t, json.Unmarshal(rec.Body.Bytes(), &runs))
	if len(runs) != 2 {
		t.Fatalf("expected only 2 managed runs (excluding unmanaged-ds), got %+v", runs)
	}
	manage(t, s, "GET", "/v1/activity/pipeline-runs?dataset_id=unmanaged-ds", "", http.StatusForbidden)
}

func TestRecallPartialAndSharedMCPAuthenticator(t *testing.T) {
	var eng *codeActivityTestEngine
	var mcpSrv *mcp.Server
	s, store, _ := testServer(t, func(cfg *Config) {
		eng = &codeActivityTestEngine{fakeEngine: cfg.Engine.(*fakeEngine)}
		cfg.Engine = eng
		var err error
		mcpSrv, err = mcp.NewServer(mcp.Config{Store: cfg.Store, Engine: eng})
		must(t, err)
		cfg.MCPHandler = mcpSrv.Handler()
	})
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"read"}`, 200)
	manage(t, s, "PUT", "/api/common/acl/agent", `{"access":"read"}`, 200)

	// 1. Gateway injects shared authenticator into MCPHandler, so in-memory AgentTokens work on /mcp too.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("X-Api-Key", testAgent)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"agent_id":"agent"`) {
		t.Fatalf("MCP with shared gateway authenticator = %d %s", rec.Code, rec.Body.String())
	}

	// 2. Hook /recall: project hits + common failure -> 200 with partial=true and scope_errors.
	eng.searchByScope = func(sc engine.Scope) (engine.SearchResult, error) {
		if sc.DatasetID == "common" {
			return engine.SearchResult{}, engine.ErrUnavailable
		}
		return engine.SearchResult{OK: true, Hits: []engine.SearchHit{{DatasetID: "ds", DocumentID: "doc-1", Snippet: "project hit"}}}, nil
	}
	out := agentRequest(t, s, "/recall", `{"project_id":"p","query":"check"}`, http.StatusOK)
	if out["partial"] != true {
		t.Fatalf("expected partial=true, got %+v", out)
	}
	scopeErrs, _ := out["scope_errors"].([]any)
	if len(scopeErrs) != 1 || scopeErrs[0].(map[string]any)["scope_kind"] != "common" {
		t.Fatalf("unexpected scope_errors: %+v", out["scope_errors"])
	}

	// 3. Hook /recall: new unprovisioned project + common hit -> 200 with partial=false and no scope_errors.
	eng.searchByScope = func(sc engine.Scope) (engine.SearchResult, error) {
		if sc.DatasetID == "ds" {
			return engine.SearchResult{}, errors.Join(engine.ErrDatasetNotProvisioned, engine.ErrNotFound)
		}
		return engine.SearchResult{OK: true, Hits: []engine.SearchHit{{DatasetID: "common", DocumentID: "c-1", Snippet: "common pref"}}}, nil
	}
	out = agentRequest(t, s, "/recall", `{"project_id":"p","query":"check"}`, http.StatusOK)
	if out["partial"] != false || out["scope_errors"] != nil || len(out["hits"].([]any)) != 1 {
		t.Fatalf("unprovisioned project recall = %+v", out)
	}

	// 4. Non-blocking telemetry: dropping recall_query_stats table must NOT fail /recall!
	_, err := store.DB().ExecContext(t.Context(), `DROP TABLE recall_query_stats`)
	must(t, err)
	out = agentRequest(t, s, "/recall", `{"project_id":"p","query":"check"}`, http.StatusOK)
	if out["ok"] != true || len(out["hits"].([]any)) != 1 {
		t.Fatalf("telemetry failure should not block recall: %+v", out)
	}

	// 5. Conflicting / duplicate auth headers are rejected on both management and agentOnly endpoints.
	conflictMgmt := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	conflictMgmt.Header.Set("Authorization", "Bearer "+testManagement)
	conflictMgmt.Header.Set("X-Memlane-Management", "wrong-token")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, conflictMgmt)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("conflicting management headers = %d, want 401", rec.Code)
	}

	// Valid ManagementToken alongside conflicting or duplicate X-Api-Key must also be rejected (401).
	conflictMgmtAPIKey := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	conflictMgmtAPIKey.Header.Set("Authorization", "Bearer "+testManagement)
	conflictMgmtAPIKey.Header.Set("X-Api-Key", testAgent)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, conflictMgmtAPIKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("management token + conflicting X-Api-Key = %d, want 401", rec.Code)
	}
	dupMgmtAPIKey := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	dupMgmtAPIKey.Header.Set("Authorization", "Bearer "+testManagement)
	dupMgmtAPIKey.Header.Add("X-Api-Key", testManagement)
	dupMgmtAPIKey.Header.Add("X-Api-Key", testManagement)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, dupMgmtAPIKey)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("management token + duplicate X-Api-Key = %d, want 401", rec.Code)
	}

	conflictAgent := httptest.NewRequest(http.MethodPost, "/recall", strings.NewReader(`{"project_id":"p","query":"check"}`))
	conflictAgent.Header.Set("Content-Type", "application/json")
	conflictAgent.Header.Set("Authorization", "Bearer "+testAgent)
	conflictAgent.Header.Set("X-Api-Key", "other-token")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, conflictAgent)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("conflicting agent headers = %d, want 401", rec.Code)
	}

	// 6. MCP 403 preserves res.Code (agent_retired) in response body.
	retiredKey := filepath.Join(t.TempDir(), "retired.key")
	must(t, os.WriteFile(retiredKey, []byte("retired-mcp-token\n"), 0o600))
	must(t, store.UpsertAgent(t.Context(), domain.Agent{ID: "ret-mcp", Name: "RetiredMCP", KeyFile: retiredKey, RetiredAt: "2026-11-27T00:00:00Z"}))
	retReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	retReq.Header.Set("X-Api-Key", "retired-mcp-token")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, retReq)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "agent_retired") {
		t.Fatalf("MCP retired agent = %d %q, want 403 agent_retired", rec.Code, rec.Body.String())
	}
}
