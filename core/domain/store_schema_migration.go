package domain

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateStoreSchema(ctx context.Context, tx *sql.Tx, version int) error {
	if version < 3 {
		for _, table := range []struct {
			name    string
			columns []string
		}{
			{"projects", []string{"auto_capture_reason", "auto_capture_actor"}},
			{"capture_inbox", []string{"last_error_at"}},
		} {
			existing, err := queryList(ctx, tx, func(row scanner) (string, error) {
				var name string
				var cid, required, primaryKey int
				var fieldType string
				var defaultValue sql.NullString
				err := row.Scan(&cid, &name, &fieldType, &required, &defaultValue, &primaryKey)
				return name, err
			}, "PRAGMA table_info("+table.name+")")
			if err != nil {
				return fmt.Errorf("inspect %s columns: %w", table.name, err)
			}
			present := make(map[string]bool, len(existing))
			for _, column := range existing {
				present[column] = true
			}
			for _, column := range table.columns {
				if present[column] {
					continue
				}
				if err := execSQL(ctx, tx, "ALTER TABLE "+table.name+" ADD COLUMN "+column+" TEXT NOT NULL DEFAULT ''"); err != nil {
					return fmt.Errorf("migrate %s.%s: %w", table.name, column, err)
				}
			}
		}
	}
	if version < 4 {
		if err := execSQL(ctx, tx, sessionRelationsDDL); err != nil {
			return fmt.Errorf("migrate session_relations: %w", err)
		}
	}
	if version < 5 {
		if err := execSQL(ctx, tx, sessionFlushDDL); err != nil {
			return fmt.Errorf("migrate session_flush_jobs: %w", err)
		}
		if err := execSQL(ctx, tx, captureInboxV5IndexesDDL); err != nil {
			return fmt.Errorf("migrate capture_inbox indexes: %w", err)
		}
	}
	if version < 6 {
		if err := migrateCaptureTablesToV6(ctx, tx); err != nil {
			return err
		}
	}
	if version < 7 {
		if err := migrateSessionFlushToV7(ctx, tx); err != nil {
			return err
		}
	}
	return nil
}

func migrateSessionFlushToV7(ctx context.Context, tx *sql.Tx) error {
	existing, err := queryList(ctx, tx, func(row scanner) (string, error) {
		var name string
		var cid, required, primaryKey int
		var fieldType string
		var defaultValue sql.NullString
		err := row.Scan(&cid, &name, &fieldType, &required, &defaultValue, &primaryKey)
		return name, err
	}, "PRAGMA table_info(session_flush_jobs)")
	if err != nil {
		return fmt.Errorf("inspect session_flush_jobs columns for v7: %w", err)
	}
	present := make(map[string]bool, len(existing))
	for _, col := range existing {
		present[col] = true
	}
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"ready_at", "TEXT NOT NULL DEFAULT ''"},
		{"first_queued_at", "TEXT NOT NULL DEFAULT ''"},
		{"pending_qa_count", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if present[col.name] {
			continue
		}
		if err := execSQL(ctx, tx, "ALTER TABLE session_flush_jobs ADD COLUMN "+col.name+" "+col.ddl); err != nil {
			return fmt.Errorf("migrate session_flush_jobs.%s: %w", col.name, err)
		}
	}
	return execSQL(ctx, tx, `CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_ready ON session_flush_jobs(status,ready_at,updated_at)`)
}

func migrateCaptureTablesToV6(ctx context.Context, tx *sql.Tx) error {
	const inboxRebuildDDL = `
CREATE TABLE capture_inbox_v6 (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, dataset_id TEXT NOT NULL, agent_id TEXT NOT NULL, session_id TEXT NOT NULL,
 message_id TEXT NOT NULL, capture_key TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, payload_json TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','processing','succeeded','failed','cancelled_project_removed')),
 last_error TEXT NOT NULL DEFAULT '', last_error_at TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO capture_inbox_v6 (id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at)
 SELECT id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at
 FROM capture_inbox;
DROP TABLE capture_inbox;
ALTER TABLE capture_inbox_v6 RENAME TO capture_inbox;
CREATE INDEX IF NOT EXISTS idx_capture_inbox_status_updated ON capture_inbox(status,updated_at);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_project_status ON capture_inbox(project_id,status);
CREATE INDEX IF NOT EXISTS idx_capture_inbox_claim ON capture_inbox(status,created_at);
` + captureInboxV5IndexesDDL
	if err := execSQL(ctx, tx, inboxRebuildDDL); err != nil {
		return fmt.Errorf("migrate capture_inbox to v6: %w", err)
	}

	flushCols, err := queryList(ctx, tx, func(row scanner) (string, error) {
		var name string
		var cid, required, primaryKey int
		var fieldType string
		var defaultValue sql.NullString
		err := row.Scan(&cid, &name, &fieldType, &required, &defaultValue, &primaryKey)
		return name, err
	}, "PRAGMA table_info(session_flush_jobs)")
	if err != nil {
		return fmt.Errorf("inspect session_flush_jobs columns: %w", err)
	}
	hasFlushLastErrorAt := false
	for _, col := range flushCols {
		if col == "last_error_at" {
			hasFlushLastErrorAt = true
			break
		}
	}
	flushLastErrorAtExpr := "''"
	if hasFlushLastErrorAt {
		flushLastErrorAtExpr = "last_error_at"
	}

	flushRebuildDDL := `
CREATE TABLE session_flush_jobs_v6 (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, dataset_id TEXT NOT NULL, session_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','processing','succeeded','failed','cancelled_project_removed')),
 last_error TEXT NOT NULL DEFAULT '', last_error_at TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0, rerun_requested INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(dataset_id,session_id)
);
INSERT INTO session_flush_jobs_v6 (id,project_id,dataset_id,session_id,status,last_error,last_error_at,attempts,rerun_requested,created_at,updated_at)
 SELECT id,project_id,dataset_id,session_id,status,last_error,` + flushLastErrorAtExpr + `,attempts,rerun_requested,created_at,updated_at
 FROM session_flush_jobs;
DROP TABLE session_flush_jobs;
ALTER TABLE session_flush_jobs_v6 RENAME TO session_flush_jobs;
CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_status_updated ON session_flush_jobs(status,updated_at);
CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_project_status ON session_flush_jobs(project_id,status);
CREATE INDEX IF NOT EXISTS idx_session_flush_jobs_claim ON session_flush_jobs(status,created_at);
`
	if err := execSQL(ctx, tx, flushRebuildDDL); err != nil {
		return fmt.Errorf("migrate session_flush_jobs to v6: %w", err)
	}
	if err := execSQL(ctx, tx, `
CREATE TABLE IF NOT EXISTS removed_session_owners (
 dataset_id TEXT NOT NULL, session_id TEXT NOT NULL, removed_at TEXT NOT NULL,
 PRIMARY KEY(dataset_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_removed_session_owners_session ON removed_session_owners(session_id);
`); err != nil {
		return fmt.Errorf("migrate removed_session_owners to v6: %w", err)
	}
	if err := normalizeStoredProjectUUIDsTx(ctx, tx); err != nil {
		return fmt.Errorf("normalize project UUIDs to v6: %w", err)
	}
	return nil
}

func normalizeStoredProjectUUIDsTx(ctx context.Context, tx *sql.Tx) error {
	type projRow struct {
		id, ds, codeDS string
	}
	projects, err := queryList(ctx, tx, func(row scanner) (projRow, error) {
		var r projRow
		err := row.Scan(&r.id, &r.ds, &r.codeDS)
		return r, err
	}, `SELECT id, dataset_id, code_dataset_id FROM projects`)
	if err != nil {
		return err
	}
	for _, r := range projects {
		newDS, newCode := CanonicalUUID(r.ds), CanonicalUUID(r.codeDS)
		if newDS != r.ds || newCode != r.codeDS {
			if err := execSQL(ctx, tx, `UPDATE projects SET dataset_id=?, code_dataset_id=? WHERE id=?`, newDS, newCode, r.id); err != nil {
				return err
			}
		}
	}
	type commonRow struct {
		singleton int
		ds        string
	}
	commons, err := queryList(ctx, tx, func(row scanner) (commonRow, error) {
		var r commonRow
		err := row.Scan(&r.singleton, &r.ds)
		return r, err
	}, `SELECT singleton, dataset_id FROM common_config`)
	if err != nil {
		return err
	}
	for _, r := range commons {
		if newDS := CanonicalUUID(r.ds); newDS != r.ds {
			if err := execSQL(ctx, tx, `UPDATE common_config SET dataset_id=? WHERE singleton=?`, newDS, r.singleton); err != nil {
				return err
			}
		}
	}
	return nil
}
