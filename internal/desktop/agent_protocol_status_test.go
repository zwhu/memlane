package desktop

import (
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestProtocolStatusDoesNotImplyMemoryAuthorizationOrReadWriteVerification(t *testing.T) {
	a := New("test", nil)
	a.serviceState = "ready"
	c := records.Connection{ID: "synthetic", Agent: "pi", Enabled: true, Generation: 1}
	d := agents.Discovery{Agent: "pi", Path: "/synthetic/pi", Version: "0.84.1"}
	i := agents.Installation{Connection: c, Options: agents.InstallOptions{AgentPath: d.Path}, VerifiedGeneration: 1, VerifiedAt: "old read/write fixture"}
	a.agents.Installs = map[string]agents.Installation{c.ID: i}
	if state, _ := a.agentStatus(d, &c); state != "待验证" {
		t.Fatal("historical read/write receipt became protocol success", state)
	}
	i.ProtocolGeneration = 1
	i.ProtocolVerifiedAt = "protocol fixture"
	a.agents.Installs[c.ID] = i
	state, message := a.agentStatus(d, &c)
	if state != "已连接" || !strings.Contains(message, "尚未授权") {
		t.Fatal("connection implied access", state, message)
	}
	a.serviceState = ""
	a.agents.Batch.ServiceReachable = true
	if state, message = a.agentStatus(d, &c); state != "已连接" || !strings.Contains(message, "环境尚未就绪") || !strings.Contains(message, "尚未授权") {
		t.Fatal("reachable protocol connection was confused with engine readiness", state, message)
	}
	a.agents.Batch.ServiceReachable = false
	if state, _ = a.agentStatus(d, &c); state != "异常" {
		t.Fatal("unreachable connection appeared live", state)
	}
	a.serviceState = "stopped"
	if state, _ = a.agentStatus(d, &c); state != "异常" {
		t.Fatal("stopped service presented as usable", state)
	}
	a.serviceState = "ready"
	i.ProtocolError = "cancelled"
	a.agents.Installs[c.ID] = i
	if state, _ = a.agentStatus(d, &c); state != "异常" {
		t.Fatal("failed protocol verification retained success", state)
	}
	c.Generation = 2
	if state, _ = a.agentStatus(d, &c); state != "待重载" {
		t.Fatal("stale protocol generation accepted", state)
	}
}
