package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func projectRepositoryIdentity(root, common string) (string, error) {
	parts := []string{root, common}
	for _, p := range []string{root, common} {
		info, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", errors.New("repository identity unavailable")
		}
		parts = append(parts, fmt.Sprintf("%d:%d", st.Dev, st.Ino))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:]), nil
}

// InspectProject never uses ProjectForWorkspace: that path writes a cache.
func (s *Store) InspectProject(ctx context.Context, path string) (records.ProjectInspection, error) {
	out := records.ProjectInspection{Path: path, State: "invalid_path"}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if len(path) > 4096 || !filepath.IsAbs(path) {
		return out, nil
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return out, nil
	}
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			out.State = "permission_denied"
		} else {
			out.State = "unavailable"
		}
		return out, nil
	}
	// Refuse FIFOs/devices and a directory swapped to one after Stat.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_DIRECTORY, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			out.State = "permission_denied"
		} else {
			out.State = "unavailable"
		}
		return out, nil
	}
	info, err = f.Stat()
	if err != nil || !info.IsDir() {
		f.Close()
		return out, nil
	}
	_, err = f.Readdirnames(1)
	f.Close()
	if errors.Is(err, os.ErrPermission) {
		out.State = "permission_denied"
		return out, nil
	}
	root, common, err := repository(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		output, e := exec.CommandContext(ctx, "/usr/bin/git", "-C", path, "rev-parse", "--is-inside-work-tree").CombinedOutput()
		out.State = "unavailable"
		if e != nil {
			if strings.Contains(strings.ToLower(string(output)), "permission denied") {
				out.State = "permission_denied"
			} else if strings.Contains(string(output), "not a git repository") {
				out.State = "not_git"
			}
		}
		return out, nil
	}
	identity, err := projectRepositoryIdentity(root, common)
	if err != nil {
		out.State = "unavailable"
		return out, nil
	}
	out.Root, out.Name, out.Identity, out.State = root, filepath.Base(root), identity, "ready"
	scopes, err := s.Scopes(ctx)
	if err != nil {
		return out, err
	}
	for _, scope := range scopes {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if scope.Kind != "project" {
			continue
		}
		_, known, e := repository(ctx, scope.Path)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if e == nil && known == common {
			if out.Existing != nil {
				out.State = "ambiguous"
				out.Existing = nil
				return out, nil
			}
			v := scope
			out.Existing = &v
			out.State = "existing"
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, nil
}
