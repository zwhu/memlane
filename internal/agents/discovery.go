// Package agents discovers and adapts supported local Agent installations.
package agents

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Discovery struct {
	Agent    string `json:"agent"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Channel  string `json:"channel,omitempty"`
	Error    string `json:"error,omitempty"`
}

func Discover(ctx context.Context, home string, selected map[string]string) []Discovery {
	paths := filepath.SplitList(os.Getenv("PATH"))
	paths = append(paths, "/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local/bin"), filepath.Join(home, ".npm-global/bin"))
	// Bounded known Node installation directory, useful for Finder's short PATH.
	versions, _ := os.ReadDir(filepath.Join(home, ".nvm/versions/node"))
	sort.Slice(versions, func(i, j int) bool { return versions[i].Name() > versions[j].Name() })
	for _, version := range versions[:min(100, len(versions))] {
		if version.IsDir() {
			paths = append(paths, filepath.Join(home, ".nvm/versions/node", version.Name(), "bin"))
		}
	}
	results := []Discovery{}
	for _, agent := range []string{"codex", "pi", "traex"} {
		name := agent
		if agent == "codex" {
			name = "Codex"
		} else if agent == "traex" {
			name = "TRAE CLI"
		}
		if chosen := selected[agent]; chosen != "" {
			results = append(results, Probe(ctx, agent, chosen))
			continue
		}
		pathsForAgent := []string{}
		if path := selected[agent]; path != "" {
			pathsForAgent = append(pathsForAgent, path)
		}
		for _, dir := range paths {
			if filepath.IsAbs(dir) {
				if agent == "traex" {
					pathsForAgent = append(pathsForAgent, filepath.Join(dir, "traecli"), filepath.Join(dir, "traex"))
				} else {
					pathsForAgent = append(pathsForAgent, filepath.Join(dir, agent))
				}
			}
		}
		if agent == "codex" {
			pathsForAgent = append(pathsForAgent, "/Applications/Codex.app/Contents/Resources/codex")
		}
		found := Discovery{Agent: agent, Name: name}
		for _, path := range pathsForAgent {
			if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
				found = Probe(ctx, agent, path)
				break
			}
		}
		results = append(results, found)
	}
	return results
}

func Probe(ctx context.Context, agent, path string) Discovery {
	name := agent
	if agent == "codex" {
		name = "Codex"
	} else if agent == "traex" {
		name = "TRAE CLI"
	}
	out := Discovery{Agent: agent, Name: name, Path: path}
	if !filepath.IsAbs(path) {
		out.Error = "请选择绝对可执行文件路径"
		return out
	}
	isolated, err := os.MkdirTemp("", "memlane-agent-probe-")
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer os.RemoveAll(isolated)
	env := append(os.Environ(), "PATH="+filepath.Dir(path)+":"+os.Getenv("PATH"), "CODEX_HOME="+filepath.Join(isolated, "codex"), "PI_CODING_AGENT_DIR="+filepath.Join(isolated, "pi"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "TRAE_HOME="+filepath.Join(isolated, "trae"), "TRAECLI_HOME="+filepath.Join(isolated, "trae/cli"))
	command := func(args ...string) (string, error) {
		run, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		cmd := exec.CommandContext(run, path, args...)
		cmd.Dir = isolated
		cmd.Env = env
		data, e := cmd.Output()
		if len(data) > 32768 {
			return "", errors.New("Agent help output too large")
		}
		return string(data), e
	}
	version, err := command("--version")
	if err != nil {
		out.Error = "无法读取 Agent 版本"
		return out
	}
	out.Version = strings.TrimSpace(version)
	if agent == "traex" {
		for _, line := range strings.Split(version, "\n") {
			if strings.HasPrefix(line, "installation: ") {
				out.Channel = strings.TrimSpace(strings.TrimPrefix(line, "installation: "))
			}
		}
		out.Version = strings.TrimSpace(strings.Split(version, "\n")[0])
	}
	if projectMCPAgent(agent) {
		help, e := command("mcp", "add", "--help")
		if e == nil && strings.Contains(help, "--url") && strings.Contains(help, "COMMAND") {
			out.Protocol = "mcp"
		} else {
			out.Error = "此版本未验证 stdio MCP 支持"
		}
	} else {
		help, e := command("--help")
		if e == nil && strings.Contains(help, "--extension") {
			out.Protocol = "extension"
		} else {
			out.Error = "此版本未验证 Extension 支持"
		}
	}
	return out
}
