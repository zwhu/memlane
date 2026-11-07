package store

import "database/sql"

// v5 leaves every v4 table intact; review items cannot enter the automatic table.
func migrateOrganizationReviews(tx *sql.Tx) error {
	_, err := tx.Exec(`
 CREATE TABLE organization_reviews(
 id TEXT PRIMARY KEY,task TEXT NOT NULL REFERENCES organization_tasks(id),position INTEGER NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('merge','overwrite','delete','move','duplicate','conflict')),
 digest TEXT NOT NULL,payload TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('review_pending','applied','rejected','expired','cancelled')),
 error_code TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT 'null',UNIQUE(task,position));
 CREATE TABLE organization_decisions(
 id TEXT PRIMARY KEY,item TEXT NOT NULL REFERENCES organization_reviews(id),digest TEXT NOT NULL,
 decision TEXT NOT NULL CHECK(decision IN ('confirm','reject')),receipt TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE scope_history(
 memory TEXT NOT NULL REFERENCES memories(id),revision INTEGER NOT NULL,previous_scope TEXT NOT NULL REFERENCES scopes(id),
 scope TEXT NOT NULL REFERENCES scopes(id),item TEXT NOT NULL REFERENCES organization_reviews(id),created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(memory,revision),FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision));
 PRAGMA user_version=5;`)
	return err
}
