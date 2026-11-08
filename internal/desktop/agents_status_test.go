package desktop

import (
	"testing"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestAgentInstallationIsSeparateFromConnectionUsability(t *testing.T) {
	a := New("test", nil)
	a.serviceState = "ready"
	d := agents.Discovery{Agent: "trae", Path: "/synthetic/trae-cn", Version: "3.4.15", Channel: "cn/stable", CapabilityError: "该版本接入尚未验证"}
	if state, _ := a.agentStatus(d, nil); state != "已发现" {
		t.Fatalf("installed Agent hidden as %q", state)
	}
	c := records.Connection{ID: "synthetic-connection", Agent: "trae", Generation: 1, Enabled: true}
	a.agents.Installs = map[string]agents.Installation{c.ID: {Connection: c, Options: agents.InstallOptions{AgentPath: d.Path}, VerifiedGeneration: 1}}
	if state, _ := a.agentStatus(d, &c); state != "待适配" {
		t.Fatalf("old verification made unsupported update usable: %q", state)
	}
	d.CapabilityError = ""
	if state, _ := a.agentStatus(d, &c); state != "可用" {
		t.Fatalf("supported verified connection regressed: %q", state)
	}
	d.Error = "无法读取安装信息"
	if state, _ := a.agentStatus(d, &c); state != "异常" {
		t.Fatalf("discovery failure masked: %q", state)
	}
}

func TestAgentConnectionReceiptDoesNotHideInstalledUpdate(t *testing.T) {
	a := New("test", nil)
	c := records.Connection{ID: "synthetic-connection", Agent: "trae", Generation: 1, Enabled: true}
	d := agents.Discovery{Agent: "trae", Path: "/synthetic/trae-cn", Version: "3.4.15", Channel: "cn/stable", CapabilityError: "新版本尚未验证"}
	a.agents.Found = []agents.Discovery{d}
	a.agents.Selection = "connection-" + c.ID
	a.agents.Connections = []records.Connection{c}
	a.agents.Installs = map[string]agents.Installation{c.ID: {Connection: c, Options: agents.InstallOptions{AgentPath: d.Path}, AgentVersion: "3.4.14", AgentChannel: "cn/stable", VerifiedGeneration: 1}}
	selected, conn := a.selectedAgent()
	if conn == nil || selected.Version != "3.4.15" || selected.CapabilityError != d.CapabilityError {
		t.Fatalf("old receipt replaced actual installation: %+v", selected)
	}
	i := a.agents.Installs[c.ID]
	i.Options.AgentPath = "/synthetic/other/trae-cn"
	a.agents.Installs[c.ID] = i
	selected, _ = a.selectedAgent()
	if selected.Error == "" || selected.Path != i.Options.AgentPath || selected.Version != "" {
		t.Fatalf("another installation inherited discovered support: %+v", selected)
	}
	d.CapabilityError = ""
	a.serviceState = "ready"
	if state, _ := a.agentStatus(d, &c); state != "异常" {
		t.Fatalf("list row inherited support from another installation: %q", state)
	}
}
