package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

// PrepareRuntimeResources materializes only code and manifests. Nothing is
// downloaded, executed or inferred from a checkout on the user's computer.
func PrepareRuntimeResources(ctx context.Context, root string, assets fs.FS) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, data, names, err := readRuntimeAssets(ctx, assets)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, "runtime-"+id[:16])
	if info, err := os.Lstat(root); filepath.IsAbs(root) && err == nil && info.IsDir() && info.Mode().Perm()&0077 == 0 &&
		verifyPreparedRuntimeResourcesData(destination, id, data, names) {
		return destination, nil
	}
	if err := (&Installer{Root: root}).withLock(func() error {
		return materializeRuntimeResources(ctx, destination, id, data, names)
	}); err != nil {
		return "", err
	}
	return destination, nil
}

// BundledRuntimeSHA256 returns the digest used to name bundled runtime generations.
func BundledRuntimeSHA256(assets fs.FS) (string, error) {
	id, _, _, err := readRuntimeAssets(context.Background(), assets)
	return id, err
}

// BundledRuntimeHash returns the short generation hash without materializing assets.
func BundledRuntimeHash(assets fs.FS) (string, error) {
	id, err := BundledRuntimeSHA256(assets)
	if err != nil {
		return "", err
	}
	return id[:16], nil
}

func readRuntimeAssets(ctx context.Context, assets fs.FS) (string, map[string][]byte, []string, error) {
	if assets == nil {
		return "", nil, nil, errors.New("runtime assets missing")
	}
	data := map[string][]byte{}
	err := fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(name, "engines/cognee/") && strings.HasPrefix(path.Base(name), "test_") {
			return nil
		}
		if !fs.ValidPath(name) || entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("invalid runtime asset")
		}
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			return err
		}
		data[name] = b
		return ctx.Err()
	})
	if err != nil {
		return "", nil, nil, err
	}
	if len(data) == 0 {
		return "", nil, nil, errors.New("runtime assets missing")
	}
	names := make([]string, 0, len(data))
	for name := range data {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name + "\x00"))
		digest := sha256.Sum256(data[name])
		hash.Write(digest[:])
	}
	return hex.EncodeToString(hash.Sum(nil)), data, names, nil
}

func prepareRuntimeResources(ctx context.Context, root string, assets fs.FS) (string, error) {
	id, data, names, err := readRuntimeAssets(ctx, assets)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, "runtime-"+id[:16])
	if err := materializeRuntimeResources(ctx, destination, id, data, names); err != nil {
		return "", err
	}
	return destination, nil
}

var runtimeWritableDirs = [...]string{".cache", "build", "build/disk-cleanup"}

func materializeRuntimeResources(ctx context.Context, destination, id string, data map[string][]byte, names []string) error {
	marker := ".memlane-runtime.json"
	identity, _ := json.Marshal(map[string]string{"sha256": id})
	if err := aiinstaller.ClaimComponentRoot(destination, marker, identity); err != nil {
		return err
	}
	if err := checkRuntimeResourceTree(destination, marker, data); err != nil {
		return err
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if b, err := privateReadBounded(target, 8<<20); err == nil {
			if string(b) != string(data[name]) {
				return errors.New("runtime resource modified: " + path.Base(name))
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := atomicPrivateFile(target, data[name]); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	for _, name := range runtimeWritableDirs {
		dir := filepath.Join(destination, name)
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return errors.New("runtime workspace must be an unlinked directory: " + name)
		}
	}
	return nil
}

// verifyPreparedRuntimeResources checks immutable contents without the installation lock.
func verifyPreparedRuntimeResources(destination string, assets fs.FS) bool {
	id, data, names, err := readRuntimeAssets(context.Background(), assets)
	return err == nil && verifyPreparedRuntimeResourcesData(destination, id, data, names)
}

func verifyPreparedRuntimeResourcesData(destination, id string, data map[string][]byte, names []string) bool {
	if filepath.Base(destination) != "runtime-"+id[:16] {
		return false
	}
	const marker = ".memlane-runtime.json"
	if checkRuntimeResourceTree(destination, marker, data) != nil {
		return false
	}
	for _, name := range runtimeWritableDirs {
		if info, err := os.Lstat(filepath.Join(destination, name)); err != nil || !info.IsDir() {
			return false
		}
	}
	identity, _ := json.Marshal(map[string]string{"sha256": id})
	b, err := privateRead(filepath.Join(destination, marker))
	if err != nil || !bytes.Equal(b, identity) {
		return false
	}
	for _, name := range names {
		b, err := privateReadBounded(filepath.Join(destination, filepath.FromSlash(name)), 8<<20)
		if err != nil || !bytes.Equal(b, data[name]) {
			return false
		}
	}
	return true
}

func checkRuntimeResourceTree(destination, marker string, data map[string][]byte) error {
	// Extra modules could affect Python imports even when every expected file
	// still matches. Reject them before executing anything from this directory.
	return filepath.WalkDir(destination, func(target string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("linked runtime resource retained")
		}
		relative, err := filepath.Rel(destination, target)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		// The reused dev resource guard writes only its lock and small receipts
		// here. These directories are never Python import or executable paths.
		if entry.IsDir() && (name == ".cache" || name == "build") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("runtime resource is not a regular file: " + name)
		}
		if name != marker {
			if _, ok := data[name]; !ok {
				return errors.New("unexpected runtime resource retained: " + name)
			}
		}
		return nil
	})
}

var runtimeDirPattern = regexp.MustCompile(`^runtime-([0-9a-f]{16})$`)

// RuntimeHashFromDir extracts the generation hash from a managed directory name.
func RuntimeHashFromDir(dir string) (string, bool) {
	match := runtimeDirPattern.FindStringSubmatch(filepath.Base(filepath.Clean(dir)))
	if match == nil {
		return "", false
	}
	return match[1], true
}

const graphAssetsSuffix = "web/official-graph/dist"

func managedGraphAssetsHash(root, path string) (string, bool) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", false
	}
	runtimeDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if filepath.Dir(runtimeDir) != root || filepath.Join(runtimeDir, graphAssetsSuffix) != path {
		return "", false
	}
	// Old generations may have been removed. Reject symlinks in the existing prefix.
	for p := path; p != root; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() {
			return "", false
		}
	}
	return RuntimeHashFromDir(runtimeDir)
}

func validateManagedResourcePath(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("managed resource paths must be absolute")
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed resource paths must not contain symlinks")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}

// RefreshManagedGraphAssets updates an empty or previously managed graph assets path.
func RefreshManagedGraphAssets(configPath, installationRoot, newResourcesDir string) (bool, error) {
	for _, p := range []string{configPath, installationRoot, newResourcesDir} {
		if err := validateManagedResourcePath(p); err != nil {
			return false, err
		}
	}
	installationRoot = filepath.Clean(installationRoot)
	newResourcesDir = filepath.Clean(newResourcesDir)
	if _, ok := RuntimeHashFromDir(newResourcesDir); !ok || filepath.Dir(newResourcesDir) != installationRoot {
		return false, errors.New("runtime resources must be a managed generation under installation root")
	}
	// The parent directory inode stays stable across the atomic config rename.
	// Locking it serializes concurrent refreshes without creating a lock file.
	parent, err := os.OpenFile(filepath.Dir(configPath), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	if err := syscall.Flock(int(parent.Fd()), syscall.LOCK_EX); err != nil {
		return false, err
	}
	original, err := privateRead(configPath)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	if !json.Valid(original) {
		return false, errors.New("invalid runtime configuration JSON")
	}
	if err := decoder.Decode(&doc); err != nil {
		return false, err
	}
	if doc == nil {
		return false, errors.New("runtime configuration must be an object")
	}
	existing, ok := doc["graph_assets"].(string)
	if _, present := doc["graph_assets"]; present && !ok {
		return false, nil
	}
	target := filepath.Join(newResourcesDir, graphAssetsSuffix)
	if existing == target {
		return false, nil
	}
	if existing != "" {
		if _, ok := managedGraphAssetsHash(installationRoot, existing); !ok {
			return false, nil
		}
	}
	doc["graph_assets"] = target
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	current, err := privateRead(configPath)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(original, current) {
		return false, errors.New("runtime configuration changed during graph assets refresh")
	}
	if err := atomicPrivateFile(configPath, append(updated, '\n')); err != nil {
		return false, err
	}
	return true, nil
}
