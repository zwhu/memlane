package simpledesktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestConnectionExpandedDirectoryStaysInsideFirstUseWorkspace(t *testing.T) {
	for _, width := range []int{1200, 1440} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := setupLayoutFixture()
			a.environment.RuntimeStarted = true
			a.environment.InitialStep = 3
			a.integrations = integrationState{Loaded: true, ConnectionBulk: true,
				EnvironmentOpen: map[string]bool{"traex": true},
				Connections: ConnectionsDirectory{Connections: []AgentConnection{
					{ID: "pi", Kind: "pi", Detected: true, Status: "configured_pending_new_session", HostVersion: "0.84.1"},
					{ID: "traex", Kind: "traex", Detected: true, Status: "configured_pending_new_session", HostVersion: "0.215.0", Profile: "/Users/example/" + strings.Repeat("a-long-profile-directory/", 8)},
				}}}
			tt := ui.NewTester(a.View, width, 820)
			if tt.HasText("确认连接（0）") || tt.HasText("已选 0 个") {
				t.Fatal("completed selection left an empty bulk action bar")
			}
			detail, ok := tt.Find("Agent目录详情：TRAE CLI")
			if !ok || detail.W <= 0 || detail.X+detail.W > float32(width)-48 {
				t.Fatal("directory detail escaped list inset", detail)
			}
			fixtureImage(t, tt, fmt.Sprintf("first-use-expanded-agents-%d", width))
		})
	}
}

func TestConnectionCardsUseColumnsAndKeepBatchSelection(t *testing.T) {
	for _, width := range []int{1200, 1440} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := settingsFixture()
			a.page = "设置"
			a.access.SettingsSection = "Agent 与模型"
			a.integrations = integrationState{Loaded: true, Selected: map[string]bool{}, Connections: ConnectionsDirectory{Connections: []AgentConnection{
				{ID: "codex", Kind: "codex", Detected: true, Status: "configured_pending_new_session", NewSessions: 1},
				{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"},
				{ID: "traex", Kind: "traex", Detected: true, Status: "configured_pending_new_session", NewSessions: 1},
				{ID: "antigravity", Kind: "antigravity", Detected: true, Status: "not_connected"},
				{ID: "claude", Kind: "claude_code", Detected: true, Status: "not_connected"},
			}}}
			tt := ui.NewTester(a.View, width, 820)
			codex, cOK := tt.Find("Codex")
			pi, pOK := tt.Find("pi")
			traex, tOK := tt.Find("TRAE CLI")
			agy, aOK := tt.Find("Gemini App")
			if !cOK || !pOK || !tOK || !aOK || codex.X != pi.X || pi.X != traex.X || !(codex.Y < pi.Y && pi.Y < traex.Y && traex.Y < agy.Y) {
				t.Fatal("Agent connections are not aligned compact rows", codex, pi, traex, agy)
			}
			if tt.HasText("Claude Code") {
				t.Fatal("unreleased Claude Code row should be hidden in beta UI")
			}
			if tt.HasText("批量连接") || tt.HasText("选择 pi") || tt.HasText("连接所选") {
				t.Fatal("bulk connection controls should not appear")
			}
			if !tt.HasText("连接 pi") || !tt.HasText("连接 Gemini App") {
				t.Fatal("direct per-row connect actions missing")
			}
		})
	}
}
