package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateExperiencePreviews(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE experience_preview_head(singleton INTEGER PRIMARY KEY CHECK(singleton=1),identity TEXT NOT NULL,generation INTEGER NOT NULL DEFAULT 0,current TEXT NOT NULL DEFAULT '',operation TEXT NOT NULL DEFAULT '',digest TEXT NOT NULL DEFAULT '');
CREATE TABLE experience_previews(id TEXT PRIMARY KEY,payload TEXT NOT NULL);
INSERT INTO experience_preview_head(singleton,identity) VALUES(1,?);
PRAGMA user_version=22;`, uuid.NewString())
	return err
}

func previewIdentity(v string) bool { _, err := uuid.Parse(v); return err == nil && len(v) == 36 }

func validExperiencePreview(p records.ExperiencePreview) bool {
	if !previewIdentity(p.ID) || !previewIdentity(p.Project) || !previewIdentity(p.Source.Memory) || p.Source.Revision < 1 || len(p.Drafts) > 32 || len(p.Focus) > 128 || len(p.Selected) > 128 || len(p.ReturnSelected) > 128 {
		return false
	}
	for _, n := range []float32{p.Scroll, p.ReturnScroll, p.BodyScroll} {
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) || n < 0 || n > 1e8 {
			return false
		}
	}
	for _, v := range []string{p.Page, p.ReturnPage} {
		if !strings.Contains("|总览|项目|通用知识|全部记忆|设置|整理记录|使用记录|使用指引|搜索|代码图谱|初始化|官方记忆|", "|"+v+"|") || v == "" {
			return false
		}
	}
	if p.TaskID != "" && !previewIdentity(p.TaskID) {
		return false
	}
	if p.Start != nil {
		d := p.Start
		if !previewIdentity(d.OperationID) || d.Project != p.Project || !d.Global || !d.Review || d.Purpose != "global" || len(d.Inputs) != 1 || d.Inputs[0] != p.Source || len(d.Destinations) > 0 || d.Processing == nil {
			return false
		}
	}
	return p.TaskID != "" || p.Start != nil
}

// Validate lineage inside the same transaction as the CAS. Empty and partial
// text is allowed in a draft; a confirmation retains its own stricter gate.
func validateExperiencePreview(ctx context.Context, tx *sql.Tx, p records.ExperiencePreview) error {
	invalid := records.MergeError("invalid_preview")
	if !validExperiencePreview(p) {
		return invalid
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT s.kind FROM memories m JOIN scopes s ON s.id=m.dataset JOIN versions v ON v.memory=m.id AND v.revision=? WHERE m.id=? AND m.dataset=?`, p.Source.Revision, p.Source.Memory, p.Project).Scan(&kind); err != nil || kind != "project" {
		return records.MergeError("scope_denied")
	}
	var t records.OrganizationTask
	if p.TaskID != "" {
		var err error
		t, err = organizationTask(ctx, tx, p.TaskID)
		if err != nil {
			return err
		}
		if t.Project != p.Project || t.Snapshot.Purpose != "global" || len(t.Snapshot.Inputs) != 1 || t.Snapshot.Inputs[0].Memory != p.Source.Memory || t.Snapshot.Inputs[0].Revision != p.Source.Revision {
			return invalid
		}
	}
	entries := map[string]records.OrganizationEntry{}
	for _, e := range t.Entries {
		entries[e.ID] = e
	}
	if p.Candidate != "" {
		if _, ok := entries[p.Candidate]; !ok {
			return invalid
		}
	}
	for id, d := range p.Drafts {
		e, ok := entries[id]
		if !ok || e.Item.Action != "global_experience" || d.ProposalDigest != records.MergeDigest(e.Item) {
			return invalid
		}
		v := d.Value
		if len(v.Title) > 512 || len(v.Text) > records.MergeInputLimit || len(v.Conditions) > 4096 || !utf8.ValidString(v.Title+v.Text+v.Conditions) {
			return invalid
		}
		for i, r := range []*records.OrganizationReview{d.Request, d.RejectRequest} {
			if r == nil {
				continue
			}
			if r.Task != p.TaskID || r.Item != id || r.Digest != d.ProposalDigest || !previewIdentity(r.Receipt) || r.Confirm != (i == 0) || i == 0 && (r.Edit == nil || !r.Edit.Valid()) || i == 1 && r.Edit != nil {
				return invalid
			}
		}
	}
	return nil
}

func previewHead(ctx context.Context, tx *sql.Tx) (records.ExperiencePreviewState, string, error) {
	var out records.ExperiencePreviewState
	var current string
	err := tx.QueryRowContext(ctx, `SELECT identity,generation,current FROM experience_preview_head WHERE singleton=1`).Scan(&out.Identity, &out.Generation, &current)
	return out, current, err
}

// Get never starts or resumes work. Pending-start recovery looks up only the
// original operation receipt and reads the specified immutable source version.
func (s *Store) ExperiencePreview(ctx context.Context) (records.ExperiencePreviewState, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.ExperiencePreviewState{}, err
	}
	defer tx.Rollback()
	out, current, err := previewHead(ctx, tx)
	if err != nil || current == "" {
		return out, err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM experience_previews WHERE id=?`, current).Scan(&raw); err != nil {
		return out, err
	}
	var p records.ExperiencePreview
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return out, err
	}
	if err = validateExperiencePreview(ctx, tx, p); err != nil {
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
			p.TaskID = id
			p.Start = nil
		}
	}
	if p.TaskID != "" {
		t, e := organizationTask(ctx, tx, p.TaskID)
		if e != nil {
			return out, e
		}
		out.Task = &t
		if t.Project != p.Project || t.Snapshot.Purpose != "global" || len(t.Snapshot.Inputs) != 1 || t.Snapshot.Inputs[0].Memory != p.Source.Memory || t.Snapshot.Inputs[0].Revision != p.Source.Revision {
			return out, records.MergeError("invalid_preview")
		}
		input := t.Snapshot.Inputs[0].MergeInput
		out.Source = &input
	} else {
		input := records.MergeInput{Memory: p.Source.Memory, Revision: p.Source.Revision}
		if err = tx.QueryRowContext(ctx, `SELECT title,text,created_at FROM versions WHERE memory=? AND revision=?`, p.Source.Memory, p.Source.Revision).Scan(&input.Title, &input.Text, &input.CapturedAt); err != nil {
			return out, err
		}
		out.Source = &input
	}
	out.Preview = &p
	return out, tx.Commit()
}

func (s *Store) SaveExperiencePreview(ctx context.Context, w records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
	var empty records.ExperiencePreviewState
	if !previewIdentity(w.Identity) || !previewIdentity(w.Operation) || w.Generation < 0 {
		return empty, records.MergeError("invalid_preview")
	}
	raw, err := json.Marshal(w.Preview)
	if err != nil || len(raw) > 768*1024 {
		return empty, records.MergeError("invalid_preview")
	}
	digest := records.MergeDigest(w.Preview)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	out, _, err := previewHead(ctx, tx)
	if err != nil {
		return out, err
	}
	if out.Identity != w.Identity {
		return out, records.MergeError("preview_retired")
	}
	var operation, prior string
	if err = tx.QueryRowContext(ctx, `SELECT operation,digest FROM experience_preview_head WHERE singleton=1`).Scan(&operation, &prior); err != nil {
		return out, err
	}
	// An uncertain transport response can replay the exact last committed write.
	if operation == w.Operation {
		if prior != digest || out.Generation != w.Generation+1 {
			return out, ErrConflict
		}
		return out, nil
	}
	if out.Generation != w.Generation || out.Generation == math.MaxInt64 {
		return out, ErrConflict
	}
	if err = validateExperiencePreview(ctx, tx, w.Preview); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO experience_previews(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, w.Preview.ID, string(raw)); err != nil {
		return out, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE experience_preview_head SET generation=generation+1,current=?,operation=?,digest=? WHERE singleton=1 AND identity=? AND generation=?`, w.Preview.ID, w.Operation, digest, w.Identity, w.Generation)
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
