package store

// Real SQLite/Git contract checks; no Agent/model run is claimed by these tests.
import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func projectTaskFixture(t *testing.T) (*Store, records.Scope, records.Connection) {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	s, err := Open(filepath.Join(base, "memory.sqlite"))
	mustMerge(t, err)
	t.Cleanup(func() { s.Close() })
	p, err := s.RegisterProject(context.Background(), records.ScopeDraft{Name: "项目整理契约", Path: repo})
	mustMerge(t, err)
	c, err := s.ConfigureConnection(context.Background(), records.ConnectionDraft{Agent: "codex", Name: "契约连接", Project: p.ID, AllowedScopes: []string{p.ID}})
	mustMerge(t, err)
	return s, p, c
}

func bindProjectFixture(t *testing.T, s *Store, p records.Scope, c records.Connection) records.ProjectBinding {
	t.Helper()
	mustMerge(t, s.TouchConnection(context.Background(), c.ID, c.Generation))
	b, err := s.ChangeProjectBinding(context.Background(), records.ProjectBindingChange{Project: p.ID, ConnectionID: c.ID, ExpectedGeneration: c.Generation})
	mustMerge(t, err)
	return b
}

func projectExecutorFixture(c records.Connection) records.ProjectTaskExecutor {
	return records.ProjectTaskExecutor{ConnectionID: c.ID, Generation: c.Generation, Agent: c.Agent,
		Version: "contract-version", Model: "bound-agent-current-model", ConfigurationDigest: strings.Repeat("a", 64)}
}

func TestProjectBindingIsExplicitVersionedAndDoesNotChangeProcessing(t *testing.T) {
	ctx := context.Background()
	s, p, c := projectTaskFixture(t)
	b, err := s.ProjectBinding(ctx, p.ID)
	mustMerge(t, err)
	if b.State != "unbound" || b.Revision != 0 {
		t.Fatal("connection implicitly bound the project", b)
	}
	d := records.ProjectBindingChange{Project: p.ID, ConnectionID: c.ID, ExpectedGeneration: c.Generation}
	if _, err = s.ChangeProjectBinding(ctx, d); err == nil {
		t.Fatal("unverified connection was accepted")
	}
	b = bindProjectFixture(t, s, p, c)
	if b.State != "available" || b.Revision != 1 || b.Agent != c.Agent {
		t.Fatal(b)
	}
	if _, err = s.ChangeProjectBinding(ctx, d); err == nil {
		t.Fatal("stale project editor overwrote binding")
	}
	d.ExpectedRevision = b.Revision
	noChange, err := s.ChangeProjectBinding(ctx, d)
	mustMerge(t, err)
	if noChange.Revision != b.Revision {
		t.Fatal("same binding incremented revision")
	}
	d.ConnectionID, d.ExpectedGeneration = "", 0
	cleared, err := s.ChangeProjectBinding(ctx, d)
	mustMerge(t, err)
	if cleared.Revision != 2 || cleared.State != "unbound" {
		t.Fatal("clear lost revision history", cleared)
	}
	if _, err = s.ChangeProjectBinding(ctx, records.ProjectBindingChange{Project: p.ID, ConnectionID: c.ID, ExpectedGeneration: c.Generation, ExpectedRevision: 1}); err == nil {
		t.Fatal("clear/rebind allowed a stale revision")
	}
	var generation, active, memories int
	mustMerge(t, s.DB.QueryRow(`SELECT generation,active FROM processing_current WHERE id=1`).Scan(&generation, &active))
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&memories))
	if generation != 0 || active != 0 || memories != 0 {
		t.Fatal("binding switched processing or created knowledge")
	}
}

func TestProjectTaskConcurrentReplayFixedScopeAndQueuedCancel(t *testing.T) {
	ctx := context.Background()
	s, p, c := projectTaskFixture(t)
	b := bindProjectFixture(t, s, p, c)
	d := records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}
	e := projectExecutorFixture(c)
	var results [2]records.ProjectTask
	var created [2]bool
	var failures [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], created[i], failures[i] = s.StartProjectTask(ctx, d, e, nil) }(i)
	}
	wg.Wait()
	for _, err := range failures {
		mustMerge(t, err)
	}
	if results[0].ID != results[1].ID || created[0] == created[1] || results[0].Snapshot.Executor.Model != e.Model || results[0].Snapshot.Workspace != p.Path {
		t.Fatal("replay duplicated task or changed its executor", results, created)
	}
	_, err := s.DB.Exec(`UPDATE project_tasks SET snapshot='{}' WHERE id=?`, results[0].ID)
	if err == nil {
		t.Fatal("task scope could be overwritten")
	}
	_, err = s.ChangeProjectBinding(ctx, records.ProjectBindingChange{Project: p.ID, ExpectedRevision: b.Revision})
	if err == nil {
		t.Fatal("active task allowed executor change")
	}
	other := d
	other.OperationID = uuid.NewString()
	if _, _, err = s.StartProjectTask(ctx, other, e, nil); err == nil {
		t.Fatal("second operation started a duplicate active task")
	}
	cancelled, err := s.CancelProjectTask(ctx, results[0].ID)
	mustMerge(t, err)
	if !cancelled.Cancelled || cancelled.State != "cancelled" {
		t.Fatal("queued cancellation claimed an active process", cancelled)
	}
	repeated, newTask, err := s.StartProjectTask(ctx, d, e, nil)
	mustMerge(t, err)
	if newTask || repeated.ID != cancelled.ID || repeated.State != "cancelled" {
		t.Fatal("replayed operation revived cancelled intent", repeated)
	}
}

func TestProjectTaskHistoryAndConnectionGenerationFences(t *testing.T) {
	ctx := context.Background()
	s, p, c := projectTaskFixture(t)
	b := bindProjectFixture(t, s, p, c)
	source := records.ProjectSessionSource{ProjectSessionSelection: records.ProjectSessionSelection{ID: "codex_current_project_session", Digest: strings.Repeat("b", 64)}, Agent: c.Agent, Project: p.ID}
	d := records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision, IncludeHistory: true, Sessions: []records.ProjectSessionSelection{source.ProjectSessionSelection}}
	e := projectExecutorFixture(c)
	wrong := source
	wrong.Agent = "pi"
	if _, _, err := s.StartProjectTask(ctx, d, e, []records.ProjectSessionSource{wrong}); err == nil {
		t.Fatal("another Agent's history was accepted")
	}
	wrong = source
	wrong.Project = uuid.NewString()
	if _, _, err := s.StartProjectTask(ctx, d, e, []records.ProjectSessionSource{wrong}); err == nil {
		t.Fatal("another project's history was accepted")
	}
	wrong = source
	wrong.Digest = strings.Repeat("c", 64)
	if _, _, err := s.StartProjectTask(ctx, d, e, []records.ProjectSessionSource{wrong}); err == nil {
		t.Fatal("changed history was accepted")
	}
	d.IncludeHistory = false
	if _, _, err := s.StartProjectTask(ctx, d, e, []records.ProjectSessionSource{source}); err == nil {
		t.Fatal("history entered an unchecked task")
	}
	d.IncludeHistory = true
	task, _, err := s.StartProjectTask(ctx, d, e, []records.ProjectSessionSource{source})
	mustMerge(t, err)
	mustMerge(t, s.Disconnect(ctx, c.ID))
	current, err := s.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if current.AccessState != "disconnected" || current.Snapshot.Executor.Generation != c.Generation {
		t.Fatal("disconnection rewrote frozen scope or retained authorization", current)
	}
	if _, _, err = s.StartProjectTask(ctx, d, e, []records.ProjectSessionSource{source}); err == nil {
		t.Fatal("revoked executor replay was accepted")
	}
}

func TestProjectTaskRepositoryReplacementAndRestoreInvalidateAuthority(t *testing.T) {
	ctx := context.Background()
	s, p, c := projectTaskFixture(t)
	b := bindProjectFixture(t, s, p, c)
	task, _, err := s.StartProjectTask(ctx, records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}, projectExecutorFixture(c), nil)
	mustMerge(t, err)
	mustMerge(t, os.Rename(p.Path, p.Path+"-previous"))
	if out, err := exec.Command("git", "init", p.Path).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	changed, err := s.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if changed.AccessState != "workspace_changed" {
		t.Fatal("same path hid repository replacement", changed)
	}
	_, snapshot, err := s.snapshot(ctx)
	mustMerge(t, err)
	restored, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, restored, 0600))
	copy, err := Open(path)
	mustMerge(t, err)
	defer copy.Close()
	old, err := copy.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if old.State != "interrupted" || old.Error != "backup_restored" || old.AccessState != "retired" {
		t.Fatal("restore revived project task authority", old)
	}
}

func TestProjectTaskSchema17BackupRetainsPreviousTables(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	path := filepath.Join(filepath.Dir(p.Path), "memory.sqlite")
	_, err := s.DB.Exec(`DROP TABLE project_tasks; DROP TABLE project_bindings; PRAGMA user_version=17`)
	mustMerge(t, err)
	s.Schema = 17
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v17-*.sqlite"))
	mustMerge(t, err)
	if current.Schema != 18 || len(backups) != 1 {
		t.Fatal("schema17 backup or migration absent", backups)
	}
	backup, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer backup.Close()
	var version int
	var saved string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`SELECT name FROM connections WHERE id=?`, c.ID).Scan(&saved))
	if version != 17 || saved != c.Name {
		t.Fatal("migration changed the previous backup", version, saved)
	}
	b, err := current.ProjectBinding(context.Background(), p.ID)
	mustMerge(t, err)
	if b.State != "unbound" {
		t.Fatal("migration inferred an organizer", b)
	}
}
