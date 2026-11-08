package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func codeFaultFixture(t *testing.T) (*Server, records.CodeState, codegraph.Manifest) {
	t.Helper()
	root := t.TempDir()
	db, e := store.Open(filepath.Join(root, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	repo := filepath.Join(root, "repo")
	if output, e := exec.Command("/usr/bin/git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	if e = os.WriteFile(filepath.Join(repo, "file.ts"), []byte("export function syntheticFault(){}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := db.RegisterProject(context.Background(), records.ScopeDraft{Name: "synthetic daemon code faults", Path: repo})
	if e != nil {
		t.Fatal(e)
	}
	c, e := db.CodeAction(context.Background(), records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: p.ID, Path: repo})
	if e != nil {
		t.Fatal(e)
	}
	m, e := codegraph.Capture(context.Background(), repo, "")
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{store: db, codeRuns: map[string]context.CancelFunc{}, codeWake: make(chan struct{}, 1)}
	return s, c, m
}

// Explicit NO MODEL SQLite publication fault. This exercises the actual daemon
// retry/terminal path, rather than calling Store.Finish manually to repair it.
func TestCodePublicationFaultReachesFailedTerminal(t *testing.T) {
	s, c, m := codeFaultFixture(t)
	ctx := context.Background()
	j, e := s.store.StartCodeJob(ctx, c, m)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.store.DB.Exec(`CREATE TRIGGER synthetic_code_publication_fault BEFORE UPDATE OF state ON code_worktrees WHEN NEW.state='ready' BEGIN SELECT RAISE(ABORT,'synthetic publish fault'); END`)
	if e != nil {
		t.Fatal(e)
	}
	r := codegraph.Receipt{Dataset: c.Dataset, Snapshot: uuid.NewSHA1(uuid.MustParse(c.Dataset), []byte(m.Digest)).String(), SourceDigest: m.Digest, GraphDigest: uuid.NewString(), Extractor: "enola-0.4.30-v351", Nodes: 1}
	s.finishCodePublication(ctx, j, &r, "")
	current, e := s.store.Code(ctx, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	jobs, e := s.store.CodeJobs(ctx, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if current.State != "failed" || current.Error != "publication_failed" || current.Snapshot != "" || len(jobs) != 1 || jobs[0].State != "failed" {
		t.Fatal("publication fault stayed active or applied", current, jobs)
	}
}

func TestCodeOldBatchAndInitialCaptureCancellation(t *testing.T) {
	s, c, _ := codeFaultFixture(t)
	ctx := context.Background()
	_, e := s.codeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: c.Generation})
	if e != nil {
		t.Fatal(e)
	}
	reads := 0
	s.codeCapture = func(ctx context.Context, path, shadow string) (codegraph.Manifest, error) {
		reads++
		return codegraph.Capture(ctx, path, shadow)
	}
	s.refreshCode(ctx, c)
	if reads != 0 {
		t.Fatal("stale enabled batch scanned disabled worktree")
	}
	off, e := s.store.Code(ctx, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	on, e := s.codeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: c.Project, Path: c.Worktree, ExpectedGeneration: off.Generation})
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	captured := make(chan error, 1)
	s.codeCapture = func(ctx context.Context, _, _ string) (codegraph.Manifest, error) {
		close(started)
		<-ctx.Done()
		captured <- ctx.Err()
		return codegraph.Manifest{}, ctx.Err()
	}
	done := make(chan struct{})
	go func() { defer close(done); s.refreshCode(ctx, on) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial capture not entered")
	}
	_, e = s.codeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: on.ID, ExpectedGeneration: on.Generation})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-captured:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial capture had no cancellation owner")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled initial capture did not stop")
	}
}
