package daemon

import (
	"errors"
	"github.com/zwhu/memlane/internal/serviceipc"
	sqlite "modernc.org/sqlite"
)

// The database driver stays in the daemon, never in the lightweight IPC bridge.
func storageResponseCode(err error) string {
	if code := serviceipc.StorageFailureCode(err); code != "" {
		return code
	}
	var database *sqlite.Error
	if errors.As(err, &database) {
		switch database.Code() & 255 {
		case 13:
			return "storage_full"
		case 8:
			return "storage_read_only"
		}
	}
	return ""
}
