package bootstrap

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedInstallFailureRetainsExitCodeAndRedactsOutput(t *testing.T) {
	err := runManaged(context.Background(), t.TempDir(), "/bin/sh", "-c", "echo 'API_KEY=private-value' >&2; exit 7")
	var failure *CommandFailure
	if !errors.As(err, &failure) || failure.ExitCode != 7 || strings.Contains(failure.Output, "private-value") {
		t.Fatal(err)
	}
}
func TestManagedInstallCancellationStopsOwnedProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runManaged(ctx, t.TempDir(), "/bin/sh", "-c", "sleep 30 & wait")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatal("owned install did not stop promptly", err)
	}
}

func TestManagedInstallMissingExecutableRetainsOriginalLaunchError(t *testing.T) {
	err := runManaged(context.Background(), t.TempDir(), "/nonexistent/memlane-python")
	var failure *CommandFailure
	if !errors.As(err, &failure) || failure.ExitCode >= 0 || !strings.Contains(failure.Output, "no such file") {
		t.Fatal(err)
	}
}

func TestRuntimeInstallHandlesApplicationSupportPathWithRealUV(t *testing.T) {
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not installed; real parser regression requires uv")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python not installed")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := RuntimeDriver{Lock: []byte("# empty lock: no network or dependencies\n"),
		Python: func(context.Context) (string, error) { return python, nil },
		UV:     func(context.Context) (string, error) { return uv, nil },
	}
	r := Requirement{ID: "runtime", Digest: d.Digest(), Destination: filepath.Join(parent, "Application Support", "runtime")}
	if err := d.Install(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}
