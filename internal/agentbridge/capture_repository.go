//go:build darwin || linux

package agentbridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/zwhu/memlane/internal/records"
)

func captureDirectoryIdentity(path string) (uint64, uint64, bool) {
	i, e := os.Stat(path)
	if e != nil || !i.IsDir() {
		return 0, 0, false
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(s.Dev), s.Ino, true
}

// Same local common-dir and physical directory identity rules as project
// registration. No remote config, repository content, or Git hooks are read.
func CaptureRepository(ctx context.Context, path string) records.CaptureRepository {
	out := records.CaptureRepository{State: "unknown", Workspace: filepath.Clean(path)}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil {
		return out
	}
	out.Workspace = canonical
	var ok bool
	out.WorkspaceDevice, out.WorkspaceInode, ok = captureDirectoryIdentity(canonical)
	if !ok {
		return out
	}
	run, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(run, "/usr/bin/git", "-C", canonical, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	command.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	data, e := command.Output()
	if e != nil || len(data) > 16384 {
		return out
	}
	parts := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(parts) != 2 {
		return out
	}
	for index, p := range parts {
		if !filepath.IsAbs(p) || strings.IndexFunc(p, unicode.IsControl) >= 0 {
			return out
		}
		parts[index], e = filepath.EvalSymlinks(p)
		if e != nil {
			return out
		}
	}
	root, common := parts[0], parts[1]
	rd, ri, ok := captureDirectoryIdentity(root)
	if !ok {
		return out
	}
	cd, ci, ok := captureDirectoryIdentity(common)
	if !ok {
		return out
	}
	// Ensure the path still identifies the same workspace after Git exits.
	wd, wi, ok := captureDirectoryIdentity(canonical)
	if !ok || wd != out.WorkspaceDevice || wi != out.WorkspaceInode {
		return out
	}
	out.State, out.GitRoot, out.GitCommon = "known", root, common
	out.RootDevice, out.RootInode, out.CommonDevice, out.CommonInode = rd, ri, cd, ci
	return out
}
