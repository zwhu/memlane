package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// OfficialSource is the only input directory the official SDK should receive.
// The original worktree is used for source validation, never passed to Enola.
// Existing bounded TS/TSX/Python selection and ignore rules remain explicit;
// this does not claim all languages supported by the upstream extractor.
type OfficialSource struct {
	Directory  string
	Repository string
	Manifest   Manifest
}

// PrepareOfficialSource captures into a new private root. The owner must use
// one stable directory slot per worktree and remove it only after the previous
// operation is reaped: the SDK's repository Data row is keyed on the full path.
// The repository basename is also stable because Enola uses it in graph IDs.
// Persisted row/path lifecycle belongs to the executor, not this helper.
func PrepareOfficialSource(ctx context.Context, worktree, directory string) (out OfficialSource, err error) {
	root, common, err := SourceRoot(ctx, worktree)
	if err != nil {
		return out, err
	}
	directory, err = privateDirectory(root, directory)
	if err != nil {
		return out, err
	}
	parent, err := os.Stat(filepath.Dir(directory))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return out, errors.New("official source parent must be private")
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return out, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(directory)
		}
	}()
	identity := sha256.Sum256([]byte(root + "\x00" + common))
	out.Directory = directory
	out.Repository = filepath.Join(directory, "repository-"+hex.EncodeToString(identity[:8]))
	out.Manifest, err = Capture(ctx, root, out.Repository)
	if err != nil {
		return out, err
	}
	if out.Manifest.Worktree != root || out.Manifest.CommonDir != common {
		return out, errors.New("official source workspace identity changed")
	}
	err = out.Verify(ctx)
	return out, err
}

// Verify checks both the current source and captured bytes. Only official
// .enola output may appear in addition to the exact captured source set.
// Source changes reject publication; the old graph must be labeled stale.
func (s OfficialSource) Verify(ctx context.Context) error {
	if s.Manifest.Digest == "" || s.Repository == "" {
		return errors.New("official source missing")
	}
	live, err := Capture(ctx, s.Manifest.Worktree, "")
	if err != nil {
		return err
	}
	if live.Digest != s.Manifest.Digest {
		return errors.New("official source changed")
	}
	root, err := os.Open(s.Repository)
	if err != nil {
		return err
	}
	defer root.Close()
	want := make(map[string]File, len(s.Manifest.Files))
	for _, file := range s.Manifest.Files {
		want[file.Path] = file
	}
	count := 0
	err = filepath.WalkDir(s.Repository, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, e := filepath.Rel(s.Repository, path)
		if e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("official source contains link")
		}
		if rel == ".enola" {
			if !entry.IsDir() {
				return errors.New("official output is not directory")
			}
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		file, ok := want[filepath.ToSlash(rel)]
		if !ok || !safePath(filepath.ToSlash(rel)) {
			return errors.New("official source contains unexpected file")
		}
		f, e := openSource(root, filepath.ToSlash(rel))
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != file.Bytes || hex.EncodeToString(sum[:]) != file.SHA256 {
			return errors.New("official captured source changed")
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(want) {
		return errors.New("official captured source incomplete")
	}
	held, err := root.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(s.Repository)
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(held, current) {
		return errors.New("official source identity changed")
	}
	return nil
}
