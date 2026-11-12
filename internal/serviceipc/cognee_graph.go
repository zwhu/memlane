package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) OfficialVisualization(ctx context.Context, q records.OfficialVisualizationQuery) (records.OfficialVisualization, error) {
	var out records.OfficialVisualization
	err := Call(ctx, l.Root, Request{Op: "official_visualization", OfficialVisualization: &q}, &out)
	return out, err
}

func (l Library) OfficialVisualizationBinding(ctx context.Context, q records.OfficialVisualizationQuery) (records.CogneeBinding, error) {
	var out records.CogneeBinding
	err := Call(ctx, l.Root, Request{Op: "official_visualization_authorize", OfficialVisualization: &q}, &out)
	return out, err
}

func (l Library) ReauthorizeCogneeRead(ctx context.Context, q records.CogneeReadReauthorization) (records.CogneeBinding, error) {
	var out records.CogneeBinding
	err := Call(ctx, l.Root, Request{Op: "cognee_read_reauthorize", CogneeReadReauthorization: &q, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) BindCognee(ctx context.Context, binding records.CogneeBinding) (records.CogneeBinding, error) {
	var out records.CogneeBinding
	err := Call(ctx, l.Root, Request{Op: "cognee_bind", CogneeBinding: &binding, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) CogneeGraph(ctx context.Context, query records.CogneeGraphQuery) (records.CogneeGraph, error) {
	var out records.CogneeGraph
	err := Call(ctx, l.Root, Request{Op: "cognee_graph", CogneeGraphQuery: &query}, &out)
	return out, err
}

func (l Library) AuthorizeCognee(ctx context.Context, d records.CogneeAuthorization) (records.CogneeBinding, error) {
	var out records.CogneeBinding
	err := Call(ctx, l.Root, Request{Op: "cognee_authorize", CogneeAuthorization: &d, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) CogneeBindings(ctx context.Context, project string) ([]records.CogneeBinding, error) {
	var out []records.CogneeBinding
	err := Call(ctx, l.Root, Request{Op: "cognee_bindings", Scope: project}, &out)
	return out, err
}
func (l Library) SyncCognee(ctx context.Context, d records.CogneeSync) (records.CogneeSyncReceipt, error) {
	var out records.CogneeSyncReceipt
	err := Call(ctx, l.Root, Request{Op: "cognee_sync", CogneeSync: &d, UserConfirmed: true}, &out)
	return out, err
}
