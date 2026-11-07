package store

import "database/sql"

func migrateSchedules(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE organization_plans(
 id TEXT PRIMARY KEY,generation INTEGER NOT NULL,project TEXT NOT NULL REFERENCES scopes(id),project_path TEXT NOT NULL,destinations TEXT NOT NULL,spec TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),anchor_ms INTEGER NOT NULL,next_run_ms INTEGER NOT NULL,
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
 CREATE UNIQUE INDEX organization_enabled_plan_project ON organization_plans(project) WHERE enabled=1;
 CREATE TABLE organization_plan_operations(id TEXT PRIMARY KEY,plan TEXT NOT NULL REFERENCES organization_plans(id),digest TEXT NOT NULL,receipt TEXT NOT NULL);
 CREATE TABLE organization_plan_runs(id TEXT PRIMARY KEY,plan TEXT NOT NULL REFERENCES organization_plans(id),generation INTEGER NOT NULL,due_ms INTEGER NOT NULL,state TEXT NOT NULL CHECK(state IN ('queued','skipped')),reason TEXT NOT NULL DEFAULT '',task TEXT REFERENCES organization_tasks(id),input_digest TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(plan,generation,due_ms));
 CREATE UNIQUE INDEX organization_plan_task ON organization_plan_runs(task) WHERE task IS NOT NULL;
 PRAGMA user_version=7;`)
	return err
}
