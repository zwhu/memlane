package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/internal/records"
)

func organizationItemCurrent(ctx context.Context, tx *sql.Tx, x records.OrganizationSnapshot, item records.OrganizationItem) (bool, error) {
	ok, err := organizationCurrent(ctx, tx, x.Project, item.Sources)
	if err != nil || !ok {
		return ok, err
	}
	if item.Action == "move" || item.Action == "global_experience" {
		for _, d := range x.Destinations {
			if d.ID == item.Destination {
				if item.Action == "global_experience" {
					var count int
					err = tx.QueryRowContext(ctx, `SELECT count(*) FROM scopes WHERE id=? AND kind='experience'`, d.ID).Scan(&count)
					return count == 1, err
				}
				var count int
				err = tx.QueryRowContext(ctx, `SELECT count(*) FROM scopes WHERE id=? AND kind='project' AND path=?`, d.ID, d.Path).Scan(&count)
				return count == 1, err
			}
		}
		return false, nil
	}
	return true, nil
}

func (s *Store) ReviewOrganization(ctx context.Context, d records.OrganizationReview) (records.OrganizationTask, error) {
	var empty records.OrganizationTask
	if strings.TrimSpace(d.Receipt) == "" || len(d.Receipt) > 128 || d.Digest == "" || len(d.RejectionReason) > 1024 || !utf8.ValidString(d.RejectionReason) || d.Confirm && d.RejectionReason != "" {
		return empty, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, d.Task)
	if err != nil {
		return t, err
	}
	var entry records.OrganizationEntry
	found := false
	for _, e := range t.Entries {
		if e.ID == d.Item && e.Item.RequiresReview() {
			entry = e
			found = true
		}
	}
	if !found || records.MergeDigest(entry.Item) != d.Digest {
		return t, ErrConflict
	}
	if d.Edit != nil && (!d.Confirm || entry.Item.Action != "global_experience" || !d.Edit.Valid()) {
		return t, records.MergeError("invalid_request")
	}
	decision := "reject"
	if d.Confirm {
		decision = "confirm"
	}
	var priorItem, priorDigest, priorDecision, priorReceipt string
	sqlDecision := `SELECT item,digest,decision,receipt FROM organization_decisions WHERE id=?`
	decisionArgs := []any{d.Receipt}
	if s.Schema >= 6 {
		sqlDecision += ` UNION ALL SELECT item,digest,decision,receipt FROM global_experience_decisions WHERE id=?`
		decisionArgs = append(decisionArgs, d.Receipt)
	}
	err = tx.QueryRowContext(ctx, sqlDecision, decisionArgs...).Scan(&priorItem, &priorDigest, &priorDecision, &priorReceipt)
	if err == nil {
		if priorItem != d.Item || priorDigest != d.Digest || priorDecision != decision {
			return t, ErrConflict
		}
		var saved records.OrganizationReceipt
		if err = json.Unmarshal([]byte(priorReceipt), &saved); err != nil {
			return t, err
		}
		if records.MergeDigest(saved.EditedExperience) != records.MergeDigest(d.Edit) || saved.RejectionReason != d.RejectionReason || saved.MoveScopeAccepted != d.MoveScopeAccepted {
			return t, ErrConflict
		}
		return t, nil
	}
	if err != sql.ErrNoRows {
		return t, err
	}
	// Previously committed receipts remain replayable; a new move requires the
	// separate review-time consent, not only the task's destination selection.
	if d.Confirm && entry.Item.Action == "move" && !d.MoveScopeAccepted {
		return t, records.MergeError("move_scope_confirmation_required")
	}
	if d.MoveScopeAccepted && (!d.Confirm || entry.Item.Action != "move") {
		return t, records.MergeError("invalid_request")
	}
	if entry.State == "applied" || entry.State == "rejected" {
		return t, ErrConflict
	}
	if t.Cancelled || entry.State == "cancelled" {
		return t, records.MergeError("task_cancelled")
	}
	if entry.State != "review_pending" {
		return t, records.MergeError("proposal_expired")
	}
	ok, err := organizationItemCurrent(ctx, tx, t.Snapshot, entry.Item)
	if err != nil {
		return t, err
	}
	if !ok {
		if _, err = tx.ExecContext(ctx, `UPDATE `+organizationReviewTable(entry.Item.Action)+` SET state='expired',error_code='proposal_expired' WHERE id=?`, entry.ID); err != nil {
			return t, err
		}
		if err = tx.Commit(); err != nil {
			return t, err
		}
		return t, records.MergeError("proposal_expired")
	}
	receipt := records.OrganizationReceipt{ID: d.Receipt, RejectionReason: d.RejectionReason}
	state := "rejected"
	if d.Confirm {
		state = "applied"
		confirmed := entry
		if d.Edit != nil {
			confirmed.Item.Title, confirmed.Item.Text, confirmed.Item.Conditions = d.Edit.Title, d.Edit.Text, d.Edit.Conditions
		}
		if receipt, err = s.applyOrganizationReview(ctx, tx, t, confirmed, d.Receipt); err != nil {
			return t, err
		}
		receipt.EditedExperience = d.Edit
		receipt.MoveScopeAccepted = d.MoveScopeAccepted
	}
	encoded, _ := json.Marshal(receipt)
	if _, err = tx.ExecContext(ctx, `INSERT INTO `+organizationDecisionTable(entry.Item.Action)+`(id,item,digest,decision,receipt) VALUES(?,?,?,?,?)`, d.Receipt, d.Item, d.Digest, decision, string(encoded)); err != nil {
		return t, err
	}
	persisted := string(encoded)
	if _, err = tx.ExecContext(ctx, `UPDATE `+organizationReviewTable(entry.Item.Action)+` SET state=?,receipt=? WHERE id=? AND state='review_pending'`, state, persisted, entry.ID); err != nil {
		return t, err
	}
	t, err = organizationTask(ctx, tx, t.ID)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func (s *Store) applyOrganizationReview(ctx context.Context, tx *sql.Tx, t records.OrganizationTask, e records.OrganizationEntry, id string) (records.OrganizationReceipt, error) {
	receipt := records.OrganizationReceipt{ID: id}
	item := e.Item
	if item.Action == "global_experience" {
		return s.applyGlobalExperience(ctx, tx, t, e, id)
	}
	if item.Action == "duplicate" || item.Action == "conflict" {
		return receipt, nil
	}
	var target records.OrganizationInput
	for _, v := range t.Snapshot.Inputs {
		if v.Memory == item.Target {
			target = v
		}
	}
	m := Mutation{OperationID: "organization-review:" + e.ID, Dataset: t.Project, Memory: item.Target, ExpectedRevision: target.Revision, Kind: "edit", Text: item.Text, Title: item.Title}
	switch item.Action {
	case "merge":
		m.Kind = "save"
		m.Memory = e.ID
		m.ExpectedRevision = 0
		m.Origin = "knowledge"
		m.Source = organizationProducer(t) + "：用户确认整理合并"
	case "overwrite":
	case "delete":
		m.Kind = "delete"
	case "move":
		m.Text = target.Text
		m.Title = target.Title
	default:
		return receipt, records.MergeError("invalid_proposal")
	}
	v, err := s.applyTx(ctx, tx, m)
	if err != nil {
		return receipt, err
	}
	receipt.Memory = v.Memory
	receipt.Revision = v.Revision
	// Every confirmed change records immutable versioned material, including the
	// target's previous revision. This association never grants cross-scope read.
	// applyTx carries prior lineage for ordinary user edits. This approved
	// revision instead binds exactly its frozen proposal sources; the target's
	// previous revision remains a source, preserving older lineage transitively.
	if _, err = tx.ExecContext(ctx, `DELETE FROM organization_sources WHERE memory=? AND revision=?`, v.Memory, v.Revision); err != nil {
		return receipt, err
	}
	for _, ref := range item.Sources {
		var source records.OrganizationInput
		for _, input := range t.Snapshot.Inputs {
			if input.Memory == ref.Memory {
				source = input
			}
		}
		raw, _ := json.Marshal(source)
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,?,?,?,?,?,?)`, v.Memory, v.Revision, ref.Memory, ref.Revision, t.Project, string(raw), ref.Quote); err != nil {
			return receipt, err
		}
	}
	if item.Action == "merge" {
		for _, ref := range item.Sources {
			var source records.OrganizationInput
			for _, input := range t.Snapshot.Inputs {
				if input.Memory == ref.Memory {
					source = input
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO versions(memory,revision,text,operation,title) VALUES(?,?,?,'merge_archive',?)`, ref.Memory, ref.Revision+1, source.Text, source.Title); err != nil {
				return receipt, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE memories SET current_revision=?,state='archived',indexed_revision=0,generation='' WHERE id=?`, ref.Revision+1, ref.Memory); err != nil {
				return receipt, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='superseded' WHERE memory=? AND state IN ('pending','failed','indexing')`, ref.Memory); err != nil {
				return receipt, err
			}
		}
	}
	if item.Action == "move" {
		if _, err = tx.ExecContext(ctx, `UPDATE versions SET operation='move' WHERE memory=? AND revision=?`, v.Memory, v.Revision); err != nil {
			return receipt, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memories SET dataset=? WHERE id=?`, item.Destination, v.Memory); err != nil {
			return receipt, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scope_history(memory,revision,previous_scope,scope,item) VALUES(?,?,?,?,?)`, v.Memory, v.Revision, t.Project, item.Destination, e.ID); err != nil {
			return receipt, err
		}
	}
	return receipt, nil
}

// Current-scope authorization does not grant access to the initial body's old
// scope. Trusted App history keeps the original; daily Agents see it only when
// that original scope is also in the explicitly requested allowed scopes.
func (s *Store) FilterAgentVersion(ctx context.Context, v *records.Version, scopes []string) error {
	if s.Schema < 5 {
		return nil
	}
	return filterVersionProvenance(ctx, s.DB, v, scopes)
}

type provenanceReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func filterVersionProvenance(ctx context.Context, q provenanceReader, v *records.Version, scopes []string) error {
	var originalScope string
	err := q.QueryRowContext(ctx, `SELECT previous_scope FROM scope_history WHERE memory=? ORDER BY revision LIMIT 1`, v.Memory).Scan(&originalScope)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if scope == originalScope {
			return nil
		}
	}
	v.Original = ""
	v.SourceDetail = nil
	v.Source = "项目移动 · 原范围出处未授权"
	return nil
}

func organizationReviewTable(action string) string {
	if action == "global_experience" {
		return "global_experience_reviews"
	}
	return "organization_reviews"
}
func organizationDecisionTable(action string) string {
	if action == "global_experience" {
		return "global_experience_decisions"
	}
	return "organization_decisions"
}
