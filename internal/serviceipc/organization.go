package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

type OrganizationClient struct{ Root string }

func (c OrganizationClient) Start(ctx context.Context, d records.OrganizationStart) (records.OrganizationTask, error) {
	var out records.OrganizationTask
	e := Call(ctx, c.Root, Request{Op: "organization_start", UserConfirmed: true, OrganizationStart: &d}, &out)
	return out, e
}
func (c OrganizationClient) Get(ctx context.Context, id string) (records.OrganizationTask, error) {
	var out records.OrganizationTask
	e := Call(ctx, c.Root, Request{Op: "organization_get", OrganizationTask: id}, &out)
	return out, e
}
func (c OrganizationClient) Cancel(ctx context.Context, id string) (records.OrganizationTask, error) {
	var out records.OrganizationTask
	e := Call(ctx, c.Root, Request{Op: "organization_cancel", UserConfirmed: true, OrganizationTask: id}, &out)
	return out, e
}
func (c OrganizationClient) Resume(ctx context.Context, d records.OrganizationResume) (records.OrganizationTask, error) {
	var out records.OrganizationTask
	e := Call(ctx, c.Root, Request{Op: "organization_resume", UserConfirmed: true, OrganizationResume: &d}, &out)
	return out, e
}
func (c OrganizationClient) List(ctx context.Context, q records.MergeListQuery) (records.OrganizationPage, error) {
	var out records.OrganizationPage
	e := Call(ctx, c.Root, Request{Op: "organization_list", OrganizationList: &q}, &out)
	return out, e
}
func (c OrganizationClient) Sources(ctx context.Context, id string, revision int64) ([]records.OrganizationSource, error) {
	var out []records.OrganizationSource
	e := Call(ctx, c.Root, Request{Op: "organization_sources", Memory: id, ExpectedRevision: revision}, &out)
	return out, e
}

func (c OrganizationClient) Review(ctx context.Context, d records.OrganizationReview) (records.OrganizationTask, error) {
	var out records.OrganizationTask
	e := Call(ctx, c.Root, Request{Op: "organization_review", UserConfirmed: true, OrganizationReview: &d}, &out)
	return out, e
}
