package recoveryfiles

// Real SQLite files and explicitly labelled filesystem interruption fixtures.
// The preparation callback is a byte-preserving IO fixture, not the production
// schema/authorization transformation or App/Rust acceptance.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func restoreIOFixture(t *testing.T) (string, string, []byte, map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	source := sqliteFixture(t)
	id := uuid.NewString()
	if _, err := (Manager{Root: root}).Create(ctx, id, func(context.Context) (int, []byte, error) { return 13, source, nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "memlane.sqlite")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL;PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err = hold.QueryRow(`SELECT text FROM original`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE original SET text='synthetic pre-restore committed WAL v2'`); err != nil {
		t.Fatal(err)
	}
	prior := map[string][]byte{}
	for _, name := range []string{"memlane.sqlite", "memlane.sqlite-wal", "memlane.sqlite-shm"} {
		prior[name], err = os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(prior["memlane.sqlite-wal"]) == 0 {
		t.Fatal("committed WAL fixture absent")
	}
	hold.Rollback()
	reader.Close()
	db.Close()
	// Put back the captured committed files after all SQL connections close.
	// The restore manager operates on these actual bytes, with no live owner.
	for name, data := range prior {
		if err = os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(root, "service-state.json"), []byte(`{"stopped":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	return root, id, source, prior
}
func capturedPrepare(data []byte, calls *int) PrepareSnapshot {
	return func(context.Context, []byte, string) ([]byte, records.RestoreStats, error) {
		*calls++
		return data, records.RestoreStats{Memories: 1}, nil
	}
}
func assertRestoreFiles(t *testing.T, root, op string, prepared []byte, prior map[string][]byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "memlane.sqlite"))
	if err != nil || !bytes.Equal(data, prepared) {
		t.Fatal("installed bytes differ", err)
	}
	for name, want := range prior {
		got, e := os.ReadFile(filepath.Join(root, "backups/restores", op, "previous-"+name))
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("pre-restore archive changed", name, e)
		}
		if name != "memlane.sqlite" {
			if _, e = os.Stat(filepath.Join(root, name)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("old sidecar survived", name, e)
			}
		}
	}
	if _, err = os.Stat(filepath.Join(root, restorePointer)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pending pointer survived completed restore", err)
	}
}

func TestRestoreIOJournalInterruptionsReplayAndArchivedWAL(t *testing.T) {
	for _, stage := range []string{"archive_published", "journal_persisted", "main_replaced", "sidecars_removed", "receipt_persisted", "stop_state_persisted"} {
		t.Run(stage, func(t *testing.T) {
			root, backup, prepared, prior := restoreIOFixture(t)
			op := uuid.NewString()
			fault := errors.New("explicit restore interruption: " + stage)
			calls := 0
			m := RestoreManager{Root: root, checkpoint: func(point string) error {
				if point == stage {
					return fault
				}
				return nil
			}}
			if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); !errors.Is(err, fault) {
				t.Fatal("interruption reported success", err)
			}
			m.checkpoint = nil
			r, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls))
			if err != nil || r.ID != op || r.State != "restored_stopped" || calls != 1 {
				t.Fatal("restore replay failed or re-prepared source", r, err, calls)
			}
			assertRestoreFiles(t, root, op, prepared, prior)
			// Subsequent explicit service writes must survive replay of this ID.
			db, err := sql.Open("sqlite", filepath.Join(root, "memlane.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`UPDATE original SET text='synthetic later v3'`)
			if err != nil {
				t.Fatal(err)
			}
			db.Close()
			later, err := os.ReadFile(filepath.Join(root, "memlane.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(root, "memlane.sqlite"))
			if err != nil || !bytes.Equal(later, after) || calls != 1 {
				t.Fatal("completed replay switched twice", err, calls)
			}
		})
	}
}

func TestRestoreIONegativeOwnersStopAndUnknownCurrentFailClosed(t *testing.T) {
	root, backup, prepared, prior := restoreIOFixture(t)
	op := uuid.NewString()
	calls := 0
	m := RestoreManager{Root: root}
	for _, name := range []string{".service.lock", "memlane.sqlite.owner.lock"} {
		f, err := os.OpenFile(filepath.Join(root, name), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		_, err = m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls))
		f.Close()
		if err == nil || !strings.Contains(err.Error(), "owner still active") || calls != 0 {
			t.Fatal("live owner accepted", name, err, calls)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "service-state.json"), []byte(`{"stopped":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err == nil || !strings.Contains(err.Error(), "explicitly stopped") || calls != 0 {
		t.Fatal("unstopped service accepted", err)
	}
	os.WriteFile(filepath.Join(root, "service-state.json"), []byte(`{"stopped":true}`), 0600)
	fault := errors.New("explicit journal stop")
	m.checkpoint = func(stage string) error {
		if stage == "journal_persisted" {
			return fault
		}
		return nil
	}
	if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), prior["memlane.sqlite"]...)
	unknown[len(unknown)-1] ^= 1
	if err := os.WriteFile(filepath.Join(root, "memlane.sqlite"), unknown, 0600); err != nil {
		t.Fatal(err)
	}
	m.checkpoint = nil
	if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err == nil || !strings.Contains(err.Error(), "changed outside pending restore") {
		t.Fatal("unknown current database replaced", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "memlane.sqlite"))
	if err != nil || !bytes.Equal(after, unknown) {
		t.Fatal("fail closed changed unknown file", err)
	}
	if err = os.WriteFile(filepath.Join(root, "memlane.sqlite"), prior["memlane.sqlite"], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err != nil {
		t.Fatal("recovered identity failed later restore", err)
	}
	assertRestoreFiles(t, root, op, prepared, prior)
}

func TestRestoreIODirectoryFailureRetryAndCancelBeforeIntent(t *testing.T) {
	t.Run("parent_sync_retry", func(t *testing.T) {
		root, backup, prepared, prior := restoreIOFixture(t)
		op, calls := uuid.NewString(), 0
		fault := errors.New("explicit backups directory sync failure")
		fail := true
		attempts := 0
		m := RestoreManager{Root: root, syncDir: func(dir *os.Root) error {
			if _, err := dir.Lstat("restores"); err == nil {
				attempts++
				if fail {
					return fault
				}
			}
			return syncDirectory(dir)
		}}
		for i := 0; i < 2; i++ {
			if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); !errors.Is(err, fault) {
				t.Fatal("retry skipped failed parent sync", err)
			}
			if _, err := os.Stat(filepath.Join(root, restorePointer)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("intent published before archive durability", err)
			}
		}
		if attempts != 2 || calls != 1 {
			t.Fatal("retry did not resync original archive", attempts, calls)
		}
		fail = false
		if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err != nil {
			t.Fatal(err)
		}
		assertRestoreFiles(t, root, op, prepared, prior)
	})
	t.Run("cancel_after_archive", func(t *testing.T) {
		root, backup, prepared, prior := restoreIOFixture(t)
		op, calls := uuid.NewString(), 0
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m := RestoreManager{Root: root, checkpoint: func(stage string) error {
			if stage == "archive_published" {
				cancel()
			}
			return nil
		}}
		if _, err := m.Restore(ctx, backup, op, capturedPrepare(prepared, &calls)); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-intent cancellation ignored", err)
		}
		if _, err := os.Stat(filepath.Join(root, restorePointer)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cancelled operation published intent", err)
		}
		for name, want := range prior {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("pre-intent cancel changed current files", name, err)
			}
		}
		m.checkpoint = nil
		if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err != nil {
			t.Fatal("explicit retry failed after cancellation", err)
		}
		assertRestoreFiles(t, root, op, prepared, prior)
	})
	t.Run("stop_before_intent_clear", func(t *testing.T) {
		root, backup, prepared, _ := restoreIOFixture(t)
		op, calls := uuid.NewString(), 0
		fault := errors.New("explicit stop durability crash position")
		m := RestoreManager{Root: root, checkpoint: func(stage string) error {
			if stage == "receipt_persisted" {
				// Model an explicit Resume preceding startup recovery.
				return os.WriteFile(filepath.Join(root, "service-state.json"), []byte(`{"stopped":false,"stop_generation":4}`), 0600)
			}
			if stage == "stop_state_persisted" {
				return fault
			}
			return nil
		}}
		if _, err := m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); !errors.Is(err, fault) {
			t.Fatal(err)
		}
		bytes, err := os.ReadFile(filepath.Join(root, "service-state.json"))
		if err != nil {
			t.Fatal(err)
		}
		var state records.ServiceState
		if err = json.Unmarshal(bytes, &state); err != nil || !state.Stopped || state.StopGeneration != 5 {
			t.Fatal("intent cleanup preceded stop durability", state, err)
		}
		if _, err = os.Stat(filepath.Join(root, restorePointer)); err != nil {
			t.Fatal("fault cleared persistent intent", err)
		}
		m.checkpoint = nil
		if _, err = m.Restore(context.Background(), backup, op, capturedPrepare(prepared, &calls)); err != nil {
			t.Fatal(err)
		}
	})
}
