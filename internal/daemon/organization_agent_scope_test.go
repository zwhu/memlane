package daemon

// A synthetic Store and authenticated request exercise the actual Agent
// dispatch, including operation replay after a user-approved cross-scope move.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestOrganizationMoveAgentSaveReplayKeepsCurrentDataAndProvenance(t *testing.T) {
	ctx := context.Background()
	db, e := store.Open(filepath.Join(t.TempDir(), "scope-fixture.sqlite"))
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
	a, b := uuid.NewString(), uuid.NewString()
	_, e = db.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','A','/synthetic/a'),(?,'project','B','/synthetic/b')`, a, b)
	check(e)
	connection, e := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "group fixture", Project: a})
	check(e)
	request := records.AgentRequest{Protocol: 1, ConnectionID: connection.ID, Generation: connection.Generation, Tool: "memory_save", Arguments: records.AgentArguments{Scope: a, OperationID: "stable-save", Text: "A original body", Source: &records.AgentSource{Fragment: "private A original fragment"}}}
	s := &Server{store: db}
	svc := &memory.Service{Store: db}
	result, e := s.agent(ctx, request, svc)
	check(e)
	saved := result.(records.SaveReceipt)
	start, _, e := db.StartOrganization(ctx, records.OrganizationStart{OperationID: uuid.NewString(), Project: a, Review: true, Destinations: []string{b}, Inputs: []records.MergeTarget{{Memory: saved.Record.Memory, Revision: 1}}})
	check(e)
	item := records.OrganizationItem{Index: 1, Action: "move", Reason: "synthetic user-approved scope move", Target: saved.Record.Memory, Destination: b, Tags: []string{}, Sources: []records.MergeSource{{Memory: saved.Record.Memory, Revision: 1, Quote: saved.Record.Text}}}
	r := records.OrganizationResult{Schema: start.Snapshot.Schema, Task: start.ID, Attempt: 1, Digest: start.Snapshot.Digest, ID: start.Snapshot.ProposalID(), Items: []records.OrganizationItem{item}, Explanation: "protocol fixture"}
	raw, _ := json.Marshal(r)
	check(db.ClaimOrganization(ctx, start.ID, 1))
	_, e = db.StageOrganization(ctx, start.ID, 1, raw, nil, "")
	check(e)
	_, e = db.FinishOrganization(ctx, start.ID, 1)
	check(e)
	_, e = db.ReviewOrganization(ctx, records.OrganizationReview{Task: start.ID, Item: start.Snapshot.ItemID(1), Digest: records.MergeDigest(item), Receipt: uuid.NewString(), Confirm: true, MoveScopeAccepted: true})
	check(e)
	_, e = db.Apply(ctx, records.Mutation{Kind: "edit", OperationID: uuid.NewString(), Dataset: b, Memory: saved.Record.Memory, ExpectedRevision: 2, Text: "B private current body"})
	check(e)
	replay, e := s.agent(ctx, request, svc)
	check(e)
	receipt := replay.(records.SaveReceipt)
	if !receipt.Persisted || receipt.SavedRevision != 1 || receipt.Record.Dataset != b || receipt.Record.Revision != 3 {
		t.Fatal("save replay lost original receipt or current version", replay)
	}
	v, e := db.Current(ctx, saved.Record.Memory)
	check(e)
	if v.Revision != 3 {
		t.Fatal("replay changed knowledge")
	}
	connectionB, e := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "B fixture", Project: b})
	check(e)
	get := records.AgentRequest{Protocol: 1, ConnectionID: connectionB.ID, Generation: connectionB.Generation, Tool: "memory_get", Arguments: records.AgentArguments{Memory: v.Memory, Scope: b}}
	public, e := s.agent(ctx, get, svc)
	check(e)
	filtered := public.(records.Version)
	if filtered.Text != v.Text || filtered.Original == "" || filtered.SourceDetail == nil {
		t.Fatal("get lost original provenance", filtered)
	}
	get.ConnectionID, get.Generation = connection.ID, connection.Generation
	get.Arguments.Scope = ""
	if _, e = s.agent(ctx, get, svc); e != nil {
		t.Fatal("current project was treated as a read permission", e)
	}
}
