package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func batchDraft(s *Store) records.AgentConnectionBatchStart {
	identity, _ := s.AgentConnectionBatchIdentity(context.Background())
	return records.AgentConnectionBatchStart{Identity: identity, OperationID: uuid.NewString(), Targets: []records.AgentConnectionTarget{{Agent: "pi", Path: "/synthetic/pi", Directory: "/synthetic/pi-config"}, {Agent: "codex", Path: "/synthetic/codex", Directory: "/synthetic/codex-config"}}}
}

func TestAgentBatchRestoreRetiresRequestsAndKeepsOriginalSnapshot(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := batchDraft(s)
	b, _, err := s.CreateAgentConnectionBatch(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := s.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prepared, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	if err = os.WriteFile(path, prepared, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	identity, err := restored.AgentConnectionBatchIdentity(ctx)
	if err != nil || identity == d.Identity {
		t.Fatal("restore kept old request authority", identity, err)
	}
	retired, err := restored.AgentConnectionBatch(ctx, b.ID)
	if err != nil || retired.State != "retired" {
		t.Fatal("restored batch still actionable", retired, err)
	}
	if _, _, err = restored.CreateAgentConnectionBatch(ctx, d); err == nil {
		t.Fatal("old unknown start replayed after restore")
	}
	if _, err = restored.SaveAgentConnectionBatch(ctx, b); err == nil {
		t.Fatal("old worker result applied after restore")
	}
	original, err := s.AgentConnectionBatch(ctx, b.ID)
	if err != nil || original.State != b.State || original.Revision != b.Revision {
		t.Fatal("restore preparation changed original batch", err)
	}
	if oldIdentity, _ := s.AgentConnectionBatchIdentity(ctx); oldIdentity != d.Identity {
		t.Fatal("restore changed live owner identity")
	}
}

func TestAgentBatchReplayCASAndInterruptedRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	d := batchDraft(s)
	b, created, err := s.CreateAgentConnectionBatch(ctx, d)
	if err != nil || !created || len(b.Items) != 2 {
		t.Fatal(b, created, err)
	}
	replayed, created, err := s.CreateAgentConnectionBatch(ctx, d)
	if err != nil || created || records.MergeDigest(b) != records.MergeDigest(replayed) {
		t.Fatal("replayed batch changed identities", replayed, err)
	}
	d.Targets[0].Directory = "/changed"
	if _, _, err = s.CreateAgentConnectionBatch(ctx, d); err == nil {
		t.Fatal("same operation changed frozen targets")
	}
	if _, _, err = s.CreateAgentConnectionBatch(ctx, batchDraft(s)); err == nil {
		t.Fatal("overlapping active batch accepted")
	}
	stale := b
	b.State = "partial_failed"
	b.Items[0].State = "connected"
	b.Items[1].State = "failed"
	b, err = s.SaveAgentConnectionBatch(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveAgentConnectionBatch(ctx, stale); err == nil {
		t.Fatal("stale outcome replaced newer results")
	}
	next, _, err := s.CreateAgentConnectionBatch(ctx, batchDraft(s))
	if err != nil {
		t.Fatal(err)
	}
	next.Items[0].State = "verifying"
	next.Items[0].Phase = "verify"
	if _, err = s.SaveAgentConnectionBatch(ctx, next); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InterruptAgentConnectionBatches(ctx); err != nil {
		t.Fatal(err)
	}
	next, err = s.AgentConnectionBatch(ctx, next.ID)
	if err != nil || next.State != "interrupted" || next.Items[0].State != "interrupted" || next.Items[0].Phase != "verify" {
		t.Fatal("restart re-ran or lost the active phase", next, err)
	}
	old, err := s.AgentConnectionBatch(ctx, b.ID)
	if err != nil || old.Items[0].State != "connected" || old.Revision != b.Revision {
		t.Fatal("restart changed successful item", old, err)
	}
	connections, err := s.Connections(ctx)
	if err != nil || len(connections) != 0 {
		t.Fatal("batch bookkeeping granted access", connections, err)
	}
}

func TestAgentBatchSchema23MigrationBacksUpBeforeAddingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Scopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts; DROP TABLE processing_suppliers; DROP TABLE processing_validation_diagnostics; DROP TABLE index_rebuild_controls; DROP TABLE index_attempts; DROP TABLE agent_connection_batch_head; DROP TABLE agent_connection_batches;PRAGMA user_version=23`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Schema != SchemaVersion {
		t.Fatal("current schema migration missing", s.Schema)
	}
	after, err := s.Scopes(context.Background())
	if err != nil || records.MergeDigest(before) != records.MergeDigest(after) {
		t.Fatal("migration changed scopes", err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v23-*.sqlite"))
	if err != nil || len(files) != 1 {
		t.Fatal("missing pre-migration backup", files, err)
	}
	db, err := sql.Open("sqlite", files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 23 {
		t.Fatal("backup not original schema", version, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='agent_connection_batches'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("backup includes new table", count, err)
	}
}
