package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

// Library is the typed UI transport. All reads and writes go to the single
// service owner; the client never opens SQLite or calls the engine directly.
type Library struct{ Root string }

func (l Library) Scopes(ctx context.Context) ([]records.Scope, error) {
	var out []records.Scope
	err := Call(ctx, l.Root, Request{Op: "scopes"}, &out)
	return out, err
}
func (l Library) RegisterProject(ctx context.Context, draft records.ScopeDraft) (records.Scope, error) {
	var out records.Scope
	err := Call(ctx, l.Root, Request{Op: "register_project", Project: draft, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) List(ctx context.Context, q records.ListQuery) (records.Page, error) {
	var out records.Page
	err := Call(ctx, l.Root, Request{Op: "list", ListQuery: q}, &out)
	return out, err
}
func (l Library) Get(ctx context.Context, id string) (records.Version, error) {
	var out records.Version
	err := Call(ctx, l.Root, Request{Op: "get", Memory: id}, &out)
	return out, err
}
func (l Library) History(ctx context.Context, id string, before int64) (records.HistoryPage, error) {
	var out records.HistoryPage
	err := Call(ctx, l.Root, Request{Op: "history", Memory: id, BeforeRevision: before, Limit: 50}, &out)
	return out, err
}
func (l Library) Activities(ctx context.Context, zone string) (records.ActivitySummary, error) {
	var out records.ActivitySummary
	err := Call(ctx, l.Root, Request{Op: "activities", Timezone: zone}, &out)
	return out, err
}
func (l Library) Apply(ctx context.Context, mutation records.Mutation) (records.Version, error) {
	var out records.Version
	err := Call(ctx, l.Root, Request{Op: "apply", Mutation: mutation, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) RetryIndex(ctx context.Context, id string, revision int64) error {
	return Call(ctx, l.Root, Request{Op: "retry_index", Memory: id, ExpectedRevision: revision}, nil)
}
