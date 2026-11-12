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

// Separate operation: older daemons must reject rather than ignore the revision.
func (l Library) DisconnectRevision(ctx context.Context, id string, generation int64) error {
	return Call(ctx, l.Root, Request{Op: "disconnect_revision", ConnectionID: id, ConnectionGeneration: generation, UserConfirmed: true}, nil)
}

func (l Library) CheckWorkspace(ctx context.Context, scope, path string) error {
	return Call(ctx, l.Root, Request{Op: "check_workspace", Scope: scope, Project: records.ScopeDraft{Path: path}}, nil)
}

func (l Library) CheckIDEVerification(ctx context.Context, d records.IDEVerification) (records.IDEVerificationReceipt, error) {
	var out records.IDEVerificationReceipt
	err := Call(ctx, l.Root, Request{Op: "ide_verification", IDEVerification: &d}, &out)
	return out, err
}
