package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

type projectRunContextKey struct{}

// The opaque owner is never accepted from IPC or serialized into task views.
func WithProjectRun(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, projectRunContextKey{}, owner)
}

func (s *Store) projectRunTx(ctx context.Context, tx *sql.Tx, id string, attempt int64, allowCancel bool) (records.ProjectTask, error) {
	t, err := scanProjectTask(tx.QueryRowContext(ctx, `SELECT `+projectTaskColumns+` FROM project_tasks WHERE id=?`, id))
	if err != nil {
		return t, err
	}
	owner, _ := ctx.Value(projectRunContextKey{}).(string)
	var current string
	var until int64
	var released bool
	err = tx.QueryRowContext(ctx, `SELECT owner,expires_ms,released FROM project_task_runs WHERE task=? AND attempt=? AND backup_restored=''`, id, attempt).Scan(&current, &until, &released)
	if err != nil || owner == "" || owner != current || released || !allowCancel && until <= time.Now().UnixMilli() || t.Attempt != attempt || !allowCancel && t.Cancelled {
		return t, access("project_run_invalid", "current live task owner required")
	}
	if !allowCancel {
		b, e := s.projectBindingTx(ctx, tx, t.Snapshot.Project)
		if e != nil {
			return t, e
		}
		if b.Revision != t.Snapshot.BindingRevision || !sameProjectExecutor(b, t.Snapshot.Executor) {
			return t, access("binding_changed", "project task authorization changed")
		}
	}
	return t, nil
}

func (s *Store) ClaimProjectRun(ctx context.Context, id string, attempt int64, owner string) error {
	if s.Schema < 19 || !projectUUID(owner) {
		return access("invalid_request", "project run owner required")
	}
	t, err := s.ProjectTask(ctx, id)
	if err != nil {
		return err
	}
	if t.AccessState != "available" || t.Attempt != attempt || t.Cancelled || t.State != "queued" {
		return access("project_task_closed", "queued authorized project task required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := s.projectBindingTx(ctx, tx, t.Snapshot.Project)
	if err != nil {
		return err
	}
	if b.Revision != t.Snapshot.BindingRevision || !sameProjectExecutor(b, t.Snapshot.Executor) {
		return access("binding_changed", "project binding changed")
	}
	result, err := tx.ExecContext(ctx, `UPDATE project_tasks SET state='starting' WHERE id=? AND attempt=? AND state='queued' AND cancel_requested=0`, id, attempt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return access("project_task_closed", "task already claimed or cancelled")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO project_task_runs(task,attempt,owner,expires_ms) VALUES(?,?,?,?)`, id, attempt, owner, time.Now().Add(30*time.Second).UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RegisterProjectProcess(ctx context.Context, id string, attempt int64, pid int) error {
	if pid <= 1 {
		return access("invalid_request", "actual process group required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return err
	}
	if t.State != "starting" {
		return access("project_task_closed", "task is no longer starting")
	}
	result, err := tx.ExecContext(ctx, `UPDATE project_task_runs SET process_pid=? WHERE task=? AND attempt=? AND process_pid=0`, pid, id, attempt)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return records.ErrConflict
	}
	return tx.Commit()
}

func (s *Store) RenewProjectRun(ctx context.Context, id string, attempt int64) error {
	t, err := s.ProjectTask(ctx, id)
	if err != nil {
		return err
	}
	if t.AccessState != "available" || t.Cancelled {
		return access("project_task_closed", "task scope changed or was cancelled")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.projectRunTx(ctx, tx, id, attempt, false); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET expires_ms=? WHERE task=? AND attempt=?`, time.Now().Add(30*time.Second).UnixMilli(), id, attempt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ProjectRunOrganizing(ctx context.Context, id string, attempt int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return err
	}
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT process_pid FROM project_task_runs WHERE task=?`, id).Scan(&pid); err != nil {
		return err
	}
	if t.State != "starting" || pid <= 1 {
		return access("project_run_invalid", "registered starting Agent required")
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_tasks SET state='organizing' WHERE id=?`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// FinishProcess stores actual cleanup independently of model validity or save
// success. A cancelled/changed scope still needs an honest process receipt.
func (s *Store) FinishProjectProcess(ctx context.Context, id string, attempt int64, execution records.ProjectTaskExecution) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.projectRunTx(ctx, tx, id, attempt, true); err != nil {
		return err
	}
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT process_pid FROM project_task_runs WHERE task=?`, id).Scan(&pid); err != nil {
		return err
	}
	if pid <= 1 || execution.PID != pid || execution.EndedMS < execution.StartedMS || !execution.Reaped || !execution.GroupGone {
		return access("project_process_unresolved", "actual registered process cleanup required")
	}
	b, err := json.Marshal(execution)
	if err != nil || len(b) > 65536 {
		return access("invalid_request", "bounded process receipt required")
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET execution=?,group_gone=1 WHERE task=? AND attempt=?`, string(b), id, attempt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) EndProjectRun(ctx context.Context, id string, attempt int64, state, code string) error {
	if state != "failed" && state != "cancelled" && state != "interrupted" {
		return access("invalid_request", "explicit terminal state required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := s.projectRunTx(ctx, tx, id, attempt, true)
	if err != nil {
		return err
	}
	var pid int
	var gone bool
	if err = tx.QueryRowContext(ctx, `SELECT process_pid,group_gone FROM project_task_runs WHERE task=?`, id).Scan(&pid, &gone); err != nil {
		return err
	}
	if pid > 1 && !gone {
		return access("project_process_unresolved", "wait for actual process cleanup before ending task")
	}
	if t.State == "completed" || t.State == "failed" || t.State == "cancelled" || t.State == "interrupted" {
		return records.ErrConflict
	}
	if t.Cancelled {
		state = "cancelled"
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_tasks SET state=?,error_code=? WHERE id=? AND attempt=?`, state, code, id, attempt)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_task_items SET state='cancelled' WHERE task=? AND state='pending'`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET released=1,expires_ms=0 WHERE task=? AND attempt=?`, id, attempt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) projectTaskRuntime(ctx context.Context, t *records.ProjectTask) error {
	if s.Schema < 19 {
		return nil
	}
	var execution, receipt string
	err := s.DB.QueryRowContext(ctx, `SELECT execution,receipt FROM project_task_runs WHERE task=? AND attempt=?`, t.ID, t.Attempt).Scan(&execution, &receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if execution != "null" {
		if err = json.Unmarshal([]byte(execution), &t.Execution); err != nil {
			return err
		}
	}
	if receipt != "null" {
		if err = json.Unmarshal([]byte(receipt), &t.Receipt); err != nil {
			return err
		}
		indexed, failed, sourceIndexed, sourceFailed, _, err := s.projectReceiptIndexes(ctx, *t.Receipt)
		if err != nil {
			return err
		}
		t.Receipt.Indexed, t.Receipt.IndexFailed = indexed, failed
		t.Receipt.SourceIndexed, t.Receipt.SourceIndexFailed = sourceIndexed, sourceFailed
	}
	return nil
}
