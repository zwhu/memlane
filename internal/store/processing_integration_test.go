package store

// Validation receipts here are explicit ledger fixtures. These tests prove
// actual SQLite ownership/transactions, never Provider or Agent capability.
import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

var processingTime = time.Unix(1800000000, 0)

func processingProvider(model string) records.ProcessingSpec {
	return records.ProcessingSpec{Mode: "provider", Provider: "explicit-ledger-fixture", Model: model, Protocol: "openai-responses", Endpoint: "https://provider.invalid/v1"}
}

func processingVerifiedFixture(t *testing.T, s *Store, revision, dependency string) records.ProcessingValidation {
	t.Helper()
	v, err := s.BeginProcessingValidation(mergeCtx, uuid.NewString(), revision, dependency, processingTime)
	mustMerge(t, err)
	mustMerge(t, s.FinishProcessingValidation(mergeCtx, v.ID, v.Attempt, "succeeded", records.MergeDigest("ledger-receipt-fixture"), "", processingTime))
	return v
}

func processingFreezeFixture(t *testing.T, s *Store, dependency string) records.ProcessingSnapshot {
	t.Helper()
	tx, err := s.DB.BeginTx(mergeCtx, nil)
	mustMerge(t, err)
	defer tx.Rollback()
	out, err := freezeProcessingTx(mergeCtx, tx, dependency, processingTime)
	mustMerge(t, err)
	mustMerge(t, tx.Commit())
	return out
}

func TestProcessingCandidateSaveAtomicActivationAndFrozenRevision(t *testing.T) {
	s, _ := mergeDB(t)
	first, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("first")}, processingTime)
	mustMerge(t, err)
	dep := records.MergeDigest("first dependencies")
	processingVerifiedFixture(t, s, first.ID, dep)
	activation := records.ProcessingActivation{OperationID: uuid.NewString(), Revision: first.ID}
	oldReceipt, err := s.ActivateProcessing(mergeCtx, activation, dep, processingTime)
	mustMerge(t, err)
	frozen := processingFreezeFixture(t, s, dep)
	second, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: first.ID, Spec: processingProvider("second")}, processingTime.Add(time.Minute))
	mustMerge(t, err)
	_, err = s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "magpie", Model: "fixture-model", Protocol: "openai-chat", Endpoint: "http://127.0.0.1:3425/v1"}}, processingTime)
	mustMerge(t, err)
	current, err := s.ProcessingConfiguration(mergeCtx)
	mustMerge(t, err)
	if current.Current.Revision.ID != first.ID || current.Current.Generation != 1 || !current.Current.Active || current.Saved["provider"].ID != second.ID || len(current.Saved) != 2 {
		t.Fatal("candidate save changed active revision or other mode", current)
	}
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: second.ID, ExpectedGeneration: 1}, dep, processingTime)
	if err == nil {
		t.Fatal("unverified candidate activated")
	}
	processingVerifiedFixture(t, s, second.ID, dep)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: second.ID, ExpectedGeneration: 0}, dep, processingTime)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale activation generation accepted", err)
	}
	newReceipt, err := s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: second.ID, ExpectedGeneration: 1}, dep, processingTime)
	mustMerge(t, err)
	if newReceipt.Generation != 2 || frozen.Revision.ID != first.ID || frozen.Revision.Spec.Model != "first" || processingFreezeFixture(t, s, dep).Revision.ID != second.ID {
		t.Fatal("running snapshot or next task selection changed incorrectly")
	}
	replayed, err := s.ActivateProcessing(mergeCtx, activation, dep, processingTime)
	mustMerge(t, err)
	current, err = s.ProcessingConfiguration(mergeCtx)
	mustMerge(t, err)
	if replayed != oldReceipt || current.Current.Revision.ID != second.ID {
		t.Fatal("replayed historical receipt reactivated old configuration")
	}
	if _, err = s.DB.Exec(`UPDATE processing_revisions SET spec='{}' WHERE id=?`, first.ID); err == nil {
		t.Fatal("immutable revision updated")
	}
}

func TestProcessingValidationCancelLateResultAndConnectionGeneration(t *testing.T) {
	s, _ := mergeDB(t)
	scopes, err := s.Scopes(mergeCtx)
	mustMerge(t, err)
	c, err := s.ConfigureConnection(mergeCtx, records.ConnectionDraft{Agent: "codex", Name: "ledger-only connection", AllowedScopes: []string{scopes[0].ID}, AutoProject: true})
	mustMerge(t, err)
	spec := records.ProcessingSpec{Mode: "agent", Agent: "codex", Connection: c.ID, ConnectionGeneration: c.Generation, Model: "fixture-model"}
	revision, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: spec}, processingTime)
	mustMerge(t, err)
	dep := records.MergeDigest("connection dependencies")
	v, err := s.BeginProcessingValidation(mergeCtx, uuid.NewString(), revision.ID, dep, processingTime)
	mustMerge(t, err)
	mustMerge(t, s.RequestProcessingValidationCancel(mergeCtx, v.ID))
	if err = s.FinishProcessingValidation(mergeCtx, v.ID, v.Attempt, "succeeded", records.MergeDigest("late success fixture"), "", processingTime); !errors.Is(err, ErrConflict) {
		t.Fatal("late success won over cancel", err)
	}
	mustMerge(t, s.FinishProcessingValidation(mergeCtx, v.ID, v.Attempt, "cancelled", "", "cancelled", processingTime))
	newValidation := processingVerifiedFixture(t, s, revision.ID, dep)
	if newValidation.Attempt != 2 {
		t.Fatal("retry did not create an independent attempt")
	}
	if err = s.FinishProcessingValidation(mergeCtx, v.ID, v.Attempt, "succeeded", records.MergeDigest("old attempt fixture"), "", processingTime); !errors.Is(err, ErrConflict) {
		t.Fatal("old attempt replaced retry", err)
	}
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}, dep, processingTime)
	mustMerge(t, err)
	mustMerge(t, s.Disconnect(mergeCtx, c.ID))
	tx, err := s.DB.BeginTx(mergeCtx, nil)
	mustMerge(t, err)
	_, err = freezeProcessingTx(mergeCtx, tx, dep, processingTime)
	tx.Rollback()
	if err == nil {
		t.Fatal("disconnected generation still executable")
	}
	current, err := s.ProcessingConfiguration(mergeCtx)
	mustMerge(t, err)
	if current.Current.Revision.ID != revision.ID {
		t.Fatal("invalid connection silently switched configuration")
	}
}

func TestProcessingSchema16WALMigrationPreservesEveryOldTableAndStopIntent(t *testing.T) {
	s, plan, now := scheduleFixture(t)
	plan.Enabled = true
	_, err := s.PutSchedule(mergeCtx, plan, now)
	mustMerge(t, err)
	stoppedPlan := plan
	stoppedPlan.ID, stoppedPlan.OperationID = uuid.NewString(), uuid.NewString()
	stoppedPlan.Enabled = false
	_, err = s.PutSchedule(mergeCtx, stoppedPlan, now)
	mustMerge(t, err)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	scopes, err := s.Scopes(mergeCtx)
	mustMerge(t, err)
	id := uuid.NewString()
	_, err = s.Apply(mergeCtx, Mutation{OperationID: uuid.NewString(), Kind: "save", Memory: id, Dataset: scopes[0].ID, Text: "schema16 original"})
	mustMerge(t, err)
	_, err = s.Apply(mergeCtx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: id, Dataset: scopes[0].ID, ExpectedRevision: 1, Text: "schema16 current user version"})
	mustMerge(t, err)
	// Deliberately remove only the unpopulated v17 ledger additions. This
	// reconstructs the actual schema16, instead of relabeling a newer schema.
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts; DROP TABLE processing_suppliers; DROP TABLE processing_validation_diagnostics; DROP TABLE index_rebuild_controls; DROP TABLE index_attempts; DROP TABLE agent_connection_batch_head; DROP TABLE agent_connection_batches; DROP TABLE organization_start_intents; DROP TABLE organization_previews; DROP TABLE organization_preview_head; DROP TABLE experience_previews; DROP TABLE experience_preview_head; DROP TABLE project_task_histories; DROP TABLE project_index_recoveries; DROP TABLE project_task_items; DROP TABLE project_task_sources; DROP TABLE project_task_runs; DROP TABLE project_tasks; DROP TABLE project_bindings; DROP TABLE processing_operations; DROP TABLE processing_validations; DROP TABLE processing_saved; DROP TABLE processing_current; DROP TABLE processing_revisions; PRAGMA user_version=16; PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, err)
	s.Schema = 16
	before := restoreTables(t, s.DB)
	stop := []byte(`{"stopped":true,"epoch":8}`)
	stopPath := filepath.Join(filepath.Dir(path), "service-state.json")
	mustMerge(t, os.WriteFile(stopPath, stop, 0600))
	reader, err := sql.Open("sqlite", path)
	mustMerge(t, err)
	hold, err := reader.Begin()
	mustMerge(t, err)
	var count int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM versions`).Scan(&count))
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v16-*.sqlite"))
	mustMerge(t, err)
	if current.Schema != SchemaVersion || len(backups) != 1 {
		t.Fatal("schema16 WAL consistency backup missing", backups)
	}
	backup, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer backup.Close()
	for name, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, current.DB, name)) || !reflect.DeepEqual(rows, sqlRows(t, backup, name)) {
			t.Fatal("old table or WAL backup changed", name)
		}
	}
	saved, err := current.ProcessingConfiguration(mergeCtx)
	mustMerge(t, err)
	retained, err := os.ReadFile(stopPath)
	mustMerge(t, err)
	if len(saved.Saved) != 0 || saved.Current.Active || saved.Current.Revision != nil || string(retained) != string(stop) {
		t.Fatal("migration enabled a model or changed stop intent")
	}
}

func TestProcessingRestorePreservesSelectionButRequiresExplicitReactivation(t *testing.T) {
	s, _ := mergeDB(t)
	v, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("restore")}, processingTime)
	mustMerge(t, err)
	dep := records.MergeDigest("restore dependencies")
	processingVerifiedFixture(t, s, v.ID, dep)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: v.ID}, dep, processingTime)
	mustMerge(t, err)
	_, snapshot, err := s.snapshot(mergeCtx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(mergeCtx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	current, err := restored.ProcessingConfiguration(mergeCtx)
	mustMerge(t, err)
	if current.Current.Active || current.Current.Revision.ID != v.ID || current.Saved["provider"].ID != v.ID || current.Current.Revision.Validation.State != "superseded" {
		t.Fatal("restore lost configuration or resumed old paid authority", current)
	}
	processingVerifiedFixture(t, restored, v.ID, dep)
	tx, err := restored.DB.BeginTx(mergeCtx, nil)
	mustMerge(t, err)
	_, err = freezeProcessingTx(mergeCtx, tx, dep, processingTime)
	tx.Rollback()
	if err == nil {
		t.Fatal("verification reactivated restored selection without explicit action")
	}
	_, err = restored.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: v.ID, ExpectedGeneration: current.Current.Generation}, dep, processingTime)
	mustMerge(t, err)
	processingFreezeFixture(t, restored, dep)
}
