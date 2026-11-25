package bootstrap

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type DetectedHost struct {
	Kind                  string `json:"kind"`
	DisplayName           string `json:"display_name"`
	Detected              bool   `json:"detected"`
	BetaPrimary           bool   `json:"beta_primary"`
	Executable            string `json:"executable,omitempty"`
	Application           string `json:"application,omitempty"`
	Version               string `json:"version,omitempty"`
	ProfileDir            string `json:"profile_dir,omitempty"`
	ProfileFound          bool   `json:"profile_found"`
	SupportsCapture       bool   `json:"supports_capture"`
	SupportsProcessingCLI bool   `json:"supports_processing_cli"`
	BundlesProcessingCLI  bool   `json:"bundles_processing_cli,omitempty"`
	InstallCommand        string `json:"install_command,omitempty"`
	DocsURL               string `json:"docs_url,omitempty"`
	LoginHint             string `json:"login_hint,omitempty"`
}

type RecommendedProcessingCLI struct {
	Kind                 string `json:"kind"`
	DisplayName          string `json:"display_name"`
	BundlesProcessingCLI bool   `json:"bundles_processing_cli,omitempty"`
	InstallCommand       string `json:"install_command,omitempty"`
	DocsURL              string `json:"docs_url,omitempty"`
	LoginHint            string `json:"login_hint,omitempty"`
}

type HostDetectOptions struct {
	Home            string
	AppDirs         []string
	LookPath        func(string) (string, error)
	Environ         func(string) string
	BetaPrimaryOnly bool
}

type localHostSpec struct {
	kind, name                string
	primary                   bool
	commands, apps            []string
	bundleExecutable, profile string
	profileEnv                []string
	supportsCapture           bool
	supportsProcessingCLI     bool
	bundlesProcessingCLI      bool
	installCommand            string
	docsURL                   string
	loginHint                 string
}

var localHostSpecs = []localHostSpec{
	{
		kind: "trae_ide", name: "TRAE CN", primary: true,
		apps: []string{"Trae CN.app", "TRAE CN.app"}, bundleExecutable: "Contents/MacOS/Electron",
		profile: ".trae-cn", supportsCapture: true,
	},
	{
		kind: "codex", name: "Codex", primary: true,
		commands: []string{"codex"}, apps: []string{"Codex.app", "ChatGPT.app"}, bundleExecutable: "Contents/Resources/codex-cli/bin/codex",
		profile: ".codex", profileEnv: []string{"CODEX_HOME"},
		supportsCapture: true, supportsProcessingCLI: true, bundlesProcessingCLI: true,
		installCommand: "npm i -g @openai/codex",
		docsURL:        "https://developers.openai.com/codex/cli",
		loginHint:      "安装后在终端运行 codex 完成登录",
	},
	{
		kind: "pi", name: "pi", primary: true,
		commands: []string{"pi"}, profile: ".pi/agent", profileEnv: []string{"PI_CODING_AGENT_DIR"},
		supportsCapture: true, supportsProcessingCLI: true,
		installCommand: "npm i -g @mariozechner/pi-coding-agent",
		docsURL:        "https://github.com/badlogic/pi-mono",
		loginHint:      "安装后运行 pi 并配置模型登录",
	},
	{
		kind: "traex", name: "TRAE CLI", primary: true,
		commands: []string{"traecli", "traex", "trae-cli"}, profile: ".trae", profileEnv: []string{"TRAE_HOME", "TRAECLI_HOME"},
		supportsCapture: true, supportsProcessingCLI: true,
		installCommand: "curl -fsSL https://lf-cdn.trae.com.cn/obj/trae-com-cn/pkg/cli/install.sh | bash",
		docsURL:        "https://www.trae.cn/",
		loginHint:      "安装后运行 traecli / traex 完成登录",
	},
	{
		kind: "antigravity", name: "Antigravity", primary: true,
		commands: []string{"agy"}, apps: []string{"Gemini.app"}, bundleExecutable: "Contents/Helpers/agy",
		profile: ".gemini/config", supportsCapture: true, supportsProcessingCLI: true, bundlesProcessingCLI: true,
		docsURL:   "https://gemini.google.com/",
		loginHint: "安装并登录 Gemini App 后即可使用内置 agy CLI",
	},
	{
		kind: "claude_code", name: "Claude Code", commands: []string{"claude"}, apps: []string{"Claude.app"},
		profile: ".claude", profileEnv: []string{"CLAUDE_CONFIG_DIR"}, supportsCapture: true,
	},
	{
		kind: "cursor", name: "Cursor", commands: []string{"cursor"}, apps: []string{"Cursor.app"},
		bundleExecutable: "Contents/Resources/app/bin/cursor", profile: ".cursor", supportsCapture: true,
	},
	{
		kind: "opencode", name: "OpenCode", commands: []string{"opencode"}, apps: []string{"OpenCode.app"},
		profile: ".config/opencode", profileEnv: []string{"OPENCODE_CONFIG_DIR"}, supportsCapture: true,
	},
}

func RecommendedProcessingCLIs() []RecommendedProcessingCLI {
	var recommendations []RecommendedProcessingCLI
	for _, spec := range localHostSpecs {
		if spec.supportsProcessingCLI {
			recommendations = append(recommendations, RecommendedProcessingCLI{
				Kind:                 spec.kind,
				DisplayName:          spec.name,
				BundlesProcessingCLI: spec.bundlesProcessingCLI,
				InstallCommand:       spec.installCommand,
				DocsURL:              spec.docsURL,
				LoginHint:            spec.loginHint,
			})
		}
	}
	return recommendations
}

func HasDetectedProcessingCLI(hosts []DetectedHost) bool {
	for _, host := range hosts {
		if host.Detected && host.SupportsProcessingCLI && host.Executable != "" {
			return true
		}
	}
	return false
}

func AllowedAgentKinds() []string {
	kinds := make([]string, 0, len(localHostSpecs))
	for _, spec := range localHostSpecs {
		kinds = append(kinds, spec.kind)
	}
	return kinds
}

func NormalizeSelectedAgents(kinds []string) []string {
	selected := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		selected[kind] = true
	}
	var result []string
	for _, spec := range localHostSpecs {
		if selected[spec.kind] {
			result = append(result, spec.kind)
		}
	}
	return result
}

// ReadBundleVersion reads version strings from the root XML plist dictionary.
func ReadBundleVersion(appPath string) string {
	const maxPlistBytes = 2 << 20
	f, err := os.OpenFile(filepath.Join(appPath, "Contents/Info.plist"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPlistBytes {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxPlistBytes))
	if err != nil || int64(len(raw)) != info.Size() {
		return ""
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
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&plist); err != nil {
		return ""
	}
	var fallback string
	entries := plist.Dict.Entries
	for i, entry := range entries {
		if entry.XMLName.Local != "key" || i+1 == len(entries) || entries[i+1].XMLName.Local != "string" {
			continue
		}
		version := strings.TrimSpace(entries[i+1].Value)
		switch strings.TrimSpace(entry.Value) {
		case "CFBundleShortVersionString":
			if version != "" {
				return version
			}
		case "CFBundleVersion":
			fallback = version
		}
	}
	return fallback
}

func DetectLocalHosts(opts HostDetectOptions) []DetectedHost {
	if opts.Home == "" {
		opts.Home, _ = os.UserHomeDir()
	}
	if opts.AppDirs == nil {
		opts.AppDirs = defaultHostAppDirs(opts.Home)
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Environ == nil {
		opts.Environ = os.Getenv
	}
	var hosts []DetectedHost
	for _, spec := range localHostSpecs {
		if opts.BetaPrimaryOnly && !spec.primary {
			continue
		}
		host := detectHostInstallation(spec, opts.AppDirs, opts.LookPath, opts.Home)
		host.ProfileDir = detectHostProfile(spec, opts)
		if info, err := os.Stat(host.ProfileDir); err == nil {
			host.ProfileFound = info.IsDir()
		}
		if host.Application != "" {
			host.Version = ReadBundleVersion(host.Application)
		}
		hosts = append(hosts, host)
	}
	return hosts
}

func defaultHostAppDirs(home string) []string {
	dirs := []string{"/Applications"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

func detectHostProfile(spec localHostSpec, opts HostDetectOptions) string {
	for _, key := range spec.profileEnv {
		if value := opts.Environ(key); value != "" {
			if strings.HasPrefix(value, "~/") && opts.Home != "" {
				return filepath.Join(opts.Home, value[2:])
			}
			return value
		}
	}
	if spec.kind == "opencode" {
		if dir := opts.Environ("XDG_CONFIG_HOME"); dir != "" {
			return filepath.Join(dir, "opencode")
		}
	}
	if opts.Home == "" {
		return ""
	}
	return filepath.Join(opts.Home, spec.profile)
}

func isHostExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 && syscall.Access(path, 1) == nil
}

func detectHostInstallation(spec localHostSpec, dirs []string, lookup func(string) (string, error), home string) DetectedHost {
	host := DetectedHost{
		Kind: spec.kind, DisplayName: spec.name, BetaPrimary: spec.primary,
		SupportsCapture:       spec.supportsCapture,
		SupportsProcessingCLI: spec.supportsProcessingCLI,
		BundlesProcessingCLI:  spec.bundlesProcessingCLI,
		InstallCommand:        spec.installCommand,
		DocsURL:               spec.docsURL,
		LoginHint:             spec.loginHint,
	}
	for _, command := range spec.commands {
		if found, err := lookup(command); err == nil && found != "" {
			host.Executable = found
			break
		}
	}
	if host.Executable == "" && home != "" {
		for _, base := range []string{filepath.Join(home, ".local/bin"), filepath.Join(home, ".bun/bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
			for _, command := range spec.commands {
				candidate := filepath.Join(base, command)
				if isHostExecutable(candidate) {
					host.Executable = candidate
					break
				}
			}
			if host.Executable != "" {
				break
			}
		}
		if candidate := filepath.Join(home, ".opencode/bin/opencode"); host.Executable == "" && spec.kind == "opencode" && isHostExecutable(candidate) {
			host.Executable = candidate
		}
	}
	for _, dir := range dirs {
		for _, name := range spec.apps {
			app := filepath.Join(dir, name)
			if info, err := os.Stat(filepath.Join(app, "Contents/Info.plist")); host.Application == "" && err == nil && info.Mode().IsRegular() {
				host.Application = app
			}
			if spec.bundleExecutable != "" && host.Executable == "" {
				candidate := filepath.Join(app, spec.bundleExecutable)
				if isHostExecutable(candidate) {
					host.Executable = candidate
					host.Application = app
				}
			}
		}
		if host.Application != "" && (host.Executable != "" || spec.bundleExecutable == "") {
			break
		}
	}
	host.Detected = host.Application != "" || host.Executable != ""
	return host
}
