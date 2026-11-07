package store

// Filesystem/SQLite and structured-input fixtures. Actual App/Agent/Rust
// initialization acceptance is separate; these checks never call a model.
import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func initializationFixture(t *testing.T) (*Store, records.Connection, records.InitializationStart, records.InitializationBatch) {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if out, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	mustMerge(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("本项目使用 pnpm 管理依赖。\n依赖变更同时提交 pnpm-lock.yaml。\n"), 0600))
	s, e := Open(filepath.Join(base, "memory.sqlite"))
	mustMerge(t, e)
	t.Cleanup(func() { s.Close() })
	scope, e := s.RegisterProject(ctx, records.ScopeDraft{Name: "initialization fixture", Path: repo})
	mustMerge(t, e)
	c, e := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "initialization fixture", Project: scope.ID, AllowedScopes: []string{scope.ID}})
	mustMerge(t, e)
	d := records.InitializationStart{OperationID: uuid.NewString(), Project: scope.ID, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Paths: []string{"README.md"}}
	b, e := s.StartInitialization(ctx, d)
	mustMerge(t, e)
	return s, c, d, b
}
func initializationCommit(b records.InitializationBatch) records.InitializationCommit {
	source := b.Sources[0]
	return records.InitializationCommit{Batch: b.ID, OperationID: "stable-import", Items: []records.InitializationItem{{ID: "package-manager", Title: "依赖管理", Text: "本项目使用 pnpm，并提交锁文件。", Sources: []records.InitializationQuote{{Path: source.Path, SHA256: source.SHA256, Quote: "本项目使用 pnpm 管理依赖。"}}}}}
}
func TestInitializationActualSourcesAtomicReplayImportAndCancelAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, _, d, b := initializationFixture(t)
	if b.State != "pending" || b.Receipt != nil || len(b.Sources) != 1 || len(b.Sources[0].SHA256) != 64 {
		t.Fatal(b)
	}
	repeated, e := s.StartInitialization(ctx, d)
	mustMerge(t, e)
	if repeated.ID != b.ID {
		t.Fatal("copy created duplicate batch")
	}
	served, e := s.ReadInitializationSources(ctx, b)
	mustMerge(t, e)
	if served.SourceReadMS <= 0 || served.State != "pending" {
		t.Fatal("source query fabricated submission")
	}
	var n int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&n))
	if n != 0 {
		t.Fatal("pre-submit knowledge changed")
	}
	commit := initializationCommit(b)
	receipt, e := s.SubmitInitialization(ctx, b, commit)
	mustMerge(t, e)
	mustMerge(t, os.WriteFile(filepath.Join(d.Workspace, "README.md"), []byte("后来用户改变了文件。"), 0600))
	replay, e := s.SubmitInitialization(ctx, b, commit)
	mustMerge(t, e)
	if !reflect.DeepEqual(receipt, replay) {
		t.Fatal("committed receipt replay changed")
	}
	v, e := s.Current(ctx, receipt.Items[0].Memory)
	mustMerge(t, e)
	if v.Origin != "imported" || v.Revision != 1 || len(v.SourceDetail.References) != 1 {
		t.Fatal(v)
	}
	activity, e := s.Activities(ctx, "UTC")
	mustMerge(t, e)
	if activity.Imported != 1 || activity.Total != 0 {
		t.Fatal("import fabricated new-knowledge activity", activity)
	}
	cancelled, e := s.CancelInitialization(ctx, b.ID)
	mustMerge(t, e)
	if cancelled.State != "received" || cancelled.Receipt == nil {
		t.Fatal("cancel removed committed facts")
	}
}
func TestInitializationCancelSourceChangeAndQuoteGates(t *testing.T) {
	ctx := context.Background()
	t.Run("cancel", func(t *testing.T) {
		s, _, _, b := initializationFixture(t)
		_, e := s.CancelInitialization(ctx, b.ID)
		mustMerge(t, e)
		if _, e = s.ReadInitializationSources(ctx, b); e == nil {
			t.Fatal("cancelled sources served")
		}
		if _, e = s.SubmitInitialization(ctx, b, initializationCommit(b)); e == nil {
			t.Fatal("late cancelled import")
		}
	})
	t.Run("source-changed", func(t *testing.T) {
		s, _, d, b := initializationFixture(t)
		mustMerge(t, os.WriteFile(filepath.Join(d.Workspace, "README.md"), []byte("changed"), 0600))
		if _, e := s.SubmitInitialization(ctx, b, initializationCommit(b)); e == nil {
			t.Fatal("changed file accepted")
		}
		current, e := s.Initialization(ctx, b.ID)
		mustMerge(t, e)
		if current.State != "expired" || current.Error != "source_changed" {
			t.Fatal(current)
		}
	})
	t.Run("quote", func(t *testing.T) {
		s, _, _, b := initializationFixture(t)
		c := initializationCommit(b)
		c.Items[0].Sources[0].Quote = "没有出现的原文"
		if _, e := s.SubmitInitialization(ctx, b, c); e == nil {
			t.Fatal("invented quote")
		}
		current, e := s.Initialization(ctx, b.ID)
		mustMerge(t, e)
		if current.State != "pending" || current.Error == "" {
			t.Fatal("known failure not recorded")
		}
	})
}
func TestInitializationCrossWorktreeGenerationAndDuplicateIdentity(t *testing.T) {
	ctx := context.Background()
	s, c, d, b := initializationFixture(t)
	if _, e := s.CheckInitializationAgent(ctx, b.ID, c.ID, c.Generation, UsageDigest("wrong workspace"), []string{d.Project}); e == nil {
		t.Fatal("wrong worktree served")
	}
	other, e := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "other", Project: d.Project, AllowedScopes: []string{d.Project}})
	mustMerge(t, e)
	if _, e = s.CheckInitializationAgent(ctx, b.ID, other.ID, other.Generation, b.WorkspaceID, []string{d.Project}); e == nil {
		t.Fatal("other connection served")
	}
	first, e := s.SubmitInitialization(ctx, b, initializationCommit(b))
	mustMerge(t, e)
	d.OperationID = uuid.NewString()
	second, e := s.StartInitialization(ctx, d)
	mustMerge(t, e)
	commit := initializationCommit(second)
	newItem := commit.Items[0]
	newItem.ID = "new-before-duplicate"
	commit.Items = append([]records.InitializationItem{newItem}, commit.Items...)
	if _, e = s.SubmitInitialization(ctx, second, commit); e == nil {
		t.Fatal("cross-batch stable identity duplicated")
	}
	var n int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&n))
	if n != 1 {
		t.Fatal("partial duplicate import", n)
	}
	mustMerge(t, s.Disconnect(ctx, c.ID))
	if _, e = s.ReadInitializationSources(ctx, b); e == nil {
		t.Fatal("revoked source query")
	}
	if _, e = s.SubmitInitialization(ctx, b, initializationCommit(b)); e == nil {
		t.Fatal("revoked receipt replay")
	}
	v, e := s.Current(ctx, first.Items[0].Memory)
	mustMerge(t, e)
	if v.Revision != 1 {
		t.Fatal("duplicate or revoke changed knowledge")
	}
}
func TestInitializationSQLRollbackAndFileScope(t *testing.T) {
	ctx := context.Background()
	s, _, d, b := initializationFixture(t)
	_, e := s.DB.Exec(`CREATE TRIGGER initialization_version_fault BEFORE INSERT ON versions BEGIN SELECT RAISE(ABORT,'initialization SQL fixture'); END`)
	mustMerge(t, e)
	if _, e = s.SubmitInitialization(ctx, b, initializationCommit(b)); e == nil {
		t.Fatal("SQL fault succeeded")
	}
	var n int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&n))
	if n != 0 {
		t.Fatal("partial import")
	}
	current, e := s.Initialization(ctx, b.ID)
	mustMerge(t, e)
	if current.Receipt != nil || current.State != "pending" {
		t.Fatal("receipt survived rollback")
	}
	outside := filepath.Join(filepath.Dir(d.Workspace), "outside.md")
	mustMerge(t, os.WriteFile(outside, []byte("outside secret fixture"), 0600))
	mustMerge(t, os.Symlink(outside, filepath.Join(d.Workspace, "escape.md")))
	for _, path := range []string{"../outside.md", "escape.md", ".git/config", "auth.json"} {
		request := d
		request.OperationID = uuid.NewString()
		request.Paths = []string{path}
		if _, e = s.StartInitialization(ctx, request); e == nil {
			t.Fatal("unapproved path", path)
		}
	}
}

func TestInitializationConcurrentStartReplaysStableBatch(t *testing.T) {
	ctx := context.Background()
	s, _, d, _ := initializationFixture(t)
	d.OperationID = uuid.NewString()
	type response struct {
		batch records.InitializationBatch
		err   error
	}
	results := make(chan response, 8)
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); b, e := s.StartInitialization(ctx, d); results <- response{b, e} }()
	}
	wg.Wait()
	close(results)
	id := ""
	for result := range results {
		mustMerge(t, result.err)
		if id == "" {
			id = result.batch.ID
		}
		if result.batch.ID != id {
			t.Fatal("concurrent Start duplicated batch")
		}
	}
}
func TestInitializationConcurrentCommittedReplaySurvivesLaterFileChange(t *testing.T) {
	ctx := context.Background()
	s, _, d, b := initializationFixture(t)
	commit := initializationCommit(b)
	begun := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.initializationReader = func(ctx context.Context, root string, paths []string) ([]records.InitializationSource, error) {
		if calls.Add(1) == 1 {
			close(begun)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return readInitializationSources(ctx, root, paths)
	}
	type response struct {
		receipt records.InitializationReceipt
		err     error
	}
	result := make(chan response, 1)
	go func() { r, e := s.SubmitInitialization(ctx, b, commit); result <- response{r, e} }()
	select {
	case <-begun:
	case <-time.After(3 * time.Second):
		t.Fatal("first source reader did not begin")
	}
	r, e := s.SubmitInitialization(ctx, b, commit)
	mustMerge(t, e)
	mustMerge(t, os.WriteFile(filepath.Join(d.Workspace, "README.md"), []byte("changed after concurrent commit"), 0600))
	close(release)
	select {
	case replay := <-result:
		mustMerge(t, replay.err)
		if !reflect.DeepEqual(r, replay.receipt) {
			t.Fatal("durable commit misreported after file change")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replay blocked")
	}
	current, e := s.Initialization(ctx, b.ID)
	mustMerge(t, e)
	if current.State != "received" || current.Error != "" {
		t.Fatal("committed batch expired", current)
	}
}

func TestInitializationConcurrentStartReplaySurvivesLaterFileRemoval(t *testing.T) {
	ctx := context.Background()
	s, _, d, _ := initializationFixture(t)
	d.OperationID = uuid.NewString()
	begun := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.initializationReader = func(ctx context.Context, root string, paths []string) ([]records.InitializationSource, error) {
		if calls.Add(1) == 1 {
			close(begun)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return readInitializationSources(ctx, root, paths)
	}
	type response struct {
		batch records.InitializationBatch
		err   error
	}
	result := make(chan response, 1)
	go func() { b, e := s.StartInitialization(ctx, d); result <- response{b, e} }()
	select {
	case <-begun:
	case <-time.After(3 * time.Second):
		t.Fatal("first Start reader absent")
	}
	first, e := s.StartInitialization(ctx, d)
	mustMerge(t, e)
	mustMerge(t, os.Remove(filepath.Join(d.Workspace, "README.md")))
	close(release)
	select {
	case replay := <-result:
		mustMerge(t, replay.err)
		if replay.batch.ID != first.ID || !reflect.DeepEqual(replay.batch.Sources, first.Sources) {
			t.Fatal("concurrent original snapshot lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("start replay blocked")
	}
}
