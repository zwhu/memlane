package store

import "database/sql"

func migrateCode(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE code_worktrees(
 id TEXT PRIMARY KEY,project TEXT NOT NULL REFERENCES scopes(id),worktree TEXT NOT NULL,common_dir TEXT NOT NULL,
 dataset TEXT NOT NULL UNIQUE,enabled INTEGER NOT NULL,generation INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('disabled','queued','indexing','ready','stale','failed','interrupted','cancelled')),
 snapshot TEXT NOT NULL DEFAULT '',manifest TEXT NOT NULL DEFAULT 'null',receipt TEXT NOT NULL DEFAULT 'null',
 observed_digest TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',updated_ms INTEGER NOT NULL,
 UNIQUE(project,worktree));
CREATE TABLE code_jobs(
 id TEXT PRIMARY KEY,worktree_id TEXT NOT NULL REFERENCES code_worktrees(id),generation INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','failed','cancelled','interrupted')),
 manifest TEXT NOT NULL DEFAULT 'null',receipt TEXT NOT NULL DEFAULT 'null',error TEXT NOT NULL DEFAULT '',
 created_ms INTEGER NOT NULL,finished_ms INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX code_active_job ON code_jobs(worktree_id) WHERE state IN ('queued','running');
CREATE INDEX code_job_history ON code_jobs(worktree_id,created_ms,id);
CREATE TABLE code_actions(id TEXT PRIMARY KEY,digest TEXT NOT NULL,receipt TEXT NOT NULL);
PRAGMA user_version=12;`)
	return err
}
