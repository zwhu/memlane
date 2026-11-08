package store

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/zwhu/memlane/internal/records"
)

// Git common directory identifies one local repository including worktrees.
// Remote URLs and directory basenames never merge independent clones.
func repository(ctx context.Context, path string) (root, common string, err error) {
	if !filepath.IsAbs(path) {
		return "", "", access("invalid_request", "absolute workspace path required")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", access("workspace_unavailable", "workspace path does not exist")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	git := func(args ...string) (string, error) {
		out, e := exec.CommandContext(ctx, "/usr/bin/git", append([]string{"-C", canonical, "rev-parse"}, args...)...).Output()
		if e != nil {
			return "", access("workspace_unavailable", "workspace is not a readable Git repository")
		}
		path := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
		if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return "", access("workspace_unavailable", "invalid Git workspace path receipt")
		}
		return filepath.EvalSymlinks(path)
	}
	root, err = git("--show-toplevel")
	if err != nil {
		return "", "", err
	}
	common, err = git("--path-format=absolute", "--git-common-dir")
	return
}

// Backfill identities lazily for existing P1 registered paths, without
// rejecting a migrated database whose repository is temporarily unavailable.
func (s *Store) ProjectForWorkspace(ctx context.Context, path string) (records.Scope, error) {
	_, common, err := repository(ctx, path)
	if err != nil {
		return records.Scope{}, err
	}
	scopes, err := s.Scopes(ctx)
	if err != nil {
		return records.Scope{}, err
	}
	matches := []records.Scope{}
	for _, scope := range scopes {
		if scope.Kind != "project" {
			continue
		}
		_, known, e := repository(ctx, scope.Path)
		if e == nil && known == common {
			matches = append(matches, scope)
		}
	}
	if len(matches) == 0 {
		return records.Scope{}, access("scope_required", "register this repository in Memlane and choose its scope")
	}
	if len(matches) > 1 {
		return records.Scope{}, access("scope_ambiguous", "multiple registered projects match this repository; choose one in Memlane")
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO repositories(common_dir,scope) VALUES(?,?) ON CONFLICT(common_dir) DO NOTHING`, common, matches[0].ID)
	return matches[0], err
}

func (s *Store) knownRepository(ctx context.Context, path string) (records.Scope, bool, error) {
	scope, err := s.ProjectForWorkspace(ctx, path)
	var ae *records.AccessError
	if errors.As(err, &ae) && ae.Code == "scope_required" {
		return records.Scope{}, false, nil
	}
	return scope, err == nil, err
}

// An explicit project binding takes precedence over ambiguous legacy project
// entries, but still must match the actual workspace's local repository.
func (s *Store) CheckBoundWorkspace(ctx context.Context, project, path string) error {
	var registered string
	if err := s.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, project).Scan(&registered); err != nil {
		return err
	}
	_, known, err := repository(ctx, registered)
	if err != nil {
		return err
	}
	_, current, err := repository(ctx, path)
	if err != nil {
		return err
	}
	if known != current {
		return access("workspace_mismatch", "workspace differs from bound project")
	}
	return nil
}
