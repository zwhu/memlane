package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/zwhu/memlane/internal/records"
)

// Ordinary recovery still replays pending/indexing jobs. A failed saved C
// version waits for an explicit index intent. Retrying during the original
// indexing phase can keep one job indexing forever and hide its terminal fault.
func (s *Store) IndexReplayCandidates(ctx context.Context) (*sql.Rows, error) {
	return s.DB.QueryContext(ctx, `SELECT j.memory FROM jobs j JOIN memories m ON m.id=j.memory AND m.current_revision=j.revision
 WHERE j.state IN ('pending','failed','indexing') AND m.state='active' AND NOT (j.state='failed' AND EXISTS(
 SELECT 1 FROM project_task_runs r JOIN project_tasks t ON t.id=r.task AND t.attempt=r.attempt,
 json_each('["added","sources"]') a,json_each(json_extract(r.receipt,'$.'||a.value)) v
 WHERE r.backup_restored='' AND t.state IN ('saving','indexing','cancelling','failed','cancelled','interrupted','completed')
 AND json_extract(v.value,'$.memory')=j.memory AND json_extract(v.value,'$.revision')=j.revision))`)
}

func migrateProjectIndexRecovery(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE project_index_recoveries(
 operation TEXT PRIMARY KEY REFERENCES index_rebuilds(id),task TEXT NOT NULL REFERENCES project_tasks(id),
 attempt INTEGER NOT NULL,retired INTEGER NOT NULL DEFAULT 0);
CREATE INDEX project_index_recovery_task ON project_index_recoveries(task,attempt);
PRAGMA user_version=20;`)
	return err
}

// Recover only the immutable save receipt. This has no Agent access, repository
// reader, model executor, or authority to change a knowledge version.
func (s *Store) startProjectIndexRecovery(ctx context.Context, start records.IndexRebuildStart) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	if s.Schema < 20 || !projectUUID(start.ProjectTask) || start.Attempt < 1 || start.Scope != "" {
		return out, access("invalid_request", "exact project task and attempt required for index recovery")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var scope, receipt, state, restored string
	var attempt int64
	var released, gone bool
	var pid int
	err = tx.QueryRowContext(ctx, `SELECT t.project,t.attempt,t.state,r.receipt,r.released,r.process_pid,r.group_gone,r.backup_restored
 FROM project_tasks t JOIN project_task_runs r ON r.task=t.id AND r.attempt=t.attempt WHERE t.id=?`, start.ProjectTask).
		Scan(&scope, &attempt, &state, &receipt, &released, &pid, &gone, &restored)
	if err != nil {
		return out, err
	}
	if attempt != start.Attempt || !released || pid > 1 && !gone || restored != "" ||
		(state != "failed" && state != "cancelled" && state != "interrupted" && state != "completed") {
		return out, access("project_index_recovery_denied", "closed, non-restored save receipt required")
	}
	var priorTask string
	var priorAttempt int64
	var retired bool
	err = tx.QueryRowContext(ctx, `SELECT task,attempt,retired FROM project_index_recoveries WHERE operation=?`, start.OperationID).Scan(&priorTask, &priorAttempt, &retired)
	if err == nil {
		if priorTask != start.ProjectTask || priorAttempt != start.Attempt || retired {
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
	var used, active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM index_rebuilds WHERE id=?`, start.OperationID).Scan(&used); err != nil {
		return out, err
	}
	if used != 0 {
		return out, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM project_index_recoveries r JOIN index_rebuild_items i ON i.task=r.operation
 WHERE r.task=? AND r.attempt=? AND r.retired=0 AND i.state='queued'`, start.ProjectTask, start.Attempt).Scan(&active); err != nil {
		return out, err
	}
	if active != 0 {
		return out, access("project_index_recovery_active", "return to the current index recovery")
	}
	var saved records.ProjectTaskReceipt
	if err = json.Unmarshal([]byte(receipt), &saved); err != nil {
		return out, err
	}
	if !saved.Persisted || saved.Task != start.ProjectTask || saved.Attempt != start.Attempt || len(saved.Added)+len(saved.Sources) == 0 {
		return out, access("project_save_incomplete", "saved versions required for index recovery")
	}
	versions := map[string]int64{}
	for _, item := range append(append([]records.ProjectTaskSaved(nil), saved.Added...), saved.Sources...) {
		if !projectUUID(item.Memory) || item.Revision < 1 || versions[item.Memory] != 0 && versions[item.Memory] != item.Revision {
			return out, access("project_save_incomplete", "consistent saved version receipt required")
		}
		versions[item.Memory] = item.Revision
	}
	ids := make([]string, 0, len(versions))
	for id := range versions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuilds(id,scope) VALUES(?,?)`, start.OperationID, scope); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_index_recoveries(operation,task,attempt) VALUES(?,?,?)`, start.OperationID, start.ProjectTask, start.Attempt); err != nil {
		return out, err
	}
	for _, id := range ids {
		revision := versions[id]
		var dataset, memoryState, generation, job string
		var current, indexed int64
		var stale bool
		err = tx.QueryRowContext(ctx, `SELECT m.dataset,m.state,m.current_revision,m.indexed_revision,m.generation,coalesce(j.state,''),
 EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=? AND d.state='stale')
 FROM memories m LEFT JOIN jobs j ON j.memory=m.id AND j.revision=? WHERE m.id=?`, revision, revision, id).
			Scan(&dataset, &memoryState, &current, &indexed, &generation, &job, &stale)
		if err != nil {
			return out, err
		}
		if dataset != scope {
			return out, access("scope_denied", "saved version changed scope")
		}
		itemState := "queued"
		switch {
		case memoryState != "active" || current != revision || stale || job == "superseded":
			itemState = "expired"
		case job == "completed" && indexed == revision && generation != "":
			itemState = "completed"
		case job == "":
			return out, errors.New("saved index job missing")
		default:
			// A worker may already own pending/indexing. Never enqueue it twice.
			if job != "pending" && job != "indexing" {
				if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='pending',error='' WHERE memory=? AND revision=?`, id, revision); err != nil {
					return out, err
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE memories SET indexed_revision=0,generation='' WHERE id=? AND current_revision=?`, id, revision); err != nil {
				return out, err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO index_rebuild_items(task,memory,revision,state) VALUES(?,?,?,?)`, start.OperationID, id, revision, itemState); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return s.IndexRebuild(ctx, start.OperationID)
}
