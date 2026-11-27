package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
)

func TestServiceMonitorPollAndSettingsCardMetrics(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	const officialURL = "http://127.0.0.1:61255"
	const controlURL = "http://127.0.0.1:61256"
	official := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(404)
	}

	var monitorMode atomic.Int32
	control := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runtime/monitor" {
			state := "ready"
			var logTail []string
			if monitorMode.Load() == 1 {
				state = "exited"
				logTail = []string{"official process exited with status 1 (Authorization: Bearer [REDACTED])"}
			}
			rss := int64(420 * 1024 * 1024)
			ctrlRSS := int64(64 * 1024 * 1024)
			recentMs := 125.0
			verified := true
			alive := state != "exited"
			json.NewEncoder(w).Encode(ServiceMonitorSnapshot{
				State:      state,
				ServiceURL: officialURL,
				Processes: map[string]MonitoredProcess{
					"official":        {PID: 5001, Alive: &alive, RSSBytes: &rss},
					"project_control": {PID: 5002, RSSBytes: &ctrlRSS},
				},
				Disk: &DiskMonitorState{
					FreeBytes:        82 * 1024 * 1024 * 1024,
					MinimumFreeBytes: 50 * 1024 * 1024 * 1024,
					StopFreeBytes:    55 * 1024 * 1024 * 1024,
					Low:              false,
				},
				Summary: &MonitorBusinessSummary{
					ProjectsCount:             3,
					GenerationReceiptVerified: &verified,
					RecentProcessingMs:        &recentMs,
					HookErrorsCount:           1,
				},
				LogTail: logTail,
			})
			return
		}
		w.WriteHeader(404)
	}

	a := New(Config{
		ServiceURL:     officialURL,
		ProjectControl: &ProjectControlConfig{URL: controlURL, ManagementTokenFile: token},
	}, nil)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "127.0.0.1:61255":
			official(w, r)
		case "127.0.0.1:61256":
			control(w, r)
		default:
			http.NotFound(w, r)
		}
	})}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }

	// Default New() leaves PollEnabled=false so passive renders never spawn background polls.
	a.refreshServiceMonitorIfDue(time.Now().Add(time.Hour))
	if a.monitor.Polling {
		t.Fatal("refreshServiceMonitorIfDue polled while PollEnabled is false")
	}

	a.monitor.PollEnabled = true
	a.refreshServiceMonitorIfDue(time.Now().Add(time.Hour))
	if !a.monitor.Polling {
		t.Fatal("expected refreshServiceMonitorIfDue to start polling when PollEnabled=true")
	}
	drainReading(t, updates, func() bool { return !a.monitor.Polling })

	if a.monitor.EngineStatus != "已连接" || a.monitor.State != "ready" || !a.monitor.ControlReachable || !a.monitor.HasControlLatency {
		t.Fatalf("unexpected monitor state after healthy poll: service=%q state=%q ctrl=%v hasCtrlLatency=%v err=%q",
			a.monitor.EngineStatus, a.monitor.State, a.monitor.ControlReachable, a.monitor.HasControlLatency, a.monitor.ControlError)
	}
	summary := a.settingsController.ServiceSummary()
	if !strings.Contains(summary, "内存 420 MB") || !strings.Contains(summary, "磁盘余量 82.0 GiB") {
		t.Fatalf("missing expected metrics in summary line: %q", summary)
	}

	// Render Settings -> 服务 expanded card & check diagnostic text does not leak tokens.
	a.monitor.Expanded = true
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.settingsController.ViewServiceOverview(c) })
	}, 1000, 700)
	if !tt.HasText("记忆引擎（Cognee）") || !tt.HasText("接入网关") || !tt.HasText("已连接") || !tt.HasText("复制诊断") {
		t.Fatal("expected expanded service monitor details in settings card")
	}
	if tt.HasText("接入网关：已连接") || tt.HasText("接入网关：异常") {
		t.Fatalf("gateway status value must not repeat component name prefix: %v", tt.Texts())
	}
	if tt.HasText("最近请求") {
		t.Fatalf("gateway row must not display misleading 最近请求 label: %v", tt.Texts())
	}
	if !tt.HasText("最近模型生成 125 ms") {
		t.Fatalf("expected Cognee row to display direct-provider generation duration when channel row is absent: %v", tt.Texts())
	}
	if tt.HasText("API Key") || tt.HasText("切换") {
		t.Fatal("service card must never expose generation controls")
	}

	// Simulate exited official PID from control monitor.
	monitorMode.Store(1)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State != "exited" || a.monitor.EngineStatus != "服务已退出" {
		t.Fatalf("expected exited state, got state=%q service=%q", a.monitor.State, a.monitor.EngineStatus)
	}
	tt.Frame()
	if !tt.HasText("服务未自动重启") || !tt.HasText("[REDACTED]") {
		t.Fatal("expected non-auto-restart notice and redacted log tail in UI")
	}
	diag := a.settingsController.ServiceDiagnosticText()
	if !strings.Contains(diag, "记忆引擎（Cognee）状态: exited") || strings.Contains(diag, strings.Repeat("m", 40)) {
		t.Fatalf("unexpected diagnostic output: %q", diag)
	}
	if !strings.Contains(diag, "最近模型生成耗时: 125 ms") || strings.Contains(diag, "最近接入网关耗时") || !strings.Contains(diag, "接入网关状态: 已连接 (延迟 ") {
		t.Fatalf("unexpected diagnostic latency attribution:\n%s", diag)
	}
}

func TestServiceMonitorDebouncePreservesReadingPanes(t *testing.T) {
	a := New(Config{ServiceURL: "http://127.0.0.1:61255"}, nil)
	a.page = "知识"
	a.project = 0
	p := a.state()
	p.Items = []Item{{ID: "k1", Dataset: "ds", Kind: "知识", Title: "保留记忆正文"}}
	p.Selected = &p.Items[0]

	now := time.Now()
	a.settingsController.ApplyServiceMonitorResult(now, nil, 12.0, "未配置", 0, false, false, nil, nil, 0, false)
	if a.monitor.State != "ready" || a.monitor.EngineStatus != "已连接" {
		t.Fatalf("expected ready, got %q / %q", a.monitor.State, a.monitor.EngineStatus)
	}

	// Failures 1 & 2 enter warning while preserving connected indicator and reading content.
	for step := 1; step <= 2; step++ {
		a.settingsController.ApplyServiceMonitorResult(now.Add(time.Duration(step)*10*time.Second), errors.New("timeout"), 0, "未配置", 0, false, false, nil, nil, 0, false)
		if a.monitor.State != "warning" || a.monitor.ConsecutiveFailures != step || a.monitor.EngineStatus != "已连接" {
			t.Fatalf("step %d: expected warning preserving connected state, got state=%q failures=%d service=%q",
				step, a.monitor.State, a.monitor.ConsecutiveFailures, a.monitor.EngineStatus)
		}
	}

	// 3rd failure enters degraded, still preserving reading pane items & selection.
	a.settingsController.ApplyServiceMonitorResult(now.Add(30*time.Second), errors.New("connection refused"), 0, "未配置", 0, false, false, nil, nil, 0, false)
	if a.monitor.State != "degraded" || a.monitor.ConsecutiveFailures != 3 || a.monitor.EngineStatus != "connection refused" {
		t.Fatalf("expected degraded on 3rd failure, got state=%q failures=%d service=%q",
			a.monitor.State, a.monitor.ConsecutiveFailures, a.monitor.EngineStatus)
	}
	if len(p.Items) != 1 || p.Selected == nil || p.Selected.ID != "k1" {
		t.Fatal("monitor degradation cleared active reading state")
	}

	// Recovery resets consecutive failures and restores ready state.
	a.settingsController.ApplyServiceMonitorResult(now.Add(40*time.Second), nil, 9.0, "未配置", 0, false, false, nil, nil, 0, false)
	if a.monitor.State != "ready" || a.monitor.ConsecutiveFailures != 0 || a.monitor.EngineStatus != "已连接" {
		t.Fatalf("expected recovery to ready, got state=%q failures=%d service=%q",
			a.monitor.State, a.monitor.ConsecutiveFailures, a.monitor.EngineStatus)
	}
	if len(p.Items) != 1 || p.Selected == nil || p.Selected.ID != "k1" {
		t.Fatal("monitor recovery altered active reading state")
	}
}

func TestServiceMonitorFormatsANSILogsAndHookErrors(t *testing.T) {
	a := New(Config{ServiceURL: "http://127.0.0.1:61255"}, nil)
	a.monitor.Expanded = true
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = true
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		State: "ready",
		Summary: &MonitorBusinessSummary{
			ProjectsCount:   8,
			HookErrorsCount: 1,
			HookErrors: []MonitorHookError{{
				InstanceID: "antigravity-1",
				Kind:       "antigravity",
				At:         1795155000,
				Script:     "store-user-prompt.py",
				ErrorType:  "NotReady",
				Reason:     "existing_conversation_not_captured",
			}},
		},
		LogTail: []string{
			"\x1b[2m2026-11-20T06:07:54.031873\x1b[0m [\x1b[32m\x1b[1minfo     \x1b[0m] \x1b[1mDataset routing index for 0785e3d2: 0 row(s) written, 0 removed\x1b[0m [\x1b[34mdataset_routing\x1b[0m]",
			"\x1b[2m2026-11-20T06:07:54.032418\x1b[0m [\x1b[32m\x1b[1minfo     \x1b[0m] \x1b[1mCoroutine task completed: `add_data_points`\x1b[0m [\x1b[34mrun_tasks_base\x1b[0m]",
			"\x1b[2m2026-11-20T06:07:54.049872\x1b[0m [\x1b[32m\x1b[1minfo     \x1b[0m] \x1b[1mimprove: global context index updated\x1b[0m [\x1b[34mimprove\x1b[0m]",
			"\x1b[2m2026-11-20T06:11:36.321599\x1b[0m [\x1b[32m\x1b[1minfo     \x1b[0m] \x1b[1mrecall: 1 results across sources=['graph'] (session=-)\x1b[0m [\x1b[34mrecall\x1b[0m]",
			`{"schema_version":1,"event":"request_finished","component":"generation_http","backend_kind":"codex","at":"2026-11-20T06:21:19.469931+00:00","elapsed_ms":1500.0}`,
			"\x1b[2m2026-11-20T06:12:00.000000\x1b[0m [\x1b[31merror\x1b[0m] failed with Bearer [REDACTED]",
		},
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.settingsController.ViewServiceOverview(c) })
	}, 1000, 700)
	for _, want := range []string{
		"Hook 未记录 1 项",
		"Gemini App",
		"当前仍是安装前的旧会话",
		"最近服务动态（已脱敏）：",
		"更新项目索引路由（无变更）",
		"完成后台任务：写入知识图谱节点",
		"全局上下文索引已更新",
		"记忆召回完成（命中 1 条）",
		"完成处理请求（codex，耗时 1.5s）",
		"failed with Bearer [REDACTED]",
		"查看原始日志",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected readable monitor text %q", want)
		}
	}
	if tt.HasText("[2m") || tt.HasText("\x1b") {
		t.Fatal("ANSI escape codes leaked into monitor UI")
	}

	clickProjectUI(t, tt, "查看原始日志")
	if !tt.HasText("最近服务原始日志（已脱敏）：") || !tt.HasText("查看可读摘要") || !tt.HasText("recall: 1 results") {
		t.Fatal("raw log toggle did not reveal cleaned raw log lines")
	}
	if tt.HasText("[2m") || tt.HasText("\x1b") {
		t.Fatal("ANSI escape codes leaked into raw log mode")
	}

	diag := a.settingsController.ServiceDiagnosticText()
	if !strings.Contains(diag, "Hook 未记录数: 1") || !strings.Contains(diag, "Gemini App") || strings.Contains(diag, "\x1b") {
		t.Fatalf("unexpected diagnostic text: %q", diag)
	}
}

func TestSidebarServicePopoverAndChannelNaming(t *testing.T) {
	a := settingsFixture()
	a.monitor.EngineStatus = "已连接"
	a.Config.CompletionURL = "http://127.0.0.1:61256"
	a.monitor.State = "ready"
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = true
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		State: "ready",
		Disk:  &DiskMonitorState{FreeBytes: 80 * 1024 * 1024 * 1024},
	}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }

	// 1) Hover 300ms opens popover; before 300ms it stays closed.
	a.syncServicePopoverHover(true, false, false)
	time.Sleep(120 * time.Millisecond)
	if a.servicePopover.PopoverOpen {
		t.Fatal("popover opened too early before 300ms hover delay")
	}
	drainReading(t, updates, func() bool { return a.servicePopover.PopoverOpen })

	tt := ui.NewTester(a.View, 1200, 820)
	for _, want := range []string{"记忆引擎（Cognee）", "接入网关", "Agent 模型代理", "磁盘空间", "在设置中查看详情"} {
		if !tt.HasText(want) {
			t.Fatalf("service popover missing %q", want)
		}
	}
	if tt.HasText("补全桥") {
		t.Fatal("retired term 补全桥 appeared in service popover")
	}
	// Popover should attach above the bottom service button.
	svcBox, svcOK := tt.Find("服务 · 已连接")
	popBox, popOK := tt.Find("服务组件状态")
	if !svcOK || !popOK || popBox.Y+popBox.H > svcBox.Y+2 {
		t.Fatalf("service popover did not attach above trigger button: svc=%+v pop=%+v", svcBox, popBox)
	}

	// 2) Leaving both trigger and popover closes it after ~200ms.
	a.syncServicePopoverHover(false, false, false)
	drainReading(t, updates, func() bool { return !a.servicePopover.PopoverOpen })

	// 3) Keyboard focus opens popover immediately without waiting for timer.
	a.syncServicePopoverHover(false, false, true)
	if !a.servicePopover.PopoverOpen {
		t.Fatal("keyboard focus did not open service popover immediately")
	}
	tt.Frame()
	clickProjectUI(t, tt, "在设置中查看详情")
	if a.page != "设置" || a.access.SettingsSection != "服务" || a.servicePopover.PopoverOpen {
		t.Fatalf("detail click did not navigate to settings service tab: page=%q section=%q open=%v", a.page, a.access.SettingsSection, a.servicePopover.PopoverOpen)
	}

	// 4) Direct click on sidebar "服务 · 已连接" jumps straight to 设置 › 服务.
	a.navigate("总览", 0)
	tt.Frame()
	clickProjectUI(t, tt, "服务 · 已连接")
	if a.page != "设置" || a.access.SettingsSection != "服务" || a.servicePopover.PopoverOpen {
		t.Fatalf("direct service button click did not jump to settings service tab: page=%q section=%q open=%v", a.page, a.access.SettingsSection, a.servicePopover.PopoverOpen)
	}
}

func TestServiceMonitorDeserializesExtendedMetricsAndFormatsDiagnostics(t *testing.T) {
	rawJSON := `{
		"state": "ready",
		"processes": {
			"official": {"pid": 5001, "alive": true, "rss_bytes": 524288000, "child_count": 2, "self_rss_bytes": 314572800}
		},
		"summary": {
			"projects_count": 4,
			"hook_errors_count": 0,
			"backlog_files_count": 7,
			"largest_graph_db_bytes": 41943040,
			"recall_queries": {"total_queries": 10, "hit_queries": 7, "empty_queries": 3, "total_hits": 12, "hit_rate": 0.7}
		}
	}`
	var snap ServiceMonitorSnapshot
	if err := json.Unmarshal([]byte(rawJSON), &snap); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	proc := snap.Processes["official"]
	if proc.ChildCount != 2 || proc.SelfRSSBytes == nil || *proc.SelfRSSBytes != 314572800 {
		t.Fatalf("unexpected extended process fields: %+v", proc)
	}
	if snap.Summary.BacklogFilesCount != 7 || snap.Summary.LargestGraphDBBytes != 41943040 || snap.Summary.RecallQueries == nil || snap.Summary.RecallQueries.HitRate == nil || *snap.Summary.RecallQueries.HitRate != 0.7 {
		t.Fatalf("unexpected extended summary fields: %+v", snap.Summary)
	}

	a := New(Config{ServiceURL: "http://127.0.0.1:61255"}, nil)
	a.monitor.Snapshot = &snap
	diag := a.settingsController.ServiceDiagnosticText()
	for _, want := range []string{"self_rss=300 MB", "children=2", "积压文件数: 7", "最大图谱库: 40.0 MB", "记忆召回统计: 查询 10 次 | 命中 7 次 (命中率 70.0%)"} {
		if !strings.Contains(diag, want) {
			t.Fatalf("serviceDiagnosticText missing %q in:\n%s", want, diag)
		}
	}
}

func TestServiceConnectionCardsAttributeGenerationLatencyLightAndDark(t *testing.T) {
	if got := settings.FormatDurationCompact(9.2); got != "9 ms" {
		t.Fatalf("settings.FormatDurationCompact(9.2) = %q, want %q", got, "9 ms")
	}
	if got := settings.FormatDurationCompact(999.6); got != "1.0 s" {
		t.Fatalf("settings.FormatDurationCompact(999.6) = %q, want %q", got, "1.0 s")
	}
	if got := settings.FormatDurationCompact(41494.0); got != "41.5 s" {
		t.Fatalf("settings.FormatDurationCompact(41494.0) = %q, want %q", got, "41.5 s")
	}

	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a := settingsFixture()
			a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
			a.initDesktopPreferences()
			a.setAppearancePreference(appearance)
			t.Cleanup(func() { applyAppearance(false) })
			a.monitor.EngineStatus = "已连接"
			a.Config.CompletionURL = "http://127.0.0.1:18173"
			a.syncSettingsTransport()
			a.monitor.CompletionStatus = "端点已连接（登录状态以实际请求为准）"
			a.monitor.State = "ready"
			a.monitor.HasLatency = true
			a.monitor.LastLatencyMs = 9.0
			a.monitor.ControlChecked = true
			a.monitor.ControlReachable = true
			a.monitor.HasControlLatency = true
			a.monitor.ControlLatencyMs = 18.0
			a.monitor.HasCompletionLatency = true
			a.monitor.CompletionLatencyMs = 3.0

			officialRSS := int64(1818 * 1024 * 1024)
			controlRSS := int64(70 * 1024 * 1024)
			completionRSS := int64(214 * 1024 * 1024)
			recentGenMs := 41494.0
			verified := true
			a.monitor.Snapshot = &ServiceMonitorSnapshot{
				State: "ready",
				Processes: map[string]MonitoredProcess{
					"official":        {PID: 54545, RSSBytes: &officialRSS},
					"project_control": {PID: 54555, RSSBytes: &controlRSS},
					"completion":      {PID: 54540, RSSBytes: &completionRSS},
				},
				Summary: &MonitorBusinessSummary{
					ProjectsCount:             9,
					GenerationReceiptVerified: &verified,
					RecentProcessingMs:        &recentGenMs,
				},
			}

			tt := ui.NewTester(func(c *ui.Context) {
				applyAppearance(a.effectiveDark(c.Theme().Dark))
				ui.Themed(c, homeTheme).Children(func() {
					ui.Column(c).FillWidth().Padding(24).Children(func() { a.settingsController.ViewServiceOverview(c) })
				})
			}, 1000, 700)

			texts := tt.Texts()
			for _, want := range []string{
				"延迟 18 ms",
				"延迟 3 ms",
				"最近模型生成 41.5 s",
			} {
				if !tt.HasText(want) {
					t.Fatalf("missing %q in %v", want, texts)
				}
			}
			for _, forbidden := range []string{"最近请求", "41494"} {
				if tt.HasText(forbidden) {
					t.Fatalf("unexpected forbidden text %q in %v", forbidden, texts)
				}
			}
			// Generation duration belongs to Agent 模型代理 row, not 记忆引擎 or 接入网关 rows when channel row exists.
			cogBox, ok := tt.Find("记忆引擎（Cognee）")
			ctrlDescBox, cok := tt.Find("延迟 18 ms · 内存 70 MB · 项目 9 个 · 配置回执一致")
			genDescBox, gok := tt.Find("延迟 3 ms · 内存 214 MB · 最近模型生成 41.5 s · http://127.0.0.1:18173")
			if !ok || !cok || !gok {
				t.Fatalf("missing expected service rows: cogOK=%v ctrlOK=%v genOK=%v texts=%v", ok, cok, gok, texts)
			}
			if !(cogBox.Y < ctrlDescBox.Y && ctrlDescBox.Y < genDescBox.Y) {
				t.Fatalf("unexpected row vertical order: cog=%+v ctrlDesc=%+v genDesc=%+v", cogBox, ctrlDescBox, genDescBox)
			}
			// Unified V4 status color rule: healthy status text uses neutral Text2 (never ok green).
			for _, comp := range a.settingsController.ServiceComponents() {
				if comp.DotState == "ok" && comp.StatusColor != tok.Text2 {
					t.Fatalf("healthy component %q status color = %v, want neutral tok.Text2 (%v) not ok green (%v)", comp.ID, comp.StatusColor, tok.Text2, tok.OK)
				}
			}
		})
	}
}

func TestSidebarServicePopoverUsesMonitorDiskAndChannelStatus(t *testing.T) {
	const gib = 1024 * 1024 * 1024
	a := settingsFixture()
	a.Config.CompletionURL = "http://127.0.0.1:61256"
	a.servicePopover.PopoverOpen = true
	a.monitor.EngineStatus = "已连接"
	a.monitor.State = "ready"
	a.monitor.HasLatency = true
	a.monitor.LastLatencyMs = 10
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = true
	a.monitor.HasControlLatency = true
	a.monitor.ControlLatencyMs = 4
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		Disk:    &DiskMonitorState{FreeBytes: 4 * gib},
		Summary: &MonitorBusinessSummary{ProjectsCount: 5},
	}
	a.monitor.HasCompletionLatency = true
	a.monitor.CompletionLatencyMs = 27
	a.monitor.CompletionProvider = "codex"
	a.monitor.CompletionAgentDefault = true
	for _, tc := range []struct {
		stop                                                      int64
		low                                                       bool
		completion, diskText, popStatus, popMetrics, trayText, dot string
	}{
		{5 * gib, false, "端点已连接（自定义状态）", "可用空间正常 · 预警线 5 GiB", "已连接", "延迟 27 ms · Codex 默认", "已连接 · 27 ms", "ok"},
		{5 * gib, true, "请求超时", "低于 5 GiB 预警线", "待检查", "连接状态待核对", "待检查", "warn"},
		{0, false, "请求超时", "可用空间正常 · 预警线 55 GiB", "待检查", "连接状态待核对", "待检查", "warn"},
		{0, true, "请求超时", "低于 55 GiB 预警线", "待检查", "连接状态待核对", "待检查", "warn"},
		{gib + gib/2, true, "请求超时", "低于 1.5 GiB 预警线", "待检查", "连接状态待核对", "待检查", "warn"},
	} {
		a.monitor.Snapshot.Disk.StopFreeBytes, a.monitor.Snapshot.Disk.Low = tc.stop, tc.low
		a.monitor.CompletionStatus = tc.completion
		tt := ui.NewTester(a.View, 1200, 820)
		for _, want := range []string{tc.diskText, tc.popStatus, tc.popMetrics, "延迟 10 ms", "延迟 4 ms · 项目 5 个"} {
			if !tt.HasText(want) {
				t.Fatalf("missing %q in %v", want, tt.Texts())
			}
		}
		if tt.HasText("http://127.0.0.1:61256") || tt.HasText("已连接 · 27 ms") {
			t.Fatalf("sidebar popover must not display raw URL or mix latency into status column: %v", tt.Texts())
		}
		if dot, metric, color := a.trayChannelStatus(); dot != tc.dot || metric != tc.trayText || (dot == "warn" && color != tok.Warning) || (dot == "ok" && color != tok.Text2) {
			t.Fatalf("unexpected shared channel status: dot=%s metric=%q color=%v", dot, metric, color)
		}
	}
}

func TestServiceMonitorGatewayOnlyStartingInboxAndCapabilitiesInvalidation(t *testing.T) {
	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("z", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	const controlURL = "http://127.0.0.1:61288"
	settings.InvalidateControlCapabilitiesCache(controlURL)
	t.Cleanup(func() { settings.InvalidateControlCapabilitiesCache(controlURL) })

	var phase atomic.Int32 // 0=starting, 1=ready
	var directHealthCalls, monitorCalls, capsCalls atomic.Int32

	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			directHealthCalls.Add(1)
			http.Error(w, "must not probe direct engine /health", http.StatusTeapot)
		case "/v1/capabilities":
			capsCalls.Add(1)
			ready := phase.Load() == 1
			_ = json.NewEncoder(w).Encode(map[string]any{
				"api_versions":       []string{"v2", "v1"},
				"reading_contract":   settings.ReadingContract,
				"engine_ready":       ready,
				"capabilities_epoch": int64(phase.Load() + 1),
				"features": map[string]bool{
					"async_capture": true,
					"local_body":    ready,
				},
			})
		case "/v1/runtime/monitor":
			monitorCalls.Add(1)
			if phase.Load() == 0 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"gateway_ok":     true,
					"engine_ok":      false,
					"engine_phase":   "starting",
					"state":          "starting",
					"startup_error":  "",
					"status_summary": "记忆引擎启动中，已可正常浏览和采集",
					"inbox": map[string]any{
						"pending":           3,
						"processing":        1,
						"failed":            0,
						"awaiting_publish":  0,
						"publish_failed":    0,
						"extraction_paused": true,
						"pause_reason":      "engine_starting",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"gateway_ok":     true,
				"engine_ok":      true,
				"engine_phase":   "ready",
				"state":          "ready",
				"latency_ms":     14.5,
				"status_summary": "提炼服务运行正常",
				"inbox": map[string]any{
					"pending":           0,
					"processing":        0,
					"failed":            0,
					"awaiting_publish":  0,
					"publish_failed":    0,
					"extraction_paused": false,
				},
			})
		case "/v1/completion/health":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_configured"}}`))
		default:
			http.NotFound(w, r)
		}
	})

	// Pure gateway config without ServiceURL must still poll normally.
	a := New(Config{
		ServiceURL:     "",
		ProjectControl: &ProjectControlConfig{URL: controlURL, ManagementTokenFile: token},
	}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.monitor.PollEnabled = true

	// Prime capabilities cache while engine is starting (engine_ready=false).
	ctrlClient := settings.ProjectControlClient{
		Config: *a.Config.ProjectControl,
		HTTP:   a.Client.HTTP,
	}
	if ok, err := ctrlClient.ProbeFeature(context.Background(), "local_body"); err != nil || ok {
		t.Fatalf("expected local_body=false while starting, got (%v, %v)", ok, err)
	}
	if capsCalls.Load() != 1 {
		t.Fatalf("expected 1 capabilities call, got %d", capsCalls.Load())
	}

	// Poll 1: engine is starting -> no direct /health, monitor enters starting state.
	a.refreshServiceMonitorIfDue(time.Now().Add(time.Hour))
	if !a.monitor.Polling {
		t.Fatal("RefreshServiceMonitorIfDue skipped pure-gateway config with empty ServiceURL")
	}
	drainReading(t, updates, func() bool { return !a.monitor.Polling })

	if directHealthCalls.Load() != 0 || monitorCalls.Load() != 1 {
		t.Fatalf("expected gateway-only monitor poll: directHealth=%d monitor=%d", directHealthCalls.Load(), monitorCalls.Load())
	}
	if a.monitor.State != "starting" || a.monitor.EngineStatus != "启动中" || !a.monitor.ControlReachable {
		t.Fatalf("unexpected starting monitor state: state=%q engine=%q ctrl=%v", a.monitor.State, a.monitor.EngineStatus, a.monitor.ControlReachable)
	}
	dot, status, _ := a.settingsController.ServiceCogneeStatus()
	if dot != "run" || status != "启动中" {
		t.Fatalf("ServiceCogneeStatus = (%q, %q), want (\"run\", \"启动中\")", dot, status)
	}
	dual := a.settingsController.DualStateSummary()
	if !dual.GatewayHealthy || !dual.EngineStarting || dual.BacklogCount != 4 {
		t.Fatalf("unexpected starting DualStateSummary: %+v", dual)
	}

	// Poll 2: engine transitions starting -> ready -> must invalidate capabilities cache!
	phase.Store(1)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })

	if a.monitor.State != "ready" || a.monitor.EngineStatus != "已连接" {
		t.Fatalf("expected ready after recovery, got state=%q engine=%q", a.monitor.State, a.monitor.EngineStatus)
	}
	if ok, err := ctrlClient.ProbeFeature(context.Background(), "local_body"); err != nil || !ok {
		t.Fatalf("expected invalidated capabilities cache to refresh local_body=true, got (%v, %v)", ok, err)
	}
	if capsCalls.Load() != 2 {
		t.Fatalf("expected capabilities refetch after engine recovery, calls=%d", capsCalls.Load())
	}
}

func TestServiceMonitorIncompleteAndContradictoryPayloads(t *testing.T) {
	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("p", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	const controlURL = "http://127.0.0.1:61289"
	settings.InvalidateControlCapabilitiesCache(controlURL)
	t.Cleanup(func() { settings.InvalidateControlCapabilitiesCache(controlURL) })

	var monitorBody atomic.Value
	monitorBody.Store(`{}`)
	var capsCalls atomic.Int32

	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			capsCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"api_versions":       []string{"v2", "v1"},
				"reading_contract":   settings.ReadingContract,
				"engine_ready":       true,
				"capabilities_epoch": 1,
				"features":           map[string]bool{"local_body": true},
			})
		case "/v1/runtime/monitor":
			_, _ = w.Write([]byte(monitorBody.Load().(string)))
		case "/v1/completion/health":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_configured"}}`))
		default:
			http.NotFound(w, r)
		}
	})

	a := New(Config{
		ConfigFile:     "/synthetic/runtime.json",
		ProjectControl: &ProjectControlConfig{URL: controlURL, ManagementTokenFile: token},
	}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }

	// Prime capabilities cache (engine_ready=true, 5m TTL).
	ctrlClient := settings.ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	if _, err := ctrlClient.ProbeFeature(context.Background(), "local_body"); err != nil {
		t.Fatal(err)
	}
	if capsCalls.Load() != 1 {
		t.Fatalf("expected 1 capabilities fetch, got %d", capsCalls.Load())
	}

	// 1. Empty JSON {} -> state="unknown", EngineStatus="状态未知", ControlReachable=true, no cache invalidation.
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State != "unknown" || a.monitor.EngineStatus != "状态未知" || !a.monitor.ControlReachable || a.monitor.HasLatency {
		t.Fatalf("empty payload mishandled: state=%q engine=%q ctrl=%v hasLatency=%v",
			a.monitor.State, a.monitor.EngineStatus, a.monitor.ControlReachable, a.monitor.HasLatency)
	}
	if a.isExternalManagedStoreStopped() {
		t.Fatal("empty payload with reachable gateway must not trigger external-store-stopped")
	}
	if _, err := ctrlClient.ProbeFeature(context.Background(), "local_body"); err != nil || capsCalls.Load() != 1 {
		t.Fatalf("empty payload must not invalidate capabilities cache: calls=%d err=%v", capsCalls.Load(), err)
	}

	// 2. Contradictory payload: engine_ok=false + state=ready -> never ready, no cache invalidation.
	monitorBody.Store(`{"gateway_ok":true,"engine_ok":false,"state":"ready"}`)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State == "ready" || a.monitor.EngineStatus == "已连接" {
		t.Fatalf("contradictory engine_ok=false payload must not enter ready: state=%q engine=%q", a.monitor.State, a.monitor.EngineStatus)
	}
	if _, err := ctrlClient.ProbeFeature(context.Background(), "local_body"); err != nil || capsCalls.Load() != 1 {
		t.Fatalf("contradictory payload must not invalidate capabilities cache: calls=%d err=%v", capsCalls.Load(), err)
	}

	// 3. Legacy payload {"state":"ready"} without engine_ok -> enters ready and invalidates cache.
	monitorBody.Store(`{"state":"ready"}`)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State != "ready" || a.monitor.EngineStatus != "已连接" {
		t.Fatalf("legacy ready payload did not enter ready: state=%q engine=%q", a.monitor.State, a.monitor.EngineStatus)
	}
	if _, err := ctrlClient.ProbeFeature(context.Background(), "local_body"); err != nil || capsCalls.Load() != 2 {
		t.Fatalf("transition to ready must invalidate capabilities cache: calls=%d err=%v", capsCalls.Load(), err)
	}

	// 4. Explicit gateway_ok=false takes precedence over state=ready and engine_phase=starting.
	for _, payload := range []string{
		`{"gateway_ok":false,"state":"ready"}`,
		`{"gateway_ok":false,"engine_phase":"starting"}`,
	} {
		monitorBody.Store(payload)
		a.refreshServiceMonitor()
		drainReading(t, updates, func() bool { return !a.monitor.Polling })
		if a.monitor.State != "degraded" {
			t.Fatalf("payload %s: expected State=degraded, got %q", payload, a.monitor.State)
		}
		if dual := a.settingsController.DualStateSummary(); dual.GatewayHealthy {
			t.Fatalf("payload %s: expected GatewayHealthy=false, got %+v", payload, dual)
		}
	}

	// 5. Transition ready -> explicit engine_ok=false + extraction_paused=false surfaces degraded immediately (no debounce).
	monitorBody.Store(`{"gateway_ok":true,"engine_ok":true,"engine_phase":"ready","state":"ready"}`)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State != "ready" {
		t.Fatalf("expected ready before fault, got %q", a.monitor.State)
	}
	monitorBody.Store(`{"gateway_ok":true,"engine_ok":false,"engine_phase":"degraded","state":"degraded","last_error":"cognee probe timeout","inbox":{"extraction_paused":false}}`)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State != "degraded" || a.monitor.EngineStatus == "已连接" {
		t.Fatalf("explicit degraded snapshot must not be debounced as connected: state=%q engine=%q", a.monitor.State, a.monitor.EngineStatus)
	}
	dual := a.settingsController.DualStateSummary()
	if !dual.GatewayHealthy || !dual.EngineFault || dual.ExtractionPaused {
		t.Fatalf("expected GatewayHealthy=true EngineFault=true ExtractionPaused=false, got %+v", dual)
	}
}
