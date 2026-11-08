package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// Explicit filesystem fault fixtures, not Rust/model acceptance.
func TestCodePruneRetainsCurrentAndCannotEscapeManagedRoot(t *testing.T) {
	base := t.TempDir()
	dataset, current, prior, obsolete := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	data := filepath.Join(base, "engine")
	dir := filepath.Join(data, "code", dataset)
	stage := ".building-" + uuid.NewString()
	for _, name := range []string{current, prior, obsolete, stage, "unknown-directory"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(base, "outside")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(out, "sentinel")
	if err := os.WriteFile(secret, []byte("synthetic unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	link := uuid.NewString()
	if err := os.Symlink(out, filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	c := &Client{config: Config{DataDir: data}}
	n, err := c.CodePrune(context.Background(), map[string][]string{dataset: {current, prior}})
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	for _, name := range []string{current, prior, "unknown-directory"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{obsolete, stage, link} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("obsolete path remains", name, err)
		}
	}
	if text, err := os.ReadFile(secret); err != nil || string(text) != "synthetic unrelated data" {
		t.Fatal("prune escaped root", err)
	}
	foreign := uuid.NewString()
	if err := os.Symlink(out, filepath.Join(data, "code", foreign)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CodePrune(context.Background(), map[string][]string{foreign: {}}); err == nil {
		t.Fatal("foreign dataset link followed")
	}
	if _, err := c.CodePrune(context.Background(), map[string][]string{"../outside": {}}); err == nil {
		t.Fatal("untrusted path accepted")
	}
	// Internal relative links must not redirect one scope's keep set to another.
	alias := uuid.NewString()
	if err := os.Symlink(dataset, filepath.Join(data, "code", alias)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CodePrune(context.Background(), map[string][]string{alias: {}}); err == nil {
		t.Fatal("sibling dataset link followed")
	}
	for _, name := range []string{current, prior, "unknown-directory"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("sibling current removed", err)
		}
	}
	aliasData := filepath.Join(base, "alias-engine")
	if err := os.Mkdir(aliasData, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(aliasData, "other"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", filepath.Join(aliasData, "code")); err != nil {
		t.Fatal(err)
	}
	aliasClient := &Client{config: Config{DataDir: aliasData}}
	if _, err := aliasClient.CodePrune(context.Background(), map[string][]string{dataset: {}}); err == nil {
		t.Fatal("code root relative link followed")
	}
}
