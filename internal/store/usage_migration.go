package store

import "database/sql"

func migrateUsage(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE usage_coverage(id INTEGER PRIMARY KEY CHECK(id=1),started_ms INTEGER NOT NULL);
INSERT INTO usage_coverage VALUES(1,CAST((julianday('now')-2440587.5)*86400000 AS INTEGER));
CREATE TABLE usage_operations(id TEXT PRIMARY KEY,digest TEXT NOT NULL,receipt TEXT NOT NULL);
CREATE TABLE usage_events(id TEXT NOT NULL REFERENCES usage_operations(id),kind TEXT NOT NULL,channel TEXT NOT NULL,
 connection TEXT NOT NULL,generation INTEGER NOT NULL,workspace TEXT NOT NULL,dataset TEXT NOT NULL REFERENCES scopes(id),memory TEXT NOT NULL,revision INTEGER NOT NULL,
 observed_ms INTEGER NOT NULL,exposure TEXT NOT NULL,PRIMARY KEY(id,memory,revision),FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision));
CREATE INDEX usage_recent ON usage_events(memory,dataset,observed_ms,channel,kind);
CREATE TABLE memory_importance(memory TEXT PRIMARY KEY REFERENCES memories(id),important INTEGER NOT NULL CHECK(important IN (0,1)));
PRAGMA user_version=10;`)
	return err
}
