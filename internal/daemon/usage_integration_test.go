package daemon

// Real SQLite service-contract checks; state changes are explicit fixtures.
import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestUsageAgentFlaggedGetStatusAndAdoptionContract(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	project := uuid.NewString()
	_, e = db.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','usage service fixture')`, project)
	check(e)
	c, e := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "protocol fixture", Project: project, AllowedScopes: []string{project}})
	check(e)
	v, e := db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "synthetic current body"})
	check(e)
	server := &Server{store: db}
	svc := &memory.Service{Store: db}
	for _, state := range []string{"active", "archived", "deleted"} {
		_, e = db.DB.Exec(`UPDATE memories SET state=? WHERE id=?`, state, v.Memory)
		check(e)
		req := records.AgentRequest{Protocol: 1, ConnectionID: c.ID, Generation: c.Generation, CallID: uuid.NewString(), Tool: "memory_get", Arguments: records.AgentArguments{Memory: v.Memory, Scope: project}}
		result, e := server.agent(ctx, req, svc)
		check(e)
		current := result.(records.Version)
		if current.State != state || current.Revision != 1 || current.UsageReceipt == nil {
			t.Fatal("get changed flags or missing observation", current)
		}
		req.Tool = "memory_status"
		req.CallID = uuid.NewString()
		status, e := server.agent(ctx, req, svc)
		check(e)
		if status.(records.Version).State != state {
			t.Fatal("status changed flags")
		}
		req.Tool = "memory_adopt"
		req.CallID = uuid.NewString()
		req.Arguments.OperationID = uuid.NewString()
		req.Arguments.Revision = 1
		req.Arguments.Exposure = current.UsageReceipt.ID
		_, e = server.agent(ctx, req, svc)
		if state == "active" {
			check(e)
		} else if e == nil {
			t.Fatal("flagged current adopted", state)
		}
	}
	u, e := db.Usage(ctx, v.Memory)
	check(e)
	if u.Daily.Reads != 3 || u.Daily.Adoptions != 1 || u.Daily.Returns != 0 {
		t.Fatal("status/flags polluted usage", u)
	}
}
