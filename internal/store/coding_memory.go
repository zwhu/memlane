package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func migrateCodingMemoryGrants(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE coding_memory_grants(instance TEXT NOT NULL,dataset TEXT NOT NULL,project TEXT NOT NULL REFERENCES scopes(id),binding TEXT NOT NULL,user TEXT NOT NULL,generation INTEGER NOT NULL,enabled INTEGER NOT NULL,changed_ms INTEGER NOT NULL,PRIMARY KEY(instance,dataset)); PRAGMA user_version=36;`)
	return err
}

func codingMemoryGrantRow(row scanner) (records.CodingMemoryGrant, error) {
	var grant records.CodingMemoryGrant
	var binding string
	err := row.Scan(&binding, &grant.User, &grant.Generation, &grant.Enabled, &grant.ChangedMS)
	if err == nil {
		err = json.Unmarshal([]byte(binding), &grant.Binding)
	}
	return grant, err
}

// This returns the persisted permission, not runtime readiness. Call
// AuthorizeCodingMemoryGrant before executing or accepting a model result.
func (s *Store) CodingMemoryGrant(ctx context.Context, b records.CogneeBinding) (records.CodingMemoryGrant, error) {
	grant, err := codingMemoryGrantRow(s.DB.QueryRowContext(ctx, `SELECT binding,user,generation,enabled,changed_ms FROM coding_memory_grants WHERE instance=? AND dataset=? AND project=?`, b.Instance, b.Dataset, b.Project))
	if err == sql.ErrNoRows {
		return records.CodingMemoryGrant{Binding: b}, nil
	}
	return grant, err
}

func (s *Store) CodingMemoryGrantForDataset(ctx context.Context, instance, dataset string) (records.CodingMemoryGrant, error) {
	return codingMemoryGrantRow(s.DB.QueryRowContext(ctx, `SELECT binding,user,generation,enabled,changed_ms FROM coding_memory_grants WHERE instance=? AND dataset=?`, instance, dataset))
}

func (s *Store) ChangeCodingMemoryGrant(ctx context.Context, q records.CodingMemoryGrantChange) (records.CodingMemoryGrant, error) {
	var empty records.CodingMemoryGrant
	for _, id := range []string{q.Operation, q.Binding.Operation, q.Binding.Instance, q.Binding.Dataset, q.Binding.Project, q.User} {
		if !processingUUID(id) {
			return empty, access("invalid_request", "canonical coding memory scope required")
		}
	}
	if q.ExpectedGeneration < 0 || q.Binding.MaterialSubmission {
		return empty, access("invalid_request", "explicit coding memory binding required")
	}
	if q.Enabled {
		if err := s.AuthorizeCogneeBinding(ctx, q.Binding); err != nil {
			return empty, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	digest := records.MergeDigest(struct {
		Kind   string
		Change records.CodingMemoryGrantChange
	}{"coding_memory_grant_v1", q})
	var replay records.CodingMemoryGrant
	if found, e := processingReplay(ctx, tx, q.Operation, digest, &replay); found || e != nil {
		// Replaying an old enable returns only its historical receipt. It never
		// applies again and cannot undo a later disable.
		return replay, e
	}
	old, err := codingMemoryGrantRow(tx.QueryRowContext(ctx, `SELECT binding,user,generation,enabled,changed_ms FROM coding_memory_grants WHERE instance=? AND dataset=?`, q.Binding.Instance, q.Binding.Dataset))
	if err != nil && err != sql.ErrNoRows {
		return empty, err
	}
	if old.Generation != q.ExpectedGeneration {
		return empty, ErrConflict
	}
	if !q.Enabled && old.Generation > 0 && (old.Binding != q.Binding || old.User != q.User) {
		return empty, ErrConflict
	}
	if q.Enabled {
		var binding string
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM cognee_bindings WHERE instance=? AND dataset=? AND project=?`, q.Binding.Instance, q.Binding.Dataset, q.Binding.Project).Scan(&binding); err != nil {
			return empty, err
		}
		if binding != string(mustCogneeJSON(q.Binding)) {
			return empty, ErrConflict
		}
		var enabled bool
		var generation int64
		if err = tx.QueryRowContext(ctx, `SELECT enabled,generation FROM connections WHERE id=?`, q.Binding.ConnectionID).Scan(&enabled, &generation); err != nil {
			return empty, err
		}
		if !enabled || generation != q.Binding.Generation {
			return empty, access("scope_changed", "coding memory connection changed")
		}
	}
	grant := records.CodingMemoryGrant{Binding: q.Binding, User: q.User, Enabled: q.Enabled, Generation: old.Generation + 1, ChangedMS: time.Now().UnixMilli()}
	_, err = tx.ExecContext(ctx, `INSERT INTO coding_memory_grants(instance,dataset,project,binding,user,generation,enabled,changed_ms) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance,dataset) DO UPDATE SET project=excluded.project,binding=excluded.binding,user=excluded.user,generation=excluded.generation,enabled=excluded.enabled,changed_ms=excluded.changed_ms`, q.Binding.Instance, q.Binding.Dataset, q.Binding.Project, string(mustCogneeJSON(q.Binding)), q.User, grant.Generation, q.Enabled, grant.ChangedMS)
	if err == nil {
		err = processingRecord(ctx, tx, q.Operation, digest, grant)
	}
	if err == nil {
		err = tx.Commit()
	}
	return grant, err
}

// Re-check the exact generation on every run and before releasing results.
func (s *Store) AuthorizeCodingMemoryGrant(ctx context.Context, expected records.CodingMemoryGrant) error {
	if !expected.Enabled || expected.Generation < 1 {
		return access("coding_memory_disabled", "coding memory requires an explicit project grant")
	}
	current, err := s.CodingMemoryGrant(ctx, expected.Binding)
	if err != nil {
		return err
	}
	if current != expected {
		return access("scope_changed", "coding memory grant changed")
	}
	if err = s.AuthorizeCogneeBinding(ctx, expected.Binding); err != nil {
		return err
	}
	var binding string
	if err = s.DB.QueryRowContext(ctx, `SELECT payload FROM cognee_bindings WHERE instance=? AND dataset=? AND project=?`, expected.Binding.Instance, expected.Binding.Dataset, expected.Binding.Project).Scan(&binding); err != nil {
		return err
	}
	if binding != string(mustCogneeJSON(expected.Binding)) {
		return access("scope_changed", "coding memory dataset binding changed")
	}
	return nil
}
