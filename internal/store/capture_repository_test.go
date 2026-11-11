package store

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func captureAssignmentOrigin(source *records.DeliveryAssignmentSource, repository records.CaptureRepository) {
	raw, _ := json.Marshal(map[string]any{"agent": "codex", "collection_consent": uuid.NewString(), "capture_repository": repository})
	source.Review.Arguments.Source = &records.AgentSource{Fragment: string(raw)}
	source.Review.Item.Status.RequestDigest = UsageDigest(source.Review.Arguments)
	source.Review.Item.Status.ReviewDigest = UsageDigest([]any{source.Review.Arguments, "retained"})
}

func TestCapturedRepositoryRegistrationAndReplacementGuard(t *testing.T) {
	s, source, r := assignmentFixture(t)
	ctx := context.Background()
	captured := agentbridge.CaptureRepository(ctx, source.Review.Item.Workspace)
	if captured.State != "known" {
		t.Fatal(captured)
	}
	inspection, err := s.InspectProject(ctx, source.Review.Item.Workspace)
	mustMerge(t, err)
	if captured.RegistrationIdentity() != inspection.Identity {
		t.Fatal("capture identity differs from project registration contract")
	}
	captureAssignmentOrigin(&source, captured)
	preview, err := s.DeliveryAssignmentPreview(ctx, source, r.Project)
	mustMerge(t, err)
	if !preview.CanConfirm {
		t.Fatal(preview)
	}
	before := organizationCount(t, s, `SELECT count(*) FROM versions`)
	root := captured.GitRoot
	if err = os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "previous-git")); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	preview, err = s.DeliveryAssignmentPreview(ctx, source, r.Project)
	mustMerge(t, err)
	if preview.CanConfirm || preview.Code != "capture_repository_changed" {
		t.Fatal("new repository at the same path accepted old originals", preview)
	}
	if organizationCount(t, s, `SELECT count(*) FROM versions`) != before || source.Review.Arguments.Text != "Generated raw retained content" {
		t.Fatal("identity check changed data")
	}
}

func TestUnknownCapturedRepositoryRemainsUnassigned(t *testing.T) {
	s, source, r := assignmentFixture(t)
	captureAssignmentOrigin(&source, records.CaptureRepository{State: "unknown", Workspace: source.Review.Item.Workspace})
	preview, err := s.DeliveryAssignmentPreview(context.Background(), source, r.Project)
	mustMerge(t, err)
	if preview.CanConfirm || preview.Code != "capture_repository_unverified" {
		t.Fatal("unknown capture silently acquired a project", preview)
	}
}

func TestCapturedWorktreeUsesExistingProject(t *testing.T) {
	s, source, r := assignmentFixture(t)
	root := source.Review.Item.Workspace
	if output, err := exec.Command("/usr/bin/git", "-C", root, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--allow-empty", "-qm", "synthetic fixture").CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	if output, err := exec.Command("/usr/bin/git", "-C", root, "worktree", "add", "-q", "-b", "synthetic-worktree", worktree).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(worktree, alias); err != nil {
		t.Fatal(err)
	}
	captured := agentbridge.CaptureRepository(context.Background(), alias)
	if captured.State != "known" {
		t.Fatal(captured)
	}
	source.Review.Item.Workspace = captured.Workspace
	var err error
	source.WorkspacePhysical, err = WorkspaceDirectoryIdentity(captured.Workspace)
	mustMerge(t, err)
	captureAssignmentOrigin(&source, captured)
	preview, err := s.DeliveryAssignmentPreview(context.Background(), source, r.Project)
	mustMerge(t, err)
	if !preview.CanConfirm || preview.MatchingProject == nil || preview.MatchingProject.ID != r.Project {
		t.Fatal("worktree did not reuse its common-dir project", preview)
	}
}
