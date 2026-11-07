package store

import (
	"context"
	"database/sql"
)

func (s *Store) pauseUnownedRecoveries(ctx context.Context, tx *sql.Tx, project, except string) error {
	if s.Schema < 9 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.attempt,t.state FROM organization_recovery r JOIN organization_tasks t ON t.id=r.task JOIN organization_attempts a ON a.task=t.id AND a.attempt=(SELECT max(attempt) FROM organization_attempts WHERE task=t.id) WHERE t.project=? AND t.id!=? AND r.state IN ('active','waiting') AND (t.state IN ('failed','partial_failed','interrupted') OR (t.state IN ('queued','applying') AND a.resume_id LIKE 'auto-recovery:%' AND NOT EXISTS(SELECT 1 FROM organization_leases l WHERE l.task=t.id AND l.attempt=t.attempt AND (l.released=0 OR l.process_pid>1 AND l.group_gone=0))))`, project, except)
	if err != nil {
		return err
	}
	type pending struct {
		id, state string
		attempt   int64
	}
	var old []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.attempt, &p.state); err != nil {
			rows.Close()
			return err
		}
		old = append(old, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range old {
		if _, err = tx.ExecContext(ctx, `UPDATE organization_recovery SET state='cancelled',next_ms=0,reason='manual_task_started' WHERE task=?`, p.id); err != nil {
			return err
		}
		if p.state == "queued" || p.state == "applying" {
			if _, err = tx.ExecContext(ctx, `UPDATE organization_tasks SET state='interrupted',attempt=attempt+1,error_code='manual_superseded' WHERE id=? AND attempt=?`, p.id, p.attempt); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE organization_attempts SET state='interrupted' WHERE task=? AND attempt=?`, p.id, p.attempt); err != nil {
				return err
			}
		}
	}
	return nil
}
