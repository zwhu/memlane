package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestKnowledgeSourceListAndReaderShareInheritedLineage(t *testing.T) {
	s, input, _ := cogneeImportFixture(t)
	ctx := context.Background()
	raw, err := s.ImportCognee(ctx, input)
	mustMerge(t, err)
	knowledge, err := s.ImportCognee(ctx, cogneeKnowledge(input, raw))
	mustMerge(t, err)
	initial, err := s.OrganizationSources(ctx, knowledge.Record.Memory, 1)
	mustMerge(t, err)
	if len(initial) != 1 {
		t.Fatal("initial provenance missing", initial)
	}
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: input.Project, Memory: knowledge.Record.Memory, ExpectedRevision: 1, Title: "用户修订知识", Text: "用户补充条件，原材料仍保留。"})
	mustMerge(t, err)
	reader, err := s.ReadSource(ctx, records.SourceRead{Project: input.Project, Owner: knowledge.Record.Memory, OwnerRevision: 2, Memory: raw.Record.Memory, Revision: 1})
	mustMerge(t, err)
	if reader.State != "available" || reader.Record.Text != raw.Record.Text {
		t.Fatal("reader lost inherited original", reader)
	}
	before := cogneeImportCounts(t, s)
	sources, err := s.OrganizationSources(ctx, knowledge.Record.Memory, 2)
	mustMerge(t, err)
	if !reflect.DeepEqual(sources, initial) {
		t.Fatalf("source list disagrees with reader after user edit: got %d sources, want immutable inherited provenance", len(sources))
	}
	if !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("listing inherited provenance wrote new history")
	}
	missing, err := s.OrganizationSources(ctx, knowledge.Record.Memory, 99)
	mustMerge(t, err)
	if len(missing) != 0 {
		t.Fatal("invented knowledge revision acquired provenance", missing)
	}
}
