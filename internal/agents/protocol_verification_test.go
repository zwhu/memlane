package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func TestProtocolTranscriptRequiresEmptyAuthorizedHandshake(t *testing.T) {
	valid := `{"scopes":[],"bound_project":"","current_project":"","requires_scope":true}`
	for _, test := range []struct {
		name, tool, body string
		ok, pass         bool
	}{
		{"empty grants", "memory_scopes", valid, true, true},
		{"no receipt", "memory_scopes", `{}`, true, false},
		{"null scopes", "memory_scopes", `{"scopes":null,"bound_project":"","current_project":"","requires_scope":true}`, true, false},
		{"granted scope", "memory_scopes", `{"scopes":[{}],"bound_project":"","current_project":"","requires_scope":true}`, true, false},
		{"wrong tool", "memory_save", valid, true, false},
		{"failed reply", "memory_scopes", valid, false, false},
		{"bound project", "memory_scopes", `{"scopes":[],"bound_project":"project","current_project":"","requires_scope":true}`, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := Execution{ProtocolReceipt: &agentbridge.Result{Protocol: 1, OK: test.ok, Result: json.RawMessage(test.body)}}
			if test.tool != "memory_scopes" {
				e.Calls = []ToolCall{{Tool: test.tool}}
			}
			if (checkProtocolTranscript(e) == nil) != test.pass {
				t.Fatal("unexpected handshake validation")
			}
		})
	}
	if checkProtocolTranscript(Execution{}) == nil {
		t.Fatal("missing transcript accepted")
	}
}

// Explicit process/protocol fixture, not real pi/model acceptance. It verifies
// launch isolation, tool allowlist and distinct persistent verification fields.
func TestProtocolVerificationIsolatedProcessAndReceipt(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "pi-fixture")
	id := uuid.NewString()
	code := `#!/bin/sh
test "$MEMLANE_TOOL_MODE" = connection || exit 31
test "$MEMLANE_USAGE_CHANNEL" = verification || exit 32
test "$(basename "$PWD")" = workspace || exit 33
test -z "$(ls -A)" || exit 34
previous=''
for argument in "$@"; do
  if test "$previous" = --tools; then test "$argument" = memory_scopes || exit 35; fi
  previous="$argument"
done
printf '%s\n' '{"type":"message_end","message":{"role":"custom","customType":"memlane-connection-result","details":{"connection":"__ID__","generation":1,"reply":{"protocol_version":1,"ok":true,"result":{"scopes":[],"bound_project":"","current_project":"","requires_scope":true}}}}}'
`
	if err := os.WriteFile(program, []byte(strings.ReplaceAll(code, "__ID__", id)), 0700); err != nil {
		t.Fatal(err)
	}
	i := Installation{Connection: records.Connection{ID: id, Agent: "pi", Generation: 1, Enabled: true}, Options: InstallOptions{ClientRoot: root, AgentPath: program, PiDirectory: filepath.Join(root, "pi")}, ConfigPath: filepath.Join(root, "settings.json"), ExtensionPath: filepath.Join(root, "extension.ts")}
	source := []byte("// synthetic extension fixture")
	i.ExtensionHash = hash(source)
	if err := os.WriteFile(i.ExtensionPath, source, 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"extensions": []string{i.ExtensionPath}})
	if err := os.WriteFile(i.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(i)
	if err := privateWrite(installationPath(root, i.Connection.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	e, err := VerifyProtocolConnection(context.Background(), Discovery{Agent: "pi", Path: program}, i, RunOptions{})
	if err != nil || !e.ProcessGroupGone {
		t.Fatal("isolated protocol process failed", err, e)
	}
	saved, err := LoadInstallation(root, i.Connection.ID)
	if err != nil || saved.ProtocolGeneration != 1 || saved.ProtocolVerifiedAt == "" || saved.VerifiedGeneration != 0 || saved.VerifiedAt != "" {
		t.Fatal("verification receipts conflated", saved, err)
	}
	if err = SaveProtocolVerification(root, i.Connection.ID, 2, ""); err == nil {
		t.Fatal("stale generation accepted")
	}
	if err = SaveProtocolVerification(root, i.Connection.ID, 1, "cancelled"); err != nil {
		t.Fatal(err)
	}
	saved, err = LoadInstallation(root, i.Connection.ID)
	if err != nil || saved.ProtocolGeneration != 0 || saved.ProtocolVerifiedAt != "" || saved.ProtocolError != "cancelled" {
		t.Fatal("failed protocol verification retained success", saved, err)
	}
	// A live process cancellation must also clear a previous protocol success.
	if err = SaveProtocolVerification(root, id, 1, ""); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "active")
	code = "#!/bin/sh\nprintf ready > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\nsleep 30\n"
	if err = os.WriteFile(program, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	observed := make(chan bool, 1)
	go func() {
		for {
			if _, e := os.Stat(marker); e == nil {
				observed <- true
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				observed <- false
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	e, err = VerifyProtocolConnection(ctx, Discovery{Agent: "pi", Path: program}, i, RunOptions{})
	if !<-observed || err == nil || e.PID == 0 || !e.ProcessGroupGone {
		t.Fatal("live protocol cancellation failed", err, e)
	}
	saved, err = LoadInstallation(root, id)
	if err != nil || saved.ProtocolGeneration != 0 || saved.ProtocolVerifiedAt != "" || saved.ProtocolError == "" {
		t.Fatal("cancelled process retained success", saved, err)
	}
}

func TestConnectionVerificationKeepsProtocolSeparateFromModelAndGrants(t *testing.T) {
	i := Installation{Connection: records.Connection{Agent: "pi", Generation: 1, Enabled: true}, ProtocolGeneration: 1, ProtocolVerifiedAt: "protocol"}
	if proof, ok := ConnectionVerification(i, 1); !ok || proof != "protocol:protocol" {
		t.Fatal("missing distinct protocol proof", proof, ok)
	}
	if _, ok := ConnectionVerification(i, 2); ok {
		t.Fatal("wrong connection generation accepted")
	}
	i.Connection.AllowedScopes = []string{"project"}
	if _, ok := ConnectionVerification(i, 1); ok {
		t.Fatal("protocol receipt inherited a later authorization")
	}
	i.VerifiedGeneration = 1
	i.VerifiedAt = "legacy"
	if proof, ok := ConnectionVerification(i, 1); !ok || proof != "legacy" {
		t.Fatal("legacy dependency token changed")
	}
	i.Connection.Enabled = false
	if _, ok := ConnectionVerification(i, 1); ok {
		t.Fatal("disabled identity accepted")
	}
}
