package simpledesktop

import (
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/controlplane"
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

func TestFocusedOnboardingConnectionBatch(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		dark  bool
		drift bool
		all   bool
	}{
		{name: "all_light", all: true},
		{name: "dark", dark: true},
		{name: "changed_preview", drift: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testFocusedOnboardingConnectionBatch(t, scenario.dark, scenario.drift, scenario.all, "")
		})
	}
}

func TestOnboardingLegacyPluginPreviewBeforeConnection(t *testing.T) {
	for _, confirmation := range []string{"batch", "single"} {
		t.Run(confirmation, func(t *testing.T) {
			testFocusedOnboardingConnectionBatch(t, false, false, true, confirmation)
		})
	}
}

func testFocusedOnboardingConnectionBatch(t *testing.T, dark, drift, all bool, legacyConfirmation string) {
	t.Helper()
	root := t.TempDir()
	a := New(Config{GenerationFile: filepath.Join(root, "generation.json"), ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.initialSetup = true
	a.environment.InitialStep, a.environment.RuntimeStarted, a.environment.HostsLoaded = 3, true, true
	a.environment.SelectedAgents = []string{"pi", "trae_ide"}
	if legacyConfirmation != "" {
		a.environment.SelectedAgents[0] = "codex"
	}
	if !all {
		a.environment.SelectedAgents = []string{"codex", "pi", "trae_ide", "opencode"}
	}
	s := &a.integrations
	s.Loaded, s.ConnectionsLoaded = true, true
	s.NextReceiptCheck = time.Now().Add(time.Hour)
	s.PinnedAgentKinds = slices.Clone(a.environment.SelectedAgents)
	directory := ConnectionsDirectory{Revision: 7}
	for _, kind := range append(slices.Clone(a.environment.SelectedAgents), "traex") {
		directory.Connections = append(directory.Connections, AgentConnection{ID: kind, Kind: kind, Detected: true, Status: "not_connected"})
	}
	s.Connections = directory
	directory.Connections = slices.Clone(directory.Connections)
	previews := map[string]controlplane.ConnectionPreview{}
	for _, kind := range a.environment.SelectedAgents {
		previews[kind] = controlplane.ConnectionPreview{
			InstanceID: kind, Kind: kind, Target: "/profiles/" + kind + "/hooks.json", Revision: 7,
			OK: true, Status: "not_connected", BaselineSHA256: "baseline-" + kind, ProposedSHA256: "reviewed-" + kind,
		}
	}
	if legacyConfirmation != "" {
		preview := previews["codex"]
		preview.DisabledPlugins = []string{"legacy-cognee-a", "legacy-cognee-b"}
		previews["codex"] = preview
	}
	s.ConnectionPreviews = maps.Clone(previews)
	var mutations []string
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		var body any
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/connections/codex/preview", "GET /v1/connections/pi/preview", "GET /v1/connections/trae_ide/preview":
			parts := strings.Split(r.URL.Path, "/")
			current := previews[parts[3]]
			current.Revision = directory.Revision
			if current.Kind == "trae_ide" && drift {
				current.Target = "/changed/trae/hooks.json"
			}
			body = current
		case "POST /v1/connections/codex/prepare", "POST /v1/connections/codex/activate",
			"POST /v1/connections/pi/prepare", "POST /v1/connections/pi/activate",
			"POST /v1/connections/trae_ide/prepare", "POST /v1/connections/trae_ide/activate":
			parts := strings.Split(r.URL.Path, "/")
			id, action := parts[3], parts[4]
			mutations = append(mutations, id+"/"+action)
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("malformed %s payload: %v", action, err)
			}
			idx := slices.IndexFunc(directory.Connections, func(row AgentConnection) bool { return row.ID == id })
			row := &directory.Connections[idx]
			pending, err := loadGenerationOperationsFromDir(a.Config.GenerationFile + ".ui-operations")
			if err != nil || len(pending) != 1 || pending[0].Operation != payload["operation_id"] ||
				float64(pending[0].Revision) != payload["expected_revision"] || payload["expected_revision"] != float64(directory.Revision) {
				t.Errorf("mutation lacks the corresponding persisted receipt or fresh revision: %s %+v %v", r.URL.Path, payload, pending)
			}
			if action == "prepare" {
				row.Status = "prepared"
				row.Preview = &CodexConnectionPreview{BaselineSHA256: previews[id].BaselineSHA256, ProposedSHA: previews[id].ProposedSHA256}
			} else {
				if row.Status != "prepared" || payload["proposed_sha256"] != row.Preview.ProposedSHA {
					t.Errorf("activation did not match prepared preview: %+v", payload)
				}
				row.Status = "configured_pending_new_session"
			}
			directory.Revision++
			body = map[string]any{"revision": directory.Revision, "connection": *row}
		case "GET /v1/connections":
			body = directory
		case "GET /v1/projects":
			body = AccessDirectory{}
		default:
			t.Errorf("unexpected connection request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(body)
	})}
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	if legacyConfirmation != "" {
		a.confirmOnboardingConnection(s.Connections.Connections[0])
		if s.Busy {
			t.Fatal("single connection accepted before its legacy-plugin changes were shown")
		}
		a.runOnboardingConnectionBatch()
		if s.Busy {
			t.Fatal("batch connection accepted before its legacy-plugin changes were shown")
		}
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.SetDark(dark)
	if legacyConfirmation != "" {
		if !tt.HasText("将停用旧插件：legacy-cognee-a") || !tt.HasText("将停用旧插件：legacy-cognee-b") {
			t.Fatalf("missing same-page legacy plugin changes: %v", tt.Texts())
		}
		expected := []string{"codex/prepare", "codex/activate"}
		if legacyConfirmation == "batch" {
			clickProjectUI(t, tt, "全部接入（2）")
			expected = append(expected, "trae_ide/prepare", "trae_ide/activate")
		} else {
			clickProjectUI(t, tt, "Codex 更多操作")
			clickProjectUI(t, tt, "只接入这个")
		}
		drainReading(t, updates, func() bool { return !s.Busy && !a.access.Loading })
		if !slices.Equal(mutations, expected) {
			t.Fatalf("preview confirmation did not apply the expected connections: %v", mutations)
		}
		return
	}
	tt.Advance(700 * time.Millisecond)
	if !tt.HasText("将写入 /profiles/pi/hooks.json") || tt.HasText("TRAE CLI") || tt.HasText("此 Agent 需要在准备配置后再次确认接入。") == all {
		t.Fatalf("selected rows, paths, or second confirmation missing: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "查看变更预览：pi")
	if !tt.HasText("加入 Memlane 的记忆采集与召回入口") {
		t.Fatal("preview did not open in the same screen")
	}
	clickProjectUI(t, tt, "收起变更预览：pi")
	action := "全部接入（2）"
	if !all {
		clickProjectUI(t, tt, "Codex 更多操作")
		if !tt.HasText("跳过这个") || !tt.HasText("立即重新检测") {
			t.Fatalf("row menu unavailable: %v", tt.Texts())
		}
		tt.Key(0, ui.KeyEscape)
		if tt.HasText("跳过这个") || a.onboardingConnection.Skipped["codex"] {
			t.Fatal("Escape did not close the row menu without acting")
		}
		clickProjectUI(t, tt, "Codex 更多操作")
		clickProjectUI(t, tt, "跳过这个")
		if !a.onboardingConnection.Skipped["codex"] || !tt.HasText("已跳过") {
			t.Fatal("row-local skip was not applied")
		}
		action = "接入其余（2）"
	}
	clickProjectUI(t, tt, action)
	if !a.onboardingConnection.BatchRunning || !tt.HasText("写入中") {
		t.Fatal("batch did not expose the current host's writing stage")
	}
	drainReading(t, updates, func() bool { return !s.Busy && !a.access.Loading })
	tt.Frame()
	expected := []string{"pi/prepare", "pi/activate"}
	if !drift {
		expected = append(expected, "trae_ide/prepare", "trae_ide/activate")
	}
	if !slices.Equal(mutations, expected) {
		t.Fatalf("unexpected writes, batch must be serial and leave second confirmations alone: %v", mutations)
	}
	if !tt.HasText("待新会话") || !tt.HasText("最后一步：重启已接入的 Agent") || len(s.PendingOperations) != 0 {
		t.Fatalf("new session state, shared instructions, or journal cleanup missing: %v", tt.Texts())
	}
	if drift {
		if !strings.Contains(s.Error, "配置变更预览已更新") || !strings.Contains(strings.Join(tt.Texts(), "\n"), "/changed/trae/hooks.json") {
			t.Fatalf("changed file was not surfaced for another review: %v", tt.Texts())
		}
	} else if !tt.HasText("在 TRAE CN「设置 → Hooks → 全局」打开开关并确认安全提示。") {
		t.Fatalf("TRAE CN instructions are missing from its row: %v", tt.Texts())
	}
	if !drift {
		theme := "light"
		if dark {
			theme = "dark"
		}
		fixtureImage(t, tt, "focused-onboarding-connect-"+theme)
	}
	if all {
		clickProjectUI(t, tt, "继续")
		if a.onboardingPage() != onboardingProcess {
			t.Fatal("confirming all selected Agents did not allow moving to processing")
		}
		return
	}
	clickProjectUI(t, tt, "稍后接入")
	if a.onboardingPage() != onboardingProcess || !a.onboardingConnection.Skipped["opencode"] {
		t.Fatal("deferring the remaining Agent did not continue to processing")
	}
}

func TestFocusedOnboardingAllConnectionAction(t *testing.T) {
	a := New(Config{}, nil)
	a.environment.RuntimeStarted = true
	a.environment.SelectedAgents = []string{"pi"}
	s := &a.integrations
	s.ConnectionsLoaded = true
	s.Connections.Connections = []AgentConnection{{Kind: "pi", ID: "pi", Status: "not_connected", Detected: true}}
	s.ConnectionPreviews = map[string]controlplane.ConnectionPreview{"pi": {
		InstanceID: "pi", Kind: "pi", OK: true, Status: "not_connected", Target: "/pi/index.ts",
		BaselineSHA256: "original", ProposedSHA256: "reviewed",
	}}
	label, disabled, run := a.onboardingConnectAction()
	if label != "全部接入（1）" || disabled || run == nil {
		t.Fatalf("all selected Agents are ready to confirm, got %q disabled=%v", label, disabled)
	}
	preview := s.ConnectionPreviews["pi"]
	preview.NeedsHookRepair = true
	s.ConnectionPreviews["pi"] = preview
	if _, disabled, run = a.onboardingConnectAction(); !disabled || run != nil {
		t.Fatal("a preview requiring repair must stay out of the batch")
	}
	a.environment.SelectedAgents = nil
	label, disabled, run = a.onboardingConnectAction()
	if label != "继续" || disabled || run == nil {
		t.Fatalf("empty selection must permit proceeding, got %q disabled=%v", label, disabled)
	}
}

func TestOnboardingCaptureNeedsActionWithNewSessions(t *testing.T) {
	a := setupLayoutFixture()
	a.environment.InitialStep, a.environment.RuntimeStarted = 3, true
	a.environment.SelectedAgents = []string{"trae_ide", "codex"}
	a.integrations.ConnectionsLoaded = true
	a.integrations.Connections.Connections = []AgentConnection{
		{ID: "trae", Kind: "trae_ide", Status: "configured_pending_new_session", NewSessions: 2, Capture: &AgentCaptureState{State: "needs_permission"}},
		{ID: "codex", Kind: "codex", Status: "configured_pending_new_session", NewSessions: 2, Capture: &AgentCaptureState{State: "waiting_message"}},
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("已生效") || !tt.HasText("待新会话") || !tt.HasText("在 TRAE CN「设置 → Hooks → 全局」打开开关并确认安全提示。") {
		t.Fatalf("capture status hid the TRAE CN action: %v", tt.Texts())
	}
	a.showOnboardingDone("Ollama")
	tt.Frame()
	if !tt.HasText("0 个已生效 · 2 个待新会话") || !tt.HasText("下一步：重启已接入的 Agent") {
		t.Fatalf("completion page hid remaining steps: %v", tt.Texts())
	}
}

func TestOnboardingRecheckClearsSkipAfterExternalConnection(t *testing.T) {
	root := t.TempDir()
	a := New(Config{ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.initialSetup = true
	a.environment.InitialStep, a.environment.RuntimeStarted = 3, true
	a.environment.SelectedAgents = []string{"codex"}
	s := &a.integrations
	s.ConnectionsLoaded = true
	row := AgentConnection{ID: "codex", Kind: "codex", Status: "not_connected"}
	s.Connections.Connections = []AgentConnection{row}
	a.skipOnboardingConnection(row.Kind)
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	row.Status = "configured_pending_new_session"
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/connections" {
			json.NewEncoder(w).Encode(ConnectionsDirectory{Connections: []AgentConnection{row}})
			return
		}
		http.NotFound(w, r)
	})}
	a.RefreshConnections()
	drainReading(t, updates, func() bool { return !s.Loading })
	if a.onboardingConnection.Skipped[row.Kind] {
		t.Fatal("recheck retained the stale local skip")
	}
	a.showOnboardingDone("Ollama")
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("0 个已生效 · 1 个待新会话") {
		t.Fatalf("completion page did not count newly connected agent: %v", tt.Texts())
	}
}

func TestOnboardingSkipWorksOnNeedsRepairRowAndUnblocksContinue(t *testing.T) {
	root := t.TempDir()
	a := New(Config{ProjectControl: beta3ControlConfig(t, root)}, nil)
	a.initialSetup = true
	a.environment.InitialStep, a.environment.RuntimeStarted = 3, true
	a.environment.SelectedAgents = []string{"trae_ide"}
	s := &a.integrations
	s.ConnectionsLoaded = true
	row := AgentConnection{ID: "trae-1", Kind: "trae_ide", Status: "configured_pending_new_session", NeedsHookRepair: true}
	s.Connections.Connections = []AgentConnection{row}
	if label, _ := a.onboardingConnectionStatus(row); label != "待修复" {
		t.Fatalf("status before skip = %q, want 待修复", label)
	}
	a.skipOnboardingConnection(row.Kind)
	if label, _ := a.onboardingConnectionStatus(row); label != "已跳过" {
		t.Fatalf("status after skip = %q, want 已跳过", label)
	}
	primary, disabled, _ := a.onboardingConnectAction()
	if primary != "继续" || disabled {
		t.Fatalf("skipping needs-repair row did not unblock continue: label=%q disabled=%v", primary, disabled)
	}
	row.NeedsHookRepair = false
	a.acceptConnectionsDirectory(ConnectionsDirectory{Connections: []AgentConnection{row}})
	if a.onboardingConnection.Skipped[row.Kind] {
		t.Fatal("repaired row retained stale skip flag")
	}
	if label, _ := a.onboardingConnectionStatus(row); label != "待新会话" {
		t.Fatalf("status after repair = %q, want 待新会话", label)
	}
}
