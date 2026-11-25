package bootstrap

import (
	"fmt"
	"strings"

	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestPrepareRuntimeResourcesLockFreeWhenStaged(t *testing.T) {
	installer := Installer{Root: filepath.Join(workerTestRoot(t), "installation")}
	ctx := t.Context()
	assets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("# bundled launcher")}}
	wantBusy := func() {
		t.Helper()
		if dir, err := PrepareRuntimeResources(ctx, installer.Root, assets); !errors.Is(err, ErrInstallWorkerBusy) || dir != "" {
			t.Fatalf("prepare with lock held = %q, %v", dir, err)
		}
	}
	if err := installer.withLock(func() error {
		wantBusy()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	staged, err := PrepareRuntimeResources(ctx, installer.Root, assets)
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.withLock(func() error {
		if dir, err := PrepareRuntimeResources(ctx, installer.Root, assets); err != nil || dir != staged {
			t.Fatalf("reuse with lock held = %q, %v; want %q", dir, err, staged)
		}
		for _, name := range []string{".cache", "build/disk-cleanup"} {
			dir := filepath.Join(staged, name)
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
			wantBusy()
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleanup := filepath.Join(staged, "build/disk-cleanup")
	if err := os.Remove(cleanup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), cleanup); err != nil {
		t.Fatal(err)
	}
	if verifyPreparedRuntimeResources(staged, assets) {
		t.Fatal("verified a symlinked runtime workspace")
	}
	if _, err := PrepareRuntimeResources(ctx, installer.Root, assets); err == nil {
		t.Fatal("staging accepted a symlinked runtime workspace")
	}
}

func TestRuntimeResourcesRejectChangedAndExtraModules(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resources")
	assets := fstest.MapFS{
		"engines/cognee/service.py":      {Data: []byte("# fixed adapter\n")},
		"engines/cognee/test_service.py": {Data: []byte("# never materialize tests\n")},
		"engine/components.json":         {Data: []byte("{}")},
	}
	directory, err := PrepareRuntimeResources(context.Background(), root, assets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "engines/cognee/test_service.py")); !os.IsNotExist(err) {
		t.Fatal("test module materialized", err)
	}
	if same, err := PrepareRuntimeResources(context.Background(), root, assets); err != nil || same != directory {
		t.Fatal(same, err)
	}
	injected := filepath.Join(directory, "engines/cognee/extra.py")
	if err := os.WriteFile(injected, []byte("# foreign import"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntimeResources(context.Background(), root, assets); err == nil {
		t.Fatal("extra import accepted")
	}
	if b, _ := os.ReadFile(injected); string(b) != "# foreign import" {
		t.Fatal("foreign material removed")
	}
	if err := os.Remove(injected); err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(directory, "engines/cognee/service.py")
	if err := os.WriteFile(service, []byte("# modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntimeResources(context.Background(), root, assets); err == nil {
		t.Fatal("modified adapter accepted")
	}
	if b, _ := os.ReadFile(service); string(b) != "# modified" {
		t.Fatal("modified adapter replaced")
	}
}

func TestRuntimeResourcesVersionChangesDoNotRewritePreviousGeneration(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resources")
	assets := fstest.MapFS{"engines/cognee/service.py": {Data: []byte("# first")}}
	first, err := PrepareRuntimeResources(context.Background(), root, assets)
	if err != nil {
		t.Fatal(err)
	}
	assets["engines/cognee/service.py"].Data = []byte("# second")
	second, err := PrepareRuntimeResources(context.Background(), root, assets)
	if err != nil || first == second {
		t.Fatal(first, second, err)
	}
	if b, _ := os.ReadFile(filepath.Join(first, "engines/cognee/service.py")); string(b) != "# first" {
		t.Fatal("previous generation changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareRuntimeResources(ctx, root, assets); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestBundledRuntimeHashReadOnly(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	assets := os.DirFS(source)
	before, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	full, err := BundledRuntimeSHA256(assets)
	if err != nil || len(full) != 64 {
		t.Fatal(full, err)
	}
	hash, err := BundledRuntimeHash(assets)
	if err != nil || hash != full[:16] {
		t.Fatal(hash, err)
	}
	if after, err := os.ReadDir(source); err != nil || len(after) != len(before) || after[0].Name() != before[0].Name() {
		t.Fatal(after, err)
	}
	if after, err := os.ReadDir(root); err != nil || len(after) != 1 {
		t.Fatal(after, err)
	}
	dir, err := PrepareRuntimeResources(context.Background(), filepath.Join(root, "installation"), assets)
	if got, ok := RuntimeHashFromDir(dir); err != nil || !ok || got != hash {
		t.Fatal(dir, got, err)
	}
}

func TestRefreshManagedGraphAssetsCAS(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(root, "installation")
	next := filepath.Join(install, "runtime-0123456789abcdef")
	if err := os.MkdirAll(next, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "runtime.json")
	old := filepath.Join(install, "runtime-aaaaaaaaaaaaaaaa", graphAssetsSuffix)
	target := filepath.Join(next, graphAssetsSuffix)
	for _, tc := range []struct {
		path    string
		changed bool
	}{
		{old, true}, {"", true}, {target, false},
		{filepath.Join(root, "dev checkout", graphAssetsSuffix), false},
		{filepath.Join(root, "installation-other", "runtime-aaaaaaaaaaaaaaaa", graphAssetsSuffix), false},
	} {
		original := fmt.Sprintf(`{"graph_assets":%q,"other":{"count":9007199254740993,"id":"kept"}}`, tc.path)
		if err := os.WriteFile(config, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := RefreshManagedGraphAssets(config, install, next)
		if got != tc.changed || err != nil {
			t.Fatal(tc, got, err)
		}
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		if !got && string(raw) != original {
			t.Fatal("unchanged config rewritten")
		}
		if got && (!strings.Contains(string(raw), fmt.Sprintf("%q", target)) || !strings.Contains(string(raw), "9007199254740993") || !strings.HasSuffix(string(raw), "\n")) {
			t.Fatal(string(raw))
		}
	}
	linked := filepath.Join(install, "runtime-bbbbbbbbbbbbbbbb")
	if err := os.Symlink(next, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshManagedGraphAssets(config, install, linked); err == nil {
		t.Fatal("linked runtime accepted")
	}
}

func TestRefreshManagedGraphAssetsConcurrentCalls(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(root, "installation")
	next := filepath.Join(install, "runtime-0123456789abcdef")
	config := filepath.Join(root, "runtime.json")
	writeBootstrapTestJSON(t, config, map[string]any{"graph_assets": ""})
	type result struct {
		changed bool
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			changed, err := RefreshManagedGraphAssets(config, install, next)
			results <- result{changed, err}
		}()
	}
	changes := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.changed {
			changes++
		}
	}
	if changes != 1 {
		t.Fatal("expected one write, got", changes)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 || entries[0].Name() != "runtime.json" {
		t.Fatal(entries, err)
	}
}
