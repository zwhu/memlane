package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Exercise the same prelaunch probe as a frozen processing task. The fake
// executable never launches a model and its stderr must not enter a receipt.
func TestPiCompletionPrelaunchFailureReceipt(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "pi")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho 'private-probe-secret' >&2\nexit 27\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := RunPiCompletion(context.Background(), PiCompletionOptions{
		Executable: executable, AuthHome: root, Provider: "deepseek", Model: "deepseek-flash",
		SystemPrompt: "synthetic", Prompt: "synthetic", OutputSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil || out.Started {
		t.Fatalf("probe failure launched a model: %+v %v", out, err)
	}
	b, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var receipt map[string]any
	_ = json.Unmarshal(b, &receipt)
	if receipt["failure_stage"] != "agent_probe" || receipt["failure_code"] != "pi_agent_probe_version_exited" {
		t.Fatalf("prelaunch failure is not distinguishable: %s", b)
	}
	if strings.Contains(string(b), root) || strings.Contains(string(b), "private-probe-secret") {
		t.Fatalf("receipt contains private diagnostic: %s", b)
	}
}

func TestPiCompletionOrganizationSchemasReachProbe(t *testing.T) {
	for _, schema := range []string{records.OrganizationSchema, records.OrganizationReviewSchema, records.OrganizationGlobalSchema} {
		t.Run(schema, func(t *testing.T) {
			snapshot := records.OrganizationSnapshot{Schema: schema, Task: uuid.NewString(), Project: uuid.NewString(), Attempt: 1}
			out, err := RunPiCompletion(context.Background(), PiCompletionOptions{
				Executable: filepath.Join(t.TempDir(), "missing-pi"), AuthHome: t.TempDir(),
				SystemPrompt: "synthetic", Prompt: "synthetic", OutputSchema: snapshot.OutputSchema(),
			})
			if err == nil || out.FailureStage != "agent_probe" || out.Started {
				t.Fatalf("schema failed before probe: %+v %v", out, err)
			}
		})
	}
}

func TestPiCompletionPrivateConfigurationDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, settings, models, stage, code string }{
		{"settings", "{broken", "", "model_settings", "pi_model_settings_invalid"},
		{"selection", "{}", "", "model_selection", "pi_model_selection_required"},
		{"models", `{"defaultProvider":"deepseek","defaultModel":"deepseek-flash"}`, "{broken", "provider_configuration", "pi_provider_configuration_invalid"},
		{"command", `{"defaultProvider":"deepseek","defaultModel":"deepseek-flash"}`, `{"providers":{"key":"!private-command-secret"}}`, "provider_configuration", "pi_command_configuration_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "pi")
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nif [ \"$1\" = '--version' ]; then echo 0.84.1; else echo --extension; fi\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(tc.settings), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.models != "" {
				if err := os.WriteFile(filepath.Join(root, "models.json"), []byte(tc.models), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := RunPiCompletion(context.Background(), PiCompletionOptions{Executable: executable, AuthHome: root, SystemPrompt: "synthetic", Prompt: "synthetic", OutputSchema: json.RawMessage(`{"type":"object"}`)})
			if err == nil || out.Started || out.FailureStage != tc.stage || out.FailureCode != tc.code || out.AgentVersion != "0.84.1" {
				t.Fatalf("invalid receipt: %+v %v", out, err)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), root) || strings.Contains(string(b), "private-command-secret") {
				t.Fatalf("private receipt: %s", b)
			}
		})
	}
}
