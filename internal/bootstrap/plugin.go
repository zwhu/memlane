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
	"strings"
	"syscall"

	"github.com/zwhu/memlane"
)

type PluginDriver struct {
	Spec                                SourceSpec
	Version, ExistingPath, ResourceRoot string
	Manifest                            []byte
	Python                              func(context.Context) (string, error)
}

func readPluginFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
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
	return io.ReadAll(io.LimitReader(f, (4<<20)+1))
}

func (d PluginDriver) matches(r Requirement) bool {
	return r.ID == "agent-plugin" && r.Version == d.Version && r.Source == d.Spec.URL && r.Digest == d.Spec.SHA256
}

func VerifyPlugin(root string, expected []byte) error {
	var wanted, receipt map[string]any
	if err := json.Unmarshal(expected, &wanted); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("插件位置必须为已有目录")
	}
	b, err := readPluginFile(filepath.Join(root, ".memlane-project-access.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &receipt); err != nil {
		return err
	}
	if !reflect.DeepEqual(receipt, wanted) {
		return errors.New("插件固定补丁回执不同")
	}
	files, ok := wanted["files"].(map[string]any)
	if !ok || len(files) == 0 {
		return errors.New("插件固定文件清单缺失")
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(target string, entry fs.DirEntry, err error) error {
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
	if err := (SourceDriver{Spec: d.Spec}).Install(ctx, upstream); err != nil {
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
							return owner.Config, nil
						}
					}
				}
			}
		}
	}
	fallback := filepath.Join(filepath.Dir(configFile), "runtime.json")
	if err := rejectSymlinkAncestors(fallback); err == nil {
		if st, err := os.Lstat(fallback); err == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0 {
			return fallback, nil
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
