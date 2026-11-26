package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeCleanupPlansAndAppliesSafely(t *testing.T) {
	root := t.TempDir()
	compRoot := filepath.Join(root, "components")
	storeRoot := filepath.Join(root, "library")
	cogneeDir := filepath.Join(compRoot, "service-ec6f40859e5f2df9e7862dde27d580ac3268ed43")
	installRoot := filepath.Join(compRoot, "installation")

	// Create verified Cognee source tree with legacy .pyc/.pyo and a non-pyc sentinel file inside __pycache__.
	if err := os.MkdirAll(filepath.Join(cogneeDir, "cognee", "__pycache__"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cogneeDir, "cognee", "sub", "__pycache__"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cogneeDir, "cognee", "__init__.py"), []byte("# init\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cogneeDir, "cognee", "__pycache__", "mod.cpython-312.pyc"), []byte("pyc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cogneeDir, "cognee", "sub", "__pycache__", "sub.pyo"), []byte("pyo"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unexpected regular non-bytecode file inside __pycache__ must be preserved!
	keepInCache := filepath.Join(cogneeDir, "cognee", "sub", "__pycache__", "notes.txt")
	if err := os.WriteFile(keepInCache, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create one active runtime dir (kept), one retired runtime dir (cleaned), and one unrelated dir (kept).
	activeRuntime := filepath.Join(installRoot, "runtime-0123456789abcdef")
	retiredRuntime := filepath.Join(installRoot, "runtime-fedcba9876543210")
	unrelatedDir := filepath.Join(installRoot, "other-data")
	for _, d := range []string{activeRuntime, retiredRuntime, unrelatedDir, storeRoot} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	storeDB := filepath.Join(storeRoot, "memlane.sqlite3")
	if err := os.WriteFile(storeDB, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := UpgradeCleanupOptions{
		ComponentRoot:     compRoot,
		StoreRoot:         storeRoot,
		CogneeSourceDir:   cogneeDir,
		KeepRuntimeHashes: []string{"0123456789abcdef"},
	}

	plan, err := PlanUpgradeCleanup(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NeedsCleanup || plan.LegacyPycCount != 2 || plan.LegacyPycacheDirs != 2 || len(plan.RetiredRuntimeDirs) != 1 || plan.RetiredRuntimeDirs[0] != "runtime-fedcba9876543210" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.PlanSHA == "" || len(plan.InstallItems) == 0 || len(plan.CleanupItems) == 0 || len(plan.PreservedItems) == 0 {
		t.Fatalf("expected manifest lists and plan sha: %+v", plan)
	}

	// Wrong reviewed SHA fails closed without deleting anything.
	if _, err := ApplyUpgradeCleanup(context.Background(), opts, "wrong-sha"); !errors.Is(err, ErrUpgradeCleanupPlanChanged) {
		t.Fatalf("expected ErrUpgradeCleanupPlanChanged, got %v", err)
	}

	res, err := ApplyUpgradeCleanup(context.Background(), opts, plan.PlanSHA)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied || res.RemovedPycCount != 2 || res.RemovedPycacheDirs != 1 || len(res.RemovedRuntimeDirs) != 1 {
		t.Fatalf("unexpected apply result: %+v", res)
	}

	// Verify preserved assets are untouched.
	for _, preserved := range []string{
		filepath.Join(cogneeDir, "cognee", "__init__.py"),
		keepInCache,
		activeRuntime,
		unrelatedDir,
		storeDB,
	} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("expected preserved path %q to exist: %v", preserved, err)
		}
	}
	if _, err := os.Stat(retiredRuntime); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected retired runtime %q to be removed, got %v", retiredRuntime, err)
	}

	// Second plan is clean (only the non-empty __pycache__ containing notes.txt remains, wait: verify pyc count is 0).
	after, err := PlanUpgradeCleanup(opts)
	if err != nil {
		t.Fatal(err)
	}
	if after.LegacyPycCount != 0 || len(after.RetiredRuntimeDirs) != 0 {
		t.Fatalf("expected 0 pyc and 0 retired runtimes after cleanup, got %+v", after)
	}
}

func TestUpgradeCleanupRejectsSymlinkedCogneeSource(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "cognee"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "cognee", "__init__.py"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanUpgradeCleanup(UpgradeCleanupOptions{CogneeSourceDir: linkDir}); err == nil {
		t.Fatal("expected symlinked cognee source to be rejected")
	}
}
