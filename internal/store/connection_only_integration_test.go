package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

// Real SQLite authorization contract; no Agent configuration or model calls.
func TestConnectionOnlyDoesNotGrantMemoryAccess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	scopes, err := s.Scopes(ctx)
	if err != nil || len(scopes) == 0 {
		t.Fatal("missing built-in scopes", err)
	}
	d := records.ConnectionDraft{Agent: "pi", Name: "电脑级测试", ConnectionOnly: true}
	c, err := s.ConfigureConnection(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || len(c.AllowedScopes) != 0 || c.Project != "" || c.AutoProject || c.TrustedWorkspace != "" {
		t.Fatal("connection granted implicit access", c)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := records.AgentRequest{ConnectionID: c.ID, Generation: c.Generation, Tool: "memory_scopes"}
	_, allowed, err := s.Authorize(ctx, r)
	if err != nil || len(allowed) != 0 {
		t.Fatal("empty scope handshake failed", allowed, err)
	}
	for _, tool := range []string{"memory_search", "memory_get", "memory_save", "memory_adopt", "memory_status"} {
		r.Tool = tool
		r.Arguments = records.AgentArguments{}
		if _, _, err = s.Authorize(ctx, r); err == nil {
			t.Fatal("unscoped operation authorized", tool)
		}
		r.Arguments.Scope = scopes[0].ID
		if _, _, err = s.Authorize(ctx, r); err == nil {
			t.Fatal("explicit unauthorized scope accepted", tool)
		}
	}
	if err = s.TouchConnection(ctx, c.ID, c.Generation); err != nil {
		t.Fatal(err)
	}
	if err = s.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	r.Tool = "memory_scopes"
	r.Arguments = records.AgentArguments{}
	if _, _, err = s.Authorize(ctx, r); err == nil {
		t.Fatal("disconnected identity still usable")
	}
}

func TestConnectionOnlyRequiresExplicitAndEmptyAuthorization(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, d := range []records.ConnectionDraft{
		{Agent: "pi", Name: "accidental empty"},
		{Agent: "pi", Name: "scope", ConnectionOnly: true, AllowedScopes: []string{"anything"}},
		{Agent: "pi", Name: "project", ConnectionOnly: true, Project: "anything"},
		{Agent: "pi", Name: "automatic", ConnectionOnly: true, AutoProject: true},
		{Agent: "pi", Name: "workspace", ConnectionOnly: true, Workspace: t.TempDir()},
	} {
		if _, err = s.ConfigureConnection(context.Background(), d); err == nil {
			t.Fatal("ambiguous authorization accepted", d)
		}
	}
}
