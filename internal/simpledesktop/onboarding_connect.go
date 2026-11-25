package simpledesktop

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
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

func (a *App) onboardingConnectionRows() []AgentConnection {
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

func (a *App) onboardingConnectionStatus(row AgentConnection) (string, string) {
	s := &a.integrations
	needsAttention := s.pendingConnection(row.ID) != nil || connectionNeedsReview(row) ||
		needsTraeHooksRepair(row) || s.ConnectionPreviews[row.ID].NeedsHookRepair
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
	case needsTraeHooksRepair(row), s.ConnectionPreviews[row.ID].NeedsHookRepair:
		return "待修复", "warn"
	default:
		return "待接入", ""
	}
}

func (a *App) onboardingConnectionPreview(row AgentConnection) (controlplane.ConnectionPreview, bool) {
	s := &a.integrations
	p, ok := s.ConnectionPreviews[row.ID]
	eligible := row.ID != "" && p.InstanceID == row.ID && p.Kind == row.Kind && p.Target != "" && p.OK &&
		!p.NeedsHookRepair && len(p.Blockers) == 0 && len(p.BlockerDetails) == 0 &&
		!needsTraeHooksRepair(row) && !connectionNeedsReview(row) && s.pendingConnection(row.ID) == nil &&
		(connectionActionAllowed(row, "prepare") || row.Status == "prepared") && previewEligibleForConfirm(p)
	return p, ok && eligible
}

func (a *App) acceptConnectionsDirectory(directory ConnectionsDirectory) {
	s := &a.integrations
	s.Connections = directory
	for _, row := range directory.Connections {
		needsAttention := s.pendingConnection(row.ID) != nil || connectionNeedsReview(row) ||
			needsTraeHooksRepair(row) || s.ConnectionPreviews[row.ID].NeedsHookRepair
		if !needsAttention && (captureVerifiedHealthy(row) || row.Status == "configured_pending_new_session" || row.Status == "environment_receipt_unconfirmed") {
			delete(a.onboardingConnection.Skipped, row.Kind)
		}
	}
}

func (s *onboardingConnectionState) previewNeedsDisplay(p controlplane.ConnectionPreview) bool {
	shown, ok := s.displayedLegacyPreviews[p.InstanceID]
	return len(p.DisabledPlugins) != 0 && (!ok || !sameReviewedOnboardingPreview(shown, p))
}

func (a *App) onboardingConnectionBatch(rows []AgentConnection) (int, []controlplane.ConnectionPreview) {
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

func (a *App) onboardingConnectionBusy() bool {
	s := &a.environment
	return !s.RuntimeStarted || s.staleRuntimeOwner() || s.ExternalWorkerActive || s.Busy ||
		s.RestartingStaleDaemon || a.integrations.Loading || a.integrations.Busy
}

func (a *App) onboardingConnectAction() (string, bool, func()) {
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

func (a *App) onboardingConnectHint() string {
	pending, previews := a.onboardingConnectionBatch(a.onboardingConnectionRows())
	if len(previews) > 0 || a.onboardingConnection.BatchRunning {
		return "写入前会自动备份原配置文件"
	}
	if pending != 0 && !a.onboardingConnectionBusy() {
		return "请在对应行处理待确认项，也可以稍后接入"
	}
	return ""
}

func (a *App) canDeferOnboardingConnections() bool {
	pending, _ := a.onboardingConnectionBatch(a.onboardingConnectionRows())
	return pending != 0 && !a.onboardingConnectionBusy()
}

func (a *App) skipOnboardingConnection(kind string) {
	if a.onboardingConnectionBusy() {
		return
	}
	if a.onboardingConnection.Skipped == nil {
		a.onboardingConnection.Skipped = make(map[string]bool)
	}
	a.onboardingConnection.Skipped[kind] = true
}

func (a *App) deferOnboardingConnections() {
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

func (a *App) runOnboardingConnectionBatch() {
	client, ok := a.accessClient()
	if !ok || a.Update == nil || a.onboardingConnectionBusy() {
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
func (a *App) runNextOnboardingConnection(client ProjectControlClient, root string, previews []controlplane.ConnectionPreview, completed int) {
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
		a.Update(func() {
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
					s.Error = bootstrap.RedactInstallerText(controlUserError(operationErr))
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

func (a *App) onboardingConnect(c *ui.Context) {
	s := &a.integrations
	if !a.windowHidden && !a.windowInactive && s.hasPendingAgentReceipts() {
		c.After(2 * time.Second)
		a.refreshAgentReceiptsIfDue(time.Now())
	}
	rows := a.onboardingConnectionRows()
	if len(rows) == 0 {
		if a.environment.HostsLoading {
			onboardingHeading(c, onboardingConnect, "接入 Agent", "正在读取 Agent 选择…")
			v4Spinner(c)
			return
		}
		onboardingHeading(c, onboardingConnect, "接入 Agent", "你在第 1 步没有选择 Agent。")
		ui.Row(c).FillWidth().Padding(sp4).Gap(sp3).Radius(8).Background(tok.Fill1).AlignItems(ui.Center).Children(func() {
			ui.Image(c, v4IconSettings).Size(32, 32).TextColor(tok.Accent)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				homeText(c, "暂不接入也可以", homeBody).FontWeight(600)
				homeText(c, "记忆提炼仍可先配置好。之后在「设置 › Agent 与模型」随时接入。", homeMeta)
			})
			v4SmallButton(c, "返回选择", nil, func() { a.navigateOnboarding(onboardingAgents) }).Disabled(a.onboardingConnectionBusy())
		})
		return
	}
	ui.Column(c).FillWidth().Gap(sp1).Children(func() {
		homeText(c, "第 3 步，共 4 步", homeMeta).FontSize(fsCaption)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			setupHeading(c, "接入你选择的 Agent")
			ui.Spacer(c)
			v4GhostButton(c, "调整选择", nil, func() { a.navigateOnboarding(onboardingAgents) }).Disabled(a.onboardingConnectionBusy())
		})
		setupDescription(c, "Memlane 会在这些 Agent 的配置里加入记忆采集与召回入口。只改动下面列出的文件，改动前自动备份。")
	})
	if s.Error != "" {
		homeText(c, bootstrap.RedactInstallerText(s.Error), homeMeta).TextColor(tok.Danger)
	}
	v4Group(c, fmt.Sprintf("%d 个 Agent", len(rows)), "", func() {
		if a.onboardingConnection.BatchRunning {
			v4SmallButton(c, "停止接入", nil, func() { a.onboardingConnection.StopBatch = true }).Disabled(a.onboardingConnection.StopBatch)
		}
	}, func() {
		for _, row := range rows {
			a.onboardingConnectRow(c, row)
		}
	})
	for _, row := range rows {
		status, _ := a.onboardingConnectionStatus(row)
		if status == "待新会话" || status == "已生效" {
			onboardingRestartCallout(c)
			break
		}
	}
}

func onboardingRestartCallout(c *ui.Context) {
	ui.Row(c).FillWidth().Padding(sp4).Gap(sp3).Radius(8).Background(tok.Fill1).Children(func() {
		ui.Row(c).Size(28, 28).Shrink(0).Radius(7).Background(tok.AccentSoft).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, v4IconRefresh).FontSize(15).TextColor(tok.Accent)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			homeText(c, "最后一步：重启已接入的 Agent", homeBody).FontWeight(600)
			homeText(c, "1. 在 Agent 里按 ⌘Q 完全退出，再重新打开", homeMeta)
			homeText(c, "2. 在项目里新建会话，正常发一条消息", homeMeta)
			homeText(c, "不用在这里等待：验证会在后台自动完成，结果显示在总览和菜单栏。", homeMeta).TextColor(tok.Text3)
		})
	})
}

var onboardingMoreIcon = v4IconSVG(`<path stroke-width="2.6" d="M4 10h.01M10 10h.01M16 10h.01"/>`)

func (a *App) onboardingConnectRow(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	name := connectionName(row.Kind)
	preview, previewPresent := s.ConnectionPreviews[row.ID]
	status, tone := a.onboardingConnectionStatus(row)
	path := bootstrap.RedactInstallerText(preview.Target)
	if path != "" && status != "待接入" {
		path = "配置文件 " + path
	} else if path != "" {
		path = "将写入 " + path
	}
	if status == "写入中" {
		path = "正在写入接入配置并备份原文件…"
	} else if path == "" && status != "已跳过" {
		path = "正在核对配置文件…"
		if !s.Busy && !s.Loading {
			path = "配置文件尚未确认"
		}
	}
	version := row.HostVersion
	if version == "" {
		version = s.HostVersions[row.Kind]
	}
	locked := a.onboardingConnectionBusy()
	v4Row(c, v4RowOptions{
		Key: "onboarding-connect-" + row.Kind, Title: name, Desc: path, DescMono: true, Tooltip: path,
		TitleExtra: func() {
			if version != "" {
				homeText(c, version, homeMeta).Font("ui-monospace").Label(name + " 版本：" + version).SingleLine()
			}
		},
		Leading: func() {
			logo := agentLogo(c, agentOriginalIcon(row.Kind), 22)
			if status == "已跳过" {
				logo.Opacity(0.5)
			}
		},
		DescExtra: func() {
			note := ""
			switch status {
			case "待新会话":
				note = "已写入配置 · 等你在新会话里发一条消息"
				if row.Status == "environment_receipt_unconfirmed" {
					note = "已检测到会话，等待确认使用了此配置目录"
				}
			case "已生效":
				note = "已收到新会话的接入回执"
			case "已跳过":
				note = "之后可在「设置 › Agent 与模型」接入"
			}
			if note != "" || previewPresent {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
					if note != "" {
						homeText(c, note, homeMeta)
					}
					if previewPresent {
						label := "查看变更预览"
						if a.onboardingConnection.PreviewOpen[row.ID] || a.onboardingConnection.previewNeedsDisplay(preview) {
							label = "收起变更预览"
						}
						textButton(c, label, func() { a.toggleOnboardingConnectionPreview(row.ID) }).
							Label(label + "：" + name).Shrink(0)
					}
				})
			}
			if status == "待新会话" && row.Kind == "trae_ide" {
				homeText(c, "在 TRAE CN「设置 → Hooks → 全局」打开开关并确认安全提示。", homeMeta).TextColor(tok.Warning)
			}
		},
		Control: func() {
			onboardingConnectionBadge(c, status, tone)
			btn := ui.ButtonBase(c.Key("onboarding-connect-menu-"+row.Kind)).Label(name+" 更多操作").Size(26, 26).
				Radius(radius).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center).Disabled(locked)
			btn.Background(homeHoverColor(btn, "onboarding-connect-menu-bg", ui.Transparent, tok.Hover))
			btn.Children(func() { ui.Icon(c, onboardingMoreIcon).FontSize(14).TextColor(tok.Text3) })
			btn.PopoverMenuButton(func(m *ui.Menu) { a.onboardingConnectionMenu(m, row, status) })
		},
	})
	ui.Column(c.Key("onboarding-connect-details-"+row.Kind)).FillWidth().Padding(0, 0, 0, 34).Gap(sp1).Children(func() {
		a.onboardingConnectionRowDetails(c, row, preview, status)
	})
}

func onboardingConnectionBadge(c *ui.Context, status, tone string) {
	switch tone {
	case "ok":
		ui.Row(c).Height(20).Padding(0, sp2).Gap(5).Radius(10).Background(tok.OK.Alpha(0.14)).Shrink(0).
			AlignItems(ui.Center).Children(func() {
			ui.Icon(c, v4IconCheck).FontSize(11).TextColor(tok.OK)
			homeText(c, status, homeMeta).FontSize(fsCaption).TextColor(tok.OK).SingleLine()
		})
	case "plain":
		homeText(c, status, homeMeta).FontSize(fsCaption).SingleLine().Shrink(0)
	default:
		v4Badge(c, status, tone).Radius(10)
	}
}

func (a *App) toggleOnboardingConnectionPreview(id string) {
	flow := &a.onboardingConnection
	if flow.PreviewOpen == nil {
		flow.PreviewOpen = make(map[string]bool)
	}
	flow.PreviewOpen[id] = !flow.PreviewOpen[id]
}

func (a *App) onboardingConnectionMenu(m *ui.Menu, row AgentConnection, status string) {
	p, eligible := a.onboardingConnectionPreview(row)
	items := []v4MenuItem{
		{Label: "只接入这个", Disabled: !eligible || a.onboardingConnection.previewNeedsDisplay(p) || (status != "待接入" && status != "已跳过"), OnSelect: func() {
			a.confirmOnboardingConnection(row)
		}},
		{Label: "跳过这个", Disabled: status != "待接入" && status != "待修复" && status != "待核对", OnSelect: func() {
			a.skipOnboardingConnection(row.Kind)
		}},
		{Label: "更换配置目录…", Disabled: row.ID == "" || a.integrations.pendingConnection(row.ID) != nil ||
			(a.ChooseProjectFolder == nil && len(row.Environments) == 0), OnSelect: func() {
			a.changeOnboardingConnectionDirectory(row)
		}},
		{Label: "立即重新检测", OnSelect: func() {
			a.environment.pinnedPreviewRequested = nil
			a.RefreshConnections()
		}},
	}
	v4BuildMenu(m, items)
}

func (a *App) changeOnboardingConnectionDirectory(row AgentConnection) {
	if a.ChooseProjectFolder != nil {
		path, err := a.ChooseProjectFolder()
		if err != nil {
			a.showOnboardingToast("无法选择目录：" + bootstrap.RedactInstallerText(err.Error()))
		} else if path != "" {
			a.selectAgentEnvironment(row, path)
		}
		return
	}
	if a.integrations.EnvironmentOpen == nil {
		a.integrations.EnvironmentOpen = make(map[string]bool)
	}
	a.integrations.EnvironmentOpen[row.ID] = true
}

func (a *App) confirmOnboardingConnection(row AgentConnection) {
	p, eligible := a.onboardingConnectionPreview(row)
	if !eligible || a.onboardingConnection.previewNeedsDisplay(p) || a.onboardingConnectionBusy() || (p.Status == "prepared" && p.EffectiveProposedSHA() == "") {
		return
	}
	delete(a.onboardingConnection.Skipped, row.Kind)
	a.startReviewedConnectionMutation(p, true)
}

func (a *App) onboardingConnectionRowDetails(c *ui.Context, row AgentConnection, preview controlplane.ConnectionPreview, status string) {
	s, flow := &a.integrations, &a.onboardingConnection
	locked := a.onboardingConnectionBusy()
	pending := s.pendingConnection(row.ID)
	switch {
	case pending != nil:
		v4SmallButton(c, "核对连接", nil, func() { a.reviewConnectionOperation(*pending) }).Disabled(locked).AlignSelf(ui.Start)
		a.pendingConnectionReconcileDetails(c, *pending, row.Kind)
	case connectionNeedsReview(row) && row.Status != "requires_review":
		v4SmallButton(c, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Disabled(locked).AlignSelf(ui.Start)
	case needsTraeHooksRepair(row), preview.NeedsHookRepair:
		if p, ok := s.TraeRepairPreview[row.ID]; ok {
			a.traeHooksRepairDetails(c, row.ID, p, connectionNeedsReview(row))
		} else {
			if row.RepairSummary != "" {
				homeText(c, bootstrap.RedactInstallerText(row.RepairSummary), homeMeta).TextColor(tok.Warning)
			}
			v4SmallButton(c, "查看 TRAE Hooks 修复预览", nil, func() { a.previewTraeHooksRepair(row.ID) }).Disabled(locked).AlignSelf(ui.Start)
		}
	case connectionNeedsReview(row):
		v4SmallButton(c, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Disabled(locked).AlignSelf(ui.Start)
	}
	_, eligible := a.onboardingConnectionPreview(row)
	sha := preview.EffectiveProposedSHA()
	manual := eligible && (preview.Kind == "opencode" || sha == "")
	if flow.previewNeedsDisplay(preview) && eligible {
		if flow.PreviewOpen == nil {
			flow.PreviewOpen = make(map[string]bool)
		}
		flow.PreviewOpen[row.ID] = true
	}
	if flow.PreviewOpen[row.ID] && preview.InstanceID != "" {
		onboardingPreviewDetails(c, preview)
		if eligible && len(preview.DisabledPlugins) != 0 {
			if flow.displayedLegacyPreviews == nil {
				flow.displayedLegacyPreviews = make(map[string]controlplane.ConnectionPreview)
			}
			flow.displayedLegacyPreviews[row.ID] = preview
		}
	}
	if ((flow.PreviewOpen[row.ID] && eligible) || manual) && (status == "待接入" || status == "已跳过") {
		label := "确认接入"
		if manual && preview.Status != "prepared" {
			homeText(c, "此 Agent 需要在准备配置后再次确认接入。", homeMeta)
			label = "准备接入"
		}
		v4SmallButton(c, label, nil, func() { a.confirmOnboardingConnection(row) }).Disabled(locked || (preview.Status == "prepared" && sha == "")).AlignSelf(ui.Start).Margin(0, 0, sp2, 0)
	}
	if a.integrations.EnvironmentOpen[row.ID] || row.EnvironmentIssue != "" {
		a.agentEnvironmentDetails(c, row)
	}
	if message := s.ItemMessages[row.ID]; message != "" {
		homeText(c, bootstrap.RedactInstallerText(message), homeMeta).TextColor(tok.Danger)
	}

	// Preserve the explicit Codex capture permission action without repeating
	// the shared restart instructions for every selected Agent.
	if pending == nil && connectionActionAllowed(row, "allow_capture") {
		a.onboardingCapturePermission(c, row)
	}
}

func onboardingPreviewDetails(c *ui.Context, preview controlplane.ConnectionPreview) {
	ui.Column(c).FillWidth().Padding(sp3).Margin(sp2, 0).Gap(sp1).Radius(radius).Background(tok.Fill1).Children(func() {
		if preview.Target != "" {
			homeText(c, bootstrap.RedactInstallerText(preview.Target), homeMeta).Font("ui-monospace")
		}
		summary := preview.DiffSummary
		if summary == "" {
			summary = preview.Summary
		}
		if summary == "" {
			summary = "加入 Memlane 的记忆采集与召回入口"
		}
		homeText(c, bootstrap.RedactInstallerText(summary), homeMeta)
		if preview.EnabledPlugin != "" {
			homeText(c, "启用 "+bootstrap.RedactInstallerText(preview.EnabledPlugin), homeMeta)
		}
		for _, plugin := range preview.DisabledPlugins {
			homeText(c, "将停用旧插件："+bootstrap.RedactInstallerText(plugin), homeMeta).TextColor(tok.Warning)
		}
		if preview.RepairSummary != "" {
			homeText(c, bootstrap.RedactInstallerText(preview.RepairSummary), homeMeta)
		}
		if preview.BackupPlan == "" {
			homeText(c, "写入前自动备份原配置文件", homeMeta)
		} else {
			homeText(c, "备份位置 "+bootstrap.RedactInstallerText(preview.BackupPlan), homeMeta)
		}
		for _, blocker := range preview.BlockerDetails {
			homeText(c, formatControlConflictMessage(blocker.Code, blocker.Phase), homeMeta).TextColor(tok.Warning)
		}
		if len(preview.BlockerDetails) == 0 {
			for _, code := range preview.Blockers {
				homeText(c, formatControlConflictMessage(code, ""), homeMeta).TextColor(tok.Warning)
			}
		}
	})
}

func (a *App) onboardingCapturePermission(c *ui.Context, row AgentConnection) {
	// The shared details show consent controls before their restart checklist.
	// Render that consent with the checklist intentionally collapsed.
	s := &a.integrations
	if s.CaptureHelpCollapsed == nil {
		s.CaptureHelpCollapsed = make(map[string]bool)
	}
	wasCollapsed, hadChoice := s.CaptureHelpCollapsed[row.ID]
	s.CaptureHelpCollapsed[row.ID] = true
	a.captureConnectionDetails(c, row)
	if hadChoice {
		s.CaptureHelpCollapsed[row.ID] = wasCollapsed
	} else {
		delete(s.CaptureHelpCollapsed, row.ID)
	}
	if _, consentOpen := s.CaptureReview[row.ID]; !consentOpen {
		a.capturePermissionButton(c, row, "允许自动采集")
	}
}
