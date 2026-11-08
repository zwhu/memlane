package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// Explicit filesystem fault fixture; no Enola/worker/App claim.
func TestCompletedExtractionCleanupKeepsActiveUnknownAndOutside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "owner")
	dir := filepath.Join(root, "code-extraction")
	done, active, link := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, name := range []string{done, active, "unknown"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("synthetic unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	n, err := RemoveCompletedExtractions(context.Background(), root, []string{done, link})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	for _, name := range []string{active, "unknown"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if text, err := os.ReadFile(sentinel); err != nil || string(text) != "synthetic unrelated" {
		t.Fatal("cleanup escaped", err)
	}
	if _, err := RemoveCompletedExtractions(context.Background(), root, []string{"../outside"}); err == nil {
		t.Fatal("invalid job path accepted")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "other"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveCompletedExtractions(context.Background(), root, []string{done}); err == nil {
		t.Fatal("relative management directory alias followed")
	}
}
