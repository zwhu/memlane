package desktop

import (
	"context"
	"testing"

	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestMaintenanceRetirementCannotKeepOldRetryBarrierOrReceipt(t *testing.T) {
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "retry" || !r.UserConfirmed {
			t.Errorf("unexpected request %#v", r)
		}
		return serviceipc.Response{OK: true, Result: map[string]any{"retry_requested": true, "recovery_owner": "old-owner", "recovery_generation": 2}}
	})
	a.runtimeStatus = RuntimeStatus{Phase: "unavailable", Installed: true}
	a.serviceState = "failed"
	a.maintenanceNext.Observe = func(context.Context) (RuntimeAvailability, error) { return RuntimeAvailability{}, nil }
	a.maintenanceNext.OpenSettings = func() error { return nil }
	a.maintenanceNext.Reopen = func() {}
	a.retryMaintenanceService()
	if !a.maintenanceNext.Busy {
		t.Fatal("retry not started")
	}
	a.libraryEpoch++
	a.retireMaintenanceRecoveryState()
	a.maintenanceNext.Notice = "replacement library"
	awaitExperienceDispatch(t, dispatch)
	n := a.maintenanceNext
	if n.Busy || n.Awaiting || n.WaitingOwner != "" || n.Notice != "replacement library" || n.Storage != nil {
		t.Fatal("old retry changed replacement state", n)
	}
	if n.Observe == nil || n.OpenSettings == nil || n.Reopen == nil {
		t.Fatal("retirement removed configured platform hooks")
	}
}

func TestAvailabilityObservationCannotEnterAnotherLibrary(t *testing.T) {
	a := New("test", nil)
	a.library.Root = "replacement-root"
	a.runtimeStatus = RuntimeStatus{Phase: "unavailable", Installed: true}
	a.ApplyRuntimeAvailability(RuntimeAvailability{LibraryRoot: "old-root", Phase: "ready", Message: "old ready", RecoveryOwner: "old-owner", Recovery: "exhausted"})
	if a.runtimeStatus.Phase != "unavailable" || a.maintenanceNext.Recovery != "" {
		t.Fatal("old root observation entered new library")
	}
	a.ApplyRuntimeAvailability(RuntimeAvailability{LibraryRoot: "replacement-root", Phase: "ready", RecoveryOwner: "new-owner"})
	if a.runtimeStatus.Phase != "ready" {
		t.Fatal("current root observation was rejected")
	}
}
