// Package agents discovers and adapts supported local Agent installations.
package agents

import (
	"context"
	"encoding/json"
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
	for _, agent := range []string{"codex", "pi", "traex", "trae"} {
		name := agent
		if agent == "codex" {
			name = "Codex"
		} else if agent == "traex" {
			name = "TRAE CLI"
		} else if agent == "trae" {
			name = "TRAE IDE"
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
		} else if agent == "trae" {
			pathsForAgent = []string{filepath.Join(home, "Applications/Trae CN.app/Contents/Resources/app/bin/trae-cn"), "/Applications/Trae CN.app/Contents/Resources/app/bin/trae-cn"}
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
	} else if agent == "trae" {
		name = "TRAE IDE"
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
	if agent == "trae" {
		// The IDE launcher --version reports its VS Code base version. Bind the
		// supported MCP loader to the actual product commit and product help.
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			out.Error = "TRAE IDE 安装路径不可用"
			return out
		}
		var product struct{ ApplicationName, Commit string }
		data, e := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(resolved)), "product.json"))
		if e != nil || json.Unmarshal(data, &product) != nil || product.ApplicationName != "trae-cn" || product.Commit != "5d9926ed75dce7fa36dff87dbef5fb53e06b790a" {
			out.Error = "此 IDE 渠道或版本的项目 MCP 格式尚未验证"
			return out
		}
		version, e := command("--version", "--user-data-dir", filepath.Join(isolated, "profile"), "--extensions-dir", filepath.Join(isolated, "extensions"))
		help, h := command("--help")
		// The installed CN launcher prints this harmless preamble with an empty
		// user home. Keep the actual version and commit checks below strict.
		help = strings.TrimPrefix(help, "No config file found\n")
		if e != nil || h != nil || !strings.Contains(version, product.Commit) || !strings.HasPrefix(help, "Trae CN 3.4.14\n") || !strings.Contains(help, "--user-data-dir") {
			out.Error = "无法确认此 IDE 的版本和隔离启动能力"
			return out
		}
		out.Version, out.Channel, out.Protocol = "3.4.14", "cn/stable", "mcp-ide-external"
		return out
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
