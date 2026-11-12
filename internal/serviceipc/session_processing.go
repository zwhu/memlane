package serviceipc

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
)

func (l Library) StartSessionProcessing(ctx context.Context, q records.SessionProcessingRequest) (records.SessionProcessingTask, error) {
	var out records.SessionProcessingTask
	err := Call(ctx, l.Root, Request{Op: "session_processing_start", SessionProcessing: &q, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) SessionProcessingTask(ctx context.Context, operation string) (records.SessionProcessingTask, error) {
	var out records.SessionProcessingTask
	err := Call(ctx, l.Root, Request{Op: "session_processing_get", SessionProcessingID: operation}, &out)
	return out, err
}

func (l Library) LatestSessionProcessing(ctx context.Context, q records.SessionProcessingRequest) (records.SessionProcessingTask, error) {
	var out records.SessionProcessingTask
	err := Call(ctx, l.Root, Request{Op: "session_processing_latest", SessionProcessing: &q}, &out)
	return out, err
}

func (l Library) CancelSessionProcessing(ctx context.Context, operation string) (records.SessionProcessingTask, error) {
	var out records.SessionProcessingTask
	err := Call(ctx, l.Root, Request{Op: "session_processing_cancel", SessionProcessingID: operation, UserConfirmed: true}, &out)
	return out, err
}
