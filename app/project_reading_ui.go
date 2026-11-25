package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

type projectReadingState struct {
	MenuOpen, InfoOpen   bool
	SidebarMenuOpen      bool
	SidebarProject       string
	UnreadPopoverOpen    bool
	UnreadPopoverProject string
	InfoKey              string
	Expanded             map[string]bool
	Copied               string
	CopyUntil            time.Time
	InfoScroll           ui.ScrollState
}
type readingLocation struct {
	Page, Kind, ProjectID string
	Index                 int
	FromOverview          bool
	Floors                map[*pane]int
	SettingsParent        *readingLocation
	SettingsProject       string
	FeedDetail            bool
	SearchOrigin          *readingLocation
	SearchAnchor          *Item
	SearchIdentity        string
}

func (a *App) readingLocation() *readingLocation {
	p, _ := a.current()
	location := &readingLocation{Page: a.page, Kind: a.kind, ProjectID: p.ID, Index: a.project, FromOverview: a.fromOverview, Floors: a.overviewSourceFloor}
	location.FeedDetail = a.state().Knowledge.Detail
	if a.hasSearchOriginFor(a.state()) {
		location.SearchOrigin = a.searchOrigin
		location.SearchAnchor = a.searchAnchor
		location.SearchIdentity = a.readingIdentity
	}
	if a.page == "设置" {
		location.SettingsParent = a.settingsOrigin
		if a.settingsOrigin != nil {
			location.SettingsProject = a.access.OpenProject
		}
	}
	return location
}
func (a *App) restoreReadingLocation(v *readingLocation) {
	if v == nil {
		return
	}
	index := v.Index
	ps := a.Config.Projects
	if v.Page == "通用知识" {
		ps = a.Config.Common
	}
	if v.ProjectID != "" {
		index = -1
		for n, p := range ps {
			if p.ID == v.ProjectID {
				index = n
				break
			}
		}
		if index < 0 {
			a.navigate("总览", 0)
			return
		}
	}
	a.navigateKind(v.Page, index, v.Kind)
	a.state().Knowledge.Detail = v.FeedDetail
	if v.Page == "设置" {
		a.settingsOrigin = v.SettingsParent
		a.access.OpenProject = v.SettingsProject
	}
	a.fromOverview = v.FromOverview
	a.overviewSourceFloor = v.Floors
	if p, ok := a.current(); ok && v.SearchOrigin != nil && v.SearchAnchor != nil && v.SearchIdentity == a.readingIdentity && v.SearchAnchor.Dataset == p.Dataset {
		a.searchOrigin = v.SearchOrigin
		a.searchPane = a.state()
		a.searchAnchor = v.SearchAnchor
	}
	if v.Page == "图谱" {
		a.loadGraph()
	}
}

var readingBack = homeSVG(`<path d="m15 5-7 7 7 7"/>`)
var readingMore = homeSVG(`<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`)
var readingInfo = homeSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 6v1"/>`)
var readingCopy = homeSVG(`<path d="M8 8h12v13H8zM4 16V3h12"/>`)
var readingClose = homeSVG(`<path d="m6 6 12 12M18 6 6 18"/>`)

func readingAction(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Cursor(ui.CursorPointer).Label(label).Tooltip(label).
		MinWidth(26).MinHeight(26).Padding(5).Gap(6).Radius(radius).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center)
	b.Background(homeHoverColor(b, "reading-act-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(b, 0.94)
	icoCol := tok.Text3
	if b.Hovered() {
		icoCol = tok.Text1
	}
	return b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(15).TextColor(icoCol)
		}
	}).OnClick(fn)
}
func (a *App) projectHeader(c *ui.Context) {
	appToolbar(c, "页面工具栏", func() {
		var back func()
		label := "返回总览"
		if a.hasSearchOriginFor(a.state()) {
			if len(a.state().History) > 0 {
				back = func() { a.returnFromSource(a.state()) }
				label = "返回搜索结果"
			} else {
				back = a.returnToSearch
				label = "返回搜索"
			}
		} else if (a.page == "知识" || a.page == "通用知识") && len(a.state().History) > a.overviewSourceFloor[a.state()] {
			back = func() { a.returnFromSource(a.state()) }
			label = "返回记忆"
		} else if (a.page == "设置" || (a.page == "知识" && a.kind == "管理")) && a.settingsOrigin != nil && a.settingsOrigin.Page != "知识" {
			back = func() { v := a.settingsOrigin; a.restoreReadingLocation(v) }
			label = "返回项目"
			switch a.settingsOrigin.Page {
			case "通用知识", "总览", "设置":
				label = "返回" + memoryKindLabel(a.settingsOrigin.Page)
			}
		}
		if back != nil {
			readingAction(c, label, readingBack, back)
		}
		// V4: fused titlebar displays current page title, or project name + path.
		switch {
		case a.page == "总览":
			homeText(c, "总览", homeTopTitle).SingleLine()
		case a.page == "通用知识":
			ui.Icon(c, v4IconCommon).FontSize(16).TextColor(tok.Text2).Shrink(0)
			homeText(c, "通用记忆", homeTopTitle).SingleLine()
		case a.page == "用户画像":
			ui.Icon(c, v4IconProfile).FontSize(16).TextColor(tok.Text2).Shrink(0)
			homeText(c, "用户画像", homeTopTitle).SingleLine()
		case a.page == "设置":
			title := "设置"
			if a.settingsOrigin != nil {
				title = "项目设置"
			}
			ui.Icon(c, v4IconSettings).FontSize(16).TextColor(tok.Text2).Shrink(0)
			homeText(c, title, homeTopTitle).SingleLine()
		case a.page == "知识" || a.page == "图谱":
			if p, ok := a.current(); ok {
				ui.Icon(c, v4IconFolder).FontSize(16).TextColor(tok.Text2).Shrink(0)
				homeText(c, p.Name, homeTopTitle).SingleLine()
				if p.Path != "" {
					homeText(c, p.Path, homeMeta).SingleLine().MaxLines(1).MinWidth(0)
				}
			}
		}
		ui.Spacer(c)
		if a.page == "通用知识" {
			var anchor ui.Element
			ui.Row(c).AlignItems(ui.Center).Gap(sp2).Shrink(0).Children(func() {
				if a.Config.ProjectControl != nil && a.access.Loaded {
					if label := a.settingsController.CommonAccessLabel(); label != "" {
						v4SmallButton(c, label, nil, func() { a.commonPage.MenuOpen = !a.commonPage.MenuOpen }).
							Tooltip("按 Agent 设置通用记忆的只读访问权限")
					}
				}
				anchor = readingAction(c, "通用记忆菜单", readingMore, func() { a.commonPage.MenuOpen = !a.commonPage.MenuOpen })
			})
			ui.Popover(c, anchor, &a.commonPage.MenuOpen, func() {
				ui.Column(c).Width(360).Padding(12).Gap(12).Children(func() {
					if a.Config.ProjectControl == nil {
						homeText(c, "尚未配置记忆库存储服务", homeMeta)
					} else {
						a.settingsController.ViewCommonPermissions(c)
					}
				})
			})
		}
		a.unifiedSearchTrigger(c)
	}).Height(titlebarH).Padding(0, sp4, 0, sp6).Background(tok.Bg).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).DragWindow()
}

// One stable tab host, with the reading divider continuing to the toolbar.
func (a *App) projectNavigation(c *ui.Context) {
	if a.layout.ProjectListWidth == 0 {
		a.layout.ProjectListWidth = listPaneW
	}
	ui.Row(c.Key("project-view-navigation")).Label("项目列表头部").Height(listHeaderH).Shrink(0).Padding(0, sp4).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).AlignItems(ui.Center).Children(func() {
		a.projectTabs(c)
		ui.Spacer(c)
	})
}

func (a *App) projectTabs(c *ui.Context) {
	labels := []string{"记忆", "原始记录", "图谱", "管理"}
	selected := 0
	switch {
	case a.page == "图谱":
		selected = 2
	case a.kind == "管理" || (a.page == "设置" && a.settingsOrigin != nil):
		selected = 3
	case a.kind != "知识":
		selected = 1
	}
	v4UnderlineTabs(c, "project-view-tabs", "项目视图", &selected, labels, func() {
		a.projectUI.InfoOpen = false
		switch selected {
		case 2:
			a.navigate("图谱", a.project)
			a.loadGraph()
		case 3:
			a.openProjectSettings(a.project)
		default:
			a.switchProjectKind([]string{"知识", "原始记录"}[selected])
		}
	})
}

func (a *App) projectReading(c *ui.Context) {
	if a.kind == "管理" {
		ui.Column(c.Key("project-management-page")).Fill().Background(tok.Bg).Children(func() {
			a.projectNavigation(c)
			a.settingsController.ViewProject(c, a.access.OpenProject)
		})
		return
	}
	s := a.state()
	if a.kind == "知识" {
		a.knowledgePage(c, s)
		return
	}
	readingSplit(c, "project-reading-split", &a.layout.ProjectListWidth, func() {
		ui.Column(c.Key("raw-list-pane")).Fill().Background(tok.Bg).Children(func() {
			ui.Row(c.Key("project-view-navigation")).Label("项目列表头部").Height(listHeaderH).Shrink(0).Padding(0, sp4).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).AlignItems(ui.Center).Children(func() {
				a.projectTabs(c)
				ui.Spacer(c)
			})
			bodyKey := fmt.Sprintf("raw-list-body-%d", a.motionRuns.rawPage)
			body := ui.Column(c.Key(bodyKey)).Grow(1).MinHeight(0).FillWidth()
			a.v4PageEnter(c, body, "raw-list-body", a.motionRuns.rawPage)
			body.Children(func() {
				ui.Column(c).FillWidth().Padding(sp3, sp4, sp2, sp4).Gap(sp2).Shrink(0).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						labels := []string{"全部", "会话", "文档"}
						selected := 0
						if a.kind == "会话" {
							selected = 1
						} else if a.kind == "文档" {
							selected = 2
						}
						v4Segmented(c, "raw-kind-seg", "原始记录筛选", &selected, labels, func() {
							kind := labels[selected]
							if selected == 0 {
								kind = "原始记录"
							}
							a.switchProjectKind(kind)
						})
						ui.Spacer(c)
						refreshAction(c, "刷新列表", func() { a.load(s) })
					})
					if s.Error != "" {
						ui.Row(c).FillWidth().Padding(sp1, 0, 0, 0).Gap(sp2).AlignItems(ui.Center).Shrink(0).Children(func() {
							homeText(c, "读取未完成 · "+s.Error, homeMeta).MaxLines(2)
							projectLink(c, "重试读取", func() { a.load(s) })
						})
					}
				})
				a.rawRecordsAutoLoad(s)
				a.itemList(c, s, false)
			})
			totalRaw := max(len(s.RawCatalog), len(s.Items))
			appFooter(c, "原始记录加载状态").Padding(0, sp4).Children(func() {
				if s.Loaded || len(s.Items) > 0 {
					homeText(c, fmt.Sprintf("已载入 %d 项", len(s.Items)), homeMeta)
				}
				ui.Spacer(c)
				switch {
				case !s.Loaded || s.Error != "":
				case s.RawV2Mode && s.More:
					homeText(c, "向下滚动加载更多", homeMeta)
				case s.More:
					homeText(c, fmt.Sprintf("共 %d 条 · 向下滚动加载", totalRaw), homeMeta)
				case totalRaw > 0:
					homeText(c, fmt.Sprintf("共 %d 条", totalRaw), homeMeta)
				}
			})
		})
	}, func() { a.projectDetail(c, s) })
}
