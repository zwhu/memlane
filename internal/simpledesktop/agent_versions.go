package simpledesktop

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var agentVersionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?\b`)

// Read the selected host's version only. Never launch a session or change its profile.
func loadAgentVersions(ctx context.Context, config Config, rows []AgentConnection) {
	var workers sync.WaitGroup
	for n := range rows {
		if !rows[n].Detected {
			continue
		}
		executable := config.AgentHosts[rows[n].Kind].Executable
		if executable == "" && rows[n].Kind == "codex" {
			executable = config.CodexExecutable
		}
		if executable == "" {
			name := map[string]string{"codex": "codex", "pi": "pi", "traex": "traecli", "claude_code": "claude", "opencode": "opencode", "cursor": "cursor"}[rows[n].Kind]
			if name == "" {
				continue
			}
			executable, _ = exec.LookPath(name)
		}
		if executable == "" {
			continue
		}
		workers.Add(1)
		go func(n int, executable string) {
			defer workers.Done()
			probe, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			command := exec.CommandContext(probe, executable, "--version")
			command.WaitDelay = 250 * time.Millisecond
			output, err := command.Output()
			if err == nil && len(output) <= 4096 {
				rows[n].HostVersion = agentVersionPattern.FindString(strings.TrimSpace(string(output)))
			}
		}(n, executable)
	}
	workers.Wait()
}
