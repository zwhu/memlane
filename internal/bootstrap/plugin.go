package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"

	"github.com/zwhu/memlane"
)

type PluginDriver struct {
	Spec                                SourceSpec
	Version, ExistingPath, ResourceRoot string
	Manifest                            []byte
	Python                              func(context.Context) (string, error)
	DownloadSource                      string
}

func readPluginFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, errors.New("插件文件不是有界普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if len(data) > 4<<20 {
		return nil, errors.New("插件文件不是有界普通文件")
	}
	return data, err
}

func (d PluginDriver) matches(r Requirement) bool {
	if r.ID != "agent-plugin" || r.Version != d.Version || r.Digest != d.Spec.SHA256 {
		return false
	}
	if d.DownloadSource == "" {
		return r.Source == d.Spec.URL
	}
	return r.Source == d.Spec.URL || r.Source == DisplaySourceURL(r.ID, d.Spec.URL, d.Spec.SHA256, d.DownloadSource)
}

type PluginUpdateKind string

const (
	PluginUpdateNone               PluginUpdateKind = ""
	PluginUpdatePolicyOnly         PluginUpdateKind = "policy_only"
	PluginUpdateVersionOrContent   PluginUpdateKind = "version_or_content"
	PluginPolicyUpdateReasonPrefix                  = "官方插件版本未变，仅 Memlane 策略补丁有更新"
)

type PluginVerificationError struct {
	Kind             PluginUpdateKind
	PolicyUpdateOnly bool
	OfficialCommit   string
	ExpectedCommit   string
	ChangedFiles     []string
	Detail           string
}

func (e *PluginVerificationError) Error() string {
	return e.Detail
}

func IsPluginPolicyUpdate(obs Observation) bool {
	return obs.Status == Incompatible && strings.HasPrefix(obs.Reason, PluginPolicyUpdateReasonPrefix)
}

func VerifyPlugin(root string, expected []byte) error {
	var wanted, receipt map[string]any
	failure := &PluginVerificationError{Kind: PluginUpdateVersionOrContent}
	if err := json.Unmarshal(expected, &wanted); err != nil || wanted == nil {
		failure.Detail = "插件固定清单无效"
		return failure
	}
	failure.ExpectedCommit, _ = wanted["plugin_commit"].(string)
	info, err := os.Lstat(root)
	if err != nil {
		failure.Detail = err.Error()
		return failure
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		failure.Detail = "插件位置必须为已有目录"
		return failure
	}
	data, err := readPluginFile(filepath.Join(root, ".memlane-project-access.json"))
	if err != nil {
		failure.Detail = err.Error()
		return failure
	}
	if err := json.Unmarshal(data, &receipt); err != nil || receipt == nil {
		failure.Detail = "插件固定补丁回执无效"
		return failure
	}
	failure.OfficialCommit, _ = receipt["plugin_commit"].(string)
	oldFiles, _ := receipt["files"].(map[string]any)
	newFiles, _ := wanted["files"].(map[string]any)
	for name, digest := range oldFiles {
		if next, present := newFiles[name]; !present || !reflect.DeepEqual(digest, next) {
			failure.ChangedFiles = append(failure.ChangedFiles, name)
		}
	}
	for name := range newFiles {
		if _, present := oldFiles[name]; !present {
			failure.ChangedFiles = append(failure.ChangedFiles, name)
		}
	}
	sort.Strings(failure.ChangedFiles)
	if err := verifyPluginTree(root, oldFiles); err != nil {
		failure.Detail = err.Error()
		return failure
	}
	if reflect.DeepEqual(receipt, wanted) {
		return nil
	}
	failure.Detail = "插件固定补丁回执不同"
	if failure.OfficialCommit != "" && failure.OfficialCommit == failure.ExpectedCommit &&
		pluginPolicyReceiptChanged(receipt, wanted, failure.ChangedFiles) {
		failure.Kind, failure.PolicyUpdateOnly = PluginUpdatePolicyOnly, true
		failure.Detail = PluginPolicyUpdateReasonPrefix
		if len(failure.ChangedFiles) == 1 {
			failure.Detail += "（" + failure.ChangedFiles[0] + "）"
		}
	}
	return failure
}

var pluginPolicyPatchAllowlist = map[string]bool{
	"scripts/_memlane_policy.py":      true,
	"scripts/_memlane_access.py":      true,
	"scripts/config.py":               true,
	"scripts/session-start.py":        true,
	"scripts/_coding_memory.py":       true,
	"scripts/_plugin_common.py":       true,
	"scripts/_remember_http.py":       true,
	"scripts/cognee-remember.sh":      true,
	"scripts/store-user-prompt.py":    true,
	"scripts/sync-session-to-graph.py": true,
	"scripts/_env_file.py":            true,
	"scripts/_recall_http.py":         true,
	"scripts/cognee-search.sh":        true,
	"scripts/_code_graph.py":          true,
	"scripts/cognee-forget.sh":        true,
	"scripts/cognee-index-repo.sh":    true,
}

func pluginPolicyReceiptChanged(receipt, wanted map[string]any, changed []string) bool {
	oldFiles, _ := receipt["files"].(map[string]any)
	files, _ := wanted["files"].(map[string]any)
	if len(files) == 0 || len(oldFiles) == 0 {
		return false
	}
	for path, value := range files {
		digest, _ := value.(string)
		decoded, err := hex.DecodeString(digest)
		if !fs.ValidPath(path) || path == "." || path == ".memlane-project-access.json" ||
			err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) {
			return false
		}
	}
	if !pluginPatchMetadataValid(receipt, wanted) {
		return false
	}
	if value, present := wanted["upstream_lock_sha256"]; present {
		lock, ok := value.(string)
		if !ok || lock == "" || !reflect.DeepEqual(receipt["upstream_lock_sha256"], lock) {
			return false
		}
		for _, name := range changed {
			if !pluginPolicyPatchAllowlist[name] {
				return false
			}
		}
	} else {
		for _, name := range changed {
			if name != "scripts/_memlane_policy.py" && name != "scripts/_memlane_access.py" {
				return false
			}
		}
	}
	return len(changed) > 0 || !reflect.DeepEqual(receipt["patch_sha256"], wanted["patch_sha256"]) ||
		!reflect.DeepEqual(receipt["extension_sha256"], wanted["extension_sha256"])
}

func pluginPatchMetadataValid(receipt, wanted map[string]any) bool {
	for _, manifests := range [][2]map[string]any{{receipt, wanted}, {wanted, receipt}} {
		for key, value := range manifests[0] {
			switch key {
			case "plugin_commit", "files", "upstream_lock_sha256":
			case "patch_sha256", "extension_sha256":
				if digest, ok := value.(string); !ok || digest == "" {
					return false
				}
			default:
				if other, present := manifests[1][key]; !present || !reflect.DeepEqual(value, other) {
					return false
				}
			}
		}
	}
	return true
}

func verifyPluginTree(root string, files map[string]any) error {
	if len(files) == 0 {
		return errors.New("插件固定文件清单缺失")
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(target string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("插件文件不能包含链接")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, target)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if name == ".memlane-project-access.json" {
			return nil
		}
		wanted, ok := files[name].(string)
		if !ok || !fs.ValidPath(name) {
			return errors.New("插件包含清单以外的文件")
		}
		b, err := readPluginFile(target)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(b)
		if hex.EncodeToString(digest[:]) != wanted {
			return errors.New("插件文件摘要不同: " + name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(files) {
		return errors.New("插件文件不完整")
	}
	return nil
}

func (d PluginDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	o := Observation{Status: Missing, Path: filepath.Join(r.Destination, "plugin"), Version: d.Version}
	if err := ctx.Err(); err != nil {
		return o, err
	}
	if !d.matches(r) {
		return o, errors.New("插件准备范围与固定计划不同")
	}
	digest := sha256.Sum256(d.Manifest)
	o.Fingerprint = hex.EncodeToString(digest[:])
	if d.ExistingPath != "" {
		if err := VerifyPlugin(d.ExistingPath, d.Manifest); err == nil {
			o.Status, o.Path = Available, d.ExistingPath
			return o, nil
		} else {
			o.Status, o.Reason = Incompatible, err.Error()
		}
	}
	if _, err := os.Lstat(o.Path); errors.Is(err, os.ErrNotExist) {
		return o, nil
	} else if err != nil {
		return o, err
	}
	if err := VerifyPlugin(o.Path, d.Manifest); err != nil {
		o.Status, o.Reason = Incompatible, err.Error()
	} else {
		o.Status, o.Reason = Available, ""
	}
	return o, nil
}

func (d PluginDriver) Install(ctx context.Context, r Requirement) error {
	if !d.matches(r) {
		return errors.New("插件准备范围与固定计划不同")
	}
	digest := sha256.Sum256(d.Manifest)
	identity, _ := json.Marshal(map[string]any{"source": d.Spec, "patch_sha256": hex.EncodeToString(digest[:])})
	if err := claimComponentRoot(r.Destination, ".memlane-plugin-component.json", identity); err != nil {
		return err
	}
	output := filepath.Join(r.Destination, "plugin")
	if _, err := os.Lstat(output); err == nil {
		return VerifyPlugin(output, d.Manifest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	upstream := r
	upstream.ID, upstream.Version, upstream.Destination = "cognee-source", d.Spec.Commit, filepath.Join(r.Destination, "integrations-upstream")
	if err := (SourceDriver{Spec: d.Spec, DownloadSource: d.DownloadSource}).Install(ctx, upstream); err != nil {
		return err
	}
	// Installer.Run already holds this resource root's installation lock.
	resources, err := prepareRuntimeResources(ctx, d.ResourceRoot, memlane.RuntimeAssets)
	if err != nil {
		return err
	}
	python, err := d.Python(ctx)
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(r.Destination, ".plugin-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	plugin := filepath.Join(staging, "plugin")
	if err := runManaged(ctx, staging, python, "-I", "-B", filepath.Join(resources, "scripts/build_project_plugin.py"),
		"--source", filepath.Join(upstream.Destination, "source/integrations/codex/plugins/cognee"), "--output", plugin); err != nil {
		return err
	}
	if err := VerifyPlugin(plugin, d.Manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(plugin, output)
}

func rejectSymlinkAncestors(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return errors.New("路径必须为绝对路径")
	}
	for p := clean; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && p == clean {
				if parent := filepath.Dir(p); parent != p {
					p = parent
					continue
				}
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("配置或组件路径不能包含软链接")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}

func runtimeConfigMatchesStore(path, officialData string) bool {
	if officialData == "" {
		return true
	}
	wantStore, err := prospectivePath(officialData)
	if err != nil {
		return false
	}
	raw, err := readPluginFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		OfficialData string `json:"official_data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.OfficialData == "" {
		return false
	}
	gotStore, err := prospectivePath(doc.OfficialData)
	return err == nil && gotStore == wantStore
}

func resolvePersistentRuntimeConfig(configFile, officialData string) (string, error) {
	if configFile == "" {
		return "", errors.New("未找到日常配置文件路径")
	}
	if err := rejectSymlinkAncestors(configFile); err != nil {
		return "", err
	}
	base := filepath.Base(configFile)
	if !strings.HasPrefix(base, ".runtime-active-") {
		return configFile, nil
	}
	if officialData != "" {
		ownerPath := filepath.Join(officialData, ".memlane-dev-owner.json")
		if info, err := os.Lstat(ownerPath); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			if raw, err := readPluginFile(ownerPath); err == nil {
				var owner struct {
					Config string `json:"config"`
				}
				if err := json.Unmarshal(raw, &owner); err == nil && owner.Config != "" && filepath.IsAbs(owner.Config) {
					if err := rejectSymlinkAncestors(owner.Config); err == nil {
						if st, err := os.Lstat(owner.Config); err == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0 {
							if runtimeConfigMatchesStore(owner.Config, officialData) {
								return owner.Config, nil
							}
						}
					}
				}
			}
		}
	}
	fallback := filepath.Join(filepath.Dir(configFile), "runtime.json")
	if err := rejectSymlinkAncestors(fallback); err == nil {
		if st, err := os.Lstat(fallback); err == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0 {
			if runtimeConfigMatchesStore(fallback, officialData) {
				return fallback, nil
			}
		}
	}
	return configFile, nil
}

func updateRuntimeConfigPluginDir(path, newPluginDir string) (string, error) {
	if err := rejectSymlinkAncestors(path); err != nil {
		return "", err
	}
	raw, err := readPluginFile(path)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	pc, ok := doc["project_control"].(map[string]any)
	if !ok || pc == nil {
		return "", errors.New("日常配置缺少 project_control 节点")
	}
	previous, _ := pc["plugin_dir"].(string)
	if previous == newPluginDir {
		return previous, nil
	}
	pc["plugin_dir"] = newPluginDir
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	if err := atomicPrivateFile(path, encoded); err != nil {
		return "", err
	}
	return previous, nil
}

// ApplyManagedPluginUpdate verifies a newly prepared managed plugin directory
// against the pinned manifest and atomically updates project_control.plugin_dir
// in the persistent runtime config (resolving active session snapshots back to
// runtime.json via .memlane-dev-owner.json), while retaining the old directory.
func ApplyManagedPluginUpdate(configFile, officialData, newPluginDir string, manifest []byte) (string, error) {
	if newPluginDir == "" || !filepath.IsAbs(newPluginDir) {
		return "", errors.New("插件目录必须为绝对路径")
	}
	cleanPlugin := filepath.Clean(newPluginDir)
	if err := rejectSymlinkAncestors(cleanPlugin); err != nil {
		return "", err
	}
	if err := validateComponentLocation(EnvironmentOptions{Root: cleanPlugin, ProtectedStore: officialData}); err != nil {
		return "", err
	}
	if err := VerifyPlugin(cleanPlugin, manifest); err != nil {
		return "", err
	}
	targetPath, err := resolvePersistentRuntimeConfig(configFile, officialData)
	if err != nil {
		return "", err
	}
	previous, err := updateRuntimeConfigPluginDir(targetPath, cleanPlugin)
	if err != nil {
		return "", err
	}
	if filepath.Clean(configFile) != filepath.Clean(targetPath) && strings.HasPrefix(filepath.Base(configFile), ".runtime-active-") {
		if st, err := os.Lstat(configFile); err == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0 {
			_, _ = updateRuntimeConfigPluginDir(configFile, cleanPlugin)
		}
	}
	return previous, nil
}
