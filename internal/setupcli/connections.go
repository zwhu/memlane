package setupcli

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/zwhu/memlane/internal/bootstrap"

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

func (s *session) showLocalHosts(offline, populateUnconfigured bool) {
	existing := map[string]bool{}
	for _, host := range s.Connections.Hosts {
		existing[host.Kind] = true
	}
	for _, host := range s.DetectHostsFn(bootstrap.HostDetectOptions{Home: s.HomeDir, Environ: s.Environ}) {
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
	client := s.requireClient()
	if client == nil {
		return
	}
	kind, phase := s.get("agent"), s.get("phase")
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
