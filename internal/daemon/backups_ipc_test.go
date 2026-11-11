package daemon_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Real private IPC and SQLite snapshot; no runtime engine or user data.
func TestBackupPrivateIPCCancelGateAndCommittedReceipt(t *testing.T) {
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	ctx := context.Background()
	id := uuid.NewString()
	if err := serviceipc.Call(ctx, root, serviceipc.Request{Op: "backup_cancel", BackupID: id}, nil); err == nil {
		t.Fatal("cancel bypassed confirmation gate")
	}
	client := serviceipc.BackupClient{Root: root}
	receipt, err := client.Create(ctx, id)
	requireService(t, err)
	result, err := client.Cancel(ctx, id)
	requireService(t, err)
	if result.State != "completed" || result.Receipt == nil || result.Receipt.SHA256 != receipt.SHA256 {
		t.Fatal("late cancellation concealed committed snapshot", result)
	}
	status, err := client.Status(ctx, id)
	requireService(t, err)
	if status.State != "completed" || status.Receipt == nil || status.Receipt.ID != id {
		t.Fatal(status)
	}
}
