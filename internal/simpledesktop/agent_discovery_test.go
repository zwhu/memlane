package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestLocalAgentDetectionSettingsShowsRefreshAndUnavailableBinding(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "服务与连接"
	a.integrations = integrationState{Loaded: true, Selected: map[string]bool{}, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"},
		{ID: "opencode-discovery", Kind: "opencode", Detected: true, Status: "integration_pending"},
	}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"重新检测", "pi", "未连接", "其他 Agent（1）"} {
		if !tt.HasText(label) {
			t.Fatalf("missing detection control %q", label)
		}
	}
	if tt.HasText("OpenCode") || tt.HasText("暂不支持连接") {
		t.Fatal("unavailable rows should be collapsed")
	}
	clickProjectUI(t, tt, "其他 Agent（1）")
	if !tt.HasText("OpenCode") || !tt.HasText("暂不支持连接") {
		t.Fatal("expanded unavailable status missing")
	}
	clickProjectUI(t, tt, "批量连接")
	clickProjectUI(t, tt, "选择 pi")
	if !tt.HasText("连接所选（1）") {
		t.Fatal("selected action missing")
	}
	row := a.integrations.Connections.Connections[1]
	if connectionActionAllowed(row, "prepare") || connectionActionAllowed(row, "activate") {
		t.Fatal("unfinished adapter allowed binding")
	}
	if !connectionActionAllowed(a.integrations.Connections.Connections[0], "prepare") {
		t.Fatal("installed supported Agent cannot prepare")
	}
}
