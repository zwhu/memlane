package memlane_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
	for _, pattern := range []string{
		"engines/cognee/*.py", "engine/first-install-components.json", "engine/coding-memory-components.json",
		"engine/embedding-components.json", "engine/agents/pi/*.ts",
		"engine/agents/pi/LICENSE.pi-cognee", "engine/prompts/*",
		"web/official-graph/dist/*", "deps/models/*.json", "deps/cognee-rs/patches.json",
		"deps/cognee-rs/Cargo.lock", "deps/cognee-rs/onnx-input-contract.patch",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			t.Fatalf("missing asset group %s: %v", pattern, err)
		}
		for _, name := range matches {
			if strings.HasPrefix(filepath.Base(name), "test_") {
				continue
			}
			disk, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if disk.IsDir() {
				continue
			}
			if _, err := fs.Stat(memlane.RuntimeAssets, filepath.ToSlash(name)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
				t.Fatal(err)
			}
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

	forbidden := []string{
		path.Join("engine", "python") + "/", path.Join("engine", "agents", "opencode") + "/",
		path.Join("engine", "patches") + "/", "engine/" + "managed-project" + "-plugin", "scripts/",
	}
	if err := fs.WalkDir(memlane.RuntimeAssets, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		for _, prefix := range forbidden {
			if strings.HasPrefix(name, prefix) {
				t.Errorf("forbidden asset embedded: %s", name)
			}
		}
		if strings.HasPrefix(path.Base(name), "test_") && strings.HasSuffix(name, ".py") {
			t.Errorf("test module embedded: %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	second, err := bootstrap.PrepareRuntimeResources(context.Background(), root, memlane.RuntimeAssets)
	if err != nil {
		t.Fatalf("idempotent PrepareRuntimeResources failed: %v", err)
	}
	if second != destination {
		t.Fatalf("idempotent destination mismatch: got %q, want %q", second, destination)
	}

}
