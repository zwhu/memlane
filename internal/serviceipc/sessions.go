package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) Sessions(ctx context.Context, q records.SessionQuery) (records.SessionPage, error) {
	var out records.SessionPage
	err := Call(ctx, l.Root, Request{Op: "official_sessions", SessionQuery: &q}, &out)
	return out, err
}

func (l Library) Session(ctx context.Context, q records.SessionQuery) (records.SessionDetail, error) {
	var out records.SessionDetail
	err := Call(ctx, l.Root, Request{Op: "official_session", SessionQuery: &q}, &out)
	return out, err
}

func (l Library) SourceSession(ctx context.Context, q records.SourceRead) (records.SourceSession, error) {
	var out records.SourceSession
	err := Call(ctx, l.Root, Request{Op: "official_source_session", SourceRead: &q}, &out)
	return out, err
}
