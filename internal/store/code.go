package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

const codeColumns = `id,project,worktree,common_dir,dataset,enabled,generation,state,snapshot,manifest,receipt,observed_digest,error,updated_ms`

type codeScanner interface{ Scan(...any) error }

func readCode(row codeScanner) (records.CodeState, error) {
	var s records.CodeState
	var manifest, receipt string
	err := row.Scan(&s.ID, &s.Project, &s.Worktree, &s.CommonDir, &s.Dataset, &s.Enabled, &s.Generation, &s.State, &s.Snapshot, &manifest, &receipt, &s.ObservedDigest, &s.Error, &s.UpdatedMS)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal([]byte(manifest), &s.Manifest); err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(receipt), &s.Receipt)
	return s, err
}
func (s *Store) Code(ctx context.Context, id string) (records.CodeState, error) {
	return readCode(s.DB.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, id))
}
func (s *Store) Codes(ctx context.Context) ([]records.CodeState, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees ORDER BY project,worktree,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []records.CodeState{}
	for rows.Next() {
		r, e := readCode(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CodeAction records a stable command receipt. No file scan/extractor/worker is
// performed in this transaction; even enable merely persists authorization.
func (s *Store) CodeAction(ctx context.Context, a records.CodeAction) (records.CodeState, error) {
	var out records.CodeState
	if _, e := uuid.Parse(a.OperationID); e != nil {
		return out, access("invalid_request", "stable code action UUID required")
	}
	if a.Kind != "enable" && a.Kind != "disable" && a.Kind != "refresh" {
		return out, access("invalid_request", "unknown code action")
	}
	digest := UsageDigest(a)
	var old, raw string
	e := s.DB.QueryRowContext(ctx, `SELECT digest,receipt FROM code_actions WHERE id=?`, a.OperationID).Scan(&old, &raw)
	if e == nil {
		if old != digest {
			return out, ErrConflict
		}
		e = json.Unmarshal([]byte(raw), &out)
		out.Replayed = true
		return out, e
	}
	if e != sql.ErrNoRows {
		return out, e
	}
	var root, common string
	if a.Kind == "enable" {
		if e = s.CheckBoundWorkspace(ctx, a.Project, a.Path); e != nil {
			return out, e
		}
		root, common, e = codegraph.SourceRoot(ctx, a.Path)
		if e != nil {
			return out, e
		}
		var registered string
		if e = s.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, a.Project).Scan(&registered); e != nil {
			return out, e
		}
		_, known, e := repository(ctx, registered)
		if e != nil {
			return out, e
		}
		if known != common {
			return out, access("workspace_mismatch", "code worktree differs from authorized project")
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT digest,receipt FROM code_actions WHERE id=?`, a.OperationID).Scan(&old, &raw)
	if e == nil {
		if old != digest {
			return out, ErrConflict
		}
		if e = json.Unmarshal([]byte(raw), &out); e != nil {
			return out, e
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	if e != sql.ErrNoRows {
		return out, e
	}
	if a.Kind == "enable" {
		out, e = readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE project=? AND worktree=?`, a.Project, root))
	} else {
		out, e = readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, a.ID))
	}
	now := time.Now().UnixMilli()
	if e == sql.ErrNoRows && a.Kind == "enable" {
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM code_worktrees WHERE enabled=1`).Scan(&count); e != nil {
			return out, e
		}
		if count >= 16 {
			return out, access("limit_exceeded", "at most 16 enabled code worktrees")
		}
		out = records.CodeState{ID: uuid.NewString(), Project: a.Project, Worktree: root, CommonDir: common, Dataset: uuid.NewSHA1(uuid.MustParse(a.Project), []byte("memlane-code:"+root)).String(), Generation: 1, Enabled: true, State: "queued", UpdatedMS: now}
		_, e = tx.ExecContext(ctx, `INSERT INTO code_worktrees(id,project,worktree,common_dir,dataset,enabled,generation,state,updated_ms) VALUES(?,?,?,?,?,1,1,'queued',?)`, out.ID, out.Project, root, common, out.Dataset, now)
	} else if e == nil {
		if a.ExpectedGeneration != out.Generation {
			return out, ErrConflict
		}
		if a.Kind == "refresh" && !out.Enabled {
			return out, access("code_disabled", "enable this worktree in Memlane")
		}
		if a.Kind == "enable" && !out.Enabled {
			var count int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM code_worktrees WHERE enabled=1`).Scan(&count); e != nil {
				return out, e
			}
			if count >= 16 {
				return out, access("limit_exceeded", "at most 16 enabled code worktrees")
			}
		}
		out.Generation++
		out.Enabled = a.Kind != "disable"
		out.Error = ""
		out.ObservedDigest = ""
		out.State = "queued"
		out.UpdatedMS = now
		if !out.Enabled {
			out.State = "disabled"
		}
		_, e = tx.ExecContext(ctx, `UPDATE code_jobs SET state='cancelled',error='generation_changed',finished_ms=? WHERE worktree_id=? AND state IN ('queued','running')`, now, out.ID)
		if e != nil {
			return out, e
		}
		_, e = tx.ExecContext(ctx, `UPDATE code_worktrees SET enabled=?,generation=?,state=?,observed_digest='',error='',updated_ms=? WHERE id=?`, out.Enabled, out.Generation, out.State, now, out.ID)
	} else {
		return out, e
	}
	if e != nil {
		return out, e
	}
	payload, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO code_actions VALUES(?,?,?)`, a.OperationID, digest, string(payload)); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func (s *Store) StartCodeJob(ctx context.Context, c records.CodeState, m codegraph.Manifest) (records.CodeJob, error) {
	j := records.CodeJob{ID: uuid.NewString(), WorktreeID: c.ID, Generation: c.Generation, State: "running", Manifest: &m, CreatedMS: time.Now().UnixMilli()}
	if m.Worktree != c.Worktree || m.CommonDir != c.CommonDir || m.Recipe != codegraph.Recipe {
		return j, access("workspace_mismatch", "code worktree identity changed")
	}
	data, e := json.Marshal(m)
	if e != nil {
		return j, e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return j, e
	}
	defer tx.Rollback()
	current, e := readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, c.ID))
	if e != nil {
		return j, e
	}
	if !current.Enabled || current.Generation != c.Generation {
		return j, ErrConflict
	}
	if current.State != "queued" && current.ObservedDigest == m.Digest {
		return j, access("code_unchanged", "same source snapshot already attempted")
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO code_jobs(id,worktree_id,generation,state,manifest,created_ms) VALUES(?,?,?,'running',?,?)`, j.ID, c.ID, c.Generation, string(data), j.CreatedMS)
	if e != nil {
		return j, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='indexing',observed_digest=?,error='',updated_ms=? WHERE id=?`, m.Digest, j.CreatedMS, c.ID)
	if e != nil {
		return j, e
	}
	return j, tx.Commit()
}
func (s *Store) FinishCodeJob(ctx context.Context, j records.CodeJob, receipt *codegraph.Receipt, cause string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var state string
	var generation int64
	var mJSON string
	if e = tx.QueryRowContext(ctx, `SELECT state,generation,manifest FROM code_jobs WHERE id=? AND worktree_id=?`, j.ID, j.WorktreeID).Scan(&state, &generation, &mJSON); e != nil {
		return e
	}
	c, e := readCode(tx.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE id=?`, j.WorktreeID))
	if e != nil {
		return e
	}
	if state != "running" || generation != j.Generation || c.Generation != j.Generation || !c.Enabled {
		return ErrConflict
	}
	var m codegraph.Manifest
	if e = json.Unmarshal([]byte(mJSON), &m); e != nil {
		return e
	}
	jobState, codeState := "failed", "failed"
	if cause == "" {
		if receipt == nil || receipt.Dataset != c.Dataset || receipt.SourceDigest != m.Digest || receipt.Snapshot != uuid.NewSHA1(uuid.MustParse(c.Dataset), []byte(m.Digest)).String() || receipt.Extractor != "enola-0.4.30-v351" {
			return errors.New("invalid code completion receipt")
		}
		jobState, codeState = "completed", "ready"
	} else if cause == "cancelled" {
		jobState, codeState = "cancelled", "cancelled"
	} else if cause == "interrupted" {
		jobState, codeState = "interrupted", "interrupted"
	} else if cause == "source_changed" {
		codeState = "stale"
	}
	now := time.Now().UnixMilli()
	rJSON, e := json.Marshal(receipt)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE code_jobs SET state=?,receipt=?,error=?,finished_ms=? WHERE id=?`, jobState, string(rJSON), cause, now, j.ID)
	if e != nil {
		return e
	}
	if cause == "" {
		_, e = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='ready',snapshot=?,manifest=?,receipt=?,error='',updated_ms=? WHERE id=?`, receipt.Snapshot, mJSON, string(rJSON), now, c.ID)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE code_worktrees SET state=?,error=?,updated_ms=?,observed_digest=CASE WHEN ?='interrupted' THEN '' ELSE observed_digest END WHERE id=?`, codeState, cause, now, cause, c.ID)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) CodeFailure(ctx context.Context, c records.CodeState, cause string) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE code_worktrees SET state='failed',observed_digest='',error=?,updated_ms=? WHERE id=? AND generation=? AND enabled=1 AND state NOT IN ('indexing','failed')`, cause, time.Now().UnixMilli(), c.ID, c.Generation)
	return e
}
func (s *Store) MarkCodeStale(ctx context.Context, id string, generation int64) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE code_worktrees SET state='stale',error='source_changed',updated_ms=? WHERE id=? AND generation=? AND enabled=1 AND state='ready'`, time.Now().UnixMilli(), id, generation)
	return e
}
func (s *Store) InterruptCode(ctx context.Context) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `UPDATE code_worktrees SET state='interrupted',observed_digest='',error='service_interrupted',updated_ms=? WHERE enabled=1 AND id IN (SELECT worktree_id FROM code_jobs WHERE state IN ('running','queued'));UPDATE code_jobs SET state='interrupted',error='service_interrupted',finished_ms=? WHERE state IN ('running','queued')`, time.Now().UnixMilli(), time.Now().UnixMilli())
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) CodeJobs(ctx context.Context, id string) ([]records.CodeJob, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,worktree_id,generation,state,manifest,receipt,error,created_ms,finished_ms FROM code_jobs WHERE worktree_id=? ORDER BY created_ms DESC,id DESC LIMIT 50`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []records.CodeJob{}
	for rows.Next() {
		var j records.CodeJob
		var m, r string
		if e = rows.Scan(&j.ID, &j.WorktreeID, &j.Generation, &j.State, &m, &r, &j.Error, &j.CreatedMS, &j.FinishedMS); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(m), &j.Manifest); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(r), &j.Receipt); e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) CodeForWorkspace(ctx context.Context, project, path string) (records.CodeState, error) {
	root, _, e := codegraph.SourceRoot(ctx, path)
	if e != nil {
		return records.CodeState{}, e
	}
	c, e := readCode(s.DB.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE project=? AND worktree=? AND enabled=1`, project, root))
	if e == sql.ErrNoRows {
		return c, access("code_disabled", fmt.Sprintf("enable this worktree in Memlane before code queries"))
	}
	return c, e
}
