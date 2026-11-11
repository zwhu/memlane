package daemon

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestD04DaemonRecomputesAuthorityAndOriginalReceipt(t *testing.T) {
	s := agentBatchServer(t)
	ctx := context.Background()
	work, _ := filepath.EvalSymlinks(t.TempDir())
	if err := exec.Command("git", "init", "-q", work).Run(); err != nil {
		t.Fatal(err)
	}
	p, err := s.store.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic delivery", Path: work})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic", AllowedScopes: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	r := records.AgentRequest{Protocol: 1, ConnectionID: c.ID, Generation: c.Generation, Workspace: work, Tool: "memory_save", Channel: "daily", Arguments: records.AgentArguments{OperationID: "synthetic-fixed-operation", Text: "synthetic original", Origin: "knowledge"}}
	_, err = s.store.DB.Exec(`DELETE FROM repositories`)
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.authorizeAgentDelivery(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	var repositories int
	if err = s.store.DB.QueryRow(`SELECT count(*) FROM repositories`).Scan(&repositories); err != nil || repositories != 0 {
		t.Fatal("authorize observation wrote repository cache", repositories, err)
	}
	if g.Scope != p.ID || g.WorkspaceDigest == "" {
		t.Fatal(g)
	}
	r.ExpectedLibraryID = g.LibraryID
	r.DeliveryGrant = &g
	bad := g
	bad.WorkspaceDigest = "client-self-asserted"
	r.DeliveryGrant = &bad
	if _, err = s.agent(ctx, r, nil); err == nil {
		t.Fatal("client digest trusted")
	}
	r.DeliveryGrant = &g
	value, err := s.agent(ctx, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	one := value.(records.SaveReceipt)
	if !one.Persisted || one.SavedRevision != 1 {
		t.Fatal(one)
	}
	_, err = s.store.Apply(ctx, records.Mutation{Kind: "edit", OperationID: "synthetic-edit", Memory: one.Record.Memory, Dataset: p.ID, ExpectedRevision: 1, Text: "synthetic later version", Source: "app", Origin: "knowledge"})
	if err != nil {
		t.Fatal(err)
	}
	value, err = s.agent(ctx, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if value.(records.SaveReceipt).SavedRevision != 1 || value.(records.SaveReceipt).Record.Revision != 2 {
		t.Fatal("replay lost saved/current version distinction", value)
	}
	if value.(records.SaveReceipt).Record.Memory != one.Record.Memory {
		t.Fatal(value)
	}
	var count int
	if err = s.store.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	_, err = s.store.DB.Exec(`UPDATE cognee_library_identity SET identity=? WHERE id=1`, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.agent(ctx, r, nil); err == nil {
		t.Fatal("old epoch replay accepted")
	}
	for _, ch := range []string{"verification", "background", "app", "unknown"} {
		r.Channel = ch
		if _, err = s.authorizeAgentDelivery(ctx, r); err == nil {
			t.Fatalf("grant issued for %s", ch)
		}
	}
	r.Channel = "daily"
	for _, mode := range []string{"connection", "initialization", "processing", "organization"} {
		r.ToolMode = mode
		if _, err = s.authorizeAgentDelivery(ctx, r); err == nil {
			t.Fatalf("grant issued for %s", mode)
		}
	}
}
