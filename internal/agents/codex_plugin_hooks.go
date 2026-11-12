//go:build darwin || linux

package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type CodexPluginHookOptions struct {
	Executable, Home, CodexDirectory, Directory, Guard string
	PluginID                                           string
	// Taken from the verified installed plugin manifest, not a guessed universal
	// set of events. This inspector does not attest the plugin's provenance.
	RequiredEvents []string
}

type CodexPluginHookState struct {
	Event   string `json:"event"`
	Hash    string `json:"hash"`
	Trust   string `json:"trust"`
	Enabled bool   `json:"enabled"`
}

type CodexPluginHookInspection struct {
	PluginID string `json:"plugin_id"`
	// hooks_ready proves only the observed hook definitions are enabled/trusted.
	// It does not prove service configuration, collection or a saved receipt.
	State      string                 `json:"state"`
	Hooks      []CodexPluginHookState `json:"hooks"`
	OtherHooks int                    `json:"other_hooks"`
	Warnings   int                    `json:"warnings"`
	Errors     int                    `json:"errors"`
}

type codexHookCatalog struct {
	Data []struct {
		Cwd   string `json:"cwd"`
		Hooks []struct {
			Event    string  `json:"eventName"`
			Hash     string  `json:"currentHash"`
			Trust    string  `json:"trustStatus"`
			Source   string  `json:"source"`
			PluginID *string `json:"pluginId"`
			Enabled  bool    `json:"enabled"`
		} `json:"hooks"`
		Warnings []string   `json:"warnings"`
		Errors   []struct{} `json:"errors"`
	} `json:"data"`
}

// InspectCodexPluginHooks asks the actual host to resolve its hook layers. It
// sends no thread/start, turn/start, login, config write or hook trust mutation.
// Raw commands, diagnostics and config values never enter the returned state.
func InspectCodexPluginHooks(ctx context.Context, o CodexPluginHookOptions) (CodexPluginHookInspection, error) {
	out := CodexPluginHookInspection{PluginID: o.PluginID, State: "unread"}
	if o.PluginID == "" || len(o.RequiredEvents) == 0 || !filepath.IsAbs(o.Executable) {
		return out, errors.New("codex_plugin_hook_options_invalid")
	}
	for _, directory := range []string{o.Home, o.CodexDirectory, o.Directory} {
		info, err := os.Stat(directory)
		if !filepath.IsAbs(directory) || err != nil || !info.IsDir() {
			return out, errors.New("codex_plugin_hook_directory_unavailable")
		}
	}
	run, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	env := []string{"HOME=" + o.Home, "CODEX_HOME=" + o.CodexDirectory, "PATH=" + filepath.Dir(o.Executable) + ":/usr/bin:/bin"}
	rpc, err := startCodexRPC(run, o.Executable, o.Guard, []string{"app-server", "--stdio"}, env, o.Directory, nil)
	if err != nil {
		return out, errors.New("codex_plugin_hook_host_unavailable")
	}
	defer rpc.close(nil)
	if err = rpc.initialize(run); err != nil {
		return out, errors.New("codex_plugin_hook_initialize_failed")
	}
	var catalog codexHookCatalog
	if err = rpc.call(run, "hooks/list", map[string]any{"cwds": []string{o.Directory}}, &catalog); err != nil {
		return out, errors.New("codex_plugin_hook_read_failed")
	}
	return summarizeCodexPluginHooks(catalog, o)
}

func summarizeCodexPluginHooks(c codexHookCatalog, o CodexPluginHookOptions) (CodexPluginHookInspection, error) {
	out := CodexPluginHookInspection{PluginID: o.PluginID, State: "hooks_missing", Hooks: []CodexPluginHookState{}}
	if len(c.Data) != 1 || filepath.Clean(c.Data[0].Cwd) != filepath.Clean(o.Directory) {
		return out, errors.New("codex_plugin_hook_scope_mismatch")
	}
	entry := c.Data[0]
	out.Warnings, out.Errors = len(entry.Warnings), len(entry.Errors)
	seen := map[string]bool{}
	for _, h := range entry.Hooks {
		if h.Source != "plugin" || h.PluginID == nil || *h.PluginID != o.PluginID {
			out.OtherHooks++
			continue
		}
		out.Hooks = append(out.Hooks, CodexPluginHookState{Event: h.Event, Hash: h.Hash, Trust: h.Trust, Enabled: h.Enabled})
		seen[h.Event] = true
	}
	if out.Errors > 0 || out.Warnings > 0 {
		out.State = "catalog_incomplete"
		return out, nil
	}
	if len(out.Hooks) == 0 {
		return out, nil
	}
	for _, h := range out.Hooks {
		if h.Hash == "" {
			out.State = "catalog_incomplete"
			return out, nil
		}
		if !h.Enabled {
			out.State = "hooks_disabled"
			return out, nil
		}
		if h.Trust != "trusted" {
			out.State = "review_required"
			return out, nil
		}
	}
	for _, event := range o.RequiredEvents {
		if !seen[event] {
			out.State = "hooks_incomplete"
			return out, nil
		}
	}
	out.State = "hooks_ready"
	return out, nil
}
