package simpledesktop

import "github.com/egoist/mygo/ui"

// Settings groups share the same surface, inset and divider treatment.
func settingsListSurface(c *ui.Context, content func()) {
	listSurface(c, content)
}

func settingsListDivider(c *ui.Context) {
	ui.Column(c).Height(1).FillWidth().Background(homeBorder)
}

func settingsListRow(c *ui.Context, content func()) {
	ui.Row(c).FillWidth().MinHeight(48).Gap(16).AlignItems(ui.Center).Children(content)
}

func settingsSectionHeader(c *ui.Context, title string, status, actions func()) {
	ui.Row(c).FillWidth().MinHeight(28).AlignItems(ui.Center).Gap(12).Children(func() {
		homeText(c, title, homeHeading).FontSize(15).FontWeight(500).TextColor(homeInk)
		if status != nil {
			status()
		}
		ui.Spacer(c)
		if actions != nil {
			actions()
		}
	})
}

func settingsSection(c *ui.Context, title string, status, actions, content func()) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		settingsSectionHeader(c, title, status, actions)
		settingsListSurface(c, content)
	})
}

func (a *App) serviceConnectionOverview(c *ui.Context) {
	settingsSection(c, "记忆服务", nil, func() {
		label := "展开指标"
		if a.monitor.Expanded {
			label = "收起指标"
		}
		textButton(c, label, func() {
			a.monitor.Expanded = !a.monitor.Expanded
			if a.monitor.Expanded {
				a.refreshServiceMonitor()
			}
		}).Label(label + "：记忆服务")
		refreshActionLoading(c, "重新检查", a.monitor.Polling, a.health).Disabled(a.monitor.Polling)
	}, func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			settingsListRow(c, func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
					homeText(c, "Cognee", homeBody).FontSize(13).TextColor(homeInk)
					homeText(c, a.Config.ServiceURL, homeMeta).SingleLine().Tooltip(a.Config.ServiceURL)
				})
				status := a.service
				color := homeMuted
				switch a.monitor.State {
				case "ready":
					status = "已连接"
					color = tok.OK
				case "warning":
					status = "短暂波动"
					color = tok.Warning
				case "exited":
					status = "服务已退出"
					color = tok.Danger
				case "degraded":
					status = "响应异常"
					color = tok.Danger
				default:
					if a.service == "已连接" {
						color = tok.OK
					}
				}
				homeText(c, status, homeMeta).TextColor(color)
				if a.service != "已连接" || a.monitor.State == "degraded" || a.monitor.State == "exited" {
					textButton(c, "重新检查", a.health).Disabled(a.monitor.Polling)
				}
			})
			if summary := serviceMonitorSummaryLine(a.monitor); summary != "" {
				homeText(c, summary, homeMeta).Padding(0, 0, 8, 0)
			}
			a.serviceMonitorDetails(c)
		})
	})
}

// All rows share one stable surface; expanding one Agent never stretches others.
func (a *App) agentConnectionGrid(c *ui.Context, rows []AgentConnection) {
	if len(rows) == 0 {
		return
	}
	settingsListSurface(c, func() {
		for index, row := range rows {
			a.agentConnectionRow(c, row)
			if index < len(rows)-1 {
				settingsListDivider(c)
			}
		}
	})
}
