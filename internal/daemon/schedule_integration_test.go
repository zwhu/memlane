package daemon

// Real SQLite/controller fixtures, without model subprocesses. Clock changes
// here are inputs to Store only; the system clock is never modified.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func scheduleControllerFixture(t *testing.T) (*Server, records.ScheduleDraft, time.Time) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "plan.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	project := uuid.NewString()
	_, e = db.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','plan fixture','/synthetic/plan')`, project)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "项目使用pnpm。"})
	if e != nil {
		t.Fatal(e)
	}
	d := records.ScheduleDraft{ID: uuid.NewString(), OperationID: uuid.NewString(), Enabled: true, Spec: records.ScheduleSpec{Project: project, Executor: "traex", Timezone: "UTC", Frequency: "daily", LocalTime: "10:00", WindowMinutes: 60, MaterialLimit: 8}}
	s := &Server{store: db, lifecycle: ctx, organizationRuns: map[string]organizationOwner{}}
	return s, d, time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
}

func TestScheduleControllerReplayDoesNotCancelNewGeneration(t *testing.T) {
	s, d, now := scheduleControllerFixture(t)
	ctx := context.Background()
	first, e := s.store.PutSchedule(ctx, d, now)
	if e != nil {
		t.Fatal(e)
	}
	_, old, e := s.store.FireSchedule(ctx, first.ID, first.Generation, time.UnixMilli(first.NextRunMS).Add(time.Second))
	if e != nil {
		t.Fatal(e)
	}
	disabled := d
	disabled.OperationID = uuid.NewString()
	disabled.ExpectedGeneration = 1
	disabled.Enabled = false
	_, e = s.store.PutSchedule(ctx, disabled, now)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.store.CancelOrganization(ctx, old.ID)
	if e != nil {
		t.Fatal(e)
	}
	newDraft := d
	newDraft.OperationID = uuid.NewString()
	newDraft.ExpectedGeneration = 2
	current, e := s.store.PutSchedule(ctx, newDraft, now)
	if e != nil {
		t.Fatal(e)
	}
	_, task, e := s.store.FireSchedule(ctx, current.ID, current.Generation, time.UnixMilli(current.NextRunMS).Add(time.Second))
	if e != nil {
		t.Fatal(e)
	}
	cancelled := 0
	s.organizationRuns[task.ID] = organizationOwner{Attempt: task.Attempt, Cancel: func() { cancelled++ }}
	replay, e := s.putSchedule(ctx, d)
	if e != nil {
		t.Fatal(e)
	}
	active, e := s.store.OrganizationTask(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if replay.Generation != 1 || cancelled != 0 || active.State != "queued" {
		t.Fatal("historical plan receipt canceled newer run", replay, cancelled, active.State)
	}
	newDraft.Enabled = false
	newDraft.OperationID = uuid.NewString()
	newDraft.ExpectedGeneration = current.Generation
	_, e = s.putSchedule(ctx, newDraft)
	if e != nil {
		t.Fatal(e)
	}
	active, e = s.store.OrganizationTask(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if cancelled != 1 || !active.Cancelled {
		t.Fatal("explicit disable failed to cancel current generation")
	}
}

func TestScheduleDisableFencesClaimStageAndAutomaticApply(t *testing.T) {
	for _, phase := range []string{"claim", "stage", "apply"} {
		t.Run(phase, func(t *testing.T) {
			s, d, now := scheduleControllerFixture(t)
			ctx := context.Background()
			plan, e := s.store.PutSchedule(ctx, d, now)
			if e != nil {
				t.Fatal(e)
			}
			_, task, e := s.store.FireSchedule(ctx, plan.ID, plan.Generation, time.UnixMilli(plan.NextRunMS).Add(time.Second))
			if e != nil {
				t.Fatal(e)
			}
			source := task.Snapshot.Inputs[0]
			r := records.OrganizationResult{Schema: task.Snapshot.Schema, Task: task.ID, Attempt: task.Attempt, Digest: task.Snapshot.Digest, ID: task.Snapshot.ProposalID(), Explanation: "protocol fault fixture", Items: []records.OrganizationItem{{Index: 1, Action: "summary", Title: "合成摘要", Text: "项目使用pnpm。", Tags: []string{}, Sources: []records.MergeSource{{Memory: source.Memory, Revision: source.Revision, Quote: source.Text}}}}}
			raw, _ := json.Marshal(r)
			if phase != "claim" {
				if e = s.store.ClaimOrganization(ctx, task.ID, task.Attempt); e != nil {
					t.Fatal(e)
				}
			}
			if phase == "apply" {
				if _, e = s.store.StageOrganization(ctx, task.ID, task.Attempt, raw, nil, ""); e != nil {
					t.Fatal(e)
				}
			}
			d.Enabled = false
			d.OperationID = uuid.NewString()
			d.ExpectedGeneration = plan.Generation
			if _, e = s.store.PutSchedule(ctx, d, now); e != nil {
				t.Fatal(e)
			}
			switch phase {
			case "claim":
				e = s.store.ClaimOrganization(ctx, task.ID, task.Attempt)
			case "stage":
				_, e = s.store.StageOrganization(ctx, task.ID, task.Attempt, raw, nil, "")
			case "apply":
				_, e = s.store.ApplyOrganizationItem(ctx, task.ID, task.Attempt, 1)
			}
			if e == nil {
				t.Fatal("disabled plan accepted late " + phase)
			}
			var count int
			if e = s.store.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&count); e != nil || count != 1 {
				t.Fatal("disabled plan changed knowledge", count, e)
			}
		})
	}
}

func TestOrganizerSlotCancellationReleasesWaitingWork(t *testing.T) {
	s := &Server{}
	release, e := s.acquireOrganizer(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.acquireOrganizer(ctx); e == nil {
		t.Fatal("canceled waiter entered occupied model slot")
	}
	release()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	release, e = s.acquireOrganizer(ctx)
	if e != nil {
		t.Fatal("slot not released", e)
	}
	release()
}
