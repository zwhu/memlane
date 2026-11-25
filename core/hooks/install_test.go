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
			if hooks := data["hooks"].(object); len(hooks) < 4 || !strings.Contains(raw, "hook "+host+" Stop") {
				t.Fatal(raw)
			}
			if _, err := UninstallHostConfig(opts); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readConfig(t, filepath.Join(opts.ProfileDir, "hooks.json")), marker) {
				t.Fatal("hook remains after uninstall")
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
	if !strings.Contains(installed, userConfig) || strings.Contains(installed, "legacy helper") || !strings.Contains(installed, "/mcp") {
		t.Fatal(installed)
	}
	if got := readConfig(t, hooksPath); !strings.Contains(got, "echo keep") || strings.Contains(got, "legacy-hook") {
		t.Fatal(got)
	}

	writeConfig(t, toml, installed+foreignConfig)
	if _, err := UninstallHostConfig(opts); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, toml); !strings.Contains(got, userConfig) || !strings.Contains(got, foreignConfig) || strings.Contains(got, marker) || strings.Contains(got, "bearer_token_file") {
		t.Fatal(got)
	}
	if got := readConfig(t, hooksPath); !strings.Contains(got, "echo keep") || strings.Contains(got, marker) {
		t.Fatal(got)
	}
}
