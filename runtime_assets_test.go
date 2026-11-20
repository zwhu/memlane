package memlane_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func TestRuntimeAssetsMaterializeAndEmbedLocalScriptImports(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resources")
	destination, err := bootstrap.PrepareRuntimeResources(context.Background(), root, memlane.RuntimeAssets)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"engine/python/user_profiles/__init__.py",
		"engine/python/user_profiles/providers/antigravity.py",
		"scripts/dev.py",
		"scripts/dev_recovery.py",
		"scripts/local_prepare.py",
		"scripts/build_project_plugin.py",
		"scripts/task.py",
		"scripts/disk-space.py",
	}
	for _, rel := range expected {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing materialized asset %s: %v", rel, err)
		}
	}

	// Dev-only UI watcher module is excluded intentionally from the App runtime bundle.
	devOnly := map[string]bool{"mygo_sdk": true}
	fromImport := regexp.MustCompile(`(?m)^\s*from\s+([A-Za-z_][A-Za-z0-9_]*)\s+import\b`)
	plainImport := regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_][A-Za-z0-9_]*)`)

	entries, err := fs.ReadDir(memlane.RuntimeAssets, "scripts")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".py") {
			continue
		}
		raw, err := fs.ReadFile(memlane.RuntimeAssets, "scripts/"+entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for _, re := range []*regexp.Regexp{fromImport, plainImport} {
			for _, match := range re.FindAllStringSubmatch(src, -1) {
				mod := match[1]
				if devOnly[mod] {
					continue
				}
				repoModule := filepath.Join("scripts", mod+".py")
				if _, err := os.Stat(repoModule); err == nil {
					if _, err := fs.Stat(memlane.RuntimeAssets, "scripts/"+mod+".py"); err != nil {
						t.Fatalf("embedded scripts/%s imports %s (%s), which is missing from RuntimeAssets: %v",
							entry.Name(), mod, repoModule, err)
					}
				}
			}
		}
	}
}
