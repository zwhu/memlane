package simpledesktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAgentVersionsUseConfiguredHostAndDoNotRunUndetectedHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected host")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 7\nprintf 'pi 0.84.1\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var config Config
	b, _ := json.Marshal(map[string]any{"agent_hosts": map[string]any{"pi": map[string]string{"executable": path}}})
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	rows := []AgentConnection{{Kind: "pi", Detected: true}, {Kind: "pi", Detected: false}}
	loadAgentVersions(context.Background(), config, rows)
	if rows[0].HostVersion != "0.84.1" || rows[1].HostVersion != "" {
		t.Fatalf("wrong host versions: %+v", rows)
	}
}

func TestAgentVersionSurvivesCaptureStatusRefresh(t *testing.T) {
	a := New(Config{}, nil)
	a.integrations.HostVersions = map[string]string{"pi": "0.84.1"}
	row := AgentConnection{ID: "pi", Kind: "pi", Detected: true, Status: "configured_pending_new_session"}
	tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1000, 120)
	if !tt.HasText("0.84.1") {
		t.Fatal("capture refresh erased detected host version")
	}
}

func TestAntigravityVersionDetectionUsesAgyBinary(t *testing.T) {
	dir := t.TempDir()
	agy := filepath.Join(dir, "agy")
	if err := os.WriteFile(agy, []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 7\nprintf '1.5.13\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	rows := []AgentConnection{{Kind: "antigravity", Detected: true}}
	loadAgentVersions(context.Background(), Config{}, rows)
	if rows[0].HostVersion != "1.5.13" {
		t.Fatalf("expected agy version 1.5.13, got %q", rows[0].HostVersion)
	}
}

func TestTraexVersionResolvesExecutableAliases(t *testing.T) {
	for _, name := range []string{"traecli", "traex", "trae-cli"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			bunBin := filepath.Join(home, ".bun", "bin")
			if err := os.MkdirAll(bunBin, 0700); err != nil {
				t.Fatal(err)
			}
			pathBin := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", pathBin)
			bin := pathBin
			if name == "trae-cli" {
				bin = bunBin
			}
			binary := filepath.Join(bin, name)
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 7\nprintf 'traecli 0.216.0\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			rows := []AgentConnection{{Kind: "traex", Detected: true}}
			loadAgentVersions(context.Background(), Config{}, rows)
			if rows[0].HostVersion != "0.216.0" {
				t.Fatalf("expected %s version 0.216.0, got %q", name, rows[0].HostVersion)
			}
		})
	}
}

func TestTraexVersionPrefersPATHAcrossAliases(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0700); err != nil {
		t.Fatal(err)
	}
	pathBin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", pathBin)
	for path, version := range map[string]string{
		filepath.Join(localBin, "traecli"): "0.100.0",
		filepath.Join(pathBin, "traex"):    "0.216.0",
	} {
		script := "#!/bin/sh\n[ \"$1\" = --version ] || exit 7\nprintf 'traecli " + version + "\\n'\n"
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rows := []AgentConnection{{Kind: "traex", Detected: true}}
	loadAgentVersions(context.Background(), Config{}, rows)
	if got := rows[0].HostVersion; got != "0.216.0" {
		t.Fatalf("expected PATH alias version 0.216.0, got %q", got)
	}
}

func TestTraeVersionReadsBundleWithoutLaunchingExecutable(t *testing.T) {
	root := t.TempDir()
	contents := filepath.Join(root, "Trae CN.app", "Contents")
	binary := filepath.Join(contents, "MacOS", "Trae CN")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch \"$0.was-launched\"\nprintf '9.9.9'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(`<?xml version="1.0"?>
<plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>2.1.23</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	var config Config
	b, _ := json.Marshal(map[string]any{"agent_hosts": map[string]any{"trae_ide": map[string]string{"executable": binary}}})
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	rows := []AgentConnection{{Kind: "trae_ide", Detected: true}}
	loadAgentVersions(context.Background(), config, rows)
	if rows[0].HostVersion != "2.1.23" {
		t.Fatalf("unexpected bundle version %q", rows[0].HostVersion)
	}
	if _, err := os.Stat(binary + ".was-launched"); !os.IsNotExist(err) {
		t.Fatalf("TRAE executable was launched: %v", err)
	}
}
