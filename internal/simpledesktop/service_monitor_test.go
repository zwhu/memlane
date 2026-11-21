package simpledesktop

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestServiceMonitorPollAndSettingsCardMetrics(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "control.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer official.Close()

	var monitorMode atomic.Int32
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				ServiceURL: official.URL,
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
	}))
	defer control.Close()

	a := New(Config{
		ServiceURL:     official.URL,
		ProjectControl: &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token},
	}, nil)
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

	if a.service != "已连接" || a.monitor.State != "ready" || !a.monitor.ControlReachable {
		t.Fatalf("unexpected monitor state after healthy poll: service=%q state=%q ctrl=%v err=%q",
			a.service, a.monitor.State, a.monitor.ControlReachable, a.monitor.ControlError)
	}
	summary := serviceMonitorSummaryLine(a.monitor)
	if !strings.Contains(summary, "内存 420 MB") || !strings.Contains(summary, "磁盘余量 82.0 GiB") {
		t.Fatalf("missing expected metrics in summary line: %q", summary)
	}

	// Render Settings -> 服务 expanded card & check diagnostic text does not leak tokens.
	a.monitor.Expanded = true
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.serviceConnectionOverview(c) })
	}, 1000, 700)
	if !tt.HasText("记忆服务") || !tt.HasText("控制面：已连接") || !tt.HasText("复制诊断") {
		t.Fatal("expected expanded service monitor details in settings card")
	}
	if tt.HasText("API Key") || tt.HasText("切换") {
		t.Fatal("service card must never expose generation controls")
	}

	// Simulate exited official PID from control monitor.
	monitorMode.Store(1)
	a.refreshServiceMonitor()
	drainReading(t, updates, func() bool { return !a.monitor.Polling })
	if a.monitor.State != "exited" || a.service != "服务已退出" {
		t.Fatalf("expected exited state, got state=%q service=%q", a.monitor.State, a.service)
	}
	tt.Frame()
	if !tt.HasText("服务未自动重启") || !tt.HasText("[REDACTED]") {
		t.Fatal("expected non-auto-restart notice and redacted log tail in UI")
	}
	diag := a.serviceDiagnosticText()
	if !strings.Contains(diag, "Cognee 服务状态: exited") || strings.Contains(diag, strings.Repeat("m", 40)) {
		t.Fatalf("unexpected diagnostic output: %q", diag)
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
	a.applyServiceMonitorResult(now, nil, 12.0, "未配置", false, nil, nil)
	if a.monitor.State != "ready" || a.service != "已连接" {
		t.Fatalf("expected ready, got %q / %q", a.monitor.State, a.service)
	}

	// Failures 1 & 2 enter warning while preserving connected indicator and reading content.
	for step := 1; step <= 2; step++ {
		a.applyServiceMonitorResult(now.Add(time.Duration(step)*10*time.Second), errors.New("timeout"), 0, "未配置", false, nil, nil)
		if a.monitor.State != "warning" || a.monitor.ConsecutiveFailures != step || a.service != "已连接" {
			t.Fatalf("step %d: expected warning preserving connected state, got state=%q failures=%d service=%q",
				step, a.monitor.State, a.monitor.ConsecutiveFailures, a.service)
		}
	}

	// 3rd failure enters degraded, still preserving reading pane items & selection.
	a.applyServiceMonitorResult(now.Add(30*time.Second), errors.New("connection refused"), 0, "未配置", false, nil, nil)
	if a.monitor.State != "degraded" || a.monitor.ConsecutiveFailures != 3 || a.service != "connection refused" {
		t.Fatalf("expected degraded on 3rd failure, got state=%q failures=%d service=%q",
			a.monitor.State, a.monitor.ConsecutiveFailures, a.service)
	}
	if len(p.Items) != 1 || p.Selected == nil || p.Selected.ID != "k1" {
		t.Fatal("monitor degradation cleared active reading state")
	}

	// Recovery resets consecutive failures and restores ready state.
	a.applyServiceMonitorResult(now.Add(40*time.Second), nil, 9.0, "未配置", false, nil, nil)
	if a.monitor.State != "ready" || a.monitor.ConsecutiveFailures != 0 || a.service != "已连接" {
		t.Fatalf("expected recovery to ready, got state=%q failures=%d service=%q",
			a.monitor.State, a.monitor.ConsecutiveFailures, a.service)
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
		ui.Column(c).FillWidth().Padding(24).Children(func() { a.serviceConnectionOverview(c) })
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

	diag := a.serviceDiagnosticText()
	if !strings.Contains(diag, "Hook 未记录数: 1") || !strings.Contains(diag, "Gemini App") || strings.Contains(diag, "\x1b") {
		t.Fatalf("unexpected diagnostic text: %q", diag)
	}
}

func TestSidebarServicePopoverAndChannelNaming(t *testing.T) {
	a := settingsFixture()
	a.service = "已连接"
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
	if a.monitor.PopoverOpen {
		t.Fatal("popover opened too early before 300ms hover delay")
	}
	drainReading(t, updates, func() bool { return a.monitor.PopoverOpen })

	tt := ui.NewTester(a.View, 1200, 820)
	for _, want := range []string{"Cognee 记忆服务", "控制面服务", "Agent 整理通道", "磁盘空间", "在设置中查看详情"} {
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
	drainReading(t, updates, func() bool { return !a.monitor.PopoverOpen })

	// 3) Keyboard focus opens popover immediately without waiting for timer.
	a.syncServicePopoverHover(false, false, true)
	if !a.monitor.PopoverOpen {
		t.Fatal("keyboard focus did not open service popover immediately")
	}
	tt.Frame()
	clickProjectUI(t, tt, "在设置中查看详情")
	if a.page != "设置" || a.access.SettingsSection != "服务" || a.monitor.PopoverOpen {
		t.Fatalf("detail click did not navigate to settings service tab: page=%q section=%q open=%v", a.page, a.access.SettingsSection, a.monitor.PopoverOpen)
	}

	// 4) Direct click on sidebar "服务 · 已连接" jumps straight to 设置 › 服务.
	a.navigate("总览", 0)
	tt.Frame()
	clickProjectUI(t, tt, "服务 · 已连接")
	if a.page != "设置" || a.access.SettingsSection != "服务" || a.monitor.PopoverOpen {
		t.Fatalf("direct service button click did not jump to settings service tab: page=%q section=%q open=%v", a.page, a.access.SettingsSection, a.monitor.PopoverOpen)
	}
}
