package desktop

// Native frame and lifecycle state checks only. These do not represent real
// component preparation, system registration or official Agent acceptance.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestFirstLaunchNativeFramesAndReadyActionsRequireActualAvailability(t *testing.T) {
	for _, stage := range []string{"discovering", "results", "confirmation", "preparing", "verifying", "ready", "failed", "cancelled", "interrupted"} {
		t.Run(stage, func(t *testing.T) {
			a := New("test", nil)
			a.firstLaunch.Open = true
			a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: t.TempDir()}}
			a.firstLaunch.Context = context.Background()
			a.firstLaunch.State.Stage = stage
			a.firstLaunch.State.ReducedMotion = true
			tt := ui.NewTester(a.View, 1200, 820)
			if stage == "ready" {
				if err := tt.Click("选择项目并开始记录"); err != nil {
					t.Fatal(err)
				}
				if a.firstLaunch.State.Completed || a.firstLaunch.State.Stage != "failed" {
					t.Fatal("unavailable service marked usable")
				}
			}
		})
	}
}

func TestFirstLaunchConfirmedRecoveryUsesDurableStopMarker(t *testing.T) {
	a := New("test", nil)
	root := t.TempDir()
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: root}}
	a.firstLaunch.State.Stage = "confirmation"
	if err := serviceipc.MarkStopped(filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	prepared, resumed := false, false
	a.prepareRuntime = func() { prepared = true }
	a.resumeRuntime = func() { resumed = true }
	// A reopened App has no previous transient runtime phase. The user's
	// explicit preparation confirmation must consult the durable stop marker.
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("确认并准备本机环境"); err != nil {
		t.Fatal(err)
	}
	if !resumed || prepared {
		t.Fatal("confirmed recovery did not resume the durably stopped service")
	}
}

func TestFirstLaunchCancelLateReadyCannotCompleteAndStateIsDurable(t *testing.T) {
	a := New("test", nil)
	root := t.TempDir()
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: root}}
	a.firstLaunch.Context = context.Background()
	a.firstLaunch.State.Stage = "cancelling"
	a.SetRuntimeStatus(RuntimeStatus{Phase: "ready", Installed: true})
	if a.firstLaunch.State.Stage != "cancelling" || a.firstLaunch.State.Completed {
		t.Fatal("late ready replaced cancellation")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "stopped", Installed: true})
	if a.firstLaunch.State.Stage != "cancelled" {
		t.Fatal("cancellation did not wait for stop receipt")
	}
	loaded := FirstLaunchState{}
	raw, err := os.ReadFile(filepath.Join(root, "first-launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Stage != "cancelled" || loaded.Completed {
		t.Fatal("cancel state not durable")
	}
}
