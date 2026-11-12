package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCogneeReadReauthorizationRequiresFreshExplicitReview(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(filepath.Join(root, "test.sqlite"))
	mustMerge(t, err)
	defer s.Close()
	repo := filepath.Join(root, "repo")
	mustMerge(t, exec.Command("git", "init", "-q", repo).Run())
	project, err := s.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic read reauthorization", Path: repo})
	mustMerge(t, err)
	draft := records.ConnectionDraft{Agent: "codex", Name: "synthetic", Project: project.ID, AllowedScopes: []string{project.ID}}
	connection, err := s.ConfigureConnection(ctx, draft)
	mustMerge(t, err)
	old := records.CogneeBinding{Operation: uuid.NewString(), Instance: uuid.NewString(), Dataset: uuid.NewString(), Project: project.ID, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, Endpoint: "http://127.0.0.1:18099"}
	_, err = s.BindCognee(ctx, old)
	mustMerge(t, err)
	mustMerge(t, s.Disconnect(ctx, connection.ID))
	draft.ID = connection.ID
	fresh, err := s.ConfigureConnection(ctx, draft)
	mustMerge(t, err)
	if err = s.AuthorizeCogneeBinding(ctx, old); err == nil {
		t.Fatal("old grant revived")
	}
	next := old
	next.Operation = uuid.NewString()
	next.Generation = fresh.Generation
	if _, err = s.BindCognee(ctx, next); err == nil {
		t.Fatal("ordinary bind silently reauthorized")
	}
	q := records.CogneeReadReauthorization{Previous: old, Operation: next.Operation, Generation: next.Generation}
	for _, change := range []string{"previous_operation", "endpoint", "dataset", "project", "connection", "workspace", "submission", "old_generation"} {
		bad := q
		switch change {
		case "previous_operation":
			bad.Previous.Operation = uuid.NewString()
		case "endpoint":
			bad.Previous.Endpoint = "http://127.0.0.1:18098"
		case "dataset":
			bad.Previous.Dataset = uuid.NewString()
		case "project":
			bad.Previous.Project = uuid.NewString()
		case "connection":
			bad.Previous.ConnectionID = uuid.NewString()
		case "workspace":
			bad.Previous.Workspace = root
		case "submission":
			bad.Previous.MaterialSubmission = true
		case "old_generation":
			bad.Generation = old.Generation
		}
		if _, err = s.ReauthorizeCogneeRead(ctx, bad); err == nil {
			t.Fatal("changed review accepted", change)
		}
	}
	out, err := s.ReauthorizeCogneeRead(ctx, q)
	mustMerge(t, err)
	if out.Generation != fresh.Generation || out.ReadPreviousOperation != old.Operation || out.ReadReviewDigest != records.MergeDigest(q) {
		t.Fatal("missing exact review receipt", out)
	}
	replay, err := s.ReauthorizeCogneeRead(ctx, q)
	mustMerge(t, err)
	if replay != out {
		t.Fatal("replay changed receipt")
	}
	bad := q
	bad.Previous.ReadReviewDigest = "altered"
	if _, err = s.ReauthorizeCogneeRead(ctx, bad); err == nil {
		t.Fatal("operation reused with changed review")
	}
	rows, err := s.CogneeBindings(ctx, project.ID)
	mustMerge(t, err)
	if len(rows) != 1 || rows[0] != out {
		t.Fatal("binding duplicated or not replaced", rows)
	}
	mustMerge(t, s.Disconnect(ctx, connection.ID))
	if _, err = s.ReauthorizeCogneeRead(ctx, q); err == nil {
		t.Fatal("receipt replay revived revoked connection")
	}
	for _, table := range []string{"memories", "versions", "operations", "jobs"} {
		var n int
		mustMerge(t, s.DB.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Fatal("reauthorization copied material", table, n)
		}
	}
}
