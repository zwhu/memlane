package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) Connections(ctx context.Context) ([]records.Connection, error) {
	var out []records.Connection
	err := Call(ctx, l.Root, Request{Op: "connections"}, &out)
	return out, err
}
func (l Library) ConfigureConnection(ctx context.Context, d records.ConnectionDraft) (records.Connection, error) {
	var out records.Connection
	err := Call(ctx, l.Root, Request{Op: "configure_connection", Connection: d, UserConfirmed: true}, &out)
	return out, err
}
func (l Library) Disconnect(ctx context.Context, id string) error {
	return Call(ctx, l.Root, Request{Op: "disconnect", ConnectionID: id, UserConfirmed: true}, nil)
}
