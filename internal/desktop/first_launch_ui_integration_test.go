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
	"github.com/zwhu/memlane/internal/agents"
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

func TestFirstLaunchDiscoveryIsCompactAndDetailsDoNotAuthorizePreparation(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: t.TempDir(), WorkerID: "cognee-rust", ModelID: "e5-base"}}
	a.firstLaunch.State.Stage = "results"
	for _, agent := range []string{"codex", "pi", "traex", "trae"} {
		a.firstLaunch.State.Agents = append(a.firstLaunch.State.Agents, agents.Discovery{Agent: agent, Name: agent, Path: "/synthetic/" + agent, Version: "synthetic-frame-only"})
		a.firstLaunch.State.Items = append(a.firstLaunch.State.Items, EnvironmentItem{Name: agent, State: "installed", Evidence: "synthetic-details-only"})
	}
	tt := ui.NewTester(a.View, 1200, 820)
	button, ok := tt.Find("继续")
	if !ok || button.Y+button.H > 820 {
		t.Fatal("primary action is outside the window")
	}
	details, ok := tt.Find("查看检查详情")
	if !ok || details.Y+details.H > button.Y {
		t.Fatal("default discovery requires scrolling to reach its final row")
	}
	if tt.HasText("synthetic-details-only") {
		t.Fatal("technical evidence is expanded by default")
	}
	if err := tt.Click("查看检查详情"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("synthetic-details-only") || a.firstLaunch.State.Stage != "results" || a.firstLaunch.State.Attempt != 0 {
		t.Fatal("expanding details changed preparation scope or failed to display evidence")
	}
	if err := tt.Click("收起检查详情"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("继续"); err != nil {
		t.Fatal(err)
	}
	if a.firstLaunch.State.Stage != "confirmation" || a.firstLaunch.State.Attempt != 0 {
		t.Fatal("reviewing the plan started preparation")
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	if a.firstLaunch.State.Stage != "results" || a.firstLaunch.State.Completed {
		t.Fatal("returning from confirmation lost the discovery state")
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

func TestFirstLaunchFailureRetainsConcreteControllerError(t *testing.T) {
	a := New("test", nil)
	root := t.TempDir()
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: root}}
	a.firstLaunch.State.Stage = "preparing"
	a.SetRuntimeStatus(RuntimeStatus{Phase: "failed", Message: "正在准备本地引擎与模型"})
	a.SetRuntimeStatus(RuntimeStatus{Phase: "failed", Message: "组件下载失败：HTTP 503"})
	a.SetRuntimeStatus(RuntimeStatus{Phase: "ready", Installed: true})
	if a.firstLaunch.State.Stage != "failed" || a.firstLaunch.State.Message != "组件下载失败：HTTP 503" {
		t.Fatal("concrete failure lost or late ready replaced failed preparation")
	}
	var state FirstLaunchState
	raw, err := os.ReadFile(filepath.Join(root, "first-launch.json"))
	if err != nil || json.Unmarshal(raw, &state) != nil || state.Message != "组件下载失败：HTTP 503" {
		t.Fatal("concrete error is not persisted")
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
