package simpledesktop

import "github.com/egoist/mygo/ui"

func (a *App) serviceConnectionOverview(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(112).Padding(16).Gap(8).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(func() {
			homeText(c, "记忆服务", homeMeta)
			homeText(c, "Cognee · "+a.service, homeHeading).FontSize(15)
			homeText(c, a.Config.ServiceURL, homeMeta).MaxLines(1).Tooltip(a.Config.ServiceURL)
			if a.service != "已连接" {
				projectLink(c, "重新检查连接", a.health).AlignSelf(ui.Start)
			}
		})
		ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(112).Padding(16).Gap(8).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(func() {
			homeText(c, "记忆处理", homeMeta)
			homeText(c, a.Config.Model, homeHeading).FontSize(15).MaxLines(1).Tooltip(a.Config.Model)
			mode := "Codex 订阅推理"
			if a.Config.GenerationMode == "provider" {
				mode = "Provider API"
			}
			homeText(c, mode, homeMeta)
			if a.Config.GenerationMode != "provider" {
				homeText(c, a.completion, homeMeta).FontSize(10).MaxLines(2)
			}
		})
	})
}

func (a *App) agentConnectionGrid(c *ui.Context, rows []AgentConnection) {
	width, _ := c.Size()
	columns := 3
	if width-homeSidebarWidth-64 < 840 {
		columns = 2
	}
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		for start := 0; start < len(rows); start += columns {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
				for col := 0; col < columns; col++ {
					index := start + col
					ui.Column(c).Width(0).Grow(1).MinWidth(0).Children(func() {
						if index < len(rows) {
							a.agentConnectionRow(c, rows[index])
						}
					})
				}
			})
		}
	})
}
