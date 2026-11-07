package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestOrganizationLeaseV7MigrationRetainsPlanResultAndNonemptyWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7.sqlite")
	legacy, e := open(path, false)
	mustMerge(t, e)
	tx, e := legacy.DB.Begin()
	mustMerge(t, e)
	for _, migration := range []func(*sql.Tx) error{migrateMerge, migrateOrganization, migrateOrganizationReviews, migrateExperiences, migrateSchedules} {
		mustMerge(t, migration(tx))
	}
	mustMerge(t, tx.Commit())
	legacy.Schema = 7
	task, _, raw := organizationFixture(t, legacy)
	stageOrganizationFixture(t, legacy, task, raw)
	_, e = legacy.ApplyOrganizationItem(mergeCtx, task.ID, task.Attempt, 1)
	mustMerge(t, e)
	d := records.ScheduleDraft{ID: uuid.NewString(), OperationID: uuid.NewString(), Enabled: true, Spec: records.ScheduleSpec{Project: task.Project, Executor: "traex", Timezone: "UTC", Frequency: "interval", IntervalHours: 1, WindowMinutes: 60, MaterialLimit: 8}}
	_, e = legacy.PutSchedule(mergeCtx, d, time.Now())
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
		t.Fatal("v7 WAL absent")
	}
	current, e := Open(path)
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v7-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != 8 {
		t.Fatal("v7 backup missing", backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 7 || integrity != "ok" {
		t.Fatal(version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("v7 table changed", name)
		}
	}
	v, e := current.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if v.Result == nil || v.Entries[0].State != "applied" {
		t.Fatal("partial result lost")
	}
}
