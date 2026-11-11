package store

import (
	"context"
	"errors"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func inspectionGit(t *testing.T, args ...string) {
	t.Helper()
	if out, e := exec.Command("/usr/bin/git", args...).CombinedOutput(); e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
}
func TestProjectInspectionReadOnlyWorktreeAndIdentityGate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	inspectionGit(t, "init", repo)
	inspectionGit(t, "-C", repo, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--allow-empty", "-m", "synthetic")
	s, e := Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	changes := func() int {
		var n int
		if e := s.DB.QueryRow("SELECT total_changes()").Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := changes()
	p, e := s.InspectProject(ctx, repo)
	if e != nil || p.State != "ready" || p.Identity == "" {
		t.Fatalf("%+v %v", p, e)
	}
	if changes() != before {
		t.Fatal("inspect wrote cache")
	}
	scope, e := s.RegisterProject(ctx, records.ScopeDraft{Name: "Synthetic", Path: repo, ExpectedRepository: p.Identity})
	if e != nil {
		t.Fatal(e)
	}
	wt := filepath.Join(dir, "worktree")
	inspectionGit(t, "-C", repo, "worktree", "add", "-b", "synthetic-worktree", wt)
	before = changes()
	w, e := s.InspectProject(ctx, wt)
	if e != nil || w.State != "existing" || w.Existing == nil || w.Existing.ID != scope.ID {
		t.Fatalf("%+v %v", w, e)
	}
	if changes() != before {
		t.Fatal("worktree probe wrote cache")
	}
	before = changes()
	_, e = s.RegisterProject(ctx, records.ScopeDraft{Name: "Wrong", Path: repo, ExpectedRepository: "stale"})
	var ae *records.AccessError
	if !errors.As(e, &ae) || ae.Code != "repository_changed" || changes() != before {
		t.Fatalf("stale check wrote or accepted: %v", e)
	}
	old := repo + "-old"
	if e = os.Rename(repo, old); e != nil {
		t.Fatal(e)
	}
	inspectionGit(t, "init", repo)
	_, e = s.RegisterProject(ctx, records.ScopeDraft{Name: "Replacement", Path: repo, ExpectedRepository: p.Identity})
	if !errors.As(e, &ae) || ae.Code != "repository_changed" {
		t.Fatalf("replaced repo accepted: %v", e)
	}
	plain := filepath.Join(dir, "plain")
	if e = os.Mkdir(plain, 0700); e != nil {
		t.Fatal(e)
	}
	for path, state := range map[string]string{"relative": "invalid_path", plain: "not_git", filepath.Join(dir, "missing"): "unavailable"} {
		v, e := s.InspectProject(ctx, path)
		if e != nil || v.State != state {
			t.Fatalf("%s: %+v %v", path, v, e)
		}
	}
}

func TestProjectInspectionRefusesFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	path := filepath.Join(dir, "fifo")
	if e = syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan records.ProjectInspection, 1)
	go func() { p, _ := s.InspectProject(context.Background(), path); done <- p }()
	select {
	case p := <-done:
		if p.State != "invalid_path" {
			t.Fatal(p.State)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO probe blocked")
	}
}

func TestProjectInspectionCancelledAndPermission(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.InspectProject(ctx, dir); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled inspect returned ready")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission fixture requires unprivileged user")
	}
	locked := filepath.Join(dir, "locked")
	if e = os.Mkdir(locked, 0000); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(locked, 0700)
	p, e := s.InspectProject(context.Background(), locked)
	if e != nil || p.State != "permission_denied" {
		t.Fatalf("%+v %v", p, e)
	}
}
