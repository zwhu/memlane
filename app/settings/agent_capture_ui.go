package settings

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func captureVerifiedHealthy(row AgentConnection) bool {
	if row.Capture != nil {
		return row.Capture.State == "healthy"
	}
	return !captureConnection(row) && row.Status == "configured_pending_new_session" && row.NewSessions > 0
}

func shouldExpandChecklist(row AgentConnection) bool {
	if captureVerifiedHealthy(row) {
		return false
	}
	if row.Capture != nil {
		switch row.Capture.State {
		case "waiting_session", "waiting_message", "awaiting_generation_activation", "needs_permission",
			"check_failed", "project_disabled", "project_write_denied", "project_pending", "needs_hook_repair":
			return true
		}
	}
	if needsTraeHooksRepair(row) {
		return true
	}
	switch row.Status {
	case "configured_pending_new_session":
		return captureConnection(row) || row.NewSessions == 0
	case "environment_receipt_unconfirmed", "requires_review", "activation_unknown",
		"preparation_unknown", "staging_unknown", "agent_creation_unconfirmed", "check_failed":
		return true
	}
	return false
}

func (a *Controller) captureConnectionAction(c *ui.Context, row AgentConnection) {
	tok := a.tokens()
	s := &a.integrations
	if shouldExpandChecklist(row) {
		label := "收起步骤"
		if s.CaptureHelpCollapsed[row.ID] {
			label = "查看步骤"
		}
		kitui.TextButton(c, tok, label, func() {
			if s.CaptureHelpCollapsed == nil {
				s.CaptureHelpCollapsed = make(map[string]bool)
			}
			s.CaptureHelpCollapsed[row.ID] = !s.CaptureHelpCollapsed[row.ID]
		}).Label(label + "：" + connectionName(row.Kind))
	}
	if row.Capture != nil && row.Capture.State == "deferred" && s.pendingConnection(row.ID) == nil {
		a.capturePermissionButton(c, row, "开启")
	}
}

func (a *Controller) capturePermissionButton(c *ui.Context, row AgentConnection, label string) {
	tok := a.tokens()
	s := &a.integrations
	kitui.TextButton(c, tok, label, func() {
		if s.CaptureReview == nil {
			s.CaptureReview = make(map[string]string)
		}
		s.CaptureReview[row.ID] = row.Capture.ReviewSHA
	}).Label(label + "：" + connectionName(row.Kind)).Disabled(s.Busy || s.Loading || row.Capture.ReviewSHA == "")
}

func (a *Controller) captureConnectionDetails(c *ui.Context, row AgentConnection) {
	tok := a.tokens()
	s := &a.integrations
	reviewed, consentOpen := s.CaptureReview[row.ID]
	canConsent := s.pendingConnection(row.ID) == nil && connectionActionAllowed(row, "allow_capture")
	if consentOpen && canConsent {
		ui.Column(c).FillWidth().Padding(0, 0, 0, 42).Children(func() {
			ui.Column(c.Key("capture-consent-" + row.ID)).FillWidth().Padding(12).Gap(10).Background(tok.Fill1).Radius(6).Children(func() {
				kitui.Text(c, tok, "允许 Codex 自动采集已授权项目中的会话到 Memlane。", kitui.RoleBody).FontSize(12)
				ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
					ui.Button(c, "确认允许").Label("确认允许自动采集："+connectionName(row.Kind)).Padding(5, 12).Disabled(s.Busy || s.Loading).OnClick(func() {
						a.connectAgentsReviewed("allow_capture", []string{row.ID}, reviewed)
					})
					kitui.TextButton(c, tok, "取消", func() { delete(s.CaptureReview, row.ID) }).Disabled(s.Busy)
					kitui.TextButton(c, tok, "暂不开启", func() {
						a.connectAgentsReviewed("defer_capture", []string{row.ID}, reviewed)
					}).Label("暂不开启自动采集：" + connectionName(row.Kind)).Disabled(s.Busy || s.Loading)
				})
			})
		})
	}
	if row.Capture != nil && row.Capture.State == "check_failed" {
		kitui.Text(c, tok, "暂时无法读取采集结果，请重新检测。", kitui.RoleMeta).Margin(0, 0, 0, 42)
	}
	if row.LastHookError != nil && (row.Capture == nil || row.Capture.State != "healthy") {
		kitui.Text(c, tok, "最近一次 Hook 未记录："+hookErrorSummary(row.LastHookError)+" · "+formatUnixTimeLocal(row.LastHookError.At), kitui.RoleMeta).Margin(0, 0, 0, 42).LineHeight(1.5)
	}
	if !shouldExpandChecklist(row) || s.CaptureHelpCollapsed[row.ID] {
		return
	}
	ui.Column(c.Key("capture-steps-"+row.ID)).FillWidth().Padding(0, 0, 12, 46).Gap(6).Children(func() {
		projectState := ""
		if row.Capture != nil {
			projectState = row.Capture.State
		}
		message := ""
		switch projectState {
		case "project_disabled":
			message = "最近会话所属项目未开启会话采集。"
		case "project_pending":
			message = "最近会话所属项目的权限设置尚未生效。"
		case "project_write_denied":
			message = "当前 Agent 没有最近会话所属项目的写入权限。"
		}
		if message != "" {
			kitui.Text(c, tok, message, kitui.RoleMeta)
			for _, project := range a.config.Projects {
				if project.ID == row.Capture.ProjectID {
					kitui.TextButton(c, tok, "项目设置", func() { a.OpenProjectManagement(project.ID) }).AlignSelf(ui.Start)
					return
				}
			}
			kitui.Text(c, tok, "调整项目设置后，再发送新消息。", kitui.RoleMeta)
			return
		}
		restart := "1. 如果你原本已打开 " + connectionName(row.Kind) + "，请先 ⌘Q 完全退出后重新打开。"
		if row.Kind == "antigravity" {
			restart = "1. 如果你原本已打开 Gemini App，请先 ⌘Q 完全退出后重新打开，并在右上角打开 Antigravity。"
		}
		kitui.Text(c, tok, restart, kitui.RoleMeta).FontWeight(500).TextColor(tok.Text1)
		newSessionStep := "打开项目工作区新建会话，发送一条消息；无需等待此处变绿即可继续后续步骤。"
		if row.Kind == "trae_ide" {
			kitui.Text(c, tok, "2. 在 TRAE 设置 → Hooks → 全局 中开启开关并确认安全提示。", kitui.RoleMeta)
			kitui.Text(c, tok, "3. "+newSessionStep, kitui.RoleMeta)
		} else {
			kitui.Text(c, tok, "2. "+newSessionStep, kitui.RoleMeta)
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).AlignSelf(ui.Start).Children(func() {
			if canConsent && !consentOpen && row.Capture.State == "needs_permission" {
				a.capturePermissionButton(c, row, "允许自动采集")
			}
			kitui.TextButton(c, tok, "重新检测", a.RefreshConnections).Label("重新检测自动采集：" + connectionName(row.Kind)).Disabled(s.Busy || s.Loading)
			if s.Loading {
				ui.Spinner(c).Size(13, 13).TextColor(tok.Accent)
			}
		})
	})
}

func hookErrorSummary(e *AgentHookError) string {
	if e == nil {
		return ""
	}
	switch {
	case e.Reason == "apply_patch_key_rule" || e.ErrorType == "apply_patch_key_rule":
		return "补丁路径命中 `*.key` 规则，记录保留在本机（规则命中本身不足以确认补丁包含密钥）"
	case e.Reason == "capture_rule_filtered" || e.ErrorType == "capture_rule_filtered":
		return "记录因采集规则保留在本机，可在项目的采集状态中查看原因"
	}
	switch e.Reason {
	case "installation_not_activated":
		return "插件尚未完成激活（" + e.Reason + "）"
	case "native_transcript_identity_required":
		return "会话文件路径未通过校验（" + e.Reason + "）"
	case "existing_conversation_not_captured":
		return "当前仍是安装前的旧会话（仅记录新会话）（" + e.Reason + "）"
	case "host_workspace_required":
		return "未收到当前项目工作目录（" + e.Reason + "）"
	case "project_policy_unconfirmed":
		return "无法确认项目权限或控制服务未响应（" + e.Reason + "）"
	default:
		if e.Reason != "" {
			return e.Reason
		}
		return e.ErrorType
	}
}

func captureSavedTip(row AgentConnection) string {
	if row.Capture != nil && (row.Capture.LastSavedAt > 0 || row.Capture.State == "healthy") {
		return "最近采集：" + formatUnixTimeLocal(row.Capture.LastSavedAt)
	}
	return ""
}

func (a *Controller) captureReadFailed() {
	for index, row := range a.integrations.Connections.Connections {
		if captureConnection(row) {
			capture := AgentCaptureState{State: "check_failed", Permission: "unknown"}
			if row.Capture != nil {
				capture.LastSavedAt = row.Capture.LastSavedAt
			}
			a.integrations.Connections.Connections[index].Capture = &capture
		}
	}
}
