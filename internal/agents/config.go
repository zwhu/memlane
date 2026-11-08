package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/records"
)

func projectMCPAgent(agent string) bool { return agent == "codex" || agent == "traex" }

var installationMu sync.Mutex

//go:embed pi-extension.ts
var piExtension string

type InstallOptions struct {
	Project     string // explicit App verification scope; pi daily context still comes from ctx.cwd
	ClientRoot  string
	ServiceRoot string
	Bridge      string
	AgentPath   string
	Workspace   string // Codex's explicit project binding
	PiDirectory string // known user directory, or isolated validation directory
}

type Installation struct {
	IDEChallenge       *records.IDEVerification        `json:"ide_challenge,omitempty"`
	IDEReceipt         *records.IDEVerificationReceipt `json:"ide_receipt,omitempty"`
	ToolsetVersion     int                             `json:"toolset_version,omitempty"`
	AgentChannel       string                          `json:"agent_channel,omitempty"`
	AgentVersion       string                          `json:"agent_version,omitempty"`
	Connection         records.Connection              `json:"connection"`
	Options            InstallOptions                  `json:"options"`
	ConfigPath         string                          `json:"config_path"`
	BackupPath         string                          `json:"backup_path,omitempty"`
	ExtensionPath      string                          `json:"extension_path,omitempty"`
	Block              string                          `json:"block,omitempty"`
	ExtensionHash      string                          `json:"extension_hash,omitempty"`
	VerifiedGeneration int64                           `json:"verified_generation,omitempty"`
	VerifiedAt         string                          `json:"verified_at,omitempty"`
	VerificationError  string                          `json:"verification_error,omitempty"`
}

func SaveVerification(root, id string, generation int64, failure string) error {
	installationMu.Lock()
	defer installationMu.Unlock()
	i, err := LoadInstallation(root, id)
	if err != nil {
		return err
	}
	if i.Connection.Generation != generation {
		return errors.New("connection changed during verification")
	}
	i.VerificationError = failure
	i.VerifiedAt = ""
	i.VerifiedGeneration = 0
	if failure == "" {
		i.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
		i.VerifiedGeneration = generation
	}
	data, _ := json.MarshalIndent(i, "", "  ")
	return privateWrite(installationPath(root, id), data, 0600)
}

func CheckInstallation(i Installation) error {
	if info, err := os.Stat(i.Options.AgentPath); err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
		return errors.New("Agent 可执行文件已不可用，请选择安装路径后重新连接")
	}
	data, _, err := readConfig(i.ConfigPath)
	if err != nil {
		return err
	}
	if i.Connection.Agent == "trae" {
		return checkIDEConfig(data, i.Block)
	}
	if projectMCPAgent(i.Connection.Agent) {
		if i.Block == "" {
			return errors.New("Memlane 配置回执不完整，请重新连接")
		}
		if !bytes.Contains(data, []byte(i.Block)) {
			return errors.New("Memlane 配置已变更，请重新连接")
		}
	} else {
		var settings map[string]json.RawMessage
		if err = json.Unmarshal(data, &settings); err != nil {
			return err
		}
		var paths []string
		if err = json.Unmarshal(settings["extensions"], &paths); err != nil {
			return err
		}
		found := false
		for _, path := range paths {
			if path == i.ExtensionPath {
				found = true
			}
		}
		if !found {
			return errors.New("pi 尚未加载 Memlane 配置，请重新连接")
		}
		source, e := os.ReadFile(i.ExtensionPath)
		if e != nil || hash(source) != i.ExtensionHash {
			return errors.New("Memlane Extension 已变更，请重新连接")
		}
	}
	return nil
}

func installationPath(root, id string) string {
	return filepath.Join(root, "agent-adapters", id, "installation.json")
}
func LoadInstallation(root, id string) (Installation, error) {
	var out Installation
	data, err := os.ReadFile(installationPath(root, id))
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func privateWrite(path string, data []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute configuration path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".memlane-config-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readConfig(path string) ([]byte, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, 0600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if len(data) > 1024*1024 {
		return nil, 0, errors.New("configuration is too large")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}
func backup(path string, data []byte) (string, error) {
	if data == nil {
		return "", nil
	}
	name := path + ".memlane-" + time.Now().UTC().Format("20060102T150405.000000000") + ".bak"
	return name, privateWrite(name, data, 0600)
}
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func validateOptions(c records.Connection, o InstallOptions) error {
	for _, path := range []string{o.ClientRoot, o.ServiceRoot, o.Bridge, o.AgentPath} {
		if !filepath.IsAbs(path) {
			return errors.New("absolute bridge, Agent and client paths required")
		}
	}
	if c.ID == "" || c.Generation < 1 {
		return errors.New("registered connection required")
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return errors.New("invalid connection ID")
	}
	for _, path := range []string{o.Bridge, o.AgentPath} {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
			return errors.New("bridge or Agent executable is unavailable")
		}
	}
	if (projectMCPAgent(c.Agent) || c.Agent == "trae") && !filepath.IsAbs(o.Workspace) {
		return errors.New("MCP CLI requires a selected project workspace")
	}
	if c.Agent == "pi" && !filepath.IsAbs(o.PiDirectory) {
		return errors.New("pi configuration directory required")
	}
	return nil
}

// Install is invoked only by an explicit App Connect/Apply action. It never
// scans or changes settings during discovery, and backs up existing files.
func Install(ctx context.Context, c records.Connection, o InstallOptions) (result Installation, retErr error) {
	installationMu.Lock()
	defer installationMu.Unlock()
	if err := validateOptions(c, o); err != nil {
		return Installation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Installation{}, err
	}
	probe := Probe(ctx, c.Agent, o.AgentPath)
	if probe.Error != "" {
		return Installation{}, errors.New(probe.Error)
	}
	prior, priorErr := LoadInstallation(o.ClientRoot, c.ID)
	if priorErr != nil && !os.IsNotExist(priorErr) {
		return Installation{}, priorErr
	}
	if priorErr == nil && (prior.Connection.Agent != c.Agent || ((projectMCPAgent(c.Agent) || c.Agent == "trae") && prior.Options.Workspace != o.Workspace) || prior.Options.PiDirectory != o.PiDirectory) {
		return Installation{}, errors.New("disconnect before changing Agent configuration location")
	}
	i := Installation{Connection: c, Options: o, ToolsetVersion: 2}
	i.AgentVersion = probe.Version
	i.AgentChannel = probe.Channel
	var content []byte
	var originalConfig []byte
	type edited struct {
		path          string
		before, after []byte
		mode          os.FileMode
	}
	edits := []edited{}
	write := func(path string, data []byte, mode os.FileMode) error {
		before, oldMode, e := readConfig(path)
		if e != nil {
			return e
		}
		if e = privateWrite(path, data, mode); e != nil {
			return e
		}
		edits = append(edits, edited{path, before, data, oldMode})
		return nil
	}
	defer func() {
		if retErr == nil {
			return
		}
		for index := len(edits) - 1; index >= 0; index-- {
			edit := edits[index]
			current, _, e := readConfig(edit.path)
			if e != nil || !bytes.Equal(current, edit.after) {
				retErr = errors.Join(retErr, errors.New("configuration changed during rollback; external changes preserved"))
				continue
			}
			if edit.before == nil {
				e = os.Remove(edit.path)
			} else {
				e = privateWrite(edit.path, edit.before, edit.mode)
			}
			retErr = errors.Join(retErr, e)
		}
	}()
	var mode os.FileMode
	var err error
	if projectMCPAgent(c.Agent) {
		i.ConfigPath = filepath.Join(o.Workspace, ".codex/config.toml")
		if c.Agent == "traex" {
			i.ConfigPath = filepath.Join(o.Workspace, ".trae/traecli.toml")
		}
		if target, e := filepath.EvalSymlinks(i.ConfigPath); e == nil {
			i.ConfigPath = target
		}
		content, mode, err = readConfig(i.ConfigPath)
		if err != nil {
			return i, err
		}
		originalConfig = content
		var parsed map[string]any
		if err = toml.Unmarshal(content, &parsed); err != nil {
			return i, errors.New("Agent project TOML is invalid; configuration preserved")
		}
		if priorErr == nil {
			if !bytes.Contains(content, []byte(prior.Block)) {
				return i, errors.New("Memlane MCP entry was changed externally; configuration preserved")
			}
			content = bytes.Replace(content, []byte(prior.Block), nil, 1)
		} else if servers, ok := parsed["mcp_servers"].(map[string]any); ok && servers["memlane"] != nil {
			return i, errors.New("an unrelated memlane MCP entry already exists; configuration preserved")
		}
		args := []string{"mcp", "--root", o.ServiceRoot, "--connection", c.ID, "--generation", fmt.Sprint(c.Generation), "--workspace", o.Workspace}
		// JSON strings/arrays are valid TOML basic strings/arrays, with all
		// shell-looking text preserved as literal configuration values.
		command, _ := json.Marshal(o.Bridge)
		arguments, _ := json.Marshal(args)
		i.Block = fmt.Sprintf("\n# Memlane %s begin\n[mcp_servers.memlane]\ncommand = %s\nargs = %s\n[mcp_servers.memlane.tools.memory_save]\napproval_mode = \"approve\"\n# Memlane %s end\n", c.ID, command, arguments, c.ID)
		content = append(content, []byte(i.Block)...)
		if err = toml.Unmarshal(content, &parsed); err != nil {
			return i, errors.New("Memlane configuration would conflict with Agent TOML; original preserved")
		}
	} else if c.Agent == "trae" {
		i.ConfigPath = filepath.Join(o.Workspace, ".trae", "mcp.json")
		if target, e := filepath.EvalSymlinks(i.ConfigPath); e == nil {
			i.ConfigPath = target
		}
		content, mode, err = readConfig(i.ConfigPath)
		if err != nil {
			return i, err
		}
		originalConfig = content
		args := []string{"mcp", "--root", o.ServiceRoot, "--connection", c.ID, "--generation", fmt.Sprint(c.Generation), "--workspace", o.Workspace}
		entry, _ := json.Marshal(map[string]any{"command": o.Bridge, "args": args, "cwd": o.Workspace, "disabled": false})
		owned := ""
		if priorErr == nil {
			owned = prior.Block
		}
		content, i.Block, err = editIDEConfig(content, owned, entry)
		if err != nil {
			return i, err
		}
	} else if c.Agent == "pi" {
		i.ConfigPath = filepath.Join(o.PiDirectory, "settings.json")
		if target, e := filepath.EvalSymlinks(i.ConfigPath); e == nil {
			i.ConfigPath = target
		}
		content, mode, err = readConfig(i.ConfigPath)
		if err != nil {
			return i, err
		}
		originalConfig = content
		settings := map[string]json.RawMessage{}
		if len(content) > 0 {
			if err = json.Unmarshal(content, &settings); err != nil || settings == nil {
				return i, errors.New("pi settings.json is invalid; configuration preserved")
			}
		}
		exts := []string{}
		if raw := settings["extensions"]; len(raw) > 0 && string(raw) != "null" {
			if err = json.Unmarshal(raw, &exts); err != nil {
				return i, errors.New("pi extensions must be a path array; settings preserved")
			}
		}
		i.ExtensionPath = filepath.Join(o.ClientRoot, "agent-adapters", c.ID, "memlane.ts")
		if os.IsNotExist(priorErr) {
			if _, e := os.Stat(i.ExtensionPath); e == nil {
				return i, errors.New("an existing unowned Extension file is preserved; create a new connection")
			}
		}
		if priorErr == nil {
			old, e := os.ReadFile(prior.ExtensionPath)
			if e != nil || hash(old) != prior.ExtensionHash {
				return i, errors.New("Memlane Extension was changed externally; preserved")
			}
		}
		cfg, _ := json.Marshal(map[string]any{"bridge": o.Bridge, "root": o.ServiceRoot, "connection_id": c.ID, "generation": c.Generation})
		source := []byte(strings.ReplaceAll(piExtension, "__MEMLANE_CONFIG__", string(cfg)))
		i.ExtensionHash = hash(source)
		found := false
		for _, path := range exts {
			if path == i.ExtensionPath {
				found = true
			}
		}
		if !found {
			exts = append(exts, i.ExtensionPath)
		}
		settings["extensions"], _ = json.Marshal(exts)
		content, err = json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return i, err
		}
		content = append(content, '\n')
		if err = write(i.ExtensionPath, source, 0600); err != nil {
			return i, err
		}
	} else {
		return i, errors.New("unsupported Agent")
	}
	original, _, err := readConfig(i.ConfigPath)
	if err != nil {
		return i, err
	}
	if !bytes.Equal(original, originalConfig) {
		return i, errors.New("Agent configuration changed while connecting; changes preserved, retry")
	}
	if i.BackupPath, err = backup(i.ConfigPath, original); err != nil {
		return i, err
	}
	if err = write(i.ConfigPath, content, mode); err != nil {
		return i, err
	}
	meta, _ := json.MarshalIndent(i, "", "  ")
	if err = privateWrite(installationPath(o.ClientRoot, c.ID), meta, 0600); err != nil {
		return i, err
	}
	return i, nil
}

// Uninstall removes only the exact owned entry. External edits cause a
// conflict, leaving unrelated user configuration and changed files untouched.
func Uninstall(ctx context.Context, root, id string) error {
	installationMu.Lock()
	defer installationMu.Unlock()
	i, err := LoadInstallation(root, id)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	content, mode, err := readConfig(i.ConfigPath)
	if err != nil {
		return err
	}
	originalConfig := content
	if i.Connection.Agent == "trae" {
		content, _, err = editIDEConfig(content, i.Block, nil)
		if err != nil {
			return err
		}
	} else if projectMCPAgent(i.Connection.Agent) {
		if !bytes.Contains(content, []byte(i.Block)) {
			return errors.New("Memlane MCP entry changed externally; configuration preserved")
		}
		content = bytes.Replace(content, []byte(i.Block), nil, 1)
	} else {
		settings := map[string]json.RawMessage{}
		if err = json.Unmarshal(content, &settings); err != nil {
			return err
		}
		exts := []string{}
		if raw := settings["extensions"]; len(raw) > 0 {
			if err = json.Unmarshal(raw, &exts); err != nil {
				return err
			}
		}
		kept := []string{}
		for _, path := range exts {
			if path != i.ExtensionPath {
				kept = append(kept, path)
			}
		}
		settings["extensions"], _ = json.Marshal(kept)
		content, err = json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return err
		}
		content = append(content, '\n')
		// The configured path is exclusively owned. Preserve a modified
		// Extension on disk even after removing its settings reference.
	}
	current, _, err := readConfig(i.ConfigPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, originalConfig) {
		return errors.New("Agent configuration changed while disconnecting; changes preserved, retry")
	}
	if _, err = backup(i.ConfigPath, originalConfig); err != nil {
		return err
	}
	if err = privateWrite(i.ConfigPath, content, mode); err != nil {
		return err
	}
	if i.ExtensionPath != "" {
		if source, e := os.ReadFile(i.ExtensionPath); e == nil && hash(source) == i.ExtensionHash {
			if err = os.Remove(i.ExtensionPath); err != nil {
				return err
			}
		}
	}
	return os.Remove(installationPath(root, id))
}
