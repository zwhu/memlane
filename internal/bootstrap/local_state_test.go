package bootstrap

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualPathsSurviveRestartWithoutGrantingInstallConsent(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "components")
	options := EnvironmentOptions{Root: root, Python: "/existing/venv/bin/python", UV: "/existing/uv", Source: "/existing/source", Archive: "/existing/archive", ModelCache: "/existing/model"}
	if _, err := LoadSelections(root, "/daily/runtime.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reading selections created state")
	}
	if err := SaveSelections(options, "/daily/runtime.json"); err != nil {
		t.Fatal(err)
	}
	reopened, err := LoadSelections(root, "/daily/runtime.json")
	if err != nil || reopened.Python != options.Python || reopened.ModelCache != options.ModelCache {
		t.Fatal(reopened, err)
	}
	if _, err := LoadSelections(root, "/different/runtime.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("another configuration inherited selected paths", err)
	}
	if _, err := os.Stat(filepath.Join(root, "installation/installation.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("path choice granted installation permission")
	}
	if err := SaveSelections(EnvironmentOptions{Root: root}, "/daily/runtime.json"); err != nil {
		t.Fatal(err)
	}
	reopened, err = LoadSelections(root, "/daily/runtime.json")
	if err != nil || reopened.Python != "" {
		t.Fatal("explicit automatic selection was not retained", reopened, err)
	}
}
func TestSelectionsProtectDailyStoreAndRejectPublicOrLinkedState(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(base, "daily")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveSelections(EnvironmentOptions{Root: filepath.Join(store, "components"), ProtectedStore: store}, "daily"); err == nil {
		t.Fatal("selection state written inside content store")
	}
	root := filepath.Join(base, "components")
	if err := SaveSelections(EnvironmentOptions{Root: root}, "daily"); err != nil {
		t.Fatal(err)
	}
	file := selectionPath(root, "daily")
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSelections(root, "daily"); err == nil {
		t.Fatal("public state accepted")
	}
	if err := os.Rename(file, file+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file+".original", file); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSelections(root, "daily"); err == nil {
		t.Fatal("linked state accepted")
	}
}
func TestDiagnosticExportWorksWithoutRuntimeAndContainsOnlyRedactedContext(t *testing.T) {
	code := 7
	input := DiagnosticInput{Operation: "original-operation", Stage: "runtime", ExitCode: &code, Error: "network failed", InstallerLog: "API_KEY=private-value\nhttps://user:pw@example.test/install?secret=query-value", Plan: &Plan{Items: []Item{{Requirement: Requirement{ID: "runtime", Version: "fixed"}, Observation: Observation{Status: Missing}}}}}
	file := filepath.Join(t.TempDir(), "diagnostic.zip")
	if err := ExportDiagnostic(file, input); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.File) != 3 {
		t.Fatal("incomplete diagnostic bundle")
	}
	for _, entry := range bundle.File {
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-value", "user:pw", "query-value"} {
			if strings.Contains(string(body), secret) {
				t.Fatal("secret in export", entry.Name)
			}
		}
		if entry.Name == "diagnostic.json" {
			var report DiagnosticInput
			if err := json.Unmarshal(body, &report); err != nil {
				t.Fatal(err)
			}
			if report.ExitCode == nil || *report.ExitCode != 7 || report.Operation != input.Operation || report.Plan == nil {
				t.Fatal("original context lost", report)
			}
		}
	}
}
func TestExistingDiagnosticAgentsNeverTreatMissingHostAsInstalled(t *testing.T) {
	root := t.TempDir()
	lookup := func(name string) (string, error) {
		if name == "pi" {
			return "/existing/bin/pi", nil
		}
		return "", os.ErrNotExist
	}
	if err := os.Mkdir(filepath.Join(root, "Codex.app"), 0700); err != nil {
		t.Fatal(err)
	}
	agents := ExistingDiagnosticAgents([]string{root}, lookup)
	if len(agents) != 1 || agents[0].Name != "pi" || agents[0].Application != "" {
		t.Fatal(agents)
	}
	contents := filepath.Join(root, "Codex.app/Contents")
	if err := os.Mkdir(contents, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte("synthetic installed host"), 0600); err != nil {
		t.Fatal(err)
	}
	agents = ExistingDiagnosticAgents([]string{root}, lookup)
	if len(agents) != 2 || agents[0].Name != "Codex" || agents[0].Application != filepath.Join(root, "Codex.app") {
		t.Fatal(agents)
	}
}
