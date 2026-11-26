package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/kit/agents"
)

func TestActiveProfileGateResolvesBridgeExecutableAndAuthHome(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require(t, err)

	store, err := domain.OpenStore(filepath.Join(resolvedRoot, "memlane.sqlite3"))
	require(t, err)
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	}()

	homeDir := filepath.Join(resolvedRoot, "home")
	traeDir := filepath.Join(homeDir, ".trae")
	require(t, os.MkdirAll(filepath.Join(traeDir, "cli"), 0o700))
	require(t, os.WriteFile(filepath.Join(traeDir, "cli", "auth.json"), []byte(`{"token":"fake"}`), 0o600))
	require(t, os.WriteFile(filepath.Join(traeDir, "traecli.toml"), []byte("model = \"GPT-6.1-Sol\"\nmodel_provider = \"trae\"\n"), 0o600))

	exePath := filepath.Join(resolvedRoot, "bin", "traecli")
	require(t, os.MkdirAll(filepath.Dir(exePath), 0o700))
	require(t, os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	ctx := context.Background()
	saved, err := store.SaveGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID:  "agent-traex",
		Revision:   1,
		Mode:       "agent_bridge",
		BridgeKind: agents.HostTraeX,
		Model:      "GPT-6.1-Sol",
	}, "")
	require(t, err)
	require(t, store.SetActiveGeneration(ctx, saved.ProfileID, saved.Revision))

	gate := activeProfileGate{
		store: store,
		discovery: agents.DiscoveryOptions{
			Home:    homeDir,
			AppDirs: []string{},
			Environ: func(string) string { return "" },
			LookPath: func(name string) (string, error) {
				if name == "traecli" {
					return exePath, nil
				}
				return "", os.ErrNotExist
			},
		},
	}

	active, err := gate.ResolveActive(ctx)
	require(t, err)
	if active == nil {
		t.Fatal("expected active bridge profile")
	}
	if active.Executable != exePath {
		t.Fatalf("expected Executable=%q, got %q", exePath, active.Executable)
	}
	if active.AuthHome != traeDir {
		t.Fatalf("expected AuthHome=%q, got %q", traeDir, active.AuthHome)
	}

	rt, err := agents.CheckBridgeRuntime(active.BridgeKind, active.Executable, active.AuthHome, agents.ModelDefaultsOptions{})
	require(t, err)
	if rt.Executable != exePath || rt.Home != traeDir {
		t.Fatalf("unexpected verified runtime: %+v", rt)
	}
}
