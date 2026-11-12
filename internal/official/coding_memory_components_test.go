package official

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/internal/components"
)

func TestCodingMemoryRecipeMatchesRuntimeGate(t *testing.T) {
	raw, err := os.ReadFile("../../engine/coding-memory-components.json")
	if err != nil {
		t.Fatal(err)
	}
	var recipe struct {
		Service struct {
			Commit           string
			ComponentVersion string `json:"component_version"`
			ArchiveSHA       string `json:"archive_sha256"`
			SDK              string `json:"sdk_version"`
		}
		Plugin struct{ Commit, Version string }
	}
	if err = json.Unmarshal(raw, &recipe); err != nil {
		t.Fatal(err)
	}
	c := codingMemoryContract()
	if c["service_commit"] != recipe.Service.Commit || c["component_version"] != recipe.Service.ComponentVersion || c["service_archive_sha256"] != recipe.Service.ArchiveSHA || c["sdk_version"] != recipe.Service.SDK || c["plugin_commit"] != recipe.Plugin.Commit || c["plugin_version"] != recipe.Plugin.Version {
		t.Fatal("component recipe and runtime gate differ")
	}
	if !CodingMemoryIdentity(recipe.Service.Commit, []string{"coding_memory_format_v1", "coding_memory_trigger_v1"}) || CodingMemoryIdentity("foreign", []string{"coding_memory_format_v1", "coding_memory_trigger_v1"}) || CodingMemoryIdentity(recipe.Service.Commit, []string{"session_improve_v1"}) {
		t.Fatal("runtime version label substituted for coding source/capabilities")
	}
}

func TestCodingMemoryComponentPairBindsExplicitDaemonWithoutChangingLegacyModel(t *testing.T) {
	root := t.TempDir()
	service, plugin := filepath.Join(root, "service"), filepath.Join(root, "plugin")
	if err := os.MkdirAll(filepath.Join(service, "service/_internal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(plugin, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(service, "service/_internal/memlane-coding-memory.json")
	provenance := filepath.Join(plugin, "upstream.json")
	write(marker, codingMemoryContract())
	write(provenance, map[string]any{"repository": "https://github.com/topoteretes/cognee-integrations", "sha": CodingMemoryPluginCommit, "version": "1.8.0"})
	installed := map[string]components.Installed{
		"cognee-service":      {Directory: service, Metadata: components.Metadata{Version: CodingMemoryComponentVersion, Executable: "service/memlane-cognee-service"}},
		"cognee-codex-plugin": {Directory: plugin, Metadata: components.Metadata{Version: "1.8.0"}},
	}
	c := &Controller{Root: filepath.Join(root, "official"), ServiceRoot: filepath.Join(root, "data")}
	settings := Settings{Environment: map[string]string{"LLM_MODEL": "retained-synthetic-model", "LLM_API_KEY": "retained-synthetic-key"}}
	ready, err := c.codingMemorySettings(installed, &settings)
	if err != nil || !ready || settings.ProcessingOwner != filepath.Join(c.ServiceRoot, "coding-memory-owner.json") || settings.Environment["LLM_API_KEY"] != "retained-synthetic-key" || settings.Environment["LLM_MODEL"] != "retained-synthetic-model" {
		t.Fatal("coding setup changed retained model settings or inferred daemon root", err)
	}
	c.ServiceRoot = ""
	if _, err = c.codingMemorySettings(installed, &settings); err == nil {
		t.Fatal("implicit daemon root accepted")
	}
	c.ServiceRoot = filepath.Join(root, "data")
	write(provenance, map[string]any{"repository": "https://github.com/topoteretes/cognee-integrations", "sha": "foreign", "version": "1.8.0"})
	if _, err = c.codingMemorySettings(installed, &settings); err == nil {
		t.Fatal("unmatched plugin source accepted")
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, err = c.codingMemorySettings(installed, &settings); err == nil {
		t.Fatal("version label alone proved coding capability")
	}
	legacy := installed["cognee-service"]
	legacy.Metadata.Version = "1.6.7-memlane.12"
	installed["cognee-service"] = legacy
	if ready, err = c.codingMemorySettings(installed, &settings); ready || err != nil {
		t.Fatal("legacy recipe changed", ready, err)
	}
}
