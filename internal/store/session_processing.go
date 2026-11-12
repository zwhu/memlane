package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateSessionProcessing(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_processing_tasks(operation TEXT PRIMARY KEY,request TEXT NOT NULL,binding TEXT NOT NULL,processing TEXT NOT NULL,library TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'prepared',cancel_requested INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT '',executions TEXT NOT NULL DEFAULT '[]'); PRAGMA user_version=32;`)
	return err
}

var sessionProcessingDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var sessionProcessingCode = regexp.MustCompile(`^[a-z_]{1,64}$`)

func validateSessionProcessingRequest(q records.SessionProcessingRequest) error {
	for _, value := range []string{q.Operation, q.Project, q.Instance, q.Dataset, q.User} {
		if id, err := uuid.Parse(value); err != nil || id.String() != value {
			return access("invalid_request", "canonical session processing identity required")
		}
	}
	if len(q.Session) == 0 || len(q.Session) > 128 || strings.ContainsFunc(q.Session, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) || !sessionProcessingDigest.MatchString(q.ObservedDigest) {
		return access("invalid_request", "bounded observed session identity required")
	}
	return nil
}

// Replay is checked before current model resolution. Settings drift never turns
// a lost response into a second paid execution; authorization is still current.
func (s *Store) ReplaySessionProcessing(ctx context.Context, q records.SessionProcessingRequest) (records.SessionProcessingTask, bool, error) {
	f, err := s.LoadSessionProcessing(ctx, q.Operation)
	if err == sql.ErrNoRows {
		return records.SessionProcessingTask{}, false, nil
	}
	if err != nil {
		return records.SessionProcessingTask{}, false, err
	}
	if records.MergeDigest(f.Task.Request) != records.MergeDigest(q) {
		return records.SessionProcessingTask{}, true, ErrConflict
	}
	if err = s.AuthorizeCogneeBinding(ctx, f.Binding); err != nil {
		return records.SessionProcessingTask{}, true, err
	}
	return f.Task, true, nil
}

func (s *Store) PrepareSessionProcessing(ctx context.Context, b records.CogneeBinding, q records.SessionProcessingRequest, dependency string) (records.SessionProcessingTask, error) {
	var empty records.SessionProcessingTask
	if err := validateSessionProcessingRequest(q); err != nil {
		return empty, err
	}
	if b.MaterialSubmission || b.Project != q.Project || b.Instance != q.Instance || b.Dataset != q.Dataset {
		return empty, access("scope_denied", "current official session grant required")
	}
	if q.AutomaticGrant != nil {
		if q.AutomaticGrant.Binding != b || q.AutomaticGrant.User != q.User {
			return empty, access("scope_denied", "automatic processing scope changed")
		}
		if err := s.AuthorizeCodingMemoryGrant(ctx, *q.AutomaticGrant); err != nil {
			return empty, err
		}
	}
	if prior, exists, err := s.ReplaySessionProcessing(ctx, q); exists || err != nil {
		return prior, err
	}
	if err := s.AuthorizeCogneeBinding(ctx, b); err != nil {
		return empty, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if q.AutomaticGrant != nil {
		grant, err := codingMemoryGrantRow(tx.QueryRowContext(ctx, `SELECT binding,user,generation,enabled,changed_ms FROM coding_memory_grants WHERE instance=? AND dataset=?`, b.Instance, b.Dataset))
		if err != nil {
			return empty, err
		}
		if grant != *q.AutomaticGrant {
			return empty, access("scope_changed", "automatic processing permission changed")
		}
	}
	// Recheck mutable ledger facts inside the same transaction as task freeze.
	var payload, agent, allowed string
	var enabled bool
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM cognee_bindings WHERE instance=? AND dataset=? AND project=?`, b.Instance, b.Dataset, b.Project).Scan(&payload); err != nil {
		return empty, err
	}
	if payload != string(mustCogneeJSON(b)) {
		return empty, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT agent,enabled,generation,allowed_scopes FROM connections WHERE id=?`, b.ConnectionID).Scan(&agent, &enabled, &generation, &allowed); err != nil {
		return empty, err
	}
	var scopes []string
	if err = json.Unmarshal([]byte(allowed), &scopes); err != nil {
		return empty, err
	}
	found := false
	for _, scope := range scopes {
		found = found || scope == b.Project
	}
	if agent != "codex" || !enabled || generation != b.Generation || !found {
		return empty, access("scope_denied", "session processing grant changed")
	}
	frozen, err := freezeProcessingTx(ctx, tx, dependency, time.Now())
	if err != nil {
		return empty, err
	}
	if frozen.Revision.ID != q.Processing.Revision || frozen.SelectionGeneration != q.Processing.Generation {
		return empty, ErrConflict
	}
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return empty, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session_processing_tasks(operation,request,binding,processing,library) VALUES(?,?,?,?,?)`, q.Operation, string(mustCogneeJSON(q)), payload, string(mustCogneeJSON(frozen)), library)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return records.SessionProcessingTask{Request: q, Library: library, State: "prepared", Executions: json.RawMessage(`[]`)}, nil
}

func (s *Store) LoadSessionProcessing(ctx context.Context, operation string) (records.SessionProcessingFrozen, error) {
	var f records.SessionProcessingFrozen
	var request, binding, processing, receipt, executions string
	err := s.DB.QueryRowContext(ctx, `SELECT request,binding,processing,library,state,cancel_requested,error_code,receipt,executions FROM session_processing_tasks WHERE operation=?`, operation).Scan(&request, &binding, &processing, &f.Task.Library, &f.Task.State, &f.Task.CancelRequested, &f.Task.ErrorCode, &receipt, &executions)
	if err != nil {
		return f, err
	}
	for _, item := range []struct {
		raw string
		out any
	}{{request, &f.Task.Request}, {binding, &f.Binding}, {processing, &f.Processing}, {executions, &f.Task.Executions}} {
		if err = json.Unmarshal([]byte(item.raw), item.out); err != nil {
			return f, err
		}
	}
	if receipt != "" {
		err = json.Unmarshal([]byte(receipt), &f.Task.Receipt)
	}
	return f, err
}

func (s *Store) SessionProcessingTask(ctx context.Context, operation string) (records.SessionProcessingTask, error) {
	f, err := s.LoadSessionProcessing(ctx, operation)
	if err == nil {
		err = s.AuthorizeCogneeBinding(ctx, f.Binding)
	}
	return f.Task, err
}

func (s *Store) LatestSessionProcessing(ctx context.Context, q records.SessionProcessingRequest) (records.SessionProcessingTask, error) {
	var operation string
	err := s.DB.QueryRowContext(ctx, `SELECT operation FROM session_processing_tasks WHERE json_extract(request,'$.project')=? AND json_extract(request,'$.instance')=? AND json_extract(request,'$.dataset')=? AND json_extract(request,'$.session')=? AND json_extract(request,'$.user')=? ORDER BY rowid DESC LIMIT 1`, q.Project, q.Instance, q.Dataset, q.Session, q.User).Scan(&operation)
	if err == sql.ErrNoRows {
		return records.SessionProcessingTask{Request: q, State: "none"}, nil
	}
	if err != nil {
		return records.SessionProcessingTask{}, err
	}
	return s.SessionProcessingTask(ctx, operation)
}

func (s *Store) ClaimSessionProcessing(ctx context.Context, operation, library string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE session_processing_tasks SET state='running' WHERE operation=? AND library=? AND state='prepared' AND cancel_requested=0`, operation, library)
	return sessionProcessingChanged(result, err)
}

func sessionProcessingChanged(result sql.Result, err error) error {
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			return ErrConflict
		}
	}
	return err
}

func (s *Store) CancelSessionProcessing(ctx context.Context, operation string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE session_processing_tasks SET cancel_requested=1,state=CASE WHEN state='prepared' THEN 'cancelled' ELSE state END WHERE operation=? AND state IN ('prepared','running')`, operation)
	return err
}

func (s *Store) InterruptSessionProcessing(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE session_processing_tasks SET state='interrupted',cancel_requested=1,error_code='service_restarted' WHERE state IN ('prepared','running')`)
	return err
}

// Receipt means official stages ran; it never asserts knowledge was generated.
// Callers seal safe gateway observations, without prompts or live capabilities.
func (s *Store) FinishSessionProcessing(ctx context.Context, operation, library, state, code string, receipt *records.SessionProcessingReceipt, executions json.RawMessage) error {
	if state != "completed" && state != "failed" && state != "cancelled" && state != "interrupted" {
		return access("invalid_request", "terminal session processing state required")
	}
	if code != "" && !sessionProcessingCode.MatchString(code) {
		return access("invalid_request", "safe processing code required")
	}
	var observations []struct {
		Success bool `json:"success"`
	}
	if len(executions) > 256*1024 || json.Unmarshal(executions, &observations) != nil || observations == nil {
		return access("invalid_request", "bounded execution observations required")
	}
	f, err := s.LoadSessionProcessing(ctx, operation)
	if err != nil {
		return err
	}
	if library != f.Task.Library {
		return ErrConflict
	}
	encoded := ""
	if receipt != nil {
		q := f.Task.Request
		if receipt.Instance != q.Instance || receipt.Dataset != q.Dataset || receipt.Project != q.Project || receipt.Operation != q.Operation || receipt.Library != library || receipt.Session != q.Session || receipt.User != q.User || receipt.CogneeVersion != "1.6.7" || receipt.RevisionDigest != f.Processing.Revision.Digest || receipt.DependencyDigest != f.Processing.DependencyDigest {
			return access("invalid_provenance", "official session processing receipt changed")
		}
		if receipt.Status != "completed" && receipt.Status != "errored" && receipt.Status != "skipped" || len(receipt.Stages) == 0 || len(receipt.Stages) > 16 {
			return access("invalid_provenance", "official stage result required")
		}
		for _, stage := range receipt.Stages {
			if !sessionProcessingCode.MatchString(stage.Stage) || stage.Status != "completed" && stage.Status != "already_completed" && stage.Status != "skipped" && stage.Status != "errored" || stage.Reason != "" && !sessionProcessingCode.MatchString(stage.Reason) || stage.ErrorCode != "" && stage.ErrorCode != "official_stage_failed" || len(stage.Counts) > 32 {
				return access("invalid_provenance", "safe official stage facts required")
			}
			for key, value := range stage.Counts {
				if !sessionProcessingCode.MatchString(key) || value < 0 {
					return access("invalid_provenance", "safe official stage counts required")
				}
			}
			if state == "completed" && stage.Status == "errored" {
				return access("invalid_provenance", "errored stage cannot complete a task")
			}
		}
		encoded = string(mustCogneeJSON(receipt))
	}
	if state == "completed" {
		if receipt == nil || receipt.Status == "errored" {
			return access("invalid_provenance", "successful official result required")
		}
		for _, observation := range observations {
			if !observation.Success {
				return access("invalid_provenance", "failed model observation cannot complete a task")
			}
		}
	}
	query := `UPDATE session_processing_tasks SET state=?,error_code=?,receipt=?,executions=? WHERE operation=? AND library=? AND state='running' AND (?!='completed' OR cancel_requested=0)`
	args := []any{state, code, encoded, string(executions), operation, library, state}
	if state == "completed" && f.Task.Request.AutomaticGrant != nil {
		g := *f.Task.Request.AutomaticGrant
		query += ` AND EXISTS(SELECT 1 FROM coding_memory_grants WHERE instance=? AND dataset=? AND project=? AND binding=? AND user=? AND generation=? AND enabled=1 AND changed_ms=?)`
		args = append(args, g.Binding.Instance, g.Binding.Dataset, g.Binding.Project, string(mustCogneeJSON(g.Binding)), g.User, g.Generation, g.ChangedMS)
	}
	result, err := s.DB.ExecContext(ctx, query, args...)
	return sessionProcessingChanged(result, err)
}
