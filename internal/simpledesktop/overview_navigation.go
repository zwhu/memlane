package simpledesktop

import (
	"fmt"
	"net/url"
	"time"

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
	a.refreshSidebarQueueIfDue(time.Now())
	selected := a.page
	if (a.page == "知识" || a.page == "图谱" || a.page == "检索结果") && a.project >= 0 && a.project < len(a.Config.Projects) {
		selected = "project:" + a.Config.Projects[a.project].ID
	}
	if a.page == "设置" && a.settingsOrigin != nil {
		selected = "project:" + a.access.OpenProject
	}
	ui.Themed(c, sidebarTheme).Width(homeSidebarWidth).Shrink(0).Background(shellBackground).Children(func() {
		ui.Row(c).Height(48).Margin(48, 0, 0, 0).Shrink(0).Padding(4, 24, 0, 24).AlignItems(ui.Center).Gap(10).DragWindow().Children(func() {
			ui.Icon(c, mark).FontSize(32).TextColor(ui.Hex("#777b82"))
			homeText(c, "Memlane", homeBrand).FontSize(21).FontWeight(600).LetterSpacing(0).TextColor(homeInk)
		})
		ui.Sidebar(c.Key("main-sidebar"), &selected, func() {
			sidebarSelection(ui.SidebarItem(c, "总览", navIcons["总览"], "总览").Height(32), selected == "总览")
			sidebarSelection(ui.SidebarItem(c, "通用知识", homeCommon, "通用记忆").Height(32), selected == "通用知识")
			sidebarSelection(ui.SidebarItem(c, "用户画像", userProfileIcon, "用户画像").Height(32), selected == "用户画像")
			ui.Row(c).Padding(12, 8, 8, 8).AlignItems(ui.Center).Gap(4).Children(func() {
				homeText(c, "项目", homeMeta).FontSize(14)
				ui.Spacer(c)
				ui.Button(c, fmt.Sprintf("全部 %d", len(a.Config.Projects))).Cursor(ui.CursorPointer).FontSize(10).BorderWidth(0).Background(ui.Transparent).OnClick(a.openAllProjects)
				a.projectAddMenu(c)
			})
			labels := projectLabels(a.Config.Projects)
			for idx, p := range a.Config.Projects {
				if a.projectIsHidden(p) {
					continue
				}
				index := idx
				item := ui.SidebarItem(c.Key("project-nav-"+p.ID), "project:"+p.ID, homeFolder, middleProjectName(labels[idx])).Label(labels[idx]).Tooltip(p.Name + " · " + p.Path).MinHeight(32).FillWidth().MinWidth(0).Gap(6)
				a.projectNotificationMenu(c, item, p)
				sidebarSelection(item, selected == "project:"+p.ID)
				item.Children(func() {
					a.unreadProjectBadge(c, p, index)
					if label, detail := a.sidebarQueueBadge(p); label != "" {
						openQueue := func() { a.openProjectSettings(index) }
						ui.Column(c).Shrink(0).Height(28).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							if label == "!" {
								ui.Button(c, "").Cursor(ui.CursorPointer).Label("待处理文档："+p.Name).Tooltip(detail).Size(10, 10).Padding(0).Radius(5).Background(ui.Hex("#e77e38")).Border(2, ui.Hex("#f3dac8")).OnClick(openQueue)
							} else {
								ui.Button(c, label).Cursor(ui.CursorPointer).Label("待处理文档："+p.Name).Tooltip(detail).FontSize(10).FontWeight(600).Padding(2, 6).Radius(8).Background(ui.Hex("#dce6ef")).TextColor(ui.Hex("#536b81")).BorderWidth(0).OnClick(openQueue)
							}
						})
					}
				})
			}
		}).FillWidth().Grow(1).MinHeight(0).Padding(0, 8).Background(ui.Transparent).TrackScroll(&a.overviewState.SidebarScroll).OnChange(func() {
			for idx, p := range a.Config.Projects {
				if selected == "project:"+p.ID {
					a.navigate("知识", idx)
					return
				}
			}
			a.navigate(selected, 0)
		})
		ui.Toolbar(c, func() {
			entry := ui.Button(c, "").Cursor(ui.CursorPointer).Label("设置").FillWidth().Height(32).Padding(0, 10).Gap(10).Justify(ui.Start).BorderWidth(0)
			if a.page == "设置" && a.settingsOrigin == nil {
				entry.Background(ui.Hex("#d8e3ef"))
			} else {
				entry.Background(ui.Transparent)
			}
			entry.Children(func() {
				ui.Icon(c, homeSettings).FontSize(17).TextColor(homeIconInk)
				ui.Text(c, "设置").Font("system-ui").FontSize(13).FontWeight(500).TextColor(homeInk)
			}).OnClick(func() { a.settingsOrigin = nil; a.access.OpenProject = ""; a.navigate("设置", 0) })
		}).Height(44).Shrink(0).Padding(0, 8)
		appFooter(c, "侧栏状态栏").Padding(0, 24).Gap(7).Tooltip(a.serviceConnectionTip()).Children(func() {
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
			homeText(c, "服务 · "+status, homeMeta).FontSize(10).SingleLine().Tooltip(a.serviceConnectionTip())
		})
	})
}

func (a *App) serviceConnectionTip() string {
	address := "未配置"
	if u, err := url.Parse(a.Config.ServiceURL); err == nil && u.Host != "" {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		address = u.String()
	}
	state := "Cognee 记忆服务 · 待检查"
	if a.service == "已连接" {
		state = "Cognee 记忆服务 · 已连接"
	} else if a.service != "尚未检查" {
		state = "Cognee 记忆服务 · 连接异常"
	}
	return state + "\n地址：" + address
}

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
			homeText(c, fmt.Sprint(len(a.Config.Projects)), homeMeta).Margin(0, 7).TextColor(ui.Hex("#a1aab3"))
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
					card := ui.Button(c, "").Cursor(ui.CursorPointer).Key("home-project-"+p.ID).Label(p.Name+" · "+p.Path+" · 查看项目记忆").Width(0).Grow(1).MinWidth(0).MinHeight(94).Padding(15).Gap(11).Justify(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(homeBorder).Radius(0)
					a.projectNotificationMenu(c, card, p)
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
								path = "未关联路径"
							}
							homeText(c, path, homePath)
							if a.projectIsHidden(p) {
								homeText(c, "已隐藏 · 右键取消隐藏", homeMeta)
							}
						})
						ui.Icon(c, homeArrow).FontSize(12).TextColor(ui.Hex("#9aabba")).Shrink(0)
					}).OnClick(func() { a.navigate("知识", idx) })
				}
			})
		}
	})
}
