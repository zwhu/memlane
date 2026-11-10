package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

func migrateDeferredProjects(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS connection_intakes(connection_id TEXT PRIMARY KEY REFERENCES connections(id),auto_project INTEGER NOT NULL,pending_project TEXT NOT NULL,trusted_workspace TEXT NOT NULL,trusted_repository TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS pending_repository ON connection_intakes(trusted_repository,pending_project);
CREATE TABLE IF NOT EXISTS pending_cognee_bindings(instance TEXT NOT NULL,dataset TEXT NOT NULL,project TEXT NOT NULL,operation TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(instance,dataset));
PRAGMA user_version=16;`)
	return err
}

func (s *Store) checkPendingWorkspace(ctx context.Context, c records.Connection, workspace string) error {
	if c.PendingProject == "" || c.TrustedRepository == "" || workspace == "" {
		return access("scope_required", "confirm this task's workspace in Memlane first")
	}
	_, common, err := repository(ctx, workspace)
	if err != nil {
		return err
	}
	if common != c.TrustedRepository {
		return access("workspace_mismatch", "workspace differs from the host-confirmed repository")
	}
	// Detect a replaced/moved Git identity rather than trusting the old path.
	_, original, err := repository(ctx, c.TrustedWorkspace)
	if err != nil {
		return err
	}
	if original != common {
		return access("workspace_mismatch", "approved repository identity changed; reconnect explicitly")
	}
	return nil
}

// Called only after the entire incoming record/provenance has been validated.
// Scope, grant promotion, pending binding and first saved version commit or
// roll back together. No network or Git process runs inside this transaction.
func (s *Store) acceptFirstProjectTx(ctx context.Context, tx *sql.Tx, c records.Connection) error {
	if c.PendingProject == "" {
		return nil
	}
	current, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, c.ID))
	if err != nil {
		return err
	}
	if !current.Enabled || current.Generation != c.Generation || current.PendingProject != c.PendingProject || current.TrustedRepository != c.TrustedRepository {
		return access("reload_required", "connection changed before the first record was committed")
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT scope FROM repositories WHERE common_dir=?`, c.TrustedRepository).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && id != c.PendingProject {
		return access("scope_ambiguous", "repository project changed; choose its scope in Memlane")
	}
	var kind, path string
	err = tx.QueryRowContext(ctx, `SELECT kind,path FROM scopes WHERE id=?`, c.PendingProject).Scan(&kind, &path)
	if err == sql.ErrNoRows {
		name := filepath.Base(c.TrustedWorkspace)
		if name == "个人偏好" || name == "全局经验" {
			name += " 项目"
		}
		name = string([]rune(name)[:min(80, len([]rune(name)))])
		_, err = tx.ExecContext(ctx, `INSERT INTO scopes(id,kind,name,path) VALUES(?,'project',?,?)`, c.PendingProject, name, c.TrustedWorkspace)
	} else if err == nil && kind != "project" {
		err = access("scope_denied", "pending identity does not refer to a project")
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repositories(common_dir,scope) VALUES(?,?) ON CONFLICT(common_dir) DO NOTHING`, c.TrustedRepository, c.PendingProject); err != nil {
		return err
	}
	allowed := append([]string{}, current.AllowedScopes...)
	found := false
	for _, id := range allowed {
		found = found || id == c.PendingProject
	}
	if !found {
		allowed = append(allowed, c.PendingProject)
	}
	encoded, _ := json.Marshal(allowed)
	if _, err = tx.ExecContext(ctx, `UPDATE connections SET project=?,allowed_scopes=? WHERE id=? AND enabled=1 AND generation=?`, c.PendingProject, string(encoded), c.ID, c.Generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE connection_intakes SET pending_project='' WHERE connection_id=?`, c.ID); err != nil {
		return err
	}
	// A binding was verified against the explicit pending grant. Once the
	// record creates its project it acquires the ordinary project foreign key.
	if _, err = tx.ExecContext(ctx, `INSERT INTO cognee_bindings(instance,dataset,project,operation,digest,payload) SELECT instance,dataset,project,operation,digest,payload FROM pending_cognee_bindings WHERE project=? AND json_extract(payload,'$.connection_id')=? AND json_extract(payload,'$.generation')=?`, c.PendingProject, c.ID, c.Generation); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM pending_cognee_bindings WHERE project=? AND json_extract(payload,'$.connection_id')=? AND json_extract(payload,'$.generation')=?`, c.PendingProject, c.ID, c.Generation)
	return err
}

// ApplyAgentSave extends the existing mutation transaction only for the first
// accepted record. Explicit global saves do not create a repository project.
func (s *Store) ApplyAgentSave(ctx context.Context, c records.Connection, m Mutation) (Version, error) {
	if m.SourceDetail != nil && m.SourceDetail.History != nil {
		return Version{}, access("invalid_source", "historical provenance is stamped only by the project task owner")
	}
	if c.PendingProject == "" || m.Dataset != c.PendingProject {
		return s.Apply(ctx, m)
	}
	if strings.TrimSpace(m.Text) == "" || m.SourceDetail != nil && (m.SourceDetail.Official != nil || m.SourceDetail.Native != nil) {
		return Version{}, access("invalid_request", "valid new Agent content required")
	}
	if err := s.checkPendingWorkspace(ctx, c, c.TrustedWorkspace); err != nil {
		return Version{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback()
	if err = s.acceptFirstProjectTx(ctx, tx, c); err != nil {
		return Version{}, err
	}
	v, err := s.applyTx(ctx, tx, m)
	if err != nil {
		return Version{}, err
	}
	if err = tx.Commit(); err != nil {
		return Version{}, err
	}
	return v, s.enrichVersions(ctx, []*records.Version{&v})
}
