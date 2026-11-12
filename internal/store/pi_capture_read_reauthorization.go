package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Metadata only. The original binding, capture consent and saved receipt
// remain immutable. This grants reading those receipts at a new connection
// generation, without granting history or enabling future capture.
type PiCaptureReadGrant struct {
	Review            records.CogneeReadReauthorization `json:"review"`
	Library           string                            `json:"library"`
	WorkspaceDigest   string                            `json:"workspace_digest"`
	WorkspacePhysical string                            `json:"workspace_physical"`
	PolicyDigest      string                            `json:"policy_digest"`
}

func migratePiCaptureReadGrants(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE pi_capture_read_grants(operation TEXT PRIMARY KEY,binding TEXT NOT NULL,connection TEXT NOT NULL,project TEXT NOT NULL,generation INTEGER NOT NULL,payload TEXT NOT NULL); CREATE UNIQUE INDEX pi_capture_read_generation ON pi_capture_read_grants(binding,connection,project,generation); PRAGMA user_version=35;`)
	return err
}

func (s *Store) PreparePiCaptureReadReauthorization(ctx context.Context, q records.CogneeReadReauthorization, policy string) (PiCaptureReadGrant, error) {
	var out PiCaptureReadGrant
	p := q.Previous
	for _, v := range []string{q.Operation, p.Operation, p.Instance, p.Dataset, p.Project, p.ConnectionID} {
		id, err := uuid.Parse(v)
		if err != nil || id.String() != v {
			return out, access("invalid_request", "canonical captured read identities required")
		}
	}
	if !p.MaterialSubmission || p.ReadPreviousOperation != "" || p.ReadReviewDigest != "" || q.Operation == p.Operation || q.Generation <= p.Generation || !officialCaptureHash.MatchString(policy) {
		return out, access("invalid_request", "explicit new captured read generation required")
	}
	bindings, err := s.CogneeBindings(ctx, p.Project)
	if err != nil {
		return out, err
	}
	matched := 0
	for _, b := range bindings {
		if b == p {
			matched++
		}
	}
	if matched != 1 {
		return out, access("scope_changed", "original installed capture binding required")
	}
	next := p
	next.Generation = q.Generation
	current, err := s.piCaptureWorkspaceAuthority(ctx, next)
	if err != nil {
		return out, err
	}
	if _, err = s.piCaptureOriginalGrants(ctx, p, current, policy); err != nil {
		return out, err
	}
	out = PiCaptureReadGrant{Review: q, Library: current.Library, WorkspaceDigest: current.WorkspaceDigest, WorkspacePhysical: current.WorkspacePhysical, PolicyDigest: policy}
	return out, nil
}

func (s *Store) ReauthorizePiCaptureRead(ctx context.Context, q records.CogneeReadReauthorization, policy string) (PiCaptureReadGrant, error) {
	out, err := s.PreparePiCaptureReadReauthorization(ctx, q, policy)
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var enabled bool
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT enabled,generation FROM connections WHERE id=?`, q.Previous.ConnectionID).Scan(&enabled, &generation); err != nil {
		return out, err
	}
	if !enabled || generation != q.Generation {
		return out, access("scope_changed", "captured read generation changed")
	}
	var bindingJSON string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM cognee_bindings WHERE instance=? AND dataset=? AND project=?`, q.Previous.Instance, q.Previous.Dataset, q.Previous.Project).Scan(&bindingJSON); err != nil {
		return out, err
	}
	var installed records.CogneeBinding
	if json.Unmarshal([]byte(bindingJSON), &installed) != nil || installed != q.Previous {
		return out, access("scope_changed", "installed capture binding changed")
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT payload FROM pi_capture_read_grants WHERE operation=? OR (binding=? AND connection=? AND project=? AND generation=?)`, q.Operation, q.Previous.Operation, q.Previous.ConnectionID, q.Previous.Project, q.Generation).Scan(&prior)
	if err == nil {
		if prior != string(raw) {
			return out, ErrConflict
		}
		return out, nil
	}
	if err != sql.ErrNoRows {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pi_capture_read_grants(operation,binding,connection,project,generation,payload) VALUES(?,?,?,?,?,?)`, q.Operation, q.Previous.Operation, q.Previous.ConnectionID, q.Previous.Project, q.Generation, string(raw))
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Only used by the trusted review operation, before committing its read grant.
func (s *Store) PiCaptureReadReviewDeliveries(ctx context.Context, q records.CogneeReadReauthorization, policy string) ([]records.OfficialCaptureDelivery, error) {
	g, err := s.PreparePiCaptureReadReauthorization(ctx, q, policy)
	if err != nil {
		return nil, err
	}
	current := records.PiCaptureConsent{Library: g.Library, WorkspaceDigest: g.WorkspaceDigest, WorkspacePhysical: g.WorkspacePhysical}
	grants, err := s.piCaptureOriginalGrants(ctx, q.Previous, current, policy)
	if err != nil {
		return nil, err
	}
	return s.capturedDeliveriesForGrants(ctx, q.Previous, g.Library, grants)
}

func (s *Store) piCaptureReadAuthority(ctx context.Context, b records.CogneeBinding, policy string) (records.PiCaptureConsent, error) {
	var out records.PiCaptureConsent
	c, err := s.Connection(ctx, b.ConnectionID)
	if err != nil {
		return out, err
	}
	if !c.Enabled {
		return out, access("connection_revoked", "captured read connection disabled")
	}
	if c.Generation <= b.Generation {
		return out, access("scope_denied", "current captured read review required")
	}
	var raw string
	err = s.DB.QueryRowContext(ctx, `SELECT payload FROM pi_capture_read_grants WHERE binding=? AND connection=? AND project=? AND generation=?`, b.Operation, b.ConnectionID, b.Project, c.Generation).Scan(&raw)
	if err == sql.ErrNoRows {
		return out, access("scope_changed", "review saved captured entries at the current generation")
	}
	if err != nil {
		return out, err
	}
	var grant PiCaptureReadGrant
	if err = json.Unmarshal([]byte(raw), &grant); err != nil {
		return out, err
	}
	if grant.Review.Previous != b || grant.Review.Generation != c.Generation {
		return out, access("scope_changed", "captured read review changed")
	}
	current, err := s.PreparePiCaptureReadReauthorization(ctx, grant.Review, policy)
	if err != nil {
		return out, err
	}
	if current != grant {
		return out, access("scope_changed", "captured read identity changed")
	}
	out.Binding, out.Library, out.WorkspaceDigest, out.WorkspacePhysical = b, grant.Library, grant.WorkspaceDigest, grant.WorkspacePhysical
	return out, nil
}
