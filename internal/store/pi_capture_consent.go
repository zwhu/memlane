package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migratePiCaptureConsent(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE pi_capture_consents(operation TEXT PRIMARY KEY,key TEXT NOT NULL UNIQUE,connection TEXT NOT NULL,project TEXT NOT NULL,payload TEXT NOT NULL,enabled INTEGER NOT NULL CHECK(enabled IN (0,1))); CREATE UNIQUE INDEX pi_capture_one_active ON pi_capture_consents(connection,project) WHERE enabled=1; PRAGMA user_version=34;`)
	return err
}

// Resolve the actual currently installed material binding and physical pi
// workspace. Connection/tool access by itself never implies capture consent.
func (s *Store) piCaptureAuthority(ctx context.Context, b records.CogneeBinding) (records.PiCaptureConsent, error) {
	out, err := s.piCaptureWorkspaceAuthority(ctx, b)
	if err != nil {
		return out, err
	}
	bindings, err := s.CogneeBindings(ctx, b.Project)
	if err != nil {
		return out, err
	}
	matched := 0
	for _, current := range bindings {
		if current == b {
			matched++
		}
	}
	if matched != 1 {
		return out, access("scope_changed", "current installed official binding required")
	}
	return out, nil
}

// Resolves a current connection/workspace identity without changing the
// installed binding or granting access to any saved operation.
func (s *Store) piCaptureWorkspaceAuthority(ctx context.Context, b records.CogneeBinding) (records.PiCaptureConsent, error) {
	var out records.PiCaptureConsent
	if !b.MaterialSubmission || b.ReadReviewDigest != "" || b.ReadPreviousOperation != "" {
		return out, access("scope_denied", "new-message material capture binding required")
	}
	c, scopes, err := s.ResolveAgentScopesReadOnly(ctx, records.AgentRequest{Protocol: 1, ConnectionID: b.ConnectionID, Generation: b.Generation, Workspace: b.Workspace, Tool: "memory_save", Channel: "daily", Arguments: records.AgentArguments{Scope: b.Project}})
	if err != nil {
		return out, err
	}
	if c.Agent != "pi" || c.ContextProject != b.Project || len(scopes) != 1 || scopes[0] != b.Project {
		return out, access("scope_denied", "current explicit pi project grant required")
	}
	canonical, digest, err := DeliveryWorkspaceIdentity(ctx, b.Project, b.Workspace)
	if err != nil {
		return out, err
	}
	physical, err := WorkspaceDirectoryIdentity(canonical)
	if err != nil {
		return out, err
	}
	library, err := s.CogneeLibraryIdentity(ctx)
	if err != nil {
		return out, err
	}
	out.Binding, out.Library, out.WorkspaceDigest, out.WorkspacePhysical = b, library, digest, physical
	return out, nil
}

func scanPiCaptureConsent(row *sql.Row) (records.PiCaptureConsent, error) {
	var out records.PiCaptureConsent
	var payload, operation, key string
	var enabled bool
	err := row.Scan(&operation, &key, &payload, &enabled)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(payload), &out); err != nil {
		return records.PiCaptureConsent{}, err
	}
	if out.Operation != operation || out.Key != key {
		return records.PiCaptureConsent{}, ErrConflict
	}
	out.Enabled = enabled
	return out, nil
}

// This is called only after explicit trusted App confirmation. It does not
// install an extension, read a transcript, promote a binding to history access,
// or turn an old consent operation into a new authorization on replay.
func (s *Store) ReviewPiCaptureConsent(ctx context.Context, q records.PiCaptureConsentReview, policy string) (records.PiCaptureConsent, error) {
	var empty records.PiCaptureConsent
	if id, err := uuid.Parse(q.Operation); err != nil || id.String() != q.Operation || !officialCaptureHash.MatchString(policy) || q.PreviousKey != "" && !officialCaptureHash.MatchString(q.PreviousKey) {
		return empty, access("invalid_request", "canonical capture review and pinned policy required")
	}
	grant, err := s.piCaptureAuthority(ctx, q.Binding)
	if err != nil {
		return empty, err
	}
	grant.Operation, grant.PolicyDigest, grant.Enabled = q.Operation, policy, true
	grant.PreviousKey = q.PreviousKey
	grant.Key = records.MergeDigest(grant)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var library, payload string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return empty, err
	}
	if library != grant.Library {
		return empty, access("delivery_library_changed", "capture library changed during review")
	}
	c, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, q.Binding.ConnectionID))
	if err != nil {
		return empty, err
	}
	allowed := false
	for _, scope := range c.AllowedScopes {
		allowed = allowed || scope == q.Binding.Project
	}
	if c.Agent != "pi" || !c.Enabled || c.Generation != q.Binding.Generation || !allowed {
		return empty, access("scope_changed", "pi authority changed during review")
	}
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM cognee_bindings WHERE instance=? AND dataset=? AND project=?`, q.Binding.Instance, q.Binding.Dataset, q.Binding.Project).Scan(&payload); err != nil {
		return empty, err
	}
	if payload != string(mustCogneeJSON(q.Binding)) {
		return empty, ErrConflict
	}
	prior, err := scanPiCaptureConsent(tx.QueryRowContext(ctx, `SELECT operation,key,payload,enabled FROM pi_capture_consents WHERE operation=?`, q.Operation))
	if err == nil {
		if prior.Key != grant.Key {
			return empty, ErrConflict
		}
		return prior, nil // A revoked operation stays revoked, even on replay.
	}
	if err != sql.ErrNoRows {
		return empty, err
	}
	latest, err := scanPiCaptureConsent(tx.QueryRowContext(ctx, `SELECT operation,key,payload,enabled FROM pi_capture_consents WHERE connection=? AND project=? ORDER BY rowid DESC LIMIT 1`, q.Binding.ConnectionID, q.Binding.Project))
	if err != nil && err != sql.ErrNoRows {
		return empty, err
	}
	if err == sql.ErrNoRows && q.PreviousKey != "" || err == nil && q.PreviousKey != latest.Key {
		return empty, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE pi_capture_consents SET enabled=0 WHERE connection=? AND project=? AND enabled=1`, q.Binding.ConnectionID, q.Binding.Project); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pi_capture_consents(operation,key,connection,project,payload,enabled) VALUES(?,?,?,?,?,1)`, grant.Operation, grant.Key, q.Binding.ConnectionID, q.Binding.Project, string(mustCogneeJSON(grant))); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return grant, nil
}

func (s *Store) DisablePiCaptureConsent(ctx context.Context, q records.PiCaptureConsentDisable) error {
	if !officialCaptureHash.MatchString(q.ExpectedKey) {
		return access("invalid_request", "reviewed capture key required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE pi_capture_consents SET enabled=0 WHERE key=? AND connection=? AND project=?`, q.ExpectedKey, q.Connection, q.Project)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

// The host must supply daily intent and trusted ctx.cwd, not model parameters.
// Reading this grant rechecks current connection, library, binding and physical
// workspace; it cannot enumerate or import any previous session.
func (s *Store) AuthorizePiCapture(ctx context.Context, r records.AgentRequest, policy string) (records.PiCaptureConsent, error) {
	var empty records.PiCaptureConsent
	if r.Protocol != 1 || r.Tool != "memory_save" || r.Channel != "daily" || r.ToolMode != "" || r.Workspace == "" || len(r.Arguments.Scopes) != 0 || !officialCaptureHash.MatchString(policy) {
		return empty, access("scope_denied", "ordinary daily pi capture intent required")
	}
	if r.Arguments.Scope == "" {
		connection, _, err := s.ResolveAgentScopesReadOnly(ctx, r)
		if err != nil {
			return empty, err
		}
		if connection.Agent != "pi" || connection.ContextProject == "" {
			return empty, access("scope_required", "approved current pi project required")
		}
		// Resolve only the trusted cwd's current project. Tool access still
		// does not imply consent, and global grants are never capture targets.
		r.Arguments.Scope = connection.ContextProject
	}
	grant, err := scanPiCaptureConsent(s.DB.QueryRowContext(ctx, `SELECT operation,key,payload,enabled FROM pi_capture_consents WHERE connection=? AND project=? AND enabled=1`, r.ConnectionID, r.Arguments.Scope))
	if err != nil {
		return empty, access("scope_denied", "explicit enabled pi capture consent required")
	}
	if grant.Binding.Generation != r.Generation || grant.PolicyDigest != policy {
		return empty, access("scope_changed", "reviewed pi capture authority changed")
	}
	current, err := s.piCaptureAuthority(ctx, grant.Binding)
	if err != nil {
		return empty, err
	}
	if current.Library != grant.Library || current.WorkspacePhysical != grant.WorkspacePhysical || current.WorkspaceDigest != grant.WorkspaceDigest {
		return empty, access("scope_changed", "reviewed pi capture workspace or library changed")
	}
	canonical, digest, err := DeliveryWorkspaceIdentity(ctx, grant.Binding.Project, r.Workspace)
	if err != nil {
		return empty, err
	}
	physical, err := WorkspaceDirectoryIdentity(canonical)
	if err != nil {
		return empty, err
	}
	if physical != grant.WorkspacePhysical || digest != grant.WorkspaceDigest {
		return empty, access("scope_changed", "pi host workspace differs from reviewed workspace")
	}
	return grant, nil
}
