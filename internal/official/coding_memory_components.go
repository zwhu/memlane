package official

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/zwhu/memlane/internal/components"
)

const CodingMemoryComponentVersion = "1.6.7-memlane.14-coding-ec6f408"
const CodingMemoryServiceCommit = "ec6f40859e5f2df9e7862dde27d580ac3268ed43"
const CodingMemoryPluginCommit = "a176248914fdb32a78354854b300174203b15a5c"

func CodingMemoryIdentity(commit string, capabilities []string) bool {
	if commit != CodingMemoryServiceCommit {
		return false
	}
	format, trigger := false, false
	for _, capability := range capabilities {
		format = format || capability == "coding_memory_format_v1"
		trigger = trigger || capability == "coding_memory_trigger_v1"
	}
	return format && trigger
}

func codingMemoryContract() map[string]any {
	return map[string]any{"protocol": float64(1), "service_commit": CodingMemoryServiceCommit,
		"service_archive_sha256": "6e11047ef690efedac06653c67a11bea0c0e0a8c6af0c89dad634574f4bc01bc",
		"sdk_version":            "1.6.7", "component_version": CodingMemoryComponentVersion,
		"plugin_commit": CodingMemoryPluginCommit, "plugin_version": "1.8.0"}
}

func readPublicComponentJSON(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("coding_memory_component_contract_invalid")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Manager has already checked both components' file manifests. This gate
// prevents a fixed coding service being paired with an older/different plugin,
// and binds only the explicit host daemon root. It grants no project access.
func (c *Controller) codingMemorySettings(installed map[string]components.Installed, settings *Settings) (bool, error) {
	service := installed["cognee-service"]
	marker := filepath.Join(service.Directory, filepath.Dir(service.Metadata.Executable), "_internal/memlane-coding-memory.json")
	var source map[string]any
	err := readPublicComponentJSON(marker, &source)
	if os.IsNotExist(err) && service.Metadata.Version != CodingMemoryComponentVersion {
		return false, nil
	}
	if err != nil || service.Metadata.Version != CodingMemoryComponentVersion || !reflect.DeepEqual(source, codingMemoryContract()) {
		return false, errors.New("coding_memory_service_input_mismatch")
	}
	plugin := installed["cognee-codex-plugin"]
	var provenance map[string]any
	if readPublicComponentJSON(filepath.Join(plugin.Directory, "upstream.json"), &provenance) != nil || plugin.Metadata.Version != "1.8.0" || !reflect.DeepEqual(provenance, map[string]any{"repository": "https://github.com/topoteretes/cognee-integrations", "sha": CodingMemoryPluginCommit, "version": "1.8.0"}) {
		return false, errors.New("coding_memory_plugin_input_mismatch")
	}
	if !filepath.IsAbs(c.ServiceRoot) || filepath.Clean(c.ServiceRoot) == filepath.Clean(c.Root) {
		return false, errors.New("coding_memory_explicit_daemon_root_required")
	}
	settings.ProcessingOwner = filepath.Join(c.ServiceRoot, "coding-memory-owner.json")
	return true, nil
}
