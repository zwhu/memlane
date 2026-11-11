package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestAgentBatchCardsKeepSelectionAndRejectUnsupportedOrExisting(t *testing.T) {
	a := New("test", nil)
	a.page = "设置"
	a.settingsSection = "agents"
	a.agents.PiDir = t.TempDir()
	a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "0.84.1"}, {Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: "synthetic"}}
	tt := ui.NewTester(a.View, 1200, 820)
	if len(a.selectedConnectionTargets()) != 0 {
		t.Fatal("agents preselected")
	}
	if err := tt.Click("选择 pi"); err != nil {
		t.Fatal(err)
	}
	if len(a.selectedConnectionTargets()) != 1 || !tt.HasText("连接所选 Agent（1）") || a.settingsSection != "agents" {
		t.Fatal("selection opened a legacy page or lost targets")
	}
	_ = tt.Click("选择 Codex")
	if a.agents.Batch.Selected["codex"] || len(a.selectedConnectionTargets()) != 1 {
		t.Fatal("unsupported adapter selected")
	}
	a.agents.Connections = []records.Connection{{Agent: "pi", Enabled: true, ID: "synthetic", Generation: 1}}
	if len(a.selectedConnectionTargets()) != 0 {
		t.Fatal("existing connection counted for reinstall")
	}
	a.selectAllAgentConnections()
	if len(a.selectedConnectionTargets()) != 0 {
		t.Fatal("select all included existing or unsupported items")
	}
}

func TestAgentBatchSupportsVerifiedCodexVersionsWithSeparateConfiguration(t *testing.T) {
	for _, version := range []string{"codex-cli 0.167.0", "codex-cli 0.167.0-alpha.3.2"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			codex := filepath.Join(root, "isolated-codex")
			t.Setenv("CODEX_HOME", codex)
			a := New("test", nil)
			a.page, a.settingsSection = "设置", "agents"
			a.agents.PiDir, a.agents.Home = filepath.Join(root, "isolated-pi"), root
			a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "0.84.1"}, {Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: version}}
			tt := ui.NewTester(a.View, 1200, 820)
			for _, label := range []string{"选择 pi", "选择 Codex"} {
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			targets := a.selectedConnectionTargets()
			if len(targets) != 2 || targets[0].Directory != a.agents.PiDir || targets[1].Agent != "codex" || targets[1].Directory != codex || !tt.HasText("连接所选 Agent（2）") {
				t.Fatal("batch lost an adapter or mixed Agent configuration roots", targets)
			}
			a.agents.Connections = []records.Connection{{Agent: "codex", Enabled: true}}
			if len(a.selectedConnectionTargets()) != 1 {
				t.Fatal("already connected Codex remained reinstallable")
			}
		})
	}
}

func TestAgentBatchFooterAndPendingIntentSurviveNarrowSetup(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Open = true
	a.firstLaunch.State.Step = "agents"
	a.firstLaunch.State.Stage = "ready"
	a.agents.Batch.State = records.AgentConnectionBatchState{Identity: "synthetic", Batch: &records.AgentConnectionBatch{ID: "batch", State: "waiting", Items: []records.AgentConnectionBatchItem{{Target: records.AgentConnectionTarget{Agent: "pi"}, State: "awaiting_reload"}, {Target: records.AgentConnectionTarget{Agent: "codex"}, State: "failed"}}}}
	tt := ui.NewTester(a.View, 980, 620)
	for _, label := range []string{"已重载，验证连接", "重试失败项", "取消本批连接", "继续配置记忆处理"} {
		node, ok := tt.Find(label)
		if !ok || node.Y+node.H > 620 || node.X+node.W > 980 {
			t.Fatal("batch footer clipped", label, node)
		}
	}
	root := t.TempDir()
	a.agents.Root = root
	a.library = serviceipc.Library{Root: filepath.Join(root, "service")}
	intent := AgentBatchIntent{ServiceRoot: a.library.Root, Start: &records.AgentConnectionBatchStart{Identity: "synthetic", OperationID: "frozen-operation", Targets: []records.AgentConnectionTarget{{Agent: "pi", Path: "/synthetic/pi", Directory: "/synthetic/config"}}}}
	if err := a.persistAgentBatchIntent(intent); err != nil {
		t.Fatal(err)
	}
	b := New("reopened", nil)
	b.agents.Root = root
	b.library = a.library
	b.loadAgentBatchIntent()
	if !b.agents.Batch.pending() || b.agents.Batch.Intent.Start.OperationID != "frozen-operation" || !b.agents.Batch.frozen() {
		t.Fatal("unknown start request lost its stable identity")
	}
}

func TestAgentConnectionHeaderActionsRemainSeparated(t *testing.T) {
	for _, setup := range []bool{false, true} {
		for _, width := range []int{1200, 980} {
			a := New("test", nil)
			if setup {
				a.firstLaunch.Open = true
				a.firstLaunch.State.Step = "agents"
				a.firstLaunch.State.Stage = "ready"
			} else {
				a.page = "设置"
				a.settingsSection = "agents"
			}
			tt := ui.NewTester(a.View, width, 820)
			left, ok := tt.Find("全选可连接")
			if !ok {
				t.Fatal("selection action missing")
			}
			right, ok := tt.Find("重新检测")
			if !ok {
				t.Fatal("discovery action missing")
			}
			if right.X-(left.X+left.W) < 8 {
				t.Fatalf("adjacent header actions touch at width %d, setup=%v: %+v %+v", width, setup, left, right)
			}
		}
	}
}

func TestFailedInstallationIdentityIsNotPresentedAsConfigured(t *testing.T) {
	a := New("test", nil)
	a.page = "设置"
	a.settingsSection = "agents"
	a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "0.84.1"}}
	a.agents.Connections = []records.Connection{{ID: "synthetic", Agent: "pi", Name: "pi", Generation: 1, Enabled: true}}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("配置未完成") || tt.HasText("已配置连接") {
		t.Fatal("registered identity falsely presented as installed configuration")
	}
	if !tt.HasText("尚未授权项目与通用知识") {
		t.Fatal("empty grants presented as existing authorization")
	}
}

func TestAgentCardsWholeSurfaceAndKeyboardToggle(t *testing.T) {
	a := New("test", nil)
	a.page = "设置"
	a.settingsSection = "agents"
	a.agents.PiDir = t.TempDir()
	a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "0.84.1"}, {Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: "synthetic"}}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("连接所选 Agent（0）") {
		t.Fatal("empty selection action missing")
	}
	node, ok := tt.Find("选择 pi")
	if !ok || node.W < 300 || node.H < 156 {
		t.Fatal("only small checkbox is selectable", node)
	}
	tt.ClickAt(node.X+24, node.Y+node.H-20)
	if !a.agents.Batch.Selected["pi"] {
		t.Fatal("card surface did not select")
	}
	tt.Key(0, ui.KeySpace)
	if a.agents.Batch.Selected["pi"] {
		t.Fatal("keyboard did not toggle focused card")
	}
	_ = tt.Click("选择 Codex")
	if a.agents.Batch.Selected["codex"] {
		t.Fatal("disabled card toggled")
	}
}

func TestConnectionCardHeadingsAlignDespiteDifferentDetails(t *testing.T) {
	for _, width := range []int{1200, 980} {
		a := New("test", nil)
		a.page = "设置"
		a.settingsSection = "agents"
		a.agents.PiDir = t.TempDir()
		a.agents.Found = []agents.Discovery{{Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: "synthetic"}, {Agent: "pi", Name: "pi", Path: "/synthetic/pi", Version: "0.84.1"}}
		tt := ui.NewTester(a.View, width, 820)
		left, ok := tt.Find("Codex")
		if !ok {
			t.Fatal("left heading missing")
		}
		right, ok := tt.Find("pi")
		if !ok {
			t.Fatal("right heading missing")
		}
		if left.Y != right.Y {
			t.Fatalf("card headings misaligned at %d: %v vs %v", width, left.Y, right.Y)
		}
	}
}

func TestSettingsLegacyConnectionShowsManagementWithoutGrantOrModelForm(t *testing.T) {
	for _, agent := range []string{"pi", "codex"} {
		for _, width := range []int{1200, 800} {
			t.Run(fmt.Sprintf("%s/%d", agent, width), func(t *testing.T) {
				a := New("test", nil)
				a.page, a.settingsSection = "设置", "agents"
				a.runtimeStatus = RuntimeStatus{Phase: "ready"}
				a.serviceState = "ready"
				a.agents.Context = context.Background()
				a.agents.Dispatch = func(func()) {}
				conn := records.Connection{ID: "old", Agent: agent, Name: "旧连接", Project: "project", AllowedScopes: []string{"project", "experience"}, Enabled: true, Generation: 4}
				path := "/synthetic/" + agent
				a.agents.Found = []agents.Discovery{{Agent: agent, Name: agent, Path: path, Version: map[string]string{"pi": "0.84.1", "codex": "codex-cli 0.167.0"}[agent]}}
				a.agents.PiDir = "/synthetic/pi-config"
				a.agents.Home = "/synthetic/home"
				a.agents.Connections = []records.Connection{conn}
				installation := agents.Installation{Connection: conn, Options: agents.InstallOptions{AgentPath: path, PiDirectory: a.agents.PiDir, Workspace: "/synthetic/project"}, ConfigPath: "/synthetic/config", VerifiedAt: "old-receipt", VerifiedGeneration: 4}
				a.agents.Installs = map[string]agents.Installation{conn.ID: installation}
				a.projects = []ProjectItem{{ID: "project", Name: "项目", Path: "/synthetic/project"}}
				tt := ui.NewTester(a.View, width, 720)
				if err := tt.Click("旧连接"); err != nil {
					t.Fatal(err)
				}
				for _, label := range []string{"项目与检索范围", "项目（可跳过）", "任务工作目录", "验证模型（留空使用 Agent 默认模型）", "应用范围", "验证读写", "开始首次任务", "验证项目"} {
					if tt.HasText(label) {
						t.Fatal("legacy connection form leaked into settings", agent, width, label)
					}
				}
				for _, label := range []string{"连接层级", "重新读取连接", "已重载，验证连接", "断开"} {
					if !tt.HasText(label) {
						t.Fatal("management action missing", label)
					}
				}
				if a.settingsProtocolVerificationReason(a.agents.Found[0], &a.agents.Connections[0]) == "" {
					t.Fatal("legacy model-backed validation offered as protocol check")
				}
				if !reflect.DeepEqual(a.agents.Connections[0], conn) || !reflect.DeepEqual(a.agents.Installs[conn.ID], installation) || len(a.agents.Batch.Selected) != 0 {
					t.Fatal("opening settings changed legacy authorization or installation")
				}
				writeScreenshot(t, tt, fmt.Sprintf("f4-%s-%d.png", agent, width))
			})
		}
	}
}
func TestSettingsVerifyCannotFallBackToLegacyModelTask(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "agents"
	conn := records.Connection{ID: "old", Agent: "pi", Enabled: true, Generation: 2, AllowedScopes: []string{"project"}}
	a.agents.Connections = []records.Connection{conn}
	a.agents.Selection = "connection-old"
	a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/never-run", Version: "0.84.1"}}
	a.agents.Installs = map[string]agents.Installation{"old": {Connection: conn, Options: agents.InstallOptions{AgentPath: "/synthetic/never-run"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.agents.Context = ctx
	a.agents.Dispatch = func(func()) { t.Error("blocked validation dispatched") }
	a.verifyAgent()
	if a.agents.Busy || a.agents.Cancel != nil || a.agents.Notice == "" || a.agents.Provider != "" || a.agents.Model != "" {
		t.Fatal("settings entered model verification or read model config")
	}
}
func TestSettingsProtocolManagementUsesExactSupportedConnection(t *testing.T) {
	for _, agent := range []string{"pi", "codex"} {
		a := New("test", nil)
		a.page, a.settingsSection = "设置", "agents"
		conn := records.Connection{ID: "d9d592f0-6ca5-4d6d-ae45-4da4b0f96028", Agent: agent, Enabled: true, Generation: 3}
		d := agents.Discovery{Agent: agent, Path: "/synthetic/" + agent, Version: map[string]string{"pi": "0.84.1", "codex": "codex-cli 0.167.0"}[agent]}
		a.agents.Installs = map[string]agents.Installation{conn.ID: {Connection: conn, Options: agents.InstallOptions{ComputerLevel: true, AgentPath: d.Path}}}
		if reason := a.settingsProtocolVerificationReason(d, &conn); reason != "" {
			t.Fatal("ungranted protocol-only connection blocked", agent, reason)
		}
		installation := a.agents.Installs[conn.ID]
		installation.Connection.Generation = 2
		a.agents.Installs[conn.ID] = installation
		if a.settingsProtocolVerificationReason(d, &conn) == "" {
			t.Fatal("stale generation accepted")
		}
		installation.Connection = conn
		a.agents.Installs[conn.ID] = installation
		conn.AllowedScopes = []string{"0e20f00e-09f5-4820-9100-c2aad2d700ac"}
		installation.Connection = conn
		a.agents.Installs[conn.ID] = installation
		a.projects = []ProjectItem{{ID: conn.AllowedScopes[0]}}
		reason := a.settingsProtocolVerificationReason(d, &conn)
		if agent == "pi" && reason != "" {
			t.Fatal("settled granted pi metadata check blocked", reason)
		}
		if agent == "codex" && reason == "" {
			t.Fatal("Codex project context gate changed")
		}
	}
}

func TestSettingsSettledPiProtocolStatusRequiresCurrentGrantProof(t *testing.T) {
	project := "0e20f00e-09f5-4820-9100-c2aad2d700ac"
	conn := records.Connection{ID: "d9d592f0-6ca5-4d6d-ae45-4da4b0f96028", Agent: "pi", Name: "pi", Enabled: true, Generation: 5, Project: project, AllowedScopes: []string{project}}
	path := "/synthetic/pi"
	d := agents.Discovery{Agent: "pi", Name: "pi", Path: path, Version: "0.84.1"}
	installation := agents.Installation{Connection: conn, Options: agents.InstallOptions{AgentPath: path, PiDirectory: "/synthetic/pi-config", Workspace: "/synthetic/old-cwd"}, ProtocolGeneration: 5, ProtocolVerifiedAt: "2026-11-11T12:00:00Z", ProtocolScopeDigest: records.MergeDigest([]any{conn.Project, conn.AllowedScopes})}
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "agents"
	a.serviceState = "ready"
	a.agents.Selection = "connection-" + conn.ID
	a.agents.Connections = []records.Connection{conn}
	a.agents.Installs = map[string]agents.Installation{conn.ID: installation}
	a.agents.Found = []agents.Discovery{d}
	state, detail := a.agentStatus(d, &conn)
	if state != "已连接" || !strings.Contains(detail, "授权元数据已核对") || strings.Contains(detail, "已通过保存") {
		t.Fatal("metadata proof enlarged or absent", state, detail)
	}
	tt := ui.NewTester(a.agentDetail, 800, 400)
	if !tt.HasText(installation.ProtocolVerifiedAt) {
		t.Fatal("current metadata proof timestamp absent")
	}
	changed := conn
	changed.Generation++
	a.agents.Connections[0] = changed
	a.agents.Installs[conn.ID] = installation
	if state, _ := a.agentStatus(d, &changed); state == "已连接" {
		t.Fatal("old generation still connected")
	}
	installation.Connection = changed
	installation.ProtocolGeneration = changed.Generation
	changed.AllowedScopes = append([]string{}, conn.AllowedScopes...)
	changed.AllowedScopes = append(changed.AllowedScopes, "38780da8-26ed-4345-a75d-22b56a08b11f")
	installation.Connection = changed
	a.agents.Connections[0] = changed
	a.agents.Installs[conn.ID] = installation
	if state, _ := a.agentStatus(d, &changed); state != "待验证" {
		t.Fatal("old scope digest accepted", state)
	}
	tt.Frame()
	if tt.HasText(installation.ProtocolVerifiedAt) {
		t.Fatal("stale scope proof timestamp shown")
	}
	installation.ProtocolScopeDigest = records.MergeDigest([]any{changed.Project, changed.AllowedScopes})
	a.agents.Installs[conn.ID] = installation
	if state, _ := a.agentStatus(d, &changed); state != "已连接" {
		t.Fatal("current exact metadata proof not accepted", state)
	}
}
func TestSettingsSettledPiVerificationChoosesProtocolWithoutReadingModelConfig(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "agents"
	project := "0e20f00e-09f5-4820-9100-c2aad2d700ac"
	conn := records.Connection{ID: "d9d592f0-6ca5-4d6d-ae45-4da4b0f96028", Agent: "pi", Enabled: true, Generation: 5, Project: project, AllowedScopes: []string{project}}
	root := t.TempDir()
	a.agents.PiDir = root
	a.agents.Root = root
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"defaultProvider":"must-not-read","defaultModel":"must-not-read"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a.agents.Selection = "connection-" + conn.ID
	a.agents.Workspace = "/synthetic/old-selected-cwd"
	a.agents.Connections = []records.Connection{conn}
	a.agents.Found = []agents.Discovery{{Agent: "pi", Name: "pi", Path: "/synthetic/never-execute", Version: "0.84.1"}}
	a.agents.Installs = map[string]agents.Installation{conn.ID: {Connection: conn, Options: agents.InstallOptions{AgentPath: "/synthetic/never-execute", Workspace: "/synthetic/old-installed-cwd", ClientRoot: root}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.agents.Context = ctx
	delivered := make(chan func(), 1)
	a.agents.Dispatch = func(fn func()) { delivered <- fn }
	a.verifyAgent()
	if !a.agents.Busy || a.agents.Notice != "正在核对本机连接及当前授权元数据…" || a.agents.Provider != "" || a.agents.Model != "" {
		t.Fatal("granted pi fell back to model config or model task")
	}
	// CheckInstallation rejects this deliberately absent synthetic config before
	// launching anything. Capture terminal dispatch without invoking discovery.
	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("local protocol preparation did not terminate")
	}
	if !reflect.DeepEqual(a.agents.Connections[0], conn) {
		t.Fatal("metadata verification changed grant")
	}
}
