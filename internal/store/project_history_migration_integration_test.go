package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectHistorySchema20MigrationKeepsOriginalTaskAndBackup(t *testing.T) {
	s, ctx, task, _ := failedProjectIndexFixture(t)
	_, err := s.DB.Exec(`DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts; DROP TABLE processing_suppliers; DROP TABLE processing_validation_diagnostics; DROP TABLE index_rebuild_controls; DROP TABLE index_attempts; DROP TABLE agent_connection_batch_head; DROP TABLE agent_connection_batches; DROP TABLE organization_start_intents; DROP TABLE organization_previews; DROP TABLE organization_preview_head; DROP TABLE experience_previews; DROP TABLE experience_preview_head; DROP TABLE project_task_histories; PRAGMA user_version=20`)
	mustMerge(t, err)
	s.Schema = 20
	before := restoreTables(t, s.DB)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v20-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal("schema20 original backup missing", backups)
	}
	old, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer old.Close()
	var version int
	mustMerge(t, old.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 20 {
		t.Fatal(version)
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, old, table)) || !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) {
			t.Fatal("migration changed old records", table)
		}
	}
	read, err := current.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if !reflect.DeepEqual(task.Execution, read.Execution) {
		t.Fatal("migration rewrote model receipt")
	}
	if len(sqlRows(t, current.DB, "project_task_histories")) != 0 {
		t.Fatal("migration invented historical material")
	}
	_, err = current.Scopes(context.Background())
	mustMerge(t, err)
}
