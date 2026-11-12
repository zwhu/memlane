package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestCodingMemoryGrantDefaultOffExplicitGenerationAndRevocation(t *testing.T) {
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	grant, err := s.CodingMemoryGrant(ctx, b)
	mustMerge(t, err)
	if grant.Enabled || grant.Generation != 0 || s.AuthorizeCodingMemoryGrant(ctx, grant) == nil {
		t.Fatal("connection or dataset binding enabled automatic processing")
	}
	q := records.CodingMemoryGrantChange{Operation: uuid.NewString(), Binding: b, User: uuid.NewString(), Enabled: true}
	grant, err = s.ChangeCodingMemoryGrant(ctx, q)
	mustMerge(t, err)
	mustMerge(t, s.AuthorizeCodingMemoryGrant(ctx, grant))
	if grant.Generation != 1 || !grant.Enabled {
		t.Fatal("explicit grant not applied", grant)
	}
	// Duplicate clicks do not create new authorization generations.
	replayed, err := s.ChangeCodingMemoryGrant(ctx, q)
	mustMerge(t, err)
	if replayed != grant {
		t.Fatal("same operation granted a second generation")
	}
	stale := q
	stale.Operation = uuid.NewString()
	if _, err = s.ChangeCodingMemoryGrant(ctx, stale); err == nil {
		t.Fatal("stale expected generation accepted")
	}
	stop := q
	stop.Operation, stop.ExpectedGeneration, stop.Enabled = uuid.NewString(), 1, false
	disabled, err := s.ChangeCodingMemoryGrant(ctx, stop)
	mustMerge(t, err)
	if disabled.Enabled || disabled.Generation != 2 || s.AuthorizeCodingMemoryGrant(ctx, grant) == nil {
		t.Fatal("old in-flight authorization survived disable")
	}
	_, err = s.ChangeCodingMemoryGrant(ctx, q)
	mustMerge(t, err)
	current, err := s.CodingMemoryGrant(ctx, b)
	mustMerge(t, err)
	if current != disabled {
		t.Fatal("lost enable response silently re-enabled processing")
	}
	changed := q
	changed.User = uuid.NewString()
	if _, err = s.ChangeCodingMemoryGrant(ctx, changed); err == nil {
		t.Fatal("operation id changed authenticated principal")
	}
}

func TestCodingMemoryGrantBindingAndDisconnectNeverRevivePermission(t *testing.T) {
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	q := records.CodingMemoryGrantChange{Operation: uuid.NewString(), Binding: b, User: uuid.NewString(), Enabled: true}
	for _, change := range []string{"endpoint", "binding_operation", "material", "workspace"} {
		bad := q
		bad.Operation = uuid.NewString()
		switch change {
		case "endpoint":
			bad.Binding.Endpoint = "http://127.0.0.1:9"
		case "binding_operation":
			bad.Binding.Operation = uuid.NewString()
		case "material":
			bad.Binding.MaterialSubmission = true
		case "workspace":
			bad.Binding.Workspace = t.TempDir()
		}
		if _, err := s.ChangeCodingMemoryGrant(ctx, bad); err == nil {
			t.Fatal("foreign binding accepted", change)
		}
	}
	grant, err := s.ChangeCodingMemoryGrant(ctx, q)
	mustMerge(t, err)
	mustMerge(t, s.Disconnect(ctx, b.ConnectionID))
	if s.AuthorizeCodingMemoryGrant(ctx, grant) == nil {
		t.Fatal("disconnect retained automatic permission")
	}
	_, err = s.ConfigureConnection(ctx, records.ConnectionDraft{ID: b.ConnectionID, Agent: "codex", Name: "reconnected", Project: b.Project, AllowedScopes: []string{b.Project}})
	mustMerge(t, err)
	if s.AuthorizeCodingMemoryGrant(ctx, grant) == nil {
		t.Fatal("reconnect revived the previous authorization generation")
	}
	// A failed service/credential or connection must not block turning it off.
	q.Operation, q.ExpectedGeneration, q.Enabled = uuid.NewString(), 1, false
	disabled, err := s.ChangeCodingMemoryGrant(ctx, q)
	mustMerge(t, err)
	if disabled.Enabled {
		t.Fatal("offline disable failed")
	}
}

func TestCodingMemoryGrantMigrationAndRestartPreserveNoContent(t *testing.T) {
	s, _, b, _, _ := graphContractFixture(t)
	ctx := context.Background()
	counts := map[string]int{}
	for _, table := range []string{"memories", "versions", "jobs", "session_processing_tasks"} {
		var n int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		counts[table] = n
	}
	q := records.CodingMemoryGrantChange{Operation: uuid.NewString(), Binding: b, User: uuid.NewString(), Enabled: true}
	grant, err := s.ChangeCodingMemoryGrant(ctx, q)
	mustMerge(t, err)
	for table, before := range counts {
		var n int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		if n != before {
			t.Fatal("grant created content or processing", table)
		}
	}
	// Reopen the same authoritative database; no second dataset/runtime root.
	var path string
	var seq int
	var name string
	rows, err := s.DB.Query(`PRAGMA database_list`)
	mustMerge(t, err)
	for rows.Next() {
		mustMerge(t, rows.Scan(&seq, &name, &path))
		if name == "main" {
			break
		}
	}
	mustMerge(t, rows.Close())
	mustMerge(t, s.Close())
	reopened, err := Open(path)
	mustMerge(t, err)
	defer reopened.Close()
	current, err := reopened.CodingMemoryGrant(ctx, b)
	mustMerge(t, err)
	if current != grant {
		t.Fatal("restart changed explicit permission")
	}
	// A real schema35 database has no new grant table. Its migration grants
	// nothing, and the pre-upgrade copy is kept by Store.Open.
	mustMerge(t, reopened.Close())
	legacy, err := Open(filepath.Join(t.TempDir(), "legacy.sqlite"))
	mustMerge(t, err)
	var legacyPath string
	mustMerge(t, legacy.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&legacyPath))
	_, err = legacy.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; PRAGMA user_version=35;`)
	mustMerge(t, err)
	mustMerge(t, legacy.Close())
	migrated, err := Open(legacyPath)
	mustMerge(t, err)
	defer migrated.Close()
	current, err = migrated.CodingMemoryGrant(ctx, b)
	mustMerge(t, err)
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(legacyPath), "backups/schema-v35-*.sqlite"))
	mustMerge(t, err)
	if migrated.Schema != SchemaVersion || current.Enabled || current.Generation != 0 || len(backups) != 1 {
		t.Fatal("migration invented automatic processing permission")
	}
}
