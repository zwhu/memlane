//go:build darwin || linux

package desktop

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeliveryViewWaitsForCaptureLockAndHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent-outbox")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() { time.Sleep(75 * time.Millisecond); _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); close(released) }()
	page, err := readAgentDeliveryPage(context.Background(), root)
	<-released
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("transient capture lock became permanent UI error: %v", err)
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = readAgentDeliveryPage(ctx, root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("closed view did not cancel waiting: %v", err)
	}
}
