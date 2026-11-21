package simpledesktop

import (
	"bytes"
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

	// Add an unacknowledged project alert on project 0 ("memlane").
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.Sessions = []queuedSession{{
		Agent:   AccessAgent{ID: "agent", Name: "本机 Codex"},
		Binding: ProjectBinding{HostSessionKey: "s1"},
		View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
	}}

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
	q.Sessions = nil
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
	q.Sessions = []queuedSession{{
		Agent:   AccessAgent{ID: "agent", Name: "本机 Codex"},
		Binding: ProjectBinding{HostSessionKey: "s1"},
		View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
	}}
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
			q.AutomaticSave = true
			q.Backlog = &backlogJob{Status: "watching", ProcessingActive: 1}
			q.Sessions = []queuedSession{{
				Agent:   AccessAgent{ID: agent.ID, Name: "Gemini App"},
				Binding: ProjectBinding{ProjectID: p.ID, AgentID: agent.ID, HostSessionKey: "s-progress"},
				View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 2}},
			}}

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
			q.Sessions = append(q.Sessions, queuedSession{
				Agent:   AccessAgent{ID: "agent", Name: "Codex"},
				Binding: ProjectBinding{ProjectID: p.ID, AgentID: "agent", HostSessionKey: "s-unknown"},
				View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
			})
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
