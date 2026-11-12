package store

import (
	"context"
	"database/sql"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

func TestOfficialCodeExplicitScopeGenerationAndEngineSeparation(t *testing.T) {
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	scopes, err := s.Codes(ctx)
	mustMerge(t, err)
	if len(scopes) != 0 {
		t.Fatal("connection enabled a code scan")
	}
	scope := records.OfficialCodeScope{Instance: b.Instance, Dataset: uuid.NewString(), User: uuid.NewString(), Endpoint: b.Endpoint}
	a := records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: b.Project, Path: b.Workspace, Official: &scope}
	c, err := s.CodeAction(ctx, a)
	mustMerge(t, err)
	if c.Official == nil || *c.Official != scope || c.Dataset == scope.Dataset {
		t.Fatal("official engine/dataset identity lost", c)
	}
	mustMerge(t, s.AuthorizeOfficialCode(ctx, c))
	if _, err := s.StartCodeJob(ctx, c, codegraph.Manifest{}); err == nil {
		t.Fatal("official scope entered local worker job")
	}
	stop, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: c.Generation})
	mustMerge(t, err)
	if stop.Enabled || stop.Official == nil || s.AuthorizeOfficialCode(ctx, c) == nil {
		t.Fatal("disabled generation remained authorized")
	}
	replay, err := s.CodeAction(ctx, a)
	mustMerge(t, err)
	if !replay.Replayed || s.AuthorizeOfficialCode(ctx, replay) == nil {
		t.Fatal("replay reactivated old scope")
	}
	// Legacy callers omitting a backend must not turn an existing official
	// record back into a second local scanner.
	on, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: b.Project, Path: b.Workspace, ExpectedGeneration: stop.Generation})
	mustMerge(t, err)
	if on.Official == nil || *on.Official != scope {
		t.Fatal("omitted field changed engine")
	}
	mustMerge(t, s.AuthorizeOfficialCode(ctx, on))
	changed := scope
	changed.Dataset = uuid.NewString()
	a.OperationID = uuid.NewString()
	a.ExpectedGeneration = on.Generation
	a.Official = &changed
	if _, err := s.CodeAction(ctx, a); err == nil {
		t.Fatal("old official dataset silently replaced")
	}
	fresh, err := s.Code(ctx, c.ID)
	mustMerge(t, err)
	if fresh.Generation != on.Generation {
		t.Fatal("rejected scope changed state")
	}
	// The reverse association is also denied: code permission is not a
	// historical session/knowledge import grant.
	foreign := b
	foreign.Operation = uuid.NewString()
	foreign.Dataset = scope.Dataset
	if _, err := s.BindCognee(ctx, foreign); err == nil {
		t.Fatal("code dataset granted knowledge/session access")
	}
}

func TestOfficialCodeRejectsKnowledgeDatasetAndPreservesLocalSnapshot(t *testing.T) {
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	scope := records.OfficialCodeScope{Instance: b.Instance, Dataset: b.Dataset, User: uuid.NewString(), Endpoint: b.Endpoint}
	a := records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: b.Project, Path: b.Workspace, Official: &scope}
	if _, err := s.CodeAction(ctx, a); err == nil {
		t.Fatal("knowledge dataset adopted as code graph")
	}
	states, err := s.Codes(ctx)
	mustMerge(t, err)
	if len(states) != 0 {
		t.Fatal("failed binding left code scan enabled")
	}
	a.Official = nil
	a.OperationID = uuid.NewString()
	local, err := s.CodeAction(ctx, a)
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE code_worktrees SET snapshot='retained-local-history' WHERE id=?`, local.ID)
	mustMerge(t, err)
	scope.Dataset = uuid.NewString()
	a.Official = &scope
	a.OperationID = uuid.NewString()
	a.ExpectedGeneration = local.Generation
	if _, err := s.CodeAction(ctx, a); err == nil {
		t.Fatal("two engines enabled without stopping local backend")
	}
	stop, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: local.ID, ExpectedGeneration: local.Generation})
	mustMerge(t, err)
	a.OperationID = uuid.NewString()
	a.ExpectedGeneration = stop.Generation
	official, err := s.CodeAction(ctx, a)
	mustMerge(t, err)
	if official.Snapshot != "retained-local-history" || official.Official == nil {
		t.Fatal("switch deleted old snapshot metadata")
	}
}

func TestOfficialCodeMigration36KeepsDefaultLegacyAndBackup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "schema36.sqlite")
	s, err := Open(path)
	mustMerge(t, err)
	repo := filepath.Join(filepath.Dir(path), "repo")
	if output, err := exec.Command("/usr/bin/git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	project, err := s.RegisterProject(ctx, records.ScopeDraft{Name: "retained before official-code migration", Path: repo})
	mustMerge(t, err)
	legacy, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: project.ID, Path: repo})
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE code_worktrees SET snapshot='old-local-snapshot' WHERE id=?`, legacy.ID)
	mustMerge(t, err)
	before := sqlRows(t, s.DB, "code_worktrees")
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;PRAGMA user_version=36;`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	s, err = Open(path)
	mustMerge(t, err)
	defer s.Close()
	var version, count int
	mustMerge(t, s.DB.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM official_code_scopes`).Scan(&count))
	if version != SchemaVersion || count != 0 {
		t.Fatal("migration enabled official code", version, count)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v36-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 {
		t.Fatal("upgrade backup missing", backups)
	}
	backup, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer backup.Close()
	var priorVersion int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&priorVersion))
	if priorVersion != 36 || !reflect.DeepEqual(before, sqlRows(t, backup, "code_worktrees")) || !reflect.DeepEqual(before, sqlRows(t, s.DB, "code_worktrees")) {
		t.Fatal("migration or backup changed old code state")
	}
	states, err := s.Codes(ctx)
	mustMerge(t, err)
	if len(states) != 1 || !states[0].Enabled || states[0].Official != nil || s.AuthorizeOfficialCode(ctx, states[0]) == nil {
		t.Fatal("migration changed the old backend or invented official permission")
	}
}
