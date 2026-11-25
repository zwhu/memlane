package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const testManagement = "management-token-for-gateway-tests"
const testAgent = "registered-agent-key"
const readIdentity = "desktop-reader"

type fakeEngine struct {
	mu                                                  sync.Mutex
	calls                                               []string
	ingest                                              []engine.IngestRequest
	extract                                             []engine.ExtractRequest
	search                                              []engine.SearchRequest
	remaining                                           time.Duration
	probe                                               []engine.ProbeRequest
	apply                                               []engine.ApplyModelRequest
	local                                               []engine.LocalBodyRef
	graph                                               []engine.GraphRequest
	hits                                                []engine.SearchHit
	probeReply                                          engine.ProbeResult
	ingestErr, extractErr, syncErr, applyErr, searchErr error
	modelDigest                                         string
	documents                                           map[string]engine.Item
	common                                              map[string]domain.CatalogDoc
	deleted                                             map[string]string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{modelDigest: "live-digest", documents: make(map[string]engine.Item),
		common: make(map[string]domain.CatalogDoc), deleted: make(map[string]string),
		probeReply: engine.ProbeResult{Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true, StructuredMode: "json_schema"}}
}

func (f *fakeEngine) Health(context.Context) (engine.HealthStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.HealthStatus{OK: true, Engine: "fake", Version: "2", Mode: engine.HealthModeReady, ModelDigest: f.modelDigest,
		Capabilities: engine.Capabilities{ToolGraph: true, ModelHandoff: true, LanguageEgress: true}}, nil
}

func (f *fakeEngine) Ingest(_ context.Context, in engine.IngestRequest) (engine.IngestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ingest")
	f.ingest = append(f.ingest, in)
	if f.ingestErr != nil {
		return engine.IngestResult{}, f.ingestErr
	}
	id := "doc-" + in.IdempotencyKey
	if in.Kind == engine.IngestKindDocument {
		content, ok := in.Content.(string)
		if !ok {
			return engine.IngestResult{}, errors.New("expected text document")
		}
		title, _ := in.SourceMeta.ExternalMetadata["title"].(string)
		category, _ := in.SourceMeta.ExternalMetadata["category"].(string)
		f.documents[id] = engine.Item{ID: id, DatasetID: in.Scope.DatasetID, Content: content, Label: title, MimeType: "text/plain"}
		f.common[id] = domain.CatalogDoc{DatasetID: in.Scope.DatasetID, DocID: id, Kind: "common", IsKnowledge: true,
			CommonCategory: category, DocLabel: title, EffectiveDate: time.Now().Format(time.DateOnly), SortTSDescKey: -time.Now().UnixNano()}
	}
	return engine.IngestResult{OK: true, EntryID: id, DocumentID: id}, nil
}

func (f *fakeEngine) Extract(_ context.Context, in engine.ExtractRequest) (engine.ExtractResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "extract")
	f.extract = append(f.extract, in)
	return engine.ExtractResult{OK: f.extractErr == nil}, f.extractErr
}

func (f *fakeEngine) Search(ctx context.Context, in engine.SearchRequest) (engine.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.search = append(f.search, in)
	if d, ok := ctx.Deadline(); ok {
		f.remaining = time.Until(d)
	}
	return engine.SearchResult{OK: f.searchErr == nil, Hits: slices.Clone(f.hits)}, f.searchErr
}

func (f *fakeEngine) ListItems(context.Context, engine.ListRequest) (engine.ListResult, error) {
	return engine.ListResult{OK: true}, nil
}

func (f *fakeEngine) GetItem(_ context.Context, scope engine.Scope, id string) (engine.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.documents[id]
	if !ok || item.DatasetID != scope.DatasetID {
		return engine.Item{}, engine.ErrNotFound
	}
	return item, nil
}

func (f *fakeEngine) DeleteItem(_ context.Context, scope engine.Scope, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[id] = scope.DatasetID
	delete(f.documents, id)
	delete(f.common, id)
	return nil
}

func (f *fakeEngine) Graph(_ context.Context, in engine.GraphRequest) (engine.GraphPayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.graph = append(f.graph, in)
	return engine.GraphPayload{OK: true, Nodes: []engine.GraphNode{{ID: "node-a"}}}, nil
}

func (f *fakeEngine) ApplyModel(_ context.Context, in engine.ApplyModelRequest) (engine.ApplyModelResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apply = append(f.apply, in)
	if f.applyErr != nil {
		return engine.ApplyModelResult{}, f.applyErr
	}
	key, _ := in.GenerationConfig["api_key"].(string)
	return engine.ApplyModelResult{OK: true, Applied: true, OperationID: in.OperationID, ModelDigest: key}, nil
}

func (f *fakeEngine) ModelStatus(context.Context) (engine.ModelStatusResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.ModelStatusResult{OK: true, ModelDigest: f.modelDigest}, nil
}

func (f *fakeEngine) ProbeModel(_ context.Context, in engine.ProbeRequest) (engine.ProbeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probe = append(f.probe, in)
	out := f.probeReply
	out.Details = map[string]any{"api_key": in.Environment["LLM_API_KEY"]}
	out.ErrorCode = in.Environment["LLM_API_KEY"]
	return out, nil
}

func (f *fakeEngine) SyncCatalog(ctx context.Context, store *domain.Store) (*engine.SyncStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "sync")
	if f.syncErr != nil {
		return nil, f.syncErr
	}
	for _, d := range f.common {
		if err := store.UpsertCatalogDoc(ctx, d); err != nil {
			return nil, err
		}
	}
	for id, ds := range f.deleted {
		if _, err := store.DB().ExecContext(ctx, "DELETE FROM catalog_docs WHERE dataset_id=? AND doc_id=?", ds, id); err != nil {
			return nil, err
		}
	}
	return &engine.SyncStats{}, nil
}

func (f *fakeEngine) ReadLocalBody(_ context.Context, in engine.LocalBodyRef) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.local = append(f.local, in)
	return "projector body", nil
}

func openTestStore(t *testing.T) *domain.Store {
	t.Helper()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "store.sqlite3"))
	must(t, err)
	t.Cleanup(func() { must(t, store.Close()) })
	return store
}

func testServer(t *testing.T, configure func(*Config)) (*Server, *domain.Store, *fakeEngine) {
	t.Helper()
	ctx := context.Background()
	store, fake := openTestStore(t), newFakeEngine()
	must(t, store.UpsertProject(ctx, domain.Project{ID: "p", Name: "Project", DatasetID: "ds", AutoCapture: true, Aliases: []string{t.TempDir()}}))
	must(t, store.UpsertCommonConfig(ctx, domain.CommonConfig{ID: "general", Name: "Common", DatasetID: "common"}))
	must(t, store.ConnectAgent(ctx, domain.Agent{ID: "agent", Name: "agent", Kind: "codex"}))
	cfg := Config{Store: store, Engine: fake, ManagementToken: testManagement, AgentTokens: map[string]string{"agent": testAgent},
		InboxPollInterval: 10 * time.Millisecond}
	if configure != nil {
		configure(&cfg)
	}
	s, err := NewServer(cfg)
	must(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		must(t, s.Shutdown(ctx))
	})
	return s, store, fake
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func request(t *testing.T, s *Server, method, path, body string, headers map[string]string, want int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s: got %d want %d (%s)", method, path, w.Code, want, w.Body)
	}
	return w
}

func manage(t *testing.T, s *Server, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, s, method, path, body, map[string]string{"Authorization": "Bearer " + testManagement,
		"X-Memlane-Reading-Identity": readIdentity}, want)
}

func decodeResponse[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	must(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

func requireCode(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	if got := decodeResponse[map[string]any](t, w)["error"]; got != code {
		t.Fatalf("error code = %v, want %s", got, code)
	}
}

func seedDoc(t *testing.T, store *domain.Store, ds, id, kind, category string, at time.Time, key int64) {
	t.Helper()
	doc := domain.CatalogDoc{DatasetID: ds, DocID: id, CogneeRowID: -key, Kind: kind, Category: category, IsKnowledge: kind != "raw_doc",
		DocLabel: "match " + id, CreatedAtRaw: domain.FormatUTCNS(at), CreatedAtNS: domain.FormatUTCNS(at), EffectiveDate: at.In(time.Local).Format(time.DateOnly),
		DayDescKey: key, SortTSDescKey: key, RawDataLocation: "owned://" + id}
	if kind == "common" {
		doc.CommonCategory = category
	}
	must(t, store.UpsertCatalogDoc(context.Background(), doc))
}

func countRows(t *testing.T, store *domain.Store, table string) int {
	t.Helper()
	var n int
	must(t, store.DB().QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n))
	return n
}
