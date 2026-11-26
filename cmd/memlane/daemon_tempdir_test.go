package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	memlane "github.com/zwhu/memlane"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func TestCanonicalTempDirResolvesSymlinkedTMPDIR(t *testing.T) {
	realRoot := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(realRoot)
	require(t, err)
	realSub := filepath.Join(resolvedRoot, "real-tmp")
	require(t, os.MkdirAll(realSub, 0o700))
	linkSub := filepath.Join(resolvedRoot, "link-tmp")
	require(t, os.Symlink(realSub, linkSub))
	t.Setenv("TMPDIR", linkSub)

	dir, err := canonicalTempDir("memlane-engine-code-")
	require(t, err)
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			t.Errorf("remove temp dir: %v", rmErr)
		}
	}()

	workDir, err := bootstrap.PrepareRuntimeResources(context.Background(), dir, memlane.GoEngineAssets)
	require(t, err)
	if workDir == "" {
		t.Fatal("expected non-empty runtime work dir")
	}
}
