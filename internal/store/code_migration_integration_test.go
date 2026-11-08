package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Real SQLite v11 with committed WAL pages. No code scan or model is involved.
func TestCodeV11MigrationBacksUpAndPreservesEveryOldTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v11.sqlite")
	old, e := open(path, false)
	mustMerge(t, e)
	tx, e := old.DB.Begin()
	mustMerge(t, e)
	for _, m := range []func(*sql.Tx) error{migrateMerge, migrateOrganization, migrateOrganizationReviews, migrateExperiences, migrateSchedules, migrateOrganizationLeases, migrateOrganizationRecovery, migrateUsage, migrateInitialization} {
		mustMerge(t, m(tx))
	}
	mustMerge(t, tx.Commit())
	old.Schema = 11
	project := uuid.NewString()
	_, e = old.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','synthetic pre-code project')`, project)
	mustMerge(t, e)
	c, e := old.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic pre-code", Project: project, AllowedScopes: []string{project}})
	mustMerge(t, e)
	v, e := old.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "synthetic pre-code immutable knowledge"})
	mustMerge(t, e)
	_, e = old.RecordUsage(ctx, records.UsageObservation{OperationID: uuid.NewString(), Digest: UsageDigest("synthetic pre-code read"), Channel: "daily", Kind: "read", ConnectionID: c.ID, Generation: c.Generation, WorkspaceID: UsageDigest("synthetic workspace"), Targets: []records.UsageTarget{{Memory: v.Memory, Dataset: project, Revision: 1}}})
	mustMerge(t, e)
	mustMerge(t, old.SetImportant(ctx, records.ImportantRequest{Memory: v.Memory, ExpectedRevision: 1, Important: true}))
	rows, e := old.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	mustMerge(t, e)
	tables := []string{}
	for rows.Next() {
		var name string
		mustMerge(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	mustMerge(t, rows.Err())
	rows.Close()
	before := map[string][][]any{}
	for _, name := range tables {
		before[name] = sqlRows(t, old.DB, name)
	}
	_, e = old.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, e)
	reader, e := sql.Open("sqlite", path)
	mustMerge(t, e)
	hold, e := reader.Begin()
	mustMerge(t, e)
	var count int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM memories`).Scan(&count))
	mustMerge(t, old.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("committed v11 WAL missing")
	}
	current, e := Open(path)
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v11-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != 12 {
		t.Fatal("code migration/backup missing", backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 11 || integrity != "ok" {
		t.Fatal(version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("old table changed or lost", name)
		}
	}
	states, e := current.Codes(ctx)
	mustMerge(t, e)
	if len(states) != 0 {
		t.Fatal("migration enabled or scanned code without user action")
	}
}
