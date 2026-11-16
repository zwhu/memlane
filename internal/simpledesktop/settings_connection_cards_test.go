package simpledesktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestConnectionCardsUseColumnsAndKeepBatchSelection(t *testing.T) {
	for _, width := range []int{1200, 1440} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := settingsFixture()
			a.page = "设置"
			a.access.SettingsSection = "服务与连接"
			a.integrations = integrationState{Loaded: true, Selected: map[string]bool{}, Connections: ConnectionsDirectory{Connections: []AgentConnection{
				{ID: "codex", Kind: "codex", Detected: true, Status: "configured_pending_new_session", NewSessions: 1},
				{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"},
				{ID: "traex", Kind: "traex", Detected: true, Status: "configured_pending_new_session", NewSessions: 1},
				{ID: "claude", Kind: "claude_code", Detected: true, Status: "not_connected"},
			}}}
			tt := ui.NewTester(a.View, width, 820)
			codex, cOK := tt.Find("Codex")
			pi, pOK := tt.Find("pi")
			traex, tOK := tt.Find("TraeX")
			claude, clOK := tt.Find("Claude Code")
			if !cOK || !pOK || !tOK || !clOK || codex.Y != pi.Y || pi.Y != traex.Y || codex.X >= pi.X || pi.X >= traex.X || claude.Y <= codex.Y {
				t.Fatal("Agent cards did not form independent grid rows", codex, pi, traex, claude)
			}
			clickProjectUI(t, tt, "选择 pi")
			if !a.integrations.Selected["pi"] || !tt.HasText("连接所选（1）") {
				t.Fatal("card layout lost batch selection")
			}
		})
	}
}
