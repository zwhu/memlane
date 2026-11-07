package store

import "database/sql"

const SchemaVersion = 6

// These tables exist only for the shipped summary/tag flow. Lease and schedule
// policy are deliberately added by their own delivery slices.
func migrateOrganization(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE organization_tasks(
 id TEXT PRIMARY KEY,operation_id TEXT NOT NULL UNIQUE,request_digest TEXT NOT NULL,
 project TEXT NOT NULL REFERENCES scopes(id),state TEXT NOT NULL CHECK(state IN ('queued','running','applying','completed','partial_failed','failed','cancelled','interrupted')),
 attempt INTEGER NOT NULL DEFAULT 1,cancelled INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE organization_attempts(
 task TEXT NOT NULL REFERENCES organization_tasks(id),attempt INTEGER NOT NULL,state TEXT NOT NULL,
 kind TEXT NOT NULL DEFAULT 'model' CHECK(kind IN ('model','apply')),resume_id TEXT NOT NULL DEFAULT '',request_digest TEXT NOT NULL DEFAULT '',
 snapshot TEXT NOT NULL,execution TEXT NOT NULL DEFAULT 'null',PRIMARY KEY(task,attempt));
CREATE UNIQUE INDEX organization_resume ON organization_attempts(task,resume_id) WHERE resume_id!='';
CREATE TABLE organization_results(
 task TEXT NOT NULL,attempt INTEGER NOT NULL,id TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,payload TEXT NOT NULL,
 PRIMARY KEY(task,attempt),FOREIGN KEY(task,attempt) REFERENCES organization_attempts(task,attempt));
CREATE TABLE organization_items(
 id TEXT PRIMARY KEY,task TEXT NOT NULL REFERENCES organization_tasks(id),position INTEGER NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('summary','add_tags')),digest TEXT NOT NULL,payload TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','applied','expired','failed','cancelled')),error_code TEXT NOT NULL DEFAULT '',
 receipt TEXT NOT NULL DEFAULT 'null',UNIQUE(task,position));
CREATE TABLE organization_sources(
 memory TEXT NOT NULL,revision INTEGER NOT NULL,source_memory TEXT NOT NULL,source_revision INTEGER NOT NULL,
 project TEXT NOT NULL REFERENCES scopes(id),snapshot TEXT NOT NULL,quote TEXT NOT NULL,
 PRIMARY KEY(memory,revision,source_memory),
 FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision),FOREIGN KEY(source_memory,source_revision) REFERENCES versions(memory,revision));
CREATE INDEX organization_source_dependency ON organization_sources(source_memory,source_revision);
CREATE TABLE derived_memories(
 memory TEXT PRIMARY KEY REFERENCES memories(id),revision INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('valid','stale','user_confirmed')),
 FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision));
CREATE TABLE memory_tags(memory TEXT NOT NULL REFERENCES memories(id),tag TEXT NOT NULL,PRIMARY KEY(memory,tag));
CREATE TABLE tag_events(
 item TEXT NOT NULL REFERENCES organization_items(id),memory TEXT NOT NULL REFERENCES memories(id),revision INTEGER NOT NULL,
 tag TEXT NOT NULL,sources TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(item,tag));
CREATE TRIGGER organization_invalidate AFTER UPDATE OF current_revision,dataset,state ON memories
WHEN OLD.current_revision!=NEW.current_revision OR OLD.dataset!=NEW.dataset OR OLD.state!=NEW.state
BEGIN
 UPDATE derived_memories SET state='stale' WHERE state!='user_confirmed' AND memory IN (
  WITH RECURSIVE dependencies(memory) AS (
   SELECT p.memory FROM organization_sources p JOIN derived_memories d ON d.memory=p.memory AND d.revision=p.revision
    WHERE d.state!='user_confirmed' AND p.source_memory=NEW.id AND (p.source_revision!=NEW.current_revision OR NEW.state!='active' OR NEW.dataset!=(SELECT dataset FROM memories WHERE id=p.memory))
   UNION
   SELECT p.memory FROM organization_sources p JOIN derived_memories d ON d.memory=p.memory AND d.revision=p.revision JOIN dependencies x ON p.source_memory=x.memory WHERE d.state!='user_confirmed'
  ) SELECT memory FROM dependencies
 );
 UPDATE memories SET indexed_revision=0,generation='' WHERE id IN (SELECT memory FROM derived_memories WHERE state='stale');
 UPDATE jobs SET state='superseded' WHERE memory IN (SELECT memory FROM derived_memories WHERE state='stale') AND state IN ('pending','failed','indexing','completed');
END;
PRAGMA user_version=4;`)
	return err
}

func finishMigration(tx *sql.Tx, formal bool) error {
	if formal {
		var version int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version == 3 {
			if err := migrateOrganization(tx); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version == 4 {
			if err := migrateOrganizationReviews(tx); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version == 5 {
			if err := migrateExperiences(tx); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
