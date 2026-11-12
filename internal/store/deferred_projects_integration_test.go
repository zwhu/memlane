package store

// Real SQLite and local Git checks. These validate authorization/transactions,
// not actual Agent collection, official HTTP identity or model execution.
import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func deferredFixture(t *testing.T) (*Store, string, []string) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	repo := filepath.Join(t.TempDir(), "repository")
	if out, err := exec.Command("/usr/bin/git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	scopes, err := s.Scopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, scope := range scopes {
		ids = append(ids, scope.ID)
	}
	return s, repo, ids
}

func TestDeferredFirstRecordCommitsProjectBindingAndRecordAtomically(t *testing.T) {
	s, repo, globals := deferredFixture(t)
	ctx := context.Background()
	c, err := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "first connection", AllowedScopes: globals, AutoProject: true})
	mustMerge(t, err)
	if c.Project != "" || c.PendingProject != "" {
		t.Fatal("connecting without a workspace created a project identity")
	}
	c, err = s.ConfigureConnection(ctx, records.ConnectionDraft{ID: c.ID, Agent: c.Agent, Name: c.Name, AllowedScopes: globals, AutoProject: true, Workspace: repo})
	mustMerge(t, err)
	scopes, err := s.Scopes(ctx)
	mustMerge(t, err)
	if len(scopes) != 2 || c.Project != "" || c.PendingProject == "" {
		t.Fatal("workspace approval registered an empty project", scopes, c)
	}
	b := records.CogneeBinding{Operation: uuid.NewString(), Instance: uuid.NewString(), Dataset: uuid.NewString(), Project: c.PendingProject, Endpoint: "http://127.0.0.1:45000", ConnectionID: c.ID, Generation: c.Generation, Workspace: repo}
	_, err = s.BindCognee(ctx, b)
	mustMerge(t, err)
	d := records.CogneeImport{ConnectionID: c.ID, Generation: c.Generation, Project: c.PendingProject, Workspace: repo, ExternalID: "cognee://" + b.Dataset + "/sessions/first-session/qa/" + uuid.NewString(), Origin: "raw", Text: "真实流程的合成输入：使用 pnpm。"}
	bad := d
	bad.Text = ""
	if _, err = s.ImportCognee(ctx, bad); err == nil {
		t.Fatal("empty material was accepted")
	}
	_, err = s.DB.Exec(`CREATE TRIGGER first_record_fault BEFORE INSERT ON memories BEGIN SELECT RAISE(FAIL,'first record fault'); END`)
	mustMerge(t, err)
	if _, err = s.ImportCognee(ctx, d); err == nil {
		t.Fatal("first-record fault did not fail")
	}
	scopes, err = s.Scopes(ctx)
	mustMerge(t, err)
	still, err := s.Connection(ctx, c.ID)
	mustMerge(t, err)
	if len(scopes) != 2 || still.Project != "" || still.PendingProject != c.PendingProject {
		t.Fatal("failed first record left an empty project or promoted grant")
	}
	var pending, normal int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM pending_cognee_bindings`).Scan(&pending))
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM cognee_bindings`).Scan(&normal))
	if pending != 1 || normal != 0 {
		t.Fatal("failed record promoted its pending binding")
	}
	_, err = s.DB.Exec(`DROP TRIGGER first_record_fault`)
	mustMerge(t, err)
	saved, err := s.ImportCognee(ctx, d)
	mustMerge(t, err)
	promoted, err := s.Connection(ctx, c.ID)
	mustMerge(t, err)
	scopes, err = s.Scopes(ctx)
	mustMerge(t, err)
	if len(scopes) != 3 || promoted.Project != c.PendingProject || promoted.PendingProject != "" || promoted.Generation != c.Generation || saved.Record.Dataset != promoted.Project {
		t.Fatal("first accepted record did not establish its project and grant", promoted, saved)
	}
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM pending_cognee_bindings`).Scan(&pending))
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM cognee_bindings`).Scan(&normal))
	if pending != 0 || normal != 1 {
		t.Fatal("pending binding not promoted with the record")
	}
	replayed, err := s.ImportCognee(ctx, d)
	mustMerge(t, err)
	if replayed.Record.Memory != saved.Record.Memory || replayed.SavedRevision != 1 {
		t.Fatal("first record replay duplicated memory")
	}
	mustMerge(t, s.Disconnect(ctx, c.ID))
	d.ExternalID = "cognee://" + b.Dataset + "/sessions/first-session/qa/" + uuid.NewString()
	if _, err = s.ImportCognee(ctx, d); err == nil {
		t.Fatal("revoked grant accepted late material")
	}
}

func TestDeferredProjectsReuseRepositoryAndRejectUnapprovedWorkspace(t *testing.T) {
	s, repo, globals := deferredFixture(t)
	ctx := context.Background()
	draft := records.ConnectionDraft{Agent: "codex", Name: "first", AllowedScopes: globals, AutoProject: true, Workspace: repo}
	a, err := s.ConfigureConnection(ctx, draft)
	mustMerge(t, err)
	draft.Name = "second"
	b, err := s.ConfigureConnection(ctx, draft)
	mustMerge(t, err)
	if a.PendingProject != b.PendingProject || a.ID == b.ID {
		t.Fatal("same repository did not share its future project")
	}
	foreign := filepath.Join(t.TempDir(), "other-clone")
	if out, err := exec.Command("/usr/bin/git", "init", foreign).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	r := records.AgentRequest{Protocol: 1, ConnectionID: a.ID, Generation: a.Generation, Workspace: foreign, Tool: "memory_save", Arguments: records.AgentArguments{Text: "unexpected repository", OperationID: "one"}}
	if _, _, err = s.ResolveAgentScopes(ctx, r); err == nil {
		t.Fatal("an arbitrary bridge workspace expanded the grant")
	}
	draft.Workspace = foreign
	other, err := s.ConfigureConnection(ctx, draft)
	mustMerge(t, err)
	if other.PendingProject == a.PendingProject {
		t.Fatal("independent clones were merged")
	}
	project, err := s.RegisterProject(ctx, records.ScopeDraft{Name: "manual choice", Path: repo})
	mustMerge(t, err)
	if project.ID != a.PendingProject {
		t.Fatal("manual registration ignored the pending repository identity")
	}
	draft.Workspace = repo
	reused, err := s.ConfigureConnection(ctx, draft)
	mustMerge(t, err)
	if reused.PendingProject != project.ID {
		t.Fatal("existing repository project was not reused")
	}
}
