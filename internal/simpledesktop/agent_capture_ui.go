package simpledesktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) captureConnectionAction(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	if s.CaptureReview == nil {
		s.CaptureReview = map[string]string{}
	}
	if s.CaptureHelp == nil {
		s.CaptureHelp = map[string]bool{}
	}
	if _, open := s.CaptureReview[row.ID]; open {
		textButton(c, "取消", func() { delete(s.CaptureReview, row.ID) }).Disabled(s.Busy)
		return
	}
	if row.Capture != nil {
		switch row.Capture.State {
		case "needs_permission", "deferred":
			label := "允许自动采集"
			if row.Capture.State == "deferred" {
				label = "开启"
			}
			textButton(c, label, func() { s.CaptureReview[row.ID] = row.Capture.ReviewSHA }).Label(label + "：" + connectionName(row.Kind)).Disabled(s.Busy || row.Capture.ReviewSHA == "")
			return
		case "healthy", "checking":
			return
		case "check_failed":
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if s.Loading {
					ui.Spinner(c).Size(13, 13).TextColor(homeLinkInk)
				}
				textButton(c, "重新检测", a.RefreshConnections).Label("重新检测自动采集：" + connectionName(row.Kind)).Disabled(s.Busy || s.Loading)
			})
			return
		}
	}
	label := "查看步骤"
	if s.CaptureHelp[row.ID] {
		label = "收起步骤"
	}
	textButton(c, label, func() { s.CaptureHelp[row.ID] = !s.CaptureHelp[row.ID] }).Label(label + "：" + connectionName(row.Kind))
}

func (a *App) captureConnectionDetails(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	if reviewed, open := s.CaptureReview[row.ID]; open {
		ui.Column(c).FillWidth().Padding(0, 0, 0, 42).Children(func() {
			ui.Column(c.Key("capture-consent-" + row.ID)).FillWidth().Padding(12).Gap(10).Background(tok.Fill1).Radius(6).Children(func() {
				homeText(c, "允许 Codex 自动采集已授权项目中的会话到 Memlane。", homeBody).FontSize(12)
				ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
					ui.Button(c, "确认允许").Label("确认允许自动采集："+connectionName(row.Kind)).Padding(5, 12).Disabled(s.Busy || s.Loading).OnClick(func() {
						a.connectAgentsReviewed("allow_capture", []string{row.ID}, reviewed)
					})
					textButton(c, "暂不开启", func() {
						a.connectAgentsReviewed("defer_capture", []string{row.ID}, reviewed)
					}).Label("暂不开启自动采集：" + connectionName(row.Kind)).Disabled(s.Busy || s.Loading)
				})
			})
		})
		return
	}
	if row.Capture != nil && row.Capture.State == "check_failed" {
		homeText(c, "暂时无法读取采集结果，请重新检测。", homeMeta).Margin(0, 0, 0, 42)
	}
	if row.LastHookError != nil && (row.Capture == nil || row.Capture.State != "healthy") {
		homeText(c, "最近一次 Hook 未记录："+hookErrorSummary(row.LastHookError), homeMeta).Margin(0, 0, 0, 42).LineHeight(1.5)
	}
	if !s.CaptureHelp[row.ID] {
		return
	}
	ui.Column(c.Key("capture-steps-"+row.ID)).FillWidth().Padding(0, 0, 12, 46).Gap(6).Children(func() {
		if row.Capture != nil && (row.Capture.State == "project_disabled" || row.Capture.State == "project_pending") {
			message := "最近会话所属项目未开启会话采集。"
			if row.Capture.State == "project_pending" {
				message = "最近会话所属项目的权限设置尚未生效。"
			}
			homeText(c, message, homeMeta)
			for index, project := range a.Config.Projects {
				if project.ID == row.Capture.ProjectID {
					textButton(c, "项目设置", func() { a.openProjectSettings(index) }).AlignSelf(ui.Start)
					return
				}
			}
			homeText(c, "在项目设置中开启会话采集后，再发送新消息。", homeMeta)
			return
		}
		if row.Kind == "antigravity" {
			homeText(c, "1. 在 Gemini App 右上角打开 Antigravity，选择项目并新建会话、发送消息。", homeMeta)
		} else {
			homeText(c, "1. 重新打开 "+connectionName(row.Kind)+"，新建会话并发送一条消息。", homeMeta)
		}
		homeText(c, "2. 等待回复后，点击重新检测。", homeMeta)
		ui.Row(c).Gap(6).AlignItems(ui.Center).AlignSelf(ui.Start).Children(func() {
			textButton(c, "重新检测", a.RefreshConnections).Label("重新检测自动采集：" + connectionName(row.Kind)).Disabled(s.Busy || s.Loading)
			if s.Loading {
				ui.Spinner(c).Size(13, 13).TextColor(homeLinkInk)
			}
		})
	})
}

func hookErrorSummary(e *AgentHookError) string {
	if e == nil {
		return ""
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
	if row.Capture != nil && row.Capture.LastSavedAt > 0 {
		return "最近采集：" + time.Unix(int64(row.Capture.LastSavedAt), 0).Local().Format("2006-01-02 15:04")
	}
	return ""
}

func (a *App) captureReadFailed() {
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
