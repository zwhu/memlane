package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateCogneeSubmissions(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE cognee_library_identity(id INTEGER PRIMARY KEY CHECK(id=1),identity TEXT NOT NULL UNIQUE);
CREATE TABLE cognee_submission_tasks(operation TEXT PRIMARY KEY,digest TEXT NOT NULL,processing TEXT NOT NULL,binding TEXT NOT NULL,request TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'prepared',cancel_requested INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',executions TEXT NOT NULL DEFAULT '[]');
CREATE TABLE cognee_submissions(operation TEXT NOT NULL,memory TEXT NOT NULL,revision INTEGER NOT NULL,library TEXT NOT NULL,instance TEXT NOT NULL,dataset TEXT NOT NULL,project TEXT NOT NULL REFERENCES scopes(id),digest TEXT NOT NULL,payload TEXT NOT NULL,document TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT '',PRIMARY KEY(operation,memory),FOREIGN KEY(memory,revision) REFERENCES versions(memory,revision));`)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO cognee_library_identity VALUES(1,?)`, uuid.NewString()); err != nil {
		return err
	}
	_, err = tx.Exec(`PRAGMA user_version=28`)
	return err
}

func materialDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// PrepareCogneeSubmission freezes exact current versions, preserving knowledge
// and raw source metadata in place. It never starts extraction or modifies them.
func (s *Store) PrepareCogneeSubmission(ctx context.Context, b records.CogneeBinding, q records.CogneeSubmissionRequest, dependency string) ([]records.CogneeSubmissionMaterial, error) {
	if q.Project != b.Project || q.Dataset != b.Dataset || len(q.Sources) == 0 || len(q.Sources) > 16 {
		return nil, access("invalid_request", "explicit bounded project material selection required")
	}
	if id, e := uuid.Parse(q.Operation); e != nil || id.String() != q.Operation {
		return nil, access("invalid_request", "canonical submission operation required")
	}
	if err := s.AuthorizeCogneeSubmissionBinding(ctx, b); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return nil, err
	}

	requestDigest := records.MergeDigest(struct {
		Binding records.CogneeBinding
		Request records.CogneeSubmissionRequest
	}{b, q})
	var priorDigest, processingJSON string
	err = tx.QueryRowContext(ctx, `SELECT digest,processing FROM cognee_submission_tasks WHERE operation=?`, q.Operation).Scan(&priorDigest, &processingJSON)
	var frozen records.ProcessingSnapshot
	if err == nil {
		if priorDigest != requestDigest {
			return nil, ErrConflict
		}
		if err = json.Unmarshal([]byte(processingJSON), &frozen); err != nil {
			return nil, err
		}
	} else if err == sql.ErrNoRows {
		frozen, err = freezeProcessingTx(ctx, tx, dependency, time.Now())
		if err != nil {
			return nil, err
		}
		if frozen.Revision.ID != q.Processing.Revision || frozen.SelectionGeneration != q.Processing.Generation {
			return nil, ErrConflict
		}
		raw, _ := json.Marshal(frozen)
		_, err = tx.ExecContext(ctx, `INSERT INTO cognee_submission_tasks(operation,digest,processing,binding,request) VALUES(?,?,?,?,?)`, q.Operation, requestDigest, string(raw), string(mustCogneeJSON(b)), string(mustCogneeJSON(q)))
		if err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	out := []records.CogneeSubmissionMaterial{}
	seen := map[string]bool{}
	for _, source := range q.Sources {
		if seen[source.Memory] {
			return nil, access("invalid_request", "duplicate material")
		}
		seen[source.Memory] = true
		v, e := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, source.Memory))
		if e != nil {
			return nil, e
		}
		if v.Dataset != b.Project || v.Revision != source.Revision || v.State != "active" || (v.Origin != "knowledge" && v.Origin != "raw") || len(v.Text) == 0 || len(v.Text) > records.CogneeRawLimit {
			return nil, access("stale_source", "current bounded project knowledge or original required")
		}
		var stale int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM derived_memories WHERE memory=? AND revision=? AND state='stale'`, v.Memory, v.Revision).Scan(&stale); e != nil {
			return nil, e
		}
		if stale > 0 {
			return nil, access("stale_source", "derived source requires review")
		}
		m := records.CogneeSubmissionMaterial{Title: v.Title, Processing: frozen, Library: library, Operation: q.Operation, Instance: b.Instance, Dataset: b.Dataset, Project: b.Project, Memory: v.Memory, Revision: v.Revision, Origin: v.Origin, Digest: materialDigest(v.Text), Text: v.Text}
		encoded, _ := json.Marshal(m)
		var prior string
		e = tx.QueryRowContext(ctx, `SELECT payload FROM cognee_submissions WHERE operation=? AND memory=?`, q.Operation, v.Memory).Scan(&prior)
		if e == nil {
			if prior != string(encoded) {
				return nil, ErrConflict
			}
		} else if e == sql.ErrNoRows {
			// Pending markers cannot be interpreted as official document UUIDs.
			_, e = tx.ExecContext(ctx, `INSERT INTO cognee_submissions(operation,memory,revision,library,instance,dataset,project,digest,payload,document) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.Operation, m.Memory, m.Revision, m.Library, m.Instance, m.Dataset, m.Project, m.Digest, string(encoded), "pending:"+m.Operation+":"+m.Memory)
			if e != nil {
				return nil, e
			}
		} else {
			return nil, e
		}
		out = append(out, m)
	}
	return out, tx.Commit()
}

// Complete accepts a verified official receipt only; caller is the daemon's
// owner, never an Agent tool or frontend supplied receipt.
func (s *Store) CompleteCogneeSubmission(ctx context.Context, r records.CogneeSubmissionReceipt) error {
	if r.State != "processed" || r.CogneeVersion != "1.6.7" {
		return access("invalid_provenance", "official processing completion required")
	}
	if id, e := uuid.Parse(r.Document); e != nil || id.String() != r.Document {
		return access("invalid_provenance", "official document identity required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var payload, receipt string
	if err = tx.QueryRowContext(ctx, `SELECT payload,receipt FROM cognee_submissions WHERE operation=? AND memory=?`, r.Operation, r.Memory).Scan(&payload, &receipt); err != nil {
		return err
	}
	var m records.CogneeSubmissionMaterial
	if err = json.Unmarshal([]byte(payload), &m); err != nil {
		return err
	}
	if r.ProcessingRevisionDigest != m.Processing.Revision.Digest || r.ProcessingDependencyDigest != m.Processing.DependencyDigest || r.Library != m.Library || r.Instance != m.Instance || r.Dataset != m.Dataset || r.Project != m.Project || r.Memory != m.Memory || r.Revision != m.Revision || r.Digest != m.Digest {
		return access("invalid_provenance", "official receipt differs from immutable material")
	}
	encoded, _ := json.Marshal(r)
	if receipt != "" {
		if receipt != string(encoded) {
			return ErrConflict
		}
		return nil
	}
	var permitted int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cognee_submission_tasks t JOIN cognee_library_identity l ON l.identity=? WHERE t.operation=? AND t.state='running' AND t.cancel_requested=0`, r.Library, r.Operation).Scan(&permitted); err != nil {
		return err
	}
	if permitted != 1 {
		return ErrConflict
	}
	var foreign int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cognee_submissions WHERE instance=? AND dataset=? AND document=? AND (library!=? OR memory!=? OR revision!=? OR digest!=?)`, r.Instance, r.Dataset, r.Document, r.Library, r.Memory, r.Revision, r.Digest).Scan(&foreign); err != nil {
		return err
	}
	if foreign != 0 {
		return access("invalid_provenance", "document belongs to different material")
	}
	_, err = tx.ExecContext(ctx, `UPDATE cognee_submissions SET document=?,receipt=? WHERE operation=? AND memory=?`, r.Document, string(encoded), r.Operation, r.Memory)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Separate proof path from cognee-import-v1: only a completed immutable
// submission can bind a graph document; current version/digest must still match.
func submittedCogneeDocuments(ctx context.Context, tx *sql.Tx, b records.CogneeBinding) (map[string]records.Version, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.document,c.memory,c.revision,c.digest FROM cognee_submissions c JOIN cognee_library_identity l ON l.identity=c.library JOIN cognee_submission_tasks t ON t.operation=c.operation AND t.state='completed' AND t.executions!='[]' WHERE c.instance=? AND c.dataset=? AND c.project=? AND c.receipt!='' ORDER BY c.document`, b.Instance, b.Dataset, b.Project)
	if err != nil {
		return nil, err
	}
	type entry struct {
		document, memory, digest string
		revision                 int64
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.document, &e.memory, &e.revision, &e.digest); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := map[string]records.Version{}
	for _, e := range entries {
		v, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, e.memory))
		if err != nil {
			return nil, err
		}
		if v.Dataset != b.Project || v.State != "active" || v.Revision != e.revision || materialDigest(v.Text) != e.digest || (v.Origin != "knowledge" && v.Origin != "raw") {
			continue
		}
		var stale int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM derived_memories WHERE memory=? AND revision=? AND state='stale'`, v.Memory, v.Revision).Scan(&stale); err != nil {
			return nil, err
		}
		if stale > 0 {
			continue
		}
		if !strings.HasPrefix(e.document, "pending:") {
			out[e.document] = v
		}
	}
	return out, nil
}

// New admission path: exact project authorization is resolved from the trusted
// workspace for either supported Agent. It does not rewrite ContextProject or
// authorize historical Cognee import/session access.
func (s *Store) AuthorizeCogneeSubmissionBinding(ctx context.Context, b records.CogneeBinding) error {
	if !b.MaterialSubmission {
		return access("scope_denied", "explicit material submission binding required")
	}
	c, scopes, err := s.ResolveAgentScopes(ctx, records.AgentRequest{Protocol: 1, ConnectionID: b.ConnectionID, Generation: b.Generation, Workspace: b.Workspace, Tool: "memory_save", Arguments: records.AgentArguments{Scope: b.Project}})
	if err != nil {
		return err
	}
	if (c.Agent != "pi" && c.Agent != "codex") || c.ContextProject != b.Project || len(scopes) != 1 || scopes[0] != b.Project {
		return access("scope_denied", "current authorized material project required")
	}
	return nil
}

func (s *Store) AuthorizeCogneeGraphBinding(ctx context.Context, b records.CogneeBinding) error {
	if b.MaterialSubmission {
		return s.AuthorizeCogneeSubmissionBinding(ctx, b)
	}
	return s.AuthorizeCogneeBinding(ctx, b)
}

func mustCogneeJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func (s *Store) CogneeSubmissionTask(ctx context.Context, id string) (records.CogneeSubmissionTask, error) {
	var t records.CogneeSubmissionTask
	var request, executions string
	err := s.DB.QueryRowContext(ctx, `SELECT operation,request,state,error_code,cancel_requested,executions,(SELECT count(*) FROM cognee_submissions c WHERE c.operation=t.operation),(SELECT count(*) FROM cognee_submissions c WHERE c.operation=t.operation AND receipt!='') FROM cognee_submission_tasks t WHERE operation=?`, id).Scan(&t.Operation, &request, &t.State, &t.ErrorCode, &t.CancelRequested, &executions, &t.Materials, &t.Completed)
	if err != nil {
		return t, err
	}
	var q records.CogneeSubmissionRequest
	if err = json.Unmarshal([]byte(request), &q); err != nil {
		return t, err
	}
	t.Project, t.Dataset = q.Project, q.Dataset
	t.Executions = json.RawMessage(executions)
	_, materials, err := s.LoadCogneeSubmission(ctx, id)
	if err != nil {
		return t, err
	}
	t.Sources = []records.CogneeSubmissionSource{}
	for _, m := range materials {
		t.Sources = append(t.Sources, records.CogneeSubmissionSource{Memory: m.Memory, Revision: m.Revision, Origin: m.Origin, Title: m.Title})
	}
	if len(materials) > 0 {
		p := materials[0].Processing
		t.ProcessingRevision, t.ProcessingMode, t.RequestedModel = p.Revision.ID, p.Revision.Spec.Mode, p.Revision.Spec.Model
	}
	return t, nil
}

func (s *Store) LoadCogneeSubmission(ctx context.Context, id string) (records.CogneeBinding, []records.CogneeSubmissionMaterial, error) {
	var b records.CogneeBinding
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT binding FROM cognee_submission_tasks WHERE operation=?`, id).Scan(&raw); err != nil {
		return b, nil, err
	}
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return b, nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT payload FROM cognee_submissions WHERE operation=? ORDER BY memory`, id)
	if err != nil {
		return b, nil, err
	}
	defer rows.Close()
	out := []records.CogneeSubmissionMaterial{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return b, nil, err
		}
		var m records.CogneeSubmissionMaterial
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			return b, nil, err
		}
		out = append(out, m)
	}
	return b, out, rows.Err()
}

func (s *Store) ClaimCogneeSubmission(ctx context.Context, id, library string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE cognee_submission_tasks SET state='running' WHERE operation=? AND state='prepared' AND cancel_requested=0 AND EXISTS(SELECT 1 FROM cognee_library_identity WHERE identity=?)`, id, library)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (s *Store) CancelCogneeSubmission(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cognee_submission_tasks SET cancel_requested=1,state=CASE WHEN state='prepared' THEN 'cancelled' ELSE state END WHERE operation=? AND state IN ('prepared','running')`, id)
	return err
}
func (s *Store) FinishCogneeSubmission(ctx context.Context, id, library, state, code string) error {
	if state != "completed" && state != "failed" && state != "cancelled" && state != "interrupted" {
		return access("invalid_request", "terminal material state required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE cognee_submission_tasks SET state=?,error_code=? WHERE operation=? AND state='running' AND (?!='completed' OR (json_array_length(executions)>0 AND NOT EXISTS(SELECT 1 FROM json_each(executions) WHERE json_extract(value,'$.success') IS NOT 1) AND cancel_requested=0 AND NOT EXISTS(SELECT 1 FROM cognee_submissions c WHERE c.operation=cognee_submission_tasks.operation AND c.receipt=''))) AND EXISTS(SELECT 1 FROM cognee_library_identity WHERE identity=?)`, state, code, id, state, library)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}

// A service restart never resumes an unsealed paid extraction implicitly.
func (s *Store) InterruptCogneeSubmissions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cognee_submission_tasks SET state='interrupted',cancel_requested=1,error_code='service_restarted' WHERE state IN ('prepared','running')`)
	return err
}

// Execution evidence comes only from the owner's sealed, safe gateway receipts.
func (s *Store) SealCogneeSubmissionExecutions(ctx context.Context, id, library string, raw json.RawMessage) error {
	if !json.Valid(raw) || len(raw) > 256*1024 {
		return access("invalid_request", "bounded execution receipts required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE cognee_submission_tasks SET executions=? WHERE operation=? AND state='running' AND EXISTS(SELECT 1 FROM cognee_library_identity WHERE identity=?)`, string(raw), id, library)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}

// Lost IPC responses replay the original immutable task. Later settings or
// source versions do not grant a second execution and do not erase its receipt.
// Current project authorization is still checked before returning any metadata.
func (s *Store) ReplayCogneeSubmission(ctx context.Context, q records.CogneeSubmissionRequest) (records.CogneeSubmissionTask, bool, error) {
	var request, binding string
	err := s.DB.QueryRowContext(ctx, `SELECT request,binding FROM cognee_submission_tasks WHERE operation=?`, q.Operation).Scan(&request, &binding)
	if err == sql.ErrNoRows {
		return records.CogneeSubmissionTask{}, false, nil
	}
	if err != nil {
		return records.CogneeSubmissionTask{}, false, err
	}
	var b records.CogneeBinding
	if err = json.Unmarshal([]byte(binding), &b); err != nil {
		return records.CogneeSubmissionTask{}, true, err
	}
	if err = s.AuthorizeCogneeSubmissionBinding(ctx, b); err != nil {
		return records.CogneeSubmissionTask{}, true, err
	}
	if string(mustCogneeJSON(q)) != request {
		return records.CogneeSubmissionTask{}, true, ErrConflict
	}
	task, err := s.CogneeSubmissionTask(ctx, q.Operation)
	return task, true, err
}

func (s *Store) CogneeLibraryIdentity(ctx context.Context) (string, error) {
	var identity string
	err := s.DB.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&identity)
	return identity, err
}

// Host-owned authorization may reconnect the same private project dataset after
// an explicit grant/generation change. Legacy bindings can never be converted.
func (s *Store) BindProjectCogneeMaterial(ctx context.Context, b records.CogneeBinding) (records.CogneeBinding, error) {
	if err := s.AuthorizeCogneeSubmissionBinding(ctx, b); err != nil {
		return b, err
	}
	for _, id := range []string{b.Operation, b.Instance, b.Dataset, b.Project} {
		if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
			return b, access("invalid_request", "canonical material binding required")
		}
	}
	bindings, err := s.CogneeBindings(ctx, b.Project)
	if err != nil {
		return b, err
	}
	for _, existing := range bindings {
		if existing.Instance != b.Instance || existing.Dataset != b.Dataset {
			continue
		}
		if !existing.MaterialSubmission {
			return b, access("scope_denied", "legacy dataset cannot become a material dataset")
		}
		unchanged := b
		unchanged.Operation = existing.Operation
		if records.MergeDigest(unchanged) == records.MergeDigest(existing) {
			return existing, nil
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			return b, err
		}
		sum := sha256.Sum256(encoded)
		result, err := s.DB.ExecContext(ctx, `UPDATE cognee_bindings SET operation=?,digest=?,payload=? WHERE instance=? AND dataset=? AND project=? AND json_extract(payload,'$.material_submission')=1`, b.Operation, hex.EncodeToString(sum[:]), string(encoded), b.Instance, b.Dataset, b.Project)
		if err != nil {
			return b, err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			return b, ErrConflict
		}
		return b, err
	}
	return s.BindCognee(ctx, b)
}

func (s *Store) LatestCogneeSubmission(ctx context.Context, project string) (records.CogneeSubmissionTask, error) {
	if id, e := uuid.Parse(project); e != nil || id.String() != project {
		return records.CogneeSubmissionTask{}, access("scope_required", "registered project required")
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT operation FROM cognee_submission_tasks WHERE json_extract(request,'$.project')=? ORDER BY rowid DESC LIMIT 1`, project).Scan(&id)
	if err == sql.ErrNoRows {
		return records.CogneeSubmissionTask{Project: project, State: "none", Sources: []records.CogneeSubmissionSource{}}, nil
	}
	if err != nil {
		return records.CogneeSubmissionTask{}, err
	}
	return s.CogneeSubmissionTask(ctx, id)
}
