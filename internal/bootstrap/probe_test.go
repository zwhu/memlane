package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var bootstrapWarmDispatcher string

func TestMain(m *testing.M) {
	var warmDir string
	if os.Getenv("MEMLANE_BOOTSTRAP_HELPER") != "1" && os.Getenv("MEMLANE_TEST_WORKER_ROLE") == "" {
		if dir, err := os.MkdirTemp("", "memlane-probe-warm-"); err == nil {
			warmDir = dir
			p := filepath.Join(dir, "warm-dispatcher.sh")
			body := "#!/bin/sh\nif [ -f \"$0.body.sh\" ]; then\n  . \"$0.body.sh\"\nfi\n"
			if os.WriteFile(p, []byte(body), 0500) == nil {
				_ = exec.Command(p).Run()
				bootstrapWarmDispatcher = p
			}
		}
	}
	code := m.Run()
	if warmDir != "" {
		_ = os.RemoveAll(warmDir)
	}
	os.Exit(code)
}

func writeWarmBootstrapScript(t *testing.T, target, script string) {
	t.Helper()
	if bootstrapWarmDispatcher != "" {
		if err := os.WriteFile(target+".body.sh", []byte(script+"\n"), 0600); err == nil {
			if err := os.Link(bootstrapWarmDispatcher, target); err == nil {
				return
			}
			_ = os.Remove(target + ".body.sh")
		}
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
}

func sourceFixture(t *testing.T) (archive, source, digest string) {
	t.Helper()
	root := t.TempDir()
	archive, source = filepath.Join(root, "source.tar.gz"), filepath.Join(root, "source")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "fixed upstream commit"}}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"pyproject.toml": "version='1.6.7'", "cognee/__init__.py": "# pinned upstream"} {
		if err := tw.WriteHeader(&tar.Header{Name: "upstream/" + name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(archive)
	h := sha256.Sum256(b)
	return archive, source, hex.EncodeToString(h[:])
}
func TestFixedSourceRequiresArchiveAndExactFiles(t *testing.T) {
	t.Parallel()
	archive, source, digest := sourceFixture(t)
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "cognee/__init__.py"), []byte("# modified but same version"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err == nil {
		t.Fatal("version-equivalent mutation accepted")
	}
}
func TestFixedSourceRejectsExtraModulesAndMissingPackages(t *testing.T) {
	t.Parallel()
	archive, source, digest := sourceFixture(t)
	if err := VerifySource(archive, source, digest, []string{"cognee", "missing"}); err == nil {
		t.Fatal("missing package accepted")
	}
	if err := os.WriteFile(filepath.Join(source, "cognee/injected.py"), []byte("# extra"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err == nil {
		t.Fatal("extra module accepted")
	}
}
func TestFixedSourceRejectsLinkedPackageAndWrongDigest(t *testing.T) {
	t.Parallel()
	archive, source, digest := sourceFixture(t)
	if err := VerifySource(archive, source, "wrong", []string{"cognee"}); err == nil {
		t.Fatal("wrong digest accepted")
	}
	target := filepath.Join(source, "cognee/__init__.py")
	if err := os.Rename(target, target+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target+".original", target); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err == nil {
		t.Fatal("linked package accepted")
	}
}

func setShortProbeTimeouts(t *testing.T) {
	t.Helper()
	routine, postInstall := routineProbeTimeout, postInstallProbeTimeout
	routineProbeTimeout, postInstallProbeTimeout = 150*time.Millisecond, 400*time.Millisecond
	t.Cleanup(func() { routineProbeTimeout, postInstallProbeTimeout = routine, postInstall })
}

func fakePython(t *testing.T, script string) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "python")
	writeWarmBootstrapScript(t, executable, script)
	return executable
}

func pythonVersionScript() string {
	machine := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
	return `printf '%s\n' '{"version":"3.12.12","machine":"` + machine + `"}'`
}

func TestPythonProbeTimeoutModes(t *testing.T) {
	setShortProbeTimeouts(t)
	ctx := context.Background()
	// The subprocess inherits stdout. Killing only the shell would leave the pipe open.
	python := fakePython(t, "sleep 10 & wait")
	start := time.Now()
	for _, tc := range []struct {
		ctx    context.Context
		reason string
	}{
		{ctx, "Python 版本检测超时（150 毫秒）"},
		{withPostInstallProbe(ctx), "Python 版本检测超时（400 毫秒）"},
	} {
		o, err := ProbePython(tc.ctx, python)
		if err != nil || o.Status != Incompatible || !o.transientTimeout || o.Reason != tc.reason {
			t.Fatalf("unexpected probe result: %+v, %v", o, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe subprocess pipe remained open for %s", elapsed)
	}

	parent, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	o, err := ProbePython(withPostInstallProbe(parent), python)
	if !errors.Is(err, context.DeadlineExceeded) || o.transientTimeout {
		t.Fatalf("parent deadline was classified as a probe timeout: %+v, %v", o, err)
	}
}

func TestPythonProbeDeterministicResults(t *testing.T) {
	t.Parallel()
	ctx := withPostInstallProbe(context.Background())
	for _, script := range []string{"echo invalid-json", "exit 23", `echo '{"version":"3.11.0"}'`} {
		o, err := ProbePython(ctx, fakePython(t, script))
		if err != nil || o.Status != Incompatible || o.transientTimeout {
			t.Fatalf("unexpected result for %q: %+v, %v", script, o, err)
		}
	}
	o, err := ProbePython(ctx, fakePython(t, pythonVersionScript()))
	if err != nil || o.Status != Available || o.transientTimeout {
		t.Fatalf("unexpected valid Python result: %+v, %v", o, err)
	}
}

func TestIsolatedPythonPreservesDeterministicExitAndReapsDescendant(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// Parent exits deterministically with 42 while a grandchild keeps stdout open.
	script := "sleep 15 &\necho $! > '" + pidFile + "'\nexit 42"
	python := fakeDependencyPython(t, script)
	routine, postInstall, waitDelay := routineProbeTimeout, postInstallProbeTimeout, probeWaitDelay
	// Keep timeout below isolatedPython's WaitDelay so probeCtx expires while draining pipes.
	routineProbeTimeout, postInstallProbeTimeout, probeWaitDelay = 150*time.Millisecond, 150*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() {
		routineProbeTimeout, postInstallProbeTimeout, probeWaitDelay = routine, postInstall, waitDelay
	})

	o, err := ProbeDependencies(withPostInstallProbe(context.Background()), python, []byte("locked==1"))
	if err != nil || o.Status != Incompatible || o.transientTimeout || !strings.Contains(o.Reason, "exit_code=42") {
		t.Fatalf("deterministic exit 42 lost or misclassified as timeout: %+v, %v", o, err)
	}
	raw, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("missing descendant pid file: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if parseErr != nil || pid <= 0 {
		t.Fatalf("invalid descendant pid %q: %v", string(raw), parseErr)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("descendant pid %d survived after probe exited", pid)
}

func TestIsolatedPythonPreservesCrashSignalWhenDescendantHoldsPipeUntilTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// Parent terminates with SIGABRT while a grandchild keeps stdout open past the probe timeout.
	script := "sleep 15 &\necho $! > '" + pidFile + "'\nkill -ABRT $$"
	python := fakeDependencyPython(t, script)
	routine, postInstall, waitDelay := routineProbeTimeout, postInstallProbeTimeout, probeWaitDelay
	// Keep timeout below isolatedPython's WaitDelay so probeCtx expires while draining pipes.
	routineProbeTimeout, postInstallProbeTimeout, probeWaitDelay = 150*time.Millisecond, 150*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() {
		routineProbeTimeout, postInstallProbeTimeout, probeWaitDelay = routine, postInstall, waitDelay
	})

	o, err := ProbeDependencies(withPostInstallProbe(context.Background()), python, []byte("locked==1"))
	if err != nil || o.Status != Incompatible || o.transientTimeout || !strings.Contains(o.Reason, "exit_code=-1") {
		t.Fatalf("deterministic SIGABRT misclassified as retryable timeout: %+v, %v", o, err)
	}
	raw, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("missing descendant pid file: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if parseErr != nil || pid <= 0 {
		t.Fatalf("invalid descendant pid %q: %v", string(raw), parseErr)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("descendant pid %d survived after SIGABRT probe exited", pid)
}
