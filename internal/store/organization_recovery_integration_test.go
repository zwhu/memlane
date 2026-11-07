package store

// Real SQLite and explicit clock/result fixtures; never normal model evidence.
import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func recoveryFixture(t *testing.T, limit int) (*Store, records.Schedule, records.OrganizationTask, time.Time) {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "retry.sqlite"))
	mustMerge(t, e)
	t.Cleanup(func() { s.Close() })
	manual, _, _ := organizationFixture(t, s)
	_, e = s.CancelOrganization(mergeCtx, manual.ID)
	mustMerge(t, e)
	d := records.ScheduleDraft{ID: uuid.NewString(), OperationID: uuid.NewString(), Enabled: true, Spec: records.ScheduleSpec{Project: manual.Project, Executor: "traex", Timezone: "UTC", Frequency: "interval", IntervalHours: 1, WindowMinutes: 60, MaterialLimit: 8, RetryLimit: limit}}
	if limit > 0 {
		d.Spec.RecoveryWindowMinutes = 10
	}
	p, e := s.PutSchedule(mergeCtx, d, time.Now())
	mustMerge(t, e)
	now := time.UnixMilli(p.NextRunMS).Add(time.Second)
	_, task, e := s.FireSchedule(mergeCtx, p.ID, p.Generation, now)
	mustMerge(t, e)
	if task == nil {
		t.Fatal("scheduled task absent")
	}
	if limit > 0 && (task.Recovery == nil || task.Recovery.DeadlineMS != now.Add(10*time.Minute).UnixMilli()) {
		t.Fatal("first run policy/window absent", task.Recovery)
	}
	return s, p, *task, now
}

func recoveryFail(t *testing.T, s *Store, task records.OrganizationTask, failure string, now time.Time) {
	t.Helper()
	owner := uuid.NewString()
	ctx := WithOrganizationLease(mergeCtx, owner)
	mustMerge(t, s.AcquireOrganizationLease(ctx, task.ID, task.Attempt, owner, time.Now()))
	mustMerge(t, s.ClaimOrganization(ctx, task.ID, task.Attempt))
	_, e := s.StageOrganization(ctx, task.ID, task.Attempt, nil, map[string]bool{"protocol_fixture": true}, failure)
	mustMerge(t, e)
	mustMerge(t, s.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
	mustMerge(t, s.ScheduleOrganizationRecovery(mergeCtx, task.ID, task.Snapshot.Attempt, now))
}

func TestOrganizationRecoveryExplicitBudgetBackoffStableTaskAndFiniteAttempts(t *testing.T) {
	s, _, task, now := recoveryFixture(t, 1)
	recoveryFail(t, s, task, "agent_transient", now)
	stored, e := s.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if stored.Recovery.State != "waiting" || stored.Recovery.Used != 0 || stored.Recovery.NextMS != now.Add(15*time.Second).UnixMilli() {
		t.Fatal(stored.Recovery)
	}
	early, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(14*time.Second))
	mustMerge(t, e)
	if early != nil {
		t.Fatal("backoff ignored")
	}
	next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second))
	mustMerge(t, e)
	if next == nil || next.ID != task.ID || next.Attempt != 2 || next.AttemptKind != "model" || next.Snapshot.Digest == task.Snapshot.Digest || next.Snapshot.ProposalID() == task.Snapshot.ProposalID() || next.Recovery.Used != 1 {
		t.Fatal("new attempt receipt", next)
	}
	replay, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second))
	mustMerge(t, e)
	if replay != nil {
		t.Fatal("duplicate retry trigger")
	}
	if _, e = s.StageOrganization(mergeCtx, task.ID, task.Attempt, nil, nil, "agent_transient"); e == nil {
		t.Fatal("old attempt late result accepted")
	}
	recoveryFail(t, s, *next, "agent_timeout", now.Add(20*time.Second))
	stored, e = s.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if stored.Recovery.State != "exhausted" || stored.Recovery.Used != 1 {
		t.Fatal("retry budget not exhausted", stored.Recovery)
	}
	if next, e = s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(time.Hour)); e != nil || next != nil {
		t.Fatal("extra attempt after budget", next, e)
	}
}

func TestOrganizationRecoveryZeroAndPermanentFailureDoNotAuthorizeModel(t *testing.T) {
	for _, failure := range []string{"agent_transient", "authentication_failed", "invalid_proposal", "agent_failed"} {
		t.Run(failure, func(t *testing.T) {
			limit := 1
			if failure == "agent_transient" {
				limit = 0
			}
			s, _, task, now := recoveryFixture(t, limit)
			recoveryFail(t, s, task, failure, now)
			v, e := s.OrganizationTask(mergeCtx, task.ID)
			mustMerge(t, e)
			if limit == 0 && v.Recovery != nil || limit > 0 && v.Recovery.State != "done" {
				t.Fatal("unauthorized failure retry", v.Recovery)
			}
			next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(time.Minute))
			mustMerge(t, e)
			if next != nil {
				t.Fatal("permanent failure rerun")
			}
		})
	}
}

func TestOrganizationRecoverySourceChangedCancelAndManualOverride(t *testing.T) {
	for _, mode := range []string{"source", "cancel", "manual"} {
		t.Run(mode, func(t *testing.T) {
			s, _, task, now := recoveryFixture(t, 1)
			recoveryFail(t, s, task, "agent_transient", now)
			if mode == "source" {
				input := task.Snapshot.Inputs[0]
				_, e := s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: input.Memory, Dataset: task.Project, ExpectedRevision: input.Revision, Text: input.Text + "后来更新"})
				mustMerge(t, e)
			}
			if mode == "cancel" {
				v, e := s.CancelOrganization(mergeCtx, task.ID)
				mustMerge(t, e)
				if !v.Cancelled {
					t.Fatal("waiting retry could not cancel")
				}
			}
			if mode == "manual" {
				d := records.OrganizationStart{OperationID: uuid.NewString(), Project: task.Project}
				for _, input := range task.Snapshot.Inputs {
					d.Inputs = append(d.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
				}
				_, _, e := s.StartOrganization(mergeCtx, d)
				mustMerge(t, e)
			}
			next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second))
			mustMerge(t, e)
			if next != nil {
				t.Fatal("changed/cancelled/overridden task rerun")
			}
			v, e := s.OrganizationTask(mergeCtx, task.ID)
			mustMerge(t, e)
			if mode == "source" && v.Recovery.State != "expired" || mode != "source" && v.Recovery.State != "cancelled" {
				t.Fatal(v.Recovery)
			}
		})
	}
}

func TestOrganizationRecoveryRetainedResultUsesApplyAttemptAndReceipts(t *testing.T) {
	s, _, task, now := recoveryFixture(t, 1)
	owner := uuid.NewString()
	ctx := WithOrganizationLease(mergeCtx, owner)
	mustMerge(t, s.AcquireOrganizationLease(ctx, task.ID, task.Attempt, owner, time.Now()))
	mustMerge(t, s.ClaimOrganization(ctx, task.ID, task.Attempt))
	refs := []records.MergeSource{}
	for _, input := range task.Snapshot.Inputs {
		refs = append(refs, records.MergeSource{Memory: input.Memory, Revision: input.Revision, Quote: input.Text})
	}
	result := records.OrganizationResult{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: task.Attempt, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Items: []records.OrganizationItem{{Index: 1, Action: "summary", Title: "合成摘要", Text: "有出处合成摘要", Tags: []string{}, Sources: refs}, {Index: 2, Action: "add_tags", Tags: []string{"pnpm"}, Target: refs[0].Memory, Sources: refs[:1]}}}
	raw, _ := json.Marshal(result)
	_, e := s.StageOrganization(ctx, task.ID, task.Attempt, raw, nil, "")
	mustMerge(t, e)
	first, e := s.ApplyOrganizationItem(ctx, task.ID, task.Attempt, 1)
	mustMerge(t, e)
	mustMerge(t, s.FailOrganizationItem(ctx, task.ID, task.Attempt, 2))
	_, e = s.FinishOrganization(ctx, task.ID, task.Attempt)
	mustMerge(t, e)
	mustMerge(t, s.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
	mustMerge(t, s.ScheduleOrganizationRecovery(mergeCtx, task.ID, task.Snapshot.Attempt, now))
	next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second))
	mustMerge(t, e)
	if next == nil || next.AttemptKind != "apply" || next.Result == nil || next.Result.ID != result.ID {
		t.Fatal("retained result reran model", next)
	}
	newOwner := uuid.NewString()
	newCtx := WithOrganizationLease(mergeCtx, newOwner)
	mustMerge(t, s.AcquireOrganizationLease(newCtx, next.ID, next.Attempt, newOwner, time.Now()))
	replayed, e := s.ApplyOrganizationItem(newCtx, next.ID, next.Attempt, 1)
	mustMerge(t, e)
	if replayed.Receipt.Memory != first.Receipt.Memory {
		t.Fatal("receipt changed")
	}
	_, e = s.ApplyOrganizationItem(newCtx, next.ID, next.Attempt, 2)
	mustMerge(t, e)
	done, e := s.FinishOrganization(newCtx, next.ID, next.Attempt)
	mustMerge(t, e)
	if done.State != "completed" {
		t.Fatal(done.State)
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM versions WHERE memory=?`, first.Receipt.Memory).Scan(&count))
	if count != 1 {
		t.Fatal("duplicate summary version")
	}
}

func TestOrganizationRecoveryManualStartFencesUnleasedQueuedAttempt(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "unleased", true: "lease_already_acquired"}[owned], func(t *testing.T) {
			s, _, task, now := recoveryFixture(t, 1)
			recoveryFail(t, s, task, "agent_transient", now)
			next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second))
			mustMerge(t, e)
			owner := uuid.NewString()
			ctx := WithOrganizationLease(mergeCtx, owner)
			if owned {
				mustMerge(t, s.AcquireOrganizationLease(ctx, next.ID, next.Attempt, owner, time.Now()))
			}
			d := records.OrganizationStart{OperationID: uuid.NewString(), Project: task.Project}
			for _, input := range task.Snapshot.Inputs {
				d.Inputs = append(d.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
			}
			_, _, e = s.StartOrganization(mergeCtx, d)
			mustMerge(t, e)
			v, e := s.OrganizationTask(mergeCtx, task.ID)
			mustMerge(t, e)
			if owned {
				if v.Recovery.State != "active" || v.Attempt != next.Attempt {
					t.Fatal("manual task preempted owning attempt", v)
				}
			} else {
				if v.Recovery.State != "cancelled" || v.Attempt <= next.Attempt {
					t.Fatal("queued automatic attempt not fenced", v)
				}
				if s.AcquireOrganizationLease(ctx, next.ID, next.Attempt, owner, time.Now()) == nil {
					t.Fatal("superseded queued attempt got owner")
				}
			}
		})
	}
}

func TestOrganizationRecoverySourcesRecheckedAtClaimAndPermission(t *testing.T) {
	for _, boundary := range []string{"claim", "permission"} {
		t.Run(boundary, func(t *testing.T) {
			s, _, task, now := recoveryFixture(t, 1)
			recoveryFail(t, s, task, "agent_transient", now)
			next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second))
			mustMerge(t, e)
			owner := uuid.NewString()
			ctx := WithOrganizationLease(mergeCtx, owner)
			mustMerge(t, s.AcquireOrganizationLease(ctx, next.ID, next.Attempt, owner, time.Now()))
			if boundary == "permission" {
				mustMerge(t, s.ClaimOrganization(ctx, next.ID, next.Attempt))
			}
			input := next.Snapshot.Inputs[0]
			_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: input.Memory, Dataset: task.Project, ExpectedRevision: input.Revision, Text: input.Text + "来源后来变化"})
			mustMerge(t, e)
			if boundary == "claim" {
				e = s.ClaimOrganization(ctx, next.ID, next.Attempt)
			} else {
				e = s.RegisterOrganizationProcess(ctx, next.ID, next.Attempt, 123456)
			}
			if e == nil {
				t.Fatal("stale automatic source reached model permission")
			}
			v, e := s.OrganizationTask(mergeCtx, task.ID)
			mustMerge(t, e)
			if v.Recovery.State != "expired" || v.Error != "proposal_expired" {
				t.Fatal(v.Recovery, v.Error)
			}
		})
	}
}

func TestOrganizationRecoveryStopWatermarkCancelsOldButNotNew(t *testing.T) {
	s, _, task, now := recoveryFixture(t, 1)
	recoveryFail(t, s, task, "agent_transient", now)
	var created int64
	mustMerge(t, s.DB.QueryRow(`SELECT created_ms FROM organization_recovery WHERE task=?`, task.ID).Scan(&created))
	mustMerge(t, s.CancelRecoveriesBefore(mergeCtx, created))
	v, e := s.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if v.Recovery.State != "cancelled" || v.Recovery.Reason != "user_stopped" {
		t.Fatal(v.Recovery)
	}
	if next, e := s.BeginOrganizationRecovery(mergeCtx, task.ID, now.Add(16*time.Second)); e != nil || next != nil {
		t.Fatal("stopped task restored")
	}
	// A later authorized task has its own exact creation watermark. Replaying
	// the old stop must leave that new task's policy active.
	_, e = s.DB.Exec(`UPDATE organization_recovery SET created_ms=?,state='active',reason='' WHERE task=?`, created+1, task.ID)
	mustMerge(t, e)
	mustMerge(t, s.CancelRecoveriesBefore(mergeCtx, created))
	v, e = s.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if v.Recovery.State != "active" {
		t.Fatal("old stop suppressed newer authorization")
	}
}

func TestOrganizationRecoveryKnownPermanentOutcomeSurvivesStageWriteFailure(t *testing.T) {
	s, _, task, now := recoveryFixture(t, 1)
	owner := uuid.NewString()
	ctx := WithOrganizationLease(mergeCtx, owner)
	mustMerge(t, s.AcquireOrganizationLease(ctx, task.ID, task.Attempt, owner, time.Now()))
	mustMerge(t, s.ClaimOrganization(ctx, task.ID, task.Attempt))
	_, e := s.DB.Exec(`UPDATE organization_attempts SET execution='{"outcome_recorded":true,"outcome_code":"invalid_proposal"}' WHERE task=?`, task.ID)
	mustMerge(t, e)
	_, e = s.DB.Exec(`CREATE TRIGGER recovery_stage_failure BEFORE UPDATE ON organization_tasks WHEN NEW.state='failed' BEGIN SELECT RAISE(ABORT,'explicit Stage fault'); END`)
	mustMerge(t, e)
	if _, e = s.StageOrganization(ctx, task.ID, task.Attempt, nil, nil, "invalid_proposal"); e == nil {
		t.Fatal("Stage fault absent")
	}
	_, e = s.DB.Exec(`DROP TRIGGER recovery_stage_failure`)
	mustMerge(t, e)
	mustMerge(t, s.InterruptOrganizations(mergeCtx))
	mustMerge(t, s.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
	mustMerge(t, s.ScheduleOrganizationRecovery(mergeCtx, task.ID, task.Snapshot.Attempt, now))
	v, e := s.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	if v.State != "failed" || v.Error != "invalid_proposal" || v.Recovery.State != "done" || v.Recovery.Used != 0 {
		t.Fatal("known permanent outcome upgraded to retry", v)
	}
}
