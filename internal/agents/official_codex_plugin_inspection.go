//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type OfficialCodexPluginInspection struct {
	Installation OfficialCodexPluginReceipt         `json:"installation"`
	Hooks        *CodexPluginHookInspection         `json:"hooks,omitempty"`
	CodingMode   *OfficialCodexCodingModeInspection `json:"coding_mode,omitempty"`
}

// Reconstructs installed state and asks the host for its actual hook trust.
// Ready hooks do not prove service configuration, capture or saved material.
func VerifyOfficialCodexPlugin(ctx context.Context, o OfficialCodexPluginOptions) (OfficialCodexPluginInspection, error) {
	var out OfficialCodexPluginInspection
	r, err := InspectOfficialCodexPlugin(ctx, o)
	out.Installation = r
	if err != nil || r.State != "installed_pending_verification" {
		return out, err
	}
	path := filepath.Join(r.Directory, "hooks.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return out, errors.New("official_plugin_hooks_manifest_unverified")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, errors.New("official_plugin_hooks_manifest_unread")
	}
	var manifest struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if json.Unmarshal(raw, &manifest) != nil || len(manifest.Hooks) == 0 || len(manifest.Hooks) > 32 {
		return out, errors.New("official_plugin_hooks_manifest_invalid")
	}
	var events []string
	for name := range manifest.Hooks {
		if name == "" {
			return out, errors.New("official_plugin_hooks_manifest_invalid")
		}
		events = append(events, strings.ToLower(name[:1])+name[1:])
	}
	sort.Strings(events)
	hooks, err := InspectCodexPluginHooks(ctx, CodexPluginHookOptions{Executable: o.Executable, Home: o.Home, CodexDirectory: o.CodexDirectory, Directory: o.Home, PluginID: r.PluginID, RequiredEvents: events})
	if err != nil {
		return out, err
	}
	after, err := InspectOfficialCodexPlugin(ctx, o)
	if err != nil || after != r {
		return out, errors.New("official_plugin_changed_during_inspection")
	}
	out.Hooks = &hooks
	if o.CodingMode != nil {
		// These semantics are pinned to the fixed 1.8.0 official plugin. An
		// installed legacy plugin cannot borrow its configuration verdict.
		if r.Version != "1.8.0" {
			return out, errors.New("official_plugin_coding_mode_version_unsupported")
		}
		mode, e := InspectOfficialCodexCodingMode(*o.CodingMode)
		if e != nil {
			return out, e
		}
		out.CodingMode = &mode
	}
	return out, nil
}
