package store

// Storage-only validation receipts are explicit ledger fixtures. Live HTTP
// transport/owner/strict proposal evidence lives in processing and daemon tests.
import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestHTTPProcessingSchema26MigrationPreservesLedgerAndBackup(t *testing.T) {
	s, path := mergeDB(t)
	revision, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("old26-fixture")}, processingTime)
	mustMerge(t, err)
	validation, err := s.BeginProcessingValidation(mergeCtx, uuid.NewString(), revision.ID, records.MergeDigest("old26-dependency"), processingTime)
	mustMerge(t, err)
	mustMerge(t, s.FinishProcessingValidation(mergeCtx, validation.ID, 1, "failed", "", "provider_unavailable", processingTime))
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts;DROP TABLE processing_suppliers;PRAGMA user_version=26`)
	mustMerge(t, err)
	before := restoreTables(t, s.DB)
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	if current.Schema != SchemaVersion {
		t.Fatal(current.Schema)
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) {
			t.Fatal("schema27 changed existing ledger", table)
		}
	}
	restored, err := current.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if restored.State != "failed" || restored.HTTP != nil {
		t.Fatal("migration invented HTTP proof", restored)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v26-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 {
		t.Fatal("missing original schema26 backup", backups)
	}
}

func TestHTTPProcessingReceiptAtomicityAndRestoreRetiresSuccessKeepsMetadata(t *testing.T) {
	s, _ := mergeDB(t)
	secret := []byte("never-persist-owned-secret")
	defer clear(secret)
	write := records.ProcessingCredentialWrite{Credential: uuid.NewString(), Revision: 1, Provider: "ledger-only-supplier", Endpoint: "https://fixture.invalid/v1", Protocol: "openai-chat", Secret: secret}
	supplier, err := s.SaveProcessingSupplier(context.Background(), write)
	mustMerge(t, err)
	spec := records.ProcessingSpec{Mode: "provider", Provider: supplier.Provider, Endpoint: supplier.Endpoint, Protocol: supplier.Protocol, Credential: supplier.Credential, CredentialRevision: supplier.CredentialRevision, Model: "requested-fixture"}
	revision, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: spec}, processingTime)
	mustMerge(t, err)
	validation, err := s.BeginProcessingValidation(mergeCtx, uuid.NewString(), revision.ID, records.MergeDigest("explicit-ledger-fixture"), processingTime)
	mustMerge(t, err)
	receipt := records.ProcessingHTTPReceipt{Protocol: spec.Protocol, RequestedModel: spec.Model, ReportedModel: "reported-fixture", NativeSchemaRequested: true, RequestSent: true, ResponseComplete: true, SchemaValid: true, Status: 200, StartedMS: 1, FinishedMS: 2}
	bad := receipt
	bad.SchemaValid = false
	if err = s.FinishHTTPProcessingValidation(mergeCtx, validation.ID, 1, "succeeded", records.MergeDigest(bad), "", processingTime, records.ProcessingValidationDiagnostics{Stage: "postflight"}, &bad); err == nil {
		t.Fatal("incomplete HTTP capability accepted")
	}
	before, err := s.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if before.State != "validating" || before.HTTP != nil {
		t.Fatal("invalid proof partially published", before)
	}
	mustMerge(t, s.FinishHTTPProcessingValidation(mergeCtx, validation.ID, 1, "succeeded", records.MergeDigest(receipt), "", time.Now(), records.ProcessingValidationDiagnostics{Stage: "postflight"}, &receipt))
	current, err := s.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if current.HTTP == nil || !reflect.DeepEqual(*current.HTTP, receipt) {
		t.Fatal("durable HTTP receipt lost", current)
	}
	_, snapshot, err := s.snapshot(mergeCtx)
	mustMerge(t, err)
	if bytes.Contains(snapshot, secret) {
		t.Fatal("supplier key entered SQLite snapshot")
	}
	prepared, _, err := PrepareRestoredSnapshot(mergeCtx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	historical, err := restored.ProcessingValidation(mergeCtx, validation.ID)
	mustMerge(t, err)
	if historical.State != "superseded" || historical.HTTP == nil || !reflect.DeepEqual(*historical.HTTP, receipt) {
		t.Fatal("restore revived success or destroyed historical proof", historical)
	}
	suppliers, err := restored.ProcessingSuppliers(mergeCtx)
	mustMerge(t, err)
	if len(suppliers) != 1 || suppliers[0] != supplier {
		t.Fatal("restore lost supplier references", suppliers)
	}
	config, err := restored.ProcessingConfiguration(mergeCtx)
	mustMerge(t, err)
	if config.Current.Active {
		t.Fatal("restore enabled HTTP inference")
	}
}
