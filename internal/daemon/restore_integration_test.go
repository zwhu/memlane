package daemon

// Real SQLite/file restoration and actual trusted Agent dispatch. These are
// synthetic transport fixtures, not daily Agent model or native App proof.
import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func TestRestoreRetiredConnectionActualAgentDispatchAndFreshScope(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "memlane.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	a, b := uuid.NewString(), uuid.NewString()
	_, err = db.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','restore A'),(?,'project','restore B')`, a, b)
	check(err)
	old, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "traex", Name: "synthetic restore connector", Project: a, AllowedScopes: []string{a}})
	check(err)
	v, err := db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: a, Text: "synthetic restored current original"})
	check(err)
	backup, err := db.Backup(ctx, root, uuid.NewString())
	check(err)
	check(db.Close())
	check(serviceipc.MarkStopped(root))
	_, err = (recoveryfiles.RestoreManager{Root: root}).Restore(ctx, backup.ID, uuid.NewString(), store.PrepareRestoredSnapshot)
	check(err)
	restored, err := store.Open(path)
	check(err)
	defer restored.Close()
	s := &Server{store: restored}
	svc := &memory.Service{Store: restored}
	for _, generation := range []int64{old.Generation, old.Generation + 1} {
		for _, tool := range []string{"memory_scopes", "memory_get", "memory_save", "memory_initialize"} {
			r, e := s.agent(ctx, records.AgentRequest{Protocol: 1, ConnectionID: old.ID, Generation: generation, Tool: tool, Arguments: records.AgentArguments{Memory: v.Memory, OperationID: uuid.NewString(), Text: "late unauthorized write"}}, svc)
			var denied *records.AccessError
			if r != nil || !errors.As(e, &denied) || denied.Code != "restore_reconnect_required" {
				t.Fatal("retired bridge reached trusted dispatch", tool, generation, r, e)
			}
		}
	}
	fresh, err := restored.ConfigureConnection(ctx, records.ConnectionDraft{ID: old.ID, Agent: old.Agent, Name: old.Name, Project: a, AllowedScopes: []string{a}})
	check(err)
	if fresh.ID == old.ID || fresh.Generation != 1 {
		t.Fatal("fresh connector reused retired ID", fresh)
	}
	request := records.AgentRequest{Protocol: 1, ConnectionID: fresh.ID, Generation: fresh.Generation, Tool: "memory_get", Arguments: records.AgentArguments{Memory: v.Memory, Scope: a}}
	result, err := s.agent(ctx, request, svc)
	check(err)
	current := result.(records.Version)
	if current.Revision != 1 || current.Text != v.Text {
		t.Fatal("fresh authorized connector could not get restored version", current)
	}
	request.Arguments.Scope = b
	result, err = s.agent(ctx, request, svc)
	var denied *records.AccessError
	if result != nil || !errors.As(err, &denied) || denied.Code != "scope_denied" {
		t.Fatal("new connection expanded authorized scope", result, err)
	}
	var rows int
	check(restored.DB.QueryRow(`SELECT count(*) FROM versions`).Scan(&rows))
	if rows != 1 {
		t.Fatal("late retired call changed original history", rows)
	}
}
