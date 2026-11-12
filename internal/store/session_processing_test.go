package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func sessionProcessingFixture(t *testing.T) (*Store, records.CogneeBinding, records.SessionProcessingRequest, string) {
	t.Helper()
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	revision, err := s.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("session-fixture")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("session-processing-fixture")
	processingVerifiedFixture(t, s, revision.ID, dependency)
	active, err := s.ActivateProcessing(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}, dependency, processingTime)
	mustMerge(t, err)
	q := records.SessionProcessingRequest{Operation: uuid.NewString(), Instance: b.Instance, Dataset: b.Dataset, Project: b.Project, User: uuid.NewString(), Session: "official-synthetic-session", ObservedDigest: records.MergeDigest("observed read snapshot"), Processing: records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: active.Generation}}
	return s, b, q, dependency
}

func sessionProcessingTestReceipt(f records.SessionProcessingFrozen) *records.SessionProcessingReceipt {
	q := f.Task.Request
	return &records.SessionProcessingReceipt{Library: f.Task.Library, Operation: q.Operation, Instance: q.Instance, Project: q.Project, Dataset: q.Dataset, Session: q.Session, User: q.User, CogneeVersion: "1.6.7", RevisionDigest: f.Processing.Revision.Digest, DependencyDigest: f.Processing.DependencyDigest, Status: "completed", Stages: []records.SessionProcessingStage{{Stage: "distill_sessions", Status: "completed", Counts: map[string]int{"sessions": 1}}}}
}

func TestSessionProcessingFreezeReplayAndNoMirror(t *testing.T) {
	s, b, q, dependency := sessionProcessingFixture(t)
	ctx := context.Background()
	before := map[string]int{}
	for _, table := range []string{"memories", "versions", "operations", "jobs"} {
		var n int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		before[table] = n
	}
	task, err := s.PrepareSessionProcessing(ctx, b, q, dependency)
	mustMerge(t, err)
	f, err := s.LoadSessionProcessing(ctx, q.Operation)
	mustMerge(t, err)
	if task.State != "prepared" || f.Processing.Revision.ID != q.Processing.Revision || f.Processing.DependencyDigest != dependency {
		t.Fatal("task did not freeze the selected model", f)
	}
	mustMerge(t, s.ClaimSessionProcessing(ctx, q.Operation, task.Library))
	if s.ClaimSessionProcessing(ctx, q.Operation, task.Library) == nil {
		t.Fatal("same operation acquired a second execution")
	}
	// Later saves do not replace a running task's selection or its replay.
	_, err = s.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: q.Processing.Revision, Spec: processingProvider("later-candidate")}, processingTime)
	mustMerge(t, err)
	mustMerge(t, s.FinishSessionProcessing(ctx, q.Operation, task.Library, "completed", "", sessionProcessingTestReceipt(f), json.RawMessage(`[{"success":true}]`)))
	replayed, exists, err := s.ReplaySessionProcessing(ctx, q)
	mustMerge(t, err)
	if !exists || replayed.State != "completed" || replayed.Receipt == nil {
		t.Fatal("lost response could not replay the original terminal result")
	}
	latest, err := s.LatestSessionProcessing(ctx, q)
	mustMerge(t, err)
	if latest.Request.Operation != q.Operation || latest.State != "completed" {
		t.Fatal("reopen lookup lost the owned task")
	}
	changed := q
	changed.ObservedDigest = records.MergeDigest("new snapshot")
	if _, _, err = s.ReplaySessionProcessing(ctx, changed); err == nil {
		t.Fatal("same operation changed its selected source")
	}
	for table, old := range before {
		var n int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		if n != old {
			t.Fatalf("processing task created mirrored %s", table)
		}
	}
}

func TestSessionProcessingAutomaticGrantRejectsLateSuccessAtomically(t *testing.T) {
	s, b, q, dependency := sessionProcessingFixture(t)
	ctx := context.Background()
	change := records.CodingMemoryGrantChange{Operation: uuid.NewString(), Binding: b, User: q.User, Enabled: true}
	grant, err := s.ChangeCodingMemoryGrant(ctx, change)
	mustMerge(t, err)
	q.AutomaticGrant = &grant
	task, err := s.PrepareSessionProcessing(ctx, b, q, dependency)
	mustMerge(t, err)
	mustMerge(t, s.ClaimSessionProcessing(ctx, q.Operation, task.Library))
	frozen, err := s.LoadSessionProcessing(ctx, q.Operation)
	mustMerge(t, err)
	change.Operation, change.Enabled, change.ExpectedGeneration = uuid.NewString(), false, grant.Generation
	_, err = s.ChangeCodingMemoryGrant(ctx, change)
	mustMerge(t, err)
	if s.FinishSessionProcessing(ctx, q.Operation, task.Library, "completed", "", sessionProcessingTestReceipt(frozen), json.RawMessage(`[]`)) == nil {
		t.Fatal("revoked automatic grant published a late successful result")
	}
	// Retain partial official facts while sealing failure; no rollback claim.
	mustMerge(t, s.FinishSessionProcessing(ctx, q.Operation, task.Library, "failed", "scope_denied", sessionProcessingTestReceipt(frozen), json.RawMessage(`[]`)))
	current, err := s.LoadSessionProcessing(ctx, q.Operation)
	mustMerge(t, err)
	if current.Task.State != "failed" || current.Task.Receipt == nil {
		t.Fatal("revocation discarded actual partial official receipt")
	}
}

func TestSessionProcessingCancelRestartAndInvalidReceipt(t *testing.T) {
	s, b, q, dependency := sessionProcessingFixture(t)
	ctx := context.Background()
	task, err := s.PrepareSessionProcessing(ctx, b, q, dependency)
	mustMerge(t, err)
	mustMerge(t, s.ClaimSessionProcessing(ctx, q.Operation, task.Library))
	f, err := s.LoadSessionProcessing(ctx, q.Operation)
	mustMerge(t, err)
	wrong := sessionProcessingTestReceipt(f)
	wrong.User = uuid.NewString()
	if s.FinishSessionProcessing(ctx, q.Operation, task.Library, "completed", "", wrong, json.RawMessage(`[]`)) == nil {
		t.Fatal("foreign source owner receipt accepted")
	}
	mustMerge(t, s.CancelSessionProcessing(ctx, q.Operation))
	if s.FinishSessionProcessing(ctx, q.Operation, task.Library, "completed", "", sessionProcessingTestReceipt(f), json.RawMessage(`[]`)) == nil {
		t.Fatal("cancelled task adopted a late successful receipt")
	}
	mustMerge(t, s.InterruptSessionProcessing(ctx))
	current, exists, err := s.ReplaySessionProcessing(ctx, q)
	mustMerge(t, err)
	if !exists || current.State != "interrupted" || !current.CancelRequested || current.ErrorCode != "service_restarted" {
		t.Fatal("restart revived a paid operation", current)
	}
	if s.ClaimSessionProcessing(ctx, q.Operation, task.Library) == nil {
		t.Fatal("interrupted task was silently re-executed")
	}
	mustMerge(t, s.Disconnect(ctx, b.ConnectionID))
	if _, _, err = s.ReplaySessionProcessing(ctx, q); err == nil {
		t.Fatal("revoked grant exposed an old task result")
	}
}

func TestSessionProcessingSchema31MigrationPreservesOldContent(t *testing.T) {
	s, _, _, _ := sessionProcessingFixture(t)
	ctx := context.Background()
	var path string
	rows, err := s.DB.Query(`PRAGMA database_list`)
	mustMerge(t, err)
	for rows.Next() {
		var seq int
		var name, file string
		mustMerge(t, rows.Scan(&seq, &name, &file))
		if name == "main" {
			path = file
		}
	}
	mustMerge(t, rows.Close())
	var old int
	mustMerge(t, s.DB.QueryRowContext(ctx, `SELECT count(*) FROM versions`).Scan(&old))
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; PRAGMA user_version=31`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	upgraded, err := Open(path)
	mustMerge(t, err)
	defer upgraded.Close()
	var n, tasks int
	mustMerge(t, upgraded.DB.QueryRow(`SELECT count(*) FROM versions`).Scan(&n))
	mustMerge(t, upgraded.DB.QueryRow(`SELECT count(*) FROM session_processing_tasks`).Scan(&tasks))
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v31-*.sqlite"))
	mustMerge(t, err)
	if upgraded.Schema != SchemaVersion || old != n || tasks != 0 || len(backups) != 1 {
		t.Fatal("session migration changed old content or missed backup", old, n, tasks, backups)
	}
}

func TestSessionProcessingRestoreRetainsIntentWithoutResuming(t *testing.T) {
	s, b, q, dependency := sessionProcessingFixture(t)
	ctx := context.Background()
	task, err := s.PrepareSessionProcessing(ctx, b, q, dependency)
	mustMerge(t, err)
	mustMerge(t, s.ClaimSessionProcessing(ctx, q.Operation, task.Library))
	_, snapshot, err := s.snapshot(ctx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	if len(prepared) == 0 {
		t.Fatal("restore produced no snapshot")
	}
	// Inspect the actual prepared backup without opening any production root.
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	f, err := restored.LoadSessionProcessing(ctx, q.Operation)
	mustMerge(t, err)
	if f.Task.State != "interrupted" || !f.Task.CancelRequested || f.Task.ErrorCode != "backup_restored" || f.Task.Request.ObservedDigest != q.ObservedDigest {
		t.Fatal("backup restore lost intent or revived paid processing", f.Task)
	}
	if restored.ClaimSessionProcessing(ctx, q.Operation, task.Library) == nil {
		t.Fatal("restored operation could execute again")
	}
}
