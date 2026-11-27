package settings

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGenerationCardsSeparateBridgeAndGroupProviderModels(t *testing.T) {
	a := newTestHost(Config{}, nil)
	a.integrations.DraftMode = "agent_bridge"
	a.integrations.Generation = GenerationDirectory{
		Profiles: map[string][]GenerationRow{
			"codex": {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "obsolete-model"}}},
			"one":   {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "instruct"}}},
			"two":   {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "thinking"}}},
		},
		AgentBridges:  map[string]GenerationProfile{"codex": {Mode: "agent_bridge", BridgeKind: "codex"}},
		AgentDefaults: map[string]AgentModelDefaults{"codex": {Model: "session-model", ReasoningEffort: "medium", ServiceTier: "fast"}},
	}
	tt := ui.NewTester(func(c *ui.Context) { a.generationProfileCards(c) }, 1200, 820)
	for _, text := range []string{"Codex CLI", "session-model / medium / fast"} {
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
		a := newTestHost(Config{}, nil)
		active := &GenerationRef{ID: "codex", Revision: 1}
		a.integrations.Loaded, a.integrations.GenerationLoaded = true, true
		a.integrations.Generation = GenerationDirectory{HandoffAvailable: true, Active: active,
			Profiles: map[string][]GenerationRow{
				"codex":    {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "native"}}},
				"provider": {{Revision: 1, Digest: "p", Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Endpoint: "http://localhost:11434", Model: "target"}}},
			}, AgentBridges: map[string]GenerationProfile{"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "native"}},
			Validations: map[string]GenerationValidation{"p": {Digest: "p", Status: "passed", Plain: true, Schema: true, Quality: true}},
		}
		tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.generationConfiguration(c, toolGraph) }) }, 1200, 820)
		clickProjectUI(t, tt, "直接调用模型 API")
		if a.integrations.HandoffOpen || a.integrations.Generation.Active != active || !tt.HasText("供应商协议") {
			t.Fatal("configuration source switched runtime or hid fields")
		}
		a.integrations.Model = "private-draft-model"
		a.integrations.APIKey = "unsaved-key"
		a.integrations.Temperature = "0"
		clickProjectUI(t, tt, "用本机 Agent CLI 提炼")
		if a.integrations.DraftMode != "agent_bridge" || a.integrations.APIKey != "" || a.integrations.Generation.Active != active {
			t.Fatal("Provider draft leaked into Agent")
		}
		clickProjectUI(t, tt, "直接调用模型 API")
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
	a.integrations = IntegrationState{Loaded: true, GenerationLoaded: true, Generation: GenerationDirectory{
		Active: &GenerationRef{ID: "api", Revision: 1}, Profiles: map[string][]GenerationRow{
			"api": {{Revision: 1, Profile: GenerationProfile{Mode: "provider", Provider: "openai", Model: "fixture-model"}}},
		}}}
	tt := ui.NewTester(a.View, 1200, 820)
	if a.access.SettingsSection != "记忆提炼" || (!tt.HasText("切换") && !tt.HasText("使用中")) || tt.HasText("API Key") || tt.HasText("下次启动") {
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
	if !tt.HasText("保存配置") || !tt.HasText("API Key") || tt.HasText("切换") || tt.HasText("用本机 Agent CLI 提炼") || tt.HasText("选择本机 Agent CLI") || tt.HasText("直接调用模型 API") {
		t.Fatal("configuration tab mixes activation or hides editable fields")
	}
	fixtureImage(t, tt, "settings-agent-model-management")
	clickProjectUI(t, tt, "关闭 Provider 配置")
	clickProjectUI(t, tt, "服务")
	if !tt.HasText("运行环境") || !tt.HasText("组件状态") || tt.HasText("API Key") || tt.HasText("切换") {
		t.Fatal("service tab includes processing controls")
	}
	fixtureImage(t, tt, "settings-service")
	if a.integrations.Generation.Active.ID != "api" {
		t.Fatal("tab browsing changed active configuration")
	}
}

func TestProcessingAgentChoiceShowsAvailabilityAndValidationSeparately(t *testing.T) {
	a := newTestHost(Config{}, nil)
	a.integrations = IntegrationState{Loaded: true, GenerationLoaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{Kind: "codex", Status: "configured_pending_new_session"}, {Kind: "pi", Status: "not_connected"},
	}}, Generation: GenerationDirectory{
		AgentBridges: map[string]GenerationProfile{
			"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "native"},
			"pi":    {Mode: "agent_bridge", BridgeKind: "pi", BridgeProvider: "deepseek", Model: "native"},
		},
		Profiles: map[string][]GenerationRow{
			"agent-codex": {{Revision: 1, Digest: "codex-digest", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "native"}}},
			"agent-traex": {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "traex", Model: "native"}}},
		},
		Validations: map[string]GenerationValidation{
			"probe": {Digest: "codex-digest", Status: "passed", Plain: true, Schema: true, Quality: true},
		},
	}}
	a.chooseGenerationSource("agent_bridge")
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.generationWorkspaceFor(c, false, true) }) }, 1200, 820)
	if !tt.HasText("未检测到 CLI") {
		t.Fatalf("unavailable Agent CLI missing status: %v", tt.Texts())
	}
	if tt.HasText("未验证") || tt.HasText("已验证") || tt.HasText("验证（会消耗额度）") {
		t.Fatalf("regular Settings Agent CLI list should not clutter with validation badges: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "配置模型：agent-pi")
	if a.integrations.BridgeKind != "pi" || !tt.HasText("切换") || a.integrations.Generation.Active != nil {
		t.Fatalf("locally detected Agent could not switch directly without pre-validation: bridge=%q texts=%v", a.integrations.BridgeKind, tt.Texts())
	}
	clickProjectUI(t, tt, "配置模型：agent-traex")
	if a.integrations.BridgeKind != "pi" {
		t.Fatal("unavailable CLI could be selected")
	}
	clickProjectUI(t, tt, "配置模型：agent-codex")
	if a.integrations.BridgeKind != "codex" || !tt.HasText("切换") || a.integrations.Generation.Active != nil {
		t.Fatal("available Codex CLI lost its direct switch action")
	}
}

func TestSwitchAgentBridgeAutoSavesUpdatedDefaultModelAndActivatesDirectly(t *testing.T) {
	root := t.TempDir()
	a := newTestHost(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	var savedModel string
	var handoffRef GenerationRef
	active := GenerationRef{ID: "agent-traex", Revision: 1}
	directory := GenerationDirectory{
		Revision:         4,
		HandoffAvailable: true,
		Active:           &active,
		AgentBridges: map[string]GenerationProfile{
			"codex": {Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-6.2-sol"},
			"traex": {Mode: "agent_bridge", BridgeKind: "traex", Model: "GPT-6.1-Sol"},
		},
		Profiles: map[string][]GenerationRow{
			"existing-codex": {{Revision: 2, Digest: "old-codex", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "gpt-6.1-sol"}}},
			"agent-traex":    {{Revision: 1, Digest: "traex", Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "traex", Model: "GPT-6.1-Sol"}}},
		},
	}
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/generation/profiles/existing-codex":
			var body struct {
				Profile GenerationProfile `json:"profile"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			savedModel = body.Profile.Model
			directory.Profiles["existing-codex"] = append(directory.Profiles["existing-codex"], GenerationRow{
				Revision: 3, Digest: "new-codex", Profile: body.Profile,
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"result": GenerationRef{ID: "existing-codex", Revision: 3}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generation/handoffs":
			var body struct {
				Profile GenerationRef `json:"profile"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			handoffRef = body.Profile
			directory.Active = &handoffRef
			_ = json.NewEncoder(w).Encode(GenerationHandoffDirectory{
				Handoff: &GenerationHandoffReceipt{Operation: "op-1", Reference: handoffRef, Status: "applied"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/generation":
			_ = json.NewEncoder(w).Encode(directory)
		default:
			http.NotFound(w, r)
		}
	})}
	updates := make(chan func(), 16)
	a.dispatch = func(fn func()) { updates <- fn }
	a.integrations.Loaded, a.integrations.GenerationLoaded = true, true
	a.integrations.Generation = directory
	a.chooseGenerationSource("agent_bridge")

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.generationSettings(c) })
	}, 1200, 820)
	clickProjectUI(t, tt, "配置模型：existing-codex")
	if !tt.HasText("切换") || tt.HasText("待核对操作") || tt.HasText("验证（会消耗额度）") {
		t.Fatalf("unexpected direct switch UI: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "切换")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	if savedModel != "gpt-6.2-sol" {
		t.Fatalf("expected updated codex default model saved before switch, got %q", savedModel)
	}
	if handoffRef != (GenerationRef{ID: "existing-codex", Revision: 3}) {
		t.Fatalf("expected handoff to new revision 3, got %+v", handoffRef)
	}
	if a.integrations.Generation.Active == nil || *a.integrations.Generation.Active != handoffRef {
		t.Fatalf("expected active updated to %+v, got %+v", handoffRef, a.integrations.Generation.Active)
	}
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
