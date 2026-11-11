package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) DeletionImpact(ctx context.Context, q records.DeletionImpactQuery) (records.DeletionImpact, error) {
	var out records.DeletionImpact
	err := Call(ctx, l.Root, Request{Op: "deletion_impact", Memory: q.Memory, Scope: q.Dataset, ExpectedRevision: q.ExpectedRevision, Datasets: q.Datasets}, &out)
	return out, err
}
