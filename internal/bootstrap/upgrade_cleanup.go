package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zwhu/memlane/internal/supervisor"
)

// ErrUpgradeCleanupPlanChanged indicates that the on-disk upgrade cleanup targets
// changed between --dry-run preview and --apply confirmation.
var ErrUpgradeCleanupPlanChanged = errors.New("upgrade cleanup plan changed since preview")

type UpgradeCleanupOptions struct {
	ComponentRoot     string
	StoreRoot         string
	CogneeSourceDir   string
	KeepRuntimeHashes []string
}

type UpgradeCleanupPlan struct {
	LegacyPycCount     int      `json:"legacy_pyc_count"`
	LegacyPycacheDirs  int      `json:"legacy_pycache_dirs"`
	RetiredRuntimeDirs []string `json:"retired_runtime_dirs"`
	InstallItems       []string `json:"install_items"`
	CleanupItems       []string `json:"cleanup_items"`
	PreservedItems     []string `json:"preserved_items"`
	NeedsCleanup       bool     `json:"needs_cleanup"`
	PlanSHA            string   `json:"plan_sha"`

	cogneeRoot   string
	pycFiles     []string
	pycacheDirs  []string
	retiredPaths []string
}

type UpgradeCleanupResult struct {
	Applied            bool     `json:"applied"`
	RemovedPycCount    int      `json:"removed_pyc_count"`
	RemovedPycacheDirs int      `json:"removed_pycache_dirs"`
	RemovedRuntimeDirs []string `json:"removed_runtime_dirs"`
	PlanSHA            string   `json:"plan_sha"`
}

func defaultUpgradeManifestItems(plan *UpgradeCleanupPlan) {
	plan.InstallItems = []string{
		"新版 /Applications/Memlane.app（单二进制桌面端与后台守护）",
		"受管内嵌运行资源 components/installation/runtime-<新hash>（零重新下载已有 Python/Cognee/向量模型）",
		"方案 C 宿主接入（Go Memlane hook 与极薄 pi 扩展，需按 next_actions 预览确认）",
	}
	cleanup := []string{}
	if plan.LegacyPycCount > 0 || plan.LegacyPycacheDirs > 0 {
		cleanup = append(cleanup, fmt.Sprintf("受管 Cognee 源码目录残留字节码（%d 个 .pyc/.pyo，%d 个 __pycache__）", plan.LegacyPycCount, plan.LegacyPycacheDirs))
	} else {
		cleanup = append(cleanup, "受管 Cognee 源码目录残留 .pyc/.pyo 与空 __pycache__（当前无残留）")
	}
	if len(plan.RetiredRuntimeDirs) > 0 {
		cleanup = append(cleanup, fmt.Sprintf("已停用且未被活跃守护引用的旧运行代目录（%d 个：%s）", len(plan.RetiredRuntimeDirs), strings.Join(plan.RetiredRuntimeDirs, "、")))
	} else {
		cleanup = append(cleanup, "已停用旧版 components/installation/runtime-* 目录（当前无残留）")
	}
	plan.CleanupItems = cleanup
	plan.PreservedItems = []string{
		"个人记忆库（library/ 或 official-store/ 下的 cognee_db、cache.db、cognee.lancedb、cognee_graph_kuzu、memlane.sqlite3）",
		"自动迁移留存的历史备份目录（migration-backup-*）",
		"已下载运行依赖（Python 3.12、Cognee 1.6.7、LanceDB、Ladybug、向量模型）与模型配置/凭据",
		"各宿主工具原有第三方插件、Hooks 与 MCP 配置",
	}
}

// PlanUpgradeCleanup inspects managed component directories read-only and computes
// a fail-closed allowlisted cleanup plan for legacy bytecode and retired runtime dirs.
func PlanUpgradeCleanup(opts UpgradeCleanupOptions) (UpgradeCleanupPlan, error) {
	plan := UpgradeCleanupPlan{
		RetiredRuntimeDirs: []string{},
	}
	keep := map[string]bool{}
	for _, h := range opts.KeepRuntimeHashes {
		if trimmed := strings.TrimSpace(h); trimmed != "" {
			keep[trimmed] = true
		}
	}

	sourceDir, err := resolveManagedCogneeSource(opts.ComponentRoot, opts.CogneeSourceDir)
	if err != nil {
		return plan, err
	}
	if sourceDir != "" {
		plan.cogneeRoot = sourceDir
		pycFiles, pycacheDirs, err := scanCogneeBytecode(sourceDir)
		if err != nil {
			return plan, err
		}
		plan.pycFiles = pycFiles
		plan.pycacheDirs = pycacheDirs
		plan.LegacyPycCount = len(pycFiles)
		plan.LegacyPycacheDirs = len(pycacheDirs)
	}

	if opts.ComponentRoot != "" {
		names, paths, err := scanRetiredRuntimes(opts.ComponentRoot, keep)
		if err != nil {
			return plan, err
		}
		plan.RetiredRuntimeDirs = names
		plan.retiredPaths = paths
	}

	plan.NeedsCleanup = plan.LegacyPycCount > 0 || plan.LegacyPycacheDirs > 0 || len(plan.RetiredRuntimeDirs) > 0
	defaultUpgradeManifestItems(&plan)
	plan.PlanSHA = computeUpgradePlanSHA(plan)
	return plan, nil
}

// ApplyUpgradeCleanup executes a previously reviewed allowlisted upgrade cleanup plan.
func ApplyUpgradeCleanup(ctx context.Context, opts UpgradeCleanupOptions, reviewedSHA string) (UpgradeCleanupResult, error) {
	result := UpgradeCleanupResult{
		RemovedRuntimeDirs: []string{},
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if opts.StoreRoot != "" {
		if _, err := os.Stat(opts.StoreRoot); err == nil {
			lock, err := supervisor.AcquireUpgradeLock(opts.StoreRoot)
			if err != nil {
				return result, err
			}
			defer func() { _ = lock.Release() }()
		}
	}

	plan, err := PlanUpgradeCleanup(opts)
	if err != nil {
		return result, err
	}
	result.PlanSHA = plan.PlanSHA
	if reviewedSHA != "" && plan.PlanSHA != reviewedSHA {
		return result, ErrUpgradeCleanupPlanChanged
	}
	if !plan.NeedsCleanup {
		result.Applied = true
		return result, nil
	}

	if plan.cogneeRoot != "" {
		removedFiles, removedDirs, err := removeCogneeBytecode(plan.cogneeRoot, plan.pycFiles, plan.pycacheDirs)
		if err != nil {
			return result, err
		}
		result.RemovedPycCount = removedFiles
		result.RemovedPycacheDirs = removedDirs
	}

	installRoot := filepath.Join(opts.ComponentRoot, "installation")
	for i, dirPath := range plan.retiredPaths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !isStrictChild(installRoot, dirPath) {
			return result, errors.New("retired runtime path escapes managed installation root")
		}
		info, err := os.Lstat(dirPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return result, errors.New("refusing to remove non-directory or symlink runtime entry")
		}
		if _, ok := RuntimeHashFromDir(dirPath); !ok {
			return result, errors.New("refusing to remove non-managed installation directory")
		}
		if err := os.RemoveAll(dirPath); err != nil {
			return result, err
		}
		result.RemovedRuntimeDirs = append(result.RemovedRuntimeDirs, plan.RetiredRuntimeDirs[i])
	}
	result.Applied = true
	return result, nil
}

// CleanCogneeSourceBytecode removes only regular .pyc/.pyo files and empty
// __pycache__ directories strictly inside a verified Cognee source directory.
func CleanCogneeSourceBytecode(sourceDir string) (int, int, error) {
	resolved, err := resolveManagedCogneeSource("", sourceDir)
	if err != nil || resolved == "" {
		return 0, 0, err
	}
	pycFiles, pycacheDirs, err := scanCogneeBytecode(resolved)
	if err != nil {
		return 0, 0, err
	}
	if len(pycFiles) == 0 && len(pycacheDirs) == 0 {
		return 0, 0, nil
	}
	return removeCogneeBytecode(resolved, pycFiles, pycacheDirs)
}

func resolveManagedCogneeSource(componentRoot, explicit string) (string, error) {
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", errors.New("cognee source path must be absolute")
		}
		info, err := os.Lstat(explicit)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("cognee source must be a real directory")
		}
		if !looksLikeCogneeSource(explicit) {
			return "", nil
		}
		return filepath.Clean(explicit), nil
	}
	if componentRoot == "" {
		return "", nil
	}
	entries, err := os.ReadDir(componentRoot)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "service-") && !strings.HasPrefix(name, "cognee-") {
			continue
		}
		candidate := filepath.Join(componentRoot, name)
		info, err := os.Lstat(candidate)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		if looksLikeCogneeSource(candidate) {
			return filepath.Clean(candidate), nil
		}
	}
	return "", nil
}

func looksLikeCogneeSource(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, "cognee", "__init__.py"))
	return err == nil && info.Mode().IsRegular()
}

func scanCogneeBytecode(root string) ([]string, []string, error) {
	var pycFiles []string
	var pycacheDirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				pycacheDirs = append(pycacheDirs, path)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		lower := strings.ToLower(d.Name())
		if strings.HasSuffix(lower, ".pyc") || strings.HasSuffix(lower, ".pyo") {
			pycFiles = append(pycFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	slices.Sort(pycFiles)
	// Remove deepest __pycache__ dirs first.
	slices.SortFunc(pycacheDirs, func(a, b string) int {
		if len(a) != len(b) {
			if len(a) > len(b) {
				return -1
			}
			return 1
		}
		return strings.Compare(b, a)
	})
	return pycFiles, pycacheDirs, nil
}

func removeCogneeBytecode(root string, pycFiles, pycacheDirs []string) (int, int, error) {
	removedFiles := 0
	for _, filePath := range pycFiles {
		if !isStrictChild(root, filePath) {
			return removedFiles, 0, errors.New("bytecode file escapes cognee source root")
		}
		info, err := os.Lstat(filePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removedFiles, 0, err
		}
		lower := strings.ToLower(filepath.Base(filePath))
		if !info.Mode().IsRegular() || (!strings.HasSuffix(lower, ".pyc") && !strings.HasSuffix(lower, ".pyo")) {
			return removedFiles, 0, errors.New("refusing to remove non-bytecode file")
		}
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removedFiles, 0, err
		}
		removedFiles++
	}
	removedDirs := 0
	for _, dirPath := range pycacheDirs {
		if !isStrictChild(root, dirPath) || filepath.Base(dirPath) != "__pycache__" {
			continue
		}
		info, err := os.Lstat(dirPath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		// Only os.Remove (non-recursive) so any unexpected non-bytecode file inside is preserved.
		if err := os.Remove(dirPath); err == nil {
			removedDirs++
		}
	}
	return removedFiles, removedDirs, nil
}

func scanRetiredRuntimes(componentRoot string, keep map[string]bool) ([]string, []string, error) {
	installRoot := filepath.Join(componentRoot, "installation")
	info, err := os.Lstat(installRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil, errors.New("managed installation directory must not be a symlink")
	}
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return nil, nil, err
	}
	names := []string{}
	var paths []string
	for _, entry := range entries {
		candidate := filepath.Join(installRoot, entry.Name())
		hash, ok := RuntimeHashFromDir(candidate)
		if !ok || keep[hash] {
			continue
		}
		st, err := os.Lstat(candidate)
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	for _, name := range names {
		paths = append(paths, filepath.Join(installRoot, name))
	}
	return names, paths, nil
}

func isStrictChild(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func computeUpgradePlanSHA(plan UpgradeCleanupPlan) string {
	relPyc := make([]string, 0, len(plan.pycFiles))
	for _, p := range plan.pycFiles {
		if plan.cogneeRoot != "" {
			if rel, err := filepath.Rel(plan.cogneeRoot, p); err == nil {
				relPyc = append(relPyc, filepath.ToSlash(rel))
				continue
			}
		}
		relPyc = append(relPyc, filepath.Base(p))
	}
	canonical := struct {
		PycCount   int      `json:"pyc_count"`
		PycRel     []string `json:"pyc_rel"`
		PycacheCnt int      `json:"pycache_count"`
		Retired    []string `json:"retired"`
	}{
		PycCount:   plan.LegacyPycCount,
		PycRel:     relPyc,
		PycacheCnt: plan.LegacyPycacheDirs,
		Retired:    plan.RetiredRuntimeDirs,
	}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
