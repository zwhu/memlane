package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func TestCodexScopeProtocolAndSingleProjectProof(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	i := Installation{AgentVersion: "codex-cli 0.167.0", Connection: records.Connection{Agent: "codex", Enabled: true, Generation: 3, AllowedScopes: []string{"project-a", "project-b"}}, Options: InstallOptions{ComputerLevel: true}}
	result := func(current string, scopes []records.Scope) Execution {
		raw, _ := json.Marshal(map[string]any{"scopes": scopes, "bound_project": "", "current_project": current, "requires_scope": current == ""})
		return Execution{ProtocolReceipt: &agentbridge.Result{Protocol: 1, OK: true, Result: raw}}
	}
	scopes := []records.Scope{{ID: "project-a", Kind: "project"}, {ID: "project-b", Kind: "project"}}
	proof, err := checkCodexProtocolTranscript(result("project-a", scopes), i, a)
	if err != nil {
		t.Fatal(err)
	}
	i.ProtocolGeneration, i.ProtocolVerifiedAt, i.ProtocolScopeDigest = 3, "protocol", protocolScopeDigest(i.Connection)
	i.ProtocolProject, i.ProtocolWorkspaceDigest = proof.Project, proof.WorkspaceDigest
	if !CodexProjectVerification(i, "project-a", a, 3) || CodexProjectVerification(i, "project-b", b, 3) {
		t.Fatal("project A proof covered B")
	}
	if _, err = checkCodexProtocolTranscript(result("", scopes), i, ""); err == nil {
		t.Fatal("project grants verified without host workspace")
	}
	if _, err = checkCodexProtocolTranscript(result("foreign", scopes), i, b); err == nil {
		t.Fatal("foreign current project accepted")
	}
	if _, err = checkCodexProtocolTranscript(result("project-a", scopes[:1]), i, a); err == nil {
		t.Fatal("truncated grant receipt accepted")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	if !CodexProjectVerification(i, "project-a", alias, 3) {
		t.Fatal("same filesystem alias rejected")
	}
	if err = os.Rename(a, a+"-moved"); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(a + "-moved")
	if err = os.Mkdir(a, 0700); err != nil {
		t.Fatal(err)
	}
	if CodexProjectVerification(i, "project-a", a, 3) {
		t.Fatal("replaced directory retained proof")
	}
	i.VerifiedGeneration, i.VerifiedAt, i.VerificationError = 3, "old-read-write", ""
	i.ProtocolScopeDigest = "outdated"
	if _, ok := ConnectionVerification(i, 3); ok {
		t.Fatal("legacy read/write masked current scope protocol failure")
	}
}

func TestCodexCommonScopeProtocolHasNoProjectPrerequisite(t *testing.T) {
	i := Installation{Connection: records.Connection{AllowedScopes: []string{"preference"}}}
	raw := json.RawMessage(`{"scopes":[{"id":"preference","kind":"preference"}],"bound_project":"","current_project":"","requires_scope":true}`)
	proof, err := checkCodexProtocolTranscript(Execution{ProtocolReceipt: &agentbridge.Result{Protocol: 1, OK: true, Result: raw}}, i, "")
	if err != nil || proof.Project != "" || proof.WorkspaceDigest != "" {
		t.Fatal("common knowledge grant required a project", err)
	}
}
