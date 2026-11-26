package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/kit/agents"
)

func writeConfig(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallAllHosts(t *testing.T) {
	for _, host := range []string{HostTraeIDE, HostTraeX, HostCodex, HostAntigravity, HostPi} {
		t.Run(host, func(t *testing.T) {
			opts := InstallOptions{Host: host, ProfileDir: t.TempDir(), BinaryPath: "/path with space/memlane", GatewayURL: "http://localhost:18790", KeyFile: "/key's directory/key"}
			want := 2
			if host == HostTraeIDE || host == HostPi {
				want = 1
			}
			preview, err := InstallHostConfig(opts)
			if err != nil || len(preview.Previews) != want || preview.Applied {
				t.Fatalf("%+v %v", preview, err)
			}
			for _, p := range preview.Previews {
				if _, err := os.Stat(p.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("preview wrote %s", p.Path)
				}
			}
			opts.Apply = true
			result, err := InstallHostConfig(opts)
			if err != nil || !result.Applied {
				t.Fatalf("%+v %v", result, err)
			}
			if repeat, err := InstallHostConfig(opts); err != nil || len(repeat.Previews) != 0 {
				t.Fatalf("repeat %+v %v", repeat, err)
			}
			if host == HostPi {
				binding := result.Previews[0].Path
				if got := readConfig(t, binding); strings.Contains(got, "python") || !strings.Contains(got, "token_file") {
					t.Fatal(got)
				}
				if _, err := UninstallHostConfig(opts); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(binding); !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				return
			}
			raw := readConfig(t, filepath.Join(opts.ProfileDir, "hooks.json"))
			data, err := agents.ParseJSONC([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if host == HostAntigravity {
				if _, hasLegacy := data["hooks"]; hasLegacy {
					t.Fatalf("antigravity hooks.json must use top-level named hooks, got: %s", raw)
				}
				memlane, ok := data["memlane"].(object)
				if !ok || len(memlane) != 4 {
					t.Fatalf("expected 4 events in antigravity memlane hook, got: %s", raw)
				}
				for _, flat := range []string{SessionStart, PreInvocation, Stop} {
					rows, ok := memlane[flat].([]any)
					if !ok || len(rows) != 1 {
						t.Fatalf("expected flat array for %s, got: %s", flat, raw)
					}
					handler, ok := rows[0].(object)
					if !ok || handler["type"] != "command" || !strings.Contains(stringField(handler, "command"), "hook antigravity "+flat) {
						t.Fatalf("unexpected %s handler: %+v", flat, rows[0])
					}
				}
				postRows, ok := memlane[PostToolUse].([]any)
				if !ok || len(postRows) != 1 {
					t.Fatalf("expected grouped array for PostToolUse, got: %s", raw)
				}
				postGroup, ok := postRows[0].(object)
				if !ok || postGroup["matcher"] != "*" {
					t.Fatalf("expected matcher '*' for PostToolUse, got: %+v", postRows[0])
				}
				nested, ok := postGroup["hooks"].([]any)
				if !ok || len(nested) != 1 || !strings.Contains(stringField(nested[0].(object), "command"), "hook antigravity PostToolUse") {
					t.Fatalf("unexpected PostToolUse nested hooks: %+v", postGroup["hooks"])
				}
				for _, forbidden := range []string{UserPromptSubmit, PostToolUseFailure, PreCompact, SessionEnd} {
					if _, exists := memlane[forbidden]; exists {
						t.Fatalf("unexpected non-antigravity event %s in: %s", forbidden, raw)
					}
				}
			} else {
				hooks := data["hooks"].(object)
				wantEvents := []string{SessionStart, UserPromptSubmit, PostToolUse, Stop}
				if host != HostTraeIDE {
					wantEvents = append(wantEvents, PostToolUseFailure, PreCompact, SessionEnd)
				}
				if len(hooks) != len(wantEvents) {
					t.Fatalf("host %s: got %d events (%v), want %d (%v)\n%s", host, len(hooks), hooks, len(wantEvents), wantEvents, raw)
				}
				for _, ev := range wantEvents {
					if _, exists := hooks[ev]; !exists || !strings.Contains(raw, "hook "+host+" "+ev) {
						t.Fatalf("host %s missing event %s in: %s", host, ev, raw)
					}
				}
				if host == HostTraeIDE {
					for _, forbidden := range []string{PostToolUseFailure, PreCompact, SessionEnd} {
						if _, exists := hooks[forbidden]; exists {
							t.Fatalf("TRAE IDE must not install unsupported event %s: %s", forbidden, raw)
						}
					}
				}
			}
			if _, err := UninstallHostConfig(opts); err != nil {
				t.Fatal(err)
			}
			afterUninstall := readConfig(t, filepath.Join(opts.ProfileDir, "hooks.json"))
			if strings.Contains(afterUninstall, marker) || strings.Contains(afterUninstall, "hook "+host+" ") || strings.Contains(afterUninstall, PreCompact) {
				t.Fatalf("hook remains after uninstall: %s", afterUninstall)
			}
		})
	}
}

func TestInstallConflictTiers(t *testing.T) {
	dir := t.TempDir()
	opts := InstallOptions{Host: HostCodex, ProfileDir: dir, BinaryPath: "/bin/memlane", GatewayURL: "http://127.0.0.1:9123", KeyFile: "/agent.key", Apply: true}
	toml := filepath.Join(dir, "config.toml")
	hooksPath := filepath.Join(dir, "hooks.json")
	const userConfig = "# shared config\n[mcp_servers.user]\nurl = \"http://localhost:9910\" # keep endpoint\n"
	const legacyConfig = "[mcp_servers.memlane-cognee]\ncommand = \"legacy helper\"\n"
	const foreignConfig = "[mcp_servers.cognee]\ncommand = \"external-cognee\"\n"
	const originalHooks = `{"hooks":{"Stop":[{"matcher":"*", "hooks":[{"type":"command","command":"echo keep"}, {"type":"command","command":"/p/memlane-cognee/legacy-hook Stop"}]}]}}`
	writeConfig(t, toml, userConfig+legacyConfig+foreignConfig)
	writeConfig(t, hooksPath, originalHooks)
	result, err := InstallHostConfig(opts)
	if !errors.Is(err, ErrConflictBlocked) || !result.Blocked || result.Conflicts == nil || len(result.Conflicts.ThirdPartyConflicts) == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if readConfig(t, toml) != userConfig+legacyConfig+foreignConfig || readConfig(t, hooksPath) != originalHooks {
		t.Fatal("blocked install wrote configuration")
	}

	writeConfig(t, toml, userConfig+legacyConfig)
	opts.Apply = false
	result, err = InstallHostConfig(opts)
	if err != nil || len(result.Conflicts.ManagedLegacy) < 2 || result.Conflicts.UnrelatedCount == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if readConfig(t, toml) != userConfig+legacyConfig {
		t.Fatal("preview changed TOML")
	}
	opts.Apply = true
	if _, err := InstallHostConfig(opts); err != nil {
		t.Fatal(err)
	}
	installed := readConfig(t, toml)
	installedDoc, err := agents.ParseTOML([]byte(installed))
	if err != nil {
		t.Fatal(err)
	}
	installedMemlane := installedDoc["mcp_servers"].(object)["memlane"].(object)
	if !strings.Contains(installed, userConfig) || strings.Contains(installed, "legacy helper") ||
		installedMemlane["command"] != "/bin/memlane" || installedMemlane["url"] != nil || installedMemlane["bearer_token_file"] != nil {
		t.Fatal(installed)
	}
	if got := readConfig(t, hooksPath); !strings.Contains(got, "echo keep") || strings.Contains(got, "legacy-hook") {
		t.Fatal(got)
	}

	// Legacy HTTP [mcp_servers.memlane] (url + bearer_token_file) upgrades cleanly to stdio.
	legacyHTTP := userConfig + "[mcp_servers.memlane]\nurl = \"http://127.0.0.1:9123/mcp\"\nbearer_token_file = \"/agent.key\"\n_memlane_managed = \"memlane-hook\"\n"
	writeConfig(t, toml, legacyHTTP)
	if _, err := InstallHostConfig(opts); err != nil {
		t.Fatal(err)
	}
	upgraded := readConfig(t, toml)
	upgradedDoc, err := agents.ParseTOML([]byte(upgraded))
	if err != nil {
		t.Fatal(err)
	}
	upgradedMemlane := upgradedDoc["mcp_servers"].(object)["memlane"].(object)
	if strings.Contains(upgraded, "bearer_token_file") || strings.Contains(upgraded, "http://127.0.0.1:9123/mcp") ||
		upgradedMemlane["command"] != "/bin/memlane" || !strings.Contains(upgraded, `"mcp"`) {
		t.Fatalf("legacy HTTP MCP entry did not upgrade cleanly to stdio:\n%s", upgraded)
	}
	if repeat, err := InstallHostConfig(opts); err != nil || len(repeat.Previews) != 0 {
		t.Fatalf("repeat after stdio upgrade not idempotent: %+v %v", repeat, err)
	}

	writeConfig(t, toml, upgraded+foreignConfig)
	if _, err := UninstallHostConfig(opts); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, toml); !strings.Contains(got, userConfig) || !strings.Contains(got, foreignConfig) || strings.Contains(got, marker) || strings.Contains(got, "bearer_token_file") || strings.Contains(got, "/bin/memlane") {
		t.Fatal(got)
	}
	if got := readConfig(t, hooksPath); !strings.Contains(got, "echo keep") || strings.Contains(got, marker) {
		t.Fatal(got)
	}
}

func TestInstallAntigravityMigration(t *testing.T) {
	t.Run("zero-byte mcp and legacy plugin dir disabled in config.json", func(t *testing.T) {
		dir := t.TempDir()
		mcpPath := filepath.Join(dir, "mcp_config.json")
		cfgPath := filepath.Join(dir, "config.json")
		hooksPath := filepath.Join(dir, "hooks.json")
		pluginDir := filepath.Join(dir, "plugins", "memlane-cognee")
		if err := os.MkdirAll(pluginDir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeConfig(t, filepath.Join(pluginDir, "plugin.json"), `{"name":"memlane-cognee"}`)
		writeConfig(t, mcpPath, "")
		const origCfg = "{\n  \"theme\": \"dark\",\n  \"plugins\": {\n    \"memlane-cognee\": {\"enabled\": true},\n    \"swarm\": {\"enabled\": true}\n  }\n}\n"
		writeConfig(t, cfgPath, origCfg)

		opts := InstallOptions{
			Host: HostAntigravity, ProfileDir: dir,
			BinaryPath: "/Applications/Memlane.app/Contents/MacOS/Memlane",
			GatewayURL: "http://127.0.0.1:18777", KeyFile: "/tmp/antigravity.token",
		}
		preview, err := InstallHostConfig(opts)
		if err != nil || preview.Blocked || preview.Applied || len(preview.Previews) != 3 {
			t.Fatalf("preview: %+v %v", preview, err)
		}
		if readConfig(t, mcpPath) != "" || readConfig(t, cfgPath) != origCfg {
			t.Fatal("preview mutated files")
		}

		opts.Apply = true
		res, err := InstallHostConfig(opts)
		if err != nil || !res.Applied || len(res.Previews) != 3 {
			t.Fatalf("apply: %+v %v", res, err)
		}
		// Idempotent reinstall produces zero previews.
		if repeat, err := InstallHostConfig(opts); err != nil || len(repeat.Previews) != 0 {
			t.Fatalf("repeat: %+v %v", repeat, err)
		}
		cfgDoc, err := agents.ParseJSONC([]byte(readConfig(t, cfgPath)))
		if err != nil {
			t.Fatal(err)
		}
		plugins := cfgDoc["plugins"].(object)
		if plugins["memlane-cognee"].(object)["enabled"] != false {
			t.Fatalf("legacy plugin not disabled: %+v", plugins)
		}
		if plugins["swarm"].(object)["enabled"] != true || cfgDoc["theme"] != "dark" {
			t.Fatalf("unrelated config altered: %+v", cfgDoc)
		}
		mcpDoc, err := agents.ParseJSONC([]byte(readConfig(t, mcpPath)))
		if err != nil {
			t.Fatal(err)
		}
		memlaneMCP, ok := mcpDoc["mcpServers"].(object)["memlane"].(object)
		if !ok || memlaneMCP["command"] != opts.BinaryPath || memlaneMCP["serverUrl"] != nil {
			t.Fatalf("mcp_config not populated with stdio bridge: %+v", mcpDoc)
		}
		hooksDoc, err := agents.ParseJSONC([]byte(readConfig(t, hooksPath)))
		if err != nil {
			t.Fatal(err)
		}
		if hooksDoc["memlane"] == nil || hooksDoc["hooks"] != nil {
			t.Fatalf("unexpected hooks.json: %+v", hooksDoc)
		}

		// Uninstall removes hooks & mcp entry but does NOT re-enable the legacy plugin.
		if _, err := UninstallHostConfig(opts); err != nil {
			t.Fatal(err)
		}
		afterCfg, err := agents.ParseJSONC([]byte(readConfig(t, cfgPath)))
		if err != nil {
			t.Fatal(err)
		}
		if afterCfg["plugins"].(object)["memlane-cognee"].(object)["enabled"] != false {
			t.Fatalf("uninstall re-enabled legacy plugin: %+v", afterCfg)
		}
	})

	t.Run("legacy plugin dir without config.json creates disabled entry", func(t *testing.T) {
		dir := t.TempDir()
		pluginDir := filepath.Join(dir, "plugins", "memlane-cognee")
		if err := os.MkdirAll(pluginDir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeConfig(t, filepath.Join(pluginDir, "plugin.json"), `{"name":"memlane-cognee"}`)
		opts := InstallOptions{
			Host: HostAntigravity, ProfileDir: dir,
			BinaryPath: "/bin/memlane", GatewayURL: "http://127.0.0.1:18777", KeyFile: "/tmp/k", Apply: true,
		}
		if _, err := InstallHostConfig(opts); err != nil {
			t.Fatal(err)
		}
		cfgDoc, err := agents.ParseJSONC([]byte(readConfig(t, filepath.Join(dir, "config.json"))))
		if err != nil || cfgDoc["plugins"].(object)["memlane-cognee"].(object)["enabled"] != false {
			t.Fatalf("expected disabled entry in created config.json: %+v %v", cfgDoc, err)
		}
		if repeat, err := InstallHostConfig(opts); err != nil || len(repeat.Previews) != 0 {
			t.Fatalf("repeat: %+v %v", repeat, err)
		}
	})

	t.Run("stale config.json entry without plugin dir is deleted", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")
		writeConfig(t, cfgPath, `{"plugins":{"memlane-cognee":{"enabled":true},"swarm":{"enabled":true}}}`)
		opts := InstallOptions{
			Host: HostAntigravity, ProfileDir: dir,
			BinaryPath: "/bin/memlane", GatewayURL: "http://127.0.0.1:18777", KeyFile: "/tmp/k", Apply: true,
		}
		if _, err := InstallHostConfig(opts); err != nil {
			t.Fatal(err)
		}
		cfgDoc, err := agents.ParseJSONC([]byte(readConfig(t, cfgPath)))
		if err != nil {
			t.Fatal(err)
		}
		plugins := cfgDoc["plugins"].(object)
		if _, exists := plugins["memlane-cognee"]; exists {
			t.Fatalf("stale memlane-cognee remained: %+v", plugins)
		}
		if plugins["swarm"].(object)["enabled"] != true {
			t.Fatalf("swarm plugin lost: %+v", plugins)
		}
	})

	t.Run("migrates stale wrapper hooks and preserves user named hooks", func(t *testing.T) {
		dir := t.TempDir()
		hooksPath := filepath.Join(dir, "hooks.json")
		writeConfig(t, hooksPath, `{
  "hooks": {
    "UserPromptSubmit": [{"_memlane_managed": "memlane-hook", "hooks": [{"type": "command", "command": "'/old/memlane' hook antigravity UserPromptSubmit", "_memlane_managed": "memlane-hook"}]}]
  },
  "memlane-cognee": {
    "PreInvocation": [{"type": "command", "command": "./scripts/run-agy-hook"}]
  },
  "lint-checker": {
    "PostToolUse": [{"matcher": "run_command", "hooks": [{"type": "command", "command": "./scripts/lint.sh"}]}]
  }
}`)
		opts := InstallOptions{
			Host: HostAntigravity, ProfileDir: dir,
			BinaryPath: "/bin/memlane", GatewayURL: "http://127.0.0.1:18777", KeyFile: "/tmp/k", Apply: true,
		}
		if _, err := InstallHostConfig(opts); err != nil {
			t.Fatal(err)
		}
		doc, err := agents.ParseJSONC([]byte(readConfig(t, hooksPath)))
		if err != nil {
			t.Fatal(err)
		}
		if doc["hooks"] != nil || doc["memlane-cognee"] != nil || doc["memlane"] == nil || doc["lint-checker"] == nil {
			t.Fatalf("unexpected migrated hooks.json: %+v", doc)
		}
		if _, err := UninstallHostConfig(opts); err != nil {
			t.Fatal(err)
		}
		afterDoc, err := agents.ParseJSONC([]byte(readConfig(t, hooksPath)))
		if err != nil {
			t.Fatal(err)
		}
		if afterDoc["memlane"] != nil || afterDoc["lint-checker"] == nil {
			t.Fatalf("unexpected hooks.json after uninstall: %+v", afterDoc)
		}
	})

	t.Run("refuses to overwrite unmanaged memlane hook", func(t *testing.T) {
		dir := t.TempDir()
		hooksPath := filepath.Join(dir, "hooks.json")
		const orig = `{"memlane":{"Stop":[{"type":"command","command":"echo user-owned"}]}}`
		writeConfig(t, hooksPath, orig)
		opts := InstallOptions{
			Host: HostAntigravity, ProfileDir: dir,
			BinaryPath: "/bin/memlane", GatewayURL: "http://127.0.0.1:18777", KeyFile: "/tmp/k", Apply: true,
		}
		if _, err := InstallHostConfig(opts); err == nil {
			t.Fatal("expected error when unmanaged memlane hook exists")
		}
		if readConfig(t, hooksPath) != orig {
			t.Fatal("unmanaged memlane hook was modified")
		}
		if _, err := UninstallHostConfig(opts); err != nil {
			t.Fatal(err)
		}
		if readConfig(t, hooksPath) != orig {
			t.Fatal("uninstall modified unmanaged memlane hook")
		}
	})
}

func TestInstallPreCompactPreservesUserHook(t *testing.T) {
	for _, host := range []string{HostCodex, HostTraeX} {
		t.Run(host, func(t *testing.T) {
			dir := t.TempDir()
			hooksPath := filepath.Join(dir, "hooks.json")
			writeConfig(t, hooksPath, `{"hooks":{"PreCompact":[{"hooks":[{"type":"command","command":"echo user-precompact"}]}]}}`)
			opts := InstallOptions{
				Host: host, ProfileDir: dir,
				BinaryPath: "/bin/memlane", GatewayURL: "http://127.0.0.1:18790", KeyFile: "/tmp/agent.key",
				Apply: true,
			}
			if _, err := InstallHostConfig(opts); err != nil {
				t.Fatal(err)
			}
			installed := readConfig(t, hooksPath)
			if !strings.Contains(installed, "echo user-precompact") || !strings.Contains(installed, "hook "+host+" "+PreCompact) {
				t.Fatalf("unexpected PreCompact hooks after install: %s", installed)
			}
			if _, err := UninstallHostConfig(opts); err != nil {
				t.Fatal(err)
			}
			uninstalled := readConfig(t, hooksPath)
			if !strings.Contains(uninstalled, "echo user-precompact") || strings.Contains(uninstalled, "hook "+host+" "+PreCompact) || strings.Contains(uninstalled, marker) {
				t.Fatalf("unexpected PreCompact hooks after uninstall: %s", uninstalled)
			}
		})
	}
}

func TestInstallTraeIDEMCPConfig(t *testing.T) {
	profileDir := t.TempDir()
	userDir := t.TempDir()
	mcpPath := filepath.Join(userDir, "mcp.json")
	const origMCP = "{\n  \"mcpServers\": {\n    \"LarkDocs\": {\n      \"command\": \"npx\",\n      \"args\": [\"-y\", \"lark-docs\"]\n    }\n  }\n}\n"
	writeConfig(t, mcpPath, origMCP)

	opts := InstallOptions{
		Host:          HostTraeIDE,
		ProfileDir:    profileDir,
		BinaryPath:    "/Applications/Memlane.app/Contents/MacOS/Memlane",
		GatewayURL:    "http://127.0.0.1:18777",
		KeyFile:       "/tmp/trae_ide.token",
		MCPConfigPath: mcpPath,
	}
	preview, err := InstallHostConfig(opts)
	if err != nil || preview.Blocked || preview.Applied || len(preview.Previews) != 2 {
		t.Fatalf("preview failed: %+v %v", preview, err)
	}
	if readConfig(t, mcpPath) != origMCP {
		t.Fatal("preview mutated mcp.json")
	}

	opts.Apply = true
	res, err := InstallHostConfig(opts)
	if err != nil || !res.Applied || len(res.Previews) != 2 {
		t.Fatalf("apply failed: %+v %v", res, err)
	}
	if repeat, err := InstallHostConfig(opts); err != nil || len(repeat.Previews) != 0 {
		t.Fatalf("repeat not idempotent: %+v %v", repeat, err)
	}
	doc, err := agents.ParseJSONC([]byte(readConfig(t, mcpPath)))
	if err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(object)
	if servers["LarkDocs"] == nil {
		t.Fatalf("unrelated LarkDocs server lost: %+v", servers)
	}
	memlane := servers["memlane"].(object)
	if memlane["command"] != opts.BinaryPath || memlane["_memlane_managed"] != marker {
		t.Fatalf("unexpected trae_ide mcp entry: %+v", memlane)
	}

	// Third-party cognee MCP entry in TRAE CN User/mcp.json blocks without mutating files.
	const conflictMCP = `{"mcpServers":{"cognee":{"command":"external-cognee"}}}`
	writeConfig(t, mcpPath, conflictMCP)
	blocked, err := InstallHostConfig(opts)
	if !errors.Is(err, ErrConflictBlocked) || !blocked.Blocked || blocked.Applied {
		t.Fatalf("expected ErrConflictBlocked, got %+v %v", blocked, err)
	}
	if readConfig(t, mcpPath) != conflictMCP {
		t.Fatal("blocked install mutated mcp.json")
	}

	// Uninstall removes only managed memlane entry while keeping LarkDocs.
	writeConfig(t, mcpPath, origMCP)
	if _, err := InstallHostConfig(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallHostConfig(opts); err != nil {
		t.Fatal(err)
	}
	afterUninstall := readConfig(t, mcpPath)
	if !strings.Contains(afterUninstall, "LarkDocs") || strings.Contains(afterUninstall, marker) || strings.Contains(afterUninstall, `"memlane"`) {
		t.Fatalf("unexpected mcp.json after uninstall: %s", afterUninstall)
	}
}
