package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
)

func migrateOfficialCodeScopes(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE official_code_scopes(worktree_id TEXT PRIMARY KEY REFERENCES code_worktrees(id),instance TEXT NOT NULL,dataset TEXT NOT NULL,user_id TEXT NOT NULL,operation TEXT NOT NULL UNIQUE,payload TEXT NOT NULL,UNIQUE(instance,dataset));PRAGMA user_version=37;`)
	return err
}

func (s *Store) OfficialCodeScopeMatches(ctx context.Context, project, path string, scope records.OfficialCodeScope) (bool, error) {
	current, err := s.OfficialCodeScopeForWorkspace(ctx, project, path)
	return current != nil && *current == scope, err
}

func (s *Store) OfficialCodeScopeForWorkspace(ctx context.Context, project, path string) (*records.OfficialCodeScope, error) {
	root, _, err := codegraph.SourceRoot(ctx, path)
	if err != nil {
		return nil, err
	}
	c, err := readCode(s.DB.QueryRowContext(ctx, `SELECT `+codeColumns+` FROM code_worktrees WHERE project=? AND worktree=?`, project, root))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.Official, nil
}

func validateOfficialCodeScope(scope records.OfficialCodeScope) error {
	for _, value := range []string{scope.Instance, scope.Dataset, scope.User} {
		id, err := uuid.Parse(value)
		if err != nil || id.String() != value {
			return access("invalid_request", "canonical official code scope required")
		}
	}
	u, err := url.Parse(scope.Endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return access("invalid_request", "explicit loopback code service required")
	}
	return nil
}

func bindOfficialCodeScope(ctx context.Context, tx *sql.Tx, a records.CodeAction, out *records.CodeState) error {
	if a.Official == nil {
		return nil
	}
	if err := validateOfficialCodeScope(*a.Official); err != nil {
		return err
	}
	if out.Official != nil {
		if *out.Official != *a.Official {
			return access("code_scope_change_requires_review", "retain the existing official code dataset")
		}
		return nil
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cognee_bindings WHERE instance=? AND dataset=?`, a.Official.Instance, a.Official.Dataset).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return access("scope_denied", "code graph cannot reuse a knowledge or material dataset")
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pending_cognee_bindings WHERE instance=? AND dataset=?`, a.Official.Instance, a.Official.Dataset).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return access("scope_denied", "code graph cannot reuse a pending knowledge dataset")
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM official_code_scopes WHERE instance=? AND dataset=?`, a.Official.Instance, a.Official.Dataset).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return access("scope_denied", "official code dataset already belongs to a worktree")
	}
	raw, err := json.Marshal(a.Official)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO official_code_scopes(worktree_id,instance,dataset,user_id,operation,payload) VALUES(?,?,?,?,?,?)`, out.ID, a.Official.Instance, a.Official.Dataset, a.Official.User, a.OperationID, string(raw))
	if err != nil {
		return err
	}
	scope := *a.Official
	out.Official = &scope
	return nil
}

// A frozen per-run state must still be the exact current enabled scope. No
// legacy enable, connection, or reconnect implicitly grants official indexing.
func (s *Store) AuthorizeOfficialCode(ctx context.Context, frozen records.CodeState) error {
	current, err := s.Code(ctx, frozen.ID)
	if err != nil {
		return err
	}
	if !current.Enabled || current.Official == nil || frozen.Official == nil || current.Generation != frozen.Generation || current.Project != frozen.Project || current.Worktree != frozen.Worktree || current.CommonDir != frozen.CommonDir || *current.Official != *frozen.Official {
		return access("code_disabled", "current explicit official code permission required")
	}
	root, common, err := codegraph.SourceRoot(ctx, current.Worktree)
	if err != nil {
		return err
	}
	if root != current.Worktree || common != current.CommonDir {
		return access("workspace_mismatch", "official code worktree identity changed")
	}
	return s.CheckBoundWorkspace(ctx, current.Project, current.Worktree)
}
