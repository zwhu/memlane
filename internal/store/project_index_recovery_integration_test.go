package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func TestProjectIndexFailureDoesNotReplayDuringOriginalIndexing(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	ctx, task := projectRunFixture(t, s, p, c)
	source := projectSourceFixture("README.md", "本项目使用 pnpm。")
	r, err := s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, projectProposalFixture(task, source), []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	v, err := s.Current(ctx, r.Added[0].Memory)
	mustMerge(t, err)
	mustMerge(t, s.IndexFailed(ctx, v.Record, errors.New("explicit publication fault")))
	rows, err := s.IndexReplayCandidates(ctx)
	mustMerge(t, err)
	got := map[string]bool{}
	for rows.Next() {
		var id string
		mustMerge(t, rows.Scan(&id))
		got[id] = true
	}
	mustMerge(t, rows.Err())
	rows.Close()
	if got[r.Added[0].Memory] || !got[r.Sources[0].Memory] {
		t.Fatal("original failure retried or pending source suppressed", got)
	}
}

// SQLite and fault fixtures only; no Agent inference is claimed by these tests.
func failedProjectIndexFixture(t *testing.T) (*Store, context.Context, records.ProjectTask, records.ProjectTaskReceipt) {
	t.Helper()
	s, p, c := projectTaskFixture(t)
	ctx, task := projectRunFixture(t, s, p, c)
	source := projectSourceFixture("README.md", "本项目使用 pnpm。")
	r, err := s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, projectProposalFixture(task, source), []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE jobs SET state='failed',error='explicit-index-fault' WHERE memory IN (?,?)`, r.Added[0].Memory, r.Sources[0].Memory)
	mustMerge(t, err)
	done, err := s.FinishProjectIndexes(ctx, task.ID, task.Attempt)
	mustMerge(t, err)
	if !done {
		t.Fatal("failure not sealed")
	}
	task, err = s.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	return s, ctx, task, r
}

func TestProjectIndexRecoveryExactVersionsReplayAndNoModelMutation(t *testing.T) {
	s, ctx, task, receipt := failedProjectIndexFixture(t)
	p := task.Snapshot.Project
	other, err := s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: p, Memory: uuid.NewString(), Text: "unrelated failed knowledge"})
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE jobs SET state='failed' WHERE memory=?`, other.Memory)
	mustMerge(t, err)
	beforeVersions := sqlRows(t, s.DB, "versions")
	beforeRuns := sqlRows(t, s.DB, "project_task_runs")
	beforeTasks := sqlRows(t, s.DB, "project_tasks")
	rows, err := s.IndexReplayCandidates(ctx)
	mustMerge(t, err)
	for rows.Next() {
		var id string
		mustMerge(t, rows.Scan(&id))
		if id == receipt.Added[0].Memory || id == receipt.Sources[0].Memory {
			t.Fatal("closed C failure replayed without intent")
		}
	}
	mustMerge(t, rows.Err())
	rows.Close()
	// Recovery belongs to local stored content, not to a still-connected Agent.
	mustMerge(t, s.Disconnect(ctx, task.Snapshot.Executor.ConnectionID))
	d := records.IndexRebuildStart{OperationID: uuid.NewString(), ProjectTask: task.ID, Attempt: task.Attempt}
	recovery, err := s.StartIndexRebuild(ctx, d)
	mustMerge(t, err)
	if recovery.State != "running" || len(recovery.Items) != 2 || recovery.Scope != p || recovery.ProjectTask != task.ID {
		t.Fatal(recovery)
	}
	rows, err = s.IndexReplayCandidates(ctx)
	mustMerge(t, err)
	queued := map[string]bool{}
	for rows.Next() {
		var id string
		mustMerge(t, rows.Scan(&id))
		queued[id] = true
	}
	mustMerge(t, rows.Err())
	rows.Close()
	if !queued[receipt.Added[0].Memory] || !queued[receipt.Sources[0].Memory] {
		t.Fatal("explicit recovery did not enqueue saved versions")
	}
	replay, err := s.StartIndexRebuild(ctx, d)
	mustMerge(t, err)
	if !reflect.DeepEqual(recovery, replay) {
		t.Fatal("replay changed frozen versions")
	}
	changed := d
	changed.Attempt++
	if _, err = s.StartIndexRebuild(ctx, changed); err == nil {
		t.Fatal("wrong attempt accepted")
	}
	if _, err = s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: d.OperationID, Scope: p}); err == nil {
		t.Fatal("project replay accepted as scope rebuild")
	}
	if _, err = s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), ProjectTask: task.ID, Attempt: task.Attempt}); err == nil {
		t.Fatal("second recovery accepted while running")
	}
	for _, saved := range append(receipt.Added, receipt.Sources...) {
		v, e := s.Current(ctx, saved.Memory)
		mustMerge(t, e)
		mustMerge(t, s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "real-store-fixture", ChunkIDs: []string{uuid.NewString()}}))
	}
	after, err := s.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if after.State != "failed" || after.IndexRecovery.State != "completed" || after.Receipt.IndexFailed != 0 || after.Receipt.SourceIndexFailed != 0 || !reflect.DeepEqual(task.Execution, after.Execution) {
		t.Fatal(after)
	}
	if !reflect.DeepEqual(beforeVersions, sqlRows(t, s.DB, "versions")) || !reflect.DeepEqual(beforeRuns, sqlRows(t, s.DB, "project_task_runs")) || !reflect.DeepEqual(beforeTasks, sqlRows(t, s.DB, "project_tasks")) {
		t.Fatal("recovery rewrote model or knowledge history")
	}
	var job string
	mustMerge(t, s.DB.QueryRow(`SELECT state FROM jobs WHERE memory=?`, other.Memory).Scan(&job))
	if job != "failed" {
		t.Fatal("unrelated job reset")
	}
}

func TestProjectIndexRecoveryExpiredVersionAndAtomicFailure(t *testing.T) {
	for _, mode := range []string{"edited", "sql-failure", "missing-job", "source-success"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, task, r := failedProjectIndexFixture(t)
			switch mode {
			case "edited":
				_, err := s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: r.Added[0].Memory, Dataset: task.Snapshot.Project, ExpectedRevision: 1, Text: "new user version"})
				mustMerge(t, err)
			case "sql-failure":
				_, err := s.DB.Exec(`CREATE TRIGGER recovery_fault BEFORE INSERT ON index_rebuild_items BEGIN SELECT RAISE(ABORT,'explicit recovery fault'); END`)
				mustMerge(t, err)
			case "missing-job":
				_, err := s.DB.Exec(`DELETE FROM jobs WHERE memory=?`, r.Sources[0].Memory)
				mustMerge(t, err)
			case "source-success":
				v, err := s.Current(ctx, r.Sources[0].Memory)
				mustMerge(t, err)
				mustMerge(t, s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "keep-source-generation", ChunkIDs: []string{uuid.NewString()}}))
			}
			beforeJobs := sqlRows(t, s.DB, "jobs")
			d := records.IndexRebuildStart{OperationID: uuid.NewString(), ProjectTask: task.ID, Attempt: task.Attempt}
			got, err := s.StartIndexRebuild(ctx, d)
			if mode == "sql-failure" || mode == "missing-job" {
				if err == nil {
					t.Fatal("fault accepted")
				}
				if !reflect.DeepEqual(beforeJobs, sqlRows(t, s.DB, "jobs")) || len(sqlRows(t, s.DB, "index_rebuilds")) != 0 {
					t.Fatal("partial recovery left jobs or task")
				}
				return
			}
			mustMerge(t, err)
			for _, item := range got.Items {
				if mode == "edited" && item.Memory == r.Added[0].Memory && (item.Revision != 1 || item.State != "expired") {
					t.Fatal("new version substituted", item)
				}
				if mode == "source-success" && item.Memory == r.Sources[0].Memory && item.State != "completed" {
					t.Fatal("successful source reset", item)
				}
			}
			var generation string
			mustMerge(t, s.DB.QueryRow(`SELECT generation FROM memories WHERE id=?`, r.Sources[0].Memory).Scan(&generation))
			if mode == "source-success" && generation != "keep-source-generation" {
				t.Fatal("source generation reset")
			}
		})
	}
}

func TestProjectIndexRecoveryMenuReopenRestoreAndRetry(t *testing.T) {
	s, ctx, task, _ := failedProjectIndexFixture(t)
	q := records.MergeListQuery{Project: task.Snapshot.Project, ActiveOnly: true, IncludeIndexRecovery: true}
	page, err := s.ListProjectTasks(ctx, q)
	mustMerge(t, err)
	if len(page.Items) != 1 || page.Items[0].ID != task.ID {
		t.Fatal("failed saved task unavailable after reopen", page)
	}
	d := records.IndexRebuildStart{OperationID: uuid.NewString(), ProjectTask: task.ID, Attempt: task.Attempt}
	_, err = s.StartIndexRebuild(ctx, d)
	mustMerge(t, err)
	// A further real indexing failure ends recovery; a new explicit intent may retry.
	_, err = s.DB.Exec(`UPDATE jobs SET state='failed'; UPDATE index_rebuild_items SET state='failed' WHERE task=?`, d.OperationID)
	mustMerge(t, err)
	d.OperationID = uuid.NewString()
	_, err = s.StartIndexRebuild(ctx, d)
	mustMerge(t, err)
	_, snapshot, err := s.snapshot(ctx)
	mustMerge(t, err)
	restored, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, restored, 0600))
	copy, err := Open(path)
	mustMerge(t, err)
	defer copy.Close()
	got, err := copy.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if got.IndexRecovery.State != "expired" {
		t.Fatal("restore revived recovery", got.IndexRecovery)
	}
	if _, err = copy.StartIndexRebuild(ctx, d); err == nil {
		t.Fatal("restored recovery replay accepted")
	}
}
