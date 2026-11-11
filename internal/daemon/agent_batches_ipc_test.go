package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Real socket/SQLite lost-reply check. The unsupported adapter is deliberate;
// this test never treats it as a successful Agent install or model response.
func TestAgentBatchPrivateIPCLostStartReply(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	library := serviceipc.Library{Root: root}
	state, err := library.AgentConnectionBatch(ctx, "")
	requireService(t, err)
	d := records.AgentConnectionBatchStart{Identity: state.Identity, OperationID: uuid.NewString(), Targets: []records.AgentConnectionTarget{{Agent: "traex", Path: "/synthetic/traex", Directory: "/synthetic/config"}}}
	err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "agent_connection_batch_start", AgentConnectionBatchStart: &d}, nil)
	if e, ok := err.(*serviceipc.ServiceError); !ok || e.Code != "confirmation_required" {
		t.Fatal("missing explicit action gate", err)
	}
	conn, err := net.Dial("unix", serviceipc.SocketPath(root))
	requireService(t, err)
	requireService(t, json.NewEncoder(conn).Encode(serviceipc.Request{Protocol: 1, ID: uuid.NewString(), Op: "agent_connection_batch_start", AgentConnectionBatchStart: &d, UserConfirmed: true}))
	conn.Close()
	waitService(t, 5*time.Second, func() bool {
		state, err = library.AgentConnectionBatchByOperation(ctx, d.OperationID)
		return err == nil && state.Batch != nil && state.Batch.State == "partial_failed"
	})
	if state.Batch.Items[0].State != "unsupported" {
		t.Fatal("unsupported adapter reported as connected", state.Batch)
	}
	b, err := library.StartAgentConnectionBatch(ctx, d)
	requireService(t, err)
	if b.ID != state.Batch.ID || b.Items[0].Attempt != 1 {
		t.Fatal("lost reply replay created another attempt", b)
	}
	connections, err := library.Connections(ctx)
	requireService(t, err)
	if len(connections) != 0 {
		t.Fatal("unsupported adapter configured an identity", connections)
	}
}
