package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPluginReuseVerifiesEveryFileAndRetainsForeignChanges(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(root, "existing")
	if err := os.MkdirAll(filepath.Join(plugin, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("# synthetic official plugin\n")
	digest := sha256.Sum256(content)
	manifest, _ := json.Marshal(map[string]any{"plugin_commit": "fixed", "files": map[string]string{"scripts/main.py": hex.EncodeToString(digest[:])}})
	if err := os.WriteFile(filepath.Join(plugin, "scripts/main.py"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, ".memlane-project-access.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	driver := PluginDriver{Spec: SourceSpec{Commit: "fixed", URL: "https://example.invalid/fixed", SHA256: "digest"}, Version: "fixed-adaptation", ExistingPath: plugin, Manifest: manifest}
	requirement := Requirement{ID: "agent-plugin", Version: driver.Version, Source: driver.Spec.URL, Digest: driver.Spec.SHA256, Destination: filepath.Join(root, "managed")}
	observation, err := driver.Probe(context.Background(), requirement)
	if err != nil || observation.Status != Available || observation.Path != plugin {
		t.Fatal(observation, err)
	}
	foreign := filepath.Join(plugin, "scripts/extra.py")
	if err := os.WriteFile(foreign, []byte("# foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	observation, err = driver.Probe(context.Background(), requirement)
	if err != nil || observation.Status != Incompatible {
		t.Fatal(observation, err)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "# foreign" {
		t.Fatal("foreign plugin content changed")
	}
	if _, err := os.Stat(requirement.Destination); !os.IsNotExist(err) {
		t.Fatal("read-only probe created managed directory", err)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "scripts/main.py"), []byte("# changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPlugin(plugin, manifest); err == nil {
		t.Fatal("modified plugin accepted")
	}
}

func TestPluginReceiptAndFilesCannotBeLinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"files":{"main.py":"synthetic"}}`)
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(root, "plugin")
	if err := os.Mkdir(plugin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(plugin, ".memlane-project-access.json")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPlugin(plugin, manifest); err == nil {
		t.Fatal("linked receipt accepted")
	}
}

// Exercise the real pinned integration archive, extraction, patch construction,
// and manifest verification together. No external network or model is used.
func TestPinnedIntegrationArchiveBuildsManagedPlugin(t *testing.T) {
	archive := filepath.Join("..", "..", ".cache", "coding-memory-components", "a176248914fdb32a78354854b300174203b15a5c.tar.gz")
	b, err := os.ReadFile(archive)
	if os.IsNotExist(err) {
		t.Skip("pinned upstream archive not cached")
	}
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required for official patch builder")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "engine", "managed-project-plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	driver := PluginDriver{Spec: SourceSpec{Commit: "a176248914fdb32a78354854b300174203b15a5c", URL: "https://codeload.github.com/topoteretes/cognee-integrations/tar.gz/a176248914fdb32a78354854b300174203b15a5c", SHA256: "a896399a93d596c11ca1e2b10e72943a7abe04afb0a355dfbdaf5c7a71f11d5f", Packages: []string{"integrations"}}, Version: "fixed", Manifest: manifest, ResourceRoot: filepath.Join(root, "resources"), Python: func(context.Context) (string, error) { return python, nil }}
	req := Requirement{ID: "agent-plugin", Version: driver.Version, Source: driver.Spec.URL, Digest: driver.Spec.SHA256, Destination: filepath.Join(root, "component")}
	// Seed only the owned download cache; Install still extracts and verifies it.
	patchDigest := sha256.Sum256(manifest)
	componentIdentity, _ := json.Marshal(map[string]any{"source": driver.Spec, "patch_sha256": hex.EncodeToString(patchDigest[:])})
	if err := claimComponentRoot(req.Destination, ".memlane-plugin-component.json", componentIdentity); err != nil {
		t.Fatal(err)
	}
	upstream := filepath.Join(req.Destination, "integrations-upstream")
	identity, _ := json.Marshal(driver.Spec)
	if err := claimComponentRoot(upstream, ".memlane-source.json", identity); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "service.tar.gz"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := driver.Install(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	obs, err := driver.Probe(context.Background(), req)
	if err != nil || obs.Status != Available {
		t.Fatal(obs, err)
	}
	if err := driver.Install(context.Background(), req); err != nil {
		t.Fatal("resume:", err)
	}
	// Missing project metadata must still be rejected.
	if err := os.Remove(filepath.Join(upstream, "source", "pyproject.toml")); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(filepath.Join(upstream, "service.tar.gz"), filepath.Join(upstream, "source"), driver.Spec.SHA256, driver.Spec.Packages); err == nil {
		t.Fatal("engine metadata requirement was weakened")
	}
	file := filepath.Join(req.Destination, "plugin", "scripts", "_memlane_policy.py")
	if err := os.WriteFile(file, []byte("# modified"), 0600); err != nil {
		t.Fatal(err)
	}
	obs, err = driver.Probe(context.Background(), req)
	if err != nil || obs.Status != Incompatible {
		t.Fatal("modified plugin accepted", obs, err)
	}
	if content, _ := os.ReadFile(file); string(content) != "# modified" {
		t.Fatal("probe changed foreign content")
	}
}
