package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func readingFilterStore(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "filters.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	scopes, err := s.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, scope := range scopes {
		ids[scope.Kind] = scope.ID
	}
	for _, name := range []string{"project", "other"} {
		id := uuid.NewString()
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO scopes(id,kind,name)VALUES(?,'project',?)`, id, "Synthetic "+name); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	return s, ids
}
func saveReadingFilterRecord(t *testing.T, s *Store, dataset, origin, source, stamp string) Version {
	t.Helper()
	v, err := s.Apply(context.Background(), Mutation{OperationID: uuid.NewString(), Kind: "save", Memory: uuid.NewString(), Dataset: dataset, Origin: origin, Source: source, Title: "Synthetic " + source, Text: "Synthetic filter content"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE versions SET created_at=? WHERE memory=? AND revision=1`, stamp, v.Memory); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE memory_metadata SET created_at=? WHERE memory=?`, stamp, v.Memory); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReadingFiltersSQLiteUseCurrentVersionAndKeepFullScopeTotals(t *testing.T) {
	s, ids := readingFilterStore(t)
	ctx := context.Background()
	old, recent := "2026-10-01T08:00:00.000Z", "2026-11-11T08:00:00.000Z"
	cutoff := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC).UnixMilli()
	saveReadingFilterRecord(t, s, ids["project"], "knowledge", "pi", old)
	edited := saveReadingFilterRecord(t, s, ids["project"], "knowledge", "codex", old)
	updated, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: edited.Memory, Dataset: edited.Dataset, ExpectedRevision: 1, Title: "Edited after cutoff", Text: "Current updated version"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE versions SET created_at=? WHERE memory=? AND revision=?`, recent, updated.Memory, updated.Revision); err != nil {
		t.Fatal(err)
	}
	saveReadingFilterRecord(t, s, ids["project"], "raw", "capture", recent)
	saveReadingFilterRecord(t, s, ids["other"], "knowledge", "foreign", recent)
	saveReadingFilterRecord(t, s, ids["preference"], "knowledge", "preference-source", recent)
	saveReadingFilterRecord(t, s, ids["experience"], "knowledge", "experience-source", old)
	deleted := saveReadingFilterRecord(t, s, ids["project"], "knowledge", "deleted-source", recent)
	if _, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "delete", Dataset: deleted.Dataset, Memory: deleted.Memory, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		q    records.ListQuery
		want []string
		k, r int
	}{
		{"current-version", records.ListQuery{Datasets: []string{ids["project"]}, ScopeKind: "project", Source: "codex", UpdatedSinceMS: cutoff}, []string{edited.Memory}, 2, 1},
		{"old-version-not-date", records.ListQuery{Datasets: []string{ids["project"]}, UpdatedSinceMS: cutoff, Source: "pi"}, nil, 2, 1},
		{"category", records.ListQuery{ScopeKind: "preference", UpdatedSinceMS: cutoff}, nil, 5, 1},
		{"scope-kind-contradiction", records.ListQuery{Datasets: []string{ids["project"]}, ScopeKind: "experience", Source: "missing"}, nil, 2, 1},
		{"raw-independent", records.ListQuery{Datasets: []string{ids["project"]}, ScopeKind: "project", Kind: "raw", Source: "capture", UpdatedSinceMS: cutoff}, nil, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.List(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if page.KnowledgeTotal == nil || page.RawTotal == nil || *page.KnowledgeTotal != tc.k || *page.RawTotal != tc.r {
				t.Fatalf("filters changed scope totals: %+v", page)
			}
			if tc.want != nil {
				if page.Total != len(tc.want) || page.Items[0].Memory != tc.want[0] || page.Items[0].Revision != 2 || page.Items[0].UpdatedAt != recent || page.Items[0].CreatedAt != old {
					t.Fatal("current/created version semantics mixed", page)
				}
			}
			if tc.name == "category" && (page.Total != 1 || page.Items[0].Dataset != ids["preference"]) {
				t.Fatal("category widened scope", page)
			}
			if tc.name == "raw-independent" && (page.Total != 1 || page.Items[0].Origin != "raw") {
				t.Fatal("category converted raw type", page)
			}
			if (tc.name == "old-version-not-date" || tc.name == "scope-kind-contradiction") && page.Total != 0 {
				t.Fatal("nonmatching filter yielded content", page)
			}
			for _, source := range page.Sources {
				if source == "deleted-source" {
					t.Fatal("deleted record polluted source catalogue")
				}
			}
		})
	}
	for _, q := range []records.ListQuery{{ScopeKind: "unconfirmed-category"}, {UpdatedSinceMS: -1}} {
		if _, err := s.List(ctx, q); err == nil {
			t.Fatal("invalid filter accepted")
		}
	}
}

func TestReadingFiltersSQLiteSourceCatalogueIsNotCurrentFiftyOrFilteredSample(t *testing.T) {
	s, ids := readingFilterStore(t)
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		source, stamp := "visible-source", "2026-11-11T08:00:00.000Z"
		if i >= 50 {
			source, stamp = "archive-only-source", "2026-01-01T08:00:00.000Z"
		}
		saveReadingFilterRecord(t, s, ids["project"], "knowledge", source, stamp)
	}
	saveReadingFilterRecord(t, s, ids["other"], "knowledge", "other-project-source", "2026-11-11T08:00:00.000Z")
	q := records.ListQuery{Datasets: []string{ids["project"]}, Sort: "updated", Limit: 50}
	page, err := s.List(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 || page.NextCursor == "" || page.Total != 60 {
		t.Fatal("wrong pagination fixture", page.Total, len(page.Items))
	}
	for _, item := range page.Items {
		if item.Source != "visible-source" {
			t.Fatal("first page unexpectedly contains hidden source")
		}
	}
	if !reflect.DeepEqual(page.Sources, []string{"archive-only-source", "visible-source"}) || page.SourcesTruncated {
		t.Fatal("source catalogue sampled or leaked another project", page.Sources)
	}
	q.Source = "archive-only-source"
	q.UpdatedSinceMS = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	none, err := s.List(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if none.Total != 0 || !reflect.DeepEqual(none.Sources, page.Sources) || *none.KnowledgeTotal != 60 {
		t.Fatal("source/date filter erased full catalogue or totals", none)
	}
}

func TestReadingFiltersSQLitePaginationBindsEveryFilterAndNoDuplicates(t *testing.T) {
	s, ids := readingFilterStore(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		saveReadingFilterRecord(t, s, ids["project"], "knowledge", "pi", fmt.Sprintf("2026-11-%02dT08:00:00.000Z", i+1))
	}
	q := records.ListQuery{Datasets: []string{ids["project"]}, Kind: "knowledge", Sort: "updated", Source: "pi", ScopeKind: "project", UpdatedSinceMS: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), Limit: 2}
	first, err := s.List(ctx, q)
	if err != nil || first.NextCursor == "" {
		t.Fatal("missing filtered page", err)
	}
	changes := []struct {
		name   string
		change func(*records.ListQuery)
	}{
		{"source", func(q *records.ListQuery) { q.Source = "codex" }},
		{"scope", func(q *records.ListQuery) { q.Datasets = []string{ids["other"]} }},
		{"scope-kind", func(q *records.ListQuery) { q.ScopeKind = "experience" }},
		{"saved-time", func(q *records.ListQuery) { q.UpdatedSinceMS++ }},
		{"kind", func(q *records.ListQuery) { q.Kind = "raw" }},
		{"text", func(q *records.ListQuery) { q.Text = "Synthetic" }},
		{"sort", func(q *records.ListQuery) { q.Sort = "important" }},
		{"activity-date", func(q *records.ListQuery) { q.Date = "2026-11-11" }},
		{"timezone", func(q *records.ListQuery) { q.Timezone = "UTC" }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			changed := q
			changed.Cursor = first.NextCursor
			tc.change(&changed)
			if _, err := s.List(ctx, changed); err == nil {
				t.Fatal("changed filter accepted old cursor")
			}
		})
	}
	got := []string{}
	seen := map[string]bool{}
	for pageCount := 0; pageCount < 5; pageCount++ {
		page, err := s.List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 7 || *page.KnowledgeTotal != 7 {
			t.Fatal("page lost stable filtered/scope totals")
		}
		for _, item := range page.Items {
			if seen[item.Memory] {
				t.Fatal("duplicate across pages", item.Memory)
			}
			seen[item.Memory] = true
			got = append(got, item.UpdatedAt)
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(got) != 7 || !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] > got[j] }) {
		t.Fatal("filtered pagination omitted or reordered items", got)
	}
}

func TestReadingFiltersSQLiteLegacyAndRenamedSourcesKeepIndependentIdentity(t *testing.T) {
	s, ids := readingFilterStore(t)
	ctx := context.Background()
	oldSource, newSource := "用户：原文复制到全局", "用户：原文复制到通用知识"
	old := saveReadingFilterRecord(t, s, ids["experience"], "knowledge", oldSource, "2026-11-11T08:00:00.000Z")
	current := saveReadingFilterRecord(t, s, ids["experience"], "knowledge", newSource, "2026-11-11T09:00:00.000Z")
	for _, tc := range []struct{ source, id string }{{oldSource, old.Memory}, {newSource, current.Memory}} {
		page, err := s.List(ctx, records.ListQuery{Datasets: []string{ids["experience"]}, Source: tc.source})
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Memory != tc.id || len(page.Sources) != 2 {
			t.Fatal("source normalization changed identity", page, err)
		}
	}
}

func TestReadingFiltersSQLiteLegacyCursorCannotRebindScopeKindOrTimezone(t *testing.T) {
	s, ids := readingFilterStore(t)
	v := saveReadingFilterRecord(t, s, ids["project"], "knowledge", "pi", "2026-11-11T08:00:00.000Z")
	encoded, err := json.Marshal(listCursor{Created: "2026-11-11T08:00:00.000Z", ID: v.Memory})
	if err != nil {
		t.Fatal(err)
	}
	legacy := base64.RawURLEncoding.EncodeToString(encoded)
	for _, q := range []records.ListQuery{{Datasets: []string{ids["project"]}}, {Kind: "raw"}, {Kind: "knowledge"}, {Timezone: "UTC"}} {
		q.Cursor = legacy
		if _, err := s.List(context.Background(), q); err == nil {
			t.Fatal("unbound legacy cursor guessed new query scope", q)
		}
	}
	if _, err := s.List(context.Background(), records.ListQuery{Cursor: legacy, Limit: 1}); err != nil {
		t.Fatal("fully unfiltered legacy created-order cursor lost compatibility", err)
	}
}
