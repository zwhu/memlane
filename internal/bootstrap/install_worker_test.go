package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/supervisor"
)

func workerTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveInstallOperationID(t *testing.T) {
	root := workerTestRoot(t)
	if got, err := ResolveInstallOperationID(root, "custom"); got != "" || !errors.Is(err, ErrOperationIDMismatch) {
		t.Fatalf("custom new operation = %q, %v", got, err)
	}
	for _, request := range []string{"", "first-install"} {
		if got, err := ResolveInstallOperationID(root, request); got != "first-install" || err != nil {
			t.Fatalf("new operation = %q, %v", got, err)
		}
	}
	installer := Installer{Root: filepath.Join(root, "installation")}
	plan := Plan{}
	if err := installer.Review("retained-op", plan, plan.Digest(), true); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"", "retained-op"} {
		if got, err := ResolveInstallOperationID(root, request); got != "retained-op" || err != nil {
			t.Fatalf("retained operation = %q, %v", got, err)
		}
	}
	if got, err := ResolveInstallOperationID(root, "first-install"); got != "" || !errors.Is(err, ErrOperationIDMismatch) {
		t.Fatalf("mismatch = %q, %v", got, err)
	}
}

func TestInstallWorkerProtectsExistingStore(t *testing.T) {
	prepared := newReadOnlyRuntimeFixture(t)
	cfg := InstallWorkerConfig{ComponentRoot: prepared.StoreRoot, ConfigPath: prepared.ConfigPath}
	err := RunInstallWorker(t.Context(), engine.BootstrapAssets, cfg)
	if err == nil || !strings.Contains(err.Error(), "组件目录必须与现有日常库分开") {
		t.Fatalf("overlapping component root = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cfg.ComponentRoot, "installation")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid component root wrote installation state: %v", err)
	}
}

func TestInstallWorkerProtectsProspectiveStore(t *testing.T) {
	root := workerTestRoot(t)
	cfg := InstallWorkerConfig{
		ComponentRoot: filepath.Join(root, "library", "components"),
		ConfigPath:    filepath.Join(root, "runtime.json"),
		OperationID:   "would-fail-if-resolved-first",
	}
	err := RunInstallWorker(t.Context(), engine.BootstrapAssets, cfg)
	if err == nil || !strings.Contains(err.Error(), "组件目录必须与现有日常库分开") {
		t.Fatalf("overlapping prospective store = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "library")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worker created a path inside prospective library: %v", err)
	}
}

func TestInstallWorkerProtectsProspectiveStoreWithProvidedEnv(t *testing.T) {
	for _, mode := range []string{"prospective store", "existing store", "invalid JSON", "symlinked config"} {
		t.Run(mode, func(t *testing.T) {
			var root, configPath string
			wantError := "组件目录必须与现有日常库分开"
			if mode == "existing store" {
				prepared := newReadOnlyRuntimeFixture(t)
				root, configPath = prepared.StoreRoot, prepared.ConfigPath
			} else {
				home := workerTestRoot(t)
				root, configPath = filepath.Join(home, "components"), filepath.Join(home, "runtime.json")
				switch mode {
				case "prospective store":
					root = filepath.Join(home, "library", "components")
				case "invalid JSON":
					if err := os.WriteFile(configPath, []byte("{"), 0600); err != nil {
						t.Fatal(err)
					}
				case "symlinked config":
					if err := os.Symlink(filepath.Join(home, "missing.json"), configPath); err != nil {
						t.Fatal(err)
					}
				}
				if mode != "prospective store" {
					_, err := loadFirstRuntimeConfig(configPath)
					if err == nil {
						t.Fatal("invalid config fixture was accepted")
					}
					wantError = err.Error()
				}
			}
			env := &Environment{Options: EnvironmentOptions{Root: root}}
			err := RunInstallWorkerWith(t.Context(), env, nil, InstallWorkerConfig{
				ConfigPath: configPath, OperationID: "would-fail-if-resolved-first",
			})
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("worker error = %v, want %q", err, wantError)
			}
			if _, err := os.Lstat(filepath.Join(root, "installation")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("worker wrote installation state: %v", err)
			}
		})
	}
}

func TestInstallWorkerMatchesScope(t *testing.T) {
	state := InstallWorkerState{
		PID: 42, State: "starting", Components: []string{"uv", "python"},
		StartRuntime: true, ConfigPath: "/store/runtime.json", OperationID: "first-install", PlanDigest: "reviewed",
		RuntimeHash: "0123456789abcdef",
	}
	for _, tc := range []struct {
		name   string
		change func(*InstallWorkerState, *InstallWorkerConfig)
		match  bool
	}{
		{"normalized match", func(*InstallWorkerState, *InstallWorkerConfig) {}, true},
		{"running", func(s *InstallWorkerState, _ *InstallWorkerConfig) { s.State = "running" }, true},
		{"optional identifiers", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.OperationID, c.PlanDigest = "", "" }, true},
		{"components", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.Components = []string{"uv"} }, false},
		{"all components", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.Components = nil }, false},
		{"start runtime", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.StartRuntime = false }, false},
		{"config path", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.ConfigPath = "/other/runtime.json" }, false},
		{"missing config path", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.ConfigPath = " " }, false},
		{"operation", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.OperationID = "other" }, false},
		{"digest", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.PlanDigest = "other" }, false},
		{"matching runtime hash", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.RuntimeHash = "0123456789abcdef" }, true},
		{"different runtime hash", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.RuntimeHash = "fedcba9876543210" }, false},
		{"empty requested runtime hash", func(_ *InstallWorkerState, c *InstallWorkerConfig) { c.RuntimeHash = "" }, true},
		{"empty recorded runtime hash", func(s *InstallWorkerState, _ *InstallWorkerConfig) { s.RuntimeHash = "" }, true},
		{"completed", func(s *InstallWorkerState, _ *InstallWorkerConfig) { s.State = "completed" }, false},
		{"invalid PID", func(s *InstallWorkerState, _ *InstallWorkerConfig) { s.PID = 1 }, false},
		{"empty receipt", func(s *InstallWorkerState, _ *InstallWorkerConfig) { *s = InstallWorkerState{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := state
			cfg := InstallWorkerConfig{
				Components: []string{" python ", "uv", "", "python"}, StartRuntime: true,
				ConfigPath: " /store/./runtime.json ", OperationID: state.OperationID, PlanDigest: state.PlanDigest,
				RuntimeHash: state.RuntimeHash,
			}
			tc.change(&s, &cfg)
			if got := s.MatchesScope(cfg); got != tc.match {
				t.Fatalf("MatchesScope = %t, want %t", got, tc.match)
			}
		})
	}
	if normalized := NormalizeWorkerComponents([]string{"", " "}); normalized != nil {
		t.Fatalf("empty normalized components = %v", normalized)
	}
}

func TestLaunchDetachedInstallWorkerRejectsIncompatibleActiveWorker(t *testing.T) {
	root := workerTestRoot(t)
	installer := Installer{Root: filepath.Join(root, "installation")}
	path, err := WorkerStatePath(root)
	if err != nil {
		t.Fatal(err)
	}
	state := InstallWorkerState{Schema: 1, PID: 42, State: "starting", Components: []string{"uv"}, StartRuntime: true,
		ConfigPath: filepath.Join(root, "runtime.json"), OperationID: "first-install", PlanDigest: "reviewed"}

	if err := installer.withLock(func() error {
		cfg := InstallWorkerConfig{ComponentRoot: root, Components: state.Components, StartRuntime: state.StartRuntime,
			ConfigPath: state.ConfigPath, OperationID: state.OperationID, PlanDigest: state.PlanDigest}
		if _, err := LaunchDetachedInstallWorker(t.Context(), "/unused", nil, cfg); !errors.Is(err, ErrInstallWorkerBusy) {
			t.Fatalf("launch before worker published state = %v", err)
		}
		writeBootstrapTestJSON(t, path, state)
		for _, tc := range []struct {
			name   string
			change func(*InstallWorkerConfig)
			want   error
		}{
			{"matching scope", func(c *InstallWorkerConfig) { c.Components = []string{" uv ", "uv"} }, nil},
			{"different components", func(c *InstallWorkerConfig) { c.Components = []string{"python"} }, ErrInstallWorkerBusy},
			{"different start runtime", func(c *InstallWorkerConfig) { c.StartRuntime = false }, ErrInstallWorkerBusy},
			{"different config path", func(c *InstallWorkerConfig) { c.ConfigPath = "/other/runtime.json" }, ErrInstallWorkerBusy},
		} {
			t.Run(tc.name, func(t *testing.T) {
				request := cfg
				tc.change(&request)
				got, err := LaunchDetachedInstallWorker(t.Context(), "/unused", nil, request)
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, state) {
					t.Fatalf("launch = %+v, %v; want %+v, %v", got, err, state, tc.want)
				}
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInstallWorkerStartsRuntimeWithCompletePlan(t *testing.T) {
	oldStartTime, oldTable := workerProcessStartTimeFn, workerProcessTableFn
	t.Cleanup(func() {
		workerProcessStartTimeFn, workerProcessTableFn = oldStartTime, oldTable
	})
	workerProcessStartTimeFn = func(context.Context, int) (string, error) { return "worker-start", nil }
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	for _, tc := range []struct {
		name, state, stage, code string
		err                      error
	}{
		{name: "ready", state: "completed", stage: "ready"},
		{name: "owner busy", state: "failed", stage: "starting_runtime", code: "owner_busy", err: supervisor.ErrOwnerBusy},
		{name: "owner locked", state: "failed", stage: "starting_runtime", code: "owner_busy", err: supervisor.ErrOwnerLocked},
		{name: "installation locked", state: "failed", stage: "starting_runtime", code: "install_worker_active", err: ErrInstallWorkerBusy},
		{name: "parent cancelled", state: "cancelled", stage: "starting_runtime", code: "cancelled", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer, driver, reqs := fixture(t)
			root := filepath.Dir(installer.Root)
			installer.Root = filepath.Join(root, "installation")
			for _, req := range reqs {
				driver.observed[req.ID] = Observation{Status: Available, Path: req.Destination}
			}
			detect := func(ctx context.Context) (Plan, error) { return installer.Detect(ctx, reqs) }
			plan, err := detect(t.Context())
			if err != nil || !plan.Complete() {
				t.Fatalf("expected a complete plan: %+v, %v", plan, err)
			}
			check := func(wantState, stage, code string) {
				t.Helper()
				state, err := ReadInstallWorkerState(root)
				if err != nil || state.State != wantState || state.Stage != stage || state.Component != "" ||
					state.ErrorCode != code || state.NetworkBytesDownloaded != 0 || state.RuntimeSyncRan || len(driver.installs) != 0 {
					t.Fatalf("worker receipt = %+v, %v, component installs = %v", state, err, driver.installs)
				}
			}
			cfg := InstallWorkerConfig{
				ComponentRoot: root, OperationID: DefaultInstallOperationID, PlanDigest: plan.Digest(), StartRuntime: true,
			}
			started := false
			err = runInstallWorker(t.Context(), cfg, installer, detect, func(context.Context) error {
				started = true
				check("running", "starting_runtime", "")
				return tc.err
			})
			if !errors.Is(err, tc.err) || !started {
				t.Fatalf("runtime start invoked = %t, worker error = %v, want %v", started, err, tc.err)
			}
			check(tc.state, tc.stage, tc.code)
		})
	}
}

func TestInstallWorkerBoundsDetachedRuntimeStart(t *testing.T) {
	oldTimeout, oldStartTime, oldTable := workerRuntimeStartTimeout, workerProcessStartTimeFn, workerProcessTableFn
	t.Cleanup(func() {
		workerRuntimeStartTimeout, workerProcessStartTimeFn, workerProcessTableFn = oldTimeout, oldStartTime, oldTable
	})
	workerRuntimeStartTimeout = 200 * time.Millisecond
	workerProcessStartTimeFn = func(context.Context, int) (string, error) { return "worker-start", nil }
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	prepared := newReadOnlyRuntimeFixture(t)
	scripts := filepath.Join(prepared.Resources, "scripts")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "dev.py"), []byte("exec /bin/sleep 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	installer, driver, reqs := fixture(t)
	root := filepath.Dir(installer.Root)
	installer.Root = filepath.Join(root, "installation")
	for _, req := range reqs {
		driver.observed[req.ID] = Observation{Status: Available, Path: req.Destination}
	}
	cfg := InstallWorkerConfig{ComponentRoot: root, OperationID: DefaultInstallOperationID, StartRuntime: true}
	done := make(chan error, 1)
	go func() {
		done <- runInstallWorker(context.Background(), cfg, installer, func(ctx context.Context) (Plan, error) {
			return installer.Detect(ctx, reqs)
		}, func(ctx context.Context) error {
			return runInstallWorkerRuntime(ctx, func(runCtx context.Context) error {
				return StartFirstRuntimeDetached(runCtx, prepared)
			})
		})
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "detached runtime startup timed out before readiness" {
			t.Fatalf("worker returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not bound detached runtime startup")
	}
	state, err := ReadInstallWorkerState(root)
	if err != nil || state.State != "failed" || state.ErrorCode != "runtime_start_failed" || state.Stage != "starting_runtime" {
		t.Fatalf("worker receipt = %+v, %v", state, err)
	}
	upgrade, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
	if err != nil {
		t.Fatalf("upgrade lock retained: %v", err)
	}
	upgrade.Release()
}

func TestInstallWorkerBoundsRuntimePreparation(t *testing.T) {
	oldStartTime, oldTable := workerProcessStartTimeFn, workerProcessTableFn
	t.Cleanup(func() { workerProcessStartTimeFn, workerProcessTableFn = oldStartTime, oldTable })
	workerProcessStartTimeFn = func(context.Context, int) (string, error) { return "worker-start", nil }
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	for _, mode := range []string{"deadline error", "killed subprocess", "parent cancellation"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				installer, driver, reqs := fixture(t)
				root := filepath.Dir(installer.Root)
				installer.Root = filepath.Join(root, "installation")
				for _, req := range reqs {
					driver.observed[req.ID] = Observation{Status: Available, Path: req.Destination}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				parent := context.Background()
				if mode == "parent cancellation" {
					parent = ctx
				}
				cfg := InstallWorkerConfig{ComponentRoot: root, OperationID: DefaultInstallOperationID, StartRuntime: true}
				err := runInstallWorker(parent, cfg, installer, func(ctx context.Context) (Plan, error) {
					return installer.Detect(ctx, reqs)
				}, func(ctx context.Context) error {
					return runInstallWorkerRuntime(ctx, func(runCtx context.Context) error {
						if deadline, ok := runCtx.Deadline(); !ok || deadline.Sub(time.Now()) != 180*time.Second {
							t.Fatalf("preparation deadline = %v, want 180 seconds", deadline)
						}
						if mode == "parent cancellation" {
							cancel()
						}
						<-runCtx.Done()
						if mode == "deadline error" {
							return runCtx.Err()
						}
						return errors.New("signal: killed")
					})
				})
				wantState, wantCode := "failed", "runtime_start_failed"
				if mode == "parent cancellation" {
					wantState, wantCode = "cancelled", "cancelled"
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("worker error = %v, want parent cancellation", err)
					}
				} else if err == nil || err.Error() != "detached runtime startup timed out before readiness" {
					t.Fatalf("worker error = %v, want runtime timeout", err)
				}
				state, err := ReadInstallWorkerState(root)
				if err != nil || state.State != wantState || state.ErrorCode != wantCode || state.Stage != "starting_runtime" {
					t.Fatalf("worker receipt = %+v, %v", state, err)
				}
			})
		})
	}
}

func TestRunInstallWorkerWithTimesOutPreparingRuntime(t *testing.T) {
	oldTimeout, oldStartTime, oldTable := workerRuntimeStartTimeout, workerProcessStartTimeFn, workerProcessTableFn
	t.Cleanup(func() {
		workerRuntimeStartTimeout, workerProcessStartTimeFn, workerProcessTableFn = oldTimeout, oldStartTime, oldTable
	})
	workerRuntimeStartTimeout = time.Second
	workerProcessStartTimeFn = func(context.Context, int) (string, error) { return "worker-start", nil }
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	prepared, env, assets := newRuntimePreparationFixture(t, `
case "$3" in
	-c) echo '{"mismatch":[]}' ;;
	*/first_install_runtime.py)
		: > preflight-started
		exec /bin/sleep 5 ;;
	*) exit 42 ;;
esac`)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	err := RunInstallWorkerWith(ctx, env, assets, InstallWorkerConfig{ConfigPath: prepared.ConfigPath, StartRuntime: true})
	if err == nil || err.Error() != "detached runtime startup timed out before readiness" {
		t.Fatalf("worker returned %v, want preparation timeout", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.StoreRoot, "preflight-started")); err != nil {
		t.Fatalf("runtime preflight did not start: %v", err)
	}
	state, err := ReadInstallWorkerState(env.Options.Root)
	if err != nil || state.State != "failed" || state.ErrorCode != "runtime_start_failed" || state.Stage != "starting_runtime" {
		t.Fatalf("worker receipt = %+v, %v", state, err)
	}
}

func TestLaunchDetachedInstallWorkerPreservesExitContention(t *testing.T) {
	oldTable := workerProcessTableFn
	t.Cleanup(func() { workerProcessTableFn = oldTable })
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	for _, tc := range []struct {
		name, exit string
		holdLock   bool
	}{
		{"held lock", "20", true},
		{"released lock", "20", false},
		{"fatal exit", "50", false},
		{"reinspect competing lock", "50", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workerTestRoot(t)
			fifo := filepath.Join(root, "exit-worker")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			done := make(chan error, 1)
			go func() {
				_, err := LaunchDetachedInstallWorker(ctx, "/bin/sh",
					[]string{"-c", `exec 3<> "$1"; read -r -t 5 line <&3; exit "$2"`, "worker", fifo, tc.exit},
					InstallWorkerConfig{ComponentRoot: root})
				done <- err
			}()
			signal := openDetachedTestSignal(t, ctx.Done(), fifo, done, ticker.C)
			if tc.holdLock || tc.exit == "20" {
				path := filepath.Join(root, "installation", "installation.lock")
				lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { lock.Close() })
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				if !tc.holdLock {
					if err := lock.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := signal.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
			if err := signal.Close(); err != nil {
				t.Fatal(err)
			}
			err := <-done
			if tc.holdLock || tc.exit == "20" {
				if !errors.Is(err, ErrInstallWorkerBusy) {
					t.Fatalf("worker exit = %v, want retryable contention", err)
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 50 {
					t.Fatalf("worker exit = %v, want exit code 50", err)
				}
			}
			if _, err := ReadInstallWorkerState(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("worker published unexpected state: %v", err)
			}
		})
	}
}

func openDetachedTestSignal(t *testing.T, deadline <-chan struct{}, fifo string, done <-chan error, ticks <-chan time.Time) *os.File {
	t.Helper()
	for {
		signal, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			t.Cleanup(func() { signal.Close() })
			return signal
		}
		if !errors.Is(err, syscall.ENXIO) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("launcher exited before opening the test FIFO: %v", err)
		case <-deadline:
			t.Fatal("launcher did not open the test FIFO")
		case <-ticks:
		}
	}
}

func TestInstallWorkerRecordsUpgradeLockedDuringRuntimeStart(t *testing.T) {
	root := workerTestRoot(t)
	installer := Installer{Root: filepath.Join(root, "installation")}
	if err := prepareInstallationDir(installer.Root); err != nil {
		t.Fatal(err)
	}
	receipt := installWorkerReceipt{path: filepath.Join(installer.Root, "worker-state.json"), state: InstallWorkerState{Schema: 1}}
	if err := receipt.fail(supervisor.ErrUpgradeLocked, "runtime_start_failed"); !errors.Is(err, supervisor.ErrUpgradeLocked) {
		t.Fatal(err)
	}
	state, err := ReadInstallWorkerState(root)
	if err != nil || state.State != "failed" || state.ErrorCode != "upgrade_locked" {
		t.Fatalf("worker receipt = %+v, %v", state, err)
	}
}

func TestInstallWorkerFailsClosedOnExistingInvalidConfig(t *testing.T) {
	for _, mode := range []string{"invalid JSON", "linked config"} {
		t.Run(mode, func(t *testing.T) {
			root := workerTestRoot(t)
			install := filepath.Join(root, "installation")
			if err := os.Mkdir(install, 0700); err != nil {
				t.Fatal(err)
			}
			files := []string{filepath.Join(install, "installation.json"), filepath.Join(install, "worker-state.json")}
			for _, path := range files {
				if err := os.WriteFile(path, []byte("retained state"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(root, "runtime.json")
			if mode == "invalid JSON" {
				if err := os.WriteFile(configPath, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(root, "missing.json"), configPath); err != nil {
				t.Fatal(err)
			}
			_, want := loadFirstRuntimeConfig(configPath)
			err := RunInstallWorker(t.Context(), engine.BootstrapAssets, InstallWorkerConfig{ComponentRoot: root, ConfigPath: configPath})
			if err == nil || want == nil || err.Error() != want.Error() {
				t.Fatalf("worker error = %v, want config error %v", err, want)
			}
			for _, path := range files {
				if got, err := os.ReadFile(path); err != nil || string(got) != "retained state" {
					t.Fatalf("retained %s = %q, %v", path, got, err)
				}
			}
		})
	}
}

func TestInstallWorkerPublishesStartingBeforeOrphanScan(t *testing.T) {
	ctx := t.Context()
	if _, err := supervisor.ProcessStartTime(ctx, os.Getpid()); errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("ps unavailable in sandbox: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	root := workerTestRoot(t)
	installer := Installer{Root: filepath.Join(root, "installation")}
	oldTable := workerProcessTableFn
	t.Cleanup(func() { workerProcessTableFn = oldTable })
	scanned := false
	const digest = "approved-plan-digest"
	const hash = "0123456789abcdef"
	configPath := filepath.Join(root, "runtime.json")
	workerProcessTableFn = func(ctx context.Context) (map[int]supervisor.ProcessInfo, error) {
		scanned = true
		got, err := ReadInstallWorkerState(root)
		if err != nil || got.State != "starting" || got.Stage != "starting" ||
			got.PID != os.Getpid() || got.OperationID != DefaultInstallOperationID || got.PlanDigest != digest ||
			!reflect.DeepEqual(got.Components, []string{"python", "uv"}) || got.ConfigPath != configPath || !got.StartRuntime || got.RuntimeHash != hash {
			t.Fatalf("worker receipt at orphan scan = %+v, %v", got, err)
		}
		if active, err := workerProcessActive(ctx, got); err != nil || !active {
			t.Fatalf("starting worker active = %t, %v", active, err)
		}
		return nil, nil
	}
	stop := errors.New("stop fixture after probing")
	detect := func(context.Context) (Plan, error) {
		got, err := ReadInstallWorkerState(root)
		if err != nil || got.State != "running" || got.Stage != "probing" {
			t.Fatalf("worker receipt at probe = %+v, %v", got, err)
		}
		return Plan{}, stop
	}
	cfg := InstallWorkerConfig{
		ComponentRoot: root, PlanDigest: digest, RuntimeHash: hash, Components: []string{"uv", "python ", "uv"},
		ConfigPath: " " + root + "/./runtime.json ", StartRuntime: true,
	}
	err := runInstallWorker(ctx, cfg, installer, detect, nil)
	if !errors.Is(err, stop) || !scanned {
		t.Fatalf("worker result = %v, orphan scan ran = %t", err, scanned)
	}
}

type fallbackProgressTestDriver struct {
	*testDriver
	checkAttempt func(string, int64)
}

func (d fallbackProgressTestDriver) Install(ctx context.Context, r Requirement) error {
	tracker := progressTracker(ctx)
	if r.ID == "python" {
		progressAttempt(ctx, "downloading", "https://first.example.test/artifact", 1, 3)
		if _, err := io.Copy(io.Discard, installProgressReader{strings.NewReader("partial"), tracker}); err != nil {
			return err
		}
	}
	progressAttempt(ctx, "downloading", "https://mirror.example.test/artifact", 2, 3)
	d.checkAttempt(r.ID, 7)
	tracker.finishTransfer(99, true)
	d.checkAttempt(r.ID, 7)
	if _, err := io.Copy(io.Discard, installProgressReader{strings.NewReader("more"), tracker}); err != nil {
		return err
	}
	return d.testDriver.Install(ctx, r)
}

func TestInstallWorkerPersistsFallbackProgress(t *testing.T) {
	const baseNetworkBytes = 4096
	installer, driver, reqs := fixture(t)
	root := filepath.Dir(installer.Root)
	installer.Root = filepath.Join(root, "installation")
	path, err := WorkerStatePath(root)
	if err != nil {
		t.Fatal(err)
	}
	oldTable := workerProcessTableFn
	t.Cleanup(func() { workerProcessTableFn = oldTable })
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	installer.Driver = fallbackProgressTestDriver{testDriver: driver, checkAttempt: func(component string, firstBytes int64) {
		got, err := ReadInstallWorkerState(root)
		wantBytes := baseNetworkBytes + firstBytes
		if component != "python" {
			wantBytes += 4
		}
		if err != nil || got.Component != component || got.Stage != "downloading" || got.FallbackAttempt != 2 ||
			got.FallbackTotal != 3 || got.NetworkBytesDownloaded != wantBytes {
			t.Fatalf("worker fallback receipt = %+v, %v", got, err)
		}
	}}
	detect := func(ctx context.Context) (Plan, error) { return installer.Detect(ctx, reqs) }
	plan, err := detect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg := InstallWorkerConfig{ComponentRoot: root, OperationID: DefaultInstallOperationID, PlanDigest: plan.Digest()}
	receipt := installWorkerReceipt{path: path, state: InstallWorkerState{
		Schema: 1, NetworkBytesDownloaded: baseNetworkBytes, RuntimeSyncRan: true,
	}}
	if err := installer.withLock(func() error {
		return installWorkerComponents(t.Context(), cfg, installer, detect, &receipt)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadInstallWorkerState(root)
	if err != nil || got.Stage != "ready" || got.FallbackAttempt != 0 || got.FallbackTotal != 0 ||
		got.PlanDigest != cfg.PlanDigest || got.NetworkBytesDownloaded != baseNetworkBytes+15 || !got.RuntimeSyncRan {
		t.Fatalf("worker receipt after component ready = %+v, %v", got, err)
	}
}

type workerDownloadTestDriver struct {
	*testDriver
	body string
}

func (d workerDownloadTestDriver) Install(ctx context.Context, r Requirement) error {
	if _, err := io.Copy(io.Discard, installProgressReader{strings.NewReader(d.body), progressTracker(ctx)}); err != nil {
		return err
	}
	return d.testDriver.Install(ctx, r)
}

func TestInstallWorkerCarriesForwardSameGenerationTelemetry(t *testing.T) {
	if _, err := supervisor.ProcessStartTime(t.Context(), os.Getpid()); errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("ps unavailable in sandbox: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	const previousHash = "0123456789abcdef"
	const previousBytes int64 = 4096
	oldTable := workerProcessTableFn
	t.Cleanup(func() { workerProcessTableFn = oldTable })
	probeErr := errors.New("probe failed before downloading")
	for _, tc := range []struct {
		name, hash, body string
		wantBaseBytes    int64
		wantSync         bool
		failProbe        bool
	}{
		{name: "cached same generation", hash: previousHash, wantBaseBytes: previousBytes, wantSync: true},
		{name: "downloads on same generation", hash: previousHash, body: "more", wantBaseBytes: previousBytes, wantSync: true},
		{name: "probe failure on same generation", hash: previousHash, wantBaseBytes: previousBytes, wantSync: true, failProbe: true},
		{name: "new generation", hash: "fedcba9876543210"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer, driver, reqs := fixture(t)
			root := filepath.Dir(installer.Root)
			installer.Root = filepath.Join(root, "installation")
			if err := prepareInstallationDir(installer.Root); err != nil {
				t.Fatal(err)
			}
			writeBootstrapTestJSON(t, filepath.Join(installer.Root, "worker-state.json"), InstallWorkerState{
				Schema: 1, State: "completed", RuntimeHash: previousHash,
				NetworkBytesDownloaded: previousBytes, RuntimeSyncRan: true,
			})
			req := reqs[0]
			if tc.body == "" {
				driver.observed[req.ID] = Observation{Status: Available, Path: req.Destination}
			}
			installer.Driver = workerDownloadTestDriver{testDriver: driver, body: tc.body}
			scanned := false
			workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) {
				scanned = true
				got, err := ReadInstallWorkerState(root)
				if err != nil || got.State != "starting" || got.RuntimeHash != tc.hash ||
					got.NetworkBytesDownloaded != tc.wantBaseBytes || got.RuntimeSyncRan != tc.wantSync {
					t.Fatalf("starting worker receipt = %+v, %v", got, err)
				}
				return nil, nil
			}
			detect := func(ctx context.Context) (Plan, error) {
				if tc.failProbe {
					return Plan{}, probeErr
				}
				return installer.Detect(ctx, []Requirement{req})
			}
			cfg := InstallWorkerConfig{ComponentRoot: root, RuntimeHash: tc.hash}
			wantState := "completed"
			var wantErr error
			if tc.failProbe {
				wantState, wantErr = "failed", probeErr
			}
			if err := runInstallWorker(t.Context(), cfg, installer, detect, nil); !errors.Is(err, wantErr) || !scanned {
				t.Fatalf("worker result = %v, want %v, orphan scan ran = %t", err, wantErr, scanned)
			}
			got, err := ReadInstallWorkerState(root)
			wantBytes := tc.wantBaseBytes + int64(len(tc.body))
			if err != nil || got.State != wantState || got.RuntimeHash != tc.hash ||
				got.NetworkBytesDownloaded != wantBytes || got.RuntimeSyncRan != tc.wantSync {
				t.Fatalf("worker receipt = %+v, %v, want state=%s bytes=%d sync=%t", got, err, wantState, wantBytes, tc.wantSync)
			}
		})
	}
}

func TestInstallWorkerPersistsRuntimeSyncOnFailure(t *testing.T) {
	root := workerTestRoot(t)
	driver := runtimeSyncTestDriver(t, "exit 19")
	installer := Installer{Root: filepath.Join(root, "installation"), Driver: driver}
	reqs := []Requirement{{
		ID: "runtime", Name: "Runtime", Purpose: "Dependencies", Version: "fixed", Source: "https://example.test/lock",
		Digest: driver.Digest(), Destination: filepath.Join(root, "runtime"),
	}}
	oldTable := workerProcessTableFn
	t.Cleanup(func() { workerProcessTableFn = oldTable })
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) { return nil, nil }
	path, err := WorkerStatePath(root)
	if err != nil {
		t.Fatal(err)
	}
	receipt := installWorkerReceipt{path: path, state: InstallWorkerState{Schema: 1}}
	detect := func(ctx context.Context) (Plan, error) { return installer.Detect(ctx, reqs) }
	cfg := InstallWorkerConfig{ComponentRoot: root, OperationID: DefaultInstallOperationID}
	err = installer.withLock(func() error {
		return installWorkerComponents(t.Context(), cfg, installer, detect, &receipt)
	})
	var failure *CommandFailure
	if !errors.As(err, &failure) || failure.ExitCode != 19 {
		t.Fatalf("install error = %v, want fake sync command failure", err)
	}
	state, err := ReadInstallWorkerState(root)
	if err != nil || !state.RuntimeSyncRan || state.NetworkBytesDownloaded != 0 || state.Detail != "uv pip sync" {
		t.Fatalf("sync failure receipt = %+v, %v", state, err)
	}
}

func TestDetectOrphanInstallProcesses(t *testing.T) {
	parent := workerTestRoot(t)
	root := filepath.Join(parent, "components")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	args := map[int][]string{
		10001:       {"/opt/bin/uv", "venv", filepath.Join(root, "future-venv")},
		10002:       {"uv", "venv", filepath.Join(root+"-unrelated", "venv")},
		10003:       {"runner", "uv", "pip", "sync", "--python=" + filepath.Join(alias, "venv/bin/python")},
		10004:       {"python", filepath.Join(root, "uv")},
		10005:       {filepath.Join(root, "bin/uv"), "venv", filepath.Join(parent, "other")},
		os.Getpid(): {"uv", root},
	}
	oldTable, oldArgs := workerProcessTableFn, workerProcessArgsFn
	t.Cleanup(func() { workerProcessTableFn, workerProcessArgsFn = oldTable, oldArgs })
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) {
		table := make(map[int]supervisor.ProcessInfo)
		for pid := range args {
			table[pid] = supervisor.ProcessInfo{PID: pid}
		}
		return table, nil
	}
	workerProcessArgsFn = func(pid int) ([]string, error) { return args[pid], nil }
	pids, err := DetectOrphanInstallProcesses(t.Context(), root)
	if err != nil || !reflect.DeepEqual(pids, []int{10001, 10003}) {
		t.Fatalf("orphan processes = %v, %v", pids, err)
	}
	state, err := LaunchDetachedInstallWorker(t.Context(), "/unused", nil, InstallWorkerConfig{ComponentRoot: root})
	if !errors.Is(err, ErrOrphanInstallProcess) || state.State != "needs_review" || state.ErrorCode != "orphan_install_process" {
		t.Fatalf("orphan launch = %+v, %v", state, err)
	}
}

func TestInspectInstallWorkerReadOnly(t *testing.T) {
	root := workerTestRoot(t)
	state, active, err := InspectInstallWorker(t.Context(), root)
	if err != nil || active || !reflect.DeepEqual(state, InstallWorkerState{}) {
		t.Fatalf("inspect empty root = %+v, %t, %v", state, active, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("inspect created files: %v, %v", entries, err)
	}
}

func waitForWorkerTestState(t *testing.T, root string, want func(InstallWorkerState) bool) InstallWorkerState {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		state, err := ReadInstallWorkerState(root)
		if err == nil && want(state) {
			return state
		}
		if time.Now().After(deadline) || state.State == "failed" || state.State == "needs_review" || state.State == "cancelled" {
			log, _ := os.ReadFile(filepath.Join(root, "installation", "worker.log"))
			t.Fatalf("unexpected worker state: %+v, %v\n%s", state, err, log)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInstallWorkerSurvivesParentSIGKILL(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestInstallWorkerSurvivesParentSIGKILL$"}
	root := os.Getenv("MEMLANE_TEST_WORKER_ROOT")
	cfg := InstallWorkerConfig{ComponentRoot: root, Components: []string{"cognee-source"}}
	switch os.Getenv("MEMLANE_TEST_WORKER_ROLE") {
	case "parent":
		t.Setenv("MEMLANE_TEST_WORKER_ROLE", "child")
		state, err := LaunchDetachedInstallWorker(t.Context(), executable, args, cfg)
		if err != nil || state.PID <= 1 {
			t.Fatalf("launch worker = %+v, %v", state, err)
		}
		// Wait to be killed by the top-level test.
		time.Sleep(15 * time.Second)
		t.Fatal("parent was not killed")
	case "child":
		runSlowWorkerFixture(t, cfg)
		return
	}
	if _, err := supervisor.ProcessStartTime(t.Context(), os.Getpid()); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("Seatbelt restricts ps: %v", err)
		}
		t.Fatal(err)
	}
	root = workerTestRoot(t)
	parent := exec.Command(executable, args...)
	parent.Env = append(os.Environ(), "MEMLANE_TEST_WORKER_ROLE=parent", "MEMLANE_TEST_WORKER_ROOT="+root)
	var output bytes.Buffer
	parent.Stdout, parent.Stderr = &output, &output
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- parent.Wait() }()
	t.Cleanup(func() {
		_ = parent.Process.Kill()
		// Release the child if an assertion failed before the normal handshake.
		_ = os.WriteFile(filepath.Join(root, "continue"), nil, 0600)
	})
	running := waitForWorkerTestState(t, root, func(s InstallWorkerState) bool {
		return s.State == "running" && s.Stage == "downloading" && s.BytesDone > 0
	})
	if running.PID == parent.Process.Pid {
		t.Fatalf("launcher published its PID as the worker: %+v", running)
	}
	if err := parent.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var killed *exec.ExitError
	if err := <-exited; !errors.As(err, &killed) || killed.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("parent exit = %v, output = %s", err, output.String())
	}
	lock := filepath.Join(root, "installation", "installation.lock")
	if locked, _, err := supervisor.ProbeFileLockedReadOnly(lock); !locked || err != nil {
		t.Fatalf("lock after parent SIGKILL = %t, %v", locked, err)
	}
	attached, active, err := InspectInstallWorker(t.Context(), root)
	if err != nil || !active || attached.PID != running.PID {
		t.Fatalf("inspect detached worker = %+v, %t, %v", attached, active, err)
	}
	if err := os.WriteFile(filepath.Join(root, "continue"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	completed := waitForWorkerTestState(t, root, func(s InstallWorkerState) bool { return s.State == "completed" })
	if completed.PID != running.PID || completed.OperationID != "first-install" || completed.Stage != "ready" || completed.SafeMessage != "" || completed.ErrorCode != "" {
		t.Fatalf("unexpected completed receipt: %+v", completed)
	}
	path, err := WorkerStatePath(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Contains(raw, []byte("worker-fixture-secret")) {
		t.Fatalf("invalid or sensitive worker state: %s, %v", raw, err)
	}
	if locked, exists, err := supervisor.ProbeFileLockedReadOnly(lock); locked || !exists || err != nil {
		t.Fatalf("lock after completion = %t, %t, %v", locked, exists, err)
	}
	j, err := (Installer{Root: filepath.Join(root, "installation")}).Snapshot()
	if err != nil || j.ApprovedDigest != j.Plan.Digest() || len(j.Steps) != 1 || j.Steps["cognee-source"].Status != Ready {
		t.Fatalf("incorrect filtered consent or journal: %+v, %v", j, err)
	}
}

func runSlowWorkerFixture(t *testing.T, cfg InstallWorkerConfig) {
	t.Helper()
	root := cfg.ComponentRoot
	archive, _, digest := sourceFixture(t)
	content, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	source := SourceDriver{Spec: SourceSpec{
		Commit: "fixture", URL: "https://fixture.example/tar?token=worker-fixture-secret",
		SHA256: digest, Packages: []string{"cognee"},
	}}
	runtime := RuntimeDriver{}
	env := &Environment{
		Options: EnvironmentOptions{Root: root},
		Runtime: runtime,
		Source:  sourceLocations{Managed: source},
		requirements: []Requirement{
			{
				ID: "runtime", Name: "Runtime", Purpose: "Provide engine dependencies",
				Version: "requirements.lock", Source: "https://fixture.example/runtime",
				Digest: runtime.Digest(), Destination: filepath.Join(root, "runtime"),
			},
			{
				ID: "cognee-source", Name: "Cognee", Purpose: "Provide engine source",
				Version: source.Spec.Commit, Source: source.Spec.URL,
				Digest: digest, Destination: filepath.Join(root, "source"),
			},
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	old := artifactHTTPClientFactory
	t.Cleanup(func() { artifactHTTPClientFactory = old })
	artifactHTTPClientFactory = func(context.Context) *http.Client {
		return &http.Client{Transport: progressTestTransport(func(*http.Request) (*http.Response, error) {
			r := bytes.NewReader(content)
			body := progressTestReader(func(b []byte) (int, error) {
				if r.Len() == len(content) {
					time.Sleep(525 * time.Millisecond)
					return r.Read(b[:1])
				}
				if r.Len() == 0 {
					return 0, io.EOF
				}
				deadline := time.Now().Add(275 * time.Millisecond)
				for {
					_, err := os.Stat(filepath.Join(root, "continue"))
					if err == nil && time.Now().After(deadline) {
						return r.Read(b)
					}
					select {
					case <-ctx.Done():
						return 0, ctx.Err()
					case <-time.After(10 * time.Millisecond):
					}
				}
			})
			return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(content)), Body: io.NopCloser(body)}, nil
		})}
	}
	if err := RunInstallWorkerWith(ctx, env, nil, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestInstallWorkerComponentsApproveFilteredPlanUnderLock(t *testing.T) {
	i, d, reqs := fixture(t)
	ctx := t.Context()
	detect := func(ctx context.Context) (Plan, error) { return i.Detect(ctx, reqs) }
	full, err := detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(i.Root)
	i.Root = filepath.Join(root, "installation")
	path, err := WorkerStatePath(root)
	if err != nil {
		t.Fatal(err)
	}
	oldTable := workerProcessTableFn
	t.Cleanup(func() { workerProcessTableFn = oldTable })
	workerProcessTableFn = func(context.Context) (map[int]supervisor.ProcessInfo, error) {
		locked, _, err := supervisor.ProbeFileLockedReadOnly(filepath.Join(i.Root, "installation.lock"))
		if !locked || err != nil {
			t.Fatalf("orphan scan ran without the installation lock: %t, %v", locked, err)
		}
		return nil, nil
	}
	i.OnChange = func(Journal) {
		locked, _, err := supervisor.ProbeFileLockedReadOnly(filepath.Join(i.Root, "installation.lock"))
		if !locked || err != nil {
			t.Fatalf("journal write ran without the installation lock: %t, %v", locked, err)
		}
	}
	receipt := installWorkerReceipt{path: path, state: InstallWorkerState{Schema: 1}}
	cfg := InstallWorkerConfig{ComponentRoot: root, OperationID: "first-install", PlanDigest: "outdated", Components: []string{"cognee"}}
	run := func() error {
		return i.withLock(func() error {
			return installWorkerComponents(ctx, cfg, i, detect, &receipt)
		})
	}
	if err := run(); !errors.Is(err, ErrPlanChanged) || len(d.installs) != 0 {
		t.Fatalf("stale plan was installed: %v, %v", err, d.installs)
	}
	if _, err := i.Snapshot(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale plan changed the journal: %v", err)
	}
	cfg.PlanDigest = full.Digest()
	if err := run(); err != nil {
		t.Fatal(err)
	}
	j, err := i.Snapshot()
	if err != nil || j.ApprovedDigest != j.Plan.Digest() || j.ApprovedDigest == full.Digest() || !reflect.DeepEqual(d.installs, []string{"cognee"}) {
		t.Fatalf("unexpected filtered installation: %+v, %v, %v", j, d.installs, err)
	}
}
