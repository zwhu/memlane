package settings

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

const sampleStatusTime = "2026-11-26T11:05:00.123456+00:00"

func TestCaptureStatusFormatting(t *testing.T) {
	ts, err := time.Parse(time.RFC3339Nano, sampleStatusTime)
	if err != nil {
		t.Fatal(err)
	}
	local := ts.Local().Format(eventTimeLayout)
	title, desc, tone := formatAutoCaptureStatus(false, sampleStatusTime, "user", "user_disabled")
	if title != "自动采集已关闭" || tone != "neutral" || !strings.Contains(desc, local+" · 由用户关闭") {
		t.Fatalf("%s / %s / %s", title, desc, tone)
	}
	_, desc, _ = formatAutoCaptureStatus(false, "", "", "")
	if !strings.Contains(desc, "时间未知 · 原因未记录") {
		t.Fatal(desc)
	}
	_, desc, _ = formatAutoCaptureStatus(false, sampleStatusTime, "system", "maintenance")
	if !strings.Contains(desc, "由系统暂停") {
		t.Fatal(desc)
	}
	title, desc, tone = formatAutoCaptureStatus(true, sampleStatusTime, "", "")
	if title != "自动采集已开启 · 正常" || tone != "ok" || !strings.Contains(desc, "开启于 "+local) {
		t.Fatalf("%s / %s / %s", title, desc, tone)
	}
	headline, detail, _ := formatInboxFailureUI("提炼服务连接超时", "", nil)
	if headline != "提炼服务连接超时" || !strings.Contains(detail, "时间未知") || !strings.Contains(detail, "一键重试") {
		t.Fatalf("%s / %s", headline, detail)
	}
}

func TestProjectQueueShowsReadableFilterAndExpandableFailure(t *testing.T) {
	a, q := projectQueueFixture()
	q.InboxPending, q.InboxFailed = 0, 1
	q.LastError = "dial tcp: connection refused"
	q.RecentFailures = []api.InboxFailure{{ID: "failure-id-1", Attempts: 2, LastError: q.LastError,
		ErrorCode: "engine_unavailable", Summary: "提炼服务暂时无法连接", LastErrorAt: sampleStatusTime}}
	const reason = "TRAE CLI 补丁路径命中 `*.key` 规则，记录保留在本机（规则命中本身不足以确认补丁包含密钥）"
	q.FilteredCount = 2
	q.RecentFiltered = []api.CaptureFilterEvent{
		{ID: "filter-1", Rule: "*.key", Reason: reason, NextAction: "记录未上传，无需手动重试。", OccurredAt: sampleStatusTime},
		{ID: "filter-2", Rule: "*.pem", Reason: "TRAE CLI 补丁路径命中 `*.pem` 规则"},
	}
	p := a.access.Directory.Projects[0]
	t.Cleanup(func() { applyAppearance(false) })
	tt := ui.NewTester(func(c *ui.Context) {
		applyAppearance(a.effectiveDark(c.Theme().Dark))
		ui.Themed(c, homeTheme).Fill().Background(a.tokens().Bg).Children(func() { a.projectQueueSection(c, p) })
	}, 1200, 820)
	fixtureImage(t, tt, "capture-status-light")
	for _, text := range []string{"自动采集已开启 · 正常", "因采集规则未采集", reason, formatEventTimeLocal(sampleStatusTime),
		"记录未上传，无需手动重试", "提炼服务暂时无法连接", "一键重试"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	for _, text := range []string{"failure-id-1", "engine_unavailable", "dial tcp", "certs/server.key", "-----BEGIN PRIVATE KEY-----"} {
		if tt.HasText(text) {
			t.Fatalf("exposed %q in summary", text)
		}
	}
	clickProjectUI(t, tt, "查看技术详情")
	for _, text := range []string{"failure-id-1", "尝试 2 次", "engine_unavailable", "dial tcp: connection refused"} {
		if !tt.HasText(text) {
			t.Fatalf("missing diagnostic %q", text)
		}
	}
	clickProjectUI(t, tt, "收起技术详情")
	clickProjectUI(t, tt, "查看近期记录")
	if !tt.HasText("*.pem") || !tt.HasText("时间未知") || tt.HasText("failure-id-1") {
		t.Fatalf("unexpected expanded filters: %v", tt.Texts())
	}
	q.ShowFilteredDetails = false
	a.appearance = "dark"
	tt.Frame()
	if !tt.HasText(reason) || !tt.HasText("提炼服务暂时无法连接") {
		t.Fatal("capture status is missing in dark appearance")
	}
	fixtureImage(t, tt, "capture-status-dark")
	q.InboxFailed, q.LastError = 0, ""
	tt.Frame()
	if !tt.HasText("自动采集已开启 · 正常") || !tt.HasText("因采集规则未采集") {
		t.Fatal("filtered-only project lost a status row")
	}
}

func TestMonitorAndHookMessagesShowUnknownTimes(t *testing.T) {
	a := settingsFixture()
	a.monitor.State = "degraded"
	a.monitor.LastError = "dial tcp: connection refused"
	a.monitor.Snapshot = &ServiceMonitorSnapshot{State: "degraded", StatusSummary: "提炼服务暂时无法连接",
		NextAction: "请检查服务状态", Summary: &MonitorBusinessSummary{FilteredCount: 1, RecentFiltered: []api.CaptureFilterEvent{
			{ID: "filtered", Rule: "*.key", Reason: "TRAE CLI 补丁路径命中 `*.key` 规则"},
		}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() { a.serviceMonitorAlerts(c) })
	}, 1000, 650)
	if !tt.HasText("提炼服务暂时无法连接 · 时间未知 · 请检查服务状态") ||
		!tt.HasText("时间未知 · TRAE CLI 补丁路径命中") || tt.HasText("dial tcp") {
		t.Fatalf("unexpected monitor alerts: %v", tt.Texts())
	}
	report := a.ServiceDiagnosticText()
	for _, want := range []string{"最近检查时间：时间未知", "最近正常时间：时间未知", "*.key · 时间未知", "dial tcp"} {
		if !strings.Contains(report, want) {
			t.Fatalf("monitor diagnostic missing %q", want)
		}
	}
	if got := formatMonitorHookErrorLine(MonitorHookError{Kind: "traex", Reason: "apply_patch_key_rule"}); !strings.Contains(got, "TRAE CLI · 时间未知") || !strings.Contains(got, "*.key") {
		t.Fatal(got)
	}
	if got := captureSavedTip(AgentConnection{Capture: &AgentCaptureState{State: "healthy"}}); got != "最近采集：时间未知" {
		t.Fatal(got)
	}
	if got, _ := formatServiceLogLine("failed with Bearer [REDACTED]"); got != "时间未知 · failed with Bearer [REDACTED]" {
		t.Fatal(got)
	}
}
