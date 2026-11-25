package simpledesktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSlowSidebarProjectDoesNotBlockOtherProjectBadges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "processing-status") {
			w.Write([]byte(`{"items":[]}`))
		} else {
			w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	a := settingsFixture()
	a.Client.Config.ServiceURL = server.URL
	a.access.Directory.Bindings = nil
	a.access.Directory.Projects = append(a.access.Directory.Projects,
		AccessProject{ID: "third", Dataset: "third"}, AccessProject{ID: "fourth", Dataset: "fourth"})
	a.access.Queues = nil
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	slow := a.projectQueueStateFor(a.access.Directory.Projects[0])
	slow.Loading = true
	fast := a.projectQueueStateFor(a.access.Directory.Projects[1])
	a.refreshSidebarQueueIfDue(time.Now())
	if !fast.Loading {
		t.Fatal("a slow project blocked the entire sidebar queue")
	}
	active := 0
	for _, q := range a.access.Queues {
		if q.Loading {
			active++
		}
	}
	if active != 3 || a.projectQueueStateFor(a.access.Directory.Projects[3]).Loading {
		t.Fatal("sidebar project reads exceeded the concurrency limit")
	}
	drainReading(t, updates, func() bool { return !fast.Loading })
	if !fast.Loaded || !slow.Loading {
		t.Fatal("independent project could not finish while the slow project remained pending")
	}
}

func TestSidebarShowsUnreadCountWithoutProjectAlertDot(t *testing.T) {
	t.Cleanup(func() { applyAppearance(false) })
	a, q := projectQueueFixture()
	q.Loaded = true
	q.KnowledgeRead = knowledgeReadState{Read: map[string]bool{"unread": false}}
	if _, isTemplate, _ := a.TrayIconState(); isTemplate {
		t.Fatal("project anomaly did not mark the macOS status icon")
	}

	a.page = "总览"
	tt := ui.NewTester(a.View, 1200, 820)
	for _, dark := range []bool{false, true} {
		applyAppearance(dark)
		tt.SetDark(dark)
		tt.Frame()
		if !tt.HasText("未读记忆：memlane") || tt.HasText("待处理文档：memlane") {
			t.Fatalf("sidebar did not show only the unread badge in dark=%t", dark)
		}
		name := "project-sidebar-no-alert-dot-light"
		if dark {
			name = "project-sidebar-no-alert-dot-dark"
		}
		fixtureImage(t, tt, name)
	}
	a.openProjectSettings(0)
	tt.Frame()
	if !tt.HasText("1 条消息未收到采集结果") {
		t.Fatal("project management lost the anomaly")
	}
}

func TestProjectAlertShortcutScrollsDirectlyToStatusAnomaly(t *testing.T) {
	t.Cleanup(func() { applyAppearance(false) })

	setupApp := func() (*App, AccessProject, *projectQueueState) {
		a := settingsFixture()
		agents := []AccessAgent{
			{ID: "gemini", Name: "Gemini App", Installation: &AgentInstallation{HostKind: "antigravity", Status: "configured"}},
			{ID: "claude", Name: "Claude Code", Installation: &AgentInstallation{HostKind: "claude_code", Status: "configured"}},
			{ID: "agent", Name: "Codex", Installation: &AgentInstallation{HostKind: "codex", Status: "configured"}},
			{ID: "opencode", Name: "OpenCode", Installation: &AgentInstallation{HostKind: "opencode", Status: "configured"}},
			{ID: "pi", Name: "pi", Installation: &AgentInstallation{HostKind: "pi", Status: "configured"}},
			{ID: "traex", Name: "TraeX", Installation: &AgentInstallation{HostKind: "traex", Status: "configured"}},
		}
		dir := a.access.Directory
		dir.Agents = agents
		a.applyAccessDirectory(dir)
		p := a.access.Directory.Projects[0]
		q := a.projectQueueStateFor(p)
		q.Loaded = true
		q.Next = time.Now().Add(time.Hour)
		q.BacklogLoaded = true
		q.BacklogNext = time.Now().Add(time.Hour)
		return a, p, q
	}

	t.Run("info anomaly after receipt", func(t *testing.T) {
		a, p, q := setupApp()
		q.Sessions = []queuedSession{
			{
				Agent:   a.access.Directory.Agents[2],
				Binding: ProjectBinding{ProjectID: p.ID, AgentID: "agent", HostSessionKey: "codex-ok"},
				View:    &pendingBufferView{Loaded: true, Message: "上次采集已完成", Status: PendingMaterials{Pending: 0}},
			},
			{
				Agent:   a.access.Directory.Agents[5],
				Binding: ProjectBinding{ProjectID: p.ID, AgentID: "traex", HostSessionKey: "traex-unknown"},
				View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
			},
		}
		wantKey := noticeSessionKey(q.Sessions[1], 1) + "/unknown"
		a.page = "总览"
		tt := ui.NewTester(a.View, 1200, 820)

		a.openProjectQueueStatus(0)
		tt.Frame()
		if a.access.OpenProject != p.ID || a.page != "知识" || a.kind != "管理" {
			t.Fatalf("did not open project management tab: page=%q kind=%q open=%q", a.page, a.kind, a.access.OpenProject)
		}
		if a.access.RevealQueueProject != "" {
			t.Fatalf("expected RevealQueueProject cleared after reveal, got %q", a.access.RevealQueueProject)
		}
		if q.LocatedNoticeKey != wantKey {
			t.Fatalf("LocatedNoticeKey = %q, want %q", q.LocatedNoticeKey, wantKey)
		}
		r, ok := tt.Find("1 条消息未收到采集结果")
		if !ok || r.Y < 80 || r.Y+r.H > 820 {
			t.Fatalf("target anomaly not visible inside viewport: ok=%v bounds=%+v", ok, r)
		}

		// Scroll down away from top while already on 管理 tab and use the project alert shortcut again.
		q.Scroll.Y = 300
		tt.Frame()
		a.openProjectQueueStatus(0)
		tt.Frame()
		if r2, ok := tt.Find("1 条消息未收到采集结果"); !ok || r2.Y < 80 || r2.Y+r2.H > 820 {
			t.Fatalf("target anomaly not visible after repeated click: ok=%v bounds=%+v", ok, r2)
		}

		// Regular openProjectSettings must stay at top and clear LocatedNoticeKey.
		a.openProjectSettings(0)
		tt.Frame()
		if q.LocatedNoticeKey != "" || a.access.RevealQueueProject != "" {
			t.Fatalf("openProjectSettings leaked target state: located=%q reveal=%q", q.LocatedNoticeKey, a.access.RevealQueueProject)
		}

		// Render in both light and dark appearances without issues.
		for _, dark := range []bool{false, true} {
			applyAppearance(dark)
			tt.SetDark(dark)
			tt.Frame()
		}
	})

	t.Run("needs user anomaly", func(t *testing.T) {
		a, p, q := setupApp()
		q.Sessions = []queuedSession{
			{
				Agent:   a.access.Directory.Agents[2],
				Binding: ProjectBinding{ProjectID: p.ID, AgentID: "agent", HostSessionKey: "codex-denied"},
				View: &pendingBufferView{
					Loaded: true,
					Status: PendingMaterials{
						Pending: 2,
						Blocked: &struct {
							Reason string `json:"reason"`
						}{Reason: "write_denied"},
					},
				},
			},
		}
		wantKey := noticeSessionKey(q.Sessions[0], 0) + "/pending"
		a.page = "总览"
		tt := ui.NewTester(a.View, 1200, 820)

		a.openProjectQueueStatus(0)
		tt.Frame()
		if q.LocatedNoticeKey != wantKey {
			t.Fatalf("LocatedNoticeKey = %q, want %q", q.LocatedNoticeKey, wantKey)
		}
		if r, ok := tt.Find("2 条消息等待采集"); !ok || r.Y < 80 || r.Y+r.H > 820 {
			t.Fatalf("needs-user anomaly not visible inside viewport: ok=%v bounds=%+v", ok, r)
		}
	})
}

func TestProjectActiveMessageAlertsAndTrayProjectAlerts(t *testing.T) {
	a := settingsFixture()
	p0 := a.access.Directory.Projects[0]
	q0 := a.projectQueueStateFor(p0)
	q0.Loaded = true
	q0.BacklogLoaded = true

	// Pure queued documents awaiting cognify must NOT trigger yellow alert dots.
	q0.Documents = []Item{{ID: "doc-1", Dataset: p0.Dataset, Title: "queued.md"}}
	if a.projectHasActiveMessageAlerts(p0) {
		t.Fatal("queued documents alone should not trigger active message alerts")
	}
	if alerts := a.trayProjectAlerts(); len(alerts) != 0 {
		t.Fatalf("expected no tray alerts for queued docs only, got %+v", alerts)
	}

	// Pure completed receipt notice must NOT trigger active alert dots.
	q0.Sessions = []queuedSession{{
		Agent:   a.access.Directory.Agents[0],
		Binding: ProjectBinding{ProjectID: p0.ID, AgentID: a.access.Directory.Agents[0].ID, HostSessionKey: "ok-sess"},
		View:    &pendingBufferView{Loaded: true, Message: "上次采集已完成", Status: PendingMaterials{Pending: 0}},
	}}
	if a.projectHasActiveMessageAlerts(p0) {
		t.Fatal("receipt-only info should not trigger active message alerts")
	}

	// Add 1 needs-user alert (permission denied) and 1 system anomaly (unknown outcome).
	read := "read"
	p0.Overrides[a.access.Directory.Agents[0].ID] = ProjectOverride{Access: &read}
	a.access.Directory.Projects[0] = p0
	q0.Sessions = []queuedSession{
		{
			Agent:   a.access.Directory.Agents[0],
			Binding: ProjectBinding{ProjectID: p0.ID, AgentID: a.access.Directory.Agents[0].ID, HostSessionKey: "s-needs"},
			View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 2}},
		},
		{
			Agent:   a.access.Directory.Agents[0],
			Binding: ProjectBinding{ProjectID: p0.ID, AgentID: a.access.Directory.Agents[0].ID, HostSessionKey: "s-err"},
			View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
		},
	}
	if !a.projectHasActiveMessageAlerts(p0) {
		t.Fatal("expected active message alerts for needs + error")
	}
	alerts := a.trayProjectAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 tray alert, got %+v", alerts)
	}
	if alerts[0].ProjectID != p0.ID || alerts[0].ProjectIndex != 0 || alerts[0].ActionCount != 1 || alerts[0].ErrorCount != 1 {
		t.Fatalf("unexpected tray alert summary: %+v", alerts[0])
	}
}

func TestInProgressAndNeutralNoticesDoNotCountAsProjectAlerts(t *testing.T) {
	a := settingsFixture()
	agent := a.access.Directory.Agents[0]
	p0 := a.access.Directory.Projects[0]
	maintain := "maintain"
	p0.Overrides[agent.ID] = ProjectOverride{Access: &maintain}
	a.access.Directory.Projects[0] = p0
	q0 := a.projectQueueStateFor(p0)
	q0.Loaded = true
	q0.BacklogLoaded = true
	q0.AutomaticSave = true
	q0.Documents = []Item{{ID: "doc-1", Dataset: p0.Dataset, Title: "queued.md"}}
	q0.Backlog = &backlogJob{
		Status:            "watching",
		ProcessingActive:  1,
		ProcessingPartial: 1,
	}
	q0.Sessions = []queuedSession{
		{
			Agent:   agent,
			Binding: ProjectBinding{ProjectID: p0.ID, AgentID: agent.ID, HostSessionKey: "s-active"},
			View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 4, Signature: "sig-a"}},
		},
		{
			Agent:   agent,
			Binding: ProjectBinding{ProjectID: p0.ID, AgentID: agent.ID, HostSessionKey: "s-receipt"},
			View:    &pendingBufferView{Loaded: true, Message: "上次采集已完成", Status: PendingMaterials{Pending: 0}},
		},
	}
	q0.BufferReviews = []bufferReview{{AgentID: agent.ID, Host: "s-active", Signature: "sig-a", Filtered: 2}}

	actionCount, errorCount := a.countProjectMessageAlerts(p0)
	if actionCount != 0 || errorCount != 0 {
		t.Fatalf("expected 0 alerts for normal in-progress/filtered/receipt state, got action=%d error=%d", actionCount, errorCount)
	}
	if a.projectHasActiveMessageAlerts(p0) {
		t.Fatal("in-progress state must not pin alerts or show orange dot")
	}
	if alerts := a.trayProjectAlerts(); len(alerts) != 0 {
		t.Fatalf("expected empty trayProjectAlerts, got %+v", alerts)
	}
	progress := a.trayProjectProgressItems()
	if len(progress) != 1 || progress[0].ProjectID != p0.ID || progress[0].Count != 4 {
		// 1 pending ("待采集") + 1 ProcessingActive ("处理中") + 1 ProcessingPartial ("等待接续") + 1 queued document = 4; Filtered/Receipt/Paused excluded.
		t.Fatalf("unexpected trayProjectProgressItems: %+v", progress)
	}

	// Non-actionable user_stopped pause (0 recoverable) is also neutral and excluded from both alerts and active progress.
	q0.Documents = nil
	q0.Sessions = nil
	q0.Backlog = &backlogJob{Status: "stopped", Reason: "user_stopped"}
	if a.projectHasActiveMessageAlerts(p0) {
		t.Fatal("non-actionable user_stopped pause should not trigger active alerts")
	}
	if got := a.trayProjectProgressItems(); len(got) != 0 {
		t.Fatalf("paused backlog with 0 recoverable should not show as active progress, got %+v", got)
	}

	// Mixed state: 1 real anomaly + 2 active progress items -> alerts count = 1, progress count = 2.
	q0.Backlog = &backlogJob{Status: "watching", ProcessingActive: 1}
	q0.Sessions = []queuedSession{
		{
			Agent:   agent,
			Binding: ProjectBinding{ProjectID: p0.ID, AgentID: agent.ID, HostSessionKey: "s-wait"},
			View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 2}},
		},
		{
			Agent:   agent,
			Binding: ProjectBinding{ProjectID: p0.ID, AgentID: agent.ID, HostSessionKey: "s-err"},
			View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
		},
	}
	actionCount, errorCount = a.countProjectMessageAlerts(p0)
	if actionCount != 0 || errorCount != 1 {
		t.Fatalf("expected action=0 error=1 in mixed state, got action=%d error=%d", actionCount, errorCount)
	}
	progress = a.trayProjectProgressItems()
	if len(progress) != 1 || progress[0].Count != 2 {
		t.Fatalf("expected progress count=2 in mixed state, got %+v", progress)
	}
}

func TestClosedAutomaticCaptureDoesNotCreatePermissionOrProgressBadge(t *testing.T) {
	a, q := projectQueueFixture()
	p := &a.access.Directory.Projects[0]
	agent := a.access.Directory.Agents[0]
	access := "maintain"
	p.Overrides[agent.ID] = ProjectOverride{Access: &access}
	q.Sessions = q.Sessions[:1]
	q.Documents = nil
	q.AutomaticSave = true
	q.Backlog = &backlogJob{Automatic: true, Status: "running"}
	if got := a.trayCachedSummary(); len(got.Alerts) != 0 || len(got.Progress) != 1 {
		t.Fatalf("expected one active capture, got %+v", got)
	}
	enabled := false
	p.AutoCapture, p.AutoCaptureVersion = &enabled, 2
	if got := a.trayCachedSummary(); len(got.Alerts) != 0 || len(got.Progress) != 0 {
		t.Fatalf("closed capture still appeared active or denied, got %+v", got)
	}
	q.Backlog = &backlogJob{Automatic: true, Status: "paused", Reason: "project_auto_capture_disabled"}
	if got := a.trayCachedSummary(); len(got.Alerts) != 0 || len(got.Progress) != 0 {
		t.Fatalf("intentional stop became a badge, got %+v", got)
	}
	q.Backlog = &backlogJob{Status: "running"}
	if got := a.trayCachedSummary(); len(got.Alerts) != 0 || len(got.Progress) != 1 {
		t.Fatalf("closed capture hid manual job progress, got %+v", got)
	}
	access = "read"
	if got := a.trayCachedSummary(); len(got.Alerts) != 1 || got.Alerts[0].ActionCount != 1 {
		t.Fatalf("closed capture hid an actual write restriction, got %+v", got)
	}
}
