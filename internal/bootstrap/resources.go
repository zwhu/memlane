package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// PrepareRuntimeResources materializes only code and manifests. Nothing is
// downloaded, executed or inferred from a checkout on the user's computer.
func PrepareRuntimeResources(ctx context.Context, root string, assets fs.FS) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var destination string
	err := (&Installer{Root: root}).withLock(func() error {
		var err error
		destination, err = prepareRuntimeResources(ctx, root, assets)
		return err
	})
	return destination, err
}

func prepareRuntimeResources(ctx context.Context, root string, assets fs.FS) (string, error) {
	data := map[string][]byte{}
	err := fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(name, "engine/python/") && strings.HasPrefix(path.Base(name), "test_") {
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
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("runtime assets missing")
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
	id := hex.EncodeToString(hash.Sum(nil))
	destination := filepath.Join(root, "runtime-"+id[:16])
	marker := ".memlane-runtime.json"
	identity, _ := json.Marshal(map[string]string{"sha256": id})
	if err := claimComponentRoot(destination, marker, identity); err != nil {
		return "", err
	}
	if b, err := privateRead(filepath.Join(destination, marker)); err != nil || string(b) != string(identity) {
		return "", errors.New("runtime resource ownership differs; original files retained")
	}
	// Extra modules could affect Python imports even when every expected file
	// still matches. Reject them before executing anything from this directory.
	err = filepath.WalkDir(destination, func(target string, entry fs.DirEntry, err error) error {
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
		if name != marker {
			if _, ok := data[name]; !ok {
				return errors.New("unexpected runtime resource retained: " + name)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", err
		}
		if b, err := privateRead(target); err == nil {
			if string(b) != string(data[name]) {
				return "", errors.New("runtime resource modified: " + path.Base(name))
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := atomicPrivateFile(target, data[name]); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	for _, name := range []string{".cache", "build/disk-cleanup"} {
		if err := os.MkdirAll(filepath.Join(destination, name), 0700); err != nil {
			return "", err
		}
	}
	return destination, nil
}
