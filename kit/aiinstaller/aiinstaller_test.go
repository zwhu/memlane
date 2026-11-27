package aiinstaller

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testDriver struct {
	probe   func(context.Context, ComponentSpec) (Observation, error)
	install func(context.Context, ComponentSpec) error
}

func (d testDriver) Probe(ctx context.Context, s ComponentSpec) (Observation, error) {
	return d.probe(ctx, s)
}
func (d testDriver) Install(ctx context.Context, s ComponentSpec) error {
	return d.install(ctx, s)
}

func installerRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func component(root string) ComponentSpec {
	return ComponentSpec{ID: "tool", Name: "Tool", Purpose: "Test component", Version: "1.0",
		Source: "https://example.test/tool", Digest: strings.Repeat("a", 64), Destination: filepath.Join(root, "tool")}
}

func TestInstallerConsentRetryAndResume(t *testing.T) {
	root := installerRoot(t)
	var installs, verifies int
	var changes []Status
	i := Installer{Root: root, PostInstallProbeRetryDelay: time.Millisecond,
		OnChange: func(j Journal) {
			changes = append(changes, j.Status)
			clear(j.Steps)
		},
		Driver: testDriver{
			probe: func(ctx context.Context, s ComponentSpec) (Observation, error) {
				if IsPostInstallProbe(ctx) {
					verifies++
					if verifies == 1 {
						return Observation{Status: Missing, TransientTimeout: true}, nil
					}
				}
				if installs > 0 {
					return Observation{Status: Available, Path: s.Destination}, nil
				}
				return Observation{Status: Missing}, nil
			},
			install: func(context.Context, ComponentSpec) error { installs++; return nil },
		}}
	p, err := i.Detect(t.Context(), []ComponentSpec{component(root)})
	if err != nil || p.Complete() {
		t.Fatalf("detect = %+v, %v", p, err)
	}
	if err := i.Review("operation", p, "wrong", true); !errors.Is(err, ErrPlanChanged) {
		t.Fatal(err)
	}
	digest := p.Digest()
	if err := i.Review("operation", p, digest, false); err != nil {
		t.Fatal(err)
	}
	j, err := i.Snapshot()
	if err != nil || j.DeclinedDigest != digest || j.ApprovedDigest != "" {
		t.Fatalf("decline = %+v, %v", j, err)
	}
	if err := i.Run(t.Context(), "operation", p); !errors.Is(err, ErrConsent) {
		t.Fatal(err)
	}
	p.Items[0].Observation.TransientTimeout = true
	if p.Digest() != digest {
		t.Fatal("transient timeout changed consent digest")
	}
	if err := i.Review("operation", p, digest, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := i.Run(t.Context(), "operation", p); err != nil {
			t.Fatal(err)
		}
	}
	j, err = i.Snapshot()
	if err != nil || j.Status != Ready || j.Steps["tool"].Attempts != 1 || installs != 1 || verifies != 2 || !slices.Contains(changes, Ready) {
		t.Fatalf("journal = %+v, err %v, installs %d, verifies %d, changes %v", j, err, installs, verifies, changes)
	}
	if info, err := os.Stat(filepath.Join(root, "installation.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal permissions = %v, %v", info, err)
	}
}

func TestInstallerRejectsDisappearedComponentAndSymlink(t *testing.T) {
	root := installerRoot(t)
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	var installed bool
	i := Installer{Root: root, Driver: testDriver{
		probe:   func(context.Context, ComponentSpec) (Observation, error) { return Observation{Status: Missing}, nil },
		install: func(context.Context, ComponentSpec) error { installed = true; return nil },
	}}
	p := Plan{Items: []Item{{component(root), Observation{Status: Available, Path: "/opt/tool"}}}}
	if err := (Installer{Root: link}).Review("operation", p, p.Digest(), true); err == nil {
		t.Fatal("accepted symlink state directory")
	}
	if err := i.Review("operation", p, p.Digest(), true); err != nil {
		t.Fatal(err)
	}
	if err := i.Run(t.Context(), "operation", p); !errors.Is(err, ErrPlanChanged) || installed {
		t.Fatalf("reuse authorization allowed replacement: %v", err)
	}
}

func TestInstallerCancellation(t *testing.T) {
	t.Run("post-install probe", func(t *testing.T) {
		root := installerRoot(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		i := Installer{Root: root, Driver: testDriver{
			probe: func(ctx context.Context, _ ComponentSpec) (Observation, error) {
				if IsPostInstallProbe(ctx) {
					cancel()
					return Observation{Status: Missing, TransientTimeout: true}, nil
				}
				return Observation{Status: Missing}, nil
			},
			install: func(context.Context, ComponentSpec) error { return nil },
		}}
		assertCancelledRun(t, i, ctx, context.Canceled, Cancelled)
	})
	t.Run("resource monitor", func(t *testing.T) {
		root := installerRoot(t)
		full := errors.New("insufficient space")
		var installing atomic.Bool
		i := Installer{Root: root, SpaceCheckInterval: time.Millisecond,
			CheckSpace: func(context.Context) error {
				if installing.Load() {
					return full
				}
				return nil
			},
			Driver: testDriver{
				probe: func(context.Context, ComponentSpec) (Observation, error) { return Observation{Status: Missing}, nil },
				install: func(ctx context.Context, _ ComponentSpec) error {
					installing.Store(true)
					<-ctx.Done()
					return ctx.Err()
				},
			}}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		assertCancelledRun(t, i, ctx, full, Failed)
	})
}

func assertCancelledRun(t *testing.T, i Installer, ctx context.Context, want error, status Status) {
	t.Helper()
	p, err := i.Detect(ctx, []ComponentSpec{component(i.Root)})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Review("operation", p, p.Digest(), true); err != nil {
		t.Fatal(err)
	}
	if err := i.Run(ctx, "operation", p); !errors.Is(err, want) {
		t.Fatalf("Run = %v, want %v", err, want)
	}
	j, err := i.Snapshot()
	if err != nil || j.Status != status {
		t.Fatalf("Snapshot = %+v, %v", j, err)
	}
}

func TestDiagnosticsAndManualCommandsUseCallerBrand(t *testing.T) {
	brand := AppBrand{Name: "AcmeTool", ExecutablePath: "/opt/acme/bin/acme"}
	if b := brand.Normalize(); b.UserAgent != "AIInstaller/1.0" || !slices.Equal(b.DoctorArgs, []string{"doctor", "--json"}) {
		t.Fatalf("defaults = %+v", b)
	}
	input := DiagnosticInput{Error: "token=stored-secret\nHTTPS_PROXY=http://proxy-password@private-proxy.test:2021\n/Users/build-user/tool"}
	report, prompt, err := Diagnostics(brand, input)
	if err != nil || !strings.Contains(prompt, brand.ExecutablePath+" doctor --json") || !strings.Contains(prompt, brand.Name) {
		t.Fatalf("diagnostics = %s, %v", prompt, err)
	}
	data, err := DiagnosticArchive(brand, input)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	readme, err := z.Open("README.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer readme.Close()
	doc, err := io.ReadAll(readme)
	if err != nil || !bytes.Contains(doc, []byte(brand.Name)) {
		t.Fatalf("README = %s, %v", doc, err)
	}
	plan := Plan{Items: []Item{{component(t.TempDir()), Observation{Status: Missing}}}}
	script := ManualInstallScript(brand, plan, func(item Item) string {
		return ManualCLIInstallCommand(brand, item.Requirement.ID, DownloadSourceOfficial, "embedded lock")
	})
	if !strings.Contains(script, brand.ExecutablePath+" install --plan --component tool --mirror official") || !strings.Contains(script, "回到 "+brand.Name) {
		t.Fatal(script)
	}
	for _, secret := range []string{"stored-secret", "proxy-password", "private-proxy.test", "build-user", "Mem" + "lane"} {
		for _, output := range []string{report, prompt, script, string(doc)} {
			if strings.Contains(output, secret) {
				t.Fatalf("unexpected text in output: %q", secret)
			}
		}
	}
}

func TestMirrorsAndProxy(t *testing.T) {
	official := "https://codeload.github.com/example/project/tar.gz/pin"
	candidates := SourceCandidates(official, "digest", DownloadSourceChina)
	if len(candidates) != 3 || candidates[2].URL != official || candidates[0].SHA256 != "digest" {
		t.Fatalf("candidates = %+v", candidates)
	}
	modelURL := "https://huggingface.co/acme/example/resolve/pin/file"
	ref := ModelFileRef{Name: "file", URL: modelURL, SHA256: "digest"}
	if got := ModelFileCandidates("acme/example", ref, DownloadSourceChina); len(got) != 2 || got[1].URL != modelURL ||
		got[0].URL != "https://modelscope.cn/models/acme/example/resolve/master/file" {
		t.Fatalf("model candidates = %+v", got)
	}
	proxy := ParseScutilProxy(`<dictionary> {
  HTTPEnable : 1
  HTTPProxy : proxy.invalid
  HTTPPort : 8080
  ExceptionsList : <array> {
    0 : *.local.test
  }
}`)
	req, _ := http.NewRequest(http.MethodGet, "https://public.test", nil)
	u, err := proxy.ProxyURLFor(req)
	if err != nil || u == nil || u.Host != "proxy.invalid:8080" || !proxy.MatchException("SUB.local.test") || proxy.MatchException("local.test") {
		t.Fatalf("proxy = %+v, %v, %v", proxy, u, err)
	}
}

func TestProgressThrottleAndBatch(t *testing.T) {
	var events []InstallProgress
	ctx := WithInstallProgressInterval(t.Context(), time.Hour, func(p InstallProgress) { events = append(events, p) })
	ProgressComponent(ctx, "tool", true)
	ProgressBatch(ctx, 7)
	ProgressAttempt(ctx, "downloading", "https://mirror.test/tool", 1, 2)
	ProgressSetLength(ctx, 4)
	n := len(events)
	if _, err := io.Copy(io.Discard, WrapProgressReader(ctx, strings.NewReader("data"))); err != nil {
		t.Fatal(err)
	}
	if len(events) != n {
		t.Fatal("read bypassed the configured throttle")
	}
	ProgressFinishTransfer(ctx, 4, false)
	ProgressAttempt(ctx, "downloading", "https://mirror.test/next", 1, 2)
	ProgressSetLength(ctx, 3)
	ProgressFinishTransfer(ctx, 3, true)
	last := events[len(events)-1]
	if last.BytesDone != 7 || last.BytesTotal != 7 || last.NetworkBytesDownloaded != 4 {
		t.Fatalf("progress = %+v", last)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDownloaderFailoverRedirectAndDigest(t *testing.T) {
	const data = "verified"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
	root := t.TempDir()
	d := Downloader{Options: DownloadOptions{UserAgent: "Acme/1",
		ExtraHeaders: func(*url.URL) http.Header { return http.Header{"Authorization": []string{"Bearer sample"}} },
		HTTPClientFactory: func(context.Context) *http.Client {
			return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.UserAgent() != "Acme/1" {
					t.Errorf("User-Agent = %q", req.UserAgent())
				}
				status, body := http.StatusOK, data
				headers := make(http.Header)
				switch req.URL.Path {
				case "/bad":
					body = "unverified"
				case "/redirect":
					status = http.StatusFound
					headers.Set("Location", "/verified")
				case "/downgrade":
					status = http.StatusFound
					headers.Set("Location", "http://example.test/verified")
				case "/verified":
					if req.Header.Get("Authorization") != "" {
						t.Error("redirect forwarded Authorization")
					}
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
		}}}
	dest := filepath.Join(root, "artifact")
	if err := d.FetchArchive(t.Context(), "http://example.test/file", digest, dest); !errors.Is(err, ErrArtifactHTTPSRequired) {
		t.Fatal(err)
	}
	if err := d.FetchArchive(t.Context(), "https://example.test/downgrade", digest, dest); !errors.Is(err, ErrUnsafeArtifactRedirect) {
		t.Fatal(err)
	}
	if err := d.FetchArchive(t.Context(), "https://example.test/bad", digest, dest); !errors.Is(err, ErrArtifactDigestMismatch) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad transfer published: %v", err)
	}
	candidates := []ArtifactCandidate{{URL: "https://example.test/bad", SHA256: digest}, {URL: "https://example.test/redirect", SHA256: digest}}
	winner, err := d.FetchCandidates(t.Context(), candidates, func(ArtifactCandidate) string { return dest })
	if err != nil || winner != candidates[1] {
		t.Fatalf("winner = %+v, %v", winner, err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != data {
		t.Fatalf("destination = %q, %v", got, err)
	}
}

type stalledBody struct{ ctx context.Context }

func (r stalledBody) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestDownloaderReadStall(t *testing.T) {
	d := Downloader{Options: DownloadOptions{ReadStallTimeout: time.Millisecond,
		HTTPClientFactory: func(context.Context) *http.Client {
			return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(stalledBody{req.Context()})}, nil
			})}
		}}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := d.FetchArchive(ctx, "https://example.test/stalled", strings.Repeat("a", 64), filepath.Join(t.TempDir(), "file"))
	if !errors.Is(err, ErrArtifactStalled) {
		t.Fatal(err)
	}
}

func TestExtractAndVerifyTarGzSource(t *testing.T) {
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tarball := tar.NewWriter(gz)
	for _, name := range []string{"project/pyproject.toml", "project/lib/__init__.py"} {
		if err := tarball.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	archive, source := filepath.Join(root, "archive.tar.gz"), filepath.Join(root, "output")
	if err := os.WriteFile(archive, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	packages := []string{"lib"}
	digest := fmt.Sprintf("%x", sha256.Sum256(data.Bytes()))
	if err := ExtractTarGzPackages(t.Context(), archive, source, packages); err != nil {
		t.Fatal(err)
	}
	if err := VerifyTarGzSource(archive, source, digest, packages); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "lib", "__init__.py"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyTarGzSource(archive, source, digest, packages); err == nil {
		t.Fatal("changed source passed verification")
	}
}

func TestWorkerStateMatchesScope(t *testing.T) {
	components := []string{" z ", "a", "z", ""}
	if got := NormalizeWorkerComponents(components); !slices.Equal(got, []string{"a", "z"}) || components[0] != " z " {
		t.Fatalf("normalized components = %v, input = %v", got, components)
	}
	cfg := WorkerConfig{OperationID: "operation", PlanDigest: "digest", Components: components,
		ConfigPath: " /tmp/./runtime.json ", RuntimeHash: "hash", StartRuntime: true}
	state := WorkerState{PID: 42, State: "running", OperationID: "operation", PlanDigest: "digest",
		Components: []string{"a", "z"}, ConfigPath: "/tmp/runtime.json", StartRuntime: true}
	if !state.MatchesScope(cfg) {
		t.Fatal("matching scope with legacy receipt was rejected")
	}
	for _, change := range []func(*WorkerState){
		func(s *WorkerState) { s.State = "completed" },
		func(s *WorkerState) { s.PID = 1 },
		func(s *WorkerState) { s.OperationID = "other" },
		func(s *WorkerState) { s.PlanDigest = "other" },
		func(s *WorkerState) { s.Components = []string{"a"} },
		func(s *WorkerState) { s.ConfigPath = "/tmp/other.json" },
		func(s *WorkerState) { s.StartRuntime = false },
		func(s *WorkerState) { s.RuntimeHash = "other" },
	} {
		changed := state
		change(&changed)
		if changed.MatchesScope(cfg) {
			t.Fatalf("mismatched scope attached: %+v", changed)
		}
	}
}

func TestWorkerResolveOperationID(t *testing.T) {
	i := Installer{Root: installerRoot(t)}
	if got, err := ResolveOperationID(i, ""); got != DefaultInstallOperationID || err != nil {
		t.Fatalf("new operation = %q, %v", got, err)
	}
	if _, err := ResolveOperationID(i, "arbitrary"); !errors.Is(err, ErrOperationIDMismatch) {
		t.Fatalf("arbitrary new operation = %v", err)
	}
	p := Plan{Items: []Item{{component(i.Root), Observation{Status: Missing}}}}
	if err := i.Review("retained", p, p.Digest(), true); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveOperationID(i, ""); got != "retained" || err != nil {
		t.Fatalf("retained operation = %q, %v", got, err)
	}
	if _, err := ResolveOperationID(i, DefaultInstallOperationID); !errors.Is(err, ErrOperationIDMismatch) {
		t.Fatalf("mismatched retained operation = %v", err)
	}
}

func TestWorkerInspectReadOnly(t *testing.T) {
	root := installerRoot(t)
	path := WorkerStatePath(root)
	locked, matchingProcess := false, false
	processes := ProcessInspector{
		ProbeFileLockedReadOnly: func(lockPath string) (bool, error) {
			if want := filepath.Join(root, "installation", "installation.lock"); lockPath != want {
				t.Fatalf("lock path = %q, want %q", lockPath, want)
			}
			return locked, nil
		},
		ProcessStartMatches: func(_ context.Context, pid int, started string) (bool, error) {
			if pid != 42 || started != "process-start" {
				t.Fatalf("process identity = %d, %q", pid, started)
			}
			return matchingProcess, nil
		},
	}
	assertActive := func(want bool) {
		t.Helper()
		if _, active, err := InspectWorker(t.Context(), processes, root); active != want || err != nil {
			t.Fatalf("InspectWorker active = %t, error %v, want active = %t", active, err, want)
		}
	}
	assertActive(false)
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created installation directory: %v", err)
	}
	locked = true
	assertActive(true)
	locked = false
	if err := PreparePrivateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	receipt := NewWorkerReceipt(path, WorkerState{Schema: 1, PID: 42, Started: "process-start", State: "starting"})
	if err := receipt.writeWorkerState(func(*WorkerState) {}); err != nil {
		t.Fatal(err)
	}
	assertActive(false)
	matchingProcess = true
	assertActive(true)
}

func TestWorkerDetectOrphanProcesses(t *testing.T) {
	parent := installerRoot(t)
	root := filepath.Join(parent, "components")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	processes := ProcessInspector{
		ProcessPIDs: func(context.Context) ([]int, error) { return []int{103, 102, 101, 104, 1, os.Getpid()}, nil },
		ProcessArgs: func(pid int) ([]string, error) {
			switch pid {
			case 101:
				return []string{"runner", "pkg", "--target=" + filepath.Join(link, "future", "child")}, nil
			case 102:
				return []string{"/opt/bin/pkg", filepath.Join(root+"-neighbor", "child")}, nil
			case 103:
				return []string{"/opt/bin/pkg", filepath.Join(root, "child")}, nil
			case 104:
				return []string{"/opt/bin/other", filepath.Join(root, "child")}, nil
			default:
				t.Fatalf("read arguments for excluded PID %d", pid)
				return nil, nil
			}
		},
	}
	pids, err := DetectOrphanProcesses(t.Context(), processes, root, []string{"pkg"})
	if err != nil || !slices.Equal(pids, []int{101, 103}) {
		t.Fatalf("orphan processes = %v, %v", pids, err)
	}
}

func TestWorkerRunPublishesReceiptAndClassifiesErrors(t *testing.T) {
	runtimeError := errors.New("runtime token=private-secret")
	for _, tc := range []struct {
		name, state, stage, code string
		err                      error
	}{
		{name: "success", state: "completed", stage: "ready"},
		{name: "runtime classified", state: "needs_review", stage: "starting_runtime", code: "runtime_conflict", err: runtimeError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := installerRoot(t)
			var installed bool
			installer := Installer{Root: filepath.Dir(WorkerStatePath(root)), Driver: testDriver{
				probe: func(_ context.Context, spec ComponentSpec) (Observation, error) {
					if installed {
						return Observation{Status: Available, Path: spec.Destination}, nil
					}
					return Observation{Status: Missing}, nil
				},
				install: func(context.Context, ComponentSpec) error { installed = true; return nil },
			}}
			detect := func(ctx context.Context) (Plan, error) {
				return installer.Detect(ctx, []ComponentSpec{component(root)})
			}
			plan, err := detect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			opts := RunWorkerOptions{
				Config:    WorkerConfig{ComponentRoot: root, PlanDigest: plan.Digest(), Components: []string{"tool"}, StartRuntime: true},
				Installer: installer,
				Processes: ProcessInspector{
					ProcessStartTime: func(_ context.Context, pid int) (string, error) {
						if pid != os.Getpid() {
							t.Fatalf("worker PID = %d", pid)
						}
						return "worker-start", nil
					},
				},
				Detect:       detect,
				StartRuntime: func(context.Context) error { return tc.err },
				ClassifyError: func(err error) (string, string, string, bool) {
					return "needs_review", "runtime_conflict", err.Error(), errors.Is(err, runtimeError)
				},
			}
			if err := RunWorker(t.Context(), opts); !errors.Is(err, tc.err) || !installed {
				t.Fatalf("RunWorker = %v, want %v, installed = %t", err, tc.err, installed)
			}
			state, err := ReadWorkerState(root)
			if err != nil || state.Schema != 1 || state.OperationID != DefaultInstallOperationID ||
				state.State != tc.state || state.Stage != tc.stage || state.ErrorCode != tc.code ||
				state.HeartbeatAt == "" || strings.Contains(state.SafeMessage, "private-secret") {
				t.Fatalf("worker receipt = %+v, %v", state, err)
			}
		})
	}
}

func TestClaimComponentRoot(t *testing.T) {
	const marker = ".component-owner.json"
	identity := []byte(`{"id":"tool","version":"1.0"}`)

	t.Run("creates private directory and marker and allows idempotent reentry", func(t *testing.T) {
		base := installerRoot(t)
		dest := filepath.Join(base, "nested", "component")
		if err := ClaimComponentRoot(dest, marker, identity); err != nil {
			t.Fatalf("initial ClaimComponentRoot failed: %v", err)
		}
		dirInfo, err := os.Lstat(dest)
		if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0700 {
			t.Fatalf("directory info = %v, %v; want dir with 0700", dirInfo, err)
		}
		markerPath := filepath.Join(dest, marker)
		markerInfo, err := os.Lstat(markerPath)
		if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm() != 0600 {
			t.Fatalf("marker info = %v, %v; want regular file with 0600", markerInfo, err)
		}
		got, err := os.ReadFile(markerPath)
		if err != nil || !bytes.Equal(got, identity) {
			t.Fatalf("marker content = %q, %v; want %q", got, err, identity)
		}
		payload := filepath.Join(dest, "payload.txt")
		if err := os.WriteFile(payload, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(dest, marker, identity); err != nil {
			t.Fatalf("idempotent reentry failed: %v", err)
		}
		if kept, err := os.ReadFile(payload); err != nil || string(kept) != "keep" {
			t.Fatalf("reentry altered payload: %q, %v", kept, err)
		}
	})

	t.Run("rejects existing directory without marker or with mismatched identity", func(t *testing.T) {
		base := installerRoot(t)
		noMarker := filepath.Join(base, "unowned")
		if err := os.Mkdir(noMarker, 0700); err != nil {
			t.Fatal(err)
		}
		userFile := filepath.Join(noMarker, "user.txt")
		if err := os.WriteFile(userFile, []byte("retain"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(noMarker, marker, identity); err == nil {
			t.Fatal("accepted existing directory without marker")
		}
		if entries, err := os.ReadDir(noMarker); err != nil || len(entries) != 1 || entries[0].Name() != "user.txt" {
			t.Fatalf("unowned directory modified: %v, %v", entries, err)
		}

		mismatch := filepath.Join(base, "mismatch")
		if err := os.Mkdir(mismatch, 0700); err != nil {
			t.Fatal(err)
		}
		other := []byte(`{"id":"tool","version":"0.9"}`)
		if err := os.WriteFile(filepath.Join(mismatch, marker), other, 0600); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(mismatch, marker, identity); err == nil {
			t.Fatal("accepted existing directory with mismatched identity")
		}
		if got, err := os.ReadFile(filepath.Join(mismatch, marker)); err != nil || !bytes.Equal(got, other) {
			t.Fatalf("mismatched marker overwritten: %q, %v", got, err)
		}
	})

	t.Run("rejects non-directory, non-private directory, non-private marker, and symlink marker", func(t *testing.T) {
		base := installerRoot(t)

		regularFile := filepath.Join(base, "not-a-dir")
		if err := os.WriteFile(regularFile, []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(regularFile, marker, identity); err == nil {
			t.Fatal("accepted regular file as component destination")
		}

		publicDir := filepath.Join(base, "public-dir")
		if err := os.Mkdir(publicDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(publicDir, marker), identity, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(publicDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(publicDir, marker, identity); err == nil {
			t.Fatal("accepted non-private existing directory")
		}

		publicMarkerDir := filepath.Join(base, "public-marker")
		if err := os.Mkdir(publicMarkerDir, 0700); err != nil {
			t.Fatal(err)
		}
		publicMarker := filepath.Join(publicMarkerDir, marker)
		if err := os.WriteFile(publicMarker, identity, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(publicMarker, 0644); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(publicMarkerDir, marker, identity); err == nil {
			t.Fatal("accepted non-private marker file")
		}

		symlinkMarkerDir := filepath.Join(base, "symlink-marker")
		if err := os.Mkdir(symlinkMarkerDir, 0700); err != nil {
			t.Fatal(err)
		}
		externalMarker := filepath.Join(base, "real-marker.json")
		if err := os.WriteFile(externalMarker, identity, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(externalMarker, filepath.Join(symlinkMarkerDir, marker)); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(symlinkMarkerDir, marker, identity); err == nil {
			t.Fatal("accepted symlink marker file")
		}
	})

	t.Run("rejects relative paths and ancestor or target symlinks", func(t *testing.T) {
		base := installerRoot(t)
		if err := ClaimComponentRoot("relative/component", marker, identity); err == nil {
			t.Fatal("accepted relative destination")
		}

		realParent := filepath.Join(base, "real-parent")
		if err := os.Mkdir(realParent, 0700); err != nil {
			t.Fatal(err)
		}
		linkedParent := filepath.Join(base, "linked-parent")
		if err := os.Symlink(realParent, linkedParent); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(filepath.Join(linkedParent, "child"), marker, identity); err == nil {
			t.Fatal("accepted destination traversing symlink ancestor")
		}

		realTarget := filepath.Join(base, "real-target")
		if err := os.Mkdir(realTarget, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(realTarget, marker), identity, 0600); err != nil {
			t.Fatal(err)
		}
		linkedTarget := filepath.Join(base, "linked-target")
		if err := os.Symlink(realTarget, linkedTarget); err != nil {
			t.Fatal(err)
		}
		if err := ClaimComponentRoot(linkedTarget, marker, identity); err == nil {
			t.Fatal("accepted symlink destination")
		}
		if err := ClaimComponentRoot(linkedTarget+string(filepath.Separator), marker, identity); err == nil {
			t.Fatal("accepted symlink destination with trailing slash")
		}
	})
}
