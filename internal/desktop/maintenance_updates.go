package desktop

import "github.com/egoist/mygo/ui"

func (a *App) maintenanceUpdatesPage(c *ui.Context) {
	ui.Text(c, "客户端更新").FontSize(design.Small).TextColor(design.Muted).Padding(20, 28).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border)
	ui.Scroll(c.Key("maintenance-update-body")).Grow(1).MinHeight(0).Padding(28).Gap(24).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "自动更新").FontSize(design.Small).TextColor(design.Muted)
			ui.Text(c, "保持知识与设置").FontSize(design.Heading).Bold()
			ui.Text(c, "客户端更新复用已有引擎与模型组件。").TextColor(design.Muted)
		})
		ui.Text(c, a.updateText).Padding(16).Background(design.Surface).Border(1, design.Border).Radius(6).Selectable()
		meta(c, "当前版本", a.version)
		ui.Text(c, "下载与安装分别显示状态，安装完成后重新打开。已有知识、设置与组件保留。").FontSize(design.Small).TextColor(design.Muted)
	})
	ui.Row(c).Key("maintenance-update-footer").MinHeight(64).Shrink(0).Padding(14, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "更新期间已有知识保留").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		if a.cancelUpdate != nil {
			actionButton(c, "取消更新", nil).OnClick(a.cancelUpdate)
			return
		}
		if a.checking {
			ui.Text(c, "等待更新回执").FontSize(design.Small).TextColor(design.Muted)
			return
		}
		quietButton(c, "检查更新").Disabled(a.check == nil).OnClick(func() {
			if a.check != nil {
				a.check()
			}
		})
		if a.installUpdate != nil {
			actionButton(c, "安装更新并重新打开", nil).OnClick(a.installUpdate)
		}
	})
}
