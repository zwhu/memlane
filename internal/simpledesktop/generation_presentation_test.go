package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestGenerationCardsSeparateBridgeAndGroupProviderModels(t *testing.T) {
	a := New(Config{}, nil)
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
	a.integrations.GenerationDraftMode = "provider"
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

func TestProcessingModeSelectorFollowsRuntimeUntilSwitchConfirmed(t *testing.T) {
	for _, toolGraph := range []bool{false, true} {
		a := New(Config{}, nil)
		active := &GenerationRef{ID: "codex", Revision: 1}
		a.integrations.Loaded = true
		a.integrations.Generation = GenerationDirectory{HandoffAvailable: true, Active: active,
			Profiles: map[string][]GenerationRow{
				"codex":    {{Revision: 1, Profile: GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "old"}}},
				"provider": {{Revision: 1, Digest: "p", Profile: GenerationProfile{Mode: "provider", Provider: "ollama", Model: "target"}}},
			}, Validations: map[string]GenerationValidation{"p": {Digest: "p", Status: "passed", Plain: true, Schema: true, Quality: true}},
		}
		tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.generationConfiguration(c, toolGraph) }) }, 1200, 820)
		if _, ok := tt.Find("记忆处理方式配置"); ok {
			t.Fatal("two configuration tabs returned")
		}
		clickProjectUI(t, tt, "Provider")
		if !a.integrations.HandoffOpen || a.integrations.HandoffMode != "provider" || a.integrations.Generation.Active != active {
			t.Fatal("mode click bypassed confirmed handoff")
		}
		if !tt.HasText("切换到 Provider") || !tt.HasText("Provider · Ollama · target") {
			t.Fatal("mode switch did not select matching configuration")
		}
		clickProjectUI(t, tt, "取消")
		if a.integrations.Generation.Active != active || tt.HasText("target") {
			t.Fatal("cancel left the inactive provider selected")
		}
	}
}
