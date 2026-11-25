package bootstrap

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/zwhu/memlane/internal/supervisor"
	"github.com/zwhu/memlane/kit/aiinstaller"
)

const DefaultInstallOperationID = aiinstaller.DefaultInstallOperationID

var workerRuntimeStartTimeout = aiinstaller.DefaultRuntimeStartTimeout

var installWorkerOrphanBinaries = []string{"uv"}

var (
	ErrOrphanInstallProcess = aiinstaller.ErrOrphanInstallProcess
	ErrInstallWorkerBusy    = aiinstaller.ErrInstallWorkerBusy
	ErrOperationIDMismatch  = aiinstaller.ErrOperationIDMismatch
)

type InstallWorkerState = aiinstaller.WorkerState
type InstallWorkerConfig = aiinstaller.WorkerConfig

func NormalizeWorkerComponents(components []string) []string {
	return aiinstaller.NormalizeWorkerComponents(components)
}

func WorkerStatePath(componentRoot string) (string, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return "", err
	}
	return aiinstaller.WorkerStatePath(root), nil
}

func ReadInstallWorkerState(componentRoot string) (InstallWorkerState, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return InstallWorkerState{}, err
	}
	return aiinstaller.ReadWorkerState(root)
}

func ResolveInstallOperationID(componentRoot, requested string) (string, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return "", err
	}
	return aiinstaller.ResolveOperationID(aiinstaller.Installer{Root: filepath.Dir(aiinstaller.WorkerStatePath(root))}, requested)
}

var (
	workerProcessStartTimeFn = supervisor.ProcessStartTime
	workerProcessTableFn     = supervisor.ProcessTable
	workerProcessArgsFn      = supervisor.ProcessArgs
)

func bootstrapProcessInspector() aiinstaller.ProcessInspector {
	return aiinstaller.ProcessInspector{
		ProcessStartTime: workerProcessStartTimeFn,
		ProcessStartMatches: func(ctx context.Context, pid int, started string) (bool, error) {
			active, err := supervisor.ProcessStartMatches(ctx, pid, started)
			var exit *exec.ExitError
			// ps exits with status 1 if the process no longer exists.
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				return false, nil
			}
			return active, err
		},
		ProcessPIDs: func(ctx context.Context) ([]int, error) {
			table, err := workerProcessTableFn(ctx)
			if err != nil {
				return nil, err
			}
			pids := make([]int, 0, len(table))
			for pid := range table {
				pids = append(pids, pid)
			}
			return pids, nil
		},
		ProcessArgs: workerProcessArgsFn,
		ProbeFileLockedReadOnly: func(path string) (bool, error) {
			locked, _, err := supervisor.ProbeFileLockedReadOnly(path)
			return locked, err
		},
	}
}

func DetectOrphanInstallProcesses(ctx context.Context, componentRoot string) ([]int, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return nil, err
	}
	return aiinstaller.DetectOrphanProcesses(ctx, bootstrapProcessInspector(), root, installWorkerOrphanBinaries)
}

func workerProcessActive(ctx context.Context, state InstallWorkerState) (bool, error) {
	return aiinstaller.WorkerProcessActive(ctx, state, bootstrapProcessInspector())
}

func InspectInstallWorker(ctx context.Context, componentRoot string) (InstallWorkerState, bool, error) {
	root, err := ComponentRoot(componentRoot)
	if err != nil {
		return InstallWorkerState{}, false, err
	}
	return aiinstaller.InspectWorker(ctx, bootstrapProcessInspector(), root)
}

func LaunchDetachedInstallWorker(ctx context.Context, executable string, workerArgs []string, cfg InstallWorkerConfig) (InstallWorkerState, error) {
	root, err := ComponentRoot(cfg.ComponentRoot)
	if err != nil {
		return InstallWorkerState{}, err
	}
	cfg.ComponentRoot = root
	return aiinstaller.LaunchDetachedWorker(ctx, bootstrapProcessInspector(), executable, workerArgs, cfg, installWorkerOrphanBinaries)
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
	cfg.ConfigPath = aiinstaller.CleanOptionalPath(cfg.ConfigPath)
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
			prepared, err := env.PrepareFirstRuntime(ctx, cfg.ConfigPath, assets)
			if err != nil {
				return err
			}
			return StartFirstRuntimeDetached(ctx, prepared)
		}
	}
	return runInstallWorker(ctx, cfg, installer, env.Detect, startRuntime)
}

func runInstallWorkerRuntime(ctx context.Context, run func(context.Context) error) error {
	return aiinstaller.RunWorkerRuntime(ctx, workerRuntimeStartTimeout, run)
}

func classifyInstallWorkerError(err error) (string, string, string, bool) {
	switch {
	case errors.Is(err, supervisor.ErrUpgradeLocked):
		return "failed", "upgrade_locked", "Runtime upgrade is in progress; retry shortly.", true
	case errors.Is(err, supervisor.ErrOwnerBusy), errors.Is(err, supervisor.ErrOwnerLocked):
		return "failed", "owner_busy", "Runtime owner lock is busy; retry shortly.", true
	default:
		return "", "", "", false
	}
}

func runInstallWorker(ctx context.Context, cfg InstallWorkerConfig, installer Installer, detect func(context.Context) (Plan, error), startRuntime func(context.Context) error) error {
	cfg.DownloadSource = NormalizeDownloadSource(cfg.DownloadSource)
	options := installWorkerOptions(cfg, installer, detect)
	options.StartRuntime = startRuntime
	return aiinstaller.RunWorker(ctx, options)
}

func installWorkerOptions(cfg InstallWorkerConfig, installer Installer, detect func(context.Context) (Plan, error)) aiinstaller.RunWorkerOptions {
	return aiinstaller.RunWorkerOptions{
		Config:              cfg,
		Installer:           installer.toInstaller(),
		OrphanBinaries:      installWorkerOrphanBinaries,
		RuntimeStartTimeout: workerRuntimeStartTimeout,
		Processes:           bootstrapProcessInspector(),
		ClassifyError:       classifyInstallWorkerError,
		Detect: func(ctx context.Context) (aiinstaller.Plan, error) {
			plan, err := detect(ctx)
			return plan.toInstallerPlan(), err
		},
	}
}

type installWorkerReceipt struct {
	path  string
	state InstallWorkerState
}

func (w *installWorkerReceipt) fail(err error, code string) error {
	return aiinstaller.NewWorkerReceipt(w.path, w.state).Fail(err, code, classifyInstallWorkerError)
}

func installWorkerComponents(ctx context.Context, cfg InstallWorkerConfig, installer Installer, detect func(context.Context) (Plan, error), w *installWorkerReceipt) error {
	options := installWorkerOptions(cfg, installer, detect)
	return aiinstaller.RunWorkerComponentsLocked(ctx, cfg, options, aiinstaller.NewWorkerReceipt(w.path, w.state))
}
