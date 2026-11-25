package setupcli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/supervisor"
)

const (
	smokeHelperEnv      = "MEMLANE_SETUPCLI_SMOKE_HELPER"
	smokeDelayEnv       = "MEMLANE_SETUPCLI_SMOKE_DELAY_MS"
	smokeReadyFlagEnv   = "MEMLANE_SETUPCLI_SMOKE_READY_FILE"
	smokeCompiledBinEnv = "MEMLANE_SETUPCLI_SMOKE_BIN"
)

var (
	compiledAppBinary   string
	compiledAppBuildErr error
)

func TestMain(m *testing.M) {
	if os.Getenv(smokeHelperEnv) == "1" {
		os.Exit(runSmokeHelperCLI(os.Args[1:]))
	}
	flag.Parse()

	// Compile cmd/memlane-app once per package test invocation so real binary tests
	// stay fast and reuse a single artifact inside a disposable temp directory.
	var tmpDir string
	if !testing.Short() {
		dir, err := os.MkdirTemp("", "memlane-setupcli-bin-")
		if err != nil {
			compiledAppBuildErr = fmt.Errorf("create temp dir for app binary: %w", err)
		} else {
			tmpDir = dir
			outBin := filepath.Join(dir, "Memlane")
			buildCmd := exec.Command("go", "build", "-o", outBin, "../../cmd/memlane-app")
			buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if out, err := buildCmd.CombinedOutput(); err == nil {
				compiledAppBinary = outBin
			} else {
				compiledAppBuildErr = fmt.Errorf("go build ../../cmd/memlane-app failed: %w (%s)", err, strings.TrimSpace(string(out)))
			}
		}
	}

	code := m.Run()
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
	}
	os.Exit(code)
}

func runSmokeHelperCLI(args []string) int {
	delay := 350 * time.Millisecond
	if raw := os.Getenv(smokeDelayEnv); raw != "" {
		if parsed, err := time.ParseDuration(raw + "ms"); err == nil && parsed > 0 {
			delay = parsed
		}
	}
	readyFile := os.Getenv(smokeReadyFlagEnv)
	assets := fstest.MapFS{"scripts/dev.py": {Data: []byte("runtime")}}
	bootstrapAssets := fstest.MapFS{"first-install-components.json": {Data: []byte("bootstrap")}}
	completePlan := bootstrap.Plan{Items: []bootstrap.Item{
		{Requirement: bootstrap.Requirement{ID: "runtime", DownloadBytes: 100}, Observation: bootstrap.Observation{Status: bootstrap.Available}},
	}}
	bundledHash, _ := bootstrap.BundledRuntimeHash(assets)

	selfExe, _ := os.Executable()
	runner := Runner{
		Executable: selfExe,
		DiskFreeFn: func(string) (uint64, error) { return 4 << 30, nil },
		CheckDNSFn: func(context.Context, string) bool { return true },
		NewEnvFn: func(_ context.Context, _ fs.FS, opts bootstrap.EnvironmentOptions) (*bootstrap.Environment, error) {
			return &bootstrap.Environment{Options: opts}, nil
		},
		DetectPlanFn: func(*bootstrap.Environment, context.Context) (bootstrap.Plan, error) {
			return completePlan, nil
		},
		// Keep LaunchWorkerFn, InspectWorkerFn, DetectOrphansFn real (Setsid + flock + worker-state.json).
		RunWorkerFn: func(ctx context.Context, env *bootstrap.Environment, _ fs.FS, cfg bootstrap.InstallWorkerConfig) error {
			// Exercise the real bootstrap install worker with a bounded slow runtime startup (0.35s).
			return runRealDetachedSlowWorker(ctx, env, cfg, delay, readyFile)
		},
		InspectOwnerStatusFn: func(_ context.Context, configPath, _ string, _ fs.FS) (bootstrap.OwnerRuntimeStatus, error) {
			if readyFile != "" {
				if _, err := os.Stat(readyFile); err == nil {
					return bootstrap.OwnerRuntimeStatus{
						State:              "ready",
						OwnerLocked:        true,
						HasReceipt:         true,
						OwnerPhase:         "ready",
						OwnerFingerprint:   strings.Repeat("e", 64),
						RunningRuntimeHash: bundledHash,
						BundledRuntimeHash: bundledHash,
					}, nil
				}
			}
			return bootstrap.OwnerRuntimeStatus{State: "stopped", BundledRuntimeHash: bundledHash}, nil
		},
		FirstRuntimeReadyFn: func(_ context.Context, _ string) (bool, error) {
			if readyFile == "" {
				return false, nil
			}
			_, err := os.Stat(readyFile)
			return err == nil, nil
		},
	}

	_, code := runner.DispatchIO(context.Background(), args, assets, bootstrapAssets, "0.1.0-beta.3", os.Stdout, os.Stderr)
	return code
}

func runRealDetachedSlowWorker(ctx context.Context, env *bootstrap.Environment, cfg bootstrap.InstallWorkerConfig, delay time.Duration, readyFile string) error {
	// Write a real worker-state.json under the real installation.lock via bootstrap.RunInstallWorkerWith
	// is tested with a fast fake runtime start; here we execute the real detached Setsid child flow.
	instDir := filepath.Join(cfg.ComponentRoot, "installation")
	if err := os.MkdirAll(instDir, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(instDir, "installation.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return bootstrap.ErrInstallWorkerBusy
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	started, err := supervisor.ProcessStartTime(ctx, os.Getpid())
	if err != nil {
		return err
	}
	statePath := filepath.Join(instDir, "worker-state.json")
	writeState := func(st bootstrap.InstallWorkerState) error {
		st.Schema = 1
		st.PID = os.Getpid()
		st.Started = started
		st.OperationID = cfg.OperationID
		st.PlanDigest = cfg.PlanDigest
		st.ConfigPath = cfg.ConfigPath
		st.StartRuntime = cfg.StartRuntime
		st.RuntimeHash = cfg.RuntimeHash
		st.HeartbeatAt = time.Now().UTC().Format(time.RFC3339Nano)
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		tmp := statePath + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, statePath)
	}

	if err := writeState(bootstrap.InstallWorkerState{State: "running", Stage: "starting_runtime"}); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
			return err
		}
	}
	return writeState(bootstrap.InstallWorkerState{State: "completed", Stage: "ready"})
}

func TestRealBinaryCompletePlanSlowStart(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "components")
	store := filepath.Join(base, "library")
	cfgPath := filepath.Join(base, "runtime.json")
	readyFile := filepath.Join(base, "runtime-ready")
	for _, d := range []string{home, root, store} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	storeID := strings.Repeat("a", 32)
	writeFile(t, filepath.Join(store, "owner.token"), []byte(strings.Repeat("k", 40)))
	writeJSON(t, filepath.Join(store, "store-identity.json"), bootstrap.StoreReceipt{
		Schema: 1, StoreID: storeID, OwnerID: "owner-1", KeyID: "key-1",
		SourceCommit: strings.Repeat("b", 40), StorageInitialized: true,
	})
	writeJSON(t, cfgPath, map[string]any{
		"first_install":   map[string]any{"schema": 1, "store_id": storeID, "setup": true},
		"official_data":   store,
		"service_commit":  strings.Repeat("b", 40),
		"token_file":      filepath.Join(store, "owner.token"),
		"generation_file": filepath.Join(store, "generation.json"),
		"generation_mode": "unconfigured",
	})

	selfExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runHelper := func(args ...string) (int, envelope) {
		t.Helper()
		cmd := exec.Command(selfExe, args...)
		cmd.Env = []string{
			"HOME=" + home,
			"TMPDIR=" + base,
			"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
			smokeHelperEnv + "=1",
			smokeDelayEnv + "=350",
			smokeReadyFlagEnv + "=" + readyFile,
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if !strings.Contains(err.Error(), "exit status") {
				t.Fatalf("exec failed: %v stderr=%q", err, stderr.String())
			}
			if ok := errorAsExit(err, &exitErr); ok {
				code = exitErr.ExitCode()
			}
		}
		if stderr.Len() != 0 {
			t.Fatalf("non-empty stderr: %q", stderr.String())
		}
		var env envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("invalid JSON stdout=%q err=%v", stdout.String(), err)
		}
		if env.ExitCode != code {
			t.Fatalf("envelope exit_code=%d != process exit=%d", env.ExitCode, code)
		}
		return code, env
	}

	scope := []string{"--config", cfgPath, "--component-root", root, "--json"}
	code, planEnv := runHelper(append([]string{"install", "--plan"}, scope...)...)
	if code != ExitActionRequired || !planEnv.Components.Complete || planEnv.Artifacts.PlanDigest == "" {
		t.Fatalf("expected complete plan needing runtime start, got code=%d env=%+v", code, planEnv)
	}

	// Launch real detached Setsid __install-worker --start-runtime with --wait 0:
	// must immediately return exit 20 with worker_active=true and starting_runtime stage.
	code, applyEnv := runHelper(append([]string{
		"install", "--apply", "--plan-digest", planEnv.Artifacts.PlanDigest, "--wait", "0",
	}, scope...)...)
	if code != ExitInProgressOrBusy || !applyEnv.Components.WorkerActive ||
		applyEnv.Components.Progress == nil || applyEnv.Components.Progress.Stage != "starting_runtime" ||
		applyEnv.OperationID != "first-install" {
		t.Fatalf("detached slow startup contract broken: code=%d env=%+v", code, applyEnv)
	}

	// Poll doctor --operation-id until the detached OS worker finishes (~350ms).
	deadline := time.Now().Add(5 * time.Second)
	var finalEnv envelope
	for time.Now().Before(deadline) {
		_, docEnv := runHelper(append([]string{"doctor", "--operation-id", applyEnv.OperationID}, scope...)...)
		finalEnv = docEnv
		if !docEnv.Components.WorkerActive && docEnv.Daemon.State == "ready" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalEnv.Components.WorkerActive || finalEnv.Daemon.State != "ready" ||
		finalEnv.Components.Progress == nil || finalEnv.Components.Progress.State != "completed" {
		t.Fatalf("detached worker did not reach completed/ready: %+v", finalEnv)
	}
}

func errorAsExit(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func TestRealCompiledAppBinaryDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real compiled app binary smoke in -short mode")
	}
	if compiledAppBuildErr != nil {
		t.Fatalf("compiled app binary build failed in TestMain: %v", compiledAppBuildErr)
	}
	if compiledAppBinary == "" {
		t.Fatal("compiled app binary path was not populated in TestMain")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	runApp := func(extraEnv []string, args ...string) (int, envelope) {
		t.Helper()
		cmd := exec.Command(compiledAppBinary, args...)
		cmd.Env = append([]string{
			"HOME=" + home,
			"TMPDIR=" + base,
			"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		}, extraEnv...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errorAsExit(err, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				t.Fatalf("exec app failed: %v", err)
			}
		}
		if stderr.Len() != 0 {
			t.Fatalf("app stderr non-empty: %q", stderr.String())
		}
		var env envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("invalid app JSON stdout=%q err=%v", stdout.String(), err)
		}
		if env.SchemaVersion != 1 || env.ExitCode != code {
			t.Fatalf("unexpected envelope header: code=%d env=%+v", code, env)
		}
		return code, env
	}

	if code, env := runApp(nil, "version", "--json"); code != ExitOK || env.Capabilities == nil {
		t.Fatalf("version failed: code=%d env=%+v", code, env)
	}
	if code, env := runApp(nil, "doctor", "--json"); code != ExitActionRequired || env.Command != "doctor" {
		t.Fatalf("cold doctor failed: code=%d env=%+v", code, env)
	}
	if code, env := runApp(nil, "install", "--plan", "--json"); code != ExitActionRequired || len(env.Artifacts.PlanDigest) != 64 {
		t.Fatalf("install --plan failed: code=%d env=%+v", code, env)
	}
	if code, env := runApp([]string{"MEMLANE_TOKEN=forbidden"}, "doctor", "--json"); code != ExitFatal || !hasBlocker(env, "secret_input_forbidden") {
		t.Fatalf("secret input guard failed: code=%d env=%+v", code, env)
	}
}
