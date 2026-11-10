package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestProjectIndexSchema19MigrationPreservesReceiptAndBackup(t *testing.T) {
	s, ctx, task, _ := failedProjectIndexFixture(t)
	_, err := s.DB.Exec(`DROP TABLE project_task_histories; DROP TABLE project_index_recoveries; PRAGMA user_version=19; PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, err)
	s.Schema = 19
	before := restoreTables(t, s.DB)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
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
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v19-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal("missing schema19 original backup", backups, current.Schema)
	}
	old, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer old.Close()
	var version int
	mustMerge(t, old.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 19 {
		t.Fatal(version)
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, old, table)) || !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) {
			t.Fatal("migration changed original rows", table)
		}
	}
	after, err := current.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if !reflect.DeepEqual(task.Execution, after.Execution) || after.IndexRecovery != nil {
		t.Fatal("migration invented or changed execution")
	}
	if rows := sqlRows(t, current.DB, "project_index_recoveries"); len(rows) != 0 {
		t.Fatal("migration started recovery")
	}
	_, err = current.ListProjectTasks(context.Background(), records.MergeListQuery{Project: task.Snapshot.Project, ActiveOnly: true, IncludeIndexRecovery: true})
	mustMerge(t, err)
}
