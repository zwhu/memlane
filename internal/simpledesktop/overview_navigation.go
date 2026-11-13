package simpledesktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *App) overviewShell(c *ui.Context) {
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
	if a.page != "总览" {
		a.navigate("总览", 0)
	}
	a.overviewState.AllProjects = true
	a.overviewState.RevealProjects = true
}

func (a *App) overviewNavigation(c *ui.Context) {
	selected := a.page
	if (a.page == "知识" || a.page == "图谱" || a.page == "检索结果") && a.project >= 0 && a.project < len(a.Config.Projects) {
		selected = "project:" + a.Config.Projects[a.project].ID
	}
	if a.page == "设置" && a.settingsOrigin != nil {
		selected = "project:" + a.access.OpenProject
	}
	ui.Column(c).Width(homeSidebarWidth).Shrink(0).Background(ui.Hex("#eaf1f8")).BorderWidth(0, 1, 0, 0).BorderColor(homeBorder).Children(func() {
		ui.Row(c).Height(112).Shrink(0).Padding(30, 26, 0, 26).Gap(10).DragWindow().Children(func() {
			ui.Icon(c, mark).FontSize(27).TextColor(ui.Hex("#4d6986"))
			homeText(c, "Memlane", homeBrand)
		})
		ui.Sidebar(c.Key("main-sidebar"), &selected, func() {
			ui.SidebarItem(c, "总览", navIcons["总览"], "总览").Height(39)
			ui.SidebarItem(c, "通用知识", homeCommon, "通用知识").Height(39)
			ui.Row(c).Padding(24, 8, 8, 8).AlignItems(ui.Center).Children(func() {
				homeText(c, "项目", homeMeta)
				ui.Spacer(c)
				ui.Button(c, fmt.Sprintf("全部 %d", len(a.Config.Projects))).FontSize(10).OnClick(a.showOverviewProjects)
				a.projectAddMenu(c)
			})
			labels := projectLabels(a.Config.Projects)
			for idx, p := range a.Config.Projects {
				index := idx
				ui.Row(c.Key("project-nav-" + p.ID)).AlignItems(ui.Center).Gap(4).Children(func() {
					ui.SidebarItem(c, "project:"+p.ID, homeFolder, p.Name).Label(labels[idx]).Tooltip(p.Name + " · " + p.Path).MinHeight(38).Grow(1).MinWidth(0)
					readingAction(c, "项目菜单："+labels[idx], readingMore, func() {}).PopoverMenuButton(func(m *ui.Menu) {
						m.Item(p.Name).Disabled(true)
						paths := []string{p.Path}
						for _, record := range a.access.Directory.Projects {
							if record.ID == p.ID && len(record.Aliases) > 0 {
								paths = record.Aliases
								break
							}
						}
						for _, path := range paths {
							if path != "" {
								m.Item(path).Disabled(true)
							}
						}
						m.Separator()
						if m.Item("编辑项目").Chosen() {
							a.openProjectCreation(p.ID)
						}
						if m.Item("项目设置").Chosen() {
							origin := a.readingLocation()
							a.navigate("设置", index)
							a.access.OpenProject = p.ID
							a.settingsOrigin = origin
						}
					})
				})
			}
		}).FillWidth().Grow(1).MinHeight(0).Padding(0, 14).Background(ui.Transparent).TrackScroll(&a.overviewState.SidebarScroll).OnChange(func() {
			for idx, p := range a.Config.Projects {
				if selected == "project:"+p.ID {
					a.navigate("知识", idx)
					return
				}
			}
			a.navigate(selected, 0)
		})
		ui.Toolbar(c, func() {
			ui.Button(c, "").Label("设置").Children(func() { ui.Icon(c, homeSettings).FontSize(17); ui.Text(c, "设置") }).OnClick(func() { a.settingsOrigin = nil; a.access.OpenProject = ""; a.navigate("设置", 0) })
		}).Height(44).Shrink(0).Padding(0, 20)
		appFooter(c, "侧栏状态栏").Padding(0, 26).Gap(7).Children(func() {
			color := ui.Hex("#9aabbc")
			if a.service == "已连接" {
				color = ui.Hex("#72978a")
			}
			ui.Box(c).Size(6, 6).Radius(3).Background(color)
			status := "待检查"
			if a.service == "已连接" {
				status = "已连接"
			} else if a.service != "尚未检查" {
				status = "连接异常"
			}
			homeText(c, "本机服务 · "+status, homeMeta).FontSize(10).SingleLine()
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
