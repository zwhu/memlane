package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) openAgentSettings() {
	a.navigate("设置", "")
	a.settingsSection = "agents"
	a.refreshAgents(true)
}

// Reuse the verified discovery and connection receipts. Two-column cards do
// not imply that a project-level adapter supports machine-wide batch install.
func (a *App) agentConnectionCards(c *ui.Context, setup bool) {
	s := &a.agents
	width, _ := c.Size()
	available := width - 196 - 60
	if setup {
		available -= 154
	}
	cardWidth := (available - 14) / 2
	for start := 0; start < len(s.Found); start += 2 {
		ui.Row(c).Gap(14).Children(func() {
			for index := start; index < min(start+2, len(s.Found)); index++ {
				found := s.Found[index]
				var connection *records.Connection
				for i := range s.Connections {
					if s.Connections[i].Agent == found.Agent && s.Connections[i].Enabled {
						connection = &s.Connections[i]
						break
					}
				}
				state, _ := a.agentStatus(found, connection)
				version := found.Version
				if version == "" {
					version = "版本待确认"
				}
				icon := icons[found.Agent]
				if icon == nil {
					icon = icons["Agent"]
				}
				ui.Button(c.Key("connection-card-"+found.Agent), "").Label(found.Name+" · "+state).Width(cardWidth).MinHeight(134).Padding(18).Border(1, ui.Hex("#d8e2ed")).Background(ui.Hex("#fafbfd")).Justify(ui.Start).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
						ui.Icon(c, icon).FontSize(28).TextColor(design.Muted)
						ui.Row(c).FillWidth().Children(func() {
							ui.Text(c, found.Name).Bold()
							ui.Spacer(c)
							ui.Text(c, state).FontSize(10).TextColor(design.Muted)
						})
						ui.Text(c, version).FontSize(design.Small).TextColor(design.Muted)
					})
				}).Disabled(s.Busy).OnClick(func() {
					key := "agent-" + found.Agent
					if connection != nil {
						key = "connection-" + connection.ID
					}
					a.selectAgent(key)
					if setup {
						a.openFirstLaunchAgentSettings()
					}
				})
			}
		})
	}
	if !setup {
		ui.Row(c).Gap(14).Children(func() {
			for _, found := range s.Found {
				if found.Agent == "pi" {
					continue
				}
				quietButton(c, "添加 "+found.Name+" 连接").Key("add-connection-" + found.Agent).Disabled(s.Busy || found.ConnectionError() != nil).OnClick(func() { a.selectAgent("agent-" + found.Agent) })
			}
		})
	}
	if len(s.Found) == 0 {
		a.empty(c, "正在发现 Agent", "连接可以稍后设置。")
	}
}

func (a *App) agentSettingsPage(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(28, 30).Gap(18).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "Agent 连接").FontSize(design.Heading).Bold()
				ui.Spacer(c)
				quietButton(c, "重新检测").Disabled(a.agents.Busy).OnClick(func() { a.refreshAgents(true) })
			})
			ui.Divider(c)
			a.agentConnectionCards(c, false)
			ui.Text(c, "选择卡片管理当前连接。批量连接暂未开放。").FontSize(design.Small).TextColor(design.Muted)
			if len(a.agents.Connections) > 0 {
				ui.Text(c, "已配置连接").Bold()
				for _, connection := range a.agents.Connections {
					state := "已断开"
					if connection.Enabled {
						state = "已配置"
					}
					a.agentRow(c, "connection-"+connection.ID, connection.Name, "保留已有访问范围", state)
				}
			}
			if a.agents.Selection != "" {
				ui.Column(c).Height(480).MinHeight(0).Border(1, design.Border).Radius(6).Children(func() { a.agentDetail(c) })
			}
		})
	})
}
