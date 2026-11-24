package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

func TestApplyManagedPluginUpdateResolvesActiveSnapshotAndPreservesFields(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	daily := filepath.Join(root, "daily")
	officialData := filepath.Join(daily, "official-store")
	oldPlugin := filepath.Join(daily, "old-plugin")
	newPlugin := filepath.Join(daily, "components", "plugin-new", "plugin")
	for _, dir := range []string{officialData, oldPlugin, filepath.Join(newPlugin, "scripts")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := []byte("# verified new plugin\n")
	sum := sha256.Sum256(script)
	manifest, _ := json.Marshal(map[string]any{
		"plugin_commit": "fixed",
		"files":         map[string]string{"scripts/main.py": hex.EncodeToString(sum[:])},
	})
	if err := os.WriteFile(filepath.Join(newPlugin, "scripts/main.py"), script, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newPlugin, ".memlane-project-access.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}

	persistentCfg := filepath.Join(daily, "runtime.json")
	activeCfg := filepath.Join(daily, ".runtime-active-999.json")
	cfgDoc := map[string]any{
		"official_data": officialData,
		"model":         "gpt-6.1-sol",
		"project_control": map[string]any{
			"url":        "http://127.0.0.1:18777",
			"plugin_dir": oldPlugin,
		},
	}
	rawCfg, _ := json.MarshalIndent(cfgDoc, "", "  ")
	if err := os.WriteFile(persistentCfg, rawCfg, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeCfg, rawCfg, 0600); err != nil {
		t.Fatal(err)
	}
	ownerRaw, _ := json.Marshal(map[string]any{"pid": 999, "config": persistentCfg})
	if err := os.WriteFile(filepath.Join(officialData, ".memlane-dev-owner.json"), ownerRaw, 0600); err != nil {
		t.Fatal(err)
	}

	prev, err := ApplyManagedPluginUpdate(activeCfg, officialData, newPlugin, manifest)
	if err != nil {
		t.Fatalf("ApplyManagedPluginUpdate failed: %v", err)
	}
	if prev != oldPlugin {
		t.Fatalf("expected previous=%q, got %q", oldPlugin, prev)
	}
	for _, path := range []string{persistentCfg, activeCfg} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("expected 0600 permissions on %s, got %v err=%v", path, info.Mode().Perm(), err)
		}
		var updated map[string]any
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &updated); err != nil {
			t.Fatal(err)
		}
		if updated["model"] != "gpt-6.1-sol" || updated["official_data"] != officialData {
			t.Fatalf("lost unrelated keys in %s: %v", path, updated)
		}
		pc := updated["project_control"].(map[string]any)
		if pc["plugin_dir"] != newPlugin || pc["url"] != "http://127.0.0.1:18777" {
			t.Fatalf("unexpected project_control in %s: %v", path, pc)
		}
	}
	if _, err := os.Stat(oldPlugin); err != nil {
		t.Fatalf("old plugin directory must be retained for rollback: %v", err)
	}
}

func TestApplyManagedPluginUpdateRefusesUnverifiedOrInsideStore(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	officialData := filepath.Join(root, "official-store")
	oldPlugin := filepath.Join(root, "old-plugin")
	if err := os.MkdirAll(officialData, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldPlugin, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "runtime.json")
	orig := []byte(`{"project_control":{"plugin_dir":"` + oldPlugin + `"}}`)
	if err := os.WriteFile(cfgPath, orig, 0600); err != nil {
		t.Fatal(err)
	}

	manifest := []byte(`{"plugin_commit":"fixed","files":{"scripts/main.py":"deadbeef"}}`)
	insideStore := filepath.Join(officialData, "plugin-inside")
	if err := os.MkdirAll(insideStore, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyManagedPluginUpdate(cfgPath, officialData, insideStore, manifest); err == nil {
		t.Fatal("accepted plugin path inside official_data")
	}
	unverified := filepath.Join(root, "unverified-plugin")
	if err := os.MkdirAll(unverified, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyManagedPluginUpdate(cfgPath, officialData, unverified, manifest); err == nil {
		t.Fatal("accepted unverified plugin directory")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(orig) {
		t.Fatalf("config modified despite refusal: %s", string(after))
	}
}

func TestPluginClassifiesVerifiedPolicyUpdates(t *testing.T) {
	const policy = "scripts/_memlane_policy.py"
	const original = "scripts/main.py"
	digest := func(contents string) string {
		sum := sha256.Sum256([]byte(contents))
		return hex.EncodeToString(sum[:])
	}
	for _, tc := range []struct {
		name       string
		change     func(receipt, wanted map[string]any, root string) error
		policyOnly bool
	}{
		{name: "same upstream", policyOnly: true},
		{name: "different commit", change: func(_, wanted map[string]any, _ string) error {
			wanted["plugin_commit"] = "new-commit"
			return nil
		}},
		{name: "different lock", change: func(_, wanted map[string]any, _ string) error {
			wanted["upstream_lock_sha256"] = "different"
			return nil
		}},
		{name: "missing old lock", change: func(receipt, _ map[string]any, _ string) error {
			delete(receipt, "upstream_lock_sha256")
			return nil
		}},
		{name: "minimal policy receipt", policyOnly: true, change: func(receipt, wanted map[string]any, _ string) error {
			delete(receipt, "upstream_lock_sha256")
			delete(wanted, "upstream_lock_sha256")
			return nil
		}},
		{name: "minimal receipt with upstream file change", change: func(receipt, wanted map[string]any, _ string) error {
			delete(receipt, "upstream_lock_sha256")
			delete(wanted, "upstream_lock_sha256")
			wanted["files"].(map[string]string)[original] = digest("updated official file")
			return nil
		}},
		{name: "unrelated receipt metadata change", change: func(_, wanted map[string]any, _ string) error {
			wanted["other"] = "unrelated"
			return nil
		}},
		{name: "malformed policy metadata", change: func(receipt, _ map[string]any, _ string) error {
			receipt["patch_sha256"] = []string{"malformed"}
			return nil
		}},
		{name: "tampered policy", change: func(_, _ map[string]any, root string) error {
			return os.WriteFile(filepath.Join(root, policy), []byte("tampered"), 0600)
		}},
		{name: "foreign file", change: func(_, _ map[string]any, root string) error {
			return os.WriteFile(filepath.Join(root, "foreign"), []byte("foreign"), 0600)
		}},
		{name: "missing policy", change: func(_, _ map[string]any, root string) error {
			return os.Remove(filepath.Join(root, policy))
		}},
		{name: "linked file", change: func(_, _ map[string]any, root string) error {
			if err := os.Remove(filepath.Join(root, policy)); err != nil {
				return err
			}
			return os.Symlink("main.py", filepath.Join(root, policy))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "scripts"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, contents := range map[string]string{policy: "original policy", original: "official file"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			receipt := map[string]any{"plugin_commit": "fixed", "upstream_lock_sha256": "upstream", "extension_sha256": "old",
				"files": map[string]string{policy: digest("original policy"), original: digest("official file")}}
			wanted := map[string]any{"plugin_commit": "fixed", "upstream_lock_sha256": "upstream", "extension_sha256": "new",
				"files": map[string]string{policy: digest("updated policy"), original: digest("official file")}}
			if tc.change != nil {
				if err := tc.change(receipt, wanted, root); err != nil {
					t.Fatal(err)
				}
			}
			old, _ := json.Marshal(receipt)
			if err := os.WriteFile(filepath.Join(root, ".memlane-project-access.json"), old, 0600); err != nil {
				t.Fatal(err)
			}
			manifest, _ := json.Marshal(wanted)
			var failure *PluginVerificationError
			kind := PluginUpdateVersionOrContent
			if tc.policyOnly {
				kind = PluginUpdatePolicyOnly
			}
			if err := VerifyPlugin(root, manifest); !errors.As(err, &failure) || failure.Kind != kind || failure.PolicyUpdateOnly != tc.policyOnly {
				t.Fatalf("verification classification = %#v (%v)", failure, err)
			}
			if tc.policyOnly && (failure.OfficialCommit != "fixed" || failure.ExpectedCommit != "fixed" ||
				!reflect.DeepEqual(failure.ChangedFiles, []string{policy}) || !strings.Contains(failure.Error(), policy)) {
				t.Fatalf("policy diagnostics incomplete: %+v", failure)
			}
			driver := PluginDriver{Version: "fixed", ExistingPath: root, Manifest: manifest}
			requirement := Requirement{ID: "agent-plugin", Version: driver.Version, Destination: t.TempDir()}
			obs, err := driver.Probe(context.Background(), requirement)
			if err != nil || obs.Status != Incompatible || IsPluginPolicyUpdate(obs) != tc.policyOnly {
				t.Fatalf("probe classification = %+v (%v)", obs, err)
			}
		})
	}
}
