package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pendingDigest(value []byte) string { return fmt.Sprintf("%x", sha256.Sum256(value)) }
func pendingShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
func pendingTestInstallation(t *testing.T) (AgentInstallation, PendingOperatorConfig, string) {
	t.Helper()
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = canonical
	plugin := filepath.Join(root, "artifact")
	if err := os.MkdirAll(filepath.Join(plugin, "dist/src"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, name := range []string{"managed-pending-cli.js", "managed-pending.js"} {
		value := []byte("verified synthetic worker\n")
		path := filepath.Join(plugin, "dist/src", name)
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
		files["dist/src/"+name] = pendingDigest(value)
	}
	manifest, _ := json.Marshal(map[string]any{"protocol": 1, "files": files})
	if err := os.WriteFile(filepath.Join(plugin, ".memlane-opencode-artifact.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	binding := filepath.Join(root, "binding.json")
	if err := os.WriteFile(binding, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(root, "request.json")
	environment := filepath.Join(root, "environment.txt")
	runtime := filepath.Join(root, "node-fixture")
	script := "#!/bin/sh\ncat > " + pendingShellQuote(capture) + "\nprintf '%s|%s' \"${NODE_OPTIONS-unset}\" \"${COGNEE_API_KEY-unset}\" > " + pendingShellQuote(environment) + "\nprintf '%s\\n' '{\"pending\":1,\"unknown_outcomes\":0,\"signature\":\"" + strings.Repeat("a", 64) + "\"}'\n"
	if err := os.WriteFile(runtime, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(root, "manager-token")
	if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	return AgentInstallation{HostKind: "opencode", PluginDir: plugin, ManifestSHA: pendingDigest(manifest), HelperSHA: files["dist/src/managed-pending-cli.js"], BindingFile: binding, BindingSHA: pendingDigest([]byte("{}")), NodeExecutable: runtime, NodeSHA: pendingDigest([]byte(script))},
		PendingOperatorConfig{TokenFile: token, ControlURL: "http://127.0.0.1:19301", ServiceURL: "http://127.0.0.1:19302"}, capture
}

func TestOpenCodePendingDispatchKeepsManagementCredentialPrivate(t *testing.T) {
	installation, operator, capture := pendingTestInstallation(t)
	t.Setenv("NODE_OPTIONS", "forbidden-ambient-loader")
	t.Setenv("COGNEE_API_KEY", "forbidden-ambient-owner")
	binding := ProjectBinding{AgentID: "opencode-synthetic", ProjectID: "11111111-1111-4111-8111-111111111111", HostSessionKey: "opencode-" + strings.Repeat("b", 64)}
	result, err := runPendingMaterials(context.Background(), "not-used-python", installation, binding, "22222222-2222-4222-8222-222222222222", "show", "", operator)
	if err != nil || result.Pending != 1 {
		t.Fatalf("OpenCode dispatch did not return metadata: %v", err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), strings.Repeat("m", 40)) || strings.Contains(string(raw), "forbidden-ambient-owner") {
		t.Fatal("credentials leaked into worker input")
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		t.Fatal("invalid worker input")
	}
	var actual PendingOperatorConfig
	if json.Unmarshal(input["operator"], &actual) != nil || actual != operator {
		t.Fatal("private operator references missing")
	}
	environment, err := os.ReadFile(filepath.Join(filepath.Dir(capture), "environment.txt"))
	if err != nil || string(environment) != "unset|unset" {
		t.Fatal("ambient loader or owner reached worker")
	}
}

func TestOpenCodePendingRejectsChangedInputsBeforeLaunchingWorker(t *testing.T) {
	for _, mutation := range []string{"worker", "runtime", "binding_permissions", "extra_file", "operator_missing"} {
		t.Run(mutation, func(t *testing.T) {
			installation, operator, capture := pendingTestInstallation(t)
			switch mutation {
			case "worker":
				if err := os.WriteFile(filepath.Join(installation.PluginDir, "dist/src/managed-pending-cli.js"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "runtime":
				if err := os.WriteFile(installation.NodeExecutable, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "binding_permissions":
				if err := os.Chmod(installation.BindingFile, 0644); err != nil {
					t.Fatal(err)
				}
			case "extra_file":
				if err := os.WriteFile(filepath.Join(installation.PluginDir, "untracked.js"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			case "operator_missing":
				operator.TokenFile = ""
			}
			_, err := runPendingMaterials(context.Background(), "not-used-python", installation, ProjectBinding{AgentID: "opencode-synthetic"}, "dataset", "show", "", operator)
			if err == nil {
				t.Fatal("unconfirmed input accepted")
			}
			if _, err := os.Stat(capture); !os.IsNotExist(err) {
				t.Fatal("unconfirmed worker was executed")
			}
		})
	}
}

// Opt-in integration through the actual desktop dispatcher and prepared worker.
// The private case is produced only by the owned synthetic acceptance driver.
func TestOpenCodePendingPreparedWorker(t *testing.T) {
	path := os.Getenv("MEMLANE_OPENCODE_PENDING_TEST_CASE")
	if path == "" {
		t.Skip("set an owned synthetic prepared-worker case")
	}
	if _, err := pendingFileHash(path, 65536, true); err != nil {
		t.Fatal("unconfirmed private test case")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		SyntheticOnly     bool                  `json:"synthetic_only"`
		Installation      AgentInstallation     `json:"installation"`
		Operator          PendingOperatorConfig `json:"operator"`
		Binding           ProjectBinding        `json:"binding"`
		Dataset           string                `json:"dataset"`
		Action            string                `json:"action"`
		Signature         string                `json:"signature"`
		ExpectedError     string                `json:"expected_error"`
		ExpectedPending   int                   `json:"expected_pending"`
		ExpectedDrained   int                   `json:"expected_drained"`
		ExpectedDiscarded int                   `json:"expected_discarded"`
		ExpectedMatched   int                   `json:"expected_matched"`
	}
	if json.Unmarshal(raw, &input) != nil || !input.SyntheticOnly {
		t.Fatal("owned synthetic case required")
	}
	result, err := runPendingMaterials(context.Background(), "unused-python", input.Installation, input.Binding, input.Dataset, input.Action, input.Signature, input.Operator)
	report := struct {
		Status PendingMaterials `json:"status"`
		Error  bool             `json:"error"`
	}{result, err != nil}
	encoded, _ := json.Marshal(report)
	if writeErr := os.WriteFile(path+".result.json", encoded, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if input.ExpectedError != "" {
		if err == nil || result.Error != input.ExpectedError {
			t.Fatal("expected refusal was not preserved")
		}
		return
	}
	if err != nil {
		t.Fatalf("prepared worker did not finish: %v", err)
	}
	if input.Action == "check" && (!result.Checked || result.Matched != input.ExpectedMatched) {
		t.Fatal("original receipt review metadata differs")
	}
	if result.Pending != input.ExpectedPending || result.Drained != input.ExpectedDrained || result.Discarded != input.ExpectedDiscarded {
		t.Fatal("prepared worker metadata differs from owned case")
	}
}
