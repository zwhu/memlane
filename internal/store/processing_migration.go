package store

import "database/sql"

func migrateProcessing(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE processing_revisions(
 id TEXT PRIMARY KEY,mode TEXT NOT NULL CHECK(mode IN ('provider','agent','magpie')),
 spec TEXT NOT NULL,digest TEXT NOT NULL,created_ms INTEGER NOT NULL);
CREATE TRIGGER processing_revision_immutable_update BEFORE UPDATE ON processing_revisions
 BEGIN SELECT RAISE(ABORT,'processing revisions are immutable'); END;
CREATE TRIGGER processing_revision_immutable_delete BEFORE DELETE ON processing_revisions
 BEGIN SELECT RAISE(ABORT,'processing revisions are immutable'); END;
CREATE TABLE processing_saved(mode TEXT PRIMARY KEY,revision TEXT REFERENCES processing_revisions(id));
INSERT INTO processing_saved(mode) VALUES('provider'),('agent'),('magpie');
CREATE TABLE processing_current(
 id INTEGER PRIMARY KEY CHECK(id=1),generation INTEGER NOT NULL DEFAULT 0,
 active INTEGER NOT NULL DEFAULT 0,
 revision TEXT REFERENCES processing_revisions(id),changed_ms INTEGER NOT NULL DEFAULT 0);
INSERT INTO processing_current(id) VALUES(1);
CREATE TABLE processing_validations(
 id TEXT PRIMARY KEY,revision TEXT NOT NULL REFERENCES processing_revisions(id),attempt INTEGER NOT NULL,
 dependency_digest TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('validating','succeeded','failed','cancelled','superseded')),
 cancel_requested INTEGER NOT NULL DEFAULT 0,receipt_digest TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',
 started_ms INTEGER NOT NULL,finished_ms INTEGER NOT NULL DEFAULT 0,UNIQUE(revision,attempt));
CREATE TABLE processing_operations(id TEXT PRIMARY KEY,digest TEXT NOT NULL,receipt TEXT NOT NULL);
PRAGMA user_version=17;`)
	return err
}
