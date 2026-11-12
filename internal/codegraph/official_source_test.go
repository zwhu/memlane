package codegraph

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func officialSourceFixture(t *testing.T) (string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "worktree")
	if b, e := exec.Command("/usr/bin/git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	for name, body := range map[string]string{"source.ts": "export const value = 1;\n", "other.py": "def value():\n    return 1\n", "ignored.ts": "synthetic excluded", ".gitignore": "ignored.ts\n", ".env": "SYNTHETIC_SECRET"} {
		if e := os.WriteFile(filepath.Join(repo, name), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if b, e := exec.Command("/usr/bin/git", "-C", repo, "add", "-f", "ignored.ts").CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	if e := os.Symlink("other.py", filepath.Join(repo, "linked.py")); e != nil {
		t.Fatal(e)
	}
	return base, repo
}

func TestOfficialSourcePrivateCopyAndStableIdentity(t *testing.T) {
	base, repo := officialSourceFixture(t)
	ctx := context.Background()
	first, err := PrepareOfficialSource(ctx, repo, filepath.Join(base, "operation-one"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Manifest.Files) != 2 {
		t.Fatal("ignore/link/hidden contract changed", first.Manifest.Files)
	}
	if e := os.Mkdir(filepath.Join(first.Repository, ".enola"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(first.Repository, ".enola", "facts.jsonl"), []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := first.Verify(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(repo, ".enola")); !os.IsNotExist(e) {
		t.Fatal("source received extractor output")
	}
	if e := os.Rename(filepath.Join(repo, "source.ts"), filepath.Join(repo, "renamed.ts")); e != nil {
		t.Fatal(e)
	}
	if e := first.Verify(ctx); e == nil {
		t.Fatal("renamed source accepted as current")
	}
	second, err := PrepareOfficialSource(ctx, repo, filepath.Join(base, "operation-two"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second.Repository) != filepath.Base(first.Repository) || second.Manifest.Digest == first.Manifest.Digest {
		t.Fatal("unstable repo identity or stale manifest")
	}
	if _, e := os.Stat(filepath.Join(second.Repository, "source.ts")); !os.IsNotExist(e) {
		t.Fatal("deleted path retained")
	}
	if _, e := os.Stat(filepath.Join(second.Repository, "renamed.ts")); e != nil {
		t.Fatal(e)
	}
}

func TestOfficialSourceRejectsStaleOrTamperedInputs(t *testing.T) {
	for _, mode := range []string{"modified", "deleted", "copy_modified", "copy_deleted", "copy_added", "copy_link", "output_link"} {
		t.Run(mode, func(t *testing.T) {
			base, repo := officialSourceFixture(t)
			ctx := context.Background()
			s, err := PrepareOfficialSource(ctx, repo, filepath.Join(base, "operation"))
			if err != nil {
				t.Fatal(err)
			}
			var e error
			switch mode {
			case "modified":
				e = os.WriteFile(filepath.Join(repo, "source.ts"), []byte("changed"), 0600)
			case "deleted":
				e = os.Remove(filepath.Join(repo, "source.ts"))
			case "copy_modified":
				e = os.WriteFile(filepath.Join(s.Repository, "source.ts"), []byte("changed"), 0600)
			case "copy_deleted":
				e = os.Remove(filepath.Join(s.Repository, "source.ts"))
			case "copy_added":
				e = os.WriteFile(filepath.Join(s.Repository, "extra.ts"), []byte("unexpected"), 0600)
			case "copy_link":
				e = os.Remove(filepath.Join(s.Repository, "source.ts"))
				if e == nil {
					e = os.Symlink(filepath.Join(repo, "source.ts"), filepath.Join(s.Repository, "source.ts"))
				}
			case "output_link":
				e = os.Symlink(repo, filepath.Join(s.Repository, ".enola"))
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Verify(ctx); e == nil {
				t.Fatal("stale/tampered source accepted")
			}
		})
	}
}

func TestOfficialSourceRejectsReuseAndProjectOutput(t *testing.T) {
	base, repo := officialSourceFixture(t)
	ctx := context.Background()
	existing := filepath.Join(base, "existing")
	if e := os.Mkdir(existing, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(existing, "keep"), []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := PrepareOfficialSource(ctx, repo, existing); e == nil {
		t.Fatal("existing output accepted")
	}
	if b, e := os.ReadFile(filepath.Join(existing, "keep")); e != nil || string(b) != "preserve" {
		t.Fatal("existing output changed")
	}
	inside := filepath.Join(repo, "private-output")
	if _, e := PrepareOfficialSource(ctx, repo, inside); e == nil {
		t.Fatal("project output accepted")
	}
	if _, e := os.Stat(inside); !os.IsNotExist(e) {
		t.Fatal("project output created")
	}
}
