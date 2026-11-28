package setupcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/kit/agents"
)

const (
	doctorCommandTimeout        = 10 * time.Second
	doctorHookTrustProbeTimeout = 4 * time.Second
)

func (s *session) doctor() {
	baseCtx := s.ctx
	ctx, cancel := context.WithTimeout(baseCtx, doctorCommandTimeout)
	defer cancel()
	s.ctx = ctx
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
	owner, ok := s.inspectOwner()
	contention := matchesWorker && workerOK && !active && retryableWorkerContention(worker)
	staleContention := contention && s.Components.Complete && ownerCurrent(owner, s.App.BundledRuntimeHash)
	retryableFailure := contention && !staleContention
	if matchesWorker && workerOK && !active && worker.State != "" {
		s.operation(id)
		if !staleContention {
			s.terminalWorker(worker)
		}
	}
	if ok && s.Environment.StoreMode != "external_readonly" && (!(active || retryableFailure) || owner.State != "stopped") {
		s.ownerAdvice(owner)
	}
	if ok && !owner.DeveloperCheckout && !active {
		s.inspectUpgradeCleanup(owner)
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
	genOK := false
	controlCoveredHosts := map[string]bool{}
	if client != nil && s.config.Control.PluginDir != "" {
		dir, err := client.Connections(s.ctx, false)
		if err != nil {
			s.controlFailure(err)
		} else {
			s.showConnections(dir)
			for _, row := range dir.Connections {
				if isAgent(row.Kind) {
					controlCoveredHosts[row.Kind] = true
				}
				if !isAgent(row.Kind) || s.has("agent") && row.Kind != s.get("agent") {
					continue
				}
				needsActivation := unprepared(row.Status) || oneOf(row.Status, "prepared", "ready_to_activate")
				inScope := s.has("agent") || slices.Contains(s.Environment.SelectedAgents, row.Kind) ||
					(s.Environment.StoreMode == "first_install_setup" && row.Detected && captureMonitoredHost(row.Kind))
				if row.NeedsHookRepair || rowNeedsHookTrust(row) || unknownConnection(row.Status) || s.activationPending(row) || (needsActivation && inScope) {
					s.connectionOutcome(row, false)
				}
			}
		}
		genOK = s.generation(client)
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
				s.showHookRepairFromReceipt(res)
			}
		}
	} else if client != nil {
		genOK = s.gatewayGeneration(client)
	} else if owner.OwnerLocked && oneOf(owner.State, "ready", "stale_runtime") {
		s.block(ExitNeedsReview, "control_origin_unavailable")
	}
	if s.has("include-logs") {
		s.showLogs(owner, client)
	}
	hosts := s.localHosts()
	s.showLocalHosts(hosts, client == nil, client != nil && genOK)
	s.inspectHostConflicts(hosts)
	s.inspectHostHookTrust(baseCtx, hosts, controlCoveredHosts)
	if (client == nil || genOK) && len(s.Generation.AvailableBridges) == 0 && s.Generation.Active == nil {
		detected := s.Generation.DetectedUnconfiguredCLIs
		recommendations := bootstrap.RecommendedProcessingCLIs()
		if len(detected) == 0 {
			s.Generation.RecommendedProcessingCLIs = recommendations
			s.Generation.MissingProcessingCLIHint = processingHint
			return
		}
		s.Generation.MissingProcessingCLIHint = fmt.Sprintf(unconfiguredProcessingHintFormat, strings.Join(detected, "、"))
		for _, rec := range recommendations {
			if slices.Contains(detected, rec.Kind) && !slices.ContainsFunc(s.Generation.RecommendedProcessingCLIs, func(existing bootstrap.RecommendedProcessingCLI) bool {
				return existing.Kind == rec.Kind
			}) {
				rec.InstallCommand = ""
				s.Generation.RecommendedProcessingCLIs = append(s.Generation.RecommendedProcessingCLIs, rec)
			}
		}
	}
}

func (s *session) gatewayGeneration(client *controlplane.Client) bool {
	var data struct {
		State struct {
			ActiveRevision int64 `json:"active_revision"`
		} `json:"state"`
		Profiles []struct {
			Mode       string `json:"mode"`
			BridgeKind string `json:"bridge_kind"`
			Removed    bool   `json:"removed"`
		} `json:"profiles"`
	}
	err := client.Request(s.ctx, "GET", "/api/generation", nil, &data)
	var status *controlplane.ControlError
	if errors.As(err, &status) && status.Status == 404 {
		return s.generation(client)
	}
	if err != nil {
		s.controlFailure(err)
		return false
	}
	if data.State.ActiveRevision > 0 {
		s.Generation.Active = &generationReference{Revision: data.State.ActiveRevision}
	}
	for _, profile := range data.Profiles {
		if profile.Mode == "agent_bridge" && !profile.Removed && isProcessingCLI(profile.BridgeKind) &&
			!slices.Contains(s.Generation.AvailableBridges, profile.BridgeKind) {
			s.Generation.AvailableBridges = append(s.Generation.AvailableBridges, profile.BridgeKind)
		}
	}
	slices.Sort(s.Generation.AvailableBridges)
	return true
}

func (s *session) inspectUpgradeCleanup(owner bootstrap.OwnerRuntimeStatus) {
	plan, err := s.PlanUpgradeCleanupFn(s.upgradeCleanupOptions(owner))
	if err != nil || !plan.NeedsCleanup {
		return
	}
	s.Artifacts.UpgradeCleanup = upgradeCleanupSummaryFromPlan(plan)
	s.block(ExitActionRequired, "legacy_upgrade_artifacts_present")
	s.action(readOnly, "repair", "upgrade", "--dry-run")
}

func (s *session) inspectHostConflicts(hosts []agents.DetectedHost) {
	for _, host := range hosts {
		if !host.Detected || hooks.CanonicalHost(host.Kind) == "" || !filepath.IsAbs(host.ProfileDir) ||
			s.has("agent") && s.get("agent") != host.Kind {
			continue
		}
		report, err := s.ScanConflictsFn(host, hookConflictPolicy)
		if err != nil {
			s.block(ExitNeedsReview, "connection_config_unreadable")
			continue
		}
		if report.Blocked {
			s.showHookConflict(host, report)
			continue
		}
		if len(report.ManagedLegacy) == 0 {
			continue
		}
		// Current managed entries also carry the Memlane marker.
		opts, ok := s.hostHookOptions(host)
		if ok {
			result, err := s.InstallHostConfigFn(opts)
			if result.Blocked || errors.Is(err, hooks.ErrConflictBlocked) {
				if result.Conflicts != nil {
					s.showHookConflict(host, *result.Conflicts)
				} else {
					s.block(ExitPreflightBlocked, "third_party_cognee_conflict")
				}
				continue
			}
			if err == nil && len(result.Previews) == 0 {
				continue
			}
		}
		for i := range s.Connections.Hosts {
			if s.Connections.Hosts[i].Kind == host.Kind {
				s.Connections.Hosts[i].NeedsHookRepair = true
			}
		}
		s.block(ExitActionRequired, "hook_repair_required")
		if host.Kind == agents.HostTraeIDE {
			s.action(readOnly, "repair", "trae-hooks", "--dry-run")
		} else {
			s.action(readOnly, "connect", "--agent", host.Kind, "--dry-run")
		}
	}
}

func (s *session) inspectHostHookTrust(baseCtx context.Context, hosts []agents.DetectedHost, controlCovered map[string]bool) {
	type probeTarget struct {
		host      agents.DetectedHost
		hookPaths []string
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	var targets []probeTarget
	for _, host := range hosts {
		if !host.Detected || (host.Kind != agents.HostCodex && host.Kind != agents.HostTraeX) || !filepath.IsAbs(host.ProfileDir) ||
			s.has("agent") && s.get("agent") != host.Kind {
			continue
		}
		if controlCovered[host.Kind] {
			continue
		}
		alreadyFlagged := false
		for _, h := range s.Connections.Hosts {
			if h.Kind == host.Kind && (h.NeedsHookRepair || h.NeedsHookTrust) {
				alreadyFlagged = true
				break
			}
		}
		if alreadyFlagged {
			continue
		}
		hookPaths, _ := agents.HookTrustConfigFiles(host.Kind, host.ProfileDir)
		if !agents.HasManagedHostHooks(host.Kind, host.ProfileDir, hookPaths) {
			continue
		}
		targets = append(targets, probeTarget{host: host, hookPaths: hookPaths})
	}
	if len(targets) == 0 {
		return
	}

	reports := make([]agents.HostHookTrustReport, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(idx int, t probeTarget) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(baseCtx, doctorHookTrustProbeTimeout)
			defer cancel()
			reports[idx] = s.ProbeHookTrustFn(probeCtx, t.host.Kind, t.host.Executable, t.host.ProfileDir, t.hookPaths)
		}(i, target)
	}
	wg.Wait()

	for i, target := range targets {
		report := reports[i]
		if !report.NeedsTrust() {
			continue
		}
		for j := range s.Connections.Hosts {
			if s.Connections.Hosts[j].Kind == target.host.Kind {
				s.Connections.Hosts[j].NeedsHookTrust = true
				s.Connections.Hosts[j].UntrustedHookCount = report.UntrustedCount
				s.Connections.Hosts[j].ModifiedHookCount = report.ModifiedCount
				s.Connections.Hosts[j].DisabledHookCount = report.DisabledCount
			}
		}
		s.hookTrustAdvice(target.host.Kind, report.UntrustedCount, report.ModifiedCount, report.DisabledCount)
	}
}
