package store

// Real SQLite/Git lineage checks. Synthetic official inputs here are contract
// fixtures; real Codex/Cognee capture and App evidence are separate acceptance.
import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestSourceReaderSQLiteOriginalLineageAndCrossProject(t *testing.T) {
	s, a, b := cogneeImportFixture(t)
	ctx := context.Background()
	a.Text = strings.Repeat("完整中文工具记录\n", 3000) + "原始约定：Ticket 仅接受 str。"
	raw, err := s.ImportCognee(ctx, a)
	mustMerge(t, err)
	learned, err := s.ImportCognee(ctx, cogneeKnowledge(a, raw))
	mustMerge(t, err)
	q := records.SourceRead{Project: a.Project, Owner: learned.Record.Memory, OwnerRevision: 1, Memory: raw.Record.Memory, Revision: 1}
	before := cogneeImportCounts(t, s)
	out, err := s.ReadSource(ctx, q)
	mustMerge(t, err)
	if out.State != "available" || out.Record.Text != a.Text || out.QuoteOffset != strings.Index(a.Text, "Ticket 仅接受 str") || out.Record.SourceDetail.References[0] != a.ExternalID {
		t.Fatal("original/quote/version/reference did not survive", out.State, out.QuoteOffset)
	}
	if !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("reading mutated durable facts")
	}
	q.Project = b.Project
	if _, err = s.ReadSource(ctx, q); err == nil {
		t.Fatal("cross-project original disclosed")
	}
	q.Project = a.Project
	q.Owner = raw.Record.Memory
	out, err = s.ReadSource(ctx, q)
	mustMerge(t, err)
	if out.Record.Text != a.Text || out.QuoteOffset != -1 {
		t.Fatal("direct original read incomplete")
	}
	q.Owner = learned.Record.Memory
	q.Memory = uuid.NewString()
	if _, err = s.ReadSource(ctx, q); err == nil {
		t.Fatal("invented citation accepted")
	}
}

func TestSourceReaderSQLiteChangedOrRemovedSourceStopsDisclosure(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	ctx := context.Background()
	raw, err := s.ImportCognee(ctx, a)
	mustMerge(t, err)
	learned, err := s.ImportCognee(ctx, cogneeKnowledge(a, raw))
	mustMerge(t, err)
	q := records.SourceRead{Project: a.Project, Owner: learned.Record.Memory, OwnerRevision: 1, Memory: raw.Record.Memory, Revision: 1}
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: a.Project, Memory: raw.Record.Memory, ExpectedRevision: 1, Text: "新的原始记录"})
	mustMerge(t, err)
	out, err := s.ReadSource(ctx, q)
	mustMerge(t, err)
	if out.State != "source_changed" || out.CurrentRevision != 2 || out.Record.Text != "" || out.Record.SourceDetail != nil {
		t.Fatal("stale source disclosed old/new prose", out)
	}
	q.Owner = raw.Record.Memory
	out, err = s.ReadSource(ctx, q)
	mustMerge(t, err)
	if out.State != "owner_changed" || out.Record.Text != "" {
		t.Fatal("stale owner accepted", out)
	}
	q.OwnerRevision = 2
	q.Revision = 2
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "delete", Dataset: a.Project, Memory: raw.Record.Memory, ExpectedRevision: 2})
	mustMerge(t, err)
	q.OwnerRevision = 3
	q.Revision = 3
	out, err = s.ReadSource(ctx, q)
	mustMerge(t, err)
	if out.State != "source_changed" || out.Record.Text != "" {
		t.Fatal("removed original disclosed", out)
	}
}
