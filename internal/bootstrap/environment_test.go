package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/engine"
)

func TestDiscoverUVWithLocalBinAndCargoFallback(t *testing.T) {
	home := t.TempDir()
	missLookPath := func(string) (string, error) { return "", errors.New("not found") }

	// Non-executable ~/.local/bin/uv is ignored; falls through to executable ~/.cargo/bin/uv.
	localBin := filepath.Join(home, ".local/bin")
	cargoBin := filepath.Join(home, ".cargo/bin")
	if err := os.MkdirAll(localBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cargoBin, 0700); err != nil {
		t.Fatal(err)
	}
	localUV := filepath.Join(localBin, "uv")
	cargoUV := filepath.Join(cargoBin, "uv")
	if err := os.WriteFile(localUV, []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cargoUV, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := discoverUVWith(missLookPath, home); got != cargoUV {
		t.Fatalf("expected cargo fallback %s, got %s", cargoUV, got)
	}

	if err := os.Chmod(localUV, 0700); err != nil {
		t.Fatal(err)
	}
	if got := discoverUVWith(missLookPath, home); got != localUV {
		t.Fatalf("expected ~/.local/bin/uv %s, got %s", localUV, got)
	}
}

func TestNewEnvironmentChinaDefaultAndOfficialSources(t *testing.T) {
	root := t.TempDir()
	envChina, err := NewEnvironment(context.Background(), engine.BootstrapAssets, EnvironmentOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if envChina.Options.DownloadSource != DownloadSourceChina {
		t.Fatalf("expected default DownloadSource=%q, got %q", DownloadSourceChina, envChina.Options.DownloadSource)
	}
	reqByID := map[string]Requirement{}
	for _, r := range envChina.requirements {
		reqByID[r.ID] = r
		if r.OfficialSource == "" {
			t.Fatalf("expected non-empty OfficialSource for %s", r.ID)
		}
	}
	if !strings.Contains(reqByID["python"].Source, "registry.npmmirror.com") {
		t.Fatalf("expected python china mirror, got %s", reqByID["python"].Source)
	}
	if !strings.Contains(reqByID["runtime"].Source, "pypi.tuna.tsinghua.edu.cn") {
		t.Fatalf("expected runtime china PyPI mirror, got %s", reqByID["runtime"].Source)
	}
	if !strings.Contains(reqByID["cognee-source"].Source, "gh-proxy.org") {
		t.Fatalf("expected source china mirror, got %s", reqByID["cognee-source"].Source)
	}
	if !strings.Contains(reqByID["embedding"].Source, "modelscope.cn") {
		t.Fatalf("expected embedding china mirror, got %s", reqByID["embedding"].Source)
	}

	envOfficial, err := NewEnvironment(context.Background(), engine.BootstrapAssets, EnvironmentOptions{
		Root:           root,
		DownloadSource: DownloadSourceOfficial,
	})
	if err != nil {
		t.Fatal(err)
	}
	if envOfficial.Options.DownloadSource != DownloadSourceOfficial {
		t.Fatalf("expected DownloadSource=%q, got %q", DownloadSourceOfficial, envOfficial.Options.DownloadSource)
	}
	for _, r := range envOfficial.requirements {
		if r.Source != r.OfficialSource {
			t.Fatalf("official mode %s Source=%q != OfficialSource=%q", r.ID, r.Source, r.OfficialSource)
		}
	}
}

func TestSelectionsPersistsDownloadSource(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "runtime.json")
	if err := SaveSelections(EnvironmentOptions{
		Root:           root,
		DownloadSource: DownloadSourceOfficial,
	}, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSelections(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.DownloadSource != DownloadSourceOfficial {
		t.Fatalf("expected persisted DownloadSource=%q, got %q", DownloadSourceOfficial, got.DownloadSource)
	}
}
