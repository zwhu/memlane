package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

type InitializationClient struct{ Root string }

func (c InitializationClient) Start(ctx context.Context, d records.InitializationStart) (records.InitializationBatch, error) {
	var out records.InitializationBatch
	err := Call(ctx, c.Root, Request{Op: "initialization_start", InitializationStart: &d, UserConfirmed: true}, &out)
	return out, err
}
func (c InitializationClient) Get(ctx context.Context, id string) (records.InitializationBatch, error) {
	var out records.InitializationBatch
	err := Call(ctx, c.Root, Request{Op: "initialization_get", InitializationBatch: id}, &out)
	return out, err
}
func (c InitializationClient) List(ctx context.Context, project string) ([]records.InitializationBatch, error) {
	var out []records.InitializationBatch
	err := Call(ctx, c.Root, Request{Op: "initialization_list", Scope: project}, &out)
	return out, err
}
func (c InitializationClient) Cancel(ctx context.Context, id string) (records.InitializationBatch, error) {
	var out records.InitializationBatch
	err := Call(ctx, c.Root, Request{Op: "initialization_cancel", InitializationBatch: id, UserConfirmed: true}, &out)
	return out, err
}
