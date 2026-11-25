package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type ConflictTier int

const (
	TierManagedLegacy ConflictTier = iota + 1
	TierThirdPartyConflict
	TierUnrelated
)

type ConflictAction string

const (
	ActionMigrate ConflictAction = "migrate"
	ActionBlock   ConflictAction = "block"
	ActionIgnore  ConflictAction = "ignore"
)

type ConflictPolicy struct {
	ManagedMarkers, ConflictKeywords []string
	BackupDir                        string
	WorkspaceDirs                    []string
}
type ConflictFinding struct {
	Tier                                             ConflictTier
	Action                                           ConflictAction
	HostKind, Surface, EntryName, ConfigPath, Detail string
	Blocked                                          bool
	path                                             []string
	directoryEntry                                   bool
	configDigest                                     string
	entryDevice, entryInode                          uint64
}
type ConflictReport struct {
	HostKind                           string
	Blocked                            bool
	ManagedLegacy, ThirdPartyConflicts []ConflictFinding
	UnrelatedCount                     int
	host                               DetectedHost
}
type BackupRecord struct{ OriginalPath, BackupPath, EntryName, Surface string }

type configSurface struct{ name, surface string }

var surfaceNames = map[string]string{
	"plugins": "plugin", "plugin": "plugin", "enabledPlugins": "plugin",
	"hooks": "hook", "mcp_servers": "mcp_server", "mcpServers": "mcp_server", "mcp": "mcp_server",
	"extensions": "extension", "packages": "package",
}

func hostSurfaces(kind string) ([]string, []configSurface) {
	switch kind {
	case HostCodex:
		return []string{"config.toml", "hooks.json"}, []configSurface{{"plugins", "plugin"}}
	case HostTraeX:
		return []string{"traecli.toml", "cli/traecli.toml", "hooks.json", "cli/hooks.json", "mcp_config.json", "cli/mcp_config.json"}, []configSurface{{"plugins", "plugin"}, {"cli/plugins", "plugin"}, {"extensions", "extension"}}
	case HostTraeIDE:
		return []string{"settings.json", "mcp.json", "mcp_config.json", "hooks.json", "User/settings.json", "User/mcp.json"}, []configSurface{{"extensions", "extension"}, {"plugins", "plugin"}}
	case HostAntigravity:
		return []string{"config.json", "hooks.json", "mcp_config.json", "settings.json"}, []configSurface{{"plugins", "plugin"}, {"extensions", "extension"}}
	case HostPi:
		return []string{"settings.json", "hooks.json", "mcp.json"}, []configSurface{{"extensions", "extension"}, {"packages", "package"}}
	case HostOpenCode:
		return []string{"opencode.json", "opencode.jsonc"}, []configSurface{{"plugin", "plugin"}, {"plugins", "plugin"}}
	case HostClaudeCode:
		return []string{"settings.json", "settings.local.json", ".mcp.json", "mcp.json", "hooks.json", "plugins/installed_plugins.json"}, []configSurface{{"plugins", "plugin"}}
	case HostCursor:
		return []string{"settings.json", "mcp.json", "mcp_config.json", "hooks.json"}, []configSurface{{"extensions", "extension"}, {"plugins", "plugin"}}
	default:
		return nil, nil
	}
}

func matchesMarkers(value string, markers []string) bool {
	value = strings.ToLower(value)
	for _, marker := range markers {
		if marker != "" && strings.Contains(value, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func (r *ConflictReport) add(f ConflictFinding, value any, policy ConflictPolicy) {
	encoded, err := json.Marshal(value)
	candidate := f.EntryName
	if err == nil {
		candidate += "\n" + string(encoded)
	}
	switch {
	case matchesMarkers(candidate, policy.ManagedMarkers):
		f.Tier, f.Action = TierManagedLegacy, ActionMigrate
		r.ManagedLegacy = append(r.ManagedLegacy, f)
	case matchesMarkers(candidate, policy.ConflictKeywords):
		f.Tier, f.Action, f.Blocked = TierThirdPartyConflict, ActionBlock, true
		r.Blocked = true
		r.ThirdPartyConflicts = append(r.ThirdPartyConflicts, f)
	default:
		r.UnrelatedCount++
	}
}

func arrayEntryName(index int, value any) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"name", "id", "path", "source", "command"} {
			if s, ok := object[key].(string); ok && s != "" {
				return s
			}
		}
	}
	return strconv.Itoa(index)
}

func (r *ConflictReport) scanEntries(path, surface string, keys []string, value any, policy ConflictPolicy) {
	add := func(name, key string, entry any) {
		r.add(ConflictFinding{HostKind: r.HostKind, Surface: surface, EntryName: name, ConfigPath: path,
			Detail: "configured " + surface, path: append(slices.Clone(keys), key)}, entry, policy)
	}
	switch entries := value.(type) {
	case map[string]any:
		for _, name := range sortedKeys(entries) {
			add(name, name, entries[name])
		}
	case []any:
		for i, entry := range entries {
			add(arrayEntryName(i, entry), strconv.Itoa(i), entry)
		}
	case []map[string]any:
		for i, entry := range entries {
			add(arrayEntryName(i, entry), strconv.Itoa(i), entry)
		}
	}
}

func (r *ConflictReport) scanConfig(path string, policy ConflictPolicy) error {
	first := len(r.ThirdPartyConflicts)
	raw, err := SafeReadFile(path, configLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc map[string]any
	if filepath.Ext(path) == ".toml" {
		doc, err = ParseTOML(raw)
	} else {
		doc, err = ParseJSONC(raw)
	}
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(doc) {
		if surface := surfaceNames[key]; surface != "" {
			if key == "hooks" {
				r.scanHooks(path, []string{key}, doc[key], policy)
			} else {
				r.scanEntries(path, surface, []string{key}, doc[key], policy)
			}
		}
	}
	if filepath.Base(path) == "hooks.json" && doc["hooks"] == nil {
		r.scanHooks(path, nil, doc, policy)
	}
	if profiles, ok := doc["profiles"].(map[string]any); ok {
		for _, name := range sortedKeys(profiles) {
			profile, _ := profiles[name].(map[string]any)
			for _, key := range sortedKeys(profile) {
				if surface := surfaceNames[key]; surface != "" {
					r.scanEntries(path, surface, []string{"profiles", name, key}, profile[key], policy)
				}
			}
		}
	}
	if len(r.ThirdPartyConflicts) > first {
		stamp := digest(raw)
		for i := first; i < len(r.ThirdPartyConflicts); i++ {
			r.ThirdPartyConflicts[i].configDigest = stamp
		}
	}

	return nil
}

func (r *ConflictReport) scanHooks(path string, keys []string, value any, policy ConflictPolicy) {
	if groups, ok := value.(map[string]any); ok {
		for _, name := range sortedKeys(groups) {
			rows, ok := groups[name].([]any)
			if !ok {
				r.add(ConflictFinding{HostKind: r.HostKind, Surface: "hook", EntryName: name, ConfigPath: path,
					Detail: "configured hook", path: append(slices.Clone(keys), name)}, groups[name], policy)
				continue
			}
			for i, row := range rows {
				rowPath := append(slices.Clone(keys), name, strconv.Itoa(i))
				object, _ := row.(map[string]any)
				if nested, ok := object["hooks"].([]any); ok {
					r.scanEntries(path, "hook", append(rowPath, "hooks"), nested, policy)
				} else {
					r.add(ConflictFinding{HostKind: r.HostKind, Surface: "hook", EntryName: arrayEntryName(i, row), ConfigPath: path,
						Detail: "configured hook", path: rowPath}, row, policy)
				}
			}
		}
		return
	}
	r.scanEntries(path, "hook", keys, value, policy)
}

func ScanHostConflicts(host DetectedHost, policy ConflictPolicy) (ConflictReport, error) {
	report := ConflictReport{HostKind: host.Kind, host: host}
	configs, dirs := hostSurfaces(host.Kind)
	if len(configs) == 0 {
		return report, errors.New("unknown host kind")
	}
	var paths []string
	if host.ProfileDir != "" {
		for _, name := range configs {
			paths = append(paths, filepath.Join(host.ProfileDir, name))
		}
	}
	if host.Kind == HostCodex {
		for _, dir := range policy.WorkspaceDirs {
			paths = append(paths, filepath.Join(dir, ".codex", "config.toml"))
		}
	}
	slices.Sort(paths)
	for _, path := range slices.Compact(paths) {
		if err := report.scanConfig(path, policy); err != nil {
			return report, fmt.Errorf("scan %s: %w", path, err)
		}
	}
	if host.ProfileDir == "" {
		return report, nil
	}
	for _, dir := range dirs {
		if err := report.scanDirectory(filepath.Join(host.ProfileDir, dir.name), dir.surface, policy); err != nil {
			return report, err
		}
	}
	return report, nil
}

func openDirectory(path string) (*os.File, error) {
	returnDir, _, err := openParent(filepath.Join(path, "entry"))
	return returnDir, err
}

func (r *ConflictReport) scanDirectory(path, surface string, policy ConflictPolicy) error {
	dir, err := openDirectory(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, entry := range entries {
		if entry.IsDir() || entry.Type().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			var st unix.Stat_t
			if err := unix.Fstatat(int(dir.Fd()), entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			r.add(ConflictFinding{HostKind: r.HostKind, Surface: surface, EntryName: entry.Name(),
				ConfigPath: filepath.Join(path, entry.Name()), Detail: "installed " + surface, directoryEntry: true, entryDevice: uint64(st.Dev), entryInode: uint64(st.Ino)}, nil, policy)
		}
	}
	return nil
}

func DisableThirdPartyConflicts(report ConflictReport, policy ConflictPolicy, confirm bool) ([]BackupRecord, error) {
	if !confirm || strings.TrimSpace(policy.BackupDir) == "" {
		return nil, errors.New("confirmed backup directory required")
	}
	if len(report.ThirdPartyConflicts) == 0 {
		return nil, nil
	}
	fresh, err := ScanHostConflicts(report.host, policy)
	if err != nil {
		return nil, err
	}
	groups := map[string][]ConflictFinding{}
	for _, finding := range report.ThirdPartyConflicts {
		index := slices.IndexFunc(fresh.ThirdPartyConflicts, func(f ConflictFinding) bool {
			return finding.ConfigPath == f.ConfigPath && finding.EntryName == f.EntryName && finding.Surface == f.Surface &&
				finding.directoryEntry == f.directoryEntry && slices.Equal(finding.path, f.path) &&
				finding.entryDevice == f.entryDevice && finding.entryInode == f.entryInode
		})
		if index < 0 {
			return nil, errors.New("conflict report is stale")
		}
		current := fresh.ThirdPartyConflicts[index]
		if !slices.ContainsFunc(groups[finding.ConfigPath], func(existing ConflictFinding) bool { return slices.Equal(existing.path, current.path) }) {
			groups[finding.ConfigPath] = append(groups[finding.ConfigPath], current)
		}
	}
	directory, err := makePrivateDirectory(policy.BackupDir)
	if err != nil {
		return nil, err
	}
	directory.Close()
	root, err := os.MkdirTemp(policy.BackupDir, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return nil, err
	}
	var records []BackupRecord
	for i, path := range sortedKeys(groups) {
		findings := groups[path]
		target := filepath.Join(root, fmt.Sprintf("%04d-%s", i, filepath.Base(path)))
		if findings[0].directoryEntry {
			if err := moveConflict(findings[0], target); err != nil {
				return records, err
			}
		} else {
			preview, err := conflictPreview(path, findings)
			if err != nil {
				return records, err
			}
			if err := AtomicWriteFile(target, preview.Before, 0o600); err != nil {
				return records, err
			}
			if err := ApplyEditPreview(preview, 0); err != nil {
				return records, err
			}
		}
		for _, finding := range findings {
			records = append(records, BackupRecord{path, target, finding.EntryName, finding.Surface})
		}
	}
	return records, nil
}

func moveConflict(finding ConflictFinding, to string) error {
	source, name, err := openParent(finding.ConfigPath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, backup, err := openParent(to)
	if err != nil {
		return err
	}
	defer destination.Close()
	var st unix.Stat_t
	if err := unix.Fstatat(int(source.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if uint64(st.Dev) != finding.entryDevice || uint64(st.Ino) != finding.entryInode {
		return errors.New("conflict entry changed")
	}
	return unix.Renameat(int(source.Fd()), name, int(destination.Fd()), backup)
}
