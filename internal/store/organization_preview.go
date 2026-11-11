package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateOrganizationPreviews(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE organization_preview_head(singleton INTEGER PRIMARY KEY CHECK(singleton=1),identity TEXT NOT NULL,generation INTEGER NOT NULL DEFAULT 0,current TEXT NOT NULL DEFAULT '',operation TEXT NOT NULL DEFAULT '',digest TEXT NOT NULL DEFAULT '');
CREATE TABLE organization_previews(id TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE organization_start_intents(operation TEXT PRIMARY KEY,request_digest TEXT NOT NULL,preview TEXT NOT NULL,abandoned INTEGER NOT NULL DEFAULT 0 CHECK(abandoned IN (0,1)));
INSERT INTO organization_preview_head(singleton,identity) VALUES(1,?);
PRAGMA user_version=23;`, uuid.NewString())
	return err
}

func organizationPreviewHead(ctx context.Context, tx *sql.Tx) (records.OrganizationPreviewState, string, error) {
	var out records.OrganizationPreviewState
	var current string
	err := tx.QueryRowContext(ctx, `SELECT identity,generation,current FROM organization_preview_head WHERE singleton=1`).Scan(&out.Identity, &out.Generation, &current)
	return out, current, err
}

func validPreviewScroll(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) && v >= 0 && v <= 1e8
}

func organizationPreviewValid(p records.OrganizationPreview) bool {
	if !previewIdentity(p.ID) || p.Kind != "knowledge" && p.Kind != "raw" || len(p.Inputs) > 16 || len(p.Destinations) > 8 || len(p.Decisions) > 64 || len(p.RejectReasons) > 32 || len(p.ExperienceDrafts) > 32 || len(p.ReviewScroll) > 32 || len(p.ReviewFocus) > 32 || len(p.MoveAuthorizations) > 32 || len(p.Focus) > 128 {
		return false
	}
	if p.Project != "" && !previewIdentity(p.Project) || p.TaskID != "" && !previewIdentity(p.TaskID) || p.ProjectFilter != "" && !previewIdentity(p.ProjectFilter) || p.OriginFilter != "" && p.OriginFilter != "manual" && p.OriginFilter != "schedule" {
		return false
	}
	if p.Purpose != "" && p.Purpose != "global" || p.Purpose != "" && (!p.Global || !p.Review) {
		return false
	}
	for _, v := range []float32{p.Scroll, p.ListScroll, p.ResultScroll} {
		if !validPreviewScroll(v) {
			return false
		}
	}
	for _, v := range p.ReviewScroll {
		if !validPreviewScroll(v) {
			return false
		}
	}
	for _, v := range p.RejectReasons {
		if len(v) > 1024 || !utf8.ValidString(v) {
			return false
		}
	}
	if p.Project == "" && (len(p.Inputs) > 0 || len(p.Destinations) > 0 || p.Start != nil || p.TaskID != "") {
		return false
	}
	if p.TaskID == "" && (p.Candidate != "" || p.Resume != nil || len(p.Decisions) > 0 || len(p.RejectReasons) > 0 || len(p.ExperienceDrafts) > 0 || len(p.MoveAuthorizations) > 0) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range p.Inputs {
		if !previewIdentity(v.Memory) || v.Revision < 1 || seen[v.Memory] {
			return false
		}
		seen[v.Memory] = true
	}
	seen = map[string]bool{}
	for _, v := range p.Destinations {
		if !previewIdentity(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	if p.Start != nil {
		d := p.Start
		if !previewIdentity(d.OperationID) || d.Project != p.Project || d.Review != p.Review || d.Global != p.Global || d.Purpose != p.Purpose || d.Processing == nil || !previewIdentity(d.Processing.Revision) || d.Processing.Generation < 1 || len(d.Inputs) < 1 || records.MergeDigest(d.Inputs) != records.MergeDigest(p.Inputs) || records.MergeDigest(d.Destinations) != records.MergeDigest(p.Destinations) {
			return false
		}
	}
	return true
}

func organizationPreviewTask(ctx context.Context, tx *sql.Tx, p records.OrganizationPreview) (*records.OrganizationTask, error) {
	if p.TaskID == "" {
		return nil, nil
	}
	t, err := organizationTask(ctx, tx, p.TaskID)
	if err != nil {
		return nil, err
	}
	if t.Project != p.Project {
		return nil, records.MergeError("invalid_preview")
	}
	refs := []records.MergeTarget{}
	for _, input := range t.Snapshot.Inputs {
		refs = append(refs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
	}
	if p.New || records.MergeDigest(refs) != records.MergeDigest(p.Inputs) || p.Review != (t.Snapshot.Schema != records.OrganizationSchema) || p.Global != (t.Snapshot.Schema == records.OrganizationGlobalSchema) || p.Purpose != t.Snapshot.Purpose {
		return nil, records.MergeError("invalid_preview")
	}
	if p.Start != nil {
		var id, digest string
		if err = tx.QueryRowContext(ctx, `SELECT id,request_digest FROM organization_tasks WHERE operation_id=?`, p.Start.OperationID).Scan(&id, &digest); err != nil || id != p.TaskID || digest != records.MergeDigest(*p.Start) {
			return nil, records.MergeError("invalid_preview")
		}
	}
	return &t, nil
}

func validateOrganizationPreview(ctx context.Context, tx *sql.Tx, p records.OrganizationPreview) error {
	invalid := records.MergeError("invalid_preview")
	if !organizationPreviewValid(p) {
		return invalid
	}
	for _, id := range []string{p.Project, p.ProjectFilter} {
		if id == "" {
			continue
		}
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, id).Scan(&kind); err != nil || kind != "project" {
			return records.MergeError("scope_denied")
		}
	}
	t, err := organizationPreviewTask(ctx, tx, p)
	if err != nil {
		return err
	}
	for _, ref := range p.Inputs {
		var scope string
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT h.previous_scope FROM scope_history h WHERE h.memory=m.id AND h.revision>? ORDER BY h.revision LIMIT 1),m.dataset) FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=? WHERE m.id=?`, ref.Revision, ref.Revision, ref.Memory).Scan(&scope)
		if err != nil || scope != p.Project {
			return records.MergeError("scope_denied")
		}
	}
	entries := map[string]records.OrganizationEntry{}
	if t != nil {
		for _, e := range t.Entries {
			entries[e.ID] = e
		}
	}
	if p.Candidate != "" {
		if _, ok := entries[p.Candidate]; !ok {
			return invalid
		}
	}
	for id := range p.ReviewScroll {
		if _, ok := entries[id]; !ok {
			return invalid
		}
	}
	for id, key := range p.ReviewFocus {
		if _, ok := entries[id]; !ok || len(key) > 128 || !utf8.ValidString(key) {
			return invalid
		}
	}
	for id := range p.RejectReasons {
		if e, ok := entries[id]; !ok || !e.Item.RequiresReview() {
			return invalid
		}
	}
	for id, digest := range p.MoveAuthorizations {
		e, ok := entries[id]
		if !ok || e.Item.Action != "move" || digest != p.TaskID+":"+records.MergeDigest(e.Item) {
			return invalid
		}
	}
	validDecision := func(r records.OrganizationReview) bool {
		e, ok := entries[r.Item]
		if !ok || !e.Item.RequiresReview() || r.Task != p.TaskID || r.Digest != records.MergeDigest(e.Item) || !previewIdentity(r.Receipt) || len(r.RejectionReason) > 1024 || !utf8.ValidString(r.RejectionReason) || r.Confirm && r.RejectionReason != "" {
			return false
		}
		if r.MoveScopeAccepted && (!r.Confirm || e.Item.Action != "move") {
			return false
		}
		return r.Edit == nil || r.Confirm && e.Item.Action == "global_experience" && r.Edit.Valid()
	}
	for key, r := range p.Decisions {
		if key != r.Item+boolText(r.Confirm) || !validDecision(r) {
			return invalid
		}
	}
	for id, d := range p.ExperienceDrafts {
		e, ok := entries[id]
		if !ok || e.Item.Action != "global_experience" || d.ProposalDigest != records.MergeDigest(e.Item) || len(d.Value.Title) > 512 || len(d.Value.Text) > records.MergeInputLimit || len(d.Value.Conditions) > 4096 || !utf8.ValidString(d.Value.Title+d.Value.Text+d.Value.Conditions) {
			return invalid
		}
		for i, r := range []*records.OrganizationReview{d.Request, d.RejectRequest} {
			if r != nil && (!validDecision(*r) || r.Item != id || r.Confirm != (i == 0)) {
				return invalid
			}
		}
	}
	if p.Resume != nil && (p.Resume.Task != p.TaskID || !previewIdentity(p.Resume.OperationID) || p.Resume.ExpectedAttempt < 1) {
		return invalid
	}
	return nil
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Read-only recovery resolves only the original operation and immutable inputs.
func (s *Store) OrganizationPreview(ctx context.Context) (records.OrganizationPreviewState, error) {
	return s.OrganizationPreviewFor(ctx, "")
}

// A task re-entry reads its existing draft without starting or resuming work.
// Generation still belongs to the single writer for the whole library.
func (s *Store) OrganizationPreviewFor(ctx context.Context, taskID string) (records.OrganizationPreviewState, error) {
	if taskID != "" && !previewIdentity(taskID) {
		return records.OrganizationPreviewState{}, records.MergeError("invalid_preview")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.OrganizationPreviewState{}, err
	}
	defer tx.Rollback()
	out, current, err := organizationPreviewHead(ctx, tx)
	if err != nil || current == "" && taskID == "" {
		return out, err
	}
	var raw string
	if taskID == "" {
		err = tx.QueryRowContext(ctx, `SELECT payload FROM organization_previews WHERE id=?`, current).Scan(&raw)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT payload FROM organization_previews WHERE json_extract(payload,'$.task')=? ORDER BY rowid DESC LIMIT 1`, taskID).Scan(&raw)
		if err == sql.ErrNoRows {
			return out, nil
		}
	}
	if err != nil {
		return out, err
	}
	var p records.OrganizationPreview
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return out, err
	}
	if p.TaskID == "" && p.Start != nil {
		var id, digest string
		err = tx.QueryRowContext(ctx, `SELECT id,request_digest FROM organization_tasks WHERE operation_id=?`, p.Start.OperationID).Scan(&id, &digest)
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		if err == nil {
			if digest != records.MergeDigest(*p.Start) {
				return out, ErrConflict
			}
			p.TaskID, p.New = id, false
		}
	}
	if err = validateOrganizationPreview(ctx, tx, p); err != nil {
		return out, err
	}
	out.Task, err = organizationPreviewTask(ctx, tx, p)
	if err != nil {
		return out, err
	}
	if out.Task != nil {
		out.Inputs = out.Task.Snapshot.Inputs
	} else {
		for _, ref := range p.Inputs {
			input := records.OrganizationInput{MergeInput: records.MergeInput{Memory: ref.Memory, Revision: ref.Revision}}
			if err = tx.QueryRowContext(ctx, `SELECT title,text,created_at FROM versions WHERE memory=? AND revision=?`, ref.Memory, ref.Revision).Scan(&input.Title, &input.Text, &input.CapturedAt); err != nil {
				return out, err
			}
			out.Inputs = append(out.Inputs, input)
		}
	}
	out.Preview = &p
	return out, nil
}

func (s *Store) SaveOrganizationPreview(ctx context.Context, w records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
	var empty records.OrganizationPreviewState
	if !previewIdentity(w.Identity) || !previewIdentity(w.Operation) || w.Generation < 0 {
		return empty, records.MergeError("invalid_preview")
	}
	raw, err := json.Marshal(w.Preview)
	if err != nil || len(raw) > 768*1024 {
		return empty, records.MergeError("invalid_preview")
	}
	digest := records.MergeDigest(struct {
		Preview records.OrganizationPreview
		Abandon bool
	}{w.Preview, w.AbandonStart})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	out, _, err := organizationPreviewHead(ctx, tx)
	if err != nil {
		return out, err
	}
	if out.Identity != w.Identity {
		return out, records.MergeError("preview_retired")
	}
	var operation, priorDigest string
	if err = tx.QueryRowContext(ctx, `SELECT operation,digest FROM organization_preview_head WHERE singleton=1`).Scan(&operation, &priorDigest); err != nil {
		return out, err
	}
	if operation == w.Operation {
		if priorDigest != digest || out.Generation != w.Generation+1 {
			return out, ErrConflict
		}
		return out, nil
	}
	if out.Generation != w.Generation || out.Generation == math.MaxInt64 {
		return out, ErrConflict
	}
	var priorRaw string
	err = tx.QueryRowContext(ctx, `SELECT payload FROM organization_previews WHERE id=?`, w.Preview.ID).Scan(&priorRaw)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	var old records.OrganizationPreview
	if err == nil {
		if err = json.Unmarshal([]byte(priorRaw), &old); err != nil {
			return out, err
		}
	}
	if old.TaskID != "" && w.Preview.TaskID != old.TaskID {
		return out, records.MergeError("invalid_preview")
	}
	if old.Resume != nil {
		if w.Preview.Resume != nil && records.MergeDigest(*old.Resume) != records.MergeDigest(*w.Preview.Resume) {
			return out, records.MergeError("preview_request_frozen")
		}
		if w.Preview.Resume == nil {
			var prior string
			if err = tx.QueryRowContext(ctx, `SELECT request_digest FROM organization_attempts WHERE task=? AND resume_id=?`, old.Resume.Task, old.Resume.OperationID).Scan(&prior); err != nil || prior != records.MergeDigest(*old.Resume) {
				return out, records.MergeError("preview_request_frozen")
			}
		}
	}
	if old.Start != nil {
		if w.Preview.Start != nil && records.MergeDigest(*old.Start) != records.MergeDigest(*w.Preview.Start) {
			return out, records.MergeError("preview_request_frozen")
		}
		if w.Preview.Start == nil {
			var task, request string
			err = tx.QueryRowContext(ctx, `SELECT id,request_digest FROM organization_tasks WHERE operation_id=?`, old.Start.OperationID).Scan(&task, &request)
			if err != nil && err != sql.ErrNoRows {
				return out, err
			}
			if w.AbandonStart {
				if err != sql.ErrNoRows || w.Preview.TaskID != "" {
					return out, records.MergeError("preview_start_exists")
				}
				if _, err = tx.ExecContext(ctx, `UPDATE organization_start_intents SET abandoned=1 WHERE operation=? AND request_digest=?`, old.Start.OperationID, records.MergeDigest(*old.Start)); err != nil {
					return out, err
				}
			} else if err != nil || task != w.Preview.TaskID || request != records.MergeDigest(*old.Start) {
				return out, records.MergeError("preview_request_frozen")
			}
		}
	} else if w.AbandonStart {
		return out, records.MergeError("invalid_preview")
	}
	for key, request := range old.Decisions {
		if current, ok := w.Preview.Decisions[key]; !ok || records.MergeDigest(current) != records.MergeDigest(request) {
			return out, records.MergeError("preview_request_frozen")
		}
	}
	for id, draft := range old.ExperienceDrafts {
		current := w.Preview.ExperienceDrafts[id]
		for i, request := range []*records.OrganizationReview{draft.Request, draft.RejectRequest} {
			if request == nil {
				continue
			}
			next := current.Request
			if i == 1 {
				next = current.RejectRequest
			}
			if next == nil || records.MergeDigest(*next) != records.MergeDigest(*request) {
				return out, records.MergeError("preview_request_frozen")
			}
		}
	}
	if err = validateOrganizationPreview(ctx, tx, w.Preview); err != nil {
		return out, err
	}
	if start := w.Preview.Start; start != nil {
		request := records.MergeDigest(*start)
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_start_intents(operation,request_digest,preview) VALUES(?,?,?) ON CONFLICT(operation) DO NOTHING`, start.OperationID, request, w.Preview.ID); err != nil {
			return out, err
		}
		if err = organizationStartIntent(ctx, tx, *start); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_previews(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, w.Preview.ID, string(raw)); err != nil {
		return out, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE organization_preview_head SET generation=generation+1,current=?,operation=?,digest=? WHERE singleton=1 AND identity=? AND generation=?`, w.Preview.ID, w.Operation, digest, w.Identity, w.Generation)
	if err != nil {
		return out, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return out, ErrConflict
	}
	out.Generation++
	return out, tx.Commit()
}

func organizationStartIntent(ctx context.Context, tx *sql.Tx, d records.OrganizationStart) error {
	var digest string
	var abandoned bool
	err := tx.QueryRowContext(ctx, `SELECT request_digest,abandoned FROM organization_start_intents WHERE operation=?`, d.OperationID).Scan(&digest, &abandoned)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if digest != records.MergeDigest(d) {
		return ErrConflict
	}
	if abandoned {
		return records.MergeError("start_abandoned")
	}
	return nil
}
