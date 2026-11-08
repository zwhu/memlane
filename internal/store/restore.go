package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
)

func migrateConnectionRetirement(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE connection_retirements(connection TEXT PRIMARY KEY REFERENCES connections(id),restore TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);PRAGMA user_version=14;`)
	return err
}

type RestoreStats = records.RestoreStats

// PrepareRestoredSnapshot changes only an isolated in-memory copy. The verified
// source backup is the pre-migration snapshot; no production file is opened here.
func PrepareRestoredSnapshot(ctx context.Context, data []byte, operation string) ([]byte, RestoreStats, error) {
	var stats RestoreStats
	if parsed, err := uuid.Parse(operation); err != nil || parsed.String() != operation {
		return nil, stats, errors.New("canonical restore UUID required")
	}
	if len(data) < 100 || int64(len(data)) > recoveryfiles.MaxSnapshotBytes {
		return nil, stats, errors.New("restore snapshot exceeds supported limit")
	}
	// Online backup Serialize includes every committed page but can retain a
	// WAL-mode header. SQLite Deserialize cannot open WAL sidecars for an
	// in-memory database; normalize only this private copy to rollback mode.
	if string(data[:16]) != "SQLite format 3\x00" {
		return nil, stats, errors.New("restore snapshot header invalid")
	}
	image := append([]byte(nil), data...)
	image[18], image[19] = 1, 1
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, stats, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, stats, err
	}
	err = conn.Raw(func(raw any) error {
		loader, ok := raw.(interface{ Deserialize([]byte) error })
		if !ok {
			return errors.New("SQLite snapshot loader unavailable")
		}
		return loader.Deserialize(image)
	})
	conn.Close()
	if err != nil {
		return nil, stats, err
	}
	var version int
	var integrity string
	if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return nil, stats, err
	}
	if version < 13 || version > SchemaVersion {
		return nil, stats, errors.New("restore supports manual schema13 or schema14 snapshots")
	}
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return nil, stats, errors.New("restore snapshot integrity invalid")
	}
	if err = migrate(db, true); err != nil {
		return nil, stats, err
	}
	if _, err = db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return nil, stats, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, stats, err
	}
	defer tx.Rollback()
	for _, table := range []string{"connections", "organization_plans", "code_worktrees"} {
		var invalid int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE generation<1 OR generation>=9223372036854775807`).Scan(&invalid); err != nil {
			return nil, stats, err
		}
		if invalid > 0 {
			return nil, stats, errors.New("restored authorization generation invalid")
		}
	}
	for query, target := range map[string]*int{
		`SELECT count(*) FROM connections`:        &stats.Connections,
		`SELECT count(*) FROM organization_plans`: &stats.Plans,
		`SELECT count(*) FROM code_worktrees`:     &stats.CodeWorktrees,
		`SELECT count(*) FROM memories m WHERE m.state='active' AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')`: &stats.Memories,
	} {
		if err = tx.QueryRowContext(ctx, query).Scan(target); err != nil {
			return nil, stats, err
		}
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO connection_retirements(connection,restore) SELECT id,? FROM connections WHERE true ON CONFLICT(connection) DO NOTHING`, []any{operation}},
		{`UPDATE connections SET enabled=0,generation=generation+1,last_seen=''`, nil},
		{`UPDATE organization_plans SET enabled=0,generation=generation+1,next_run_ms=0`, nil},
		{`UPDATE organization_recovery SET state='cancelled',reason='backup_restored',next_ms=0 WHERE state IN ('active','waiting')`, nil},
		{`UPDATE organization_tasks SET state='interrupted',cancelled=1,error_code='backup_restored' WHERE state IN ('queued','running','applying')`, nil},
		{`UPDATE organization_attempts SET state='interrupted' WHERE state IN ('queued','running','applying')`, nil},
		// PIDs from a past snapshot are historical facts, not current ownership.
		// Retain original execution JSON with an explicit restore annotation;
		// reset only the operational lease's PID instead of claiming it was reaped.
		{`UPDATE organization_leases SET released=1,expires_ms=0,process_pid=0`, nil},
		{`UPDATE organization_items SET state='expired',error_code='backup_restored' WHERE state IN ('pending','failed')`, nil},
		{`UPDATE organization_reviews SET state='expired',error_code='backup_restored' WHERE state='review_pending'`, nil},
		{`UPDATE global_experience_reviews SET state='expired',error_code='backup_restored' WHERE state='review_pending'`, nil},
		{`UPDATE initialization_batches SET state='expired',error_code='backup_restored' WHERE state='pending'`, nil},
		{`UPDATE merge_tasks SET state='interrupted',cancelled=1,error_code='backup_restored' WHERE state IN ('queued','running')`, nil},
		{`UPDATE merge_attempts SET state='interrupted' WHERE state IN ('queued','running')`, nil},
		{`UPDATE merge_attempts SET execution=json_set(execution,'$.backup_restored',?) WHERE json_valid(execution) AND json_type(execution)='object'`, []any{operation}},
		{`UPDATE merge_proposals SET state='expired' WHERE state='pending'`, nil},
		{`UPDATE code_worktrees SET enabled=0,generation=generation+1,state='disabled',observed_digest='',error='backup_restored'`, nil},
		{`UPDATE code_jobs SET state='interrupted',error='backup_restored',finished_ms=? WHERE state IN ('queued','running')`, []any{time.Now().UnixMilli()}},
		{`UPDATE index_rebuild_items SET state='expired',error='backup_restored' WHERE state IN ('queued','failed')`, nil},
		{`UPDATE memories SET indexed_revision=0,generation=''`, nil},
		{`UPDATE jobs SET state='superseded' WHERE state IN ('pending','failed','indexing')`, nil},
		{`INSERT INTO jobs(memory,revision,state,error) SELECT m.id,m.current_revision,'pending','' FROM memories m WHERE m.state='active' AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale') ON CONFLICT(memory,revision) DO UPDATE SET state='pending',error=''`, nil},
	}
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return nil, stats, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, stats, err
	}
	s := &Store{DB: db, Schema: SchemaVersion}
	_, prepared, err := s.snapshot(ctx)
	return prepared, stats, err
}
