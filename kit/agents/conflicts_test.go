package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisabledThirdPartyPluginsDoNotBlock(t *testing.T) {
	for _, test := range []struct {
		name, kind, config, template, active, inactive string
	}{
		{"codex-disabled", HostCodex, "config.toml", "[plugins.\"cognee@official\"]\n%s", "enabled = true\n", "enabled = false\n"},
		{"codex-missing-enabled", HostCodex, "config.toml", "[plugins.\"cognee@official\"]\n%s", "enabled = true\n", ""},
		{"traex-disabled", HostTraeX, "traecli.toml", "[plugins.\"cognee@official\"]\n%s", "enabled = true\n", "enabled = false\n"},
		{"claude-plugin", HostClaudeCode, "settings.json", `{"enabledPlugins":{"cognee@official":%s}}`, "true", "false"},
		{"antigravity-plugin", HostAntigravity, "config.json", `{"plugins":{"cognee":{"enabled":%s}}}`, "true", "false"},
		{"disabled-mcp", HostAntigravity, "mcp_config.json", `{"mcpServers":{"cognee":{"disabled":%s}}}`, "false", "true"},
		{"disabled-hook", HostClaudeCode, "settings.json", `{"hooks":{"PreToolUse":[{"hooks":[{"command":"cognee","disabled":%s}]}]}}`, "false", "true"},
		{"disabled-hook-group", HostClaudeCode, "hooks.json", `{"PreToolUse":[{"disabled":%s,"hooks":[{"command":"cognee"}]}]}`, "false", "true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := DetectedHost{Kind: test.kind, ProfileDir: t.TempDir()}
			path := filepath.Join(host.ProfileDir, test.config)
			policy := ConflictPolicy{ConflictKeywords: []string{"cognee"}}

			putFile(t, path, fmt.Sprintf(test.template, test.active), 0o600)
			report, err := ScanHostConflicts(host, policy)
			if err != nil || !report.Blocked || len(report.ThirdPartyConflicts) != 1 {
				t.Fatalf("active entry: report %+v, error %v", report, err)
			}

			putFile(t, path, fmt.Sprintf(test.template, test.inactive), 0o600)
			report, err = ScanHostConflicts(host, policy)
			if err != nil || report.Blocked || len(report.ThirdPartyConflicts) != 0 {
				t.Fatalf("disabled entry: report %+v, error %v", report, err)
			}
		})
	}
}

func TestDisableThirdPartyConflictsRejectsStaleConfig(t *testing.T) {
	for _, test := range []struct {
		name, kind, config, original, edited string
	}{
		{"toml", HostCodex, "config.toml",
			"[plugins.\"cognee@official\"]\nenabled = true\n",
			"# keep this unrelated edit\n[plugins.\"cognee@official\"]\nenabled = true\n"},
		{"json", HostClaudeCode, "settings.json",
			`{"enabledPlugins":{"cognee@official":true}}`,
			`{"enabledPlugins":{"cognee@official":true},"unrelated":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, unstamped := range []bool{false, true} {
				name := "edited-config"
				if unstamped {
					name = "unstamped-report"
				}
				t.Run(name, func(t *testing.T) {
					host := DetectedHost{Kind: test.kind, ProfileDir: t.TempDir()}
					path := filepath.Join(host.ProfileDir, test.config)
					policy := ConflictPolicy{ConflictKeywords: []string{"cognee"}, BackupDir: t.TempDir()}
					putFile(t, path, test.original, 0o600)
					report, err := ScanHostConflicts(host, policy)
					if err != nil || len(report.ThirdPartyConflicts) != 1 {
						t.Fatalf("initial report: %+v, error %v", report, err)
					}

					expected := test.original
					if unstamped {
						report.ThirdPartyConflicts[0].configDigest = ""
					} else {
						expected = test.edited
						putFile(t, path, expected, 0o600)
					}

					_, err = DisableThirdPartyConflicts(report, policy, true)
					if err == nil || !strings.Contains(err.Error(), "conflict report is stale") {
						t.Errorf("expected a stale report error, got %v", err)
					}
					actual, err := os.ReadFile(path)
					if err != nil || string(actual) != expected {
						t.Fatalf("expected file to stay %q, got %q, error %v", expected, actual, err)
					}
				})
			}
		})
	}
}

func TestConflictScanAndDisable(t *testing.T) {
	for _, test := range []struct{ kind, config, key string }{
		{HostCodex, "config.toml", "mcp_servers"}, {HostTraeX, "traecli.toml", "plugins"},
		{HostTraeIDE, "mcp_config.json", "mcpServers"}, {HostAntigravity, "config.json", "plugins"},
		{HostPi, "settings.json", "packages"}, {HostOpenCode, "opencode.json", "plugin"},
		{HostClaudeCode, "settings.json", "enabledPlugins"}, {HostCursor, "mcp.json", "mcpServers"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, test.config)
			var data string
			if strings.HasSuffix(path, ".toml") {
				data = "# retain\n[" + test.key + ".\"managed-memory\"]\nenabled = true\n[" + test.key + ".\"other-memory\"]\nenabled = true\n[" + test.key + ".\"third-party\"]\nenabled = true # untouched\n"
			} else {
				data = "// retain\n{\"" + test.key + "\": {\"managed-memory\": true, \"other-memory\": true, \"third-party\": true /* untouched */}}"
			}
			putFile(t, path, data, 0o600)
			policy := ConflictPolicy{ManagedMarkers: []string{"MANAGED-"}, ConflictKeywords: []string{"MEMORY"}, BackupDir: filepath.Join(t.TempDir(), "backups")}
			host := DetectedHost{Kind: test.kind, ProfileDir: home}
			report, err := ScanHostConflicts(host, policy)
			if err != nil || !report.Blocked || report.UnrelatedCount != 1 || len(report.ManagedLegacy) != 1 || len(report.ThirdPartyConflicts) != 1 {
				t.Fatalf("report: %+v %v", report, err)
			}
			finding := report.ThirdPartyConflicts[0]
			if finding.EntryName != "other-memory" || finding.ConfigPath != path || finding.Action != ActionBlock || !finding.Blocked ||
				report.ManagedLegacy[0].Action != ActionMigrate || report.ManagedLegacy[0].Blocked {
				t.Fatalf("findings: %+v", report)
			}
			if _, err = DisableThirdPartyConflicts(report, policy, false); err == nil {
				t.Fatal("confirmation bypassed")
			}
			noBackup := policy
			noBackup.BackupDir = ""
			if _, err = DisableThirdPartyConflicts(report, noBackup, true); err == nil {
				t.Fatal("backup requirement bypassed")
			}
			records, err := DisableThirdPartyConflicts(report, policy, true)
			if err != nil || len(records) != 1 {
				t.Fatalf("%+v %v", records, err)
			}
			backup, err := os.ReadFile(records[0].BackupPath)
			if err != nil || string(backup) != data {
				t.Fatalf("backup: %q %v", backup, err)
			}
			edited, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(edited), "other-memory") {
				t.Fatalf("edited: %q %v", edited, err)
			}
			for _, keep := range []string{"managed-memory", "third-party", "untouched", "retain"} {
				if !strings.Contains(string(edited), keep) {
					t.Errorf("lost %s", keep)
				}
			}
			report, err = ScanHostConflicts(host, policy)
			if err != nil || report.Blocked || report.UnrelatedCount != 1 || len(report.ManagedLegacy) != 1 {
				t.Fatalf("after: %+v %v", report, err)
			}
		})
	}
}

func TestConflictDirectoryMoved(t *testing.T) {
	home := t.TempDir()
	bad := filepath.Join(home, "extensions", "third-memory")
	good := filepath.Join(home, "extensions", "other")
	putFile(t, filepath.Join(bad, "index.ts"), "original", 0o600)
	putFile(t, filepath.Join(good, "index.ts"), "keep", 0o600)
	policy := ConflictPolicy{ConflictKeywords: []string{"memory"}, BackupDir: t.TempDir()}
	report, err := ScanHostConflicts(DetectedHost{Kind: HostPi, ProfileDir: home}, policy)
	if err != nil || len(report.ThirdPartyConflicts) != 1 || report.UnrelatedCount != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	records, err := DisableThirdPartyConflicts(report, policy, true)
	if err != nil || len(records) != 1 {
		t.Fatalf("%+v %v", records, err)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
	if actual, err := os.ReadFile(filepath.Join(records[0].BackupPath, "index.ts")); err != nil || string(actual) != "original" {
		t.Fatalf("backup: %s %v", actual, err)
	}
	if _, err := os.Stat(good); err != nil {
		t.Fatal(err)
	}
}

func TestConflictArraysPreserveNeighbors(t *testing.T) {
	for _, scenario := range []struct{ kind, file, input, keep string }{
		{HostOpenCode, "opencode.jsonc",
			"{\"plugin\":[\"first\", // keep leading\n \"other-memory\", // keep context\n \"last\" /* keep following */]}",
			"\"last\" /* keep following */"},
		{HostCodex, "config.toml",
			"plugins = [\n {name='first'}, # keep leading\n {name='other-memory'}, # keep context\n {name='last'} # keep following\n]\n",
			"{name='last'} # keep following"},
		{HostCodex, "config.toml",
			"[[plugins]] # keep leading\nname = 'first'\n[[plugins]] # keep context\nname = 'other-memory'\n[[plugins]] # keep following\nname = 'last'\n",
			"[[plugins]] # keep following\nname = 'last'"},
	} {
		t.Run(scenario.kind+"/"+scenario.file, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, scenario.file)
			putFile(t, path, scenario.input, 0o600)
			host := DetectedHost{Kind: scenario.kind, ProfileDir: home}
			policy := ConflictPolicy{ConflictKeywords: []string{"memory"}, BackupDir: t.TempDir()}
			report, err := ScanHostConflicts(host, policy)
			if err != nil || len(report.ThirdPartyConflicts) != 1 || report.UnrelatedCount != 2 {
				t.Fatalf("%+v %v", report, err)
			}
			backups, err := DisableThirdPartyConflicts(report, policy, true)
			if err != nil || len(backups) != 1 {
				t.Fatalf("%+v %v", backups, err)
			}
			data, err := os.ReadFile(path)
			actual := string(data)
			if err != nil || strings.Contains(actual, "other-memory") || !strings.Contains(actual, "keep leading") ||
				!strings.Contains(actual, "keep context") || !strings.Contains(actual, scenario.keep) {
				t.Fatalf("%s %v", data, err)
			}
		})
	}
}

func TestAntigravityEmptyConfigsNamedHooksAndDisabledPlugins(t *testing.T) {
	t.Run("empty json tolerated and malformed rejected", func(t *testing.T) {
		home := t.TempDir()
		putFile(t, filepath.Join(home, "mcp_config.json"), "", 0o600)
		putFile(t, filepath.Join(home, "settings.json"), "  \n\t ", 0o600)
		host := DetectedHost{Kind: HostAntigravity, ProfileDir: home}
		policy := ConflictPolicy{ManagedMarkers: []string{"memlane"}, ConflictKeywords: []string{"cognee"}}

		report, err := ScanHostConflicts(host, policy)
		if err != nil || report.Blocked || len(report.ManagedLegacy) != 0 || len(report.ThirdPartyConflicts) != 0 {
			t.Fatalf("empty configs: %+v, %v", report, err)
		}

		putFile(t, filepath.Join(home, "mcp_config.json"), "{malformed", 0o600)
		if _, err := ScanHostConflicts(host, policy); err == nil {
			t.Fatal("malformed mcp_config.json unexpectedly succeeded")
		}
	})

	t.Run("named hooks scan and surgical disable", func(t *testing.T) {
		home := t.TempDir()
		hooksPath := filepath.Join(home, "hooks.json")
		putFile(t, hooksPath, `{
  "memlane": {
    "SessionStart": [{"type": "command", "command": "'/bin/memlane' hook antigravity SessionStart --gateway-url 'http://127.0.0.1:18777' --key-file '/tmp/k'"}],
    "PreInvocation": [{"type": "command", "command": "'/bin/memlane' hook antigravity PreInvocation --gateway-url 'http://127.0.0.1:18777' --key-file '/tmp/k'"}],
    "PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "'/bin/memlane' hook antigravity PostToolUse --gateway-url 'http://127.0.0.1:18777' --key-file '/tmp/k'"}]}],
    "Stop": [{"type": "command", "command": "'/bin/memlane' hook antigravity Stop --gateway-url 'http://127.0.0.1:18777' --key-file '/tmp/k'"}]
  },
  "lint-checker": {
    "PostToolUse": [{"matcher": "run_command", "hooks": [{"type": "command", "command": "./scripts/lint.sh"}]}]
  },
  "disabled-cognee": {
    "enabled": false,
    "PreInvocation": [{"type": "command", "command": "cognee-hook"}]
  },
  "third-party-cognee": {
    "PreInvocation": [{"type": "command", "command": "./run.sh"}]
  },
  "mixed-tools": {
    "Stop": [{"type": "command", "command": "echo keep"}],
    "PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "cognee-audit"}]}]
  }
}`, 0o600)
		host := DetectedHost{Kind: HostAntigravity, ProfileDir: home}
		policy := ConflictPolicy{
			ManagedMarkers:   []string{"memlane-cognee", "memlane hook", "memlane"},
			ConflictKeywords: []string{"cognee"},
			BackupDir:        t.TempDir(),
		}
		report, err := ScanHostConflicts(host, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !report.Blocked || len(report.ManagedLegacy) != 1 || report.ManagedLegacy[0].EntryName != "memlane" || len(report.ThirdPartyConflicts) != 2 {
			t.Fatalf("unexpected report: %+v", report)
		}
		records, err := DisableThirdPartyConflicts(report, policy, true)
		if err != nil || len(records) != 2 {
			t.Fatalf("disable: %+v %v", records, err)
		}
		edited, err := os.ReadFile(hooksPath)
		if err != nil {
			t.Fatal(err)
		}
		got := string(edited)
		for _, want := range []string{`"memlane"`, `"lint-checker"`, `./scripts/lint.sh`, `"disabled-cognee"`, `echo keep`} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %s in:\n%s", want, got)
			}
		}
		for _, gone := range []string{`"third-party-cognee"`, `cognee-audit`} {
			if strings.Contains(got, gone) {
				t.Errorf("conflict %s remained in:\n%s", gone, got)
			}
		}
		after, err := ScanHostConflicts(host, policy)
		if err != nil || after.Blocked || len(after.ThirdPartyConflicts) != 0 || len(after.ManagedLegacy) != 1 {
			t.Fatalf("after disable: %+v %v", after, err)
		}
	})

	t.Run("disabled plugin directory does not block", func(t *testing.T) {
		home := t.TempDir()
		putFile(t, filepath.Join(home, "plugins", "cognee", "plugin.json"), `{"name":"cognee"}`, 0o600)
		putFile(t, filepath.Join(home, "plugins", "memlane-cognee", "plugin.json"), `{"name":"memlane-cognee"}`, 0o600)
		host := DetectedHost{Kind: HostAntigravity, ProfileDir: home}
		policy := ConflictPolicy{ManagedMarkers: []string{"memlane-cognee", "memlane"}, ConflictKeywords: []string{"cognee"}}

		// Active third-party plugins/cognee blocks by default (auto-discovered).
		report, err := ScanHostConflicts(host, policy)
		if err != nil || !report.Blocked || len(report.ThirdPartyConflicts) != 1 {
			t.Fatalf("active third-party dir: %+v %v", report, err)
		}

		// Disabling both in config.json prevents third-party blocking even when directories remain on disk.
		putFile(t, filepath.Join(home, "config.json"), `{"plugins":{"cognee":{"enabled":false},"memlane-cognee":{"enabled":false},"swarm":{"enabled":true}}}`, 0o600)
		report, err = ScanHostConflicts(host, policy)
		if err != nil || report.Blocked || len(report.ThirdPartyConflicts) != 0 {
			t.Fatalf("disabled dirs with managed policy: %+v %v", report, err)
		}
		// Even without ManagedMarkers, disabled memlane-cognee must not be a third-party conflict.
		noManaged := ConflictPolicy{ConflictKeywords: []string{"cognee"}}
		report, err = ScanHostConflicts(host, noManaged)
		if err != nil || report.Blocked || len(report.ThirdPartyConflicts) != 0 {
			t.Fatalf("disabled dirs without managed policy: %+v %v", report, err)
		}
	})
}

func TestTraeXCLIHooksConflictScanAndDisable(t *testing.T) {
	home := t.TempDir()
	cliHooksPath := filepath.Join(home, "cli", "hooks.json")
	legacyHooksPath := filepath.Join(home, "hooks.json")
	putFile(t, cliHooksPath, `{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "/opt/flux/flux-hook session-start"}]}],
    "Stop": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "/opt/axis/axis-chat-log-hook"}, {"type": "command", "command": "third-party-cognee-hook Stop"}]}
    ]
  }
}`, 0o600)
	putFile(t, legacyHooksPath, `{
  "hooks": {
    "Stop": [{"_memlane_managed": "memlane-hook", "hooks": [{"type": "command", "command": "'/bin/memlane' hook traex Stop --gateway-url 'http://127.0.0.1:18790' --key-file '/tmp/k'", "_memlane_managed": "memlane-hook"}]}]
  }
}`, 0o600)

	host := DetectedHost{Kind: HostTraeX, ProfileDir: home}
	policy := ConflictPolicy{
		ManagedMarkers:   []string{"memlane-cognee", "memlane hook", "memlane"},
		ConflictKeywords: []string{"cognee"},
		BackupDir:        t.TempDir(),
	}
	report, err := ScanHostConflicts(host, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Blocked || len(report.ThirdPartyConflicts) != 1 || report.ThirdPartyConflicts[0].ConfigPath != cliHooksPath ||
		len(report.ManagedLegacy) != 1 || report.ManagedLegacy[0].ConfigPath != legacyHooksPath || report.UnrelatedCount != 2 {
		t.Fatalf("unexpected traex conflict report: %+v", report)
	}

	records, err := DisableThirdPartyConflicts(report, policy, true)
	if err != nil || len(records) != 1 || records[0].OriginalPath != cliHooksPath {
		t.Fatalf("disable traex cli/hooks.json conflict: %+v %v", records, err)
	}
	edited, err := os.ReadFile(cliHooksPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(edited)
	if strings.Contains(got, "third-party-cognee-hook") || !strings.Contains(got, "/opt/flux/flux-hook session-start") || !strings.Contains(got, "/opt/axis/axis-chat-log-hook") {
		t.Fatalf("unexpected cli/hooks.json after disabling conflict:\n%s", got)
	}
	after, err := ScanHostConflicts(host, policy)
	if err != nil || after.Blocked || len(after.ThirdPartyConflicts) != 0 || len(after.ManagedLegacy) != 1 {
		t.Fatalf("unexpected traex report after disable: %+v %v", after, err)
	}
}
