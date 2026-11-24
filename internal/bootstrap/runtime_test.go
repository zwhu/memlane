package bootstrap

import (
	"context"
	"errors"
	"os"
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

func TestRuntimePostInstallProbeIgnoresExistingPython(t *testing.T) {
	routine, postInstall := routineProbeTimeout, postInstallProbeTimeout
	routineProbeTimeout, postInstallProbeTimeout = 1500*time.Millisecond, 1500*time.Millisecond
	t.Cleanup(func() { routineProbeTimeout, postInstallProbeTimeout = routine, postInstall })

	lock := []byte("locked==1\n")
	writeManaged := func(t *testing.T, script string) string {
		t.Helper()
		dest := t.TempDir()
		binDir := filepath.Join(dest, "venv", "bin")
		if err := os.MkdirAll(binDir, 0700); err != nil {
			t.Fatal(err)
		}
		managed := filepath.Join(binDir, "python")
		body := "#!/bin/sh\nif [ \"$#\" = 4 ]; then " + pythonVersionScript() + "; exit 0; fi\n" + script + "\n"
		if err := os.WriteFile(managed, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		return dest
	}

	t.Run("managed timeout not overwritten by incompatible existing python", func(t *testing.T) {
		dest := writeManaged(t, "sleep 10")
		existing := fakeDependencyPython(t, `echo '{"mismatch":[{"name":"locked","expected":"1","actual":"missing"}]}'`)
		d := RuntimeDriver{Lock: lock, ExistingPython: existing}
		r := Requirement{ID: "runtime", Digest: d.Digest(), Destination: dest}

		o, err := d.Probe(withPostInstallProbe(context.Background()), r)
		if err != nil || o.Status != Incompatible || !o.transientTimeout || !strings.Contains(o.Reason, "运行库探测超时") {
			t.Fatalf("expected managed cold timeout preserved, got %+v, %v", o, err)
		}
	})

	t.Run("managed deterministic failure not overwritten by existing python timeout", func(t *testing.T) {
		dest := writeManaged(t, `echo '{"mismatch":[{"name":"locked","expected":"1","actual":"missing"}]}'`)
		existing := fakeDependencyPython(t, "sleep 10")
		d := RuntimeDriver{Lock: lock, ExistingPython: existing}
		r := Requirement{ID: "runtime", Digest: d.Digest(), Destination: dest}

		o, err := d.Probe(withPostInstallProbe(context.Background()), r)
		if err != nil || o.Status != Incompatible || o.transientTimeout || !strings.Contains(o.Reason, "locked需要1，已有missing") {
			t.Fatalf("expected managed deterministic mismatch without retry, got %+v, %v", o, err)
		}
	})

	t.Run("routine detect still probes existing python for reuse", func(t *testing.T) {
		dest := t.TempDir()
		existing := fakeDependencyPython(t, `echo '{"mismatch":[],"native_error":null}'`)
		d := RuntimeDriver{Lock: lock, ExistingPython: existing}
		r := Requirement{ID: "runtime", Digest: d.Digest(), Destination: dest}

		o, err := d.Probe(context.Background(), r)
		if err != nil || o.Status != Available || o.Path != existing {
			t.Fatalf("expected routine detect to reuse compatible existing python, got %+v, %v", o, err)
		}
	})
}
