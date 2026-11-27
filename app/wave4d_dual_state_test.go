package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
)

func TestWave4DDualStateMainWindowAndTrayAcrossAppearances(t *testing.T) {
	for _, appearance := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(appearance.name, func(t *testing.T) {
			a := settingsFixture()
			a.Config.ConfigFile = "/synthetic/runtime.json"
			a.Config.ServiceURL = ""
			a.Config.ProjectControl = &ProjectControlConfig{URL: "http://127.0.0.1:61290"}
			a.syncSettingsTransport()
			a.monitor.ControlChecked = true
			a.monitor.ControlReachable = true
			t.Cleanup(func() { applyAppearance(false) })

			// 1. Engine starting: main window shows progress without blocking; tray does not report danger.
			a.monitor.State = "starting"
			a.monitor.EngineStatus = "启动中"
			a.monitor.Snapshot = &ServiceMonitorSnapshot{
				GatewayOK:     settings.BoolPtr(true),
				EngineOK:      settings.BoolPtr(false),
				EnginePhase:   "starting",
				State:         "starting",
				StatusSummary: "记忆引擎启动中，已可正常浏览和采集",
				Inbox: &settings.MonitorInboxSummary{
					Pending:          2,
					Processing:       0,
					ExtractionPaused: true,
					PauseReason:      "engine_starting",
				},
			}

			mainTT := ui.NewTester(a.View, 1200, 820)
			mainTT.SetDark(appearance.dark)
			mainTT.Frame()
			if !mainTT.HasText("记忆引擎正在启动，已可正常浏览历史记忆并继续采集对话。") {
				t.Fatalf("missing starting progress banner in %s mode: %v", appearance.name, mainTT.Texts())
			}
			if mainTT.HasText("外部托管记忆库服务尚未运行") {
				t.Fatalf("must not show stopped banner while gateway is reachable and engine starting: %v", mainTT.Texts())
			}
			if level := a.trayStatusLevel(); level == "danger" {
				t.Fatalf("starting engine must not report tray danger level")
			}
			_, _, tip := a.TrayIconState()
			if !strings.Contains(tip, "采集正常，引擎启动中") {
				t.Fatalf("unexpected starting tray tooltip: %q", tip)
			}
			trayTT := ui.NewTester(a.TrayFlyoutView, TrayFlyoutWidth, a.TrayFlyoutHeight())
			trayTT.SetDark(appearance.dark)
			trayTT.Frame()
			if !trayTT.HasText("采集正常，引擎启动中，2 条待处理") || !trayTT.HasText("启动中") {
				t.Fatalf("missing starting dual-state copy in tray flyout (%s): %v", appearance.name, trayTT.Texts())
			}

			// 2. Extraction paused + backlog N=5 + startup/fault reason.
			a.monitor.State = "exited"
			a.monitor.EngineStatus = "服务已退出"
			a.monitor.LastError = "cognee_boot_failed: port 18171 in use"
			a.monitor.Snapshot = &ServiceMonitorSnapshot{
				GatewayOK:     settings.BoolPtr(true),
				EngineOK:      settings.BoolPtr(false),
				EnginePhase:   "exited",
				State:         "exited",
				StartupError:  "cognee_boot_failed: port 18171 in use",
				LastError:     "cognee_boot_failed: port 18171 in use",
				StatusSummary: "Cognee 进程已退出，服务未自动重启",
				Inbox: &settings.MonitorInboxSummary{
					Pending:          4,
					Processing:       1,
					Failed:           0,
					ExtractionPaused: true,
					PauseReason:      "cognee_boot_failed",
				},
			}
			pausedHeight := a.TrayFlyoutHeight()
			mainTT.Frame()
			for _, want := range []string{
				"提炼已暂停，采集仍在继续。已有 5 条记录等待提炼。",
				"原因：cognee_boot_failed: port 18171 in use",
				"查看服务与日志",
			} {
				if !mainTT.HasText(want) {
					t.Fatalf("missing paused dual-state text %q in %s main window: %v", want, appearance.name, mainTT.Texts())
				}
			}
			if mainTT.HasText("外部托管记忆库服务尚未运行") {
				t.Fatalf("must not show stopped banner when gateway is healthy and engine exited: %v", mainTT.Texts())
			}

			_, _, pausedTip := a.TrayIconState()
			if !strings.Contains(pausedTip, "采集正常，提炼暂停，5 条待处理") {
				t.Fatalf("unexpected paused tray tooltip: %q", pausedTip)
			}
			trayPausedTT := ui.NewTester(a.TrayFlyoutView, TrayFlyoutWidth, pausedHeight)
			trayPausedTT.SetDark(appearance.dark)
			trayPausedTT.Frame()
			if !trayPausedTT.HasText("采集正常，提炼暂停，5 条待处理") {
				t.Fatalf("missing paused dual-state text in %s tray flyout: %v", appearance.name, trayPausedTT.Texts())
			}

			// Click shortcut to Settings -> 服务.
			clickProjectUI(t, mainTT, "查看服务与日志")
			if a.page != "设置" || a.access.SettingsSection != "服务" {
				t.Fatalf("shortcut did not jump to 设置 › 服务: page=%q section=%q", a.page, a.access.SettingsSection)
			}

			// 3. Engine recovers -> dual-state banner disappears automatically and tray height shrinks back.
			a.monitor.State = "ready"
			a.monitor.EngineStatus = "已连接"
			a.monitor.LastError = ""
			a.monitor.Snapshot = &ServiceMonitorSnapshot{
				GatewayOK:   settings.BoolPtr(true),
				EngineOK:    settings.BoolPtr(true),
				EnginePhase: "ready",
				State:       "ready",
				Inbox: &settings.MonitorInboxSummary{
					Pending:          0,
					Processing:       0,
					ExtractionPaused: false,
				},
			}
			recoveredHeight := a.TrayFlyoutHeight()
			if recoveredHeight >= pausedHeight {
				t.Fatalf("expected TrayFlyoutHeight to account for dual-state strip: paused=%d recovered=%d", pausedHeight, recoveredHeight)
			}
			mainTT.Frame()
			if mainTT.HasText("提炼已暂停，采集仍在继续。") || mainTT.HasText("记忆引擎正在启动") {
				t.Fatalf("dual-state banner did not clear after engine recovery: %v", mainTT.Texts())
			}
		})
	}
}

func TestWave4DFirstProbeFailureDoesNotDeadlockPolling(t *testing.T) {
	a := New(Config{
		ConfigFile:     "/synthetic/runtime.json",
		ProjectControl: &ProjectControlConfig{URL: "http://127.0.0.1:61291"},
	}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway down", http.StatusBadGateway)
	})}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.monitor.PollEnabled = true
	a.monitor.NextPollAt = time.Now().Add(-time.Second)

	// Simulate first probe failing before any prior OK.
	a.settingsController.ApplyServiceMonitorResult(
		time.Now(), http.ErrHandlerTimeout, 0, "未配置", 0, false,
		true, nil, http.ErrHandlerTimeout, 0, false,
	)
	if !a.isExternalManagedStoreStopped() {
		t.Fatal("expected external store stopped state when gateway probe fails")
	}

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !a.monitor.PollEnabled {
		t.Fatal("first probe failure must never disable PollEnabled")
	}
	if a.monitor.Request == 0 {
		t.Fatal("expected due service monitor poll to fire automatically while gateway is down")
	}
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if !a.monitor.PollEnabled {
		t.Fatal("polling became disabled after failed retry")
	}
}

func TestWave4DEngineFaultWithoutExtractionPauseAndStaleStartingPrecedence(t *testing.T) {
	a := settingsFixture()
	a.Config.ConfigFile = "/synthetic/runtime.json"
	a.Config.ProjectControl = &ProjectControlConfig{URL: "http://127.0.0.1:61292"}
	a.syncSettingsTransport()
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = true

	// 1. engine_phase=degraded with extraction_paused=false must NOT misreport "提炼已暂停".
	a.monitor.State = "degraded"
	a.monitor.EngineStatus = "响应异常"
	a.monitor.LastError = "engine health timeout"
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		GatewayOK:   settings.BoolPtr(true),
		EngineOK:    settings.BoolPtr(false),
		EnginePhase: "degraded",
		State:       "degraded",
		LastError:   "engine health timeout",
		Inbox: &settings.MonitorInboxSummary{
			Pending:          0,
			Processing:       0,
			ExtractionPaused: false,
		},
	}
	dual := a.settingsController.DualStateSummary()
	if dual.ExtractionPaused || !dual.EngineFault {
		t.Fatalf("expected EngineFault=true and ExtractionPaused=false, got %+v", dual)
	}
	mainTT := ui.NewTester(a.View, 1200, 820)
	if !mainTT.HasText("记忆引擎连接异常，历史浏览与对话采集仍可继续。") || mainTT.HasText("提炼已暂停") {
		t.Fatalf("unexpected main window copy for engine fault without pause: %v", mainTT.Texts())
	}
	trayTT := ui.NewTester(a.TrayFlyoutView, TrayFlyoutWidth, a.TrayFlyoutHeight())
	if !trayTT.HasText("采集正常，引擎异常") || trayTT.HasText("提炼暂停") {
		t.Fatalf("unexpected tray copy for engine fault without pause: %v", trayTT.Texts())
	}

	// 2. Long fault reason increases TrayFlyoutHeight relative to no/short reason and clamps at TrayFlyoutMaxHeight.
	a.SetKeepAliveOnQuit(false)
	a.monitor.LastError = ""
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		GatewayOK:   settings.BoolPtr(true),
		EngineOK:    settings.BoolPtr(false),
		EnginePhase: "degraded",
		State:       "degraded",
		Inbox:       &settings.MonitorInboxSummary{ExtractionPaused: false},
	}
	noReasonHeight := a.TrayFlyoutHeight()
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		GatewayOK:   settings.BoolPtr(true),
		EngineOK:    settings.BoolPtr(false),
		EnginePhase: "degraded",
		State:       "degraded",
		LastError:   "engine timeout",
		Inbox:       &settings.MonitorInboxSummary{ExtractionPaused: false},
	}
	shortHeight := a.TrayFlyoutHeight()
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		GatewayOK:   settings.BoolPtr(true),
		EngineOK:    settings.BoolPtr(false),
		EnginePhase: "degraded",
		State:       "degraded",
		LastError:   strings.Repeat("记忆引擎连接超时且端口不可达，请检查本地后台服务日志。", 3),
		Inbox:       &settings.MonitorInboxSummary{ExtractionPaused: false},
	}
	longHeight := a.TrayFlyoutHeight()
	if !(noReasonHeight < shortHeight && shortHeight < longHeight && longHeight <= TrayFlyoutMaxHeight) {
		t.Fatalf("expected fault reason height progression and clamp: none=%d short=%d long=%d max=%d",
			noReasonHeight, shortHeight, longHeight, TrayFlyoutMaxHeight)
	}

	// 3. Stale starting snapshot must NOT suppress gateway disconnect -> danger.
	a.monitor.State = "starting"
	a.monitor.EngineStatus = "启动中"
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		GatewayOK:   settings.BoolPtr(true),
		EngineOK:    settings.BoolPtr(false),
		EnginePhase: "starting",
		State:       "starting",
	}
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = false
	if level := a.trayStatusLevel(); level != "danger" {
		t.Fatalf("gateway disconnect with stale starting snapshot must return danger, got %q", level)
	}

	// 4. Starting engine + project error alert must still return danger.
	a.monitor.ControlReachable = true
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.InboxFailed = 2
	if level := a.trayStatusLevel(); level != "danger" {
		t.Fatalf("starting engine with project error alert must return danger, got %q", level)
	}

	// 5. engine_ok=true + model startup_error + extraction_paused=true -> EngineFault=false, tray header & icon both warn (yellow).
	q.InboxFailed = 0
	a.monitor.State = "ready"
	a.monitor.EngineStatus = "已连接"
	a.monitor.ControlReachable = true
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		GatewayOK:    settings.BoolPtr(true),
		EngineOK:     settings.BoolPtr(true),
		EnginePhase:  "ready",
		State:        "ready",
		StartupError: "no_active_profile",
		Inbox: &settings.MonitorInboxSummary{
			Pending:          3,
			Processing:       0,
			ExtractionPaused: true,
			PauseReason:      "no_active_profile",
		},
	}
	modelPaused := a.settingsController.DualStateSummary()
	if modelPaused.EngineFault || !modelPaused.ExtractionPaused {
		t.Fatalf("model startup_error with engine_ok=true must not set EngineFault: %+v", modelPaused)
	}
	dot, label := a.trayOverallServiceStatus()
	if dot != "warn" || a.trayStatusLevel() != "warn" || label != "采集正常，提炼暂停，3 条待处理" {
		t.Fatalf("expected consistent warn styling for model-paused extraction: dot=%q level=%q label=%q", dot, a.trayStatusLevel(), label)
	}
}
