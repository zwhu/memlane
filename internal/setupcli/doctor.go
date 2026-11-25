package setupcli

import (
	"errors"
	"os"
	"slices"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

func (s *session) doctor() {
	s.checkDisk()
	if s.loadSelections() {
		if s.has("check-network") {
			ok := s.CheckDNSFn(s.ctx, s.Environment.DownloadSource)
			s.Environment.DNSOK = &ok
			if !ok {
				s.block(ExitPreflightBlocked, "dns_resolution_failed")
				s.HumanSteps = append(s.HumanSteps, "检查系统 DNS 设置和网络连接，或切换 china / official 下载源后重新检查。")
			}
		}
		env, err := s.environment()
		if err != nil {
			s.block(ExitFatal, "component_environment_unavailable")
		} else if _, ok := s.detectPlan(env); ok && !s.Components.Complete {
			s.block(ExitActionRequired, "components_incomplete")
			s.installAdvice()
		}
	}
	worker, active, workerOK := s.inspectWorker()
	if active {
		s.installBusy("install_worker_active")
	}
	id := s.get("operation-id")
	matchesWorker := id != "" && worker.OperationID == id
	if matchesWorker && workerOK && !active && worker.State != "" {
		s.operation(id)
		s.terminalWorker(worker)
	}
	owner, ok := s.inspectOwner()
	if ok && s.Environment.StoreMode != "external_readonly" {
		s.ownerAdvice(owner)
	}
	ops, opsOK := s.pendingOperations()
	client := s.liveClient(owner)
	if s.firstInstall() {
		var err error
		s.Generation.InitializationComplete, err = bootstrap.FirstInitializationComplete(s.configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.block(ExitNeedsReview, "initialization_receipt_unavailable")
		}
		s.Generation.Deferred, err = bootstrap.FirstSetupDeferred(s.configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.block(ExitNeedsReview, "deferred_setup_receipt_unavailable")
		}
	}
	if client != nil {
		dir, err := client.Connections(s.ctx, false)
		if err != nil {
			s.controlFailure(err)
		} else {
			s.showConnections(dir)
			for _, row := range dir.Connections {
				if !isAgent(row.Kind) || s.has("agent") && row.Kind != s.get("agent") {
					continue
				}
				needsActivation := unprepared(row.Status) || oneOf(row.Status, "prepared", "ready_to_activate")
				inScope := s.has("agent") || slices.Contains(s.Environment.SelectedAgents, row.Kind) ||
					(s.Environment.StoreMode == "first_install_setup" && row.Detected && captureMonitoredHost(row.Kind))
				if row.NeedsHookRepair || unknownConnection(row.Status) || s.activationPending(row) || (needsActivation && inScope) {
					s.connectionOutcome(row, false)
				}
			}
		}
		s.generation(client)
		if opsOK {
			for _, op := range ops {
				if op.IsConnection() && (!s.has("agent") || slices.ContainsFunc(s.Connections.Hosts, func(host hostSummary) bool { return host.ID == op.Profile })) {
					s.pendingAdvice(client, op, dir)
				}
			}
		}
		if !matchesWorker && controlplane.ValidOperationID(id) {
			res, err := client.ConnectionOperation(s.ctx, id)
			if err != nil {
				s.controlFailure(err)
			} else {
				s.operation(id)
				s.Artifacts.ConnectionOperation = receiptSummary(id, res)
			}
		}
	} else if owner.OwnerLocked && oneOf(owner.State, "ready", "stale_runtime") {
		s.block(ExitNeedsReview, "control_origin_unavailable")
	}
	if s.has("include-logs") {
		s.showLogs(owner, client)
	}
	s.showLocalHosts(client == nil)
	if len(s.Generation.AvailableBridges) == 0 && s.Generation.Active == nil {
		s.Generation.RecommendedProcessingCLIs = bootstrap.RecommendedProcessingCLIs()
		s.Generation.MissingProcessingCLIHint = processingHint
	}
}
