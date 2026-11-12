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
	if d.ConnectionOnly && (d.Project != "" || d.AutoProject || d.Workspace != "") {
		return records.Connection{}, access("invalid_request", "computer-level connection cannot bind a workspace")
	}
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 160 {
		return records.Connection{}, access("invalid_request", "connection name required")
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
	if d.Project != "" {
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
	encoded := []byte("[]")
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

// Compare-and-swap prevents a stale confirmation revoking a reconnected grant.
func (s *Store) DisconnectRevision(ctx context.Context, id string, generation int64) error {
	if generation < 1 {
		return access("invalid_request", "explicit connection generation required")
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE connections SET enabled=0,generation=generation+1,last_seen='' WHERE id=? AND generation=? AND enabled=1`, id, generation)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return access("scope_changed", "connection changed; review its current scope before disconnecting")
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

// Resolve a connected client's requested data groups. Project selection affects
// relevance and save destination only; every connected Agent can read/write all
// registered groups. Connection incarnation checks remain transport lifecycle.
func (s *Store) ResolveAgentScopes(ctx context.Context, r records.AgentRequest) (records.Connection, []string, error) {
	return s.resolveAgentScopes(ctx, r, false)
}
func (s *Store) ResolveAgentScopesReadOnly(ctx context.Context, r records.AgentRequest) (records.Connection, []string, error) {
	return s.resolveAgentScopes(ctx, r, true)
}
func (s *Store) resolveAgentScopes(ctx context.Context, r records.AgentRequest, readOnly bool) (records.Connection, []string, error) {
	if s.Schema >= 14 {
		var retired int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, r.ConnectionID).Scan(&retired); err != nil {
			return records.Connection{}, nil, err
		}
		if retired > 0 {
			return records.Connection{}, nil, access("restore_reconnect_required", "reconnect restored transport identity")
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
	if r.Arguments.Scope != "" && len(r.Arguments.Scopes) > 0 {
		return c, nil, access("invalid_request", "use scope or scopes")
	}
	selected := append([]string{}, r.Arguments.Scopes...)
	if r.Arguments.Scope != "" {
		selected = []string{r.Arguments.Scope}
	}
	explicit := len(selected) > 0
	current := c.Project
	pendingSave := false
	if c.PendingProject != "" && r.Tool == "memory_save" && (!explicit || len(selected) == 1 && selected[0] == c.PendingProject) {
		if err = s.checkPendingWorkspace(ctx, c, r.Workspace); err != nil {
			return c, nil, err
		}
		pendingSave = true
		current = c.PendingProject
	} else if r.Workspace != "" {
		project, e := s.projectForWorkspace(ctx, r.Workspace, !readOnly)
		if e == nil {
			current = project.ID
		} else if !explicit {
			current = ""
		}
	}
	c.ContextProject = current
	groups, err := s.Scopes(ctx)
	if err != nil {
		return c, nil, err
	}
	known := map[string]bool{}
	for _, group := range groups {
		known[group.ID] = true
	}
	if pendingSave {
		known[current] = true
	}
	if !explicit {
		if r.Tool == "memory_scopes" || current == "" && r.Tool != "memory_save" {
			for _, group := range groups {
				selected = append(selected, group.ID)
			}
		} else {
			if current == "" {
				return c, nil, access("scope_required", "choose a save destination")
			}
			selected = append(selected, current)
			if r.Tool != "memory_save" {
				for _, group := range groups {
					if group.Kind == "preference" || group.Kind == "experience" {
						selected = append(selected, group.ID)
					}
				}
			}
		}
	}
	if len(selected) > 100 {
		return c, nil, access("invalid_request", "too many selected groups")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range selected {
		if !known[id] {
			return c, nil, access("invalid_request", "selected data group does not exist")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return c, out, nil
}
