package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func latestScheduleDue(plan records.Schedule, now time.Time) (int64, error) {
	if plan.Spec.Frequency == "interval" {
		step := int64(plan.Spec.IntervalHours) * int64(time.Hour/time.Millisecond)
		return plan.AnchorMS + (now.UnixMilli()-plan.AnchorMS)/step*step, nil
	}
	// A daily/weekly policy has at most two slots in this calendar window.
	start := now.Add(-8 * 24 * time.Hour)
	due := plan.NextRunMS
	for i := 0; i < 16; i++ {
		next, err := NextScheduleRun(plan.Spec, plan.AnchorMS, start)
		if err != nil {
			return 0, err
		}
		if next > now.UnixMilli() {
			break
		}
		if next > due {
			due = next
		}
		start = time.UnixMilli(next)
	}
	return due, nil
}

// FireSchedule has no process or network side effects. The slot receipt, next
// time and frozen task are one transaction, so crashes cannot duplicate a run.
func (s *Store) FireSchedule(ctx context.Context, id string, generation int64, now time.Time) (records.ScheduleRun, *records.OrganizationTask, error) {
	var run records.ScheduleRun
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return run, nil, err
	}
	defer tx.Rollback()
	plan, err := scheduleTx(ctx, tx, id)
	if err != nil {
		return run, nil, err
	}
	if !plan.Enabled || plan.Generation != generation {
		return run, nil, records.MergeError("plan_changed")
	}
	if plan.NextRunMS > now.UnixMilli() {
		return run, nil, records.MergeError("schedule_not_due")
	}
	due, err := latestScheduleDue(plan, now)
	if err != nil {
		return run, nil, err
	}
	next, err := NextScheduleRun(plan.Spec, plan.AnchorMS, now)
	if err != nil {
		return run, nil, err
	}
	run = records.ScheduleRun{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan-run:"+id+":"+stringRevision(generation)+":"+stringRevision(due))).String(), Plan: id, Generation: generation, DueMS: due, State: "skipped"}
	var task *records.OrganizationTask
	var currentPath, kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind,path FROM scopes WHERE id=?`, plan.Spec.Project).Scan(&kind, &currentPath); err != nil {
		return run, nil, err
	}
	scopesCurrent, err := scheduleScopesCurrent(ctx, tx, plan)
	if err != nil {
		return run, nil, err
	}
	if kind != "project" || currentPath != plan.ProjectPath || !scopesCurrent {
		run.Reason = "scope_changed"
		if _, err = tx.ExecContext(ctx, `UPDATE organization_plans SET enabled=0 WHERE id=?`, id); err != nil {
			return run, nil, err
		}
		next = 0
	} else if now.UnixMilli()-due > int64(plan.Spec.WindowMinutes)*int64(time.Minute/time.Millisecond) {
		run.Reason = "missed_window"
	} else {
		var busy bool
		busySQL := `SELECT EXISTS(SELECT 1 FROM organization_tasks WHERE project=? AND state IN ('queued','running','applying') UNION ALL SELECT 1 FROM merge_tasks WHERE project=? AND state IN ('queued','running')`
		busyArgs := []any{plan.Spec.Project, plan.Spec.Project}
		if s.Schema >= 9 {
			busySQL += ` UNION ALL SELECT 1 FROM organization_recovery r JOIN organization_tasks t ON t.id=r.task WHERE t.project=? AND r.state IN ('active','waiting') AND t.state IN ('failed','partial_failed','interrupted')`
			busyArgs = append(busyArgs, plan.Spec.Project)
		}
		busySQL += `)`
		if err = tx.QueryRowContext(ctx, busySQL, busyArgs...).Scan(&busy); err != nil {
			return run, nil, err
		}
		if busy {
			run.Reason = "project_busy"
		} else {
			// Automatic summaries are excluded to avoid feeding a plan's own
			// output back as newly changed independent evidence. User revisions
			// of derived knowledge remain eligible under their confirmation fact.
			rows, e := tx.QueryContext(ctx, `SELECT m.id,m.current_revision FROM memories m JOIN memory_metadata md ON md.memory=m.id JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision WHERE m.dataset=? AND m.state='active' AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.state IN ('valid','stale')) ORDER BY v.created_at DESC,m.id DESC LIMIT ?`, plan.Spec.Project, plan.Spec.MaterialLimit)
			if e != nil {
				return run, nil, e
			}
			inputs := []records.MergeTarget{}
			for rows.Next() {
				var input records.MergeTarget
				if e = rows.Scan(&input.Memory, &input.Revision); e != nil {
					rows.Close()
					return run, nil, e
				}
				inputs = append(inputs, input)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return run, nil, e
			}
			// Changing only time/window/material budget must not repeat already
			// processed inputs. An explicitly changed action/model policy may.
			fingerprint := records.MergeDigest(struct {
				Inputs          []records.MergeTarget
				Review, Global  bool
				Destinations    []string
				Executor, Model string
			}{inputs, plan.Spec.Review, plan.Spec.Global, plan.Spec.Destinations, plan.Spec.Executor, plan.Spec.Model})
			var prior string
			e = tx.QueryRowContext(ctx, `SELECT r.input_digest FROM organization_plan_runs r WHERE r.plan=? AND EXISTS(SELECT 1 FROM organization_results x WHERE x.task=r.task) ORDER BY r.due_ms DESC LIMIT 1`, id).Scan(&prior)
			if e != nil && e != sql.ErrNoRows {
				return run, nil, e
			}
			if len(inputs) == 0 {
				run.Reason = "no_materials"
			} else if prior == fingerprint {
				run.Reason = "no_changes"
			} else {
				created, _, e := s.startOrganizationTx(ctx, tx, records.OrganizationStart{OperationID: "schedule:" + run.ID, Project: plan.Spec.Project, Review: plan.Spec.Review, Global: plan.Spec.Global, Destinations: plan.Spec.Destinations, Inputs: inputs})
				if e != nil {
					var access *records.AccessError
					if errors.As(e, &access) && access.Code == "invalid_proposal" {
						run.Reason = "input_limit"
					} else {
						return run, nil, e
					}
				} else {
					run.State = "queued"
					run.Task = created.ID
					task = &created
					if s.Schema >= 9 && plan.Spec.RetryLimit > 0 {
						deadline := now.Add(time.Duration(plan.Spec.RecoveryWindowMinutes) * time.Minute).UnixMilli()
						if _, e = tx.ExecContext(ctx, `INSERT INTO organization_recovery(task,created_ms,stop_generation,extra_limit,deadline_ms) VALUES(?,?,?,?,?)`, created.ID, time.Now().UnixMilli(), s.ServiceStopGeneration, plan.Spec.RetryLimit, deadline); e != nil {
							return run, nil, e
						}
						created.Recovery = &records.OrganizationRecovery{State: "active", ExtraLimit: plan.Spec.RetryLimit, DeadlineMS: deadline}
					}
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO organization_plan_runs(id,plan,generation,due_ms,state,reason,task,input_digest) VALUES(?,?,?,?,?,?,?,?)`, run.ID, id, generation, due, run.State, run.Reason, nullableTask(run.Task), fingerprint); err != nil {
				return run, nil, err
			}
		}
	}
	if run.Reason == "scope_changed" || run.Reason == "missed_window" || run.Reason == "project_busy" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_plan_runs(id,plan,generation,due_ms,state,reason) VALUES(?,?,?,?,?,?)`, run.ID, id, generation, due, run.State, run.Reason); err != nil {
			return run, nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_plans SET next_run_ms=? WHERE id=? AND generation=?`, next, id, generation); err != nil {
		return run, nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT created_at FROM organization_plan_runs WHERE id=?`, run.ID).Scan(&run.CreatedAt); err != nil {
		return run, nil, err
	}
	return run, task, tx.Commit()
}

func nullableTask(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func scheduleScopesCurrent(ctx context.Context, tx *sql.Tx, plan records.Schedule) (bool, error) {
	for _, scope := range plan.FrozenScopes {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM scopes WHERE id=? AND kind=? AND path=?`, scope.ID, scope.Kind, scope.Path).Scan(&count); err != nil {
			return false, err
		}
		if count != 1 {
			return false, nil
		}
	}
	return true, nil
}
