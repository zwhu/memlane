package setupcli

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/supervisor"
)

func (s *session) installAdvice() {
	args := []string{"install"}
	var scope []string
	if s.has("mirror") {
		scope = append(scope, "--mirror", s.get("mirror"))
	}
	for _, id := range s.options.components {
		scope = append(scope, "--component", id)
	}
	if s.has("operation-id") && s.Command == "install" {
		scope = append(scope, "--operation-id", s.get("operation-id"))
	}
	s.action(readOnly, append(append(slices.Clone(args), "--plan"), scope...)...)
	if s.Components.PlanDigest != "" && s.Environment.StoreMode != "external_readonly" {
		s.action(confirmation, append(append(args, "--apply", "--plan-digest", s.Components.PlanDigest), scope...)...)
	}
}

func (s *session) install() {
	if s.has("apply") {
		budget := time.Duration(s.wait) * time.Second
		if budget == 0 {
			// Allow a bounded immediate probe and detached launch without polling.
			budget = 3 * time.Second
		}
		deadline := s.NowFn().Add(budget)
		waitCtx, cancel := context.WithDeadline(s.ctx, deadline)
		defer cancel()
		s.ctx = waitCtx
	}
	if !s.loadSelections() || s.has("apply") && !s.checkDisk() {
		return
	}
	env, err := s.environment()
	if s.installDeadlineReached("install_worker_active") {
		return
	}
	if err != nil {
		s.block(ExitFatal, "component_environment_unavailable")
		return
	}
	plan, ok := s.detectPlan(env)
	if !ok {
		return
	}
	opID := ""
	if s.has("apply") {
		opID, err = bootstrap.ResolveInstallOperationID(s.root, s.get("operation-id"))
		if err != nil {
			s.installError(err)
			return
		}
		if safeOperationID(opID) == "" {
			s.block(ExitNeedsReview, "invalid_retained_operation")
			return
		}
		s.operation(opID)
	}

	state, active, ok := s.inspectWorker()
	if !ok {
		return
	}
	if !s.has("apply") {
		owner, ok := s.inspectOwner()
		if !ok {
			return
		}
		switch {
		case active:
			s.installBusy("install_worker_active")
		case plan.Complete() && ownerCurrent(owner, s.App.BundledRuntimeHash):
		default:
			s.block(ExitActionRequired, "installation_required")
			s.installAdvice()
		}
		return
	}

	digest := s.get("plan-digest")
	cfg := s.installWorkerConfig(opID, plan)
	expectRuntime := cfg.StartRuntime
	if active {
		if state.OperationID != "" && state.OperationID != opID {
			s.block(ExitPreflightBlocked, "operation_id_mismatch")
			return
		}
		if digest != state.PlanDigest && digest != plan.Digest() {
			journal, err := env.Installer().Snapshot()
			if err != nil || digest != journal.ApprovedDigest {
				s.block(ExitPreflightBlocked, "plan_digest_mismatch")
				s.installAdvice()
				return
			}
		}
		if !state.MatchesScope(cfg) {
			s.installBusy("install_worker_active")
			return
		}
		s.pollWorker(env, opID, expectRuntime)
		return
	}
	if plan.Digest() != digest {
		s.block(ExitPreflightBlocked, "plan_digest_mismatch")
		s.installAdvice()
		return
	}
	if len(s.options.components) != 0 {
		allAvailable, absent := requestedComponentsStatus(plan, s.options.components)
		if absent {
			s.block(ExitPreflightBlocked, "component_absent_from_plan")
			return
		}
		if allAvailable {
			return
		}
	}
	if plan.Complete() {
		owner, ok := s.inspectOwner()
		if !ok {
			return
		}
		switch {
		case owner.State == "starting" || owner.OwnerPhase == "starting":
			s.installBusy("daemon_starting")
			return
		case owner.State == "mismatch" || owner.State == "stale_owner" || owner.StaleVersionDetected:
			s.ownerAdvice(owner)
			return
		case owner.State == "ready" && owner.OwnerLocked:
			s.confirmRuntimeReady(owner)
			return
		}
	}

	args := []string{"__install-worker", "--component-root", s.root, "--config", s.configPath,
		"--mirror", cfg.DownloadSource, "--operation-id", opID, "--plan-digest", cfg.PlanDigest}
	for _, id := range cfg.Components {
		args = append(args, "--component", id)
	}
	if cfg.StartRuntime {
		args = append(args, "--start-runtime")
	}
	if s.installDeadlineReached("install_worker_active") {
		return
	}
	state, err = s.LaunchWorkerFn(s.ctx, s.Executable, args, cfg)
	s.workerState(state, state.State == "starting" || state.State == "running")
	if s.installDeadlineReached("install_worker_active") {
		return
	}
	if err != nil {
		s.installError(err)
		return
	}
	if s.Components.WorkerActive && (!state.MatchesScope(cfg) || s.wait == 0) {
		s.installBusy("install_worker_active")
		return
	}
	s.pollWorker(env, opID, expectRuntime)
}

func requestedComponentsStatus(plan bootstrap.Plan, requested []string) (allAvailable, absent bool) {
	runtimeAvailable := slices.ContainsFunc(plan.Items, func(item bootstrap.Item) bool {
		return item.Requirement.ID == "runtime" && item.Observation.Status == bootstrap.Available
	})
	allAvailable = true
	for _, id := range requested {
		index := slices.IndexFunc(plan.Items, func(item bootstrap.Item) bool { return item.Requirement.ID == id })
		if index < 0 {
			if id != "uv" || (!runtimeAvailable && !plan.Complete()) {
				absent = true
			}
			continue
		}
		if plan.Items[index].Observation.Status != bootstrap.Available {
			allAvailable = false
		}
	}
	return allAvailable, absent
}

func (s *session) installWorkerConfig(id string, plan bootstrap.Plan) bootstrap.InstallWorkerConfig {
	cfg := s.workerConfig(id)
	if !slices.ContainsFunc(plan.Items, func(item bootstrap.Item) bool { return item.Requirement.ID == "uv" }) {
		cfg.Components = slices.DeleteFunc(cfg.Components, func(id string) bool { return id == "uv" })
	}
	return cfg
}

func (s *session) workerConfig(id string) bootstrap.InstallWorkerConfig {
	return bootstrap.InstallWorkerConfig{
		ComponentRoot: s.root, ConfigPath: s.configPath, DownloadSource: s.Environment.DownloadSource,
		OperationID: id, PlanDigest: s.get("plan-digest"), Components: bootstrap.NormalizeWorkerComponents(s.options.components),
		RuntimeHash:  s.App.BundledRuntimeHash,
		StartRuntime: s.has("start-runtime") || s.Command == "install" && len(s.options.components) == 0,
	}
}

func (s *session) worker() {
	if !s.loadSelections() || !s.checkDisk() {
		return
	}
	s.operation(s.get("operation-id"))
	env, err := s.environment()
	if err != nil {
		s.block(ExitFatal, "component_environment_unavailable")
		return
	}
	// The environment reads engine-relative manifests. Preparing the daemon
	// later in the worker needs the full bundle, including scripts and assets.
	if err := s.RunWorkerFn(s.ctx, env, s.runtimeAssets, s.workerConfig(s.OperationID)); err != nil {
		s.installError(err)
	}
}

func (s *session) installError(err error) {
	switch {
	case errors.Is(err, bootstrap.ErrOrphanInstallProcess):
		s.block(ExitNeedsReview, "orphan_install_process")
	case errors.Is(err, bootstrap.ErrInstallWorkerBusy), errors.Is(err, context.DeadlineExceeded):
		s.installBusy("install_worker_active")
	case errors.Is(err, supervisor.ErrUpgradeLocked):
		s.installBusy("upgrade_locked")
	case errors.Is(err, supervisor.ErrOwnerBusy), errors.Is(err, supervisor.ErrOwnerLocked):
		s.installBusy("owner_busy")
	case errors.Is(err, bootstrap.ErrPlanChanged):
		s.block(ExitPreflightBlocked, "plan_changed")
	case errors.Is(err, bootstrap.ErrOperationIDMismatch):
		s.block(ExitPreflightBlocked, "operation_id_mismatch")
	default:
		s.block(ExitFatal, "installation_failed")
	}
}

func (s *session) installBusy(reason string) {
	s.busy(reason)
	id := s.OperationID
	if id == "" {
		id = "first-install"
		s.operation(id)
	}
	s.action(readOnly, "doctor", "--operation-id", id)
}

func (s *session) installDeadlineReached(reason string) bool {
	if s.Command != "install" || !s.has("apply") || !errors.Is(s.ctx.Err(), context.DeadlineExceeded) {
		return false
	}
	s.installBusy(reason)
	return true
}

func retryableWorkerContention(state bootstrap.InstallWorkerState) bool {
	return state.State == "failed" && oneOf(state.ErrorCode, "upgrade_locked", "owner_busy", "install_worker_active")
}

func (s *session) terminalWorker(state bootstrap.InstallWorkerState) {
	switch state.State {
	case "needs_review":
		s.block(ExitNeedsReview, "install_worker_needs_review")
	case "failed", "cancelled":
		switch state.ErrorCode {
		case "upgrade_locked", "owner_busy", "install_worker_active":
			s.busy(state.ErrorCode)
			s.installAdvice()
		case "plan_changed", "operation_id_mismatch":
			s.block(ExitPreflightBlocked, state.ErrorCode)
		default:
			s.block(ExitFatal, "install_worker_failed")
		}
	}
}

func (s *session) pollWorker(env *bootstrap.Environment, opID string, expectRuntime bool) {
	deadline, _ := s.ctx.Deadline()
	for {
		state, active, ok := s.inspectWorker()
		if !ok {
			return
		}
		if state.OperationID != "" && state.OperationID != opID {
			s.block(ExitPreflightBlocked, "operation_id_mismatch")
			return
		}
		s.operation(opID)
		if !active && state.State == "completed" {
			plan, ok := s.detectPlan(env)
			if !ok {
				return
			}
			if !expectRuntime {
				allAvailable, absent := requestedComponentsStatus(plan, s.options.components)
				if absent || !allAvailable {
					s.block(ExitActionRequired, "components_incomplete")
					s.installAdvice()
				}
				return
			}
			// A full worker can create the first runtime configuration.
			if !s.loadPaths() {
				return
			}
			if owner, ok := s.inspectOwner(); ok {
				s.confirmRuntimeReady(owner)
			}
			return
		}
		if !active && oneOf(state.State, "failed", "cancelled", "needs_review") {
			if retryableWorkerContention(state) {
				// The worker may have changed component availability before
				// encountering contention during runtime startup.
				plan, err := s.DetectPlanFn(env, s.ctx)
				if err == nil {
					s.showPlan(plan)
				} else {
					s.Components.PlanDigest, s.Artifacts.PlanDigest = "", ""
				}
			}
			s.terminalWorker(state)
			return
		}
		remaining := deadline.Sub(s.NowFn())
		if s.wait == 0 || remaining <= 0 || s.SleepFn(s.ctx, min(500*time.Millisecond, remaining)) != nil {
			s.installBusy("install_worker_active")
			return
		}
	}
}

func ownerCurrent(owner bootstrap.OwnerRuntimeStatus, bundled string) bool {
	return owner.State == "ready" && owner.OwnerLocked && owner.OwnerPhase != "starting" &&
		!owner.StaleVersionDetected && !owner.GraphAssetsStale && bundled != "" && owner.RunningRuntimeHash == bundled
}

func (s *session) confirmRuntimeReady(owner bootstrap.OwnerRuntimeStatus) {
	if owner.State == "starting" || owner.OwnerPhase == "starting" {
		s.installBusy("daemon_starting")
		return
	}
	if owner.StaleVersionDetected || owner.State != "ready" || !owner.OwnerLocked {
		s.ownerAdvice(owner)
		if s.ExitCode == ExitOK {
			s.block(ExitActionRequired, "daemon_not_ready")
		}
		return
	}
	if owner.GraphAssetsStale {
		s.block(ExitActionRequired, "graph_assets_stale")
		return
	}
	ready, err := s.FirstRuntimeReadyFn(s.ctx, s.configPath)
	if s.installDeadlineReached("daemon_starting") {
		return
	}
	if err != nil || !ready {
		s.block(ExitActionRequired, "daemon_not_ready")
	}
}

func (s *session) ownerAdvice(owner bootstrap.OwnerRuntimeStatus) {
	switch {
	case owner.State == "mismatch":
		s.block(ExitNeedsReview, "owner_mismatch")
	case owner.State == "starting" || owner.OwnerPhase == "starting":
		s.installBusy("daemon_starting")
	case owner.StaleVersionDetected:
		s.Artifacts.OwnerFingerprint = s.Daemon.OwnerFingerprint
		s.block(ExitActionRequired, "stale_daemon_runtime")
		s.action(readOnly, "repair", "stale-daemon", "--dry-run")
		if fp := s.Daemon.OwnerFingerprint; fp != "" {
			s.action(confirmation, "repair", "stale-daemon", "--apply", "--owner-fingerprint", fp)
		}
	case owner.State == "stale_owner":
		s.block(ExitActionRequired, "stale_owner_receipt")
		s.action(readOnly, "repair", "stale-owner", "--dry-run")
		s.action(confirmation, "repair", "stale-owner", "--apply")
	case owner.State == "stopped" && s.Components.Complete:
		s.block(ExitActionRequired, "daemon_stopped")
		s.installAdvice()
	}
}
