package store

import "database/sql"

func migrateOrganizationRecovery(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE organization_recovery(
 task TEXT PRIMARY KEY REFERENCES organization_tasks(id),created_ms INTEGER NOT NULL,stop_generation INTEGER NOT NULL DEFAULT 0,extra_limit INTEGER NOT NULL CHECK(extra_limit BETWEEN 1 AND 2),used INTEGER NOT NULL DEFAULT 0,
 deadline_ms INTEGER NOT NULL,next_ms INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','waiting','done','exhausted','expired','cancelled')),
 reason TEXT NOT NULL DEFAULT '');
 PRAGMA user_version=9;`)
	return err
}
