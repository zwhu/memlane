package main

// Real temporary SQLite and private fixture markers. No App/Agent/model starts.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	"github.com/zwhu/memlane/internal/store"
)

func replayFixture(t *testing.T) (string, string, string) {
	t.Helper()
	f, err := os.MkdirTemp("", "memlane-p3-code-app-replay-gate-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(f) })
	root := filepath.Join(f, "client/data")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, v any) {
		t.Helper()
		data, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(f, "source-info.json"), map[string]any{"synthetic_only": true, "user_data_copied": false, "agent_configs_copied": false})
	write(filepath.Join(f, "runtime-config.json"), map[string]string{"root": filepath.Dir(root)})
	write(filepath.Join(root, "service-state.json"), records.ServiceState{Stopped: true})
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var scope string
	if err = db.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err = db.Apply(context.Background(), records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "synthetic completed replay gate original"})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	backup, err := db.Backup(context.Background(), root, uuid.NewString())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	op := uuid.NewString()
	if _, err = (recoveryfiles.RestoreManager{Root: root}).Restore(context.Background(), backup.ID, op, store.PrepareRestoredSnapshot); err != nil {
		t.Fatal(err)
	}
	return root, backup.ID, op
}

func TestReplayGateAcceptsCompletedAndRejectsNewOrPendingWithoutWrites(t *testing.T) {
	root, backup, op := replayFixture(t)
	before, err := os.ReadFile(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = validateReplay(context.Background(), root, backup, op); err != nil {
		t.Fatal(err)
	}
	if err = validateReplay(context.Background(), root, backup, uuid.NewString()); err == nil || !strings.Contains(err.Error(), "completed replay receipt required") {
		t.Fatal("new UUID accepted as completed replay", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "backups/restores"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != op {
		t.Fatal("gate created new restore operation", entries)
	}
	if err = os.WriteFile(filepath.Join(root, ".restore-pending.json"), []byte(`{"id":"`+op+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = validateReplay(context.Background(), root, backup, op); err == nil || !strings.Contains(err.Error(), "refuses pending restore") {
		t.Fatal("pending accepted", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "memlane.sqlite"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("preflight changed database bytes", err)
	}
}

func TestReplayGateRejectsLinkedRootAndWrongSyntheticBinding(t *testing.T) {
	root, backup, op := replayFixture(t)
	fixture := filepath.Dir(filepath.Dir(root))
	sentinel := filepath.Join(root, "untouched-marker")
	if err := os.WriteFile(sentinel, []byte("synthetic untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root+"-original", root); err != nil {
		t.Fatal(err)
	}
	if err := validateReplay(context.Background(), root, backup, op); err == nil || !strings.Contains(err.Error(), "must not be links") {
		t.Fatal("symlink root accepted", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root+"-original", root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "source-info.json"), []byte(`{"synthetic_only":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateReplay(context.Background(), root, backup, op); err == nil || !strings.Contains(err.Error(), "synthetic source marker required") {
		t.Fatal("non-synthetic marker accepted", err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "source-info.json"), []byte(`{"synthetic_only":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{"root": os.TempDir()})
	if err := os.WriteFile(filepath.Join(fixture, "runtime-config.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateReplay(context.Background(), root, backup, op); err == nil || !strings.Contains(err.Error(), "runtime config differs") {
		t.Fatal("wrong runtime binding accepted", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "synthetic untouched" {
		t.Fatal("rejected root changed files", err)
	}
}
