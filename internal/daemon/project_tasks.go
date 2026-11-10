package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

type projectTaskOwner struct {
	Attempt int64
	Cancel  context.CancelFunc
}

func (s *Server) resolveProjectExecutor(ctx context.Context, project string) (agents.CodexProjectPlan, error) {
	if s.projectResolve != nil {
		return s.projectResolve(ctx, project)
	}
	var empty agents.CodexProjectPlan
	b, err := s.store.ProjectBinding(ctx, project)
	if err != nil {
		return empty, err
	}
	if b.State != "available" || b.Agent != "codex" {
		return empty, &records.AccessError{Code: "project_executor_unavailable", Message: "select a verified project Codex organizer"}
	}
	c, err := s.store.Connection(ctx, b.ConnectionID)
	if err != nil {
		return empty, err
	}
	clientRoot := s.config.ClientRoot
	if clientRoot == "" {
		clientRoot = filepath.Dir(s.config.Root)
	}
	i, err := agents.LoadInstallation(clientRoot, c.ID)
	if err != nil || i.Connection.ID != c.ID || i.Connection.Generation != c.Generation || !i.Connection.Enabled || i.Connection.Agent != c.Agent || i.ToolsetVersion != 2 || i.Options.ClientRoot != clientRoot || i.Options.ServiceRoot != s.config.Root || !filepath.IsAbs(i.Options.Bridge) {
		return empty, &records.AccessError{Code: "connection_verification_required", Message: "current installed connection verification required"}
	}
	var workspace string
	if err = s.store.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, project).Scan(&workspace); err != nil {
		return empty, err
	}
	return agents.ResolveCodexProject(ctx, i, workspace, i.Options.CodexDirectory, s.proposalGuard)
}

func (s *Server) startProjectTask(ctx context.Context, d records.ProjectTaskStart) (records.ProjectTask, error) {
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	var empty records.ProjectTask
	if s.lifecycle == nil || s.lifecycle.Err() != nil {
		return empty, &records.AccessError{Code: "service_stopping", Message: "background service is stopping"}
	}
	plan, err := s.resolveProjectExecutor(ctx, d.Project)
	if err != nil {
		return empty, err
	}
	if d.IncludeHistory {
		prior, found, e := s.store.ProjectTaskForIntent(ctx, d)
		if e != nil || found {
			if e == nil && prior.Snapshot.Executor != plan.Executor {
				prior.AccessState = "configuration_changed"
			}
			return prior, e
		}
	}
	selected, histories, err := s.freezeProjectHistory(ctx, d)
	if err != nil {
		return empty, err
	}
	t, created, err := s.store.StartProjectTaskWithHistory(ctx, d, plan.Executor, selected, histories)
	if err != nil || !created {
		return t, err
	}
	if s.projectRuns == nil {
		s.projectRuns = map[string]projectTaskOwner{}
	}
	run, cancel := context.WithCancel(s.lifecycle)
	s.projectRuns[t.ID] = projectTaskOwner{Attempt: t.Attempt, Cancel: cancel}
	s.projectWG.Add(1)
	s.projectTaskChanged()
	go s.runProjectTask(run, cancel, t)
	return t, nil
}

func (s *Server) cancelProjectTask(ctx context.Context, id string) (records.ProjectTask, error) {
	s.projectMu.Lock()
	defer s.projectMu.Unlock()
	t, err := s.store.CancelProjectTask(ctx, id)
	if err == nil {
		s.projectTaskChanged()
		if owner, ok := s.projectRuns[id]; ok && owner.Attempt == t.Attempt {
			owner.Cancel()
		}
	}
	return t, err
}

func (s *Server) runProjectTask(ctx context.Context, cancel context.CancelFunc, t records.ProjectTask) {
	defer s.projectTaskChanged()
	defer s.projectWG.Done()
	defer cancel()
	defer func() {
		s.projectMu.Lock()
		defer s.projectMu.Unlock()
		if owner, ok := s.projectRuns[t.ID]; ok && owner.Attempt == t.Attempt {
			delete(s.projectRuns, t.ID)
		}
	}()
	claimed := false
	completed := false
	endState, endCode := "interrupted", "project_run_interrupted"
	defer func() {
		if completed {
			return
		}
		final, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if !claimed {
			_, _ = s.store.DB.ExecContext(final, `UPDATE project_tasks SET state='interrupted',cancel_requested=1,error_code='project_start_interrupted' WHERE id=? AND attempt=? AND state='queued'`, t.ID, t.Attempt)
			return
		}
		final = store.WithProjectRun(final, ctx.Value(projectOwnerKey{}).(string))
		if err := s.store.EndProjectRun(final, t.ID, t.Attempt, endState, endCode); err != nil && s.stop != nil {
			s.stop()
		}
	}()
	release, err := s.acquireOrganizer(ctx)
	if err != nil {
		return
	}
	slotHeld := true
	defer func() {
		if slotHeld {
			release()
		}
	}()
	// A queued request must still use the captured executor after waiting. Never
	// switch a paid run to a changed model/provider or another connection.
	plan, err := s.resolveProjectExecutor(ctx, t.Snapshot.Project)
	if err != nil || plan.Executor != t.Snapshot.Executor {
		return
	}
	owner := uuid.NewString()
	if err = s.store.ClaimProjectRun(ctx, t.ID, t.Attempt, owner); err != nil {
		return
	}
	claimed = true
	s.projectTaskChanged()
	ctx = context.WithValue(store.WithProjectRun(ctx, owner), projectOwnerKey{}, owner)
	done := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.store.RenewProjectRun(ctx, t.ID, t.Attempt) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(done); <-watchDone }()
	runner := s.projectExecute
	if runner == nil {
		runner = agents.RunCodexProject
	}
	plan.KnowledgeReader = func(readCtx context.Context, cursor string) (records.ProjectKnowledgeContextPage, error) {
		s.agentAccess.RLock()
		defer s.agentAccess.RUnlock()
		return s.store.ProjectTaskKnowledge(readCtx, t.ID, t.Attempt, cursor)
	}
	if t.Snapshot.IncludeHistory {
		plan.HistoryReader = func(readCtx context.Context, session string) (records.ProjectRepositorySource, error) {
			s.agentAccess.RLock()
			defer s.agentAccess.RUnlock()
			return s.store.ProjectTaskHistory(readCtx, t.ID, t.Attempt, session)
		}
	}
	var stageErr error
	execution, runErr := runner(ctx, plan, t, func(pid int) error { return s.store.RegisterProjectProcess(ctx, t.ID, t.Attempt, pid) }, func(stage string) {
		if stage == "organizing" {
			if e := s.store.ProjectRunOrganizing(ctx, t.ID, t.Attempt); e != nil {
				stageErr = e
				cancel()
			}
			s.projectTaskChanged()
		}
	})
	final, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	if execution.Started {
		proof := records.ProjectTaskExecution{PID: execution.PID, StartedMS: execution.StartedMS, EndedMS: execution.EndedMS, Reaped: execution.Reaped, GroupGone: execution.GroupGone, Terminal: execution.Terminal, Thread: execution.Thread, Turn: execution.Turn, Model: execution.SelectedModel, Provider: execution.SelectedProvider, PermissionProfile: execution.PermissionProfile, InterruptSent: execution.InterruptSent, InterruptAcknowledged: execution.InterruptAcknowledged, KnowledgeContext: execution.KnowledgeContext, HistoryContext: execution.HistoryContext}
		if err = s.store.FinishProjectProcess(final, t.ID, t.Attempt, proof); err != nil {
			stop()
			if s.stop != nil {
				s.stop()
			}
			return
		}
	}
	stop()
	if runErr != nil || stageErr != nil || execution.Proposal == nil || ctx.Err() != nil {
		if s.lifecycle.Err() == nil && ctx.Err() == nil {
			endState, endCode = "failed", "project_execution_failed"
		}
		return
	}
	release()
	slotHeld = false
	s.agentAccess.RLock()
	err = s.store.PrepareProjectKnowledge(ctx, t.ID, t.Attempt, *execution.Proposal, execution.Sources)
	if err == nil {
		s.projectTaskChanged()
		_, err = s.store.ApplyProjectKnowledge(ctx, t.ID, t.Attempt)
	}
	s.agentAccess.RUnlock()
	s.projectTaskChanged()
	if err != nil {
		endState, endCode = "failed", "project_save_failed"
		return
	}
	select {
	case s.work <- struct{}{}:
	default:
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		finished, e := s.store.FinishProjectIndexes(ctx, t.ID, t.Attempt)
		if e != nil {
			return
		}
		if finished {
			completed = true
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type projectOwnerKey struct{}

// Old guards terminate on owner EOF. Observe groups without ever signalling
// persisted PIDs, which can be recycled. Old tasks never restart paid models.
func prepareProjectRuns(ctx context.Context, db *store.Store) error {
	if db.Schema < 19 {
		return nil
	}
	rows, err := db.DB.QueryContext(ctx, `SELECT process_pid FROM project_task_runs WHERE process_pid>1 AND group_gone=0 AND backup_restored=''`)
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
				return errors.New("previous project Agent group still exists; startup blocked")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET released=1,expires_ms=0,group_gone=CASE WHEN process_pid>1 AND backup_restored='' THEN 1 ELSE group_gone END`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_task_items SET state='cancelled' WHERE state='pending'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_tasks SET state='interrupted',cancel_requested=1,error_code='daemon_restarted' WHERE state IN ('queued','starting','organizing','saving','indexing','cancelling')`); err != nil {
		return err
	}
	return tx.Commit()
}
