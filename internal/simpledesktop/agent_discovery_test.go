package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestLocalAgentDetectionSettingsShowsRefreshAndUnavailableBinding(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "Agent 与模型"
	a.integrations = integrationState{Loaded: true, Selected: map[string]bool{}, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"},
		{ID: "traex-discovery", Kind: "traex", Detected: true, Status: "component_unavailable"},
		{ID: "opencode-discovery", Kind: "opencode", Detected: true, Status: "integration_pending"},
		{ID: "claude-discovery", Kind: "claude_code", Detected: true, Status: "not_connected"},
		{ID: "cursor-discovery", Kind: "cursor", Detected: true, Status: "not_connected"},
	}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"重新检测", "pi", "未连接", "连接 pi", "其他 Agent（1）"} {
		if !tt.HasText(label) {
			t.Fatalf("missing detection control %q", label)
		}
	}
	for _, hidden := range []string{"OpenCode", "Claude Code", "Cursor", "TraeX", "暂时无法连接"} {
		if tt.HasText(hidden) {
			t.Fatalf("unreleased/collapsed host %q should not be visible", hidden)
		}
	}
	if tt.HasText("批量连接") {
		t.Fatal("obsolete bulk connection toggle should be removed")
	}
	clickProjectUI(t, tt, "其他 Agent（1）")
	if !tt.HasText("TraeX") || !tt.HasText("暂时无法连接") {
		t.Fatal("expanded unavailable beta host missing")
	}
	if tt.HasText("OpenCode") || tt.HasText("Claude Code") || tt.HasText("Cursor") {
		t.Fatal("unreleased hosts should remain hidden after expanding unavailable beta hosts")
	}
	row := a.integrations.Connections.Connections[2]
	if connectionActionAllowed(row, "prepare") || connectionActionAllowed(row, "activate") {
		t.Fatal("unfinished adapter allowed binding")
	}
	if !connectionActionAllowed(a.integrations.Connections.Connections[0], "prepare") {
		t.Fatal("installed supported Agent cannot prepare")
	}
}
