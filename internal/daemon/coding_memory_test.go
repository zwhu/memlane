package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestCodingMemoryGrantRequiresConfirmationAndOfficialPrincipal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := filepath.Join(root, "repo")
	if err = exec.Command("git", "init", "-q", repo).Run(); err != nil {
		t.Fatal(err)
	}
	project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic coding memory", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "synthetic", Project: project.ID, AllowedScopes: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	instance, dataset, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/memlane/coding-memory/scope" || r.URL.Query().Get("dataset") != dataset || r.Header.Get("X-Api-Key") != "synthetic-scope-token" {
			t.Error("scope probe requested material or used wrong credential")
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"instance": instance, "dataset": dataset, "user": user, "managed": true})
	}))
	defer remote.Close()
	b := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, Endpoint: remote.URL}
	if _, err = db.BindCognee(ctx, b); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, config: Config{Root: root}}
	if err = s.writeCogneeToken(instance, dataset, "synthetic-scope-token"); err != nil {
		t.Fatal(err)
	}
	q := records.CodingMemoryGrantChange{Operation: uuid.NewString(), Binding: b, User: user, Enabled: true}
	if _, err = s.perform(ctx, Request{Op: "coding_memory_grant_change", CodingMemoryGrantChange: &q}); err == nil || calls != 0 {
		t.Fatal("unconfirmed request reached service or changed permission", err)
	}
	foreign := q
	foreign.User = uuid.NewString()
	if _, err = s.perform(ctx, Request{Op: "coding_memory_grant_change", CodingMemoryGrantChange: &foreign, UserConfirmed: true}); err == nil {
		t.Fatal("proposed principal overrode official authentication")
	}
	value, err := s.perform(ctx, Request{Op: "coding_memory_grant_change", CodingMemoryGrantChange: &q, UserConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	grant := value.(records.CodingMemoryGrant)
	if !grant.Enabled || grant.User != user || grant.Generation != 1 {
		t.Fatal("scope not applied", grant)
	}
	if err = db.Disconnect(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	remote.Close()
	q.Operation, q.ExpectedGeneration, q.Enabled = uuid.NewString(), 1, false
	value, err = s.perform(ctx, Request{Op: "coding_memory_grant_change", CodingMemoryGrantChange: &q, UserConfirmed: true})
	if err != nil || value.(records.CodingMemoryGrant).Enabled {
		t.Fatal("offline disable failed", err)
	}
}
