package bootstrap

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zwhu/memlane"
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
func TestPackagedFirstStoreResources(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required for packaged resource check")
	}
	if err := CheckFirstStoreResources(context.Background(), python, memlane.RuntimeAssets); err != nil {
		t.Fatal(err)
	}
	// Reproduce the original split resource package using the real scripts.
	partial := fstest.MapFS{}
	for _, name := range []string{
		"engines/cognee/first_install_store.py",
		"engines/cognee/__init__.py",
		"engine/coding-memory-components.json",
	} {
		data, err := fs.ReadFile(memlane.RuntimeAssets, name)
		if err != nil {
			t.Fatal(err)
		}
		partial[name] = &fstest.MapFile{Data: data}
	}
	if err := CheckFirstStoreResources(context.Background(), python, partial); err == nil || !strings.Contains(err.Error(), "ModuleNotFoundError: No module named 'engines.cognee.ladybug_extension'") {
		t.Fatalf("expected the missing packaged dependency error, got %v", err)
	}
}
