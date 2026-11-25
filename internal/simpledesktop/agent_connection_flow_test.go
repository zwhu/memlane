package simpledesktop

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPreparedAgentConnectionUsesFreshPreviewBeforeConfirmation(t *testing.T) {
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	var stage, previews, activations atomic.Int32
	stage.Store(1)
	row := func() AgentConnection {
		r := AgentConnection{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"}
		if stage.Load() == 1 {
			r.Status = "prepared"
			r.Preview = &CodexConnectionPreview{ProposedSHA: "cached-stale", DisabledPlugins: []string{"old-cognee"}}
		}
		if stage.Load() == 2 {
			r.Status = "configured_pending_new_session"
		}
		return r
	}
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			switch r.URL.Path {
			case "/v1/connections/pi/activate":
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["proposed_sha256"] != "reviewed" || body["expected_revision"] != float64(7) {
					t.Error("confirmation lost preview identity")
				}
				activations.Add(1)
				stage.Store(2)
			default:
				t.Error("unexpected mutation", r.URL.Path)
			}
			json.NewEncoder(w).Encode(map[string]any{"revision": stage.Load() + 1, "connection": row()})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/connections/pi/preview" {
			previews.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"revision": 7, "ok": true, "instance_id": "pi", "kind": "pi", "status": "prepared", "proposed_sha256": "reviewed"})
			return
		}
		if r.URL.Path == "/v1/connections" {
			json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: int64(stage.Load() + 1), Connections: []AgentConnection{row()}})
			return
		}
		json.NewEncoder(w).Encode(AccessDirectory{})
	})
	a := New(Config{GenerationFile: filepath.Join(filepath.Dir(token), "generation.json"), ProjectControl: &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	a.integrations = integrationState{Loaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{row()}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Gap(12).Children(func() { a.agentConnectionSettings(c) })
	}, 900, 650)
	if tt.HasText("连接所选（1）") || tt.HasText("准备所选 Agent") || tt.HasText("启用已准备项") {
		t.Fatal("technical or premature actions visible")
	}
	clickProjectUI(t, tt, "确认连接 pi")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	tt.Frame()
	if previews.Load() != 1 || activations.Load() != 0 || !tt.HasText("确认连接") {
		t.Fatal("opening the fresh preview activated the connection or omitted confirmation")
	}
	fixtureImage(t, tt, "agent-connection-confirmation")
	clickProjectUI(t, tt, "确认连接")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	tt.Frame()
	if activations.Load() != 1 || a.integrations.Selected["pi"] {
		t.Fatal("activation or selection cleanup incorrect")
	}
	const restart = "1. 如果你原本已打开 pi，请先 ⌘Q 完全退出后重新打开。"
	if !tt.HasText("等待采集验证") || !tt.HasText("收起步骤：pi") || !tt.HasText(restart) {
		t.Fatal("capture verification steps should be open by default")
	}
	clickProjectUI(t, tt, "收起步骤：pi")
	if tt.HasText(restart) || !tt.HasText("查看步骤：pi") {
		t.Fatal("explicit collapse was ignored")
	}
	clickProjectUI(t, tt, "查看步骤：pi")
	if !tt.HasText(restart) {
		t.Fatal("capture verification steps did not reopen")
	}
	if tt.HasText("选择 pi") {
		t.Fatal("configured Agent remains selectable")
	}
	fixtureImage(t, tt, "agent-connection-waiting-session")
}

func TestAgentConnectionDefaultShowsClearStatusesAndCollapsesUnavailable(t *testing.T) {
	a := New(Config{}, nil)
	a.integrations = integrationState{Loaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "codex", Kind: "codex", Status: "configured_pending_new_session", NewSessions: 1},
		{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"},
		{ID: "traex", Kind: "traex", Detected: true, Status: "not_connected"},
		{ID: "antigravity", Kind: "antigravity", Detected: false, Status: "not_connected"},
		{ID: "claude", Kind: "claude_code", Detected: true, Status: "not_connected"},
		{ID: "cursor", Kind: "cursor", Status: "not_connected"},
		{ID: "opencode", Kind: "opencode", Detected: true, Status: "integration_pending"},
	}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Gap(12).Children(func() { a.agentConnectionSettings(c) })
	}, 900, 560)
	if !tt.HasText("等待采集验证") || !tt.HasText("其他 Agent（1）") {
		t.Fatal("simple statuses absent")
	}
	if _, ok := tt.Find("选择 Codex"); ok {
		t.Fatal("connected Agent shows a selection box")
	}
	if tt.HasText("Gemini App") || tt.HasText("Claude Code") || tt.HasText("Cursor") || tt.HasText("OpenCode") || tt.HasText("准备所选 Agent") || tt.HasText("启用已准备项") {
		t.Fatal("unavailable/unreleased or internal controls visible")
	}
	fixtureImage(t, tt, "agent-connection-default")
}

func TestBetaHiddenHostsStayVisibleWhenAlreadyConfiguredOrPending(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "Agent 与模型"
	a.integrations = integrationState{Loaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "claude-ready", Kind: "claude_code", Detected: true, Status: "configured_pending_new_session", NewSessions: 1},
		{ID: "cursor-prepared", Kind: "cursor", Detected: true, Status: "prepared", Preview: &CodexConnectionPreview{ProposedSHA: "reviewed"}},
		{ID: "opencode-unknown", Kind: "opencode", Detected: true, Status: "agent_creation_unconfirmed"},
		{ID: "claude-idle", Kind: "claude_code", Detected: true, Status: "not_connected"},
	}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, visible := range []string{"Claude Code", "Cursor", "OpenCode", "确认连接 Cursor", "核对连接"} {
		if !tt.HasText(visible) {
			t.Fatalf("expected existing/pending connection %q to remain visible", visible)
		}
	}
	if tt.HasText("连接 Claude Code") {
		t.Fatal("unconnected non-beta Claude Code row should be hidden")
	}
}
