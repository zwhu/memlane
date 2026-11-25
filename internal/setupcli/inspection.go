package setupcli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
)

func (s *session) checkDisk() bool {
	free, err := s.DiskFreeFn(s.root)
	s.Environment.DiskFreeBytes = free
	if err != nil {
		s.block(ExitFatal, "disk_space_unavailable")
		return false
	}
	if free < diskMinimum {
		s.block(ExitFatal, "insufficient_disk_space")
		return false
	}
	return true
}

func (s *session) environment() (*bootstrap.Environment, error) {
	sel := s.selections
	opts := bootstrap.EnvironmentOptions{
		Root: s.root, DownloadSource: s.Environment.DownloadSource,
		Python: sel.Python, UV: sel.UV, Source: sel.Source, Archive: sel.Archive, ModelCache: sel.ModelCache, Plugin: sel.Plugin,
		SelectedAgents: sel.SelectedAgents,
	}
	if s.config.Root != "" {
		opts.ProtectedStore = s.config.Root
	} else if s.configPath != "" {
		root, err := resolveSafePath(s.root)
		if err != nil {
			return nil, err
		}
		store, err := resolveSafePath(filepath.Join(filepath.Dir(s.configPath), "library"))
		if err != nil {
			return nil, err
		}
		within := func(parent, path string) bool {
			rel, err := filepath.Rel(parent, path)
			return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		}
		if within(root, store) || within(store, root) {
			return nil, errUnsafePath
		}
	}
	return s.NewEnvFn(s.ctx, s.bootstrapAssets, opts)
}

func (s *session) detectPlan(env *bootstrap.Environment) (bootstrap.Plan, bool) {
	plan, err := s.DetectPlanFn(env, s.ctx)
	if s.installDeadlineReached("install_worker_active") {
		return plan, false
	}
	if err != nil {
		s.block(ExitFatal, "component_probe_failed")
		return plan, false
	}
	s.showPlan(plan)
	return plan, true
}

func (s *session) showPlan(plan bootstrap.Plan) {
	s.Components.Complete = plan.Complete()
	s.Components.PlanDigest = plan.Digest()
	s.Artifacts.PlanDigest = s.Components.PlanDigest
	s.Components.Items = make([]componentItem, 0, len(plan.Items))
	for _, item := range plan.Items {
		if !slices.Contains(componentIDs, item.Requirement.ID) {
			continue
		}
		status := known(string(item.Observation.Status), "available", "missing", "incompatible", "failed", "ready")
		reason := known(item.Observation.Reason, "missing", "incompatible", "digest_mismatch", "verification_failed", "policy_updated")
		if reason == "" && status != "available" && status != "ready" {
			reason = known(status, "missing", "incompatible")
			if reason == "" {
				reason = "verification_failed"
			}
		}
		downloads := item.Requirement.DownloadBytes
		if status == "available" {
			downloads = 0
		}
		s.Components.Items = append(s.Components.Items, componentItem{item.Requirement.ID, status, reason, downloads, item.Requirement.InstalledBytes})
	}
}

func (s *session) inspectOwner() (bootstrap.OwnerRuntimeStatus, bool) {
	var owner bootstrap.OwnerRuntimeStatus
	if !s.firstInstall() {
		owner.State = "stopped"
		if s.Environment.StoreMode == "uninitialized" {
			s.Daemon.State = owner.State
		}
		return owner, true
	}
	owner, err := s.InspectOwnerStatusFn(s.ctx, s.configPath, s.root, s.runtimeAssets)
	if s.installDeadlineReached("daemon_starting") {
		return owner, false
	}
	if err != nil {
		s.block(ExitFatal, "owner_status_unavailable")
		return owner, false
	}
	// Upgrade receipt errors affect verification evidence only. Keep reporting live owner state.
	s.Daemon.State = known(owner.State, "stopped", "starting", "ready", "stale_runtime", "stale_owner", "mismatch", "unknown")
	if s.Daemon.State == "" {
		s.Daemon.State = "unknown"
	}
	s.Daemon.OwnerLocked = owner.OwnerLocked
	s.Daemon.OwnerFingerprint = safeHash(owner.OwnerFingerprint, false)
	s.Daemon.RunningRuntimeHash = safeHash(owner.RunningRuntimeHash, true)
	s.Daemon.StaleVersionDetected = owner.StaleVersionDetected
	s.Daemon.GraphAssetsStale = owner.GraphAssetsStale
	s.Daemon.DeveloperCheckout = owner.DeveloperCheckout
	for name, value := range map[string]string{
		"service_url": s.config.ServiceURL, "project_control_url": s.config.Control.URL, "completion_url": s.config.CompletionURL,
	} {
		if origin, err := supervisor.ValidateLoopbackURL(value); err == nil {
			s.Daemon.Endpoints[name] = origin
		}
	}
	return owner, true
}

func (s *session) liveClient(owner bootstrap.OwnerRuntimeStatus) *controlplane.Client {
	if !owner.OwnerLocked || !oneOf(owner.State, "ready", "stale_runtime") || owner.OwnerPhase == "starting" {
		return nil
	}
	url, err := controlplane.ValidateLoopbackOrigin(s.config.Control.URL)
	if err != nil {
		return nil
	}
	if owner.Record != nil {
		boundURL, err := controlplane.ValidateLoopbackOrigin(owner.Record.ProjectControlURL)
		if err != nil || boundURL != url {
			return nil
		}
	}
	cfg := s.config.Control
	cfg.URL = url
	return s.ControlClientFn(cfg)
}

func (s *session) requireClient() *controlplane.Client {
	owner, ok := s.inspectOwner()
	if !ok {
		return nil
	}
	if client := s.liveClient(owner); client != nil {
		return client
	}
	switch {
	case owner.State == "starting" || owner.OwnerPhase == "starting":
		s.busy("daemon_starting")
	case owner.State == "mismatch":
		s.block(ExitNeedsReview, "owner_mismatch")
	case owner.OwnerLocked:
		s.block(ExitNeedsReview, "control_origin_unavailable")
	default:
		s.block(ExitActionRequired, "daemon_stopped")
		s.action(readOnly, "install", "--plan")
	}
	return nil
}

func (s *session) workerState(state bootstrap.InstallWorkerState, active bool) {
	s.Components.WorkerActive = active
	if state.State == "" {
		return
	}
	progress := workerProgress{
		State: known(state.State, "starting", "running", "failed", "cancelled", "needs_review", "completed"),
		Stage: known(state.Stage, "launching", "starting", "probing", "starting_runtime", "ready", "syncing", "downloading", "unpacking",
			"verifying", "installing", "checking", "extracting", "skipped", "fallback", "cached", "dependencies", "validating",
			"acquiring_lock", "reusing", "syncing_dependencies", "verifying_model", "validating_runtime"),
		Component: known(state.Component, componentIDs...),
		ErrorCode: known(state.ErrorCode, "plan_changed", "cancelled", "consent_required", "operation_id_mismatch",
			"orphan_install_process", "runtime_start_failed", "component_install_failed", "install_failed", "upgrade_locked", "owner_busy", "install_worker_active"),
		BytesDone: state.BytesDone, BytesTotal: state.BytesTotal, Indeterminate: state.Indeterminate, ElapsedMS: state.ElapsedMs,
		FallbackAttempt: state.FallbackAttempt, FallbackTotal: state.FallbackTotal, NetworkBytesDownloaded: state.NetworkBytesDownloaded,
		RuntimeSyncRan: state.RuntimeSyncRan,
		Mirror: known(state.Mirror, "pypi.tuna.tsinghua.edu.cn", "modelscope.cn", "www.modelscope.cn", "pypi.org", "github.com",
			"huggingface.co", "hf-mirror.com", "files.pythonhosted.org", "codeload.github.com"),
	}
	switch progress.State {
	case "failed":
		switch progress.ErrorCode {
		case "upgrade_locked":
			progress.SafeMessage = "运行环境升级正在进行中，请稍后重试。"
		case "owner_busy":
			progress.SafeMessage = "后台服务锁正在被占用，请稍后重试。"
		case "install_worker_active":
			progress.SafeMessage = "另一项组件安装正在进行中，请稍后重试。"
		default:
			progress.SafeMessage = "组件安装失败。"
		}
	case "cancelled":
		progress.SafeMessage = "组件安装已取消。"
	case "needs_review":
		progress.SafeMessage = "需要核对安装进程。"
	}
	s.Components.Progress = &progress
}

func (s *session) inspectWorker() (bootstrap.InstallWorkerState, bool, bool) {
	state, active, err := s.InspectWorkerFn(s.ctx, s.root)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(s.ctx.Err(), context.DeadlineExceeded)) &&
		s.installDeadlineReached("install_worker_active") {
		if active {
			s.workerState(state, true)
		}
		return state, active, false
	}
	s.workerState(state, active)
	if err != nil {
		s.block(ExitFatal, "install_worker_status_unavailable")
		return state, active, false
	}
	if active {
		s.operation(safeOperationID(state.OperationID))
		return state, active, true
	}
	orphans, err := s.DetectOrphansFn(s.ctx, s.root)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(s.ctx.Err(), context.DeadlineExceeded)) &&
		s.installDeadlineReached("install_worker_active") {
		return state, false, false
	}
	switch {
	case err != nil:
		s.block(ExitFatal, "install_process_status_unavailable")
	case len(orphans) > 0:
		s.block(ExitNeedsReview, "orphan_install_process")
	default:
		return state, active, true
	}
	return state, active, false
}

func (s *session) showLogs(owner bootstrap.OwnerRuntimeStatus, client *controlplane.Client) {
	var lines []string
	var err error
	switch {
	case client != nil:
		var res controlplane.ServiceMonitorSnapshot
		res, err = client.RuntimeMonitor(s.ctx, true)
		lines = res.LogTail
	case owner.State == "stopped" && s.firstInstall():
		lines, err = s.ReadLogTailFn(filepath.Join(filepath.Dir(s.configPath), "runtime-logs", "launcher.log"), 50, 64<<10)
	}
	if err != nil {
		return
	}
	for _, line := range lines[max(0, len(lines)-50):] {
		if safe := SanitizeLogLine(line, s.HomeDir); safe != "" {
			s.Artifacts.LogTail = append(s.Artifacts.LogTail, safe)
		}
	}
}

func preMutation(err error) bool {
	var controlErr *controlplane.ControlError
	return errors.As(err, &controlErr) && controlErr.IsPreMutationRejected()
}

func (s *session) controlFailure(err error) {
	var controlErr *controlplane.ControlError
	if errors.As(err, &controlErr) {
		switch {
		case controlErr.IsCode(409, "revision_conflict"):
			s.block(ExitPreflightBlocked, "revision_conflict")
			return
		case controlErr.IsPreMutationRejected():
			s.block(ExitPreflightBlocked, "control_preflight_rejected")
			return
		case controlErr.IsAuthDenied():
			s.block(ExitFatal, "control_access_denied")
			return
		}
	}
	s.block(ExitPreflightBlocked, "control_request_failed")
}
