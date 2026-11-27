package settings

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/controlplane"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func compactHomePath(path string) string {
	if path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+"/") {
			return "~" + path[len(home):]
		}
	}
	return path
}

func agentConnectionBadgeTone(row AgentConnection, pending *generationUIOperation, busy, repair, review bool) string {
	if pending != nil {
		if busy {
			return "accent"
		}
		return "warn"
	}
	if repair {
		return "warn"
	}
	if captureVerifiedHealthy(row) {
		return "ok"
	}
	if (row.Capture != nil && row.Capture.State == "check_failed") || row.Status == "check_failed" {
		return "danger"
	}
	if review {
		return "warn"
	}
	switch row.Status {
	case "environment_receipt_unconfirmed", "verification_environment", "environment_ambiguous",
		"environment_missing", "environment_unavailable", "staging_not_complete":
		return "warn"
	}
	if row.Capture != nil {
		switch row.Capture.State {
		case "needs_permission", "project_disabled", "project_write_denied", "project_pending":
			return "warn"
		}
	}
	return ""
}

func agentConnectionDesc(row AgentConnection, pending *generationUIOperation, repair, review bool) (string, bool) {
	compactProfile := compactHomePath(row.Profile)
	if row.Capture != nil && row.Capture.LastSavedAt > 0 {
		saved := "最近采集：" + formatUnixTimeLocal(row.Capture.LastSavedAt)
		if compactProfile != "" {
			return compactProfile + " · " + saved, false
		}
		return saved, false
	}
	if compactProfile != "" {
		return compactProfile, true
	}
	if pending != nil {
		return "上一次连接操作结果尚未确认", false
	}
	if repair {
		return "检测到 Hooks 配置异常，修复后可恢复采集", false
	}
	if review {
		return "连接配置存在变更，请核对当前状态", false
	}
	if row.Status == "registered" {
		if !row.Detected {
			return "已在 Memlane 登记 · 未检测到本机程序", false
		}
		switch row.Kind {
		case "codex", "pi", "traex", "antigravity":
			return "已在 Memlane 登记 · 支持自动采集、召回与提炼", false
		default:
			return "已在 Memlane 登记 · 支持自动采集与记忆召回", false
		}
	}
	if captureVerifiedHealthy(row) {
		return "已验证自动采集与记忆召回", false
	}
	if captureConnection(row) {
		if row.Capture != nil {
			switch row.Capture.State {
			case "needs_permission":
				return "允许后将自动采集已授权项目的会话", false
			case "deferred":
				return "可随时开启自动采集", false
			case "check_failed":
				return "暂时无法读取采集状态，请重新检测", false
			case "awaiting_generation_activation":
				return "启用记忆提炼后将自动验证保存", false
			case "project_disabled", "project_write_denied", "project_pending":
				return "请检查最近会话所属项目的采集与权限设置", false
			}
		}
		return "新建会话发送消息后将自动验证采集", false
	}
	switch row.Status {
	case "prepared":
		return "已生成接入预览，确认后写入配置", false
	case "integration_pending":
		return "当前版本暂未开放该宿主接入", false
	}
	if !row.Detected {
		return "未检测到本机程序", false
	}
	return "检测到本机程序，连接后可自动采集与召回", false
}

func (a *Controller) agentConnectionRow(c *ui.Context, row AgentConnection) {
	tok := a.tokens()
	s := &a.integrations
	name := connectionName(row.Kind)
	if s.EnvironmentOpen == nil {
		s.EnvironmentOpen = map[string]bool{}
	}
	pending := s.pendingConnection(row.ID)
	repair := needsTraeHooksRepair(row)
	preview, previewOpen := s.ConnectionPreviews[row.ID]
	repairPreview, repairPreviewOpen := s.TraeRepairPreview[row.ID]
	review := connectionNeedsReview(row)
	unknownReview := review && row.Status != "requires_review"
	allowed := connectionActionAllowed(row, "prepare") || row.Status == "prepared"
	version := row.HostVersion
	if version == "" {
		version = s.HostVersions[row.Kind]
	}
	if version == "" {
		version = "—"
	}
	status := connectionStatus(row)
	if pending != nil {
		status = "连接结果待确认"
		if pending.Kind == "connection_allow_capture" || pending.Kind == "connection_defer_capture" {
			status = "自动采集设置待确认"
		}
		if s.Busy {
			status = "正在连接…"
			if pending.Kind == "connection_allow_capture" || pending.Kind == "connection_defer_capture" {
				status = "正在设置自动采集…"
			}
		}
	}
	badgeTone := agentConnectionBadgeTone(row, pending, s.Busy, repair, review)
	desc, descMono := agentConnectionDesc(row, pending, repair, review)
	savedTip := captureSavedTip(row)
	ui.Column(c.Key("agent-connection-" + row.ID)).FillWidth().Gap(0).Children(func() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key:      "agent-row-" + row.ID,
			Title:    name,
			Desc:     desc,
			DescMono: descMono,
			TitleExtra: func() {
				ui.Text(c, version).Font(kitui.FontMono).FontSize(fsCaption).TextColor(tok.Text3).Label(name + " 版本：" + version).SingleLine()
				if slices.Contains(s.PinnedAgentKinds, row.Kind) {
					kitui.Badge(c, tok, "已选", "")
				}
			},
			Leading: func() {
				ui.Box(c).Size(24, 24).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Image(c, agentOriginalIcon(row.Kind)).Size(20, 20).Fit(ui.Contain).TextColor(tok.Text1)
				})
			},
			Control: func() {
				badge := kitui.Badge(c, tok, status, badgeTone)
				if savedTip != "" {
					badge.Tooltip(savedTip)
				}
				if row.Profile != "" || row.EnvironmentIssue != "" || len(row.Environments) > 0 {
					label := "配置目录"
					if s.EnvironmentOpen[row.ID] {
						label = "收起目录"
					}
					kitui.SmallButton(c, tok, label, nil, func() { s.EnvironmentOpen[row.ID] = !s.EnvironmentOpen[row.ID] }).Label("查看配置目录：" + name).Disabled(s.Busy)
				}
				if shouldExpandChecklist(row) || captureConnection(row) {
					a.captureConnectionAction(c, row)
				}
				if pending == nil && review && !unknownReview && repair &&
					(!repairPreviewOpen || repairPreview.Repairable && len(repairPreview.Blockers) == 0) {
					kitui.SmallButton(c, tok, "核对连接状态", nil, func() { a.connectAgents("review", []string{row.ID}) }).Disabled(s.Busy || s.Loading)
				}
				switch {
				case pending != nil:
					kitui.SmallButton(c, tok, "核对连接", nil, func() { a.reviewConnectionOperation(*pending) }).Label("核对连接：" + name).Disabled(s.Busy || s.Loading)
				case unknownReview || (review && !repair):
					kitui.SmallButton(c, tok, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Label("核对连接：" + name).Disabled(s.Busy || s.Loading)
				case repair && !repairPreviewOpen:
					kitui.SmallPrimaryButton(c, tok, "一键修复 TRAE Hooks", func() { a.previewTraeHooksRepair(row.ID) }).Disabled(s.Busy || s.Loading)
				case repair:
				case allowed && !previewOpen:
					label := "连接"
					if row.Status == "prepared" {
						label = "确认连接"
					}
					kitui.SmallPrimaryButton(c, tok, label, func() { a.preloadConnectionPreviews([]string{row.ID}) }).Label(label + " " + name).Disabled(s.Busy || s.Loading)
				}
			},
		})
		if row.Status == "prepared" && row.Preview != nil && !previewOpen && !repair {
			ui.Column(c).FillWidth().Padding(0, sp4, sp3, sp4+36).Gap(4).Children(func() {
				kitui.Text(c, tok, "将为 "+name+" 添加 Memlane 连接，确认后生效。", kitui.RoleMeta)
				if len(row.Preview.DisabledPlugins) > 0 {
					kitui.Text(c, tok, fmt.Sprintf("将替换 %d 个旧 Cognee 连接。", len(row.Preview.DisabledPlugins)), kitui.RoleMeta)
				}
			})
		}
		if shouldExpandChecklist(row) || captureConnection(row) {
			a.captureConnectionDetails(c, row)
		}
		if notice := connectionReviewNotice(name, row); notice != "" {
			ui.Row(c).FillWidth().Padding(0, sp4, sp2, sp4+36).Children(func() { kitui.Text(c, tok, notice, kitui.RoleMeta).LineHeight(1.5) })
		}
		if repair && pending == nil && !unknownReview {
			if repairPreviewOpen {
				a.traeHooksRepairDetails(c, row.ID, repairPreview, review)
			} else if row.RepairSummary != "" {
				ui.Row(c).FillWidth().Padding(0, sp4, sp2, sp4+36).Children(func() {
					kitui.Text(c, tok, row.RepairSummary, kitui.RoleMeta).TextColor(tok.Warning).LineHeight(1.5)
				})
			}
		}
		if previewOpen && pending == nil && !repair && allowed {
			a.connectionPreviewDetails(c, row, preview)
		}
		ambiguityAction := false
		if pending != nil {
			ambiguityAction = a.pendingConnectionReconcileDetails(c, *pending, row.Kind)
		}
		if !ambiguityAction && (repair || review || row.ErrorCode != "" || s.ItemMessages[row.ID] != "" || row.Status == "check_failed" ||
			(row.Capture != nil && row.Capture.State == "check_failed") || row.LastHookError != nil && !captureVerifiedHealthy(row)) {
			ui.Row(c).Padding(0, sp4, sp2, sp4+36).Children(func() { copyConnectionTroubleshooting(tok, c, row.Kind) })
		}
		if row.Profile != "" || row.EnvironmentIssue != "" || len(row.Environments) > 0 {
			a.agentEnvironmentDetails(c, row)
		}
		if row.Status == "environment_receipt_unconfirmed" {
			msg := "已检测到会话，但还不能确认使用的是哪个配置目录。"
			if row.AwaitingPostUpgradeReceipt {
				msg = "应用更新后等待新一次会话回执，原有插件配置正常。"
			}
			ui.Row(c).FillWidth().Padding(0, sp4, sp2, sp4+36).Children(func() { kitui.Text(c, tok, msg, kitui.RoleMeta) })
		}
		if message := s.ItemMessages[row.ID]; message != "" {
			ui.Row(c).FillWidth().Padding(0, sp4, sp2, sp4+36).Children(func() { kitui.Text(c, tok, message, kitui.RoleMeta).LineHeight(1.5) })
		}
	})
}

func copyConnectionTroubleshooting(tok *kitui.Tokens, c *ui.Context, kind string) {
	kitui.TextButton(c, tok, "复制排障指令交给 Agent", func() {
		c.WriteClipboard(agentTakeoverConnectionTroubleshootPrompt(kind))
	})
}

func (a *Controller) pendingConnectionReconcileDetails(c *ui.Context, pending generationUIOperation, kind string) bool {
	tok := a.tokens()
	s := &a.integrations
	preview, present := s.PendingReconcile[pending.Operation]
	if !present {
		return false
	}
	safe := canSafelyUnlockPreview(preview)
	reviewHost := preview.Recorded && (preview.Status == "unknown" || preview.Phase == "post_mutation")
	for _, row := range s.Connections.Connections {
		if row.ID == pending.Profile && connectionNeedsReview(row) {
			reviewHost = true
			break
		}
	}
	ui.Column(c.Key("reconcile-"+pending.Operation)).FillWidth().Padding(0, sp4, sp3, sp4+36).Children(func() {
		ui.Column(c).FillWidth().Padding(sp3).Gap(sp2).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Children(func() {
			if safe {
				kitui.Text(c, tok, "服务端已确认未写入宿主文件。", kitui.RoleMeta)
				kitui.SmallPrimaryButton(c, tok, "安全解锁", func() { a.reconcilePendingConnectionOperation(pending) }).AlignSelf(ui.Start).Disabled(s.Busy || s.Loading)
			} else {
				kitui.Text(c, tok, "该待核对操作存在写入风险，需人工核对", kitui.RoleMeta).TextColor(tok.Warning)
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
					if reviewHost {
						kitui.SmallButton(c, tok, "核对连接状态", nil, func() { a.reviewHostStateForPendingOperation(pending) }).Disabled(s.Busy || s.Loading)
					}
					copyConnectionTroubleshooting(tok, c, kind)
				})
			}
		})
	})
	return !safe
}

func (a *Controller) traeHooksRepairDetails(c *ui.Context, id string, preview controlplane.TraeHooksRepairResult, review bool) {
	tok := a.tokens()
	s := &a.integrations
	ui.Column(c.Key("repair-preview-"+id)).FillWidth().Padding(0, sp4, sp3, sp4+36).Children(func() {
		ui.Column(c).FillWidth().Padding(sp3).Gap(sp1).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Children(func() {
			summary := preview.Summary
			if summary == "" {
				summary = preview.RepairSummary
			}
			if summary != "" {
				kitui.Text(c, tok, summary, kitui.RoleBody).FontWeight(500)
			}
			if len(preview.RemovedEvents) != 0 {
				kitui.Text(c, tok, "移除事件 "+strings.Join(preview.RemovedEvents, "、"), kitui.RoleMeta)
			}
			if len(preview.RetainedEvents) != 0 {
				kitui.Text(c, tok, "保留事件 "+strings.Join(preview.RetainedEvents, "、"), kitui.RoleMeta)
			}
			kitui.Text(c, tok, fmt.Sprintf("保留 %d 组第三方 Hooks", preview.PreservedThirdPartyGroups), kitui.RoleMeta)
			if preview.BackupPlan != "" {
				kitui.Text(c, tok, "备份原配置为 "+preview.BackupPlan, kitui.RoleMeta)
			}
			for _, blocker := range preview.Blockers {
				kitui.Text(c, tok, formatControlConflictMessage(blocker.Code, blocker.Phase), kitui.RoleMeta).TextColor(tok.Warning)
			}
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				kitui.SmallPrimaryButton(c, tok, "确认修复", func() { a.applyTraeHooksRepair(id) }).Disabled(s.Busy || s.Loading || !preview.Repairable || preview.ReviewedSHA == "" || len(preview.Blockers) != 0)
				if review && (!preview.Repairable || len(preview.Blockers) != 0) {
					kitui.SmallButton(c, tok, "核对连接状态", nil, func() { a.connectAgents("review", []string{id}) }).Disabled(s.Busy || s.Loading)
				}
				kitui.TextButton(c, tok, "取消", func() { s.clearTraeRepairPreview(id) })
			})
		})
	})
}

func (a *Controller) connectionPreviewDetails(c *ui.Context, row AgentConnection, preview controlplane.ConnectionPreview) {
	tok := a.tokens()
	s := &a.integrations
	sha := preview.EffectiveProposedSHA()
	ui.Column(c.Key("connection-preview-"+row.ID)).FillWidth().Padding(0, sp4, sp3, sp4+36).Children(func() {
		ui.Column(c).FillWidth().Padding(sp3).Gap(sp1).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Children(func() {
			kitui.Text(c, tok, "将为 "+connectionName(row.Kind)+" 添加 Memlane 连接。", kitui.RoleBody).FontWeight(500)
			if preview.NeedsHookRepair {
				kitui.Text(c, tok, "请重新检测连接，然后打开「一键修复 TRAE Hooks」预览。", kitui.RoleMeta).TextColor(tok.Warning)
			}
			if preview.Target != "" {
				kitui.Text(c, tok, "配置文件 "+preview.Target, kitui.RoleMeta)
			}
			if len(preview.DisabledPlugins) > 0 {
				kitui.Text(c, tok, fmt.Sprintf("将替换 %d 个旧 Cognee 连接。", len(preview.DisabledPlugins)), kitui.RoleMeta)
			}
			for _, blocker := range preview.BlockerDetails {
				kitui.Text(c, tok, formatControlConflictMessage(blocker.Code, blocker.Phase), kitui.RoleMeta).TextColor(tok.Warning)
			}
			if len(preview.BlockerDetails) == 0 {
				for _, code := range preview.Blockers {
					kitui.Text(c, tok, formatControlConflictMessage(code, ""), kitui.RoleMeta).TextColor(tok.Warning)
				}
			}
			label := "确认连接"
			prepared := preview.Status == "prepared"
			if prepared && preview.OK && sha == "" {
				kitui.Text(c, tok, "接入预览缺少确认信息，请重新预览。", kitui.RoleMeta).TextColor(tok.Warning)
			}
			if !prepared && (sha == "" || preview.Kind == "opencode") {
				label = "准备连接"
			} else if !prepared && slices.Contains(s.PinnedAgentKinds, row.Kind) && preview.OK && !preview.NeedsHookRepair {
				label = "一键确认接入"
			}
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				kitui.SmallPrimaryButton(c, tok, label, func() { a.confirmPrepareAndActivateConnection(row.ID, sha) }).Disabled(s.Busy || s.Loading || !previewEligibleForConfirm(preview) || !preview.OK || preview.NeedsHookRepair || (prepared && sha == ""))
				kitui.TextButton(c, tok, "取消", func() { delete(s.ConnectionPreviews, row.ID) })
			})
		})
	})
}

func (a *Controller) agentEnvironmentDetails(c *ui.Context, row AgentConnection) {
	tok := a.tokens()
	s := &a.integrations
	if row.EnvironmentIssue != "" {
		message := "请选择你日常使用的配置目录。"
		if row.EnvironmentIssue == "verification_environment" {
			message = "当前是测试目录，请换成日常使用的配置目录。"
		}
		ui.Row(c).FillWidth().Padding(0, sp4, sp2, sp4+36).Children(func() {
			kitui.Text(c, tok, message, kitui.RoleMeta)
		})
	}
	if !s.EnvironmentOpen[row.ID] {
		return
	}
	// Inset inside the measured width. FillWidth plus an external left margin
	// made the expanded directory escape the shared list surface.
	ui.Column(c.Key("environment-"+row.ID)).Label("Agent目录详情："+connectionName(row.Kind)).FillWidth().Padding(0, sp4, sp3, sp4+36).Children(func() {
		ui.Column(c).FillWidth().Padding(sp3).Gap(8).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Children(func() {
			if row.Profile != "" {
				kitui.Text(c, tok, "配置目录："+row.Profile, kitui.RoleMeta).FillWidth().MinWidth(0).SingleLine().Tooltip(row.Profile)
			}
			if row.EnvironmentVerified {
				kitui.Text(c, tok, "插件已确认 · "+row.PluginVersion, kitui.RoleMeta)
			} else if row.AwaitingPostUpgradeReceipt {
				kitui.Text(c, tok, "配置目录已确认，等待应用更新后的新一次会话回执。", kitui.RoleMeta)
			} else if row.LastReceiptAt > 0 {
				kitui.Text(c, tok, "检测到会话活动，尚未确认来自此目录。", kitui.RoleMeta)
			}
			for _, candidate := range row.Environments {
				option := candidate
				if option.Profile == row.Profile {
					continue
				}
				label := "其他目录"
				if option.Source == "default" {
					label = "默认目录"
				}
				if option.Source == "environment" {
					label = "启动时指定的目录"
				}
				if option.Verification {
					label = "测试目录 · 不可用于日常连接"
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
					kitui.Text(c, tok, label+"："+option.Profile, kitui.RoleMeta).Grow(1).MinWidth(0)
					if option.Exists && !option.Verification {
						kitui.TextButton(c, tok, "使用此目录", func() { a.selectAgentEnvironment(row, option.Profile) }).Label("使用目录：" + option.Profile).Disabled(s.Busy || s.Loading)
					}
				})
			}
			if a.host.ChooseProjectFolder != nil {
				kitui.TextButton(c, tok, "更换目录", func() {
					path, err := a.host.ChooseProjectFolder()
					if err == nil && path != "" {
						a.selectAgentEnvironment(row, path)
					}
				}).Disabled(s.Busy || s.Loading).AlignSelf(ui.Start)
			}
		})
	})
}
