package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) ChangeProjectAccess(ctx context.Context, d records.ConnectionScopeChange) (records.Connection, error) {
	for _, id := range []string{d.ConnectionID, d.Project} {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return records.Connection{}, access("invalid_request", "explicit connection and project required")
		}
	}
	if d.ExpectedGeneration < 1 {
		return records.Connection{}, access("invalid_request", "current connection generation required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.Connection{}, err
	}
	defer tx.Rollback()
	c, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, d.ConnectionID))
	if err != nil {
		return c, err
	}
	if !c.Enabled || c.Generation != d.ExpectedGeneration {
		return c, access("conflict", "connection changed; reload project access")
	}
	if s.Schema >= 14 {
		var retired int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, c.ID).Scan(&retired); err != nil {
			return c, err
		}
		if retired > 0 {
			return c, access("restore_reconnect_required", "reconnect this retired connection")
		}
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, d.Project).Scan(&kind); err != nil {
		return c, access("scope_denied", "registered project required")
	}
	if kind != "project" {
		return c, access("scope_denied", "project grant required")
	}
	if !d.Allowed && (c.Project == d.Project || c.PendingProject == d.Project) {
		return c, access("bound_project", "disconnect or change the bound connection before revoking its primary project")
	}
	allowed := make([]string, 0, len(c.AllowedScopes)+1)
	found := false
	for _, id := range c.AllowedScopes {
		if id == d.Project {
			found = true
			if !d.Allowed {
				continue
			}
		}
		allowed = append(allowed, id)
	}
	if found == d.Allowed {
		return c, tx.Commit()
	}
	if d.Allowed {
		allowed = append(allowed, d.Project)
	}
	if len(allowed) == 0 || len(allowed) > 100 {
		return c, access("scope_denied", "keep a valid explicit allowed scope set")
	}
	encoded, err := json.Marshal(allowed)
	if err != nil {
		return c, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE connections SET allowed_scopes=?,generation=generation+1,last_seen='' WHERE id=? AND enabled=1 AND generation=?`, string(encoded), c.ID, d.ExpectedGeneration)
	if err != nil {
		return c, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return c, err
	}
	if n != 1 {
		return c, access("conflict", "connection changed during project access save")
	}
	c, err = scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, c.ID))
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
