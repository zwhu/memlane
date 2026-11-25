package settings

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGeminiProcessingChoiceKeepsOrderAndCurrentProcessor(t *testing.T) {
	g := GenerationDirectory{Active: &GenerationRef{ID: "existing-codex", Revision: 1},
		AgentBridges: map[string]GenerationProfile{}, AgentDefaults: map[string]AgentModelDefaults{}}
	for _, kind := range []string{"codex", "pi", "traex", "antigravity"} {
		g.AgentBridges[kind] = GenerationProfile{Mode: "agent_bridge", BridgeKind: kind, Model: "current " + kind}
		g.AgentDefaults[kind] = AgentModelDefaults{Model: "current " + kind}
	}
	g.AgentDefaults["antigravity"] = AgentModelDefaults{Model: "native configured Gemini", Provider: "google", Source: "agent_config", ConfigurationSource: "agy_cli_settings"}
	items := generationWorkspaceGroups(g, "agent_bridge")[0].Items
	for i, kind := range []string{"codex", "pi", "traex", "antigravity"} {
		if items[i].Icon != kind {
			t.Fatal("existing order changed", items)
		}
	}
	if items[3].Title != "Gemini CLI" || !strings.Contains(items[3].Detail, "agy CLI 默认配置") {
		t.Fatal("Gemini identity or model source missing", items[3])
	}
	a := newTestHost(Config{}, nil)
	a.integrations = IntegrationState{Loaded: true, GenerationLoaded: true, Generation: g,
		Connections: ConnectionsDirectory{Connections: []AgentConnection{{Kind: "codex", Status: "configured_pending_new_session"}, {Kind: "antigravity", Status: "configured_pending_new_session"}}}}
	a.chooseGenerationSource("agent_bridge")
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.generationWorkspaceFor(c, false, true) }) }, 1200, 820)
	clickProjectUI(t, tt, "配置模型：agent-antigravity")
	if a.integrations.BridgeKind != "antigravity" || a.integrations.draftProfile().Model != "current antigravity" {
		t.Fatal("Gemini selection lost the native bridge profile")
	}
	if *a.integrations.Generation.Active != *g.Active {
		t.Fatal("browsing Gemini changed the current processor")
	}
	a.integrations.Connections.Connections[1].Status = "not_connected"
	if a.integrations.connectedProcessingAgent("antigravity") {
		t.Fatal("disconnected Gemini can be applied")
	}
}
