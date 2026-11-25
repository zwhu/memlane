package simpledesktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
)

var agentVersionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?\b`)

func resolveAgentExecutable(kind, name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local/bin", name))
		if kind == "opencode" {
			candidates = append(candidates, filepath.Join(home, ".opencode/bin", name))
		}
		if kind == "antigravity" {
			candidates = append(candidates, filepath.Join(home, "Applications/Gemini.app/Contents/Helpers/agy"))
		}
	}
	candidates = append(candidates,
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	)
	if kind == "antigravity" {
		candidates = append(candidates, "/Applications/Gemini.app/Contents/Helpers/agy")
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return candidate
		}
	}
	return ""
}

// Read the selected host's version only. Never launch a session or change its profile.
func loadAgentVersions(ctx context.Context, config Config, rows []AgentConnection) {
	var workers sync.WaitGroup
	for n := range rows {
		if !rows[n].Detected {
			continue
		}
		executable := config.AgentHosts[rows[n].Kind].Executable
		if rows[n].Kind == "trae_ide" {
			workers.Add(1)
			go func(n int, executable string) {
				defer workers.Done()
				if ctx.Err() == nil {
					rows[n].HostVersion = readTraeBundleVersion(executable)
				}
			}(n, executable)
			continue
		}
		if executable == "" && rows[n].Kind == "codex" {
			executable = config.CodexExecutable
		}
		if executable == "" {
			name := map[string]string{
				"codex": "codex", "pi": "pi", "traex": "traecli",
				"claude_code": "claude", "opencode": "opencode",
				"cursor": "cursor", "antigravity": "agy",
			}[rows[n].Kind]
			if name == "" {
				continue
			}
			executable = resolveAgentExecutable(rows[n].Kind, name)
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

func readTraeBundleVersion(executable string) string {
	var candidates []string
	for path := filepath.Clean(executable); filepath.IsAbs(path) && path != filepath.Dir(path); path = filepath.Dir(path) {
		if strings.EqualFold(filepath.Ext(path), ".app") {
			candidates = append(candidates, path)
			break
		}
	}
	dirs := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	for _, dir := range dirs {
		candidates = append(candidates, filepath.Join(dir, "Trae CN.app"), filepath.Join(dir, "TRAE CN.app"))
	}
	for _, path := range candidates {
		if version := bootstrap.ReadBundleVersion(path); version != "" {
			return version
		}
	}
	return ""
}
