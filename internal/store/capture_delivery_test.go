package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func capturedDeliveryFixture(t *testing.T) (*Store, records.DeliveryAssignmentSource, records.CaptureRoute) {
	t.Helper()
	s, source, intent := assignmentFixture(t)
	c, err := s.ConfigureConnection(context.Background(), records.ConnectionDraft{Agent: "codex", Name: "Synthetic capture", ConnectionOnly: true})
	mustMerge(t, err)
	source.Review.Item.ConnectionID = c.ID
	source.Review.Item.Generation = c.Generation
	source.Review.Item.Status.ID = UsageDigest([]string{c.ID, source.Review.Arguments.OperationID})
	captureAssignmentOrigin(&source, agentbridge.CaptureRepository(context.Background(), source.Review.Item.Workspace))
	origin, _ := records.ReadCaptureOrigin(source.Review.Arguments)
	return s, source, records.CaptureRoute{OperationID: uuid.NewString(), LibraryID: intent.LibraryID, ConnectionID: c.ID, ConsentID: origin.Consent, Repository: origin.Repository, Project: intent.Project}
}

func TestCapturedDeliveryIdempotentRawWithoutKnowledgeGrant(t *testing.T) {
	s, source, route := capturedDeliveryFixture(t)
	ctx := context.Background()
	before := organizationCount(t, s, `SELECT count(*) FROM versions`)
	saved, err := s.SaveCapturedRaw(ctx, source, route)
	mustMerge(t, err)
	replay, err := s.SaveCapturedRaw(ctx, source, route)
	mustMerge(t, err)
	if saved != replay || !saved.Persisted || saved.Revision != 1 || organizationCount(t, s, `SELECT count(*) FROM versions`) != before+1 {
		t.Fatal(saved, replay)
	}
	connection, err := s.Connection(ctx, route.ConnectionID)
	mustMerge(t, err)
	if len(connection.AllowedScopes) != 0 {
		t.Fatal("capture granted knowledge scopes", connection)
	}
	var text, origin string
	mustMerge(t, s.DB.QueryRowContext(ctx, `SELECT v.text,md.origin FROM versions v JOIN memory_metadata md ON md.memory=v.memory WHERE v.memory=? AND v.revision=1`, saved.Memory).Scan(&text, &origin))
	if text != source.Review.Arguments.Text || origin != "raw" {
		t.Fatal("original altered", text, origin)
	}
	changed := source
	changed.Review.Arguments.Text = "different original"
	if _, err = s.SaveCapturedRaw(ctx, changed, route); err == nil {
		t.Fatal("same operation accepted different original")
	}
}

func TestCapturedDeliveryRejectsInvalidAuthorityAndRepository(t *testing.T) {
	for _, kind := range []string{"library", "consent", "connection", "repository"} {
		t.Run(kind, func(t *testing.T) {
			s, source, route := capturedDeliveryFixture(t)
			switch kind {
			case "library":
				route.LibraryID = uuid.NewString()
			case "consent":
				route.ConsentID = uuid.NewString()
			case "connection":
				_, err := s.DB.Exec(`UPDATE connections SET enabled=0 WHERE id=?`, route.ConnectionID)
				mustMerge(t, err)
			case "repository":
				root := source.Review.Item.Workspace
				mustMerge(t, os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "previous-git")))
				if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
			}
			before := organizationCount(t, s, `SELECT count(*) FROM versions`)
			if _, err := s.SaveCapturedRaw(context.Background(), source, route); err == nil {
				t.Fatal("invalid capture authority accepted", kind)
			}
			if organizationCount(t, s, `SELECT count(*) FROM versions`) != before {
				t.Fatal("rejected capture wrote data")
			}
		})
	}
}

func TestCapturedDeliveryAndManualAssignmentExcludeEachOther(t *testing.T) {
	for _, automaticFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "automatic-first", false: "manual-first"}[automaticFirst], func(t *testing.T) {
			s, source, route := capturedDeliveryFixture(t)
			ctx := context.Background()
			preview, err := s.DeliveryAssignmentPreview(ctx, source, route.Project)
			mustMerge(t, err)
			intent := records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: preview.LibraryID, EntryID: preview.EntryID, RequestDigest: preview.RequestDigest, ReviewDigest: preview.ReviewDigest, Project: route.Project, WorkspaceDigest: preview.WorkspaceDigest}
			before := organizationCount(t, s, `SELECT count(*) FROM versions`)
			if automaticFirst {
				_, err = s.SaveCapturedRaw(ctx, source, route)
				mustMerge(t, err)
				if _, err = s.ConfirmDeliveryAssignment(ctx, source, intent); err == nil {
					t.Fatal("manual assignment duplicated automatic original")
				}
			} else {
				_, err = s.ConfirmDeliveryAssignment(ctx, source, intent)
				mustMerge(t, err)
				if _, err = s.SaveCapturedRaw(ctx, source, route); err == nil {
					t.Fatal("automatic delivery duplicated manual assignment")
				}
			}
			if organizationCount(t, s, `SELECT count(*) FROM versions`) != before+1 {
				t.Fatal("duplicate original version")
			}
		})
	}
}
