package codegraph

import (
	"context"
	"errors"
	"os"

	"github.com/google/uuid"
)

// RemoveCompletedExtractions is called by the service's serial code owner only
// after startup recovery/operation completion. SQL supplies terminal job IDs;
// unknown directories and active jobs are never removed. No source paths enter.
func RemoveCompletedExtractions(ctx context.Context, ownerRoot string, jobs []string) (int, error) {
	for _, job := range jobs {
		id, err := uuid.Parse(job)
		if err != nil || id.String() != job {
			return 0, errors.New("invalid extraction cleanup job ID")
		}
	}
	root, err := os.OpenRoot(ownerRoot)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	before, err := root.Lstat("code-extraction")
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("extraction cleanup directory must not be a link")
	}
	dir, err := root.OpenRoot("code-extraction")
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	opened, err := dir.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return 0, errors.New("extraction cleanup directory changed")
	}
	removed := 0
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if _, err := dir.Lstat(job); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return removed, err
		}
		if err := dir.RemoveAll(job); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
