package store

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestProjectGrantChangesOnlyTargetAndFencesOldGeneration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var projects []records.Scope
	for _, name := range []string{"a", "b"} {
		path := filepath.Join(t.TempDir(), name)
		if out, err := exec.Command("git", "init", path).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		p, err := s.RegisterProject(ctx, records.ScopeDraft{Name: name, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, p)
	}
	scopes, err := s.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	global := ""
	for _, v := range scopes {
		if v.Kind == "preference" {
			global = v.ID
		}
	}
	conn, err := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "actual-contract", Project: projects[0].ID, AllowedScopes: []string{projects[0].ID, global}})
	if err != nil {
		t.Fatal(err)
	}
	change := records.ConnectionScopeChange{ConnectionID: conn.ID, ExpectedGeneration: conn.Generation, Project: projects[1].ID, Allowed: true}
	updated, err := s.ChangeProjectAccess(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != conn.Generation+1 || updated.Project != conn.Project || updated.Name != conn.Name || updated.Agent != conn.Agent || !reflect.DeepEqual(updated.AllowedScopes, append(conn.AllowedScopes, projects[1].ID)) {
		t.Fatal("unrelated connection fields changed", updated)
	}
	request := records.AgentRequest{ConnectionID: conn.ID, Generation: conn.Generation, Workspace: projects[0].Path, Tool: "memory_search", Arguments: records.AgentArguments{Scopes: []string{projects[1].ID}}}
	if _, _, err = s.Authorize(ctx, request); err == nil {
		t.Fatal("old generation retained access")
	}
	request.Generation = updated.Generation
	if _, _, err = s.Authorize(ctx, request); err != nil {
		t.Fatal("new generation lost explicit target grant", err)
	}
	if _, err = s.ChangeProjectAccess(ctx, change); err == nil {
		t.Fatal("stale editor overwrote another generation")
	}
	change.ExpectedGeneration = updated.Generation
	noop, err := s.ChangeProjectAccess(ctx, change)
	if err != nil || noop.Generation != updated.Generation {
		t.Fatal("repeat grant changed generation", noop, err)
	}
	change.Allowed = false
	removed, err := s.ChangeProjectAccess(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed.AllowedScopes, conn.AllowedScopes) || removed.Generation != updated.Generation+1 {
		t.Fatal("revoke removed another scope", removed)
	}
	request.Generation = removed.Generation
	if _, _, err = s.Authorize(ctx, request); err == nil {
		t.Fatal("revoked project remained accessible")
	}
	change.ExpectedGeneration, change.Project = removed.Generation, conn.Project
	if _, err = s.ChangeProjectAccess(ctx, change); err == nil {
		t.Fatal("primary project silently unbound")
	}
	latest, err := s.Connection(ctx, conn.ID)
	if err != nil || latest.Generation != removed.Generation {
		t.Fatal("failed primary revoke changed connection", latest, err)
	}
	change.Project = global
	if _, err = s.ChangeProjectAccess(ctx, change); err == nil {
		t.Fatal("project editor changed a global grant")
	}
	if err = s.Disconnect(ctx, conn.ID); err != nil {
		t.Fatal(err)
	}
	change.Project, change.ExpectedGeneration, change.Allowed = projects[1].ID, removed.Generation+1, true
	if _, err = s.ChangeProjectAccess(ctx, change); err == nil {
		t.Fatal("grant re-enabled a disconnected connection")
	}
}

func TestProjectGrantPreservesDeferredBindingAndRejectsRetiredConnection(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	repo := filepath.Join(t.TempDir(), "repository")
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	project, err := s.RegisterProject(ctx, records.ScopeDraft{Name: "项目", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := s.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	global := ""
	for _, v := range scopes {
		if v.Kind == "preference" {
			global = v.ID
		}
	}
	c, err := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "待绑定连接", AutoProject: true, Workspace: repo, AllowedScopes: []string{global}})
	if err != nil {
		t.Fatal(err)
	}
	change := records.ConnectionScopeChange{ConnectionID: c.ID, ExpectedGeneration: c.Generation, Project: project.ID, Allowed: true}
	updated, err := s.ChangeProjectAccess(ctx, change)
	if err != nil || updated.PendingProject != project.ID || !updated.AutoProject || updated.TrustedWorkspace != c.TrustedWorkspace || updated.TrustedRepository != c.TrustedRepository {
		t.Fatal("grant replaced deferred binding or trusted workspace", updated, err)
	}
	change.ExpectedGeneration, change.Allowed = updated.Generation, false
	if _, err = s.ChangeProjectAccess(ctx, change); err == nil {
		t.Fatal("pending binding revoked through the project editor")
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO connection_retirements(connection,restore) VALUES(?,?)`, c.ID, "isolated-retirement"); err != nil {
		t.Fatal(err)
	}
	change.Allowed = true
	if _, err = s.ChangeProjectAccess(ctx, change); err == nil {
		t.Fatal("project grant revived retired connection")
	}
	latest, err := s.Connection(ctx, c.ID)
	if err != nil || latest.Generation != updated.Generation {
		t.Fatal("failed authorization changed retired generation", latest, err)
	}
}
