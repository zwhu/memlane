package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func access(code, message string) error { return &records.AccessError{Code: code, Message: message} }

const connectionColumns = `id,agent,name,project,allowed_scopes,enabled,generation,last_seen`

// Keep the shipped connection table unchanged for old snapshots and callers.
// Deferred grants are an additive table, projected only in schema16 onward.
func (s *Store) connectionProjection() string {
	if s.Schema < 16 {
		return connectionColumns + `,0,'','',''`
	}
	return connectionColumns + `,COALESCE((SELECT auto_project FROM connection_intakes i WHERE i.connection_id=connections.id),0),COALESCE((SELECT pending_project FROM connection_intakes i WHERE i.connection_id=connections.id),''),COALESCE((SELECT trusted_workspace FROM connection_intakes i WHERE i.connection_id=connections.id),''),COALESCE((SELECT trusted_repository FROM connection_intakes i WHERE i.connection_id=connections.id),'')`
}

func scanConnection(row scanner) (records.Connection, error) {
	var c records.Connection
	var scopes string
	err := row.Scan(&c.ID, &c.Agent, &c.Name, &c.Project, &scopes, &c.Enabled, &c.Generation, &c.LastSeen, &c.AutoProject, &c.PendingProject, &c.TrustedWorkspace, &c.TrustedRepository)
	if err == nil {
		err = json.Unmarshal([]byte(scopes), &c.AllowedScopes)
	}
	return c, err
}

func (s *Store) Connection(ctx context.Context, id string) (records.Connection, error) {
	return scanConnection(s.DB.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, id))
}

func (s *Store) Connections(ctx context.Context) ([]records.Connection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []records.Connection{}
	for rows.Next() {
		c, e := scanConnection(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *Store) ConfigureConnection(ctx context.Context, d records.ConnectionDraft) (records.Connection, error) {
	if d.Agent != "codex" && d.Agent != "pi" && d.Agent != "traex" && d.Agent != "trae" {
		return records.Connection{}, access("invalid_request", "unsupported Agent")
	}
	if d.ConnectionOnly && (d.Project != "" || d.AutoProject || d.Workspace != "" || len(d.AllowedScopes) != 0) {
		return records.Connection{}, access("invalid_request", "connection-only setup cannot grant memory access or bind a workspace")
	}
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 160 || (!d.ConnectionOnly && len(d.AllowedScopes) == 0) || len(d.AllowedScopes) > 100 {
		return records.Connection{}, access("invalid_request", "name and explicit allowed scopes required")
	}
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(d.ID); err != nil {
		return records.Connection{}, access("invalid_request", "invalid connection ID")
	}
	if d.AutoProject && d.Project != "" {
		return records.Connection{}, access("invalid_request", "choose either a registered project or deferred project creation")
	}
	if d.AutoProject && s.Schema < 16 {
		return records.Connection{}, access("unsupported_schema", "deferred project connection requires the current schema")
	}
	var pending, workspace, common string
	if d.AutoProject && d.Workspace != "" {
		var err error
		workspace, common, err = repository(ctx, d.Workspace)
		if err != nil {
			return records.Connection{}, err
		}
		known, found, err := s.knownRepository(ctx, workspace)
		if err != nil {
			return records.Connection{}, err
		}
		pending = uuid.NewSHA1(uuid.NameSpaceOID, []byte("memlane-auto-project-v1\x00"+common)).String()
		if found {
			pending = known.ID
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.Connection{}, err
	}
	defer tx.Rollback()
	if s.Schema >= 14 {
		var retired int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, d.ID).Scan(&retired); err != nil {
			return records.Connection{}, err
		}
		if retired > 0 {
			d.ID = uuid.NewString()
		}
	}
	allowed := []string{}
	seen := map[string]bool{}
	for _, id := range d.AllowedScopes {
		var kind string
		if err = tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, id).Scan(&kind); err != nil {
			return records.Connection{}, access("scope_denied", "unknown allowed scope")
		}
		if kind != "project" && kind != "preference" && kind != "experience" {
			return records.Connection{}, access("scope_denied", "register project before granting Agent access")
		}
		if !seen[id] {
			seen[id] = true
			allowed = append(allowed, id)
		}
	}
	if d.Project != "" {
		if !seen[d.Project] {
			return records.Connection{}, access("scope_denied", "bound project must be allowed")
		}
		var kind string
		if err = tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, d.Project).Scan(&kind); err != nil {
			return records.Connection{}, err
		}
		if kind != "project" {
			return records.Connection{}, access("invalid_request", "binding must be a project")
		}
	}
	var priorAgent string
	err = tx.QueryRowContext(ctx, `SELECT agent FROM connections WHERE id=?`, d.ID).Scan(&priorAgent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return records.Connection{}, err
	}
	if err == nil && priorAgent != d.Agent {
		return records.Connection{}, access("conflict", "cannot change connection Agent")
	}
	encoded, _ := json.Marshal(allowed)
	_, err = tx.ExecContext(ctx, `INSERT INTO connections(id,agent,name,project,allowed_scopes,enabled,generation) VALUES(?,?,?,?,?,1,1) ON CONFLICT(id) DO UPDATE SET name=excluded.name,project=excluded.project,allowed_scopes=excluded.allowed_scopes,enabled=1,generation=connections.generation+1,last_seen=''`, d.ID, d.Agent, d.Name, d.Project, string(encoded))
	if err != nil {
		return records.Connection{}, err
	}
	if s.Schema >= 16 {
		_, err = tx.ExecContext(ctx, `INSERT INTO connection_intakes(connection_id,auto_project,pending_project,trusted_workspace,trusted_repository) VALUES(?,?,?,?,?) ON CONFLICT(connection_id) DO UPDATE SET auto_project=excluded.auto_project,pending_project=excluded.pending_project,trusted_workspace=excluded.trusted_workspace,trusted_repository=excluded.trusted_repository`, d.ID, d.AutoProject, pending, workspace, common)
		if err != nil {
			return records.Connection{}, err
		}
	}
	c, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, d.ID))
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *Store) Disconnect(ctx context.Context, id string) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE connections SET enabled=0,generation=generation+1,last_seen='' WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) TouchConnection(ctx context.Context, id string, generation int64) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE connections SET last_seen=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND enabled=1 AND generation=?`, id, generation)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return access("reload_required", "connection changed; reload Agent")
	}
	return err
}

// Authorize selects explicit scopes or current project plus allowed globals.
// It never expands to all allowed projects implicitly.
func (s *Store) Authorize(ctx context.Context, r records.AgentRequest) (records.Connection, []string, error) {
	return s.authorize(ctx, r, false)
}

// AuthorizeReadOnly applies the same grants and physical-workspace checks,
// without lazily writing the repository cache or touching connection activity.
func (s *Store) AuthorizeReadOnly(ctx context.Context, r records.AgentRequest) (records.Connection, []string, error) {
	return s.authorize(ctx, r, true)
}
func (s *Store) authorize(ctx context.Context, r records.AgentRequest, readOnly bool) (records.Connection, []string, error) {
	if s.Schema >= 14 {
		var retired int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, r.ConnectionID).Scan(&retired); err != nil {
			return records.Connection{}, nil, err
		}
		if retired > 0 {
			return records.Connection{}, nil, access("restore_reconnect_required", "backup restored; reconnect this Agent with a new identity")
		}
	}
	c, err := s.Connection(ctx, r.ConnectionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = access("connection_not_found", "connect Agent in Memlane first")
	}
	if err != nil {
		return c, nil, err
	}
	if !c.Enabled {
		return c, nil, access("disconnected", "connection is disconnected")
	}
	if c.Generation != r.Generation {
		return c, nil, access("reload_required", "connection changed; reload Agent")
	}
	current := c.Project
	explicit := r.Arguments.Scope != "" || len(r.Arguments.Scopes) > 0
	pendingSave := false
	if c.PendingProject != "" {
		if err = s.checkPendingWorkspace(ctx, c, r.Workspace); err != nil {
			return c, nil, err
		}
		var exists int
		if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM scopes WHERE id=? AND kind='project'`, c.PendingProject).Scan(&exists); err != nil {
			return c, nil, err
		}
		pendingSave = r.Tool == "memory_save"
		if pendingSave || exists == 1 {
			current = c.PendingProject
		}
	} else if r.Workspace != "" && c.Project != "" {
		if err := s.CheckBoundWorkspace(ctx, c.Project, r.Workspace); err != nil {
			return c, nil, err
		}
	} else if r.Workspace != "" {
		project, e := s.projectForWorkspace(ctx, r.Workspace, !readOnly)
		if e != nil {
			// With no fixed binding, an explicitly chosen allowed scope is
			// sufficient even outside a registered Git repository. Still fail
			// closed for default reads and never infer a different project.
			if c.Project != "" || (!explicit && r.Tool != "memory_scopes") {
				return c, nil, e
			}
			current = ""
		} else {
			if current != "" && project.ID != current {
				return c, nil, access("workspace_mismatch", "workspace differs from bound project")
			}
			current = project.ID
		}
	}
	allowed := map[string]bool{}
	c.ContextProject = current
	for _, id := range c.AllowedScopes {
		allowed[id] = true
	}
	if c.PendingProject != "" && current == c.PendingProject {
		allowed[current] = true
	}
	if current != "" && !allowed[current] && !explicit && r.Tool != "memory_scopes" {
		return c, nil, access("scope_denied", "current project is not allowed")
	}
	selected := append([]string{}, r.Arguments.Scopes...)
	if r.Arguments.Scope != "" {
		if len(selected) > 0 {
			return c, nil, access("invalid_request", "use scope or scopes")
		}
		selected = []string{r.Arguments.Scope}
	}
	if len(selected) == 0 {
		if r.Tool == "memory_scopes" {
			return c, append([]string{}, c.AllowedScopes...), nil
		}
		if current == "" {
			if c.AutoProject && r.Tool != "memory_save" {
				for _, scope := range c.AllowedScopes {
					var kind string
					if err = s.DB.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, scope).Scan(&kind); err != nil {
						return c, nil, err
					}
					if kind == "preference" || kind == "experience" {
						selected = append(selected, scope)
					}
				}
				return c, selected, nil
			}
			return c, nil, access("scope_required", "select an allowed scope or bind the current project")
		}
		selected = append(selected, current)
		if r.Tool != "memory_save" {
			scopes, e := s.Scopes(ctx)
			if e != nil {
				return c, nil, e
			}
			for _, scope := range scopes {
				if allowed[scope.ID] && (scope.Kind == "preference" || scope.Kind == "experience") {
					selected = append(selected, scope.ID)
				}
			}
		}
	}
	if len(selected) > 100 {
		return c, nil, access("invalid_request", "too many scopes")
	}
	unique := []string{}
	seen := map[string]bool{}
	for _, id := range selected {
		if !allowed[id] {
			return c, nil, access("scope_denied", "requested scope is not allowed")
		}
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	return c, unique, nil
}
