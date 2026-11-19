package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestGenerationCardsSeparateBridgeAndGroupProviderModels(t *testing.T) {
	a := New(Config{}, nil)
	a.integrations.DraftMode = "agent_bridge"
	a.integrations.Generation = GenerationDirectory{
		Profiles: map[string][]GenerationRow{
			"codex": {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "obsolete-model"}}},
			"one":   {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "instruct"}}},
			"two":   {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "thinking"}}},
		},
		AgentDefaults: map[string]AgentModelDefaults{"codex": {Model: "session-model", ReasoningEffort: "medium", ServiceTier: "fast"}},
	}
	tt := ui.NewTester(func(c *ui.Context) { a.generationProfileCards(c) }, 1200, 820)
	for _, text := range []string{"Codex", "session-model / medium / fast"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %s", text)
		}
	}
	if tt.HasText("obsolete-model") || tt.HasText("当前模型 · session-model / medium / fast") {
		t.Fatal("bridge rendered as a fixed model")
	}
	if tt.HasText("Ollama") {
		t.Fatal("inactive provider form rendered alongside bridge")
	}
	activeBefore := a.integrations.Generation.Active
	a.integrations.DraftMode = "provider"
	tt.Frame()
	for _, text := range []string{"Ollama", "instruct", "thinking", "配置模型：one", "配置模型：two"} {
		if !tt.HasText(text) {
			t.Fatalf("missing provider %s", text)
		}
	}
	if a.integrations.Generation.Active != activeBefore {
		t.Fatal("viewing another mode changed active configuration")
	}
	groups := generationProfileGroups(a.integrations.Generation)
	if len(groups) != 2 || len(groups[1].Items) != 2 {
		t.Fatal("provider connection duplicated", groups)
	}
	// Grouping preserves two independent model editors and delete actions.
	clickProjectUI(t, tt, "配置模型：two")
	if a.integrations.ProfileID != "two" || a.integrations.Model != "thinking" {
		t.Fatal("group selected another config")
	}
}

func TestGenerationProviderEndpointsRemainDistinct(t *testing.T) {
	g := GenerationDirectory{Profiles: map[string][]GenerationRow{
		"one": {{Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "one"}}},
		"two": {{Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11435", Model: "two"}}},
	}}
	groups := generationProfileGroups(g)
	if len(groups) != 2 || groups[0].Title == groups[1].Title {
		t.Fatal("different provider connections collapsed", groups)
	}
}

func TestProcessingSourcesKeepDraftsAndDoNotEnableWhileConfiguring(t *testing.T) {
	for _, toolGraph := range []bool{false, true} {
		a := New(Config{}, nil)
		active := &GenerationRef{ID: "codex", Revision: 1}
		a.integrations.Loaded = true
		a.integrations.Generation = GenerationDirectory{HandoffAvailable: true, Active: active,
			Profiles: map[string][]GenerationRow{
				"codex":    {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "native"}}},
				"provider": {{Revision: 1, Digest: "p", Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "target"}}},
			}, AgentBridges: map[string]GenerationProfile{"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "native"}},
			Validations: map[string]GenerationValidation{"p": {Digest: "p", Status: "passed", Plain: true, Schema: true, Quality: true}},
		}
		tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.generationConfiguration(c, toolGraph) }) }, 1200, 820)
		clickProjectUI(t, tt, "模型 Provider")
		if a.integrations.HandoffOpen || a.integrations.Generation.Active != active || !tt.HasText("供应商协议") {
			t.Fatal("configuration source switched runtime or hid fields")
		}
		a.integrations.Model = "private-draft-model"
		a.integrations.APIKey = "unsaved-key"
		a.integrations.Temperature = "0"
		clickProjectUI(t, tt, "使用 Agent")
		if a.integrations.DraftMode != "agent_bridge" || a.integrations.APIKey != "" || a.integrations.Generation.Active != active {
			t.Fatal("Provider draft leaked into Agent")
		}
		clickProjectUI(t, tt, "模型 Provider")
		if a.integrations.Model != "private-draft-model" || a.integrations.APIKey != "unsaved-key" || a.integrations.Temperature != "0" || a.integrations.Generation.Active != active {
			t.Fatal("source switch discarded draft or changed active")
		}
		if a.integrations.cleanSaved() {
			t.Fatal("unsaved candidate considered eligible to enable")
		}
		clickProjectUI(t, tt, "取消修改")
		if a.integrations.Model != "target" || a.integrations.APIKey != "" || a.integrations.Generation.Active != active {
			t.Fatal("discard did not restore saved candidate")
		}
		clickProjectUI(t, tt, "切换")
		if !a.integrations.HandoffOpen || !tt.HasText("确认启用") || a.integrations.Generation.Active != active {
			t.Fatal("explicit enable lacks inline review")
		}
		clickProjectUI(t, tt, "取消")
		if a.integrations.HandoffOpen || a.integrations.Generation.Active != active || !tt.HasText("供应商协议") {
			t.Fatal("cancel changed active or hid editor")
		}
	}
}

func TestSettingsSeparateProcessingSelectionFromConfigurationAndService(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.integrations = integrationState{Loaded: true, Generation: GenerationDirectory{
		Active: &GenerationRef{ID: "api", Revision: 1}, Profiles: map[string][]GenerationRow{
			"api": {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "openai", Model: "fixture-model"}}},
		}}}
	tt := ui.NewTester(a.View, 1200, 820)
	if a.access.SettingsSection != "记忆处理" || (!tt.HasText("切换") && !tt.HasText("使用中")) || tt.HasText("API Key") || tt.HasText("下次启动") {
		t.Fatal("first tab mixes selection with configuration")
	}
	fixtureImage(t, tt, "settings-processing-selection")
	clickProjectUI(t, tt, "Agent 与模型")
	if tt.HasText("API Key") {
		t.Fatal("management page rendered the editor inline")
	}
	clickProjectUI(t, tt, "配置模型：api")
	if !a.integrations.GenerationProviderDialog {
		t.Fatal("model configuration did not open a dialog")
	}
	if !tt.HasText("保存配置") || !tt.HasText("API Key") || tt.HasText("切换") || tt.HasText("使用 Agent") || tt.HasText("选择 Agent") || tt.HasText("模型 Provider") {
		t.Fatal("configuration tab mixes activation or hides editable fields")
	}
	fixtureImage(t, tt, "settings-agent-model-management")
	clickProjectUI(t, tt, "关闭 Provider 配置")
	clickProjectUI(t, tt, "服务")
	if !tt.HasText("运行环境") || !tt.HasText("记忆服务") || tt.HasText("API Key") || tt.HasText("切换") {
		t.Fatal("service tab includes processing controls")
	}
	fixtureImage(t, tt, "settings-service")
	if a.integrations.Generation.Active.ID != "api" {
		t.Fatal("tab browsing changed active configuration")
	}
}

func TestProcessingAgentChoicesDependOnConnectionNotModelValidation(t *testing.T) {
	a := New(Config{}, nil)
	a.integrations = integrationState{Loaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{Kind: "codex", Status: "configured_pending_new_session"}, {Kind: "traex", Status: "configured_pending_new_session"}, {Kind: "pi", Status: "not_connected"},
	}}, Generation: GenerationDirectory{AgentBridges: map[string]GenerationProfile{
		"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "native"}, "traex": {Mode: "agent_bridge", BridgeKind: "traex", Model: "native"}, "pi": {Mode: "agent_bridge", BridgeKind: "pi", BridgeProvider: "deepseek", Model: "native"},
	}}}
	a.chooseGenerationSource("agent_bridge")
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.generationWorkspaceFor(c, false, true) }) }, 1200, 820)
	for _, label := range []string{"未验证", "未配置", "已保存 · 未验证", "保存配置", "验证（会消耗额度）"} {
		if tt.HasText(label) {
			t.Fatal("Agent selection leaked model setup state", label)
		}
	}
	clickProjectUI(t, tt, "配置模型：agent-traex")
	if a.integrations.BridgeKind != "traex" {
		t.Fatal("connected Agent requires validation to select")
	}
	clickProjectUI(t, tt, "配置模型：agent-pi")
	if a.integrations.BridgeKind != "traex" {
		t.Fatal("disconnected Agent can be selected")
	}
	fixtureImage(t, tt, "connected-agent-selection")
}

func TestAgentChoiceOrderIsStableAcrossSaveAndRefresh(t *testing.T) {
	g := GenerationDirectory{AgentBridges: map[string]GenerationProfile{
		"codex": {Mode: "agent_bridge", BridgeKind: "codex"}, "pi": {Mode: "agent_bridge", BridgeKind: "pi"}, "traex": {Mode: "agent_bridge", BridgeKind: "traex"}}}
	check := func() {
		t.Helper()
		items := generationWorkspaceGroups(g, "agent_bridge")[0].Items
		for i, kind := range []string{"codex", "pi", "traex"} {
			if items[i].Icon != kind {
				t.Fatal("Agent order changed", items)
			}
		}
	}
	check()
	g.Profiles = map[string][]GenerationRow{"agent-pi": {{Revision: 1, Profile: g.AgentBridges["pi"]}}}
	check()
	g.Active = &GenerationRef{ID: "agent-pi", Revision: 1}
	check()
	g.Profiles["existing-codex"] = []GenerationRow{{Revision: 1, Profile: g.AgentBridges["codex"]}}
	check()
}
