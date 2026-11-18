package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestFirstStoreNeverCreatesAnotherLibraryForExistingDailyEnvironment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-create")
	environment := Environment{Options: EnvironmentOptions{Root: root, ProtectedStore: "/existing/daily"}}
	if _, err := environment.InitializeStore(context.Background(), filepath.Join(root, "new-library"), nil); err == nil {
		t.Fatal("existing daily environment created a second store")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("existing store guard mutated state")
	}
}
func TestPinnedStoreAdaptersAreVerifiedBeforeReuse(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"python/first_install_store.py": &fstest.MapFile{Data: []byte("synthetic initializer")}, "python/official_service_dev.py": &fstest.MapFile{Data: []byte("synthetic source verification adapter")}, "coding-memory-components.json": &fstest.MapFile{Data: []byte("{}")}}
	resources, err := prepareStoreResources(root, assets)
	if err != nil {
		t.Fatal(err)
	}
	again, err := prepareStoreResources(root, assets)
	if err != nil || again != resources {
		t.Fatal(again, err)
	}
	path := filepath.Join(resources, "python/first_install_store.py")
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareStoreResources(root, assets); err == nil {
		t.Fatal("changed bootstrap code accepted")
	}
	retained, _ := os.ReadFile(path)
	if string(retained) != "modified" {
		t.Fatal("changed bootstrap code overwritten")
	}
}
