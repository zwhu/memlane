package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateIndexRebuild(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE index_rebuilds(id TEXT PRIMARY KEY,scope TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE index_rebuild_items(task TEXT NOT NULL REFERENCES index_rebuilds(id),memory TEXT NOT NULL,revision INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','completed','failed','expired')),error TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(task,memory),FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision));
CREATE INDEX index_rebuild_pending ON index_rebuild_items(memory,revision,state);
PRAGMA user_version=13;`)
	return err
}

func (s *Store) StartIndexRebuild(ctx context.Context, start records.IndexRebuildStart) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	id, err := uuid.Parse(start.OperationID)
	if err != nil || id.String() != start.OperationID {
		return out, errors.New("canonical rebuild operation UUID required")
	}
	if start.Scope != "" {
		parsed, e := uuid.Parse(start.Scope)
		if e != nil || parsed.String() != start.Scope {
			return out, errors.New("canonical rebuild scope UUID required")
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var scope string
	err = tx.QueryRowContext(ctx, `SELECT scope FROM index_rebuilds WHERE id=?`, start.OperationID).Scan(&scope)
	if err == nil {
		if scope != start.Scope {
			return out, ErrConflict
		}
		if err = tx.Rollback(); err != nil {
			return out, err
		}
		return s.IndexRebuild(ctx, start.OperationID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if start.Scope != "" {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM scopes WHERE id=?`, start.Scope).Scan(&scope); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.current_revision FROM memories m WHERE m.state='active' AND (?='' OR m.dataset=?)
 AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale') ORDER BY m.id LIMIT 10001`, start.Scope, start.Scope)
	if err != nil {
		return out, err
	}
	items := []records.IndexRebuildItem{}
	for rows.Next() {
		var item records.IndexRebuildItem
		if err = rows.Scan(&item.Memory, &item.Revision); err != nil {
			rows.Close()
			return out, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(items) > 10000 {
		return out, errors.New("rebuild exceeds 10000 items; choose a scope")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuilds(id,scope) VALUES(?,?)`, start.OperationID, start.Scope); err != nil {
		return out, err
	}
	for _, item := range items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuild_items(task,memory,revision,state) VALUES(?,?,?,'queued')`, start.OperationID, item.Memory, item.Revision); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memories SET indexed_revision=0,generation='' WHERE id=?`, item.Memory); err != nil {
			return out, err
		}
		result, e := tx.ExecContext(ctx, `UPDATE jobs SET state='pending',error='' WHERE memory=? AND revision=?`, item.Memory, item.Revision)
		if e != nil {
			return out, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return out, e
		}
		if n != 1 {
			return out, errors.New("current index job missing")
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return s.IndexRebuild(ctx, start.OperationID)
}

func (s *Store) IndexRebuild(ctx context.Context, id string) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT id,scope,created_at FROM index_rebuilds WHERE id=?`, id).Scan(&out.ID, &out.Scope, &out.CreatedAt); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE index_rebuild_items SET state='expired' WHERE task=? AND state IN ('queued','failed') AND NOT EXISTS(
 SELECT 1 FROM memories m WHERE m.id=index_rebuild_items.memory AND m.current_revision=index_rebuild_items.revision AND m.state='active'
 AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale'))`, id)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT memory,revision,state,error FROM index_rebuild_items WHERE task=? ORDER BY memory`, id)
	if err != nil {
		return out, err
	}
	out.Items = []records.IndexRebuildItem{}
	queued, completed, failed := 0, 0, 0
	for rows.Next() {
		var item records.IndexRebuildItem
		if err = rows.Scan(&item.Memory, &item.Revision, &item.State, &item.Error); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, item)
		switch item.State {
		case "queued":
			queued++
		case "completed":
			completed++
		case "failed":
			failed++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	switch {
	case queued > 0:
		out.State = "running"
	case failed > 0 && completed > 0:
		out.State = "partial_failed"
	case failed > 0:
		out.State = "failed"
	case completed > 0 || len(out.Items) == 0:
		out.State = "completed"
	default:
		out.State = "expired"
	}
	return out, nil
}

func (s *Store) LatestIndexRebuild(ctx context.Context) (records.IndexRebuild, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM index_rebuilds ORDER BY created_at DESC,rowid DESC LIMIT 1`).Scan(&id); err != nil {
		return records.IndexRebuild{}, err
	}
	return s.IndexRebuild(ctx, id)
}
