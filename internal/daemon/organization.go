package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zwhu/memlane/internal/records"
	"time"
)

func (s *Server) startOrganization(ctx context.Context, d records.OrganizationStart) (records.OrganizationTask, error) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	if s.lifecycle.Err() != nil {
		return records.OrganizationTask{}, s.lifecycle.Err()
	}
	if err := s.reconcileOrganizations(ctx); err != nil {
		return records.OrganizationTask{}, err
	}
	if d.Processing != nil {
		if prior, found, e := s.store.ReplayOrganizationStart(ctx, d); found || e != nil {
			return prior, e
		}
		if s.processing == nil {
			return records.OrganizationTask{}, records.MergeError("unsupported_processing")
		}
		binding, e := s.processing.ResolveCurrentTask(ctx, *d.Processing)
		if e != nil {
			return records.OrganizationTask{}, e
		}
		t, created, e := s.store.StartProcessingOrganization(ctx, d, binding.Dependency)
		if e != nil || !created {
			return t, e
		}
		s.launchOrganization(t)
		return t, nil
	}
	t, created, err := s.store.StartOrganization(ctx, d)
	if err != nil || !created {
		return t, err
	}
	s.launchOrganization(t)
	return t, nil
}
func (s *Server) applyOrganization(ctx context.Context, t records.OrganizationTask) {
	for _, entry := range t.Entries {
		if entry.Item.RequiresReview() {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if _, err := s.store.ApplyOrganizationItem(ctx, t.ID, t.Attempt, entry.Item.Index); err != nil {
			_ = s.store.FailOrganizationItem(context.WithoutCancel(ctx), t.ID, t.Attempt, entry.Item.Index)
		}
	}
	_, _ = s.store.FinishOrganization(ctx, t.ID, t.Attempt)
	select {
	case s.work <- struct{}{}:
	default:
	}
}
func (s *Server) cancelOrganization(ctx context.Context, id string) (records.OrganizationTask, error) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	t, err := s.store.CancelOrganization(ctx, id)
	if err != nil {
		return t, err
	}
	if t.Cancelled {
		if owner, live := s.organizationRuns[id]; live {
			owner.Cancel()
		}
	}
	return t, nil
}
func (s *Server) resumeOrganization(ctx context.Context, d records.OrganizationResume) (records.OrganizationTask, error) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	if s.lifecycle.Err() != nil {
		return records.OrganizationTask{}, s.lifecycle.Err()
	}
	if err := s.reconcileOrganizations(ctx); err != nil {
		return records.OrganizationTask{}, err
	}
	t, created, err := s.store.ResumeOrganization(ctx, d)
	if err != nil || !created {
		return t, err
	}
	execution := t
	execution.Recovery = nil // explicit user apply-resume has its own authorization
	s.launchOrganization(execution)
	return t, nil
}

func (s *Server) finishOrganizationRun(t records.OrganizationTask) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if outcome, ok := s.organizationOutcomes[t.ID]; ok && outcome.Attempt == t.Attempt {
		if s.store.InterruptOrganizationOutcome(ctx, t.ID, t.Attempt, outcome.Known, outcome.Failure) == nil {
			delete(s.organizationOutcomes, t.ID)
		}
	} else {
		_ = s.store.InterruptOrganization(ctx, t.ID, t.Attempt)
	}
	if owner, ok := s.organizationRuns[t.ID]; ok && owner.Attempt == t.Attempt {
		delete(s.organizationRuns, t.ID)
	}
	_ = s.store.ScheduleOrganizationRecovery(ctx, t.ID, t.Snapshot.Attempt, time.Now())
}

// Caller holds organizationMu: start/resume registration and reconciliation
// cannot race. No model or network call runs inside this persistence boundary.
func (s *Server) reconcileOrganizations(ctx context.Context) error {
	rows, err := s.store.DB.QueryContext(ctx, `SELECT id,attempt FROM organization_tasks WHERE state IN ('queued','running','applying')`)
	if err != nil {
		return err
	}
	type owner struct {
		id      string
		attempt int64
	}
	var dead []owner
	for rows.Next() {
		var o owner
		if err = rows.Scan(&o.id, &o.attempt); err != nil {
			rows.Close()
			return err
		}
		if _, live := s.organizationRuns[o.id]; !live {
			dead = append(dead, o)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range dead {
		outcome := s.organizationOutcomes[o.id]
		if outcome.Attempt == o.attempt {
			err = s.store.InterruptOrganizationOutcome(ctx, o.id, o.attempt, outcome.Known, outcome.Failure)
		} else {
			err = s.store.InterruptOrganization(ctx, o.id, o.attempt)
		}
		if err != nil {
			return err
		}
		delete(s.organizationOutcomes, o.id)
	}
	return s.reconcileDeadOrganizationLeases(ctx)
}
func (s *Server) getOrganization(ctx context.Context, id string) (records.OrganizationTask, error) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	if err := s.reconcileOrganizations(ctx); err != nil {
		return records.OrganizationTask{}, err
	}
	return s.store.OrganizationTask(ctx, id)
}
func (s *Server) listOrganizations(ctx context.Context, q records.OrganizationListQuery) (records.OrganizationPage, error) {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	if err := s.reconcileOrganizations(ctx); err != nil {
		return records.OrganizationPage{}, err
	}
	return s.store.ListOrganizations(ctx, q)
}

type organizationOwner struct {
	Attempt int64
	Cancel  context.CancelFunc
}

type organizationOutcome struct {
	Attempt int64
	Known   bool
	Failure string
}

func (s *Server) finalizeOrganizationShutdown(ctx context.Context) error {
	s.organizationMu.Lock()
	defer s.organizationMu.Unlock()
	// All runner/handler groups have finished. Preserve in-memory known
	// outcomes rather than bypassing them through a generic interrupted pass.
	return s.reconcileOrganizations(ctx)
}

// Caller holds organizationMu so a frozen queued task gains its in-memory
// owner before queries can reconcile it as interrupted.
func (s *Server) launchOrganization(t records.OrganizationTask) {
	runCtx, cancel := context.WithCancel(s.lifecycle)
	s.organizationRuns[t.ID] = organizationOwner{Attempt: t.Attempt, Cancel: cancel}
	s.organizationWG.Add(1)
	go func() {
		defer s.organizationWG.Done()
		defer cancel()
		defer s.finishOrganizationRun(t)
		if t.Recovery != nil {
			bounded, stop := context.WithDeadline(runCtx, time.UnixMilli(t.Recovery.DeadlineMS))
			defer stop()
			runCtx = bounded
		}
		release, err := s.acquireOrganizer(runCtx)
		if err != nil {
			return
		}
		defer release()
		owned, finish, err := s.beginOrganizationLease(runCtx, t, cancel)
		if err != nil {
			return
		}
		runCtx = owned
		reaped, gone := false, true
		defer func() { finish(reaped, gone) }()
		if t.AttemptKind == "apply" {
			s.applyOrganization(runCtx, t)
			return
		}
		if err := s.store.ClaimOrganization(runCtx, t.ID, t.Attempt); err != nil {
			return
		}
		spec, err := s.store.ScheduleTaskSpec(runCtx, t.ID)
		if err != nil {
			return
		}
		model := ""
		if spec != nil {
			model = spec.Model
		}
		counts := map[string]int{}
		out, err := s.runOrganizationModel(runCtx, t, model, func(pid int) error {
			return s.store.RegisterOrganizationProcess(runCtx, t.ID, t.Attempt, pid)
		}, func(event string) {
			counts[event]++
			if event == "http.request_sent" {
				b, _ := json.Marshal(map[string]any{"events": counts, "http_active": true})
				_, _ = s.store.DB.ExecContext(runCtx, `UPDATE organization_attempts SET execution=? WHERE task=? AND attempt=? AND state='running'`, string(b), t.ID, t.Attempt)
			}
			if event == "turn.started" || event == "message_start" {
				b, _ := json.Marshal(map[string]any{"events": counts, "process_active": true})
				_, _ = s.store.DB.ExecContext(runCtx, `UPDATE organization_attempts SET execution=? WHERE task=? AND attempt=? AND state='running'`, string(b), t.ID, t.Attempt)
			}
		})

		if out.Started {
			reaped, gone = out.Reaped, out.GroupGone
		}
		if !gone && s.stop != nil {
			s.stop()
		}
		failure := ""
		known := true
		if err != nil {
			failure = "agent_failed"
			var access *records.AccessError
			if errors.As(err, &access) {
				failure = access.Code
			}
			if errors.Is(err, context.DeadlineExceeded) {
				failure = "agent_timeout"
			}
			if failure == "agent_failed" && transientOrganizationError(err) {
				failure = "agent_transient"
			}
			if errors.Is(err, context.Canceled) {
				if s.lifecycle.Err() != nil {
					known = false
				} else {
					failure = "lease_interrupted"
				}
			}
		}
		s.organizationMu.Lock()
		if s.organizationOutcomes == nil {
			s.organizationOutcomes = map[string]organizationOutcome{}
		}
		s.organizationOutcomes[t.ID] = organizationOutcome{Attempt: t.Attempt, Known: known, Failure: failure}
		s.organizationMu.Unlock()
		processData := map[string]any{}
		encoded, _ := json.Marshal(out.Detail)
		_ = json.Unmarshal(encoded, &processData)
		if processData == nil {
			processData = map[string]any{}
		}
		processData["outcome_recorded"], processData["outcome_code"] = known, failure
		process, _ := json.Marshal(processData)
		_, _ = s.store.DB.ExecContext(context.Background(), `UPDATE organization_attempts SET execution=? WHERE task=? AND attempt=?`, string(process), t.ID, t.Attempt)
		if runCtx.Err() != nil {
			return
		}
		var raw []byte
		if out.Result != nil {
			raw, _ = json.Marshal(out.Result)
		}
		staged, err := s.store.StageOrganization(runCtx, t.ID, t.Attempt, raw, processData, failure)
		if err != nil || staged.State != "applying" {
			return
		}
		s.applyOrganization(runCtx, staged)
	}()
}
