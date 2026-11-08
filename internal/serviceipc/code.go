package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

type CodeClient struct{ Root string }

func (c CodeClient) List(ctx context.Context) ([]records.CodeState, error) {
	var out []records.CodeState
	e := Call(ctx, c.Root, Request{Op: "code_list"}, &out)
	return out, e
}
func (c CodeClient) Action(ctx context.Context, a records.CodeAction) (records.CodeState, error) {
	var out records.CodeState
	e := Call(ctx, c.Root, Request{Op: "code_action", CodeAction: &a, UserConfirmed: true}, &out)
	return out, e
}
func (c CodeClient) Jobs(ctx context.Context, id string) ([]records.CodeJob, error) {
	var out []records.CodeJob
	e := Call(ctx, c.Root, Request{Op: "code_jobs", CodeID: id}, &out)
	return out, e
}
func (c CodeClient) Query(ctx context.Context, q records.CodeQuery) (records.CodeResult, error) {
	var out records.CodeResult
	e := Call(ctx, c.Root, Request{Op: "code_query", CodeQuery: &q}, &out)
	return out, e
}
