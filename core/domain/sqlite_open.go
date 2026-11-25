package domain

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	DefaultSQLiteJournalSizeLimit = 4 * 1024 * 1024
	DefaultSQLiteBusyTimeoutMS    = 5000
)

// OpenPrivateSQLite opens a private WAL database with one pooled connection.
func OpenPrivateSQLite(path string) (*sql.DB, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, errors.New("SQLite path is empty")
	}
	absPath, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("create SQLite directory: %w", err)
	}
	file, err := os.OpenFile(absPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create SQLite file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close pre-created SQLite file: %w", err)
	}
	if err := os.Chmod(absPath, 0o600); err != nil {
		return nil, fmt.Errorf("set SQLite file permissions: %w", err)
	}

	q := url.Values{"_pragma": {
		"busy_timeout=" + strconv.Itoa(DefaultSQLiteBusyTimeoutMS),
		"foreign_keys=ON",
		"journal_mode=WAL",
		"journal_size_limit=" + strconv.Itoa(DefaultSQLiteJournalSizeLimit),
		"synchronous=NORMAL",
	}}
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(absPath), RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize SQLite: %w", err), db.Close())
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		filename := absPath + suffix
		if err := os.Chmod(filename, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, errors.Join(fmt.Errorf("set permissions on %s: %w", filename, err), db.Close())
		}
	}
	return db, nil
}
