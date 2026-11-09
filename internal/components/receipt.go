package components

import (
	"context"
	"io/fs"
	"path/filepath"
)

// PreparationReceipt identifies the actual dependency, rather than deriving
// card progress from a combined percentage. OccupiedBytes is a measurement of
// installed files (including metadata); a missing measurement stays unknown.
type PreparationReceipt struct {
	Progress
	ExpectedBytes int64
	OccupiedBytes *int64
	Reused        bool
}

func InstalledReceipt(ctx context.Context, d Descriptor, installed Installed) PreparationReceipt {
	r := PreparationReceipt{Progress: Progress{ID: d.ID, Phase: "installed", Downloaded: installed.DownloadedBytes, Total: d.Bytes}, ExpectedBytes: d.UnpackedBytes, Reused: installed.Reused}
	var size int64
	err := filepath.WalkDir(installed.Directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	if err == nil {
		r.OccupiedBytes = &size
	}
	return r
}
