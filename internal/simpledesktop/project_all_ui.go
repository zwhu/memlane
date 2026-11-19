package simpledesktop

import "github.com/egoist/mygo/ui"

func (a *App) openAllProjects() {
	a.allProjectsOpen = true
	a.allProjectsScroll = ui.ScrollState{}
}

// The sidebar's all-project entry is independent of the current page and its
// scroll position. Hidden projects remain here until explicitly restored.
func (a *App) allProjectsDialog(c *ui.Context) {
	if !a.allProjectsOpen {
		return
	}
	w, h := c.Size()
	ui.Modal(c.Key("all-projects-modal"), &a.allProjectsOpen, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(0, 0, 8, 0).Children(func() {
			homeText(c, "全部项目", homeHeading).FontSize(20)
			ui.Spacer(c)
			readingAction(c, "关闭全部项目", readingClose, func() { a.allProjectsOpen = false })
		})
		ui.Scroll(c.Key("all-projects-list")).Width(min(float32(560), w-120)).MaxHeight(h - 220).MinHeight(0).TrackScroll(&a.allProjectsScroll).Children(func() {
			if len(a.Config.Projects) == 0 {
				homeText(c, "暂无项目", homeMeta)
			}
			for idx, p := range a.Config.Projects {
				index := idx
				hidden := a.projectIsHidden(p)
				row := ui.Row(c.Key("all-project-"+p.ID)).Label("全部项目："+p.Name).FillWidth().MinHeight(64).AlignItems(ui.Center).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(homeBorder)
				a.projectNotificationMenu(c, row, p)
				row.Children(func() {
					open := ui.Button(c, "").Label("打开项目："+p.Name).Grow(1).MinWidth(0).Justify(ui.Start).Gap(10).Padding(10, 4).BorderWidth(0)
					open.Background(homeHoverColor(open, "all-project-surface", ui.Transparent, ui.Hex("#f8fafc")))
					open.Children(func() {
						ui.Icon(c, homeFolder).FontSize(17).TextColor(homeIconInk)
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							homeText(c, p.Name, homeBody).MaxLines(2)
							path := p.Path
							if path == "" {
								path = "未关联路径"
							}
							homeText(c, path, homeMeta).SingleLine().Tooltip(path)
						})
					}).OnClick(func() {
						a.allProjectsOpen = false
						a.navigate("知识", index)
					})
					if hidden {
						ui.Column(c).Shrink(0).Gap(4).AlignItems(ui.End).Children(func() {
							homeText(c, "已隐藏", homeMeta)
							ui.Button(c, "取消隐藏").Label("取消隐藏项目："+p.Name).FontSize(12).Padding(4, 0).BorderWidth(0).Background(ui.Transparent).TextColor(c.Theme().Accent).OnClick(func() {
								if err := a.setProjectHidden(p, false); err != nil {
									c.Toast(err.Error())
								} else {
									c.Toast("已恢复显示「" + p.Name + "」")
								}
							})
						})
					}
				})
			}
		})
	})
}
