package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) RebuildIndex(ctx context.Context, start records.IndexRebuildStart) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	err := Call(ctx, l.Root, Request{Op: "rebuild_index", IndexRebuildStart: &start, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) IndexRebuild(ctx context.Context, id string) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	err := Call(ctx, l.Root, Request{Op: "index_rebuild", IndexRebuildID: id}, &out)
	return out, err
}
