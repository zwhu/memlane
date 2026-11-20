package simpledesktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *App) overviewShell(c *ui.Context) {
	applyAppearance(a.effectiveDark(c.Theme().Dark))
	ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.mainShell(c) })
}

func overviewReadingTitle(i Item) string {
	if i.Document != nil && readableDocumentTitle(*i.Document) == "" && i.BodyLoaded {
		if title := firstReadingSentenceLimit(itemPresentation(i).VisibleBody, 240); title != "" {
			return title
		}
	}
	return i.Title
}

func (a *App) returnToOverview() {
	if a.page == "知识" || a.page == "搜索" || a.page == "通用知识" {
		s := a.state()
		if len(s.History) > a.overviewSourceFloor[s] {
			a.returnFromSource(s)
			return
		}
	}
	a.navigate("总览", 0)
}

func (a *App) showOverviewProjects() {
	a.openAllProjects()
}

func (a *App) overviewNavigation(c *ui.Context) {
	a.refreshSidebarQueueIfDue(time.Now())
	a.refreshServiceMonitorIfDue(time.Now())
	selected := a.page
	if (a.page == "知识" || a.page == "图谱" || a.page == "检索结果") && a.project >= 0 && a.project < len(a.Config.Projects) {
		selected = "project:" + a.Config.Projects[a.project].ID
	}
	if a.page == "设置" && a.settingsOrigin != nil {
		selected = "project:" + a.access.OpenProject
	}
	sidebar := ui.Themed(c, sidebarTheme).Width(sidebarW).Shrink(0).Background(tok.SidebarTint)
	if !c.Vibrancy() {
		sidebar.Background(tok.Sidebar)
	}
	sidebar.Children(func() {
		// Drag region below macOS traffic lights + V4 brand header.
		ui.Row(c).Height(titlebarH).Shrink(0).DragWindow()
		ui.Row(c).Height(32).Shrink(0).Padding(0, sp4, sp2, sp4).AlignItems(ui.Center).Gap(sp2).DragWindow().Children(func() {
			ui.Image(c, tok.BrandMark).Size(18, 18).Fit(ui.Contain)
			homeText(c, "Memlane", homeBrand)
		})
		var activeItem ui.Element
		sb := ui.Sidebar(c.Key("main-sidebar"), &selected, func() {
			navEntry := func(id, label string, linear, solid *ui.SVG) {
				on := selected == id
				buildItem := func() {
					icon := linear
					if on {
						icon = solid
					}
					item := ui.SidebarItem(c, id, icon, label).Height(rowHeight1).Gap(10).Padding(0, sp2)
					if on {
						activeItem = item
					}
					sidebarSelection(item, on, activeItem.Bounds().H > 0)
				}
				if on {
					ui.Themed(c, sidebarSelectedTheme).FillWidth().Background(ui.Transparent).Children(buildItem)
				} else {
					buildItem()
				}
			}
			navEntry("总览", "总览", v4IconOverview, tok.SolidOverview)
			navEntry("通用知识", "通用记忆", v4IconCommon, tok.SolidCommon)
			navEntry("用户画像", "用户画像", v4IconProfile, tok.SolidProfile)

			ui.Row(c).Padding(sp5, sp1, sp1, sp2).AlignItems(ui.Center).Gap(sp1).Children(func() {
				ui.Text(c, "项目").Font("system-ui").FontSize(fsCaption).FontWeight(550).TextColor(tok.Text3)
				ui.Spacer(c)
				allBtn := ui.ButtonBase(c).Role(ui.RoleButton).Label(fmt.Sprintf("全部 %d", len(a.Config.Projects))).
					Cursor(ui.CursorPointer).Height(22).Padding(0, 6).Radius(radiusSmall).BorderWidth(0)
				allBtn.Background(homeHoverColor(allBtn, "all-proj-bg", ui.Transparent, tok.Hover)).Children(func() {
					ui.Text(c, fmt.Sprintf("全部 %d", len(a.Config.Projects))).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3).SingleLine()
				}).OnClick(a.openAllProjects)
				a.projectAddMenu(c)
			})
			labels := projectLabels(a.Config.Projects)
			for idx, p := range a.Config.Projects {
				if a.projectIsHidden(p) {
					continue
				}
				index := idx
				proj := p
				id := "project:" + proj.ID
				on := selected == id
				buildProject := func() {
					icon := v4IconFolder
					if on {
						icon = tok.SolidFolder
					}
					item := ui.SidebarItem(c.Key("project-nav-"+proj.ID), id, icon, middleProjectName(labels[index])).
						Label(labels[index]).Tooltip(proj.Name + " · " + proj.Path).
						MinHeight(rowHeight1).Height(rowHeight1).FillWidth().MinWidth(0).Gap(8).Padding(0, sp2)
					a.projectNotificationMenu(c, item, proj)
					if on {
						activeItem = item
					}
					sidebarSelection(item, on, activeItem.Bounds().H > 0)
					item.Children(func() {
						a.unreadProjectBadge(c, proj, index, on)
						if label, detail := a.sidebarQueueBadge(proj); label != "" {
							openQueue := func() { a.openProjectSettings(index) }
							ui.Column(c).Shrink(0).Height(24).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								if label == "!" {
									ui.Button(c, "").Cursor(ui.CursorPointer).Label("待处理文档："+proj.Name).Tooltip(detail).Size(8, 8).Padding(0).Radius(4).Background(tok.Warning).BorderWidth(0).OnClick(openQueue)
								} else {
									ui.Button(c, label).Cursor(ui.CursorPointer).Label("待处理文档："+proj.Name).Tooltip(detail).FontSize(fsCaption).FontWeight(600).Padding(1, 6).Radius(8).Background(tok.Fill2).TextColor(tok.Text2).BorderWidth(0).OnClick(openQueue)
								}
							})
						}
					})
				}
				if on {
					ui.Themed(c, sidebarSelectedTheme).FillWidth().Background(ui.Transparent).Children(buildProject)
				} else {
					buildProject()
				}
			}
		}).FillWidth().Grow(1).MinHeight(0).Padding(0, sp2).Background(ui.Transparent).TrackScroll(&a.overviewState.SidebarScroll).OnChange(func() {
			for idx, p := range a.Config.Projects {
				if selected == "project:"+p.ID {
					a.navigate("知识", idx)
					return
				}
			}
			a.navigate(selected, 0)
		})
		// Spring-sliding accent-soft indicator behind the selected nav item.
		sbBox := sb.Bounds()
		itemBox := activeItem.Bounds()
		hasActive := itemBox.H > 0 && sbBox.H > 0 && selected != "设置"
		targetY := itemBox.Y - sbBox.Y
		targetH := itemBox.H
		indY := sb.Spring("nav-ind-y", targetY, v4Snappy.Spring)
		indH := sb.Spring("nav-ind-h", targetH, v4Snappy.Spring)
		sb.Draw(func(p *ui.Painter, r ui.Rect) {
			if !hasActive || indH <= 0 {
				return
			}
			if indY+indH < 0 || indY > r.H {
				return
			}
			p.Fill(ui.Rect{X: r.X + sp2, Y: r.Y + indY, W: r.W - 2*sp2, H: indH}, tok.AccentSoft, radius)
		})

		ui.Toolbar(c, func() {
			on := a.page == "设置" && a.settingsOrigin == nil
			entry := ui.ButtonBase(c).Role(ui.RoleButton).Cursor(ui.CursorPointer).Label("设置").
				FillWidth().Height(rowHeight1).Padding(0, sp2).Gap(10).Radius(radius).
				AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0)
			if on {
				entry.Background(tok.AccentSoft)
			} else {
				entry.Background(homeHoverColor(entry, "settings-nav-bg", ui.Transparent, tok.Hover))
			}
			v4ButtonPress(entry, 0.98)
			icoColor, txtColor, weight := tok.Text2, tok.Text1, 400
			if on {
				icoColor, txtColor, weight = tok.Accent, tok.Accent, 500
			}
			entry.Children(func() {
				ui.Icon(c, v4IconSettings).FontSize(16).TextColor(icoColor)
				ui.Text(c, "设置").Font("system-ui").FontSize(fsBody).FontWeight(weight).TextColor(txtColor)
			}).OnClick(func() { a.settingsOrigin = nil; a.access.OpenProject = ""; a.navigate("设置", 0) })
		}).Height(40).Shrink(0).Padding(0, sp2)

		appFooter(c, "侧栏状态栏").Padding(0, sp3, 0, sp4).Gap(sp2).Children(func() {
			color := tok.Text4
			hookErrors := 0
			if a.monitor.Snapshot != nil && a.monitor.Snapshot.Summary != nil {
				hookErrors = a.monitor.Snapshot.Summary.HookErrorsCount
			}
			switch {
			case a.monitor.State == "degraded" || a.monitor.State == "exited" || (a.service != "已连接" && a.service != "尚未检查"):
				color = tok.Danger
			case a.monitor.State == "warning" || hookErrors > 0:
				color = tok.Warning
			case a.service == "已连接":
				color = tok.OK
			}
			status := "待检查"
			if a.service == "已连接" {
				status = "已连接"
			} else if a.service != "尚未检查" {
				status = "连接异常"
			}
			svcBtn := ui.ButtonBase(c.Key("sidebar-service-status")).Role(ui.RoleButton).Label("服务 · "+status).
				Cursor(ui.CursorPointer).Grow(1).MinWidth(0).Height(28).Padding(0, 4).Radius(radiusSmall).BorderWidth(0).
				AlignItems(ui.Center).Justify(ui.Start).Gap(sp2).Tooltip(a.serviceConnectionTip())
			svcBtn.Background(homeHoverColor(svcBtn, "svc-foot-bg", ui.Transparent, tok.Hover)).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(color).Shrink(0)
				homeText(c, "服务 · "+status, homeMeta).SingleLine()
			}).OnClick(func() {
				a.settingsOrigin = nil
				a.access.OpenProject = ""
				a.access.SettingsSection = "服务"
				a.monitor.Expanded = true
				a.navigate("设置", 0)
			})
			a.sidebarAppearanceButton(c)
		})
	})
}

func (a *App) serviceConnectionTip() string {
	address := sanitizedMonitorURL(a.Config.ServiceURL)
	state := "Cognee 记忆服务 · 待检查"
	if a.service == "已连接" {
		state = "Cognee 记忆服务 · 已连接"
		if a.monitor.State == "warning" {
			state = "Cognee 记忆服务 · 短暂波动"
		}
	} else if a.service != "尚未检查" {
		state = "Cognee 记忆服务 · 连接异常"
	}
	tip := state + "\n地址：" + address
	if summary := serviceMonitorSummaryLine(a.monitor); summary != "" {
		tip += "\n" + summary
	}
	if a.monitor.LastError != "" && a.monitor.State != "ready" {
		tip += "\n错误：" + a.monitor.LastError
	}
	return tip
}

// overviewProjectGrid is retained for direct project visibility tests; V4 removes
// the duplicate project cards section from the overview home page.
func (a *App) overviewProjectGrid(c *ui.Context) {
	v := &a.overviewState
	indices := []int{}
	hasHidden := false
	for idx, p := range a.Config.Projects {
		hidden := a.projectIsHidden(p)
		hasHidden = hasHidden || hidden
		if v.AllProjects || !hidden {
			indices = append(indices, idx)
		}
	}
	ui.Column(c).Gap(homeGridGap).Children(func() {
		head := ui.Row(c).Margin(0, 0, 3, 0).Children(func() {
			homeText(c, "项目入口", homeHeading)
			homeText(c, fmt.Sprint(len(a.Config.Projects)), homeMeta).Margin(0, 7)
			ui.Spacer(c)
			if len(a.Config.Projects) > 3 || hasHidden {
				label := "查看全部  ›"
				if v.AllProjects {
					label = "收起项目"
				}
				homeLink(c, label, func() { v.AllProjects = !v.AllProjects })
			}
		})
		if v.RevealProjects {
			head.ScrollIntoView()
			v.RevealProjects = false
		}
		count := min(3, len(indices))
		if v.AllProjects {
			count = len(indices)
		}
		if count == 0 && hasHidden {
			homeText(c, "项目已全部隐藏，可从查看全部中恢复。", homeMeta)
		}
		for start := 0; start < count; start += 3 {
			ui.Row(c).Gap(homeGridGap).AlignItems(ui.Stretch).Children(func() {
				for col := 0; col < 3; col++ {
					position := start + col
					if position >= count {
						ui.Column(c).Width(0).Grow(1)
						continue
					}
					idx := indices[position]
					p := a.Config.Projects[idx]
					card := ui.Button(c, "").Cursor(ui.CursorPointer).Key("home-project-"+p.ID).Label(p.Name+" · "+p.Path+" · 查看项目记忆").Width(0).Grow(1).MinWidth(0).MinHeight(94).Padding(15).Gap(11).Justify(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Radius(0)
					a.projectNotificationMenu(c, card, p)
					card.Background(homeHoverColor(card, "card-surface", tok.Bg, tok.Hover))
					card.Children(func() {
						ui.Column(c).Size(30, 31).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Radius(radius).Background(tok.Fill1).Children(func() { ui.Icon(c, homeFolder).FontSize(16).TextColor(tok.Text2) })
						ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
							homeText(c, p.Name, homeTitle).MaxLines(2)
							path := p.Path
							if path == "" {
								path = "未关联路径"
							}
							homeText(c, path, homePath)
							if a.projectIsHidden(p) {
								homeText(c, "已隐藏 · 右键取消隐藏", homeMeta)
							}
						})
						ui.Icon(c, homeArrow).FontSize(12).TextColor(tok.Text4).Shrink(0)
					}).OnClick(func() { a.navigate("知识", idx) })
				}
			})
		}
	})
}
