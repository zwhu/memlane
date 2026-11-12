package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) openAgentSettings() {
	a.navigate("设置", "")
	a.settingsSection = "agents"
	a.refreshAgents(true)
	a.refreshAgentBatch()
}

// Installation and verification outcomes come from the daemon's durable batch.
func (a *App) agentConnectionCards(c *ui.Context, setup bool) {
	s := &a.agents
	width, _ := c.Size()
	available := width - settingsNavigationWidth(width) - 64
	if setup {
		available = width - 196 - 154 - 60
	}
	cardWidth := (available - 14) / 2
	for start := 0; start < len(s.Found); start += 2 {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			for index := start; index < min(start+2, len(s.Found)); index++ {
				found := s.Found[index]
				state := "待连接"
				detail := ""
				var connection *records.Connection
				for i := range s.Connections {
					if a.matchesMachineConnection(s.Connections[i], found) {
						connection = &s.Connections[i]
						break
					}
				}
				if connection != nil {
					state, detail = a.agentStatus(found, connection)
				} else if found.Path == "" {
					state = "未找到安装"
				} else if !a.machineConnectionEligible(found) {
					state = "电脑级接入暂不支持"
					detail = "当前版本尚不能在这里连接。"
				}
				if batch := s.Batch.State.Batch; batch != nil && batch.State != "retired" {
					for _, item := range batch.Items {
						if item.Target.Agent == found.Agent {
							state = agentBatchItemState(item.State)
							detail = item.Error
							if item.State == "awaiting_reload" {
								detail = "重载 Agent 后，点击下方验证连接。"
							}
							if item.State == "connected" || item.State == "already_connected" {
								if connection == nil {
									state = "连接状态待核对"
								} else if connection.ID != item.ConnectionID || connection.Generation != item.Generation {
									state = "连接已变化"
									detail = "请重新验证当前连接。"
								} else {
									state, detail = a.agentStatus(found, connection)
								}
							}
							if item.Target.Path != found.Path {
								detail = "本批保留启动时的安装路径；检测到的路径已变化。"
							}
							break
						}
					}
				}
				version := found.Version
				if version == "" {
					version = "版本待确认"
				}
				selected := s.Batch.Selected[found.Agent]
				enabled := a.machineConnectionEligible(found) && !s.Batch.frozen() && !s.Busy
				background := ui.Hex("#fafbfd")
				if selected {
					background = ui.Hex("#f1f5fa")
				}
				card := ui.CheckboxBase(c.Key("connection-card-"+found.Agent), &selected).Label("选择 "+found.Name).AlignItems(ui.Start).Width(cardWidth).MinHeight(156).Padding(18).Border(1, ui.Hex("#d8e2ed")).Radius(7).Background(background).Disabled(!enabled).OnChange(func() {
					if s.Batch.Selected == nil {
						s.Batch.Selected = map[string]bool{}
					}
					s.Batch.Selected[found.Agent] = selected
				})
				card.Children(func() {
					ui.Column(c).FillWidth().MinHeight(120).Gap(10).Children(func() {
						ui.Text(c, found.Name).Bold()
						ui.Text(c, version).FontSize(design.Small).TextColor(design.Muted)
						if found.Agent == "codex" && connection != nil {
							if i, ok := s.Installs[connection.ID]; ok && i.Options.ComputerLevel {
								capture, _ := codexCaptureStateLabel(i, s.CaptureStatus[connection.ID], s.CaptureErrors[connection.ID])
								ui.Text(c, "对话记录："+capture).FontSize(design.Small).TextColor(design.Muted)
							}
						}
						if detail != "" {
							ui.Text(c, detail).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.5)
						}
						ui.Spacer(c)
						ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
							ui.Text(c, state).FontSize(design.Small).Grow(1)
							ui.Box(c).Size(12, 12).Radius(2).Border(1, ui.Hex("#a6b8ca")).Draw(func(p *ui.Painter, r ui.Rect) {
								if selected {
									p.Fill(r, ui.Hex("#66829b"), 2)
									p.StrokePath((&ui.Path{}).MoveTo(r.X+3, r.Y+6).LineTo(r.X+5, r.Y+8).LineTo(r.X+9, r.Y+4), 1.2, ui.Hex("#ffffff"))
								}
							})
						})
					})
				})
			}
		})
	}
	if len(s.Found) == 0 {
		a.empty(c, "正在发现 Agent", "连接可以稍后设置。")
	}
	if s.Batch.Error != "" {
		ui.Text(c, s.Batch.Error).FontSize(design.Small).TextColor(design.Muted).Selectable()
	}
	if s.Batch.pending() {
		ui.Text(c, "上次连接操作的回执尚待核对。重试会复用同一操作，不另建连接。").FontSize(design.Small).TextColor(design.Muted)
	}
}

func (a *App) agentConnectionActionButtons(c *ui.Context) {
	b := &a.agents.Batch
	if b.pending() {
		quietButton(c, "核对并重试上次操作").Key("agent-batch-reconcile").Disabled(b.Submitting).OnClick(a.submitAgentBatchIntent)
		return
	}
	count := len(a.selectedConnectionTargets())
	firstLaunchPrimary(c, fmt.Sprintf("连接所选 Agent（%d）", count)).Key("agent-batch-start").Disabled(count == 0 || b.frozen() || b.State.Identity == "" || a.agents.Context == nil).OnClick(a.startSelectedAgentBatch)
	if b.State.Batch == nil {
		return
	}
	waiting, failed := false, false
	for _, item := range b.State.Batch.Items {
		if item.State == "awaiting_reload" {
			waiting = true
		}
		if item.State == "failed" || item.State == "interrupted" || item.State == "cancelled" {
			failed = true
		}
	}
	if waiting {
		quietButton(c, "已重载，验证连接").Key("agent-batch-verify").Disabled(b.Submitting || b.State.Batch.State == "running" || b.State.Batch.State == "cancelling").OnClick(func() { a.actAgentBatch("verify") })
	}
	if failed {
		quietButton(c, "重试失败项").Key("agent-batch-retry").Disabled(b.Submitting || b.State.Batch.State == "running" || b.State.Batch.State == "cancelling").OnClick(func() { a.actAgentBatch("retry") })
	}
	if b.State.Batch.State == "running" || b.State.Batch.State == "waiting" {
		quietButton(c, "取消本批连接").Key("agent-batch-cancel").Disabled(b.Submitting).OnClick(func() { a.actAgentBatch("cancel") })
	}
	if b.State.Batch.State == "cancelling" {
		ui.Text(c, "正在取消，等待实际操作结束…").FontSize(design.Small).TextColor(design.Muted)
	}
}

func (a *App) agentSettingsPage(c *ui.Context) {
	if _, conn := a.selectedAgent(); a.agents.Selection != "" && conn != nil {
		a.settingsAgentDetailPage(c)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("settings-agents-body")).TrackScroll(a.settingsScrollState("agents")).Grow(1).MinHeight(0).Padding(30, 32).Gap(18).Children(func() {
			settingsHeading(c, "Agent 连接", "选择要接入或待验证的 Agent")
			settingsNotice(c, "配置到这台电脑的 Agent；项目知识读取与通用知识授权分别管理。")

			a.agentConnectionCards(c, false)
			settingsCard(c, "settings-agent-integration", "接入方式", func() {
				ui.Text(c, "优先安装官方集成，复用 Agent 的现有配置。同配置目录的多个客户端共用接入项，能力状态逐客户端显示。").FontSize(design.Small).TextColor(design.Muted)
				ui.Text(c, "已有 Codex 日常采集待迁移至官方方案；工具连通不能证明自动采集成功。").FontSize(design.Small).TextColor(design.Muted)
			})
			for _, connection := range a.agents.Connections {
				detail := "查看逐客户端能力与接入详情"
				if len(connection.AllowedScopes) == 0 {
					detail = "尚未授权项目与通用知识"
				}
				a.agentRow(c, "connection-"+connection.ID, connection.Name, detail, a.agentConnectionRecordState(connection))
			}
		})
		ui.Row(c).Wrap().MinHeight(58).Shrink(0).Padding(14, 30).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			a.agentConnectionActionButtons(c)
			quietButton(c, "全选可连接").Disabled(a.agents.Batch.frozen()).OnClick(a.selectAllAgentConnections)
			quietButton(c, "重新检测").Disabled(a.agents.Busy || a.agents.Loading).OnClick(func() { a.refreshAgents(true); a.refreshAgentBatch() })
		})
	})
}

func (a *App) agentConnectionRecordState(connection records.Connection) string {
	if !connection.Enabled {
		return "已断开"
	}
	if _, installed := a.agents.Installs[connection.ID]; !installed {
		return "配置未完成"
	}
	d := agents.Discovery{Agent: connection.Agent}
	for _, found := range a.agents.Found {
		if found.Agent == connection.Agent {
			d = found
			break
		}
	}
	state, _ := a.agentStatus(d, &connection)
	return state
}
