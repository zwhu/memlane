package recoveryfiles

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Real SQLite bytes; only the directory fsync error below is explicitly injected.
func sqliteFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE original(id INTEGER PRIMARY KEY,text TEXT);INSERT INTO original VALUES(1,'synthetic backup durability fixture');PRAGMA user_version=13;`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestBackupReplayRetriesFailedDirectoryDurability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	data := sqliteFixture(t)
	id := uuid.NewString()
	fault := errors.New("explicit directory fsync fault")
	fail := true
	captures := 0
	m := Manager{Root: root, syncDir: func(dir *os.Root) error {
		if _, err := dir.Lstat(id); err == nil && fail {
			return fault
		}
		return syncDirectory(dir)
	}}
	source := func(context.Context) (int, []byte, error) { captures++; return 13, data, nil }
	if _, err := m.Create(ctx, id, source); !errors.Is(err, fault) {
		t.Fatal("durability failure reported successful", err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups/manual", id, "snapshot.sqlite")); err != nil {
		t.Fatal("published content lost during durability fault", err)
	}
	if _, err := m.Create(ctx, id, source); !errors.Is(err, fault) {
		t.Fatal("same-ID replay skipped fsync failure", err)
	}
	if captures != 1 {
		t.Fatal("replay recaptured changing data", captures)
	}
	fail = false
	r, err := m.Create(ctx, id, source)
	if err != nil {
		t.Fatal(err)
	}
	if captures != 1 || r.Verification != "verified" {
		t.Fatal(captures, r)
	}
}
func TestBackupCatalogueKeepsValidRecordsBesideMissingFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	data := sqliteFixture(t)
	m := Manager{Root: root}
	one, two := uuid.NewString(), uuid.NewString()
	for _, id := range []string{one, two} {
		if _, err := m.Create(ctx, id, func(context.Context) (int, []byte, error) { return 13, data, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "backups/manual", one, "snapshot.sqlite")); err != nil {
		t.Fatal(err)
	}
	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, r := range list {
		states[r.ID] = r.Verification
	}
	if len(list) != 2 || states[one] != "invalid" || states[two] != "unverified" {
		t.Fatal(list)
	}
	if _, err = m.Verify(ctx, one); err == nil {
		t.Fatal("missing snapshot verified")
	}
	if _, err = m.Verify(ctx, two); err != nil {
		t.Fatal("bad record blocked healthy backup", err)
	}
	if err = os.Remove(filepath.Join(root, ".vault-id")); err != nil {
		t.Fatal(err)
	}
	if _, err = m.List(ctx); err == nil {
		t.Fatal("missing vault with existing backups reported empty")
	}
	captures := 0
	if _, err = m.Create(ctx, uuid.NewString(), func(context.Context) (int, []byte, error) { captures++; return 13, data, nil }); err == nil {
		t.Fatal("lost vault identity silently rotated")
	}
	if captures != 0 {
		t.Fatal("missing vault reached source capture", captures)
	}
	if _, err = os.Lstat(filepath.Join(root, ".vault-id")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing vault identity was recreated", err)
	}
}

func TestBackupLateCancelAfterPublicationReportsCommittedReceipt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	data := sqliteFixture(t)
	id := uuid.NewString()
	manager := Manager{Root: root, syncDir: func(dir *os.Root) error {
		// Catalogue sync happens after rename, the publication commit point.
		cancel()
		return syncDirectory(dir)
	}}
	receipt, err := manager.Create(ctx, id, func(context.Context) (int, []byte, error) { return 13, data, nil })
	if err != nil || receipt.ID != id || receipt.Verification != "verified" {
		t.Fatal("late cancel concealed committed backup", receipt, err)
	}
	verified, err := (Manager{Root: root}).Verify(context.Background(), id)
	if err != nil || verified.Receipt.SHA256 != receipt.SHA256 {
		t.Fatal(verified.Receipt, err)
	}
}
