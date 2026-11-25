package agents

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

type DetectedHost struct {
	Kind                  string `json:"kind"`
	DisplayName           string `json:"display_name"`
	Detected              bool   `json:"detected"`
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
}

type HostKind = string

const (
	HostTraeIDE     HostKind = "trae_ide"
	HostCodex       HostKind = "codex"
	HostPi          HostKind = "pi"
	HostTraeX       HostKind = "traex"
	HostAntigravity HostKind = "antigravity"
	HostClaudeCode  HostKind = "claude_code"
	HostCursor      HostKind = "cursor"
	HostOpenCode    HostKind = "opencode"
)

type HostOverride struct{ Executable, ProfileDir string }
type DiscoveryOptions struct {
	Home             string
	AppDirs          []string
	LookPath         func(string) (string, error)
	Environ          func(string) string
	Kinds            []string
	RuntimeOverrides map[string]HostOverride
}
type HostRuntime struct{ Kind, Executable, ProfileDir string }

type HostSpec struct {
	Kind, DisplayName                     string
	Commands, Apps                        []string
	BundleExecutable, ProfileRelativePath string
	ProfileEnvironment                    []string
	SupportsCapture                       bool
	SupportsProcessingCLI                 bool
	BundlesProcessingCLI                  bool
	InstallCommand                        string
	DocsURL                               string
}

var hostSpecs = []HostSpec{
	{
		Kind: "trae_ide", DisplayName: "TRAE CN",
		Apps: []string{"Trae CN.app", "TRAE CN.app"}, BundleExecutable: "Contents/MacOS/Electron",
		ProfileRelativePath: ".trae-cn", SupportsCapture: true, DocsURL: "https://www.trae.cn/",
	},
	{
		Kind: "codex", DisplayName: "Codex",
		Commands: []string{"codex"}, Apps: []string{"Codex.app", "ChatGPT.app"}, BundleExecutable: "Contents/Resources/codex-cli/bin/codex",
		ProfileRelativePath: ".codex", ProfileEnvironment: []string{"CODEX_HOME"},
		SupportsCapture: true, SupportsProcessingCLI: true, BundlesProcessingCLI: true,
		InstallCommand: "npm i -g @openai/codex",
		DocsURL:        "https://developers.openai.com/codex/cli",
	},
	{
		Kind: "pi", DisplayName: "pi",
		Commands: []string{"pi"}, ProfileRelativePath: ".pi/agent", ProfileEnvironment: []string{"PI_CODING_AGENT_DIR"},
		SupportsCapture: true, SupportsProcessingCLI: true,
		InstallCommand: "npm i -g @mariozechner/pi-coding-agent",
		DocsURL:        "https://github.com/badlogic/pi-mono",
	},
	{
		Kind: "traex", DisplayName: "TRAE CLI",
		Commands: []string{"traecli", "traex", "trae-cli"}, ProfileRelativePath: ".trae", ProfileEnvironment: []string{"TRAE_HOME", "TRAECLI_HOME"},
		SupportsCapture: true, SupportsProcessingCLI: true,
		InstallCommand: "curl -fsSL https://lf-cdn.trae.com.cn/obj/trae-com-cn/pkg/cli/install.sh | bash",
		DocsURL:        "https://www.trae.cn/",
	},
	{
		Kind: "antigravity", DisplayName: "Antigravity",
		Commands: []string{"agy"}, Apps: []string{"Gemini.app"}, BundleExecutable: "Contents/Helpers/agy",
		ProfileRelativePath: ".gemini/config", SupportsCapture: true, SupportsProcessingCLI: true, BundlesProcessingCLI: true,
		DocsURL: "https://gemini.google.com/",
	},
	{
		Kind: "claude_code", DisplayName: "Claude Code", Commands: []string{"claude"}, Apps: []string{"Claude.app"},
		ProfileRelativePath: ".claude", ProfileEnvironment: []string{"CLAUDE_CONFIG_DIR"}, SupportsCapture: true,
	},
	{
		Kind: "cursor", DisplayName: "Cursor", Commands: []string{"cursor"}, Apps: []string{"Cursor.app"},
		BundleExecutable: "Contents/Resources/app/bin/cursor", ProfileRelativePath: ".cursor", SupportsCapture: true,
	},
	{
		Kind: "opencode", DisplayName: "OpenCode", Commands: []string{"opencode"}, Apps: []string{"OpenCode.app"},
		ProfileRelativePath: ".config/opencode", ProfileEnvironment: []string{"OPENCODE_CONFIG_DIR"}, SupportsCapture: true,
	},
}

func HostSpecification(kind HostKind) (HostSpec, bool) {
	for _, spec := range hostSpecs {
		if spec.Kind != kind {
			continue
		}
		spec.Commands = slices.Clone(spec.Commands)
		spec.Apps = slices.Clone(spec.Apps)
		spec.ProfileEnvironment = slices.Clone(spec.ProfileEnvironment)
		return spec, true
	}
	return HostSpec{}, false
}

func AllowedKinds() []string {
	kinds := make([]string, 0, len(hostSpecs))
	for _, spec := range hostSpecs {
		kinds = append(kinds, spec.Kind)
	}
	return kinds
}

func NormalizeKinds(kinds []string) []string {
	selected := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		selected[kind] = true
	}
	var result []string
	for _, spec := range hostSpecs {
		if selected[spec.Kind] {
			result = append(result, spec.Kind)
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
	raw, err := io.ReadAll(io.LimitReader(f, maxPlistBytes+1))
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

func normalizeDiscovery(opts DiscoveryOptions) DiscoveryOptions {
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
	return opts
}

func DetectHosts(opts DiscoveryOptions) []DetectedHost {
	opts = normalizeDiscovery(opts)
	selected := opts.Kinds
	if selected == nil {
		selected = AllowedKinds()
	}
	selected = NormalizeKinds(selected)
	var hosts []DetectedHost
	for _, spec := range hostSpecs {
		if !slices.Contains(selected, spec.Kind) {
			continue
		}
		host := detectHostInstallation(spec, opts.AppDirs, opts.LookPath, opts.Home)
		host.ProfileDir = profileDir(spec, opts)
		if override := opts.RuntimeOverrides[spec.Kind].Executable; override != "" {
			host.Executable = checkedOverride(override)
			host.Detected = host.Executable != "" || host.Application != ""
		}
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

func profileDir(spec HostSpec, opts DiscoveryOptions) string {
	if p := opts.RuntimeOverrides[spec.Kind].ProfileDir; p != "" {
		return p
	}
	for _, key := range spec.ProfileEnvironment {
		if value := opts.Environ(key); value != "" {
			if strings.HasPrefix(value, "~/") && opts.Home != "" {
				return filepath.Join(opts.Home, value[2:])
			}
			return value
		}
	}
	if spec.Kind == "opencode" {
		if dir := opts.Environ("XDG_CONFIG_HOME"); dir != "" {
			return filepath.Join(dir, "opencode")
		}
	}
	if opts.Home == "" {
		return ""
	}
	return filepath.Join(opts.Home, spec.ProfileRelativePath)
}

func isHostExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 && syscall.Access(path, 1) == nil
}

func detectHostInstallation(spec HostSpec, dirs []string, lookup func(string) (string, error), home string) DetectedHost {
	host := DetectedHost{
		Kind: spec.Kind, DisplayName: spec.DisplayName,
		SupportsCapture:       spec.SupportsCapture,
		SupportsProcessingCLI: spec.SupportsProcessingCLI,
		BundlesProcessingCLI:  spec.BundlesProcessingCLI,
		InstallCommand:        spec.InstallCommand,
		DocsURL:               spec.DocsURL,
	}
	for _, command := range spec.Commands {
		if found, err := lookup(command); err == nil && isHostExecutable(found) {
			host.Executable = found
			break
		}
	}
	if host.Executable == "" && home != "" {
		for _, base := range []string{filepath.Join(home, ".local/bin"), filepath.Join(home, ".bun/bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
			for _, command := range spec.Commands {
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
		if candidate := filepath.Join(home, ".opencode/bin/opencode"); host.Executable == "" && spec.Kind == "opencode" && isHostExecutable(candidate) {
			host.Executable = candidate
		}
	}
	for _, dir := range dirs {
		for _, name := range spec.Apps {
			app := filepath.Join(dir, name)
			if info, err := os.Stat(filepath.Join(app, "Contents/Info.plist")); host.Application == "" && err == nil && info.Mode().IsRegular() {
				host.Application = app
			}
			if spec.BundleExecutable != "" && host.Executable == "" {
				candidate := filepath.Join(app, spec.BundleExecutable)
				if isHostExecutable(candidate) {
					host.Executable = candidate
					host.Application = app
				}
			}
		}
		if host.Application != "" && (host.Executable != "" || spec.BundleExecutable == "") {
			break
		}
	}
	host.Detected = host.Application != "" || host.Executable != ""
	return host
}
func checkedOverride(path string) string {
	if filepath.IsAbs(path) && isHostExecutable(path) {
		return path
	}
	return ""
}

func DetectExecutable(kind, override string, opts DiscoveryOptions) string {
	if override == "" {
		override = opts.RuntimeOverrides[kind].Executable
	}
	if override != "" {
		if !slices.Contains(AllowedKinds(), kind) {
			return ""
		}
		return checkedOverride(override)
	}
	opts = normalizeDiscovery(opts)
	for _, spec := range hostSpecs {
		if spec.Kind == kind {
			return detectHostInstallation(spec, opts.AppDirs, opts.LookPath, opts.Home).Executable
		}
	}
	return ""
}

func DefaultProfileDir(kind string, opts DiscoveryOptions) string {
	opts = normalizeDiscovery(opts)
	opts.RuntimeOverrides = nil
	for _, spec := range hostSpecs {
		if spec.Kind == kind {
			return profileDir(spec, opts)
		}
	}
	return ""
}

func ResolveHostRuntime(kind string, opts DiscoveryOptions) HostRuntime {
	profile := opts.RuntimeOverrides[kind].ProfileDir
	if profile == "" {
		profile = DefaultProfileDir(kind, opts)
	}
	return HostRuntime{Kind: kind, Executable: DetectExecutable(kind, "", opts), ProfileDir: profile}
}
