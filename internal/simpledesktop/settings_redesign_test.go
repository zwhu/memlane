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
			clickProjectUI(t, tt, "项目设置")
			if a.settingsOrigin == nil || a.access.OpenProject != "p" {
				t.Fatal("project identity lost")
			}
			for _, label := range []string{"允许读取记忆", "允许写入和删除记忆"} {
				if !tt.HasText(label) {
					t.Fatal("missing", label)
				}
			}
			if tt.HasText("会话 1") || tt.HasText("默认自动采集") {
				t.Fatal("unrelated settings mixed into current project")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-project", wh[0], wh[1]))
			for _, label := range []string{"保存后续会话记录", "自动整理记忆", "自动采集", "自动处理", "已生效", "继承全局默认", "继承默认", "保存此 Agent 的设置", "撤销未保存修改", "仅影响此 Agent"} {
				if tt.HasText(label) {
					t.Fatal("removed settings text remains", label)
				}
			}
			if tt.HasText("待发材料") || tt.HasText("Agent 权限") {
				t.Fatal("removed tab remains")
			}
			clickProjectUI(t, tt, "返回项目")
			if a.state() != original || a.page != "知识" {
				t.Fatal("return lost reading pane")
			}
			clickProjectUI(t, tt, "项目设置")
			clickProjectUI(t, tt, "设置")
			if a.settingsOrigin != nil || a.access.OpenProject != "" || !tt.HasText("服务与连接") {
				t.Fatal("sidebar settings still project scoped")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-global", wh[0], wh[1]))
			for _, text := range tt.Texts() {
				if text == "默认规则" {
					t.Fatal("removed global policy tab remains")
				}
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
	clickProjectUI(t, tt, "项目设置")
	if a.access.OpenProject != "same" || a.project != 1 || a.settingsOrigin.ProjectID != "same" {
		t.Fatal("header settings lost current project")
	}
	clickProjectUI(t, tt, "返回项目")
	if a.project != 1 || a.state() != original {
		t.Fatal("settings lost project reading state")
	}
}
