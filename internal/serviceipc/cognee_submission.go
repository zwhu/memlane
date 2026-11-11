package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) StartCogneeSubmission(ctx context.Context, q records.CogneeSubmissionRequest) (records.CogneeSubmissionTask, error) {
	var out records.CogneeSubmissionTask
	err := Call(ctx, l.Root, Request{Op: "cognee_submission_start", CogneeSubmission: &q, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) CogneeSubmission(ctx context.Context, id string) (records.CogneeSubmissionTask, error) {
	var out records.CogneeSubmissionTask
	err := Call(ctx, l.Root, Request{Op: "cognee_submission_get", CogneeSubmissionID: id}, &out)
	return out, err
}
func (l Library) CancelCogneeSubmission(ctx context.Context, id string) (records.CogneeSubmissionTask, error) {
	var out records.CogneeSubmissionTask
	err := Call(ctx, l.Root, Request{Op: "cognee_submission_cancel", CogneeSubmissionID: id, UserConfirmed: true}, &out)
	return out, err
}

func (l Library) LatestCogneeSubmission(ctx context.Context, project string) (records.CogneeSubmissionTask, error) {
	var out records.CogneeSubmissionTask
	err := Call(ctx, l.Root, Request{Op: "cognee_submission_latest", Scope: project}, &out)
	return out, err
}
