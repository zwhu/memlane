package app

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zwhu/memlane/app/api"
	appgraph "github.com/zwhu/memlane/app/graph"
	"github.com/zwhu/memlane/app/settings"
	"github.com/zwhu/memlane/app/shell"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway"
)

func TestLoadConfigAcceptsPureGatewayWithoutServiceURL(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "management.token")
	if err := os.WriteFile(tokenFile, []byte("pure-gateway-management-token-0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	pureGatewayPath := filepath.Join(dir, "runtime-pure-gateway.json")
	raw := `{"project_control":{"url":"http://127.0.0.1:18910","management_token_file":` + strconv.Quote(tokenFile) + `},"projects":[{"id":"p1","name":"P1","dataset_id":"ds1"}]}`
	if err := os.WriteFile(pureGatewayPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(pureGatewayPath)
	if err != nil {
		t.Fatalf("LoadConfig failed for pure gateway config without service_url: %v", err)
	}
	if cfg.ServiceURL != "" || cfg.TokenFile != "" || cfg.ProjectControl == nil || cfg.ProjectControl.URL != "http://127.0.0.1:18910" {
		t.Fatalf("unexpected loaded config: %+v", cfg)
	}

	// Reading identity must work cleanly without service_url or token_file.
	id1, err := readingIdentity(Client{Config: cfg})
	if err != nil || id1 == "" || !strings.HasPrefix(id1, "http://127.0.0.1:18910/") {
		t.Fatalf("pure gateway readingIdentity failed: id=%q err=%v", id1, err)
	}
	if err := os.WriteFile(tokenFile, []byte("rotated-gateway-management-token-98765432"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(tokenFile, future, future)
	id2, err := readingIdentity(Client{Config: cfg})
	if err != nil || id2 == "" || id2 == id1 {
		t.Fatalf("pure gateway readingIdentity did not reflect token rotation: id1=%q id2=%q err=%v", id1, id2, err)
	}

	// Both project_control.url and service_url missing must still be rejected.
	emptyPath := filepath.Join(dir, "runtime-empty.json")
	if err := os.WriteFile(emptyPath, []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(emptyPath); err == nil {
		t.Fatal("LoadConfig accepted config with neither project_control.url nor service_url")
	}

	// Non-loopback or path-bearing project_control.url must be rejected (matches ValidateLoopbackOrigin).
	badPath := filepath.Join(dir, "runtime-bad-control-url.json")
	if err := os.WriteFile(badPath, []byte(`{"project_control":{"url":"http://127.0.0.1:18790/wrong"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(badPath); err == nil {
		t.Fatal("LoadConfig accepted project_control.url with path")
	}
}

type zeroDirectStubEngine struct {
	mu           sync.Mutex
	codeDSID     string
	codeActive   bool
	codeIndexCnt int
	commonDocs   map[string]string
	runs         []engine.PipelineRun
	histories    map[engine.SessionRef]engine.SessionHistoryResult
}

var (
	_ engine.MemoryEngine         = (*zeroDirectStubEngine)(nil)
	_ engine.ActivityReader       = (*zeroDirectStubEngine)(nil)
	_ engine.CodeIndexer          = (*zeroDirectStubEngine)(nil)
	_ engine.SessionHistoryReader = (*zeroDirectStubEngine)(nil)
)

func (e *zeroDirectStubEngine) Health(context.Context) (engine.HealthStatus, error) {
	return engine.HealthStatus{OK: true, Engine: "stub", Mode: "ready"}, nil
}

func (e *zeroDirectStubEngine) Ingest(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.commonDocs == nil {
		e.commonDocs = map[string]string{}
	}
	docID := req.IdempotencyKey
	if docID == "" {
		docID = "common-doc-1"
	}
	if s, ok := req.Content.(string); ok {
		e.commonDocs[docID] = s
	} else {
		raw, _ := json.Marshal(req.Content)
		e.commonDocs[docID] = string(raw)
	}
	return engine.IngestResult{OK: true, EntryID: docID, DocumentID: docID}, nil
}

func (e *zeroDirectStubEngine) Extract(context.Context, engine.ExtractRequest) (engine.ExtractResult, error) {
	return engine.ExtractResult{OK: true}, nil
}

func (e *zeroDirectStubEngine) Search(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	if len(req.Scopes) == 0 {
		return engine.SearchResult{OK: true}, nil
	}
	return engine.SearchResult{
		OK: true,
		Hits: []engine.SearchHit{{
			DatasetID:  req.Scopes[0].DatasetID,
			DocumentID: "doc-1",
			Title:      "Zero Direct Doc",
			Snippet:    "gateway semantic snippet for " + req.Query,
			Metadata:   map[string]any{"node_set": []any{"session_learnings"}},
		}},
	}, nil
}

func (e *zeroDirectStubEngine) ListItems(context.Context, engine.ListRequest) (engine.ListResult, error) {
	return engine.ListResult{OK: true}, nil
}

func (e *zeroDirectStubEngine) GetItem(_ context.Context, scope engine.Scope, id string) (engine.Item, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if body, ok := e.commonDocs[id]; ok {
		return engine.Item{ID: id, DatasetID: scope.DatasetID, Content: body, MimeType: "text/markdown"}, nil
	}
	return engine.Item{ID: id, DatasetID: scope.DatasetID, Content: "# Gateway Document Body", MimeType: "text/markdown"}, nil
}

func (e *zeroDirectStubEngine) DeleteItem(_ context.Context, _ engine.Scope, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.commonDocs, id)
	return nil
}

func (e *zeroDirectStubEngine) Graph(_ context.Context, req engine.GraphRequest) (engine.GraphPayload, error) {
	return engine.GraphPayload{
		OK:    true,
		Nodes: []engine.GraphNode{{ID: "node-1", Label: "GatewayNode", Type: "Entity"}},
		Edges: []engine.GraphEdge{},
	}, nil
}

func (e *zeroDirectStubEngine) EnsureDataset(_ context.Context, name string) (engine.DatasetRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.codeDSID == "" {
		e.codeDSID = "11111111-2222-3333-4444-555555555555"
	}
	return engine.DatasetRecord{ID: e.codeDSID, Name: name}, nil
}

func (e *zeroDirectStubEngine) IndexCode(_ context.Context, req engine.CodeIndexRequest) (engine.CodeIndexResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.codeIndexCnt++
	return engine.CodeIndexResult{
		Status:         "completed",
		DatasetID:      req.DatasetID,
		PipelineRunID:  "code-run-1",
		ItemsProcessed: 1,
	}, nil
}

func (e *zeroDirectStubEngine) ProjectCodeActivity(_ context.Context, codeDatasetID string) (engine.CodeActivitySnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	status := "DATASET_PROCESSING_COMPLETED"
	if e.codeActive {
		status = "running"
	}
	return engine.CodeActivitySnapshot{
		CodeDatasetID: codeDatasetID,
		Active:        e.codeActive,
		Status:        status,
		RunID:         "code-run-prev",
	}, nil
}

func (e *zeroDirectStubEngine) ListPipelineRuns(_ context.Context, q engine.PipelineRunsQuery) ([]engine.PipelineRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]engine.PipelineRun, 0, len(e.runs))
	for _, r := range e.runs {
		if q.DatasetID != "" && r.DatasetID != q.DatasetID {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (e *zeroDirectStubEngine) HydrateSessionsFromCache(_ context.Context, refs []engine.SessionRef) (map[engine.SessionRef]engine.SessionCacheInfo, error) {
	out := map[engine.SessionRef]engine.SessionCacheInfo{}
	for _, ref := range refs {
		if h, ok := e.histories[ref]; ok {
			out[ref] = engine.SessionCacheInfo{Label: h.Label, MsgCount: len(h.QAs), ToolCalls: len(h.Traces)}
		}
	}
	return out, nil
}

func (e *zeroDirectStubEngine) ReadSessionHistory(_ context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	h := e.histories[engine.SessionRef{UserID: req.UserID, SessionID: req.SessionID}]
	return engine.SessionHistoryResult{
		Label:     h.Label,
		MsgCount:  len(h.QAs),
		ToolCalls: len(h.Traces),
		QAs:       h.QAs,
		Traces:    h.Traces,
	}, nil
}

func TestZeroDirectEngineEndToEndThroughGateway(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := domain.OpenStore(filepath.Join(dir, "memlane.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const token = "zero-direct-gateway-management-token-1234"
	tokenFile := filepath.Join(dir, "management.token")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}

	const (
		projectID = "proj-zero"
		projectDS = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		commonDS  = "99999999-8888-7777-6666-555555555555"
	)
	if err := store.UpsertProject(ctx, domain.Project{
		ID: projectID, Name: "ZeroDirect", DatasetID: projectDS,
		Aliases: []string{repoDir}, Status: "verified", CodeEnabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCommonConfig(ctx, domain.CommonConfig{
		ID: "common", Name: "Common", DatasetID: commonDS, Status: "verified",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertCatalogDoc(ctx, domain.CatalogDoc{
		DatasetID: projectDS, DocID: "doc-1", Kind: "knowledge", DocName: "Zero Direct Doc",
		DocLabel: "Zero Direct Doc", IsKnowledge: true, EffectiveDate: now.Format(time.DateOnly),
		CreatedAtNS: domain.FormatUTCNS(now), CreatedAtRaw: domain.FormatUTCNS(now), ContentHash: "h1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCatalogSession(ctx, domain.CatalogSession{
		DatasetID: projectDS, UserID: "u1", AgentID: "codex-1", SessionID: "sess-1",
		Status: "active", IsTopLevel: true, LastActivityAt: domain.FormatUTCNS(now),
	}); err != nil {
		t.Fatal(err)
	}

	stub := &zeroDirectStubEngine{
		runs: []engine.PipelineRun{{
			ID: "run-row-1", PipelineRunID: "run-1", DatasetID: projectDS,
			Kind: "pipeline", OperationName: "improve", Outcome: "succeeded",
			CreatedAt: now.Format(time.RFC3339),
		}},
		histories: map[engine.SessionRef]engine.SessionHistoryResult{
			{UserID: "u1", SessionID: "sess-1"}: {
				Label: "Gateway session title",
				QAs:   []engine.SessionQA{{ID: "qa-1", Question: "Q?", Answer: "A."}},
			},
		},
	}

	server, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          stub,
		ManagementToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}

	var bodyCalls atomic.Int32
	// Wrap gateway handler to fail immediately if ANY /api/v1/* path is ever requested.
	guardHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			t.Errorf("FORBIDDEN direct engine /api/v1/* request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/body") {
			bodyCalls.Add(1)
		}
		server.Handler().ServeHTTP(w, r)
	})

	const gwURL = "http://127.0.0.1:61299"
	httpClient := &http.Client{Transport: queueTestTransport(guardHandler.ServeHTTP)}
	cfg := Config{
		// Intentionally NO ServiceURL and NO TokenFile!
		ProjectControl: &ProjectControlConfig{URL: gwURL, ManagementTokenFile: tokenFile},
		Projects:       []Project{{ID: projectID, Name: "ZeroDirect", Path: repoDir, Dataset: projectDS, CodeEnabled: false}},
		Common:         []Project{{ID: "common", Name: "Common", Dataset: commonDS}},
	}
	client := Client{Config: cfg, HTTP: httpClient}

	// 1. Full toggleCode(true) -> ensureCodeDataset + PATCH /v1/projects/{id} + GET /v1/projects + indexProjectCode,
	//    followed by toggleCode(false) through real gateway.NewServer!
	appInst := New(cfg, nil)
	appInst.Client.HTTP = httpClient
	updates := make(chan func(), 64)
	appInst.Update = func(fn func()) { updates <- fn }
	appInst.syncSettingsTransport()
	appInst.RefreshProjectDirectory()
	drainReading(t, updates, func() bool { return appInst.access.Loaded })
	appInst.toggleCode(true)
	drainReading(t, updates, func() bool {
		return !appInst.access.Busy && !appInst.graphController.CodeBusy
	})
	if !appInst.Config.Projects[0].CodeEnabled || stub.codeIndexCnt != 1 {
		t.Fatalf("toggleCode(true) failed: enabled=%v codeDS=%q cnt=%d err=%q status=%q",
			appInst.Config.Projects[0].CodeEnabled, appInst.Config.Projects[0].CodeDataset,
			stub.codeIndexCnt, appInst.graphController.Error, appInst.graphController.CodeStatus)
	}
	codeDS := appInst.Config.Projects[0].CodeDataset
	if codeDS == "" || codeDS == projectDS {
		t.Fatalf("toggleCode(true) did not assign code dataset: %+v", appInst.Config.Projects[0])
	}
	appInst.toggleCode(false)
	drainReading(t, updates, func() bool {
		return !appInst.access.Busy
	})
	if appInst.Config.Projects[0].CodeEnabled {
		t.Fatalf("toggleCode(false) did not disable: err=%q status=%q", appInst.graphController.Error, appInst.graphController.CodeStatus)
	}
	// Re-enable for remaining code-activity/index checks.
	appInst.toggleCode(true)
	drainReading(t, updates, func() bool {
		return !appInst.access.Busy && !appInst.graphController.CodeBusy
	})
	if !appInst.Config.Projects[0].CodeEnabled || stub.codeIndexCnt != 2 {
		t.Fatalf("second toggleCode(true) failed: enabled=%v cnt=%d err=%q status=%q",
			appInst.Config.Projects[0].CodeEnabled, stub.codeIndexCnt, appInst.graphController.Error, appInst.graphController.CodeStatus)
	}
	cfg.Projects[0].CodeDataset = codeDS
	cfg.Projects[0].CodeEnabled = true
	client.Config = cfg

	// 2. Code activity query via gateway GET /v1/projects/{id}/code-activity.
	act, err := client.codeActivity(ctx, cfg.Projects[0])
	if err != nil || act.Active || act.Status != "DATASET_PROCESSING_COMPLETED" {
		t.Fatalf("codeActivity via gateway failed: act=%+v err=%v", act, err)
	}

	// 3. Code index trigger via gateway POST /v1/projects/{id}/code-index + active 409 check.
	idxRes, err := client.indexProject(ctx, cfg.Projects[0])
	if err != nil || idxRes.Status != "completed" || idxRes.Dataset != codeDS || stub.codeIndexCnt != 3 {
		t.Fatalf("indexProject via gateway failed: res=%+v cnt=%d err=%v", idxRes, stub.codeIndexCnt, err)
	}
	stub.codeActive = true
	if _, err := client.indexProject(ctx, cfg.Projects[0]); err != errOfficialCodeActive {
		t.Fatalf("active code index expected errOfficialCodeActive, got %v", err)
	}
	stub.codeActive = false

	// 4. Overview activity via gateway GET /v1/activity/pipeline-runs.
	events, _, err := client.overviewActivity(ctx, 10)
	if err != nil || len(events) != 1 || events[0].Project != "ZeroDirect" {
		t.Fatalf("overviewActivity via gateway failed: events=%+v err=%v", events, err)
	}

	// 5. Common memory create, update & delete via gateway /api/common.
	draft := CommonDraft{Title: "Gateway Pref", Body: "Always use gateway.", Category: "preference"}
	receipt, err := client.writeCommon(ctx, cfg.Common[0], draft, nil)
	if err != nil || !receipt.Gateway || receipt.DataID == "" || receipt.DatasetID != commonDS {
		t.Fatalf("writeCommon create via gateway failed: receipt=%+v err=%v", receipt, err)
	}
	createdDoc := Document{ID: receipt.DataID, Dataset: commonDS, Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
	draft.Body = "Updated via gateway."
	receipt2, err := client.writeCommon(ctx, cfg.Common[0], draft, &createdDoc)
	if err != nil || !receipt2.Gateway || receipt2.DataID == "" || receipt2.DatasetID != commonDS {
		t.Fatalf("writeCommon update via gateway failed: receipt=%+v err=%v", receipt2, err)
	}
	createdDoc.ID = receipt2.DataID
	if err := client.deleteCommon(ctx, cfg.Common[0], createdDoc); err != nil {
		t.Fatalf("deleteCommon via gateway failed: %v", err)
	}

	// 6. Session history via gateway GET /v1/raw-records/session-history.
	sessItem := Item{ID: "sess-1", Dataset: projectDS, Kind: "会话", Session: &Session{ID: "sess-1", Dataset: projectDS, User: "u1"}}
	loadedSess, sessPage, err := client.readSessionHistory(ctx, sessItem)
	if err != nil || sessPage == nil || loadedSess.Title != "Gateway session title" || len(sessPage.QAs) != 1 {
		t.Fatalf("readSessionHistory via gateway failed: item=%+v page=%+v err=%v", loadedSess, sessPage, err)
	}

	// 7. Document lookup & reading via gateway: must classify kind=knowledge as "知识" and reuse content
	//    through selectItem without issuing any redundant /body fetch.
	beforeBodyCalls := bodyCalls.Load()
	docItem, err := client.documentByID(ctx, projectDS, "doc-1")
	if err != nil || docItem.ID != "doc-1" || docItem.Kind != "知识" || docItem.Body != "# Gateway Document Body" {
		t.Fatalf("documentByID via gateway failed: item=%+v err=%v", docItem, err)
	}
	appInst.page, appInst.kind = "知识", "知识"
	detailPane := appInst.state()
	appInst.selectItem(detailPane, docItem)
	drainReading(t, updates, func() bool { return !detailPane.Reading })
	if detailPane.Selected == nil || detailPane.Selected.ID != "doc-1" || detailPane.Selected.Body != "# Gateway Document Body" {
		t.Fatalf("selectItem failed to reuse BodyLoaded item: selected=%+v err=%q", detailPane.Selected, detailPane.Error)
	}
	if bodyCalls.Load() != beforeBodyCalls {
		t.Fatalf("documentByID + selectItem issued redundant /body fetch: calls=%d", bodyCalls.Load()-beforeBodyCalls)
	}

	// 8. Search via gateway POST /api/search: verify target semantic snippet & source doc are returned.
	searchRows, err := client.search(ctx, cfg.Projects, "Zero")
	if err != nil {
		t.Fatalf("search via gateway failed: err=%v", err)
	}
	foundSemantic := false
	for _, row := range searchRows {
		if row.SourceDocument == "doc-1" && strings.Contains(row.Body, "gateway semantic snippet for Zero") {
			foundSemantic = true
			break
		}
	}
	if !foundSemantic {
		t.Fatalf("search via gateway missing expected semantic snippet hit: rows=%+v", searchRows)
	}

	// 9. Graph loading via gateway GET /api/graph.
	control, _ := client.controlClient()
	identity, err := readingIdentity(client)
	if err != nil {
		t.Fatal(err)
	}
	apiClient, err := newAppAPIClient(control, identity)
	if err != nil {
		t.Fatal(err)
	}
	graphResp, err := apiClient.Graph(ctx, projectID, projectDS, "")
	if err != nil || !graphResp.OK || len(graphResp.Nodes) != 1 || graphResp.Nodes[0].ID != "node-1" {
		t.Fatalf("graph via gateway failed: resp=%+v err=%v", graphResp, err)
	}
	// Verify graph controller normalizes the gateway response cleanly without any RawFetcher.
	if _, err := appgraph.NormalizeGraphResponse(graphResp); err != nil {
		t.Fatalf("NormalizeGraphResponse failed: err=%v", err)
	}
	_ = shell.ProjectRef{ID: projectID, DatasetID: projectDS}
	_ = api.GraphResponse(graphResp)
	_ = settings.Config(cfg)
}

func TestAppProductionCodeZeroDirectEngineV1Literals(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, unqErr := strconv.Unquote(lit.Value)
			if unqErr != nil {
				val = lit.Value
			}
			if strings.Contains(val, "/api/v1/") {
				pos := fset.Position(lit.Pos())
				t.Errorf("production Go file contains forbidden direct engine route /api/v1/: %s:%d: %s", path, pos.Line, lit.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Silence unused json import if needed.
	var _ json.RawMessage
}

func TestR1ReviewRegressionFixes(t *testing.T) {
	t.Run("pure_gateway_token_rotation_and_dual_url_legacy_token_removal", func(t *testing.T) {
		dir := t.TempDir()
		mgmtToken := filepath.Join(dir, "mgmt.token")
		legacyToken := filepath.Join(dir, "legacy.token")
		if err := os.WriteFile(mgmtToken, []byte("management-token-initial-0123456789abcdef"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacyToken, []byte("legacy-token-value"), 0o600); err != nil {
			t.Fatal(err)
		}

		// 1. Pure gateway: rotating ManagementTokenFile triggers pollViewReadingIdentity withdrawal.
		a := New(Config{
			ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:61301", ManagementTokenFile: mgmtToken},
			Projects:       []Project{{ID: "p", Dataset: "ds"}},
		}, nil)
		updates := make(chan func(), 16)
		a.Update = func(fn func()) { updates <- fn }
		a.page = "知识"
		s := a.state()
		s.Loaded = true
		item := Item{ID: "k1", Dataset: "ds", Body: "secret body", BodyLoaded: true}
		s.Items = []Item{item}
		s.Selected = &item

		if err := os.WriteFile(mgmtToken, []byte("management-token-rotated-9876543210fedcba"), 0o600); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(2 * time.Second)
		_ = os.Chtimes(mgmtToken, future, future)
		a.nextViewIdentityCheck = time.Time{}
		a.pollViewReadingIdentity(time.Now())
		drainReading(t, updates, func() bool { return s.Selected == nil && !s.Loaded })

		// 2. Dual URL: removing legacy TokenFile does NOT break reading identity when ProjectControl is healthy.
		b := New(Config{
			ServiceURL:     "http://127.0.0.1:8000",
			TokenFile:      legacyToken,
			ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:61301", ManagementTokenFile: mgmtToken},
		}, nil)
		idBefore, err := readingIdentity(b.Client)
		if err != nil || idBefore == "" {
			t.Fatalf("dual-URL readingIdentity failed before legacy removal: %q %v", idBefore, err)
		}
		if err := os.Remove(legacyToken); err != nil {
			t.Fatal(err)
		}
		idAfter, err := readingIdentity(b.Client)
		if err != nil || idAfter != idBefore {
			t.Fatalf("removing unused legacy TokenFile broke dual-URL readingIdentity: before=%q after=%q err=%v", idBefore, idAfter, err)
		}
	})

	t.Run("base_identity_matches_across_list_search_and_mark_read", func(t *testing.T) {
		var listID, searchID string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/knowledge":
				listID = r.Header.Get("X-Memlane-Reading-Identity")
				_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "p", "dataset_id": "ds", "items": []any{}})
			case "/api/search":
				searchID = r.Header.Get("X-Memlane-Reading-Identity")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": []any{}, "semantic_hits": []any{}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		c := Client{
			Config:         Config{ProjectControl: readingControlConfig(t, server.URL)},
			PolicyRevision: "rev-42",
		}
		full, base, err := readingIdentityPair(c)
		if err != nil || full == base {
			t.Fatalf("expected full != base with PolicyRevision: full=%q base=%q err=%v", full, base, err)
		}
		if _, _, err := c.documents(context.Background(), Project{ID: "p", Dataset: "ds"}, 0, false); err != nil {
			t.Fatal(err)
		}
		if _, err := c.search(context.Background(), []Project{{ID: "p", Dataset: "ds"}}, "q"); err != nil {
			t.Fatal(err)
		}
		if listID != base || searchID != base {
			t.Fatalf("list/search sent PolicyRevision-polluted identity: list=%q search=%q want base=%q", listID, searchID, base)
		}
	})

	t.Run("common_404_never_double_sends_write_or_delete", func(t *testing.T) {
		var postCalls, deleteCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				postCalls.Add(1)
			}
			if r.Method == http.MethodDelete {
				deleteCalls.Add(1)
			}
			http.NotFound(w, r)
		}))
		defer server.Close()

		ctrl := readingControlConfig(t, server.URL)
		client := Client{Config: Config{ProjectControl: ctrl}}
		draft := CommonDraft{Title: "T", Body: "B", Category: "preference"}
		if _, err := saveCommonDraft(context.Background(), client, Project{Dataset: "common"}, draft, nil, [32]byte{}); err == nil {
			t.Fatal("expected 404 save error")
		}
		if postCalls.Load() != 1 {
			t.Fatalf("404 save sent %d POST requests, want 1", postCalls.Load())
		}

		a := commonUIFixture()
		a.Config.ProjectControl = ctrl
		a.Client.Config = a.Config
		a.readingIdentity, _ = readingIdentity(a.Client)
		updates := make(chan func(), 16)
		a.Update = func(fn func()) { updates <- fn }
		d := Document{ID: "doc-404", Dataset: "common", Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"}}
		a.deleteCommonKnowledge(d)
		drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
		if deleteCalls.Load() != 1 {
			t.Fatalf("404 delete sent %d DELETE requests, want 1", deleteCalls.Load())
		}
	})

	t.Run("toggleCode_without_gateway_fails_closed_without_mutating_projects", func(t *testing.T) {
		a := New(Config{Projects: []Project{{ID: "p", Path: "/tmp/repo", Dataset: "ds", CodeEnabled: false}}}, nil)
		a.toggleCode(true)
		if a.graphController.Error != "本地网关尚未配置" || a.Config.Projects[0].CodeEnabled {
			t.Fatalf("toggleCode(true) without gateway mutated state: err=%q enabled=%v", a.graphController.Error, a.Config.Projects[0].CodeEnabled)
		}
		a.Config.Projects[0].CodeEnabled = true
		a.toggleCode(false)
		if a.graphController.Error != "本地网关尚未配置" || !a.Config.Projects[0].CodeEnabled {
			t.Fatalf("toggleCode(false) without gateway mutated state: err=%q enabled=%v", a.graphController.Error, a.Config.Projects[0].CodeEnabled)
		}
	})

	t.Run("triggerProjectCodeIndexV2_preserves_context_canceled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		ctrl := ProjectControlClient{Config: *readingControlConfig(t, server.URL)}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := ctrl.triggerProjectCodeIndexV2(ctx, "p", "/tmp/repo")
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("expected wrapped context.Canceled, got %v", err)
		}
	})

	t.Run("empty_recall_ledger_is_valid_zero_not_error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"started_at":"","items":[]}`))
		}))
		defer server.Close()
		ctrl := ProjectControlClient{Config: *readingControlConfig(t, server.URL)}
		counts, err := ctrl.knowledgeRecallCounts(context.Background(), "ds")
		if err != nil || counts == nil || len(counts.Items) != 0 {
			t.Fatalf("empty recall ledger should succeed: counts=%+v err=%v", counts, err)
		}
		label, tip := knowledgeCardRecall(Item{ID: "k1", Dataset: "ds", Kind: "知识", RecallStats: &KnowledgeRecallStats{Count: 0, StartedAt: ""}})
		if label != "召回 0" || strings.Contains(tip, "自 ") || !strings.Contains(tip, "已记录的检索命中次数") {
			t.Fatalf("empty StartedAt recall label/tip unexpected: label=%q tip=%q", label, tip)
		}
	})
}

func TestR3ReviewConvergenceFixes(t *testing.T) {
	t.Run("clearing_management_token_path_triggers_poll_withdrawal", func(t *testing.T) {
		dir := t.TempDir()
		mgmtToken := filepath.Join(dir, "mgmt.token")
		if err := os.WriteFile(mgmtToken, []byte("management-token-initial-0123456789abcdef"), 0o600); err != nil {
			t.Fatal(err)
		}
		a := New(Config{
			ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:61302", ManagementTokenFile: mgmtToken},
			Projects:       []Project{{ID: "p", Dataset: "ds"}},
		}, nil)
		updates := make(chan func(), 16)
		a.Update = func(fn func()) { updates <- fn }
		a.page = "知识"
		s := a.state()
		s.Loaded = true
		item := Item{ID: "k1", Dataset: "ds", Body: "secret body", BodyLoaded: true}
		s.Items = []Item{item}
		s.Selected = &item

		// Clear ManagementTokenFile path in config while window is open.
		a.Client.Config.ProjectControl.ManagementTokenFile = ""
		if _, err := readingIdentity(a.Client); err == nil {
			t.Fatal("readingIdentity must fail when ProjectControl.ManagementTokenFile is cleared")
		}
		a.nextViewIdentityCheck = time.Time{}
		a.pollViewReadingIdentity(time.Now())
		drainReading(t, updates, func() bool { return s.Selected == nil && !s.Loaded })
	})

	t.Run("stale_directory_refresh_cannot_rollback_SetCodeIndexEnabled", func(t *testing.T) {
		var getCount atomic.Int32
		staleEntered := make(chan struct{})
		releaseStale := make(chan struct{})
		var codeEnabled atomic.Bool

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
				n := getCount.Add(1)
				if n == 2 {
					close(staleEntered)
					<-releaseStale
					// Return stale directory with CodeEnabled=false.
					_ = json.NewEncoder(w).Encode(AccessDirectory{
						Revision: 1,
						Projects: []AccessProject{{ID: "p", Name: "P", Dataset: "ds", Status: "verified", CodeDataset: "code-ds", CodeEnabled: false, Aliases: []string{"/tmp/repo"}}},
					})
					return
				}
				_ = json.NewEncoder(w).Encode(AccessDirectory{
					Revision: 2,
					Projects: []AccessProject{{ID: "p", Name: "P", Dataset: "ds", Status: "verified", CodeDataset: "code-ds", CodeEnabled: codeEnabled.Load(), Aliases: []string{"/tmp/repo"}}},
				})
			case r.Method == http.MethodPatch && r.URL.Path == "/v1/projects/p":
				codeEnabled.Store(true)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p", "code_enabled": true, "code_dataset_id": "code-ds"})
			case r.URL.Path == "/v1/projects/p/code-activity":
				_ = json.NewEncoder(w).Encode(codeActivityV2Response{ProjectID: "p", CodeDatasetID: "code-ds", Active: false, Status: "completed"})
			case r.URL.Path == "/v1/projects/p/code-index":
				_ = json.NewEncoder(w).Encode(codeIndexResult{Status: "completed", Dataset: "code-ds", Run: "r1", Items: 1})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		a := New(Config{
			ProjectControl: readingControlConfig(t, server.URL),
			Projects:       []Project{{ID: "p", Name: "P", Path: "/tmp/repo", Dataset: "ds", CodeDataset: "code-ds", CodeEnabled: false}},
		}, nil)
		updates := make(chan func(), 32)
		a.Update = func(fn func()) { updates <- fn }
		a.syncSettingsTransport()

		// 1. Initial directory load.
		a.RefreshProjectDirectory()
		drainReading(t, updates, func() bool { return a.access.Loaded })

		// 2. Start a slow RefreshProjectDirectory (GET #2) that will finish late.
		a.access.Next = time.Time{}
		a.RefreshProjectDirectory()
		<-staleEntered

		// 3. Toggle code enabled via SetCodeIndexEnabled (PATCH + GET #3).
		a.toggleCode(true)
		drainReading(t, updates, func() bool { return !a.access.Busy && !a.graphController.CodeBusy && a.Config.Projects[0].CodeEnabled })

		// 4. Release stale GET #2 and drain its queued callback; CodeEnabled must remain true!
		close(releaseStale)
		time.Sleep(30 * time.Millisecond)
		for len(updates) > 0 {
			(<-updates)()
		}
		if !a.Config.Projects[0].CodeEnabled {
			t.Fatal("stale RefreshProjectDirectory rolled back CodeEnabled to false")
		}
	})

	t.Run("documentInfoByID_preserves_pitfall_category_matching_list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/datasets/ds/documents/pit-1" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":          "pit-1",
					"datasetId":   "ds",
					"kind":        "knowledge",
					"lesson_type": "pitfall",
					"category":    "踩坑经验",
					"state_key":   "db/wal",
					"title":       "Avoid unbuffered WAL writes",
					"content":     "Avoid unbuffered WAL writes",
				})
				return
			}
			http.NotFound(w, r)
		}))
		defer server.Close()

		c := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
		lookupItem, err := c.documentInfoByID(context.Background(), "ds", "pit-1")
		if err != nil {
			t.Fatal(err)
		}
		listItem := (v2KnowledgeItem{
			DocID: "pit-1", DatasetID: "ds", Kind: "knowledge",
			LessonType: "pitfall", Category: "踩坑经验", StateKey: "db/wal",
			Title: "Avoid unbuffered WAL writes",
		}).toItem("")
		if got, want := knowledgeCategoryLabel(lookupItem), knowledgeCategoryLabel(listItem); got != "踩坑经验" || got != want {
			t.Fatalf("documentInfoByID category=%q, list category=%q, want 踩坑经验", got, want)
		}
	})

	t.Run("single_doc_knowledgeRecall_accepts_empty_ledger_zero_and_rejects_nonzero_without_started_at", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/knowledge/recalls":
				if r.URL.Query().Get("document_id") == "bad-nonzero" {
					_, _ = w.Write([]byte(`{"count":2,"started_at":""}`))
					return
				}
				_, _ = w.Write([]byte(`{"count":0,"started_at":""}`))
			case "/api/knowledge/ds/k-zero/body":
				_ = json.NewEncoder(w).Encode(map[string]any{"dataset_id": "ds", "document_id": "k-zero", "content": "# Zero recall doc"})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		ctrlCfg := readingControlConfig(t, server.URL)
		ctrl := ProjectControlClient{Config: *ctrlCfg}
		if _, err := ctrl.knowledgeRecall(context.Background(), Item{ID: "bad-nonzero", Dataset: "ds", Kind: "知识"}); err == nil {
			t.Fatal("expected error for count > 0 with empty started_at")
		}

		a := New(Config{ProjectControl: ctrlCfg, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
		updates := make(chan func(), 16)
		a.Update = func(fn func()) { updates <- fn }
		a.page, a.kind = "知识", "知识"
		s := a.state()
		doc := Document{ID: "k-zero", Dataset: "ds", Name: "zero.md"}
		a.selectItem(s, Item{ID: "k-zero", Dataset: "ds", Kind: "知识", Document: &doc})
		drainReading(t, updates, func() bool { return !s.Reading })
		if s.Selected == nil || s.Selected.RecallStats == nil || s.Selected.RecallStats.Count != 0 {
			t.Fatalf("expected valid zero RecallStats on empty ledger, got %+v", s.Selected)
		}
		if label, tip := knowledgeCardRecall(*s.Selected); label != "召回 0" || !strings.Contains(tip, "已记录的检索命中次数") {
			t.Fatalf("unexpected recall label/tip on empty ledger: label=%q tip=%q", label, tip)
		}
	})
}
