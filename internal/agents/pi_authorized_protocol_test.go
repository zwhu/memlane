package agents

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiAuthorizedProtocolIsolatedHandshake(t *testing.T) {
	root := t.TempDir()
	id, scope := uuid.NewString(), uuid.NewString()
	program := filepath.Join(root, "pi-fixture")
	body, _ := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{"role": "custom", "customType": "memlane-connection-result", "details": map[string]any{"connection": id, "generation": 2, "reply": map[string]any{"protocol_version": 1, "ok": true, "result": map[string]any{"scopes": []records.Scope{{ID: scope, Kind: "project"}}, "bound_project": scope, "current_project": scope, "requires_scope": false}}}}})
	script := `#!/bin/sh
test "$MEMLANE_TOOL_MODE" = connection || exit 31
test "$MEMLANE_USAGE_CHANNEL" = verification || exit 32
test "$(basename "$PWD")" = workspace || exit 33
test -z "$(ls -A)" || exit 34
test "$HOME" = "$PI_CODING_AGENT_DIR" || exit 35
test ! -e "$PI_CODING_AGENT_DIR/auth.json" || exit 36
test ! -e "$PI_CODING_AGENT_DIR/models.json" || exit 37
previous=''
for argument in "$@";do
 test "$argument" != --provider && test "$argument" != --model || exit 38
 if test "$previous" = --tools;then test "$argument" = memory_scopes || exit 39;fi
 previous="$argument"
done
printf '%s\n' '__BODY__'
`
	if err := os.WriteFile(program, []byte(strings.ReplaceAll(script, "__BODY__", string(body))), 0700); err != nil {
		t.Fatal(err)
	}
	i := Installation{Connection: records.Connection{ID: id, Agent: "pi", Enabled: true, Generation: 2, Project: scope, AllowedScopes: []string{scope}}, Options: InstallOptions{ClientRoot: root, AgentPath: program, Workspace: root, PiDirectory: filepath.Join(root, "source")}, ConfigPath: filepath.Join(root, "settings.json"), ExtensionPath: filepath.Join(root, "extension.ts")}
	source := []byte("// synthetic protocol fixture")
	i.ExtensionHash = hash(source)
	if err := os.WriteFile(i.ExtensionPath, source, 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"extensions": []string{i.ExtensionPath}})
	if err := os.WriteFile(i.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(i)
	if err := privateWrite(installationPath(root, id), raw, 0600); err != nil {
		t.Fatal(err)
	}
	e, err := VerifyProtocolConnection(context.Background(), Discovery{Agent: "pi", Path: program, Version: "0.84.1"}, i, RunOptions{})
	if err != nil || !e.ProcessGroupGone {
		t.Fatalf("authorized protocol rejected: %+v %v", e, err)
	}
	saved, err := LoadInstallation(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ConnectionVerification(saved, 2); !ok || saved.ProtocolScopeDigest != protocolScopeDigest(i.Connection) || saved.VerifiedGeneration != 0 {
		t.Fatal("unbound or conflated proof", saved)
	}
	saved.Connection.AllowedScopes = append(saved.Connection.AllowedScopes, uuid.NewString())
	if _, ok := ConnectionVerification(saved, 2); ok {
		t.Fatal("scope mutation reused prior proof")
	}
	changed, _ := json.Marshal(saved)
	if err = privateWrite(installationPath(root, id), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = SaveProtocolVerification(root, id, 2, "", ProtocolVerificationContext{ScopeDigest: protocolScopeDigest(i.Connection)}); err == nil {
		t.Fatal("same-generation scope drift sealed a different grant set")
	}

}

func TestPiAuthorizedProtocolRejectsAmbiguousIdentityAndReceipt(t *testing.T) {
	scope := uuid.NewString()
	c := records.Connection{ID: uuid.NewString(), Agent: "pi", Enabled: true, Generation: 2, AllowedScopes: []string{scope}}
	if !PiProtocolConnectionEligible(c) {
		t.Fatal("settled identity rejected")
	}
	for _, mutate := range []func(*records.Connection){
		func(x *records.Connection) { x.AutoProject = true }, func(x *records.Connection) { x.PendingProject = scope }, func(x *records.Connection) { x.TrustedWorkspace = t.TempDir() }, func(x *records.Connection) { x.Project = uuid.NewString() }, func(x *records.Connection) { x.AllowedScopes = []string{""} }, func(x *records.Connection) { x.AllowedScopes = []string{scope, scope} },
	} {
		bad := c
		mutate(&bad)
		if PiProtocolConnectionEligible(bad) {
			t.Fatal("ambiguous identity accepted", bad)
		}
	}
	receipt := func(scopes []records.Scope, bound, current string) Execution {
		raw, _ := json.Marshal(map[string]any{"scopes": scopes, "bound_project": bound, "current_project": current, "requires_scope": current == ""})
		return Execution{ProtocolReceipt: &agentbridge.Result{Protocol: 1, OK: true, Result: raw}}
	}
	valid := receipt([]records.Scope{{ID: scope, Kind: "project"}}, "", "")
	if err := checkPiProtocolTranscript(valid, c); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Execution{receipt([]records.Scope{}, "", ""), receipt([]records.Scope{{ID: uuid.NewString(), Kind: "project"}}, "", ""), receipt([]records.Scope{{ID: scope, Kind: "project"}}, "", scope), receipt([]records.Scope{{ID: scope, Kind: "project"}}, scope, scope)} {
		if checkPiProtocolTranscript(bad, c) == nil {
			t.Fatal("wrong grant metadata accepted")
		}
	}
	valid.Events = map[string]int{"tool_execution_start": 1}
	if checkPiProtocolTranscript(valid, c) == nil {
		t.Fatal("model tool execution accepted as no-model handshake")
	}
}
