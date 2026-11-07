package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

// MergeClient is for the trusted local host. It is deliberately absent from
// the daily Agent MCP/Extension tool registry and carries no database handle.
type MergeClient struct{ Root string }

func (c MergeClient) Start(ctx context.Context, d records.MergeStart) (records.MergeTask, error) {
	var out records.MergeTask
	err := Call(ctx, c.Root, Request{Op: "merge_start", UserConfirmed: true, MergeStart: d}, &out)
	return out, err
}
func (c MergeClient) Get(ctx context.Context, id string) (records.MergeTask, error) {
	var out records.MergeTask
	err := Call(ctx, c.Root, Request{Op: "merge_get", MergeTask: id}, &out)
	return out, err
}
func (c MergeClient) Cancel(ctx context.Context, id string) (records.MergeTask, error) {
	var out records.MergeTask
	err := Call(ctx, c.Root, Request{Op: "merge_cancel", UserConfirmed: true, MergeTask: id}, &out)
	return out, err
}
func (c MergeClient) Confirm(ctx context.Context, d records.MergeReview) (records.MergeTask, error) {
	return c.review(ctx, d, "merge_confirm")
}
func (c MergeClient) Reject(ctx context.Context, d records.MergeReview) (records.MergeTask, error) {
	return c.review(ctx, d, "merge_reject")
}
func (c MergeClient) review(ctx context.Context, d records.MergeReview, op string) (records.MergeTask, error) {
	var out records.MergeTask
	err := Call(ctx, c.Root, Request{Op: op, UserConfirmed: true, MergeReview: d}, &out)
	return out, err
}
