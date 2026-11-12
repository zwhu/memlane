package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

const officialCodeJobFormat = "official_code_job_v1"

// The existing task table owns uniqueness, history and generations. Tagged
// metadata extends its JSON fields without turning official receipts into
// legacy graph snapshots or creating a second job database.
type officialCodeManifest struct {
	codegraph.Manifest
	Format  string                     `json:"official_code_format"`
	Scope   *records.OfficialCodeScope `json:"official_code_scope"`
	Project string                     `json:"official_code_project"`
	Epoch   string                     `json:"official_code_epoch,omitempty"`
}
type officialCodeReceipt struct {
	Format  string                       `json:"official_code_format"`
	Receipt *records.OfficialCodeReceipt `json:"official_code_receipt"`
}

func decodeCodeReceipt(raw string) (*codegraph.Receipt, *records.OfficialCodeReceipt, error) {
	var tagged officialCodeReceipt
	if err := json.Unmarshal([]byte(raw), &tagged); err != nil {
		return nil, nil, err
	}
	if tagged.Format != "" || tagged.Receipt != nil {
		if tagged.Format != officialCodeJobFormat || tagged.Receipt == nil {
			return nil, nil, errors.New("invalid official code receipt format")
		}
		return nil, tagged.Receipt, nil
	}
	var legacy *codegraph.Receipt
	err := json.Unmarshal([]byte(raw), &legacy)
	return legacy, nil, err
}

func readCodeJob(row codeScanner) (records.CodeJob, error) {
	var j records.CodeJob
	var m, r string
	if err := row.Scan(&j.ID, &j.WorktreeID, &j.Generation, &j.State, &m, &r, &j.Error, &j.CreatedMS, &j.FinishedMS); err != nil {
		return j, err
	}
	if err := json.Unmarshal([]byte(m), &j.Manifest); err != nil {
		return j, err
	}
	var tagged officialCodeManifest
	if err := json.Unmarshal([]byte(m), &tagged); err != nil {
		return j, err
	}
	if tagged.Format != "" || tagged.Scope != nil {
		if tagged.Format != officialCodeJobFormat || tagged.Scope == nil {
			return j, errors.New("invalid official code job format")
		}
		j.Official = tagged.Scope
		j.Project = tagged.Project
		j.ExecutionEpoch = tagged.Epoch
		if j.ExecutionEpoch != "" {
			if id, e := uuid.Parse(j.ExecutionEpoch); e != nil || id.String() != j.ExecutionEpoch {
				return j, errors.New("invalid official code execution epoch")
			}
		}
		if id, e := uuid.Parse(j.Project); e != nil || id.String() != j.Project {
			return j, errors.New("invalid official code project")
		}
	}
	var err error
	j.Receipt, j.OfficialReceipt, err = decodeCodeReceipt(r)
	if err == nil && j.OfficialReceipt != nil && j.Official == nil {
		err = errors.New("official receipt without official job")
	}
	return j, err
}

const codeJobColumns = `id,worktree_id,generation,state,manifest,receipt,error,created_ms,finished_ms`

func (s *Store) OfficialCodeJob(ctx context.Context, id string) (records.CodeJob, error) {
	j, err := readCodeJob(s.DB.QueryRowContext(ctx, `SELECT `+codeJobColumns+` FROM code_jobs WHERE id=?`, id))
	if err == nil && j.Official == nil {
		err = access("official_code_backend_required", "official task required")
	}
	return j, err
}

func sameOfficialCode(current, frozen records.CodeState) bool {
	return current.Enabled && current.Official != nil && frozen.Official != nil && current.Generation == frozen.Generation && current.Project == frozen.Project && current.Worktree == frozen.Worktree && current.CommonDir == frozen.CommonDir && *current.Official == *frozen.Official
}

// Replay returns the original outcome and never executes or revives a task.
// A Replayed result is for observation/reconciliation only; even a running,
// still-authorized replay must never be submitted again as new execution.
func (s *Store) StartOfficialCodeJob(ctx context.Context, operation string, c records.CodeState, m codegraph.Manifest) (records.CodeJob, error) {
	var empty records.CodeJob
	id, err := uuid.Parse(operation)
	if err != nil || id.String() != operation || c.Official == nil {
		return empty, access("invalid_request", "explicit official task identity required")
	}
	if m.Worktree != c.Worktree || m.CommonDir != c.CommonDir || m.Recipe != codegraph.Recipe {
		return empty, access("workspace_mismatch", "official source identity changed")
	}
	previous, err := s.OfficialCodeJob(ctx, operation)
	if err == nil {
		if previous.Project != c.Project || previous.WorktreeID != c.ID || previous.Generation != c.Generation || previous.Official == nil || *previous.Official != *c.Official || !reflect.DeepEqual(previous.Manifest, &m) {
			return empty, ErrConflict
		}
		previous.Replayed = true
		return previous, nil
	}
	if err != sql.ErrNoRows {
		return empty, err
	}
	digest, err := hex.DecodeString(m.Digest)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != m.Digest {
		return empty, access("invalid_request", "source digest required")
	}
	var projectPath string
	if err = s.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, c.Project).Scan(&projectPath); err != nil {
		return empty, err
	}
	if err = s.AuthorizeOfficialCode(ctx, c); err != nil {
		return empty, err
	}
	data, err := json.Marshal(officialCodeManifest{Manifest: m, Format: officialCodeJobFormat, Scope: c.Official, Project: c.Project})
	if err != nil {
		return empty, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	current, err := readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, c.ID))
	if err != nil {
		return empty, err
	}
	if !sameOfficialCode(current, c) {
		return empty, ErrConflict
	}
	var nowPath string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, c.Project).Scan(&nowPath); err != nil {
		return empty, err
	}
	if nowPath != projectPath {
		return empty, ErrConflict
	}
	if current.State == "interrupted" || current.State == "cancelled" {
		return empty, access("code_retry_required", "explicit refresh required after interrupted execution")
	}
	if current.State != "queued" && current.ObservedDigest == m.Digest {
		return empty, access("code_unchanged", "same official source already attempted")
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM code_jobs WHERE worktree_id=? AND state IN ('queued','running')`, c.ID).Scan(&active); err != nil {
		return empty, err
	}
	if active != 0 {
		return empty, access("code_busy", "prior code execution has not been confirmed stopped")
	}
	j := records.CodeJob{ID: operation, Project: c.Project, WorktreeID: c.ID, Generation: c.Generation, State: "running", Manifest: &m, Official: c.Official, CreatedMS: time.Now().UnixMilli()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO code_jobs(id,worktree_id,generation,state,manifest,created_ms) VALUES(?,?,?,'running',?,?)`, j.ID, j.WorktreeID, j.Generation, string(data), j.CreatedMS); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='indexing',observed_digest=?,error='',updated_ms=? WHERE id=?`, m.Digest, j.CreatedMS, c.ID); err != nil {
		return empty, err
	}
	return j, tx.Commit()
}

func validOfficialCodeReceipt(j records.CodeJob, r *records.OfficialCodeReceipt) bool {
	if j.Official == nil || j.Manifest == nil || r == nil || r.ExecutionEpoch != j.ExecutionEpoch || r.Operation != j.ID || r.Instance != j.Official.Instance || r.Dataset != j.Official.Dataset || r.User != j.Official.User || r.SourceDigest != j.Manifest.Digest {
		return false
	}
	if r.Status != "completed" && r.Status != "failed" && r.Status != "cancelled" && r.Status != "unconfirmed" {
		return false
	}
	if r.Status == "completed" && (r.PipelineRunID == "" || r.DataID == "") {
		return false
	}
	for _, value := range []string{r.PipelineRunID, r.DataID} {
		if value != "" {
			id, err := uuid.Parse(value)
			if err != nil || id.String() != value {
				return false
			}
		}
	}
	return true
}

// Bind once before dispatch. Persisting the backend process epoch prevents a
// replacement service with the same data-root identity from claiming an old
// execution is absent/stopped. A replay is observation only.
func (s *Store) BindOfficialCodeEpoch(ctx context.Context, c records.CodeState, id, epoch string) (records.CodeJob, error) {
	var empty records.CodeJob
	u, e := uuid.Parse(epoch)
	if e != nil || u.String() != epoch {
		return empty, access("invalid_request", "backend execution epoch required")
	}
	if e = s.AuthorizeOfficialCode(ctx, c); e != nil {
		return empty, e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	j, e := readCodeJob(tx.QueryRowContext(ctx, `SELECT `+codeJobColumns+` FROM code_jobs WHERE id=?`, id))
	if e != nil {
		return empty, e
	}
	current, e := readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, c.ID))
	if e != nil {
		return empty, e
	}
	if !sameOfficialCode(current, c) || j.Official == nil || *j.Official != *c.Official || j.WorktreeID != c.ID || j.Generation != c.Generation || j.State != "running" || j.Error != "" {
		return empty, ErrConflict
	}
	if j.ExecutionEpoch != "" {
		if j.ExecutionEpoch != epoch {
			return empty, ErrConflict
		}
		j.Replayed = true
		return j, nil
	}
	if _, e = tx.ExecContext(ctx, `UPDATE code_jobs SET manifest=json_set(manifest,'$.official_code_epoch',?) WHERE id=?`, epoch, id); e != nil {
		return empty, e
	}
	j.ExecutionEpoch = epoch
	return j, tx.Commit()
}

// Preserve the running slot when a request/stop response is uncertain. This
// records observation loss, not a terminal execution result or retry permit.
func (s *Store) MarkOfficialCodeUnconfirmed(ctx context.Context, j records.CodeJob) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE code_jobs SET error='official_execution_unconfirmed' WHERE id=? AND worktree_id=? AND state='running' AND json_extract(manifest,'$.official_code_format')=?`, j.ID, j.WorktreeID, officialCodeJobFormat); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='interrupted',error='official_execution_unconfirmed',updated_ms=? WHERE id=? AND generation=? AND enabled=1 AND state='indexing'`, time.Now().UnixMilli(), j.WorktreeID, j.Generation); e != nil {
		return e
	}
	return tx.Commit()
}

// Called only after the owner observed backend completion or confirmed stop.
// A lost transport/daemon restart cannot call this merely to free the slot.
// Partial official writes may be retained as a receipt but never published as
// current after authorization/source changes.
func (s *Store) FinishOfficialCodeJob(ctx context.Context, proposed records.CodeJob, receipt *records.OfficialCodeReceipt, cause string) error {
	switch cause {
	case "", "cancelled", "interrupted", "source_changed", "official_pipeline_failed", "code_process_failed":
	default:
		return access("invalid_request", "fixed official task outcome required")
	}
	// Filesystem/repository proof is evaluated before the transaction, then its
	// exact persisted project path and authorization are fenced again inside.
	proof, proofErr := s.Code(ctx, proposed.WorktreeID)
	if proofErr != nil {
		return proofErr
	}
	var projectPath string
	if err := s.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, proof.Project).Scan(&projectPath); err != nil {
		return err
	}
	proofErr = s.AuthorizeOfficialCode(ctx, proof)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := readCodeJob(tx.QueryRowContext(ctx, `SELECT `+codeJobColumns+` FROM code_jobs WHERE id=?`, proposed.ID))
	if err != nil {
		return err
	}
	if j.Official == nil || j.Manifest == nil || j.WorktreeID != proposed.WorktreeID || j.Generation != proposed.Generation {
		return ErrConflict
	}
	if j.State != "running" {
		return ErrConflict
	}
	if receipt != nil && !validOfficialCodeReceipt(j, receipt) {
		return errors.New("invalid official code completion receipt")
	}
	if cause == "" && (receipt == nil || receipt.Status != "completed") {
		return errors.New("official completion unconfirmed")
	}
	c, err := readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, j.WorktreeID))
	if err != nil {
		return err
	}
	var nowPath string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, c.Project).Scan(&nowPath); err != nil {
		return err
	}
	current := proofErr == nil && sameOfficialCode(c, proof) && nowPath == projectPath && c.Project == j.Project && c.Worktree == j.Manifest.Worktree && c.CommonDir == j.Manifest.CommonDir && c.Generation == j.Generation && c.Official != nil && *c.Official == *j.Official
	jobState, codeState := "failed", "failed"
	if !current {
		jobState = "interrupted"
		cause = "authorization_changed"
	} else {
		switch cause {
		case "":
			jobState, codeState = "completed", "ready"
		case "cancelled":
			jobState, codeState = "cancelled", "cancelled"
		case "interrupted":
			jobState, codeState = "interrupted", "interrupted"
		case "source_changed":
			codeState = "stale"
		}
	}
	raw := "null"
	if receipt != nil {
		b, e := json.Marshal(officialCodeReceipt{officialCodeJobFormat, receipt})
		if e != nil {
			return e
		}
		raw = string(b)
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `UPDATE code_jobs SET state=?,receipt=?,error=?,finished_ms=? WHERE id=?`, jobState, raw, cause, now, j.ID); err != nil {
		return err
	}
	if current {
		if cause == "" {
			m, e := json.Marshal(j.Manifest)
			if e != nil {
				return e
			}
			_, err = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='ready',snapshot='',manifest=?,receipt=?,error='',updated_ms=? WHERE id=?`, string(m), raw, now, c.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE code_worktrees SET state=?,error=?,updated_ms=? WHERE id=?`, codeState, cause, now, c.ID)
		}
	} else if c.Generation == j.Generation && c.Enabled && c.State == "indexing" {
		// A workspace/project authorization loss without a user generation
		// change must not leave a finished task displayed as still indexing.
		_, err = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='interrupted',error='authorization_changed',updated_ms=? WHERE id=? AND generation=? AND state='indexing' AND enabled=1`, now, c.ID, j.Generation)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
