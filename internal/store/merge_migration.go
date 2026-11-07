package store

import "database/sql"

func migrateMerge(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE merge_tasks(
 id TEXT PRIMARY KEY, operation_id TEXT NOT NULL UNIQUE, request_digest TEXT NOT NULL,
 project TEXT NOT NULL REFERENCES scopes(id), state TEXT NOT NULL CHECK(state IN ('queued','running','completed','failed','cancelled','interrupted')),
 attempt INTEGER NOT NULL DEFAULT 1, cancelled INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE merge_attempts(
 task TEXT NOT NULL REFERENCES merge_tasks(id), attempt INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','failed','cancelled','interrupted')),
 snapshot TEXT NOT NULL, execution TEXT NOT NULL DEFAULT 'null', PRIMARY KEY(task,attempt));
CREATE TABLE merge_proposals(
 id TEXT PRIMARY KEY, task TEXT NOT NULL UNIQUE REFERENCES merge_tasks(id), attempt INTEGER NOT NULL,
 digest TEXT NOT NULL, payload TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','applied','rejected','cancelled','expired')));
CREATE TABLE merge_decisions(
 id TEXT PRIMARY KEY, proposal TEXT NOT NULL REFERENCES merge_proposals(id),
 digest TEXT NOT NULL, decision TEXT NOT NULL CHECK(decision IN ('confirm','reject')));
CREATE TABLE merge_receipts(
 id TEXT PRIMARY KEY, proposal TEXT NOT NULL UNIQUE REFERENCES merge_proposals(id),
 digest TEXT NOT NULL, memory TEXT NOT NULL REFERENCES memories(id), revision INTEGER NOT NULL);
CREATE TABLE merge_sources(
 memory TEXT NOT NULL, revision INTEGER NOT NULL, source_memory TEXT NOT NULL,
 source_revision INTEGER NOT NULL, snapshot TEXT NOT NULL, quote TEXT NOT NULL,
 PRIMARY KEY(memory,revision,source_memory),
 FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision),
 FOREIGN KEY(source_memory,source_revision) REFERENCES versions(memory,revision));
PRAGMA user_version=3;`)
	return err
}
