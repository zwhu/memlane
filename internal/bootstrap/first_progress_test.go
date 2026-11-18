package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstProgressRequiresAppliedGenerationAndDurableClientReceipt(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "library")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	storeID := strings.Repeat("a", 32)
	write(filepath.Join(root, "store-identity.json"), StoreReceipt{Schema: 1, StoreID: storeID, SourceCommit: "fixed", OwnerID: "synthetic-owner", KeyID: "synthetic-key", StorageInitialized: true})
	configPath := filepath.Join(parent, "runtime.json")
	first := &FirstInstallation{Schema: 1, StoreID: storeID, Setup: true}
	config := map[string]any{"first_install": first, "official_data": root, "service_commit": "fixed", "token_file": filepath.Join(root, "owner.token"), "generation_file": filepath.Join(root, "generation.json"), "generation_mode": "agent_bridge", "model": "synthetic-model"}
	write(configPath, config)
	state := map[string]any{"schema_version": 1, "initialization_completed_at": "2026-11-18T02:00:00Z", "first_used_date": "2026-11-18", "first_used_timezone": "UTC"}
	write(filepath.Join(root, "client-state.json"), state)
	assertComplete := func(expected bool, expectError bool) {
		t.Helper()
		complete, err := FirstInitializationComplete(configPath)
		if complete != expected || (err != nil) != expectError {
			t.Fatalf("complete=%v error=%v", complete, err)
		}
	}
	assertComplete(false, false) // An old client date cannot bypass setup.
	first.Setup = false
	write(configPath, config)
	assertComplete(false, false) // No generation receipt yet.
	generation := map[string]any{"schema_version": 2, "active": nil, "profiles": map[string]any{"candidate": []any{map[string]any{"revision": 1, "profile": map[string]any{"mode": "agent_bridge", "model": "synthetic-model"}}}}}
	write(filepath.Join(root, "generation.json"), generation)
	assertComplete(false, false) // Saving a profile is not activation.
	generation["active"] = FirstGenerationReference{ID: "candidate", Revision: 2}
	write(filepath.Join(root, "generation.json"), generation)
	assertComplete(false, true)
	generation["active"] = FirstGenerationReference{ID: "candidate", Revision: 1}
	write(filepath.Join(root, "generation.json"), generation)
	assertComplete(true, false)
	state["initialization_completed_at"] = "2026-11-18T02:00:00"
	write(filepath.Join(root, "client-state.json"), state)
	assertComplete(false, true) // Completion requires an explicit timezone.
	if step, err := FirstWizardStep(configPath); err != nil || step != 2 {
		t.Fatal(step, err)
	}
	if err := SaveFirstWizardStep(configPath, 3); err != nil {
		t.Fatal(err)
	}
	if step, err := FirstWizardStep(configPath); err != nil || step != 3 {
		t.Fatal(step, err)
	}
	write(filepath.Join(root, "first-install-wizard.json"), firstWizardProgress{Schema: 1, StoreID: "another-store", Step: 3})
	if _, err := FirstWizardStep(configPath); err == nil {
		t.Fatal("foreign wizard receipt accepted")
	}
}
