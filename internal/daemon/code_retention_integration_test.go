package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

// Real SQLite publication/retention; synthetic receipts are NO MODEL/RUST.
func TestCodeRetentionKeepsCurrentPreviousAndAllSQLHistory(t *testing.T) {
	s, c, _ := codeFaultFixture(t)
	ctx := context.Background()
	receipts := []string{}
	for _, text := range []string{"export function first(){}", "export function second(){}", "export function third(){}"} {
		if err := os.WriteFile(filepath.Join(c.Worktree, "file.ts"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		m, err := codegraph.Capture(ctx, c.Worktree, "")
		if err != nil {
			t.Fatal(err)
		}
		j, err := s.store.StartCodeJob(ctx, c, m)
		if err != nil {
			t.Fatal(err)
		}
		r := codegraph.Receipt{Dataset: c.Dataset, Snapshot: uuid.NewSHA1(uuid.MustParse(c.Dataset), []byte(m.Digest)).String(), SourceDigest: m.Digest, GraphDigest: uuid.NewString(), Extractor: "enola-0.4.30-v351", Nodes: 1}
		if err := s.store.FinishCodeJob(ctx, j, &r, ""); err != nil {
			t.Fatal(err)
		}
		// Deterministic ordering; no sleeps or invented user timestamps.
		if _, err := s.store.DB.Exec(`UPDATE code_jobs SET finished_ms=? WHERE id=?`, len(receipts)+1, j.ID); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, r.Snapshot)
		c, err = s.store.Code(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	off, err := s.codeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: c.Generation})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := s.store.CodeRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled || !reflect.DeepEqual(keep[c.Dataset], []string{receipts[2], receipts[1]}) {
		t.Fatal(off, keep)
	}
	jobs, err := s.store.CodeJobs(ctx, c.ID)
	if err != nil || len(jobs) != 3 {
		t.Fatal(jobs, err)
	}
	if _, err := s.store.DB.Exec(`UPDATE code_jobs SET receipt='broken' WHERE id=?`, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CodeRetention(ctx); err == nil {
		t.Fatal("corrupt history permitted pruning")
	}
}

func TestCodeSourceFailureClearsPriorObservedDigestAndSkipsPoll(t *testing.T) {
	s, c, m := codeFaultFixture(t)
	ctx := context.Background()
	j, err := s.store.StartCodeJob(ctx, c, m)
	if err != nil {
		t.Fatal(err)
	}
	r := codegraph.Receipt{Dataset: c.Dataset, Snapshot: uuid.NewSHA1(uuid.MustParse(c.Dataset), []byte(m.Digest)).String(), SourceDigest: m.Digest, GraphDigest: uuid.NewString(), Extractor: "enola-0.4.30-v351", Nodes: 1}
	if err := s.store.FinishCodeJob(ctx, j, &r, ""); err != nil {
		t.Fatal(err)
	}
	c, err = s.store.Code(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.CodeFailure(ctx, c, "source_unavailable"); err != nil {
		t.Fatal(err)
	}
	failed, err := s.store.Code(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ObservedDigest != "" || failed.Snapshot != r.Snapshot {
		t.Fatal(failed)
	}
	reads := 0
	s.codeCapture = func(context.Context, string, string) (codegraph.Manifest, error) { reads++; return m, nil }
	s.refreshCode(ctx, failed)
	if reads != 0 {
		t.Fatal("source failure automatically rescanned")
	}
	// G1 databases can contain a capture failure with the old successful digest.
	if _, err := s.store.DB.Exec(`UPDATE code_worktrees SET observed_digest=? WHERE id=?`, m.Digest, c.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.store.Code(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.refreshCode(ctx, legacy)
	if reads != 0 {
		t.Fatal("legacy G1 capture failure automatically rescanned")
	}
}
