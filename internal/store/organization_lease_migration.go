package store

import "database/sql"

func migrateOrganizationLeases(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE organization_leases(
 task TEXT NOT NULL,attempt INTEGER NOT NULL,owner TEXT NOT NULL,
 expires_ms INTEGER NOT NULL,process_pid INTEGER NOT NULL DEFAULT 0,
 released INTEGER NOT NULL DEFAULT 0 CHECK(released IN (0,1)),
 process_reaped INTEGER NOT NULL DEFAULT 0 CHECK(process_reaped IN (0,1)),
 group_gone INTEGER NOT NULL DEFAULT 0 CHECK(group_gone IN (0,1)),
 PRIMARY KEY(task,attempt),FOREIGN KEY(task,attempt) REFERENCES organization_attempts(task,attempt));
 PRAGMA user_version=8;`)
	return err
}
