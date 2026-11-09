package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestScopeContentCountsAreIndependentOfFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scopes, err := s.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var knowledge string
	for _, input := range []struct{ scope, kind string }{{scopes[0].ID, "knowledge"}, {scopes[0].ID, "knowledge"}, {scopes[0].ID, "raw"}, {scopes[1].ID, "raw"}} {
		id := uuid.NewString()
		_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: input.scope, Memory: id, Origin: input.kind, Text: "本轮真实SQLite计数样本", Source: "count-test"})
		if err != nil {
			t.Fatal(err)
		}
		if input.scope == scopes[0].ID && input.kind == "knowledge" {
			knowledge = id
		}
	}
	for _, q := range []records.ListQuery{
		{Datasets: []string{scopes[0].ID}, Kind: "knowledge", Limit: 1},
		{Datasets: []string{scopes[0].ID}, Kind: "raw", Source: "no-match", Limit: 1},
		{Datasets: []string{scopes[0].ID}, Kind: "knowledge", Text: "不匹配的内容", Limit: 1},
		{Datasets: []string{scopes[0].ID}, Kind: "knowledge", Date: "2040-01-01", Timezone: "UTC", Limit: 1},
	} {
		page, err := s.List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if page.KnowledgeTotal == nil || page.RawTotal == nil || *page.KnowledgeTotal != 2 || *page.RawTotal != 1 {
			t.Fatal("filter or another scope changed full content totals", page)
		}
		if page.NextCursor != "" {
			q.Cursor = page.NextCursor
			next, err := s.List(ctx, q)
			if err != nil || *next.KnowledgeTotal != 2 || *next.RawTotal != 1 {
				t.Fatal("pagination lost scope counts", next, err)
			}
		}
	}
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "delete", Dataset: scopes[0].ID, Memory: knowledge, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.List(ctx, records.ListQuery{Datasets: []string{scopes[0].ID}, Kind: "raw"})
	if err != nil || *page.KnowledgeTotal != 1 || *page.RawTotal != 1 {
		t.Fatal("deleted knowledge remained in current totals", page, err)
	}
	legacy, err := json.Marshal(listCursor{Created: "2026-01-01T00:00:00Z", ID: knowledge})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.List(ctx, records.ListQuery{Datasets: []string{scopes[0].ID}, Text: "内容", Cursor: base64.RawURLEncoding.EncodeToString(legacy)})
	if err == nil {
		t.Fatal("a legacy unbound cursor was accepted for a content query")
	}
}
