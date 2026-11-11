package serviceipc

import (
	"errors"
	"syscall"
)

// Classify actual typed storage errors only. Never infer free space or parse
// provider/SQLite/socket error text. Unknown errors retain their own category.
func StorageFailureCode(err error) string {
	var remote *ServiceError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "storage_full", "storage_read_only", "storage_permission":
			return remote.Code
		}
		return ""
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return "storage_full"
	}
	if errors.Is(err, syscall.EROFS) {
		return "storage_read_only"
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return "storage_permission"
	}
	return ""
}
