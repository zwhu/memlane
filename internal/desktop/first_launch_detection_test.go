package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestDetectionNeedsBothLiveServicesAndAllRequiredComponents(t *testing.T) {
	r := &RuntimeController{Config: RuntimeConfig{WorkerID: "rust", ModelID: "e5"}, Official: &official.Controller{}}
	items := []EnvironmentItem{{Name: "rust", State: "verified_reusable"}, {Name: "e5", State: "verified_reusable"}, {Name: "helper / LaunchAgent", State: "installed"}, {Name: "cognee-service", State: "verified_reusable"}, {Name: "cognee-bge-zh", State: "verified_reusable"}, {Name: "cognee-codex-plugin", State: "verified_reusable"}, {Name: "记忆服务", State: "ready"}, {Name: "Cognee 数据服务", State: "ready"}, {Name: "pi", State: "unknown"}, {Name: "后台服务", State: "enabled"}}
	if !environmentDetectedReady(items, r) {
		t.Fatal("optional Agent uncertainty blocked environment readiness")
	}
	for _, i := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		before := items[i].State
		items[i].State = "unavailable"
		if environmentDetectedReady(items, r) {
			t.Fatal("missing required receipt was ignored", items[i].Name)
		}
		items[i].State = before
	}
	for _, state := range []string{"not_registered", "requires_approval", "not_found"} {
		items[9].State = state
		if environmentDetectedReady(items, r) {
			t.Fatal("health bypassed system approval", state)
		}
	}
}

func TestPageReadsReuseStartupHealthAndKeepIndependentDiagnostics(t *testing.T) {
	requests := make(chan string, 4)
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		requests <- r.Op
		return serviceipc.Response{OK: false, Code: "service_error", Error: "isolated database read failure"}
	})
	a.visualFixture, a.activityLoaded = false, true
	a.page = "总览"
	a.firstLaunch.Startup, a.firstLaunch.StartupPreparing = true, true
	a.firstLaunch.ReturnStep = "processing"
	a.firstLaunch.beginDetection()
	a.refresh(false)
	if a.loading || a.loadSequence != 0 || len(requests) != 0 {
		t.Fatal("page started its own reads during startup")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "ready", Installed: true})
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "ready", State: "ready", ScheduleError: "调度已停止"})
	a.refresh(false)
	awaitExperienceDispatch(t, dispatch)
	if len(requests) != 1 || <-requests != "scopes" {
		t.Fatal("page refresh rechecked service health")
	}
	if a.serviceState != "ready" || a.schedules.RuntimeError != "调度已停止" || a.libraryError == "" {
		t.Fatal("data read failure replaced independently checked health or diagnostics")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("记忆读取失败") || tt.HasText("服务暂不可用") {
		t.Fatal("database read failure was mislabeled as a service check")
	}
}

func TestEnteringEnvironmentUsesExistingCheckUntilExplicitInvalidation(t *testing.T) {
	a := New("test", nil)
	r := &RuntimeController{Config: RuntimeConfig{Root: t.TempDir()}}
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	updates := make(chan func(), 1)
	a.firstLaunch.Controller, a.firstLaunch.Context = r, context.Background()
	a.firstLaunch.Dispatch = func(fn func()) { updates <- fn }
	a.environment.CheckedAt = time.Now().Add(-time.Hour)
	a.ensureEnvironment()
	if a.environment.Loading || a.environment.Sequence != 0 {
		t.Fatal("navigation restarted an already completed environment check")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "ready", Installed: true})
	if a.serviceState != "ready" {
		t.Fatal("startup health was not handed to the home page")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "checking"})
	if !a.environment.CheckedAt.IsZero() {
		t.Fatal("new preparation reused invalid component evidence")
	}
}

func TestStartupWaitsThroughPreparationAndFailureStopsAnimation(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Startup, a.firstLaunch.StartupPreparing = true, true
	a.firstLaunch.ReturnOpen = true
	a.firstLaunch.ReturnStep = "agents"
	a.firstLaunch.beginDetection()
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("连接 Agent") || !tt.HasText("正在检查本机环境") {
		t.Fatal("page displayed during startup")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "failed", Message: "health unavailable"})
	tt = ui.NewTester(a.View, 1200, 820)
	if !a.firstLaunch.Detecting || !a.firstLaunch.DetectionFailed || tt.FramePending() {
		t.Fatal("failed observation entered page or retained a motion loop")
	}
	a.cancelFirstLaunch(true)
	if a.firstLaunch.Detecting || !a.firstLaunch.State.Skipped {
		t.Fatal("skip did not leave stopped detection")
	}
}

func TestDetectionHollowBulbAndStableMotionRespectReducedEffects(t *testing.T) {
	for _, point := range detectionBulb {
		if point[0] > 45 && point[0] < 100 && point[1] > 30 && point[1] < 60 {
			t.Fatal("bulb became a filled point cloud")
		}
	}
	x, y, alpha := detectionBlockPose(0, 0, true)
	targetX, targetY, _ := detectionBlockPose(0, 2.6, true)
	if x == targetX && y == targetY || alpha >= 1 {
		t.Fatal("scatter does not organize into the bulb")
	}
	a := New("test", nil)
	a.firstLaunch.Detecting = true
	a.firstLaunch.State.ReducedMotion = true
	a.firstLaunch.ChangedAt = time.Now()
	tt := ui.NewTester(a.View, 980, 620)
	if tt.FramePending() {
		t.Fatal("reduced motion requested frames")
	}
}

func TestDetectionDoesNotReadDuringActiveLifecycleAndEscapeCancels(t *testing.T) {
	r := &RuntimeController{}
	r.lifecycle.Lock()
	_, err := r.DiscoverEnvironment(context.Background())
	r.lifecycle.Unlock()
	if !errors.Is(err, ErrRuntimeChanging) {
		t.Fatal("check raced an active service lifecycle", err)
	}
	a := New("test", nil)
	a.firstLaunch.Open, a.firstLaunch.Detecting = true, true
	a.firstLaunch.State.Stage = "discovering"
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Key(0, ui.KeyEscape)
	if a.firstLaunch.Detecting || a.firstLaunch.Open || !a.firstLaunch.State.Skipped {
		t.Fatal("Escape did not cancel detection")
	}
}
