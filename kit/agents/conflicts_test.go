package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
