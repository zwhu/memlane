package settings

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func settingsFixture() *testHost {
	a := projectUIFixture()
	a.config.ProjectControl = &ProjectControlConfig{}
	a.config.ServiceURL = "http://127.0.0.1:60000"
	a.config.Model = "当前配置的 Codex 模型"
	a.monitor.CompletionStatus = "端点已连接（登录状态以实际请求为准）"
	access := "read"
	p := AccessProject{ID: "p", Name: "memlane", Aliases: []string{"/synthetic/memlane"}, Dataset: "ds", Status: "verified", DesiredRevision: 4, Overrides: map[string]ProjectOverride{"agent": {Access: &access}}, Applied: map[string]AccessApplied{"agent": {Status: "verified", Revision: 4}}}
	a.ApplyAccessDirectory(AccessDirectory{CodexConnection: &CodexConnection{Status: "configured_pending_new_session", NewSessions: 2, Home: "/synthetic/codex"}, Revision: 4, Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: false}, Agents: []AccessAgent{{ID: "agent", Name: "本机 Codex", Installation: &AgentInstallation{Status: "configured"}}}, Projects: []AccessProject{p, {ID: "same", Name: "另一个项目", Dataset: "other"}}, Bindings: []ProjectBinding{{AgentID: "agent", ProjectID: "p", HostSessionKey: "synthetic-session-0001"}, {AgentID: "agent", ProjectID: "same", HostSessionKey: "do-not-show"}}})
	identity, _ := readingIdentity(Client{Config: a.client.Config})
	a.access.Queues = map[string]*ProjectQueueState{}
	for _, p := range a.access.Directory.Projects {
		a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset] = &ProjectQueueState{Loaded: true, Next: time.Now().Add(time.Hour)}
	}
	return a
}

func TestSettingsProjectPermissionsCardsAndNavigation(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := settingsFixture()
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
			if a.page != "知识" || a.kind != "知识" {
				t.Fatal("return lost reading pane")
			}
			clickProjectUI(t, tt, "管理")
			clickProjectUI(t, tt, "设置")
			if a.access.OpenProject != "" || !tt.HasText("Agent 连接") || !tt.HasText("通用") {
				t.Fatal("sidebar settings still project scoped or missing redesigned tabs")
			}
			fixtureImage(t, tt, fmt.Sprintf("settings-%dx%d-global", wh[0], wh[1]))
			for _, text := range tt.Texts() {
				if text == "默认规则" || text == "高级" || text == "后台运行" || text == "配置生效" || text == "Agent 与模型" || text == "管理 Agent 与模型" {
					t.Fatal("removed global policy tab or jump row remains:", text)
				}
			}
			a.access.SettingsSection = "高级"
			tt.Frame()
			if a.access.SettingsSection != "记忆提炼" || !tt.HasText("提炼策略") {
				t.Fatal("retired tab left the settings page blank after reload")
			}
			a.access.SettingsSection = "Agent 与模型"
			tt.Frame()
			if a.access.SettingsSection != "Agent 连接" {
				t.Fatalf("legacy Agent 与模型 tab did not migrate to Agent 连接: %q", a.access.SettingsSection)
			}
			clickProjectUI(t, tt, "通用")
			if !tt.HasText("外观与语言") || !tt.HasText("记忆语言") || !tt.HasText("桌面端运行方式") || tt.HasText("高级") {
				t.Fatal("general settings tab lost its content")
			}
			clickProjectUI(t, tt, "记忆提炼")
			if !tt.HasText("提炼策略") || tt.HasText("记忆语言") || tt.HasText("外观模式") {
				t.Fatal("processing settings tab kept general settings or lost strategy")
			}

		})
	}
}

func TestProjectHeaderSettingsTargetsCurrentProjectAndReturnsToReading(t *testing.T) {
	a := settingsFixture()
	a.navigate("知识", 1)
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "管理")
	if a.access.OpenProject != "same" || a.project != 1 || a.kind != "管理" {
		t.Fatal("management tab lost current project")
	}
	clickProjectUI(t, tt, "记忆")
	if a.project != 1 || a.kind != "知识" {
		t.Fatal("management tab lost project reading state")
	}
}

func TestSettingsServiceDesktopRuntimeSyncWithTray(t *testing.T) {
	a := settingsFixture()
	a.config.ConfigFile = t.TempDir() + "/runtime.json"
	a.SetOpenAtLoginFn = func(enable bool) error { return fmt.Errorf("non-.app binary") }
	a.access.SettingsSection = "通用"
	a.navigate("设置", 0)

	mainTT := ui.NewTester(a.View, 1200, 820)
	if !mainTT.HasText("桌面端运行方式") || !mainTT.HasText("退出时保持后台运行") || !mainTT.HasText("登录时静默启动") {
		t.Fatalf("missing 桌面端运行方式 section in 设置 › 通用; texts=%v", mainTT.Texts())
	}

	if err := mainTT.Click("退出时保持后台运行开关"); err != nil {
		t.Fatalf("failed to click 退出时保持后台运行开关 in 设置 › 通用: %v", err)
	}
	if a.KeepAliveOnQuit() {
		t.Fatal("expected KeepAliveOnQuit=false after toggling in settings")
	}

	// Toggling open-at-login in settings records honest error without faking enabled.
	if err := mainTT.Click("登录时静默启动开关"); err != nil {
		t.Fatalf("failed to click 登录时静默启动开关 in 设置 › 通用: %v", err)
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
	a.OpenProjectManagement("p")
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

func TestProjectPermissionsUIHonorsDefaultsAgentDefaultAccessAndOverrides(t *testing.T) {
	a := settingsFixture()
	read, none, maintain := "read", "none", "maintain"
	dir := AccessDirectory{
		Revision: 10,
		Defaults: ProjectPolicy{Access: "maintain", Capture: true, AutoProcess: true},
		Agents: []AccessAgent{
			{ID: "gemini", Name: "Gemini App", Kind: "gemini", DefaultAccess: "maintain"},
			{ID: "trae-cn", Name: "TRAE IDE", Kind: "trae_ide", DefaultAccess: "none"},
			{ID: "codex", Name: "本机 Codex", Kind: "codex", DefaultAccess: "none"},
			{ID: "pi", Name: "pi", Kind: "pi", DefaultAccess: "maintain"},
			{ID: "claude", Name: "Claude Code", Kind: "claude", DefaultAccess: "none"},
		},
		Projects: []AccessProject{{
			ID: "p", Name: "memlane", Aliases: []string{"/synthetic/memlane"}, Dataset: "ds",
			Status: "verified", DesiredRevision: 10,
			Overrides: map[string]ProjectOverride{
				"codex":  {Access: &read},
				"pi":     {Access: &none},
				"claude": {Access: &maintain},
			},
			Applied: map[string]AccessApplied{
				"gemini":  {Status: "verified", Revision: 10},
				"trae-cn": {Status: "verified", Revision: 10},
				"codex":   {Status: "verified", Revision: 10},
				"pi":      {Status: "verified", Revision: 10},
				"claude":  {Status: "verified", Revision: 10},
			},
		}},
	}
	a.ApplyAccessDirectory(dir)
	a.OpenProjectManagement("p")

	// Render each permission card directly so descriptions and tri-access selections can be verified per agent.
	cases := []struct {
		agent    AccessAgent
		wantDesc string
		wantVal  string
	}{
		// defaults=maintain, agent default_access=maintain, no override -> 读写 (maintain)
		{dir.Agents[0], "读取、写入并维护该项目记忆", "读写"},
		// defaults=maintain, agent default_access=none (TRAE CN), no override -> 不可访问 (none)
		{dir.Agents[1], "禁止访问此项目的记忆与原始记录", "不可访问"},
		// explicit read override beats default_access=none -> 只读
		{dir.Agents[2], "仅允许检索与读取已有记忆，不可写入", "只读"},
		// explicit none override beats default_access=maintain and defaults=maintain -> 不可访问
		{dir.Agents[3], "禁止访问此项目的记忆与原始记录", "不可访问"},
		// explicit maintain override beats default_access=none -> 读写
		{dir.Agents[4], "读取、写入并维护该项目记忆", "读写"},
	}
	p := a.access.Directory.Projects[0]
	for _, tc := range cases {
		t.Run(tc.agent.ID, func(t *testing.T) {
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(16).Children(func() {
					a.projectPermissionCard(c, p, tc.agent)
				})
			}, 900, 180)
			if !tt.HasText(tc.wantDesc) {
				t.Fatalf("agent %s (default_access=%q): missing expected desc %q; texts=%v", tc.agent.ID, tc.agent.DefaultAccess, tc.wantDesc, tt.Texts())
			}
			if !tt.HasText(tc.wantVal) {
				t.Fatalf("agent %s (default_access=%q): missing expected label %q; texts=%v", tc.agent.ID, tc.agent.DefaultAccess, tc.wantVal, tt.Texts())
			}
		})
	}
}

func TestSettingsFourTabsPartitioningAndMessageIsolation(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a := settingsFixture()
			a.config.ConfigFile = t.TempDir() + "/runtime.json"
			a.setAppearancePreference(appearance)
			a.page = "设置"
			active := GenerationRef{ID: "agent-codex", Revision: 1}
			a.integrations = IntegrationState{
				Loaded:           true,
				GenerationLoaded: true,
				Message:          "已切换到 Codex CLI",
				MessageScope:     "generation",
				Generation: GenerationDirectory{
					HandoffAvailable: true,
					Active:           &active,
					AgentBridges: map[string]GenerationProfile{
						"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-6.2-sol"},
						"pi":    {Mode: "agent_bridge", BridgeKind: "pi", Model: "deepseek-flash"},
					},
					AgentDefaults: map[string]AgentModelDefaults{
						"codex": {Model: "gpt-6.2-sol", ReasoningEffort: "medium", ServiceTier: "fast"},
						"pi":    {Model: "deepseek-flash", ThinkingLevel: "high"},
					},
					Profiles: map[string][]GenerationRow{
						"agent-codex": {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-6.2-sol"}}},
						"agent-pi":    {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "pi", Model: "deepseek-flash"}}},
						"ollama-1":    {{Revision: 1, Digest: "d1", Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "qwen3:4b-instruct"}}},
						"ollama-2":    {{Revision: 1, Digest: "d2", Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "qwen3:4b-thinking"}}},
					},
					Validations: map[string]GenerationValidation{
						"v1": {Digest: "d1", Status: "passed", Plain: true, Schema: true, Quality: true},
					},
				},
				Connections: ConnectionsDirectory{Connections: []AgentConnection{
					{ID: "agy", Kind: "antigravity", Detected: true, Status: "configured_pending_new_session", HostVersion: "1.150.1", Profile: "/Users/demo/.gemini/config", Capture: &AgentCaptureState{State: "healthy", LastSavedAt: 1795700000}},
					{ID: "codex", Kind: "codex", Detected: true, Status: "configured_pending_new_session", HostVersion: "26.1120", Profile: "/Users/demo/.codex", Capture: &AgentCaptureState{State: "healthy", LastSavedAt: 1795650000}},
					{ID: "pi", Kind: "pi", Detected: true, Status: "configured_pending_new_session", HostVersion: "0.84.1", Profile: "/Users/demo/.pi/agent", Capture: &AgentCaptureState{State: "waiting_session"}},
				}},
			}

			tt := ui.NewTester(a.View, 1200, 820)
			// Verify exact tab order: 记忆提炼 | Agent 连接 | 服务 | 通用
			var prevX float32 = -1
			for _, tab := range []string{"记忆提炼", "Agent 连接", "服务", "通用"} {
				rect, ok := tt.Find(tab)
				if !ok {
					t.Fatalf("[%s] missing settings tab %q", appearance, tab)
				}
				if rect.X <= prevX {
					t.Fatalf("[%s] tab %q out of order: x=%.1f prevX=%.1f", appearance, tab, rect.X, prevX)
				}
				prevX = rect.X
			}

			// Tab 1 (记忆提炼): no stale switched message, no 管理 Agent 与模型 jump row, no 外观/记忆语言.
			if tt.HasText("已切换到 Codex CLI") || tt.HasText("管理 Agent 与模型") || tt.HasText("记忆语言") || tt.HasText("外观模式") {
				t.Fatalf("[%s] 记忆提炼 tab still contains removed or moved items: %v", appearance, tt.Texts())
			}
			fixtureImage(t, tt, "settings-tab1-processing-cli-"+appearance)

			// Switch source to 直接调用模型 API -> merged Provider management right in 记忆提炼.
			clickProjectUI(t, tt, "直接调用模型 API")
			for _, want := range []string{"添加配置", "qwen3:4b-instruct", "qwen3:4b-thinking", "切换", "配置", "删除"} {
				if !tt.HasText(want) {
					t.Fatalf("[%s] 记忆提炼 API view missing %q; texts=%v", appearance, want, tt.Texts())
				}
			}
			fixtureImage(t, tt, "settings-tab1-processing-api-"+appearance)

			// Tab 2 (Agent 连接): isolated from generation messages and model configuration.
			clickProjectUI(t, tt, "Agent 连接")
			if tt.HasText("已切换到 Codex CLI") || tt.HasText("模型配置") || tt.HasText("qwen3:4b-instruct") {
				t.Fatalf("[%s] Agent 连接 leaked generation state or model config: %v", appearance, tt.Texts())
			}
			if !tt.HasText("Gemini App") || !tt.HasText("收起步骤") {
				t.Fatalf("[%s] Agent 连接 missing expected hosts or checklist toggle: %v", appearance, tt.Texts())
			}
			fixtureImage(t, tt, "settings-tab2-agents-"+appearance)

			// Tab 3 (服务): pure service & environment without general settings.
			clickProjectUI(t, tt, "服务")
			if !tt.HasText("组件状态") || !tt.HasText("诊断") || !tt.HasText("运行环境") {
				t.Fatalf("[%s] 服务 tab missing core sections: %v", appearance, tt.Texts())
			}
			if tt.HasText("桌面端运行方式") || tt.HasText("外观与语言") {
				t.Fatalf("[%s] 服务 tab still includes general settings: %v", appearance, tt.Texts())
			}
			fixtureImage(t, tt, "settings-tab3-service-"+appearance)

			// Tab 4 (通用): holds 外观与语言 + 桌面端运行方式.
			clickProjectUI(t, tt, "通用")
			for _, want := range []string{"外观与语言", "外观模式", "记忆语言", "桌面端运行方式", "退出时保持后台运行", "登录时静默启动"} {
				if !tt.HasText(want) {
					t.Fatalf("[%s] 通用 tab missing %q; texts=%v", appearance, want, tt.Texts())
				}
			}
			fixtureImage(t, tt, "settings-tab4-general-"+appearance)
		})
	}
}
