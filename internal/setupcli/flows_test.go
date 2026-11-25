package setupcli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
)

func TestInstallPlanApplyAndPoll(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.owner.State, f.owner.OwnerLocked = "stopped", false
	f.plan.Items[0].Observation.Status = bootstrap.Missing
	plan := f.command(ExitActionRequired, "install", "--plan", "--component", "runtime", "--mirror", "official")
	digest := f.plan.Digest()
	if plan.Components.Complete || plan.Artifacts.PlanDigest != digest || !slices.Contains(plan.NextActions[1].Args, "--component") ||
		!slices.Contains(plan.NextActions[1].Args, "official") || plan.NextActions[1].Requires != confirmation {
		t.Fatalf("incorrect plan: %+v", plan)
	}
	f.command(ExitPreflightBlocked, "install", "--apply", "--plan-digest", strings.Repeat("b", 64))
	f.orphans = []int{12}
	f.command(ExitNeedsReview, "install", "--plan")
	f.command(ExitNeedsReview, "install", "--apply", "--plan-digest", digest)
	f.orphans = nil
	f.r.LaunchWorkerFn = func(_ context.Context, executable string, args []string, cfg bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
		if executable != f.r.Executable || cfg.PlanDigest != digest || cfg.ComponentRoot != f.root || cfg.ConfigPath != f.config ||
			cfg.DownloadSource != "official" || cfg.OperationID != "first-install" || len(cfg.Components) != 0 || !cfg.StartRuntime ||
			cfg.RuntimeHash != f.owner.RunningRuntimeHash || args[0] != "__install-worker" || !slices.Contains(args, "--start-runtime") {
			t.Fatalf("incorrect detached worker invocation: %v %+v", args, cfg)
		}
		f.worker = bootstrap.InstallWorkerState{
			State: "running", OperationID: cfg.OperationID, Stage: "downloading", PID: 123,
			ConfigPath: cfg.ConfigPath, StartRuntime: cfg.StartRuntime, PlanDigest: cfg.PlanDigest,
		}
		f.workerActive = true
		return f.worker, nil
	}
	args := []string{"install", "--apply", "--plan-digest", digest, "--operation-id", "first-install", "--mirror", "official", "--wait"}
	e := f.command(ExitInProgressOrBusy, append(slices.Clone(args), "0")...)
	if !e.Components.WorkerActive || e.RetryAfterMS != 10000 || e.OperationID != "first-install" || len(e.NextActions) == 0 {
		t.Fatalf("worker polling receipt missing: %+v", e)
	}
	f.command(ExitInProgressOrBusy, "doctor", "--operation-id", "first-install")
	f.command(ExitPreflightBlocked, "install", "--apply", "--plan-digest", digest, "--operation-id", "other")
	f.r.SleepFn = func(ctx context.Context, d time.Duration) error {
		if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(f.now.Add(2*time.Second)) {
			t.Fatalf("poll did not inherit the install deadline: %v", deadline)
		}
		if d != 500*time.Millisecond {
			t.Fatalf("poll interval = %s", d)
		}
		f.now = f.now.Add(d)
		f.plan.Items[0].Observation.Status = bootstrap.Available
		f.worker.State, f.workerActive = "completed", false
		f.owner.State, f.owner.OwnerLocked = "ready", true
		return nil
	}
	e = f.command(ExitOK, append(slices.Clone(args), "2")...)
	if !e.Components.Complete || e.Daemon.State != "ready" || e.Components.WorkerActive {
		t.Fatalf("completed worker not re-probed: %+v", e)
	}
	for _, tc := range []struct {
		state, reason string
		exit          int
	}{
		{"failed", "plan_changed", ExitPreflightBlocked},
		{"failed", "install_failed", ExitFatal},
		{"cancelled", "cancelled", ExitFatal},
		{"needs_review", "orphan_install_process", ExitNeedsReview},
	} {
		f.worker.State, f.worker.ErrorCode = tc.state, tc.reason
		f.command(tc.exit, "doctor", "--operation-id", "first-install")
	}
}

func TestInstallCompletesWithExistingComponents(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		wait string
		exit int
	}{
		{"wait expires during polling", "1", ExitInProgressOrBusy},
		{"finishes during wait", "2", ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				f.owner.State, f.owner.OwnerLocked = "stopped", false
				f.row.Detected = false
				var workerCtx context.Context
				f.r.LaunchWorkerFn = func(ctx context.Context, executable string, args []string, cfg bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
					if workerCtx != nil || executable != f.r.Executable || args[0] != "__install-worker" ||
						!slices.Contains(args, "--start-runtime") || !cfg.StartRuntime || cfg.OperationID != "first-install" ||
						cfg.ComponentRoot != f.root || cfg.ConfigPath != f.config || cfg.PlanDigest != f.plan.Digest() {
						t.Fatalf("incorrect worker launch: %v, %+v", args, cfg)
					}
					// Model the detached process independently of the CLI's polling context.
					workerCtx = context.WithoutCancel(ctx)
					f.worker = bootstrap.InstallWorkerState{
						State: "running", Stage: "starting_runtime", OperationID: cfg.OperationID, PID: 123,
						ConfigPath: cfg.ConfigPath, PlanDigest: cfg.PlanDigest, StartRuntime: true,
					}
					f.workerActive = true
					return f.worker, nil
				}
				finish := func() {
					t.Helper()
					if workerCtx == nil || workerCtx.Err() != nil {
						t.Fatal("worker could not finish runtime startup")
					}
					f.worker.State, f.worker.Stage, f.workerActive = "completed", "ready", false
					f.owner.State, f.owner.OwnerLocked = "ready", true
				}
				f.r.SleepFn = func(context.Context, time.Duration) error {
					if tc.exit == ExitOK {
						finish()
					}
					return nil
				}
				inspect := f.r.InspectWorkerFn
				polls := 0
				f.r.InspectWorkerFn = func(ctx context.Context, root string) (bootstrap.InstallWorkerState, bool, error) {
					if f.workerActive && tc.exit == ExitInProgressOrBusy {
						if _, waiting := ctx.Deadline(); waiting {
							polls++
							if polls > 1 {
								<-ctx.Done()
								return bootstrap.InstallWorkerState{}, false, ctx.Err()
							}
						}
					}
					return inspect(ctx, root)
				}
				e := f.command(tc.exit, "install", "--apply", "--plan-digest", f.plan.Digest(), "--wait", tc.wait)
				if tc.exit == ExitInProgressOrBusy {
					if polls != 2 || !e.Components.WorkerActive || e.Components.Progress == nil || e.Components.Progress.Stage != "starting_runtime" ||
						e.RetryAfterMS != 10000 || !hasBlocker(e, "install_worker_active") {
						t.Fatalf("runtime startup progress lost at deadline: %+v", e)
					}
					status := f.command(ExitInProgressOrBusy, "doctor", "--operation-id", e.OperationID)
					if len(status.Blockers) != 1 || !hasBlocker(status, "install_worker_active") ||
						len(status.NextActions) != 1 || !hasAction(status, "doctor", "--operation-id", e.OperationID, "--json") ||
						status.RetryAfterMS != 10000 || status.Daemon.State != "stopped" || !status.Components.WorkerActive {
						t.Fatalf("active runtime worker reported conflicting advice: %+v", status)
					}
					finish()
				} else if e.Daemon.State != "ready" || e.Components.WorkerActive {
					t.Fatalf("runtime did not become ready: %+v", e)
				}
				e = f.command(ExitOK, "doctor", "--operation-id", e.OperationID)
				if e.OperationID != "first-install" || e.Daemon.State != "ready" || e.Components.WorkerActive ||
					e.Components.Progress == nil || e.Components.Progress.Stage != "ready" {
					t.Fatalf("doctor did not report the completed worker: %+v", e)
				}
			})
		})
	}
}

func TestInstallCompletePlanWithExistingDaemon(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, state, phase string
		stale              bool
		exit               int
		blocker            string
		actions            [][]string
	}{
		{name: "ready", state: "ready", exit: ExitOK},
		{name: "stale version", state: "ready", stale: true, exit: ExitActionRequired, blocker: "stale_daemon_runtime",
			actions: [][]string{{"repair", "stale-daemon", "--dry-run"}, {"repair", "stale-daemon", "--apply", "--owner-fingerprint", testFingerprint}}},
		{name: "stale owner", state: "stale_owner", exit: ExitActionRequired, blocker: "stale_owner_receipt",
			actions: [][]string{{"repair", "stale-owner", "--dry-run"}, {"repair", "stale-owner", "--apply"}}},
		{name: "mismatch", state: "mismatch", exit: ExitNeedsReview, blocker: "owner_mismatch"},
		{name: "starting state", state: "starting", exit: ExitInProgressOrBusy, blocker: "daemon_starting",
			actions: [][]string{{"doctor", "--operation-id", "first-install"}}},
		{name: "starting phase", state: "ready", phase: "starting", exit: ExitInProgressOrBusy, blocker: "daemon_starting",
			actions: [][]string{{"doctor", "--operation-id", "first-install"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.owner.State, f.owner.OwnerPhase, f.owner.StaleVersionDetected = tc.state, tc.phase, tc.stale
			f.r.LaunchWorkerFn = func(context.Context, string, []string, bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
				t.Fatal("unexpected worker for existing daemon")
				return bootstrap.InstallWorkerState{}, nil
			}
			e := f.command(tc.exit, "install", "--apply", "--plan-digest", f.plan.Digest())
			var actions [][]string
			for _, action := range e.NextActions {
				actions = append(actions, action.Args)
			}
			if (tc.blocker == "" && len(e.Blockers) != 0) ||
				(tc.blocker != "" && (len(e.Blockers) != 1 || !hasBlocker(e, tc.blocker))) ||
				!slices.EqualFunc(actions, tc.actions, func(got, want []string) bool {
					return slices.Equal(got, append(slices.Clone(want), "--json"))
				}) ||
				tc.exit == ExitInProgressOrBusy && e.RetryAfterMS != 10000 {
				t.Fatalf("unexpected daemon advice: %+v", e)
			}
		})
	}
}

func TestHiddenWorkerAssetsAndExitMapping(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	args := []string{"__install-worker", "--component-root", f.root, "--config", f.config,
		"--mirror", "official", "--operation-id", "first-install", "--plan-digest", f.plan.Digest(), "--start-runtime"}
	f.r.NewEnvFn = func(_ context.Context, assets fs.FS, options bootstrap.EnvironmentOptions) (*bootstrap.Environment, error) {
		if _, err := fs.ReadFile(assets, "first-install-components.json"); err != nil || options.Root != f.root || options.DownloadSource != "official" {
			t.Fatalf("bootstrap inputs invalid: %+v %v", options, err)
		}
		return &bootstrap.Environment{Options: options}, nil
	}
	for _, tc := range []struct {
		err  error
		code int
	}{
		{nil, ExitOK}, {bootstrap.ErrOrphanInstallProcess, ExitNeedsReview}, {bootstrap.ErrInstallWorkerBusy, ExitInProgressOrBusy},
		{supervisor.ErrUpgradeLocked, ExitInProgressOrBusy}, {bootstrap.ErrPlanChanged, ExitPreflightBlocked}, {bootstrap.ErrOperationIDMismatch, ExitPreflightBlocked}, {errors.New("private failure"), ExitFatal},
	} {
		f.r.RunWorkerFn = func(_ context.Context, env *bootstrap.Environment, assets fs.FS, cfg bootstrap.InstallWorkerConfig) error {
			if _, err := fs.ReadFile(assets, "scripts/dev.py"); err != nil || !cfg.StartRuntime || cfg.ConfigPath != f.config ||
				cfg.RuntimeHash != f.owner.RunningRuntimeHash || env.Options.Root != f.root {
				t.Fatalf("worker inputs invalid: %+v %v", cfg, err)
			}
			return tc.err
		}
		f.command(tc.code, append(slices.Clone(args), "--json")...)
		var out bytes.Buffer
		handled, got := f.r.DispatchIO(context.Background(), args, f.assets, f.bootstrapAssets, "", &out, &out)
		if !handled || got != tc.code || out.Len() != 0 {
			t.Fatalf("hidden worker without JSON: handled=%v code=%d output=%q", handled, got, &out)
		}
	}
}

func TestInstallRejectsOverlapWithProspectiveLibrary(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("component bootstrap requires macOS")
	}
	f := newFixture(t)
	dir := filepath.Join(f.base, "fresh")
	config := filepath.Join(dir, "runtime.json")
	store := filepath.Join(dir, "library")
	f.r.NewEnvFn = func(context.Context, fs.FS, bootstrap.EnvironmentOptions) (*bootstrap.Environment, error) {
		t.Fatal("constructed environment before rejecting the unsafe prospective library")
		return nil, nil
	}
	checkRejected := func(root string) {
		t.Helper()
		for _, args := range [][]string{
			{"install", "--plan"},
			{"install", "--apply", "--plan-digest", f.plan.Digest()},
			{"__install-worker", "--mirror", "official", "--operation-id", "first-install", "--plan-digest", f.plan.Digest(), "--start-runtime", "--json"},
		} {
			e := f.command(ExitFatal, append(args, "--config", config, "--component-root", root)...)
			if !hasBlocker(e, "component_environment_unavailable") {
				t.Fatalf("%v did not reject component root %q: %+v", args, root, e)
			}
		}
	}
	for _, root := range []string{store, filepath.Join(store, "sub"), dir} {
		checkRejected(root)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created files for the prospective installation: %v", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.store, store); err != nil {
		t.Fatal(err)
	}
	checkRejected(filepath.Join(dir, "components"))
}

func TestCleanInstallLeavesProtectedStoreEmptyForPrepareFirstRuntime(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("component bootstrap requires macOS")
	}
	f := newFixture(t)
	if err := os.Remove(f.config); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.store); err != nil {
		t.Fatal(err)
	}
	f.bootstrapAssets = engine.BootstrapAssets
	f.r.NewEnvFn = bootstrap.NewEnvironment
	args := []string{"--config", f.config, "--component-root", f.root}
	assertFreshEnvironment := func(entry string, env *bootstrap.Environment) {
		t.Helper()
		if env == nil {
			t.Fatalf("%s did not receive an environment", entry)
		}
		if env.Options.ProtectedStore != "" {
			t.Errorf("%s set ProtectedStore to %q for a clean install", entry, env.Options.ProtectedStore)
		}
	}

	var planEnv, workerEnv *bootstrap.Environment
	var prepareErr error
	f.r.DetectPlanFn = func(env *bootstrap.Environment, _ context.Context) (bootstrap.Plan, error) {
		planEnv = env
		return f.plan, nil
	}
	f.r.RunWorkerFn = func(ctx context.Context, env *bootstrap.Environment, assets fs.FS, cfg bootstrap.InstallWorkerConfig) error {
		workerEnv = env
		if !cfg.StartRuntime {
			t.Fatal("worker did not request runtime preparation")
		}
		_, prepareErr = env.PrepareFirstRuntime(ctx, cfg.ConfigPath, assets)
		return prepareErr
	}
	digest := f.plan.Digest()
	f.command(ExitActionRequired, append([]string{"install", "--plan"}, args...)...)
	assertFreshEnvironment("install --plan", planEnv)
	f.command(ExitFatal, append([]string{"__install-worker", "--mirror", "official", "--operation-id", "first-install", "--plan-digest", digest, "--start-runtime", "--json"}, args...)...)
	assertFreshEnvironment("__install-worker", workerEnv)
	if prepareErr == nil || prepareErr.Error() != "环境组件尚未全部准备完成" {
		t.Fatalf("PrepareFirstRuntime on a clean install = %v, want incomplete component detection", prepareErr)
	}
}

func TestVerifyConnectionRequiresPostActivationEvidence(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.row.Status = "configured_pending_new_session"
	f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
	args := []string{"verify", "--stage", "connection", "--agent", testAgent}
	f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
		if req.URL.Path == "/v1/connections" && req.URL.Query().Get("force") != "1" {
			t.Error("verification did not request fresh evidence")
		}
		return nil, nil
	}
	checkHuman := func() {
		t.Helper()
		e := f.command(ExitHumanRequired, args...)
		copy := strings.Join(e.HumanSteps, " ")
		if !strings.Contains(copy, "全局开关") || !strings.Contains(copy, "记忆") || !strings.Contains(copy, "⌘Q") ||
			strings.Contains(copy, "xattr") || strings.Contains(copy, "知识") {
			t.Fatalf("incomplete human steps: %q", copy)
		}
	}
	checkHuman()
	f.doc.First.Setup = false
	f.writeConfig()
	f.command(ExitOK, args...)
	f.row.AwaitingPostUpgradeReceipt = true
	checkHuman()
	f.row.AwaitingPostUpgradeReceipt = false
	for _, state := range []string{"awaiting_generation_activation", "waiting_session", "waiting_for_first_session", "awaiting_first_session", "permission_pending", "check_failed"} {
		f.row.Capture = &controlplane.AgentCaptureState{State: state}
		checkHuman()
	}
	f.row.Capture = nil
	f.row.NewSessions, f.row.EnvironmentVerified = 1, true
	checkHuman()
	f.row.Status = "environment_receipt_unconfirmed"
	checkHuman()
	f.row.Status = "preparation_unknown"
	f.command(ExitNeedsReview, args...)
	f.row.Status, f.row.NeedsHookRepair = "configured_pending_new_session", true
	f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
	f.command(ExitActionRequired, args...)
	f.row.Status, f.row.NeedsHookRepair = "not_connected", false
	f.row.Capture = nil
	f.command(ExitActionRequired, args...)

}

func TestDoctorNetworkGenerationAndOperationReceipt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.r.CheckDNSFn = func(_ context.Context, mirror string) bool {
		if mirror != "official" {
			t.Error("explicit mirror did not override default")
		}
		return false
	}
	e := f.command(ExitPreflightBlocked, "doctor", "--check-network", "--mirror", "official", "--operation-id", testOperation)
	if e.Environment.DNSOK == nil || *e.Environment.DNSOK || len(e.HumanSteps) == 0 ||
		e.Artifacts.ConnectionOperation == nil || !e.Artifacts.ConnectionOperation.Recorded {
		t.Fatalf("doctor missed requested checks: %+v", e)
	}
	f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
		if req.URL.Path == "/v1/generation" {
			return response(200, controlplane.GenerationDirectory{
				Active:       &controlplane.GenerationRef{ID: "sensitive-provider-profile", Revision: 2},
				AgentBridges: map[string]controlplane.GenerationProfile{"pi": {Endpoint: "https://example.com/?private=value"}},
			})
		}
		return nil, nil
	}
	f.doc.First.Setup = false
	f.writeConfig()
	f.row.Status = "configured_pending_new_session"
	f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
	e = f.command(ExitOK, "doctor", "--agent", testAgent)
	if e.Generation.Active == nil || e.Generation.Active.Revision != 2 || !slices.Equal(e.Generation.AvailableBridges, []string{"pi"}) ||
		len(e.Generation.RecommendedProcessingCLIs) != 0 || e.Generation.MissingProcessingCLIHint != "" {
		t.Fatalf("unexpected processing configuration: %+v", e.Generation)
	}
	f.doc.ServiceURL = "http://127.0.0.1:12346/?token=secret"
	f.doc.CompletionURL = "https://example.com"
	f.writeConfig()
	e = f.command(ExitOK, "doctor")
	if len(e.Daemon.Endpoints) != 1 || e.Daemon.Endpoints["project_control_url"] == "" {
		t.Fatalf("unsafe daemon endpoints: %v", e.Daemon.Endpoints)
	}
}

func TestOnlineDoctorProcessingCLIGuidance(t *testing.T) {
	t.Parallel()
	const untrusted = "private-untrusted-host-value"
	for _, tc := range []struct {
		name                                    string
		server, local, configured, wantDetected []string
		active, unavailable                     bool
	}{
		{name: "server only", server: []string{"traex"}, wantDetected: []string{"traex"}},
		{name: "local only", local: []string{"pi"}, wantDetected: []string{"pi"}},
		{name: "merge and deduplicate", server: []string{"traex", "traex", "codex", untrusted, "cursor"},
			local: []string{"traex", "pi", "pi", "cursor"}, wantDetected: []string{"codex", "pi", "traex"}},
		{name: "absent"},
		{name: "untrusted only", server: []string{untrusted, "cursor"}},
		{name: "configured", server: []string{"pi"}, local: []string{"pi"}, configured: []string{"pi"}},
		{name: "active", server: []string{"traex"}, wantDetected: []string{"traex"}, active: true},
		{name: "generation unavailable", local: []string{"pi"}, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.doc.First.Setup = false
			f.writeConfig()
			f.row.Status = "configured_pending_new_session"
			f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
			f.r.DetectHostsFn = func(bootstrap.HostDetectOptions) []bootstrap.DetectedHost {
				hosts := []bootstrap.DetectedHost{
					{Kind: "codex", Detected: true, SupportsProcessingCLI: true},
					{Kind: "antigravity", SupportsProcessingCLI: true, Executable: "/bin/fake"},
					{Kind: "trae_ide", Detected: true, Executable: "/bin/fake"},
				}
				for _, kind := range tc.local {
					hosts = append(hosts, bootstrap.DetectedHost{
						Kind: kind, Detected: true, SupportsProcessingCLI: true, Executable: "/bin/fake",
					})
				}
				return hosts
			}
			f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
				if req.URL.Path != "/v1/generation" {
					return nil, nil
				}
				if tc.unavailable {
					return response(503, nil)
				}
				dir := controlplane.GenerationDirectory{
					DetectedProcessingCLIs: tc.server, AgentBridges: map[string]controlplane.GenerationProfile{},
				}
				for _, kind := range tc.configured {
					dir.AgentBridges[kind] = controlplane.GenerationProfile{BridgeKind: kind}
				}
				if tc.active {
					dir.Active = &controlplane.GenerationRef{Revision: 2}
				}
				return response(200, dir)
			}
			exit := ExitOK
			if tc.unavailable {
				exit = ExitPreflightBlocked
			}
			out := f.rawCommand(exit, []string{"doctor"})
			got := out.Generation
			if !slices.Equal(got.AvailableBridges, tc.configured) || !slices.Equal(got.DetectedUnconfiguredCLIs, tc.wantDetected) ||
				strings.Contains(out.raw, untrusted) || (got.Active != nil) != tc.active {
				t.Fatalf("unexpected generation detection: %+v", got)
			}
			hint := got.MissingProcessingCLIHint
			want := bootstrap.RecommendedProcessingCLIs()
			switch {
			case tc.unavailable:
				if hint != "" || len(got.RecommendedProcessingCLIs) != 0 || len(got.DetectedUnconfiguredCLIs) != 0 ||
					strings.Contains(out.raw, "detected_unconfigured_clis") {
					t.Fatalf("generation failure supplied processing CLI guidance: %+v", got)
				}
				want = nil
			case tc.active || len(tc.configured) > 0:
				want = nil
				if hint != "" {
					t.Fatalf("unexpected guidance for configured generation: %q", hint)
				}
			case len(tc.wantDetected) == 0:
				if hint != processingHint {
					t.Fatalf("unexpected installation guidance: %q", hint)
				}
				if !slices.ContainsFunc(got.RecommendedProcessingCLIs, func(rec bootstrap.RecommendedProcessingCLI) bool {
					return rec.InstallCommand != ""
				}) {
					t.Fatalf("missing installation guidance: %+v", got.RecommendedProcessingCLIs)
				}
			default:
				want = slices.DeleteFunc(want, func(rec bootstrap.RecommendedProcessingCLI) bool {
					return !slices.Contains(tc.wantDetected, rec.Kind)
				})
				if !strings.Contains(hint, "本机已检测到") || !strings.Contains(hint, "记忆") ||
					!strings.Contains(hint, "尚未完成登录或未配置默认模型") ||
					!strings.Contains(hint, strings.Join(tc.wantDetected, "、")) ||
					strings.Contains(hint, "本机尚未检测到") || strings.Contains(hint, "知识") {
					t.Fatalf("unexpected login guidance: %q", hint)
				}
				for _, rec := range got.RecommendedProcessingCLIs {
					if rec.LoginHint == "" || rec.InstallCommand != "" {
						t.Fatalf("expected login-only guidance for %s: %+v", rec.Kind, rec)
					}
				}
				for i := range want {
					want[i].InstallCommand = ""
				}
			}
			if !slices.Equal(got.RecommendedProcessingCLIs, want) {
				t.Fatalf("recommendations = %+v, want %+v", got.RecommendedProcessingCLIs, want)
			}
		})
	}
}

func TestOfflineDoctorAvailableBridges(t *testing.T) {
	t.Parallel()
	gui := bootstrap.DetectedHost{Kind: "trae_ide", Detected: true, Executable: "/Applications/Trae CN.app/Contents/MacOS/Electron"}
	codex := bootstrap.DetectedHost{Kind: "codex", Detected: true, SupportsProcessingCLI: true, Executable: "/usr/local/bin/codex"}
	for _, tc := range []struct {
		name    string
		hosts   []bootstrap.DetectedHost
		bridges []string
	}{
		{"processing CLI", []bootstrap.DetectedHost{gui, codex, codex}, []string{"codex"}},
		{"GUI only", []bootstrap.DetectedHost{gui}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.owner = bootstrap.OwnerRuntimeStatus{State: "stopped"}
			f.plan.Items[0].Observation.Status = bootstrap.Missing
			f.r.DetectHostsFn = func(bootstrap.HostDetectOptions) []bootstrap.DetectedHost { return tc.hosts }
			generation := f.command(ExitActionRequired, "doctor", "--json").Generation
			if !slices.Equal(generation.AvailableBridges, tc.bridges) || len(generation.DetectedUnconfiguredCLIs) != 0 {
				t.Fatalf("available bridges: %v, want %v", generation.AvailableBridges, tc.bridges)
			}
			missing := len(tc.bridges) == 0
			if (len(generation.RecommendedProcessingCLIs) > 0) != missing ||
				(generation.MissingProcessingCLIHint != "") != missing {
				t.Fatalf("unexpected processing CLI guidance: %+v", generation)
			}
		})
	}
}

func TestOwnerRepairAndStop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.owner.State, f.owner.StaleVersionDetected = "stale_runtime", true
	configBefore := readTestFile(t, f.config)
	preparations := 0
	f.r.PrepareRuntimeForUpgradeFn = func(env *bootstrap.Environment, _ context.Context, config string, assets fs.FS) (bootstrap.FirstRuntime, error) {
		preparations++
		if env.Options.ProtectedStore != f.store || config != f.config || assets == nil {
			t.Fatal("upgrade preparation target mismatch")
		}
		return bootstrap.FirstRuntime{ConfigPath: f.config}, nil
	}
	var restartErr error
	calls := 0
	f.r.RestartStaleOwnerFn = func(_ context.Context, rt bootstrap.FirstRuntime, fingerprint string, _ fs.FS) error {
		calls++
		if rt.ConfigPath != f.config || fingerprint != testFingerprint || preparations != calls {
			t.Fatal("restart did not bind reviewed owner")
		}
		if !bytes.Equal(configBefore, readTestFile(t, f.config)) {
			t.Fatal("upgrade preparation changed runtime.json before restart")
		}
		return restartErr
	}
	e := f.command(ExitActionRequired, "repair", "--json", "stale-daemon")
	if e.Artifacts.OwnerFingerprint != testFingerprint {
		t.Fatal("stale daemon preview did not expose the owner fingerprint")
	}
	args := []string{"repair", "stale-daemon", "--apply", "--owner-fingerprint"}
	f.command(ExitPreflightBlocked, append(slices.Clone(args), "changed")...)
	if calls != 0 {
		t.Fatal("stale fingerprint attempted a restart")
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{supervisor.ErrUpgradeLocked, ExitInProgressOrBusy},
		{supervisor.ErrOwnerFingerprintMismatch, ExitPreflightBlocked}, {nil, ExitOK}} {
		restartErr = tc.err
		f.command(tc.code, append(slices.Clone(args), testFingerprint)...)
	}
	f.owner.DeveloperCheckout = true
	f.command(ExitFatal, "repair", "stale-daemon")
	f.command(ExitFatal, "stop", "--apply", "--owner-fingerprint", testFingerprint)
	f.owner.DeveloperCheckout = false

	f.owner = bootstrap.OwnerRuntimeStatus{State: "stale_owner", HasReceipt: true}
	f.command(ExitActionRequired, "repair", "stale-owner")
	var recoverErr error
	f.r.RecoverStaleOwnerFn = func(_ context.Context, config string) (supervisor.RecoverResult, error) {
		if config != f.config {
			t.Fatal("recovery config mismatch")
		}
		return supervisor.RecoverResult{}, recoverErr
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{supervisor.ErrOwnerLocked, ExitInProgressOrBusy},
		{supervisor.ErrRecoveryRefused, ExitNeedsReview}, {supervisor.ErrOwnerMismatch, ExitFatal}, {nil, ExitOK}} {
		recoverErr = tc.err
		f.command(tc.code, "repair", "stale-owner", "--apply")
	}

	f.owner = bootstrap.OwnerRuntimeStatus{State: "ready", OwnerLocked: true, OwnerPID: 99, OwnerStarted: "bound-start", OwnerFingerprint: testFingerprint}
	f.r.StopOwnerBoundFn = func(_ context.Context, opts supervisor.StopOwnerOptions, pid int, started string) error {
		if opts.ConfigPath != f.config || opts.StoreRoot != f.store || opts.ExpectedFingerprint != testFingerprint || pid != 99 || started != "bound-start" {
			t.Fatalf("stop did not bind reviewed owner: %+v pid=%d started=%q", opts, pid, started)
		}
		f.owner = bootstrap.OwnerRuntimeStatus{State: "stopped"}
		return nil
	}
	stop := []string{"stop", "--apply", "--owner-fingerprint"}
	f.command(ExitPreflightBlocked, append(slices.Clone(stop), "changed")...)
	f.command(ExitOK, append(slices.Clone(stop), testFingerprint)...)
	f.command(ExitOK, append(slices.Clone(stop), testFingerprint)...)
}

func TestConnectionActionsJournalAndConflicts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, phase, errorCode, errorPhase, receipt string
		code                                        int
		retain                                      bool
	}{
		{"prepare", "prepare", "", "", "", ExitOK, false},
		{"activate", "activate", "", "", "", ExitHumanRequired, false},
		{"allow", "allow-capture", "", "", "", ExitHumanRequired, false},
		{"defer", "defer-capture", "", "", "", ExitOK, false},
		{"pre-rejection", "activate", "revision_conflict", "pre_mutation", "rejected", ExitPreflightBlocked, false},
		{"pre-rejection-without-action", "activate", "revision_conflict", "pre_mutation", "rejected_without_action", ExitNeedsReview, true},
		{"pre-unknown", "activate", "revision_conflict", "pre_mutation", "unknown", ExitNeedsReview, true},
		{"pre-unrecorded", "activate", "revision_conflict", "pre_mutation", "unrecorded", ExitNeedsReview, true},
		{"pre-query-error", "activate", "revision_conflict", "pre_mutation", "error", ExitNeedsReview, true},
		{"ambiguous", "activate", "connection_result_requires_review", "post_mutation", "", ExitNeedsReview, true},
		{"transport", "activate", "transport", "", "", ExitNeedsReview, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			phase := strings.ReplaceAll(tc.phase, "-", "_")
			if oneOf(tc.phase, "allow-capture", "defer-capture") {
				f.row.Status = "configured_pending_new_session"
				f.row.Capture = &controlplane.AgentCaptureState{ReviewSHA: testSHA}
			}
			var opID string
			var queried bool
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				if req.Method != http.MethodPost {
					if opID == "" || req.URL.Path != "/v1/connections/operations/"+opID {
						return nil, nil
					}
					queried = true
					f.assertRetained(opID, true)
					switch tc.receipt {
					case "rejected", "rejected_without_action":
						receipt := controlplane.ConnectionOperationReceipt{
							Recorded: true, Status: "complete", Outcome: "rejected",
							OperationID: opID, InstanceID: f.row.ID, Action: phase,
						}
						if tc.receipt == "rejected_without_action" {
							receipt.Action = ""
						}
						return response(200, receipt)
					case "unknown":
						return response(200, map[string]any{"recorded": true, "status": "unknown", "outcome": ""})
					case "unrecorded":
						return response(200, map[string]any{"recorded": false})
					default:
						return response(503, nil)
					}
				}
				opID = body.OperationID
				assertAtomicOperation(t, f, body, "connection_"+phase)
				if !strings.HasSuffix(req.URL.Path, "/"+phase) || phase != "prepare" && body.ProposedSHA != testSHA {
					t.Fatalf("wrong mutation %s %+v", req.URL.Path, body)
				}
				switch tc.errorCode {
				case "transport":
					return nil, errors.New("disconnected")
				case "":
					row := f.row
					row.Status = "configured_pending_new_session"
					return response(200, controlplane.ConnectionActionResult{Revision: f.revision + 1, Connection: row})
				default:
					return response(409, map[string]string{"code": tc.errorCode, "phase": tc.errorPhase})
				}
			}
			args := []string{"connect", "--agent", testAgent, "--phase", tc.phase, "--apply"}
			if tc.phase != "prepare" {
				args = append(args, "--reviewed-sha", testSHA)
			}
			e := f.command(tc.code, args...)
			if f.writes != 1 || !strings.HasPrefix(e.OperationID, "ui-cli-") {
				t.Fatalf("missing operation execution: %+v", e)
			}
			f.assertRetained(e.OperationID, tc.retain)
			if queried != (tc.errorPhase == "pre_mutation") {
				t.Fatalf("looked up terminal rejection=%v for %q", queried, tc.errorPhase)
			}
			if tc.retain && (!hasBlocker(e, "connection_operation_unconfirmed") ||
				!hasAction(e, "repair", "pending-ops", "--operation-id", e.OperationID, "--dry-run", "--json")) {
				t.Fatalf("missing pending repair instructions: %+v", e)
			}
		})
	}
}

func assertAtomicOperation(t *testing.T, f *fixture, body requestBody, kind string) {
	t.Helper()
	journal, err := controlplane.LoadUIOperations(f.ops)
	if err != nil || len(journal) != 1 || journal[0].Operation != body.OperationID || journal[0].Kind != kind ||
		journal[0].Profile != f.row.ID || journal[0].Revision != body.Revision || body.Revision != f.revision {
		t.Fatalf("missing atomic operation before request: %v %v %+v", journal, err, body)
	}
}

func (f *fixture) assertRetained(id string, retained bool) {
	f.t.Helper()
	ops, err := controlplane.LoadUIOperations(f.ops)
	if err != nil || (len(ops) != 0) != retained || retained && (len(ops) != 1 || ops[0].Operation != id) {
		f.t.Fatalf("journal retention = %v; wanted %v for %q (err=%v)", ops, retained, id, err)
	}
}

func TestConnectionAutoConnectAndEnvironmentSelection(t *testing.T) {
	t.Parallel()
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "changed"}[changed], func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.row.Status = "not_connected"
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				switch {
				case strings.HasSuffix(req.URL.Path, "/preview") && f.row.Status == "not_connected":
					return response(200, controlplane.ConnectionPreview{Revision: f.revision})
				case strings.HasSuffix(req.URL.Path, "/preview") && changed:
					return response(200, controlplane.ConnectionPreview{Revision: f.revision, ProposedSHA256: strings.Repeat("b", 64)})
				case strings.HasSuffix(req.URL.Path, "/prepare"):
					assertAtomicOperation(t, f, body, "connection_prepare")
					f.row.Status = "prepared"
					f.revision++
					return response(200, controlplane.ConnectionActionResult{Revision: f.revision, Connection: f.row})
				case strings.HasSuffix(req.URL.Path, "/activate"):
					assertAtomicOperation(t, f, body, "connection_activate")
					f.row.Status = "configured_pending_new_session"
					return response(200, controlplane.ConnectionActionResult{Revision: f.revision + 1, Connection: f.row})
				}
				return nil, nil
			}
			dry := f.command(ExitActionRequired, "connect", "--agent", testAgent)
			if len(dry.NextActions) != 1 || !slices.Contains(dry.NextActions[0].Args, "prepare") {
				t.Fatal("unprepared connection did not offer the prepare phase")
			}
			code, writes := ExitHumanRequired, 2
			if changed {
				code, writes = ExitPreflightBlocked, 1
			}
			f.command(code, "connect", "--agent", testAgent, "--apply", "--reviewed-sha", testSHA)
			f.assertRetained("", false)
			if f.writes != writes {
				t.Fatalf("auto-connect writes=%d want %d", f.writes, writes)
			}
		})
	}
	f := newFixture(t)
	f.row.Kind, f.row.ID, f.row.Status = "codex", "codex-0123456789abcdef01234567", "environment_ambiguous"
	profile := filepath.Join(f.base, "profiles", "test")
	f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/preview") {
			t.Error("environment selection attempted a connection preview")
			return response(409, map[string]string{"code": "connection_environment_required", "phase": "pre_mutation"})
		}
		if req.Method == http.MethodPost {
			if req.URL.Path != "/v1/connections/environment" || body.Profile != profile || body.Kind != "codex" || body.Revision != f.revision {
				t.Fatalf("wrong profile selection: %s %+v", req.URL.Path, body)
			}
			return response(200, controlplane.ConnectionsDirectory{Revision: f.revision + 1})
		}
		return nil, nil
	}
	args := []string{"connect", "--agent", "codex", "--phase", "select-env"}
	dry := f.command(ExitActionRequired, append(slices.Clone(args), "--profile", profile, "--dry-run")...)
	if !hasBlocker(dry, "connection_confirmation_required") ||
		!hasAction(dry, "connect", "--agent", "codex", "--apply", "--phase", "select-env", "--profile", profile, "--json") {
		t.Fatalf("missing environment selection advice: %+v", dry)
	}
	e := f.command(ExitActionRequired, append(slices.Clone(args), "--dry-run")...)
	if !strings.Contains(strings.Join(e.HumanSteps, " "), "--profile") || len(e.NextActions) != 0 {
		t.Fatalf("missing profile guidance: %+v", e)
	}
	f.command(ExitOK, append(slices.Clone(args), "--profile", profile, "--apply")...)
}

func TestRepairPendingOperationsAndSetupIsolation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code      int
		errorCode string
	}{
		{ExitOK, ""}, {ExitNeedsReview, "connection_operation_ambiguous_requires_review"}, {ExitPreflightBlocked, "revision_conflict"},
	} {
		t.Run(statuses[tc.code], func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.pending()
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				if !strings.HasSuffix(req.URL.Path, "/reconcile") {
					return nil, nil
				}
				if body.DryRun {
					safe := tc.code != ExitNeedsReview
					phase := "post_mutation"
					if safe {
						phase = "pre_mutation"
					}
					return response(200, map[string]any{"recorded": true, "status": "unknown", "phase": phase,
						"reconcilable": safe, "reconciled": false, "safe_to_unlock": false})
				}
				if body.Revision != f.revision {
					t.Fatalf("mismatched reconciliation revision: %+v", body)
				}
				f.assertRetained(testOperation, true)
				if tc.errorCode != "" {
					return response(409, map[string]string{"code": tc.errorCode})
				}
				return response(200, controlplane.ConnectionOperationReceipt{
					Recorded: true, Status: "complete", Outcome: "rejected", Phase: "pre_mutation", Reconciled: true, SafeToUnlock: true,
					OperationID: testOperation, InstanceID: f.row.ID, Action: "activate",
				})
			}
			dryCode := ExitActionRequired
			if tc.code == ExitNeedsReview {
				dryCode = ExitNeedsReview
				f.command(ExitOK, "verify", "--stage", "setup", "--expect-zero-downloads", "--expect-runtime-current")
			}
			dry := f.command(dryCode, "repair", "pending-ops", "--dry-run", "--operation-id", testOperation)
			receipt := dry.Artifacts.ConnectionOperation
			if receipt == nil || !receipt.Recorded || receipt.Status != "unknown" || receipt.SafeToUnlock ||
				receipt.Reconcilable != (dryCode == ExitActionRequired) || (receipt.Phase == "pre_mutation") != receipt.Reconcilable {
				t.Fatalf("incorrect reconciliation evidence: %+v", receipt)
			}
			f.command(dryCode, "doctor")
			f.command(dryCode, "connect", "--agent", testAgent, "--apply", "--reviewed-sha", testSHA)
			if f.writes != 0 {
				t.Fatal("pending operation did not block a new connection mutation")
			}
			applied := f.command(tc.code, "repair", "pending-ops", "--apply", "--operation-id", testOperation)
			if tc.code == ExitOK && len(applied.Connections.Pending) != 0 {
				t.Fatalf("cleaned operation still shown as pending: %+v", applied.Connections.Pending)
			}
			f.assertRetained(testOperation, tc.code != ExitOK)
		})
	}
}

func TestRepairPendingApplyRequiresTerminalEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*controlplane.ConnectionOperationReceipt)
	}{
		{"unrecorded", func(r *controlplane.ConnectionOperationReceipt) { r.Recorded = false }},
		{"unknown", func(r *controlplane.ConnectionOperationReceipt) { r.Status = "unknown" }},
		{"wrong operation", func(r *controlplane.ConnectionOperationReceipt) { r.OperationID = "ui-other" }},
		{"wrong instance", func(r *controlplane.ConnectionOperationReceipt) { r.InstanceID = "other-instance" }},
		{"wrong action", func(r *controlplane.ConnectionOperationReceipt) { r.Action = "prepare" }},
		{"empty operation", func(r *controlplane.ConnectionOperationReceipt) { r.OperationID = "" }},
		{"empty instance", func(r *controlplane.ConnectionOperationReceipt) { r.InstanceID = "" }},
		{"empty action", func(r *controlplane.ConnectionOperationReceipt) { r.Action = "" }},
		{"no unlock evidence", func(r *controlplane.ConnectionOperationReceipt) { r.SafeToUnlock, r.Reconciled = false, false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.pending()
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "/reconcile") && !body.DryRun {
					receipt := controlplane.ConnectionOperationReceipt{
						Recorded: true, Status: "complete", OperationID: testOperation, InstanceID: f.row.ID,
						Action: "activate", SafeToUnlock: true, Reconciled: true,
					}
					tc.mutate(&receipt)
					return response(200, receipt)
				}
				return nil, nil
			}
			f.command(ExitActionRequired, "repair", "pending-ops", "--dry-run", "--operation-id", testOperation)
			e := f.command(ExitNeedsReview, "repair", "pending-ops", "--apply", "--operation-id", testOperation)
			if !hasBlocker(e, "pending_connection_operation_ambiguous") || len(e.Connections.Pending) != 1 {
				t.Fatalf("pending operation was not preserved in response: %+v", e)
			}
			f.assertRetained(testOperation, true)
		})
	}
}

func TestRepairTraeHooksReviewedAndJournaled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code  int
		phase string
	}{
		{ExitOK, ""}, {ExitPreflightBlocked, "pre_mutation"}, {ExitNeedsReview, "post_mutation"},
	} {
		t.Run(statuses[tc.code], func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				if tc.phase == "pre_mutation" && req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/operations/") {
					return response(200, controlplane.ConnectionOperationReceipt{
						Recorded: true, Status: "complete", Outcome: "rejected", Action: "repair-trae-hooks",
						OperationID: filepath.Base(req.URL.Path), InstanceID: f.row.ID,
					})
				}
				if !strings.HasSuffix(req.URL.Path, "/repair-trae-hooks") {
					return nil, nil
				}
				if body.DryRun {
					return response(200, controlplane.TraeHooksRepairResult{
						Revision: f.revision, ReviewedSHA: testSHA, NeedsHookRepair: true, Repairable: true, PreservedThirdPartyGroups: 2,
					})
				}
				assertAtomicOperation(t, f, body, "connection_repair_trae_hooks")
				if body.ReviewedSHA != testSHA {
					t.Fatal("repair sent a different reviewed digest")
				}
				if tc.phase != "" {
					return response(409, map[string]string{"code": "revision_conflict", "phase": tc.phase})
				}
				return response(200, controlplane.TraeHooksRepairResult{Repaired: true, Mode: "active_migration", PreservedThirdPartyGroups: 2, RemovedEvents: []string{"SessionStart", "untrusted"}})
			}
			dry := f.command(ExitActionRequired, "repair", "trae-hooks", "--dry-run")
			if dry.Artifacts.HookRepair == nil || dry.Artifacts.HookRepair.PreservedThirdPartyGroups != 2 {
				t.Fatalf("dry-run lost preserved group count: %+v", dry.Artifacts)
			}
			args := []string{"repair", "trae-hooks", "--apply", "--instance-id", testInstance, "--reviewed-sha"}
			f.command(ExitPreflightBlocked, append(slices.Clone(args), "wrong")...)
			if f.writes != 0 {
				t.Fatal("mismatched repair SHA wrote state")
			}
			e := f.command(tc.code, append(slices.Clone(args), testSHA)...)
			f.assertRetained(e.OperationID, tc.code == ExitNeedsReview)
			if tc.code == 0 && (len(e.HumanSteps) == 0 || e.Artifacts.HookRepair == nil || !e.Artifacts.HookRepair.Repaired ||
				e.Artifacts.HookRepair.PreservedThirdPartyGroups != 2 || !slices.Equal(e.Artifacts.HookRepair.RemovedEvents, []string{"SessionStart"})) {
				t.Fatalf("missing safe repair summary: %+v", e.Artifacts)
			}
		})
	}
}

func TestMalformedFirstInstallIdentityRemainsReadOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.store, "store-identity.json")); err != nil {
		t.Fatal(err)
	}
	f.command(ExitOK, "doctor")
	f.command(ExitFatal, "install", "--apply", "--plan-digest", f.plan.Digest())
}

func hasAction(e envelope, args ...string) bool {
	return slices.ContainsFunc(e.NextActions, func(action nextAction) bool { return slices.Equal(action.Args, args) })
}

func TestUnavailableConnectionTargets(t *testing.T) {
	t.Parallel()
	checkNoWrites := func(t *testing.T, f *fixture) {
		t.Helper()
		if f.writes != 0 {
			t.Fatalf("unavailable host caused %d writes", f.writes)
		}
		if _, err := os.Lstat(f.ops); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected no operation journal directory, got %v", err)
		}
	}
	newUnavailableFixture := func(t *testing.T, row controlplane.AgentConnection) *fixture {
		t.Helper()
		f := newFixture(t)
		f.row = row
		f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
			if req.Method != http.MethodGet || !oneOf(req.URL.Path, "/v1/connections", "/v1/generation") {
				t.Fatalf("unavailable host triggered %s %s", req.Method, req.URL.Path)
			}
			return nil, nil
		}
		return f
	}
	checkOutcome := func(t *testing.T, e envelope, wantText ...string) {
		t.Helper()
		if !hasBlocker(e, "connection_target_unavailable") || hasBlocker(e, "control_request_failed") ||
			e.Artifacts.ReviewedSHA != "" || len(e.NextActions) != 0 {
			t.Fatalf("unexpected unavailable-host result: %+v", e)
		}
		steps := strings.Join(e.HumanSteps, " ")
		for _, part := range wantText {
			if !strings.Contains(steps, part) {
				t.Errorf("human steps missing %q: %v", part, e.HumanSteps)
			}
		}
		if strings.Contains(steps, "xattr") || strings.Contains(steps, "知识") || strings.Contains(steps, testToken) {
			t.Error("human steps contain forbidden content")
		}
		for i, step := range e.HumanSteps {
			if slices.Contains(e.HumanSteps[:i], step) {
				t.Errorf("duplicate human step: %q", step)
			}
		}
	}

	codex := controlplane.AgentConnection{ID: "codex-unavailable", Kind: "codex", Status: "not_connected", Detected: false}
	for _, args := range [][]string{
		{"connect", "--agent", "codex", "--dry-run"},
		{"connect", "--agent", "codex", "--instance-id", "codex-unavailable", "--dry-run"},
		{"connect", "--agent", "codex", "--phase", "prepare", "--apply"},
		{"connect", "--agent", "codex", "--phase", "activate", "--apply", "--reviewed-sha", testSHA},
		{"connect", "--agent", "codex", "--phase", "review", "--dry-run"},
		{"connect", "--agent", "codex", "--phase", "review", "--apply"},
		{"connect", "--agent", "codex", "--phase", "allow-capture", "--apply", "--reviewed-sha", testSHA},
		{"connect", "--agent", "codex", "--phase", "defer-capture", "--apply", "--reviewed-sha", testSHA},
		{"doctor", "--agent", "codex"},
		{"verify", "--stage", "connection", "--agent", "codex"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			f := newUnavailableFixture(t, codex)
			e := f.command(ExitActionRequired, args...)
			const install = "npm i -g @openai/codex"
			checkOutcome(t, e, "Codex", install, "记忆", "安装完成后运行 connect --agent codex --dry-run --json 检查接入预览。")
			if !slices.ContainsFunc(e.Generation.RecommendedProcessingCLIs, func(rec bootstrap.RecommendedProcessingCLI) bool {
				return rec.Kind == "codex" && rec.InstallCommand == install
			}) {
				t.Fatal("Codex installation recommendation missing")
			}
			checkNoWrites(t, f)
		})
	}
	t.Run("explicit unavailable status", func(t *testing.T) {
		t.Parallel()
		f := newUnavailableFixture(t, controlplane.AgentConnection{
			ID: testInstance, Kind: "trae_ide", Status: "unavailable", Detected: false,
		})
		e := f.command(ExitActionRequired, "connect", "--agent", "trae_ide", "--dry-run")
		checkOutcome(t, e, "TRAE CN", "记忆")
		if len(e.Connections.Hosts) != 1 || e.Connections.Hosts[0].Status != "unavailable" {
			t.Fatalf("unavailable status missing from host summary: %+v", e.Connections.Hosts)
		}
		checkNoWrites(t, f)
	})
	t.Run("unknown explicit instance", func(t *testing.T) {
		t.Parallel()
		f := newUnavailableFixture(t, codex)
		e := f.command(ExitInvalidArguments, "connect", "--agent", "codex", "--instance-id", "codex-0123456789abcdef01234567", "--dry-run")
		if !hasBlocker(e, "unknown_connection_target") || hasBlocker(e, "connection_target_unavailable") {
			t.Fatalf("unexpected unknown-target result: %+v", e)
		}
		checkNoWrites(t, f)
	})
}

func TestVerifyConnectionRequestedAgentAndNonCaptureReceipts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.doc.First.Setup = false
	f.writeConfig()
	if err := bootstrap.SaveSelections(bootstrap.EnvironmentOptions{
		Root: f.root, SelectedAgents: []string{"codex", "trae_ide"},
	}, f.config); err != nil {
		t.Fatal(err)
	}
	f.row.Status = "configured_pending_new_session"
	f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
	e := f.command(ExitOK, "verify", "--stage", "connection", "--agent", "trae_ide")
	if !slices.Equal(e.Environment.SelectedAgents, []string{"trae_ide"}) || len(e.Connections.Hosts) != 1 {
		t.Fatalf("unexpected verification scope: %+v", e)
	}
	f.command(ExitActionRequired, "verify", "--stage", "connection")
	f.command(ExitActionRequired, "verify", "--stage", "connection", "--agent", "codex")

	f.row.Capture = nil
	for _, tc := range []struct {
		kind                string
		sessions            int
		environmentVerified bool
		exit                int
	}{
		{"opencode", 0, true, ExitHumanRequired},
		{"opencode", 1, false, ExitOK},
		{"cursor", 1, false, ExitHumanRequired},
		{"cursor", 1, true, ExitOK},
	} {
		f.row.Kind, f.row.ID = tc.kind, tc.kind+"-0123456789abcdef01234567"
		f.row.NewSessions, f.row.EnvironmentVerified = tc.sessions, tc.environmentVerified
		f.command(tc.exit, "verify", "--stage", "connection", "--agent", tc.kind)
	}
}

func TestDoctorOffersConnectionForTemplateAAndC(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, status        string
		setup, selected, explicit bool
	}{
		{"template-a-not-connected", "trae_ide", "not_connected", true, false, false},
		{"template-a-prepared", "trae_ide", "prepared", true, false, false},
		{"template-c", "codex", "not_connected", false, false, true},
		{"selected-agent", "cursor", "ready_to_activate", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.doc.First.Setup = tc.setup
			f.writeConfig()
			if tc.selected {
				if err := bootstrap.SaveSelections(bootstrap.EnvironmentOptions{
					Root: f.root, SelectedAgents: []string{tc.kind},
				}, f.config); err != nil {
					t.Fatal(err)
				}
			}
			f.row.Kind, f.row.Status = tc.kind, tc.status
			f.row.ID = tc.kind + "-0123456789abcdef01234567"
			args := []string{"doctor"}
			if tc.explicit {
				f.command(ExitOK, args...)
				args = append(args, "--agent", tc.kind)
			}
			e := f.command(ExitActionRequired, args...)
			if !hasBlocker(e, "connection_not_activated") || !hasAction(e, "connect", "--agent", tc.kind, "--dry-run", "--json") {
				t.Fatalf("doctor did not offer a connection preview: %+v", e)
			}
		})
	}
}

func TestCaptureReviewUsesFreshCaptureSHA(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"allow-capture", "defer-capture"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.row.Status = "configured_pending_new_session"
			args := []string{"connect", "--agent", testAgent, "--phase", phase}
			captureSHA := strings.Repeat("b", 64)
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				switch {
				case req.URL.Path == "/v1/connections":
					if req.URL.Query().Get("force") != "1" {
						t.Error("capture review did not request fresh evidence")
					}
				case strings.HasSuffix(req.URL.Path, "/preview"):
					t.Error("capture review requested the configuration preview")
					return response(200, controlplane.ConnectionPreview{ProposedSHA256: testSHA})
				case req.Method == http.MethodPost:
					action := strings.ReplaceAll(phase, "-", "_")
					assertAtomicOperation(t, f, body, "connection_"+action)
					if body.ProposedSHA != captureSHA || !strings.HasSuffix(req.URL.Path, "/"+action) {
						t.Fatalf("invalid capture mutation: %s %+v", req.URL.Path, body)
					}
					row := f.row
					row.Capture = nil // The Python mutation reply omits the live capture view.
					return response(200, controlplane.ConnectionActionResult{Revision: f.revision + 1, Connection: row})
				}
				return nil, nil
			}
			dry := func() envelope {
				return f.command(ExitPreflightBlocked, append(slices.Clone(args), "--dry-run")...)
			}
			f.row.Capture = &controlplane.AgentCaptureState{State: "needs_permission", ReviewSHA: "invalid"}
			if e := dry(); !hasBlocker(e, "capture_review_unavailable") || e.Artifacts.ReviewedSHA != "" {
				t.Fatalf("invalid capture SHA accepted: %+v", e)
			}
			f.row.Capture.ReviewSHA = captureSHA
			e := f.command(ExitActionRequired, append(slices.Clone(args), "--dry-run")...)
			if e.Artifacts.ReviewedSHA != captureSHA || !hasAction(e, "connect", "--agent", testAgent, "--apply", "--instance-id", testInstance, "--phase", phase, "--reviewed-sha", captureSHA, "--json") {
				t.Fatalf("capture SHA missing from advice: %+v", e)
			}
			apply := append(slices.Clone(args), "--apply", "--reviewed-sha")
			e = f.command(ExitPreflightBlocked, append(slices.Clone(apply), testSHA)...)
			if !hasBlocker(e, "reviewed_sha_mismatch") || e.Artifacts.ReviewedSHA != captureSHA || f.writes != 0 {
				t.Fatalf("configuration SHA accepted for capture: %+v", e)
			}
			exit := ExitOK
			if phase == "allow-capture" {
				exit = ExitHumanRequired
			}
			e = f.command(exit, append(apply, captureSHA)...)
			if f.writes != 1 {
				t.Fatal("reviewed capture action did not run")
			}
			f.assertRetained(e.OperationID, false)
		})
	}
}

func TestRepairOrphanTraeHooksOffersFreshConnection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/repair-trae-hooks") && !body.DryRun {
			assertAtomicOperation(t, f, body, "connection_repair_trae_hooks")
			return response(200, controlplane.TraeHooksRepairResult{Repaired: true, Mode: "orphan_cleanup", PreservedThirdPartyGroups: 2})
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/operations/") {
			return response(200, controlplane.ConnectionOperationReceipt{
				Recorded: true, Status: "complete", Action: "repair-trae-hooks",
				Result: &controlplane.ConnectionActionResult{
					Repaired: true, Mode: "orphan_cleanup", PreservedThirdPartyGroups: 2,
					RemovedEvents: []string{"SessionEnd", "untrusted-removed"}, RetainedEvents: []string{"Stop", "untrusted-retained"},
				},
			})
		}
		return nil, nil
	}
	e := f.command(ExitOK, "repair", "trae-hooks", "--apply", "--instance-id", testInstance, "--reviewed-sha", testSHA)
	if len(e.HumanSteps) != 0 || len(e.NextActions) != 1 || !hasAction(e, "connect", "--agent", "trae_ide", "--dry-run", "--json") ||
		e.Artifacts.HookRepair == nil || e.Artifacts.HookRepair.PreservedThirdPartyGroups != 2 {
		t.Fatalf("orphan cleanup did not offer fresh connection: %+v", e)
	}
	e = f.command(ExitActionRequired, "doctor", "--operation-id", e.OperationID)
	if receipt := e.Artifacts.ConnectionOperation; receipt == nil || receipt.Kind != "repair-trae-hooks" ||
		receipt.Status != "complete" || receipt.PreservedThirdPartyGroups != 2 {
		t.Fatalf("missing real TRAE action in receipt: %+v", receipt)
	}
	assertRecoveredHookRepair(t, e)
}

func assertRecoveredHookRepair(t *testing.T, e envelope) {
	t.Helper()
	repair := e.Artifacts.HookRepair
	if repair == nil || !repair.Repaired || repair.PreservedThirdPartyGroups != 2 ||
		!slices.Equal(repair.RemovedEvents, []string{"SessionEnd"}) || !slices.Equal(repair.RetainedEvents, []string{"Stop"}) {
		t.Fatalf("unexpected hook repair artifact: %+v", repair)
	}
}

func TestVerifyZeroDownloadsChecksRetainedWorkerAndJournal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.r.InspectWorkerFn = bootstrap.InspectInstallWorker
	args := []string{"verify", "--stage", "setup", "--expect-zero-downloads"}
	f.command(ExitOK, args...)
	statePath, err := bootstrap.WorkerStatePath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	worker := bootstrap.InstallWorkerState{Schema: 1, OperationID: "first-install", State: "completed", BytesDone: 128}
	writeJSON(t, statePath, worker)
	f.command(ExitOK, args...)
	checkBlocked := func() {
		t.Helper()
		e := f.command(ExitActionRequired, args...)
		if !e.Components.Complete || !hasBlocker(e, "zero_downloads_not_satisfied") {
			t.Fatalf("download evidence passed verification: %+v", e)
		}
	}
	worker.NetworkBytesDownloaded = 1
	writeJSON(t, statePath, worker)
	checkBlocked()
	worker.NetworkBytesDownloaded = 0
	writeJSON(t, statePath, worker)
	journalPath := filepath.Join(f.root, "installation", "installation.json")
	for _, journal := range []bootstrap.Journal{
		{Schema: 1, Steps: map[string]bootstrap.Step{}, Status: bootstrap.Installing},
		{Schema: 1, Steps: map[string]bootstrap.Step{}, Status: bootstrap.Available, ActiveStep: "runtime"},
	} {
		writeJSON(t, journalPath, journal)
		checkBlocked()
	}
	writeFile(t, journalPath, []byte("{"))
	e := f.command(ExitNeedsReview, args...)
	if !hasBlocker(e, "installation_journal_unreadable") {
		t.Fatalf("unreadable journal did not fail closed: %+v", e)
	}
}

func TestInstallApplySharesWaitDeadline(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"complete plan", "incomplete plan"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				f.owner = bootstrap.OwnerRuntimeStatus{State: "stopped"}
				if phase == "incomplete plan" {
					f.plan.Items[0].Observation.Status = bootstrap.Missing
				}
				var deadline time.Time
				checkDeadline := func(ctx context.Context) {
					t.Helper()
					got, ok := ctx.Deadline()
					if !ok || time.Until(got) > 2*time.Second {
						t.Fatalf("caller exceeded --wait 2: %v", got)
					}
					if deadline.IsZero() {
						deadline = got
					} else if !got.Equal(deadline) {
						t.Fatalf("deadline changed from %v to %v", deadline, got)
					}
				}
				newEnv := f.r.NewEnvFn
				f.r.NewEnvFn = func(ctx context.Context, assets fs.FS, opts bootstrap.EnvironmentOptions) (*bootstrap.Environment, error) {
					checkDeadline(ctx)
					return newEnv(ctx, assets, opts)
				}
				f.r.DetectPlanFn = func(_ *bootstrap.Environment, ctx context.Context) (bootstrap.Plan, error) {
					checkDeadline(ctx)
					return f.plan, nil
				}
				f.r.LaunchWorkerFn = func(ctx context.Context, _ string, _ []string, _ bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
					checkDeadline(ctx)
					<-ctx.Done()
					return bootstrap.InstallWorkerState{}, ctx.Err()
				}
				e := f.command(ExitInProgressOrBusy, "install", "--apply", "--wait", "2", "--plan-digest", f.plan.Digest())
				if !hasBlocker(e, "install_worker_active") || e.RetryAfterMS != retryMillis {
					t.Fatalf("deadline did not return retry advice: %+v", e)
				}
			})
		})
	}
}

func TestRepairStaleDaemonPassesRealProtectedStoreCheckOffline(t *testing.T) {
	t.Parallel()
	const probeScript = "#!/bin/sh\n: > \"$(/usr/bin/dirname \"$0\")/runtime-probe-called\"\nexit 1\n"
	warmDir := t.TempDir()
	warmPython := filepath.Join(warmDir, "test-python")
	writeFile(t, warmPython, []byte(probeScript))
	if err := os.Chmod(warmPython, 0500); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command(warmPython).Run()
	for _, setup := range []bool{true, false} {
		name := "ready"
		if setup {
			name = "first_install_setup"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.bootstrapAssets = engine.BootstrapAssets
			f.doc.First.Setup = setup
			f.owner.State, f.owner.StaleVersionDetected = "stale_runtime", true
			python := filepath.Join(f.root, "test-python")
			probe := filepath.Join(f.root, "runtime-probe-called")
			if err := os.MkdirAll(filepath.Dir(python), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(warmPython, python); err != nil {
				writeFile(t, python, []byte(probeScript))
				if err := os.Chmod(python, 0700); err != nil {
					t.Fatal(err)
				}
			}
			opts := bootstrap.EnvironmentOptions{Root: f.root, Python: python, UV: filepath.Join(f.root, "missing-uv")}
			if err := bootstrap.SaveSelections(opts, f.config); err != nil {
				t.Fatal(err)
			}
			env, err := bootstrap.NewEnvironment(context.Background(), f.bootstrapAssets, opts)
			if err != nil {
				t.Fatal(err)
			}
			source, err := env.Requirement("cognee-source")
			if err != nil {
				t.Fatal(err)
			}
			f.doc.ServiceCommit = source.Version
			f.writeConfig()
			configBefore := readTestFile(t, f.config)
			writeJSON(t, filepath.Join(f.store, "store-identity.json"), bootstrap.StoreReceipt{
				Schema: 1, StoreID: f.doc.First.StoreID, OwnerID: "owner", KeyID: "key", SourceCommit: source.Version, StorageInitialized: true,
			})
			model, err := env.Requirement("embedding")
			if err != nil {
				t.Fatal(err)
			}
			writeJSON(t, filepath.Join(f.store, "first-install-input.json"), struct {
				bootstrap.StoreIdentity
				Root       string `json:"root"`
				Source     string `json:"source"`
				Archive    string `json:"archive"`
				ModelCache string `json:"model_cache"`
			}{StoreIdentity: bootstrap.StoreIdentity{Schema: 1, StoreID: f.doc.First.StoreID, SourceCommit: source.Version, ModelDigest: model.Digest},
				Root: f.store, Source: source.Destination, Archive: filepath.Join(source.Destination, "service.tar.gz"), ModelCache: model.Destination})
			writeFile(t, f.doc.OwnerTokenFile, []byte("test-owner-token-unused-by-probe"))
			f.r.NewEnvFn = func(ctx context.Context, assets fs.FS, opts bootstrap.EnvironmentOptions) (*bootstrap.Environment, error) {
				env, err := bootstrap.NewEnvironment(ctx, assets, opts)
				if err == nil {
					// The local probe deliberately fails after the real binding check.
					env.Runtime.Native = nil
				}
				return env, err
			}
			f.r.RestartStaleOwnerFn = func(context.Context, bootstrap.FirstRuntime, string, fs.FS) error {
				t.Fatal("incomplete offline components should prevent daemon restart")
				return nil
			}
			f.r.PrepareRuntimeForUpgradeFn = (*bootstrap.Environment).PrepareFirstRuntimeForUpgrade
			e := f.command(ExitFatal, "repair", "stale-daemon", "--apply", "--owner-fingerprint", testFingerprint)
			if !hasBlocker(e, "runtime_preparation_failed") {
				t.Fatalf("unexpected repair failure: %+v", e)
			}
			if _, err := os.Stat(probe); err != nil {
				t.Fatalf("real upgrade preparation did not reach component probing after validating the existing store: %v", err)
			}
			if !bytes.Equal(configBefore, readTestFile(t, f.config)) {
				t.Fatal("real upgrade preparation changed runtime.json")
			}
		})
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRepairPendingUnrecordedBeta2RevisionConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const id = "ui-beta2-conflict"
	_, err := controlplane.RecordUIOperation(f.ops, controlplane.UIOperation{
		Operation: id, Profile: f.row.ID, Kind: "connection_activate", Revision: f.revision - 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
		if req.URL.Path != "/v1/connections/operations/"+id+"/reconcile" {
			return nil, nil
		}
		if req.Method != http.MethodPost || body.InstanceID != f.row.ID || body.Action != "activate" {
			t.Fatalf("reconciliation lost the beta.2 target: %s %+v", req.Method, body)
		}
		f.assertRetained(id, true)
		if body.DryRun {
			if body.Revision != 0 {
				t.Fatalf("unexpected preview revision: %d", body.Revision)
			}
			return response(200, controlplane.ConnectionOperationReceipt{
				InstanceID: f.row.ID, Action: "activate", Status: "unrecorded", Reconcilable: true, Phase: "pre_mutation",
			})
		}
		if body.Revision != f.revision {
			t.Fatalf("reconciliation used revision %d, want %d", body.Revision, f.revision)
		}
		return response(200, controlplane.ConnectionOperationReceipt{
			Recorded: true, Status: "complete", Reconciled: true, SafeToUnlock: true,
			OperationID: id, InstanceID: f.row.ID, Action: "activate",
		})
	}
	f.command(ExitActionRequired, "doctor")
	args := []string{"repair", "pending-ops", "--operation-id", id}
	preview := f.command(ExitActionRequired, append(slices.Clone(args), "--dry-run")...)
	receipt := preview.Artifacts.ConnectionOperation
	if receipt == nil || receipt.Recorded || receipt.Status != "unrecorded" || receipt.Kind != "activate" ||
		!receipt.Reconcilable || receipt.SafeToUnlock || receipt.Phase != "pre_mutation" || f.writes != 0 {
		t.Fatalf("incorrect unrecorded preview: %+v", preview)
	}
	applied := f.command(ExitOK, append(slices.Clone(args), "--apply")...)
	if receipt := applied.Artifacts.ConnectionOperation; receipt == nil || !receipt.Recorded || !receipt.Reconciled || f.writes != 1 {
		t.Fatalf("missing completed reconciliation: %+v", applied)
	}
	f.assertRetained(id, false)
}

func TestReviewRecoversInterruptedTraeHookRepair(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*controlplane.ConnectionOperationReceipt)
	}{
		{"matching", nil},
		{"unknown", func(r *controlplane.ConnectionOperationReceipt) { r.Status = "unknown" }},
		{"wrong operation", func(r *controlplane.ConnectionOperationReceipt) { r.OperationID = "ui-other" }},
		{"wrong instance", func(r *controlplane.ConnectionOperationReceipt) { r.InstanceID = "other-instance" }},
		{"wrong action", func(r *controlplane.ConnectionOperationReceipt) { r.Action = "activate" }},
		{"empty operation", func(r *controlplane.ConnectionOperationReceipt) { r.OperationID = "" }},
		{"empty instance", func(r *controlplane.ConnectionOperationReceipt) { r.InstanceID = "" }},
		{"empty action", func(r *controlplane.ConnectionOperationReceipt) { r.Action = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			args := []string{"connect", "--agent", testAgent, "--phase", "review"}
			f.command(ExitOK, append(slices.Clone(args), "--dry-run")...)
			f.row.Status = "activation_unknown"
			_, err := controlplane.RecordUIOperation(f.ops, controlplane.UIOperation{
				Operation: testOperation, Profile: f.row.ID, Kind: "connection_repair_trae_hooks", Revision: f.revision,
			})
			if err != nil {
				t.Fatal(err)
			}
			reviewed, queried := false, false
			f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
				switch req.URL.Path {
				case "/v1/connections/operations/" + testOperation + "/reconcile":
					if !body.DryRun || body.InstanceID != f.row.ID || body.Action != "repair-trae-hooks" {
						t.Fatalf("wrong hook repair preview: %+v", body)
					}
					return response(200, controlplane.ConnectionOperationReceipt{Recorded: true, Status: "unknown", Phase: "post_mutation"})
				case "/v1/connections/" + f.row.ID + "/review":
					if req.Method != http.MethodPost || body.Revision != f.revision || !strings.HasPrefix(body.OperationID, "ui-cli-") ||
						body.ProposedSHA != "" || body.ReviewedSHA != "" {
						t.Fatalf("wrong host review request: %+v", body)
					}
					f.assertRetained(testOperation, true)
					reviewed = true
					f.row.Status = "prepared"
					return response(200, controlplane.ConnectionActionResult{Revision: f.revision + 1, Connection: f.row})
				case "/v1/connections/operations/" + testOperation:
					if req.Method != http.MethodGet || !reviewed {
						t.Fatal("queried pending receipt before review")
					}
					queried = true
					f.assertRetained(testOperation, true)
					receipt := controlplane.ConnectionOperationReceipt{
						Recorded: true, Status: "complete", OperationID: testOperation, InstanceID: f.row.ID,
						Action: "repair_trae_hooks", Reconciled: true, SafeToUnlock: true,
						Result: &controlplane.ConnectionActionResult{
							Repaired: true, PreservedThirdPartyGroups: 2,
							RemovedEvents: []string{"SessionEnd", "untrusted-removed"}, RetainedEvents: []string{"Stop", "untrusted-retained"},
						},
					}
					if tc.mutate != nil {
						tc.mutate(&receipt)
					}
					return response(200, receipt)
				}
				return nil, nil
			}
			dryAdvice := []string{"connect", "--agent", testAgent, "--instance-id", f.row.ID, "--phase", "review", "--dry-run", "--json"}
			applyAdvice := []string{"connect", "--agent", testAgent, "--instance-id", f.row.ID, "--phase", "review", "--apply", "--json"}
			for _, command := range [][]string{{"doctor"}, {"repair", "pending-ops", "--dry-run"}} {
				e := f.command(ExitNeedsReview, command...)
				if !hasAction(e, dryAdvice...) || !slices.ContainsFunc(e.NextActions, func(action nextAction) bool {
					return action.Requires == confirmation && slices.Equal(action.Args, applyAdvice)
				}) {
					t.Fatalf("missing host review advice: %+v", e)
				}
			}
			e := f.command(ExitActionRequired, append(slices.Clone(args), "--dry-run")...)
			if !hasBlocker(e, "connection_review_required") || !hasAction(e, applyAdvice...) || f.writes != 0 || reviewed || queried {
				t.Fatalf("host review preview was not read-only: %+v", e)
			}
			want := ExitOK
			if tc.mutate != nil {
				want = ExitNeedsReview
			}
			e = f.command(want, append(slices.Clone(args), "--apply")...)
			if !reviewed || !queried || f.writes != 1 || e.Connections.Hosts[0].Status != "prepared" ||
				hasBlocker(e, "connection_operation_unconfirmed") != (want == ExitNeedsReview) {
				t.Fatalf("unexpected review outcome: %+v", e)
			}
			f.assertRetained(testOperation, want == ExitNeedsReview)
			if want == ExitOK {
				assertRecoveredHookRepair(t, e)
			} else if e.Artifacts.HookRepair != nil {
				t.Fatalf("unconfirmed receipt produced a hook repair artifact: %+v", e.Artifacts.HookRepair)
			}
		})
	}
}

func TestSingleConfirmationBlocksBaselineDrift(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.row.Kind, f.row.ID, f.row.Status = "antigravity", "antigravity-0123456789abcdef01234567", "not_connected"
	f.handler = func(req *http.Request, body requestBody) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/connections/" + f.row.ID + "/preview":
			baseline := strings.Repeat("b", 64)
			if f.row.Status == "prepared" {
				baseline = strings.Repeat("c", 64)
			}
			return response(200, controlplane.ConnectionPreview{
				Revision: f.revision, ProposedSHA256: testSHA, BaselineSHA256: baseline,
			})
		case "/v1/connections/" + f.row.ID + "/prepare":
			assertAtomicOperation(t, f, body, "connection_prepare")
			f.row.Status = "prepared"
			f.revision++
			return response(200, controlplane.ConnectionActionResult{Revision: f.revision, Connection: f.row})
		case "/v1/connections/" + f.row.ID + "/activate":
			t.Fatal("activation started after configuration baseline drift")
		}
		return nil, nil
	}
	e := f.command(ExitPreflightBlocked, "connect", "--agent", f.row.Kind, "--apply", "--reviewed-sha", testSHA)
	if !hasBlocker(e, "host_configuration_changed") || f.writes != 1 ||
		!hasAction(e, "connect", "--agent", f.row.Kind, "--apply", "--instance-id", f.row.ID, "--phase", "activate", "--reviewed-sha", testSHA, "--json") {
		t.Fatalf("missing baseline drift blocker or updated advice: %+v", e)
	}
}

func TestInstallReattachesWithApprovedDigestAfterPlanChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plan.Items[0].Observation.Status = bootstrap.Missing
	digest := f.plan.Digest()
	f.plan.Items[0].Observation.Status = bootstrap.Available
	if f.plan.Digest() == digest {
		t.Fatal("fixture did not change the live plan")
	}
	f.workerActive = true
	f.worker = bootstrap.InstallWorkerState{
		State: "running", OperationID: "first-install", PlanDigest: digest, PID: 123, ConfigPath: f.config, StartRuntime: true,
	}
	f.r.LaunchWorkerFn = func(context.Context, string, []string, bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
		t.Fatal("reattach launched a second worker")
		return bootstrap.InstallWorkerState{}, nil
	}
	args := []string{"install", "--apply", "--plan-digest"}
	f.command(ExitInProgressOrBusy, append(slices.Clone(args), digest, "--wait", "0")...)
	e := f.command(ExitPreflightBlocked, append(slices.Clone(args), strings.Repeat("b", 64))...)
	if !hasBlocker(e, "plan_digest_mismatch") {
		t.Fatalf("wrong digest was not blocked: %+v", e)
	}
	f.worker.PlanDigest = ""
	writeJSON(t, filepath.Join(f.root, "installation", "installation.json"), bootstrap.Journal{
		Schema: 1, Steps: map[string]bootstrap.Step{}, ApprovedDigest: digest,
	})
	f.r.SleepFn = func(context.Context, time.Duration) error {
		f.worker.State, f.workerActive = "completed", false
		return nil
	}
	busy := f.command(ExitInProgressOrBusy, append(slices.Clone(args), digest, "--wait", "2")...)
	if !hasBlocker(busy, "install_worker_active") || !f.workerActive {
		t.Fatalf("worker without its own approved digest was attached: %+v", busy)
	}
	f.worker.PlanDigest = digest
	f.command(ExitOK, append(slices.Clone(args), digest, "--wait", "2")...)
}

func TestFullInstallWorkerCompletionRequiresReadyDaemon(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, state, phase, blocker string
		locked, ready               bool
		exit                        int
	}{
		{"stopped", "stopped", "", "daemon_stopped", false, false, ExitActionRequired},
		{"not ready", "ready", "ready", "daemon_not_ready", true, false, ExitActionRequired},
		{"starting state", "starting", "", "daemon_starting", false, false, ExitInProgressOrBusy},
		{"starting owner phase", "ready", "starting", "daemon_starting", true, false, ExitInProgressOrBusy},
		{"ready", "ready", "ready", "", true, true, ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.worker = bootstrap.InstallWorkerState{
				State: "running", OperationID: "first-install", PlanDigest: f.plan.Digest(), PID: 123, ConfigPath: f.config, StartRuntime: true,
			}
			f.workerActive = true
			f.owner = bootstrap.OwnerRuntimeStatus{State: tc.state, OwnerPhase: tc.phase, OwnerLocked: tc.locked}
			f.r.FirstRuntimeReadyFn = func(context.Context, string) (bool, error) { return tc.ready, nil }
			f.r.SleepFn = func(context.Context, time.Duration) error {
				f.worker.State, f.workerActive = "completed", false
				return nil
			}
			e := f.command(tc.exit, "install", "--apply", "--plan-digest", f.plan.Digest(), "--wait", "2")
			if e.Components.WorkerActive || e.Components.Progress.State != "completed" ||
				(tc.blocker != "" && !hasBlocker(e, tc.blocker)) {
				t.Fatalf("wrong completed worker outcome: %+v", e)
			}
		})
	}
}

func TestDoctorReportsWorkerTelemetryAndMatchesExactID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.worker = bootstrap.InstallWorkerState{
		OperationID: "other-install", State: "running", Stage: "starting",
		FallbackAttempt: 2, FallbackTotal: 3, NetworkBytesDownloaded: 128, RuntimeSyncRan: true,
	}
	f.workerActive = true
	e := f.command(ExitInProgressOrBusy, "doctor", "--operation-id", f.worker.OperationID)
	p := e.Components.Progress
	if p == nil || p.Stage != "starting" || p.FallbackAttempt != 2 || p.FallbackTotal != 3 || p.NetworkBytesDownloaded != 128 || !p.RuntimeSyncRan {
		t.Fatalf("worker telemetry was dropped: %+v", p)
	}
	f.worker.State, f.workerActive = "failed", false
	e = f.command(ExitActionRequired, "doctor", "--operation-id", "first-install")
	if e.OperationID == "first-install" || hasBlocker(e, "install_worker_failed") {
		t.Fatalf("unrelated worker state matched requested ID: %+v", e)
	}
	e = f.command(ExitFatal, "doctor", "--operation-id", f.worker.OperationID)
	if e.OperationID != f.worker.OperationID || !hasBlocker(e, "install_worker_failed") {
		t.Fatalf("exact worker ID did not match: %+v", e)
	}
}

func TestComponentInstallRejectsIncompatibleWorker(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inspect", "launch"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.plan.Items = append(f.plan.Items, bootstrap.Item{
				Requirement: bootstrap.Requirement{ID: "embedding"}, Observation: bootstrap.Observation{Status: bootstrap.Missing},
			})
			digest := f.plan.Digest()
			other := bootstrap.InstallWorkerState{
				State: "running", OperationID: "first-install", PID: 123, PlanDigest: digest,
				ConfigPath: f.config, Components: []string{"python"},
			}
			if source == "inspect" {
				f.worker, f.workerActive = other, true
			}
			inspections, launches := 0, 0
			inspect := f.r.InspectWorkerFn
			f.r.InspectWorkerFn = func(ctx context.Context, root string) (bootstrap.InstallWorkerState, bool, error) {
				inspections++
				return inspect(ctx, root)
			}
			f.r.LaunchWorkerFn = func(_ context.Context, _ string, _ []string, cfg bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
				launches++
				if !slices.Equal(cfg.Components, []string{"embedding"}) || cfg.StartRuntime || cfg.RuntimeHash != f.owner.RunningRuntimeHash {
					t.Fatalf("unexpected component worker config: %+v", cfg)
				}
				return other, nil
			}
			e := f.command(ExitInProgressOrBusy, "install", "--apply", "--plan-digest", digest, "--component", "embedding", "--wait", "2")
			wantLaunches := 0
			if source == "launch" {
				wantLaunches = 1
			}
			if inspections != 1 || launches != wantLaunches || !hasBlocker(e, "install_worker_active") {
				t.Fatalf("incompatible worker was attached: inspections=%d launches=%d response=%+v", inspections, launches, e)
			}
		})
	}
}

func TestComponentInstallWithCompletePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plan.Items = append(f.plan.Items, bootstrap.Item{
		Requirement: bootstrap.Requirement{ID: "python"}, Observation: bootstrap.Observation{Status: bootstrap.Available},
	})
	f.r.InspectOwnerStatusFn = func(context.Context, string, string, fs.FS) (bootstrap.OwnerRuntimeStatus, error) {
		t.Fatal("component install checked daemon")
		return bootstrap.OwnerRuntimeStatus{}, nil
	}
	f.r.LaunchWorkerFn = func(context.Context, string, []string, bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
		t.Fatal("component install launched a worker")
		return bootstrap.InstallWorkerState{}, nil
	}
	args := []string{"install", "--apply", "--plan-digest", f.plan.Digest(), "--component"}
	f.command(ExitOK, append(slices.Clone(args), "python")...)
	f.command(ExitOK, append(slices.Clone(args), "uv")...)
	e := f.command(ExitPreflightBlocked, append(slices.Clone(args), "embedding")...)
	if !hasBlocker(e, "component_absent_from_plan") {
		t.Fatalf("missing component blocker: %+v", e)
	}
	f.plan.Items[0].Observation.Status = bootstrap.Missing
	args[3] = f.plan.Digest()
	e = f.command(ExitPreflightBlocked, append(slices.Clone(args), "uv")...)
	if !hasBlocker(e, "component_absent_from_plan") {
		t.Fatalf("missing uv blocker for incomplete runtime: %+v", e)
	}
}

func TestComponentInstallReprobesCompletedWorker(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status bootstrap.Status
		exit   int
	}{
		{"missing", bootstrap.Missing, ExitActionRequired},
		{"available", bootstrap.Available, ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.plan.Items = append(f.plan.Items, bootstrap.Item{
				Requirement: bootstrap.Requirement{ID: "python"}, Observation: bootstrap.Observation{Status: bootstrap.Missing},
			})
			digest := f.plan.Digest()
			f.worker = bootstrap.InstallWorkerState{
				State: "running", OperationID: "first-install", PID: 123, PlanDigest: digest,
				ConfigPath: f.config, Components: []string{"python"},
			}
			f.workerActive = true
			f.r.SleepFn = func(context.Context, time.Duration) error {
				f.worker.State, f.workerActive = "completed", false
				f.plan.Items[1].Observation.Status = tc.status
				return nil
			}
			// The installed runtime omits uv from the worker scope.
			e := f.command(tc.exit, "install", "--apply", "--plan-digest", digest, "--component", "uv", "--component", "python", "--wait", "2")
			if e.Components.Progress.State != "completed" || hasBlocker(e, "components_incomplete") != (tc.exit != ExitOK) {
				t.Fatalf("unexpected outcome after worker completed: %+v", e)
			}
		})
	}
}

func TestWorkerLockContentionExit(t *testing.T) {
	t.Parallel()
	for code, message := range map[string]string{
		"upgrade_locked":        "运行环境升级正在进行中，请稍后重试。",
		"owner_busy":            "后台服务锁正在被占用，请稍后重试。",
		"install_worker_active": "另一项组件安装正在进行中，请稍后重试。",
	} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			readyOwner := f.owner
			f.row.Detected = false
			f.owner = bootstrap.OwnerRuntimeStatus{State: "stopped"}
			f.r.LaunchWorkerFn = func(_ context.Context, _ string, _ []string, cfg bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
				f.worker = bootstrap.InstallWorkerState{State: "failed", ErrorCode: code, OperationID: cfg.OperationID}
				return f.worker, nil
			}
			for _, args := range [][]string{
				{"install", "--apply", "--plan-digest", f.plan.Digest()},
				{"doctor", "--operation-id", "first-install"},
			} {
				e := f.command(ExitInProgressOrBusy, args...)
				if e.RetryAfterMS != 10000 || len(e.Blockers) != 1 || !hasBlocker(e, code) ||
					e.Components.Progress == nil || e.Components.Progress.ErrorCode != code || e.Components.Progress.SafeMessage != message ||
					len(e.NextActions) != 2 || !hasAction(e, "install", "--plan", "--json") ||
					!hasAction(e, "install", "--apply", "--plan-digest", f.plan.Digest(), "--json") {
					t.Fatalf("worker lock did not return its retry receipt: %+v", e)
				}
			}
			f.owner = readyOwner
			e := f.command(ExitOK, "doctor", "--operation-id", "first-install")
			if len(e.Blockers) != 0 || len(e.NextActions) != 0 || e.OperationID != "first-install" ||
				e.Components.Progress == nil || e.Components.Progress.ErrorCode != code {
				t.Fatalf("stale contention blocked ready runtime or lost the receipt: %+v", e)
			}
		})
	}
}

func TestInspectWorkerPreservesTerminalSnapshotAtDeadline(t *testing.T) {
	t.Parallel()
	for _, state := range []bootstrap.InstallWorkerState{
		{State: "completed", Stage: "ready"},
		{State: "failed", Stage: "starting_runtime", ErrorCode: "install_worker_active"},
	} {
		t.Run(state.State, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
			s := session{ctx: ctx, envelope: &envelope{Command: "install"}, options: options{values: map[string]string{"apply": "true"}}}
			s.InspectWorkerFn = func(context.Context, string) (bootstrap.InstallWorkerState, bool, error) {
				return state, false, nil
			}
			s.DetectOrphansFn = func(context.Context, string) ([]int, error) { return nil, nil }
			got, active, ok := s.inspectWorker()
			if !ok || active || got.State != state.State || s.Components.Progress == nil ||
				s.Components.Progress.State != state.State || len(s.Blockers) != 0 {
				t.Fatalf("terminal receipt lost at deadline: %+v, active = %t, ok = %t, envelope = %+v", got, active, ok, s.envelope)
			}
		})
	}
}

func TestInstallApplyRetriesDeadlineKilledWorkerProbes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"active snapshot", "prior active snapshot", "orphan probe"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				f.owner = bootstrap.OwnerRuntimeStatus{State: "stopped"}
				worker := bootstrap.InstallWorkerState{
					OperationID: "first-install", ConfigPath: f.config, StartRuntime: true,
					PlanDigest: f.plan.Digest(), State: "running", Stage: "starting_runtime",
				}
				wantActive, wantStage := true, worker.Stage
				waitForDeadline := func(ctx context.Context) error {
					if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(f.now.Add(time.Second)) {
						t.Fatalf("probe deadline = %v, want --wait 1", deadline)
					}
					<-ctx.Done()
					if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
						t.Fatalf("probe context = %v", ctx.Err())
					}
					return errors.New("signal: killed")
				}
				if mode == "orphan probe" {
					wantActive, wantStage = false, "ready"
					f.worker = bootstrap.InstallWorkerState{State: "completed", Stage: wantStage}
					f.r.DetectOrphansFn = func(ctx context.Context, _ string) ([]int, error) {
						return nil, waitForDeadline(ctx)
					}
				} else {
					calls := 0
					f.r.InspectWorkerFn = func(ctx context.Context, _ string) (bootstrap.InstallWorkerState, bool, error) {
						calls++
						if mode == "prior active snapshot" {
							if calls == 1 {
								return worker, true, nil
							}
							return bootstrap.InstallWorkerState{}, false, waitForDeadline(ctx)
						}
						return worker, true, waitForDeadline(ctx)
					}
				}
				e := f.command(ExitInProgressOrBusy, "install", "--apply", "--wait", "1", "--plan-digest", f.plan.Digest())
				if len(e.Blockers) != 1 || e.Blockers[0].Code != "install_worker_active" || e.RetryAfterMS != 10000 ||
					e.Components.WorkerActive != wantActive || e.Components.Progress == nil || e.Components.Progress.Stage != wantStage {
					t.Fatalf("unexpected retry receipt: %+v", e)
				}
				if !strings.Contains(e.AgentGuidance, "On exit 20 follow next_actions after retry_after_ms") {
					t.Fatalf("missing retry guidance: %q", e.AgentGuidance)
				}
			})
		})
	}
}

func TestInstallWorkerContentionOffersFreshPlanDigest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.owner = bootstrap.OwnerRuntimeStatus{State: "stopped"}
	f.plan.Items[0].Observation.Status = bootstrap.Missing
	before := f.plan.Digest()
	f.r.LaunchWorkerFn = func(_ context.Context, _ string, _ []string, cfg bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
		f.plan.Items[0].Observation.Status = bootstrap.Available
		f.worker = bootstrap.InstallWorkerState{
			State: "failed", Stage: "starting_runtime", ErrorCode: "install_worker_active", OperationID: cfg.OperationID,
		}
		return f.worker, nil
	}
	e := f.command(ExitInProgressOrBusy, "install", "--apply", "--plan-digest", before)
	after := f.plan.Digest()
	if before == after || e.Artifacts.PlanDigest != after || len(e.NextActions) != 2 ||
		!hasAction(e, "install", "--apply", "--plan-digest", after, "--json") {
		t.Fatalf("contention advice did not use the updated plan: %+v", e)
	}

	f.plan.Items[0].Observation.Status = bootstrap.Missing
	e = f.command(ExitInProgressOrBusy, "doctor", "--operation-id", "first-install")
	if len(e.NextActions) != 2 || !hasAction(e, "install", "--plan", "--json") ||
		!hasAction(e, "install", "--apply", "--plan-digest", before, "--json") {
		t.Fatalf("component and contention advice duplicated the install commands: %+v", e)
	}
}

func TestInstallRuntimeRequiresAuthenticatedReadiness(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"already ready", "detached start"} {
		for _, tc := range []struct {
			name, blocker     string
			ready, graphStale bool
			exit              int
		}{
			{"not authenticated", "daemon_not_ready", false, false, ExitActionRequired},
			{"stale graph", "graph_assets_stale", true, true, ExitActionRequired},
			{"authenticated", "", true, false, ExitOK},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				if path == "detached start" {
					f.owner.State, f.owner.OwnerLocked = "stopped", false
				}
				f.owner.GraphAssetsStale = tc.graphStale
				starts, probes := 0, 0
				f.r.LaunchWorkerFn = func(_ context.Context, _ string, _ []string, cfg bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
					starts++
					f.owner.State, f.owner.OwnerLocked = "ready", true
					f.worker = bootstrap.InstallWorkerState{State: "completed", Stage: "ready", OperationID: cfg.OperationID}
					return f.worker, nil
				}
				f.r.FirstRuntimeReadyFn = func(_ context.Context, config string) (bool, error) {
					probes++
					if config != f.config {
						t.Fatalf("readiness probe used wrong config: %s", config)
					}
					return tc.ready, nil
				}
				e := f.command(tc.exit, "install", "--apply", "--plan-digest", f.plan.Digest())
				if (tc.blocker != "" && !hasBlocker(e, tc.blocker)) || (starts == 1) != (path == "detached start") ||
					(probes == 1) != !tc.graphStale {
					t.Fatalf("wrong readiness checks: starts=%d probes=%d response=%+v", starts, probes, e)
				}
			})
		}
	}
}

func TestRuntimePreparationLockContention(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err                         error
		installReason, repairReason string
	}{
		{bootstrap.ErrInstallWorkerBusy, "install_worker_active", "install_worker_active"},
		{supervisor.ErrUpgradeLocked, "upgrade_locked", "owner_busy"},
		{supervisor.ErrOwnerBusy, "owner_busy", "owner_busy"},
		{supervisor.ErrOwnerLocked, "owner_busy", "owner_busy"},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			check := func(reason string, args ...string) {
				t.Helper()
				e := f.command(ExitInProgressOrBusy, args...)
				if e.RetryAfterMS != 10000 || !hasBlocker(e, reason) {
					t.Fatalf("missing lock retry advice: %+v", e)
				}
			}
			prepare := func(*bootstrap.Environment, context.Context, string, fs.FS) (bootstrap.FirstRuntime, error) {
				return bootstrap.FirstRuntime{}, tc.err
			}
			f.owner.State, f.owner.OwnerLocked = "stopped", false
			f.r.LaunchWorkerFn = func(context.Context, string, []string, bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error) {
				return bootstrap.InstallWorkerState{}, tc.err
			}
			check(tc.installReason, "install", "--apply", "--plan-digest", f.plan.Digest())
			f.owner.State, f.owner.StaleVersionDetected = "stale_runtime", true
			f.r.PrepareRuntimeForUpgradeFn = prepare
			f.r.RestartStaleOwnerFn = func(context.Context, bootstrap.FirstRuntime, string, fs.FS) error {
				t.Fatal("attempted restart after lock contention")
				return nil
			}
			check(tc.repairReason, "repair", "stale-daemon", "--apply", "--owner-fingerprint", testFingerprint)
		})
	}
}

func TestHealthyAntigravityConnectionSkipsDefaultPreview(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.doc.First.Setup = false
	f.writeConfig()
	f.row.Kind, f.row.ID = "antigravity", "antigravity-0123456789abcdef01234567"
	f.row.Status = "configured_pending_new_session"
	f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
	const blocker = "active_antigravity_upgrade_requires_review"
	previews := 0
	f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/preview") {
			if req.Method != http.MethodGet {
				t.Fatal("connection preview did not use GET")
			}
			previews++
			return response(200, controlplane.ConnectionPreview{Blockers: []string{blocker}})
		}
		return nil, nil
	}
	f.command(ExitOK, "connect", "--agent", "antigravity", "--dry-run")
	if previews != 0 || f.writes != 0 {
		t.Fatalf("default healthy connection requests: previews=%d writes=%d", previews, f.writes)
	}
	for _, mode := range []string{"--dry-run", "--apply"} {
		e := f.command(ExitPreflightBlocked, "connect", "--agent", "antigravity", mode, "--reviewed-sha", testSHA)
		if !hasBlocker(e, blocker) {
			t.Fatalf("%s did not honor preview blocker: %+v", mode, e)
		}
	}
	for _, phase := range []string{"prepare", "activate"} {
		e := f.command(ExitPreflightBlocked, "connect", "--agent", "antigravity", "--phase", phase, "--dry-run")
		if !hasBlocker(e, blocker) {
			t.Fatalf("explicit phase did not honor preview blocker: %+v", e)
		}
	}
	if previews != 4 || f.writes != 0 {
		t.Fatalf("unexpected explicit phase requests: previews=%d writes=%d", previews, f.writes)
	}

	_, err := controlplane.RecordUIOperation(f.ops, controlplane.UIOperation{
		Operation: testOperation, Profile: f.row.ID, Kind: "connection_activate", Revision: f.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.ops, testOperation+".json")
	before := readTestFile(t, path)
	for _, args := range [][]string{
		{"connect", "--agent", "antigravity", "--dry-run"},
		{"connect", "--agent", "antigravity", "--apply", "--reviewed-sha", testSHA},
	} {
		e := f.command(ExitActionRequired, args...)
		if !hasBlocker(e, "pending_connection_operation") || e.OperationID != testOperation || previews != 4 || f.writes != 0 ||
			!bytes.Equal(before, readTestFile(t, path)) {
			t.Fatalf("%v bypassed or changed the pending operation: %+v", args, e)
		}
	}
}

func TestHealthyConnectionValidatesExplicitReviewedSHA(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.doc.First.Setup = false
	f.writeConfig()
	f.row.Status = "configured_pending_new_session"
	f.row.Capture = &controlplane.AgentCaptureState{State: "healthy"}
	previews := 0
	f.handler = func(req *http.Request, _ requestBody) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/preview") {
			if req.Method != http.MethodGet {
				t.Fatal("connection preview did not use GET")
			}
			previews++
		}
		return nil, nil
	}
	for _, mode := range []string{"--dry-run", "--apply"} {
		args := []string{"connect", "--agent", testAgent, mode, "--reviewed-sha"}
		e := f.command(ExitPreflightBlocked, append(slices.Clone(args), strings.Repeat("b", 64))...)
		if !hasBlocker(e, "reviewed_sha_mismatch") {
			t.Fatalf("%s accepted the stale review: %+v", mode, e)
		}
		f.command(ExitOK, append(args, testSHA)...)
	}
	if previews != 4 || f.writes != 0 {
		t.Fatalf("unexpected reviewed connection requests: previews=%d writes=%d", previews, f.writes)
	}
	if _, err := os.Lstat(f.ops); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("healthy connection created an operation journal: %v", err)
	}
}

func TestVerifyZeroDownloadsUsesCurrentGenerationEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, runtime string
		bytes         int64
		sync, zero    bool
		exit          int
	}{
		{"historical downloads accepted", "historical", 4096, true, true, ExitOK},
		{"upgrade downloaded", "historical", 4096, true, false, ExitActionRequired},
		{"current bytes", "current", 1, false, true, ExitActionRequired},
		{"current sync", "current", 0, true, true, ExitActionRequired},
		{"legacy bytes", "", 1, false, true, ExitActionRequired},
		{"legacy sync", "", 0, true, true, ExitActionRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			bundled := f.owner.RunningRuntimeHash
			hash := map[string]string{"historical": "1111111111111111", "current": bundled}[tc.runtime]
			f.worker = bootstrap.InstallWorkerState{
				State: "completed", RuntimeHash: hash, NetworkBytesDownloaded: tc.bytes, RuntimeSyncRan: tc.sync,
			}
			f.owner.UpgradeReceipt = &bootstrap.RuntimeUpgradeReceipt{
				Schema: 1, StoreID: f.doc.First.StoreID, ToRuntimeHash: bundled, OwnerFingerprint: testFingerprint, ZeroDownloads: tc.zero,
			}
			inspections := 0
			inspect := f.r.InspectOwnerStatusFn
			f.r.InspectOwnerStatusFn = func(ctx context.Context, config, root string, assets fs.FS) (bootstrap.OwnerRuntimeStatus, error) {
				inspections++
				return inspect(ctx, config, root, assets)
			}
			for _, args := range [][]string{
				{"verify", "--stage", "setup", "--expect-zero-downloads"},
				{"verify", "--expect-zero-downloads"},
			} {
				inspections = 0
				e := f.command(tc.exit, args...)
				if inspections != 1 || hasBlocker(e, "zero_downloads_not_satisfied") != (tc.exit != ExitOK) {
					t.Fatalf("zero-download verification %v inspections=%d response=%+v", args, inspections, e)
				}
			}
		})
	}
}

func TestVerifyComponentsReadsUpgradeReceiptFromDisk(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bundled := f.owner.RunningRuntimeHash
	f.worker = bootstrap.InstallWorkerState{
		State: "completed", RuntimeHash: "1111111111111111", NetworkBytesDownloaded: 4096, RuntimeSyncRan: true,
	}
	f.r.InspectOwnerStatusFn = func(context.Context, string, string, fs.FS) (bootstrap.OwnerRuntimeStatus, error) {
		t.Fatal("component verification inspected daemon")
		return bootstrap.OwnerRuntimeStatus{}, nil
	}
	path, err := bootstrap.RuntimeUpgradeReceiptPath(f.root, f.config)
	if err != nil {
		t.Fatal(err)
	}
	receipt := bootstrap.RuntimeUpgradeReceipt{
		Schema: 1, StoreID: f.doc.First.StoreID, ToRuntimeHash: bundled, OwnerFingerprint: testFingerprint, ZeroDownloads: true,
	}
	args := []string{"verify", "--stage", "components", "--expect-zero-downloads"}
	writeJSON(t, path, receipt)
	f.command(ExitOK, args...)
	receipt.ZeroDownloads = false
	writeJSON(t, path, receipt)
	e := f.command(ExitActionRequired, args...)
	if !hasBlocker(e, "zero_downloads_not_satisfied") {
		t.Fatalf("missing blocker for upgrade downloads: %+v", e)
	}
	receipt.StoreID = "other-store"
	writeJSON(t, path, receipt)
	f.command(ExitOK, args...)
	writeFile(t, path, []byte("{"))
	e = f.command(ExitNeedsReview, args...)
	if !hasBlocker(e, "upgrade_receipt_unreadable") {
		t.Fatalf("unreadable upgrade receipt did not fail closed: %+v", e)
	}
}

func TestCorruptUpgradeReceiptOnlyBlocksZeroDownloadVerification(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"fixture", "real inspector"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			receiptPath, err := bootstrap.RuntimeUpgradeReceiptPath(f.root, f.config)
			if err != nil {
				t.Fatal(err)
			}
			corrupt := []byte(`{"token":"corrupt-receipt-canary",`)
			writeFile(t, receiptPath, corrupt)
			f.owner.UpgradeReceiptError = "fixture-receipt-canary"
			if mode == "real inspector" {
				f.r.InspectOwnerStatusFn = bootstrap.InspectOwnerRuntimeStatus
				for _, tc := range []struct {
					args []string
					exit int
				}{
					{[]string{"doctor"}, ExitActionRequired},
					{[]string{"stop", "--dry-run"}, ExitOK},
					{[]string{"repair", "stale-daemon", "--dry-run"}, ExitOK},
				} {
					e := f.command(tc.exit, tc.args...)
					if e.Daemon.State != "stopped" || hasBlocker(e, "upgrade_receipt_unreadable") {
						t.Fatalf("%v did not report the stopped owner: %+v", tc.args, e)
					}
				}
			}
			for _, args := range [][]string{
				{"verify", "--stage", "setup", "--expect-zero-downloads"},
				{"verify", "--expect-zero-downloads"},
				{"verify", "--stage", "components", "--expect-zero-downloads"},
			} {
				e := f.command(ExitNeedsReview, args...)
				if !hasBlocker(e, "upgrade_receipt_unreadable") {
					t.Fatalf("%v did not reject unreadable evidence: %+v", args, e)
				}
			}

			t.Run("live owner", func(t *testing.T) {
				f.t = t
				const oldHash = "1111111111111111"
				fingerprint := testFingerprint
				f.owner.State, f.owner.RunningRuntimeHash = "stale_runtime", oldHash
				f.owner.StaleVersionDetected = true
				if mode == "real inspector" {
					pid := os.Getpid()
					started, err := supervisor.ProcessStartTime(t.Context(), pid)
					if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
						t.Skipf("process inspection unavailable in sandbox: %v", err)
					}
					if err != nil {
						t.Fatal(err)
					}
					oldRuntime := filepath.Join(filepath.Dir(receiptPath), "runtime-"+oldHash)
					if err := os.MkdirAll(oldRuntime, 0700); err != nil {
						t.Fatal(err)
					}
					lock, err := supervisor.AcquireOwner(f.store)
					if err != nil {
						t.Fatal(err)
					}
					defer lock.Release()
					defer lock.RemoveRecord()
					record := supervisor.OwnerRecord{
						PID: pid, Started: started, Phase: "ready", Config: f.config,
						OfficialData: f.store, ResourcesDir: oldRuntime, ResourcesHash: oldHash,
						ProjectControlURL: f.doc.Control.URL,
					}
					if err := lock.WriteRecord(record); err != nil {
						t.Fatal(err)
					}
					fingerprint = supervisor.OwnerFingerprint(record)
				}
				for _, tc := range []struct {
					args    []string
					blocker string
				}{
					{[]string{"doctor"}, "stale_daemon_runtime"},
					{[]string{"stop", "--dry-run"}, "daemon_stop_confirmation_required"},
					{[]string{"repair", "stale-daemon", "--dry-run"}, "stale_daemon_runtime"},
				} {
					e := f.command(ExitActionRequired, tc.args...)
					if !hasBlocker(e, tc.blocker) || hasBlocker(e, "owner_status_unavailable") ||
						e.Daemon.State != "stale_runtime" || !e.Daemon.OwnerLocked || e.Daemon.OwnerFingerprint != fingerprint ||
						e.Daemon.RunningRuntimeHash != oldHash || !e.Daemon.StaleVersionDetected {
						t.Fatalf("%v lost live status: %+v", tc.args, e)
					}
				}
				if f.writes != 0 || !bytes.Equal(corrupt, readTestFile(t, receiptPath)) {
					t.Fatal("inspection mutated the corrupt receipt or sent a mutating request")
				}
			})
		})
	}
}
