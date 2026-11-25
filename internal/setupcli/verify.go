package setupcli

import (
	"errors"
	"os"
	"slices"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

func (s *session) verify() {
	stage := s.get("stage")
	if stage == "connection" {
		s.verifyConnections()
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
	plan, planOK := s.detectPlan(env)
	worker, active, workerOK := s.inspectWorker()
	if active {
		s.installBusy("install_worker_active")
	}
	if !planOK || !workerOK {
		return
	}
	var owner bootstrap.OwnerRuntimeStatus
	var ownerInspected bool
	if s.has("expect-zero-downloads") {
		journal, err := env.Installer().Snapshot()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.block(ExitNeedsReview, "installation_journal_unreadable")
			return
		}
		var upgrade *bootstrap.RuntimeUpgradeReceipt
		if s.firstInstall() {
			if stage != "components" {
				var ok bool
				owner, ok = s.inspectOwner()
				if !ok {
					return
				}
				if owner.UpgradeReceiptError != "" {
					s.block(ExitNeedsReview, "upgrade_receipt_unreadable")
					return
				}
				ownerInspected = true
				upgrade = owner.UpgradeReceipt
			} else {
				receipt, recErr := bootstrap.ReadRuntimeUpgradeReceipt(s.root, s.configPath)
				if recErr != nil && !errors.Is(recErr, os.ErrNotExist) {
					s.block(ExitNeedsReview, "upgrade_receipt_unreadable")
					return
				}
				if recErr == nil && (s.config.First == nil || s.config.First.StoreID == "" || receipt.StoreID == s.config.First.StoreID) &&
					(s.App.BundledRuntimeHash == "" || receipt.ToRuntimeHash == s.App.BundledRuntimeHash) {
					upgrade = &receipt
				}
			}
		}
		hasWorker := worker.Schema != 0 || worker.State != "" || worker.PID != 0 || worker.OperationID != "" ||
			worker.RuntimeHash != "" || worker.NetworkBytesDownloaded > 0 || worker.RuntimeSyncRan
		check := bootstrap.EvaluateZeroDownloads(plan, journal, err == nil, worker, hasWorker, s.App.BundledRuntimeHash, upgrade)
		if !check.Satisfied {
			if check.Reason == "install_worker_active" {
				if !active {
					s.installBusy("install_worker_active")
				}
			} else {
				s.block(ExitActionRequired, "zero_downloads_not_satisfied")
				s.installAdvice()
			}
			return
		}
	}
	if active {
		return
	}
	if !s.Components.Complete {
		s.block(ExitActionRequired, "components_incomplete")
		s.installAdvice()
		return
	}
	if stage == "components" {
		return
	}
	if !s.firstInstall() {
		s.block(ExitActionRequired, "first_install_config_required")
		s.installAdvice()
		return
	}
	if !ownerInspected {
		var ok bool
		owner, ok = s.inspectOwner()
		if !ok {
			return
		}
	}
	if owner.State != "ready" || !owner.OwnerLocked || owner.OwnerPhase == "starting" {
		s.ownerAdvice(owner)
		if s.ExitCode == ExitOK {
			s.block(ExitActionRequired, "daemon_not_ready")
		}
		return
	}
	ready, err := s.FirstRuntimeReadyFn(s.ctx, s.configPath)
	if err != nil || !ready {
		s.block(ExitActionRequired, "daemon_not_ready")
		return
	}
	if s.has("expect-runtime-current") && !ownerCurrent(owner, s.App.BundledRuntimeHash) {
		s.block(ExitActionRequired, "runtime_not_current")
		s.ownerAdvice(owner)
	}
}

func (s *session) verifyConnections() {
	client := s.requireClient()
	if client == nil || !s.loadSelections() {
		return
	}
	dir, err := client.Connections(s.ctx, true)
	if err != nil {
		s.controlFailure(err)
		return
	}
	targets := controlplane.ConnectionsDirectory{Revision: dir.Revision}
	missing := slices.Clone(s.Environment.SelectedAgents)
	if s.has("agent") {
		missing = []string{s.get("agent")}
	}
	for _, row := range dir.Connections {
		if !isAgent(row.Kind) || s.has("agent") && row.Kind != s.get("agent") {
			continue
		}
		if s.has("agent") || row.Detected || row.AgentRegistered || !unprepared(row.Status) && row.Status != "not_detected" && row.Status != "component_unavailable" ||
			slices.Contains(s.Environment.SelectedAgents, row.Kind) {
			targets.Connections = append(targets.Connections, row)
			missing = slices.DeleteFunc(missing, func(kind string) bool { return kind == row.Kind })
			s.connectionOutcome(row, false)
		}
	}
	s.showConnections(targets)
	if len(targets.Connections) == 0 || len(missing) > 0 {
		s.block(ExitActionRequired, "connection_target_unavailable")
	}
}
