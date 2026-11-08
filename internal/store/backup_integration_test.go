package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
)

func TestBackupActualWALAllTablesAndStableReplay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "memlane.sqlite")
	s, err := Open(path)
	mustMerge(t, err)
	defer s.Close()
	var scope string
	mustMerge(t, s.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope))
	v, err := s.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "合成备份原文：保留 pnpm-lock.yaml 与来源。"})
	mustMerge(t, err)
	reader, err := sql.Open("sqlite", path)
	mustMerge(t, err)
	defer reader.Close()
	hold, err := reader.Begin()
	mustMerge(t, err)
	defer hold.Rollback()
	var count int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM memories`).Scan(&count))
	v, err = s.Apply(ctx, records.Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: scope, ExpectedRevision: 1, Text: "合成备份当前 v2：保留 TypeScript 函数边界与完整来源。"})
	mustMerge(t, err)
	mustMerge(t, s.MarkIndexed(ctx, v.Record, engine.Receipt{Generation: "synthetic-backup-generation", ChunkIDs: []string{uuid.NewString()}}))
	wal, err := os.Stat(path + "-wal")
	mustMerge(t, err)
	if wal.Size() == 0 {
		t.Fatal("committed WAL fixture missing")
	}
	rows, err := s.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
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
		before[name] = sqlRows(t, s.DB, name)
	}
	id := uuid.NewString()
	receipt, err := s.Backup(ctx, root, id)
	mustMerge(t, err)
	if receipt.ID != id || receipt.Schema != SchemaVersion || receipt.Verification != "verified" || receipt.Bytes <= 0 {
		t.Fatal(receipt)
	}
	backupPath := filepath.Join(root, "backups/manual", id, "snapshot.sqlite")
	backup, err := sql.Open("sqlite", backupPath)
	mustMerge(t, err)
	defer backup.Close()
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	for _, name := range tables {
		if !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) || !reflect.DeepEqual(before[name], sqlRows(t, s.DB, name)) {
			t.Fatal("WAL backup lost or changed table", name)
		}
	}
	_, err = s.Apply(ctx, records.Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: scope, ExpectedRevision: 2, Text: "合成备份之后的当前 v3"})
	mustMerge(t, err)
	replayed, err := s.Backup(ctx, root, id)
	mustMerge(t, err)
	if !reflect.DeepEqual(receipt, replayed) {
		t.Fatal("operation replay captured changed data", receipt, replayed)
	}
	for _, file := range []string{"snapshot.sqlite", "receipt.json"} {
		info, e := os.Stat(filepath.Join(root, "backups/manual", id, file))
		mustMerge(t, e)
		if info.Mode().Perm() != 0600 {
			t.Fatal("backup permissions", file, info.Mode())
		}
	}
	list, err := (recoveryfiles.Manager{Root: root}).List(ctx)
	mustMerge(t, err)
	if len(list) != 1 || list[0].Verification != "unverified" {
		t.Fatal(list)
	}
}

func TestBackupVaultLinksTamperAndCancellation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(filepath.Join(root, "memlane.sqlite"))
	mustMerge(t, err)
	defer s.Close()
	id := uuid.NewString()
	receipt, err := s.Backup(ctx, root, id)
	mustMerge(t, err)
	m := recoveryfiles.Manager{Root: root}
	snap, err := m.Verify(ctx, id)
	mustMerge(t, err)
	manual := filepath.Join(root, "backups/manual")
	unknown := filepath.Join(manual, "unknown-user-notes")
	mustMerge(t, os.Mkdir(unknown, 0700))
	mustMerge(t, os.WriteFile(filepath.Join(unknown, "sentinel"), []byte("explicit untouched fixture"), 0600))
	file := filepath.Join(manual, id, "snapshot.sqlite")
	original, err := os.ReadFile(file)
	mustMerge(t, err)
	damaged := append([]byte(nil), original...)
	damaged[len(damaged)-1] ^= 1
	mustMerge(t, os.WriteFile(file, damaged, 0600))
	if _, err = m.Verify(ctx, id); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("tampered backup accepted", err)
	}
	if _, err = s.Backup(ctx, root, id); err == nil {
		t.Fatal("damaged same-ID backup overwritten")
	}
	mustMerge(t, os.WriteFile(file, original, 0600))
	link := filepath.Join(root, "extra-hard-link")
	mustMerge(t, os.Link(file, link))
	if _, err = m.Verify(ctx, id); err == nil {
		t.Fatal("hardlink accepted")
	}
	mustMerge(t, os.Remove(link))
	mustMerge(t, os.Rename(file, file+".original"))
	mustMerge(t, os.Symlink("snapshot.sqlite.original", file))
	if _, err = m.Verify(ctx, id); err == nil {
		t.Fatal("internal relative snapshot link accepted")
	}
	mustMerge(t, os.Remove(file))
	mustMerge(t, os.Rename(file+".original", file))
	other := t.TempDir()
	otherManager := recoveryfiles.Manager{Root: other}
	_, err = otherManager.Create(ctx, uuid.NewString(), func(context.Context) (int, []byte, error) { return 0, nil, errors.New("explicit vault setup fault") })
	if err == nil {
		t.Fatal("setup fault accepted")
	}
	foreignDir := filepath.Join(other, "backups/manual", id)
	mustMerge(t, os.Mkdir(foreignDir, 0700))
	for _, name := range []string{"snapshot.sqlite", "receipt.json"} {
		data, e := os.ReadFile(filepath.Join(manual, id, name))
		mustMerge(t, e)
		mustMerge(t, os.WriteFile(filepath.Join(foreignDir, name), data, 0600))
	}
	if _, err = otherManager.Verify(ctx, id); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatal("foreign vault backup accepted", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	fresh := uuid.NewString()
	_, err = m.Create(canceled, fresh, func(context.Context) (int, []byte, error) { cancel(); return receipt.Schema, snap.Data, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled snapshot published", err)
	}
	if _, err = os.Stat(filepath.Join(manual, fresh)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled operation visible", err)
	}
	newID := uuid.NewString()
	if _, err = s.Backup(ctx, root, newID); err != nil {
		t.Fatal("negative tests broke subsequent backup", err)
	}
	if data, e := os.ReadFile(filepath.Join(unknown, "sentinel")); e != nil || string(data) != "explicit untouched fixture" {
		t.Fatal("unknown files changed", e)
	}
}
