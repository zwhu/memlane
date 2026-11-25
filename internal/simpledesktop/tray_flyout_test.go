package simpledesktop

import (
	"bytes"
	"fmt"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestTrayIconPNGVariantsAndCaching(t *testing.T) {
	a := settingsFixture()
	a.service = "已连接"
	a.monitor.State = "ready"

	healthy1, tmpl1, tip1 := a.TrayIconState()
	if !tmpl1 {
		t.Fatal("healthy tray icon must be a template image")
	}
	if tip1 != "Memlane" {
		t.Fatalf("unexpected healthy tooltip: %q", tip1)
	}
	img, err := png.Decode(bytes.NewReader(healthy1))
	if err != nil {
		t.Fatalf("healthy icon is not valid PNG: %v", err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Fatalf("expected 32x32 Retina tray icon, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	healthy2, _, _ := a.TrayIconState()
	if &healthy1[0] != &healthy2[0] {
		t.Fatal("expected cached tray PNG bytes to be reused with zero re-encoding")
	}

	// Warning state (light and dark menu bar variants, non-template).
	a.monitor.State = "warning"
	a.SetTrayDark(false)
	warnLight, tmplWarnLight, _ := a.TrayIconState()
	if tmplWarnLight {
		t.Fatal("warning tray icon must not be a template image")
	}
	a.SetTrayDark(true)
	warnDark, tmplWarnDark, _ := a.TrayIconState()
	if tmplWarnDark {
		t.Fatal("dark warning tray icon must not be a template image")
	}
	if bytes.Equal(warnLight, warnDark) {
		t.Fatal("light and dark warning tray icons should differ in foreground ink")
	}

	// Danger state.
	a.monitor.State = "degraded"
	a.SetTrayDark(false)
	dangerLight, tmplDanger, _ := a.TrayIconState()
	if tmplDanger {
		t.Fatal("danger tray icon must not be a template image")
	}
	if bytes.Equal(dangerLight, warnLight) {
		t.Fatal("danger and warning tray icons should differ")
	}
}

func TestTrayFlyoutViewStructureAlertsSyncAndQuitActions(t *testing.T) {
	dir := t.TempDir()
	a := settingsFixture()
	a.Config.ConfigFile = filepath.Join(dir, "runtime.json")
	a.initDesktopPreferences()
	a.service = "已连接"
	a.monitor.State = "ready"
	a.monitor.HasLatency = true
	a.monitor.LastLatencyMs = 14
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = true
	a.completion = "端点已连接（登录状态以实际请求为准）"

	nowRFC := time.Now().Format(time.RFC3339)
	a.overviewState.Loaded = true
	a.overviewState.Snapshot = overviewSnapshot{
		Knowledge: []Item{
			{ID: "k1", Dataset: "ds", Kind: "知识", Date: nowRFC, Document: &Document{ID: "k1", Created: nowRFC}},
			{ID: "k2", Dataset: "ds", Kind: "知识", Date: nowRFC, Document: &Document{ID: "k2", Created: nowRFC}},
		},
	}

	// Add a failed inbox item on project 0 ("memlane").
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.InboxFailed = 1

	var shownMain, hidFlyout, openedSearch bool
	var quitCalls []bool
	a.ShowMainWindowFn = func() { shownMain = true }
	a.HideTrayFlyoutFn = func() { hidFlyout = true }
	a.OpenGlobalSearchFn = func() { openedSearch = true }
	a.QuitAppFn = func(stopServices bool) { quitCalls = append(quitCalls, stopServices) }

	tt := ui.NewTester(a.TrayFlyoutView, 300, 480)
	for _, want := range []string{
		"服务 · 已连接",
		"记忆引擎（Cognee）",
		"接入网关",
		"Agent 模型代理",
		"今日新增记忆",
		"2 条",
		"项目待处理提醒",
		"memlane",
		"退出界面（后台保活）",
		"彻底停止并退出",
	} {
		if !tt.HasText(want) {
			t.Fatalf("tray flyout missing expected text %q; got %v", want, tt.Texts())
		}
	}

	// Clicking project alert hides flyout, shows main window, and opens project queue status.
	if err := tt.Click("打开项目提醒：memlane"); err != nil {
		t.Fatalf("failed to click project alert quick-jump button: %v", err)
	}
	if !hidFlyout || !shownMain {
		t.Fatalf("expected flyout hide and main window show, got hide=%v show=%v", hidFlyout, shownMain)
	}
	if a.kind != "管理" || a.access.RevealQueueProject != "p" {
		t.Fatalf("expected openProjectQueueStatus for project p, got kind=%q reveal=%q", a.kind, a.access.RevealQueueProject)
	}

	// Toggling keep-alive off switches footer button to 退出并停止服务 and hides 彻底停止并退出.
	if err := tt.Click("退出时保持后台运行开关"); err != nil {
		t.Fatalf("failed to click keep-alive switch in tray flyout: %v", err)
	}
	if a.KeepAliveOnQuit() {
		t.Fatal("expected KeepAliveOnQuit=false after clicking switch")
	}
	if !tt.HasText("退出并停止服务") || tt.HasText("彻底停止并退出") {
		t.Fatalf("footer should update when KeepAliveOnQuit=false; texts=%v", tt.Texts())
	}

	// Clicking primary quit button invokes QuitApp(true) when keep-alive is false.
	if err := tt.Click("退出并停止服务"); err != nil {
		t.Fatalf("failed to click 退出并停止服务 button: %v", err)
	}
	if len(quitCalls) != 1 || !quitCalls[0] {
		t.Fatalf("expected QuitApp(true), got %v", quitCalls)
	}

	// Clicking ⌘K search icon invokes OpenGlobalSearchFn.
	if err := tt.Click("⌘K 搜索"); err != nil {
		t.Fatalf("failed to click ⌘K 搜索 icon button: %v", err)
	}
	if !openedSearch {
		t.Fatal("expected OpenGlobalSearchFn to be called")
	}

	// Clearing alerts shows quiet healthy row.
	q.InboxFailed = 0
	tt.Frame()
	if !tt.HasText("所有项目消息状态正常") {
		t.Fatalf("expected quiet healthy message when no alerts exist; texts=%v", tt.Texts())
	}
}

func TestV4BadgeAndTrayAlertVerticalAlignment(t *testing.T) {
	abs := func(v float32) float32 {
		if v < 0 {
			return -v
		}
		return v
	}

	var badgeBox, countBox, kbdBox ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Padding(10).Gap(12).AlignItems(ui.Center).Children(func() {
			b := v4Badge(c, "提炼异常", "danger")
			cnt := v4CountBadge(c, "3", true)
			k := ui.Row(c).Children(func() { v4KbdHint(c, "↵", "打开") })
			badgeBox = b.Bounds()
			countBox = cnt.Bounds()
			kbdBox = k.Bounds()
		})
	}, 400, 100)
	// Second frame lets positional nodes read Frame 1's laid-out bounds safely inside build.
	tt.Frame()

	for _, tc := range []struct {
		name  string
		text  string
		outer ui.Rect
	}{
		{"v4Badge", "提炼异常", badgeBox},
		{"v4CountBadge", "3", countBox},
		{"v4KbdHint", "↵", kbdBox},
	} {
		tr, ok := tt.Find(tc.text)
		if !ok {
			t.Fatalf("%s text %q not found in %v", tc.name, tc.text, tt.Texts())
		}
		ob := tc.outer
		if ob.H <= 0 || tr.H <= 0 {
			t.Fatalf("%s invalid bounds outer=%+v text=%+v", tc.name, ob, tr)
		}
		if tr.Y <= ob.Y {
			t.Fatalf("%s text %q is pinned to top instead of vertically centered: outer=%+v text=%+v", tc.name, tc.text, ob, tr)
		}
		outerCenter := ob.Y + ob.H/2
		textCenter := tr.Y + tr.H/2
		if diff := abs(textCenter - outerCenter); diff > 1.0 {
			t.Fatalf("%s vertical center mismatch %.2f DIP (outer=%+v text=%+v)", tc.name, diff, ob, tr)
		}
	}

	a := settingsFixture()
	a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
	a.initDesktopPreferences()
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.InboxFailed = 1
	ttTray := ui.NewTester(a.TrayFlyoutView, 300, 480)
	memRect, ok := ttTray.Find("memlane")
	if !ok {
		t.Fatalf("missing memlane alert text: %v", ttTray.Texts())
	}
	badgeRect, ok := ttTray.Find("1 项待处理")
	if !ok {
		t.Fatalf("missing tray badge text: %v", ttTray.Texts())
	}
	memVisualCenter := memRect.Y + (memRect.H-1)/2
	badgeCenter := badgeRect.Y + badgeRect.H/2
	if diff := abs(badgeCenter - memVisualCenter); diff > 1.5 {
		t.Fatalf("tray alert row text/badge vertical mismatch %.2f DIP (mem=%+v badge=%+v)", diff, memRect, badgeRect)
	}
}

func TestTrayIconAndFlyoutTreatInProgressStateAsHealthyLightAndDark(t *testing.T) {
	for _, tc := range []struct {
		name       string
		appearance string
		trayDark   bool
	}{
		{name: "light", appearance: "light", trayDark: false},
		{name: "dark", appearance: "dark", trayDark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := settingsFixture()
			a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
			a.initDesktopPreferences()
			a.setAppearancePreference(tc.appearance)
			a.SetTrayDark(tc.trayDark)
			a.service = "已连接"
			a.monitor.State = "ready"

			p := a.access.Directory.Projects[0]
			agent := a.access.Directory.Agents[0]
			maintain := "maintain"
			p.Overrides[agent.ID] = ProjectOverride{Access: &maintain}
			a.access.Directory.Projects[0] = p
			q := a.projectQueueStateFor(p)
			q.Loaded = true
			q.Backlog = &backlogJob{Status: "watching", ProcessingActive: 1}
			q.InboxPending = 2

			// 1. Menu bar tray icon must stay healthy template icon (no warning/danger badge dot).
			_, isTemplate, tip := a.TrayIconState()
			if !isTemplate || tip != "Memlane" {
				t.Fatalf("[%s] expected healthy template tray icon for pure in-progress queue, got isTemplate=%v tip=%q", tc.name, isTemplate, tip)
			}

			var shownMain, hidFlyout bool
			a.ShowMainWindowFn = func() { shownMain = true }
			a.HideTrayFlyoutFn = func() { hidFlyout = true }

			// 2. Flyout must show quiet healthy status + 进行中与待提炼 section, never 项目待处理提醒.
			tt := ui.NewTester(a.TrayFlyoutView, 300, 480)
			for _, want := range []string{"所有项目消息状态正常", "进行中与待提炼", "memlane", "2 项进行中"} {
				if !tt.HasText(want) {
					t.Fatalf("[%s] tray flyout missing %q; texts=%v", tc.name, want, tt.Texts())
				}
			}
			for _, unwanted := range []string{"项目待处理提醒", "2 项待处理", "1 项待处理"} {
				if tt.HasText(unwanted) {
					t.Fatalf("[%s] tray flyout unexpectedly showed alert text %q; texts=%v", tc.name, unwanted, tt.Texts())
				}
			}

			// Clicking the progress item jumps to the project's Manage tab.
			if err := tt.Click("查看项目进度：memlane"); err != nil {
				t.Fatalf("[%s] failed to click progress row: %v", tc.name, err)
			}
			if !hidFlyout || !shownMain || a.kind != "管理" || a.access.RevealQueueProject != p.ID {
				t.Fatalf("[%s] click did not open project queue status: hide=%v show=%v kind=%q reveal=%q", tc.name, hidFlyout, shownMain, a.kind, a.access.RevealQueueProject)
			}

			// 3. Mixed state: adding 1 real anomaly switches tray icon to danger and shows both sections accurately.
			q.InboxFailed = 1
			_, isTemplateMixed, mixedTip := a.TrayIconState()
			if isTemplateMixed || mixedTip != "Memlane · 服务或项目存在异常" {
				t.Fatalf("[%s] expected danger tray icon when real anomaly exists, got isTemplate=%v tip=%q", tc.name, isTemplateMixed, mixedTip)
			}
			tt.Frame()
			for _, want := range []string{"项目待处理提醒", "1 项待处理", "进行中与待提炼", "2 项进行中"} {
				if !tt.HasText(want) {
					t.Fatalf("[%s] mixed tray flyout missing %q; texts=%v", tc.name, want, tt.Texts())
				}
			}
		})
	}
}

func TestTrayFlyoutControlAndChannelLatencies(t *testing.T) {
	a := settingsFixture()
	a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
	a.initDesktopPreferences()
	a.Config.CompletionURL = "http://127.0.0.1:18173"
	a.service = "已连接"
	a.completion = "端点已连接（登录状态以实际请求为准）"
	a.monitor.State = "ready"
	a.monitor.HasLatency = true
	a.monitor.LastLatencyMs = 9.0
	a.monitor.ControlChecked = true
	a.monitor.ControlReachable = true
	a.monitor.HasControlLatency = true
	a.monitor.ControlLatencyMs = 18.0
	a.monitor.HasCompletionLatency = true
	a.monitor.CompletionLatencyMs = 3.0

	recentGenMs := 41494.0
	a.monitor.Snapshot = &ServiceMonitorSnapshot{
		State: "ready",
		Summary: &MonitorBusinessSummary{
			ProjectsCount:      9,
			RecentProcessingMs: &recentGenMs,
		},
	}

	ctrlDot, ctrlMetric, _ := a.trayControlStatus()
	if ctrlDot != "ok" || ctrlMetric != "已连接 · 18 ms" {
		t.Fatalf("trayControlStatus() = (%q, %q), want (%q, %q)", ctrlDot, ctrlMetric, "ok", "已连接 · 18 ms")
	}
	chDot, chMetric, _ := a.trayChannelStatus()
	if chDot != "ok" || chMetric != "已连接 · 3 ms" {
		t.Fatalf("trayChannelStatus() = (%q, %q), want (%q, %q)", chDot, chMetric, "ok", "已连接 · 3 ms")
	}

	tt := ui.NewTester(a.TrayFlyoutView, 300, 480)
	for _, want := range []string{"已连接 · 9 ms", "已连接 · 18 ms", "已连接 · 3 ms"} {
		if !tt.HasText(want) {
			t.Fatalf("tray flyout missing %q; texts=%v", want, tt.Texts())
		}
	}
	if tt.HasText("41494") {
		t.Fatalf("tray flyout must not leak RecentProcessingMs onto gateway row: %v", tt.Texts())
	}
}

func TestTrayFlyoutHeightFitsContentAndCapsScrollableBody(t *testing.T) {
	check := func(t *testing.T, a *App, capped bool) int {
		t.Helper()
		height := a.TrayFlyoutHeight()
		if height < TrayFlyoutMinHeight || height > TrayFlyoutMaxHeight || (height == TrayFlyoutMaxHeight) != capped {
			t.Fatalf("height=%d, capped=%t", height, capped)
		}
		quit := "退出并停止服务"
		if a.KeepAliveOnQuit() {
			quit = "彻底停止并退出"
		}
		// Exercise actual layout in both appearances, including the pinned footer.
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(a.TrayFlyoutView, TrayFlyoutWidth, 1000)
			tt.SetDark(dark)
			fullFooter, _ := tt.Find(quit)
			fullLast, _ := tt.Find("服务与组件详情")
			tt.SetSize(TrayFlyoutWidth, height)
			footer, ok := tt.Find(quit)
			if !ok || footer.H <= 0 || footer.H != fullFooter.H || footer.Y+footer.H > float32(height) {
				t.Fatalf("footer clipped at %d DIP (dark=%t): %+v", height, dark, footer)
			}
			if capped {
				tt.Scroll(150, 150, 0, 2000)
			}
			last, ok := tt.Find("服务与组件详情")
			if !ok || last.H <= 0 || last.H != fullLast.H || last.Y+last.H > footer.Y || last.Y < 44 {
				t.Fatalf("last setting outside scroll body at %d DIP (dark=%t): last=%+v footer=%+v", height, dark, last, footer)
			}
			if fixed, _ := tt.Find(quit); fixed != footer {
				t.Fatalf("footer moved when scrolling: before=%+v after=%+v", footer, fixed)
			}
		}
		return height
	}

	a := settingsFixture()
	a.desktopPrefs.Loaded, a.desktopPrefs.KeepAliveOnQuit = true, false
	stoppingHeight := check(t, a, false)
	a.desktopPrefs.KeepAliveOnQuit = true
	if keepingHeight := check(t, a, false); keepingHeight <= stoppingHeight || keepingHeight >= TrayFlyoutMaxHeight {
		t.Fatalf("second quit button did not grow the flyout: %d to %d", stoppingHeight, keepingHeight)
	}
	a.desktopPrefs.KeepAliveOnQuit = false
	a.desktopPrefs.LoginError = "当前运行模式不支持系统登录项（仅 macOS .app 安装包支持），已保持关闭。"
	if height := check(t, a, false); height <= stoppingHeight {
		t.Fatal("login error did not reserve extra height")
	}
	a.desktopPrefs.LoginError = ""

	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.PendingDocumentsCount = 1
	q.LastError = "提炼状态读取失败"
	check(t, a, true)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("alert-%d", i)
		a.access.Directory.Projects = append(a.access.Directory.Projects, AccessProject{ID: id, Name: id, Status: "unconfirmed"})
	}
	check(t, a, true)
}

func TestSlowSidebarProjectDoesNotBlockOtherProjectBadges(t *testing.T) {
	a := settingsFixture()
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
	if !tt.HasText("采集队列有 1 项失败") {
		t.Fatal("project management lost the anomaly")
	}
}

func TestTrayAlertRevealsInboxFailure(t *testing.T) {
	t.Cleanup(func() { applyAppearance(false) })
	a, q := projectQueueFixture()
	a.page = "总览"
	tt := ui.NewTester(a.View, 1200, 820)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.openProjectQueueStatus(0)
		tt.Frame()
		if a.page != "知识" || a.kind != "管理" || a.access.RevealQueueProject != "" {
			t.Fatal("tray alert did not open the project queue")
		}
		row, ok := tt.Find("采集队列有 1 项失败")
		if !ok || row.Y < 80 || row.Y+row.H > 820 {
			t.Fatalf("failure not visible after reveal: %+v", row)
		}
		q.Scroll.Y = 300
		tt.Frame()
	}
}
