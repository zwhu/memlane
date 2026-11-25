package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/supervisor"
)

const DefaultInstallOperationID = "first-install"

// Kept in sync with setupcli.ExitInProgressOrBusy, the detached worker CLI exit code.
const installWorkerBusyExitCode = 20

var workerRuntimeStartTimeout = 180 * time.Second

var (
	ErrOrphanInstallProcess = errors.New("orphan install process detected targeting component root")
	ErrInstallWorkerBusy    = errors.New("install worker already active")
	ErrOperationIDMismatch  = errors.New("requested operation id differs from retained installation operation")
)

type InstallWorkerState struct {
	Schema                 int      `json:"schema"`
	PID                    int      `json:"pid"`
	Started                string   `json:"started"`
	OperationID            string   `json:"operation_id"`
	PlanDigest             string   `json:"plan_digest,omitempty"`
	Components             []string `json:"components,omitempty"`
	ConfigPath             string   `json:"config_path,omitempty"`
	StartRuntime           bool     `json:"start_runtime,omitempty"`
	RuntimeHash            string   `json:"runtime_hash,omitempty"`
	RuntimeSyncRan         bool     `json:"runtime_sync_ran,omitempty"`
	DownloadSource         string   `json:"download_source,omitempty"`
	Component              string   `json:"component,omitempty"`
	Stage                  string   `json:"stage,omitempty"`
	Detail                 string   `json:"detail,omitempty"`
	BytesDone              int64    `json:"bytes_done"`
	BytesTotal             int64    `json:"bytes_total"`
	NetworkBytesDownloaded int64    `json:"network_bytes_downloaded,omitempty"`
	Indeterminate          bool     `json:"indeterminate,omitempty"`
	BytesPerSec            float64  `json:"bytes_per_sec,omitempty"`
	Mirror                 string   `json:"mirror,omitempty"`
	FallbackAttempt        int      `json:"fallback_attempt,omitempty"`
	FallbackTotal          int      `json:"fallback_total,omitempty"`
	ElapsedMs              int64    `json:"elapsed_ms,omitempty"`
	HeartbeatAt            string   `json:"heartbeat_at"`
	State                  string   `json:"state"`
	ErrorCode              string   `json:"error_code,omitempty"`
	SafeMessage            string   `json:"safe_message,omitempty"`
}

type InstallWorkerConfig struct {
	ComponentRoot  string
	ConfigPath     string
	DownloadSource string
	OperationID    string
	PlanDigest     string
	Components     []string
	StartRuntime   bool
	RuntimeHash    string
}

// NormalizeWorkerComponents returns a sorted, unique copy of the non-empty component IDs.
func NormalizeWorkerComponents(components []string) []string {
	var normalized []string
	for _, component := range components {
		if id := strings.TrimSpace(component); id != "" {
			normalized = append(normalized, id)
		}
	}
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

func cleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// MatchesScope reports whether this active worker can serve the requested installation.
func (s InstallWorkerState) MatchesScope(cfg InstallWorkerConfig) bool {
	if s.PID <= 1 || (s.State != "starting" && s.State != "running") {
		return false
	}
	if (cfg.OperationID != "" && s.OperationID != cfg.OperationID) ||
		(cfg.PlanDigest != "" && s.PlanDigest != cfg.PlanDigest) {
		return false
	}
	if cfg.RuntimeHash != "" && s.RuntimeHash != "" && s.RuntimeHash != cfg.RuntimeHash {
		return false
	}
	return slices.Equal(NormalizeWorkerComponents(s.Components), NormalizeWorkerComponents(cfg.Components)) &&
		s.StartRuntime == cfg.StartRuntime && cleanOptionalPath(s.ConfigPath) == cleanOptionalPath(cfg.ConfigPath)
}

func WorkerStatePath(componentRoot string) (string, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "installation", "worker-state.json"), nil
}

func ReadInstallWorkerState(componentRoot string) (InstallWorkerState, error) {
	var state InstallWorkerState
	path, err := WorkerStatePath(componentRoot)
	if err != nil {
		return state, err
	}
	b, err := privateRead(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return InstallWorkerState{}, err
	}
	if state.Schema != 1 {
		return InstallWorkerState{}, errors.New("unsupported install worker state")
	}
	return state, nil
}

func ResolveInstallOperationID(componentRoot, requested string) (string, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return "", err
	}
	return resolveInstallOperationID(Installer{Root: filepath.Join(root, "installation")}, requested)
}

func resolveInstallOperationID(installer Installer, requested string) (string, error) {
	j, err := installer.read()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if j.Operation != "" {
		if requested != "" && requested != j.Operation {
			return "", ErrOperationIDMismatch
		}
		return j.Operation, nil
	}
	if requested == "" || requested == DefaultInstallOperationID {
		return DefaultInstallOperationID, nil
	}
	return "", ErrOperationIDMismatch
}

var (
	workerProcessStartTimeFn = supervisor.ProcessStartTime
	workerProcessTableFn     = supervisor.ProcessTable
	workerProcessArgsFn      = supervisor.ProcessArgs
)

// resolveWorkerPath resolves existing ancestors too, since a uv target may
// name a file or virtual environment that uv has yet to create.
func resolveWorkerPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolveWorkerPath(parent), filepath.Base(path))
}

func DetectOrphanInstallProcesses(ctx context.Context, componentRoot string) ([]int, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return nil, err
	}
	root = resolveWorkerPath(root)
	table, err := workerProcessTableFn(ctx)
	if err != nil {
		return nil, err
	}
	var pids []int
	for pid := range table {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid <= 1 || pid == os.Getpid() {
			continue
		}
		args, err := workerProcessArgsFn(pid)
		// Processes may disappear or deny argv access to the current user.
		if err != nil || len(args) == 0 {
			continue
		}
		if filepath.Base(args[0]) != "uv" && (len(args) < 2 || args[1] != "uv") {
			continue
		}
		// The uv binary may live in the root while working on another project.
		for _, arg := range args[1:] {
			path := arg
			if strings.HasPrefix(arg, "--") {
				_, path, _ = strings.Cut(arg, "=")
			}
			if !filepath.IsAbs(path) {
				continue
			}
			rel, err := filepath.Rel(root, resolveWorkerPath(filepath.Clean(path)))
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				pids = append(pids, pid)
				break
			}
		}
	}
	slices.Sort(pids)
	return pids, nil
}

func workerProcessActive(ctx context.Context, state InstallWorkerState) (bool, error) {
	if (state.State != "running" && state.State != "starting") || state.PID <= 1 {
		return false, nil
	}
	active, err := supervisor.ProcessStartMatches(ctx, state.PID, state.Started)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var exit *exec.ExitError
	// ps exits with status 1 if the process no longer exists.
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return active, err
}

func InspectInstallWorker(ctx context.Context, componentRoot string) (InstallWorkerState, bool, error) {
	path, err := WorkerStatePath(componentRoot)
	if err != nil {
		return InstallWorkerState{}, false, err
	}
	locked, _, err := supervisor.ProbeFileLockedReadOnly(filepath.Join(filepath.Dir(path), "installation.lock"))
	if err != nil {
		return InstallWorkerState{}, false, err
	}
	state, err := ReadInstallWorkerState(componentRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return state, locked, err
	}
	if locked {
		return state, true, nil
	}
	active, err := workerProcessActive(ctx, state)
	return state, active, err
}

func LaunchDetachedInstallWorker(ctx context.Context, executable string, workerArgs []string, cfg InstallWorkerConfig) (InstallWorkerState, error) {
	inspect := func(ctx context.Context) (InstallWorkerState, bool, error) {
		state, active, err := InspectInstallWorker(ctx, cfg.ComponentRoot)
		if active && !state.MatchesScope(cfg) {
			return state, true, ErrInstallWorkerBusy
		}
		return state, active, err
	}
	state, active, err := inspect(ctx)
	if err != nil || active {
		return state, err
	}
	pids, err := DetectOrphanInstallProcesses(ctx, cfg.ComponentRoot)
	if err != nil {
		return InstallWorkerState{}, err
	}
	if len(pids) > 0 {
		return InstallWorkerState{State: "needs_review", ErrorCode: "orphan_install_process"}, ErrOrphanInstallProcess
	}
	path, err := WorkerStatePath(cfg.ComponentRoot)
	if err != nil {
		return InstallWorkerState{}, err
	}
	dir := filepath.Dir(path)
	if err := prepareInstallationDir(dir); err != nil {
		return InstallWorkerState{}, err
	}
	launchLock, err := os.OpenFile(filepath.Join(dir, "worker-launch.lock"),
		os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return InstallWorkerState{}, err
	}
	defer launchLock.Close()
	lockInfo, err := launchLock.Stat()
	if err != nil {
		return InstallWorkerState{}, err
	}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0600 {
		return InstallWorkerState{}, errors.New("worker launch lock must be a private regular file")
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "worker.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return InstallWorkerState{}, err
	}
	defer logFile.Close()
	info, err := logFile.Stat()
	if err != nil {
		return InstallWorkerState{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return InstallWorkerState{}, errors.New("worker log must be a private regular file")
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	// Keep the launch flock on a descriptor that closes on exec. The worker
	// inherits the log descriptors and acquires its own installation lock.
	for {
		err := syscall.Flock(int(launchLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return InstallWorkerState{}, err
		}
		select {
		case <-waitCtx.Done():
			return InstallWorkerState{}, waitCtx.Err()
		case <-ticker.C:
			state, active, err = inspect(waitCtx)
			if err != nil || active {
				return state, err
			}
		}
	}
	defer syscall.Flock(int(launchLock.Fd()), syscall.LOCK_UN)
	state, active, err = inspect(waitCtx)
	if err != nil || active {
		return state, err
	}
	if err := waitCtx.Err(); err != nil {
		return InstallWorkerState{}, err
	}

	cmd := exec.Command(executable, workerArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return InstallWorkerState{}, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for {
		state, err = ReadInstallWorkerState(cfg.ComponentRoot)
		if err == nil && state.PID == cmd.Process.Pid {
			return state, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return InstallWorkerState{}, err
		}
		select {
		case <-waitCtx.Done():
			return InstallWorkerState{}, waitCtx.Err()
		case exitErr := <-exited:
			state, err = ReadInstallWorkerState(cfg.ComponentRoot)
			if err == nil && state.PID == cmd.Process.Pid {
				return state, nil
			}
			live, active, inspectErr := inspect(waitCtx)
			if inspectErr != nil || active {
				return live, inspectErr
			}
			var processErr *exec.ExitError
			if errors.As(exitErr, &processErr) && processErr.ExitCode() == installWorkerBusyExitCode {
				return InstallWorkerState{}, ErrInstallWorkerBusy
			}
			if exitErr != nil {
				return InstallWorkerState{}, exitErr
			}
			return InstallWorkerState{}, errors.New("install worker exited before publishing state")
		case <-ticker.C:
		}
	}
}

func RunInstallWorker(ctx context.Context, assets fs.FS, cfg InstallWorkerConfig) error {
	return RunInstallWorkerWith(ctx, nil, assets, cfg)
}

func RunInstallWorkerWith(ctx context.Context, env *Environment, assets fs.FS, cfg InstallWorkerConfig) error {
	if cfg.ComponentRoot == "" && env != nil {
		cfg.ComponentRoot = env.Options.Root
	}
	root, err := ComponentRoot(cfg.ComponentRoot)
	if err != nil {
		return err
	}
	cfg.ComponentRoot = root
	cfg.ConfigPath = cleanOptionalPath(cfg.ConfigPath)
	var protectedStore string
	if cfg.ConfigPath != "" {
		_, err := os.Lstat(cfg.ConfigPath)
		if err == nil {
			config, err := loadFirstRuntimeConfig(cfg.ConfigPath)
			if err != nil {
				return err
			}
			protectedStore = config.Root
			if err := validateComponentLocation(EnvironmentOptions{Root: root, ProtectedStore: protectedStore}); err != nil {
				return err
			}
		} else if errors.Is(err, os.ErrNotExist) {
			prospectiveStore := filepath.Join(filepath.Dir(cfg.ConfigPath), "library")
			if err := validateComponentLocation(EnvironmentOptions{Root: root, ProtectedStore: prospectiveStore}); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	operation, err := ResolveInstallOperationID(root, cfg.OperationID)
	if err != nil {
		return err
	}
	cfg.OperationID = operation
	if cfg.RuntimeHash == "" && assets != nil {
		cfg.RuntimeHash, err = BundledRuntimeHash(assets)
		if err != nil {
			return err
		}
	}
	if env == nil {
		env, err = NewEnvironment(ctx, assets, EnvironmentOptions{
			Root: root, DownloadSource: cfg.DownloadSource, ProtectedStore: protectedStore,
		})
		if err != nil {
			return err
		}
	}
	installer := env.Installer()
	if filepath.Clean(installer.Root) != filepath.Join(root, "installation") {
		return errors.New("install worker environment differs from component root")
	}
	var startRuntime func(context.Context) error
	if cfg.StartRuntime && cfg.ConfigPath != "" {
		startRuntime = func(ctx context.Context) error {
			return runInstallWorkerRuntime(ctx, func(runCtx context.Context) error {
				prepared, err := env.PrepareFirstRuntime(runCtx, cfg.ConfigPath, assets)
				if err != nil {
					return err
				}
				return StartFirstRuntimeDetached(runCtx, prepared)
			})
		}
	}
	return runInstallWorker(ctx, cfg, installer, env.Detect, startRuntime)
}

func runInstallWorkerRuntime(ctx context.Context, run func(context.Context) error) error {
	runCtx, cancel := context.WithTimeout(ctx, workerRuntimeStartTimeout)
	defer cancel()
	err := run(runCtx)
	if err != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return errors.New("detached runtime startup timed out before readiness")
	}
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type installWorkerReceipt struct {
	mu    sync.Mutex
	path  string
	state InstallWorkerState
}

func (w *installWorkerReceipt) writeWorkerState(update func(*InstallWorkerState)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	update(&w.state)
	w.state.HeartbeatAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.state.SafeMessage = RedactInstallerText(w.state.SafeMessage)
	b, err := json.Marshal(w.state)
	if err != nil {
		return err
	}
	return atomicPrivateFile(w.path, b)
}

func (w *installWorkerReceipt) fail(err error, code string) error {
	state, message := "failed", "Component installation failed."
	switch {
	case errors.Is(err, supervisor.ErrUpgradeLocked):
		state, code, message = "failed", "upgrade_locked", "Runtime upgrade is in progress; retry shortly."
	case errors.Is(err, supervisor.ErrOwnerBusy), errors.Is(err, supervisor.ErrOwnerLocked):
		state, code, message = "failed", "owner_busy", "Runtime owner lock is busy; retry shortly."
	case errors.Is(err, ErrInstallWorkerBusy):
		state, code, message = "failed", "install_worker_active", "Another component installation is in progress; retry shortly."
	case errors.Is(err, ErrOrphanInstallProcess):
		state, code, message = "needs_review", "orphan_install_process", ErrOrphanInstallProcess.Error()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		state, code, message = "cancelled", "cancelled", "Installation cancelled."
	case code == "runtime_start_failed":
		message = "Installed components could not start the runtime."
	case errors.Is(err, ErrPlanChanged):
		code, message = "plan_changed", ErrPlanChanged.Error()
	case errors.Is(err, ErrConsent):
		code, message = "consent_required", ErrConsent.Error()
	case errors.Is(err, ErrOperationIDMismatch):
		code, message = "operation_id_mismatch", ErrOperationIDMismatch.Error()
	}
	writeErr := w.writeWorkerState(func(s *InstallWorkerState) {
		s.State, s.ErrorCode, s.SafeMessage = state, code, message
	})
	if writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}

func runInstallWorker(ctx context.Context, cfg InstallWorkerConfig, installer Installer, detect func(context.Context) (Plan, error), startRuntime func(context.Context) error) error {
	cfg.Components = NormalizeWorkerComponents(cfg.Components)
	cfg.ConfigPath = cleanOptionalPath(cfg.ConfigPath)
	started, err := workerProcessStartTimeFn(ctx, os.Getpid())
	if err != nil {
		return err
	}
	w := installWorkerReceipt{
		path: filepath.Join(installer.Root, "worker-state.json"),
		state: InstallWorkerState{
			Schema: 1, PID: os.Getpid(), Started: started, OperationID: cfg.OperationID,
			PlanDigest: cfg.PlanDigest, DownloadSource: NormalizeDownloadSource(cfg.DownloadSource),
			Components: cfg.Components, ConfigPath: cfg.ConfigPath, StartRuntime: cfg.StartRuntime, RuntimeHash: cfg.RuntimeHash,
		},
	}
	err = installer.withLock(func() error {
		previous, readErr := ReadInstallWorkerState(cfg.ComponentRoot)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		active, err := workerProcessActive(ctx, previous)
		if err != nil {
			return err
		}
		if active {
			return ErrInstallWorkerBusy
		}
		if readErr == nil && cfg.RuntimeHash != "" && previous.RuntimeHash == cfg.RuntimeHash {
			w.state.NetworkBytesDownloaded = previous.NetworkBytesDownloaded
			w.state.RuntimeSyncRan = previous.RuntimeSyncRan
		}
		// Revalidate the operation against the journal under the installation lock.
		operation, err := resolveInstallOperationID(installer, cfg.OperationID)
		if err != nil {
			return w.fail(err, "install_failed")
		}
		cfg.OperationID = operation
		if err := w.writeWorkerState(func(s *InstallWorkerState) {
			s.OperationID = operation
			s.State, s.Stage = "starting", "starting"
		}); err != nil {
			return err
		}
		if err := installWorkerComponents(ctx, cfg, installer, detect, &w); err != nil {
			return w.fail(err, "install_failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if startRuntime != nil {
		plan, err := detect(ctx)
		if err == nil && !plan.Complete() {
			err = errors.New("full installation plan is incomplete")
		}
		if err != nil {
			return w.fail(err, "runtime_start_failed")
		}
		if err := w.writeWorkerState(func(s *InstallWorkerState) { s.Component, s.Stage = "", "starting_runtime" }); err != nil {
			return w.fail(err, "runtime_start_failed")
		}
		if err := startRuntime(ctx); err != nil {
			return w.fail(err, "runtime_start_failed")
		}
	}
	return w.writeWorkerState(func(s *InstallWorkerState) {
		s.State, s.Stage = "completed", "ready"
		s.ErrorCode, s.SafeMessage = "", ""
	})
}

func installWorkerComponents(ctx context.Context, cfg InstallWorkerConfig, installer Installer, detect func(context.Context) (Plan, error), w *installWorkerReceipt) error {
	pids, err := DetectOrphanInstallProcesses(ctx, cfg.ComponentRoot)
	if err != nil {
		return err
	}
	if len(pids) > 0 {
		return ErrOrphanInstallProcess
	}
	if err := w.writeWorkerState(func(s *InstallWorkerState) { s.State, s.Stage = "running", "probing" }); err != nil {
		return err
	}
	full, err := detect(ctx)
	if err != nil {
		return err
	}
	digest := full.Digest()
	if err := w.writeWorkerState(func(s *InstallWorkerState) { s.PlanDigest = digest }); err != nil {
		return err
	}
	if cfg.PlanDigest != "" && digest != cfg.PlanDigest {
		return ErrPlanChanged
	}
	runPlan := full
	if len(cfg.Components) > 0 {
		runPlan.Items = nil
		for _, item := range full.Items {
			if slices.Contains(cfg.Components, item.Requirement.ID) {
				runPlan.Items = append(runPlan.Items, item)
			}
		}
		for _, id := range cfg.Components {
			if !slices.ContainsFunc(runPlan.Items, func(item Item) bool { return item.Requirement.ID == id }) {
				return fmt.Errorf("%w: requested component is absent from the plan", ErrPlanChanged)
			}
		}
	}
	progressCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	baseNetworkBytes := w.state.NetworkBytesDownloaded
	runCtx := WithInstallProgressInterval(progressCtx, 500*time.Millisecond, func(p InstallProgress) {
		err := w.writeWorkerState(func(s *InstallWorkerState) {
			s.Component, s.Stage, s.Detail, s.Mirror = p.Component, p.Stage, p.Detail, p.Mirror
			s.BytesDone, s.BytesTotal, s.Indeterminate = p.BytesDone, p.BytesTotal, p.Indeterminate
			if totalNetworkBytes := baseNetworkBytes + p.NetworkBytesDownloaded; totalNetworkBytes > s.NetworkBytesDownloaded {
				s.NetworkBytesDownloaded = totalNetworkBytes
			}
			s.RuntimeSyncRan = s.RuntimeSyncRan || p.RuntimeSyncRan
			s.BytesPerSec, s.ElapsedMs = p.BytesPerSec, p.ElapsedMs
			s.FallbackAttempt, s.FallbackTotal = p.FallbackAttempt, p.FallbackTotal
			if p.Stage == "ready" {
				s.FallbackAttempt, s.FallbackTotal = 0, 0
			}
		})
		if err != nil {
			cancel(err)
		}
	})
	if err := installer.reviewLocked(cfg.OperationID, runPlan, runPlan.Digest(), true); err != nil {
		return err
	}
	if err := installer.runLocked(runCtx, cfg.OperationID, runPlan); err != nil {
		return err
	}
	return context.Cause(runCtx)
}
