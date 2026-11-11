package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) AgentConnectionBatch(ctx context.Context, id string) (records.AgentConnectionBatchState, error) {
	var b records.AgentConnectionBatchState
	err := Call(ctx, l.Root, Request{Op: "agent_connection_batch", AgentConnectionBatchID: id}, &b)
	return b, err
}

func (l Library) AgentConnectionBatchByOperation(ctx context.Context, operation string) (records.AgentConnectionBatchState, error) {
	var out records.AgentConnectionBatchState
	err := Call(ctx, l.Root, Request{Op: "agent_connection_batch", AgentConnectionBatchOperation: operation}, &out)
	return out, err
}

func (l Library) StartAgentConnectionBatch(ctx context.Context, d records.AgentConnectionBatchStart) (records.AgentConnectionBatch, error) {
	var b records.AgentConnectionBatch
	err := Call(ctx, l.Root, Request{Op: "agent_connection_batch_start", AgentConnectionBatchStart: &d, UserConfirmed: true}, &b)
	return b, err
}

func (l Library) ActAgentConnectionBatch(ctx context.Context, d records.AgentConnectionBatchAction) (records.AgentConnectionBatch, error) {
	var b records.AgentConnectionBatch
	err := Call(ctx, l.Root, Request{Op: "agent_connection_batch_action", AgentConnectionBatchAction: &d, UserConfirmed: true}, &b)
	return b, err
}
