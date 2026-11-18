package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestRuntimeResourcesRejectChangedAndExtraModules(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resources")
	assets := fstest.MapFS{
		"engine/python/service.py":      {Data: []byte("# fixed adapter\n")},
		"engine/python/test_service.py": {Data: []byte("# never materialize tests\n")},
		"engine/components.json":        {Data: []byte("{}")},
	}
	directory, err := PrepareRuntimeResources(context.Background(), root, assets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "engine/python/test_service.py")); !os.IsNotExist(err) {
		t.Fatal("test module materialized", err)
	}
	if same, err := PrepareRuntimeResources(context.Background(), root, assets); err != nil || same != directory {
		t.Fatal(same, err)
	}
	injected := filepath.Join(directory, "engine/python/extra.py")
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
	service := filepath.Join(directory, "engine/python/service.py")
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
	assets := fstest.MapFS{"engine/python/service.py": {Data: []byte("# first")}}
	first, err := PrepareRuntimeResources(context.Background(), root, assets)
	if err != nil {
		t.Fatal(err)
	}
	assets["engine/python/service.py"].Data = []byte("# second")
	second, err := PrepareRuntimeResources(context.Background(), root, assets)
	if err != nil || first == second {
		t.Fatal(first, second, err)
	}
	if b, _ := os.ReadFile(filepath.Join(first, "engine/python/service.py")); string(b) != "# first" {
		t.Fatal("previous generation changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareRuntimeResources(ctx, root, assets); err == nil {
		t.Fatal("cancel ignored")
	}
}
