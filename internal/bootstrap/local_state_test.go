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
	"syscall"
	"testing"
	"time"
)

func TestPrivateReadRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(workerTestRoot(t), "state.fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := privateRead(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "private regular file") {
			t.Fatalf("privateRead FIFO error = %v", err)
		}
	case <-time.After(2 * time.Second):
		// Release a blocked open before reporting a regression.
		if fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		t.Fatal("privateRead blocked opening a FIFO")
	}
}

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

func TestSelectedAgentsPersistAcrossDownloadSourceChanges(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "runtime.json")
	assertSelections := func(agents, python, mirror string) {
		t.Helper()
		got, err := LoadSelections(root, config)
		if err != nil || !got.SelectedAgentsConfigured || strings.Join(got.SelectedAgents, ",") != agents || got.Python != python || got.DownloadSource != mirror {
			t.Fatal(got, err)
		}
	}
	if err := SaveSelectedAgents(root, config, []string{"antigravity", "invalid", "codex", "codex"}); err != nil {
		t.Fatal(err)
	}
	assertSelections("codex,antigravity", "", "")
	opts := EnvironmentOptions{Root: root, Python: "/usr/bin/python3", DownloadSource: DownloadSourceChina}
	if err := SaveSelections(opts, config); err != nil {
		t.Fatal(err)
	}
	assertSelections("codex,antigravity", opts.Python, DownloadSourceChina)
	selection := selectionPath(root, config)
	raw, err := os.ReadFile(selection)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["future_field"] = json.RawMessage(`{"value":9007199254740993}`)
	writeBootstrapTestJSON(t, selection, doc)
	if err := SaveSelectedAgents(root, config, []string{"pi", "trae_ide"}); err != nil {
		t.Fatal(err)
	}
	assertSelections("trae_ide,pi", opts.Python, DownloadSourceChina)
	if raw, err := os.ReadFile(selection); err != nil || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal(string(raw), err)
	}
	opts.SelectedAgents = []string{}
	if err := SaveSelections(opts, config); err != nil {
		t.Fatal(err)
	}
	assertSelections("", opts.Python, DownloadSourceChina)
}

func TestSelectedAgentsConfiguredDistinguishesLegacyAndExplicitEmpty(t *testing.T) {
	root := workerTestRoot(t)
	config := filepath.Join(root, "runtime.json")
	path := selectionPath(root, config)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeBootstrapTestJSON(t, path, map[string]any{
		"schema": 1, "binding": selectionBinding(config), "selected_agents": []string{"codex"},
	})
	got, err := LoadSelections(root, config)
	if err != nil || got.SelectedAgentsConfigured || strings.Join(got.SelectedAgents, ",") != "codex" {
		t.Fatalf("legacy selections = %+v, %v", got, err)
	}
	if err := SaveSelections(EnvironmentOptions{Root: root}, config); err != nil {
		t.Fatal(err)
	}
	got, err = LoadSelections(root, config)
	if err != nil || got.SelectedAgentsConfigured || strings.Join(got.SelectedAgents, ",") != "codex" {
		t.Fatalf("retained legacy selections = %+v, %v", got, err)
	}
	if err := SaveSelectedAgents(root, config, []string{}); err != nil {
		t.Fatal(err)
	}
	assertExplicitEmpty := func() {
		t.Helper()
		got, err := LoadSelections(root, config)
		if err != nil || !got.SelectedAgentsConfigured || len(got.SelectedAgents) != 0 {
			t.Fatalf("explicitly empty selections = %+v, %v", got, err)
		}
	}
	assertExplicitEmpty()
	if err := SaveSelections(EnvironmentOptions{Root: root}, config); err != nil {
		t.Fatal(err)
	}
	assertExplicitEmpty()
}
