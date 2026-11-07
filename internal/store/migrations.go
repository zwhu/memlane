package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// P0 databases have user_version=0. Upgrade in one transaction, retaining
// originals, operations, durable jobs and chunk bindings. Future versions fail
// closed rather than being silently opened by an older client.
func migrate(db *sql.DB, formal bool) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > SchemaVersion || (!formal && version > 2) {
		return errors.New("database schema is newer than this client")
	}
	if version >= 2 {
		if formal && version == 2 {
			if err = migrateMerge(tx); err != nil {
				return err
			}
		}
		return finishMigration(tx, formal)
	}
	if version == 1 {
		if err = migrateAgents(tx); err != nil {
			return err
		}
		if formal {
			if err = migrateMerge(tx); err != nil {
				return err
			}
		}
		return finishMigration(tx, formal)
	}
	_, err = tx.Exec(`
CREATE TABLE scopes(id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, path TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX scope_path ON scopes(path) WHERE path!='';
CREATE UNIQUE INDEX global_scope_kind ON scopes(kind) WHERE kind IN ('preference','experience');
ALTER TABLE versions ADD COLUMN title TEXT NOT NULL DEFAULT '';
CREATE TABLE memory_metadata(memory TEXT PRIMARY KEY REFERENCES memories(id), source TEXT NOT NULL, origin TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE activities(memory TEXT PRIMARY KEY REFERENCES memories(id), kind TEXT NOT NULL, occurred_at TEXT NOT NULL);
CREATE INDEX memory_dataset ON memories(dataset,id);
CREATE INDEX activity_time ON activities(occurred_at,memory);
`)
	if err != nil {
		return err
	}
	for _, scope := range []struct{ kind, name string }{{"preference", "个人偏好"}, {"experience", "全局经验"}} {
		if _, err = tx.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,?,?)`, uuid.NewString(), scope.kind, scope.name); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT DISTINCT dataset FROM memories`)
	if err != nil {
		return err
	}
	datasets := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		datasets = append(datasets, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range datasets {
		if _, err = tx.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'legacy',?)`, id, "已有项目 "+id[:min(len(id), 8)]); err != nil {
			return err
		}
	}
	// Historic timestamps do not prove when knowledge first became usable.
	// Existing rows are imports, including pending indexes completed later.
	_, err = tx.Exec(`INSERT INTO memory_metadata(memory,source,origin,created_at) SELECT m.id,'原有记录','imported',strftime('%Y-%m-%dT%H:%M:%fZ',v.created_at) FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=1;
INSERT INTO activities(memory,kind,occurred_at) SELECT id,'imported',strftime('%Y-%m-%dT%H:%M:%fZ','now') FROM memories WHERE indexed_revision>0;
UPDATE versions SET title=substr(replace(text,char(10),' '),1,80);
PRAGMA user_version=1;`)
	if err != nil {
		return err
	}
	if err = migrateAgents(tx); err != nil {
		return err
	}
	if formal {
		if err = migrateMerge(tx); err != nil {
			return err
		}
	}
	return finishMigration(tx, formal)
}

func migrateAgents(tx *sql.Tx) error {
	_, err := tx.Exec(`
ALTER TABLE memory_metadata ADD COLUMN source_detail TEXT NOT NULL DEFAULT 'null';
CREATE TABLE connections(id TEXT PRIMARY KEY, agent TEXT NOT NULL, name TEXT NOT NULL, project TEXT NOT NULL, allowed_scopes TEXT NOT NULL, enabled INTEGER NOT NULL, generation INTEGER NOT NULL, last_seen TEXT NOT NULL DEFAULT '');
CREATE TABLE repositories(common_dir TEXT PRIMARY KEY, scope TEXT NOT NULL REFERENCES scopes(id));
PRAGMA user_version=2;`)
	return err
}

func titleFor(title, text string) string {
	if strings.TrimSpace(title) == "" {
		title = strings.SplitN(text, "\n", 2)[0]
	}
	return string([]rune(strings.TrimSpace(title))[:min(80, len([]rune(strings.TrimSpace(title))))])
}
