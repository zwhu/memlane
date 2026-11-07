package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

const InitializationLimit = 16
const InitializationBytes = 128 * 1024

func initializationPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") || len(path) > 2048 {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "" {
			return false
		}
		switch strings.ToLower(part) {
		case ".git", ".trae", ".agents", ".codex", ".pi", ".claude", "node_modules", ".venv", "__pycache__":
			return false
		}
	}
	name := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(name, ".env") || name == "auth.json" || name == "credentials.json" || name == "settings.json" {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".ts", ".tsx", ".js", ".jsx", ".py", ".go", ".rs", ".c", ".cpp", ".h", ".hpp", ".java", ".kt", ".swift", ".yaml", ".yml", ".toml":
		return true
	case ".json", ".jsonc":
		return name == "package.json" || name == "deno.json" || strings.HasPrefix(name, "tsconfig") || strings.HasPrefix(name, "jsconfig")
	}
	return false
}

// readInitializationSources opens only explicitly selected regular files.
// Descriptor-relative traversal prevents symlink replacement from escaping the
// approved root. All filesystem work happens outside SQLite write transactions.
func readInitializationSources(ctx context.Context, root string, paths []string) ([]records.InitializationSource, error) {
	if len(paths) < 1 || len(paths) > InitializationLimit {
		return nil, errors.New("select 1 to 16 repository text files")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	rootFile := os.NewFile(uintptr(rootFD), root)
	defer rootFile.Close()
	actual, err := rootFile.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, errors.New("workspace changed while opening sources")
	}
	total := 0
	seen := map[string]bool{}
	sources := []records.InitializationSource{}
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !initializationPath(path) {
			return nil, errors.New("material must be a permitted repository code/document path")
		}
		resolved, e := filepath.EvalSymlinks(filepath.Join(root, path))
		if e != nil {
			return nil, e
		}
		rel, e := filepath.Rel(root, resolved)
		if e != nil || !initializationPath(filepath.ToSlash(rel)) {
			return nil, errors.New("material escapes approved workspace or is excluded")
		}
		if seen[rel] {
			return nil, errors.New("duplicate material path")
		}
		seen[rel] = true
		parent, e := unix.FcntlInt(uintptr(rootFD), unix.F_DUPFD_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		parts := strings.Split(rel, string(filepath.Separator))
		for _, part := range parts[:len(parts)-1] {
			next, e := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			unix.Close(parent)
			if e != nil {
				return nil, e
			}
			parent = next
		}
		fd, e := unix.Openat(parent, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		unix.Close(parent)
		if e != nil {
			return nil, e
		}
		file := os.NewFile(uintptr(fd), resolved)
		stat, e := file.Stat()
		if e != nil || !stat.Mode().IsRegular() {
			file.Close()
			return nil, errors.New("material must be a regular file")
		}
		raw, e := io.ReadAll(io.LimitReader(file, int64(InitializationBytes-total+1)))
		file.Close()
		if e != nil {
			return nil, e
		}
		total += len(raw)
		if total > InitializationBytes || !utf8.Valid(raw) || len(raw) == 0 {
			return nil, errors.New("materials require nonempty UTF-8 and at most 128 KiB total")
		}
		digest := sha256.Sum256(raw)
		sources = append(sources, records.InitializationSource{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(digest[:]), Text: string(raw)})
	}
	return sources, nil
}

// The private IO seam allows fault checks to pause actual reads outside SQL.
// Production stores use the descriptor-restricted reader above.
func (s *Store) initializationSources(ctx context.Context, root string, paths []string) ([]records.InitializationSource, error) {
	if s.initializationReader != nil {
		return s.initializationReader(ctx, root, paths)
	}
	return readInitializationSources(ctx, root, paths)
}
