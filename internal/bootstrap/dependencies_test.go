package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockedVersionsRequiresPinnedUnambiguousVersions(t *testing.T) {
	v, err := LockedVersions([]byte("# lock\nlancedb==0.39.0 \\\n --hash=sha256:abc\npackage_name==1.2.3\n"))
	if err != nil || v["lancedb"] != "0.39.0" || v["package-name"] != "1.2.3" {
		t.Fatal(v, err)
	}
	for _, input := range []string{"# no packages", "package_name==1\npackage-name==2\n"} {
		if _, err := LockedVersions([]byte(input)); err == nil {
			t.Fatal("invalid lock accepted", input)
		}
	}
}

func fakeDependencyPython(t *testing.T, probeScript string) string {
	t.Helper()
	return fakePython(t, "if [ \"$#\" = 4 ]; then "+pythonVersionScript()+"; exit 0; fi\n"+probeScript)
}

func TestDependencyProbeTimeoutModes(t *testing.T) {
	// This test has to complete the initial version subprocess even on a cold host.
	routine, postInstall := routineProbeTimeout, postInstallProbeTimeout
	routineProbeTimeout, postInstallProbeTimeout = 1500*time.Millisecond, 2*time.Second
	t.Cleanup(func() { routineProbeTimeout, postInstallProbeTimeout = routine, postInstall })
	python := fakeDependencyPython(t, "sleep 10")
	ctx := context.Background()
	for _, tc := range []struct {
		ctx    context.Context
		reason string
	}{
		{ctx, "运行库探测超时（1.5 秒）"},
		{withPostInstallProbe(ctx), "运行库探测超时（2 秒）"},
	} {
		o, err := ProbeDependencies(tc.ctx, python, []byte("locked==1"))
		if err != nil || o.Status != Incompatible || !o.transientTimeout || o.Reason != tc.reason {
			t.Fatalf("unexpected probe result: %+v, %v", o, err)
		}
	}

	parent, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	o, err := ProbeDependencies(withPostInstallProbe(parent), python, []byte("locked==1"))
	if !errors.Is(err, context.DeadlineExceeded) || o.transientTimeout {
		t.Fatalf("parent deadline was classified as a probe timeout: %+v, %v", o, err)
	}
}

func TestDependencyProbeReportsDeterministicFailures(t *testing.T) {
	for _, tc := range []struct {
		name, script, reason string
	}{
		{"missing package", `echo '{"mismatch":[{"name":"locked","expected":"1","actual":"missing"}]}'`, "locked需要1，已有missing"},
		{"native import", `echo '{"native_error":"ImportError"}'`, "原生运行库加载失败：ImportError"},
		{"nonzero exit", "printf '  shared library missing\\n  loader detail  " + strings.Repeat("x", 300) + "\\n' >&2; exit 42", "exit_code=42）：shared library missing loader detail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			python := fakeDependencyPython(t, tc.script)
			o, err := ProbeDependencies(withPostInstallProbe(context.Background()), python, []byte("locked==1"))
			if err != nil || o.Status != Incompatible || o.transientTimeout || !strings.Contains(o.Reason, tc.reason) {
				t.Fatalf("unexpected probe result: %+v, %v", o, err)
			}
			if strings.ContainsAny(o.Reason, "\r\n") || len([]rune(o.Reason)) > 300 {
				t.Fatalf("probe detail is not bounded and single-line: %q", o.Reason)
			}
		})
	}
}

func TestDependencyProbeRedactsStderrBeforePersistingPlan(t *testing.T) {
	python := fakeDependencyPython(t, "printf 'Authorization: Bearer secret-tok API_KEY=top-secret /Users/alice/private https://u:pw@host/p?k=secret\\n' >&2; exit 17")
	o, err := ProbeDependencies(context.Background(), python, []byte("locked==1"))
	if err != nil || o.Status != Incompatible || o.transientTimeout || !strings.Contains(o.Reason, "exit_code=17") {
		t.Fatalf("unexpected probe result: %+v, %v", o, err)
	}
	for _, secret := range []string{"secret-tok", "top-secret", "/Users/alice", "u:pw", "k=secret"} {
		if strings.Contains(o.Reason, secret) {
			t.Fatalf("secret %q leaked in observation reason: %q", secret, o.Reason)
		}
	}

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "state")
	d := RuntimeDriver{Lock: []byte("locked==1\n"), ExistingPython: python}
	inst := Installer{Root: root, Driver: d}
	req := Requirement{
		ID: "runtime", Name: "runtime", Purpose: "engine", Version: "lock",
		Source: "https://pypi.org/simple/", Digest: d.Digest(),
		DownloadBytes: 1, InstalledBytes: 1, Destination: filepath.Join(root, "runtime"),
	}
	plan, err := inst.Detect(context.Background(), []Requirement{req})
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Review("op", plan, plan.Digest(), true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "installation.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-tok", "top-secret", "/Users/alice", "u:pw", "k=secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret %q persisted to installation.json: %s", secret, string(raw))
		}
	}
}
