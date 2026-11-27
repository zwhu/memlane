package settings

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestAgentEnvironmentChoiceBlocksWrongTargetAndExplainsReceipt(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.access.SettingsSection = "Agent 连接"
	row := AgentConnection{ID: "traex", Kind: "traex", Detected: true,
		Status: "verification_environment", EnvironmentIssue: "verification_environment",
		Profile: "/synthetic/native-setup/traex", Environments: []AgentEnvironment{
			{Profile: "/synthetic/daily traex", Source: "default", Exists: true},
			{Profile: "/synthetic/native-setup/traex", Source: "configured", Exists: true, Verification: true},
		}}
	a.integrations = IntegrationState{Loaded: true, Connections: ConnectionsDirectory{Connections: []AgentConnection{row}}}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("需要更换配置目录") || connectionActionAllowed(row, "prepare") {
		t.Fatal("verification environment was offered as a valid daily connection")
	}
	clickProjectUI(t, tt, "查看配置目录：TRAE CLI")
	if !tt.HasText("默认目录：/synthetic/daily traex") || !tt.HasText("使用目录：/synthetic/daily traex") {
		t.Fatal("real daily environment choice is not visible")
	}
	if tt.HasText("使用目录：/synthetic/native-setup/traex") {
		t.Fatal("verification environment can be selected")
	}
	row.Status, row.NewSessions, row.ReceiptOverdue = "configured_pending_new_session", 0, true
	if connectionStatus(row) != "等待采集验证" {
		t.Fatal("expired wait was presented as a connected host")
	}
	row.Status, row.NewSessions = "environment_receipt_unconfirmed", 1
	if connectionStatus(row) == "已连接" {
		t.Fatal("unverified environment receipt was treated as verified")
	}
}

func TestWaitingAgentReceiptsRefreshWithoutInstallingOrLosingSelection(t *testing.T) {
	requests := 0
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v1/connections" {
			t.Error("receipt polling attempted a mutation")
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 8, Connections: []AgentConnection{{ID: "daily", Kind: "traex", Status: "configured_pending_new_session", NewSessions: 1, EnvironmentVerified: true}}})
	})

	a := settingsFixture()
	a.config.ProjectControl.URL = server.URL
	a.client.HTTP = server.client
	key := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(key, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a.config.ProjectControl.ManagementTokenFile = key
	a.integrations = IntegrationState{Loaded: true, Selected: map[string]bool{"other": true}, Connections: ConnectionsDirectory{Connections: []AgentConnection{{ID: "daily", Kind: "traex", Status: "configured_pending_new_session"}}}}
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }
	now := time.Now()
	a.refreshAgentReceiptsIfDue(now)
	a.refreshAgentReceiptsIfDue(now)
	drainReading(t, updates, func() bool { return !a.integrations.ReceiptCheckLoading })
	a.refreshAgentReceiptsIfDue(now.Add(time.Second))
	if requests != 1 || !a.integrations.Selected["other"] || a.integrations.Connections.Revision != 8 || connectionStatus(a.integrations.Connections.Connections[0]) != "等待采集验证" {
		t.Fatal("receipt refresh did not preserve selection and settle the real connection")
	}
}

func TestSelectingAgentEnvironmentOnlySelectsTarget(t *testing.T) {
	requests := 0
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/v1/connections/environment" {
			t.Error("environment selection unexpectedly installed or activated a host")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["profile"] != "/synthetic/daily traex" || body["kind"] != "traex" || body["expected_revision"] != float64(4) {
			t.Error("wrong environment selection payload")
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 5, Connections: []AgentConnection{{ID: "daily", Kind: "traex", Status: "not_connected"}}})
	})

	a := settingsFixture()
	a.config.ProjectControl.URL = server.URL
	a.client.HTTP = server.client
	key := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(key, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a.config.ProjectControl.ManagementTokenFile = key
	a.integrations = IntegrationState{Connections: ConnectionsDirectory{Revision: 4}, Selected: map[string]bool{"old": true}}
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }
	a.selectAgentEnvironment(AgentConnection{Kind: "traex"}, "/synthetic/daily traex")
	drainReading(t, updates, func() bool { return !a.integrations.Busy })
	if requests != 1 || a.integrations.Error != "" || a.integrations.Connections.Revision != 5 || len(a.integrations.Selected) != 0 {
		t.Fatal("environment choice did not settle independently from installation")
	}
}

func TestWaitingAgentReceiptSettlesFromIdleViewTimer(t *testing.T) {
	for _, status := range []string{"configured_pending_new_session", "environment_receipt_unconfirmed"} {
		t.Run(status, func(t *testing.T) {
			var requests atomic.Int32
			server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/connections" {
					t.Errorf("idle receipt check attempted %s %s", r.Method, r.URL.Path)
				}
				json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 9, Connections: []AgentConnection{{
					ID: "daily", Kind: "traex", Detected: true, Status: "configured_pending_new_session",
					NewSessions: 1, EnvironmentVerified: true,
				}}})
			})

			a := settingsFixture()
			a.page, a.access.SettingsSection = "设置", "Agent 连接"
			a.access.Next = time.Now().Add(time.Hour)
			a.config.ProjectControl.URL = server.URL
			a.client.HTTP = server.client
			key := filepath.Join(t.TempDir(), "manager.token")
			if err := os.WriteFile(key, []byte(strings.Repeat("x", 40)), 0600); err != nil {
				t.Fatal(err)
			}
			a.config.ProjectControl.ManagementTokenFile = key
			a.integrations = IntegrationState{Loaded: true, NextReceiptCheck: time.Now().Add(time.Hour),
				Selected: map[string]bool{"other": true}, Connections: ConnectionsDirectory{Connections: []AgentConnection{{
					ID: "daily", Kind: "traex", Detected: true, Status: status,
				}}}}
			updates := make(chan func(), 10)
			a.dispatch = func(fn func()) { updates <- fn }
			tt := ui.NewTester(a.View, 1200, 820)
			if requests.Load() != 0 {
				t.Fatal("receipt polling ignored its deadline")
			}
			// Expire the HTTP-check deadline, then advance only scheduled UI
			// frames. No input, manual redraw or direct refresh call may wake it.
			a.integrations.NextReceiptCheck = time.Time{}
			tt.Advance(2 * time.Second)
			drainReading(t, updates, func() bool { return a.integrations.Connections.Revision == 9 })
			tt.Frame() // the HTTP completion's native win.Update redraw
			if requests.Load() != 1 || !a.integrations.Selected["other"] || !tt.HasText("等待采集验证") {
				t.Fatal("idle page did not settle the receipt while preserving selection")
			}
			a.integrations.NextReceiptCheck = time.Time{}
			tt.Advance(time.Minute)
			drainReading(t, updates, func() bool { return !a.integrations.ReceiptCheckLoading })
			if requests.Load() != 2 {
				t.Fatal("bound connection stopped checking actual saved-message evidence")
			}
		})
	}
}

func TestAgentReceiptRecoveryClearsOnlyItsOwnError(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{Revision: 9, Connections: []AgentConnection{{
			ID: "daily", Kind: "traex", Status: "configured_pending_new_session", NewSessions: 1, EnvironmentVerified: true,
		}}})
	})

	a := settingsFixture()
	a.config.ProjectControl.URL = server.URL
	a.client.HTTP = server.client
	key := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(key, []byte(strings.Repeat("x", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a.config.ProjectControl.ManagementTokenFile = key
	waiting := ConnectionsDirectory{Connections: []AgentConnection{{ID: "daily", Status: "environment_receipt_unconfirmed"}}}
	a.integrations = IntegrationState{Loaded: true, Connections: waiting}
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }
	now := time.Now()
	a.refreshAgentReceiptsIfDue(now)
	drainReading(t, updates, func() bool { return !a.integrations.ReceiptCheckLoading })
	if a.integrations.Error != "接入状态暂时无法核对，保留上次结果。" || a.integrations.Connections.Revision == 9 {
		t.Fatal("failed polling did not retain the last result with its error")
	}
	fail.Store(false)
	a.refreshAgentReceiptsIfDue(now.Add(10 * time.Second))
	drainReading(t, updates, func() bool { return !a.integrations.ReceiptCheckLoading })
	if a.integrations.Error != "" || connectionStatus(a.integrations.Connections.Connections[0]) != "等待采集验证" {
		t.Fatal("successful receipt recovery retained a stale polling error")
	}
	a.integrations.Connections = waiting
	a.integrations.Error = "另一个操作结果未确认"
	a.refreshAgentReceiptsIfDue(now.Add(20 * time.Second))
	drainReading(t, updates, func() bool { return !a.integrations.ReceiptCheckLoading })
	if a.integrations.Error != "另一个操作结果未确认" {
		t.Fatal("polling recovery erased another operation's error")
	}
}

func TestAgentReceiptPollingPausesWhileHiddenOrInactive(t *testing.T) {
	a := newTestHost(Config{ProjectControl: beta3ControlConfig(t, t.TempDir())}, nil)
	a.integrations.Loaded = true
	a.integrations.Connections = ConnectionsDirectory{Connections: []AgentConnection{
		{ID: "codex", Kind: "codex", Status: "configured_pending_new_session"},
	}}
	var requests atomic.Int32
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/v1/connections" {
			t.Errorf("unexpected receipt request %s %s", r.Method, r.URL)
		}
		json.NewEncoder(w).Encode(ConnectionsDirectory{})
	})}
	updates := make(chan func(), 8)
	a.dispatch = func(fn func()) { updates <- fn }
	now := time.Now()

	for _, visibility := range []struct{ hidden, inactive bool }{{true, false}, {false, true}} {
		a.windowHidden, a.windowInactive = visibility.hidden, visibility.inactive
		a.refreshAgentReceiptsIfDue(now)
		if a.integrations.ReceiptCheckLoading || !a.integrations.NextReceiptCheck.IsZero() || requests.Load() != 0 {
			t.Fatalf("background window scheduled receipt polling: %+v", visibility)
		}
	}

	a.windowHidden, a.windowInactive = false, false
	a.refreshAgentReceiptsIfDue(now)
	drainReading(t, updates, func() bool { return !a.integrations.ReceiptCheckLoading })
	if requests.Load() != 1 {
		t.Fatal("active window did not resume receipt polling")
	}
}
