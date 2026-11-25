package app

import "github.com/egoist/mygo/ui"

func (c *overviewController) openAllProjects() {
	c.allProjectsOpen = true
	c.allProjectsScroll = ui.ScrollState{}
}

// The sidebar's all-project entry is independent of the current page and its
// scroll position. Hidden projects remain here until explicitly restored.
func (a *App) allProjectsDialog(c *ui.Context) {
	if !a.allProjectsOpen {
		return
	}
	_, h := c.Size()
	maxListH := h - 240
	if maxListH < 180 {
		maxListH = 180
	}
	v4Dialog(c, &a.allProjectsOpen, v4DialogOptions{
		Key:         "all-projects-modal",
		Title:       "全部项目",
		Width:       v4DialogLarge,
		Hint:        "右键可清空项目通知或隐藏 / 恢复显示",
		CancelText:  "关闭",
		HideConfirm: true,
		Body: func() {
			ui.Scroll(c.Key("all-projects-list")).FillWidth().MaxHeight(maxListH).MinHeight(0).TrackScroll(&a.allProjectsScroll).Children(func() {
				if len(a.Config.Projects) == 0 {
					ui.Text(c, "暂无项目").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3).Padding(sp4, 0)
					return
				}
				ui.Column(c).FillWidth().Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
					for idx, p := range a.Config.Projects {
						index := idx
						project := p
						hidden := a.projectIsHidden(project)
						row := ui.Row(c.Key("all-project-"+project.ID)).Label("全部项目："+project.Name).
							FillWidth().MinHeight(56).AlignItems(ui.Center).Gap(sp3).
							BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
						a.projectNotificationMenu(c, row, project)
						row.Children(func() {
							open := ui.ButtonBase(c.Key("all-project-open-"+project.ID)).Role(ui.RoleButton).Label("打开项目："+project.Name).
								Grow(1).MinWidth(0).Justify(ui.Start).AlignItems(ui.Center).
								Gap(10).Padding(sp2, sp1).Radius(radius).BorderWidth(0).Cursor(ui.CursorPointer)
							open.Background(homeHoverColor(open, "all-project-surface", ui.Transparent, tok.Hover))
							v4ButtonPress(open, 0.995)
							open.Children(func() {
								ui.Icon(c, v4IconFolder).FontSize(16).TextColor(tok.Text2).Shrink(0)
								ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
									ui.Text(c, project.Name).Font("system-ui").FontSize(fsBody).FontWeight(500).TextColor(tok.Text1).MaxLines(2)
									path := project.Path
									if path == "" {
										path = "未关联路径"
									}
									ui.Text(c, path).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).SingleLine().Tooltip(path)
								})
							}).OnClick(func() {
								a.allProjectsOpen = false
								a.navigate("知识", index)
							})
							if hidden {
								ui.Row(c).Shrink(0).Gap(sp2).AlignItems(ui.Center).Children(func() {
									v4Badge(c, "已隐藏", "")
									v4SmallButton(c, "取消隐藏", nil, func() {
										if err := a.setProjectHidden(project, false); err != nil {
											c.Toast(err.Error())
										} else {
											c.Toast("已恢复显示「" + project.Name + "」")
										}
									}).Label("取消隐藏项目：" + project.Name)
								})
							}
						})
					}
				})
			})
		},
	})
}
