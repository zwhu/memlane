package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupLifecycle(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "worker.log")
	redactor := NewLogRedactor(customHeaderPattern())
	proc, err := StartManagedProcess(ServiceSpec{
		Role: "worker", Command: "/bin/sh", LogPath: logPath, LogRedactor: redactor,
		Args: []string{"-c", `trap 'wait; exit 0' TERM; /bin/sleep 60 & echo "$!"; echo 'X-Example: hidden'; wait`},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := proc.StopGroup(ctx, time.Second, time.Second); err != nil {
			t.Error(err)
		}
	})
	pgid, err := syscall.Getpgid(proc.PID())
	if err != nil || pgid != proc.PID() {
		t.Fatalf("group leader PGID = %d, PID = %d, err = %v", pgid, proc.PID(), err)
	}
	var childPID int
	until(t, func() bool {
		lines, err := redactor.ReadLogTail(logPath, 10, 1024)
		if err != nil || len(lines) < 2 {
			return false
		}
		childPID, err = strconv.Atoi(lines[0])
		return err == nil && childPID > 1
	})
	childGroup, err := syscall.Getpgid(childPID)
	if err != nil || childGroup != pgid {
		t.Fatalf("subchild PGID = %d, want %d, err = %v", childGroup, pgid, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := proc.StopGroup(ctx, time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if ProcessGroupExists(pgid) || !errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH) {
		t.Fatal("process group or subchild survived shutdown")
	}
	status := proc.Status()
	if status.State != StateStopped || !strings.Contains(strings.Join(status.TailLines, "\n"), "X-Example: [REDACTED]") {
		t.Fatalf("final status = %+v", status)
	}
}

func TestHealthDebouncesAndRecovers(t *testing.T) {
	store := t.TempDir()
	probes := make(chan chan error)
	sup := New(Config{
		StoreRoot: store, ConfigPath: filepath.Join(store, "config.json"), ExternalOwner: true,
		MonitorInterval: 5 * time.Millisecond, HealthFailureMax: 2, HealthTimeout: 5 * time.Second,
	})
	err := sup.Start(t.Context(), []ServiceSpec{{
		Role: "worker", Command: "/bin/sleep", Args: []string{"60"},
		LogPath: filepath.Join(store, "logs", "worker.log"),
		HealthCheck: func(ctx context.Context) error {
			reply := make(chan error, 1)
			select {
			case probes <- reply:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case err := <-reply:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := sup.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	nextProbe := func() chan error {
		t.Helper()
		select {
		case reply := <-probes:
			return reply
		case <-time.After(5 * time.Second):
			t.Fatal("health probe did not arrive")
			return nil
		}
	}
	failed := errors.New("unhealthy")
	reply := nextProbe()
	for _, step := range []struct {
		probeErr error
		state    ServiceState
	}{
		{failed, StateReady}, {nil, StateReady}, {failed, StateReady},
		{failed, StateDegraded}, {nil, StateReady},
	} {
		reply <- step.probeErr
		reply = nextProbe()
		snapshot := sup.Snapshot()
		if snapshot.State != step.state || snapshot.Services[0].State != step.state {
			t.Fatalf("health state = %+v, want %s", snapshot, step.state)
		}
	}
}

func customHeaderPattern() RedactPattern {
	return RedactPattern{
		Pattern: regexp.MustCompile(`(?i)(X-Example:[ \t]*)\S+`), Replacement: `${1}[REDACTED]`,
	}
}

func TestLiveLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "worker.log")
	log, err := OpenPrivateLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	before, err := log.Stat()
	if err != nil {
		t.Fatal(err)
	}
	const suffix = "Bearer default-secret\nX-Example: custom-secret\n"
	if got := RedactLogLine("X-Example: custom-secret"); got != "X-Example: custom-secret" {
		t.Fatalf("default redactor changed the custom header: %q", got)
	}
	redactor := NewLogRedactor(RedactPattern{}, customHeaderPattern())
	if err := log.Truncate(DefaultLogLimitBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := log.WriteString("discarded partial credential\n" + suffix); err != nil {
		t.Fatal(err)
	}
	if err := redactor.TrimServiceLogDir(filepath.Dir(path), 0); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || after.Size() != 0 {
		t.Fatalf("live inode was replaced or not truncated: %v, %v", after, err)
	}
	previous, err := os.ReadFile(path + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Bearer [REDACTED]\nX-Example: [REDACTED]\n"; string(previous) != want {
		t.Fatalf("archived %d bytes, want %q", len(previous), want)
	}
	if _, err := log.WriteString("Bearer " + strings.Repeat("x", 64)); err != nil {
		t.Fatal(err)
	}
	if lines, err := ReadLogTail(path, 1, 16); err != nil || len(lines) != 0 {
		t.Fatalf("partial credential exposed in tail: %v, %v", lines, err)
	}
}

func until(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
