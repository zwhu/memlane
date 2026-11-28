package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

const SchemaVersion = 7
const utcNSLayout = "2006-01-02T15:04:05.000000000Z"
const utcMicroLayout = "2006-01-02T15:04:05.000000+00:00"

var (
	ErrNotFound              = errors.New("not found")
	ErrConflict              = errors.New("conflict")
	ErrInvalidArgument       = errors.New("invalid argument")
	ErrForbidden             = errors.New("forbidden")
	ErrInboxCapacityExceeded = errors.New("inbox capacity exceeded")
)

type Store struct {
	path string
	db   *sql.DB
	mu   sync.Mutex
}

const schemaMetaDDL = `
CREATE TABLE IF NOT EXISTS schema_meta (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 schema_version INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
`

const schemaDDL = schemaMetaDDL + registryDDL + provenanceDDL + recallDDL + readingDDL +
	captureDDL + driftDDL + generationDDL + profilesToolgraphDDL + catalogDDL + captureFilterDDL

func OpenStore(path string) (*Store, error) {
	db, err := OpenPrivateSQLite(path)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, db: db}
	ctx := context.Background()
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := execSQL(ctx, tx, schemaMetaDDL); err != nil {
			return err
		}
		var version int
		err := tx.QueryRow("SELECT schema_version FROM schema_meta WHERE singleton=1").Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if version > SchemaVersion {
			return fmt.Errorf("schema version %d exceeds supported version %d: %w", version, SchemaVersion, ErrConflict)
		}
		if err := execSQL(ctx, tx, schemaDDL); err != nil {
			return err
		}
		if version > 0 && version < SchemaVersion {
			if err := migrateStoreSchema(ctx, tx, version); err != nil {
				return err
			}
		}
		if err := execSQL(ctx, tx, sessionFlushV7IndexesDDL); err != nil {
			return err
		}
		now := NowUTCNS()
		if err := execSQL(ctx, tx, `INSERT INTO generation_state(singleton,updated_at) VALUES(1,?)
			ON CONFLICT(singleton) DO NOTHING`, now); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO schema_meta VALUES(1,?,?,?)
			ON CONFLICT(singleton) DO UPDATE SET schema_version=excluded.schema_version, updated_at=excluded.updated_at
			WHERE schema_meta.schema_version <> excluded.schema_version`, SchemaVersion, now, now)
		return err
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("initialize domain store: %w", err), db.Close())
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Path() string { return s.path }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) SchemaVersion() (int, error) {
	var version int
	err := s.db.QueryRow("SELECT schema_version FROM schema_meta WHERE singleton=1").Scan(&version)
	return version, rowError(err)
}

func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transact(ctx, nil, fn)
}

func (s *Store) readTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.transact(ctx, &sql.TxOptions{ReadOnly: true}, fn)
}

func (s *Store) transact(ctx context.Context, options *sql.TxOptions, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func NowUTCNS() string                 { return FormatUTCNS(time.Now()) }
func NowUTCMicro() string              { return FormatUTCMicro(time.Now()) }
func FormatUTCMicro(t time.Time) string { return t.UTC().Format(utcMicroLayout) }
func FormatUTCNS(t time.Time) string   { return t.UTC().Format(utcNSLayout) }

func ParseUTCMicro(s string) (time.Time, bool) {
	t, err := time.Parse(utcMicroLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func IsValidUTCNS(s string) bool {
	t, err := time.Parse(utcNSLayout, s)
	return err == nil && t.Year() > 0 && FormatUTCNS(t) == s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(v int) bool               { return v != 0 }
func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

type scanner interface{ Scan(...any) error }

type sqlReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func rowError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func insertError(err error) error {
	var constraint *sqlite.Error
	if errors.As(err, &constraint) {
		switch constraint.Code() {
		case 1555, 2067: // SQLITE_CONSTRAINT_PRIMARYKEY, SQLITE_CONSTRAINT_UNIQUE
			return errors.Join(ErrConflict, err)
		}
	}
	return err
}

func execSQL(ctx context.Context, db sqlExecer, query string, args ...any) error {
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

func queryList[T any](ctx context.Context, db sqlReader, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func defaultText(value, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}

func requireIDs(values ...string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("missing identifier: %w", ErrInvalidArgument)
		}
	}
	return nil
}

func pageLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return limit
}

const registryDDL = `
CREATE TABLE IF NOT EXISTS projects (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, dataset_id TEXT NOT NULL DEFAULT '', dataset_name TEXT NOT NULL DEFAULT '',
 code_dataset_id TEXT NOT NULL DEFAULT '', code_enabled INTEGER NOT NULL DEFAULT 0, code_configured INTEGER NOT NULL DEFAULT 0,
 auto_capture INTEGER NOT NULL DEFAULT 1, auto_capture_version INTEGER NOT NULL DEFAULT 1, auto_capture_updated_at TEXT NOT NULL DEFAULT '',
 auto_capture_reason TEXT NOT NULL DEFAULT '', auto_capture_actor TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', desired_revision INTEGER NOT NULL DEFAULT 0,
 operation_id TEXT NOT NULL DEFAULT '', import_acl INTEGER NOT NULL DEFAULT 0, tool_graph_json TEXT NOT NULL DEFAULT '{}',
 applied_json TEXT NOT NULL DEFAULT '{}', extra_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS project_aliases (
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 alias_path TEXT NOT NULL, PRIMARY KEY(project_id,alias_path)
);
CREATE INDEX IF NOT EXISTS idx_project_aliases_path ON project_aliases(alias_path);
CREATE TABLE IF NOT EXISTS agents (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, principal_id TEXT NOT NULL DEFAULT '',
 key_file TEXT NOT NULL DEFAULT '', control_token_file TEXT NOT NULL DEFAULT '', default_access TEXT NOT NULL DEFAULT 'none',
 host_profile TEXT NOT NULL DEFAULT '', host_profile_fingerprint TEXT NOT NULL DEFAULT '', capabilities_json TEXT NOT NULL DEFAULT '{}',
 entrypoints_json TEXT NOT NULL DEFAULT '[]', installation_json TEXT NOT NULL DEFAULT '{}', archived INTEGER NOT NULL DEFAULT 0,
 retired_at TEXT NOT NULL DEFAULT '', extra_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS project_acl_overrides (
 target_kind TEXT NOT NULL CHECK(target_kind IN ('project','common')), target_id TEXT NOT NULL, agent_id TEXT NOT NULL,
 access TEXT CHECK(access IS NULL OR access IN ('none','read','maintain')), capture INTEGER CHECK(capture IS NULL OR capture IN (0,1)),
 auto_process INTEGER CHECK(auto_process IS NULL OR auto_process IN (0,1)), PRIMARY KEY(target_kind,target_id,agent_id)
);
CREATE TABLE IF NOT EXISTS defaults (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), access TEXT NOT NULL DEFAULT 'maintain' CHECK(access IN ('none','read','maintain')),
 capture INTEGER NOT NULL DEFAULT 1 CHECK(capture IN (0,1)), auto_process INTEGER NOT NULL DEFAULT 1 CHECK(auto_process IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 0, service_namespace TEXT NOT NULL DEFAULT '', runtime_catalog_imported INTEGER NOT NULL DEFAULT 0,
 reserved_datasets_json TEXT NOT NULL DEFAULT '[]', tool_graph_json TEXT NOT NULL DEFAULT '{}',
 host_targets_json TEXT NOT NULL DEFAULT '{}', extra_json TEXT NOT NULL DEFAULT '{}'
);
INSERT INTO defaults(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING;
CREATE TABLE IF NOT EXISTS common_config (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), id TEXT NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'common',
 dataset_id TEXT NOT NULL DEFAULT '', dataset_name TEXT NOT NULL DEFAULT '', code_enabled INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT '', desired_revision INTEGER NOT NULL DEFAULT 0, operation_id TEXT NOT NULL DEFAULT '',
 applied_json TEXT NOT NULL DEFAULT '{}', extra_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS session_bindings (
 session_key TEXT PRIMARY KEY, project_id TEXT NOT NULL, updated_at REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_bindings_project ON session_bindings(project_id);
CREATE TABLE IF NOT EXISTS removed_session_owners (
 dataset_id TEXT NOT NULL, session_id TEXT NOT NULL, removed_at TEXT NOT NULL,
 PRIMARY KEY(dataset_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_removed_session_owners_session ON removed_session_owners(session_id);
CREATE TABLE IF NOT EXISTS registry_connection_operations (
 operation_id TEXT PRIMARY KEY, payload_json TEXT NOT NULL, updated_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS registry_connection_receipts (
 agent_id TEXT PRIMARY KEY, payload_json TEXT NOT NULL, updated_at REAL NOT NULL
);
`
