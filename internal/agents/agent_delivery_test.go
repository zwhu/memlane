package agents

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"path/filepath"
	"strings"
	"testing"
)

func TestD04NewCodexInstallationOptsIntoDurabilityWithoutFixedWorkspace(t *testing.T) {
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, "ok")
	c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1, AllowedScopes: []string{}}
	i, e := Install(context.Background(), c, InstallOptions{ComputerLevel: true, ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, CodexDirectory: filepath.Join(root, "isolated-profile")})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(i.Block, "--durable-delivery") || strings.Contains(i.Block, "--workspace") || i.ToolsetVersion != 2 {
		t.Fatal(i.Block)
	}
	if e = CheckInstallation(i); e != nil {
		t.Fatal(e)
	}
}
func TestD04PiEmbeddedAdapterRequestsDurabilityAndPreservesModes(t *testing.T) {
	if !strings.Contains(piExtension, `["call", "--durable-delivery", "--root"`) || !strings.Contains(piExtension, `process.env.MEMLANE_USAGE_CHANNEL ?? "daily"`) || !strings.Contains(piExtension, `process.env.MEMLANE_TOOL_MODE ?? ""`) {
		t.Fatal("pi adapter does not forward explicit delivery and channel boundaries")
	}
}
