package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

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
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
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
