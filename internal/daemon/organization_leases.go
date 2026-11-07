package daemon

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

// beginOrganizationLease starts after acquiring the shared execution slot.
// Renewals are independent short writes. Any failure cancels the actual runner;
// its caller waits for process cleanup before returning/releasing that slot.
func (s *Server) beginOrganizationLease(ctx context.Context, t records.OrganizationTask, cancel context.CancelFunc) (context.Context, func(bool, bool), error) {
	owner := uuid.NewString()
	if err := s.store.AcquireOrganizationLease(ctx, t.ID, t.Attempt, owner, time.Now()); err != nil {
		return ctx, nil, err
	}
	ctx = store.WithOrganizationLease(ctx, owner)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.store.RenewOrganizationLease(ctx, t.ID, t.Attempt, owner, time.Now()); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	finish := func(reaped, gone bool) {
		close(done)
		<-stopped
		finalCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = s.store.ReleaseOrganizationLease(finalCtx, t.ID, t.Attempt, owner, reaped, gone)
	}
	return ctx, finish, nil
}

// The independent guard kills the previous group on owner EOF. Do not kill a
// persisted PID here: it may have been recycled. A surviving group blocks
// recovery until gone, before listening or starting any new model.
func prepareOrganizationLeases(ctx context.Context, db *store.Store) error {
	if db.Schema < 8 {
		return nil
	}
	rows, err := db.DB.QueryContext(ctx, `SELECT process_pid FROM organization_leases WHERE process_pid>1 AND group_gone=0
 UNION SELECT CAST(json_extract(execution,'$.pid') AS INTEGER) FROM merge_attempts WHERE json_valid(execution) AND coalesce(json_extract(execution,'$.process_started'),0)=1 AND coalesce(json_extract(execution,'$.process_group_gone'),0)=0 AND coalesce(json_extract(execution,'$.startup_group_gone'),0)=0 AND CAST(json_extract(execution,'$.pid') AS INTEGER)>1`)
	if err != nil {
		return err
	}
	var pids []int
	for rows.Next() {
		var pid int
		if err = rows.Scan(&pid); err != nil {
			rows.Close()
			return err
		}
		pids = append(pids, pid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids {
		for syscall.Kill(-pid, 0) != syscall.ESRCH {
			if time.Now().After(deadline) {
				return errors.New("previous organization process group still exists; recovery blocked")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	if err = db.RevokeOrganizationLeases(ctx); err != nil {
		return err
	}
	_, err = db.DB.ExecContext(ctx, `UPDATE merge_attempts SET execution=json_set(execution,'$.startup_group_gone',json('true')) WHERE json_valid(execution) AND coalesce(json_extract(execution,'$.process_started'),0)=1 AND coalesce(json_extract(execution,'$.process_group_gone'),0)=0 AND CAST(json_extract(execution,'$.pid') AS INTEGER)>1`)
	return err
}

// A dead in-process owner can have failed its final lease write while storage
// was unavailable. Observe its registered group gone before clearing that
// lease; keep the actual reaped history, never synthesize it or signal a PID.
func (s *Server) reconcileDeadOrganizationLeases(ctx context.Context) error {
	if s.store.Schema < 8 {
		return nil
	}
	rows, err := s.store.DB.QueryContext(ctx, `SELECT task,attempt,owner,process_pid FROM organization_leases WHERE released=0 OR process_pid>1 AND group_gone=0`)
	if err != nil {
		return err
	}
	type lease struct {
		task, owner string
		attempt     int64
		pid         int
	}
	var old []lease
	for rows.Next() {
		var l lease
		if err = rows.Scan(&l.task, &l.attempt, &l.owner, &l.pid); err != nil {
			rows.Close()
			return err
		}
		old = append(old, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, l := range old {
		if owner, live := s.organizationRuns[l.task]; live && owner.Attempt == l.attempt {
			continue
		}
		if l.pid > 1 && syscall.Kill(-l.pid, 0) != syscall.ESRCH {
			continue
		}
		if _, err = s.store.DB.ExecContext(ctx, `UPDATE organization_leases SET released=1,expires_ms=0,group_gone=1 WHERE task=? AND attempt=? AND owner=?`, l.task, l.attempt, l.owner); err != nil {
			return err
		}
	}
	return nil
}
