package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/google/uuid"
)

// CodePrune only removes closed derived snapshots in registered datasets. The
// service must hold its worker gate; no build/query may be running concurrently.
// SQLite sources, jobs, receipts and knowledge are never in this directory.
func (c *Client) CodePrune(ctx context.Context, keep map[string][]string) (int, error) {
	for dataset, snapshots := range keep {
		if !canonicalUUID(dataset) || len(snapshots) > 2 {
			return 0, errors.New("invalid code retention set")
		}
		for _, snapshot := range snapshots {
			if !canonicalUUID(snapshot) {
				return 0, errors.New("invalid retained code snapshot")
			}
		}
	}
	root, err := os.OpenRoot(c.config.DataDir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	code, err := openCodeDirectory(root, "code")
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer code.Close()
	removed := 0
	for dataset, snapshots := range keep {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		dir, err := openCodeDirectory(code, dataset)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return removed, err
		}
		n, err := pruneCodeDataset(ctx, dir, snapshots)
		dir.Close()
		removed += n
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// Root permits internal relative symlinks. Reject those as well, and compare
// the opened directory identity with the no-link entry checked before open.
// Subsequent operations use the pinned Root even if its name is replaced.
func openCodeDirectory(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("code directory must not be a link")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		root.Close()
		return nil, errors.New("code directory changed during open")
	}
	return root, nil
}

func canonicalUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s
}

func pruneCodeDataset(ctx context.Context, root *os.Root, keep []string) (int, error) {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	retained := map[string]bool{}
	for _, id := range keep {
		retained[id] = true
	}
	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		name := entry.Name()
		stage := strings.HasPrefix(name, ".building-") && canonicalUUID(strings.TrimPrefix(name, ".building-"))
		if retained[name] || (!canonicalUUID(name) && !stage) {
			continue
		}
		if err := root.RemoveAll(name); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
