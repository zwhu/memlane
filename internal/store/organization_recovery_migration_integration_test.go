package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOrganizationRecoveryV8MigrationPreservesWALLeasesGlobalAndPartialFacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v8.sqlite")
	legacy, e := open(path, false)
	mustMerge(t, e)
	tx, e := legacy.DB.Begin()
	mustMerge(t, e)
	for _, migration := range []func(*sql.Tx) error{migrateMerge, migrateOrganization, migrateOrganizationReviews, migrateExperiences, migrateSchedules, migrateOrganizationLeases} {
		mustMerge(t, migration(tx))
	}
	mustMerge(t, tx.Commit())
	legacy.Schema = 8
	global, decision := globalExperienceFixture(t, legacy)
	confirmed, e := legacy.ReviewOrganization(mergeCtx, decision)
	mustMerge(t, e)
	globalID := confirmed.Entries[0].Receipt.Memory
	v, e := legacy.Current(mergeCtx, globalID)
	mustMerge(t, e)
	conditions := "v8 用户明确适用条件"
	_, e = legacy.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: globalID, Dataset: v.Dataset, ExpectedRevision: 1, Text: "v8 用户正文", Conditions: &conditions})
	mustMerge(t, e)
	task, _, raw := organizationFixture(t, legacy)
	owner := uuid.NewString()
	ctx := WithOrganizationLease(mergeCtx, owner)
	mustMerge(t, legacy.AcquireOrganizationLease(ctx, task.ID, task.Attempt, owner, time.Now()))
	mustMerge(t, legacy.ClaimOrganization(ctx, task.ID, task.Attempt))
	_, e = legacy.StageOrganization(ctx, task.ID, task.Attempt, raw, map[string]bool{"fixture": true}, "")
	mustMerge(t, e)
	_, e = legacy.ApplyOrganizationItem(ctx, task.ID, task.Attempt, 1)
	mustMerge(t, e)
	mustMerge(t, legacy.FailOrganizationItem(ctx, task.ID, task.Attempt, 2))
	_, e = legacy.FinishOrganization(ctx, task.ID, task.Attempt)
	mustMerge(t, e)
	mustMerge(t, legacy.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
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
		t.Fatal("v8 committed WAL absent")
	}
	current, e := Open(path)
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v8-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal("v8 consistency backup missing", backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 8 || integrity != "ok" {
		t.Fatal(version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("v8 facts or lease changed", name)
		}
	}
	partial, e := current.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if partial.Result == nil || partial.State != "partial_failed" || partial.Entries[0].State != "applied" || partial.Recovery != nil {
		t.Fatal("old partial result/policy changed")
	}
	v, e = current.Apply(mergeCtx, Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: globalID, Dataset: v.Dataset, ExpectedRevision: 2, RestoreRevision: 1})
	mustMerge(t, e)
	if v.Revision != 3 || v.Experience.Conditions != global.Entries[0].Item.Conditions {
		t.Fatal("global history restore lost")
	}
}
