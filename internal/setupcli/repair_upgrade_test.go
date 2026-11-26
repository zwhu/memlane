package setupcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAndRepairUpgradeDryRunApply(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Seed legacy .pyc inside managed Cognee source dir + one retired runtime dir.
	cogneeDir := filepath.Join(f.root, "service-test")
	if err := os.MkdirAll(filepath.Join(cogneeDir, "cognee", "__pycache__"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cogneeDir, "cognee", "__init__.py"), []byte("# ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cogneeDir, "cognee", "__pycache__", "a.pyc"), []byte("pyc"), 0o600); err != nil {
		t.Fatal(err)
	}
	retiredDir := filepath.Join(f.root, "installation", "runtime-aaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(retiredDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// 1. doctor surfaces legacy_upgrade_artifacts_present and points to repair upgrade --dry-run.
	doc := f.command(ExitActionRequired, "doctor")
	if !hasBlocker(doc, "legacy_upgrade_artifacts_present") {
		t.Fatalf("expected legacy_upgrade_artifacts_present blocker, got %+v", doc.Blockers)
	}
	if doc.Artifacts.UpgradeCleanup == nil || doc.Artifacts.UpgradeCleanup.LegacyPycCount != 1 || len(doc.Artifacts.UpgradeCleanup.RetiredRuntimeDirs) != 1 {
		t.Fatalf("unexpected doctor upgrade summary: %+v", doc.Artifacts.UpgradeCleanup)
	}

	// 2. repair upgrade --dry-run returns reviewed_sha, install/cleanup/preserved manifest, and apply action.
	dry := f.command(ExitActionRequired, "repair", "upgrade", "--dry-run")
	if dry.Artifacts.ReviewedSHA == "" || dry.Artifacts.UpgradeCleanup == nil || !dry.Artifacts.UpgradeCleanup.NeedsCleanup {
		t.Fatalf("unexpected repair upgrade dry-run: %+v", dry.Artifacts)
	}
	if len(dry.Artifacts.UpgradeCleanup.InstallItems) == 0 || len(dry.Artifacts.UpgradeCleanup.PreservedItems) == 0 {
		t.Fatalf("expected manifest lists in dry-run output: %+v", dry.Artifacts.UpgradeCleanup)
	}
	rawJSON, _ := json.Marshal(dry)
	if strings.Contains(string(rawJSON), cogneeDir) || strings.Contains(string(rawJSON), retiredDir) {
		t.Fatalf("upgrade cleanup preview leaked full local path: %s", rawJSON)
	}

	// 3. Stale SHA fails closed with ExitPreflightBlocked.
	bad := f.command(ExitPreflightBlocked, "repair", "upgrade", "--apply", "--reviewed-sha", "stale-sha")
	if !hasBlocker(bad, "reviewed_sha_mismatch") {
		t.Fatalf("expected reviewed_sha_mismatch blocker, got %+v", bad.Blockers)
	}

	// 4. Matching SHA applies cleanly and clears artifacts.
	applied := f.command(ExitOK, "repair", "upgrade", "--apply", "--reviewed-sha", dry.Artifacts.ReviewedSHA)
	if applied.Artifacts.UpgradeCleanup == nil || !applied.Artifacts.UpgradeCleanup.Applied || applied.Artifacts.UpgradeCleanup.RemovedPycCount != 1 || len(applied.Artifacts.UpgradeCleanup.RemovedRuntimeDirs) != 1 {
		t.Fatalf("unexpected apply summary: %+v", applied.Artifacts.UpgradeCleanup)
	}
	if _, err := os.Stat(retiredDir); !os.IsNotExist(err) {
		t.Fatalf("expected retired dir removed, got %v", err)
	}

	// 5. Subsequent repair upgrade --dry-run reports clean (ExitOK, NeedsCleanup=false).
	clean := f.command(ExitOK, "repair", "upgrade", "--dry-run")
	if clean.Artifacts.UpgradeCleanup == nil || clean.Artifacts.UpgradeCleanup.NeedsCleanup {
		t.Fatalf("expected clean state after apply, got %+v", clean.Artifacts.UpgradeCleanup)
	}
}
