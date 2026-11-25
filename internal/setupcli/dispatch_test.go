package setupcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/kit/agents"
)

const (
	testAgent     = "trae_ide"
	testInstance  = "trae_ide-0123456789abcdef01234567"
	testOperation = "ui-cli-retained"
	testToken     = "fixture-management-token-1234567890"
)

var testSHA = strings.Repeat("a", 64)
var testFingerprint = strings.Repeat("f", 64)

type fixture struct {
	t                              *testing.T
	r                              Runner
	base, root, config, store, ops string
	assets, bootstrapAssets        fs.FS
	owner                          bootstrap.OwnerRuntimeStatus
	doc                            runtimeConfig
	plan                           bootstrap.Plan
	row                            controlplane.AgentConnection
	worker                         bootstrap.InstallWorkerState
	workerActive                   bool
	orphans                        []int
	now                            time.Time
	writes                         int
	revision                       int64
	handler                        func(*http.Request, requestBody) (*http.Response, error)
}

type requestBody struct {
	DryRun      bool   `json:"dry_run"`
	OperationID string `json:"operation_id"`
	Revision    int64  `json:"expected_revision"`
	ProposedSHA string `json:"proposed_sha256"`
	ReviewedSHA string `json:"reviewed_sha"`
	Profile     string `json:"profile"`
	Kind        string `json:"kind"`
	InstanceID  string `json:"instance_id"`
	Action      string `json:"action"`
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, base: base, root: filepath.Join(base, "components"), config: filepath.Join(base, "runtime.json"),
		store: filepath.Join(base, "library"), now: time.Now(), revision: 9,
		assets:          fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("runtime")}},
		bootstrapAssets: fstest.MapFS{"first-install-components.json": {Data: []byte("bootstrap")}},
	}
	const origin = "http://127.0.0.1:12345"
	f.doc = runtimeConfig{
		First: &bootstrap.FirstInstallation{Schema: 1, StoreID: strings.Repeat("a", 32), Setup: true},
		Root:  f.store, ServiceCommit: "test-commit", ServiceURL: "http://127.0.0.1:12346",
		OwnerTokenFile: filepath.Join(f.store, "owner.token"), GenerationFile: filepath.Join(f.store, "generation.json"),
		Control: controlplane.Config{URL: origin, PluginDir: filepath.Join(base, "legacy"), ManagementTokenFile: filepath.Join(f.store, "management.token")},
	}
	f.ops = controlplane.OperationsDir(f.doc.GenerationFile, f.doc.Control.ManagementTokenFile)
	f.writeConfig()
	writeJSON(t, filepath.Join(f.store, "store-identity.json"), bootstrap.StoreReceipt{
		Schema: 1, StoreID: f.doc.First.StoreID, OwnerID: "owner", KeyID: "key", SourceCommit: f.doc.ServiceCommit, StorageInitialized: true,
	})
	writeFile(t, f.doc.Control.ManagementTokenFile, []byte(testToken))
	hash, err := bootstrap.BundledRuntimeHash(f.assets)
	if err != nil {
		t.Fatal(err)
	}
	f.owner = bootstrap.OwnerRuntimeStatus{
		State: "ready", OwnerLocked: true, HasReceipt: true, OwnerPID: 123, OwnerStarted: "kernel-start",
		OwnerPhase: "ready", OwnerFingerprint: testFingerprint, RunningRuntimeHash: hash,
	}
	f.plan = bootstrap.Plan{Items: []bootstrap.Item{
		{Requirement: bootstrap.Requirement{ID: "runtime", DownloadBytes: 100}, Observation: bootstrap.Observation{Status: bootstrap.Available}},
	}}
	f.row = controlplane.AgentConnection{ID: testInstance, Kind: testAgent, Detected: true, Status: "prepared"}
	f.r = Runner{
		HomeDir: base, Executable: filepath.Join(base, "Memlane.app", "Contents", "MacOS", "Memlane"),
		Environ:                func(string) string { return "" },
		DefaultConfigFn:        func() (string, error) { return f.config, nil },
		DefaultComponentRootFn: func() (string, error) { return f.root, nil },
		DiskFreeFn:             func(string) (uint64, error) { return 3 << 30, nil },
		CheckDNSFn:             func(context.Context, string) bool { return true },
		DetectAgentsFn: func(agents.DiscoveryOptions) []agents.DetectedHost {
			return []agents.DetectedHost{{Kind: testAgent, Detected: true}}
		},
		NewEnvFn: func(_ context.Context, _ fs.FS, options bootstrap.EnvironmentOptions) (*bootstrap.Environment, error) {
			return &bootstrap.Environment{Options: options}, nil
		},
		DetectPlanFn: func(*bootstrap.Environment, context.Context) (bootstrap.Plan, error) { return f.plan, nil },
		InspectOwnerStatusFn: func(context.Context, string, string, fs.FS) (bootstrap.OwnerRuntimeStatus, error) {
			return f.owner, nil
		},
		InspectWorkerFn: func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
			return f.worker, f.workerActive, nil
		},
		DetectOrphansFn:     func(context.Context, string) ([]int, error) { return f.orphans, nil },
		FirstRuntimeReadyFn: func(context.Context, string) (bool, error) { return true, nil },
		ControlClientFn: func(config controlplane.Config) *controlplane.Client {
			return &controlplane.Client{Config: config, HTTP: &http.Client{Transport: transportFunc(f.request)}}
		},
		NowFn: func() time.Time { return f.now },
		SleepFn: func(_ context.Context, d time.Duration) error {
			f.now = f.now.Add(d)
			return nil
		},
	}
	return f
}

func (f *fixture) writeConfig() {
	f.t.Helper()
	writeJSON(f.t, f.config, struct {
		runtimeConfig
		GenerationMode string `json:"generation_mode"`
	}{f.doc, "unconfigured"})
}

func (f *fixture) command(want int, args ...string) envelope {
	f.t.Helper()
	return f.rawCommand(want, args).envelope
}

type commandOutput struct {
	envelope
	raw string
}

func (f *fixture) rawCommand(want int, args []string) commandOutput {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	handled, got := f.r.DispatchIO(context.Background(), args, f.assets, f.bootstrapAssets, "", &stdout, &stderr)
	if !handled || got != want || stderr.Len() != 0 {
		f.t.Fatalf("%v: handled=%v exit=%d (want %d) stderr=%q stdout=%s", args, handled, got, want, &stderr, &stdout)
	}
	raw := stdout.String()
	var e envelope
	if err := json.Unmarshal(stdout.Bytes(), &e); err != nil {
		f.t.Fatalf("%v: invalid envelope: %v (%s)", args, err, raw)
	}
	if e.ExitCode != want || e.Status != statuses[want] || e.OK != (want == 0) || e.SchemaVersion != 1 {
		f.t.Fatalf("%v: inconsistent envelope: %+v", args, e)
	}
	if e.Blockers == nil || e.NextActions == nil || e.HumanSteps == nil || e.Environment.SelectedAgents == nil || e.Components.Items == nil ||
		e.Connections.Pending == nil || e.Connections.Hosts == nil || e.Generation.AvailableBridges == nil || e.Generation.RecommendedProcessingCLIs == nil {
		f.t.Fatalf("%v: null slice in envelope", args)
	}
	return commandOutput{e, raw}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, data)
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func response(code int, output any) (*http.Response, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
}

func (f *fixture) request(req *http.Request) (*http.Response, error) {
	var body requestBody
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
	}
	if req.Method != http.MethodGet && !body.DryRun {
		f.writes++
	}
	if f.handler != nil {
		if res, err := f.handler(req, body); res != nil || err != nil {
			return res, err
		}
	}
	switch {
	case req.URL.Path == "/v1/connections":
		return response(200, controlplane.ConnectionsDirectory{Revision: f.revision, Connections: []controlplane.AgentConnection{f.row}})
	case strings.HasSuffix(req.URL.Path, "/preview"):
		return response(200, controlplane.ConnectionPreview{Revision: f.revision, ProposedSHA256: testSHA})
	case strings.HasSuffix(req.URL.Path, "/reconcile") && body.DryRun:
		return response(200, controlplane.ConnectionOperationReceipt{Recorded: true, Status: "unknown", Reconcilable: true, Phase: "pre_mutation"})
	case strings.HasSuffix(req.URL.Path, "/repair-trae-hooks") && body.DryRun:
		return response(200, controlplane.TraeHooksRepairResult{Revision: f.revision, ReviewedSHA: testSHA, NeedsHookRepair: true, Repairable: true})
	case strings.HasPrefix(req.URL.Path, "/v1/connections/operations/") && req.Method == http.MethodGet:
		return response(200, controlplane.ConnectionOperationReceipt{Recorded: true, InstanceID: f.row.ID, Action: "activate"})
	case req.URL.Path == "/v1/generation":
		return response(200, controlplane.GenerationDirectory{})
	}
	return response(500, map[string]string{"code": "unexpected_mock_request"})
}

func (f *fixture) pending() {
	f.t.Helper()
	_, err := controlplane.RecordUIOperation(f.ops, controlplane.UIOperation{
		Kind: "connection_activate", Operation: testOperation, Profile: testInstance, Revision: f.revision,
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func hasBlocker(e envelope, code string) bool {
	for _, item := range e.Blockers {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestSetupCLIHasZeroMyGoDependency(t *testing.T) {
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-deps", "github.com/zwhu/memlane/internal/setupcli")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v: %s", err, out)
	}
	for _, path := range []string{"github.com/egoist/mygo", "internal/simpledesktop"} {
		if bytes.Contains(out, []byte(path)) {
			t.Fatalf("unexpected dependency %s", path)
		}
	}
}

func TestDispatchAllowlistAndExitCodes(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{nil, {"-psn_0_123456"}, {"-AppleLanguages"}, {"-NSRecentDocumentsLimit"}, {"--check-first-store-resources"}, {"launch-ui"}} {
		var out bytes.Buffer
		handled, code := f.r.DispatchIO(context.Background(), args, nil, nil, "", &out, &out)
		if handled || code != 0 || out.Len() != 0 {
			t.Fatalf("GUI args %q were consumed", args)
		}
	}
	for _, args := range [][]string{
		{"--cli"}, {"--cli", "unknown"}, {"--cli", "-AppleLanguages"}, {"doctor", "--unknown"}, {"doctor", "garbage"},
		{"install", "--plan", "--apply"}, {"install", "--wait", "99"}, {"install", "--wait", "-1"},
		{"install", "--wait", "44", "--wait", "12"}, {"install", "--component", "bad"}, {"install", "--apply"},
		{"repair", "bogus"}, {"repair"}, {"repair", "stale-owner", "stale-daemon"}, {"repair", "pending-ops", "--apply"},
		{"connect", "--agent", "bogus"}, {"connect"}, {"connect", "--agent", "pi", "--phase", "bogus"},
		{"connect", "--agent", "pi", "--phase", "select-env", "--apply"}, {"connect", "--agent", "pi", "--apply"},
		{"doctor", "--config", "relative"}, {"doctor", "--component-root=relative"}, {"doctor", "--mirror", "bogus"},
		{"verify", "--stage=all"}, {"stop", "--apply"}, {"stop", "--apply", "--dry-run"}, {"version", "garbage"},
	} {
		f.command(ExitInvalidArguments, args...)
	}
	for _, args := range [][]string{{"doctor", "--api-key", "foo"}, {"connect", "--agent", "codex", "--token", "bar"},
		{"developer", "--token", "bar"}, {"stop", "--LLM_API_KEY=foo"}, {"verify", "--vendor-secret=bar"}} {
		if args[0] == "developer" {
			args = append([]string{"--cli"}, args...)
		}
		res := f.rawCommand(ExitFatal, args)
		if !hasBlocker(res.envelope, "secret_input_forbidden") || strings.Contains(res.raw, "foo") || strings.Contains(res.raw, "bar") {
			t.Fatalf("secret rejection failed for %v", args[:1])
		}
	}
	f.r.Environ = func(name string) string {
		if name == "MEMLANE_API_KEY" {
			return "secret-env-value"
		}
		return ""
	}
	f.command(ExitFatal, "doctor")
	f.r.Environ = func(name string) string {
		if name == "MEMLANE_DEV_RUNTIME_CONFIG" {
			t.Error("development config environment was consulted")
		}
		return ""
	}
	for _, args := range [][]string{{"doctor"}, {"--cli", "doctor", "--json"}} {
		e := f.command(ExitActionRequired, args...)
		if e.Environment.StoreMode != "first_install_setup" || len(e.Generation.RecommendedProcessingCLIs) == 0 ||
			!strings.Contains(e.Generation.MissingProcessingCLIHint, "记忆") || strings.Contains(e.Generation.MissingProcessingCLIHint, "知识") {
			t.Fatalf("unexpected doctor data: %+v %+v", e.Environment, e.Generation)
		}
	}
	dev := filepath.Join(f.base, ".local", "daily", "runtime.json")
	writeJSON(t, dev, map[string]string{"official_data": f.store})
	e := f.command(ExitOK, "doctor", "--config", dev)
	if e.Environment.StoreMode != "external_readonly" {
		t.Fatalf("expected external config, got %+v", e.Environment)
	}
	for _, args := range [][]string{
		{"install", "--apply", "--plan-digest", f.plan.Digest()}, {"connect", "--apply", "--agent", "codex", "--phase", "prepare"},
		{"repair", "stale-owner", "--apply"}, {"stop", "--apply", "--owner-fingerprint", testFingerprint},
	} {
		e := f.command(ExitFatal, append(args, "--config", dev)...)
		if !hasBlocker(e, "non_first_install_config_read_only") {
			t.Fatalf("missing external config blocker for %v", args[:1])
		}
	}
	for _, args := range [][]string{{"version", "--json"}, {"--version"}, {"help"}, {"--help", "--json"}, {"-h"}} {
		e := f.command(ExitOK, args...)
		if e.Capabilities == nil || len(e.Capabilities.ExitCodes) != 8 || e.App.BundledRuntimeHash != f.owner.RunningRuntimeHash || e.App.Version != DefaultVersion {
			t.Fatalf("incomplete version/help manifest: %+v", e)
		}
	}
}

func TestConfigPathSafetyAndBundleVersion(t *testing.T) {
	f := newFixture(t)
	alias := filepath.Join(f.base, "alias")
	if err := os.Symlink(f.store, alias); err != nil {
		t.Fatal(err)
	}
	f.command(ExitFatal, "doctor", "--component-root", alias+"/../components")
	f.command(ExitFatal, "doctor", "--config", filepath.Join(alias, "missing.json"))
	configLink := filepath.Join(f.base, "linked.json")
	if err := os.Symlink(f.config, configLink); err != nil {
		t.Fatal(err)
	}
	f.command(ExitFatal, "doctor", "--config", configLink)
	oversize := filepath.Join(f.base, "oversize.json")
	writeFile(t, oversize, bytes.Repeat([]byte(" "), (2<<20)+1))
	f.command(ExitFatal, "doctor", "--config", oversize)
	bundle := filepath.Join(f.base, "Memlane.app", "Contents")
	writeFile(t, filepath.Join(bundle, "Info.plist"), []byte(`<plist><dict><key>CFBundleShortVersionString</key><string>9.8.7</string></dict></plist>`))
	f.r.Executable = filepath.Join(bundle, "MacOS", "Memlane")
	if e := f.command(ExitOK, "version"); e.App.Version != "9.8.7" {
		t.Fatalf("bundle version = %s", e.App.Version)
	}
}

func TestTier0StrictlyReadOnly(t *testing.T) {
	f := newFixture(t)
	f.pending()
	writeFile(t, filepath.Join(f.store, "catalog.sqlite3"), []byte("SQLite format 3\x00"))
	writeJSON(t, filepath.Join(f.store, "registry.json"), map[string]int64{"revision": f.revision})
	writeFile(t, filepath.Join(f.base, "runtime-logs", "launcher.log"), []byte("starting\nready\n"))
	type entry struct {
		mode    fs.FileMode
		mtime   time.Time
		content string
	}
	snapshot := func() map[string]entry {
		result := map[string]entry{}
		err := filepath.WalkDir(f.base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			e := entry{mode: info.Mode(), mtime: info.ModTime()}
			if !d.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				e.content = string(data)
			}
			result[path] = e
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	initialRevision := f.revision
	for _, tc := range []struct {
		code int
		args []string
	}{
		{ExitActionRequired, []string{"doctor", "--json"}},
		{ExitOK, []string{"install", "--plan", "--json"}},
		{ExitActionRequired, []string{"connect", "--agent", testAgent, "--dry-run", "--json"}},
		{ExitOK, []string{"repair", "stale-daemon", "--dry-run", "--json"}},
		{ExitActionRequired, []string{"repair", "pending-ops", "--dry-run", "--json"}},
		{ExitActionRequired, []string{"repair", "--json", "trae-hooks", "--dry-run"}},
		{ExitInProgressOrBusy, []string{"repair", "stale-owner", "--dry-run", "--json"}},
		{ExitOK, []string{"verify", "--stage", "setup", "--expect-zero-downloads", "--expect-runtime-current", "--json"}},
		{ExitActionRequired, []string{"verify", "--stage", "connection", "--agent", testAgent, "--json"}},
		{ExitActionRequired, []string{"stop", "--dry-run", "--json"}},
		{ExitOK, []string{"version", "--json"}},
		{ExitOK, []string{"--help", "--json"}},
	} {
		f.command(tc.code, tc.args...)
		if after := snapshot(); !reflect.DeepEqual(before, after) || f.writes != 0 || f.revision != initialRevision {
			t.Fatalf("%v changed persistent state or issued a mutating request", tc.args)
		}
	}
}

func TestCLIOutputNeverLeaksSecrets(t *testing.T) {
	f := newFixture(t)
	canary := "canarysecret9f8e7d6c5b4a3210"
	receiptCanary := "upgrade-receipt-error-canary"
	f.owner.UpgradeReceiptError = receiptCanary
	sk := "sk-secret-0123456789abcdef"
	proxy := "http://user:" + canary + "@127.0.0.1:8888"
	privatePath := "/tmp/.local/daily/owner.token"
	payload := canary + " " + sk + " Bearer " + testToken + " " + proxy + " " + privatePath + " hooks.json {\"hooks\":[\"" + canary + "\"]}"
	writeFile(t, filepath.Join(f.base, "hooks.json"), []byte(payload))
	f.r.Environ = func(key string) string {
		if oneOf(key, "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY") {
			t.Error("CLI read a proxy environment variable")
			return proxy
		}
		return ""
	}
	f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/runtime/monitor":
			return response(200, controlplane.ServiceMonitorSnapshot{LogTail: append(make([]string, 60), payload)})
		default:
			return response(503, map[string]string{"code": canary, "error": payload, "phase": canary})
		}
	}
	f.plan.Items[0].Observation.Reason = payload
	f.plan.Items[0].Observation.Path = privatePath
	f.worker = bootstrap.InstallWorkerState{Schema: 1, OperationID: "first-install", State: "failed", ErrorCode: canary,
		Stage: canary, Component: canary, Mirror: proxy, SafeMessage: payload, Detail: payload}
	statePath, err := bootstrap.WorkerStatePath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, statePath, bootstrap.InstallWorkerState{Schema: 1, State: "failed", SafeMessage: "Component installation failed."})
	for _, tc := range []struct {
		code int
		args []string
	}{
		{ExitFatal, []string{"doctor", "--include-logs", "--operation-id", "first-install"}},
		{ExitOK, []string{"install", "--plan"}},
		{ExitPreflightBlocked, []string{"connect", "--agent", testAgent}},
		{ExitPreflightBlocked, []string{"repair", "trae-hooks"}},
		{ExitPreflightBlocked, []string{"verify", "--stage", "connection"}},
		{ExitNeedsReview, []string{"verify", "--expect-zero-downloads"}},
		{ExitActionRequired, []string{"stop"}},
	} {
		out := f.rawCommand(tc.code, tc.args)
		if tc.args[0] == "doctor" && len(out.Artifacts.LogTail) != 1 {
			t.Fatal("doctor did not include the bounded redacted log")
		}
		state, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatal(err)
		}
		serialized := strings.Replace(out.raw, agentGuidance, "", 1) + string(state)
		for _, forbidden := range []string{canary, receiptCanary, sk, testToken, ".local/", proxy, "hooks.json"} {
			if strings.Contains(serialized, forbidden) {
				t.Fatalf("secret material leaked for %s", tc.args[0])
			}
		}
	}
	f.r.DetectPlanFn = func(*bootstrap.Environment, context.Context) (bootstrap.Plan, error) {
		return bootstrap.Plan{}, errors.New(payload)
	}
	out := f.rawCommand(ExitFatal, []string{"doctor"})
	if strings.Contains(out.raw, canary) {
		t.Fatal("probe error leaked")
	}
}

func TestSafePathCanonicalizesMacOSSystemAliases(t *testing.T) {
	f := newFixture(t)
	temp, err := os.MkdirTemp("/tmp", "memlane-safe-path-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(temp) })
	canonical, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolveSafePath(filepath.Join(temp, "components")); err != nil || got != filepath.Join(canonical, "components") {
		t.Fatalf("system temporary directory resolved to %q: %v", got, err)
	}
	if runtime.GOOS == "darwin" {
		if got, err := resolveSafePath("/var/folders"); err != nil || got != "/private/var/folders" {
			t.Fatalf("system var resolved to %q: %v", got, err)
		}
	}
	f.r.DefaultConfigFn = func() (string, error) { return filepath.Join(temp, "runtime.json"), nil }
	e := f.command(ExitActionRequired, "doctor", "--component-root", filepath.Join(temp, "components"))
	if e.Environment.ConfigPath != filepath.Join(canonical, "runtime.json") || e.Environment.ComponentRoot != filepath.Join(canonical, "components") {
		t.Fatalf("loadPaths retained system aliases: %+v", e.Environment)
	}
	f.r.DefaultConfigFn = func() (string, error) { return f.config, nil }
	f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
		if req.URL.Path == "/v1/connections/environment" {
			if body.Profile != filepath.Join(canonical, "profile") {
				t.Fatalf("open profile path retained a system alias: %q", body.Profile)
			}
			return response(200, controlplane.ConnectionsDirectory{Revision: f.revision + 1})
		}
		return nil, nil
	}
	connect := []string{"connect", "--agent", testAgent, "--phase", "select-env", "--profile"}
	f.command(ExitOK, append(slices.Clone(connect), filepath.Join(temp, "profile"), "--apply")...)
	link := filepath.Join(temp, "user-link")
	if err := os.Symlink(f.base, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{link + "/components", temp + "/../components"} {
		e := f.command(ExitFatal, "doctor", "--component-root", bad)
		if !hasBlocker(e, "unsafe_local_path") {
			t.Fatalf("missing unsafe path blocker: %+v", e)
		}
	}
	e = f.command(ExitFatal, append(slices.Clone(connect), link+"/profile", "--apply")...)
	if !hasBlocker(e, "unsafe_local_path") || f.writes != 1 {
		t.Fatalf("environment selection accepted a nested symlink: %+v", e)
	}
	if _, err := resolveSafePath(temp + "/nul\x00"); !errors.Is(err, errUnsafePath) {
		t.Fatalf("NUL was not rejected: %v", err)
	}
}
