package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	memlane "github.com/zwhu/memlane"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func TestCanonicalStoreRuntimeDirResolvesSymlinkedStoreAndIgnoresTMPDIR(t *testing.T) {
	realRoot := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(realRoot)
	require(t, err)
	realStore := filepath.Join(resolvedRoot, "real-store")
	require(t, os.MkdirAll(realStore, 0o700))
	linkStore := filepath.Join(resolvedRoot, "link-store")
	require(t, os.Symlink(realStore, linkStore))

	ephemeralTmp := filepath.Join(resolvedRoot, "memlane-task-scratch")
	require(t, os.MkdirAll(ephemeralTmp, 0o700))
	t.Setenv("TMPDIR", ephemeralTmp)

	dir, err := canonicalStoreRuntimeDir(linkStore, "memlane-engine-code-")
	require(t, err)
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			t.Errorf("remove runtime dir: %v", rmErr)
		}
	}()

	if strings.HasPrefix(dir, ephemeralTmp) {
		t.Fatalf("daemon runtime dir %q must not live inside ephemeral TMPDIR %q", dir, ephemeralTmp)
	}
	expectedPrefix := filepath.Join(realStore, "cache", "daemon-run") + string(filepath.Separator)
	if !strings.HasPrefix(dir, expectedPrefix) {
		t.Fatalf("daemon runtime dir %q missing expected prefix %q", dir, expectedPrefix)
	}

	workDir, err := bootstrap.PrepareRuntimeResources(context.Background(), dir, memlane.GoEngineAssets)
	require(t, err)
	if workDir == "" {
		t.Fatal("expected non-empty runtime work dir")
	}
}

func TestResolveDetachedExecutableStagesEphemeralBinary(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, "store")
	ephemeralDir := filepath.Join(root, "memlane-task-xyz", "go-build123", "b001", "exe")
	require(t, os.MkdirAll(ephemeralDir, 0o700))
	ephemeralBin := filepath.Join(ephemeralDir, "memlane")
	payload := []byte("#!/bin/sh\necho ok\n")
	require(t, os.WriteFile(ephemeralBin, payload, 0o700))

	staged, err := resolveDetachedExecutable(storeRoot, ephemeralBin)
	require(t, err)
	if staged == ephemeralBin {
		t.Fatalf("expected ephemeral binary %q to be staged persistently", ephemeralBin)
	}
	if !strings.HasPrefix(staged, filepath.Join(storeRoot, "cache", "dev-bin")+string(filepath.Separator)) {
		t.Fatalf("unexpected staged binary path %q", staged)
	}
	if runtime.GOOS == "darwin" {
		if !strings.HasSuffix(staged, filepath.Join("Memlane.app", "Contents", "MacOS", "Memlane")) {
			t.Fatalf("expected macOS app bundle staged binary, got %q", staged)
		}
		if _, err := os.Stat(filepath.Join(storeRoot, "cache", "dev-bin", "Memlane.app", "Contents", "Info.plist")); err != nil {
			t.Fatalf("expected Info.plist in staged dev bundle: %v", err)
		}
		if _, err := os.Stat(filepath.Join(storeRoot, "cache", "dev-bin", "Memlane.app", "Contents", "Resources", "AppIcon.icns")); err != nil {
			t.Fatalf("expected AppIcon.icns in staged dev bundle: %v", err)
		}
	}

	// Re-resolving identical payload reuses staged binary cleanly.
	stagedAgain, err := resolveDetachedExecutable(storeRoot, ephemeralBin)
	require(t, err)
	if stagedAgain != staged {
		t.Fatalf("expected stable staged path %q, got %q", staged, stagedAgain)
	}

	// Non-ephemeral binary outside temp/go-build is returned as-is.
	stableBin := filepath.Join("/usr/local/bin", "memlane")
	gotStable, err := resolveDetachedExecutable(storeRoot, stableBin)
	require(t, err)
	if gotStable != stableBin {
		t.Fatalf("expected stable binary %q unchanged, got %q", stableBin, gotStable)
	}
}

func TestDetachedEnvScrubsTaskScratchVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"TMPDIR=/var/folders/aa/bb/T/memlane-task-abc123/",
		"TMP=/var/folders/aa/bb/T/memlane-task-abc123",
		"TEMP=/var/folders/aa/bb/T/memlane-task-abc123",
		"MEMLANE_DISK_TASK=1",
		"MEMLANE_TASK_OWNER_PID=4242",
	}
	got := detachedEnv(in)
	envMap := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}
	if envMap["PATH"] != "/usr/bin" {
		t.Fatalf("PATH lost: %v", envMap)
	}
	if envMap["TMPDIR"] != "/var/folders/aa/bb/T/" {
		t.Fatalf("expected parent TMPDIR, got %q", envMap["TMPDIR"])
	}
	for _, dropped := range []string{"TMP", "TEMP", "MEMLANE_DISK_TASK", "MEMLANE_TASK_OWNER_PID"} {
		if _, exists := envMap[dropped]; exists {
			t.Fatalf("expected %s to be stripped from detached env", dropped)
		}
	}
}
