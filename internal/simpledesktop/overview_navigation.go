package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

// A's visual system is scoped to the overview; existing reading/settings
// geometry and their shared colors retain their established contracts.
var homeInk = ui.Hex("#273340")
var homeMuted = ui.Hex("#748395")
var homeBorder = ui.Hex("#e6ebef")
var homeDocument = monoIcon(`<path d="M6 2h8l4 4v16H6zM14 2v5h4M9 12h6M9 16h6"/>`)
var homeSettings = monoIcon(`<path d="m9 3-1 3-3 1-2 5 2 5 3 1 1 3h6l1-3 3-1 2-5-2-5-3-1-1-3z"/><circle cx="12" cy="12" r="3"/>`)

func overviewReadingTitle(i Item) string {
	if i.Document != nil && readableDocumentTitle(*i.Document) == "" && i.BodyLoaded {
		if title := firstReadingSentenceLimit(itemPresentation(i).VisibleBody, 240); title != "" {
			return title
		}
	}
	return i.Title
}

func homeLink(c *ui.Context, title string, fn func()) {
	ui.Button(c, title).FontSize(11).TextColor(ui.Hex("#426683")).Padding(4, 0).Background(ui.Transparent).BorderWidth(0).OnClick(fn)
}

func (a *App) returnToOverview() {
	if a.page == "知识" || a.page == "搜索" || a.page == "通用知识" {
		s := a.state()
		if len(s.History) > 0 {
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
	ui.Column(c).Width(220).Shrink(0).Background(ui.Hex("#eaf1f8")).BorderWidth(0, 1, 0, 0).BorderColor(ui.Hex("#e0e8f0")).Padding(0, 14, 14, 14).Children(func() {
		ui.Row(c).Height(112).Shrink(0).Padding(30, 12, 0, 12).Gap(10).DragWindow().Children(func() {
			ui.Icon(c, mark).FontSize(27).TextColor(ui.Hex("#4d6986"))
			ui.Text(c, "Memlane").FontSize(19).Bold().TextColor(homeInk)
		})
		ui.Column(c).Gap(3).Children(func() {
			for _, page := range []string{"总览", "搜索", "通用知识"} {
				label := page
				n := ui.Button(c, "").Key("nav-"+page).Label(label).Height(39).Padding(10, 12).Gap(11).Justify(ui.Start).Background(ui.Transparent).BorderWidth(0).Radius(7)
				if page == a.page {
					n.Background(ui.Hex("#d7e4f1"))
				}
				n.Children(func() {
					ui.Icon(c, navIcons[label]).FontSize(17).TextColor(ui.Hex("#7189a1"))
					ui.Text(c, label).FontSize(13).TextColor(homeInk)
					if label == "搜索" {
						ui.Spacer(c)
						ui.Text(c, "⌘ K").FontSize(11).TextColor(homeMuted)
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
			ui.Text(c, "项目").FontSize(10).TextColor(homeMuted)
			ui.Spacer(c)
			homeLink(c, fmt.Sprintf("全部 %d", len(a.Config.Projects)), a.showOverviewProjects)
		})
		ui.Scroll(c.Key("overview-sidebar-projects")).TrackScroll(&a.overviewState.SidebarScroll).Grow(1).MinHeight(0).Gap(3).Children(func() {
			labels := projectLabels(a.Config.Projects)
			for idx, p := range a.Config.Projects {
				index := idx
				ui.Button(c, "").Key("project-"+p.ID).Label(labels[idx]).Tooltip(p.Name+" · "+p.Path).FillWidth().Padding(8, 12).Gap(11).Justify(ui.Start).BorderWidth(0).Background(ui.Transparent).Radius(7).Children(func() {
					ui.Icon(c, folder).FontSize(17).Shrink(0).TextColor(homeMuted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Text(c, p.Name).FontSize(12).TextColor(homeInk).SingleLine()
						if labels[idx] != p.Name {
							ui.Text(c, p.Path).FontSize(9).TextColor(homeMuted).SingleLine()
						}
					})
				}).OnClick(func() { a.navigate("知识", index) })
			}
		})
		ui.Column(c).Shrink(0).Margin(16, 0, 0, 0).Padding(12, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(ui.Hex("#d8e3ed")).Gap(10).Children(func() {
			ui.Button(c, "").Label("设置").Padding(10, 12).Gap(11).Justify(ui.Start).Background(ui.Transparent).BorderWidth(0).Children(func() {
				ui.Icon(c, homeSettings).FontSize(17).TextColor(homeMuted)
				ui.Text(c, "设置").FontSize(13).TextColor(homeInk)
			}).OnClick(func() { a.navigate("设置", 0) })
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
				ui.Text(c, "本机服务 · "+status).FontSize(10).TextColor(homeMuted).Tooltip(a.service)
			})
		})
	})
}

func (a *App) overviewProjectGrid(c *ui.Context) {
	v := &a.overviewState
	ui.Column(c).Gap(15).Children(func() {
		head := ui.Row(c).Children(func() {
			ui.Text(c, fmt.Sprintf("项目入口  %d", len(a.Config.Projects))).FontSize(14).Bold().TextColor(homeInk)
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
			ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
				for col := 0; col < 3; col++ {
					idx := start + col
					if idx >= count {
						ui.Column(c).Width(0).Grow(1)
						continue
					}
					p := a.Config.Projects[idx]
					ui.Button(c, "").Key("home-project-"+p.ID).Label(p.Name+" · "+p.Path+" · 查看项目知识").Width(0).Grow(1).MinWidth(0).MinHeight(94).Padding(15).Gap(11).Justify(ui.Start).Border(1, homeBorder).Radius(8).Background(ui.Hex("#ffffff")).Children(func() {
						ui.Column(c).Size(30, 31).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Radius(6).Background(ui.Hex("#edf3f8")).Children(func() { ui.Icon(c, folder).FontSize(16).TextColor(homeMuted) })
						ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
							ui.Text(c, p.Name).FontSize(12).LineHeight(1.6).MaxLines(2).TextColor(homeInk)
							path := p.Path
							if path == "" {
								path = "未关联本机路径"
							}
							ui.Text(c, path).FontSize(10).LineHeight(1.5).TextColor(homeMuted)
						})
						ui.Text(c, "›").FontSize(14).TextColor(homeMuted).Shrink(0)
					}).OnClick(func() { a.navigate("知识", idx) })
				}
			})
		}
	})
}
