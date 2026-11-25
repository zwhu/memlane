package aiinstaller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const DefaultInstallOperationID = "first-install"

// InstallWorkerBusyExitCode is the detached worker CLI exit code for a busy installation.
const InstallWorkerBusyExitCode = 20

// DefaultRuntimeStartTimeout bounds detached runtime preparation and startup.
const DefaultRuntimeStartTimeout = 180 * time.Second

var (
	ErrOrphanInstallProcess = errors.New("orphan install process detected targeting component root")
	ErrInstallWorkerBusy    = errors.New("install worker already active")
	ErrOperationIDMismatch  = errors.New("requested operation id differs from retained installation operation")
)

type WorkerState struct {
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

type WorkerConfig struct {
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

// CleanOptionalPath normalizes a path while preserving an empty setting.
func CleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// MatchesScope reports whether this active worker can serve the requested installation.
func (s WorkerState) MatchesScope(cfg WorkerConfig) bool {
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
		s.StartRuntime == cfg.StartRuntime && CleanOptionalPath(s.ConfigPath) == CleanOptionalPath(cfg.ConfigPath)
}

// WorkerStatePath returns the receipt path below the component root.
func WorkerStatePath(componentRoot string) string {
	return filepath.Join(componentRoot, "installation", "worker-state.json")
}

// ReadWorkerState reads the private receipt written by the installation worker.
func ReadWorkerState(componentRoot string) (WorkerState, error) {
	var state WorkerState
	b, err := PrivateRead(WorkerStatePath(componentRoot))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return WorkerState{}, err
	}
	if state.Schema != 1 {
		return WorkerState{}, errors.New("unsupported install worker state")
	}
	return state, nil
}

// ResolveOperationID validates the requested operation against the retained journal.
func ResolveOperationID(installer Installer, requested string) (string, error) {
	j, err := installer.Snapshot()
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

// ProcessInspector supplies operating system process and lock inspection.
type ProcessInspector struct {
	ProcessStartTime        func(context.Context, int) (string, error)
	ProcessStartMatches     func(context.Context, int, string) (bool, error)
	ProcessPIDs             func(context.Context) ([]int, error)
	ProcessArgs             func(int) ([]string, error)
	ProbeFileLockedReadOnly func(string) (bool, error)
}

// ErrorClassifier maps application-specific errors to terminal worker receipt fields.
type ErrorClassifier func(err error) (state, code, message string, handled bool)

// RunWorkerOptions supplies application callbacks and the installation environment.
type RunWorkerOptions struct {
	Config              WorkerConfig
	Installer           Installer
	Processes           ProcessInspector
	OrphanBinaries      []string
	ClassifyError       ErrorClassifier
	Detect              func(context.Context) (Plan, error)
	StartRuntime        func(context.Context) error
	RuntimeStartTimeout time.Duration
}

// resolveWorkerPath resolves existing ancestors too, since a target path may name
// a file or directory the installer has yet to create.
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

// DetectOrphanProcesses finds matching installer commands whose arguments target the component root.
func DetectOrphanProcesses(ctx context.Context, processes ProcessInspector, componentRoot string, binaryNames []string) ([]int, error) {
	if len(binaryNames) == 0 {
		return nil, nil
	}
	if processes.ProcessPIDs == nil || processes.ProcessArgs == nil {
		return nil, errors.New("installer process enumeration is unavailable")
	}
	root := resolveWorkerPath(componentRoot)
	pidsInSystem, err := processes.ProcessPIDs(ctx)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, pid := range pidsInSystem {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid <= 1 || pid == os.Getpid() {
			continue
		}
		args, err := processes.ProcessArgs(pid)
		// Processes may disappear or deny argv access to the current user.
		if err != nil || len(args) == 0 {
			continue
		}
		if !slices.Contains(binaryNames, filepath.Base(args[0])) &&
			(len(args) < 2 || !slices.Contains(binaryNames, args[1])) {
			continue
		}
		// The command binary may live in the root while targeting another installation.
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

// WorkerProcessActive checks the recorded process identity for an active receipt.
func WorkerProcessActive(ctx context.Context, state WorkerState, processes ProcessInspector) (bool, error) {
	if (state.State != "running" && state.State != "starting") || state.PID <= 1 {
		return false, nil
	}
	if processes.ProcessStartMatches == nil {
		return false, errors.New("installer process identity check is unavailable")
	}
	active, err := processes.ProcessStartMatches(ctx, state.PID, state.Started)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return active, err
}

// InspectWorker reads the receipt and liveness without creating files.
func InspectWorker(ctx context.Context, processes ProcessInspector, componentRoot string) (WorkerState, bool, error) {
	if processes.ProbeFileLockedReadOnly == nil {
		return WorkerState{}, false, errors.New("installation lock inspection is unavailable")
	}
	path := WorkerStatePath(componentRoot)
	locked, err := processes.ProbeFileLockedReadOnly(filepath.Join(filepath.Dir(path), "installation.lock"))
	if err != nil {
		return WorkerState{}, false, err
	}
	state, err := ReadWorkerState(componentRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return state, locked, err
	}
	if locked {
		return state, true, nil
	}
	active, err := WorkerProcessActive(ctx, state, processes)
	return state, active, err
}

// RunWorker applies the approved installation and optionally starts the runtime.
func RunWorker(ctx context.Context, options RunWorkerOptions) error {
	cfg, installer := options.Config, options.Installer
	if filepath.Clean(installer.Root) != filepath.Dir(WorkerStatePath(cfg.ComponentRoot)) {
		return errors.New("install worker environment differs from component root")
	}
	if options.Detect == nil || options.Processes.ProcessStartTime == nil {
		return errors.New("install worker requires plan detection and process identity callbacks")
	}
	cfg.Components = NormalizeWorkerComponents(cfg.Components)
	cfg.ConfigPath = CleanOptionalPath(cfg.ConfigPath)
	started, err := options.Processes.ProcessStartTime(ctx, os.Getpid())
	if err != nil {
		return err
	}
	w := WorkerReceipt{
		path: WorkerStatePath(cfg.ComponentRoot),
		state: WorkerState{
			Schema: 1, PID: os.Getpid(), Started: started, OperationID: cfg.OperationID,
			PlanDigest: cfg.PlanDigest, DownloadSource: cfg.DownloadSource,
			Components: cfg.Components, ConfigPath: cfg.ConfigPath, StartRuntime: cfg.StartRuntime, RuntimeHash: cfg.RuntimeHash,
		},
	}
	err = installer.WithLock(func() error {
		previous, readErr := ReadWorkerState(cfg.ComponentRoot)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		active, err := WorkerProcessActive(ctx, previous, options.Processes)
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
		operation, err := ResolveOperationID(installer, cfg.OperationID)
		if err != nil {
			return w.Fail(err, "install_failed", options.ClassifyError)
		}
		cfg.OperationID = operation
		if err := w.writeWorkerState(func(s *WorkerState) {
			s.OperationID = operation
			s.State, s.Stage = "starting", "starting"
		}); err != nil {
			return err
		}
		if err := RunWorkerComponentsLocked(ctx, cfg, options, &w); err != nil {
			return w.Fail(err, "install_failed", options.ClassifyError)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if options.StartRuntime != nil {
		plan, err := options.Detect(ctx)
		if err == nil && !plan.Complete() {
			err = errors.New("full installation plan is incomplete")
		}
		if err != nil {
			return w.Fail(err, "runtime_start_failed", options.ClassifyError)
		}
		if err := w.writeWorkerState(func(s *WorkerState) { s.Component, s.Stage = "", "starting_runtime" }); err != nil {
			return w.Fail(err, "runtime_start_failed", options.ClassifyError)
		}
		if err := RunWorkerRuntime(ctx, options.RuntimeStartTimeout, options.StartRuntime); err != nil {
			return w.Fail(err, "runtime_start_failed", options.ClassifyError)
		}
	}
	return w.writeWorkerState(func(s *WorkerState) {
		s.State, s.Stage = "completed", "ready"
		s.ErrorCode, s.SafeMessage = "", ""
	})
}

// RunWorkerRuntime bounds runtime preparation and startup, preserving parent cancellation.
func RunWorkerRuntime(ctx context.Context, timeout time.Duration, run func(context.Context) error) error {
	if timeout <= 0 {
		timeout = DefaultRuntimeStartTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
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

// LaunchDetachedWorker starts the worker executable or attaches to a matching worker.
func LaunchDetachedWorker(ctx context.Context, processes ProcessInspector, executable string, workerArgs []string, cfg WorkerConfig, orphanBinaries []string) (WorkerState, error) {
	inspect := func(ctx context.Context) (WorkerState, bool, error) {
		state, active, err := InspectWorker(ctx, processes, cfg.ComponentRoot)
		if active && !state.MatchesScope(cfg) {
			return state, true, ErrInstallWorkerBusy
		}
		return state, active, err
	}
	state, active, err := inspect(ctx)
	if err != nil || active {
		return state, err
	}
	pids, err := DetectOrphanProcesses(ctx, processes, cfg.ComponentRoot, orphanBinaries)
	if err != nil {
		return WorkerState{}, err
	}
	if len(pids) > 0 {
		return WorkerState{State: "needs_review", ErrorCode: "orphan_install_process"}, ErrOrphanInstallProcess
	}
	dir := filepath.Dir(WorkerStatePath(cfg.ComponentRoot))
	if err := PreparePrivateDir(dir); err != nil {
		return WorkerState{}, err
	}
	launchLock, err := os.OpenFile(filepath.Join(dir, "worker-launch.lock"),
		os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return WorkerState{}, err
	}
	defer launchLock.Close()
	lockInfo, err := launchLock.Stat()
	if err != nil {
		return WorkerState{}, err
	}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0600 {
		return WorkerState{}, errors.New("worker launch lock must be a private regular file")
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "worker.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return WorkerState{}, err
	}
	defer logFile.Close()
	info, err := logFile.Stat()
	if err != nil {
		return WorkerState{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return WorkerState{}, errors.New("worker log must be a private regular file")
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
			return WorkerState{}, err
		}
		select {
		case <-waitCtx.Done():
			return WorkerState{}, waitCtx.Err()
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
		return WorkerState{}, err
	}

	cmd := exec.Command(executable, workerArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return WorkerState{}, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for {
		state, err = ReadWorkerState(cfg.ComponentRoot)
		if err == nil && state.PID == cmd.Process.Pid {
			return state, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return WorkerState{}, err
		}
		select {
		case <-waitCtx.Done():
			return WorkerState{}, waitCtx.Err()
		case exitErr := <-exited:
			state, err = ReadWorkerState(cfg.ComponentRoot)
			if err == nil && state.PID == cmd.Process.Pid {
				return state, nil
			}
			live, active, inspectErr := inspect(waitCtx)
			if inspectErr != nil || active {
				return live, inspectErr
			}
			var processErr *exec.ExitError
			if errors.As(exitErr, &processErr) && processErr.ExitCode() == InstallWorkerBusyExitCode {
				return WorkerState{}, ErrInstallWorkerBusy
			}
			if exitErr != nil {
				return WorkerState{}, exitErr
			}
			return WorkerState{}, errors.New("install worker exited before publishing state")
		case <-ticker.C:
		}
	}
}
