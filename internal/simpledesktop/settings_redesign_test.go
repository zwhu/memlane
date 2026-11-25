package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func settingsFixture() *App {
	a := projectUIFixture()
	a.Config.ProjectControl = &ProjectControlConfig{}
	a.Config.ServiceURL = "http://127.0.0.1:60000"
	a.Config.Model = "当前配置的 Codex 模型"
	a.completion = "端点已连接（登录状态以实际请求为准）"
	access := "read"
	p := AccessProject{ID: "p", Name: "memlane", Aliases: []string{"/synthetic/memlane"}, Dataset: "ds", Status: "verified", DesiredRevision: 4, Overrides: map[string]ProjectOverride{"agent": {Access: &access}}, Applied: map[string]AccessApplied{"agent": {Status: "verified", Revision: 4}}}
	a.applyAccessDirectory(AccessDirectory{CodexConnection: &CodexConnection{Status: "configured_pending_new_session", NewSessions: 2, Home: "/synthetic/codex"}, Revision: 4, Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: false}, Agents: []AccessAgent{{ID: "agent", Name: "本机 Codex", Installation: &AgentInstallation{Status: "configured"}}}, Projects: []AccessProject{p, {ID: "same", Name: "另一个项目", Dataset: "other"}}, Bindings: []ProjectBinding{{AgentID: "agent", ProjectID: "p", HostSessionKey: "synthetic-session-0001"}, {AgentID: "agent", ProjectID: "same", HostSessionKey: "do-not-show"}}})
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	a.access.Queues = map[string]*projectQueueState{}
	for _, p := range a.access.Directory.Projects {
		a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset] = &projectQueueState{Loaded: true, Next: time.Now().Add(time.Hour)}
	}
	return a
}
func TestSettingsProjectPermissionsCardsAndNavigation(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := settingsFixture()
			original := a.state()
			tt := ui.NewTester(a.View, wh[0], wh[1])
			if tt.HasText("项目设置") {
				t.Fatal("duplicate titlebar 项目设置 button should be removed in favor of 管理 tab")
			}
			clickProjectUI(t, tt, "管理")
			if a.kind != "管理" || a.access.OpenProject != "p" {
				t.Fatal("project identity lost")
			}
			for _, label := range []string{"Agent 访问权限", "只读"} {
				if !tt.HasText(label) {
					t.Fatal("missing", label)
				}
			}
			if tt.HasText("会话 1") || tt.HasText("默认自动采集") {
				t.Fatal("unrelated settings mixed into current project")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-project", wh[0], wh[1]))
			for _, label := range []string{"保存后续会话记录", "自动整理记忆", "自动处理", "已生效", "继承全局默认", "继承默认", "保存此 Agent 的设置", "撤销未保存修改", "仅影响此 Agent"} {
				if tt.HasText(label) {
					t.Fatal("removed settings text remains", label)
				}
			}
			if tt.HasText("待发材料") || tt.HasText("Agent 权限") {
				t.Fatal("removed tab remains")
			}
			clickProjectUI(t, tt, "记忆")
			if a.state() != original || a.page != "知识" || a.kind != "知识" {
				t.Fatal("return lost reading pane")
			}
			clickProjectUI(t, tt, "管理")
			clickProjectUI(t, tt, "设置")
			if a.settingsOrigin != nil || a.access.OpenProject != "" || !tt.HasText("Agent 与模型") {
				t.Fatal("sidebar settings still project scoped")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-global", wh[0], wh[1]))
			for _, text := range tt.Texts() {
				if text == "默认规则" || text == "高级" || text == "后台运行" || text == "配置生效" {
					t.Fatal("removed global policy tab remains")
				}
			}
			a.access.SettingsSection = "高级"
			tt.Frame()
			if a.access.SettingsSection != "记忆提炼" || !tt.HasText("记忆语言") {
				t.Fatal("retired tab left the settings page blank after reload")
			}
			clickProjectUI(t, tt, "记忆提炼")
			if !tt.HasText("记忆语言") || tt.HasText("高级") {
				t.Fatal("retained settings tab lost its content")
			}

		})
	}
}
func TestProjectEmptyBackgroundAndDistinctRecordFilter(t *testing.T) {
	a := projectUIFixture()
	a.state().Selected = nil
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("项目内容") || tt.HasText("选择一项内容开始阅读") || tt.HasText("返回总览") {
		t.Fatal("redundant project labels remain")
	}
	clickProjectUI(t, tt, "原始记录")
	clickProjectUI(t, tt, "会话")
	if a.kind != "会话" {
		t.Fatal("record filter not wired")
	}
	fixtureImage(t, tt, "project-1200x820-empty-background")
}

func TestProjectHeaderSettingsTargetsCurrentProjectAndReturnsToReading(t *testing.T) {
	a := settingsFixture()
	a.navigate("知识", 1)
	original := a.state()
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "管理")
	if a.access.OpenProject != "same" || a.project != 1 || a.kind != "管理" {
		t.Fatal("management tab lost current project")
	}
	clickProjectUI(t, tt, "记忆")
	if a.project != 1 || a.state() != original || a.kind != "知识" {
		t.Fatal("management tab lost project reading state")
	}
}

func TestSettingsServiceDesktopRuntimeSyncWithTray(t *testing.T) {
	a := settingsFixture()
	a.Config.ConfigFile = t.TempDir() + "/runtime.json"
	a.initDesktopPreferences()
	a.SetOpenAtLoginFn = func(enable bool) error { return fmt.Errorf("non-.app binary") }
	a.access.SettingsSection = "服务"
	a.navigate("设置", 0)

	mainTT := ui.NewTester(a.View, 1200, 820)
	if !mainTT.HasText("桌面端运行方式") || !mainTT.HasText("退出时保持后台运行") || !mainTT.HasText("登录时静默启动") {
		t.Fatalf("missing 桌面端运行方式 section in 设置 › 服务; texts=%v", mainTT.Texts())
	}

	if err := mainTT.Click("退出时保持后台运行开关"); err != nil {
		t.Fatalf("failed to click 退出时保持后台运行开关 in 设置 › 服务: %v", err)
	}
	if a.KeepAliveOnQuit() {
		t.Fatal("expected KeepAliveOnQuit=false after toggling in settings")
	}

	// Verify TrayFlyoutView reflects the updated KeepAliveOnQuit state.
	trayTT := ui.NewTester(a.TrayFlyoutView, 300, 480)
	if !trayTT.HasText("退出并停止服务") {
		t.Fatalf("tray flyout did not sync KeepAliveOnQuit=false from settings; texts=%v", trayTT.Texts())
	}

	// Toggling open-at-login in settings records honest error without faking enabled.
	if err := mainTT.Click("登录时静默启动开关"); err != nil {
		t.Fatalf("failed to click 登录时静默启动开关 in 设置 › 服务: %v", err)
	}
	if a.OpenAtLogin() {
		t.Fatal("OpenAtLogin must stay false when OS hook fails")
	}
	if !mainTT.HasText("non-.app binary") {
		t.Fatalf("expected inline LoginError in settings, got %v", mainTT.Texts())
	}
}

func TestProjectSettingsKeepsMessageStatusStableAtTop(t *testing.T) {
	a := settingsFixture()
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true

	// Healthy project: 消息与采集状态 stays stably pinned above 项目信息.
	a.openProjectSettings(0)
	tt := ui.NewTester(a.View, 1200, 820)
	infoRect, okInfo := tt.Find("项目信息")
	msgRect, okMsg := tt.Find("消息与采集状态")
	if !okInfo || !okMsg {
		t.Fatalf("expected healthy project sections; info=%v msg=%v", okInfo, okMsg)
	}
	if msgRect.Y >= infoRect.Y {
		t.Fatalf("healthy project should render 消息与采集状态 (y=%.1f) above 项目信息 (y=%.1f)", msgRect.Y, infoRect.Y)
	}

	// Active alert: 消息与采集状态 remains at the exact top position without jumping.
	q.InboxFailed = 1
	tt.Frame()
	infoRect, okInfo = tt.Find("项目信息")
	msgTitleRect, okMsg := tt.Find("消息与采集状态")
	if !okInfo || !okMsg {
		t.Fatalf("expected alerted project sections; info=%v msg=%v", okInfo, okMsg)
	}
	if msgTitleRect.Y >= infoRect.Y {
		t.Fatalf("alerted project should render 消息与采集状态 (y=%.1f) above 项目信息 (y=%.1f)", msgTitleRect.Y, infoRect.Y)
	}
}
