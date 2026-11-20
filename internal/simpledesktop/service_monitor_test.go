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
