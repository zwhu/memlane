package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSourcePreparationResumesVerifiedArchiveWithoutNetwork(t *testing.T) {
	archive, _, digest := sourceFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "engine")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	d := SourceDriver{Spec: SourceSpec{Commit: "fixed-commit", URL: "https://invalid.example.test/must-not-download", SHA256: digest, Packages: []string{"cognee"}}}
	marker, _ := json.Marshal(d.Spec)
	if err := os.WriteFile(filepath.Join(destination, ".memlane-source.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "service.tar.gz"), b, 0600); err != nil {
		t.Fatal(err)
	}
	r := Requirement{ID: "cognee-source", Version: d.Spec.Commit, Source: d.Spec.URL, Digest: d.Spec.SHA256, Destination: destination}
	if err := d.Install(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	o, err := d.Probe(context.Background(), r)
	if err != nil || o.Status != Available {
		t.Fatal(o, err)
	}
	// A second invocation validates and reuses; it never downloads over good data.
	if err := d.Install(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "source/cognee/__init__.py"), []byte("# modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Install(context.Background(), r); err == nil {
		t.Fatal("modified source overwritten")
	}
}
func TestSourceInstallPreservesUnownedDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "existing")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "user.txt"), []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	d := SourceDriver{Spec: SourceSpec{Commit: "fixed", URL: "https://invalid.example.test/no-download", SHA256: "fixed-sha"}}
	r := Requirement{ID: "cognee-source", Version: d.Spec.Commit, Source: d.Spec.URL, Digest: d.Spec.SHA256, Destination: destination}
	if err := d.Install(context.Background(), r); err == nil {
		t.Fatal("unowned destination accepted")
	}
	entries, _ := os.ReadDir(destination)
	if len(entries) != 1 || entries[0].Name() != "user.txt" {
		t.Fatal(entries)
	}
}
