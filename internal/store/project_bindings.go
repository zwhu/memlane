package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func projectUUID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v.String() == id
}

func registeredProject(ctx context.Context, tx *sql.Tx, id string) error {
	if !projectUUID(id) {
		return access("invalid_request", "registered project UUID required")
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, id).Scan(&kind); err != nil {
		return err
	}
	if kind != "project" {
		return access("scope_denied", "organizer binding requires a project")
	}
	return nil
}

func (s *Store) projectBindingTx(ctx context.Context, tx *sql.Tx, project string) (records.ProjectBinding, error) {
	b := records.ProjectBinding{Project: project, State: "unbound"}
	if err := registeredProject(ctx, tx, project); err != nil {
		return b, err
	}
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(connection,''),generation,revision FROM project_bindings WHERE project=?`, project).Scan(&b.ConnectionID, &b.Generation, &b.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil || b.ConnectionID == "" {
		return b, err
	}
	c, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, b.ConnectionID))
	if err != nil {
		return b, err
	}
	b.Agent = c.Agent
	var retired int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, c.ID).Scan(&retired); err != nil {
		return b, err
	}
	allowed := false
	for _, id := range c.AllowedScopes {
		allowed = allowed || id == project
	}
	switch {
	case retired > 0:
		b.State = "retired"
	case !c.Enabled:
		b.State = "disconnected"
	case !allowed:
		b.State = "scope_denied"
	case c.Generation != b.Generation:
		b.State = "changed"
	case c.LastSeen == "":
		b.State = "unverified"
	default:
		b.State = "available"
	}
	return b, nil
}

func (s *Store) ProjectBinding(ctx context.Context, project string) (records.ProjectBinding, error) {
	if s.Schema < 18 {
		return records.ProjectBinding{}, access("project_task_unavailable", "project organizer schema unavailable")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.ProjectBinding{}, err
	}
	defer tx.Rollback()
	return s.projectBindingTx(ctx, tx, project)
}

func (s *Store) ChangeProjectBinding(ctx context.Context, d records.ProjectBindingChange) (records.ProjectBinding, error) {
	if s.Schema < 18 || d.ExpectedRevision < 0 || d.ConnectionID != "" && (!projectUUID(d.ConnectionID) || d.ExpectedGeneration < 1) {
		return records.ProjectBinding{}, access("invalid_request", "explicit project binding and current revisions required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.ProjectBinding{}, err
	}
	defer tx.Rollback()
	prior, err := s.projectBindingTx(ctx, tx, d.Project)
	if err != nil {
		return prior, err
	}
	if prior.Revision != d.ExpectedRevision {
		return prior, records.ErrConflict
	}
	var live int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM project_tasks WHERE project=? AND state IN ('queued','starting','organizing','saving','indexing','cancelling')`, d.Project).Scan(&live); err != nil {
		return prior, err
	}
	if live != 0 {
		return prior, access("project_task_active", "stop the current project task before changing its executor")
	}
	var connection any
	generation := int64(0)
	if d.ConnectionID != "" {
		c, e := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, d.ConnectionID))
		if e != nil {
			return prior, e
		}
		allowed := false
		for _, id := range c.AllowedScopes {
			allowed = allowed || id == d.Project
		}
		var retired int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, c.ID).Scan(&retired); e != nil {
			return prior, e
		}
		if !c.Enabled || c.Generation != d.ExpectedGeneration || c.LastSeen == "" || !allowed || retired > 0 {
			return prior, access("binding_unavailable", "select a currently verified connection allowed in this project")
		}
		connection, generation = c.ID, c.Generation
	}
	if prior.ConnectionID == d.ConnectionID && prior.Generation == generation {
		return prior, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO project_bindings(project,connection,generation,revision) VALUES(?,?,?,1)
 ON CONFLICT(project) DO UPDATE SET connection=excluded.connection,generation=excluded.generation,revision=project_bindings.revision+1`, d.Project, connection, generation)
	if err != nil {
		return prior, err
	}
	b, err := s.projectBindingTx(ctx, tx, d.Project)
	if err != nil {
		return b, err
	}
	return b, tx.Commit()
}
