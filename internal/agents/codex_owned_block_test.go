package agents

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/records"
)

func TestCodexOwnedBlockAllowsEquivalentFormattingOnly(t *testing.T) {
	id := uuid.NewString()
	i := Installation{Connection: records.Connection{ID: id, Agent: "codex"}, Options: InstallOptions{ComputerLevel: true}}
	i.Block = "\n# Memlane " + id + " begin\n[mcp_servers.memlane]\ncommand = \"synthetic-bridge\"\nargs = [\"mcp\", \"--connection\", \"synthetic\"]\n# Memlane " + id + " end\n"
	formatted := strings.ReplaceAll(i.Block, "\"", "'")
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"equivalent", formatted, true},
		{"command_changed", strings.Replace(formatted, "synthetic-bridge", "other", 1), false},
		{"argument_changed", strings.Replace(formatted, "--connection", "--other", 1), false},
		{"field_added", strings.Replace(formatted, "args =", "cwd = '/other'\nargs =", 1), false},
		{"outside_extension", formatted + "[mcp_servers.memlane.env]\nEXTRA = 'value'\n", false},
		{"duplicate_markers", formatted + "# Memlane " + id + " end\n", false},
		{"foreign_namespace_before_relocated_end", strings.Replace(formatted, "# Memlane "+id+" end", "[plugins.foreign]\nenabled = true\n# Memlane "+id+" end", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ownedMCPBlock([]byte(tc.data), i)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexReformattedMCPRemainsVerifiableReinstallableAndRemovable(t *testing.T) {
	for _, action := range []string{"reinstall", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			program := codexCompletionFixtureProgram(t, root, "ok")
			profile := filepath.Join(root, "profile")
			if err := os.Mkdir(profile, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(profile, "config.toml")
			baseline := []byte("model = 'unrelated'\n")
			if err := os.WriteFile(path, baseline, 0600); err != nil {
				t.Fatal(err)
			}
			c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1}
			o := InstallOptions{ComputerLevel: true, ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, CodexDirectory: profile}
			i, err := Install(ctx, c, o)
			if err != nil {
				t.Fatal(err)
			}
			var tree map[string]any
			if err = toml.Unmarshal([]byte(i.Block), &tree); err != nil {
				t.Fatal(err)
			}
			normalized, err := toml.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}
			block := []byte("# Memlane " + c.ID + " begin\n" + string(normalized) + "# Memlane " + c.ID + " end\n")
			raw, _ := os.ReadFile(path)
			formatted := bytes.Replace(raw, []byte(i.Block), block, 1)
			formatted = bytes.Replace(formatted, []byte("# Memlane "+c.ID+" end"), []byte("[plugins.\"cognee@cognee\"]\nenabled = true\n# Memlane "+c.ID+" end"), 1)
			if err = os.WriteFile(path, formatted, 0600); err != nil {
				t.Fatal(err)
			}
			if err = CheckInstallation(i); err != nil {
				t.Fatal("formatting invalidated MCP", err)
			}
			if action == "reinstall" {
				c.Generation++
				_, err = Install(ctx, c, o)
			} else {
				err = UninstallRevision(ctx, o.ClientRoot, c.ID, c.Generation)
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			var parsed map[string]any
			if toml.Unmarshal(after, &parsed) != nil || parsed["model"] != "unrelated" || parsed["plugins"] == nil {
				t.Fatal("foreign configuration lost")
			}
			if action == "uninstall" && parsed["mcp_servers"] != nil {
				t.Fatal("owned MCP not removed")
			}
		})
	}
}
