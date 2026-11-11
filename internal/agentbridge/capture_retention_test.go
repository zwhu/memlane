package agentbridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestCaptureRetainsUnknownWorkspaceWithoutServiceOrProject(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	c := Config{Root: root, ConnectionID: uuid.NewString(), Generation: 1, Workspace: workspace}
	a := records.AgentArguments{OperationID: "synthetic-capture-message", Title: "Codex 测试原文", Text: "用户：限定测试材料", Origin: "raw", Source: &records.AgentSource{Fragment: "synthetic source metadata"}}
	one, err := RetainCapturedRaw(c, a)
	if err != nil || !one.LocalRetained || one.PersistedConfirmed || one.State != "pending_unverified" {
		t.Fatal(one, err)
	}
	// No daemon, project registration, Git initialization, model or IPC exists.
	page, err := ListDeliveries(root)
	if err != nil || len(page.Items) != 1 || page.Items[0].Origin != "raw" {
		t.Fatal(page, err)
	}
	review, err := ReviewDelivery(root, one.ID)
	if err != nil || review.Arguments.Text != a.Text {
		t.Fatal(review, err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	c.Workspace = alias
	two, err := RetainCapturedRaw(c, a)
	if err != nil || two.ID != one.ID {
		t.Fatal("alias replay changed identity", two, err)
	}
	c.Generation = 2
	if replay, err := RetainCapturedRaw(c, a); err != nil || replay.ID != one.ID {
		t.Fatal("independent tool grant generation duplicated capture", replay, err)
	}
	a.Text = "conflicting replay"
	if _, err = RetainCapturedRaw(c, a); err == nil {
		t.Fatal("conflicting replay overwrote original")
	}
	review, err = ReviewDelivery(root, one.ID)
	if err != nil || review.Arguments.Text != "用户：限定测试材料" {
		t.Fatal("original changed", review, err)
	}
}
