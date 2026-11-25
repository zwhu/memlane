package setupcli

import (
	"errors"
	"slices"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
)

func (s *session) repair() {
	switch s.options.target {
	case "stale-daemon":
		s.repairStaleDaemon()
	case "stale-owner":
		s.repairStaleOwner()
	case "pending-ops":
		s.repairPending()
	case "trae-hooks":
		s.repairHooks()
	}
}

func (s *session) repairStaleDaemon() {
	owner, ok := s.inspectOwner()
	if !ok {
		return
	}
	s.Artifacts.OwnerFingerprint = s.Daemon.OwnerFingerprint
	switch {
	case owner.DeveloperCheckout:
		s.block(ExitFatal, "developer_checkout")
		return
	case owner.OwnerPhase == "starting" || owner.State == "starting":
		s.installBusy("daemon_starting")
		return
	case owner.State == "mismatch":
		s.block(ExitNeedsReview, "owner_mismatch")
		return
	}
	if !s.has("apply") {
		if owner.StaleVersionDetected {
			s.ownerAdvice(owner)
		}
		return
	}
	if !s.matchFingerprint() {
		return
	}
	if !owner.StaleVersionDetected {
		return
	}
	if !s.loadSelections() {
		return
	}
	env, err := s.environment()
	if err != nil {
		s.block(ExitFatal, "component_environment_unavailable")
		return
	}
	prepared, err := s.PrepareRuntimeForUpgradeFn(env, s.ctx, s.configPath, s.runtimeAssets)
	switch {
	case errors.Is(err, bootstrap.ErrInstallWorkerBusy):
		s.installBusy("install_worker_active")
		return
	case errors.Is(err, supervisor.ErrUpgradeLocked), errors.Is(err, supervisor.ErrOwnerBusy), errors.Is(err, supervisor.ErrOwnerLocked):
		s.installBusy("owner_busy")
		return
	case err != nil:
		s.block(ExitFatal, "runtime_preparation_failed")
		return
	}
	s.ownerMutationError(s.RestartStaleOwnerFn(s.ctx, prepared, owner.OwnerFingerprint, s.runtimeAssets))
}

func (s *session) matchFingerprint() bool {
	if s.Daemon.OwnerFingerprint == "" || s.get("owner-fingerprint") != s.Daemon.OwnerFingerprint {
		s.block(ExitPreflightBlocked, "owner_fingerprint_mismatch")
		return false
	}
	return true
}

func (s *session) ownerMutationError(err error) {
	switch {
	case err == nil:
	case errors.Is(err, supervisor.ErrOwnerFingerprintMismatch), errors.Is(err, supervisor.ErrOwnerProcessChanged):
		s.block(ExitPreflightBlocked, "owner_fingerprint_mismatch")
	case errors.Is(err, supervisor.ErrOwnerLocked), errors.Is(err, supervisor.ErrOwnerBusy), errors.Is(err, supervisor.ErrUpgradeLocked):
		s.installBusy("owner_busy")
	case errors.Is(err, supervisor.ErrRecoveryRefused):
		s.block(ExitNeedsReview, "owner_recovery_refused")
	case errors.Is(err, supervisor.ErrOwnerMismatch):
		s.block(ExitFatal, "owner_mismatch")
	default:
		s.block(ExitFatal, "owner_operation_failed")
	}
}

func (s *session) repairStaleOwner() {
	owner, ok := s.inspectOwner()
	if !ok {
		return
	}
	switch {
	case owner.OwnerLocked:
		s.installBusy("owner_locked")
	case !owner.HasReceipt:
	case !s.has("apply"):
		s.block(ExitActionRequired, "stale_owner_receipt")
		s.action(confirmation, "repair", "stale-owner", "--apply")
	default:
		_, err := s.RecoverStaleOwnerFn(s.ctx, s.configPath)
		s.ownerMutationError(err)
	}
}

func (s *session) repairPending() {
	ops, ok := s.pendingOperations()
	if !ok {
		return
	}
	id := s.get("operation-id")
	ops = slices.DeleteFunc(ops, func(op controlplane.UIOperation) bool {
		return !op.IsConnection() || id != "" && op.Operation != id
	})
	if len(ops) == 0 {
		if s.has("apply") {
			s.block(ExitInvalidArguments, "unknown_pending_operation")
		}
		return
	}
	client := s.requireClient()
	if client == nil {
		return
	}
	if !s.has("apply") {
		reconcilable := false
		var reviewOps []controlplane.UIOperation
		for _, op := range ops {
			res, err := client.PreviewReconcilePendingOperationFor(s.ctx, op.Operation, op.Profile, op.ConnectionAction())
			if err == nil && previewReconcilable(res) {
				reconcilable = true
				if s.Environment.StoreMode != "external_readonly" {
					s.action(confirmation, "repair", "pending-ops", "--operation-id", op.Operation, "--apply")
				}
			} else {
				s.Blockers = append(s.Blockers, blocker{Code: "pending_connection_operation_ambiguous", Status: statuses[ExitNeedsReview]})
				reviewOps = append(reviewOps, op)
			}
			if id != "" {
				s.operation(id)
				if err == nil {
					s.Artifacts.ConnectionOperation = receiptSummary(id, res)
				}
			}
		}
		if len(reviewOps) != 0 {
			dir, _ := client.Connections(s.ctx, false)
			for _, op := range reviewOps {
				s.pendingReviewAdvice(op, dir)
			}
		}
		if reconcilable {
			s.block(ExitActionRequired, "pending_connection_operation")
		} else {
			s.ExitCode = ExitNeedsReview
		}
		return
	}

	s.operation(id)
	dir, err := client.Connections(s.ctx, false)
	if err != nil {
		s.controlFailure(err)
		return
	}
	op := ops[0]
	res, err := client.ReconcilePendingOperationFor(s.ctx, op.Operation, dir.Revision, op.Profile, op.ConnectionAction())
	switch {
	case revisionConflict(err):
		s.block(ExitPreflightBlocked, "revision_conflict")
	case err != nil:
		s.block(ExitNeedsReview, "pending_connection_operation_ambiguous")
		s.pendingReviewAdvice(op, dir)
	default:
		s.Artifacts.ConnectionOperation = receiptSummary(id, res)
		if !terminalReceiptMatches(res, op) || !(res.SafeToUnlock || res.Reconciled || res.AlreadyComplete) {
			s.block(ExitNeedsReview, "pending_connection_operation_ambiguous")
			s.pendingReviewAdvice(op, dir)
			return
		}
		if err := controlplane.FinishUIOperation(s.opsDir, id); err != nil {
			s.block(ExitNeedsReview, "connection_operation_cleanup_failed")
			return
		}
		s.Connections.Pending = slices.DeleteFunc(s.Connections.Pending, func(summary operationSummary) bool {
			return summary.ID == id
		})
	}
}

func safeHookEvents(events []string) []string {
	safe := []string{}
	for _, event := range events {
		if oneOf(event, "SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
			"Stop", "SubagentStop", "SubagentStart", "Notification", "PreCompact", "PermissionRequest") {
			safe = append(safe, event)
		}
	}
	return safe
}

func (s *session) showHookRepair(res controlplane.TraeHooksRepairResult) {
	s.Artifacts.HookRepair = &hookRepairSummary{
		NeedsHookRepair: res.NeedsHookRepair, Repairable: res.Repairable, Repaired: res.Repaired,
		RemovedEvents: safeHookEvents(res.RemovedEvents), RetainedEvents: safeHookEvents(res.RetainedEvents),
		PreservedThirdPartyGroups: res.PreservedThirdPartyGroups,
	}
}

func (s *session) repairHooks() {
	client := s.requireClient()
	if client == nil {
		return
	}
	dir, err := client.Connections(s.ctx, false)
	if err != nil {
		s.controlFailure(err)
		return
	}
	row, ok := s.target(dir, "trae_ide")
	if !ok {
		return
	}
	res, err := client.PreviewRepairTraeHooks(s.ctx, row.ID)
	if err != nil {
		s.controlFailure(err)
		return
	}
	sha := res.ReviewedSHA
	if sha == "" {
		sha = res.ProposedSHA256
	}
	s.Artifacts.ReviewedSHA = safeHash(sha, false)
	s.showHookRepair(res)
	switch {
	case !res.NeedsHookRepair:
		if s.has("apply") && sha != s.get("reviewed-sha") {
			s.block(ExitPreflightBlocked, "reviewed_sha_mismatch")
		}
	case !res.Repairable:
		s.block(ExitNeedsReview, "trae_hooks_not_repairable")
	case len(res.Blockers) != 0:
		for _, item := range res.Blockers {
			s.block(ExitPreflightBlocked, knownBlocker(item.Code))
		}
	case !s.has("apply"):
		s.block(ExitActionRequired, "trae_hooks_repair_required")
		if s.Artifacts.ReviewedSHA != "" {
			s.action(confirmation, "repair", "trae-hooks", "--apply", "--instance-id", row.ID, "--reviewed-sha", s.Artifacts.ReviewedSHA)
		}
	case sha == "" || sha != s.get("reviewed-sha"):
		s.block(ExitPreflightBlocked, "reviewed_sha_mismatch")
	default:
		revision := res.Revision
		if revision == 0 {
			revision = dir.Revision
		}
		if !s.recordMutation(client, row.ID, "connection_repair_trae_hooks", revision, func(opID string) error {
			var err error
			res, err = client.RepairTraeHooks(s.ctx, row.ID, revision, opID, sha)
			return err
		}) {
			return
		}
		s.showHookRepair(res)
		if res.Mode == "orphan_cleanup" {
			s.action(readOnly, "connect", "--agent", "trae_ide", "--dry-run")
		} else {
			s.reloadSteps("trae_ide")
		}
	}
}

func (s *session) stop() {
	owner, ok := s.inspectOwner()
	if !ok {
		return
	}
	s.Artifacts.OwnerFingerprint = s.Daemon.OwnerFingerprint
	switch {
	case owner.State == "stopped":
		return
	case s.has("apply") && owner.DeveloperCheckout:
		s.block(ExitFatal, "developer_checkout")
		return
	case owner.State == "starting" || owner.OwnerPhase == "starting":
		s.installBusy("daemon_starting")
		return
	case owner.State == "stale_owner" || owner.State == "mismatch":
		s.ownerAdvice(owner)
		return
	case !s.has("apply"):
		s.block(ExitActionRequired, "daemon_stop_confirmation_required")
		if s.Daemon.OwnerFingerprint != "" && !owner.DeveloperCheckout {
			s.action(confirmation, "stop", "--apply", "--owner-fingerprint", s.Daemon.OwnerFingerprint)
		}
		return
	}
	if !s.matchFingerprint() {
		return
	}
	err := s.StopOwnerBoundFn(s.ctx, supervisor.StopOwnerOptions{
		StoreRoot: s.config.Root, ConfigPath: s.configPath, ExpectedFingerprint: owner.OwnerFingerprint,
	}, owner.OwnerPID, owner.OwnerStarted)
	if err != nil {
		s.ownerMutationError(err)
		return
	}
	if owner, ok := s.inspectOwner(); ok && (owner.OwnerLocked || owner.State != "stopped") {
		s.block(ExitNeedsReview, "daemon_stop_unconfirmed")
	}
}
