package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func migrateIndexCancellation(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE index_attempts(memory TEXT NOT NULL,revision INTEGER NOT NULL,token TEXT NOT NULL DEFAULT '',owner TEXT NOT NULL DEFAULT '',PRIMARY KEY(memory,revision),FOREIGN KEY(memory,revision) REFERENCES jobs(memory,revision));
 CREATE TABLE index_rebuild_controls(task TEXT PRIMARY KEY REFERENCES index_rebuilds(id),cancelled INTEGER NOT NULL DEFAULT 0,retry_of TEXT NOT NULL DEFAULT '');
 PRAGMA user_version=25;`)
	return err
}

type indexAttemptKey struct{}

func (s *Store) ClaimIndex(ctx context.Context, id string, revision int64) (context.Context, error) {
	if s.Schema < 25 {
		return ctx, s.IndexStarted(ctx, id, revision)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ctx, err
	}
	defer tx.Rollback()
	var state, token string
	err = tx.QueryRowContext(ctx, `SELECT j.state,coalesce(a.token,'') FROM jobs j LEFT JOIN index_attempts a ON a.memory=j.memory AND a.revision=j.revision WHERE j.memory=? AND j.revision=?`, id, revision).Scan(&state, &token)
	if err != nil {
		return ctx, err
	}
	if state == "cancelled" || state == "superseded" || state == "indexing" && token != "" {
		return ctx, ErrConflict
	}
	token = uuid.NewString()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='indexing',error='' WHERE memory=? AND revision=? AND EXISTS(SELECT 1 FROM memories WHERE id=? AND current_revision=? AND state='active')`, id, revision, id, revision)
	if err != nil {
		return ctx, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ctx, err
	}
	if n != 1 {
		return ctx, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO index_attempts(memory,revision,token) VALUES(?,?,?) ON CONFLICT(memory,revision) DO UPDATE SET token=excluded.token`, id, revision, token)
	if err != nil {
		return ctx, err
	}
	if err = tx.Commit(); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, indexAttemptKey{}, token), nil
}
func (s *Store) checkIndexAttempt(ctx context.Context, tx *sql.Tx, r engine.Record) error {
	if s.Schema < 25 {
		return nil
	}
	var state, token string
	if err := tx.QueryRowContext(ctx, `SELECT j.state,coalesce(a.token,'') FROM jobs j LEFT JOIN index_attempts a ON a.memory=j.memory AND a.revision=j.revision WHERE j.memory=? AND j.revision=?`, r.Memory, r.Revision).Scan(&state, &token); err != nil {
		return err
	}
	expected, _ := ctx.Value(indexAttemptKey{}).(string)
	if state == "cancelled" || expected != "" && (state != "indexing" || token != expected) {
		return ErrConflict
	}
	return nil
}

func (s *Store) queueRebuildIndexTx(ctx context.Context, tx *sql.Tx, id string, revision int64, owner string) error {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE memory=? AND revision=?`, id, revision).Scan(&state); err != nil {
		return err
	}
	// Ordinary pending/indexing work is shared, never stolen by a rebuild.
	if state != "pending" && state != "indexing" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO index_attempts(memory,revision,token,owner) VALUES(?,?,'',?) ON CONFLICT(memory,revision) DO UPDATE SET token='',owner=excluded.owner`, id, revision, owner); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET state=CASE WHEN state='indexing' THEN state ELSE 'pending' END,error='' WHERE memory=? AND revision=?`, id, revision)
	return err
}

// Cancel only jobs introduced by this rebuild. Ordinary pending saves and
// jobs still requested by another active rebuild retain their execution.
func (s *Store) CancelIndexRebuild(ctx context.Context, id string) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	if s.Schema < 25 || !projectUUID(id) {
		return out, errors.New("canonical rebuild UUID required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var found string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM index_rebuilds WHERE id=?`, id).Scan(&found); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO index_rebuild_controls(task,cancelled) SELECT ?,1 WHERE EXISTS(SELECT 1 FROM index_rebuild_items WHERE task=? AND state IN ('queued','failed')) ON CONFLICT(task) DO UPDATE SET cancelled=1`, id, id)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT memory,revision FROM index_attempts WHERE owner=?`, id)
	if err != nil {
		return out, err
	}
	owned := []records.IndexRebuildItem{}
	for rows.Next() {
		var item records.IndexRebuildItem
		if err = rows.Scan(&item.Memory, &item.Revision); err != nil {
			rows.Close()
			return out, err
		}
		owned = append(owned, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE index_attempts SET owner=coalesce((SELECT i.task FROM index_rebuild_items i WHERE i.memory=index_attempts.memory AND i.revision=index_attempts.revision AND i.task!=? AND i.state='queued' AND NOT EXISTS(SELECT 1 FROM index_rebuild_controls c WHERE c.task=i.task AND c.cancelled=1) ORDER BY i.task LIMIT 1),'') WHERE owner=?`, id, id)
	if err != nil {
		return out, err
	}
	for _, item := range owned {
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',error='rebuild_cancelled' WHERE state IN ('pending','indexing','failed') AND memory=? AND revision=? AND EXISTS(SELECT 1 FROM index_attempts a WHERE a.memory=jobs.memory AND a.revision=jobs.revision AND a.owner='')`, item.Memory, item.Revision)
		if err != nil {
			return out, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE index_attempts SET token='' WHERE memory=? AND revision=? AND owner=''`, item.Memory, item.Revision)
		if err != nil {
			return out, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE index_rebuild_items SET state='failed',error='rebuild_cancelled' WHERE task=? AND state IN ('queued','failed')`, id)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return s.IndexRebuild(ctx, id)
}

func (s *Store) retryIndexRebuild(ctx context.Context, start records.IndexRebuildStart) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	if s.Schema < 25 || !projectUUID(start.RetryOf) || start.ProjectTask != "" || start.Attempt != 0 || start.Scope != "" {
		return out, errors.New("explicit rebuild retry required")
	}
	old, err := s.IndexRebuild(ctx, start.RetryOf)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT coalesce(c.retry_of,'') FROM index_rebuilds r LEFT JOIN index_rebuild_controls c ON c.task=r.id WHERE r.id=?`, start.OperationID).Scan(&prior)
	if err == nil {
		if prior != start.RetryOf {
			return out, ErrConflict
		}
		tx.Rollback()
		return s.IndexRebuild(ctx, start.OperationID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if old.State == "running" {
		return out, errors.New("cancel or wait for active rebuild before retry")
	}
	if old.ProjectTask != "" {
		var allowed bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_index_recoveries r JOIN project_tasks t ON t.id=r.task AND t.attempt=r.attempt JOIN project_task_runs p ON p.task=t.id AND p.attempt=t.attempt WHERE r.operation=? AND r.retired=0 AND p.backup_restored='' AND p.released=1 AND (p.process_pid<=1 OR p.group_gone=1) AND t.state IN ('failed','cancelled','interrupted','completed'))`, old.ID).Scan(&allowed)
		if err != nil {
			return out, err
		}
		if !allowed {
			return out, access("project_index_recovery_denied", "closed non-restored project receipt required")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuilds(id,scope) VALUES(?,?)`, start.OperationID, old.Scope); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuild_controls(task,retry_of) VALUES(?,?)`, start.OperationID, old.ID); err != nil {
		return out, err
	}
	if old.ProjectTask != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO project_index_recoveries(operation,task,attempt) VALUES(?,?,?)`, start.OperationID, old.ProjectTask, old.Attempt); err != nil {
			return out, err
		}
	}

	for _, item := range old.Items {
		if item.State == "completed" {
			continue
		}
		state := "queued"
		var current bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memories WHERE id=? AND current_revision=? AND state='active' AND NOT EXISTS(SELECT 1 FROM derived_memories WHERE memory=? AND revision=? AND state='stale'))`, item.Memory, item.Revision, item.Memory, item.Revision).Scan(&current)
		if err != nil {
			return out, err
		}
		if !current || item.State == "expired" {
			state = "expired"
		} else {
			if err = s.queueRebuildIndexTx(ctx, tx, item.Memory, item.Revision, start.OperationID); err != nil {
				return out, err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuild_items(task,memory,revision,state) VALUES(?,?,?,?)`, start.OperationID, item.Memory, item.Revision, state); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return s.IndexRebuild(ctx, start.OperationID)
}

func (s *Store) requeueIndex(ctx context.Context, id string, revision int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='pending',error='' WHERE memory=? AND revision=? AND EXISTS(SELECT 1 FROM memories WHERE id=? AND current_revision=? AND state='active') AND NOT EXISTS(SELECT 1 FROM derived_memories WHERE memory=? AND revision=? AND state='stale')`, id, revision, id, revision, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM index_attempts WHERE memory=? AND revision=?`, id, revision); err != nil {
		return err
	}
	return tx.Commit()
}
