package simpledesktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *App) overviewShell(c *ui.Context) {
	ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() {
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			a.overviewNavigation(c)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Height(homeHeaderHeight).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#edf0f3")).Padding(0, 28).DragWindow().Children(func() {
					homeText(c, "总览", homeTopTitle)
					ui.Spacer(c)
					a.unifiedSearchTrigger(c)
				})
				if a.configError != "" {
					homeText(c, "运行配置不可用："+a.configError, homeBody).Margin(24)
					return
				}
				a.overviewHome(c)
			})
		})
	})
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
	if a.page != "总览" {
		a.navigate("总览", 0)
	}
	a.overviewState.AllProjects = true
	a.overviewState.RevealProjects = true
}

func (a *App) overviewNavigation(c *ui.Context) {
	ui.Column(c).Width(homeSidebarWidth).Shrink(0).Font("system-ui").FontWeight(400).Background(ui.Hex("#eaf1f8")).BorderWidth(0, 1, 0, 0).BorderColor(ui.Hex("#e0e8f0")).Padding(0, 14, 14, 14).Children(func() {
		ui.Row(c).Height(112).Shrink(0).Padding(30, 12, 0, 12).Gap(10).DragWindow().Children(func() {
			ui.Icon(c, mark).FontSize(27).TextColor(ui.Hex("#4d6986"))
			homeText(c, "Memlane", homeBrand)
		})
		ui.Column(c).Gap(3).Children(func() {
			for _, page := range []string{"总览", "通用知识"} {
				label := page
				n := ui.Button(c, "").Key("nav-"+page).Label(label).Height(39).Padding(10, 12).Gap(11).Justify(ui.Start).Background(ui.Transparent).BorderWidth(0).Radius(7)
				homeNavSurface(n, page == a.page)
				n.Children(func() {
					icon := navIcons[label]
					if label == "通用知识" {
						icon = homeCommon
					} else if label == "搜索" {
						icon = homeSearch
					}
					ui.Icon(c, icon).FontSize(17).TextColor(homeIconInk)
					text := homeText(c, label, homeBody).FontSize(13).TextColor(homeNavInk)
					if page == a.page {
						text.FontWeight(600).TextColor(ui.Hex("#304f6d"))
					}
					if label == "搜索" {
						ui.Spacer(c)
						homeText(c, "⌘ K", homeAction).TextColor(ui.Hex("#8b9aae"))
					}
				}).OnClick(func() {
					if label == "搜索" {
						a.OpenSearch()
					} else {
						a.navigate(label, 0)
					}
				})
			}
		})
		ui.Row(c).Padding(27, 12, 9, 12).Children(func() {
			homeText(c, "项目", homeMeta).FontWeight(600).LetterSpacing(1).TextColor(ui.Hex("#8191a3"))
			ui.Spacer(c)
			homeLink(c, fmt.Sprintf("全部 %d", len(a.Config.Projects)), a.showOverviewProjects).FontSize(10)
		})
		ui.Scroll(c.Key("overview-sidebar-projects")).TrackScroll(&a.overviewState.SidebarScroll).Grow(1).MinHeight(0).Gap(3).Children(func() {
			labels := projectLabels(a.Config.Projects)
			for idx, p := range a.Config.Projects {
				index := idx
				active := (a.page == "知识" || a.page == "图谱" || a.page == "检索结果") && a.project == idx
				active = active || (a.page == "设置" && a.settingsOrigin != nil && a.access.OpenProject == p.ID)
				row := ui.Row(c.Key("project-nav-" + p.ID)).FillWidth().AlignItems(ui.Center).Radius(7)
				homeNavSurface(row, active).Children(func() {
					ui.Button(c, "").Key("project-"+p.ID).Label(labels[idx]).Tooltip(p.Name+" · "+p.Path).Grow(1).MinWidth(0).Padding(8, 12).Gap(11).Justify(ui.Start).Background(ui.Transparent).BorderWidth(0).Radius(7).Children(func() {
						ui.Icon(c, homeFolder).FontSize(17).Shrink(0).TextColor(homeIconInk)
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							homeText(c, p.Name, homeBody).TextColor(homeNavInk).SingleLine()
							if labels[idx] != p.Name {
								homeText(c, p.Path, homePath).FontSize(9).TextColor(ui.Hex("#7b8ca0")).SingleLine()
							}
						})
					}).OnClick(func() { a.navigate("知识", index) })
					anchor := readingAction(c, "项目菜单："+labels[idx], readingMore, func() {
						if a.projectUI.SidebarProject == p.ID {
							a.projectUI.SidebarMenuOpen = !a.projectUI.SidebarMenuOpen
						} else {
							a.projectUI.SidebarProject = p.ID
							a.projectUI.SidebarMenuOpen = true
						}
					}).Margin(0, 4, 0, 0)
					if a.projectUI.SidebarProject == p.ID {
						ui.PopoverBase(c, anchor, &a.projectUI.SidebarMenuOpen, func(panel ui.Element) {
							panel.Width(180).Padding(10).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(8).Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, .1))
							projectLink(c, "项目设置", func() {
								origin := a.readingLocation()
								a.projectUI.SidebarMenuOpen = false
								a.navigate("设置", index)
								a.access.OpenProject = p.ID
								a.access.ProjectSection = "Agent 规则"
								a.settingsOrigin = origin
							})
						})
					}
				})
			}
		})
		ui.Column(c).Shrink(0).Margin(16, 0, 0, 0).Padding(12, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(ui.Hex("#d8e3ed")).Gap(10).Children(func() {
			b := ui.Button(c, "").Label("设置").Padding(10, 12).Gap(11).Justify(ui.Start).BorderWidth(0).Radius(7)
			homeNavSurface(b, false).Children(func() {
				ui.Icon(c, homeSettings).FontSize(17).TextColor(homeMuted)
				homeText(c, "设置", homeBody).FontSize(13).TextColor(homeNavInk)
			}).OnClick(func() { a.settingsOrigin = nil; a.access.OpenProject = ""; a.navigate("设置", 0) })
			ui.Row(c).Padding(0, 12).Gap(7).Children(func() {
				color := homeMuted
				status := "待检查"
				if a.service == "已连接" {
					color = ui.Hex("#729184")
					status = "已连接"
				} else if a.service != "尚未检查" {
					status = "连接异常"
				}
				ui.Column(c).Size(6, 6).Radius(3).Background(color)
				homeText(c, "本机服务 · "+status, homeMeta).TextColor(ui.Hex("#75899d")).Tooltip(a.service)
			})
		})
	})
}

func (a *App) overviewProjectGrid(c *ui.Context) {
	v := &a.overviewState
	ui.Column(c).Gap(homeGridGap).Children(func() {
		head := ui.Row(c).Margin(0, 0, 3, 0).Children(func() {
			homeText(c, "项目入口", homeHeading)
			homeText(c, fmt.Sprint(len(a.Config.Projects)), homeMeta).Margin(0, 7).TextColor(ui.Hex("#a1aab3"))
			ui.Spacer(c)
			if len(a.Config.Projects) > 3 {
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
		count := min(3, len(a.Config.Projects))
		if v.AllProjects {
			count = len(a.Config.Projects)
		}
		for start := 0; start < count; start += 3 {
			ui.Row(c).Gap(homeGridGap).AlignItems(ui.Stretch).Children(func() {
				for col := 0; col < 3; col++ {
					idx := start + col
					if idx >= count {
						ui.Column(c).Width(0).Grow(1)
						continue
					}
					p := a.Config.Projects[idx]
					card := ui.Button(c, "").Key("home-project-"+p.ID).Label(p.Name+" · "+p.Path+" · 查看项目知识").Width(0).Grow(1).MinWidth(0).MinHeight(94).Padding(15).Gap(11).Justify(ui.Start).Border(1, homeBorder).Radius(8)
					card.Background(homeHoverColor(card, "card-surface", ui.Hex("#ffffff"), ui.Hex("#f8fafc")))
					if card.Hovered() {
						card.BorderColor(ui.Hex("#b4c7d9"))
					}
					card.Children(func() {
						ui.Column(c).Size(30, 31).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Radius(6).Background(ui.Hex("#edf3f8")).Children(func() { ui.Icon(c, homeFolder).FontSize(16).TextColor(ui.Hex("#7d96ae")) })
						ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
							homeText(c, p.Name, homeTitle).LineHeight(1.6).MaxLines(2).TextColor(homeInk)
							path := p.Path
							if path == "" {
								path = "未关联本机路径"
							}
							homeText(c, path, homePath)
						})
						ui.Icon(c, homeArrow).FontSize(12).TextColor(ui.Hex("#9aabba")).Shrink(0)
					}).OnClick(func() { a.navigate("知识", idx) })
				}
			})
		}
	})
}
