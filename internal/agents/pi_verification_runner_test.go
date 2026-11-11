package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Local executable fixtures only; no model, auth or running daemon involved.
func TestPiVerificationModelsAndAssistantFailure(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"fixture":{"api":"openai-completions","models":[{"id":"custom-model"}]}}}`
	if err := os.WriteFile(filepath.Join(profile, "models.json"), []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "settings.json"), []byte(`{"hooks":"must-not-copy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "pi-fixture")
	script := `#!/bin/sh
test -f "$PI_CODING_AGENT_DIR/models.json" || exit 41
grep -q custom-model "$PI_CODING_AGENT_DIR/models.json" || exit 42
test ! -f "$PI_CODING_AGENT_DIR/settings.json" || exit 43
printf '%s\n' 'Warning: custom model fallback' >&2
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"synthetic provider refusal","content":[]}}'
exit 0
`
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), Discovery{Agent: "pi", Path: program, Version: "0.84.1"}, Installation{Options: InstallOptions{Workspace: root, PiDirectory: profile}}, RunOptions{Provider: "fixture", Model: "custom-model", Prompt: "synthetic"})
	if err == nil || out.ExitCode != 0 || !strings.Contains(err.Error(), "synthetic provider refusal") || strings.Contains(err.Error(), "custom model fallback") || len(out.Calls) != 0 || !out.ProcessGroupGone {
		t.Fatalf("lost actual terminal failure: %+v %v", out, err)
	}
}

func TestPiVerificationNoToolsRemainsFailure(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "pi-fixture")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"stopReason\":\"stop\",\"content\":[]}}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Discovery{Agent: "pi", Path: program}, Installation{Options: InstallOptions{Workspace: root, PiDirectory: root}}, RunOptions{Prompt: "synthetic"})
	if err == nil || !strings.Contains(err.Error(), "did not invoke a Memlane memory tool") {
		t.Fatal(err)
	}
}

func TestPiModelDefinitionsRejectCommandsBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "pi-fixture")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 77\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "models.json"), []byte(`{"providers":{"fixture":{"apiKey":"!must-never-execute"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), Discovery{Agent: "pi", Path: program}, Installation{Options: InstallOptions{Workspace: root, PiDirectory: root}}, RunOptions{Prompt: "synthetic"})
	if err == nil || out.PID != 0 || !strings.Contains(err.Error(), "command-valued") {
		t.Fatalf("unsafe provider launched: %+v %v", out, err)
	}
}

func TestPiModelDefinitionsMissingBoundSnapshot(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := copyPiModelDefinitions(source, destination, hash([]byte(`{"providers":{}}`))); err == nil {
		t.Fatal("missing bound models silently fell back to built-in definitions")
	}
	if err := copyPiModelDefinitions(source, destination, ""); err != nil {
		t.Fatal("unbound built-in model definitions rejected", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "models.json")); !os.IsNotExist(err) {
		t.Fatal("missing definitions produced output", err)
	}
}
