package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestInitializationAgentModeAuthorizationAndSourceReceipt(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if out, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	check(os.WriteFile(filepath.Join(repo, "README.md"), []byte("本项目使用 pnpm 管理依赖。"), 0600))
	db, e := store.Open(filepath.Join(base, "memory.sqlite"))
	check(e)
	defer db.Close()
	project, e := db.RegisterProject(ctx, records.ScopeDraft{Name: "Agent initialization fixture", Path: repo})
	check(e)
	c, e := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "initializer", Project: project.ID, AllowedScopes: []string{project.ID}})
	check(e)
	b, e := db.StartInitialization(ctx, records.InitializationStart{OperationID: uuid.NewString(), ConnectionID: c.ID, Generation: c.Generation, Project: project.ID, Workspace: repo, Paths: []string{"README.md"}})
	check(e)
	s := &Server{store: db, work: make(chan struct{}, 1)}
	svc := &memory.Service{Store: db}
	req := records.AgentRequest{Protocol: 1, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, ToolMode: "initialization", Tool: "memory_initialization_get", Arguments: records.AgentArguments{Scope: project.ID, InitializationBatch: b.ID}}
	result, e := s.agent(ctx, req, svc)
	check(e)
	source := result.(records.InitializationBatch)
	if source.State != "pending" || source.Receipt != nil || len(source.Sources) != 1 || source.SourceReadMS <= 0 {
		t.Fatal("source read became import", source)
	}
	blocked := req
	blocked.Tool = "memory_save"
	blocked.Arguments.OperationID = "forbidden-ordinary-save"
	blocked.Arguments.Text = "bypass batch"
	if _, e = s.agent(ctx, blocked, svc); e == nil {
		t.Fatal("ordinary save allowed during initializer mode")
	}
	submit := req
	submit.Tool = "memory_initialize"
	submit.Arguments.OperationID = "stable-init"
	submit.Arguments.InitializationItems = []records.InitializationItem{{ID: "package-manager", Title: "依赖管理", Text: "项目使用 pnpm。", Sources: []records.InitializationQuote{{Path: source.Sources[0].Path, SHA256: source.Sources[0].SHA256, Quote: source.Sources[0].Text}}}}
	committed, e := s.agent(ctx, submit, svc)
	check(e)
	receipt := committed.(records.InitializationReceipt)
	if !receipt.Persisted || len(receipt.Items) != 1 {
		t.Fatal(receipt)
	}
	status := req
	status.Tool = "memory_initialization_status"
	result, e = s.agent(ctx, status, svc)
	check(e)
	stored := result.(records.InitializationBatch)
	if len(stored.Sources) != 0 || stored.State != "received" || stored.Saved != 1 || stored.Indexed != 0 {
		t.Fatal("status conflated saved/index", stored)
	}
	other, e := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "other", Project: project.ID, AllowedScopes: []string{project.ID}})
	check(e)
	wrong := req
	wrong.ConnectionID = other.ID
	wrong.Generation = other.Generation
	if _, e = s.agent(ctx, wrong, svc); e == nil {
		t.Fatal("other connection got source snapshot")
	}
	check(db.Disconnect(ctx, c.ID))
	if _, e = s.agent(ctx, submit, svc); e == nil {
		t.Fatal("revoked initializer replay")
	}
}
