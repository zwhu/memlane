package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func scheduleTx(ctx context.Context, tx *sql.Tx, id string) (records.Schedule, error) {
	var plan records.Schedule
	var raw, scopes string
	err := tx.QueryRowContext(ctx, `SELECT id,generation,enabled,spec,anchor_ms,next_run_ms,created_at,project_path,destinations FROM organization_plans WHERE id=?`, id).Scan(&plan.ID, &plan.Generation, &plan.Enabled, &raw, &plan.AnchorMS, &plan.NextRunMS, &plan.CreatedAt, &plan.ProjectPath, &scopes)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &plan.Spec)
	}
	if err == nil {
		err = json.Unmarshal([]byte(scopes), &plan.FrozenScopes)
	}
	return plan, err
}

func (s *Store) PutSchedule(ctx context.Context, d records.ScheduleDraft, now time.Time, dependencies ...string) (records.Schedule, error) {
	var empty records.Schedule
	if s.Schema < 7 || d.ExpectedGeneration < 0 || strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 {
		return empty, records.MergeError("invalid_request")
	}
	if _, err := uuid.Parse(d.ID); err != nil {
		return empty, records.MergeError("invalid_request")
	}
	if err := validateScheduleSpec(d.Spec); err != nil {
		return empty, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	digest := records.MergeDigest(d)
	var prior, receipt string
	err = tx.QueryRowContext(ctx, `SELECT digest,receipt FROM organization_plan_operations WHERE id=?`, d.OperationID).Scan(&prior, &receipt)
	if err == nil {
		if prior != digest {
			return empty, ErrConflict
		}
		err = json.Unmarshal([]byte(receipt), &empty)
		return empty, err
	}
	if err != sql.ErrNoRows {
		return empty, err
	}
	var kind, projectPath string
	if err = tx.QueryRowContext(ctx, `SELECT kind,path FROM scopes WHERE id=?`, d.Spec.Project).Scan(&kind, &projectPath); err != nil || kind != "project" {
		return empty, records.MergeError("scope_denied")
	}
	seen := map[string]bool{}
	frozen := []records.Scope{}
	for _, id := range d.Spec.Destinations {
		var scope records.Scope
		if err = tx.QueryRowContext(ctx, `SELECT id,name,kind,path FROM scopes WHERE id=?`, id).Scan(&scope.ID, &scope.Name, &scope.Kind, &scope.Path); err != nil || scope.Kind != "project" || id == d.Spec.Project || seen[id] {
			return empty, records.MergeError("scope_denied")
		}
		seen[id] = true
		frozen = append(frozen, scope)
	}
	if d.Spec.Global {
		var scope records.Scope
		if err = tx.QueryRowContext(ctx, `SELECT id,name,kind,path FROM scopes WHERE kind='experience'`).Scan(&scope.ID, &scope.Name, &scope.Kind, &scope.Path); err != nil {
			return empty, err
		}
		frozen = append(frozen, scope)
	}
	old, err := scheduleTx(ctx, tx, d.ID)
	if err != nil && err != sql.ErrNoRows {
		return empty, err
	}
	if err == sql.ErrNoRows && d.ExpectedGeneration != 0 || err == nil && old.Generation != d.ExpectedGeneration {
		return empty, ErrConflict
	}
	if d.Spec.Executor == "processing" {
		if d.Processing != nil {
			if len(dependencies) != 1 {
				return empty, records.MergeError("verification_required")
			}
			frozen, e := freezeProcessingTx(ctx, tx, dependencies[0], now)
			if e != nil {
				return empty, e
			}
			if frozen.SelectionGeneration != d.Processing.Generation || frozen.Revision.ID != d.Processing.Revision {
				return empty, ErrConflict
			}
			d.Spec.Processing = &frozen
		} else {
			// Editing timing or explicitly enabling retains this plan's executor;
			// client-supplied snapshots cannot introduce a new paid binding.
			if old.Spec.Processing == nil || records.MergeDigest(d.Spec.Processing) != records.MergeDigest(old.Spec.Processing) {
				return empty, records.MergeError("configuration_required")
			}
			d.Spec.Processing = old.Spec.Processing
		}
		if d.Spec.Model != "" {
			return empty, records.MergeError("invalid_request")
		}
	} else if d.Processing != nil {
		return empty, records.MergeError("invalid_request")
	}
	if d.Enabled {
		var busy bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_plans WHERE project=? AND enabled=1 AND id!=?)`, d.Spec.Project, d.ID).Scan(&busy); err != nil {
			return empty, err
		}
		if busy {
			return empty, records.MergeError("plan_conflict")
		}
	}
	anchor := now.UnixMilli()
	if old.Enabled && d.Enabled && old.Spec.Frequency == d.Spec.Frequency && old.Spec.IntervalHours == d.Spec.IntervalHours {
		anchor = old.AnchorMS
	}
	next := int64(0)
	if d.Enabled {
		next, err = NextScheduleRun(d.Spec, anchor, now)
		if err != nil {
			return empty, err
		}
	}
	raw, _ := json.Marshal(d.Spec)
	frozenRaw, _ := json.Marshal(frozen)
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_plans(id,generation,project,project_path,destinations,spec,enabled,anchor_ms,next_run_ms) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET generation=excluded.generation,project=excluded.project,project_path=excluded.project_path,destinations=excluded.destinations,spec=excluded.spec,enabled=excluded.enabled,anchor_ms=excluded.anchor_ms,next_run_ms=excluded.next_run_ms`, d.ID, d.ExpectedGeneration+1, d.Spec.Project, projectPath, string(frozenRaw), string(raw), d.Enabled, anchor, next)
	if err != nil {
		return empty, err
	}
	plan, err := scheduleTx(ctx, tx, d.ID)
	if err != nil {
		return empty, err
	}
	encoded, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_plan_operations(id,plan,digest,receipt) VALUES(?,?,?,?)`, d.OperationID, d.ID, digest, string(encoded)); err != nil {
		return empty, err
	}
	return plan, tx.Commit()
}

func (s *Store) Schedules(ctx context.Context) ([]records.Schedule, error) {
	out := []records.Schedule{}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,generation,enabled,spec,anchor_ms,next_run_ms,created_at,project_path,destinations FROM organization_plans ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var plan records.Schedule
		var raw, scopes string
		if err = rows.Scan(&plan.ID, &plan.Generation, &plan.Enabled, &raw, &plan.AnchorMS, &plan.NextRunMS, &plan.CreatedAt, &plan.ProjectPath, &scopes); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &plan.Spec); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(scopes), &plan.FrozenScopes); err != nil {
			return out, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (s *Store) Schedule(ctx context.Context, id string) (records.Schedule, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.Schedule{}, err
	}
	defer tx.Rollback()
	plan, err := scheduleTx(ctx, tx, id)
	if err != nil {
		return plan, err
	}
	return plan, tx.Commit()
}

func (s *Store) checkOrganizationPlan(ctx context.Context, tx *sql.Tx, task string) error {
	if s.Schema < 7 {
		return nil
	}
	var id string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT plan,generation FROM organization_plan_runs WHERE task=?`, task).Scan(&id, &generation)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	plan, err := scheduleTx(ctx, tx, id)
	if err != nil {
		return err
	}
	var currentPath string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, plan.Spec.Project).Scan(&currentPath); err != nil {
		return err
	}
	current, err := scheduleScopesCurrent(ctx, tx, plan)
	if err != nil {
		return err
	}
	if !plan.Enabled || plan.Generation != generation || plan.ProjectPath != currentPath || !current {
		return records.MergeError("plan_changed")
	}
	return nil
}

func (s *Store) ScheduleTaskSpec(ctx context.Context, task string) (*records.ScheduleSpec, error) {
	if s.Schema < 7 {
		return nil, nil
	}
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT p.spec FROM organization_plan_runs r JOIN organization_plans p ON p.id=r.plan WHERE r.task=? AND p.generation=r.generation`, task).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var spec records.ScheduleSpec
	err = json.Unmarshal([]byte(raw), &spec)
	return &spec, err
}

func (s *Store) ScheduleRuns(ctx context.Context, id string) ([]records.ScheduleRun, error) {
	out := []records.ScheduleRun{}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id,r.plan,r.generation,r.due_ms,coalesce(t.state,r.state),CASE WHEN r.reason!='' THEN r.reason ELSE coalesce(t.error_code,'') END,coalesce(r.task,''),r.created_at FROM organization_plan_runs r LEFT JOIN organization_tasks t ON t.id=r.task WHERE r.plan=? ORDER BY r.due_ms DESC LIMIT 100`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var run records.ScheduleRun
		if err = rows.Scan(&run.ID, &run.Plan, &run.Generation, &run.DueMS, &run.State, &run.Reason, &run.Task, &run.CreatedAt); err != nil {
			return out, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ReplaySchedule resolves a lost save reply without contacting the paid runtime
// or reviving a plan that has since been disabled.
func (s *Store) ReplaySchedule(ctx context.Context, d records.ScheduleDraft) (records.Schedule, bool, error) {
	var out records.Schedule
	var digest, receipt string
	err := s.DB.QueryRowContext(ctx, `SELECT digest,receipt FROM organization_plan_operations WHERE id=?`, d.OperationID).Scan(&digest, &receipt)
	if err == sql.ErrNoRows {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if digest != records.MergeDigest(d) {
		return out, true, ErrConflict
	}
	err = json.Unmarshal([]byte(receipt), &out)
	return out, true, err
}
