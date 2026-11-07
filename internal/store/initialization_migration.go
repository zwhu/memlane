package store

import "database/sql"

func migrateInitialization(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE initialization_batches(
 id TEXT PRIMARY KEY,operation_id TEXT NOT NULL UNIQUE,request_digest TEXT NOT NULL,
 project TEXT NOT NULL REFERENCES scopes(id),connection TEXT NOT NULL REFERENCES connections(id),generation INTEGER NOT NULL,
 workspace TEXT NOT NULL,workspace_identity TEXT NOT NULL,snapshot TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','received','cancelled','expired')),error_code TEXT NOT NULL DEFAULT '',
 created_ms INTEGER NOT NULL,source_read_ms INTEGER NOT NULL DEFAULT 0,
 submit_id TEXT NOT NULL DEFAULT '',submit_digest TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT 'null');
CREATE INDEX initialization_project ON initialization_batches(project,created_ms,id);
PRAGMA user_version=11;`)
	return err
}
