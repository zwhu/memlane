package hooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/kit/agents"
)

type InstallOptions struct {
	Host, ProfileDir, BinaryPath, GatewayURL, KeyFile string
	Apply                                             bool
	ConflictPolicy                                    *agents.ConflictPolicy
}
type InstallResult struct {
	Host      string
	Blocked   bool
	Conflicts *agents.ConflictReport
	Previews  []agents.EditPreview
	Applied   bool
}

var ErrConflictBlocked = errors.New("third-party Cognee integration blocks host configuration")

func InstallHostConfig(opts InstallOptions) (InstallResult, error)   { return editHost(opts, false) }
func UninstallHostConfig(opts InstallOptions) (InstallResult, error) { return editHost(opts, true) }

const marker = "memlane-hook"

var legacyIDEHook = regexp.MustCompile(`/managed-marketplaces/trae_ide-[a-f0-9]{24}/[a-f0-9]{20}/plugins/cognee/scripts/trae_ide_hook\.py(?:['" ]|$)`)
var legacyMarketplace = regexp.MustCompile(`^cognee@memlane-[a-f0-9]{12}-[a-f0-9]{20}$`)
var legacyNames = []string{"memlane-cognee", "memlane_cognee", "memlane-memory"}

func editHost(opts InstallOptions, remove bool) (InstallResult, error) {
	host := CanonicalHost(opts.Host)
	result := InstallResult{Host: host}
	if host == "" || !filepath.IsAbs(opts.ProfileDir) {
		return result, errors.New("invalid host or profile directory")
	}
	kind := host
	if host == HostTraeIDE {
		kind = agents.HostTraeIDE
	}
	policy := agents.ConflictPolicy{
		ManagedMarkers:   []string{"memlane-cognee", "memlane_cognee", "memlane-memory", "memlane hook", "memlane", "managed-marketplaces/trae_ide-"},
		ConflictKeywords: []string{"cognee"},
	}
	if opts.ConflictPolicy != nil {
		policy = *opts.ConflictPolicy
	}
	report, err := agents.ScanHostConflicts(agents.DetectedHost{Kind: kind, ProfileDir: opts.ProfileDir}, policy)
	result.Conflicts, result.Blocked = &report, report.Blocked
	if err != nil {
		return result, err
	}
	if report.Blocked && !remove {
		return result, ErrConflictBlocked
	}
	if !remove {
		if err := ValidateGatewayURL(opts.GatewayURL); err != nil {
			return result, err
		}
		if !filepath.IsAbs(opts.KeyFile) || host != HostPi && !filepath.IsAbs(opts.BinaryPath) {
			return result, errors.New("absolute executable and key paths required")
		}
	}
	var previews []agents.EditPreview
	binding := filepath.Join(opts.ProfileDir, "extensions", "memlane-cognee", "host-binding.json")
	endpoint := strings.TrimRight(opts.GatewayURL, "/") + "/mcp"
	add := func(p agents.EditPreview, err error) error {
		if err == nil && p.Changed {
			previews = append(previews, p)
		}
		return err
	}
	if host == HostPi {
		err = add(jsonPreview(binding, remove, func(doc object) error {
			if len(doc) != 0 && doc["managed_by"] != "memlane" {
				// Older Pi deployments recorded the Python executable in their binding.
				if _, ok := doc["python"].(string); !ok {
					return errors.New("Pi binding is not managed by Memlane")
				}
			}
			clear(doc)
			if !remove {
				doc["managed_by"], doc["gateway_url"], doc["token_file"] = "memlane", opts.GatewayURL, opts.KeyFile
			}
			return nil
		}))
	} else if host == HostAntigravity {
		err = add(jsonPreview(filepath.Join(opts.ProfileDir, "hooks.json"), remove, func(doc object) error {
			return editAntigravityHooks(doc, opts, remove)
		}))
	} else {
		err = add(jsonPreview(filepath.Join(opts.ProfileDir, "hooks.json"), remove, func(doc object) error {
			return editHookGroups(doc, host, opts, remove)
		}))
	}
	if err != nil {
		return result, err
	}
	switch host {
	case HostCodex, HostTraeX:
		name := "config.toml"
		if host == HostTraeX {
			name = "traecli.toml"
		}
		err = add(tomlPreview(filepath.Join(opts.ProfileDir, name), host, endpoint, opts.KeyFile, remove))
	case HostAntigravity:
		err = add(jsonPreview(filepath.Join(opts.ProfileDir, "mcp_config.json"), remove, func(doc object) error {
			servers, err := configGroup(doc, "mcpServers", !remove)
			if err != nil || servers == nil {
				return err
			}
			for _, key := range legacyNames {
				delete(servers, key)
			}
			if old, ok := servers["memlane"]; ok {
				if !managedServer(old) {
					if remove {
						return nil
					}
					return errors.New("MCP server named memlane already exists")
				}
				delete(servers, "memlane")
			}
			if !remove {
				servers["memlane"] = object{"serverUrl": endpoint, "_memlane_managed": marker}
			}
			return nil
		}))
		if err == nil && !remove {
			err = add(antigravityLegacyPluginPreview(opts.ProfileDir))
		}
	}
	if err != nil {
		return result, err
	}
	result.Previews = previews
	if !opts.Apply {
		return result, nil
	}
	for _, preview := range previews {
		if host == HostPi && remove && preview.Path == binding {
			current, err := agents.SafeReadFile(binding, 1<<20)
			if err != nil {
				return result, err
			}
			if !bytes.Equal(current, preview.Before) {
				return result, errors.New("Pi binding preview is stale")
			}
			if err := os.Remove(binding); err != nil {
				return result, err
			}
		} else if err := applyHostPreview(preview); err != nil {
			return result, err
		}
	}
	result.Applied = true
	return result, nil
}

func applyHostPreview(preview agents.EditPreview) (err error) {
	if preview.Before != nil {
		return agents.ApplyEditPreview(preview, 0o600)
	}
	parent := filepath.Dir(preview.Path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.CreateTemp(parent, ".memlane-config-")
	if err != nil {
		return err
	}
	path := staging.Name()
	defer func() { err = errors.Join(err, os.Remove(path)) }()
	if err := staging.Close(); err != nil {
		return err
	}
	if err := agents.AtomicWriteFile(path, preview.After, 0o600); err != nil {
		return err
	}
	// Publish a new file without replacing an entry created since the preview.
	return os.Link(path, preview.Path)
}

func configGroup(doc object, key string, create bool) (object, error) {
	if val, ok := doc[key]; ok {
		group, ok := val.(object)
		if !ok {
			return nil, fmt.Errorf("invalid %s configuration", key)
		}
		return group, nil
	}
	if !create {
		return nil, nil
	}
	group := object{}
	doc[key] = group
	return group, nil
}

func editAntigravityHooks(doc object, opts InstallOptions, remove bool) error {
	if rawHooks, hasLegacyWrapper := doc["hooks"]; hasLegacyWrapper {
		if wrapper, ok := rawHooks.(object); ok {
			changed, err := stripManagedHookGroups(wrapper)
			if err != nil {
				return err
			}
			if changed && len(wrapper) == 0 {
				delete(doc, "hooks")
			}
		}
	}
	for _, name := range legacyNames {
		delete(doc, name)
	}
	if old, exists := doc["memlane"]; exists {
		if !managedAntigravityHookGroup(old) {
			if remove {
				return nil
			}
			return errors.New("hook named memlane already exists")
		}
		delete(doc, "memlane")
	}
	if remove {
		return nil
	}
	commandFor := func(event string) string {
		return shellWord(opts.BinaryPath) + " hook " + HostAntigravity + " " + event + " --gateway-url " + shellWord(opts.GatewayURL) + " --key-file " + shellWord(opts.KeyFile)
	}
	doc["memlane"] = object{
		SessionStart: []any{
			object{"type": "command", "command": commandFor(SessionStart)},
		},
		PreInvocation: []any{
			object{"type": "command", "command": commandFor(PreInvocation)},
		},
		PostToolUse: []any{
			object{
				"matcher": "*",
				"hooks": []any{
					object{"type": "command", "command": commandFor(PostToolUse)},
				},
			},
		},
		Stop: []any{
			object{"type": "command", "command": commandFor(Stop)},
		},
	}
	return nil
}

func stripManagedHookGroups(groups object) (bool, error) {
	changed := false
	for _, event := range []string{SessionStart, UserPromptSubmit, PreInvocation, PostToolUse, PostToolUseFailure, Stop, PreCompact, SessionEnd} {
		raw, exists := groups[event]
		if !exists {
			continue
		}
		rows, ok := raw.([]any)
		if !ok {
			return false, fmt.Errorf("invalid %s hook list", event)
		}
		kept := make([]any, 0, len(rows))
		eventChanged := false
		for _, row := range rows {
			group, _ := row.(object)
			if group["_memlane_managed"] == marker {
				eventChanged = true
				continue
			}
			if nested, ok := group["hooks"].([]any); ok {
				children := make([]any, 0, len(nested))
				for _, child := range nested {
					if !managedAntigravityHookHandler(child) {
						children = append(children, child)
					}
				}
				if len(children) != len(nested) {
					eventChanged = true
					if len(children) == 0 {
						continue
					}
					group["hooks"] = children
				}
			} else if managedAntigravityHookHandler(row) {
				eventChanged = true
				continue
			}
			kept = append(kept, row)
		}
		if eventChanged {
			changed = true
			if len(kept) == 0 {
				delete(groups, event)
			} else {
				groups[event] = kept
			}
		}
	}
	return changed, nil
}

func managedAntigravityHookGroup(entry any) bool {
	group, ok := entry.(object)
	if !ok {
		return false
	}
	if group["_memlane_managed"] == marker {
		return true
	}
	foundHandler := false
	for key, raw := range group {
		if key == "enabled" {
			if _, ok := raw.(bool); ok {
				continue
			}
			return false
		}
		rows, ok := raw.([]any)
		if !ok {
			return false
		}
		for _, row := range rows {
			item, ok := row.(object)
			if !ok {
				return false
			}
			if nested, hasNested := item["hooks"].([]any); hasNested {
				if len(nested) == 0 {
					return false
				}
				for _, child := range nested {
					if !managedAntigravityHookHandler(child) {
						return false
					}
					foundHandler = true
				}
			} else {
				if !managedAntigravityHookHandler(item) {
					return false
				}
				foundHandler = true
			}
		}
	}
	return foundHandler
}

func managedAntigravityHookHandler(entry any) bool {
	if managedHook(entry) {
		return true
	}
	row, _ := entry.(object)
	command := stringField(row, "command")
	return strings.Contains(command, " hook "+HostAntigravity+" ") &&
		strings.Contains(command, "--gateway-url ") &&
		strings.Contains(command, "--key-file ")
}

func antigravityLegacyPluginPreview(profileDir string) (agents.EditPreview, error) {
	installedDirs := map[string]bool{}
	for _, name := range legacyNames {
		_, err := os.Lstat(filepath.Join(profileDir, "plugins", name))
		if err == nil {
			installedDirs[name] = true
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return agents.EditPreview{}, err
		}
	}
	configPath := filepath.Join(profileDir, "config.json")
	if _, err := agents.SafeReadFile(configPath, 1<<20); errors.Is(err, os.ErrNotExist) {
		if len(installedDirs) == 0 {
			return agents.EditPreview{}, nil
		}
	} else if err != nil {
		return agents.EditPreview{}, err
	}
	return jsonPreview(configPath, false, func(doc object) error {
		plugins, err := configGroup(doc, "plugins", len(installedDirs) > 0)
		if err != nil || plugins == nil {
			return err
		}
		for _, name := range legacyNames {
			if installedDirs[name] {
				if entry, ok := plugins[name].(object); ok {
					entry["enabled"] = false
				} else {
					plugins[name] = object{"enabled": false}
				}
			} else {
				delete(plugins, name)
			}
		}
		return nil
	})
}

func editHookGroups(doc object, host string, opts InstallOptions, remove bool) error {
	if host == HostTraeIDE && !remove {
		if version, ok := doc["version"]; !ok {
			doc["version"] = float64(1)
			if len(doc) == 1 {
				doc["hooks"] = object{}
			}
		} else if version != float64(1) && version != json.Number("1") {
			return errors.New("unsupported Trae IDE hooks version")
		}
	}
	groups := doc
	if _, nested := doc["hooks"]; nested || len(doc) == 0 && !remove {
		var err error
		groups, err = configGroup(doc, "hooks", !remove)
		if err != nil {
			return err
		}
	}
	events := []string{SessionStart, UserPromptSubmit, PostToolUse, Stop}
	if host != HostTraeIDE {
		events = append(events, PostToolUseFailure, PreCompact, SessionEnd)
	}
	for _, event := range []string{SessionStart, UserPromptSubmit, PostToolUse, PostToolUseFailure, Stop, PreCompact, SessionEnd} {
		if raw, exists := groups[event]; exists {
			rows, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("invalid %s hook list", event)
			}
			kept := make([]any, 0, len(rows))
			for _, row := range rows {
				group, _ := row.(object)
				if group["_memlane_managed"] == marker {
					continue
				}
				if nested, ok := group["hooks"].([]any); ok {
					children := make([]any, 0, len(nested))
					for _, child := range nested {
						if !managedHook(child) {
							children = append(children, child)
						}
					}
					if len(children) != len(nested) {
						if len(children) == 0 {
							continue
						}
						group["hooks"] = children
					}
				} else if managedHook(row) {
					continue
				}
				kept = append(kept, row)
			}
			if len(kept) != len(rows) && len(kept) == 0 {
				delete(groups, event)
			} else {
				groups[event] = kept
			}
		}
	}
	if remove {
		return nil
	}
	for _, event := range events {
		rows, _ := groups[event].([]any)
		command := shellWord(opts.BinaryPath) + " hook " + host + " " + event + " --gateway-url " + shellWord(opts.GatewayURL) + " --key-file " + shellWord(opts.KeyFile)
		groups[event] = append(rows, object{"hooks": []any{object{"type": "command", "command": command, "_memlane_managed": marker}}, "_memlane_managed": marker})
	}
	return nil
}

func shellWord(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func managedHook(entry any) bool {
	row, _ := entry.(object)
	if row["_memlane_managed"] == marker {
		return true
	}
	command := stringField(row, "command")
	for _, name := range legacyNames {
		if strings.Contains(command, "/"+name+"/") {
			return true
		}
	}
	return legacyIDEHook.MatchString(command)
}

func managedServer(entry any) bool {
	server, _ := entry.(object)
	return server["_memlane_managed"] == marker || server["managed_by"] == "memlane"
}

func jsonPreview(path string, remove bool, mutate func(object) error) (agents.EditPreview, error) {
	_, err := agents.SafeReadFile(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		if remove {
			return agents.EditPreview{}, nil
		}
		doc := object{}
		if err := mutate(doc); err != nil {
			return agents.EditPreview{}, err
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return agents.EditPreview{}, err
		}
		return newPreview(path, append(data, '\n')), nil
	}
	if err != nil {
		return agents.EditPreview{}, err
	}
	return agents.PreviewJSONEdit(path, mutate, "edit managed Memlane integration")
}

func newPreview(path string, data []byte) agents.EditPreview {
	return agents.EditPreview{Path: path, After: data, BeforeSHA256: fmt.Sprintf("%x", sha256.Sum256(nil)), AfterSHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Changed: true, Summary: "add managed Memlane integration"}
}

func tomlPreview(path, host, endpoint, keyFile string, remove bool) (agents.EditPreview, error) {
	raw, err := agents.SafeReadFile(path, 1<<20)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return agents.EditPreview{}, err
	}
	table := []string{"mcp_servers", "memlane"}
	if missing {
		if remove {
			return agents.EditPreview{}, nil
		}
		prefix := ""
		if host == HostCodex {
			prefix = "[features]\nhooks = true\n\n"
		}
		data := []byte(fmt.Sprintf("%s[mcp_servers.memlane]\nurl = %q\nbearer_token_file = %q\n_memlane_managed = %q\n", prefix, endpoint, keyFile, marker))
		if _, err := agents.ParseTOML(data); err != nil {
			return agents.EditPreview{}, err
		}
		return newPreview(path, data), nil
	}
	doc, err := agents.ParseTOML(raw)
	if err != nil {
		return agents.EditPreview{}, err
	}
	servers, err := configGroup(doc, "mcp_servers", false)
	if err != nil {
		return agents.EditPreview{}, err
	}
	var edits []agents.TOMLChange
	for _, name := range legacyNames {
		if _, exists := servers[name]; exists {
			edits = append(edits, agents.DeleteTable([]string{"mcp_servers", name}), agents.DeleteKey([]string{"mcp_servers"}, name))
		}
	}
	if old, exists := servers["memlane"]; exists {
		if managedServer(old) {
			if remove {
				edits = append(edits, agents.DeleteTable(table), agents.DeleteKey([]string{"mcp_servers"}, "memlane"))
			}
		} else if !remove {
			return agents.EditPreview{}, errors.New("MCP server named memlane already exists")
		}
	}
	if !remove {
		if plugins, ok := doc["plugins"].(object); ok {
			names := make([]string, 0, len(plugins))
			for name := range plugins {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				base, _, _ := strings.Cut(name, "@")
				if slices.Contains(legacyNames, base) || legacyMarketplace.MatchString(name) {
					edits = append(edits, agents.DeleteTable([]string{"plugins", name}), agents.DeleteKey([]string{"plugins"}, name))
				}
			}
		}
		if host == HostCodex {
			edits = append(edits, agents.SetKey([]string{"features"}, "hooks", true))
		}
		edits = append(edits, agents.SetKey(table, "url", endpoint), agents.SetKey(table, "bearer_token_file", keyFile), agents.SetKey(table, "_memlane_managed", marker))
	}
	if len(edits) == 0 {
		return agents.EditPreview{}, nil
	}
	return agents.PreviewTOMLEdit(path, edits, "edit managed Memlane integration")
}
