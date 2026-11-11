package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// Source structs below are explicit trusted-loader fixtures. Unix IPC tests
// separately exercise the actual private spool rather than accepting RPC text.
func assignmentFixture(t *testing.T) (*Store, records.DeliveryAssignmentSource, records.DeliveryAssignmentIntent) {
	t.Helper()
	s, _ := mergeDB(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if out, e := exec.Command("git", "init", "-q", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	p, e := s.RegisterProject(mergeCtx, records.ScopeDraft{Name: "Generated assignment", Path: repo})
	mustMerge(t, e)
	c, e := s.ConfigureConnection(mergeCtx, records.ConnectionDraft{Agent: "pi", Name: "Unassigned source", ConnectionOnly: true})
	mustMerge(t, e)
	a := records.AgentArguments{OperationID: uuid.NewString(), Title: "Generated retained original", Text: "Generated raw retained content", Origin: "raw", Source: &records.AgentSource{Fragment: "Original retained fragment", References: []string{"synthetic source"}}}
	physical, e := WorkspaceDirectoryIdentity(p.Path)
	mustMerge(t, e)
	source := records.DeliveryAssignmentSource{WorkspacePhysical: physical, Review: records.AgentDeliveryReview{Arguments: a, Item: records.AgentDeliveryItem{OperationID: a.OperationID, Origin: a.Origin, ConnectionID: c.ID, Generation: c.Generation, Workspace: p.Path, Status: records.AgentDeliveryStatus{ID: UsageDigest([]string{c.ID, a.OperationID}), RequestDigest: UsageDigest([]any{c.ID, a}), ReviewDigest: UsageDigest([]any{c.ID, a, "retained"}), State: "pending_unverified", LocalRetained: true}}}}
	preview, e := s.DeliveryAssignmentPreview(mergeCtx, source, p.ID)
	mustMerge(t, e)
	if !preview.CanConfirm {
		t.Fatal(preview)
	}
	r := records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: preview.LibraryID, EntryID: preview.EntryID, RequestDigest: preview.RequestDigest, ReviewDigest: preview.ReviewDigest, Project: p.ID, WorkspaceDigest: preview.WorkspaceDigest}
	return s, source, r
}

func TestE05AssignmentReadOnlyMismatchAndIndependentSave(t *testing.T) {
	s, source, r := assignmentFixture(t)
	before := map[string][][]any{}
	for _, name := range []string{"memories", "versions", "connections", "repositories", "operations"} {
		before[name] = sqlRows(t, s.DB, name)
	}
	preview, e := s.DeliveryAssignmentPreview(mergeCtx, source, "")
	mustMerge(t, e)
	if preview.CanConfirm || preview.Code != "choose_project" || preview.MatchingProject == nil || preview.WorkspaceDigest == "" {
		t.Fatal(preview)
	}
	preview, e = s.DeliveryAssignmentPreview(mergeCtx, source, uuid.NewString())
	mustMerge(t, e)
	if preview.CanConfirm || preview.Code != "workspace_mismatch" || preview.MatchingProject.ID != r.Project {
		t.Fatal(preview)
	}
	for name, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, s.DB, name)) {
			t.Fatal("preview wrote data", name)
		}
	}
	saved, e := s.ConfirmDeliveryAssignment(mergeCtx, source, r)
	mustMerge(t, e)
	if saved.Source != "user_assignment" || saved.State != "saved" || saved.OriginalOperationID != source.Review.Arguments.OperationID || saved.OriginalConnectionID != source.Review.Item.ConnectionID {
		t.Fatal(saved)
	}
	v, e := s.Current(mergeCtx, saved.Memory)
	mustMerge(t, e)
	a := source.Review.Arguments
	if v.Origin != "raw" || v.Text != a.Text || v.Title != a.Title || v.Dataset != r.Project || v.Source != "用户确认项目归属" || !reflect.DeepEqual(v.SourceDetail, a.Source) {
		t.Fatal("assignment changed original material/provenance", v)
	}
	if !reflect.DeepEqual(before["connections"], sqlRows(t, s.DB, "connections")) {
		t.Fatal("assignment granted Agent scope")
	}
	if organizationCount(t, s, `SELECT count(*) FROM operations WHERE id='`+assignmentOriginalKey(source)+`'`) != 0 {
		t.Fatal("assignment forged original Agent success")
	}
	same, e := s.ConfirmDeliveryAssignment(mergeCtx, source, r)
	mustMerge(t, e)
	if !reflect.DeepEqual(saved, same) || organizationCount(t, s, `SELECT count(*) FROM memories`) != 1 {
		t.Fatal("replay duplicated save", same)
	}
	preview, e = s.DeliveryAssignmentPreview(mergeCtx, source, r.Project)
	mustMerge(t, e)
	if preview.Code != "assignment_saved" || preview.CanConfirm || preview.Assignment == nil || preview.Assignment.OperationID != r.OperationID {
		t.Fatal(preview)
	}
	changed := r
	changed.Project = uuid.NewString()
	if _, e = s.ConfirmDeliveryAssignment(mergeCtx, source, changed); !errors.Is(e, records.ErrConflict) {
		t.Fatal("same op changed target", e)
	}
	changed = r
	changed.OperationID = uuid.NewString()
	if _, e = s.ConfirmDeliveryAssignment(mergeCtx, source, changed); e == nil {
		t.Fatal("second operation duplicated same retained source")
	}
	// Acknowledgement loss is recoverable even if the old workspace disappears.
	mustMerge(t, os.Rename(source.Review.Item.Workspace, source.Review.Item.Workspace+"-moved"))
	replayed, ok, e := s.LookupDeliveryAssignment(mergeCtx, r)
	mustMerge(t, e)
	if !ok || !reflect.DeepEqual(saved, replayed) {
		t.Fatal("lost real receipt after path change", replayed)
	}
}

func TestE05AssignmentRejectsUnqualifiedAndProtectedSources(t *testing.T) {
	for _, kind := range []string{"grant", "attempted", "unknown", "global", "protected", "imported", "request_changed"} {
		t.Run(kind, func(t *testing.T) {
			s, source, r := assignmentFixture(t)
			switch kind {
			case "grant":
				source.Review.Item.HasGrant = true
			case "attempted":
				source.Review.Item.Attempts = 1
			case "unknown":
				source.Review.Item.Status.State = "sending"
			case "global":
				scopes, e := s.Scopes(mergeCtx)
				mustMerge(t, e)
				for _, scope := range scopes {
					if scope.Kind == "preference" {
						source.Review.Arguments.Scope = scope.ID
					}
				}
			case "protected":
				source.Review.Arguments.Source = &records.AgentSource{Native: &records.CodexOriginal{}}
			case "imported":
				source.Review.Arguments.Origin = "imported"
			case "request_changed":
				source.Review.Item.Status.RequestDigest = UsageDigest("changed source")
			}
			if _, e := s.ConfirmDeliveryAssignment(mergeCtx, source, r); e == nil {
				t.Fatal("unqualified source assigned", kind)
			}
			if organizationCount(t, s, `SELECT count(*) FROM memories`) != 0 || organizationCount(t, s, `SELECT count(*) FROM delivery_assignments`) != 0 {
				t.Fatal("failed assignment wrote effective data")
			}
		})
	}
}

func TestE05AssignmentDirectoryAndGitCAS(t *testing.T) {
	for _, kind := range []string{"directory", "git", "library"} {
		t.Run(kind, func(t *testing.T) {
			s, source, r := assignmentFixture(t)
			switch kind {
			case "directory":
				mustMerge(t, os.Rename(source.Review.Item.Workspace, source.Review.Item.Workspace+"-original"))
				mustMerge(t, os.Mkdir(source.Review.Item.Workspace, 0700))
			case "git":
				mustMerge(t, os.Rename(filepath.Join(source.Review.Item.Workspace, ".git"), filepath.Join(source.Review.Item.Workspace, ".git-old")))
				if e := exec.Command("git", "init", "-q", source.Review.Item.Workspace).Run(); e != nil {
					t.Fatal(e)
				}
			case "library":
				r.LibraryID = uuid.NewString()
			}
			if _, e := s.ConfirmDeliveryAssignment(mergeCtx, source, r); e == nil {
				t.Fatal("changed entity accepted", kind)
			}
			if organizationCount(t, s, `SELECT count(*) FROM memories`) != 0 {
				t.Fatal("failed CAS saved material")
			}
		})
	}
}

func TestE05AssignmentOriginalSaveBothDirections(t *testing.T) {
	for _, first := range []string{"agent", "assignment"} {
		t.Run(first, func(t *testing.T) {
			s, source, r := assignmentFixture(t)
			a := source.Review.Arguments
			key := assignmentOriginalKey(source)
			memory := key[len("agent:"):]
			m := Mutation{Kind: "save", OperationID: key, Memory: memory, Dataset: r.Project, Text: a.Text, Title: a.Title, Origin: a.Origin, Source: "pi", SourceDetail: a.Source}
			if first == "agent" {
				_, e := s.Apply(mergeCtx, m)
				mustMerge(t, e)
				source.Review.Item.HasGrant = true
				preview, e := s.DeliveryAssignmentPreview(mergeCtx, source, r.Project)
				mustMerge(t, e)
				if preview.Code != "original_already_saved" || preview.OriginalMemory != memory || preview.CanConfirm {
					t.Fatal(preview)
				}
				if _, e = s.ConfirmDeliveryAssignment(mergeCtx, source, r); e == nil {
					t.Fatal("duplicated original save")
				}
			} else {
				_, e := s.ConfirmDeliveryAssignment(mergeCtx, source, r)
				mustMerge(t, e)
				if _, e = s.Apply(mergeCtx, m); e == nil {
					t.Fatal("late legacy Agent op duplicated user assignment")
				}
				// Explicitly grant later only for testing the D04 owner path's same gate.
				old, e := s.Connection(mergeCtx, source.Review.Item.ConnectionID)
				mustMerge(t, e)
				c, e := s.ConfigureConnection(mergeCtx, records.ConnectionDraft{ID: old.ID, Agent: old.Agent, Name: old.Name, AllowedScopes: []string{r.Project}})
				mustMerge(t, e)
				c.ContextProject = r.Project
				canonical, digest, e := DeliveryWorkspaceIdentity(mergeCtx, r.Project, source.Review.Item.Workspace)
				mustMerge(t, e)
				grant := records.AgentDeliveryGrant{LibraryID: r.LibraryID, ConnectionID: c.ID, Generation: c.Generation, Scope: r.Project, Workspace: canonical, WorkspaceDigest: digest}
				if _, e = s.ApplyAgentSaveInLibrary(mergeCtx, c, m, r.LibraryID, grant); e == nil {
					t.Fatal("late granted D04 op duplicated assignment")
				}
			}
			if organizationCount(t, s, `SELECT count(*) FROM memories`) != 1 {
				t.Fatal("bidirectional gate changed one-save invariant")
			}
		})
	}
}

func TestE05AssignmentSchema30BackupRestoreAndSchema29Migration(t *testing.T) {
	s, source, r := assignmentFixture(t)
	_, e := s.ConfirmDeliveryAssignment(mergeCtx, source, r)
	mustMerge(t, e)
	vault := t.TempDir()
	id := uuid.NewString()
	receipt, e := s.Backup(mergeCtx, vault, id)
	mustMerge(t, e)
	if receipt.Schema != SchemaVersion {
		t.Fatal(receipt)
	}
	image, e := os.ReadFile(filepath.Join(vault, "backups/manual", id, "snapshot.sqlite"))
	mustMerge(t, e)
	data, _, e := PrepareRestoredSnapshot(context.Background(), image, uuid.NewString())
	mustMerge(t, e)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, data, 0600))
	restored, e := Open(path)
	mustMerge(t, e)
	defer restored.Close()
	if organizationCount(t, restored, `SELECT count(*) FROM delivery_assignments WHERE state='retired' AND json_extract(payload,'$.state')='retired'`) != 1 {
		t.Fatal("restore did not retire independent assignments")
	}
	if _, _, e = restored.LookupDeliveryAssignment(mergeCtx, r); e == nil {
		t.Fatal("old library request revived after restore")
	}
	if _, e = restored.DeliveryAssignmentPreview(mergeCtx, source, r.Project); e == nil {
		t.Fatal("retired source offered duplicate assignment")
	}
	legacyPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, e := Open(legacyPath)
	mustMerge(t, e)
	_, e = legacy.DB.Exec(`DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments;PRAGMA user_version=29`)
	mustMerge(t, e)
	mustMerge(t, legacy.Close())
	old, e := os.ReadFile(legacyPath)
	mustMerge(t, e)
	_, _, e = PrepareRestoredSnapshot(mergeCtx, old, uuid.NewString())
	mustMerge(t, e)
	upgraded, e := Open(legacyPath)
	mustMerge(t, e)
	defer upgraded.Close()
	if upgraded.Schema != SchemaVersion || organizationCount(t, upgraded, `SELECT count(*) FROM delivery_assignments`) != 0 {
		t.Fatal("old schema29 migration failed")
	}
}
