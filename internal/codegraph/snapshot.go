package codegraph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const Recipe = "memlane-code-v1:enola-0.4.30-v351:typescript-python:ignore-v1"
const MaxFiles = 2000
const MaxFileBytes = 1024 * 1024
const MaxBytes = 32 * 1024 * 1024

// cappedOutput stops external output from growing unbounded, including Git lists.
type cappedOutput struct {
	bytes.Buffer
	limit int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("code command output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func git(ctx context.Context, root string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/git", append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", root}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/var/empty", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0"}
	cmd.Stdin = bytes.NewReader(input)
	out := &cappedOutput{limit: 4 * 1024 * 1024}
	cmd.Stdout = out
	err := cmd.Run()
	return out.Bytes(), err
}
func SourceRoot(ctx context.Context, path string) (string, string, error) {
	if !filepath.IsAbs(path) || controls(path) {
		return "", "", errors.New("absolute code worktree required")
	}
	root, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", "", e
	}
	b, e := git(ctx, root, nil, "rev-parse", "--show-toplevel")
	if e != nil {
		return "", "", e
	}
	rootPath, e := gitPath(b)
	if e != nil {
		return "", "", e
	}
	root, e = filepath.EvalSymlinks(rootPath)
	if e != nil {
		return "", "", e
	}
	b, e = git(ctx, root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil {
		return "", "", e
	}
	commonPath, e := gitPath(b)
	if e != nil {
		return "", "", e
	}
	common, e := filepath.EvalSymlinks(commonPath)
	return root, common, e
}
func gitPath(raw []byte) (string, error) {
	path := strings.TrimSuffix(string(raw), "\n")
	path = strings.TrimSuffix(path, "\r")
	if !filepath.IsAbs(path) || controls(path) {
		return "", errors.New("invalid Git code path receipt")
	}
	return path, nil
}
func eligible(path string) bool {
	if !safePath(path) {
		return false
	}
	for _, p := range strings.Split(path, "/") {
		if strings.HasPrefix(p, ".") {
			return false
		}
		switch strings.ToLower(p) {
		case "node_modules", "vendor", "dist", "build", "target", "coverage", "__pycache__", "venv":
			return false
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".py":
		return true
	}
	switch filepath.Base(path) {
	case "package.json", "tsconfig.json", "pyproject.toml":
		return true
	}
	return false
}
func safePath(path string) bool {
	return path != "" && len(path) <= 1024 && !controls(path) && !strings.Contains(path, "\\") && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path && path != "." && path != ".." && !strings.HasPrefix(path, "../")
}
func controls(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

// Compare directory identities, not only spelling: case aliases on APFS and
// symlink aliases must never turn a supposed private output into a repo write.
func privateDirectory(source, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("private output must be absolute")
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return "", e
	}
	known, e := os.Stat(source)
	if e != nil {
		return "", e
	}
	for ancestor := parent; ; ancestor = filepath.Dir(ancestor) {
		info, e := os.Stat(ancestor)
		if e != nil {
			return "", e
		}
		if os.SameFile(known, info) {
			return "", errors.New("private output must be outside source worktree")
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			break
		}
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
func noLinks(root *os.Root, path string) (bool, error) {
	parts := strings.Split(path, "/")
	for i := range parts {
		info, e := root.Lstat(strings.Join(parts[:i+1], "/"))
		if os.IsNotExist(e) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
	}
	return true, nil
}

// Every component is opened relative to an already opened directory, with
// O_NOFOLLOW. os.Root alone allows links within the root; a concurrent swap to
// a hidden/excluded source must fail instead of being copied as eligible code.
func openSource(root *os.File, path string) (*os.File, error) {
	fd := int(root.Fd())
	owned := false
	parts := strings.Split(path, "/")
	for i, p := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, p, flags, 0)
		if owned {
			_ = unix.Close(fd)
		}
		if e != nil {
			return nil, e
		}
		fd, owned = next, true
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("code source is not regular")
	}
	return f, nil
}

// Capture reads Git-selected files through os.Root. The same bytes may be copied
// into a new private shadow. No project config, build or extractor code executes.
func Capture(ctx context.Context, path, shadow string) (Manifest, error) {
	var m Manifest
	root, common, e := SourceRoot(ctx, path)
	if e != nil {
		return m, e
	}
	m.Worktree, m.CommonDir, m.Recipe = root, common, Recipe
	b, e := git(ctx, root, nil, "rev-parse", "--verify", "HEAD")
	if e == nil {
		m.Commit = strings.TrimSpace(string(b))
	}
	b, e = git(ctx, root, nil, "ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z")
	if e != nil {
		return m, e
	}
	ignored, ie := git(ctx, root, b, "check-ignore", "--no-index", "--stdin", "-z")
	if ie != nil {
		var exit *exec.ExitError
		if !errors.As(ie, &exit) || exit.ExitCode() != 1 {
			return m, ie
		}
	}
	deny := map[string]bool{}
	for _, x := range strings.Split(string(ignored), "\x00") {
		deny[x] = true
	}
	paths := []string{}
	for _, x := range strings.Split(string(b), "\x00") {
		if controls(x) {
			return m, errors.New("code path contains unsupported control characters")
		}
		if eligible(x) && !deny[x] {
			paths = append(paths, x)
		}
	}
	sort.Strings(paths)
	opened, e := os.OpenRoot(root)
	if e != nil {
		return m, e
	}
	defer opened.Close()
	rootFD, e := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if e != nil {
		return m, e
	}
	rootHandle := os.NewFile(uintptr(rootFD), root)
	defer rootHandle.Close()
	known, e := opened.Stat(".")
	if e != nil {
		return m, e
	}
	held, e := rootHandle.Stat()
	if e != nil {
		return m, e
	}
	if !os.SameFile(known, held) {
		return m, errors.New("code worktree changed while opening source root")
	}
	if shadow != "" {
		if !filepath.IsAbs(shadow) {
			return m, errors.New("private shadow must be absolute")
		}
		var err error
		shadow, err = privateDirectory(root, shadow)
		if err != nil {
			return m, err
		}
		if _, err := os.Lstat(shadow); !os.IsNotExist(err) {
			return m, errors.New("shadow must be new")
		}
		if e = os.MkdirAll(shadow, 0700); e != nil {
			return m, e
		}
	}
	var total int64
	for _, p := range paths {
		if e = ctx.Err(); e != nil {
			return m, e
		}
		ok, e := noLinks(opened, p)
		if e != nil {
			return m, e
		}
		if !ok {
			continue
		}
		f, e := openSource(rootHandle, p)
		if e != nil {
			return m, e
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return m, e
		}
		if !info.Mode().IsRegular() {
			f.Close()
			return m, errors.New("code source is not regular")
		}
		if info.Size() > MaxFileBytes {
			f.Close()
			return m, fmt.Errorf("code file exceeds 1MiB: %s", p)
		}
		content, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		closeErr := f.Close()
		if e != nil {
			return m, e
		}
		if closeErr != nil {
			return m, closeErr
		}
		if len(content) > MaxFileBytes {
			return m, errors.New("code source changed beyond size limit")
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return m, fmt.Errorf("binary code source: %s", p)
		}
		total += int64(len(content))
		if len(m.Files) >= MaxFiles || total > MaxBytes {
			return m, errors.New("code snapshot exceeds file/byte budget")
		}
		h := sha256.Sum256(content)
		m.Files = append(m.Files, File{Path: p, SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(content)), Lines: 1 + bytes.Count(content, []byte{'\n'})})
		if shadow != "" {
			dest := filepath.Join(shadow, p)
			if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				return m, e
			}
			if e = os.WriteFile(dest, content, 0600); e != nil {
				return m, e
			}
		}
	}
	if m.Files == nil {
		m.Files = []File{}
	}
	encoded, e := json.Marshal(m)
	if e != nil {
		return m, e
	}
	h := sha256.Sum256(encoded)
	m.Digest = hex.EncodeToString(h[:])
	return m, nil
}
