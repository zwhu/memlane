package settings

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

type onboardingConnectionState struct {
	Skipped                 map[string]bool
	PreviewOpen             map[string]bool
	displayedLegacyPreviews map[string]controlplane.ConnectionPreview
	Writing                 string
	StopBatch               bool
	BatchRunning            bool
}

func (a *Controller) onboardingConnectionRows() []AgentConnection {
	var rows []AgentConnection
	for _, kind := range a.environment.SelectedAgents {
		i := slices.IndexFunc(a.integrations.Connections.Connections, func(row AgentConnection) bool { return row.Kind == kind })
		if i < 0 {
			rows = append(rows, AgentConnection{Kind: kind})
			continue
		}
		rows = append(rows, a.integrations.Connections.Connections[i])
	}
	return rows
}

func (a *Controller) onboardingConnectionStatus(row AgentConnection) (string, string) {
	s := &a.integrations
	needsAttention := s.pendingConnection(row.ID) != nil || connectionNeedsReview(row) ||
		needsHookRepair(row) || s.ConnectionPreviews[row.ID].NeedsHookRepair
	switch {
	case row.ID != "" && a.onboardingConnection.Writing == row.ID:
		return "写入中", "accent"
	case !needsAttention && captureVerifiedHealthy(row):
		return "已生效", "ok"
	case !needsAttention && (row.Status == "configured_pending_new_session" || row.Status == "environment_receipt_unconfirmed"):
		return "待新会话", "accent"
	case a.onboardingConnection.Skipped[row.Kind]:
		return "已跳过", "plain"
	case s.pendingConnection(row.ID) != nil, connectionNeedsReview(row):
		return "待核对", "warn"
	case needsHookRepair(row), s.ConnectionPreviews[row.ID].NeedsHookRepair:
		return "待修复", "warn"
	default:
		return "待接入", ""
	}
}

func (a *Controller) onboardingConnectionPreview(row AgentConnection) (controlplane.ConnectionPreview, bool) {
	s := &a.integrations
	p, ok := s.ConnectionPreviews[row.ID]
	eligible := row.ID != "" && p.InstanceID == row.ID && p.Kind == row.Kind && p.Target != "" && p.OK &&
		!p.NeedsHookRepair && len(p.Blockers) == 0 && len(p.BlockerDetails) == 0 &&
		!needsHookRepair(row) && !connectionNeedsReview(row) && s.pendingConnection(row.ID) == nil &&
		(connectionActionAllowed(row, "prepare") || row.Status == "prepared") && previewEligibleForConfirm(p)
	return p, ok && eligible
}

func (a *Controller) acceptConnectionsDirectory(directory ConnectionsDirectory) {
	s := &a.integrations
	s.Connections = directory
	for _, row := range directory.Connections {
		needsAttention := s.pendingConnection(row.ID) != nil || connectionNeedsReview(row) ||
			needsHookRepair(row) || s.ConnectionPreviews[row.ID].NeedsHookRepair
		if !needsAttention && (captureVerifiedHealthy(row) || row.Status == "configured_pending_new_session" || row.Status == "environment_receipt_unconfirmed") {
			delete(a.onboardingConnection.Skipped, row.Kind)
		}
	}
}

func (s *onboardingConnectionState) previewNeedsDisplay(p controlplane.ConnectionPreview) bool {
	shown, ok := s.displayedLegacyPreviews[p.InstanceID]
	return len(p.DisabledPlugins) != 0 && (!ok || !sameReviewedOnboardingPreview(shown, p))
}

func (a *Controller) onboardingConnectionBatch(rows []AgentConnection) (int, []controlplane.ConnectionPreview) {
	pending := 0
	unseen := false
	var previews []controlplane.ConnectionPreview
	for _, row := range rows {
		label, _ := a.onboardingConnectionStatus(row)
		switch label {
		case "已跳过", "已生效", "待新会话":
			continue
		}
		pending++
		p, eligible := a.onboardingConnectionPreview(row)
		if eligible && a.onboardingConnection.previewNeedsDisplay(p) {
			unseen = true
		}
		if eligible && p.Kind != "opencode" && p.EffectiveProposedSHA() != "" {
			previews = append(previews, p)
		}
	}
	if unseen {
		return pending, nil
	}
	return pending, previews
}

func (a *Controller) onboardingConnectionBusy() bool {
	s := &a.environment
	return !s.RuntimeStarted || s.staleRuntimeOwner() || s.ExternalWorkerActive || s.Busy ||
		s.RestartingStaleDaemon || a.integrations.Loading || a.integrations.Busy
}

func (a *Controller) onboardingConnectAction() (string, bool, func()) {
	rows := a.onboardingConnectionRows()
	pending, previews := a.onboardingConnectionBatch(rows)
	busy := a.onboardingConnectionBusy() || a.environment.HostsLoading || (len(rows) > 0 && !a.integrations.ConnectionsLoaded && !a.integrations.Loaded)
	if a.onboardingConnection.BatchRunning || a.onboardingConnection.Writing != "" {
		return "接入中…", true, nil
	}
	if pending == 0 {
		return "继续", busy, a.showOnboardingProcess
	}
	label := "接入其余"
	if len(rows) == len(previews) {
		label = "全部接入"
	}
	if len(previews) == 0 {
		return "继续", true, nil
	}
	return fmt.Sprintf("%s（%d）", label, len(previews)), busy, func() { a.runOnboardingConnectionBatch() }
}

func (a *Controller) onboardingConnectHint() string {
	pending, previews := a.onboardingConnectionBatch(a.onboardingConnectionRows())
	if len(previews) > 0 || a.onboardingConnection.BatchRunning {
		return "写入前会自动备份原配置文件"
	}
	if pending != 0 && !a.onboardingConnectionBusy() {
		return "请在对应行处理待确认项，也可以稍后接入"
	}
	return ""
}

func (a *Controller) canDeferOnboardingConnections() bool {
	pending, _ := a.onboardingConnectionBatch(a.onboardingConnectionRows())
	return pending != 0 && !a.onboardingConnectionBusy()
}

func (a *Controller) skipOnboardingConnection(kind string) {
	if a.onboardingConnectionBusy() {
		return
	}
	if a.onboardingConnection.Skipped == nil {
		a.onboardingConnection.Skipped = make(map[string]bool)
	}
	a.onboardingConnection.Skipped[kind] = true
}

func (a *Controller) deferOnboardingConnections() {
	if !a.canDeferOnboardingConnections() {
		return
	}
	for _, row := range a.onboardingConnectionRows() {
		status, _ := a.onboardingConnectionStatus(row)
		if status != "已生效" && status != "待新会话" {
			a.skipOnboardingConnection(row.Kind)
		}
	}
	a.showOnboardingProcess()
}

func sameReviewedOnboardingPreview(previous, current controlplane.ConnectionPreview) bool {
	equalBool := func(a, b *bool) bool {
		return a == nil && b == nil || a != nil && b != nil && *a == *b
	}
	return current.OK && !current.NeedsHookRepair && len(current.Blockers) == 0 && len(current.BlockerDetails) == 0 &&
		current.InstanceID == previous.InstanceID && current.Kind == previous.Kind && current.Status == previous.Status &&
		current.Target == previous.Target && current.BaselineSHA256 == previous.BaselineSHA256 && current.PlanDigest == previous.PlanDigest &&
		current.EffectiveProposedSHA() == previous.EffectiveProposedSHA() &&
		current.Summary == previous.Summary && current.DiffSummary == previous.DiffSummary &&
		current.BackupPlan == previous.BackupPlan && current.EnabledPlugin == previous.EnabledPlugin &&
		equalBool(current.RequiresReload, previous.RequiresReload) &&
		equalBool(current.RequiresHostTrust, previous.RequiresHostTrust) &&
		equalBool(current.PreserveOtherPlugins, previous.PreserveOtherPlugins) &&
		slices.Equal(current.DisabledPlugins, previous.DisabledPlugins)
}

func refreshReviewedOnboardingPreview(ctx context.Context, client ProjectControlClient, reviewed controlplane.ConnectionPreview) (controlplane.ConnectionPreview, error) {
	current, err := client.controlPlaneClient().PreviewConnection(ctx, reviewed.InstanceID)
	if err == nil && !sameReviewedOnboardingPreview(reviewed, current) {
		err = fmt.Errorf("%s 的配置变更预览已更新，请重新核对", connectionName(reviewed.Kind))
	}
	return current, err
}

func (a *Controller) runOnboardingConnectionBatch() {
	client, ok := a.accessClient()
	if !ok || !a.canDispatch() || a.onboardingConnectionBusy() {
		return
	}
	_, previews := a.onboardingConnectionBatch(a.onboardingConnectionRows())
	if len(previews) == 0 {
		return
	}
	s, flow := &a.integrations, &a.onboardingConnection
	s.Busy, s.Error, s.Message = true, "", ""
	flow.BatchRunning, flow.StopBatch = true, false
	root := a.generationOperationsDir()
	a.runNextOnboardingConnection(client, root, previews, 0)
}

// Resume through the UI dispatcher after each ordered receipt so cancellation
// can stop before the next host, while the current host finishes safely.
func (a *Controller) runNextOnboardingConnection(client ProjectControlClient, root string, previews []controlplane.ConnectionPreview, completed int) {
	s, flow := &a.integrations, &a.onboardingConnection
	if len(previews) == 0 || flow.StopBatch {
		flow.BatchRunning, flow.Writing, s.Busy = false, "", false
		if completed > 0 {
			a.showOnboardingToast(fmt.Sprintf("已接入 %d 个 Agent，原配置已备份", completed))
		}
		a.RefreshProjectDirectory()
		return
	}
	previous := previews[0]
	flow.Writing = previous.InstanceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		current, err := refreshReviewedOnboardingPreview(ctx, client, previous)
		var latestPreview *controlplane.ConnectionPreview
		if current.InstanceID == previous.InstanceID {
			latestPreview = &current
		}
		if err == nil {
			_, refreshed, applyErr := applyReviewedConnection(ctx, client, root, current)
			err = applyErr
			if refreshed != nil {
				latestPreview = refreshed
			}
		}
		pending, journalErr := loadGenerationOperationsFromDir(root)
		readCtx, endRead := context.WithTimeout(context.Background(), 20*time.Second)
		defer endRead()
		var directory ConnectionsDirectory
		readErr := client.request(readCtx, http.MethodGet, "/v1/connections", nil, &directory)
		if err == nil && readErr == nil && !slices.ContainsFunc(directory.Connections, func(row AgentConnection) bool {
			return row.ID == previous.InstanceID && (row.Status == "configured_pending_new_session" || row.Status == "environment_receipt_unconfirmed")
		}) {
			err = fmt.Errorf("%s 的接入结果尚未确认，请重新检测", connectionName(previous.Kind))
		}
		a.dispatch(func() {
			if readErr == nil {
				a.acceptConnectionsDirectory(directory)
			}
			if journalErr == nil {
				s.PendingOperations = pending
			} else {
				journalErr = fmt.Errorf("无法读取待核对操作，请重新检测连接")
			}
			if latestPreview != nil {
				s.ConnectionPreviews[previous.InstanceID] = *latestPreview
			}
			for _, operationErr := range []error{journalErr, err, readErr} {
				if operationErr != nil {
					flow.StopBatch = true
					s.Error = bootstrap.RedactInstallerText(ControlUserError(operationErr))
					if flow.PreviewOpen == nil {
						flow.PreviewOpen = make(map[string]bool)
					}
					flow.PreviewOpen[previous.InstanceID] = true
					break
				}
			}
			if journalErr == nil && err == nil && readErr == nil {
				completed++
			}
			a.runNextOnboardingConnection(client, root, previews[1:], completed)
		})
	}()
}
