package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const captureReceiptPrefix = "memlane-capture-receipt:"

func lookupCapturedReceipt(ctx context.Context, q provenanceReader, source records.DeliveryAssignmentSource, library string) (records.CaptureDeliveryReceipt, bool, error) {
	var out records.CaptureDeliveryReceipt
	var memory, project, text, title, agent, origin, detail string
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT o.memory,o.revision,m.dataset,v.text,v.title,md.source,md.origin,md.source_detail FROM operations o JOIN memories m ON m.id=o.memory JOIN versions v ON v.memory=o.memory AND v.revision=o.revision JOIN memory_metadata md ON md.memory=o.memory WHERE o.id=?`, assignmentOriginalKey(source)).Scan(&memory, &revision, &project, &text, &title, &agent, &origin, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	var stored records.AgentSource
	a := source.Review.Arguments
	if json.Unmarshal([]byte(detail), &stored) != nil || a.Source == nil || origin != "raw" || agent != "codex" || text != a.Text || title != a.Title || stored.Fragment != a.Source.Fragment || len(stored.References) != len(a.Source.References)+1 {
		return out, false, records.ErrConflict
	}
	if !reflect.DeepEqual(stored.References[:len(a.Source.References)], a.Source.References) && len(a.Source.References) > 0 {
		return out, false, records.ErrConflict
	}
	proof := stored.References[len(stored.References)-1]
	if !strings.HasPrefix(proof, captureReceiptPrefix) || json.Unmarshal([]byte(strings.TrimPrefix(proof, captureReceiptPrefix)), &out) != nil {
		return out, false, records.ErrConflict
	}
	i := source.Review.Item
	if out.LibraryID != library {
		return out, false, access("capture_library_changed", "saved capture belongs to the prior library")
	}
	if !out.Persisted || out.EntryID != i.Status.ID || out.RequestDigest != i.Status.RequestDigest || out.ReviewDigest != i.Status.ReviewDigest || out.ConnectionID != i.ConnectionID || out.OperationID != a.OperationID || out.Project != project || out.Memory != memory || out.Revision != revision {
		return out, false, records.ErrConflict
	}
	return out, true, nil
}

// Receipt lookup precedes permission/workspace revalidation so an ack lost
// after commit can be recovered without creating another record.
func (s *Store) CapturedReceipt(ctx context.Context, source records.DeliveryAssignmentSource) (records.CaptureDeliveryReceipt, bool, error) {
	library, err := s.CogneeLibraryIdentity(ctx)
	if err != nil {
		return records.CaptureDeliveryReceipt{}, false, err
	}
	return lookupCapturedReceipt(ctx, s.DB, source, library)
}

func (s *Store) captureRouteWorkspace(ctx context.Context, source records.DeliveryAssignmentSource, route records.CaptureRoute) (assignmentWorkspace, error) {
	var frozen assignmentWorkspace
	origin, ok := records.ReadCaptureOrigin(source.Review.Arguments)
	if !ok || route.ConsentID != origin.Consent || route.ConnectionID != source.Review.Item.ConnectionID || !route.Repository.SameRepository(origin.Repository) {
		return frozen, access("capture_route_mismatch", "capture route does not match original source")
	}
	workspace, err := resolveDeliveryWorkspace(ctx, "", source.Review.Item.Workspace)
	if err != nil {
		return frozen, err
	}
	if err = checkCapturedRepository(source.Review.Arguments, workspace.identity); err != nil {
		return frozen, err
	}
	var path string
	if err = s.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, route.Project).Scan(&path); err != nil {
		return frozen, err
	}
	project, err := resolveDeliveryWorkspace(ctx, route.Project, path)
	if err != nil {
		return frozen, err
	}
	if project.identity.GitCommon != workspace.identity.GitCommon || project.identity.CommonDevice != workspace.identity.CommonDevice || project.identity.CommonInode != workspace.identity.CommonInode {
		return frozen, access("capture_project_mismatch", "capture repository does not match project")
	}
	frozen = assignmentWorkspace{source: workspace, project: project}
	return frozen, frozen.check()
}

func (s *Store) ValidateCaptureRoute(ctx context.Context, source records.DeliveryAssignmentSource, route records.CaptureRoute) error {
	library, err := s.CogneeLibraryIdentity(ctx)
	if err != nil {
		return err
	}
	if library != route.LibraryID {
		return access("capture_library_changed", "capture library changed")
	}
	c, err := s.Connection(ctx, route.ConnectionID)
	if err != nil {
		return err
	}
	if !c.Enabled || c.Agent != "codex" {
		return access("capture_connection_revoked", "capture connection unavailable")
	}
	_, err = s.captureRouteWorkspace(ctx, source, route)
	return err
}

// Caller holds the capture consent/route file owner. SQL independently checks
// library and connection inside the transaction; tool scopes are untouched.
func (s *Store) SaveCapturedRaw(ctx context.Context, source records.DeliveryAssignmentSource, route records.CaptureRoute) (records.CaptureDeliveryReceipt, error) {
	if prior, found, err := s.CapturedReceipt(ctx, source); err != nil || found {
		return prior, err
	}
	var out records.CaptureDeliveryReceipt
	if err := validateAssignmentSource(source); err != nil {
		return out, err
	}
	if err := s.ValidateCaptureRoute(ctx, source, route); err != nil {
		return out, err
	}
	frozen, err := s.captureRouteWorkspace(ctx, source, route)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return out, err
	}
	if library != route.LibraryID {
		return out, access("capture_library_changed", "capture library changed")
	}
	current, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, route.ConnectionID))
	if err != nil {
		return out, err
	}
	if !current.Enabled || current.Agent != "codex" {
		return out, access("capture_connection_revoked", "capture connection revoked")
	}
	var retired int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, route.ConnectionID).Scan(&retired); err != nil {
		return out, err
	}
	if retired > 0 {
		return out, access("restore_reconnect_required", "backup restored; reconnect this Agent with a new identity")
	}
	if prior, found, e := lookupCapturedReceipt(ctx, tx, source, library); e != nil || found {
		return prior, e
	}
	var projectPath string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, route.Project).Scan(&projectPath); err != nil {
		return out, err
	}
	if projectPath != frozen.project.identity.Workspace {
		return out, access("capture_project_changed", "capture project changed")
	}
	if err = frozen.check(); err != nil {
		return out, err
	}
	i, a := source.Review.Item, source.Review.Arguments
	memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte("capture-original:"+library+"\x00"+assignmentOriginalKey(source))).String()
	out = records.CaptureDeliveryReceipt{EntryID: i.Status.ID, RequestDigest: i.Status.RequestDigest, ReviewDigest: i.Status.ReviewDigest, ConnectionID: i.ConnectionID, OperationID: a.OperationID, LibraryID: library, RouteID: route.OperationID, Project: route.Project, Memory: memory, Revision: 1, Persisted: true}
	proof, _ := json.Marshal(out)
	detail := *a.Source
	detail.References = append(append([]string{}, a.Source.References...), captureReceiptPrefix+string(proof))
	_, err = s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: assignmentOriginalKey(source), Memory: memory, Dataset: route.Project, Title: a.Title, Text: a.Text, Source: "codex", Origin: "raw", SourceDetail: &detail})
	if err != nil {
		return records.CaptureDeliveryReceipt{}, err
	}
	if err = frozen.check(); err != nil {
		return records.CaptureDeliveryReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return records.CaptureDeliveryReceipt{}, err
	}
	return out, nil
}
