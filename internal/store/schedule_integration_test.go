package store

// These real SQLite and clock/protocol fixtures verify plan persistence. Real
// App/traex and sleep/restart acceptance remain separate D milestones.
import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func scheduleFixture(t *testing.T) (*Store, records.ScheduleDraft, time.Time) {
	t.Helper()
	s, _ := mergeDB(t)
	_, e := s.Scopes(mergeCtx)
	mustMerge(t, e)
	project := uuid.NewString()
	_, e = s.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','schedule fixture','/synthetic/schedule')`, project)
	mustMerge(t, e)
	for _, body := range []string{"项目使用 pnpm 管理依赖。", "依赖变更提交锁文件。"} {
		_, e = s.Apply(mergeCtx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: body})
		mustMerge(t, e)
	}
	d := records.ScheduleDraft{ID: uuid.NewString(), OperationID: uuid.NewString(), Spec: records.ScheduleSpec{Project: project, Executor: "traex", Timezone: "UTC", Frequency: "daily", LocalTime: "10:00", WindowMinutes: 60, MaterialLimit: 8}}
	return s, d, time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
}

func TestScheduleExplicitEnableReplayAndConflict(t *testing.T) {
	s, d, now := scheduleFixture(t)
	plan, e := s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, e)
	if plan.Enabled || plan.Generation != 1 || plan.NextRunMS != 0 {
		t.Fatal("draft activated a plan", plan)
	}
	d.Enabled = true
	d.ExpectedGeneration = 1
	d.OperationID = uuid.NewString()
	plan, e = s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, e)
	if !plan.Enabled || plan.NextRunMS != now.Add(2*time.Hour).UnixMilli() {
		t.Fatal(plan)
	}
	disabled := d
	disabled.Enabled = false
	disabled.ExpectedGeneration = 2
	disabled.OperationID = uuid.NewString()
	latest, e := s.PutSchedule(mergeCtx, disabled, now)
	mustMerge(t, e)
	replay, e := s.PutSchedule(mergeCtx, d, now.Add(time.Hour))
	mustMerge(t, e)
	if !reflect.DeepEqual(plan, replay) {
		t.Fatal("replay replaced original plan receipt")
	}
	plans, e := s.Schedules(mergeCtx)
	mustMerge(t, e)
	if len(plans) != 1 || plans[0].Enabled || plans[0].Generation != latest.Generation {
		t.Fatal("replay re-enabled plan", plans)
	}
	d.Spec.MaterialLimit = 4
	if _, e = s.PutSchedule(mergeCtx, d, now); e == nil {
		t.Fatal("operation ID reused for different policy")
	}
}

func completeEmptyScheduleFixture(t *testing.T, s *Store, task records.OrganizationTask) {
	t.Helper()
	r := records.OrganizationResult{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: task.Attempt, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Items: []records.OrganizationItem{}, Explanation: "clock/protocol fixture without model call"}
	raw, e := json.Marshal(r)
	mustMerge(t, e)
	stageOrganizationFixture(t, s, task, raw)
	_, e = s.FinishOrganization(mergeCtx, task.ID, task.Attempt)
	mustMerge(t, e)
}

func TestScheduleTriggerDedupChangedInputsAndRollback(t *testing.T) {
	s, d, now := scheduleFixture(t)
	d.Enabled = true
	plan, e := s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, e)
	at := time.UnixMilli(plan.NextRunMS).Add(time.Second)
	before := sqlRows(t, s.DB, "organization_tasks")
	_, e = s.DB.Exec(`CREATE TRIGGER plan_next_fail BEFORE UPDATE ON organization_plans WHEN NEW.next_run_ms!=OLD.next_run_ms BEGIN SELECT RAISE(ABORT,'SQL next slot fault fixture'); END`)
	mustMerge(t, e)
	if _, _, e = s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at); e == nil {
		t.Fatal("SQL fault did not fail")
	}
	if !reflect.DeepEqual(before, sqlRows(t, s.DB, "organization_tasks")) || organizationCount(t, s, `SELECT count(*) FROM organization_plan_runs`) != 0 {
		t.Fatal("partial trigger persisted")
	}
	_, e = s.DB.Exec(`DROP TRIGGER plan_next_fail`)
	mustMerge(t, e)
	run, task, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at)
	mustMerge(t, e)
	if run.State != "queued" || task == nil || len(task.Snapshot.Inputs) != 2 {
		t.Fatal(run, task)
	}
	if _, _, e = s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at); e == nil {
		t.Fatal("same due slot triggered twice")
	}
	completeEmptyScheduleFixture(t, s, *task)
	next, _, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at.AddDate(0, 0, 1))
	mustMerge(t, e)
	if next.State != "skipped" || next.Reason != "no_changes" {
		t.Fatal("unchanged materials called a model again", next)
	}
	source := task.Snapshot.Inputs[0]
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: source.Memory, Dataset: plan.Spec.Project, ExpectedRevision: source.Revision, Text: "项目已确认的新约定"})
	mustMerge(t, e)
	changed, current, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at.AddDate(0, 0, 2))
	mustMerge(t, e)
	if changed.State != "queued" || current == nil {
		t.Fatal("source update did not create new frozen run", changed)
	}
}

func TestScheduleWakeWindowBusyAndScopeFences(t *testing.T) {
	for _, mode := range []string{"bounded_catchup", "missed_window", "busy", "scope_changed", "disabled", "generation"} {
		t.Run(mode, func(t *testing.T) {
			s, d, now := scheduleFixture(t)
			d.Enabled = true
			plan, e := s.PutSchedule(mergeCtx, d, now)
			mustMerge(t, e)
			at := time.UnixMilli(plan.NextRunMS).AddDate(0, 0, 40).Add(15 * time.Minute)
			switch mode {
			case "missed_window":
				at = at.Add(2 * time.Hour)
			case "busy":
				page, e := s.List(mergeCtx, records.ListQuery{Datasets: []string{plan.Spec.Project}, Kind: "knowledge", Limit: 10})
				mustMerge(t, e)
				inputs := []records.MergeTarget{}
				for _, v := range page.Items {
					inputs = append(inputs, records.MergeTarget{Memory: v.Memory, Revision: v.Revision})
				}
				_, _, e = s.StartMerge(mergeCtx, records.MergeStart{OperationID: uuid.NewString(), Project: plan.Spec.Project, Inputs: inputs})
				mustMerge(t, e)
			case "scope_changed":
				_, e = s.DB.Exec(`UPDATE scopes SET path='/synthetic/other-project' WHERE id=?`, plan.Spec.Project)
				mustMerge(t, e)
			case "disabled", "generation":
				d.Enabled = mode == "generation"
				d.ExpectedGeneration = 1
				d.OperationID = uuid.NewString()
				_, e = s.PutSchedule(mergeCtx, d, now)
				mustMerge(t, e)
			}
			run, task, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at)
			if mode == "disabled" || mode == "generation" {
				if e == nil || task != nil {
					t.Fatal("old plan authorized trigger")
				}
				return
			}
			mustMerge(t, e)
			if organizationCount(t, s, `SELECT count(*) FROM organization_plan_runs`) != 1 {
				t.Fatal("wake accumulated historical runs")
			}
			if mode == "bounded_catchup" {
				if task == nil || run.DueMS != time.UnixMilli(plan.NextRunMS).AddDate(0, 0, 40).UnixMilli() {
					t.Fatal("wrong catchup slot", run)
				}
				return
			}
			reasons := map[string]string{"busy": "project_busy", "missed_window": "missed_window", "scope_changed": "scope_changed"}
			if task != nil || run.Reason != reasons[mode] {
				t.Fatal("unsafe wake run", run)
			}
		})
	}
}

func TestScheduleCancelledWithoutResultRunsNextAuthorizedSlot(t *testing.T) {
	s, d, now := scheduleFixture(t)
	d.Enabled = true
	plan, e := s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, e)
	at := time.UnixMilli(plan.NextRunMS).Add(time.Second)
	_, task, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at)
	mustMerge(t, e)
	_, e = s.CancelOrganization(mergeCtx, task.ID)
	mustMerge(t, e)
	run, next, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at.AddDate(0, 0, 1))
	mustMerge(t, e)
	if next == nil || next.ID == task.ID || run.State != "queued" {
		t.Fatal("canceled unprocessed materials blocked later authorized slot", run)
	}
}

func TestScheduleFrequencyEditDoesNotReprocessSameMaterials(t *testing.T) {
	s, d, now := scheduleFixture(t)
	d.Enabled = true
	plan, e := s.PutSchedule(mergeCtx, d, now)
	mustMerge(t, e)
	at := time.UnixMilli(plan.NextRunMS).Add(time.Second)
	_, task, e := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, at)
	mustMerge(t, e)
	completeEmptyScheduleFixture(t, s, *task)
	d.OperationID = uuid.NewString()
	d.ExpectedGeneration = 1
	d.Spec.LocalTime = "11:00"
	updated, e := s.PutSchedule(mergeCtx, d, at)
	mustMerge(t, e)
	run, next, e := s.FireSchedule(mergeCtx, updated.ID, updated.Generation, time.UnixMilli(updated.NextRunMS).Add(time.Second))
	mustMerge(t, e)
	if next != nil || run.Reason != "no_changes" {
		t.Fatal("time edit duplicated processed materials", run)
	}
}

// SQLite ledger fixtures prove the plan's immutable paid selection; no real
// provider capability is claimed by these synthetic validation receipts.
func TestScheduleProcessingFrozenSelectionAndExplicitEnable(t *testing.T) {
	s, d, now := scheduleFixture(t)
	first, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("plan-first")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("plan runtime")
	processingVerifiedFixture(t, s, first.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: first.ID}, dependency, processingTime)
	mustMerge(t, err)
	d.Spec.Executor = "processing"
	d.Processing = &records.ProcessingTaskAuthorization{Revision: first.ID, Generation: 1}
	plan, err := s.PutSchedule(mergeCtx, d, now, dependency)
	mustMerge(t, err)
	if plan.Enabled || plan.Spec.Processing == nil || plan.Spec.Processing.Revision.ID != first.ID {
		t.Fatal("save did not freeze inactive plan", plan)
	}
	second, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: first.ID, Spec: processingProvider("plan-second")}, processingTime)
	mustMerge(t, err)
	processingVerifiedFixture(t, s, second.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: second.ID, ExpectedGeneration: 1}, dependency, processingTime)
	mustMerge(t, err)
	enable := records.ScheduleDraft{ID: plan.ID, OperationID: uuid.NewString(), ExpectedGeneration: plan.Generation, Enabled: true, Spec: plan.Spec}
	enabled, err := s.PutSchedule(mergeCtx, enable, now)
	mustMerge(t, err)
	_, task, err := s.FireSchedule(mergeCtx, enabled.ID, enabled.Generation, time.UnixMilli(enabled.NextRunMS))
	mustMerge(t, err)
	if task == nil || task.Snapshot.Processing == nil || task.Snapshot.Processing.Revision.ID != first.ID {
		t.Fatal("plan silently switched runtime", task)
	}
	if err = task.Snapshot.Seal(); err != nil {
		t.Fatal("unsealed task", err)
	}
	forged := enable
	forged.ID, forged.OperationID, forged.ExpectedGeneration = uuid.NewString(), uuid.NewString(), 0
	if _, err = s.PutSchedule(mergeCtx, forged, now); err == nil {
		t.Fatal("client introduced a frozen paid selection without service resolution")
	}
}

func TestScheduleProcessingRevokedValidationSkipsWithoutFallback(t *testing.T) {
	s, d, now := scheduleFixture(t)
	revision, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("fixture")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("fixture runtime")
	processingVerifiedFixture(t, s, revision.ID, dependency)
	_, err = s.ActivateProcessing(mergeCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}, dependency, processingTime)
	mustMerge(t, err)
	d.Spec.Executor = "processing"
	d.Processing = &records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: 1}
	d.Enabled = true
	plan, err := s.PutSchedule(mergeCtx, d, now, dependency)
	mustMerge(t, err)
	// A later failed validation invalidates authorization; no legacy executor or
	// model call may replace the frozen binding.
	_, err = s.BeginProcessingValidation(mergeCtx, uuid.NewString(), revision.ID, dependency, processingTime)
	mustMerge(t, err)
	run, task, err := s.FireSchedule(mergeCtx, plan.ID, plan.Generation, time.UnixMilli(plan.NextRunMS))
	mustMerge(t, err)
	if task != nil || run.Reason != "processing_changed" || run.State != "skipped" {
		t.Fatal(run, task)
	}
	replay, found, err := s.ReplaySchedule(mergeCtx, d)
	mustMerge(t, err)
	if !found || replay.ID != plan.ID {
		t.Fatal("lost reply did not recover original save", replay)
	}
}
