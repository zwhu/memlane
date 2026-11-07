package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func organizationRecoveryTx(ctx context.Context, tx *sql.Tx, id string) (*records.OrganizationRecovery, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='organization_recovery')`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	r := new(records.OrganizationRecovery)
	err := tx.QueryRowContext(ctx, `SELECT state,extra_limit,used,deadline_ms,next_ms,reason FROM organization_recovery WHERE task=?`, id).Scan(&r.State, &r.ExtraLimit, &r.Used, &r.DeadlineMS, &r.NextMS, &r.Reason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ScheduleOrganizationRecovery records backoff, not another model call. It is
// safe after a dead owner's fence advances, but never touches a newer snapshot.
func (s *Store) ScheduleOrganizationRecovery(ctx context.Context, id string, expectedSnapshotAttempt int64, now time.Time) error {
	if s.Schema < 9 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return err
	}
	r := t.Recovery
	if r == nil || t.Snapshot.Attempt != expectedSnapshotAttempt || r.State == "done" || r.State == "exhausted" || r.State == "expired" || r.State == "cancelled" || r.State == "waiting" {
		return nil
	}
	state, reason, next := "done", t.Error, int64(0)
	if t.Cancelled {
		state, reason = "cancelled", "task_cancelled"
	} else if e := s.checkOrganizationPlan(ctx, tx, id); e != nil {
		if _, ok := e.(*records.AccessError); !ok {
			return e
		}
		state, reason = "cancelled", "plan_changed"
	} else if t.State == "queued" || t.State == "running" || t.State == "applying" {
		return nil
	} else {
		retry := false
		if t.Result == nil {
			retry = t.State == "interrupted" && t.Error == "service_interrupted" || t.State == "failed" && (t.Error == "agent_timeout" || t.Error == "agent_transient" || t.Error == "lease_interrupted")
		}
		if t.Result != nil && (t.State == "partial_failed" || t.State == "interrupted") {
			for _, e := range t.Entries {
				if !e.Item.RequiresReview() && (e.State == "pending" || e.State == "failed") {
					retry = true
				}
			}
		}
		if retry {
			if r.Used >= r.ExtraLimit {
				state, reason = "exhausted", "retry_limit"
			} else if now.UnixMilli() >= r.DeadlineMS {
				state, reason = "expired", "recovery_window"
			} else {
				delay := 15 * time.Second
				if r.Used > 0 {
					delay = time.Minute
				}
				next = now.Add(delay).UnixMilli()
				if next >= r.DeadlineMS {
					state, reason, next = "expired", "recovery_window", 0
				} else {
					state = "waiting"
				}
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE organization_recovery SET state=?,reason=?,next_ms=? WHERE task=? AND state='active'`, state, reason, next, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// A stop watermark survives clearing the active stop marker. It suppresses
// old attempts even if the database was unwritable during the user's stop.
func (s *Store) CancelRecoveriesBefore(ctx context.Context, cutoff int64) error {
	return s.CancelRecoveriesForStop(ctx, cutoff, 0)
}

func (s *Store) CancelRecoveriesForStop(ctx context.Context, cutoff, epoch int64) error {
	if s.Schema < 9 || cutoff <= 0 {
		return nil
	}
	where, args := `created_ms<=?`, []any{cutoff}
	if epoch > 0 {
		where = `(stop_generation<? OR (stop_generation>? AND created_ms<=?))`
		args = []any{epoch, epoch, cutoff}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE organization_recovery SET state='cancelled',next_ms=0,reason='user_stopped' WHERE `+where+` AND state IN ('active','waiting')`, args...)
	return err
}

func (s *Store) OrganizationRecoveryCandidates(ctx context.Context) ([]string, error) {
	out := []string{}
	if s.Schema < 9 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.task FROM organization_recovery r JOIN organization_tasks t ON t.id=r.task WHERE r.state IN ('active','waiting') AND t.state IN ('failed','partial_failed','interrupted','cancelled','completed') ORDER BY r.next_ms,r.task LIMIT 100`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return out, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BeginOrganizationRecovery consumes one permitted retry and creates its new
// attempt atomically. Persisted result means apply-only: no model may rerun.
func (s *Store) BeginOrganizationRecovery(ctx context.Context, id string, now time.Time) (*records.OrganizationTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	r := t.Recovery
	if r == nil || r.State != "waiting" || r.NextMS > now.UnixMilli() {
		return nil, nil
	}
	state, reason := "", ""
	if t.Cancelled {
		state, reason = "cancelled", "task_cancelled"
	} else if e := s.checkOrganizationPlan(ctx, tx, id); e != nil {
		if _, ok := e.(*records.AccessError); !ok {
			return nil, e
		}
		state, reason = "cancelled", "plan_changed"
	} else if now.UnixMilli() >= r.DeadlineMS {
		state, reason = "expired", "recovery_window"
	} else if r.Used >= r.ExtraLimit {
		state, reason = "exhausted", "retry_limit"
	}
	if state != "" {
		_, err = tx.ExecContext(ctx, `UPDATE organization_recovery SET state=?,reason=?,next_ms=0 WHERE task=?`, state, reason, id)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if t.State != "failed" && t.State != "partial_failed" && t.State != "interrupted" {
		return nil, nil
	}
	var unreaped, busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_leases WHERE task=? AND released=0 OR task=? AND process_pid>1 AND group_gone=0)`, id, id).Scan(&unreaped); err != nil {
		return nil, err
	}
	if unreaped {
		return nil, nil
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_tasks WHERE project=? AND id!=? AND state IN ('queued','running','applying') UNION ALL SELECT 1 FROM merge_tasks WHERE project=? AND state IN ('queued','running'))`, t.Project, id, t.Project).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, nil
	}
	snapshot := t.Snapshot
	if t.Result == nil {
		refs := []records.MergeSource{}
		for _, input := range snapshot.Inputs {
			refs = append(refs, records.MergeSource{Memory: input.Memory, Revision: input.Revision})
		}
		current, e := organizationCurrent(ctx, tx, t.Project, refs)
		if e != nil {
			return nil, e
		}
		if !current {
			_, err = tx.ExecContext(ctx, `UPDATE organization_recovery SET state='expired',reason='proposal_expired',next_ms=0 WHERE task=?`, id)
			if err != nil {
				return nil, err
			}
			return nil, tx.Commit()
		}
	}
	snapshot.Attempt = t.Attempt + 1
	if err = snapshot.Seal(); err != nil {
		return nil, err
	}
	kind, taskState := "model", "queued"
	if t.Result != nil {
		kind, taskState = "apply", "applying"
	}
	b, _ := json.Marshal(snapshot)
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_attempts(task,attempt,state,kind,resume_id,snapshot) VALUES(?,?,?,?,?,?)`, id, snapshot.Attempt, taskState, kind, "auto-recovery:"+stringRevision(int64(r.Used+1)), string(b)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_tasks SET state=?,attempt=?,error_code='' WHERE id=? AND attempt=?`, taskState, snapshot.Attempt, id, t.Attempt); err != nil {
		return nil, err
	}
	if kind == "apply" {
		if _, err = tx.ExecContext(ctx, `UPDATE organization_items SET state='pending',error_code='' WHERE task=? AND state='failed'`, id); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_recovery SET used=used+1,state='active',next_ms=0 WHERE task=?`, id); err != nil {
		return nil, err
	}
	result, err := organizationTask(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}
