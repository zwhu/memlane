package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type captureSearchKindEngine struct {
	engine.MemoryEngine
	seenKinds  []string
	seenLimits []int
	hits       []engine.SearchHit
}

func (e *captureSearchKindEngine) Search(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	e.seenKinds = append(e.seenKinds, req.Filters.Kind)
	e.seenLimits = append(e.seenLimits, req.Limit)
	return engine.SearchResult{OK: true, Hits: e.hits}, nil
}

func TestSearchDoesNotForwardDesktopFilterAsEngineKind(t *testing.T) {
	ctx := context.Background()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const ds = "aaaaaaaa-1111-2222-3333-444444444444"
	if err := store.UpsertProject(ctx, domain.Project{
		ID: "p1", Name: "P1", DatasetID: ds, Status: "verified",
	}); err != nil {
		t.Fatal(err)
	}
	now := domain.FormatUTCNS(time.Now().UTC())
	for _, doc := range []domain.CatalogDoc{
		{DatasetID: ds, DocID: "k-doc", Kind: "knowledge", DocName: "K", IsKnowledge: true, CreatedAtNS: now, CreatedAtRaw: now},
		{DatasetID: ds, DocID: "r-doc", Kind: "raw_doc", DocName: "R", IsKnowledge: false, CreatedAtNS: now, CreatedAtRaw: now},
	} {
		if err := store.UpsertCatalogDoc(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	eng := &captureSearchKindEngine{
		hits: []engine.SearchHit{
			{DatasetID: ds, DocumentID: "k-doc", Title: "K", Snippet: "knowledge snippet"},
			{DatasetID: ds, DocumentID: "r-doc", Title: "R", Snippet: "raw snippet"},
		},
	}
	s, err := NewServer(Config{Store: store, Engine: eng, ManagementToken: testManagement})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		filter   string
		wantDocs []string
	}{
		{"all", []string{"k-doc", "r-doc"}},
		{"knowledge", []string{"k-doc"}},
		{"raw", []string{"r-doc"}},
	}
	for _, tc := range cases {
		rec := manage(t, s, http.MethodPost, "/api/search", `{"q":"needle","filter":"`+tc.filter+`","semantic":true}`, http.StatusOK)
		out := decodeResponse[struct {
			OK           bool               `json:"ok"`
			SemanticHits []engine.SearchHit `json:"semantic_hits"`
		}](t, rec)
		if !out.OK || len(out.SemanticHits) != len(tc.wantDocs) {
			t.Fatalf("filter=%s: got hits=%+v want=%v", tc.filter, out.SemanticHits, tc.wantDocs)
		}
		for i, want := range tc.wantDocs {
			if out.SemanticHits[i].DocumentID != want {
				t.Fatalf("filter=%s hit[%d]=%q, want %q", tc.filter, i, out.SemanticHits[i].DocumentID, want)
			}
		}
	}
	for i, gotKind := range eng.seenKinds {
		if gotKind != "" {
			t.Fatalf("call %d forwarded non-empty engine filter kind %q; want empty string", i, gotKind)
		}
	}

	// Regression: filter=knowledge with limit=1 must not truncate before catalog classification
	// when a raw_doc precedes the matching knowledge doc in engine hits.
	eng.hits = []engine.SearchHit{
		{DatasetID: ds, DocumentID: "r-doc", Title: "R", Snippet: "raw first"},
		{DatasetID: ds, DocumentID: "k-doc", Title: "K", Snippet: "knowledge second"},
	}
	eng.seenLimits = nil
	rec := manage(t, s, http.MethodPost, "/api/search", `{"q":"needle","filter":"knowledge","limit":1,"semantic":true}`, http.StatusOK)
	out := decodeResponse[struct {
		OK           bool               `json:"ok"`
		SemanticHits []engine.SearchHit `json:"semantic_hits"`
	}](t, rec)
	if !out.OK || len(out.SemanticHits) != 1 || out.SemanticHits[0].DocumentID != "k-doc" {
		t.Fatalf("filter=knowledge limit=1 lost hit ranked after raw_doc: %+v", out.SemanticHits)
	}
	if len(eng.seenLimits) != 1 || eng.seenLimits[0] < 50 {
		t.Fatalf("expected oversampled engine limit >= 50 for filtered search, got %v", eng.seenLimits)
	}
}
