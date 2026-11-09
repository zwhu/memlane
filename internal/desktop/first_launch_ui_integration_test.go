package desktop

// Native frame and lifecycle state checks only. These do not represent real
// component preparation, system registration or official Agent acceptance.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/records"
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
				if err := tt.Click("继续连接 Agent"); err != nil {
					t.Fatal(err)
				}
				if a.firstLaunch.State.Completed || a.firstLaunch.State.Stage != "failed" {
					t.Fatal("unavailable service marked usable")
				}
			}
		})
	}
}

func TestFirstLaunchReadyAdvancesToAgentStepWithoutCompletingSetup(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: t.TempDir()}}
	a.firstLaunch.State.Stage = "ready"
	a.runtimeStatus.Phase = "ready"
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("继续连接 Agent"); err != nil {
		t.Fatal(err)
	}
	if !a.firstLaunch.Open || a.firstLaunch.State.Completed || a.firstLaunch.State.Step != "agents" || a.page != "总览" || len(a.projects) != 0 {
		t.Fatal("environment readiness completed setup or registered a project")
	}
	for _, label := range []string{"准备环境", "连接 Agent", "配置记忆处理", "继续配置记忆处理"} {
		if !tt.HasText(label) {
			t.Fatalf("three-step setup is missing %q", label)
		}
	}
	if tt.HasText("第一项任务") || tt.HasText("记忆结果") {
		t.Fatal("superseded first-task gate returned to setup")
	}
}

func TestFirstLaunchProcessingReusesLedgerAndRequiresExplicitActiveRevision(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: t.TempDir()}}
	a.firstLaunch.State.Stage = "ready"
	a.firstLaunch.State.Step = "agents"
	a.runtimeStatus.Phase = "ready"
	a.serviceState = "ready"
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("继续配置记忆处理"); err != nil {
		t.Fatal(err)
	}
	if a.firstLaunch.State.Step != "processing" || !tt.HasText("使用已连接 Agent") || !tt.HasText("设为当前使用") {
		t.Fatal("processing setup did not reuse the production ledger page")
	}
	_ = tt.Click("完成配置")
	if a.firstLaunch.State.Completed || !a.firstLaunch.Open {
		t.Fatal("setup completed without an explicitly active processing revision")
	}
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: "connection", ConnectionGeneration: 1}
	revision := records.ProcessingRevision{ID: "revision", Spec: spec, Validation: &records.ProcessingValidation{ID: "validation", Revision: "revision", State: "succeeded"}}
	a.processing.Configuration.Saved["agent"] = revision
	a.processing.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 1, Revision: &revision}
	a.processing.Drafts["agent"] = spec
	tt = ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("pi · deepseek / deepseek-flash · 当前使用") {
		t.Fatal("processing setup hid the current production-ledger selection")
	}
	if err := tt.Click("完成配置"); err != nil {
		t.Fatal(err)
	}
	if !a.firstLaunch.State.Completed || a.firstLaunch.State.Skipped || a.firstLaunch.Open || a.page != "总览" {
		t.Fatal("active processing revision did not complete setup into the daily home")
	}
}

func TestOpenFirstLaunchResumesPersistedProcessingStepWithoutRediscovery(t *testing.T) {
	a := New("test", nil)
	root := t.TempDir()
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: root}}
	a.firstLaunch.State = FirstLaunchState{Stage: "ready", Step: "processing", Skipped: true}
	a.runtimeStatus.Phase = "ready"
	a.openFirstLaunch()
	if !a.firstLaunch.Open || a.firstLaunch.State.Skipped || a.firstLaunch.State.Step != "processing" || a.firstLaunch.State.Stage != "ready" {
		t.Fatal("resuming deferred setup restarted environment discovery")
	}
}

func TestConfigureFirstLaunchChecksBeforeOpeningPersistedPage(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(fmt.Sprint(skipped), func(t *testing.T) {
			a := New("test", nil)
			root := t.TempDir()
			state := FirstLaunchState{Stage: "ready", Step: "processing", Skipped: skipped}
			raw, _ := json.Marshal(state)
			if err := os.WriteFile(filepath.Join(root, "first-launch.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			updates := make(chan func(), 32)
			controller := &RuntimeController{Config: RuntimeConfig{Root: root}}
			if !a.ConfigureFirstLaunch(ctx, controller, func(fn func()) { updates <- fn }) {
				t.Fatal("startup verification was bypassed")
			}
			tt := ui.NewTester(a.View, 1200, 820)
			if !a.firstLaunch.Detecting || a.firstLaunch.Open || !tt.HasText("正在检查本机环境") || tt.HasText("设为当前使用") {
				t.Fatal("persisted page opened before checks")
			}
			a.firstLaunch.StartupPreparing = true
			a.SetRuntimeStatus(RuntimeStatus{Phase: "verifying", Installed: true, Message: "checking actual service"})
			if !a.firstLaunch.Detecting {
				t.Fatal("component installation skipped live checks")
			}
			a.SetRuntimeStatus(RuntimeStatus{Phase: "ready", Installed: true})
			if a.firstLaunch.Detecting || a.firstLaunch.Open == skipped || a.firstLaunch.State.Step != "processing" || a.firstLaunch.State.Completed {
				t.Fatal("live readiness lost saved step or silently completed setup")
			}
		})
	}
}

func TestFirstLaunchPreparationKeepsCardsOnOnePage(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: t.TempDir(), WorkerID: "rust", ModelID: "e5"}}
	a.firstLaunch.State.Stage = "results"
	prepared := false
	a.prepareRuntime = func() { prepared = true }
	tt := ui.NewTester(a.View, 1200, 820)
	button, ok := tt.Find("安装并继续")
	if !ok || button.Y+button.H > 820 {
		t.Fatal("primary preparation action is outside the window")
	}
	if tt.HasText("查看检查详情") || tt.HasText("第一项任务") {
		t.Fatal("obsolete preparation flow returned")
	}
	for _, label := range []string{"记忆数据库", "向量数据库", "向量模型"} {
		if !tt.HasText(label) {
			t.Fatal("missing environment card", label)
		}
	}
	if err := tt.Click("安装并继续"); err != nil {
		t.Fatal(err)
	}
	if !prepared || a.firstLaunch.State.Stage != "preparing" || a.firstLaunch.State.Attempt != 1 {
		t.Fatal("one-page confirmation did not prepare")
	}
	for _, label := range []string{"记忆数据库", "向量数据库", "向量模型", "取消准备"} {
		if !tt.HasText(label) {
			t.Fatal("preparation replaced page content", label)
		}
	}
	tt.SetSize(980, 620)
	button, ok = tt.Find("取消准备")
	if !ok || button.Y+button.H > 620 {
		t.Fatal("narrow footer is clipped")
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
	if err := tt.Click("安装并继续"); err != nil {
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

func TestEnvironmentFailureRetainsOnlyItsActualDownloadBytes(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Open = true
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: t.TempDir(), WorkerID: "rust", ModelID: "e5"}}
	a.firstLaunch.State.Stage = "cancelled"
	a.firstLaunch.State.Message = "本轮准备已取消"
	a.firstLaunch.State.Items = []EnvironmentItem{{Name: "rust", State: "prepared_verified"}, {Name: "e5", State: "needs_preparation", Bytes: 100000000}}
	a.runtimeStatus = RuntimeStatus{Phase: "cancelled", Downloaded: 99000000, Total: 100000000, Components: []components.PreparationReceipt{{Progress: components.Progress{ID: "e5", Phase: "failed", Downloaded: 25000000, Total: 100000000}}}}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("已保留下载 25.00 MB") || tt.HasText("已保留下载 99.00 MB") {
		t.Fatal("card used combined progress or lost retained bytes")
	}
}
