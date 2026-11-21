package memlane_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
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
		"engine/python/tool_graph_control.py",
		"engine/python/traex_hook.py",
		"web/official-graph/dist/assets.json",
		"web/official-graph/dist/index.html",
		"web/official-graph/dist/app.js",
		"web/official-graph/dist/app.css",
		"web/official-graph/dist/host.css",
		"web/official-graph/dist/app.js.LEGAL.txt",
		"web/official-graph/dist/COGNEE-LICENSE",
		"web/official-graph/dist/THIRD-PARTY-LICENSES.txt",
		"web/official-graph/dist/build.json",
		"scripts/dev.py",
		"scripts/dev_recovery.py",
		"scripts/local_prepare.py",
		"scripts/build_project_plugin.py",
		"scripts/task.py",
		"scripts/disk-space.py",
		"scripts/uninstall.py",
	}
	for _, rel := range expected {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing materialized asset %s: %v", rel, err)
		}
	}

	// Verify embedded graph build.json reports zero missing distribution license texts.
	buildRaw, err := fs.ReadFile(memlane.RuntimeAssets, "web/official-graph/dist/build.json")
	if err != nil {
		t.Fatalf("read embedded build.json failed: %v", err)
	}
	var buildMeta struct {
		MissingLicenseTexts []string `json:"missingLicenseTexts"`
	}
	if err := json.Unmarshal(buildRaw, &buildMeta); err != nil {
		t.Fatalf("unmarshal embedded build.json failed: %v", err)
	}
	if len(buildMeta.MissingLicenseTexts) != 0 {
		t.Fatalf("embedded graph build.json still has missingLicenseTexts: %v", buildMeta.MissingLicenseTexts)
	}

	// Verify Python test modules stay excluded from the embedded binary while all
	// production engine/python/*.py modules remain embedded.
	if err := fs.WalkDir(memlane.RuntimeAssets, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasPrefix(name, "engine/python/") && strings.HasPrefix(path.Base(name), "test_") {
			t.Errorf("test module must not be embedded in RuntimeAssets: %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	diskEntries, err := os.ReadDir(filepath.Join("engine", "python"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range diskEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".py") || strings.HasPrefix(entry.Name(), "test_") {
			continue
		}
		embeddedPath := "engine/python/" + entry.Name()
		if _, statErr := fs.Stat(memlane.RuntimeAssets, embeddedPath); statErr != nil {
			t.Fatalf("production Python module %s is missing from RuntimeAssets (update //go:embed pattern): %v", embeddedPath, statErr)
		}
	}

	second, err := bootstrap.PrepareRuntimeResources(context.Background(), root, memlane.RuntimeAssets)
	if err != nil {
		t.Fatalf("idempotent PrepareRuntimeResources failed: %v", err)
	}
	if second != destination {
		t.Fatalf("idempotent destination mismatch: got %q, want %q", second, destination)
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
