package store

import "database/sql"

func migrateProjectTasks(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE project_bindings(
 project TEXT PRIMARY KEY REFERENCES scopes(id),connection TEXT REFERENCES connections(id),
 generation INTEGER NOT NULL DEFAULT 0,revision INTEGER NOT NULL CHECK(revision>=1));
CREATE TABLE project_tasks(
 id TEXT PRIMARY KEY,operation_id TEXT NOT NULL UNIQUE,request_digest TEXT NOT NULL,
 project TEXT NOT NULL REFERENCES scopes(id),snapshot TEXT NOT NULL,
 attempt INTEGER NOT NULL DEFAULT 1 CHECK(attempt>=1),
 state TEXT NOT NULL CHECK(state IN ('queued','starting','organizing','saving','indexing','completed','failed','cancelling','cancelled','interrupted')),
 cancel_requested INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',created_ms INTEGER NOT NULL);
CREATE UNIQUE INDEX project_task_active ON project_tasks(project)
 WHERE state IN ('queued','starting','organizing','saving','indexing','cancelling');
CREATE TRIGGER project_task_scope_immutable BEFORE UPDATE OF project,snapshot,operation_id,request_digest ON project_tasks
 BEGIN SELECT RAISE(ABORT,'project task scope is immutable'); END;
PRAGMA user_version=18;`)
	return err
}
