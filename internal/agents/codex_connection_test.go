package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/records"
)

func TestCodexComputerInstallationIsIncrementalAndScopeIndependent(t *testing.T) {
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, "ok")
	profile := filepath.Join(root, "isolated-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	baseline := []byte("# unrelated generated configuration\nmodel = 'unrelated-model'\n")
	path := filepath.Join(profile, "config.toml")
	os.WriteFile(path, baseline, 0600)
	c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1, AllowedScopes: []string{}}
	o := InstallOptions{ComputerLevel: true, ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, CodexDirectory: profile}
	i, err := Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckInstallation(i); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), string(baseline)) || toml.Unmarshal(data, &parsed) != nil {
		t.Fatal("unrelated configuration changed")
	}
	if strings.Contains(i.Block, "--workspace") || strings.Contains(i.Block, "cwd =") || !strings.Contains(i.Block, "--host-workspace") {
		t.Fatal("fixed workspace installed", i.Block)
	}
	if _, err = os.Stat(i.BackupPath); err != nil {
		t.Fatal("backup missing", err)
	}
	if _, ok := ConnectionVerification(i, 1); ok {
		t.Fatal("installation invented protocol proof")
	}
	if err = SaveProtocolVerification(o.ClientRoot, c.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	i, err = LoadInstallation(o.ClientRoot, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ConnectionVerification(i, 1); !ok {
		t.Fatal("empty-grant protocol receipt unavailable")
	}
	i.Connection.AllowedScopes = []string{uuid.NewString()}
	if _, ok := ConnectionVerification(i, 1); ok {
		t.Fatal("protocol receipt silently granted memory access")
	}
	// Scope application preserves the same computer profile and receives a
	// new generation. It does not turn the profile into a project adapter.
	c.Generation = 2
	c.AllowedScopes = []string{uuid.NewString()}
	i, err = Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ConnectionVerification(i, 2); ok {
		t.Fatal("old proof survived grant generation")
	}
	if err = Uninstall(context.Background(), o.ClientRoot, c.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(baseline) {
		t.Fatal("uninstall changed unrelated config")
	}
	o.Workspace = root
	if _, err = Install(context.Background(), c, o); err == nil {
		t.Fatal("computer profile accepted fixed workspace")
	}
}

func TestComputerConnectionUsesCapabilitiesRatherThanVersion(t *testing.T) {
	for _, version := range []string{"codex-cli 0.167.0", "codex-cli 0.167.0-alpha.3.2", "codex-cli 0.167.1", "codex-cli 9.0.0", ""} {
		if err := ComputerConnectionError(Discovery{Agent: "codex", Version: version}); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []Discovery{{Agent: "codex", Error: "executable missing"}, {Agent: "codex", Version: "codex-cli 0.167.0", CapabilityError: "fixture unsupported"}, {Agent: "traex"}} {
		if ComputerConnectionError(d) == nil {
			t.Fatal("unverified capability accepted", d)
		}
	}
}
