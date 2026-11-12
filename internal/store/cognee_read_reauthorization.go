package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Prepare returns only a current, explicitly scoped candidate. In particular,
// ordinary BindCognee and a stale grant never refresh a historical read grant.
func (s *Store) PrepareCogneeReadReauthorization(ctx context.Context, q records.CogneeReadReauthorization) (records.CogneeBinding, error) {
	p := q.Previous
	for _, v := range []string{p.Operation, p.Instance, p.Dataset, p.Project, p.ConnectionID, q.Operation} {
		id, e := uuid.Parse(v)
		if e != nil || id.String() != v {
			return records.CogneeBinding{}, access("invalid_request", "canonical read authorization identities required")
		}
	}
	if p.MaterialSubmission || q.Operation == p.Operation || q.Generation <= p.Generation {
		return records.CogneeBinding{}, access("invalid_request", "a new explicit read authorization generation required")
	}
	b := p
	b.Generation = q.Generation
	b.Operation = q.Operation
	b.ReadPreviousOperation = p.Operation
	b.ReadReviewDigest = records.MergeDigest(q)
	if err := s.AuthorizeCogneeBinding(ctx, b); err != nil {
		return records.CogneeBinding{}, err
	}
	rows, err := s.CogneeBindings(ctx, p.Project)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	for _, current := range rows {
		if current.Instance == p.Instance && current.Dataset == p.Dataset {
			if current == p || current == b {
				return b, nil
			}
			return records.CogneeBinding{}, ErrConflict
		}
	}
	return records.CogneeBinding{}, access("scope_denied", "previous official read binding required")
}

func (s *Store) ReauthorizeCogneeRead(ctx context.Context, q records.CogneeReadReauthorization) (records.CogneeBinding, error) {
	b, err := s.PrepareCogneeReadReauthorization(ctx, q)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	encode := func(v records.CogneeBinding) (string, string) {
		raw, _ := json.Marshal(v)
		sum := sha256.Sum256(raw)
		return string(raw), hex.EncodeToString(sum[:])
	}
	prior, priorDigest := encode(q.Previous)
	next, digest := encode(b)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	defer tx.Rollback()
	var enabled bool
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT enabled,generation FROM connections WHERE id=?`, b.ConnectionID).Scan(&enabled, &generation); err != nil {
		return records.CogneeBinding{}, err
	}
	if !enabled || generation != b.Generation {
		return records.CogneeBinding{}, access("scope_changed", "read authorization generation changed")
	}
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM cognee_bindings WHERE instance=? AND dataset=? AND project=?`, b.Instance, b.Dataset, b.Project).Scan(&current); err != nil {
		return records.CogneeBinding{}, err
	}
	if current == next {
		return b, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE cognee_bindings SET operation=?,digest=?,payload=? WHERE instance=? AND dataset=? AND project=? AND operation=? AND digest=? AND payload=? AND EXISTS(SELECT 1 FROM connections WHERE id=? AND enabled=1 AND generation=?)`, b.Operation, digest, next, b.Instance, b.Dataset, b.Project, q.Previous.Operation, priorDigest, prior, b.ConnectionID, b.Generation)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return records.CogneeBinding{}, err
	}
	if n != 1 {
		return records.CogneeBinding{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return records.CogneeBinding{}, err
	}
	return b, nil
}
