package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type commonUpdateTestEngine struct {
	engine.MemoryEngine
	body           map[string]string
	getErr         map[string]error
	ingestIDs      []string
	ingestKeys     []string
	ingestCalls    int
	deleteErr      map[string]error
	deleteRemoves  map[string]bool
	deleted        []string
	ingestStoreDoc bool
}

func (e *commonUpdateTestEngine) GetItem(_ context.Context, scope engine.Scope, id string) (engine.Item, error) {
	if err, ok := e.getErr[id]; ok {
		return engine.Item{}, err
	}
	b, ok := e.body[id]
	if !ok {
		return engine.Item{}, engine.ErrNotFound
	}
	return engine.Item{ID: id, DatasetID: scope.DatasetID, Content: b, MimeType: "text/markdown"}, nil
}

func (e *commonUpdateTestEngine) Ingest(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	e.ingestKeys = append(e.ingestKeys, req.IdempotencyKey)
	idx := e.ingestCalls
	e.ingestCalls++
	id := "new-doc"
	if idx < len(e.ingestIDs) {
		id = e.ingestIDs[idx]
	}
	if e.ingestStoreDoc {
		if e.body == nil {
			e.body = map[string]string{}
		}
		if text, ok := req.Content.(string); ok {
			e.body[id] = text
		}
	}
	return engine.IngestResult{OK: true, DocumentID: id}, nil
}

func (e *commonUpdateTestEngine) DeleteItem(_ context.Context, _ engine.Scope, id string) error {
	e.deleted = append(e.deleted, id)
	if e.deleteRemoves[id] {
		delete(e.body, id)
	}
	if err, ok := e.deleteErr[id]; ok {
		return err
	}
	delete(e.body, id)
	return nil
}

type fakeCatalogProjector struct {
	calls int
	err   error
}

func (p *fakeCatalogProjector) SyncCatalog(_ context.Context, _ *domain.Store) (*engine.SyncStats, error) {
	p.calls++
	return &engine.SyncStats{}, p.err
}

func (p *fakeCatalogProjector) ReadLocalBody(_ context.Context, _ engine.LocalBodyRef) (string, error) {
	return "", engine.ErrNotFound
}

func commonUpdateTestServer(t *testing.T, eng engine.MemoryEngine, proj engine.CatalogProjector) *Server {
	t.Helper()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "common.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertCommonConfig(t.Context(), domain.CommonConfig{ID: "common", Name: "Common", DatasetID: "common-ds"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCatalogDoc(t.Context(), domain.CatalogDoc{
		DatasetID: "common-ds", DocID: "old-doc", Kind: "common", IsKnowledge: true,
		CommonCategory: "experience", DocLabel: "Old Title",
	}); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{Store: store, Engine: eng, Projector: proj, ManagementToken: strings.Repeat("m", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func patchCommonDoc(srv *Server, doc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/common/"+doc, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestSaveCommonUpdateDeleteOldFailureRollsBackNewDoc(t *testing.T) {
	// Only an explicit 4xx engine rejection (proving old was not deleted) rolls back new-doc.
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"new-doc"},
		ingestStoreDoc: true,
		deleteErr:      map[string]error{"old-doc": &engine.EngineError{StatusCode: http.StatusForbidden, Code: "forbidden"}},
	}
	proj := &fakeCatalogProjector{}
	srv := commonUpdateTestServer(t, eng, proj)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Fatalf("expected error status when deleting old doc is rejected, got %d: %s", rec.Code, rec.Body)
	}
	if len(eng.deleted) != 2 || eng.deleted[0] != "old-doc" || eng.deleted[1] != "new-doc" {
		t.Fatalf("expected rollback deletion of new-doc after explicit 4xx rejection, got %v", eng.deleted)
	}
	if proj.calls != 1 {
		t.Fatalf("expected SyncCatalog on rollback, got %d calls", proj.calls)
	}
}

func TestSaveCommonUpdatePartialFailureWhenRollbackAlsoFails(t *testing.T) {
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"new-doc"},
		ingestStoreDoc: true,
		deleteErr: map[string]error{
			"old-doc": engine.ErrForbidden,
			"new-doc": errors.New("engine rollback new failed"),
		},
	}
	proj := &fakeCatalogProjector{}
	srv := commonUpdateTestServer(t, eng, proj)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on double failure, got %d: %s", rec.Code, rec.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v (%s)", err, rec.Body)
	}
	if payload["error"] != "common_update_partial_failure" ||
		payload["new_document_id"] != "new-doc" ||
		payload["old_document_id"] != "old-doc" || proj.calls != 1 {
		t.Fatalf("unexpected partial failure payload=%+v syncCalls=%d", payload, proj.calls)
	}
}

func TestSaveCommonUpdateDeleteTimeoutOldStillReadableNeverRollsBackNewDoc(t *testing.T) {
	// R1-01 P0 (Round 3): Even if GetItem(old) still sees old-doc right after a DELETE
	// timeout/5xx, the in-flight DELETE may commit later. Gateway MUST NOT delete new-doc!
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"new-doc"},
		ingestStoreDoc: true,
		deleteErr:      map[string]error{"old-doc": context.DeadlineExceeded},
	}
	proj := &fakeCatalogProjector{}
	srv := commonUpdateTestServer(t, eng, proj)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 common_update_outcome_unknown, got %d: %s", rec.Code, rec.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "common_update_outcome_unknown" || len(eng.deleted) != 1 || proj.calls != 1 {
		t.Fatalf("must preserve new-doc and sync catalog on delete timeout: payload=%+v deleted=%v syncCalls=%d", payload, eng.deleted, proj.calls)
	}
	if _, exists := eng.body["new-doc"]; !exists {
		t.Fatal("new-doc must be preserved when old-doc delete outcome is unknown")
	}
}

func TestSaveCommonUpdateGetNewDocFailureSyncsCatalog(t *testing.T) {
	// R2-01 / R1-03: If GetItem(new-doc) fails transiently after Ingest, sync catalog and do not delete old-doc.
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"new-doc"},
		ingestStoreDoc: true,
		getErr:         map[string]error{"new-doc": engine.ErrUnavailable},
	}
	proj := &fakeCatalogProjector{}
	srv := commonUpdateTestServer(t, eng, proj)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Fatalf("expected error status when GetItem(new-doc) fails, got %d", rec.Code)
	}
	if len(eng.deleted) != 0 || proj.calls != 1 {
		t.Fatalf("expected 0 deletes and 1 SyncCatalog call, deleted=%v syncCalls=%d", eng.deleted, proj.calls)
	}
}

func TestSaveCommonUpdateDeleteTimeoutAfterEngineDeletePreservesNewDoc(t *testing.T) {
	// R1-01 P0: DeleteItem(old) actually deleted old-doc in engine, then returned a timeout error.
	// Gateway must verify old-doc is gone and preserve new-doc (NO rollback!).
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"new-doc"},
		ingestStoreDoc: true,
		deleteRemoves:  map[string]bool{"old-doc": true},
		deleteErr:      map[string]error{"old-doc": context.DeadlineExceeded},
	}
	proj := &fakeCatalogProjector{}
	srv := commonUpdateTestServer(t, eng, proj)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when old-doc was actually deleted despite timeout, got %d: %s", rec.Code, rec.Body)
	}
	if len(eng.deleted) != 1 || eng.deleted[0] != "old-doc" {
		t.Fatalf("must NOT roll back new-doc when old-doc is already gone, deleted=%v", eng.deleted)
	}
	if _, exists := eng.body["new-doc"]; !exists {
		t.Fatal("new-doc was lost")
	}
}

func TestSaveCommonUpdateOutcomeUnknownPreservesNewDocAndSyncsCatalog(t *testing.T) {
	// R1-01 P0 + R1-03 P1: DeleteItem(old) fails AND GetItem(old) also fails transiently.
	// Gateway must NOT blind-delete new-doc, must sync catalog, and return 502 common_update_outcome_unknown.
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"new-doc"},
		ingestStoreDoc: true,
		deleteErr:      map[string]error{"old-doc": errors.New("connection reset")},
		getErr:         map[string]error{},
	}
	// Trigger getErr on old-doc only after initial loadDocument succeeds by wrapping GetItem.
	wrapper := &transientOldCheckEngine{commonUpdateTestEngine: eng}
	proj := &fakeCatalogProjector{}
	srv := commonUpdateTestServer(t, wrapper, proj)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on outcome unknown, got %d: %s", rec.Code, rec.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "common_update_outcome_unknown" || len(eng.deleted) != 1 || proj.calls != 1 {
		t.Fatalf("unexpected outcome unknown response: payload=%+v deleted=%v syncCalls=%d", payload, eng.deleted, proj.calls)
	}
}

type transientOldCheckEngine struct {
	*commonUpdateTestEngine
	oldChecks int
}

func (e *transientOldCheckEngine) GetItem(ctx context.Context, scope engine.Scope, id string) (engine.Item, error) {
	if id == "old-doc" {
		e.oldChecks++
		if e.oldChecks > 1 {
			return engine.Item{}, engine.ErrUnavailable
		}
	}
	return e.commonUpdateTestEngine.GetItem(ctx, scope, id)
}

func TestSaveCommonUpdateStaleDedupReceiptReingestsWithFreshKeyBeforeDeletingOld(t *testing.T) {
	// R1-02 P0: First Ingest hits stale engine dedup cache returning already-deleted "stale-new-doc".
	// Gateway detects GetItem("stale-new-doc") == ErrNotFound, re-ingests with a fresh key,
	// verifies "fresh-new-doc" exists, and only then deletes "old-doc".
	eng := &commonUpdateTestEngine{
		body:           map[string]string{"old-doc": "# Old Title\n\nold body\n"},
		ingestIDs:      []string{"stale-new-doc", "fresh-new-doc"},
		ingestStoreDoc: false, // First ingest does NOT exist; second will be stored by wrapper
	}
	wrapper := &staleDedupEngine{commonUpdateTestEngine: eng}
	srv := commonUpdateTestServer(t, wrapper, nil)
	rec := patchCommonDoc(srv, "old-doc", `{"content":"updated body"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after fresh-key re-ingest, got %d: %s", rec.Code, rec.Body)
	}
	if eng.ingestCalls != 2 || len(eng.ingestKeys) != 2 || eng.ingestKeys[0] == eng.ingestKeys[1] {
		t.Fatalf("expected 2 ingests with distinct keys, calls=%d keys=%v", eng.ingestCalls, eng.ingestKeys)
	}
	if len(eng.deleted) != 1 || eng.deleted[0] != "old-doc" {
		t.Fatalf("expected old-doc deleted after fresh-new-doc verified, deleted=%v", eng.deleted)
	}
	if _, ok := eng.body["fresh-new-doc"]; !ok {
		t.Fatal("fresh-new-doc missing from engine")
	}
}

type staleDedupEngine struct {
	*commonUpdateTestEngine
}

func (e *staleDedupEngine) Ingest(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	res, err := e.commonUpdateTestEngine.Ingest(ctx, req)
	if err == nil && res.DocumentID == "fresh-new-doc" {
		e.body["fresh-new-doc"] = "updated body"
	}
	return res, err
}
