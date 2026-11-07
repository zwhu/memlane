package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/zwhu/memlane/internal/records"
)

// Recheck automatic model work both after obtaining the execution slot and at
// the last persisted permission boundary. Manual frozen-source requests keep
// their original contract; automatic work must not pay to analyze stale input.
func (s *Store) organizationModelSourcesCurrent(ctx context.Context, tx *sql.Tx, id string, attempt int64) (bool, error) {
	if s.Schema < 7 {
		return true, nil
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT a.snapshot FROM organization_attempts a WHERE a.task=? AND a.attempt=? AND a.kind='model' AND (a.resume_id LIKE 'auto-recovery:%' OR EXISTS(SELECT 1 FROM organization_plan_runs r WHERE r.task=a.task))`, id, attempt).Scan(&raw)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var snapshot records.OrganizationSnapshot
	if err = json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return false, err
	}
	refs := []records.MergeSource{}
	for _, input := range snapshot.Inputs {
		refs = append(refs, records.MergeSource{Memory: input.Memory, Revision: input.Revision})
	}
	return organizationCurrent(ctx, tx, snapshot.Project, refs)
}

func (s *Store) expireOrganizationModel(ctx context.Context, tx *sql.Tx, id string, attempt int64) error {
	r, err := tx.ExecContext(ctx, `UPDATE organization_tasks SET state='failed',error_code='proposal_expired' WHERE id=? AND attempt=? AND cancelled=0 AND state IN ('queued','running')`, id, attempt)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return records.MergeError("attempt_invalid")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE organization_attempts SET state='failed' WHERE task=? AND attempt=? AND state IN ('queued','running')`, id, attempt); err != nil {
		return err
	}
	if s.Schema >= 9 {
		if _, err := tx.ExecContext(ctx, `UPDATE organization_recovery SET state='expired',reason='proposal_expired',next_ms=0 WHERE task=?`, id); err != nil {
			return err
		}
	}
	return nil
}
