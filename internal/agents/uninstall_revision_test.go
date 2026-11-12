package agents

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallRevisionPreservesNewInstallation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, "ok")
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, "config.toml")
	baseline := []byte("model = 'unrelated-model'\n")
	if err := os.WriteFile(path, baseline, 0600); err != nil {
		t.Fatal(err)
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 3}
	o := InstallOptions{ComputerLevel: true, ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, CodexDirectory: profile}
	i, err := Install(ctx, c, o)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	metaBefore, _ := os.ReadFile(installationPath(o.ClientRoot, c.ID))
	if err = UninstallRevision(ctx, o.ClientRoot, c.ID, 1); err == nil {
		t.Fatal("old installation confirmation accepted")
	}
	after, _ := os.ReadFile(path)
	metaAfter, _ := os.ReadFile(installationPath(o.ClientRoot, c.ID))
	if !bytes.Equal(before, after) || !bytes.Equal(metaBefore, metaAfter) {
		t.Fatal("new config or installation receipt changed")
	}
	if err = UninstallRevision(ctx, o.ClientRoot, c.ID, 3); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(after, bytes.Replace(before, []byte(i.Block), nil, 1)) {
		t.Fatal("unrelated configuration changed", err)
	}
}
