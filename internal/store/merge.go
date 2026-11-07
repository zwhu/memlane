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

func (s *Store) StartMerge(ctx context.Context, d records.MergeStart) (records.MergeTask, bool, error) {
	if strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 || len(d.Inputs) != 2 || d.Inputs[0].Memory == d.Inputs[1].Memory {
		return records.MergeTask{}, false, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.MergeTask{}, false, err
	}
	defer tx.Rollback()
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, d.Project).Scan(&kind); err != nil || kind != "project" {
		return records.MergeTask{}, false, records.MergeError("scope_denied")
	}
	digest := records.MergeDigest(d)
	var id, prior string
	err = tx.QueryRowContext(ctx, `SELECT id,request_digest FROM merge_tasks WHERE operation_id=?`, d.OperationID).Scan(&id, &prior)
	if err == nil {
		if prior != digest {
			return records.MergeTask{}, false, ErrConflict
		}
		t, e := mergeTask(ctx, tx, id)
		return t, false, e
	}
	if err != sql.ErrNoRows {
		return records.MergeTask{}, false, err
	}
	x := records.MergeSnapshot{Schema: records.MergeSchema, Task: uuid.NewString(), Attempt: 1, Project: d.Project}
	for _, target := range d.Inputs {
		v, e := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, target.Memory))
		if e != nil {
			return records.MergeTask{}, false, e
		}
		var stale bool
		if stale, e = mergeDerivedStale(ctx, tx, v.Memory, v.Revision); e != nil {
			return records.MergeTask{}, false, e
		}
		if stale || v.Dataset != d.Project || v.State != "active" || v.Origin == "raw" || v.Revision != target.Revision {
			return records.MergeTask{}, false, records.MergeError("proposal_expired")
		}
		if s.Schema >= 5 {
			if e = filterVersionProvenance(ctx, tx, &v, []string{d.Project}); e != nil {
				return records.MergeTask{}, false, e
			}
		}
		x.Inputs = append(x.Inputs, records.MergeInput{Memory: v.Memory, Revision: v.Revision, Title: v.Title, Text: v.Text, Source: v.Source, SourceDetail: v.SourceDetail, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	if err = x.Seal(); err != nil {
		return records.MergeTask{}, false, err
	}
	b, _ := json.Marshal(x)
	if _, err = tx.ExecContext(ctx, `INSERT INTO merge_tasks(id,operation_id,request_digest,project,state) VALUES(?,?,?,?,'queued')`, x.Task, d.OperationID, digest, d.Project); err != nil {
		return records.MergeTask{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO merge_attempts(task,attempt,state,snapshot) VALUES(?,1,'queued',?)`, x.Task, string(b)); err != nil {
		return records.MergeTask{}, false, err
	}
	t, err := mergeTask(ctx, tx, x.Task)
	if err != nil {
		return t, false, err
	}
	return t, true, tx.Commit()
}

func mergeTask(ctx context.Context, tx *sql.Tx, id string) (records.MergeTask, error) {
	var t records.MergeTask
	var raw, execution string
	err := tx.QueryRowContext(ctx, `SELECT t.id,t.project,t.state,t.attempt,t.cancelled,t.error_code,a.snapshot,a.execution FROM merge_tasks t JOIN merge_attempts a ON a.task=t.id AND a.attempt=(SELECT max(attempt) FROM merge_attempts WHERE task=t.id) WHERE t.id=?`, id).Scan(&t.ID, &t.Project, &t.State, &t.Attempt, &t.Cancelled, &t.Error, &raw, &execution)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(raw), &t.Snapshot); err != nil {
		return t, err
	}
	t.Execution = json.RawMessage(execution)
	var payload string
	err = tx.QueryRowContext(ctx, `SELECT payload,digest,state FROM merge_proposals WHERE task=?`, id).Scan(&payload, &t.ResultDigest, &t.ReviewState)
	if err == sql.ErrNoRows {
		t.ReviewState = "none"
		return t, nil
	}
	if err != nil {
		return t, err
	}
	t.Proposal = new(records.MergeProposal)
	if err = json.Unmarshal([]byte(payload), t.Proposal); err != nil {
		return t, err
	}
	r := new(records.MergeReceipt)
	err = tx.QueryRowContext(ctx, `SELECT id,proposal,digest,memory,revision FROM merge_receipts WHERE proposal=?`, t.Proposal.ID).Scan(&r.ID, &r.Proposal, &r.Digest, &r.Memory, &r.Revision)
	if err == nil {
		t.Receipt = r
	}
	if err == sql.ErrNoRows {
		err = nil
	}
	return t, err
}
func mergeCurrent(ctx context.Context, tx *sql.Tx, x records.MergeSnapshot) (bool, error) {
	for _, v := range x.Inputs {
		var revision int64
		var project, state string
		var stale bool
		if err := tx.QueryRowContext(ctx, `SELECT current_revision,dataset,state FROM memories WHERE id=?`, v.Memory).Scan(&revision, &project, &state); err != nil {
			return false, err
		}
		var err error
		if stale, err = mergeDerivedStale(ctx, tx, v.Memory, revision); err != nil {
			return false, err
		}
		if stale || revision != v.Revision || project != x.Project || state != "active" {
			return false, nil
		}
	}
	return true, nil
}
func (s *Store) MergeTask(ctx context.Context, id string) (records.MergeTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.MergeTask{}, err
	}
	defer tx.Rollback()
	t, err := mergeTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if t.ReviewState == "pending" {
		ok, e := mergeCurrent(ctx, tx, t.Snapshot)
		if e != nil {
			return t, e
		}
		if !ok {
			if _, err = tx.ExecContext(ctx, `UPDATE merge_proposals SET state='expired' WHERE task=? AND state='pending'`, id); err != nil {
				return t, err
			}
			t.ReviewState = "expired"
		}
	}
	return t, tx.Commit()
}
func (s *Store) ClaimMerge(ctx context.Context, id string, attempt int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE merge_tasks SET state='running' WHERE id=? AND attempt=? AND state='queued' AND cancelled=0`, id, attempt)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return records.MergeError("attempt_invalid")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE merge_attempts SET state='running' WHERE task=? AND attempt=?`, id, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteMerge registers a pure result after network/process work has ended.
func (s *Store) CompleteMerge(ctx context.Context, id string, attempt int64, raw []byte, execution any, failure string) (records.MergeTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.MergeTask{}, err
	}
	defer tx.Rollback()
	t, err := mergeTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if t.Cancelled || t.Attempt != attempt || (t.State != "running" && t.State != "completed") {
		return t, records.MergeError("attempt_invalid")
	}
	if t.State == "completed" {
		p, e := t.Snapshot.Decode(raw)
		if e != nil {
			return t, e
		}
		if t.Proposal == nil || records.MergeDigest(p) != t.ResultDigest {
			return t, ErrConflict
		}
		return t, nil
	}
	state := "completed"
	if failure != "" {
		state = "failed"
	} else {
		p, e := t.Snapshot.Decode(raw)
		if e != nil {
			return t, e
		}
		payload, _ := json.Marshal(p)
		review := "pending"
		ok, e := mergeCurrent(ctx, tx, t.Snapshot)
		if e != nil {
			return t, e
		}
		if !ok {
			review = "expired"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO merge_proposals(id,task,attempt,digest,payload,state) VALUES(?,?,?,?,?,?)`, p.ID, id, attempt, records.MergeDigest(p), string(payload), review); err != nil {
			return t, err
		}
	}
	b, e := json.Marshal(execution)
	if e != nil {
		return t, e
	}
	if _, err = tx.ExecContext(ctx, `UPDATE merge_tasks SET state=?,error_code=? WHERE id=?`, state, failure, id); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE merge_attempts SET state=?,execution=? WHERE task=? AND attempt=?`, state, string(b), id, attempt); err != nil {
		return t, err
	}
	t, err = mergeTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

// Invalidate interrupted attempts, including work queued before shutdown. No
// automatic replay or retry can start a new paid model run on service restart.
func (s *Store) InterruptMerges(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE merge_attempts SET state='interrupted' WHERE state IN ('queued','running'); UPDATE merge_tasks SET state='interrupted',attempt=attempt+1,error_code='service_interrupted' WHERE state IN ('queued','running')`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CancelMerge(ctx context.Context, id string) (records.MergeTask, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.MergeTask{}, err
	}
	defer tx.Rollback()
	t, err := mergeTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	// Applied facts and reviewed decisions survive cancellation.
	if _, err = tx.ExecContext(ctx, `UPDATE merge_tasks SET cancelled=1,state=CASE WHEN state IN ('queued','running') THEN 'cancelled' ELSE state END,attempt=CASE WHEN cancelled=0 AND state IN ('queued','running') THEN attempt+1 ELSE attempt END WHERE id=?`, id); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE merge_attempts SET state='cancelled' WHERE task=? AND state IN ('queued','running'); UPDATE merge_proposals SET state='cancelled' WHERE task=? AND state='pending'`, id, id); err != nil {
		return t, err
	}
	t, err = mergeTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func (s *Store) ReviewMerge(ctx context.Context, r records.MergeReview, confirm bool) (records.MergeTask, error) {
	if strings.TrimSpace(r.Receipt) == "" || len(r.Receipt) > 128 || r.Digest == "" {
		return records.MergeTask{}, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.MergeTask{}, err
	}
	defer tx.Rollback()
	t, err := mergeTask(ctx, tx, r.Task)
	if err != nil {
		return t, err
	}
	if t.Proposal == nil || t.Proposal.ID != r.Proposal || t.ResultDigest != r.Digest {
		return t, ErrConflict
	}
	decision := "reject"
	if confirm {
		decision = "confirm"
	}
	var priorDecision string
	err = tx.QueryRowContext(ctx, `SELECT decision FROM merge_decisions WHERE id=? AND proposal=? AND digest=?`, r.Receipt, r.Proposal, r.Digest).Scan(&priorDecision)
	if err == nil && priorDecision != decision {
		return t, ErrConflict
	}
	if err != nil && err != sql.ErrNoRows {
		return t, err
	}
	var claimed int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM merge_decisions WHERE id=? AND (proposal!=? OR digest!=? OR decision!=?)`, r.Receipt, r.Proposal, r.Digest, decision).Scan(&claimed); err != nil {
		return t, err
	}
	if claimed > 0 {
		return t, ErrConflict
	}
	recordDecision := func() error {
		_, e := tx.ExecContext(ctx, `INSERT INTO merge_decisions(id,proposal,digest,decision) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, r.Receipt, r.Proposal, r.Digest, decision)
		return e
	}
	// Receipt identifiers are global to applications and cannot be rebound even
	// when this proposal already has a durable receipt under another identifier.
	var prior, digest string
	err = tx.QueryRowContext(ctx, `SELECT proposal,digest FROM merge_receipts WHERE id=?`, r.Receipt).Scan(&prior, &digest)
	if err == nil && (prior != r.Proposal || digest != r.Digest) {
		return t, ErrConflict
	}
	if err != nil && err != sql.ErrNoRows {
		return t, err
	}
	if t.ReviewState == "applied" {
		if !confirm {
			return t, ErrConflict
		}
		if err = recordDecision(); err != nil {
			return t, err
		}
		return t, tx.Commit()
	}
	if t.ReviewState == "rejected" {
		if confirm {
			return t, ErrConflict
		}
		if err = recordDecision(); err != nil {
			return t, err
		}
		return t, tx.Commit()
	}
	if t.ReviewState == "expired" {
		return t, records.MergeError("proposal_expired")
	}
	if t.Cancelled || t.ReviewState == "cancelled" {
		return t, records.MergeError("task_cancelled")
	}
	if t.ReviewState != "pending" || t.Attempt != t.Proposal.Attempt {
		return t, records.MergeError("attempt_invalid")
	}
	ok, err := mergeCurrent(ctx, tx, t.Snapshot)
	if err != nil {
		return t, err
	}
	if !ok {
		if _, err = tx.ExecContext(ctx, `UPDATE merge_proposals SET state='expired' WHERE task=?`, r.Task); err != nil {
			return t, err
		}
		if err = tx.Commit(); err != nil {
			return t, err
		}
		return t, records.MergeError("proposal_expired")
	}
	if !confirm {
		if err = recordDecision(); err != nil {
			return t, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE merge_proposals SET state='rejected' WHERE task=?`, r.Task); err != nil {
			return t, err
		}
		t, err = mergeTask(ctx, tx, r.Task)
		if err != nil {
			return t, err
		}
		return t, tx.Commit()
	}
	if err = recordDecision(); err != nil {
		return t, err
	}
	p := t.Proposal
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("merge-v1-memory:"+p.ID)).String()
	v, err := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "merge-v1:" + p.ID, Dataset: t.Project, Memory: id, Title: p.Title, Text: p.Text, Source: "traex：用户确认合并", Origin: "knowledge"})
	if err != nil {
		return t, err
	}
	for i, source := range t.Snapshot.Inputs {
		b, _ := json.Marshal(source)
		if _, err = tx.ExecContext(ctx, `INSERT INTO merge_sources(memory,revision,source_memory,source_revision,snapshot,quote) VALUES(?,?,?,?,?,?)`, id, v.Revision, source.Memory, source.Revision, string(b), p.Sources[i].Quote); err != nil {
			return t, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO versions(memory,revision,text,operation,title) VALUES(?,?,?,'merge_archive',?)`, source.Memory, source.Revision+1, source.Text, source.Title); err != nil {
			return t, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memories SET current_revision=?,state='archived',indexed_revision=0,generation='' WHERE id=?`, source.Revision+1, source.Memory); err != nil {
			return t, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='superseded' WHERE memory=? AND state IN ('pending','failed','indexing')`, source.Memory); err != nil {
			return t, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO merge_receipts(id,proposal,digest,memory,revision) VALUES(?,?,?,?,?)`, r.Receipt, p.ID, r.Digest, id, v.Revision); err != nil {
		return t, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE merge_proposals SET state='applied' WHERE task=?`, r.Task); err != nil {
		return t, err
	}
	t, err = mergeTask(ctx, tx, r.Task)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

// Experimental/legacy v2-v3 stores have no derived summaries. Keep their merge
// contract usable while formal v4 always enforces the derived validity fence.
func mergeDerivedStale(ctx context.Context, tx *sql.Tx, id string, revision int64) (bool, error) {
	var present, stale bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='derived_memories')`).Scan(&present); err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM derived_memories WHERE memory=? AND revision=? AND state='stale')`, id, revision).Scan(&stale)
	return stale, err
}
