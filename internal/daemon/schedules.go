package daemon

import (
	"context"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) acquireOrganizer(ctx context.Context) (func(), error) {
	s.organizerOnce.Do(func() { s.organizerSlot = make(chan struct{}, 1) })
	select {
	case s.organizerSlot <- struct{}{}:
		if ctx.Err() != nil {
			<-s.organizerSlot
			return nil, ctx.Err()
		}
		return func() { <-s.organizerSlot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) putSchedule(ctx context.Context, d records.ScheduleDraft) (records.Schedule, error) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	if s.lifecycle.Err() != nil {
		return records.Schedule{}, s.lifecycle.Err()
	}
	plan, err := s.store.PutSchedule(ctx, d, time.Now())
	if err != nil {
		return plan, err
	}
	// Stable operation replay returns its historical receipt. Cancellation must
	// use the current persisted generation so replay never stops newer work.
	current, err := s.store.Schedule(ctx, plan.ID)
	if err != nil {
		return plan, err
	}
	rows, err := s.store.DB.QueryContext(ctx, `SELECT t.id FROM organization_tasks t JOIN organization_plan_runs r ON r.task=t.id WHERE r.plan=? AND (r.generation!=? OR ?=0) AND t.state IN ('queued','running','applying')`, current.ID, current.Generation, current.Enabled)
	if err != nil {
		return plan, err
	}
	var obsolete []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return plan, err
		}
		obsolete = append(obsolete, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	for _, id := range obsolete {
		// The plan generation is already committed and blocks writes. Stop the
		// old process even when cancellation persistence subsequently fails.
		if owner, ok := s.organizationRuns[id]; ok {
			owner.Cancel()
		}
		if _, err = s.store.CancelOrganization(ctx, id); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func (s *Server) runSchedules(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	failures := 0
	for {
		if err := s.scheduleTick(ctx, time.Now()); err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			if failures >= 3 {
				s.mu.Lock()
				s.scheduleFailure = "计划连续触发失败，调度已停止；存储恢复后请明确重新启动服务。"
				s.mu.Unlock()
				return
			}
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) scheduleTick(ctx context.Context, now time.Time) error {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := s.reconcileOrganizations(ctx); err != nil {
		return err
	}
	plans, err := s.store.Schedules(ctx)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if !plan.Enabled || plan.NextRunMS > now.UnixMilli() {
			continue
		}
		_, task, err := s.store.FireSchedule(ctx, plan.ID, plan.Generation, now)
		if err != nil {
			return err
		}
		if task != nil {
			s.launchOrganization(*task)
		}
	}
	return nil
}
