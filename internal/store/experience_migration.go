package store

import "database/sql"

func migrateExperiences(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE global_experiences(memory TEXT NOT NULL REFERENCES memories(id),revision INTEGER NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('copy','distill')),conditions TEXT NOT NULL,PRIMARY KEY(memory,revision),FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision)); CREATE TABLE global_experience_reviews(id TEXT PRIMARY KEY,task TEXT NOT NULL REFERENCES organization_tasks(id),position INTEGER NOT NULL,action TEXT NOT NULL DEFAULT 'global_experience' CHECK(action='global_experience'),digest TEXT NOT NULL,payload TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('review_pending','applied','rejected','expired','cancelled')),error_code TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT 'null',UNIQUE(task,position));
 CREATE TABLE global_experience_decisions(id TEXT PRIMARY KEY,item TEXT NOT NULL REFERENCES global_experience_reviews(id),digest TEXT NOT NULL,decision TEXT NOT NULL CHECK(decision IN ('confirm','reject')),receipt TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
 PRAGMA user_version=6;`)
	return err
}
