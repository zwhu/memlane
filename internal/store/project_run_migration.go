package store

import "database/sql"

func migrateProjectRuns(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE project_task_runs(
 task TEXT PRIMARY KEY REFERENCES project_tasks(id),attempt INTEGER NOT NULL,
 owner TEXT NOT NULL,expires_ms INTEGER NOT NULL,process_pid INTEGER NOT NULL DEFAULT 0,
 execution TEXT NOT NULL DEFAULT 'null',result_digest TEXT NOT NULL DEFAULT '',
 proposal TEXT NOT NULL DEFAULT 'null',receipt TEXT NOT NULL DEFAULT 'null',
 released INTEGER NOT NULL DEFAULT 0,group_gone INTEGER NOT NULL DEFAULT 0,
 backup_restored TEXT NOT NULL DEFAULT '');
CREATE TABLE project_task_sources(
 task TEXT NOT NULL REFERENCES project_tasks(id),source TEXT NOT NULL,payload TEXT NOT NULL,
 PRIMARY KEY(task,source));
CREATE TABLE project_task_items(
 task TEXT NOT NULL REFERENCES project_tasks(id),position INTEGER NOT NULL,payload TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','applied','skipped','failed','cancelled')),
 error_code TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT 'null',
 PRIMARY KEY(task,position));
CREATE TRIGGER project_source_snapshot_immutable BEFORE UPDATE ON project_task_sources
 BEGIN SELECT RAISE(ABORT,'project source snapshot is immutable'); END;
CREATE TRIGGER project_item_payload_immutable BEFORE UPDATE OF task,position,payload ON project_task_items
 BEGIN SELECT RAISE(ABORT,'project knowledge item is immutable'); END;
CREATE TRIGGER project_run_identity_immutable BEFORE UPDATE OF task,attempt,owner ON project_task_runs
 BEGIN SELECT RAISE(ABORT,'project run identity is immutable'); END;
PRAGMA user_version=19;`)
	return err
}
