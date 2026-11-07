package daemon

// SQL/controller/clock fixtures, no model subprocess. These check outcomes
// that must never turn a known permanent failure into another paid attempt.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func TestOrganizationRecoveryMemoryOutcomeSurvivesTerminalSQLFailure(t *testing.T) {
	s, d, _ := scheduleControllerFixture(t)
	ctx := context.Background()
	d.Spec.RetryLimit = 1
	d.Spec.RecoveryWindowMinutes = 10
	p, e := s.store.PutSchedule(ctx, d, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	now := time.UnixMilli(p.NextRunMS).Add(time.Second)
	_, task, e := s.store.FireSchedule(ctx, p.ID, p.Generation, now)
	if e != nil {
		t.Fatal(e)
	}
	owner := uuid.NewString()
	owned := store.WithOrganizationLease(ctx, owner)
	if e = s.store.AcquireOrganizationLease(owned, task.ID, task.Attempt, owner, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = s.store.ClaimOrganization(owned, task.ID, task.Attempt); e != nil {
		t.Fatal(e)
	}
	_, e = s.store.DB.Exec(`CREATE TRIGGER terminal_sql_fault BEFORE UPDATE ON organization_tasks WHEN NEW.state='failed' BEGIN SELECT RAISE(ABORT,'terminal SQL fixture'); END`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.StageOrganization(owned, task.ID, task.Attempt, nil, nil, "invalid_proposal"); e == nil {
		t.Fatal("terminal fault absent")
	}
	s.organizationOutcomes = map[string]organizationOutcome{task.ID: {Attempt: task.Attempt, Known: true, Failure: "invalid_proposal"}}
	s.organizationRuns[task.ID] = organizationOwner{Attempt: task.Attempt, Cancel: func() {}}
	if e = s.store.ReleaseOrganizationLease(ctx, task.ID, task.Attempt, owner, true, true); e != nil {
		t.Fatal(e)
	}
	s.finishOrganizationRun(*task)
	if _, ok := s.organizationOutcomes[task.ID]; !ok {
		t.Fatal("known outcome lost while storage unavailable")
	}
	if e = s.finalizeOrganizationShutdown(ctx); e == nil {
		t.Fatal("unwritable shutdown silently succeeded")
	}
	_, e = s.store.DB.Exec(`DROP TRIGGER terminal_sql_fault`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.finalizeOrganizationShutdown(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.store.ScheduleOrganizationRecovery(ctx, task.ID, task.Snapshot.Attempt, now); e != nil {
		t.Fatal(e)
	}
	v, e := s.store.OrganizationTask(ctx, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if v.Error != "invalid_proposal" || v.State != "failed" || v.Recovery.State != "done" {
		t.Fatal("permanent error became generic retry", v)
	}
	next, e := s.store.BeginOrganizationRecovery(ctx, task.ID, now.Add(time.Minute))
	if e != nil || next != nil {
		t.Fatal("permanent error produced model attempt", next, e)
	}
}

func TestOrganizationRecoveryStopEpochSurvivesResumeAndSameMillisecond(t *testing.T) {
	s, d, _ := scheduleControllerFixture(t)
	ctx := context.Background()
	d.Spec.RetryLimit = 1
	d.Spec.RecoveryWindowMinutes = 10
	p, e := s.store.PutSchedule(ctx, d, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	now := time.UnixMilli(p.NextRunMS).Add(time.Second)
	_, old, e := s.store.FireSchedule(ctx, p.ID, p.Generation, now)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	if e = serviceipc.MarkStopped(root); e != nil {
		t.Fatal(e)
	}
	read := func() serviceipc.State {
		b, e := os.ReadFile(filepath.Join(root, "service-state.json"))
		if e != nil {
			t.Fatal(e)
		}
		var st serviceipc.State
		if e = json.Unmarshal(b, &st); e != nil {
			t.Fatal(e)
		}
		return st
	}
	st := read()
	if !st.Stopped || st.StopGeneration != 1 {
		t.Fatal(st)
	}
	if e = serviceipc.Resume(root); e != nil {
		t.Fatal(e)
	}
	resumed := read()
	if resumed.Stopped || resumed.StopGeneration != st.StopGeneration || resumed.LastStoppedMS != st.LastStoppedMS {
		t.Fatal("resume discarded stop boundary", resumed)
	}
	if e = s.store.CancelRecoveriesForStop(ctx, resumed.LastStoppedMS, resumed.StopGeneration); e != nil {
		t.Fatal(e)
	}
	v, e := s.store.OrganizationTask(ctx, old.ID)
	if e != nil {
		t.Fatal(e)
	}
	if v.Recovery.State != "cancelled" {
		t.Fatal("old stopped task restored", v.Recovery)
	}
	// A new consent in the resumed epoch is independent even if the wall-clock
	// millisecond equals the older watermark. No model runs in this fixture.
	_, e = s.store.DB.Exec(`UPDATE organization_recovery SET state='active',created_ms=?,stop_generation=? WHERE task=?`, resumed.LastStoppedMS, resumed.StopGeneration, old.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.store.CancelRecoveriesForStop(ctx, resumed.LastStoppedMS, resumed.StopGeneration); e != nil {
		t.Fatal(e)
	}
	v, e = s.store.OrganizationTask(ctx, old.ID)
	if e != nil {
		t.Fatal(e)
	}
	if v.Recovery.State != "active" {
		t.Fatal("old watermark suppressed new epoch", v.Recovery)
	}
	// A clock rollback cannot suppress consent created in the resumed epoch.
	_, e = s.store.DB.Exec(`UPDATE organization_recovery SET created_ms=? WHERE task=?`, resumed.LastStoppedMS-3600000, old.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.store.CancelRecoveriesForStop(ctx, resumed.LastStoppedMS, resumed.StopGeneration); e != nil {
		t.Fatal(e)
	}
	v, e = s.store.OrganizationTask(ctx, old.ID)
	if e != nil || v.Recovery.State != "active" {
		t.Fatal("clock rollback suppressed resumed epoch", v.Recovery, e)
	}
	if e = serviceipc.MarkStopped(root); e != nil {
		t.Fatal(e)
	}
	st = read()
	if st.StopGeneration != 2 {
		t.Fatal("stop epoch did not advance", st)
	}
	if e = s.store.CancelRecoveriesForStop(ctx, st.LastStoppedMS, st.StopGeneration); e != nil {
		t.Fatal(e)
	}
	v, e = s.store.OrganizationTask(ctx, old.ID)
	if e != nil {
		t.Fatal(e)
	}
	if v.Recovery.State != "cancelled" {
		t.Fatal("new stop did not suppress current epoch")
	}
}

func TestOrganizationRecoveryErrorClassificationIsConservative(t *testing.T) {
	for _, message := range []string{"authentication failed: connection reset", "quota exceeded", "invalid proposal", "HTTP 429 rate limit", "unsupported model", "unknown process failure"} {
		if transientOrganizationError(errors.New(message)) {
			t.Fatal("permanent/unknown error retried", message)
		}
	}
	for _, message := range []string{"connection reset by peer", "HTTP 503 service unavailable", "TLS handshake timeout"} {
		if !transientOrganizationError(errors.New(message)) {
			t.Fatal("known transient not classified", message)
		}
	}
}
