package daemon

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStorageFailureRealSQLiteFullIsClassifiedWithoutFillingHost(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "bounded.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"PRAGMA page_size=512", "PRAGMA max_page_count=16", "CREATE TABLE payload(body BLOB)"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec("INSERT INTO payload VALUES(zeroblob(131072))")
	if err == nil || storageResponseCode(err) != "storage_full" {
		t.Fatal("real bounded SQLite FULL was not identified", err)
	}
}
