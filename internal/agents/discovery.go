// Package agents discovers and adapts supported local Agent installations.
package agents

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
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
	// CapabilityError describes an installed Agent whose connection format has
	// not been verified. It must block configuration without hiding installation.
	CapabilityError string `json:"capability_error,omitempty"`
}

func (d Discovery) ConnectionError() error {
	if d.Error != "" {
		return errors.New(d.Error)
	}
	if d.CapabilityError != "" {
		return errors.New(d.CapabilityError)
	}
	return nil
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
			pathsForAgent = append(pathsForAgent, "/Applications/Codex.app/Contents/Resources/codex",
				"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
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
		appRoot := filepath.Dir(filepath.Dir(resolved))
		var product struct{ ApplicationName, Commit, Quality string }
		data, e := readProbeMetadata(filepath.Join(appRoot, "product.json"))
		if e != nil || json.Unmarshal(data, &product) != nil {
			out.Error = "无法读取 TRAE IDE 产品信息"
			return out
		}
		data, e = readProbeMetadata(filepath.Join(filepath.Dir(filepath.Dir(appRoot)), "Info.plist"))
		version, bundle, e := traeBundleVersion(data, e)
		if e != nil || product.ApplicationName != "trae-cn" || bundle != "cn.trae.app" {
			out.Error = "无法确认 TRAE IDE 产品版本和渠道"
			return out
		}
		out.Version, out.Channel = version, "cn/"+product.Quality
		if version != "3.4.14" || product.Quality != "stable" || product.Commit != "5d9926ed75dce7fa36dff87dbef5fb53e06b790a" {
			out.CapabilityError = "已发现安装；此 IDE 渠道或版本的项目 MCP 格式尚未验证。"
			return out
		}
		launcherVersion, e := command("--version", "--user-data-dir", filepath.Join(isolated, "profile"), "--extensions-dir", filepath.Join(isolated, "extensions"))
		help, h := command("--help")
		// The installed CN launcher prints this harmless preamble with an empty
		// user home. Keep the actual version and commit checks below strict.
		help = strings.TrimPrefix(help, "No config file found\n")
		if e != nil || h != nil || !strings.Contains(launcherVersion, product.Commit) || !strings.HasPrefix(help, "Trae CN 3.4.14\n") || !strings.Contains(help, "--user-data-dir") {
			out.Error = "无法确认此 IDE 的版本和隔离启动能力"
			return out
		}
		out.Protocol = "mcp-ide-external"
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
		} else if e != nil {
			out.Error = "无法检查 Agent 的 MCP 能力"
		} else {
			out.CapabilityError = "此版本未验证 stdio MCP 支持"
		}
	} else {
		help, e := command("--help")
		if e == nil && strings.Contains(help, "--extension") {
			out.Protocol = "extension"
		} else if e != nil {
			out.Error = "无法检查 Agent 的 Extension 能力"
		} else {
			out.CapabilityError = "此版本未验证 Extension 支持"
		}
	}
	return out
}

func readProbeMetadata(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > limit {
		return nil, errors.New("Agent metadata too large")
	}
	return data, err
}

// Read only direct key/string pairs. Other plist values (arrays, booleans or
// nested dictionaries) must not shift keys or supply a nested product version.
func traeBundleVersion(data []byte, readErr error) (string, string, error) {
	if readErr != nil {
		return "", "", readErr
	}
	var plist struct {
		XMLName xml.Name `xml:"plist"`
		Dict    struct {
			Entries []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		return "", "", err
	}
	values := map[string]string{}
	entries := plist.Dict.Entries
	for index := 0; index < len(entries); index += 2 {
		if index+1 >= len(entries) || entries[index].XMLName.Local != "key" {
			return "", "", errors.New("invalid Agent plist dictionary")
		}
		key := entries[index].Value
		if key != "CFBundleShortVersionString" && key != "CFBundleIdentifier" {
			continue
		}
		if _, exists := values[key]; exists || entries[index+1].XMLName.Local != "string" {
			return "", "", errors.New("ambiguous Agent product metadata")
		}
		values[key] = strings.TrimSpace(entries[index+1].Value)
	}
	version, bundle := values["CFBundleShortVersionString"], values["CFBundleIdentifier"]
	if version == "" || bundle == "" {
		return "", "", errors.New("Agent product version missing")
	}
	return version, bundle, nil
}
