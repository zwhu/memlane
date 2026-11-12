package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestCodexComputerConnectionStatusUsesOnlyCurrentProtocolEvidence(t *testing.T) {
	for _, version := range []string{"codex-cli 0.167.0", "codex-cli 0.167.0-alpha.3.2", "codex-cli 9.0.0"} {
		t.Run(version, func(t *testing.T) {
			a := New("test", nil)
			a.serviceState = "ready"
			c := records.Connection{ID: "codex-computer", Agent: "codex", Enabled: true, Generation: 1}
			d := agents.Discovery{Agent: "codex", Path: "/synthetic/codex", Version: version}
			i := agents.Installation{Connection: c, AgentVersion: "codex-cli 0.167.0", Options: agents.InstallOptions{AgentPath: d.Path, ComputerLevel: true}, VerifiedGeneration: 1, VerifiedAt: "old project receipt"}
			a.agents.Installs = map[string]agents.Installation{c.ID: i}
			if state, _ := a.agentStatus(d, &c); state != "待验证" {
				t.Fatal("old project receipt implied computer connection", state)
			}
			i.ProtocolGeneration = 1
			i.ProtocolVerifiedAt = "actual protocol receipt"
			i.ProtocolScopeDigest = records.MergeDigest([]any{c.Project, c.AllowedScopes})
			a.agents.Installs[c.ID] = i
			if state, detail := a.agentStatus(d, &c); state != "已连接" || !strings.Contains(detail, "尚未授权") || strings.Contains(detail, "首次任务") {
				t.Fatal(state, detail)
			}
			c.Generation = 2
			if state, _ := a.agentStatus(d, &c); state != "待重载" {
				t.Fatal("old generation survived grant change", state)
			}
			c.Generation = 1
			c.Enabled = false
			if state, _ := a.agentStatus(d, &c); state != "待连接" {
				t.Fatal("revoked connection retained success", state)
			}
			c.Enabled = true
			i.Options.ComputerLevel = false
			a.agents.Installs[c.ID] = i
			if state, _ := a.agentStatus(d, &c); state != "待适配" {
				t.Fatal("legacy project adapter became computer-level", state)
			}
		})
	}
}

func TestCodexComputerGrantCannotBorrowHistoricalProjectProof(t *testing.T) {
	a := New("test", nil)
	a.serviceState = "ready"
	c := records.Connection{ID: "codex", Agent: "codex", Enabled: true, Generation: 2, AllowedScopes: []string{"project-a"}}
	d := agents.Discovery{Agent: "codex", Path: "/synthetic/codex", Version: "codex-cli 0.167.0"}
	a.agents.Found = []agents.Discovery{d}
	a.agents.Installs = map[string]agents.Installation{c.ID: {Connection: c, AgentVersion: d.Version, Options: agents.InstallOptions{ComputerLevel: true, AgentPath: d.Path}, VerifiedGeneration: 2, VerifiedAt: "historical project receipt", ProtocolGeneration: 1, ProtocolVerifiedAt: "historical protocol receipt"}}
	if state, _ := a.agentStatus(d, &c); state != "待验证" {
		t.Fatal("grant used unrelated proof", state)
	}
	if state := a.projectConnectionState(c, "project-a"); state != "待重载与验证" {
		t.Fatal(state)
	}
	c.Enabled = false
	if state := a.projectConnectionState(c, "project-a"); state != "已断开" {
		t.Fatal(state)
	}
}

func TestCodexComputerDetailKeepsAuthorizationSeparateAndReachable(t *testing.T) {
	a := New("test", nil)
	a.serviceState = "ready"
	c := records.Connection{ID: "computer", Agent: "codex", Name: "Codex", Enabled: true, Generation: 3, AllowedScopes: []string{"project-a"}}
	d := agents.Discovery{Agent: "codex", Name: "Codex", Path: "/synthetic/codex", Version: "codex-cli 0.167.0"}
	a.agents.Found = []agents.Discovery{d}
	a.agents.Connections = []records.Connection{c}
	a.agents.Installs = map[string]agents.Installation{c.ID: {Connection: c, AgentVersion: d.Version, Options: agents.InstallOptions{ComputerLevel: true, AgentPath: d.Path}}}
	a.projects = []ProjectItem{{ID: "project-a", Name: "明确项目", Path: "/explicit/a"}, {ID: "project-b", Name: "其他项目", Path: "/other/b"}}
	a.scopes = []records.Scope{{ID: "project-a", Kind: "project", Name: "明确项目"}, {ID: "project-b", Kind: "project", Name: "其他项目"}}
	a.selectAgent("connection-" + c.ID)
	tt := ui.NewTester(a.agentDetail, 800, 600)
	if !tt.HasText("电脑级连接") || !tt.HasText("已重载，验证连接") || tt.HasText("项目与检索范围") || tt.HasText("开始首次任务") {
		t.Fatal("computer detail retained old project-first flow")
	}
	if a.agents.Workspace != "" || a.agents.Project != "" {
		t.Fatal("render implicitly selected a project")
	}
	a.openProjectConnectionVerification(c, records.Scope{ID: "project-a", Path: "/explicit/a"})
	if a.settingsSection != "agents" || a.agents.Selection != "connection-computer" || a.agents.Project != "project-a" || a.agents.Workspace != "/explicit/a" {
		t.Fatal("explicit project verification lost target")
	}
	a.openProjectConnectionVerification(c, records.Scope{ID: "project-b", Path: "/other/b"})
	if a.agents.Project != "project-b" {
		t.Fatal("project selection was restricted")
	}
	a.agents.Connections[0].Enabled = false
	a.selectAgent("connection-" + c.ID)
	reopened := ui.NewTester(a.agentDetail, 800, 600)
	if !reopened.HasText("选择电脑级重新连接") || reopened.HasText("开始首次任务") {
		t.Fatal("revoked computer connection returned to legacy installer")
	}
}

func TestAgentConnectionsReadRejectsPriorLibraryReceipt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "connections" {
			close(entered)
			<-release
			return serviceipc.Response{OK: true, Result: []records.Connection{{ID: "old", Agent: "codex", Enabled: true}}}
		}
		return serviceipc.Response{OK: false}
	})
	a.agents.Context = a.libraryContext
	a.agents.Dispatch = a.dispatch
	a.agents.Root = t.TempDir()
	a.refreshAgents(false)
	<-entered
	a.libraryEpoch++
	a.agents.Connections = []records.Connection{{ID: "new", Agent: "codex", Enabled: false}}
	a.agents.Loading = true
	close(release)
	manualAwait(t, updates)
	if len(a.agents.Connections) != 1 || a.agents.Connections[0].ID != "new" || !a.agents.Loading {
		t.Fatal("old connection read changed new library")
	}
}

func TestCodexComputerProjectProofIsBoundToRealSelectedDirectory(t *testing.T) {
	a := New("test", nil)
	a.serviceState = "ready"
	first, second := t.TempDir(), t.TempDir()
	c := records.Connection{ID: "computer", Agent: "codex", Enabled: true, Generation: 3, AllowedScopes: []string{"a", "b"}}
	d := agents.Discovery{Agent: "codex", Path: "/synthetic/codex", Version: "codex-cli 0.167.0"}
	digest, err := agents.CodexWorkspaceIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	i := agents.Installation{Connection: c, AgentVersion: d.Version, Options: agents.InstallOptions{ComputerLevel: true, AgentPath: d.Path}, ProtocolGeneration: 3, ProtocolVerifiedAt: "scope receipt", ProtocolScopeDigest: records.MergeDigest([]any{c.Project, c.AllowedScopes}), ProtocolProject: "a", ProtocolWorkspaceDigest: digest}
	a.agents.Found = []agents.Discovery{d}
	a.agents.Installs = map[string]agents.Installation{c.ID: i}
	a.scopes = []records.Scope{{ID: "a", Kind: "project", Path: first}, {ID: "b", Kind: "project", Path: second}}
	if state := a.projectConnectionState(c, "a"); state != "项目链路已验证" {
		t.Fatal(state)
	}
	if state := a.projectConnectionState(c, "b"); state != "待重载与验证" {
		t.Fatal("A proof implied B", state)
	}
	a.scopes[0].Path = second
	if state := a.projectConnectionState(c, "a"); state != "待重载与验证" {
		t.Fatal("replaced directory retained proof", state)
	}
	c.AllowedScopes = []string{"a"}
	if state, _ := a.agentStatus(d, &c); state == "已连接" {
		t.Fatal("changed scope reused old same-generation proof")
	}
}
