package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func piCaptureInstallFixture(t *testing.T, version string) (records.Connection, InstallOptions, string) {
	t.Helper()
	root := t.TempDir()
	program := filepath.Join(root, "synthetic-pi")
	source := "#!/bin/sh\nif test \"$1\" = --help; then echo --extension; else echo " + version + "; fi\n"
	if err := os.WriteFile(program, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "pi")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"extensions":["/synthetic/unrelated.ts"],"defaultModel":"unrelated-model","custom":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(profile, "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "auth.json"), []byte("synthetic-credential-must-stay"), 0600); err != nil {
		t.Fatal(err)
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "pi", Generation: 1, Enabled: true}
	return c, InstallOptions{ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, PiDirectory: profile}, string(settings)
}

func TestPiCaptureInstallOwnsCompanionsAndReusesSingleSettingsEntry(t *testing.T) {
	c, o, _ := piCaptureInstallFixture(t, "0.84.1")
	i, err := Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if i.PiCaptureVersion != 1 || !i.PiCaptureSupported || len(i.PiCaptureFiles) != 3 {
		t.Fatal("capture module receipt missing", i.PiCaptureVersion, i.PiCaptureSupported, i.PiCaptureFiles)
	}
	if err := CheckInstallation(i); err != nil {
		t.Fatal(err)
	}
	for _, name := range piCaptureModuleNames {
		source, err := os.ReadFile(filepath.Join(filepath.Dir(i.ExtensionPath), name))
		if err != nil || hash(source) != i.PiCaptureFiles[name] {
			t.Fatal("module digest not frozen", name, err)
		}
	}
	current, _ := os.ReadFile(i.ConfigPath)
	var settings map[string]json.RawMessage
	json.Unmarshal(current, &settings)
	var paths []string
	json.Unmarshal(settings["extensions"], &paths)
	if len(paths) != 2 || paths[0] != "/synthetic/unrelated.ts" || paths[1] != i.ExtensionPath {
		t.Fatal("duplicate or unrelated extension changed", paths)
	}
	var custom map[string]bool
	if json.Unmarshal(settings["custom"], &custom) != nil || len(custom) != 1 || !custom["keep"] || string(settings["defaultModel"]) != `"unrelated-model"` {
		t.Fatal("model or custom settings changed")
	}
	auth, _ := os.ReadFile(filepath.Join(o.PiDirectory, "auth.json"))
	if string(auth) != "synthetic-credential-must-stay" {
		t.Fatal("credentials changed")
	}
	c.Generation = 2
	next, err := Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if next.ExtensionPath != i.ExtensionPath || next.Connection.Generation != 2 {
		t.Fatal("reconnect changed identity")
	}
	if err := CheckInstallation(next); err != nil {
		t.Fatal(err)
	}
	if err := UninstallRevision(context.Background(), o.ClientRoot, c.ID, 2); err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"memlane.ts"}, piCaptureModuleNames...) {
		if _, err := os.Stat(filepath.Join(filepath.Dir(i.ExtensionPath), name)); !os.IsNotExist(err) {
			t.Fatal("owned unchanged module retained", name, err)
		}
	}
	current, _ = os.ReadFile(i.ConfigPath)
	json.Unmarshal(current, &settings)
	json.Unmarshal(settings["extensions"], &paths)
	if len(paths) != 1 || paths[0] != "/synthetic/unrelated.ts" {
		t.Fatal("unrelated extension lost", paths)
	}
}

func TestPiCaptureInstallPreservesExternallyModifiedModuleFamily(t *testing.T) {
	c, o, _ := piCaptureInstallFixture(t, "0.84.1")
	i, err := Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(i.ExtensionPath), piCaptureModuleNames[0])
	modified := []byte("// external synthetic change; preserve dependencies\n")
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(i.ConfigPath)
	if CheckInstallation(i) == nil {
		t.Fatal("modified companion accepted as intact")
	}
	c.Generation = 2
	if _, err := Install(context.Background(), c, o); err == nil {
		t.Fatal("external module overwritten")
	}
	after, _ := os.ReadFile(i.ConfigPath)
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || !bytes.Equal(actual, modified) {
		t.Fatal("failed update changed external content")
	}
	if err := UninstallRevision(context.Background(), o.ClientRoot, c.ID, 1); err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"memlane.ts"}, piCaptureModuleNames...) {
		if _, err := os.Stat(filepath.Join(filepath.Dir(i.ExtensionPath), name)); err != nil {
			t.Fatal("modified family's dependencies deleted", name, err)
		}
	}
}

func TestPiCaptureInstallRefusesUnownedCompanionBeforeSettingsWrite(t *testing.T) {
	c, o, baseline := piCaptureInstallFixture(t, "0.84.1")
	path := filepath.Join(o.ClientRoot, "agent-adapters", c.ID, piCaptureModuleNames[1])
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external-unowned-module"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), c, o); err == nil {
		t.Fatal("unowned module adopted")
	}
	after, _ := os.ReadFile(filepath.Join(o.PiDirectory, "settings.json"))
	if string(after) != baseline {
		t.Fatal("failed install changed settings")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "memlane.ts")); !os.IsNotExist(err) {
		t.Fatal("failed install left entrypoint", err)
	}
}

func TestPiCaptureInstallLegacyUpgradeAndUnsupportedHostBoundary(t *testing.T) {
	for _, version := range []string{"0.84.1", "0.84.0"} {
		t.Run(version, func(t *testing.T) {
			c, o, _ := piCaptureInstallFixture(t, version)
			i, err := Install(context.Background(), c, o)
			if err != nil {
				t.Fatal(err)
			}
			if i.PiCaptureSupported != (version == "0.84.1") {
				t.Fatal("untested host version started capture", i.PiCaptureSupported)
			}
			legacy := []byte("// synthetic older owned pi extension\nexport default function() {}\n")
			if err := os.WriteFile(i.ExtensionPath, legacy, 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range piCaptureModuleNames {
				if err := os.Remove(filepath.Join(filepath.Dir(i.ExtensionPath), name)); err != nil {
					t.Fatal(err)
				}
			}
			i.PiCaptureFiles = nil
			i.PiCaptureVersion = 0
			i.PiCaptureSupported = false
			i.ExtensionHash = hash(legacy)
			encoded, _ := json.Marshal(i)
			if err := privateWrite(installationPath(o.ClientRoot, c.ID), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			c.Generation = 2
			updated, err := Install(context.Background(), c, o)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckInstallation(updated); err != nil {
				t.Fatal(err)
			}
			source, _ := os.ReadFile(updated.ExtensionPath)
			if !strings.Contains(string(source), `"capture_supported":`) {
				t.Fatal("host capability was not explicit")
			}
		})
	}
}
