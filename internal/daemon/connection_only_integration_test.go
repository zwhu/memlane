package daemon_test

// Actual private IPC and SQLite, without installed Agent or model inference.
import (
	"context"
	"testing"

	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestConnectionOnlyPrivateIPC(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	draft := records.ConnectionDraft{Agent: "pi", Name: "电脑级协议测试", ConnectionOnly: true}
	err := serviceipc.Call(ctx, root, serviceipc.Request{Op: "configure_connection", Connection: draft}, nil)
	if failure, ok := err.(*serviceipc.ServiceError); !ok || failure.Code != "confirmation_required" {
		t.Fatal("host confirmation gate bypassed", err)
	}
	library := serviceipc.Library{Root: root}
	c, err := library.ConfigureConnection(ctx, draft)
	requireService(t, err)
	var reply struct {
		Scopes  []records.Scope `json:"scopes"`
		Bound   string          `json:"bound_project"`
		Current string          `json:"current_project"`
	}
	req := serviceipc.Request{Op: "agent", AgentRequest: records.AgentRequest{Protocol: 1, ConnectionID: c.ID, Generation: c.Generation, Tool: "memory_scopes"}}
	req.AgentRequest.ToolMode = "connection"
	requireService(t, serviceipc.Call(ctx, root, req, &reply))
	if len(reply.Scopes) != 0 || reply.Bound != "" || reply.Current != "" {
		t.Fatal("identity exposed implicit memory authorization", reply)
	}
	for _, tool := range []string{"memory_get", "memory_search", "memory_save", "memory_adopt", "memory_initialize", "memory_code_search"} {
		req.AgentRequest.Tool = tool
		err = serviceipc.Call(ctx, root, req, nil)
		if failure, ok := err.(*serviceipc.ServiceError); !ok || failure.Code != "invalid_request" {
			t.Fatal("protocol mode did not reject unavailable tool", tool, err)
		}
	}
	req.AgentRequest.ToolMode = ""
	req.AgentRequest.Tool = "memory_save"
	req.AgentRequest.Arguments = records.AgentArguments{OperationID: "must-not-save", Text: "must not persist"}
	if err = serviceipc.Call(ctx, root, req, nil); err == nil {
		t.Fatal("ungranted identity saved knowledge")
	}
	requireService(t, library.Disconnect(ctx, c.ID))
	req.AgentRequest.Tool = "memory_scopes"
	req.AgentRequest.Arguments = records.AgentArguments{}
	if err = serviceipc.Call(ctx, root, req, nil); err == nil {
		t.Fatal("revoked identity still connected")
	}
}
