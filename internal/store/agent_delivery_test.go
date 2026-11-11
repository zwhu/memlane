package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestD04BoundSaveEpochRevocationReplaySQLite(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	ctx := context.Background()
	c, err := s.Connection(ctx, a.ConnectionID)
	mustMerge(t, err)
	library, err := s.CogneeLibraryIdentity(ctx)
	mustMerge(t, err)
	c.ContextProject = a.Project
	grant := d04WorkspaceGrant(t, ctx, a.Workspace, a.Project, c, library)
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID+"\x00original-operation")).String()
	m := Mutation{Kind: "save", Memory: id, Dataset: a.Project, OperationID: "agent:" + id, Text: "synthetic immutable original", Title: "synthetic", Source: c.Agent, Origin: "knowledge"}
	one, err := s.ApplyAgentSaveInLibrary(ctx, c, m, library, grant)
	mustMerge(t, err)
	two, err := s.ApplyAgentSaveInLibrary(ctx, c, m, library, grant)
	mustMerge(t, err)
	if one.Memory != two.Memory || two.Revision != 1 {
		t.Fatal(one, two)
	}
	before := cogneeImportCounts(t, s)
	changed := m
	changed.Text = "changed"
	if _, err = s.ApplyAgentSaveInLibrary(ctx, c, changed, library, grant); err == nil {
		t.Fatal("changed operation accepted")
	}
	mustMerge(t, sameD04Counts(s, before))
	_, err = s.DB.Exec(`UPDATE cognee_library_identity SET identity=? WHERE id=1`, uuid.NewString())
	mustMerge(t, err)
	next := m
	next.Memory = uuid.NewString()
	next.OperationID = "agent:" + next.Memory
	if _, err = s.ApplyAgentSaveInLibrary(ctx, c, next, library, grant); err == nil {
		t.Fatal("cross-restore save accepted")
	}
	mustMerge(t, sameD04Counts(s, before))
	library, err = s.CogneeLibraryIdentity(ctx)
	mustMerge(t, err)
	grant.LibraryID = library
	_, err = s.DB.Exec(`UPDATE connections SET generation=generation+1 WHERE id=?`, c.ID)
	mustMerge(t, err)
	if _, err = s.ApplyAgentSaveInLibrary(ctx, c, next, library, grant); err == nil {
		t.Fatal("stale generation accepted")
	}
	mustMerge(t, sameD04Counts(s, before))
	c, err = s.Connection(ctx, c.ID)
	mustMerge(t, err)
	c.ContextProject = a.Project
	grant.Generation = c.Generation
	_, err = s.DB.Exec(`UPDATE connections SET allowed_scopes='[]' WHERE id=?`, c.ID)
	mustMerge(t, err)
	if _, err = s.ApplyAgentSaveInLibrary(ctx, c, next, library, grant); err == nil {
		t.Fatal("revoked scope accepted")
	}
	mustMerge(t, sameD04Counts(s, before))
}
func sameD04Counts(s *Store, before []int) error {
	for i, table := range []string{"memories", "versions", "operations", "jobs", "activities", "organization_sources", "derived_memories"} {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			return err
		}
		if n != before[i] {
			return errors.New("rejected delivery changed " + table)
		}
	}
	return nil
}
func TestD04ActualRestoredSnapshotRefusesOldLibraryEvenWithCopiedOperation(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	ctx := context.Background()
	c, e := s.Connection(ctx, a.ConnectionID)
	mustMerge(t, e)
	library, e := s.CogneeLibraryIdentity(ctx)
	mustMerge(t, e)
	c.ContextProject = a.Project
	grant := d04WorkspaceGrant(t, ctx, a.Workspace, a.Project, c, library)
	id := uuid.NewString()
	m := Mutation{Kind: "save", Memory: id, Dataset: a.Project, OperationID: "agent:" + id, Text: "synthetic before restore", Source: c.Agent, Origin: "knowledge"}
	_, e = s.ApplyAgentSaveInLibrary(ctx, c, m, library, grant)
	mustMerge(t, e)
	_, snapshot, e := s.snapshot(ctx)
	mustMerge(t, e)
	restored, _, e := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, e)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, restored, 0600))
	copy, e := Open(path)
	mustMerge(t, e)
	defer copy.Close()
	before := cogneeImportCounts(t, copy)
	if _, e = copy.ApplyAgentSaveInLibrary(ctx, c, m, library, grant); e == nil {
		t.Fatal("restored copy accepted pre-restore operation")
	}
	mustMerge(t, sameD04Counts(copy, before))
	actual, e := copy.CogneeLibraryIdentity(ctx)
	mustMerge(t, e)
	if actual == library {
		t.Fatal("restore failed to rotate epoch")
	}
}
func TestD04AuthorizationObservationDoesNotPopulateRepositoryCache(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	ctx := context.Background()
	c, e := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic unbound", AllowedScopes: []string{a.Project}})
	mustMerge(t, e)
	_, e = s.DB.Exec(`DELETE FROM repositories`)
	mustMerge(t, e)
	s.DB.SetMaxOpenConns(1)
	_, e = s.DB.Exec(`PRAGMA query_only=ON`)
	mustMerge(t, e)
	r := records.AgentRequest{Protocol: 1, ConnectionID: c.ID, Generation: c.Generation, Workspace: a.Workspace, Tool: "memory_save"}
	actual, scopes, e := s.AuthorizeReadOnly(ctx, r)
	mustMerge(t, e)
	if actual.ContextProject != a.Project || len(scopes) != 1 || scopes[0] != a.Project {
		t.Fatal(actual, scopes)
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM repositories`).Scan(&count))
	if count != 0 {
		t.Fatal("read-only authority changed cache")
	}
	_, e = s.DB.Exec(`PRAGMA query_only=OFF`)
	mustMerge(t, e)
	_, _, e = s.Authorize(ctx, r)
	mustMerge(t, e)
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM repositories`).Scan(&count))
	if count != 1 {
		t.Fatal("legacy cache behavior was changed", count)
	}
}

func d04WorkspaceGrant(t *testing.T, ctx context.Context, path, project string, c records.Connection, library string) records.AgentDeliveryGrant {
	t.Helper()
	canonical, digest, e := DeliveryWorkspaceIdentity(ctx, project, path)
	mustMerge(t, e)
	return records.AgentDeliveryGrant{Agent: c.Agent, LibraryID: library, ConnectionID: c.ID, Generation: c.Generation, Scope: project, Workspace: canonical, WorkspaceDigest: digest}
}
func TestD04BoundSaveRejectsSamePathWorkspaceAndGitReplacement(t *testing.T) {
	for _, gitOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(gitOnly), func(t *testing.T) {
			s, a, _ := cogneeImportFixture(t)
			ctx := context.Background()
			c, _, e := s.AuthorizeReadOnly(ctx, records.AgentRequest{Protocol: 1, ConnectionID: a.ConnectionID, Generation: a.Generation, Workspace: a.Workspace, Tool: "memory_save"})
			mustMerge(t, e)
			library, e := s.CogneeLibraryIdentity(ctx)
			mustMerge(t, e)
			grant := d04WorkspaceGrant(t, ctx, a.Workspace, a.Project, c, library)
			before := cogneeImportCounts(t, s)
			old := a.Workspace
			if gitOnly {
				old = filepath.Join(a.Workspace, ".git")
			}
			mustMerge(t, os.Rename(old, old+"-original"))
			out, e := exec.Command("/usr/bin/git", "init", "-q", a.Workspace).CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			_, current, e := DeliveryWorkspaceIdentity(ctx, c.ContextProject, grant.Workspace)
			mustMerge(t, e)
			if current == grant.WorkspaceDigest {
				t.Fatal("same path replacement retained physical identity")
			}
			id := uuid.NewString()
			m := Mutation{Kind: "save", OperationID: "agent:" + id, Memory: id, Dataset: a.Project, Text: "synthetic original workspace", Origin: "knowledge", Source: c.Agent}
			_, e = s.ApplyAgentSaveInLibrary(ctx, c, m, library, grant)
			var access *records.AccessError
			if !errors.As(e, &access) || access.Code != "delivery_authorization_changed" {
				t.Fatal("replacement not rejected at transaction boundary", e)
			}
			mustMerge(t, sameD04Counts(s, before))
		})
	}
}
func TestD04FrozenWorkspaceCheckUsesOnlyStatAndRejectsReplacement(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	_ = s
	ctx := context.Background()
	snapshot, e := resolveDeliveryWorkspace(ctx, a.Project, a.Workspace)
	mustMerge(t, e)
	head := filepath.Join(a.Workspace, ".git", "HEAD")
	mustMerge(t, os.Remove(head))
	if _, _, e = DeliveryWorkspaceIdentity(ctx, a.Project, a.Workspace); e == nil {
		t.Fatal("fixture still resolves as a Git repository")
	}
	if e = snapshot.check(); e != nil {
		t.Fatal("stat-only identity check ran repository resolution", e)
	}
	git := filepath.Join(a.Workspace, ".git")
	mustMerge(t, os.Rename(git, git+"-old"))
	out, e := exec.Command("/usr/bin/git", "init", "-q", a.Workspace).CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	if e = snapshot.check(); e == nil {
		t.Fatal("frozen common-directory replacement accepted")
	}
}
