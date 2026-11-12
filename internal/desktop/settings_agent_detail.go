package desktop

import (
	"github.com/egoist/mygo/ui"
)

func (a *App) settingsAgentDetailPage(c *ui.Context) {
	s := &a.agents
	d, conn := a.selectedAgent()
	if conn == nil {
		return
	}
	id := conn.ID
	name := conn.Name
	if name == "" {
		name = conn.Agent
	}
	ui.Scroll(c.Key("settings-agent-detail-"+id)).TrackScroll(a.settingsScrollState("agent-detail")).Grow(1).MinHeight(0).Padding(30, 32).Gap(18).Children(func() {
		settingsHeading(c, name, "同一配置目录的客户端复用接入项")
		firstLaunchNotice(c, a.agentConnectionRecordState(*conn), "能力按客户端分别验证")
		settingsCard(c, "settings-client-capabilities-"+id, "客户端能力", func() {
			// Existing protocol receipts identify the shared connection, not a client.
			// Do not project this aggregate result onto Desktop or IDE.
			clients := []string{"CLI", "Desktop", "IDE"}
			for index, client := range clients {
				if index > 0 {
					ui.Divider(c)
				}
				settingsFact(c, client, "工具协议 · 自动原文采集 · 记忆处理", "待逐项验证")
			}
			ui.Text(c, "现有回执未提供逐客户端能力；接入项验证结果保留在下方，不扩大为所有客户端已通过。").FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "配置记忆处理").OnClick(func() { a.selectSettingsSection("processing") })
		})
		settingsCard(c, "settings-agent-authorization-"+id, "接入与授权", func() {
			level := "现有配置（保留）"
			if i, ok := s.Installs[id]; ok && (i.Options.ComputerLevel || conn.Agent == "pi" && i.Options.PiDirectory != "") {
				level = "这台电脑"
			}
			settingsFact(c, "连接层级", "已有接入范围", level)
			settingsFact(c, "配置目录", "当前电脑记录的 Agent 配置目录", "共用接入项")
			if i, ok := s.Installs[id]; ok && i.ConfigPath != "" {
				ui.Text(c, i.ConfigPath).FontSize(design.Small).TextColor(design.Muted).Selectable()
			}
			ui.Divider(c)
			settingsFact(c, "日常记录", "仅采集授权后的日常新记录", "官方采集状态待核实")
			if conn.Agent == "codex" {
				ui.Text(c, "历史采集链路待迁移至官方插件；此页不新增并行采集器。").FontSize(design.Small).TextColor(design.Muted)
				a.officialCodexPluginSettings(c, *conn)
			}
			if conn.Agent == "pi" {
				a.piCaptureSettings(c, *conn)
			}
			ui.Divider(c)
			access := "尚未授权项目与通用知识"
			if len(conn.AllowedScopes) > 0 {
				access = "已有范围授权；详见项目设置"
			}
			settingsFact(c, "通用知识", "独立的跨项目访问权限", access)
		})
		ui.Collapsible(c, "安装位置与诊断", &s.SettingsDetailsOpen, func() {
			meta(c, "安装位置", d.Path)
			meta(c, "版本", d.Version)
			if i, ok := s.Installs[id]; ok {
				meta(c, "接入项验证", i.ProtocolVerifiedAt)
				if i.ProtocolError != "" {
					ui.Text(c, i.ProtocolError).Selectable().FontSize(design.Small)
				}
			}
		})
		if reason := a.settingsProtocolVerificationReason(d, conn); reason != "" {
			ui.Text(c, reason).FontSize(design.Small).TextColor(design.Muted)
		}
		if s.Notice != "" {
			ui.Text(c, s.Notice).Selectable()
		}
		if s.SettingsRemoveConfirmation == id {
			firstLaunchNotice(c, "撤除接入的影响", "将移除当前连接拥有的接入配置。同配置目录的 CLI、Desktop 与 IDE 可能共同受影响；已保存知识与原始记录保留，其他配置保持。")
		}
	})
	ui.Row(c).Wrap().MinHeight(58).Shrink(0).Padding(14, 30).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		quietButton(c, "返回连接").Key("settings-agent-back").OnClick(func() { s.Selection = ""; s.SettingsRemoveConfirmation = "" })
		if s.SettingsRemoveConfirmation == id {
			quietButton(c, "取消撤除").Disabled(s.Busy).OnClick(func() { s.SettingsRemoveConfirmation = "" })
			actionButton(c, "确认撤除接入", nil).Key("settings-agent-remove-confirm").Disabled(s.Busy || s.Context == nil).OnClick(func() { a.confirmSettingsAgentRemoval(id) })
		} else {
			quietButton(c, "重新读取连接").Disabled(s.Busy || s.Context == nil).OnClick(func() { a.refreshAgents(false) })
			if conn.Enabled {
				actionButton(c, "已重载，验证连接", nil).Key("verify-agent").Disabled(s.Busy || s.Context == nil || a.settingsProtocolVerificationReason(d, conn) != "").OnClick(a.verifyAgent)
				quietButton(c, "撤除接入").Key("settings-agent-remove").Disabled(s.Busy || s.Context == nil).OnClick(func() { s.SettingsRemoveConfirmation = id; s.SettingsRemoveGeneration = conn.Generation })
			}
			if s.Cancel != nil {
				quietButton(c, "取消验证").OnClick(s.Cancel)
			}
		}
	})
}

func (a *App) confirmSettingsAgentRemoval(id string) {
	s := &a.agents
	_, current := a.selectedAgent()
	if current == nil || current.ID != id || s.SettingsRemoveConfirmation != id {
		return
	}
	s.SettingsRemoveConfirmation = ""
	if !current.Enabled || current.Generation < 1 || current.Generation != s.SettingsRemoveGeneration {
		s.Notice = "连接已变化，请重新读取并确认撤除范围。"
		return
	}
	a.disconnectAgent()
}
