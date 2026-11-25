package setupcli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/kit/agents"

	"github.com/zwhu/memlane/internal/controlplane"
)

var connectionStatuses = []string{
	"detected", "unprepared", "not_prepared", "not_connected", "not_configured", "not_detected", "unavailable", "component_unavailable",
	"prepared", "ready_to_activate", "requires_review", "preparation_unknown", "activation_unknown", "staging_unknown",
	"agent_creation_unconfirmed", "configured_pending_new_session", "environment_receipt_unconfirmed", "awaiting_generation_activation",
	"first_install_setup",
}

func knownBlocker(code string) string {
	switch code {
	case "revision_conflict", "host_configuration_changed", "host_configuration_requires_review",
		"reviewed_preview_required", "reviewed_prepared_connection_required", "connection_preview_changed",
		"codex_preview_changed", "unknown_host_kind", "connection_hosts_not_configured",
		"connection_operation_ambiguous_requires_review", "host_executable_changed",
		"verification_environment_not_allowed", "trae_ide_hook_changed_requires_review",
		"active_antigravity_upgrade_requires_review":
		return code
	default:
		return "connection_preflight_blocked"
	}
}

func (s *session) showConnections(dir controlplane.ConnectionsDirectory) {
	s.Connections.Revision = dir.Revision
	for _, row := range dir.Connections {
		if !isAgent(row.Kind) || s.has("agent") && s.get("agent") != row.Kind {
			continue
		}
		caps := map[string]capability{}
		for _, key := range []string{"explicit_memory", "auto_capture", "auto_recall", "project_policy"} {
			if value, ok := row.Capabilities[key]; ok {
				caps[key] = capability{value.Available, value.Verified}
			}
		}
		s.Connections.Hosts = append(s.Connections.Hosts, hostSummary{
			ID: safeInstanceID(row.ID), Kind: row.Kind, Status: known(row.Status, connectionStatuses...),
			Detected: row.Detected, NeedsHookRepair: row.NeedsHookRepair,
			AwaitingPostUpgradeReceipt: row.AwaitingPostUpgradeReceipt, Capabilities: caps,
		})
	}
}

func isProcessingCLI(kind string) bool {
	return oneOf(kind, "codex", "pi", "traex", "antigravity")
}

func (s *session) addUnconfiguredProcessingCLI(kind string) {
	if isProcessingCLI(kind) && !slices.Contains(s.Generation.AvailableBridges, kind) &&
		!slices.Contains(s.Generation.DetectedUnconfiguredCLIs, kind) {
		s.Generation.DetectedUnconfiguredCLIs = append(s.Generation.DetectedUnconfiguredCLIs, kind)
	}
}

func (s *session) showLocalHosts(hosts []agents.DetectedHost, offline, populateUnconfigured bool) {
	existing := map[string]bool{}
	for _, host := range s.Connections.Hosts {
		existing[host.Kind] = true
	}
	for _, host := range hosts {
		if !isAgent(host.Kind) {
			continue
		}
		if host.Detected && host.SupportsProcessingCLI && host.Executable != "" {
			if populateUnconfigured {
				s.addUnconfiguredProcessingCLI(host.Kind)
			} else if offline && !slices.Contains(s.Generation.AvailableBridges, host.Kind) {
				s.Generation.AvailableBridges = append(s.Generation.AvailableBridges, host.Kind)
			}
		}
		if existing[host.Kind] || s.has("agent") && s.get("agent") != host.Kind {
			continue
		}
		existing[host.Kind] = true
		s.Connections.Hosts = append(s.Connections.Hosts, hostSummary{Kind: host.Kind, Detected: host.Detected, Capabilities: map[string]capability{}})
	}
	if offline {
		slices.Sort(s.Generation.AvailableBridges)
	}
	slices.Sort(s.Generation.DetectedUnconfiguredCLIs)
}

func (s *session) pendingOperations() ([]controlplane.UIOperation, bool) {
	ops, err := controlplane.LoadUIOperations(s.opsDir)
	if err != nil {
		s.block(ExitNeedsReview, "pending_operation_journal_unreadable")
		return nil, false
	}
	s.Connections.Pending = s.Connections.Pending[:0]
	for _, op := range ops {
		summary := operationSummary{ID: op.Operation, Kind: op.Kind, Revision: op.Revision}
		if op.IsConnection() {
			summary.InstanceID = safeInstanceID(op.Profile)
		}
		s.Connections.Pending = append(s.Connections.Pending, summary)
	}
	return ops, true
}

func receiptSummary(id string, res controlplane.ConnectionOperationReceipt) *operationSummary {
	summary := &operationSummary{
		ID: id, InstanceID: safeInstanceID(res.InstanceID), Revision: res.Revision,
		Kind:         known(res.Action, "prepare", "activate", "allow_capture", "defer_capture", "repair_trae_hooks", "repair-trae-hooks", "review"),
		Reconcilable: res.Reconcilable, Phase: known(res.Phase, "pre_mutation", "post_mutation"), Status: known(res.Status, "unknown", "complete", "unrecorded"),
		Recorded: res.Recorded, SafeToUnlock: res.SafeToUnlock, AlreadyComplete: res.AlreadyComplete, Reconciled: res.Reconciled,
	}
	if res.Result != nil && res.Result.PreservedThirdPartyGroups > 0 {
		summary.PreservedThirdPartyGroups = res.Result.PreservedThirdPartyGroups
	}
	return summary
}

func previewReconcilable(res controlplane.ConnectionOperationReceipt) bool {
	return ((res.Recorded || res.Status == "unrecorded") && res.Reconcilable && res.Phase != "post_mutation") ||
		(res.Recorded && (res.SafeToUnlock || res.AlreadyComplete))
}

func (s *session) pendingAdvice(client *controlplane.Client, op controlplane.UIOperation, dir controlplane.ConnectionsDirectory) {
	res, err := client.PreviewReconcilePendingOperationFor(s.ctx, op.Operation, op.Profile, op.ConnectionAction())
	if err != nil || !previewReconcilable(res) {
		s.block(ExitNeedsReview, "pending_connection_operation_ambiguous")
		s.pendingReviewAdvice(op, dir)
		return
	}
	s.block(ExitActionRequired, "pending_connection_operation")
	s.action(readOnly, "repair", "pending-ops", "--operation-id", op.Operation, "--dry-run")
	if s.Environment.StoreMode != "external_readonly" {
		s.action(confirmation, "repair", "pending-ops", "--operation-id", op.Operation, "--apply")
	}
}

func (s *session) pendingReviewAdvice(op controlplane.UIOperation, dir controlplane.ConnectionsDirectory) {
	for _, row := range dir.Connections {
		if row.ID == op.Profile && isAgent(row.Kind) && safeInstanceID(row.ID) != "" && recoverableHostStatus(row.Status) {
			s.action(readOnly, "connect", "--agent", row.Kind, "--instance-id", row.ID, "--phase", "review", "--dry-run")
			s.reviewApplyAdvice(row)
			return
		}
	}
}

func unavailableConnection(row controlplane.AgentConnection) bool {
	return strings.HasSuffix(row.ID, "-unavailable") || oneOf(row.Status, "unavailable", "not_detected") ||
		(row.Kind == "codex" && !row.Detected && !row.AgentRegistered && unprepared(row.Status))
}

func (s *session) unavailableConnectionTarget(kind string) {
	s.block(ExitActionRequired, "connection_target_unavailable")

	steps := []string{}
	var recommendation bootstrap.RecommendedProcessingCLI
	for _, rec := range bootstrap.RecommendedProcessingCLIs() {
		if rec.Kind == kind {
			recommendation = rec
			break
		}
	}
	if recommendation.Kind != "" {
		if !slices.ContainsFunc(s.Generation.RecommendedProcessingCLIs, func(rec bootstrap.RecommendedProcessingCLI) bool {
			return rec.Kind == kind
		}) {
			s.Generation.RecommendedProcessingCLIs = append(s.Generation.RecommendedProcessingCLIs, recommendation)
		}
		install := "运行 " + recommendation.InstallCommand
		if recommendation.InstallCommand == "" {
			install = "按 " + recommendation.DocsURL + " 的说明安装"
		}
		steps = append(steps, "本机未检测到 "+recommendation.DisplayName+"。请"+install+"，安装完成后重新接入 Memlane 记忆采集。")
		if hint := recommendation.LoginHint; hint != "" {
			if !strings.HasSuffix(hint, "。") {
				hint += "。"
			}
			steps = append(steps, hint)
		}
	} else {
		name := kind
		switch kind {
		case "trae_ide":
			name = "TRAE CN"
		case "claude_code":
			name = "Claude Code"
		case "cursor":
			name = "Cursor"
		case "opencode":
			name = "OpenCode"
		}
		steps = append(steps, "本机未检测到 "+name+"。请先安装，再重新接入 Memlane 记忆采集。")
	}
	if isAgent(kind) && !s.has("config") && !s.has("component-root") {
		steps = append(steps, "安装完成后运行 connect --agent "+kind+" --dry-run --json 检查接入预览。")
	}
	for _, step := range steps {
		if !slices.Contains(s.HumanSteps, step) {
			s.HumanSteps = append(s.HumanSteps, step)
		}
	}
}

func (s *session) target(dir controlplane.ConnectionsDirectory, kind string) (controlplane.AgentConnection, bool) {
	var matches, unavailable []controlplane.AgentConnection
	for _, row := range dir.Connections {
		if row.Kind == kind && safeInstanceID(row.ID) != "" && (!s.has("instance-id") || s.get("instance-id") == row.ID) {
			if unavailableConnection(row) {
				unavailable = append(unavailable, row)
			} else {
				matches = append(matches, row)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], true
	case 0:
		if len(unavailable) > 0 || !s.has("instance-id") {
			s.unavailableConnectionTarget(kind)
		} else {
			s.block(ExitInvalidArguments, "unknown_connection_target")
		}
	default:
		s.block(ExitInvalidArguments, "connection_instance_required")
	}
	return controlplane.AgentConnection{}, false
}

func (s *session) connect() {
	kind, phase := s.get("agent"), s.get("phase")
	if hooks.CanonicalHost(kind) != "" && s.useHostHooks(kind) && (phase == "" || s.config.Control.PluginDir == "") {
		if oneOf(phase, "", "prepare", "activate", "review") {
			s.configureHostHooks(kind, false)
		} else {
			s.block(ExitInvalidArguments, "unsupported_connection_phase")
			s.action(readOnly, "connect", "--agent", kind, "--dry-run")
		}
		return
	}
	if s.config.Control.PluginDir == "" {
		s.unavailableConnectionTarget(kind)
		return
	}
	client := s.requireClient()
	if client == nil {
		return
	}
	captureReview := oneOf(phase, "allow-capture", "defer-capture")
	dir, err := client.Connections(s.ctx, captureReview)
	if err != nil {
		s.controlFailure(err)
		return
	}
	s.showConnections(dir)
	if phase == "select-env" {
		s.selectEnvironment(client, dir, kind)
		return
	}

	row, ok := s.target(dir, kind)
	if !ok {
		return
	}
	if phase == "review" {
		s.reviewConnection(client, dir, row)
		return
	}
	ops, ok := s.pendingOperations()
	if !ok {
		return
	}
	for _, op := range ops {
		if op.IsConnection() && op.Profile == row.ID {
			s.operation(op.Operation)
			s.pendingAdvice(client, op, dir)
			return
		}
	}
	if captureReview {
		s.connectCapture(client, dir, row, phase)
		return
	}
	if phase == "" && !s.has("reviewed-sha") && s.connectionVerified(row) {
		return
	}
	preview, ok := s.previewConnection(client, row.ID)
	if !ok {
		return
	}
	expected := preview.EffectiveProposedSHA()
	reviewed := s.get("reviewed-sha")
	if reviewed != "" && expected != "" && reviewed != expected {
		s.block(ExitPreflightBlocked, "reviewed_sha_mismatch")
		return
	}
	if phase == "" && s.connectionVerified(row) {
		return
	}
	if !s.has("apply") {
		s.block(ExitActionRequired, "connection_confirmation_required")
		if expected == "" && phase == "" && unprepared(row.Status) {
			phase = "prepare"
		}
		s.connectAdvice(row, phase, s.Artifacts.ReviewedSHA)
		return
	}
	if phase == "prepare" || phase == "" && unprepared(row.Status) {
		reviewedBaseline := preview.BaselineSHA256
		prepared, ok := s.connectionMutation(client, row.ID, "prepare", preview.Revision, "")
		if !ok {
			return
		}
		if phase == "prepare" {
			s.action(readOnly, "connect", "--agent", kind, "--instance-id", row.ID, "--phase", "activate", "--dry-run")
			return
		}
		preview, ok = s.previewConnection(client, row.ID)
		if !ok {
			return
		}
		if preview.BaselineSHA256 != reviewedBaseline {
			s.block(ExitPreflightBlocked, "host_configuration_changed")
			s.connectAdvice(row, "activate", s.Artifacts.ReviewedSHA)
			return
		}
		if expected := preview.EffectiveProposedSHA(); expected == "" || expected != reviewed {
			s.block(ExitPreflightBlocked, "reviewed_sha_mismatch")
			s.connectAdvice(row, "activate", s.Artifacts.ReviewedSHA)
			return
		}
		s.Connections.Revision = prepared.Revision
	}
	if phase == "" {
		if !unprepared(row.Status) && !oneOf(row.Status, "prepared", "ready_to_activate", "requires_review") {
			s.connectionOutcome(row, false)
			return
		}
		phase = "activate"
	}
	result, ok := s.connectionMutation(client, row.ID, phase, preview.Revision, reviewed)
	if !ok {
		return
	}
	s.showConnectionResult(row, result, phase == "activate")
}

func (s *session) reviewConnection(client *controlplane.Client, dir controlplane.ConnectionsDirectory, row controlplane.AgentConnection) {
	ops, ok := s.pendingOperations()
	if !ok {
		return
	}
	ops = slices.DeleteFunc(ops, func(op controlplane.UIOperation) bool {
		return !op.IsConnection() || op.Profile != row.ID
	})
	if !s.has("apply") {
		if recoverableHostStatus(row.Status) || len(ops) != 0 {
			s.block(ExitActionRequired, "connection_review_required")
			s.reviewApplyAdvice(row)
		}
		return
	}
	id := controlplane.NewCLIOperationID()
	s.operation(id)
	result, err := client.ReviewConnection(s.ctx, row.ID, dir.Revision, id)
	if err != nil {
		var controlErr *controlplane.ControlError
		if preMutation(err) || revisionConflict(err) || errors.As(err, &controlErr) && controlErr.IsAuthDenied() {
			s.controlFailure(err)
		} else {
			s.block(ExitNeedsReview, "connection_review_unconfirmed")
		}
		return
	}
	s.showConnectionResult(row, result, false)
	for _, op := range ops {
		receipt, err := client.ConnectionOperation(s.ctx, op.Operation)
		if err != nil || !terminalReceiptMatches(receipt, op) {
			s.block(ExitNeedsReview, "connection_operation_unconfirmed")
			continue
		}
		s.showHookRepairFromReceipt(receipt)
		if err := controlplane.FinishUIOperation(s.opsDir, op.Operation); err != nil {
			s.block(ExitNeedsReview, "connection_operation_unconfirmed")
			continue
		}
		s.Connections.Pending = slices.DeleteFunc(s.Connections.Pending, func(summary operationSummary) bool {
			return summary.ID == op.Operation
		})
	}
}

func terminalReceiptMatches(receipt controlplane.ConnectionOperationReceipt, op controlplane.UIOperation) bool {
	return receipt.Recorded && receipt.Status == "complete" &&
		receipt.OperationID != "" && receipt.OperationID == op.Operation &&
		receipt.InstanceID != "" && receipt.InstanceID == op.Profile &&
		receipt.Action != "" && strings.ReplaceAll(receipt.Action, "-", "_") == strings.ReplaceAll(op.ConnectionAction(), "-", "_")
}

func (s *session) reviewApplyAdvice(row controlplane.AgentConnection) {
	if s.Environment.StoreMode != "external_readonly" {
		s.action(confirmation, "connect", "--agent", row.Kind, "--instance-id", row.ID, "--phase", "review", "--apply")
	}
}

func (s *session) selectEnvironment(client *controlplane.Client, dir controlplane.ConnectionsDirectory, kind string) {
	row := controlplane.AgentConnection{Kind: kind}
	if s.has("instance-id") {
		var ok bool
		row, ok = s.target(dir, kind)
		if !ok {
			return
		}
	}
	profile := s.get("profile")
	if profile != "" {
		var err error
		profile, err = resolveSafePath(profile)
		if err != nil {
			s.block(ExitFatal, "unsafe_local_path")
			return
		}
	}
	if !s.has("apply") {
		s.block(ExitActionRequired, "connection_confirmation_required")
		s.connectAdvice(row, "select-env", profile)
		return
	}
	res, err := client.SelectEnvironment(s.ctx, kind, profile, dir.Revision)
	if err != nil {
		if preMutation(err) || revisionConflict(err) {
			s.controlFailure(err)
		} else {
			s.block(ExitNeedsReview, "environment_selection_unconfirmed")
		}
		return
	}
	s.Connections.Hosts = s.Connections.Hosts[:0]
	s.showConnections(res)
}

func (s *session) connectCapture(client *controlplane.Client, dir controlplane.ConnectionsDirectory, row controlplane.AgentConnection, phase string) {
	var captureSHA string
	if row.Capture != nil {
		captureSHA = safeHash(row.Capture.ReviewSHA, false)
	}
	s.Artifacts.ReviewedSHA = captureSHA
	if !s.has("apply") {
		if captureSHA == "" {
			s.block(ExitPreflightBlocked, "capture_review_unavailable")
			return
		}
		s.block(ExitActionRequired, "connection_confirmation_required")
		s.connectAdvice(row, phase, captureSHA)
		return
	}
	if captureSHA == "" || s.get("reviewed-sha") != captureSHA {
		s.block(ExitPreflightBlocked, "reviewed_sha_mismatch")
		return
	}
	result, ok := s.connectionMutation(client, row.ID, phase, dir.Revision, captureSHA)
	if !ok {
		return
	}
	s.showConnectionResult(row, result, phase == "allow-capture")
}

func (s *session) showConnectionResult(row controlplane.AgentConnection, result controlplane.ConnectionActionResult, reportOutcome bool) {
	s.Connections.Hosts = s.Connections.Hosts[:0]
	if result.Connection.Kind == "" {
		result.Connection.Kind = row.Kind
	}
	if result.Connection.ID == "" {
		result.Connection.ID = row.ID
	}
	s.showConnections(controlplane.ConnectionsDirectory{Revision: result.Revision, Connections: []controlplane.AgentConnection{result.Connection}})
	if reportOutcome {
		s.connectionOutcome(result.Connection, true)
	}
}

func (s *session) previewConnection(client *controlplane.Client, id string) (controlplane.ConnectionPreview, bool) {
	preview, err := client.PreviewConnection(s.ctx, id)
	if err != nil {
		s.controlFailure(err)
		return preview, false
	}
	s.Artifacts.ReviewedSHA = safeHash(preview.EffectiveProposedSHA(), false)
	if preview.NeedsHookRepair {
		s.hookAdvice(id)
		return preview, false
	}
	blocked := map[string]bool{}
	for _, item := range preview.BlockerDetails {
		blocked[knownBlocker(item.Code)] = true
	}
	for _, code := range preview.Blockers {
		blocked[knownBlocker(code)] = true
	}
	for _, code := range slices.Sorted(maps.Keys(blocked)) {
		s.block(ExitPreflightBlocked, code)
	}
	return preview, len(blocked) == 0
}

func (s *session) connectAdvice(row controlplane.AgentConnection, phase, value string) {
	args := []string{"connect", "--agent", row.Kind, "--apply"}
	if row.ID != "" {
		args = append(args, "--instance-id", row.ID)
	}
	if phase != "" {
		args = append(args, "--phase", phase)
	}
	switch {
	case phase == "select-env":
		if value == "" || SanitizeString(value, "") != value {
			s.HumanSteps = append(s.HumanSteps, "选择要接入记忆采集的宿主配置目录，再将其绝对路径传给 --profile。")
			return
		}
		args = append(args, "--profile", value)
	case value != "":
		args = append(args, "--reviewed-sha", value)
	case phase != "prepare":
		return
	}
	s.action(confirmation, args...)
}

func (s *session) connectionMutation(client *controlplane.Client, id, phase string, rev int64, sha string) (controlplane.ConnectionActionResult, bool) {
	var res controlplane.ConnectionActionResult
	kind := "connection_" + strings.ReplaceAll(phase, "-", "_")
	ok := s.recordMutation(client, id, kind, rev, func(opID string) error {
		var err error
		switch phase {
		case "prepare":
			res, err = client.PrepareConnection(s.ctx, id, rev, opID)
		case "activate":
			res, err = client.ActivateConnection(s.ctx, id, rev, opID, sha)
		case "allow-capture":
			res, err = client.AllowCapture(s.ctx, id, rev, opID, sha)
		case "defer-capture":
			res, err = client.DeferCapture(s.ctx, id, rev, opID, sha)
		}
		return err
	})
	return res, ok
}

func (s *session) recordMutation(client *controlplane.Client, id, kind string, rev int64, call func(string) error) bool {
	opID := controlplane.NewCLIOperationID()
	op := controlplane.UIOperation{Operation: opID, Kind: kind, Profile: id, Revision: rev}
	s.operation(opID)
	if s.opsDir == "" || checkPath(s.opsDir) != nil {
		s.block(ExitNeedsReview, "pending_operation_journal_unavailable")
		return false
	}
	if _, err := controlplane.RecordUIOperation(s.opsDir, op); err != nil {
		s.block(ExitNeedsReview, "connection_operation_record_failed")
		return false
	}
	err := call(opID)
	confirmedRejection := false
	if preMutation(err) {
		receipt, queryErr := client.ConnectionOperation(s.ctx, opID)
		confirmedRejection = queryErr == nil && terminalReceiptMatches(receipt, op) && receipt.Outcome == "rejected"
	}
	if err == nil || confirmedRejection {
		if finishErr := controlplane.FinishUIOperation(s.opsDir, opID); finishErr != nil {
			s.block(ExitNeedsReview, "connection_operation_cleanup_failed")
			return false
		}
	}
	switch {
	case err == nil:
		return true
	case confirmedRejection:
		s.controlFailure(err)
	default:
		s.block(ExitNeedsReview, "connection_operation_unconfirmed")
		s.action(readOnly, "repair", "pending-ops", "--operation-id", opID, "--dry-run")
	}
	return false
}

func unprepared(status string) bool {
	return oneOf(status, "", "detected", "unprepared", "not_prepared", "not_connected", "not_configured")
}

func unknownConnection(status string) bool {
	return recoverableHostStatus(status) || oneOf(status, "staging_unknown", "agent_creation_unconfirmed")
}

func recoverableHostStatus(status string) bool {
	return oneOf(status, "activation_unknown", "preparation_unknown", "requires_review")
}

func captureMonitoredHost(kind string) bool {
	return oneOf(kind, "codex", "pi", "traex", "trae_ide", "antigravity")
}

func (s *session) activationPending(row controlplane.AgentConnection) bool {
	if s.connectionVerified(row) {
		return false
	}
	return row.AwaitingPostUpgradeReceipt || oneOf(row.Status, "configured_pending_new_session", "environment_receipt_unconfirmed",
		"first_install_setup", "awaiting_generation_activation") ||
		row.Capture != nil && oneOf(row.Capture.State, "awaiting_generation_activation", "waiting_session",
			"waiting_for_first_session", "awaiting_first_session", "permission_pending", "check_failed")
}

func (s *session) connectionVerified(row controlplane.AgentConnection) bool {
	if s.Environment.StoreMode == "first_install_setup" || row.NeedsHookRepair || row.AwaitingPostUpgradeReceipt || row.Status != "configured_pending_new_session" {
		return false
	}
	if captureMonitoredHost(row.Kind) {
		return row.Capture != nil && row.Capture.State == "healthy"
	}
	return row.NewSessions > 0 && (row.Kind == "opencode" || row.EnvironmentVerified)
}

func (s *session) connectionOutcome(row controlplane.AgentConnection, applied bool) {
	switch {
	case row.NeedsHookRepair:
		s.hookAdvice(row.ID)
	case unknownConnection(row.Status):
		s.block(ExitNeedsReview, "connection_state_unknown")
	case unavailableConnection(row):
		s.unavailableConnectionTarget(row.Kind)
	case s.connectionVerified(row):
	case applied || s.activationPending(row):
		s.block(ExitHumanRequired, "connection_receipt_required")
		s.reloadSteps(row.Kind)
	default:
		s.block(ExitActionRequired, "connection_not_activated")
		if isAgent(row.Kind) {
			s.action(readOnly, "connect", "--agent", row.Kind, "--dry-run")
		}
	}
}

func (s *session) reloadSteps(kind string) {
	steps := []string{"完全退出宿主应用（⌘Q）并重新打开。终端宿主请退出后重新启动。"}
	if kind == "trae_ide" {
		steps = append(steps, "在 TRAE 的「设置 → Hooks」中确认「全局开关」已开启。")
	}
	steps = append(steps, "在已确认的项目中新建会话，执行一次工具调用，等待记忆采集回执。")
	if s.Environment.StoreMode == "first_install_setup" {
		steps = append(steps, "在 Memlane 桌面端完成提炼模型配置并启用，再验证记忆采集。")
	}
	for _, step := range steps {
		if !slices.Contains(s.HumanSteps, step) {
			s.HumanSteps = append(s.HumanSteps, step)
		}
	}
	if isAgent(kind) {
		s.action(readOnly, "verify", "--stage", "connection", "--agent", kind)
		if !s.has("config") && !s.has("component-root") {
			step := "运行 verify --stage connection --agent " + kind + " --json 检查记忆采集回执。"
			if !slices.Contains(s.HumanSteps, step) {
				s.HumanSteps = append(s.HumanSteps, step)
			}
		}
	}
}

func (s *session) hookAdvice(id string) {
	s.block(ExitActionRequired, "trae_hooks_repair_required")
	if id := safeInstanceID(id); id != "" {
		s.action(readOnly, "repair", "trae-hooks", "--instance-id", id, "--dry-run")
	}
}

func revisionConflict(err error) bool {
	var controlErr *controlplane.ControlError
	return errors.As(err, &controlErr) && controlErr.IsCode(409, "revision_conflict")
}

func (s *session) generation(client *controlplane.Client) bool {
	dir, err := client.GenerationState(s.ctx)
	if err != nil {
		s.controlFailure(err)
		return false
	}
	if dir.Active != nil {
		s.Generation.Active = &generationReference{Revision: dir.Active.Revision}
	}
	for key, profile := range dir.AgentBridges {
		kind := profile.BridgeKind
		if kind == "" {
			kind = key
		}
		if isAgent(kind) && !slices.Contains(s.Generation.AvailableBridges, kind) {
			s.Generation.AvailableBridges = append(s.Generation.AvailableBridges, kind)
		}
	}
	slices.Sort(s.Generation.AvailableBridges)
	for _, kind := range dir.DetectedProcessingCLIs {
		s.addUnconfiguredProcessingCLI(kind)
	}
	return true
}

var hookConflictPolicy = agents.ConflictPolicy{
	ManagedMarkers:   []string{"memlane-cognee", "memlane_cognee", "memlane-memory", "memlane hook", "memlane", "managed-marketplaces/trae_ide-"},
	ConflictKeywords: []string{"cognee"},
}

func (s *session) localHosts() []agents.DetectedHost {
	return s.DetectAgentsFn(agents.DiscoveryOptions{Home: s.HomeDir, Environ: s.Environ})
}

func (s *session) detectedHost(kind string) (agents.DetectedHost, bool) {
	for _, host := range s.localHosts() {
		if host.Kind == kind && host.Detected && filepath.IsAbs(host.ProfileDir) {
			return host, true
		}
	}
	return agents.DetectedHost{}, false
}

func (s *session) useHostHooks(kind string) bool {
	if s.config.Control.PluginDir == "" || s.has("profile") {
		return true
	}
	_, ok := s.detectedHost(kind)
	return ok
}

func (s *session) showHookConflict(host agents.DetectedHost, report agents.ConflictReport) {
	s.block(ExitPreflightBlocked, "third_party_cognee_conflict")
	for _, item := range report.ThirdPartyConflicts {
		surface := known(item.Surface, "hook", "plugin", "mcp_server", "extension", "package")
		name := "名称已隐藏"
		if conflictEntryName.MatchString(item.EntryName) &&
			strings.Contains(strings.ToLower(item.EntryName), "cognee") && SanitizeString(item.EntryName, "") == item.EntryName {
			name = item.EntryName
		}
		s.HumanSteps = append(s.HumanSteps, fmt.Sprintf("%s 的 %s 项 %s 与 Memlane 接入冲突。", host.Kind, surface, name))
	}
	s.HumanSteps = append(s.HumanSteps, "在宿主配置中检查并停用第三方 Cognee 接入，然后重新运行预览。")
}

var conflictEntryName = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,48}$`)

func (s *session) hostHookOptions(host agents.DetectedHost) (hooks.InstallOptions, bool) {
	root := s.config.Root
	if !filepath.IsAbs(root) || checkPath(root) != nil || hooks.ValidateGatewayURL(s.config.Control.URL) != nil {
		return hooks.InstallOptions{}, false
	}
	sum := sha256.Sum256([]byte(host.ProfileDir))
	keyName := host.Kind + "-" + hex.EncodeToString(sum[:12])
	return hooks.InstallOptions{
		Host: host.Kind, ProfileDir: host.ProfileDir, BinaryPath: s.Executable,
		GatewayURL: s.config.Control.URL, KeyFile: filepath.Join(root, "agent-keys", keyName+".token"),
		ConflictPolicy: &hookConflictPolicy,
	}, true
}

func (s *session) previewHostHooks(host agents.DetectedHost) (hooks.InstallOptions, hooks.InstallResult, bool) {
	opts, ok := s.hostHookOptions(host)
	if !ok {
		s.block(ExitActionRequired, "runtime_config_required")
		s.action(readOnly, "install", "--plan")
		return opts, hooks.InstallResult{}, false
	}
	report, err := s.ScanConflictsFn(host, hookConflictPolicy)
	if err != nil {
		s.block(ExitNeedsReview, "connection_config_unreadable")
		return opts, hooks.InstallResult{}, false
	}
	if report.Blocked {
		s.showHookConflict(host, report)
		return opts, hooks.InstallResult{}, false
	}
	res, err := s.InstallHostConfigFn(opts)
	if !s.hookResultOK(host, res, err) {
		return opts, res, false
	}
	res.Conflicts = &report
	return opts, res, true
}

func (s *session) hookResultOK(host agents.DetectedHost, result hooks.InstallResult, err error) bool {
	if result.Blocked || errors.Is(err, hooks.ErrConflictBlocked) {
		var report agents.ConflictReport
		if result.Conflicts != nil {
			report = *result.Conflicts
		}
		s.showHookConflict(host, report)
		return false
	}
	if err != nil {
		s.block(ExitNeedsReview, "connection_config_unreadable")
		return false
	}
	return true
}

func hookPreviewSHA(previews []agents.EditPreview) string {
	type digest struct{ Path, Before, After string }
	rows := make([]digest, 0, len(previews))
	for _, p := range previews {
		rows = append(rows, digest{p.Path, p.BeforeSHA256, p.AfterSHA256})
	}
	slices.SortFunc(rows, func(a, b digest) int {
		for _, comparison := range []int{strings.Compare(a.Path, b.Path), strings.Compare(a.Before, b.Before), strings.Compare(a.After, b.After)} {
			if comparison != 0 {
				return comparison
			}
		}
		return 0
	})
	data, _ := json.Marshal(rows)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *session) configureHostHooks(kind string, repair bool) {
	host, ok := s.detectedHost(kind)
	if profile := s.get("profile"); profile != "" {
		resolved, err := resolveSafePath(profile)
		if err != nil {
			s.block(ExitFatal, "unsafe_local_path")
			return
		}
		host, ok = agents.DetectedHost{Kind: kind, ProfileDir: resolved, Detected: true}, true
	}
	if !ok {
		s.unavailableConnectionTarget(kind)
		return
	}
	opts, res, ok := s.previewHostHooks(host)
	if !ok {
		return
	}
	sha := hookPreviewSHA(res.Previews)
	s.Artifacts.ReviewedSHA = sha
	needsEdit := len(res.Previews) != 0
	if repair {
		s.Artifacts.HookRepair = &hookRepairSummary{
			NeedsHookRepair: needsEdit, Repairable: true, PreservedThirdPartyGroups: res.Conflicts.UnrelatedCount,
		}
	}
	if s.has("apply") && s.get("reviewed-sha") != sha {
		s.block(ExitPreflightBlocked, "reviewed_sha_mismatch")
		return
	}
	if !s.has("apply") {
		if needsEdit {
			code, args := "connection_confirmation_required", []string{"connect", "--agent", kind}
			if repair {
				code, args = "trae_hooks_repair_required", []string{"repair", "trae-hooks"}
			}
			s.block(ExitActionRequired, code)
			if s.has("profile") {
				if SanitizeString(s.get("profile"), "") != s.get("profile") {
					s.HumanSteps = append(s.HumanSteps, "使用原 --profile 参数和本次 reviewed_sha 重新运行 --apply。")
					return
				}
				args = append(args, "--profile", s.get("profile"))
			}
			s.action(confirmation, append(args, "--apply", "--reviewed-sha", sha)...)
		}
		return
	}
	if err := ensureAgentKey(opts.KeyFile); err != nil {
		s.block(ExitFatal, "agent_key_unavailable")
		return
	}
	opts.Apply = true
	applied, err := s.InstallHostConfigFn(opts)
	if !s.hookResultOK(host, applied, err) {
		return
	}
	if repair {
		s.Artifacts.HookRepair.Repaired = needsEdit
		s.Artifacts.HookRepair.NeedsHookRepair = false
	}
	registered := s.registerHookAgent(host, opts.KeyFile)
	if needsEdit || !registered {
		s.block(ExitHumanRequired, "connection_receipt_required")
		s.reloadSteps(kind)
	}
}

// Registration grants the Agent a principal with default access set to none.
func (s *session) registerHookAgent(host agents.DetectedHost, keyFile string) bool {
	owner, ok := s.inspectOwner()
	if !ok {
		return false
	}
	client := s.liveClient(owner)
	if client == nil {
		s.HumanSteps = append(s.HumanSteps, "启动 Memlane 服务后，重新预览并确认接入，以登记 Agent 身份。")
		return false
	}
	id := strings.TrimSuffix(filepath.Base(keyFile), ".token")
	var directory struct {
		Agents []struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			Archived  bool   `json:"archived"`
			RetiredAt string `json:"retired_at"`
		} `json:"registered_agents"`
	}
	if err := client.Request(s.ctx, "GET", "/api/agents", nil, &directory); err != nil {
		return false
	}
	for _, row := range directory.Agents {
		if row.ID == id {
			return row.Kind == host.Kind && !row.Archived && row.RetiredAt == ""
		}
	}
	err := client.Request(s.ctx, "POST", "/api/agents", map[string]string{
		"id": id, "kind": host.Kind, "key_file": keyFile, "host_profile": host.ProfileDir,
	}, nil)
	if err != nil {
		s.HumanSteps = append(s.HumanSteps, "Memlane 尚未确认 Agent 登记，请检查服务后重新预览接入。")
	}
	return false
}

func ensureAgentKey(path string) error {
	if err := checkPath(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		_, readErr := hooks.ReadPrivateKey(path)
		return readErr
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var secret [48]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".agent-key-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.WriteString(base64.RawURLEncoding.EncodeToString(secret[:])); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	_, err = hooks.ReadPrivateKey(path)
	return err
}

func (s *session) verifyHostHooks() {
	if !s.loadSelections() {
		return
	}
	targets := s.Environment.SelectedAgents
	if kind := s.get("agent"); kind != "" {
		targets = []string{kind}
	} else if len(targets) == 0 {
		for _, host := range s.localHosts() {
			if host.Detected && hooks.CanonicalHost(host.Kind) != "" {
				targets = append(targets, host.Kind)
			}
		}
	}
	if len(targets) == 0 {
		s.block(ExitActionRequired, "connection_target_unavailable")
	}
	for _, kind := range targets {
		host, ok := s.detectedHost(kind)
		if !ok || hooks.CanonicalHost(kind) == "" {
			s.unavailableConnectionTarget(kind)
			continue
		}
		_, result, ok := s.previewHostHooks(host)
		if !ok {
			continue
		}
		s.Connections.Hosts = append(s.Connections.Hosts, hostSummary{
			Kind: kind, Detected: true, NeedsHookRepair: len(result.Previews) != 0,
		})
		if len(result.Previews) != 0 {
			s.block(ExitActionRequired, "connection_confirmation_required")
			s.action(readOnly, "connect", "--agent", kind, "--dry-run")
		}
	}
}
