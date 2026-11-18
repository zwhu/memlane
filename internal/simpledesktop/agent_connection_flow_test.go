package simpledesktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAgentConnectionUIRequiresConfirmationAfterPreparation(t *testing.T) {
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	var stage, prepares, activations atomic.Int32
	row := func() AgentConnection {
		r := AgentConnection{ID: "pi", Kind: "pi", Detected: true, Status: "not_connected"}
		if stage.Load() == 1 {
			r.Status = "prepared"
			r.Preview = &CodexConnectionPreview{ProposedSHA: "reviewed", DisabledPlugins: []string{"old-cognee"}}
		}
		if stage.Load() == 2 {
			r.Status = "configured_pending_new_session"
		}
		return r
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			switch r.URL.Path {
			case "/v1/connections/pi/prepare":
				prepares.Add(1)
				stage.Store(1)
			case "/v1/connections/pi/activate":
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["proposed_sha256"] != "reviewed" {
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
		if r.URL.Path == "/v1/connections" {
			json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: int64(stage.Load() + 1), Connections: []AgentConnection{row()}})
			return
		}
		json.NewEncoder(w).Encode(AccessDirectory{})
	}))
	defer server.Close()
	a := New(Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	a.integrations = integrationState{Loaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{row()}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Gap(12).Children(func() { a.agentConnectionSettings(c) })
	}, 900, 650)
	if tt.HasText("连接所选（1）") || tt.HasText("准备所选 Agent") || tt.HasText("启用已准备项") {
		t.Fatal("technical or premature actions visible")
	}
	clickProjectUI(t, tt, "连接 pi")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	tt.Frame()
	if prepares.Load() != 1 || activations.Load() != 0 {
		t.Fatal("connection activated before explicit confirmation")
	}
	if !tt.HasText("确认连接 pi") || !tt.HasText("将为 pi 添加 Memlane 连接，确认后生效。") {
		t.Fatal("configuration preview missing")
	}
	fixtureImage(t, tt, "agent-connection-confirmation")
	clickProjectUI(t, tt, "确认连接 pi")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	tt.Frame()
	if activations.Load() != 1 || a.integrations.Selected["pi"] {
		t.Fatal("activation or selection cleanup incorrect")
	}
	if !tt.HasText("等待保存验证") || !tt.HasText("查看步骤：pi") {
		t.Fatal("next user action missing")
	}
	clickProjectUI(t, tt, "查看步骤：pi")
	if !tt.HasText("1. 重新打开 pi，新建会话并发送一条消息。") {
		t.Fatal("capture verification steps missing")
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
		{ID: "claude", Kind: "claude_code", Detected: true, Status: "not_connected"},
		{ID: "cursor", Kind: "cursor", Status: "not_connected"},
		{ID: "opencode", Kind: "opencode", Detected: true, Status: "integration_pending"},
	}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(24).Gap(12).Children(func() { a.agentConnectionSettings(c) })
	}, 900, 560)
	if !tt.HasText("等待保存验证") || !tt.HasText("其他 Agent（2）") {
		t.Fatal("simple statuses absent")
	}
	if _, ok := tt.Find("选择 Codex"); ok {
		t.Fatal("connected Agent shows a selection box")
	}
	if tt.HasText("Cursor") || tt.HasText("OpenCode") || tt.HasText("准备所选 Agent") || tt.HasText("启用已准备项") {
		t.Fatal("unavailable or internal controls visible")
	}
	fixtureImage(t, tt, "agent-connection-default")
}
