package store

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"path/filepath"
	"testing"
)

func TestDisconnectRevisionDoesNotRevokeReconnectedGeneration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic", ConnectionOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.ConfigureConnection(ctx, records.ConnectionDraft{ID: c.ID, Agent: c.Agent, Name: c.Name, ConnectionOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Generation == c.Generation {
		t.Fatal("fixture did not reconnect")
	}
	if err = s.DisconnectRevision(ctx, c.ID, c.Generation); err == nil {
		t.Fatal("stale confirmation accepted")
	}
	if err = s.DisconnectRevision(ctx, c.ID, 0); err == nil {
		t.Fatal("unversioned confirmation accepted")
	}
	current, err := s.Connection(ctx, c.ID)
	if err != nil || !current.Enabled || current.Generation != fresh.Generation {
		t.Fatal("new grant changed", current, err)
	}
	if err = s.DisconnectRevision(ctx, c.ID, fresh.Generation); err != nil {
		t.Fatal(err)
	}
	current, err = s.Connection(ctx, c.ID)
	if err != nil || current.Enabled || current.Generation != fresh.Generation+1 {
		t.Fatal("exact generation not revoked", current, err)
	}
}
