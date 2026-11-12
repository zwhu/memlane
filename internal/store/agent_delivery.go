package store

import (
	"context"
	"fmt"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"syscall"
)

// The epoch fence and immutable operation apply share one SQLite transaction.
// Preflight alone cannot authorize a send across restore/root replacement.
func (s *Store) ApplyAgentSaveInLibrary(ctx context.Context, c records.Connection, m Mutation, library string, grant records.AgentDeliveryGrant) (Version, error) {
	if library == "" || m.Kind != "save" || m.ExpectedRevision != 0 || grant.LibraryID != library || grant.ConnectionID != c.ID || grant.Generation != c.Generation || grant.Scope != m.Dataset || grant.Workspace == "" || grant.WorkspaceDigest == "" {
		return Version{}, access("invalid_request", "bound new save required")
	}
	if m.SourceDetail != nil && (m.SourceDetail.History != nil || m.SourceDetail.Official != nil || m.SourceDetail.Native != nil) {
		return Version{}, access("invalid_source", "Agent cannot stamp protected provenance")
	}
	pending := c.PendingProject != "" && m.Dataset == c.PendingProject
	if pending {
		if err := s.checkPendingWorkspace(ctx, c, c.TrustedWorkspace); err != nil {
			return Version{}, err
		}
	}
	workspace, err := resolveDeliveryWorkspace(ctx, c.ContextProject, grant.Workspace)
	if err != nil {
		return Version{}, err
	}
	if workspace.identity.Workspace != grant.Workspace || UsageDigest(workspace.identity) != grant.WorkspaceDigest {
		return Version{}, access("delivery_authorization_changed", "original workspace identity changed")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback()
	var actual string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&actual); err != nil {
		return Version{}, err
	}
	if actual != library {
		return Version{}, access("delivery_library_changed", "original delivery library changed; do not rebind this operation")
	}
	current, err := scanConnection(tx.QueryRowContext(ctx, `SELECT `+s.connectionProjection()+` FROM connections WHERE id=?`, c.ID))
	if err != nil {
		return Version{}, err
	}
	var retired int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM connection_retirements WHERE connection=?`, c.ID).Scan(&retired); err != nil {
		return Version{}, err
	}
	if retired > 0 || !current.Enabled || current.Generation != c.Generation {
		return Version{}, access("reload_required", "delivery connection was revoked or changed")
	}
	if err = workspace.check(); err != nil {
		return Version{}, err
	}
	if pending {
		if err = s.acceptFirstProjectTx(ctx, tx, c); err != nil {
			return Version{}, err
		}
	}
	v, err := s.applyTx(ctx, tx, m)
	if err != nil {
		return v, err
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	return v, s.enrichVersions(ctx, []*records.Version{&v})
}

// DeliveryWorkspaceIdentity performs complete Git resolution before callers
// open a save transaction. Its public identity format remains unchanged.
func DeliveryWorkspaceIdentity(ctx context.Context, project, path string) (string, string, error) {
	snapshot, err := resolveDeliveryWorkspace(ctx, project, path)
	if err != nil {
		return "", "", err
	}
	return snapshot.identity.Workspace, UsageDigest(snapshot.identity), nil
}

type deliveryWorkspaceIdentity struct {
	Project, Workspace, GitRoot, GitCommon                                            string
	WorkspaceDevice, WorkspaceInode, RootDevice, RootInode, CommonDevice, CommonInode uint64
}
type deliveryPathStamp struct {
	path   string
	info   os.FileInfo
	absent bool
}
type deliveryWorkspaceSnapshot struct {
	identity deliveryWorkspaceIdentity
	paths    []deliveryPathStamp
}

func deliveryDirectoryStat(path string) (uint64, uint64, error) {
	info, e := os.Stat(path)
	if e != nil || !info.IsDir() {
		return 0, 0, access("workspace_unavailable", "authorized directory identity unavailable")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, access("workspace_unavailable", "directory identity unsupported")
	}
	return uint64(st.Dev), st.Ino, nil
}
func resolveDeliveryWorkspace(ctx context.Context, project, path string) (deliveryWorkspaceSnapshot, error) {
	var snapshot deliveryWorkspaceSnapshot
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return snapshot, access("workspace_unavailable", "authorized workspace unavailable")
	}
	identity := deliveryWorkspaceIdentity{Project: project, Workspace: canonical}
	identity.WorkspaceDevice, identity.WorkspaceInode, err = deliveryDirectoryStat(canonical)
	if err != nil {
		return snapshot, err
	}
	root, common, repoErr := repository(ctx, canonical)
	paths := []string{canonical}
	if repoErr == nil {
		identity.GitRoot = root
		identity.GitCommon = common
		identity.RootDevice, identity.RootInode, err = deliveryDirectoryStat(root)
		if err != nil {
			return snapshot, err
		}
		identity.CommonDevice, identity.CommonInode, err = deliveryDirectoryStat(common)
		if err != nil {
			return snapshot, err
		}
		paths = append(paths, root, common)
	} else if project != "" {
		return snapshot, repoErr
	}
	snapshot.identity = identity
	for _, p := range paths {
		info, e := os.Stat(p)
		if e != nil {
			return snapshot, e
		}
		snapshot.paths = append(snapshot.paths, deliveryPathStamp{path: p, info: info})
	}
	// Pin a worktree .git file/symlink as well as the resolved common directory.
	// The transaction only re-stats metadata; no Git process or file read runs.
	gitParent := root
	if gitParent == "" {
		gitParent = canonical
	}
	gitPath := filepath.Join(gitParent, ".git")
	info, e := os.Lstat(gitPath)
	if e != nil && !os.IsNotExist(e) {
		return snapshot, e
	}
	snapshot.paths = append(snapshot.paths, deliveryPathStamp{path: gitPath, info: info, absent: os.IsNotExist(e)})
	// Ensure the frozen .git pointer still resolves to these same directories.
	// Both resolutions happen before BeginTx; the transaction only stats paths.
	if repoErr == nil {
		againRoot, againCommon, e := repository(ctx, canonical)
		if e != nil || againRoot != root || againCommon != common {
			return snapshot, access("delivery_authorization_changed", "Git identity changed while freezing workspace")
		}
	}
	if err = snapshot.check(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}
func (s deliveryWorkspaceSnapshot) check() error {
	for _, stamp := range s.paths {
		var info os.FileInfo
		var err error
		// For a frozen .git pointer use Lstat to detect replacement of the pointer.
		if filepath.Base(stamp.path) == ".git" {
			info, err = os.Lstat(stamp.path)
		} else {
			info, err = os.Stat(stamp.path)
		}
		if stamp.absent {
			if !os.IsNotExist(err) {
				return access("delivery_authorization_changed", "Git identity appeared during save")
			}
			continue
		}
		if err != nil || !os.SameFile(stamp.info, info) || stamp.info.Mode() != info.Mode() {
			return access("delivery_authorization_changed", "original workspace identity changed")
		}
		// Directory contents may legitimately change. A .git pointer's bytes may not.
		if !info.IsDir() && (stamp.info.Size() != info.Size() || !stamp.info.ModTime().Equal(info.ModTime())) {
			return access("delivery_authorization_changed", "Git pointer changed during save")
		}
	}
	// Match the exact device/inode tuple hashed for the public grant as well.
	for _, p := range []struct {
		path     string
		dev, ino uint64
	}{{s.identity.Workspace, s.identity.WorkspaceDevice, s.identity.WorkspaceInode}, {s.identity.GitRoot, s.identity.RootDevice, s.identity.RootInode}, {s.identity.GitCommon, s.identity.CommonDevice, s.identity.CommonInode}} {
		if p.path == "" {
			continue
		}
		dev, ino, err := deliveryDirectoryStat(p.path)
		if err != nil || dev != p.dev || ino != p.ino {
			return access("delivery_authorization_changed", "original workspace identity changed")
		}
	}
	return nil
}

func WorkspaceDirectoryIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", access("workspace_unavailable", "authorized directory unavailable")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", access("workspace_unavailable", "directory identity unavailable")
	}
	return fmt.Sprintf("%d:%d", uint64(st.Dev), st.Ino), nil
}
