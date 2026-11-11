package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestAgentBatchVerificationFreezesAuthorizedWorkspaceAndReplay(t *testing.T) {
	s := agentBatchServer(t)
	ctx := context.Background()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	paths := []string{filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "foreign")}
	scopes := []records.Scope{}
	for index, path := range paths {
		if err := exec.Command("/usr/bin/git", "init", "-q", path).Run(); err != nil {
			t.Fatal(err)
		}
		scope, err := s.store.RegisterProject(ctx, records.ScopeDraft{Name: []string{"A", "B", "Foreign"}[index], Path: path})
		if err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, scope)
	}
	c, err := s.store.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "Generated scoped connection", AllowedScopes: []string{scopes[0].ID, scopes[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.store.CreateAgentConnectionBatch(ctx, records.AgentConnectionBatchStart{Identity: batchIdentity(s), OperationID: uuid.NewString(), Targets: []records.AgentConnectionTarget{{Agent: "codex", Path: "/generated/codex", Directory: root}}})
	if err != nil {
		t.Fatal(err)
	}
	b.State = "waiting"
	b.Items[0].ConnectionID = c.ID
	b.Items[0].Generation = c.Generation
	b.Items[0].State = "awaiting_reload"
	b, err = s.store.SaveAgentConnectionBatch(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	s.agentBatchVerify = func(ctx context.Context, item records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
		if item.VerificationWorkspace != paths[0] || item.VerificationProject != scopes[0].ID || item.VerificationWorkspaceDigest == "" {
			t.Error("owner did not freeze registered authorized workspace", item)
		}
		item.State = "connected"
		return item, nil
	}
	action := records.AgentConnectionBatchAction{Identity: b.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "verify", Workspace: paths[2]}
	if _, err = s.actionConnectionBatch(ctx, action); err == nil {
		t.Fatal("ungranted registered workspace accepted")
	}
	action.Workspace = ""
	if _, err = s.actionConnectionBatch(ctx, action); err == nil {
		t.Fatal("project grants accepted without explicit workspace")
	}
	action.Workspace = paths[0]
	if _, err = s.actionConnectionBatch(ctx, action); err != nil {
		t.Fatal(err)
	}
	b = awaitAgentBatch(t, s, b.ID, func(b records.AgentConnectionBatch) bool { return b.Items[0].State == "connected" })
	if b.ActionDigests[action.OperationID] == "" || b.Actions[action.OperationID] != "verify" {
		t.Fatal("action digest missing or legacy UI kind broken")
	}
	if _, err = s.actionConnectionBatch(ctx, action); err != nil {
		t.Fatal("same action replay rejected", err)
	}
	action.Workspace = paths[1]
	if _, err = s.actionConnectionBatch(ctx, action); err == nil {
		t.Fatal("same operation changed verification scope")
	}
}
