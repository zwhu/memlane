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
	for _, text := range []string{"Agent 桥接", "Codex", "session-model / medium / fast"} {
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
	a.integrations.GenerationModeTab = "provider"
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
