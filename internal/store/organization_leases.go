package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const OrganizationLeaseTTL = 45 * time.Second

type organizationLeaseKey struct{}

// WithOrganizationLease is an internal service credential, never a wire field.
func WithOrganizationLease(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, organizationLeaseKey{}, owner)
}

func (s *Store) AcquireOrganizationLease(ctx context.Context, id string, attempt int64, owner string, now time.Time) error {
	if s.Schema < 8 {
		return records.MergeError("lease_unavailable")
	}
	if _, err := uuid.Parse(owner); err != nil {
		return records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkOrganizationPlan(ctx, tx, id); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_tasks WHERE id=? AND attempt=? AND cancelled=0 AND state IN ('queued','applying'))`, id, attempt).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return records.MergeError("attempt_invalid")
	}
	// Never take over an old or expired lease. A replacement needs a new attempt
	// after the old process is reaped and the task's persistent fence advances.
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_leases(task,attempt,owner,expires_ms) VALUES(?,?,?,?)`, id, attempt, owner, now.Add(OrganizationLeaseTTL).UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) checkOrganizationLease(ctx context.Context, tx *sql.Tx, id string, attempt int64) error {
	if s.Schema < 8 {
		return nil
	}
	var owner string
	var expires int64
	var released bool
	err := tx.QueryRowContext(ctx, `SELECT owner,expires_ms,released FROM organization_leases WHERE task=? AND attempt=?`, id, attempt).Scan(&owner, &expires, &released)
	// Store-only fixtures and pre-lease trusted operations remain usable. All
	// production daemon executions create a lease before claiming or applying.
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	credential, _ := ctx.Value(organizationLeaseKey{}).(string)
	if owner != credential || released || expires <= time.Now().UnixMilli() {
		return records.MergeError("lease_invalid")
	}
	return nil
}

func (s *Store) RenewOrganizationLease(ctx context.Context, id string, attempt int64, owner string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkOrganizationPlan(ctx, tx, id); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE organization_leases SET expires_ms=? WHERE task=? AND attempt=? AND owner=? AND released=0 AND expires_ms>? AND EXISTS(SELECT 1 FROM organization_tasks t WHERE t.id=task AND t.attempt=organization_leases.attempt AND t.cancelled=0 AND t.state IN ('queued','running','applying'))`, now.Add(OrganizationLeaseTTL).UnixMilli(), id, attempt, owner, now.UnixMilli())
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return records.MergeError("lease_invalid")
	}
	return tx.Commit()
}

func (s *Store) RegisterOrganizationProcess(ctx context.Context, id string, attempt int64, pid int) error {
	if pid <= 1 {
		return records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.checkOrganizationLease(ctx, tx, id, attempt); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE organization_leases SET process_pid=? WHERE task=? AND attempt=? AND released=0 AND process_pid=0 AND EXISTS(SELECT 1 FROM organization_tasks t WHERE t.id=task AND t.attempt=organization_leases.attempt AND t.cancelled=0 AND t.state='running')`, pid, id, attempt)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return records.MergeError("lease_invalid")
	}
	return tx.Commit()
}

func (s *Store) ReleaseOrganizationLease(ctx context.Context, id string, attempt int64, owner string, reaped, gone bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE organization_leases SET released=1,expires_ms=0,process_reaped=?,group_gone=? WHERE task=? AND attempt=? AND owner=?`, reaped, gone, id, attempt, owner)
	return err
}

// Called only at startup after the sole service owner observes the old groups
// gone. No claim, renewal or new result can use the previous epoch afterwards.
func (s *Store) RevokeOrganizationLeases(ctx context.Context) error {
	if s.Schema < 8 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE organization_leases SET released=1,expires_ms=0,group_gone=1 WHERE released=0 OR group_gone=0`)
	return err
}
