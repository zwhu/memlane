package agentbridge

import (
	"context"
	"encoding/json"
	"github.com/zwhu/memlane/internal/serviceipc"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func TestConnectionModeRejectsOtherToolsBeforeIPC(t *testing.T) {
	c := Config{Root: t.TempDir(), ConnectionID: "synthetic", Generation: 1, ToolMode: "connection", Channel: "verification"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"memory_get", "memory_search", "memory_save", "memory_status", "memory_adopt", "memory_initialize", "memory_code_search"} {
		r := c.Call(context.Background(), tool, records.AgentArguments{})
		if r.OK || r.Error == nil || r.Error.Code != "invalid_request" {
			t.Fatal("non-handshake tool reached IPC", tool, r)
		}
	}
}

func TestConnectionHandshakeOmitsOnlyPiMetadataWorkspace(t *testing.T) {
	root := t.TempDir()
	path := serviceipc.SocketPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Dir(path))
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(path)
	seen := make(chan serviceipc.Request, 2)
	go func() {
		for n := 0; n < 2; n++ {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			var q serviceipc.Request
			if json.NewDecoder(conn).Decode(&q) == nil {
				seen <- q
				_ = json.NewEncoder(conn).Encode(serviceipc.Response{Protocol: 1, ID: q.ID, OK: true, Result: map[string]any{"scopes": []any{}, "bound_project": "", "current_project": "", "requires_scope": true}})
			}
			conn.Close()
		}
	}()
	c := Config{Root: root, ConnectionID: "synthetic", Generation: 2, Workspace: t.TempDir(), ToolMode: "connection", Channel: "verification"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if r := c.Call(ctx, "memory_scopes", records.AgentArguments{}); !r.OK {
		t.Fatal(r)
	}
	q := <-seen
	if q.AgentRequest.Workspace != "" || q.AgentRequest.Generation != 2 || q.AgentRequest.Tool != "memory_scopes" || q.AgentRequest.ToolMode != "connection" || q.AgentRequest.Channel != "verification" {
		t.Fatal(q)
	}
	// Daily/default mode must retain the trusted workspace; clearing it globally
	// would silently change normal project authorization semantics.
	c.ToolMode = ""
	c.Channel = "daily"
	if r := c.Call(ctx, "memory_scopes", records.AgentArguments{}); !r.OK {
		t.Fatal(r)
	}
	q = <-seen
	if q.AgentRequest.Workspace != c.Workspace {
		t.Fatal("daily workspace stripped", q)
	}
	c.ToolMode = "connection"
	if r := c.Call(ctx, "memory_scopes", records.AgentArguments{}); r.OK || r.Error == nil || r.Error.Code != "invalid_request" {
		t.Fatal("daily connection handshake allowed", r)
	}
}
