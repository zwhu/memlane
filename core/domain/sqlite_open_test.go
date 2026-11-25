package domain

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func openTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := OpenPrivateSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func checkFileMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: permissions = %04o, want %04o", path, got, want)
	}
}

func TestOpenPrivateSQLite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "directory with spaces", "nested")
	path := filepath.Join(dir, "memory database.sqlite")
	db := openTestSQLite(t, path)
	checkFileMode(t, dir, 0o700)
	checkFileMode(t, path, 0o600)

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
	// Force the pool to open fresh connections when reading the pragmas.
	db.SetMaxIdleConns(0)
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	for _, tc := range []struct {
		name string
		want int
	}{
		{"journal_size_limit", 4194304},
		{"busy_timeout", 5000},
		{"foreign_keys", 1},
		{"synchronous", 1},
	} {
		var got int
		if err := db.QueryRow("PRAGMA " + tc.name).Scan(&got); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("CREATE VIRTUAL TABLE memories USING fts5(body)"); err != nil {
		t.Fatal(err)
	}
	const memory = "SQLite full text search"
	if _, err := db.Exec("INSERT INTO memories(body) VALUES (?)", memory); err != nil {
		t.Fatal(err)
	}
	var found string
	if err := db.QueryRow("SELECT body FROM memories WHERE memories MATCH ?", "sqlite").Scan(&found); err != nil {
		t.Fatal(err)
	}
	if found != memory {
		t.Errorf("MATCH returned %q, want %q", found, memory)
	}
	checkFileMode(t, path+"-wal", 0o600)
	checkFileMode(t, path+"-shm", 0o600)
}

func TestOpenPrivateSQLiteRejectsEmptyPath(t *testing.T) {
	for _, path := range []string{"", " \t\n"} {
		db, err := OpenPrivateSQLite(path)
		if db != nil {
			if closeErr := db.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if err == nil || db != nil {
			t.Errorf("OpenPrivateSQLite(%q) = (%v, %v), want nil and an error", path, db, err)
		}
	}
}

func TestOpenPrivateSQLiteTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	db, err := OpenPrivateSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	openTestSQLite(t, path)
	checkFileMode(t, path, 0o600)
}
