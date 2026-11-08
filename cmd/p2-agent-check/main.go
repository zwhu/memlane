// p2-agent-check exercises production adapters on explicitly isolated paths.
// It is a validation tool and is not included in the client bundle.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var r struct {
		Action     string                `json:"action"`
		Connection records.Connection    `json:"connection"`
		Options    agents.InstallOptions `json:"options"`
		Home       string                `json:"home"`
		Selected   map[string]string     `json:"selected"`
		Run        agents.RunOptions     `json:"run"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&r); err != nil {
		panic(err)
	}
	var result any
	var err error
	switch r.Action {
	case "discover":
		result = agents.Discover(context.Background(), r.Home, r.Selected)
	case "install", "uninstall":
		if !isolatedRoot(r.Options.ClientRoot) {
			fmt.Fprintln(os.Stderr, "isolated P2 root required")
			os.Exit(2)
		}
		if r.Action == "install" {
			result, err = agents.Install(context.Background(), r.Connection, r.Options)
		} else {
			err = agents.Uninstall(context.Background(), r.Options.ClientRoot, r.Connection.ID)
		}
	case "run":
		if !isolatedRoot(r.Options.ClientRoot) {
			err = fmt.Errorf("isolated P2 root required")
			break
		}
		var installed agents.Installation
		installed, err = agents.LoadInstallation(r.Options.ClientRoot, r.Connection.ID)
		if err == nil {
			result, err = agents.Run(context.Background(), agents.Probe(context.Background(), r.Connection.Agent, r.Options.AgentPath), installed, r.Run)
		}
	default:
		err = fmt.Errorf("unknown action")
	}
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "error": err.Error(), "result": result})
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "result": result})
}

// Reuse the accepted adapter verifier for E only on the marked synthetic root.
// This command is a validation tool and never enters the distributed bundle.
func isolatedRoot(root string) bool {
	if !filepath.IsAbs(root) {
		return false
	}
	if strings.Contains(root, "memlane-p2-") {
		return true
	}
	fixture := filepath.Dir(root)
	if filepath.Base(root) != "client" || !(strings.HasPrefix(filepath.Base(fixture), "memlane-p3-global-app-") || strings.HasPrefix(filepath.Base(fixture), "memlane-p3-usage-app-") || strings.HasPrefix(filepath.Base(fixture), "memlane-p3-onboarding-app-") || strings.HasPrefix(filepath.Base(fixture), "memlane-p3-agents-app-")) {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(fixture, "source-info.json"))
	if err != nil {
		return false
	}
	var meta struct {
		Synthetic  bool   `json:"synthetic_only"`
		Identifier string `json:"identifier"`
	}
	return json.Unmarshal(raw, &meta) == nil && meta.Synthetic && (strings.HasPrefix(meta.Identifier, "io.github.zwhu.memlane.global.") && strings.HasPrefix(filepath.Base(fixture), "memlane-p3-global-app-") || strings.HasPrefix(meta.Identifier, "io.github.zwhu.memlane.usage.") && strings.HasPrefix(filepath.Base(fixture), "memlane-p3-usage-app-") || strings.HasPrefix(meta.Identifier, "io.github.zwhu.memlane.onboarding.") && strings.HasPrefix(filepath.Base(fixture), "memlane-p3-onboarding-app-") || strings.HasPrefix(meta.Identifier, "io.github.zwhu.memlane.agents.") && strings.HasPrefix(filepath.Base(fixture), "memlane-p3-agents-app-"))
}
