package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func piCaptureConsentFixture(t *testing.T) (*Store, string, records.PiCaptureConsentReview, records.AgentRequest, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "memlane.sqlite")
	s, err := Open(path)
	mustMerge(t, err)
	t.Cleanup(func() { s.Close() })
	repo := filepath.Join(root, "synthetic-pi-repo")
	if output, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	project, err := s.RegisterProject(context.Background(), records.ScopeDraft{Name: "synthetic pi capture", Path: repo})
	mustMerge(t, err)
	c, err := s.ConfigureConnection(context.Background(), records.ConnectionDraft{Agent: "pi", Name: "synthetic pi", Project: project.ID, AllowedScopes: []string{project.ID}})
	mustMerge(t, err)
	b, err := s.BindCognee(context.Background(), records.CogneeBinding{Operation: uuid.NewString(), Instance: uuid.NewString(), Dataset: uuid.NewString(), Project: project.ID, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Endpoint: "http://127.0.0.1:49123", MaterialSubmission: true})
	mustMerge(t, err)
	q := records.PiCaptureConsentReview{Operation: uuid.NewString(), Binding: b}
	r := records.AgentRequest{Protocol: 1, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Tool: "memory_save", Channel: "daily", Arguments: records.AgentArguments{Scope: project.ID}}
	return s, path, q, r, strings.Repeat("a", 64)
}

func TestPiCaptureConsentExplicitReviewDoesNotGrantHistory(t *testing.T) {
	s, _, q, r, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	if _, err := s.AuthorizePiCapture(ctx, r, policy); err == nil {
		t.Fatal("connection alone granted capture")
	}
	grant, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	current, err := s.AuthorizePiCapture(ctx, r, policy)
	mustMerge(t, err)
	if current != grant || !current.Enabled || !officialCaptureHash.MatchString(current.Key) {
		t.Fatal("capture grant changed", current, grant)
	}
	fromWorkspace := r
	fromWorkspace.Arguments.Scope = ""
	resolved, err := s.AuthorizePiCapture(ctx, fromWorkspace, policy)
	mustMerge(t, err)
	if resolved.Key != grant.Key || resolved.Binding.Project != r.Arguments.Scope {
		t.Fatal("machine-level host did not resolve only its approved current project", resolved)
	}
	alias := filepath.Join(filepath.Dir(r.Workspace), "same-directory-alias")
	mustMerge(t, os.Symlink(r.Workspace, alias))
	aliased := r
	aliased.Workspace = alias
	if accepted, err := s.AuthorizePiCapture(ctx, aliased, policy); err != nil || accepted.Key != grant.Key {
		t.Fatal("same physical directory alias rejected", accepted, err)
	}
	if err := s.AuthorizeCogneeBinding(ctx, q.Binding); err == nil {
		t.Fatal("new-message consent became historical read grant")
	}
	replay, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	if replay != grant {
		t.Fatal("review not idempotent")
	}
	for _, change := range []string{"processing", "connection_check", "missing_channel", "workspace", "generation", "scope", "policy"} {
		request, selectedPolicy := r, policy
		switch change {
		case "processing":
			request.Channel = "processing"
		case "connection_check":
			request.ToolMode = "connection"
		case "missing_channel":
			request.Channel = ""
		case "workspace":
			request.Workspace = filepath.Dir(r.Workspace)
		case "generation":
			request.Generation++
		case "scope":
			request.Arguments.Scope = uuid.NewString()
		case "policy":
			selectedPolicy = strings.Repeat("b", 64)
		}
		if _, err := s.AuthorizePiCapture(ctx, request, selectedPolicy); err == nil {
			t.Fatal("wrong intent authorized", change)
		}
	}
	for _, table := range []string{"memories", "versions", "operations", "jobs", "official_capture_receipts"} {
		var count int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		if count != 0 {
			t.Fatal("consent touched content", table, count)
		}
	}
}

func TestPiCaptureConsentDisableReplayAndExplicitReplacement(t *testing.T) {
	s, _, q, r, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	grant, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	if err := s.DisablePiCaptureConsent(ctx, records.PiCaptureConsentDisable{Connection: r.ConnectionID, Project: r.Arguments.Scope, ExpectedKey: strings.Repeat("b", 64)}); err == nil {
		t.Fatal("stale disable accepted")
	}
	disable := records.PiCaptureConsentDisable{Connection: r.ConnectionID, Project: r.Arguments.Scope, ExpectedKey: grant.Key}
	mustMerge(t, s.DisablePiCaptureConsent(ctx, disable))
	mustMerge(t, s.DisablePiCaptureConsent(ctx, disable))
	if _, err := s.AuthorizePiCapture(ctx, r, policy); err == nil {
		t.Fatal("disabled capture authorized")
	}
	replay, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	if replay.Enabled {
		t.Fatal("review replay revived capture")
	}
	next := q
	next.Operation = uuid.NewString()
	if _, err := s.ReviewPiCaptureConsent(ctx, next, policy); err == nil {
		t.Fatal("replacement without CAS accepted")
	}
	next.PreviousKey = grant.Key
	replacement, err := s.ReviewPiCaptureConsent(ctx, next, policy)
	mustMerge(t, err)
	if replacement.Key == grant.Key {
		t.Fatal("replacement reused old capture identity")
	}
	mustMerge(t, s.DisablePiCaptureConsent(ctx, disable))
	current, err := s.AuthorizePiCapture(ctx, r, policy)
	mustMerge(t, err)
	if current.Key != replacement.Key {
		t.Fatal("old disable changed replacement")
	}
	changed := next
	changed.PreviousKey = strings.Repeat("b", 64)
	if _, err := s.ReviewPiCaptureConsent(ctx, changed, policy); err == nil {
		t.Fatal("review operation changed its prior observation")
	}
}

func TestPiCaptureConsentReopenAndRevocation(t *testing.T) {
	s, path, q, r, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	grant, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	opened, err := Open(path)
	mustMerge(t, err)
	defer opened.Close()
	current, err := opened.AuthorizePiCapture(ctx, r, policy)
	mustMerge(t, err)
	if current != grant {
		t.Fatal("restart changed consent")
	}
	mustMerge(t, opened.Disconnect(ctx, r.ConnectionID))
	if _, err := opened.AuthorizePiCapture(ctx, r, policy); err == nil {
		t.Fatal("disconnected pi could capture")
	}
}

func TestPiCaptureConsentRejectsChangedPhysicalWorkspaceOrLibrary(t *testing.T) {
	for _, change := range []string{"physical", "library", "binding", "agent"} {
		t.Run(change, func(t *testing.T) {
			s, _, q, r, policy := piCaptureConsentFixture(t)
			ctx := context.Background()
			_, err := s.ReviewPiCaptureConsent(ctx, q, policy)
			mustMerge(t, err)
			switch change {
			case "physical":
				mustMerge(t, os.Rename(r.Workspace, r.Workspace+"-retained"))
				if output, err := exec.Command("git", "init", r.Workspace).CombinedOutput(); err != nil {
					t.Fatal(err, string(output))
				}
			case "library":
				_, err = s.DB.Exec(`UPDATE cognee_library_identity SET identity=? WHERE id=1`, uuid.NewString())
				mustMerge(t, err)
			case "binding":
				_, err = s.DB.Exec(`UPDATE cognee_bindings SET payload=json_set(payload,'$.endpoint','http://127.0.0.1:49124') WHERE instance=? AND dataset=?`, q.Binding.Instance, q.Binding.Dataset)
				mustMerge(t, err)
			case "agent":
				_, err = s.DB.Exec(`UPDATE connections SET agent='codex' WHERE id=?`, r.ConnectionID)
				mustMerge(t, err)
			}
			if _, err := s.AuthorizePiCapture(ctx, r, policy); err == nil {
				t.Fatal("changed authority accepted", change)
			}
		})
	}
}

func TestPiCaptureConsentBackupRestoreDisablesWithoutDeletingEvidence(t *testing.T) {
	s, _, q, r, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	grant, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	_, snapshot, err := s.snapshot(ctx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "memlane.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	var enabled bool
	var key string
	mustMerge(t, restored.DB.QueryRow(`SELECT enabled,key FROM pi_capture_consents WHERE operation=?`, q.Operation).Scan(&enabled, &key))
	if enabled || key != grant.Key {
		t.Fatal("restore revived or erased consent evidence")
	}
	if _, err := restored.AuthorizePiCapture(ctx, r, policy); err == nil {
		t.Fatal("restored old consent authorized")
	}
}

func TestPiCaptureConsentSchema33UpgradePreservesUnknownCaptureIntent(t *testing.T) {
	s, path, review, _, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	library, err := s.CogneeLibraryIdentity(ctx)
	mustMerge(t, err)
	identity := records.OfficialCaptureIdentity{Operation: strings.Repeat("1", 64), Library: library, GrantDigest: strings.Repeat("2", 64), Instance: review.Binding.Instance, Dataset: review.Binding.Dataset, Session: "pi-" + strings.Repeat("3", 48), Kind: "qa", PayloadDigest: strings.Repeat("4", 64)}
	_, claimed, err := s.ClaimOfficialCapture(ctx, identity, false)
	mustMerge(t, err)
	if !claimed {
		t.Fatal("initial dispatch not claimed")
	}
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; PRAGMA user_version=33`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	upgraded, err := Open(path)
	mustMerge(t, err)
	defer upgraded.Close()
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "schema-v33-*.sqlite"))
	mustMerge(t, err)
	if upgraded.Schema != SchemaVersion || len(backups) != 1 {
		t.Fatal("schema34 upgrade or backup missing", upgraded.Schema, backups)
	}
	row, claimed, err := upgraded.ClaimOfficialCapture(ctx, identity, false)
	mustMerge(t, err)
	if claimed || row.State != "dispatching" {
		t.Fatal("migration replayed uncertain write", row, claimed)
	}
	grant, err := upgraded.ReviewPiCaptureConsent(ctx, review, policy)
	mustMerge(t, err)
	if !grant.Enabled {
		t.Fatal("explicit review unavailable after migration")
	}
}
