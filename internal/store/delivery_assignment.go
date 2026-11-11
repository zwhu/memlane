package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"time"
)

func migrateDeliveryAssignments(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE delivery_assignments(
 operation TEXT PRIMARY KEY,digest TEXT NOT NULL,library TEXT NOT NULL,entry TEXT NOT NULL,
 request_digest TEXT NOT NULL,review_digest TEXT NOT NULL,project TEXT NOT NULL REFERENCES scopes(id),
 original_operation TEXT NOT NULL,memory TEXT NOT NULL,revision INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('saved','retired')),payload TEXT NOT NULL,
 UNIQUE(entry),UNIQUE(original_operation),
 FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision));PRAGMA user_version=30;`)
	return err
}
func assignmentHex(s string) bool {
	raw, e := hex.DecodeString(s)
	return e == nil && len(raw) == 32 && strings.ToLower(s) == s
}
func assignmentOriginalKey(source records.DeliveryAssignmentSource) string {
	return "agent:" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(source.Review.Item.ConnectionID+"\x00"+source.Review.Arguments.OperationID)).String()
}
func validateAssignmentSource(source records.DeliveryAssignmentSource) error {
	item, a := source.Review.Item, source.Review.Arguments
	if item.HasGrant || item.LibraryID != "" || item.Attempts != 0 || item.Status.PersistedConfirmed || item.Status.SavedRevision != 0 || item.Status.Memory != "" || (item.Status.State != "pending_unverified" && item.Status.State != "blocked" && item.Status.State != "waiting") {
		return access("assignment_ineligible", "only ungranted never-attempted retained content can be assigned")
	}
	if item.Status.ID == "" || item.ConnectionID == "" || item.OperationID == "" || len(item.OperationID) > 128 || len(a.Scopes) > 100 || item.OperationID != a.OperationID || item.Status.ID != UsageDigest([]string{item.ConnectionID, a.OperationID}) || !assignmentHex(item.Status.RequestDigest) || !assignmentHex(item.Status.ReviewDigest) || source.WorkspacePhysical == "" || item.Workspace == "" || strings.TrimSpace(a.Text) == "" || len(a.Text) > 524288 || len(a.Title) > 512 || a.Memory != "" {
		return access("assignment_source_invalid", "retained original source identity is incomplete")
	}
	if a.Origin != "" && a.Origin != "knowledge" && a.Origin != "raw" {
		return access("assignment_source_invalid", "retained origin is unsupported")
	}
	if a.Source != nil && (a.Source.Native != nil || a.Source.Official != nil || a.Source.History != nil) {
		return access("invalid_source", "retained request cannot stamp protected provenance")
	}
	return nil
}
func assignmentProjectScope(ctx context.Context, q provenanceReader, source records.DeliveryAssignmentSource) error {
	scopes := append([]string{}, source.Review.Arguments.Scopes...)
	if source.Review.Arguments.Scope != "" {
		scopes = append(scopes, source.Review.Arguments.Scope)
	}
	for _, scope := range scopes {
		var kind string
		err := q.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, scope).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if kind == "preference" || kind == "experience" {
			return access("assignment_not_project", "global-scope permission failure is not a pending project assignment")
		}
	}
	return nil
}
func assignmentOriginalSaved(ctx context.Context, q provenanceReader, source records.DeliveryAssignmentSource) (string, int64, error) {
	var memory string
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT memory,revision FROM operations WHERE id=?`, assignmentOriginalKey(source)).Scan(&memory, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return memory, revision, err
}

// applyTx calls this gate before original Agent namespace replay/new save.
// The user assignment receipt is never substituted for an Agent SaveReceipt.
func (s *Store) rejectUserAssignedAgentTx(ctx context.Context, tx *sql.Tx, operation string) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM delivery_assignments WHERE library=(SELECT identity FROM cognee_library_identity WHERE id=1) AND original_operation=? AND state='saved'`, operation).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return access("delivery_assigned_by_user", "original retained request was assigned by a separate user operation; do not resend it")
	}
	return nil
}

func (s *Store) assignmentForEntry(ctx context.Context, library, entry string) (*records.DeliveryAssignmentReceipt, error) {
	var raw, state, originalLibrary string
	err := s.DB.QueryRowContext(ctx, `SELECT payload,state,library FROM delivery_assignments WHERE entry=?`, entry).Scan(&raw, &state, &originalLibrary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if state != "saved" || originalLibrary != library {
		return nil, access("assignment_retired", "this retained source has a retired assignment; do not save it again")
	}
	var out records.DeliveryAssignmentReceipt
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type assignmentWorkspace struct{ source, project deliveryWorkspaceSnapshot }

func (a assignmentWorkspace) check() error {
	if e := a.source.check(); e != nil {
		return access("workspace_changed", "source workspace identity changed")
	}
	if e := a.project.check(); e != nil {
		return access("workspace_changed", "registered project identity changed")
	}
	return nil
}

func (s *Store) assignmentPreview(ctx context.Context, source records.DeliveryAssignmentSource, selected string) (records.DeliveryAssignmentPreview, assignmentWorkspace, error) {
	var frozen assignmentWorkspace
	item := source.Review.Item
	out := records.DeliveryAssignmentPreview{EntryID: item.Status.ID, RequestDigest: item.Status.RequestDigest, ReviewDigest: item.Status.ReviewDigest, Workspace: item.Workspace, Item: item, Arguments: source.Review.Arguments, SelectedProject: selected, Projects: []records.Scope{}}
	if s.Schema < 30 {
		return out, frozen, access("assignment_unavailable", "project assignment ledger unavailable")
	}
	if item.ConnectionID == "" || item.OperationID == "" || item.OperationID != source.Review.Arguments.OperationID || item.Status.ID != UsageDigest([]string{item.ConnectionID, item.OperationID}) {
		return out, frozen, access("assignment_source_invalid", "retained operation identity invalid")
	}
	if err := assignmentProjectScope(ctx, s.DB, source); err != nil {
		return out, frozen, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&out.LibraryID); err != nil {
		return out, frozen, err
	}
	existing, err := s.assignmentForEntry(ctx, out.LibraryID, out.EntryID)
	if err != nil {
		return out, frozen, err
	}
	if existing != nil {
		out.Assignment = existing
		out.Code = "assignment_saved"
		return out, frozen, nil
	}
	out.OriginalMemory, out.OriginalRevision, err = assignmentOriginalSaved(ctx, s.DB, source)
	if err != nil {
		return out, frozen, err
	}
	if out.OriginalMemory != "" {
		out.Code = "original_already_saved"
		return out, frozen, nil
	}
	if err := validateAssignmentSource(source); err != nil {
		return out, frozen, err
	}
	scopes, err := s.Scopes(ctx)
	if err != nil {
		return out, frozen, err
	}
	for _, scope := range scopes {
		if scope.Kind == "project" {
			out.Projects = append(out.Projects, scope)
		}
	}
	workspace, err := resolveDeliveryWorkspace(ctx, "", item.Workspace)
	if err != nil {
		out.Code = "workspace_unavailable"
		return out, frozen, nil
	}
	if fmt.Sprintf("%d:%d", workspace.identity.WorkspaceDevice, workspace.identity.WorkspaceInode) != source.WorkspacePhysical {
		out.Code = "workspace_changed"
		return out, frozen, nil
	}
	if workspace.identity.GitCommon == "" {
		out.Code = "workspace_unverified"
		return out, frozen, nil
	}
	var matches []struct {
		scope    records.Scope
		snapshot deliveryWorkspaceSnapshot
	}
	for _, scope := range out.Projects {
		known, e := resolveDeliveryWorkspace(ctx, scope.ID, scope.Path)
		if e == nil && known.identity.GitCommon == workspace.identity.GitCommon && known.identity.CommonDevice == workspace.identity.CommonDevice && known.identity.CommonInode == workspace.identity.CommonInode {
			matches = append(matches, struct {
				scope    records.Scope
				snapshot deliveryWorkspaceSnapshot
			}{scope, known})
		}
	}
	if len(matches) == 0 {
		out.Code = "project_unregistered"
		return out, frozen, nil
	}
	if len(matches) != 1 {
		out.Code = "scope_ambiguous"
		return out, frozen, nil
	}
	match := matches[0]
	out.MatchingProject = &match.scope
	workspace.identity.Project = match.scope.ID
	out.Workspace = workspace.identity.Workspace
	out.WorkspaceDigest = UsageDigest([]any{workspace.identity, match.snapshot.identity, source.WorkspacePhysical})
	frozen = assignmentWorkspace{source: workspace, project: match.snapshot}
	if selected == "" {
		out.Code = "choose_project"
	} else if selected != match.scope.ID {
		out.Code = "workspace_mismatch"
	} else {
		out.Code = "ready"
		out.CanConfirm = true
	}
	return out, frozen, nil
}

func (s *Store) DeliveryAssignmentPreview(ctx context.Context, source records.DeliveryAssignmentSource, selected string) (records.DeliveryAssignmentPreview, error) {
	out, _, err := s.assignmentPreview(ctx, source, selected)
	return out, err
}

func validateAssignmentIntent(r records.DeliveryAssignmentIntent) error {
	op, err := uuid.Parse(r.OperationID)
	if err != nil || op.String() != r.OperationID || r.LibraryID == "" || r.Project == "" || !assignmentHex(r.EntryID) || !assignmentHex(r.RequestDigest) || !assignmentHex(r.ReviewDigest) || !assignmentHex(r.WorkspaceDigest) {
		return access("invalid_request", "explicit frozen user assignment operation required")
	}
	return nil
}

// Lookup first: a committed receipt survives local ack loss or a later changed
// directory. This is the actual independent user save, never the original op.
func (s *Store) LookupDeliveryAssignment(ctx context.Context, r records.DeliveryAssignmentIntent) (records.DeliveryAssignmentReceipt, bool, error) {
	var out records.DeliveryAssignmentReceipt
	if s.Schema < 30 {
		return out, false, access("assignment_unavailable", "assignment ledger unavailable")
	}
	if err := validateAssignmentIntent(r); err != nil {
		return out, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback()
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return out, false, err
	}
	if library != r.LibraryID {
		return out, false, access("library_changed", "assignment library changed; do not rebind the old action")
	}
	var digest, raw, state string
	err = tx.QueryRowContext(ctx, `SELECT digest,payload,state FROM delivery_assignments WHERE operation=?`, r.OperationID).Scan(&digest, &raw, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if digest != UsageDigest(r) {
		return out, false, records.ErrConflict
	}
	if state != "saved" {
		return out, false, access("assignment_retired", "restored assignment is retired")
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return out, false, err
	}
	return out, true, tx.Commit()
}

func (s *Store) ConfirmDeliveryAssignment(ctx context.Context, source records.DeliveryAssignmentSource, r records.DeliveryAssignmentIntent) (records.DeliveryAssignmentReceipt, error) {
	prior, ok, err := s.LookupDeliveryAssignment(ctx, r)
	if err != nil || ok {
		return prior, err
	}
	var out records.DeliveryAssignmentReceipt
	preview, frozen, err := s.assignmentPreview(ctx, source, r.Project)
	if err != nil {
		return out, err
	}
	if preview.LibraryID != r.LibraryID {
		return out, access("library_changed", "assignment library changed")
	}
	if preview.EntryID != r.EntryID || preview.RequestDigest != r.RequestDigest || preview.ReviewDigest != r.ReviewDigest {
		return out, access("assignment_source_changed", "retained source digest changed")
	}
	if !preview.CanConfirm {
		return out, access(preview.Code, "retained source is not eligible for this project assignment")
	}
	if preview.WorkspaceDigest != r.WorkspaceDigest {
		return out, access("workspace_changed", "workspace or project entity changed since preview")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var library, path string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return out, err
	}
	if library != r.LibraryID {
		return out, access("library_changed", "assignment library changed")
	}
	var storedDigest, storedPayload, storedState string
	replayErr := tx.QueryRowContext(ctx, `SELECT digest,payload,state FROM delivery_assignments WHERE operation=?`, r.OperationID).Scan(&storedDigest, &storedPayload, &storedState)
	if replayErr == nil {
		if storedDigest != UsageDigest(r) {
			return out, records.ErrConflict
		}
		if storedState != "saved" {
			return out, access("assignment_retired", "assignment is retired")
		}
		if err = json.Unmarshal([]byte(storedPayload), &out); err != nil {
			return out, err
		}
		return out, tx.Commit()
	} else if !errors.Is(replayErr, sql.ErrNoRows) {
		return out, replayErr
	}
	if err = tx.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, r.Project).Scan(&path); err != nil {
		return out, err
	}
	if path != preview.MatchingProject.Path {
		return out, access("workspace_changed", "registered project changed")
	}
	if err = assignmentProjectScope(ctx, tx, source); err != nil {
		return out, err
	}
	original, _, err := assignmentOriginalSaved(ctx, tx, source)
	if err != nil {
		return out, err
	}
	if original != "" {
		return out, access("original_already_saved", "original Agent operation already has a real saved record")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM delivery_assignments WHERE entry=? OR original_operation=?`, r.EntryID, assignmentOriginalKey(source)).Scan(&count); err != nil {
		return out, err
	}
	if count != 0 {
		return out, records.ErrConflict
	}
	if err = frozen.check(); err != nil {
		return out, err
	}
	memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte("delivery-assignment:"+library+"\x00"+r.OperationID)).String()
	a := source.Review.Arguments
	saved, err := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "assignment:" + r.OperationID, Memory: memory, Dataset: r.Project, Text: a.Text, Title: a.Title, Origin: a.Origin, Source: "用户确认项目归属", SourceDetail: a.Source})
	if err != nil {
		return out, err
	}
	out = records.DeliveryAssignmentReceipt{OperationID: r.OperationID, LibraryID: library, EntryID: r.EntryID, RequestDigest: r.RequestDigest, ReviewDigest: r.ReviewDigest, Project: r.Project, WorkspaceDigest: r.WorkspaceDigest, Memory: saved.Memory, Revision: saved.Revision, OriginalOperationID: a.OperationID, OriginalConnectionID: source.Review.Item.ConnectionID, Source: "user_assignment", State: "saved", CreatedMS: time.Now().UnixMilli()}
	data, err := json.Marshal(out)
	if err != nil {
		return records.DeliveryAssignmentReceipt{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO delivery_assignments(operation,digest,library,entry,request_digest,review_digest,project,original_operation,memory,revision,state,payload) VALUES(?,?,?,?,?,?,?,?,?,?,'saved',?)`, r.OperationID, UsageDigest(r), library, r.EntryID, r.RequestDigest, r.ReviewDigest, r.Project, assignmentOriginalKey(source), out.Memory, out.Revision, string(data))
	if err != nil {
		return records.DeliveryAssignmentReceipt{}, err
	}
	if err = frozen.check(); err != nil {
		return records.DeliveryAssignmentReceipt{}, err
	}
	return out, tx.Commit()
}
