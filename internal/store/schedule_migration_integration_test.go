package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestScheduleV6MigrationPreservesWALGlobalConditionsAndReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v6.sqlite")
	legacy, e := open(path, false)
	mustMerge(t, e)
	tx, e := legacy.DB.Begin()
	mustMerge(t, e)
	mustMerge(t, migrateMerge(tx))
	mustMerge(t, migrateOrganization(tx))
	mustMerge(t, migrateOrganizationReviews(tx))
	mustMerge(t, migrateExperiences(tx))
	mustMerge(t, tx.Commit())
	legacy.Schema = 6
	task, d := globalExperienceFixture(t, legacy)
	done, e := legacy.ReviewOrganization(mergeCtx, d)
	mustMerge(t, e)
	id := done.Entries[0].Receipt.Memory
	v, e := legacy.Current(mergeCtx, id)
	mustMerge(t, e)
	conditions := "v6合成用户修改的适用条件"
	_, e = legacy.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: id, Dataset: v.Dataset, ExpectedRevision: 1, Text: "v6用户审核的新通用正文", Conditions: &conditions})
	mustMerge(t, e)
	rows, e := legacy.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	mustMerge(t, e)
	var tables []string
	for rows.Next() {
		var name string
		mustMerge(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	mustMerge(t, rows.Err())
	rows.Close()
	before := map[string][][]any{}
	for _, name := range tables {
		before[name] = sqlRows(t, legacy.DB, name)
	}
	_, e = legacy.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, e)
	reader, e := sql.Open("sqlite", path)
	mustMerge(t, e)
	hold, e := reader.Begin()
	mustMerge(t, e)
	var count int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM versions`).Scan(&count))
	mustMerge(t, legacy.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("committed v6 WAL absent")
	}
	current, e := Open(path)
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v6-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal("missing v6 consistency backup", backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 6 || integrity != "ok" {
		t.Fatal("invalid v6 backup", version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("v6 data or backup changed", name)
		}
	}
	v, e = current.Current(mergeCtx, id)
	mustMerge(t, e)
	if v.Revision != 2 || v.Experience.Conditions != conditions {
		t.Fatal("current global conditions lost", v)
	}
	_, e = current.ReviewOrganization(mergeCtx, d)
	mustMerge(t, e)
	v, e = current.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: id, Dataset: v.Dataset, ExpectedRevision: 2, RestoreRevision: 1})
	mustMerge(t, e)
	if v.Revision != 3 || v.Experience.Conditions != task.Entries[0].Item.Conditions {
		t.Fatal("old global version could not restore under v7", v)
	}
}
