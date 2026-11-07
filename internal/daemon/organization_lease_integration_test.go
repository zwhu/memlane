package daemon

// Real process/SQLite fixtures, no Agent or model. Startup must observe group
// disappearance, persist it, and never signal an arbitrary recorded PID.
import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestOrganizationLeaseStartupBlocksLiveGroupAndPersistsReleasedCleanup(t *testing.T) {
	s, d, now := scheduleControllerFixture(t)
	ctx := context.Background()
	p, e := s.store.PutSchedule(ctx, d, now)
	if e != nil {
		t.Fatal(e)
	}
	_, task, e := s.store.FireSchedule(ctx, p.ID, p.Generation, time.UnixMilli(p.NextRunMS).Add(time.Second))
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
	cmd := exec.Command("/bin/sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if e = s.store.RegisterOrganizationProcess(owned, task.ID, task.Attempt, cmd.Process.Pid); e != nil {
		t.Fatal(e)
	}
	// Simulate a previous cleanup which reaped its leader but had not yet
	// observed the entire group gone. Released must not mean gone.
	if e = s.store.ReleaseOrganizationLease(ctx, task.ID, task.Attempt, owner, true, false); e != nil {
		t.Fatal(e)
	}
	if e = prepareOrganizationLeases(ctx, s.store); e == nil {
		t.Fatal("live old group did not block startup")
	}
	if cmd.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("startup signaled an existing group")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if e = prepareOrganizationLeases(ctx, s.store); e != nil {
		t.Fatal(e)
	}
	var released, gone bool
	var expiry int64
	if e = s.store.DB.QueryRow(`SELECT released,group_gone,expires_ms FROM organization_leases WHERE task=?`, task.ID).Scan(&released, &gone, &expiry); e != nil {
		t.Fatal(e)
	}
	if !released || !gone || expiry != 0 {
		t.Fatal("observed cleanup not persisted", released, gone, expiry)
	}
	if e = s.store.InterruptOrganizations(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.store.ClaimOrganization(owned, task.ID, task.Attempt); e == nil {
		t.Fatal("old owner claimed after restart")
	}
}

func TestOrganizationLeaseStartupTracksLegacyMergeGuardGroups(t *testing.T) {
	s, d, _ := scheduleControllerFixture(t)
	ctx := context.Background()
	second := uuid.NewString()
	if _, e := s.store.Apply(ctx, records.Mutation{Kind: "save", Memory: second, OperationID: uuid.NewString(), Dataset: d.Spec.Project, Text: "合成补充来源"}); e != nil {
		t.Fatal(e)
	}
	rows, e := s.store.DB.Query(`SELECT id,current_revision FROM memories WHERE dataset=?`, d.Spec.Project)
	if e != nil {
		t.Fatal(e)
	}
	start := records.MergeStart{OperationID: uuid.NewString(), Project: d.Spec.Project}
	for rows.Next() {
		var ref records.MergeTarget
		if e = rows.Scan(&ref.Memory, &ref.Revision); e != nil {
			t.Fatal(e)
		}
		start.Inputs = append(start.Inputs, ref)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	task, _, e := s.store.StartMerge(ctx, start)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("/bin/sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if _, e = s.store.DB.Exec(`UPDATE merge_attempts SET execution=json_object('pid',?,'process_started',json('true'),'process_group_gone',json('false')) WHERE task=?`, cmd.Process.Pid, task.ID); e != nil {
		t.Fatal(e)
	}
	if e = prepareOrganizationLeases(ctx, s.store); e == nil {
		t.Fatal("live old legacy group untracked")
	}
	if cmd.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("unowned process signaled")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if e = prepareOrganizationLeases(ctx, s.store); e != nil {
		t.Fatal(e)
	}
	var observed bool
	if e = s.store.DB.QueryRow(`SELECT json_extract(execution,'$.startup_group_gone') FROM merge_attempts WHERE task=?`, task.ID).Scan(&observed); e != nil {
		t.Fatal(e)
	}
	if !observed {
		t.Fatal("legacy group observation not persisted")
	}
}

func TestOrganizationLeasePlanChangeFencesFailureFinishAndRenew(t *testing.T) {
	s, d, now := scheduleControllerFixture(t)
	ctx := context.Background()
	p, e := s.store.PutSchedule(ctx, d, now)
	if e != nil {
		t.Fatal(e)
	}
	_, task, e := s.store.FireSchedule(ctx, p.ID, p.Generation, time.UnixMilli(p.NextRunMS).Add(time.Second))
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
	// Freeze a successful empty result: it would normally finish applying.
	_, e = s.store.DB.Exec(`UPDATE organization_tasks SET state='applying' WHERE id=?; UPDATE organization_attempts SET state='applying' WHERE task=?`, task.ID, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	d.OperationID = uuid.NewString()
	d.ExpectedGeneration = 1
	d.Enabled = false
	if _, e = s.store.PutSchedule(ctx, d, now); e != nil {
		t.Fatal(e)
	}
	if e = s.store.RenewOrganizationLease(owned, task.ID, task.Attempt, owner, time.Now()); e == nil {
		t.Fatal("old plan renewed")
	}
	if e = s.store.FailOrganizationItem(owned, task.ID, task.Attempt, 1); e == nil {
		t.Fatal("old plan failure write allowed")
	}
	if _, e = s.store.FinishOrganization(owned, task.ID, task.Attempt); e == nil {
		t.Fatal("old plan finished")
	}
	var state string
	if e = s.store.DB.QueryRow(`SELECT state FROM organization_tasks WHERE id=?`, task.ID).Scan(&state); e != nil {
		t.Fatal(e)
	}
	if state != "applying" {
		t.Fatal("old plan state changed", state)
	}
}
