package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestProcessingDiagnosticsAtomicBoundedNamesAndMigration25(t *testing.T) {
	s, path := mergeDB(t)
	revision, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("diagnostic-ledger-fixture")}, processingTime)
	mustMerge(t, err)
	validation, err := s.BeginProcessingValidation(mergeCtx, uuid.NewString(), revision.ID, records.MergeDigest("fixture dependencies"), processingTime)
	mustMerge(t, err)
	invalid := records.ProcessingValidationDiagnostics{Stage: "postflight", ChangedNames: []string{"/private/credential-value"}}
	if err = s.FinishProcessingValidation(mergeCtx, validation.ID, 1, "failed", "", "dependency_changed", time.Now(), invalid); err == nil {
		t.Fatal("unbounded dependency label persisted")
	}
	stillRunning, err := s.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if stillRunning.State != "validating" || stillRunning.Diagnostics != nil {
		t.Fatal("invalid diagnostic partially changed result", stillRunning)
	}
	diagnostic := records.ProcessingValidationDiagnostics{Stage: "postflight", ChangedNames: []string{"auth.json"}}
	mustMerge(t, s.FinishProcessingValidation(mergeCtx, validation.ID, 1, "failed", "", "dependency_changed", time.Now(), diagnostic))
	terminal, err := s.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if terminal.Diagnostics == nil || !reflect.DeepEqual(*terminal.Diagnostics, diagnostic) {
		t.Fatal(terminal)
	}
	var stored string
	mustMerge(t, s.DB.QueryRow(`SELECT changed_names FROM processing_validation_diagnostics WHERE validation=?`, validation.ID).Scan(&stored))
	if stored != `["auth.json"]` {
		t.Fatal("diagnostic stored value rather than names", stored)
	}
	// Recreate the prior shipped schema with real old validation results. The
	// migration preserves those results and cannot invent a new model receipt.
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts; DROP TABLE processing_suppliers; DROP TABLE processing_validation_diagnostics; PRAGMA user_version=25`)
	mustMerge(t, err)
	prior := sqlRows(t, s.DB, "processing_validations")
	mustMerge(t, s.Close())
	migrated, err := Open(path)
	mustMerge(t, err)
	defer migrated.Close()
	if migrated.Schema != SchemaVersion || !reflect.DeepEqual(prior, sqlRows(t, migrated.DB, "processing_validations")) {
		t.Fatal("migration changed validation facts")
	}
	current, err := migrated.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if current.State != "failed" || current.Diagnostics != nil {
		t.Fatal("migration invented old diagnostics", current)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v25-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 {
		t.Fatal("migration backup missing", backups)
	}
}
