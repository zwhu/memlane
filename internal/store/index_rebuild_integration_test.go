package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func TestIndexRebuildVersionScopeReplayAndAtomicFailure(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "data.sqlite"))
	mustMerge(t, err)
	defer s.Close()
	var scope string
	mustMerge(t, s.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope))
	other := uuid.NewString()
	_, err = s.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','synthetic other')`, other)
	mustMerge(t, err)
	save := func(dataset, text string) records.Version {
		v, e := s.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: dataset, Text: text})
		mustMerge(t, e)
		mustMerge(t, s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "synthetic-generation", ChunkIDs: []string{uuid.NewString()}}))
		return v
	}
	v := save(scope, "synthetic current original")
	outside := save(other, "synthetic other scope")
	archived := save(scope, "synthetic archived")
	stale := save(scope, "synthetic stale summary")
	_, err = s.DB.Exec(`UPDATE memories SET state='archived' WHERE id=?`, archived.Memory)
	mustMerge(t, err)
	_, err = s.DB.Exec(`INSERT INTO derived_memories(memory,revision,state) VALUES(?,1,'stale')`, stale.Memory)
	mustMerge(t, err)
	beforeVersions := sqlRows(t, s.DB, "versions")
	beforeActivities := sqlRows(t, s.DB, "activities")
	start := records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope}
	task, err := s.StartIndexRebuild(ctx, start)
	mustMerge(t, err)
	if len(task.Items) != 1 || task.Items[0].Memory != v.Memory || task.State != "running" {
		t.Fatal(task)
	}
	current, err := s.Current(ctx, v.Memory)
	mustMerge(t, err)
	if current.IndexState != "pending" || current.Revision != 1 || current.Text != v.Text {
		t.Fatal(current)
	}
	external, err := s.Current(ctx, outside.Memory)
	mustMerge(t, err)
	if external.IndexState != "indexed" {
		t.Fatal(external)
	}
	if !reflect.DeepEqual(beforeVersions, sqlRows(t, s.DB, "versions")) || !reflect.DeepEqual(beforeActivities, sqlRows(t, s.DB, "activities")) {
		t.Fatal("rebuild changed knowledge or activities")
	}
	mustMerge(t, s.IndexFailed(ctx, v.Record, errors.New("explicit synthetic index failure")))
	task, err = s.IndexRebuild(ctx, task.ID)
	mustMerge(t, err)
	if task.State != "failed" || task.Items[0].Error == "" {
		t.Fatal(task)
	}
	mustMerge(t, s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "recovered-generation", ChunkIDs: []string{uuid.NewString()}}))
	task, err = s.StartIndexRebuild(ctx, start)
	mustMerge(t, err)
	if task.State != "completed" || task.Items[0].Error == "" {
		t.Fatal(task)
	}
	current, err = s.Current(ctx, v.Memory)
	mustMerge(t, err)
	if current.IndexState != "indexed" {
		t.Fatal("replay queued completed work", current)
	}
	start.Scope = other
	if _, err = s.StartIndexRebuild(ctx, start); !errors.Is(err, ErrConflict) {
		t.Fatal("same ID changed scope", err)
	}
	changed := records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope}
	task, err = s.StartIndexRebuild(ctx, changed)
	mustMerge(t, err)
	v2, err := s.Apply(ctx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: scope, ExpectedRevision: 1, Text: "synthetic new version"})
	mustMerge(t, err)
	if err = s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "late", ChunkIDs: []string{uuid.NewString()}}); !errors.Is(err, ErrConflict) {
		t.Fatal("late old version accepted", err)
	}
	task, err = s.IndexRebuild(ctx, task.ID)
	mustMerge(t, err)
	if task.State != "expired" {
		t.Fatal(task)
	}
	before := sqlRows(t, s.DB, "memories")
	beforeJobs := sqlRows(t, s.DB, "jobs")
	_, err = s.DB.Exec(`CREATE TRIGGER rebuild_failure BEFORE INSERT ON index_rebuild_items BEGIN SELECT RAISE(ABORT,'explicit SQL fault'); END`)
	mustMerge(t, err)
	failedID := uuid.NewString()
	if _, err = s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: failedID}); err == nil {
		t.Fatal("SQL fault accepted")
	}
	if !reflect.DeepEqual(before, sqlRows(t, s.DB, "memories")) || !reflect.DeepEqual(beforeJobs, sqlRows(t, s.DB, "jobs")) {
		t.Fatal("partial rebuild transaction")
	}
	if _, err = s.IndexRebuild(ctx, failedID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed receipt retained", err)
	}
	if v2.Revision != 2 {
		t.Fatal(v2)
	}
}

func TestIndexRebuildV12BackupPreservesAllOldTables(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v12.sqlite")
	old, err := open(path, false)
	mustMerge(t, err)
	tx, err := old.DB.Begin()
	mustMerge(t, err)
	for _, m := range []func(*sql.Tx) error{migrateMerge, migrateOrganization, migrateOrganizationReviews, migrateExperiences, migrateSchedules, migrateOrganizationLeases, migrateOrganizationRecovery, migrateUsage, migrateInitialization, migrateCode} {
		mustMerge(t, m(tx))
	}
	mustMerge(t, tx.Commit())
	old.Schema = 12
	var scope string
	mustMerge(t, old.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope))
	v, err := old.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "synthetic v12 backup original"})
	mustMerge(t, err)
	mustMerge(t, old.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "synthetic-v12", ChunkIDs: []string{uuid.NewString()}}))
	_, err = old.DB.Exec(`INSERT INTO code_worktrees(id,project,worktree,common_dir,dataset,enabled,generation,state,updated_ms) VALUES(?,?,?, ?,?,0,1,'disabled',1)`, uuid.NewString(), scope, "/synthetic/worktree", "/synthetic/worktree/.git", uuid.NewString())
	mustMerge(t, err)
	rows, err := old.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	mustMerge(t, err)
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
	_, err = old.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, err)
	reader, err := sql.Open("sqlite", path)
	mustMerge(t, err)
	hold, err := reader.Begin()
	mustMerge(t, err)
	var n int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM memories`).Scan(&n))
	mustMerge(t, old.Close())
	wal, err := os.Stat(path + "-wal")
	mustMerge(t, err)
	if wal.Size() == 0 {
		t.Fatal("nonempty WAL missing")
	}
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v12-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 || current.Schema != SchemaVersion {
		t.Fatal(backups, current.Schema)
	}
	backup, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer backup.Close()
	var version int
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 12 || integrity != "ok" {
		t.Fatal(version, integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("old table changed", name)
		}
	}
	if _, err = current.LatestIndexRebuild(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("migration started rebuild", err)
	}
}
