//go:build darwin || linux

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/components"
)

// Options are host-owned. Legacy contains all known Memlane installations for
// this client, not data supplied by an Agent. No project, model or login needed.
type OfficialCodexPluginOptions struct {
	Executable, Home, CodexDirectory string
	Manager                          *components.Manager
	Component                        components.Descriptor
	Legacy                           []Installation
	Confirmed                        bool
	// Optional host-observed configuration, independent of install/hook trust.
	CodingMode *OfficialCodexCodingModeOptions
}
type OfficialCodexPluginReceipt struct {
	State     string `json:"state"`
	PluginID  string `json:"plugin_id"`
	Version   string `json:"version"`
	Directory string `json:"directory,omitempty"`
	Reused    bool   `json:"reused"`
}

type pluginCommandOutput struct {
	bytes.Buffer
	Overflow bool
}

func (b *pluginCommandOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 1024*1024 {
		b.Overflow = true
		return n, nil
	}
	b.Buffer.Write(p)
	return n, nil
}
func pluginCLI(ctx context.Context, o OfficialCodexPluginOptions, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, o.Executable, args...)
	cmd.Dir = o.Home
	cmd.Env = []string{"HOME=" + o.Home, "CODEX_HOME=" + o.CodexDirectory, "PATH=" + filepath.Dir(o.Executable) + ":/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(e, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return e
	}
	cmd.WaitDelay = 2 * time.Second
	var out, diagnostic pluginCommandOutput
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	if cmd.Run() != nil || out.Overflow || diagnostic.Overflow {
		return nil, errors.New("official_plugin_command_unconfirmed")
	}
	return out.Bytes(), nil
}

// Read only target-profile configuration, never auth.json or chat history.
// Compare non-owned TOML keys and hooks.json without returning their values.
func pluginHostSnapshot(profile string) (string, error) {
	config := map[string]any{}
	if info, e := os.Lstat(filepath.Join(profile, "config.toml")); e == nil {
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return "", errors.New("host_config_unverified")
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	raw, err := os.ReadFile(filepath.Join(profile, "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(raw) > 1024*1024 {
		return "", errors.New("host_config_budget")
	}
	if len(raw) > 0 {
		if err = toml.Unmarshal(raw, &config); err != nil {
			return "", err
		}
	}
	for key, owned := range map[string]string{"marketplaces": "cognee", "plugins": "cognee@cognee"} {
		if group, ok := config[key].(map[string]any); ok {
			delete(group, owned)
			if len(group) == 0 {
				delete(config, key)
			}
		}
	}
	hooks := []byte(nil)
	path := filepath.Join(profile, "hooks.json")
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return "", errors.New("host_hooks_unverified")
		}
		hooks, e = os.ReadFile(path)
		if e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	value, _ := json.Marshal([]any{config, hash(hooks)})
	return hash(value), nil
}

func verifyInstalledOfficialPlugin(ctx context.Context, root string, metadata components.Metadata) error {
	const prefix = "integrations/codex/plugins/cognee/"
	expected := map[string]components.Asset{}
	for _, asset := range metadata.Files {
		if strings.HasPrefix(asset.Path, prefix) {
			expected[strings.TrimPrefix(asset.Path, prefix)] = asset
		}
	}
	if len(expected) == 0 || len(expected) > 4096 {
		return errors.New("official_plugin_manifest_invalid")
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		asset, ok := expected[filepath.ToSlash(relative)]
		if !ok {
			return errors.New("official_plugin_unlisted_file")
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() != asset.Bytes {
			return errors.New("official_plugin_file_changed")
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		sum := sha256.New()
		_, e = io.Copy(sum, io.LimitReader(f, asset.Bytes+1))
		f.Close()
		if e != nil || hex.EncodeToString(sum.Sum(nil)) != asset.SHA256 {
			return errors.New("official_plugin_file_changed")
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(expected) {
		return errors.New("official_plugin_incomplete")
	}
	return nil
}

// Installs only the verified local official component through Codex's CLI.
// It does not trust hooks, configure service credentials, run a session/model,
// stop old capture, replace other namespaces or delete retained materials.
func InstallOfficialCodexPlugin(ctx context.Context, o OfficialCodexPluginOptions) (OfficialCodexPluginReceipt, error) {
	return officialCodexPlugin(ctx, o, true)
}

// InspectOfficialCodexPlugin resolves persisted host state. It never issues
// plugin add, marketplace add, enable, trust or login commands.
func InspectOfficialCodexPlugin(ctx context.Context, o OfficialCodexPluginOptions) (OfficialCodexPluginReceipt, error) {
	return officialCodexPlugin(ctx, o, false)
}

func officialCodexPlugin(ctx context.Context, o OfficialCodexPluginOptions, install bool) (OfficialCodexPluginReceipt, error) {
	out := OfficialCodexPluginReceipt{State: "not_installed", PluginID: "cognee@cognee"}
	fail := func(code string) (OfficialCodexPluginReceipt, error) { return out, errors.New(code) }
	if install && !o.Confirmed {
		return fail("official_plugin_confirmation_required")
	}
	if o.Manager == nil || o.Component.ID != "cognee-codex-plugin" {
		return fail("official_plugin_component_required")
	}
	for _, path := range []string{o.Executable, o.Home, o.CodexDirectory} {
		if !filepath.IsAbs(path) {
			return fail("official_plugin_explicit_host_required")
		}
	}
	if info, e := os.Stat(o.Executable); e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fail("official_plugin_host_unavailable")
	}
	if info, e := os.Stat(o.Home); e != nil || !info.IsDir() {
		return fail("official_plugin_home_unavailable")
	}
	for _, i := range o.Legacy {
		if !install {
			break
		}
		if i.CaptureConsent != nil && i.CaptureConsent.Enabled {
			if !filepath.IsAbs(i.Options.CodexDirectory) {
				return fail("official_plugin_legacy_scope_unverified")
			}
			if filepath.Clean(i.Options.CodexDirectory) == filepath.Clean(o.CodexDirectory) || samePluginDirectory(i.Options.CodexDirectory, o.CodexDirectory) {
				out.State = "migration_required"
				return fail("official_plugin_legacy_capture_active")
			}
		}
	}
	source, err := o.Manager.Inspect(ctx, o.Component)
	if err != nil {
		return fail("official_plugin_component_unverified")
	}
	out.Version = source.Metadata.Version
	if len(source.Metadata.Links) != 0 {
		return fail("official_plugin_component_links_unsupported")
	}
	if install {
		if err = os.MkdirAll(o.CodexDirectory, 0700); err != nil {
			return fail("official_plugin_profile_unavailable")
		}
	} else if info, e := os.Lstat(o.CodexDirectory); os.IsNotExist(e) {
		return out, nil
	} else if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("official_plugin_profile_unavailable")
	}
	run, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	before, err := pluginHostSnapshot(o.CodexDirectory)
	if err != nil {
		return fail("official_plugin_host_config_unverified")
	}
	raw, err := pluginCLI(run, o, "plugin", "marketplace", "list", "--json")
	if err != nil {
		return fail("official_plugin_marketplace_unread")
	}
	var catalog struct {
		Marketplaces []struct{ Name, Root string } `json:"marketplaces"`
	}
	if json.Unmarshal(raw, &catalog) != nil || catalog.Marketplaces == nil {
		return fail("official_plugin_marketplace_contract_changed")
	}
	found := false
	for _, market := range catalog.Marketplaces {
		if market.Name == "cognee" {
			if found || !samePluginDirectory(market.Root, source.Directory) {
				return fail("official_plugin_marketplace_conflict")
			}
			found = true
		}
	}
	target := filepath.Join(o.CodexDirectory, "plugins", "cache", "cognee", "cognee", out.Version)
	for path := target; path != filepath.Dir(o.CodexDirectory); path = filepath.Dir(path) {
		if info, e := os.Lstat(path); e == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fail("official_plugin_cache_path_unverified")
			}
		} else if !os.IsNotExist(e) {
			return fail("official_plugin_cache_path_unverified")
		}
	}
	if _, e := os.Lstat(target); e == nil {
		if verifyInstalledOfficialPlugin(run, target, source.Metadata) != nil {
			return fail("official_plugin_existing_content_conflict")
		}
		out.Reused = true
	} else if !os.IsNotExist(e) {
		return fail("official_plugin_existing_state_unread")
	}
	raw, err = pluginCLI(run, o, "plugin", "list", "--json")
	var installed struct {
		Installed []struct {
			PluginID, Version  string
			Installed, Enabled bool
			Source             struct{ Source, Path string }
		} `json:"installed"`
	}
	if err != nil || json.Unmarshal(raw, &installed) != nil || installed.Installed == nil {
		return fail("official_plugin_inventory_unverified")
	}
	registered, enabled := false, false
	for _, entry := range installed.Installed {
		if entry.PluginID != out.PluginID {
			continue
		}
		if registered || !found || !out.Reused || !entry.Installed || entry.Version != out.Version || entry.Source.Source != "local" || !samePluginDirectory(entry.Source.Path, filepath.Join(source.Directory, "integrations/codex/plugins/cognee")) {
			return fail("official_plugin_existing_registration_conflict")
		}
		registered, enabled = true, entry.Enabled
	}
	if install && !found {
		if _, err = pluginCLI(run, o, "plugin", "marketplace", "add", source.Directory, "--json"); err != nil {
			out.State = "install_unconfirmed"
			return fail("official_plugin_marketplace_add_unconfirmed")
		}
	}
	if install && !registered {
		raw, err = pluginCLI(run, o, "plugin", "add", "cognee@cognee", "--json")
		out.State = "install_unconfirmed"
		if err != nil {
			return fail("official_plugin_add_unconfirmed")
		}
		var receipt struct{ PluginID, Version, InstalledPath string }
		if json.Unmarshal(raw, &receipt) != nil || receipt.PluginID != out.PluginID || receipt.Version != out.Version || filepath.Clean(receipt.InstalledPath) != target {
			return fail("official_plugin_install_receipt_mismatch")
		}
	}
	if (install || registered) && verifyInstalledOfficialPlugin(run, target, source.Metadata) != nil {
		return fail("official_plugin_installed_content_unverified")
	}
	if _, err = o.Manager.Inspect(run, o.Component); err != nil {
		return fail("official_plugin_source_changed")
	}
	after, err := pluginHostSnapshot(o.CodexDirectory)
	if err != nil || before != after {
		return fail("official_plugin_unrelated_host_config_changed")
	}
	if !install && !registered {
		out.State = "not_installed"
		return out, nil
	}
	out.State = "installed_pending_verification"
	if registered && !enabled {
		out.State = "installed_disabled"
	}
	out.Directory = target
	return out, nil
}

func samePluginDirectory(a, b string) bool {
	x, e := filepath.EvalSymlinks(a)
	if e != nil {
		return false
	}
	y, e := filepath.EvalSymlinks(b)
	return e == nil && x == y
}
