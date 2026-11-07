package store

// Real SQLite ownership/atomicity tests. Model results and lease expiry are
// explicitly marked fixtures; normal App/Agent acceptance is separate.
import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestOrganizationLeaseRejectsWrongMissingExpiredOwnerAndLateResult(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "lease.sqlite"))
	mustMerge(t, e)
	defer s.Close()
	task, _, raw := organizationFixture(t, s)
	owner := uuid.NewString()
	mustMerge(t, s.AcquireOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, time.Now()))
	owned := WithOrganizationLease(mergeCtx, owner)
	if s.ClaimOrganization(mergeCtx, task.ID, task.Attempt) == nil || s.ClaimOrganization(WithOrganizationLease(mergeCtx, uuid.NewString()), task.ID, task.Attempt) == nil {
		t.Fatal("missing/wrong owner claimed")
	}
	mustMerge(t, s.ClaimOrganization(owned, task.ID, task.Attempt))
	mustMerge(t, s.RegisterOrganizationProcess(owned, task.ID, task.Attempt, 123456))
	if s.RegisterOrganizationProcess(owned, task.ID, task.Attempt, 123457) == nil {
		t.Fatal("process identity replaced")
	}
	_, e = s.DB.Exec(`UPDATE organization_leases SET expires_ms=? WHERE task=?`, time.Now().Add(-time.Second).UnixMilli(), task.ID)
	mustMerge(t, e)
	if s.RenewOrganizationLease(owned, task.ID, task.Attempt, owner, time.Now()) == nil {
		t.Fatal("expired lease revived")
	}
	if _, e = s.StageOrganization(owned, task.ID, task.Attempt, raw, map[string]bool{"fixture": true}, ""); e == nil {
		t.Fatal("expired owner staged")
	}
	if s.AcquireOrganizationLease(mergeCtx, task.ID, task.Attempt, uuid.NewString(), time.Now()) == nil {
		t.Fatal("old attempt lease taken over")
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM organization_results WHERE task=?`, task.ID).Scan(&count))
	if count != 0 {
		t.Fatal("late result persisted")
	}
	mustMerge(t, s.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
	mustMerge(t, s.InterruptOrganization(mergeCtx, task.ID, task.Attempt))
	if _, e = s.StageOrganization(owned, task.ID, task.Attempt, raw, nil, ""); e == nil {
		t.Fatal("old attempt accepted after fence")
	}
}

func TestOrganizationLeasePartialReceiptSurvivesNewApplyAttempt(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "partial.sqlite"))
	mustMerge(t, e)
	defer s.Close()
	task, _, raw := organizationFixture(t, s)
	owner := uuid.NewString()
	owned := WithOrganizationLease(context.Background(), owner)
	mustMerge(t, s.AcquireOrganizationLease(owned, task.ID, task.Attempt, owner, time.Now()))
	mustMerge(t, s.ClaimOrganization(owned, task.ID, task.Attempt))
	_, e = s.StageOrganization(owned, task.ID, task.Attempt, raw, nil, "")
	mustMerge(t, e)
	first, e := s.ApplyOrganizationItem(owned, task.ID, task.Attempt, 1)
	mustMerge(t, e)
	_, e = s.DB.Exec(`UPDATE organization_leases SET expires_ms=? WHERE task=?`, time.Now().Add(-time.Second).UnixMilli(), task.ID)
	mustMerge(t, e)
	if _, e = s.ApplyOrganizationItem(owned, task.ID, task.Attempt, 2); e == nil {
		t.Fatal("expired owner applied")
	}
	if s.FailOrganizationItem(owned, task.ID, task.Attempt, 2) == nil {
		t.Fatal("expired owner marked failure")
	}
	if _, e = s.FinishOrganization(owned, task.ID, task.Attempt); e == nil {
		t.Fatal("expired owner finished")
	}
	replay, e := s.ApplyOrganizationItem(mergeCtx, task.ID, task.Attempt, 1)
	mustMerge(t, e)
	if replay.Receipt.Memory != first.Receipt.Memory {
		t.Fatal("committed receipt lost")
	}
	mustMerge(t, s.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
	mustMerge(t, s.InterruptOrganization(mergeCtx, task.ID, task.Attempt))
	current, e := s.OrganizationTask(mergeCtx, task.ID)
	mustMerge(t, e)
	resumed, created, e := s.ResumeOrganization(mergeCtx, records.OrganizationResume{Task: task.ID, OperationID: uuid.NewString(), ExpectedAttempt: current.Attempt})
	mustMerge(t, e)
	if !created || resumed.Result == nil || resumed.AttemptKind != "apply" {
		t.Fatal("model re-run instead of retained result")
	}
	newOwner := uuid.NewString()
	newCtx := WithOrganizationLease(mergeCtx, newOwner)
	mustMerge(t, s.AcquireOrganizationLease(newCtx, resumed.ID, resumed.Attempt, newOwner, time.Now()))
	if s.RenewOrganizationLease(owned, task.ID, task.Attempt, owner, time.Now()) == nil {
		t.Fatal("old owner renewed")
	}
	_, e = s.ApplyOrganizationItem(newCtx, resumed.ID, resumed.Attempt, 1)
	mustMerge(t, e)
	_, e = s.ApplyOrganizationItem(newCtx, resumed.ID, resumed.Attempt, 2)
	mustMerge(t, e)
	done, e := s.FinishOrganization(newCtx, resumed.ID, resumed.Attempt)
	mustMerge(t, e)
	if done.State != "completed" {
		t.Fatal(done.State)
	}
	var versions, events int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM versions WHERE memory=?`, first.Receipt.Memory).Scan(&versions))
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM tag_events WHERE item=?`, done.Entries[1].ID).Scan(&events))
	if versions != 1 || events != 2 {
		t.Fatal("duplicate fact on apply recovery", versions, events)
	}
}

func TestOrganizationLeaseCancellationFencesRenewalAndProcessRegistration(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "cancel.sqlite"))
	mustMerge(t, e)
	defer s.Close()
	task, _, raw := organizationFixture(t, s)
	owner := uuid.NewString()
	ctx := WithOrganizationLease(mergeCtx, owner)
	mustMerge(t, s.AcquireOrganizationLease(ctx, task.ID, task.Attempt, owner, time.Now()))
	mustMerge(t, s.ClaimOrganization(ctx, task.ID, task.Attempt))
	_, e = s.CancelOrganization(mergeCtx, task.ID)
	mustMerge(t, e)
	if s.RenewOrganizationLease(ctx, task.ID, task.Attempt, owner, time.Now()) == nil || s.RegisterOrganizationProcess(ctx, task.ID, task.Attempt, 123456) == nil {
		t.Fatal("cancelled attempt retained ownership")
	}
	if _, e = s.StageOrganization(ctx, task.ID, task.Attempt, raw, nil, ""); e == nil {
		t.Fatal("cancelled attempt staged")
	}
	mustMerge(t, s.ReleaseOrganizationLease(mergeCtx, task.ID, task.Attempt, owner, true, true))
}
