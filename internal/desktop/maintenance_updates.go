package desktop

import "github.com/egoist/mygo/ui"

func (a *App) maintenanceUpdatesPage(c *ui.Context) {
	ui.Scroll(c.Key("maintenance-update-body")).TrackScroll(a.settingsScrollState("updates")).Grow(1).MinHeight(0).Padding(28).Gap(24).Children(func() {
		settingsHeading(c, "客户端更新", "客户端更新复用已有引擎与模型组件。")
		settingsCard(c, "settings-update-status", "更新状态", func() {
			ui.Text(c, a.updateText).Selectable()
			settingsFact(c, "当前版本", "已安装客户端", a.version)
		})
		settingsCard(c, "settings-update-content", "更新内容", func() {
			settingsFact(c, "主客户端", "知识与设置保留", "按更新回执执行")
			ui.Divider(c)
			settingsFact(c, "引擎与模型", "兼容且未变化的组件继续复用", "按兼容性核对")
		})
	})
	ui.Row(c).Key("maintenance-update-footer").MinHeight(64).Shrink(0).Padding(14, 24).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
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
