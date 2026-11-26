package settings

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

var (
	defaultLoadAgentVersionsFn func(context.Context, Config, []AgentConnection)
	settingsWarmScript         string
	settingsWarmScriptRaw      []byte
)

func TestMain(m *testing.M) {
	defaultLoadAgentVersionsFn = loadAgentVersionsFn
	loadAgentVersionsFn = func(context.Context, Config, []AgentConnection) {}

	var warmDir string
	if dir, err := os.MkdirTemp("", "memlane-settings-warm-"); err == nil {
		warmDir = dir
		p := filepath.Join(dir, "warm-dispatcher.sh")
		body := []byte("#!/bin/sh\nif [ -f \"$0.body.sh\" ]; then\n  . \"$0.body.sh\"\nfi\n")
		if os.WriteFile(p, body, 0500) == nil {
			_ = exec.Command(p).Run()
			settingsWarmScript = p
			settingsWarmScriptRaw = body
		}
	}
	code := m.Run()
	if warmDir != "" {
		_ = os.RemoveAll(warmDir)
	}
	os.Exit(code)
}

func writeWarmDesktopScript(t *testing.T, target, body string) []byte {
	t.Helper()
	if settingsWarmScript != "" {
		if err := os.WriteFile(target+".body.sh", []byte(body+"\n"), 0600); err == nil {
			if err := os.Link(settingsWarmScript, target); err == nil {
				return settingsWarmScriptRaw
			}
			_ = os.Remove(target + ".body.sh")
		}
	}
	raw := []byte("#!/bin/sh\n" + body + "\n")
	if err := os.WriteFile(target, raw, 0700); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAgentVersionsUseConfiguredHostAndDoNotRunUndetectedHost(t *testing.T) {
	t.Parallel()
	if reflect.ValueOf(defaultLoadAgentVersionsFn).Pointer() != reflect.ValueOf(loadAgentVersions).Pointer() {
		t.Fatal("production default loadAgentVersionsFn must be loadAgentVersions")
	}
	path := filepath.Join(t.TempDir(), "selected host")
	writeWarmDesktopScript(t, path, `[ "$1" = --version ] || exit 7`+"\n"+`printf 'pi 0.84.1\n'`)
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
	// ui.NewTester shares MyGo's global scene.Atlas; keep UI raster tests sequential.
	a := newTestHost(Config{}, nil)
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
	writeWarmDesktopScript(t, agy, `[ "$1" = --version ] || exit 7`+"\n"+`printf '1.5.13\n'`)
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
			writeWarmDesktopScript(t, binary, `[ "$1" = --version ] || exit 7`+"\n"+`printf 'traecli 0.216.0\n'`)
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
		writeWarmDesktopScript(t, path, "[ \"$1\" = --version ] || exit 7\nprintf 'traecli "+version+"\\n'")
	}
	rows := []AgentConnection{{Kind: "traex", Detected: true}}
	loadAgentVersions(context.Background(), Config{}, rows)
	if got := rows[0].HostVersion; got != "0.216.0" {
		t.Fatalf("expected PATH alias version 0.216.0, got %q", got)
	}
}

func TestTraeVersionReadsBundleWithoutLaunchingExecutable(t *testing.T) {
	t.Parallel()
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

func TestLoadAgentVersionsPreservesServerHostVersion(t *testing.T) {
	dir := t.TempDir()
	agy := filepath.Join(dir, "agy")
	writeWarmDesktopScript(t, agy, `[ "$1" = --version ] || exit 7`+"\n"+`printf '1.5.18\n'`)
	t.Setenv("PATH", dir)
	rows := []AgentConnection{{Kind: "antigravity", Detected: true, HostVersion: "1.150.1.1579"}}
	loadAgentVersions(context.Background(), Config{}, rows)
	if rows[0].HostVersion != "1.150.1.1579" {
		t.Fatalf("expected server HostVersion 1.150.1.1579 to be preserved, got %q", rows[0].HostVersion)
	}
}
