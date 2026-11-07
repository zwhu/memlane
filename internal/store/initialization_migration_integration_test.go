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

func TestInitializationV10WALMigrationKeepsUsageReceiptsCoverageAndImportance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v10.sqlite")
	old, e := open(path, false)
	mustMerge(t, e)
	tx, e := old.DB.Begin()
	mustMerge(t, e)
	for _, m := range []func(*sql.Tx) error{migrateMerge, migrateOrganization, migrateOrganizationReviews, migrateExperiences, migrateSchedules, migrateOrganizationLeases, migrateOrganizationRecovery, migrateUsage} {
		mustMerge(t, m(tx))
	}
	mustMerge(t, tx.Commit())
	old.Schema = 10
	project := uuid.NewString()
	_, e = old.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','old U fixture')`, project)
	mustMerge(t, e)
	c, e := old.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "old U", Project: project, AllowedScopes: []string{project}})
	mustMerge(t, e)
	v, e := old.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "old U knowledge"})
	mustMerge(t, e)
	observation := records.UsageObservation{OperationID: uuid.NewString(), Digest: UsageDigest("old U request"), Channel: "daily", Kind: "read", ConnectionID: c.ID, Generation: c.Generation, WorkspaceID: UsageDigest("known workspace"), Targets: []records.UsageTarget{{Memory: v.Memory, Dataset: project, Revision: 1}}}
	r, e := old.RecordUsage(ctx, observation)
	mustMerge(t, e)
	adoption := observation
	adoption.OperationID = uuid.NewString()
	adoption.Kind = "adopt"
	adoption.Exposure = r.ID
	_, e = old.RecordUsage(ctx, adoption)
	mustMerge(t, e)
	mustMerge(t, old.SetImportant(ctx, records.ImportantRequest{Memory: v.Memory, ExpectedRevision: 1, Important: true}))
	beforeUsage, e := old.Usage(ctx, v.Memory)
	mustMerge(t, e)
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
	var n int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM usage_events`).Scan(&n))
	mustMerge(t, old.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("v10 committed WAL missing")
	}
	current, e := Open(path)
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v10-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal(backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 10 || integrity != "ok" {
		t.Fatal(version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("v10 table changed", name)
		}
	}
	after, e := current.Usage(ctx, v.Memory)
	mustMerge(t, e)
	if !reflect.DeepEqual(beforeUsage, after) {
		t.Fatal("coverage/usage/important changed", beforeUsage, after)
	}
	replay, e := current.RecordUsage(ctx, observation)
	mustMerge(t, e)
	if !reflect.DeepEqual(r, replay) {
		t.Fatal("old read receipt lost")
	}
	var batches int
	mustMerge(t, current.DB.QueryRow(`SELECT count(*) FROM initialization_batches`).Scan(&batches))
	if batches != 0 {
		t.Fatal("migration fabricated initialization")
	}
}
