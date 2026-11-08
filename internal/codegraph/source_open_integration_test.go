//go:build darwin || linux

package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Actual filesystem fault fixtures, NO MODEL. A checked source can change type
// or point at an excluded file before open; no payload may be read in that case.
func TestSourceOpenFilesystemFaults(t *testing.T) {
	for _, mode := range []string{"final-link-swap", "parent-link-swap", "fifo"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			must := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			must(os.Mkdir(filepath.Join(root, "src"), 0700))
			must(os.Mkdir(filepath.Join(root, "hidden"), 0700))
			must(os.WriteFile(filepath.Join(root, "src/a.ts"), []byte("synthetic allowed"), 0600))
			must(os.WriteFile(filepath.Join(root, "hidden/a.ts"), []byte("synthetic excluded"), 0600))
			opened, e := os.OpenRoot(root)
			must(e)
			defer opened.Close()
			ok, e := noLinks(opened, "src/a.ts")
			must(e)
			if !ok {
				t.Fatal("fixture initial source is a link")
			}
			fd, e := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			must(e)
			held := os.NewFile(uintptr(fd), root)
			defer held.Close()
			switch mode {
			case "final-link-swap":
				must(os.Remove(filepath.Join(root, "src/a.ts")))
				must(os.Symlink("../hidden/a.ts", filepath.Join(root, "src/a.ts")))
			case "parent-link-swap":
				must(os.Rename(filepath.Join(root, "src"), filepath.Join(root, "old-src")))
				must(os.Symlink("hidden", filepath.Join(root, "src")))
			case "fifo":
				must(os.Remove(filepath.Join(root, "src/a.ts")))
				must(unix.Mkfifo(filepath.Join(root, "src/a.ts"), 0600))
			}
			started := time.Now()
			f, e := openSource(held, "src/a.ts")
			if f != nil {
				f.Close()
			}
			if e == nil {
				t.Fatal("changed/blocked source accepted")
			}
			if time.Since(started) > 500*time.Millisecond {
				t.Fatal("source open was not bounded")
			}
		})
	}
}

// Two independent real Git siblings prove trailing whitespace is identity,
// not output decoration. No trimming may redirect the source to its neighbor.
func TestGitWhitespaceWorkspaceIdentity(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"repo", "repo "} {
		root := filepath.Join(base, name)
		if output, e := exec.Command("/usr/bin/git", "init", root).CombinedOutput(); e != nil {
			t.Fatal(e, string(output))
		}
		content := []byte("export const syntheticProject = " + "\"" + name + "\";\n")
		if e := os.WriteFile(filepath.Join(root, "source.ts"), content, 0600); e != nil {
			t.Fatal(e)
		}
	}
	requested := filepath.Join(base, "repo ")
	m, e := Capture(context.Background(), requested, "")
	if e != nil {
		t.Fatal(e)
	}
	canonical, e := filepath.EvalSymlinks(requested)
	if e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(filepath.Join(requested, "source.ts"))
	if e != nil {
		t.Fatal(e)
	}
	sha := sha256.Sum256(body)
	if m.Worktree != canonical || len(m.Files) != 1 || m.Files[0].SHA256 != hex.EncodeToString(sha[:]) {
		t.Fatal("worktree receipt redirected to independent sibling", m)
	}
}
