package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

func officialCodeJobFixture(t *testing.T) (*Store, records.CodeState, codegraph.Manifest) {
	t.Helper()
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	mustMerge(t, os.WriteFile(filepath.Join(b.Workspace, "fixture.ts"), []byte("export const fixture = 1;\n"), 0600))
	scope := records.OfficialCodeScope{Instance: b.Instance, Dataset: uuid.NewString(), User: uuid.NewString(), Endpoint: b.Endpoint}
	c, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: b.Project, Path: b.Workspace, Official: &scope})
	mustMerge(t, err)
	m, err := codegraph.Capture(ctx, c.Worktree, "")
	mustMerge(t, err)
	return s, c, m
}

func officialCodeTestReceipt(j records.CodeJob) records.OfficialCodeReceipt {
	return records.OfficialCodeReceipt{Operation: j.ID, Instance: j.Official.Instance, Dataset: j.Official.Dataset, User: j.Official.User, SourceDigest: j.Manifest.Digest, Status: "completed", DataID: uuid.NewString(), PipelineRunID: uuid.NewString()}
}

func TestOfficialCodeJobsReplayAtomicReceiptAndTypedRead(t *testing.T) {
	s, c, m := officialCodeJobFixture(t)
	ctx := context.Background()
	op := uuid.NewString()
	j, err := s.StartOfficialCodeJob(ctx, op, c, m)
	mustMerge(t, err)
	if j.Replayed || j.State != "running" || j.Official == nil {
		t.Fatal(j)
	}
	replay, err := s.StartOfficialCodeJob(ctx, op, c, m)
	mustMerge(t, err)
	if !replay.Replayed || replay.ID != j.ID {
		t.Fatal("lost response could restart execution")
	}
	changed := m
	changed.Digest = UsageDigest("changed")
	if _, err = s.StartOfficialCodeJob(ctx, op, c, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), c, changed); err == nil {
		t.Fatal("concurrent official job accepted")
	}
	r := officialCodeTestReceipt(j)
	bad := r
	bad.Dataset = uuid.NewString()
	if err = s.FinishOfficialCodeJob(ctx, j, &bad, ""); err == nil {
		t.Fatal("wrong dataset receipt accepted")
	}
	bad = r
	bad.PipelineRunID = ""
	if err = s.FinishOfficialCodeJob(ctx, j, &bad, ""); err == nil {
		t.Fatal("missing official run accepted")
	}
	_, err = s.DB.Exec(`CREATE TRIGGER synthetic_official_publish_fault BEFORE UPDATE OF state ON code_worktrees WHEN NEW.state='ready' BEGIN SELECT RAISE(ABORT,'synthetic official publication fault'); END`)
	mustMerge(t, err)
	if err = s.FinishOfficialCodeJob(ctx, j, &r, ""); err == nil {
		t.Fatal("atomic publication fault ignored")
	}
	stored, err := s.OfficialCodeJob(ctx, j.ID)
	mustMerge(t, err)
	if stored.State != "running" || stored.OfficialReceipt != nil {
		t.Fatal("partial commit", stored)
	}
	_, err = s.DB.Exec(`DROP TRIGGER synthetic_official_publish_fault`)
	mustMerge(t, err)
	// The persisted task is authoritative; callers cannot replace its manifest.
	forged := j
	forged.Manifest = &changed
	mustMerge(t, s.FinishOfficialCodeJob(ctx, forged, &r, ""))
	current, err := s.Code(ctx, c.ID)
	mustMerge(t, err)
	if current.State != "ready" || current.Receipt != nil || current.Snapshot != "" || !reflect.DeepEqual(current.OfficialReceipt, &r) || !reflect.DeepEqual(current.Manifest, &m) {
		t.Fatal("official result masquerades as local snapshot", current)
	}
	jobs, err := s.CodeJobs(ctx, c.ID)
	mustMerge(t, err)
	if len(jobs) != 1 || jobs[0].Receipt != nil || !reflect.DeepEqual(jobs[0].OfficialReceipt, &r) {
		t.Fatal(jobs)
	}
	replay, err = s.StartOfficialCodeJob(ctx, op, c, m)
	mustMerge(t, err)
	if replay.State != "completed" || !replay.Replayed {
		t.Fatal("completion replay restarted")
	}
	if err = s.FinishOfficialCodeJob(ctx, j, &r, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate completion accepted", err)
	}
	keep, err := s.CodeRetention(ctx)
	mustMerge(t, err)
	if len(keep[c.Dataset]) != 0 {
		t.Fatal("official receipt sent to worker retention", keep)
	}
}

func TestOfficialCodeJobsRevocationReservesSlotUntilObservedExit(t *testing.T) {
	s, c, m := officialCodeJobFixture(t)
	ctx := context.Background()
	j, err := s.StartOfficialCodeJob(ctx, uuid.NewString(), c, m)
	mustMerge(t, err)
	off, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "disable", ID: c.ID, ExpectedGeneration: c.Generation})
	mustMerge(t, err)
	stored, err := s.OfficialCodeJob(ctx, j.ID)
	mustMerge(t, err)
	if stored.State != "running" || stored.Error != "cancellation_requested" || stored.FinishedMS != 0 {
		t.Fatal("revocation fabricated a stopped process", stored)
	}
	on, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: c.Project, Path: c.Worktree, ExpectedGeneration: off.Generation})
	mustMerge(t, err)
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), on, m); err == nil {
		t.Fatal("overlapped unconfirmed execution")
	}
	r := officialCodeTestReceipt(j)
	mustMerge(t, s.FinishOfficialCodeJob(ctx, j, &r, ""))
	stored, err = s.OfficialCodeJob(ctx, j.ID)
	mustMerge(t, err)
	current, err := s.Code(ctx, c.ID)
	mustMerge(t, err)
	if stored.State != "interrupted" || stored.Error != "authorization_changed" || stored.OfficialReceipt == nil || current.State != "queued" || current.OfficialReceipt != nil {
		t.Fatal("late result published", stored, current)
	}
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), on, m); err != nil {
		t.Fatal("observed exit did not free slot", err)
	}
}

func TestOfficialCodeJobsRestartRequiresReconciliationAndExplicitRefresh(t *testing.T) {
	s, c, m := officialCodeJobFixture(t)
	ctx := context.Background()
	j, err := s.StartOfficialCodeJob(ctx, uuid.NewString(), c, m)
	mustMerge(t, err)
	mustMerge(t, s.InterruptCode(ctx))
	stored, err := s.OfficialCodeJob(ctx, j.ID)
	mustMerge(t, err)
	if stored.State != "running" || stored.Error != "official_execution_unconfirmed" || stored.FinishedMS != 0 {
		t.Fatal("restart inferred backend stop", stored)
	}
	current, err := s.Code(ctx, c.ID)
	mustMerge(t, err)
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), current, m); err == nil {
		t.Fatal("unconfirmed restart accepted")
	}
	mustMerge(t, s.FinishOfficialCodeJob(ctx, j, nil, "interrupted"))
	current, err = s.Code(ctx, c.ID)
	mustMerge(t, err)
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), current, m); err == nil {
		t.Fatal("interrupted source automatically retried")
	}
	current, err = s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "refresh", ID: c.ID, ExpectedGeneration: c.Generation})
	mustMerge(t, err)
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), current, m); err != nil {
		t.Fatal(err)
	}
}

func TestOfficialCodeJobsFailedUpdateKeepsPriorReceipt(t *testing.T) {
	s, c, m := officialCodeJobFixture(t)
	ctx := context.Background()
	j, err := s.StartOfficialCodeJob(ctx, uuid.NewString(), c, m)
	mustMerge(t, err)
	r := officialCodeTestReceipt(j)
	mustMerge(t, s.FinishOfficialCodeJob(ctx, j, &r, ""))
	current, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "refresh", ID: c.ID, ExpectedGeneration: c.Generation})
	mustMerge(t, err)
	j2, err := s.StartOfficialCodeJob(ctx, uuid.NewString(), current, m)
	mustMerge(t, err)
	partial := officialCodeTestReceipt(j2)
	mustMerge(t, s.FinishOfficialCodeJob(ctx, j2, &partial, "source_changed"))
	current, err = s.Code(ctx, c.ID)
	mustMerge(t, err)
	if current.State != "stale" || !reflect.DeepEqual(current.OfficialReceipt, &r) {
		t.Fatal("failed refresh erased prior graph receipt", current)
	}
	if _, err = s.StartOfficialCodeJob(ctx, uuid.NewString(), current, m); err == nil {
		t.Fatal("same failed input retried")
	}
}

func TestOfficialCodeJobsMixedHistoryRetainsLegacyGraph(t *testing.T) {
	s, c, m := officialCodeJobFixture(t)
	ctx := context.Background()
	legacy := codegraph.Receipt{Dataset: c.Dataset, Snapshot: uuid.NewString(), SourceDigest: m.Digest, Extractor: "enola-0.4.30-v351"}
	raw, err := json.Marshal(legacy)
	mustMerge(t, err)
	manifest, err := json.Marshal(m)
	mustMerge(t, err)
	_, err = s.DB.Exec(`INSERT INTO code_jobs(id,worktree_id,generation,state,manifest,receipt,created_ms,finished_ms) VALUES(?,?,1,'completed',?,?,1,2)`, uuid.NewString(), c.ID, string(manifest), string(raw))
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE code_worktrees SET snapshot=? WHERE id=?`, legacy.Snapshot, c.ID)
	mustMerge(t, err)
	j, err := s.StartOfficialCodeJob(ctx, uuid.NewString(), c, m)
	mustMerge(t, err)
	r := officialCodeTestReceipt(j)
	mustMerge(t, s.FinishOfficialCodeJob(ctx, j, &r, ""))
	keep, err := s.CodeRetention(ctx)
	mustMerge(t, err)
	if !reflect.DeepEqual(keep[c.Dataset], []string{legacy.Snapshot}) {
		t.Fatal("old graph retention lost after backend switch", keep)
	}
	jobs, err := s.CodeJobs(ctx, c.ID)
	mustMerge(t, err)
	if len(jobs) != 2 || jobs[0].OfficialReceipt == nil || jobs[1].Official != nil || !reflect.DeepEqual(jobs[1].Receipt, &legacy) {
		t.Fatal("mixed engine history misread", jobs)
	}
}

func TestOfficialCodeJobsWorkspaceLossCannotPublishOrStayIndexing(t *testing.T) {
	s, c, m := officialCodeJobFixture(t)
	ctx := context.Background()
	j, err := s.StartOfficialCodeJob(ctx, uuid.NewString(), c, m)
	mustMerge(t, err)
	mustMerge(t, os.Rename(c.Worktree, c.Worktree+"-moved"))
	r := officialCodeTestReceipt(j)
	mustMerge(t, s.FinishOfficialCodeJob(ctx, j, &r, ""))
	current, err := s.Code(ctx, c.ID)
	mustMerge(t, err)
	if current.State != "interrupted" || current.Error != "authorization_changed" || current.OfficialReceipt != nil {
		t.Fatal("workspace loss published or kept indexing", current)
	}
}

func TestOfficialCodeJobsSchema37UpgradePreservesMetadataAndBackup(t *testing.T) {
	s, c, _ := officialCodeJobFixture(t)
	ctx := context.Background()
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	before, err := s.Code(ctx, c.ID)
	mustMerge(t, err)
	_, err = s.DB.Exec(`PRAGMA user_version=37`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	upgraded, err := Open(path)
	mustMerge(t, err)
	defer upgraded.Close()
	after, err := upgraded.Code(ctx, c.ID)
	mustMerge(t, err)
	if upgraded.Schema != 38 || !reflect.DeepEqual(before, after) {
		t.Fatal("format-reader upgrade changed scope/state", upgraded.Schema, after)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v37-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 {
		t.Fatal("reader-boundary upgrade missing backup", backups)
	}
}
