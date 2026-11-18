package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
)

type DiagnosticAgent struct {
	Name        string
	Application string
	Executable  string
}

// ExistingDiagnosticAgents discovers installed hosts only. There is no download,
// login, plugin dependency or conversation submission. Without a verified host
// draft API, the explicit fallback is copy the full prompt and open that host.
func ExistingDiagnosticAgents(directories []string, lookup func(string) (string, error)) []DiagnosticAgent {
	if directories == nil {
		directories = []string{"/Applications"}
		if home, err := os.UserHomeDir(); err == nil {
			directories = append(directories, filepath.Join(home, "Applications"))
		}
	}
	if lookup == nil {
		lookup = exec.LookPath
	}
	kinds := []struct {
		Name, CLI string
		Apps      []string
	}{
		{"Codex", "codex", []string{"Codex.app", "ChatGPT.app"}},
		{"TraeX", "traex", []string{"TRAE CN.app", "TRAE.app"}},
		{"Cursor", "cursor", []string{"Cursor.app"}},
		{"Claude", "claude", []string{"Claude.app"}},
		{"OpenCode", "opencode", []string{"OpenCode.app"}},
		{"pi", "pi", nil},
	}
	var agents []DiagnosticAgent
	for _, kind := range kinds {
		agent := DiagnosticAgent{Name: kind.Name}
		agent.Executable, _ = lookup(kind.CLI)
		for _, dir := range directories {
			for _, name := range kind.Apps {
				candidate := filepath.Join(dir, name)
				if info, err := os.Stat(filepath.Join(candidate, "Contents/Info.plist")); err == nil && info.Mode().IsRegular() {
					agent.Application = candidate
					break
				}
			}
			if agent.Application != "" {
				break
			}
		}
		if agent.Application != "" || agent.Executable != "" {
			agents = append(agents, agent)
		}
	}
	return agents
}
