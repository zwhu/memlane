package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testDriver struct {
	observed         map[string]Observation
	installs         []string
	fail             string
	cancel           context.CancelFunc
	postInstallProbe func() (Observation, error)
}

func (d *testDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	if isPostInstallProbe(ctx) && d.postInstallProbe != nil {
		return d.postInstallProbe()
	}
	return d.observed[r.ID], nil
}
func (d *testDriver) Install(_ context.Context, r Requirement) error {
	d.installs = append(d.installs, r.ID)
	if d.fail == r.ID {
		return errors.New("network unavailable")
	}
	d.observed[r.ID] = Observation{Status: Available, Path: r.Destination}
	if d.cancel != nil {
		d.cancel()
	}
	return nil
}
func fixture(t *testing.T) (Installer, *testDriver, []Requirement) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "state")
	d := &testDriver{observed: map[string]Observation{"python": {Status: Missing}, "cognee": {Status: Missing}}}
	reqs := []Requirement{}
	for _, id := range []string{"python", "cognee"} {
		reqs = append(reqs, Requirement{ID: id, Name: id, Purpose: "engine", Version: "pinned", Source: "https://example.com/pinned", Digest: "hash", DownloadBytes: 100, InstalledBytes: 200, Destination: filepath.Join(root, id)})
	}
	return Installer{Root: root, Driver: d, CheckSpace: func(context.Context) error { return nil }}, d, reqs
}
func approved(t *testing.T, i Installer, reqs []Requirement) Plan {
	t.Helper()
	p, err := i.Detect(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Review("operation", p, p.Digest(), true); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestDetectionAndDeclineNeverInstall(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	p, err := i.Detect(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("detection wrote state")
	}
	if err := i.Review("operation", p, p.Digest(), false); err != nil {
		t.Fatal(err)
	}
	if err := i.Run(context.Background(), "operation", p); !errors.Is(err, ErrConsent) {
		t.Fatal(err)
	}
	if len(d.installs) != 0 {
		t.Fatal("declined plan installed")
	}
}

func TestPostInstallVerification(t *testing.T) {
	maxAttempts, retryDelay := postInstallProbeMaxAttempts, postInstallProbeRetryDelay
	postInstallProbeMaxAttempts, postInstallProbeRetryDelay = 2, time.Millisecond
	t.Cleanup(func() {
		postInstallProbeMaxAttempts, postInstallProbeRetryDelay = maxAttempts, retryDelay
	})
	timedOut := Observation{Status: Incompatible, Reason: "运行库探测超时（90 秒）", transientTimeout: true}
	lastTimeout := timedOut
	lastTimeout.Reason = "运行库探测超时（90 秒），原生库尚未加载完毕"
	probeErr := errors.New("probe failed")
	for _, tc := range []struct {
		name         string
		observations []Observation
		probeErr     error
		status       Status
		reason       string
	}{
		{"retry succeeds", []Observation{timedOut, {Status: Available}}, nil, Ready, ""},
		{"retry exhausted", []Observation{timedOut, lastTimeout}, nil, Failed, lastTimeout.Reason},
		{"missing module", []Observation{{Status: Incompatible, Reason: "原生运行库加载失败：ModuleNotFoundError"}}, nil, Failed, "原生运行库加载失败：ModuleNotFoundError"},
		{"checksum", []Observation{{Status: Incompatible, Reason: "checksum mismatch"}}, nil, Failed, "checksum mismatch"},
		{"driver error", []Observation{timedOut}, probeErr, Failed, probeErr.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, d, reqs := fixture(t)
			calls := 0
			d.postInstallProbe = func() (Observation, error) {
				if calls >= len(tc.observations) {
					t.Fatal("unexpected post-install probe")
				}
				o := tc.observations[calls]
				calls++
				return o, tc.probeErr
			}
			p := approved(t, i, reqs[:1])
			err := i.Run(context.Background(), "operation", p)
			if calls != len(tc.observations) || len(d.installs) != 1 {
				t.Fatalf("probe calls: %d, installs: %v", calls, d.installs)
			}
			if tc.status == Ready && err != nil {
				t.Fatal(err)
			}
			if tc.status == Failed && (err == nil || !strings.Contains(err.Error(), tc.reason)) {
				t.Fatalf("probe failure lost its reason: %v", err)
			}
			if tc.probeErr != nil && !errors.Is(err, tc.probeErr) {
				t.Fatalf("driver error lost: %v", err)
			}
			j, readErr := i.Snapshot()
			if readErr != nil {
				t.Fatal(readErr)
			}
			step := j.Steps["python"]
			if j.Status != tc.status || step.Status != tc.status || step.Attempts != 1 ||
				(err != nil && step.Error != err.Error()) {
				t.Fatalf("unexpected journal: %s, %+v", j.Status, step)
			}
		})
	}
}

func TestPostInstallProbeRetryDelayIsCancellable(t *testing.T) {
	retryDelay := postInstallProbeRetryDelay
	postInstallProbeRetryDelay = 10 * time.Second
	t.Cleanup(func() { postInstallProbeRetryDelay = retryDelay })

	i, d, reqs := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	d.postInstallProbe = func() (Observation, error) {
		calls++
		time.AfterFunc(30*time.Millisecond, cancel)
		return Observation{Status: Incompatible, transientTimeout: true}, nil
	}
	p := approved(t, i, reqs[:1])
	start := time.Now()
	err := i.Run(ctx, "operation", p)
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) >= 2*time.Second {
		t.Fatalf("retry delay did not stop promptly: %d calls, %v", calls, err)
	}
	j, err := i.Snapshot()
	if err != nil || j.Status != Cancelled || j.Steps["python"].Status != Cancelled {
		t.Fatalf("unexpected cancellation journal: %+v, %v", j, err)
	}
}

func TestTransientTimeoutDoesNotChangePlanDigest(t *testing.T) {
	t.Parallel()
	plan := Plan{Items: []Item{{Observation: Observation{Status: Incompatible, Reason: "timed out"}}}}
	before := plan.Digest()
	plan.Items[0].Observation.transientTimeout = true
	if plan.Digest() != before {
		t.Fatal("transient timeout changed the serialized plan")
	}
}

func TestReuseAndChangedExternalDependency(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	d.observed["python"] = Observation{Status: Available, Path: "/external/python", Fingerprint: "one"}
	p := approved(t, i, reqs)
	if err := i.Run(context.Background(), "operation", p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.installs, ",") != "cognee" {
		t.Fatal(d.installs)
	}
	d.observed["python"] = Observation{Status: Missing}
	if err := i.Run(context.Background(), "operation", p); !errors.Is(err, ErrPlanChanged) {
		t.Fatal(err)
	}
	if len(d.installs) != 1 {
		t.Fatal("external disappearance authorized a replacement")
	}
}
func TestInterruptedInstallResumesAndReprobesReady(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	p := approved(t, i, reqs)
	d.fail = "cognee"
	if err := i.Run(context.Background(), "operation", p); err == nil {
		t.Fatal("expected failure")
	}
	j, err := i.Snapshot()
	if err != nil || j.Steps["python"].Status != Ready || j.Steps["cognee"].Status != Failed {
		t.Fatal(j, err)
	}
	// Simulate an application restart: approval and step receipts live on disk.
	i = Installer{Root: i.Root, Driver: d, CheckSpace: i.CheckSpace}
	d.fail = ""
	if err := i.Run(context.Background(), "operation", p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.installs, ",") != "python,cognee,cognee" {
		t.Fatal(d.installs)
	}
	j, _ = i.Snapshot()
	if j.Status != Ready || j.Steps["cognee"].Attempts != 2 {
		t.Fatal(j)
	}
	// A ready receipt alone cannot bypass a new probe after external damage.
	d.observed["cognee"] = Observation{Status: Missing}
	d.fail = "cognee"
	if err := i.Run(context.Background(), "operation", p); err == nil {
		t.Fatal("stale ready receipt trusted")
	}
}
func TestCancelledInstallRechecksBeforeResume(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	p := approved(t, i, reqs)
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	if err := i.Run(ctx, "operation", p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	j, _ := i.Snapshot()
	if j.Status != Cancelled || j.Steps["python"].Status != Cancelled || j.Steps["cognee"].Attempts != 0 {
		t.Fatal(j)
	}
	d.cancel = nil
	if err := i.Run(context.Background(), "operation", p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.installs, ",") != "python,cognee" {
		t.Fatal("cancelled but installed component was downloaded again", d.installs)
	}
}

type waitingDriver struct{}

func (waitingDriver) Probe(context.Context, Requirement) (Observation, error) {
	return Observation{Status: Missing}, nil
}
func (waitingDriver) Install(ctx context.Context, _ Requirement) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestSpaceMonitorCancelsInFlightInstallation(t *testing.T) {
	prevInterval := spaceCheckInterval
	spaceCheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { spaceCheckInterval = prevInterval })
	i, _, reqs := fixture(t)
	i.Driver = waitingDriver{}
	p := approved(t, i, reqs)
	var checks atomic.Int64
	low := errors.New("runtime free space below 55 GiB")
	i.CheckSpace = func(context.Context) error {
		if checks.Add(1) > 2 {
			return low
		}
		return nil
	}
	if err := i.Run(context.Background(), "operation", p); !errors.Is(err, low) {
		t.Fatal(err)
	}
	j, err := i.Snapshot()
	if err != nil || j.Status != Failed || j.Steps["python"].Error != low.Error() {
		t.Fatal(j, err)
	}
}
func TestPlanChangeRequiresNewConsent(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	p := approved(t, i, reqs)
	p.Items[0].Requirement.Source = "https://example.com/different"
	if err := i.Run(context.Background(), "operation", p); !errors.Is(err, ErrConsent) {
		t.Fatal(err)
	}
	if err := i.Review("operation", p, "stale-displayed-digest", true); !errors.Is(err, ErrPlanChanged) {
		t.Fatal(err)
	}
	if len(d.installs) != 0 {
		t.Fatal("changed plan installed")
	}
}
func TestResourceGuardBeforeInstall(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	p := approved(t, i, reqs)
	i.CheckSpace = func(context.Context) error { return errors.New("space below guard") }
	if err := i.Run(context.Background(), "operation", p); err == nil || len(d.installs) != 0 {
		t.Fatal(err, d.installs)
	}
}
func TestStateSymlinkRejected(t *testing.T) {
	t.Parallel()
	i, _, reqs := fixture(t)
	target := t.TempDir()
	if err := os.Symlink(target, i.Root); err != nil {
		t.Fatal(err)
	}
	p, _ := i.Detect(context.Background(), reqs)
	if err := i.Review("operation", p, p.Digest(), true); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "installation.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("external directory changed")
	}
}
func TestDiagnosticsWithoutRuntimeAndRedaction(t *testing.T) {
	t.Parallel()
	code := 1
	r, prompt, err := Diagnostics(DiagnosticInput{Operation: "op", Stage: "python", ExitCode: &code, Error: "download failed", InstallerLog: "Authorization: Bearer private-token\nAPI_KEY=top-secret\nhttps://user:pw@example.com/file?custom=unknown-secret#fragment\n/Users/private-user/cache\nsk-testsecret123456\neyJtest.abc.def"})
	if err != nil || !json.Valid([]byte(r)) {
		t.Fatal(r, err)
	}
	for _, secret := range []string{"private-token", "top-secret", "user:pw", "unknown-secret", "private-user", "sk-testsecret", "eyJtest"} {
		if strings.Contains(r, secret) || strings.Contains(prompt, secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	if !strings.Contains(prompt, "文字回复不能") || !strings.Contains(r, "download failed") {
		t.Fatal(prompt)
	}
}

func TestRedactInstallerTextCoversProxyURLsAndSocksSchemes(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		"HTTP_PROXY=http://alice:topsecret@corp-proxy.internal:8080",
		"HTTPS_PROXY=https://bob:pw123@10.20.30.40:8443",
		"ALL_PROXY=socks5h://carol:s3cr3t@10.0.0.5:1080",
		"failed via proxy http://192.168.55.9:7890",
		"Get https://github.com/astral-sh/uv/releases/download/0.12.3/uv.tar.gz: proxyconnect tcp: dial tcp 172.16.8.4:3128: i/o timeout",
		"dial socks5://dave:pw456@198.51.100.7:1081 refused",
		"socks5h://127.0.0.1:7891 handshake failed",
		"bare cred url http://eve:secretpass@private-host.corp:9090/internal",
	}, "\n")

	out := RedactInstallerText(input)
	for _, leaked := range []string{
		"alice", "topsecret", "corp-proxy.internal", "8080",
		"bob", "pw123", "10.20.30.40", "8443",
		"carol", "s3cr3t", "10.0.0.5", "1080",
		"192.168.55.9", "7890",
		"172.16.8.4", "3128",
		"dave", "pw456", "198.51.100.7", "1081",
		"127.0.0.1:7891",
		"eve", "secretpass", "private-host.corp", "9090",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("proxy or credential token %q leaked in redacted output:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "https://github.com/astral-sh/uv/releases/download/0.12.3/uv.tar.gz") {
		t.Fatalf("public download URL should remain readable:\n%s", out)
	}
	if !strings.Contains(out, "socks5://[代理已脱敏]") || !strings.Contains(out, "socks5h://[代理已脱敏]") {
		t.Fatalf("expected socks proxy placeholders in redacted output:\n%s", out)
	}
}

// The installed App has no checkout disk reserve. Development callers may still
// supply a guard, covered by the cancellation and pre-install guard tests above.
func TestAppInstallationDoesNotRequireDevelopmentDiskGuard(t *testing.T) {
	t.Parallel()
	i, d, reqs := fixture(t)
	i.CheckSpace = nil
	p := approved(t, i, reqs)
	if err := i.Run(context.Background(), "operation", p); err != nil {
		t.Fatal(err)
	}
	if len(d.installs) != len(reqs) {
		t.Fatal("approved components not installed", d.installs)
	}
	j, err := i.Snapshot()
	if err != nil || j.Status != Ready {
		t.Fatal("installation did not finish", j, err)
	}
}
