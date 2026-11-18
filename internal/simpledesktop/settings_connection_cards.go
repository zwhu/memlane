package simpledesktop

import "github.com/egoist/mygo/ui"

// Settings groups share the same surface, inset and divider treatment.
func settingsListSurface(c *ui.Context, content func()) {
	ui.Column(c).FillWidth().Gap(0).Padding(8, 22).Background(homeBackground).Border(1, homeBorder).Radius(10).Children(content)
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
	settingsSection(c, "记忆服务", nil, nil, func() {
		settingsListRow(c, func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
				homeText(c, "Cognee", homeBody).FontSize(13).TextColor(homeInk)
				homeText(c, a.Config.ServiceURL, homeMeta).SingleLine().Tooltip(a.Config.ServiceURL)
			})
			color := homeMuted
			if a.service == "已连接" {
				color = ui.Hex("#4d7a66")
			}
			homeText(c, a.service, homeMeta).TextColor(color)
			if a.service != "已连接" {
				textButton(c, "重新检查", a.health)
			}
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
