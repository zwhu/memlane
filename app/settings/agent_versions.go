package settings

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

func agentExecutableNames(kind string) []string {
	switch kind {
	case "traex":
		return []string{"traecli", "traex", "trae-cli"}
	case "claude_code":
		return []string{"claude"}
	case "antigravity":
		return []string{"agy"}
	case "codex", "pi", "opencode", "cursor":
		return []string{kind}
	default:
		return nil
	}
}

func resolveAgentExecutable(kind string, names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	var dirs []string
	home, _ := os.UserHomeDir()
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/bin"), filepath.Join(home, ".bun/bin"))
		if kind == "opencode" {
			dirs = append(dirs, filepath.Join(home, ".opencode/bin"))
		}
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	isExecutable := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0
	}
	for _, dir := range dirs {
		for _, name := range names {
			if candidate := filepath.Join(dir, name); isExecutable(candidate) {
				return candidate
			}
		}
	}
	if kind == "antigravity" {
		const bundleExecutable = "Applications/Gemini.app/Contents/Helpers/agy"
		if home != "" {
			if candidate := filepath.Join(home, bundleExecutable); isExecutable(candidate) {
				return candidate
			}
		}
		if candidate := "/" + bundleExecutable; isExecutable(candidate) {
			return candidate
		}
	}
	return ""
}

// Read the selected host's version only. Never launch a session or change its profile.
func loadAgentVersions(ctx context.Context, config Config, rows []AgentConnection) {
	var workers sync.WaitGroup
	for n := range rows {
		if !rows[n].Detected || strings.TrimSpace(rows[n].HostVersion) != "" {
			continue
		}
		executable := config.AgentHosts[rows[n].Kind].Executable
		if rows[n].Kind == "trae_ide" {
			workers.Add(1)
			go func(n int, executable string) {
				defer workers.Done()
				if ctx.Err() == nil {
					if v := readTraeBundleVersion(executable); v != "" {
						rows[n].HostVersion = v
					}
				}
			}(n, executable)
			continue
		}
		if executable == "" && rows[n].Kind == "codex" {
			executable = config.CodexExecutable
		}
		if executable == "" {
			executable = resolveAgentExecutable(rows[n].Kind, agentExecutableNames(rows[n].Kind)...)
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
				if v := agentVersionPattern.FindString(strings.TrimSpace(string(output))); v != "" {
					rows[n].HostVersion = v
				}
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
