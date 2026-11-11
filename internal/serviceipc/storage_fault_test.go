package serviceipc

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestStorageFailureClassificationRequiresTypedEvidence(t *testing.T) {
	for _, q := range []struct {
		err  error
		want string
	}{{&os.PathError{Op: "write", Path: "/never-output", Err: syscall.ENOSPC}, "storage_full"}, {fmt.Errorf("wrapped: %w", syscall.EROFS), "storage_read_only"}, {syscall.EACCES, "storage_permission"}, {errors.New("no space left on device"), ""}, {&ServiceError{Code: "storage_full", Message: "private details"}, "storage_full"}, {&ServiceError{Code: "service_error", Message: "database or disk is full"}, ""}, {errors.New("authentication failed"), ""}} {
		if got := StorageFailureCode(q.err); got != q.want {
			t.Fatalf("classification=%q want=%q", got, q.want)
		}
	}
}
