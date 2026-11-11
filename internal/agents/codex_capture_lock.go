//go:build darwin || linux

package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func lockCodexCapture(ctx context.Context, root, connection string) (*os.File, error) {
	id, err := uuid.Parse(connection)
	if err != nil || id.String() != connection || !filepath.IsAbs(root) {
		return nil, errors.New("capture_identity_invalid")
	}
	dir, err := unix.Open(filepath.Join(root, "agent-adapters", connection), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("capture_installation_unavailable")
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, "capture.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("capture_lock_unavailable")
	}
	f := os.NewFile(uintptr(fd), "capture.lock")
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		f.Close()
		return nil, errors.New("capture_lock_unavailable")
	}
	for {
		if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return f, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, errors.New("capture_lock_unavailable")
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, errors.New("capture_busy")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
