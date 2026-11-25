package setupcli

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
	"github.com/zwhu/memlane/kit/agents"
)

// Runner allows offline callers to substitute system probes and mutation entry
// points. Its zero value uses the production implementations.
type Runner struct {
	Executable                 string
	HomeDir                    string
	Environ                    func(string) string
	DefaultConfigFn            func() (string, error)
	DefaultComponentRootFn     func() (string, error)
	DiskFreeFn                 func(string) (uint64, error)
	CheckDNSFn                 func(context.Context, string) bool
	DetectAgentsFn             func(agents.DiscoveryOptions) []agents.DetectedHost
	ScanConflictsFn            func(agents.DetectedHost, agents.ConflictPolicy) (agents.ConflictReport, error)
	InstallHostConfigFn        func(hooks.InstallOptions) (hooks.InstallResult, error)
	NewEnvFn                   func(context.Context, fs.FS, bootstrap.EnvironmentOptions) (*bootstrap.Environment, error)
	DetectPlanFn               func(*bootstrap.Environment, context.Context) (bootstrap.Plan, error)
	InspectOwnerStatusFn       func(context.Context, string, string, fs.FS) (bootstrap.OwnerRuntimeStatus, error)
	InspectWorkerFn            func(context.Context, string) (bootstrap.InstallWorkerState, bool, error)
	DetectOrphansFn            func(context.Context, string) ([]int, error)
	LaunchWorkerFn             func(context.Context, string, []string, bootstrap.InstallWorkerConfig) (bootstrap.InstallWorkerState, error)
	RunWorkerFn                func(context.Context, *bootstrap.Environment, fs.FS, bootstrap.InstallWorkerConfig) error
	PrepareRuntimeForUpgradeFn func(*bootstrap.Environment, context.Context, string, fs.FS) (bootstrap.FirstRuntime, error)
	RestartStaleOwnerFn        func(context.Context, bootstrap.FirstRuntime, string, fs.FS) error
	RecoverStaleOwnerFn        func(context.Context, string) (supervisor.RecoverResult, error)
	StopOwnerBoundFn           func(context.Context, supervisor.StopOwnerOptions, int, string) error
	ControlClientFn            func(controlplane.Config) *controlplane.Client
	FirstRuntimeReadyFn        func(context.Context, string) (bool, error)
	ReadLogTailFn              func(string, int, int64) ([]string, error)
	NowFn                      func() time.Time
	SleepFn                    func(context.Context, time.Duration) error
}

func (r *Runner) defaults() {
	if r.Environ == nil {
		r.Environ = os.Getenv
	}
	if r.Executable == "" {
		r.Executable = executable()
	}
	homeOverride := r.HomeDir != ""
	if r.HomeDir == "" {
		r.HomeDir, _ = os.UserHomeDir()
	}
	if r.DefaultConfigFn == nil {
		r.DefaultConfigFn = bootstrap.DefaultRuntimeConfig
		if homeOverride {
			r.DefaultConfigFn = func() (string, error) {
				return filepath.Join(r.HomeDir, "Library/Application Support/Memlane/runtime.json"), nil
			}
		}
	}
	if r.DefaultComponentRootFn == nil {
		r.DefaultComponentRootFn = func() (string, error) { return bootstrap.ComponentRoot("") }
		if homeOverride {
			r.DefaultComponentRootFn = func() (string, error) {
				return filepath.Join(r.HomeDir, "Library/Application Support/Memlane/components"), nil
			}
		}
	}
	if r.DiskFreeFn == nil {
		r.DiskFreeFn = diskFree
	}
	if r.CheckDNSFn == nil {
		r.CheckDNSFn = checkDNS
	}
	if r.DetectAgentsFn == nil {
		r.DetectAgentsFn = agents.DetectHosts
	}
	if r.ScanConflictsFn == nil {
		r.ScanConflictsFn = agents.ScanHostConflicts
	}
	if r.InstallHostConfigFn == nil {
		r.InstallHostConfigFn = hooks.InstallHostConfig
	}
	if r.NewEnvFn == nil {
		r.NewEnvFn = bootstrap.NewEnvironment
	}
	if r.DetectPlanFn == nil {
		r.DetectPlanFn = (*bootstrap.Environment).Detect
	}
	if r.InspectOwnerStatusFn == nil {
		r.InspectOwnerStatusFn = bootstrap.InspectOwnerRuntimeStatus
	}
	if r.InspectWorkerFn == nil {
		r.InspectWorkerFn = bootstrap.InspectInstallWorker
	}
	if r.DetectOrphansFn == nil {
		r.DetectOrphansFn = func(ctx context.Context, root string) ([]int, error) {
			if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
				return nil, nil
			} else if err != nil {
				return nil, err
			}
			return bootstrap.DetectOrphanInstallProcesses(ctx, root)
		}
	}
	if r.LaunchWorkerFn == nil {
		r.LaunchWorkerFn = bootstrap.LaunchDetachedInstallWorker
	}
	if r.RunWorkerFn == nil {
		r.RunWorkerFn = bootstrap.RunInstallWorkerWith
	}
	if r.PrepareRuntimeForUpgradeFn == nil {
		r.PrepareRuntimeForUpgradeFn = (*bootstrap.Environment).PrepareFirstRuntimeForUpgrade
	}
	if r.RestartStaleOwnerFn == nil {
		r.RestartStaleOwnerFn = bootstrap.RestartStaleRuntimeOwnerDetached
	}
	if r.RecoverStaleOwnerFn == nil {
		r.RecoverStaleOwnerFn = bootstrap.RecoverFirstRuntimeOwner
	}
	if r.StopOwnerBoundFn == nil {
		r.StopOwnerBoundFn = supervisor.StopVerifiedOwnerBound
	}
	if r.ControlClientFn == nil {
		r.ControlClientFn = controlplane.NewClient
	}
	if r.FirstRuntimeReadyFn == nil {
		r.FirstRuntimeReadyFn = bootstrap.FirstRuntimeReady
	}
	if r.ReadLogTailFn == nil {
		r.ReadLogTailFn = supervisor.ReadLogTail
	}
	if r.NowFn == nil {
		r.NowFn = time.Now
	}
	if r.SleepFn == nil {
		r.SleepFn = sleep
	}
}

type session struct {
	Runner
	*envelope
	options
	ctx             context.Context
	runtimeAssets   fs.FS
	bootstrapAssets fs.FS
	configPath      string
	root            string
	config          runtimeConfig
	opsDir          string
	selections      bootstrap.Selections
}

func sleep(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func checkDNS(ctx context.Context, mirror string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	hosts := []string{"pypi.tuna.tsinghua.edu.cn", "modelscope.cn"}
	if mirror == "official" {
		hosts = []string{"pypi.org", "github.com", "huggingface.co"}
	}
	for _, host := range hosts {
		if ips, err := net.DefaultResolver.LookupHost(ctx, host); err != nil || len(ips) == 0 {
			return false
		}
	}
	return true
}

func (s *session) action(requires string, args ...string) {
	// Preserve explicit targets so a preview cannot accidentally authorize a
	// mutation against a different store. Suppress unsafe command suggestions.
	for _, name := range []string{"config", "component-root"} {
		if value := s.get(name); value != "" {
			if SanitizeString(value, "") != value {
				return
			}
			args = append(args, "--"+name, value)
		}
	}
	args = append(slices.Clone(args), "--json")
	for _, action := range s.NextActions {
		if action.Requires == requires && slices.Equal(action.Args, args) {
			return
		}
	}
	s.NextActions = append(s.NextActions, nextAction{Args: args, Requires: requires})
}

const readOnly = "read_only"
const confirmation = "explicit_confirmation"
