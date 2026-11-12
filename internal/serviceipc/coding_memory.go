package serviceipc

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
)

func (l Library) CodingMemoryGrant(ctx context.Context, b records.CogneeBinding) (records.CodingMemoryGrant, error) {
	var out records.CodingMemoryGrant
	err := Call(ctx, l.Root, Request{Op: "coding_memory_grant_get", CodingMemoryGrantBinding: &b}, &out)
	return out, err
}

func (l Library) ChangeCodingMemoryGrant(ctx context.Context, q records.CodingMemoryGrantChange) (records.CodingMemoryGrant, error) {
	var out records.CodingMemoryGrant
	err := Call(ctx, l.Root, Request{Op: "coding_memory_grant_change", CodingMemoryGrantChange: &q, UserConfirmed: true}, &out)
	return out, err
}
