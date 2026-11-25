package bootstrap

import (
	"os"
	"os/exec"
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
		home, _ := os.UserHomeDir()
		directories = defaultHostAppDirs(home)
	}
	if lookup == nil {
		lookup = exec.LookPath
	}

	var agents []DiagnosticAgent
	// Preserve existing display labels and relative order.
	for _, index := range []int{1, 0, 3, 4, 6, 5, 7, 2} {
		spec := localHostSpecs[index]
		host := detectHostInstallation(spec, directories, lookup, "")
		if !host.Detected {
			continue
		}
		name := spec.name
		switch spec.kind {
		case "traex":
			name = "TraeX"
		case "claude_code":
			name = "Claude"
		}
		agents = append(agents, DiagnosticAgent{Name: name, Application: host.Application, Executable: host.Executable})
	}
	return agents
}
