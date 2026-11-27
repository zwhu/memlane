package settings

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func onboardingConnectionBadge(tok *kitui.Tokens, c *ui.Context, status, tone string) {
	switch tone {
	case "ok":
		ui.Row(c).Height(20).Padding(0, sp2).Gap(4).Radius(10).Background(tok.OK.Alpha(0.14)).Shrink(0).
			AlignItems(ui.Center).Children(func() {
			ui.Icon(c, kitui.IconCheck).FontSize(11).TextColor(tok.OK)
			kitui.Text(c, tok, status, kitui.RoleMeta).FontSize(fsCaption).TextColor(tok.OK).SingleLine()
		})
	case "plain":
		kitui.Text(c, tok, status, kitui.RoleMeta).FontSize(fsCaption).SingleLine().Shrink(0)
	default:
		kitui.Badge(c, tok, status, tone).Radius(10)
	}
}

func (a *Controller) toggleOnboardingConnectionPreview(id string) {
	flow := &a.onboardingConnection
	if flow.PreviewOpen == nil {
		flow.PreviewOpen = make(map[string]bool)
	}
	flow.PreviewOpen[id] = !flow.PreviewOpen[id]
}

func (a *Controller) onboardingConnectionMenu(m *ui.Menu, row AgentConnection, status string) {
	p, eligible := a.onboardingConnectionPreview(row)
	items := []kitui.MenuItem{
		{Label: "只接入这个", Disabled: !eligible || a.onboardingConnection.previewNeedsDisplay(p) || (status != "待接入" && status != "已跳过"), OnSelect: func() {
			a.confirmOnboardingConnection(row)
		}},
		{Label: "跳过这个", Disabled: status != "待接入" && status != "待修复" && status != "待核对", OnSelect: func() {
			a.skipOnboardingConnection(row.Kind)
		}},
		{Label: "更换配置目录…", Disabled: row.ID == "" || a.integrations.pendingConnection(row.ID) != nil ||
			(a.host.ChooseProjectFolder == nil && len(row.Environments) == 0), OnSelect: func() {
			a.changeOnboardingConnectionDirectory(row)
		}},
		{Label: "立即重新检测", OnSelect: func() {
			a.environment.pinnedPreviewRequested = nil
			a.RefreshConnections()
		}},
	}
	kitui.BuildMenu(m, items)
}

func (a *Controller) changeOnboardingConnectionDirectory(row AgentConnection) {
	if a.host.ChooseProjectFolder != nil {
		path, err := a.host.ChooseProjectFolder()
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

func (a *Controller) confirmOnboardingConnection(row AgentConnection) {
	p, eligible := a.onboardingConnectionPreview(row)
	if !eligible || a.onboardingConnection.previewNeedsDisplay(p) || a.onboardingConnectionBusy() || (p.Status == "prepared" && p.EffectiveProposedSHA() == "") {
		return
	}
	delete(a.onboardingConnection.Skipped, row.Kind)
	a.startReviewedConnectionMutation(p, true)
}

func (a *Controller) onboardingConnectionRowDetails(c *ui.Context, row AgentConnection, preview controlplane.ConnectionPreview, status string) {
	tok := a.tokens()
	s, flow := &a.integrations, &a.onboardingConnection
	locked := a.onboardingConnectionBusy()
	pending := s.pendingConnection(row.ID)
	effectiveKind := row.Kind
	if effectiveKind == "" {
		effectiveKind = preview.Kind
	}
	traeIDERepair := needsTraeIDEHooksRepair(row) || (preview.NeedsHookRepair && effectiveKind == "trae_ide")
	switch {
	case pending != nil:
		kitui.SmallButton(c, tok, "核对连接", nil, func() { a.reviewConnectionOperation(*pending) }).Disabled(locked).AlignSelf(ui.Start)
		a.pendingConnectionReconcileDetails(c, *pending, row.Kind)
	case connectionNeedsReview(row) && row.Status != "requires_review":
		kitui.SmallButton(c, tok, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Disabled(locked).AlignSelf(ui.Start)
	case traeIDERepair:
		if p, ok := s.TraeRepairPreview[row.ID]; ok {
			a.traeHooksRepairDetails(c, row.ID, p, connectionNeedsReview(row))
		} else {
			if row.RepairSummary != "" {
				kitui.Text(c, tok, bootstrap.RedactInstallerText(row.RepairSummary), kitui.RoleMeta).TextColor(tok.Warning)
			}
			kitui.SmallButton(c, tok, "查看 TRAE Hooks 修复预览", nil, func() { a.previewTraeHooksRepair(row.ID) }).Disabled(locked).AlignSelf(ui.Start)
		}
	case needsHookRepair(row), preview.NeedsHookRepair:
		r := row
		r.Kind = effectiveKind
		summary := hookRepairNotice(r)
		if row.RepairSummary == "" && preview.RepairSummary != "" {
			summary = preview.RepairSummary
		}
		if summary != "" {
			kitui.Text(c, tok, bootstrap.RedactInstallerText(summary), kitui.RoleMeta).TextColor(tok.Warning).LineHeight(1.5)
		}
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Margin(0, 0, sp2, 0).Children(func() {
			if connectionNeedsReview(row) {
				kitui.SmallButton(c, tok, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Disabled(locked)
			}
			copyConnectionTroubleshooting(tok, c, effectiveKind)
		})
	case connectionNeedsReview(row):
		kitui.SmallButton(c, tok, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Disabled(locked).AlignSelf(ui.Start)
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
		onboardingPreviewDetails(tok, c, preview)
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
			kitui.Text(c, tok, "此 Agent 需要在准备配置后再次确认接入。", kitui.RoleMeta)
			label = "准备接入"
		}
		kitui.SmallButton(c, tok, label, nil, func() { a.confirmOnboardingConnection(row) }).Disabled(locked || (preview.Status == "prepared" && sha == "")).AlignSelf(ui.Start).Margin(0, 0, sp2, 0)
	}
	if a.integrations.EnvironmentOpen[row.ID] || row.EnvironmentIssue != "" {
		a.agentEnvironmentDetails(c, row)
	}
	if message := s.ItemMessages[row.ID]; message != "" {
		kitui.Text(c, tok, bootstrap.RedactInstallerText(message), kitui.RoleMeta).TextColor(tok.Danger)
	}

	// Preserve the explicit Codex capture permission action without repeating
	// the shared restart instructions for every selected Agent.
	if pending == nil && connectionActionAllowed(row, "allow_capture") {
		a.onboardingCapturePermission(c, row)
	}
}

func onboardingPreviewDetails(tok *kitui.Tokens, c *ui.Context, preview controlplane.ConnectionPreview) {
	ui.Column(c).FillWidth().Padding(sp3).Margin(sp2, 0).Gap(sp1).Radius(radius).Background(tok.Fill1).Children(func() {
		if preview.Target != "" {
			kitui.Text(c, tok, bootstrap.RedactInstallerText(preview.Target), kitui.RoleMeta).Font(kitui.FontMono)
		}
		summary := preview.DiffSummary
		if summary == "" {
			summary = preview.Summary
		}
		if summary == "" {
			summary = "加入 Memlane 的记忆采集与召回入口"
		}
		kitui.Text(c, tok, bootstrap.RedactInstallerText(summary), kitui.RoleMeta)
		if preview.EnabledPlugin != "" {
			kitui.Text(c, tok, "启用 "+bootstrap.RedactInstallerText(preview.EnabledPlugin), kitui.RoleMeta)
		}
		for _, plugin := range preview.DisabledPlugins {
			kitui.Text(c, tok, "将停用旧插件："+bootstrap.RedactInstallerText(plugin), kitui.RoleMeta).TextColor(tok.Warning)
		}
		if preview.RepairSummary != "" {
			kitui.Text(c, tok, bootstrap.RedactInstallerText(preview.RepairSummary), kitui.RoleMeta)
		}
		if preview.BackupPlan == "" {
			kitui.Text(c, tok, "写入前自动备份原配置文件", kitui.RoleMeta)
		} else {
			kitui.Text(c, tok, "备份位置 "+bootstrap.RedactInstallerText(preview.BackupPlan), kitui.RoleMeta)
		}
		for _, blocker := range preview.BlockerDetails {
			kitui.Text(c, tok, formatControlConflictMessage(blocker.Code, blocker.Phase), kitui.RoleMeta).TextColor(tok.Warning)
		}
		if len(preview.BlockerDetails) == 0 {
			for _, code := range preview.Blockers {
				kitui.Text(c, tok, formatControlConflictMessage(code, ""), kitui.RoleMeta).TextColor(tok.Warning)
			}
		}
	})
}

func (a *Controller) onboardingCapturePermission(c *ui.Context, row AgentConnection) {
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
