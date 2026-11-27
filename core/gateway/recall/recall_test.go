package recall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type stubSearchEngine struct {
	engine.MemoryEngine
	search func(context.Context, engine.SearchRequest) (engine.SearchResult, error)
}

func (s *stubSearchEngine) Search(ctx context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
	return s.search(ctx, req)
}

func TestExecuteFaultToleranceMatrix(t *testing.T) {
	projA := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-a"}, ProjectID: "p-a", Kind: ScopeKindProject}
	projB := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-b"}, ProjectID: "p-b", Kind: ScopeKindProject}
	common := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-common"}, Kind: ScopeKindCommon}

	t.Run("project hits + common real failure returns partial with scope_errors", func(t *testing.T) {
		eng := &stubSearchEngine{search: func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
			if req.Scopes[0].DatasetID == "ds-common" {
				return engine.SearchResult{}, engine.ErrUnavailable
			}
			return engine.SearchResult{
				OK:   true,
				Hits: []engine.SearchHit{{DatasetID: "ds-a", DocumentID: "doc-a", Snippet: "rule A"}},
			}, nil
		}}
		out, err := Execute(t.Context(), SearchOptions{
			Engine: eng,
			Scopes: []AuthorizedScope{projA, common},
			Query:  "rule",
			Mode:   engine.SearchModeHybrid,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.Partial || len(out.ScopeErrors) != 1 || out.ScopeErrors[0].ScopeKind != ScopeKindCommon || out.ScopeErrors[0].DatasetID != "ds-common" {
			t.Fatalf("unexpected partial/scope_errors: %+v", out)
		}
		if len(out.Hits) != 1 || out.Hits[0].DocumentID != "doc-a" {
			t.Fatalf("unexpected hits: %+v", out.Hits)
		}
	})

	t.Run("sole project real failure fails closed and never degrades to common-only", func(t *testing.T) {
		eng := &stubSearchEngine{search: func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
			if req.Scopes[0].DatasetID == "ds-a" {
				return engine.SearchResult{}, engine.ErrUnavailable
			}
			return engine.SearchResult{
				OK:   true,
				Hits: []engine.SearchHit{{DatasetID: "ds-common", DocumentID: "doc-common"}},
			}, nil
		}}
		_, err := Execute(t.Context(), SearchOptions{
			Engine: eng,
			Scopes: []AuthorizedScope{projA, common},
			Query:  "rule",
			Mode:   engine.SearchModeHybrid,
			Limit:  10,
		})
		if !errors.Is(err, engine.ErrUnavailable) {
			t.Fatalf("expected fail-closed ErrUnavailable, got %v", err)
		}
	})

	t.Run("new unprovisioned project + common hits succeeds cleanly without partial", func(t *testing.T) {
		eng := &stubSearchEngine{search: func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
			if req.Scopes[0].DatasetID == "ds-a" {
				return engine.SearchResult{}, errors.Join(engine.ErrDatasetNotProvisioned, engine.ErrNotFound)
			}
			return engine.SearchResult{
				OK:   true,
				Hits: []engine.SearchHit{{DatasetID: "ds-common", DocumentID: "doc-common"}},
			}, nil
		}}
		out, err := Execute(t.Context(), SearchOptions{
			Engine: eng,
			Scopes: []AuthorizedScope{projA, common},
			Query:  "pref",
			Mode:   engine.SearchModeHybrid,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Partial || len(out.ScopeErrors) != 0 {
			t.Fatalf("unprovisioned project must not set partial or scope_errors: %+v", out)
		}
		if len(out.Hits) != 1 || out.Hits[0].DocumentID != "doc-common" {
			t.Fatalf("expected common hit, got %+v", out.Hits)
		}
	})

	t.Run("multi-project partial failure returns healthy project and common with partial=true", func(t *testing.T) {
		eng := &stubSearchEngine{search: func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
			switch req.Scopes[0].DatasetID {
			case "ds-a":
				return engine.SearchResult{OK: true, Hits: []engine.SearchHit{{DatasetID: "ds-a", DocumentID: "doc-a"}}}, nil
			case "ds-b":
				return engine.SearchResult{}, context.DeadlineExceeded
			default:
				return engine.SearchResult{OK: true, Hits: []engine.SearchHit{{DatasetID: "ds-common", DocumentID: "doc-common"}}}, nil
			}
		}}
		out, err := Execute(t.Context(), SearchOptions{
			Engine: eng,
			Scopes: []AuthorizedScope{projA, projB, common},
			Query:  "multi",
			Mode:   engine.SearchModeHybrid,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.Partial || len(out.ScopeErrors) != 1 || out.ScopeErrors[0].DatasetID != "ds-b" || out.ScopeErrors[0].ScopeKind != ScopeKindProject {
			t.Fatalf("unexpected partial state: %+v", out)
		}
		var gotIDs []string
		for _, h := range out.Hits {
			gotIDs = append(gotIDs, h.DocumentID)
		}
		if !slices.Equal(gotIDs, []string{"doc-a", "doc-common"}) {
			t.Fatalf("hits = %v, want [doc-a doc-common]", gotIDs)
		}
	})
}

func TestProjectPriorityQuotaPreventsCommonCrowding(t *testing.T) {
	proj := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-p"}, ProjectID: "p", Kind: ScopeKindProject}
	common := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-c"}, Kind: ScopeKindCommon}

	eng := &stubSearchEngine{search: func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
		ds := req.Scopes[0].DatasetID
		hits := make([]engine.SearchHit, 0, 10)
		for i := 1; i <= 10; i++ {
			hits = append(hits, engine.SearchHit{
				DatasetID:  ds,
				DocumentID: fmt.Sprintf("%s-%d", ds, i),
				Score:      0.01 * float64(i),
			})
		}
		return engine.SearchResult{OK: true, Hits: hits}, nil
	}}

	// limit=5: commonReserve = max(1, 5/5) = 1, primaryTake = 4, commonTake = 1.
	out, err := Execute(t.Context(), SearchOptions{
		Engine: eng,
		Scopes: []AuthorizedScope{proj, common},
		Query:  "quota",
		Mode:   engine.SearchModeHybrid,
		Limit:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range out.Hits {
		ids = append(ids, h.DocumentID)
	}
	want := []string{"ds-p-1", "ds-p-2", "ds-p-3", "ds-p-4", "ds-c-1"}
	if !slices.Equal(ids, want) {
		t.Fatalf("limit=5 hits = %v, want %v", ids, want)
	}

	// limit=2 (<3): project strictly first.
	out2, err := Execute(t.Context(), SearchOptions{
		Engine: eng,
		Scopes: []AuthorizedScope{proj, common},
		Query:  "quota",
		Mode:   engine.SearchModeHybrid,
		Limit:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids2 []string
	for _, h := range out2.Hits {
		ids2 = append(ids2, h.DocumentID)
	}
	if !slices.Equal(ids2, []string{"ds-p-1", "ds-p-2"}) {
		t.Fatalf("limit=2 hits = %v, want [ds-p-1 ds-p-2]", ids2)
	}
}

func TestAgentUserIDAndProtectedProject(t *testing.T) {
	if got := AgentUserID(domain.Agent{PrincipalID: "not-a-uuid"}); got != "" {
		t.Fatalf("non-uuid PrincipalID = %q, want empty", got)
	}
	const u = "11111111-1111-4111-8111-111111111111"
	if got := AgentUserID(domain.Agent{PrincipalID: " " + u + " "}); got != u {
		t.Fatalf("uuid PrincipalID = %q, want %q", got, u)
	}
	if !IsProtectedProject(domain.Project{ID: "p", Name: "ml-user-profiles"}) {
		t.Fatal("expected ml-user-profiles to be protected")
	}
}

func TestExplicitProjectACLCannotBeBypassedByCommonDataset(t *testing.T) {
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "acl.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	proj := domain.Project{ID: "p-denied", Name: "Denied", DatasetID: "ds-denied"}
	if err := store.UpsertProject(ctx, proj); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCommonConfig(ctx, domain.CommonConfig{ID: "common", Name: "Common", DatasetID: "ds-common"}); err != nil {
		t.Fatal(err)
	}
	ag := domain.Agent{ID: "a1", Name: "A1", DefaultAccess: domain.AccessNone}
	if err := store.UpsertAgent(ctx, ag); err != nil {
		t.Fatal(err)
	}
	read := domain.AccessRead
	if err := store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "common", TargetID: "common", AgentID: "a1", Access: &read}); err != nil {
		t.Fatal(err)
	}

	_, err = ResolveScopes(ctx, ResolveInput{
		Store:      store,
		Agent:      ag,
		Projects:   []domain.Project{proj},
		Explicit:   true,
		DatasetIDs: []string{"ds-common"},
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden when explicit project is unauthorized even if common is requested, got %v", err)
	}
}

func TestMultiProjectScoreSortAndCodeFactsLimit(t *testing.T) {
	projA := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-a"}, ProjectID: "p-a", Kind: ScopeKindProject}
	projB := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-b"}, ProjectID: "p-b", Kind: ScopeKindProject}
	eng := &stubSearchEngine{search: func(_ context.Context, req engine.SearchRequest) (engine.SearchResult, error) {
		if req.Scopes[0].DatasetID == "ds-a" {
			return engine.SearchResult{
				OK:   true,
				Hits: []engine.SearchHit{{DatasetID: "ds-a", DocumentID: "a-far", Score: 0.9}},
				GraphContext: &engine.GraphSearchContext{
					CodeFacts: []engine.CodeSearchFact{
						{DatasetID: "ds-a", Name: "F1"},
						{DatasetID: "ds-a", Name: "F2"},
					},
				},
			}, nil
		}
		return engine.SearchResult{
			OK:   true,
			Hits: []engine.SearchHit{{DatasetID: "ds-b", DocumentID: "b-near", Score: 0.1}},
			GraphContext: &engine.GraphSearchContext{
				CodeFacts: []engine.CodeSearchFact{
					{DatasetID: "ds-b", Name: "F3"},
				},
			},
		}, nil
	}}
	out, err := Execute(t.Context(), SearchOptions{
		Engine: eng,
		Scopes: []AuthorizedScope{projA, projB},
		Query:  "sort",
		Mode:   engine.SearchModeHybrid,
		Limit:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 2 || out.Hits[0].DocumentID != "b-near" || out.Hits[1].DocumentID != "a-far" {
		t.Fatalf("expected ascending distance sort [b-near a-far], got %+v", out.Hits)
	}
	if out.GraphContext == nil || len(out.GraphContext.CodeFacts) != 2 {
		t.Fatalf("expected codeFacts truncated to limit=2, got %+v", out.GraphContext)
	}
}

func TestNullScoreRanksAfterExplicitZeroAndPositiveScores(t *testing.T) {
	var hits []engine.SearchHit
	raw := `[
		{"dataset_id":"ds-a","document_id":"null-score","score":null},
		{"dataset_id":"ds-a","document_id":"positive-score","score":0.4},
		{"dataset_id":"ds-a","document_id":"zero-score","score":0.0}
	]`
	if err := json.Unmarshal([]byte(raw), &hits); err != nil {
		t.Fatal(err)
	}
	if !hits[0].ScoreMissing || hits[1].ScoreMissing || hits[2].ScoreMissing {
		t.Fatalf("unexpected ScoreMissing flags: %+v", hits)
	}
	proj := AuthorizedScope{Scope: engine.Scope{DatasetID: "ds-a"}, ProjectID: "p-a", Kind: ScopeKindProject}
	eng := &stubSearchEngine{search: func(_ context.Context, _ engine.SearchRequest) (engine.SearchResult, error) {
		return engine.SearchResult{OK: true, Hits: hits}, nil
	}}
	out, err := Execute(t.Context(), SearchOptions{
		Engine: eng,
		Scopes: []AuthorizedScope{proj},
		Query:  "score",
		Mode:   engine.SearchModeHybrid,
		Limit:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range out.Hits {
		got = append(got, h.DocumentID)
	}
	want := []string{"zero-score", "positive-score", "null-score"}
	if !slices.Equal(got, want) {
		t.Fatalf("hit order = %v, want %v", got, want)
	}
}
