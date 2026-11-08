package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

// SQLite task/authorization fixtures. Synthetic receipts do not claim Rust,
// Enola or App acceptance; the separate actual substrate probe owns those.
func TestCodeJobsGenerationReplayCancellationAndAtomicPublication(t *testing.T) {
	ctx := context.Background()
	s, _ := mergeDB(t)
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if output, e := exec.Command("/usr/bin/git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	mustMerge(t, os.WriteFile(filepath.Join(repo, "main.ts"), []byte("export function syntheticCode(){}\n"), 0600))
	project, e := s.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic code tasks", Path: repo})
	mustMerge(t, e)
	before, e := s.Codes(ctx)
	mustMerge(t, e)
	if len(before) != 0 {
		t.Fatal("scan registered without explicit action")
	}
	a := records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: project.ID, Path: repo}
	c, e := s.CodeAction(ctx, a)
	mustMerge(t, e)
	if c.State != "queued" || c.Dataset == project.ID || c.Generation != 1 {
		t.Fatal(c)
	}
	m, e := codegraph.Capture(ctx, repo, "")
	mustMerge(t, e)
	j, e := s.StartCodeJob(ctx, c, m)
	mustMerge(t, e)
	replay, e := s.CodeAction(ctx, a)
	mustMerge(t, e)
	if !replay.Replayed || replay.ID != c.ID || replay.Generation != c.Generation {
		t.Fatal("action replay changed authorization", replay)
	}
	_, e = s.StartCodeJob(ctx, c, m)
	if e == nil {
		t.Fatal("concurrent duplicate code job allowed")
	}
	bad := a
	bad.Kind = "disable"
	_, e = s.CodeAction(ctx, bad)
	if !errors.Is(e, ErrConflict) {
		t.Fatal("operation ID changed meaning", e)
	}
	r := codegraph.Receipt{Dataset: c.Dataset, Snapshot: uuid.NewSHA1(uuid.MustParse(c.Dataset), []byte(m.Digest)).String(), SourceDigest: m.Digest, GraphDigest: uuid.NewString(), Extractor: "enola-0.4.30-v351", Nodes: 1}
	_, e = s.DB.Exec(`CREATE TRIGGER synthetic_code_publish_failure BEFORE UPDATE OF state ON code_worktrees WHEN NEW.state='ready' BEGIN SELECT RAISE(ABORT,'synthetic code publish fault'); END`)
	mustMerge(t, e)
	e = s.FinishCodeJob(ctx, j, &r, "")
	if e == nil {
		t.Fatal("publication fault ignored")
	}
	jobs, e := s.CodeJobs(ctx, c.ID)
	mustMerge(t, e)
	current, e := s.Code(ctx, c.ID)
	mustMerge(t, e)
	if len(jobs) != 1 || jobs[0].State != "running" || current.Snapshot != "" || current.Receipt != nil {
		t.Fatal("publication partially committed", jobs, current)
	}
	_, e = s.DB.Exec(`DROP TRIGGER synthetic_code_publish_failure`)
	mustMerge(t, e)
	mustMerge(t, s.FinishCodeJob(ctx, j, &r, ""))
	current, e = s.Code(ctx, c.ID)
	mustMerge(t, e)
	if current.State != "ready" || !reflect.DeepEqual(current.Receipt, &r) {
		t.Fatal(current)
	}
	disable := records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: c.Generation}
	off, e := s.CodeAction(ctx, disable)
	mustMerge(t, e)
	if off.Enabled || off.Generation != 2 || off.Snapshot != r.Snapshot {
		t.Fatal("disable lost history or scope", off)
	}
	if e = s.FinishCodeJob(ctx, j, &r, ""); !errors.Is(e, ErrConflict) {
		t.Fatal("late old generation accepted", e)
	}
	on, e := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: project.ID, Path: repo, ExpectedGeneration: off.Generation})
	mustMerge(t, e)
	j2, e := s.StartCodeJob(ctx, on, m)
	mustMerge(t, e)
	mustMerge(t, s.InterruptCode(ctx))
	interrupted, e := s.Code(ctx, c.ID)
	mustMerge(t, e)
	if interrupted.State != "interrupted" || interrupted.ObservedDigest != "" || interrupted.Snapshot != r.Snapshot {
		t.Fatal("restart recovery lost current snapshot", interrupted)
	}
	if e = s.FinishCodeJob(ctx, j2, &r, ""); !errors.Is(e, ErrConflict) {
		t.Fatal("interrupted attempt accepted", e)
	}
	j3, e := s.StartCodeJob(ctx, interrupted, m)
	mustMerge(t, e)
	if j3.ID == j2.ID {
		t.Fatal("recovery reused stale job ID")
	}
	mustMerge(t, s.FinishCodeJob(ctx, j3, nil, "graph_failed"))
	failed, e := s.Code(ctx, c.ID)
	mustMerge(t, e)
	if failed.State != "failed" || failed.Snapshot != r.Snapshot {
		t.Fatal("failed update erased prior graph", failed)
	}
	_, e = s.StartCodeJob(ctx, failed, m)
	if e == nil {
		t.Fatal("unchanged failed source retried every poll")
	}
}
