package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const initializationColumns = `id,project,connection,generation,workspace,workspace_identity,state,error_code,created_ms,source_read_ms,snapshot,receipt`

func scanInitialization(row scanner) (records.InitializationBatch, error) {
	var b records.InitializationBatch
	var snapshot, receipt string
	err := row.Scan(&b.ID, &b.Project, &b.ConnectionID, &b.Generation, &b.Workspace, &b.WorkspaceID, &b.State, &b.Error, &b.CreatedMS, &b.SourceReadMS, &snapshot, &receipt)
	if err == nil {
		err = json.Unmarshal([]byte(snapshot), &b.Sources)
	}
	if err == nil {
		err = json.Unmarshal([]byte(receipt), &b.Receipt)
	}
	if b.State == "received" && b.Receipt == nil {
		return b, errors.New("received initialization has no receipt")
	}
	if b.Receipt != nil {
		b.Saved = len(b.Receipt.Items)
	}
	return b, err
}
func initializationConnection(ctx context.Context, tx *sql.Tx, id string, generation int64, project string) (records.Connection, error) {
	c, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+connectionColumns+`,0,'','','' FROM connections WHERE id=?`, id))
	if err != nil {
		return c, err
	}
	if !c.Enabled || c.Generation != generation {
		return c, access("reload_required", "connection changed; reload Agent")
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, project).Scan(&kind); err != nil {
		return c, err
	}
	if kind != "project" {
		return c, access("invalid_request", "initialization requires a registered project")
	}
	return c, nil
}

func (s *Store) StartInitialization(ctx context.Context, d records.InitializationStart) (records.InitializationBatch, error) {
	var empty records.InitializationBatch
	if s.Schema < 11 {
		return empty, access("initialization_unavailable", "initialization not available")
	}
	if strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 || !filepath.IsAbs(d.Workspace) || len(d.Paths) < 1 || len(d.Paths) > InitializationLimit {
		return empty, access("invalid_request", "stable operation ID, workspace and 1 to 16 materials required")
	}
	canonical, err := filepath.EvalSymlinks(d.Workspace)
	if err != nil {
		return empty, err
	}
	d.Workspace = canonical
	if err = s.CheckBoundWorkspace(ctx, d.Project, canonical); err != nil {
		return empty, err
	}
	// Validate before reading material, and recheck again in the write transaction.
	auth, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	_, err = initializationConnection(ctx, auth, d.ConnectionID, d.Generation, d.Project)
	auth.Rollback()
	if err != nil {
		return empty, err
	}
	digest := UsageDigest(d)
	prior, err := scanInitialization(s.DB.QueryRowContext(ctx, `SELECT `+initializationColumns+` FROM initialization_batches WHERE operation_id=?`, d.OperationID))
	if err == nil {
		var oldDigest string
		if err = s.DB.QueryRowContext(ctx, `SELECT request_digest FROM initialization_batches WHERE id=?`, prior.ID).Scan(&oldDigest); err != nil {
			return empty, err
		}
		if oldDigest != digest {
			return empty, records.ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	sources, err := s.initializationSources(ctx, canonical, d.Paths)
	if err != nil {
		// Another Start may have persisted the original snapshot during this IO.
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return empty, e
		}
		defer tx.Rollback()
		if _, e = initializationConnection(ctx, tx, d.ConnectionID, d.Generation, d.Project); e != nil {
			return empty, e
		}
		prior, lookup := scanInitialization(tx.QueryRowContext(ctx, `SELECT `+initializationColumns+` FROM initialization_batches WHERE operation_id=?`, d.OperationID))
		if lookup == nil {
			var old string
			if e = tx.QueryRowContext(ctx, `SELECT request_digest FROM initialization_batches WHERE id=?`, prior.ID).Scan(&old); e != nil {
				return empty, e
			}
			if old != digest {
				return empty, records.ErrConflict
			}
			return prior, nil
		}
		if !errors.Is(lookup, sql.ErrNoRows) {
			return empty, lookup
		}
		return empty, err
	}
	encoded, _ := json.Marshal(sources)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, err = initializationConnection(ctx, tx, d.ConnectionID, d.Generation, d.Project); err != nil {
		return empty, err
	}
	prior, lookup := scanInitialization(tx.QueryRowContext(ctx, `SELECT `+initializationColumns+` FROM initialization_batches WHERE operation_id=?`, d.OperationID))
	if lookup == nil {
		var old string
		if err = tx.QueryRowContext(ctx, `SELECT request_digest FROM initialization_batches WHERE id=?`, prior.ID).Scan(&old); err != nil {
			return empty, err
		}
		if old != digest {
			return empty, records.ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(lookup, sql.ErrNoRows) {
		return empty, lookup
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM initialization_batches WHERE connection=? AND state='pending'`, d.ConnectionID).Scan(&n); err != nil {
		return empty, err
	}
	if n >= InitializationLimit {
		return empty, access("initialization_limit", "cancel an unsubmitted batch before preparing more")
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO initialization_batches(id,operation_id,request_digest,project,connection,generation,workspace,workspace_identity,snapshot,state,created_ms) VALUES(?,?,?,?,?,?,?,?,?,'pending',?)`, id, d.OperationID, digest, d.Project, d.ConnectionID, d.Generation, canonical, UsageDigest([]string{d.Project, canonical}), string(encoded), time.Now().UnixMilli())
	if err != nil {
		return empty, err
	}
	b, err := scanInitialization(tx.QueryRowContext(ctx, `SELECT `+initializationColumns+` FROM initialization_batches WHERE id=?`, id))
	if err != nil {
		return empty, err
	}
	return b, tx.Commit()
}

func (s *Store) Initialization(ctx context.Context, id string) (records.InitializationBatch, error) {
	b, err := scanInitialization(s.DB.QueryRowContext(ctx, `SELECT `+initializationColumns+` FROM initialization_batches WHERE id=?`, id))
	if err != nil {
		return b, err
	}
	if b.Receipt != nil {
		for _, v := range b.Receipt.Items {
			var dataset, state, index string
			var revision int64
			err = s.DB.QueryRowContext(ctx, `SELECT m.dataset,m.current_revision,m.state,CASE WHEN m.indexed_revision=m.current_revision THEN 'indexed' WHEN j.state='failed' THEN 'failed' ELSE 'pending' END FROM memories m LEFT JOIN jobs j ON j.memory=m.id AND j.revision=m.current_revision WHERE m.id=?`, v.Memory).Scan(&dataset, &revision, &state, &index)
			if err != nil {
				return b, err
			}
			if dataset != b.Project || revision != v.Revision || state != "active" {
				b.Changed++
				continue
			}
			if index == "indexed" {
				b.Indexed++
			} else if index == "failed" {
				b.IndexFailed++
			}
		}
	}
	return b, nil
}
func (s *Store) Initializations(ctx context.Context, project string) ([]records.InitializationBatch, error) {
	query := `SELECT id FROM initialization_batches`
	args := []any{}
	if project != "" {
		query += ` WHERE project=?`
		args = append(args, project)
	}
	query += ` ORDER BY created_ms DESC,id DESC LIMIT 100`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []records.InitializationBatch{}
	for _, id := range ids {
		b, e := s.Initialization(ctx, id)
		if e != nil {
			return nil, e
		}
		b.Sources = nil
		out = append(out, b)
	}
	return out, nil
}
func (s *Store) CancelInitialization(ctx context.Context, id string) (records.InitializationBatch, error) {
	_, err := s.DB.ExecContext(ctx, `UPDATE initialization_batches SET state='cancelled',error_code='' WHERE id=? AND state='pending'`, id)
	if err != nil {
		return records.InitializationBatch{}, err
	}
	return s.Initialization(ctx, id)
}

func (s *Store) CheckInitializationAgent(ctx context.Context, id, connection string, generation int64, workspaceID string, scopes []string) (records.InitializationBatch, error) {
	b, err := s.Initialization(ctx, id)
	if err != nil {
		return b, err
	}
	if b.ConnectionID != connection || b.Generation != generation {
		return b, access("reload_required", "batch belongs to a different connection generation")
	}
	if workspaceID == "" || b.WorkspaceID != workspaceID {
		return b, access("workspace_mismatch", "batch requires the approved worktree")
	}
	allowed := false
	for _, scope := range scopes {
		if scope == b.Project {
			allowed = true
		}
	}
	if !allowed {
		return b, access("scope_denied", "batch is outside requested scopes")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	if _, err = initializationConnection(ctx, tx, connection, generation, b.Project); err != nil {
		return b, err
	}
	return b, nil
}
func (s *Store) ReadInitializationSources(ctx context.Context, b records.InitializationBatch) (records.InitializationBatch, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	if _, err = initializationConnection(ctx, tx, b.ConnectionID, b.Generation, b.Project); err != nil {
		return b, err
	}
	current, err := scanInitialization(tx.QueryRowContext(ctx, `SELECT `+initializationColumns+` FROM initialization_batches WHERE id=?`, b.ID))
	if err != nil {
		return b, err
	}
	if current.State != "pending" && current.State != "received" {
		return b, access("initialization_closed", "batch is cancelled or expired")
	}
	current.SourceReadMS = time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `UPDATE initialization_batches SET source_read_ms=? WHERE id=?`, current.SourceReadMS, b.ID); err != nil {
		return b, err
	}
	return current, tx.Commit()
}

var initializationItemID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func validateInitializationItems(b records.InitializationBatch, d records.InitializationCommit) error {
	if d.Batch != b.ID {
		return access("invalid_request", "batch ID differs from authorized batch")
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 || len(d.Items) < 1 || len(d.Items) > InitializationLimit || len(encoded) > InitializationBytes {
		return access("invalid_request", "initialization needs 1 to 16 items and at most 128 KiB")
	}
	source := map[string]records.InitializationSource{}
	for _, v := range b.Sources {
		source[v.Path] = v
	}
	seen := map[string]bool{}
	for _, item := range d.Items {
		if !initializationItemID.MatchString(item.ID) || seen[item.ID] || strings.TrimSpace(item.Title) == "" || len(item.Title) > 512 || strings.TrimSpace(item.Text) == "" || len(item.Text) > 65536 || len(item.Sources) < 1 || len(item.Sources) > 16 {
			return access("invalid_request", "bounded unique item ID, title, text and exact source quotes required")
		}
		seen[item.ID] = true
		for _, q := range item.Sources {
			original, ok := source[q.Path]
			if !ok || original.SHA256 != q.SHA256 || strings.TrimSpace(q.Quote) == "" || len(q.Quote) > 8192 || !strings.Contains(original.Text, q.Quote) {
				return access("source_quote_mismatch", "item must quote approved file versions exactly")
			}
		}
	}
	return nil
}
func (s *Store) initializationError(ctx context.Context, b records.InitializationBatch, code string, expired bool) {
	state := "pending"
	if expired {
		state = "expired"
	}
	// Do not overwrite a user cancellation or already submitted facts.
	_, _ = s.DB.ExecContext(ctx, `UPDATE initialization_batches SET error_code=?,state=? WHERE id=? AND connection=? AND generation=? AND state='pending'`, code, state, b.ID, b.ConnectionID, b.Generation)
}

func (s *Store) SubmitInitialization(ctx context.Context, b records.InitializationBatch, d records.InitializationCommit) (records.InitializationReceipt, error) {
	var receipt records.InitializationReceipt
	fresh, e := s.Initialization(ctx, b.ID)
	if e != nil {
		return receipt, e
	}
	if fresh.ConnectionID != b.ConnectionID || fresh.Generation != b.Generation || fresh.Project != b.Project || fresh.WorkspaceID != b.WorkspaceID {
		return receipt, access("scope_denied", "batch authorization differs")
	}
	b = fresh
	if err := validateInitializationItems(b, d); err != nil {
		s.initializationError(ctx, b, "invalid_submission", false)
		return receipt, err
	}
	digest := UsageDigest(d)
	// Submitted receipts remain replayable after later filesystem changes.
	if b.State == "received" {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return receipt, err
		}
		defer tx.Rollback()
		if _, err = initializationConnection(ctx, tx, b.ConnectionID, b.Generation, b.Project); err != nil {
			return receipt, err
		}
		var operation, old, raw string
		err = tx.QueryRowContext(ctx, `SELECT submit_id,submit_digest,receipt FROM initialization_batches WHERE id=? AND state='received'`, b.ID).Scan(&operation, &old, &raw)
		if err != nil {
			return receipt, err
		}
		if operation != d.OperationID || old != digest {
			return receipt, records.ErrConflict
		}
		err = json.Unmarshal([]byte(raw), &receipt)
		return receipt, err
	}

	if b.State != "pending" {
		return receipt, access("initialization_closed", "batch is cancelled or expired")
	}
	paths := []string{}
	for _, v := range b.Sources {
		paths = append(paths, v.Path)
	}
	current, err := s.initializationSources(ctx, b.Workspace, paths)
	if err != nil || UsageDigest(current) != UsageDigest(b.Sources) {
		// A concurrent identical submit may have committed during filesystem IO.
		// Inspect and fence the durable state atomically before expiring anything.
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return receipt, e
		}
		defer tx.Rollback()
		if _, e = initializationConnection(ctx, tx, b.ConnectionID, b.Generation, b.Project); e != nil {
			return receipt, e
		}
		var state, operation, old, raw string
		e = tx.QueryRowContext(ctx, `SELECT state,submit_id,submit_digest,receipt FROM initialization_batches WHERE id=?`, b.ID).Scan(&state, &operation, &old, &raw)
		if e != nil {
			return receipt, e
		}
		if state == "received" {
			if operation != d.OperationID || old != digest {
				return receipt, records.ErrConflict
			}
			e = json.Unmarshal([]byte(raw), &receipt)
			return receipt, e
		}
		if state != "pending" {
			return receipt, access("initialization_closed", "batch is cancelled or expired")
		}
		if _, e = tx.ExecContext(ctx, `UPDATE initialization_batches SET state='expired',error_code='source_changed' WHERE id=? AND state='pending'`, b.ID); e != nil {
			return receipt, e
		}
		if e = tx.Commit(); e != nil {
			return receipt, e
		}
		return receipt, access("source_changed", "approved materials changed; prepare a new batch")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback()
	c, err := initializationConnection(ctx, tx, b.ConnectionID, b.Generation, b.Project)
	if err != nil {
		return receipt, err
	}
	var state, operation, oldDigest, oldReceipt string
	err = tx.QueryRowContext(ctx, `SELECT state,submit_id,submit_digest,receipt FROM initialization_batches WHERE id=?`, b.ID).Scan(&state, &operation, &oldDigest, &oldReceipt)
	if err != nil {
		return receipt, err
	}
	if state == "received" {
		if operation != d.OperationID || oldDigest != digest {
			return receipt, records.ErrConflict
		}
		err = json.Unmarshal([]byte(oldReceipt), &receipt)
		return receipt, err
	}
	if state != "pending" {
		return receipt, access("initialization_closed", "batch is cancelled or expired")
	}
	receipt = records.InitializationReceipt{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(b.ID+"/"+d.OperationID)).String(), Batch: b.ID, Persisted: true, Items: []records.InitializationSaved{}}
	for _, item := range d.Items {
		memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte("initialization/"+b.Project+"/"+item.ID)).String()
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE id=?`, memory).Scan(&exists); err != nil {
			return receipt, err
		}
		if exists != 0 {
			return records.InitializationReceipt{}, access("initialization_item_exists", "stable item already exists; changes require user review")
		}
		references := []string{}
		fragments := []string{}
		for _, q := range item.Sources {
			references = append(references, "repo://"+b.Project+"/"+q.Path+"?sha256="+q.SHA256)
			fragments = append(fragments, q.Path+" @ "+q.SHA256+"\n"+q.Quote)
		}
		v, e := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "initialization:" + memory, Memory: memory, Dataset: b.Project, Text: item.Text, Title: item.Title, Origin: "imported", Source: c.Agent + " 初始化导入", SourceDetail: &records.AgentSource{References: references, Fragment: strings.Join(fragments, "\n\n")}})
		if e != nil {
			return records.InitializationReceipt{}, e
		}
		// Actual import receipt is distinct from the first successful index event.
		if _, err = tx.ExecContext(ctx, `INSERT INTO activities(memory,kind,occurred_at) VALUES(?,'imported',strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, memory); err != nil {
			return records.InitializationReceipt{}, err
		}
		receipt.Items = append(receipt.Items, records.InitializationSaved{ID: item.ID, Memory: memory, Revision: v.Revision})
	}
	encoded, _ := json.Marshal(receipt)
	_, err = tx.ExecContext(ctx, `UPDATE initialization_batches SET state='received',error_code='',submit_id=?,submit_digest=?,receipt=? WHERE id=? AND state='pending'`, d.OperationID, digest, string(encoded), b.ID)
	if err != nil {
		return records.InitializationReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return records.InitializationReceipt{}, err
	}
	return receipt, nil
}

func (s *Store) NoteInitializationFailure(ctx context.Context, b records.InitializationBatch, code string) {
	s.initializationError(ctx, b, code, false)
}
