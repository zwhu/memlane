package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) StartOrganization(ctx context.Context, d records.OrganizationStart) (records.OrganizationTask, bool, error) {
	var empty records.OrganizationTask
	if len(d.Inputs) < 1 || len(d.Inputs) > 16 || strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 {
		return empty, false, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, d.Project).Scan(&kind); err != nil || kind != "project" {
		return empty, false, records.MergeError("scope_denied")
	}
	digest := records.MergeDigest(d)
	var id, prior string
	err = tx.QueryRowContext(ctx, `SELECT id,request_digest FROM organization_tasks WHERE operation_id=?`, d.OperationID).Scan(&id, &prior)
	if err == nil {
		if digest != prior {
			return empty, false, ErrConflict
		}
		t, e := organizationTask(ctx, tx, id)
		return t, false, e
	}
	if err != sql.ErrNoRows {
		return empty, false, err
	}
	snapshot := records.OrganizationSnapshot{Schema: records.OrganizationSchema, Task: uuid.NewString(), Project: d.Project, Attempt: 1}
	for _, ref := range d.Inputs {
		ok, e := organizationCurrent(ctx, tx, d.Project, []records.MergeSource{{Memory: ref.Memory, Revision: ref.Revision}})
		if e != nil {
			return empty, false, e
		}
		if !ok {
			return empty, false, records.MergeError("proposal_expired")
		}
		v, e := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, ref.Memory))
		if e != nil {
			return empty, false, e
		}
		roots, e := s.evidenceRoots(ctx, tx, v.Memory, v.Revision)
		if e != nil {
			return empty, false, e
		}
		origin := v.Origin
		var derived int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM derived_memories WHERE memory=?`, v.Memory).Scan(&derived); e != nil {
			return empty, false, e
		}
		if derived > 0 {
			origin = "summary"
		}
		snapshot.Inputs = append(snapshot.Inputs, records.OrganizationInput{MergeInput: records.MergeInput{Memory: v.Memory, Revision: v.Revision, Title: v.Title, Text: v.Text, Source: v.Source, SourceDetail: v.SourceDetail, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}, Origin: origin, Roots: roots})
	}
	if err = snapshot.Seal(); err != nil {
		return empty, false, err
	}
	raw, _ := json.Marshal(snapshot)
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_tasks(id,operation_id,request_digest,project,state) VALUES(?,?,?,?,'queued')`, snapshot.Task, d.OperationID, digest, d.Project); err != nil {
		return empty, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_attempts(task,attempt,state,snapshot) VALUES(?,1,'queued',?)`, snapshot.Task, string(raw)); err != nil {
		return empty, false, err
	}
	t, err := organizationTask(ctx, tx, snapshot.Task)
	if err != nil {
		return t, false, err
	}
	return t, true, tx.Commit()
}
func organizationTask(ctx context.Context, tx *sql.Tx, id string) (records.OrganizationTask, error) {
	var t records.OrganizationTask
	var raw, execution string
	err := tx.QueryRowContext(ctx, `SELECT t.id,t.project,t.state,t.attempt,t.cancelled,t.error_code,t.created_at,a.snapshot,a.execution FROM organization_tasks t JOIN organization_attempts a ON a.task=t.id AND a.attempt=(SELECT max(attempt) FROM organization_attempts WHERE task=t.id) WHERE t.id=?`, id).Scan(&t.ID, &t.Project, &t.State, &t.Attempt, &t.Cancelled, &t.Error, &t.CreatedAt, &raw, &execution)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(raw), &t.Snapshot); err != nil {
		return t, err
	}
	t.Execution = json.RawMessage(execution)
	err = tx.QueryRowContext(ctx, `SELECT payload FROM organization_results WHERE task=? ORDER BY attempt DESC LIMIT 1`, id).Scan(&raw)
	if err == nil {
		t.Result = new(records.OrganizationResult)
		if err = json.Unmarshal([]byte(raw), t.Result); err != nil {
			return t, err
		}
	} else if err != sql.ErrNoRows {
		return t, err
	}
	t.Entries = []records.OrganizationEntry{}
	rows, err := tx.QueryContext(ctx, `SELECT id,payload,state,error_code,receipt FROM organization_items WHERE task=? ORDER BY position`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry records.OrganizationEntry
		var payload, receipt string
		if err = rows.Scan(&entry.ID, &payload, &entry.State, &entry.Error, &receipt); err != nil {
			return t, err
		}
		if err = json.Unmarshal([]byte(payload), &entry.Item); err != nil {
			return t, err
		}
		if err = json.Unmarshal([]byte(receipt), &entry.Receipt); err != nil {
			return t, err
		}
		t.Entries = append(t.Entries, entry)
	}
	return t, rows.Err()
}
func (s *Store) OrganizationTask(ctx context.Context, id string) (records.OrganizationTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.OrganizationTask{}, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	for i, v := range t.Entries {
		if v.State == "pending" {
			ok, e := organizationCurrent(ctx, tx, t.Project, v.Item.Sources)
			if e != nil {
				return t, e
			}
			if !ok {
				if _, err = tx.ExecContext(ctx, `UPDATE organization_items SET state='expired',error_code='proposal_expired' WHERE id=? AND state='pending'`, v.ID); err != nil {
					return t, err
				}
				t.Entries[i].State = "expired"
				t.Entries[i].Error = "proposal_expired"
			}
		}
	}
	return t, tx.Commit()
}
func (s *Store) ClaimOrganization(ctx context.Context, id string, attempt int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE organization_tasks SET state='running' WHERE id=? AND attempt=? AND state='queued' AND cancelled=0`, id, attempt)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return records.MergeError("attempt_invalid")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_attempts SET state='running' WHERE task=? AND attempt=?`, id, attempt); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) StageOrganization(ctx context.Context, id string, attempt int64, raw []byte, execution any, failure string) (records.OrganizationTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.OrganizationTask{}, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if t.Cancelled || t.Attempt != attempt || t.Snapshot.Attempt != attempt {
		return t, records.MergeError("attempt_invalid")
	}
	if t.Result != nil {
		result, e := t.Snapshot.Decode(raw)
		if e != nil {
			return t, e
		}
		if records.MergeDigest(result) != records.MergeDigest(*t.Result) {
			return t, ErrConflict
		}
		return t, nil
	}
	if t.State != "running" {
		return t, records.MergeError("attempt_invalid")
	}
	var result records.OrganizationResult
	if failure == "" {
		result, err = t.Snapshot.Decode(raw)
		if err != nil {
			failure = "invalid_proposal"
		}
	}
	state := "failed"
	if failure == "" {
		state = "applying"
		payload, _ := json.Marshal(result)
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_results(task,attempt,id,digest,payload) VALUES(?,?,?,?,?)`, id, attempt, result.ID, records.MergeDigest(result), string(payload)); err != nil {
			return t, err
		}
		for _, item := range result.Items {
			b, _ := json.Marshal(item)
			if _, err = tx.ExecContext(ctx, `INSERT INTO organization_items(id,task,position,action,digest,payload,state) VALUES(?,?,?,?,?,?,'pending')`, t.Snapshot.ItemID(item.Index), id, item.Index, item.Action, records.MergeDigest(item), string(b)); err != nil {
				return t, err
			}
		}
	}
	b, _ := json.Marshal(execution)
	if _, err = tx.ExecContext(ctx, `UPDATE organization_tasks SET state=?,error_code=? WHERE id=?`, state, failure, id); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_attempts SET state=?,execution=? WHERE task=? AND attempt=?`, state, string(b), id, attempt); err != nil {
		return t, err
	}
	t, err = organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

// ApplyOrganizationItem commits one complete item. Receipt replay precedes
// current-source checks so later source edits cannot undo an earlier fact.
func (s *Store) ApplyOrganizationItem(ctx context.Context, id string, attempt int64, position int) (records.OrganizationEntry, error) {
	var entry records.OrganizationEntry
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return entry, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return entry, err
	}
	for _, v := range t.Entries {
		if v.Item.Index == position {
			entry = v
			break
		}
	}
	if entry.ID == "" {
		return entry, records.MergeError("not_found")
	}
	if entry.State == "applied" {
		return entry, nil
	}
	if t.Cancelled || t.Attempt != attempt || t.State != "applying" {
		return entry, records.MergeError("attempt_invalid")
	}
	if entry.State != "pending" {
		return entry, nil
	}
	ok, err := organizationCurrent(ctx, tx, t.Project, entry.Item.Sources)
	if err != nil {
		return entry, err
	}
	if !ok {
		_, err = tx.ExecContext(ctx, `UPDATE organization_items SET state='expired',error_code='proposal_expired' WHERE id=?`, entry.ID)
		if err != nil {
			return entry, err
		}
		entry.State = "expired"
		entry.Error = "proposal_expired"
		return entry, tx.Commit()
	}
	receipt := records.OrganizationReceipt{ID: entry.ID}
	if entry.Item.Action == "summary" {
		refs := []string{}
		for _, ref := range entry.Item.Sources {
			refs = append(refs, ref.Memory+"@v"+stringRevision(ref.Revision))
		}
		v, e := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "organization-v1:" + entry.ID, Memory: entry.ID, Dataset: t.Project, Text: entry.Item.Text, Title: entry.Item.Title, Source: "traex：自动摘要", Origin: "knowledge", SourceDetail: &records.AgentSource{References: refs}})
		if e != nil {
			return entry, e
		}
		receipt.Memory = v.Memory
		receipt.Revision = v.Revision
		if _, err = tx.ExecContext(ctx, `INSERT INTO derived_memories(memory,revision,state) VALUES(?,?,'valid')`, v.Memory, v.Revision); err != nil {
			return entry, err
		}
		for _, ref := range entry.Item.Sources {
			var source records.OrganizationInput
			for _, candidate := range t.Snapshot.Inputs {
				if candidate.Memory == ref.Memory {
					source = candidate
					break
				}
			}
			b, _ := json.Marshal(source)
			if _, err = tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,?,?,?,?,?,?)`, v.Memory, v.Revision, ref.Memory, ref.Revision, t.Project, string(b), ref.Quote); err != nil {
				return entry, err
			}
		}
	} else if entry.Item.Action == "add_tags" {
		var revision int64
		if err = tx.QueryRowContext(ctx, `SELECT current_revision FROM memories WHERE id=?`, entry.Item.Target).Scan(&revision); err != nil {
			return entry, err
		}
		receipt.Memory = entry.Item.Target
		receipt.Revision = revision
		sources, _ := json.Marshal(entry.Item.Sources)
		for _, tag := range entry.Item.Tags {
			result, e := tx.ExecContext(ctx, `INSERT INTO memory_tags(memory,tag) VALUES(?,?) ON CONFLICT DO NOTHING`, entry.Item.Target, tag)
			if e != nil {
				return entry, e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return entry, e
			}
			if n == 1 {
				receipt.AddedTags = append(receipt.AddedTags, tag)
				if _, err = tx.ExecContext(ctx, `INSERT INTO tag_events(item,memory,revision,tag,sources) VALUES(?,?,?,?,?)`, entry.ID, entry.Item.Target, revision, tag, string(sources)); err != nil {
					return entry, err
				}
			}
		}
	} else {
		return entry, records.MergeError("invalid_proposal")
	}
	b, _ := json.Marshal(receipt)
	if _, err = tx.ExecContext(ctx, `UPDATE organization_items SET state='applied',receipt=? WHERE id=? AND state='pending'`, string(b), entry.ID); err != nil {
		return entry, err
	}
	entry.State = "applied"
	entry.Receipt = &receipt
	return entry, tx.Commit()
}
func stringRevision(v int64) string { b, _ := json.Marshal(v); return string(b) }
func (s *Store) FailOrganizationItem(ctx context.Context, id string, attempt int64, position int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE organization_items SET state='failed',error_code='service_error' WHERE task=? AND position=? AND state='pending' AND EXISTS(SELECT 1 FROM organization_tasks t WHERE t.id=organization_items.task AND t.attempt=? AND t.cancelled=0 AND t.state='applying')`, id, position, attempt)
	return err
}
func (s *Store) FinishOrganization(ctx context.Context, id string, attempt int64) (records.OrganizationTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.OrganizationTask{}, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if t.Cancelled || t.Attempt != attempt {
		return t, records.MergeError("attempt_invalid")
	}
	if t.State == "completed" || t.State == "partial_failed" {
		return t, nil
	}
	if t.State != "applying" {
		return t, records.MergeError("attempt_invalid")
	}
	state := "completed"
	for _, entry := range t.Entries {
		if entry.State == "pending" {
			return t, records.MergeError("attempt_invalid")
		}
		if entry.State != "applied" {
			state = "partial_failed"
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_tasks SET state=? WHERE id=?`, state, id); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_attempts SET state=? WHERE task=? AND attempt=?`, state, id, attempt); err != nil {
		return t, err
	}
	t, err = organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) CancelOrganization(ctx context.Context, id string) (records.OrganizationTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.OrganizationTask{}, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if t.State == "completed" || t.State == "partial_failed" || t.State == "failed" || t.Cancelled {
		return t, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_tasks SET cancelled=1,state='cancelled',attempt=attempt+1 WHERE id=?`, id); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_attempts SET state='cancelled' WHERE task=? AND state IN ('queued','running','applying')`, id); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_items SET state='cancelled' WHERE task=? AND state='pending'`, id); err != nil {
		return t, err
	}
	t, err = organizationTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) InterruptOrganizations(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE organization_attempts SET state='interrupted' WHERE state IN ('queued','running','applying'); UPDATE organization_tasks SET state='interrupted',attempt=attempt+1,error_code='service_interrupted' WHERE state IN ('queued','running','applying')`)
	return err
}
func (s *Store) ResumeOrganization(ctx context.Context, d records.OrganizationResume) (records.OrganizationTask, bool, error) {
	var empty records.OrganizationTask
	if strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 || d.ExpectedAttempt < 1 {
		return empty, false, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	t, err := organizationTask(ctx, tx, d.Task)
	if err != nil {
		return t, false, err
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM organization_attempts WHERE task=? AND resume_id=?`, d.Task, d.OperationID).Scan(&prior)
	if err == nil {
		if prior != records.MergeDigest(d) {
			return t, false, ErrConflict
		}
		return t, false, nil
	}
	if err != sql.ErrNoRows {
		return t, false, err
	}
	if t.Cancelled {
		return t, false, records.MergeError("task_cancelled")
	}
	if t.Attempt != d.ExpectedAttempt {
		return t, false, ErrConflict
	}
	if t.Result == nil || (t.State != "partial_failed" && t.State != "interrupted") {
		return t, false, records.MergeError("invalid_request")
	}
	snapshot := t.Snapshot
	snapshot.Attempt = t.Attempt + 1
	if err = snapshot.Seal(); err != nil {
		return t, false, err
	}
	b, _ := json.Marshal(snapshot)
	if _, err = tx.ExecContext(ctx, `UPDATE organization_tasks SET state='applying',attempt=?,error_code='' WHERE id=?`, snapshot.Attempt, t.ID); err != nil {
		return t, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_attempts(task,attempt,state,kind,resume_id,request_digest,snapshot) VALUES(?,?,'applying','apply',?,?,?)`, t.ID, snapshot.Attempt, d.OperationID, records.MergeDigest(d), string(b)); err != nil {
		return t, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_items SET state='pending',error_code='' WHERE task=? AND state='failed'`, t.ID); err != nil {
		return t, false, err
	}
	t, err = organizationTask(ctx, tx, t.ID)
	if err != nil {
		return t, false, err
	}
	return t, true, tx.Commit()
}

// InterruptOrganization fences only the dead owner. Receipts already committed
// survive; a later read retries this transition if persistence was unavailable.
func (s *Store) InterruptOrganization(ctx context.Context, id string, attempt int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE organization_tasks SET state='interrupted',attempt=attempt+1,error_code='service_interrupted' WHERE id=? AND attempt=? AND state IN ('queued','running','applying') AND cancelled=0`, id, attempt)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE organization_attempts SET state='interrupted' WHERE task=? AND attempt=?`, id, attempt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
