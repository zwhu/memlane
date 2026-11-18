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
)

type testDriver struct {
	observed map[string]Observation
	installs []string
	fail     string
	cancel   context.CancelFunc
}

func (d *testDriver) Probe(_ context.Context, r Requirement) (Observation, error) {
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
func TestReuseAndChangedExternalDependency(t *testing.T) {
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
	i, d, reqs := fixture(t)
	p := approved(t, i, reqs)
	i.CheckSpace = func(context.Context) error { return errors.New("space below guard") }
	if err := i.Run(context.Background(), "operation", p); err == nil || len(d.installs) != 0 {
		t.Fatal(err, d.installs)
	}
}
func TestStateSymlinkRejected(t *testing.T) {
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

// The installed App has no checkout disk reserve. Development callers may still
// supply a guard, covered by the cancellation and pre-install guard tests above.
func TestAppInstallationDoesNotRequireDevelopmentDiskGuard(t *testing.T) {
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
