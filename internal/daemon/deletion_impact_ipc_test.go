package daemon_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestDeletionImpactPrivateIPCAndCAS(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	l := serviceipc.Library{Root: root}
	scopes, e := l.Scopes(ctx)
	requireService(t, e)
	v, e := l.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scopes[0].ID, Text: "synthetic deletion review target"})
	requireService(t, e)
	q := records.DeletionImpactQuery{Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: v.Revision, Datasets: []string{v.Dataset}}
	p, e := l.DeletionImpact(ctx, q)
	requireService(t, e)
	if p.Memory != v.Memory || p.Revision != v.Revision || p.Dataset != v.Dataset || p.References == nil || p.ReferenceCount != 0 || len(p.CoverageDatasets) != 1 {
		t.Fatal(p)
	}
	q.ExpectedRevision++
	if _, e = l.DeletionImpact(ctx, q); e == nil {
		t.Fatal("IPC concealed CAS failure as zero references")
	}
	q.ExpectedRevision = v.Revision
	q.Datasets = nil
	if _, e = l.DeletionImpact(ctx, q); e == nil {
		t.Fatal("missing read scopes accepted")
	}
	if e = serviceipc.Call(ctx, root, serviceipc.Request{Op: "deletion_impact"}, nil); e == nil {
		t.Fatal("missing exact target accepted")
	}
}
