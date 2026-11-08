package store

// Real temporary SQLite snapshots and explicit status/receipt fixtures. These
// checks prove the offline transformation, not App or model acceptance.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
)

func restoreTables(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	mustMerge(t, err)
	names := []string{}
	for rows.Next() {
		var name string
		mustMerge(t, rows.Scan(&name))
		names = append(names, name)
	}
	mustMerge(t, rows.Err())
	rows.Close()
	result := map[string][][]any{}
	for _, name := range names {
		result[name] = sqlRows(t, db, name)
	}
	return result
}

func TestRestoreSchema13MigrationPreservesAllTablesAndBackup(t *testing.T) {
	s, _, task, _ := recoveryFixture(t, 1)
	// The v14 retirement table is empty in this fixture. Removing only that
	// addition produces the exact shipped v13 schema, with real old rows/WAL.
	_, err := s.DB.Exec(`DROP TABLE connection_retirements; PRAGMA user_version=13; PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, err)
	s.Schema = 13
	before := restoreTables(t, s.DB)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	reader, err := sql.Open("sqlite", path)
	mustMerge(t, err)
	hold, err := reader.Begin()
	mustMerge(t, err)
	var n int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM versions`).Scan(&n))
	mustMerge(t, s.Close())
	wal, err := os.Stat(path + "-wal")
	mustMerge(t, err)
	if wal.Size() == 0 {
		t.Fatal("schema13 WAL absent")
	}
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v13-*.sqlite"))
	mustMerge(t, err)
	if current.Schema != SchemaVersion || len(backups) != 1 {
		t.Fatal("migration backup missing", current.Schema, backups)
	}
	backup, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer backup.Close()
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) || !reflect.DeepEqual(rows, sqlRows(t, backup, table)) {
			t.Fatal("migration changed old table", table)
		}
	}
	var version, retired int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, current.DB.QueryRow(`SELECT count(*) FROM connection_retirements`).Scan(&retired))
	if version != 13 || retired != 0 {
		t.Fatal("migration revoked authorization or rewrote backup", version, retired)
	}
	old, err := current.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, err)
	if old.State != task.State || old.Recovery.State != task.Recovery.State {
		t.Fatal("migration performed restore side effects")
	}
}

func TestRestorePreparedSnapshotPreservesKnowledgeAndRetiresAuthority(t *testing.T) {
	ctx := context.Background()
	s, plan, active, _ := recoveryFixture(t, 1)
	owner := uuid.NewString()
	leased := WithOrganizationLease(ctx, owner)
	mustMerge(t, s.AcquireOrganizationLease(leased, active.ID, active.Attempt, owner, time.Now()))
	mustMerge(t, s.ClaimOrganization(leased, active.ID, active.Attempt))
	c, err := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "traex", Name: "synthetic restored Agent", Project: active.Project, AllowedScopes: []string{active.Project}})
	mustMerge(t, err)
	_, decision := globalExperienceFixture(t, s)
	worktree := filepath.Join(t.TempDir(), "code")
	if output, e := exec.Command("git", "init", worktree).CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	mustMerge(t, os.WriteFile(filepath.Join(worktree, "main.ts"), []byte("export function restoredFixture() {}\n"), 0600))
	project, err := s.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic restore code", Path: worktree})
	mustMerge(t, err)
	code, err := s.CodeAction(ctx, records.CodeAction{OperationID: uuid.NewString(), Kind: "enable", Project: project.ID, Path: worktree})
	mustMerge(t, err)
	manifest, err := codegraph.Capture(ctx, code.Worktree, "")
	mustMerge(t, err)
	job, err := s.StartCodeJob(ctx, code, manifest)
	mustMerge(t, err)
	v, err := s.Current(ctx, active.Snapshot.Inputs[0].Memory)
	mustMerge(t, err)
	v, err = s.Apply(ctx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: v.Revision, Text: "合成恢复当前原文 v2"})
	mustMerge(t, err)
	mustMerge(t, s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "synthetic-old-index", ChunkIDs: []string{uuid.NewString()}}))
	_, err = s.DB.Exec(`DROP TABLE connection_retirements; PRAGMA user_version=13`)
	mustMerge(t, err)
	s.Schema = 13
	before := restoreTables(t, s.DB)
	_, source, err := s.snapshot(ctx)
	mustMerge(t, err)
	untouched := append([]byte(nil), source...)
	op := uuid.NewString()
	prepared, stats, err := PrepareRestoredSnapshot(ctx, source, op)
	mustMerge(t, err)
	if !bytes.Equal(source, untouched) || !reflect.DeepEqual(before, restoreTables(t, s.DB)) {
		t.Fatal("preparation modified source backup or live source")
	}
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	for _, table := range []string{"versions", "operations", "memory_metadata", "activities", "organization_sources", "organization_results", "global_experience_decisions", "usage_events", "index_bindings", "scope_history"} {
		if !reflect.DeepEqual(before[table], sqlRows(t, restored.DB, table)) {
			t.Fatal("restore changed original/history", table)
		}
	}
	current, err := restored.Current(ctx, v.Memory)
	mustMerge(t, err)
	if current.Revision != 2 || current.Original != v.Original || current.IndexState != "pending" || stats.Connections != 1 || stats.Plans != 1 || stats.CodeWorktrees != 1 {
		t.Fatal("current version or restore statistics", current, stats)
	}
	old, err := restored.Connection(ctx, c.ID)
	mustMerge(t, err)
	if old.Enabled || old.Generation != c.Generation+1 {
		t.Fatal("connection still enabled", old)
	}
	for _, generation := range []int64{c.Generation, old.Generation} {
		_, _, err = restored.Authorize(ctx, records.AgentRequest{ConnectionID: c.ID, Generation: generation})
		var denied *records.AccessError
		if !errors.As(err, &denied) || denied.Code != "restore_reconnect_required" {
			t.Fatal("old connection authorized", generation, err)
		}
	}
	fresh, err := restored.ConfigureConnection(ctx, records.ConnectionDraft{ID: c.ID, Agent: c.Agent, Name: c.Name, Project: c.Project, AllowedScopes: c.AllowedScopes})
	mustMerge(t, err)
	if fresh.ID == c.ID || fresh.Generation != 1 {
		t.Fatal("explicit reconnect reused retired identity", fresh)
	}
	_, _, err = restored.Authorize(ctx, records.AgentRequest{ConnectionID: fresh.ID, Generation: fresh.Generation, Tool: "memory_read"})
	mustMerge(t, err)
	if _, _, err = restored.Authorize(ctx, records.AgentRequest{ConnectionID: c.ID, Generation: old.Generation}); err == nil {
		t.Fatal("reconnect revived old bridge")
	}
	off, err := restored.Schedule(ctx, plan.ID)
	mustMerge(t, err)
	if off.Enabled || off.Generation != plan.Generation+1 || off.NextRunMS != 0 {
		t.Fatal("restored plan authorized", off)
	}
	task, err := restored.OrganizationTask(ctx, active.ID)
	mustMerge(t, err)
	if task.State != "interrupted" || !task.Cancelled || task.Recovery.State != "cancelled" {
		t.Fatal("active model task survived restore", task)
	}
	if err = restored.RenewOrganizationLease(ctx, active.ID, active.Attempt, owner, time.Now()); err == nil {
		t.Fatal("old lease renewed")
	}
	_, err = restored.ReviewOrganization(ctx, decision)
	if err == nil {
		t.Fatal("restored global proposal applied")
	}
	stopped, err := restored.Code(ctx, code.ID)
	mustMerge(t, err)
	if stopped.Enabled || stopped.Generation != code.Generation+1 || stopped.State != "disabled" {
		t.Fatal("restored graph authorized", stopped)
	}
	if err = restored.FinishCodeJob(ctx, job, nil, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("old graph result accepted", err)
	}
}

func TestRestoreExpiresInitializationAndRejectsInvalidSnapshot(t *testing.T) {
	s, _, _, batch := initializationFixture(t)
	_, source, err := s.snapshot(mergeCtx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(mergeCtx, source, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "initialization.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	b, err := restored.Initialization(mergeCtx, batch.ID)
	mustMerge(t, err)
	if b.State != "expired" || b.Error != "backup_restored" {
		t.Fatal("pending initialization survived", b)
	}
	for _, invalid := range []string{"bad-operation", strings.ToUpper(uuid.NewString())} {
		if _, _, err = PrepareRestoredSnapshot(mergeCtx, source, invalid); err == nil || !strings.Contains(err.Error(), "UUID") {
			t.Fatal("noncanonical restore operation accepted", err)
		}
	}
	cancelled, cancel := context.WithCancel(mergeCtx)
	cancel()
	if _, _, err = PrepareRestoredSnapshot(cancelled, source, uuid.NewString()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled transformation returned success", err)
	}
	if _, _, err = PrepareRestoredSnapshot(mergeCtx, []byte("damaged"), uuid.NewString()); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatal("invalid source accepted", err)
	}
	if _, _, err = PrepareRestoredSnapshot(mergeCtx, source, uuid.NewString()); err != nil {
		t.Fatal("negative tests broke later restore", err)
	}
}

func TestRestoreFilesystemProductionTransformAndBeforeOpenRecovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "memlane.sqlite")
	s, err := Open(path)
	mustMerge(t, err)
	var scope string
	mustMerge(t, s.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope))
	v, err := s.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "合成完整恢复 v1", Source: "synthetic source", SourceDetail: &records.AgentSource{Fragment: "synthetic original provenance"}})
	mustMerge(t, err)
	v, err = s.Apply(ctx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: scope, ExpectedRevision: 1, Text: "合成完整恢复 v2"})
	mustMerge(t, err)
	backup, err := s.Backup(ctx, root, uuid.NewString())
	mustMerge(t, err)
	before := restoreTables(t, s.DB)
	_, err = s.Apply(ctx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: scope, ExpectedRevision: 2, Text: "合成恢复之前 v3"})
	mustMerge(t, err)
	mustMerge(t, s.Close())
	prior, err := os.ReadFile(path)
	mustMerge(t, err)
	mustMerge(t, os.WriteFile(filepath.Join(root, "service-state.json"), []byte(`{"stopped":true}`), 0600))
	op := uuid.NewString()
	r, err := (recoveryfiles.RestoreManager{Root: root}).Restore(ctx, backup.ID, op, PrepareRestoredSnapshot)
	mustMerge(t, err)
	if r.ID != op || r.Stats.Memories != 1 {
		t.Fatal(r)
	}
	archive, err := os.ReadFile(filepath.Join(root, "backups/restores", op, "previous-memlane.sqlite"))
	mustMerge(t, err)
	if !bytes.Equal(prior, archive) {
		t.Fatal("production transform lost pre-restore database")
	}
	restored, err := Open(path)
	mustMerge(t, err)
	current, err := restored.Current(ctx, v.Memory)
	mustMerge(t, err)
	if current.Revision != 2 || current.Original != v.Original || current.SourceDetail.Fragment != v.SourceDetail.Fragment || current.IndexState != "pending" {
		t.Fatal("production restore failed current/source/index", current)
	}
	for _, table := range []string{"versions", "operations", "memory_metadata", "activities"} {
		if !reflect.DeepEqual(before[table], sqlRows(t, restored.DB, table)) {
			t.Fatal("filesystem transform changed original history", table)
		}
	}
	mustMerge(t, restored.Close())
	// Explicit crash-position fixture: restore the archived original main file
	// and durable commit intent, remove only the completed receipt. This models
	// a journal-persisted crash before main replacement, not a real crash event.
	mustMerge(t, os.WriteFile(path, prior, 0600))
	pointer, err := json.Marshal(map[string]string{"id": op})
	mustMerge(t, err)
	mustMerge(t, os.WriteFile(filepath.Join(root, ".restore-pending.json"), pointer, 0600))
	mustMerge(t, os.Remove(filepath.Join(root, "backups/restores", op, "receipt.json")))
	// A user Resume before startup recovery has cleared stopped. Recovery must
	// durably set it again before removing the pending intent.
	mustMerge(t, os.WriteFile(filepath.Join(root, "service-state.json"), []byte(`{"stopped":false,"stop_generation":1}`), 0600))
	if opened, e := Open(path); !errors.Is(e, ErrRestoreRecovered) || opened != nil {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("opening SQL bypassed pending restore", e)
	}
	var stopped records.ServiceState
	state, err := os.ReadFile(filepath.Join(root, "service-state.json"))
	mustMerge(t, err)
	mustMerge(t, json.Unmarshal(state, &stopped))
	if !stopped.Stopped || stopped.StopGeneration != 2 {
		t.Fatal("recovery intent cleared without stopped epoch", stopped)
	}
	resumed, err := Open(path)
	mustMerge(t, err)
	defer resumed.Close()
	current, err = resumed.Current(ctx, v.Memory)
	mustMerge(t, err)
	if current.Revision != 2 || current.Original != v.Original {
		t.Fatal("before-open recovery loaded archived v3", current)
	}
}
