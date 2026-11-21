package simpledesktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type projectReadingState struct {
	MenuOpen, InfoOpen   bool
	SidebarMenuOpen      bool
	SidebarProject       string
	UnreadPopoverOpen    bool
	UnreadPopoverProject string
	InfoKey              string
	Expanded           map[string]bool
	Copied             string
	CopyUntil          time.Time
	InfoScroll         ui.ScrollState
	SessionDrifts      map[string]*projectDriftState
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
					if label := commonAccessButtonLabel(a.access.Directory); label != "" {
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
						a.commonDatasetSettings(c)
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
			a.projectSettingsPage(c)
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
func projectDisplayTitle(i Item) string {
	if i.Session != nil {
		if sessionHasTopic(i) {
			return strings.TrimSpace(i.Title)
		}
		return sessionFallbackTitle(i)
	}
	if i.Kind == "知识" {
		if i.Document != nil {
			return readableDocumentTitle(*i.Document)
		}
		return ""
	}
	return itemPresentation(i).DisplayTitle
}
func (a *App) projectDetail(c *ui.Context, s *pane) {
	ui.Column(c.Key("project-detail-panel")).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		if s.Selected == nil {
			switch {
			case s.Locating:
				v4EmptyState(c, "reader-locating", "正在定位记忆…", "")
			case !s.Loaded && s.Loading && s.Error == "":
				ui.Column(c).Grow(1).MinHeight(0).FillWidth()
			case a.kind != "知识":
				v4EmptyState(c, "reader-empty", "选择一条原始记录", "")
			default:
				v4EmptyState(c, "reader-empty", "选择一条记忆", "")
			}
			appFooter(c, "项目阅读栏").Padding(0, sp6).Children(func() {
				homeText(c, "Cognee 提供 · 只读", homeMeta)
			})
			return
		}
		i := s.Selected
		key := i.key()
<<<<<<< HEAD
		paged := i.Detail != nil && sessionHistoryFor(s, *i) != nil
=======
		appToolbar(c, "项目内容工具栏", func() {
			ui.Row(c).FillWidth().MaxWidth(readerMaxW).AlignItems(ui.Center).Gap(sp2).Children(func() {
				agentLogo(c, recentAgentIcon(*i), 18)
				status := sourceAgentName(*i)
				if p, ok := a.current(); ok && p.Name != "" {
					status += " · " + p.Name
				}
				status += " · " + displayDate(i.Date)
				row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
				row.Children(func() {
					homeText(c, status, homeBody).SingleLine()
					hoverCopyAction(c, row, "复制内容", func() { c.WriteClipboard(sessionCopyBody(s, *i)); a.copyFeedback("内容") }).Disabled(!(i.BodyLoaded || i.Detail != nil || (i.Document == nil && i.Preview != "")))
				})
				knowledgeRecallLabel(c, *i)
				knowledgeBasisTag(c, *i)
				ui.Spacer(c)
				anchor := readingAction(c, "内容信息", readingInfo, func() {
					a.projectUI.InfoOpen = !a.projectUI.InfoOpen
					a.projectUI.InfoKey = key
					a.projectUI.MenuOpen = false
				})
				if a.projectUI.InfoKey != key {
					a.projectUI.InfoOpen = false
				}
				a.contentInformation(c, anchor, *i)
			})
		}).Height(44).Padding(0, sp10)
>>>>>>> aa8b7eb (perf(desktop): land P1 v2 cursor APIs, body fastpath, reading-state sync, sliding window, and 3-lane search)
		scrollKey := key
		if i.Session != nil && s.SessionSubagents && len(i.Subagents) > 0 {
			scrollKey += "/subagents"
		} else if i.Session != nil && s.SessionTools {
			scrollKey += "/tools"
		}
		scroll := projectDetailScroll(s, scrollKey)
		if a.motionRuns.docScrollKey != key {
			a.motionRuns.docScrollKey = key
			a.motionRuns.docEnter++
		}
		bodyLoading := !i.BodyLoaded && i.Detail == nil && i.Preview == "" && i.Body == "" && s.Reading
		bodyState := scrollKey + ":ready"
		if bodyLoading {
			bodyState = scrollKey + ":loading"
		}
		if a.motionRuns.docBodyKey != bodyState {
			a.motionRuns.docBodyKey = bodyState
			if !bodyLoading {
				a.motionRuns.docBodyFade++
			}
		}
		if !bodyLoading && i.Detail != nil && paged {
			a.checkSessionAutoLoad(s, *i, scroll)
		}

		headerBorder := ui.Transparent
		if paged || scroll.Y > 2 {
			headerBorder = tok.Border
		}
		ui.Column(c.Key("project-detail-header")).FillWidth().Shrink(0).Padding(sp6, sp10, sp2, sp10).
			BorderWidth(0, 0, 1, 0).BorderColor(headerBorder).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp3).Children(func() {
				a.projectDetailHeader(c, s, i, paged)
			})
		})
<<<<<<< HEAD

		fadeKey := fmt.Sprintf("project-detail-body-%d", a.motionRuns.docBodyFade)
		bodyHost := ui.Column(c.Key(fadeKey)).Grow(1).MinHeight(0).FillWidth()
		if !bodyLoading {
			a.v4ContentFade(c, bodyHost, "detail-body", a.motionRuns.docBodyFade)
		}
		bodyHost.Children(func() {
			switch {
			case bodyLoading:
				ui.Column(c).Grow(1).FillWidth().Padding(sp3, sp10, sp8, sp10).Children(func() {
					ui.Column(c).FillWidth().MaxWidth(readerMaxW).Children(func() {
						v4SkeletonGroup(c, 3, 2, false, false)
					})
				})
			case i.Detail != nil:
				rows := buildSessionDetailRows(a, s, *i, paged)
				a.renderDetailRowList(c, s, *i, scrollKey, scroll, rows)
			default:
				presentation := itemPresentation(*i)
				if shouldVirtualizeMarkdown(presentation.VisibleBody) {
					rows := buildDocumentDetailRows(a, *i, presentation)
					a.renderDetailRowList(c, s, *i, scrollKey, scroll, rows)
				} else {
					ui.Scroll(c.Key("project-detail-scroll-"+scrollKey)).Grow(1).MinHeight(0).FillWidth().TrackScroll(scroll).Padding(sp3, sp10, sp8, sp10).Children(func() {
						ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp4).Children(func() {
							markdown.View(c.Key("body-"+key), presentation.VisibleBody, a.v4MarkdownOptions(fsReading))
							if presentation.Unrecognized {
								ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
							}
							a.projectDetailTail(c, s, i)
						})
					})
				}
			}
		})

		canCopy := i.BodyLoaded || i.Detail != nil || i.Preview != ""
=======
		canCopy := i.BodyLoaded || i.Detail != nil || (i.Document == nil && i.Preview != "")
>>>>>>> aa8b7eb (perf(desktop): land P1 v2 cursor APIs, body fastpath, reading-state sync, sliding window, and 3-lane search)
		appFooter(c, "项目阅读栏").Padding(0, sp6).Gap(sp2).Children(func() {
			homeText(c, "Cognee 提供 · 只读", homeMeta)
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
			v4OutlineButton(c, "复制正文", v4IconCopy, func() {
				c.WriteClipboard(sessionCopyBody(s, *i))
				a.copyFeedback("正文")
			}).Disabled(!canCopy)
		})
	})
}

func (a *App) projectDetailHeader(c *ui.Context, s *pane, i *Item, paged bool) {
	key := i.key()
	if sourceSession(s) {
		a.sourceContext(c, s)
	}
	a.subagentContext(c, s, *i)
	ui.Row(c).Label("项目内容工具栏").FillWidth().MinHeight(28).Padding(0, 0, sp4, 0).Gap(sp2).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		agentLogo(c, recentAgentIcon(*i), 18)
		status := sourceAgentName(*i)
		if p, ok := a.current(); ok && p.Name != "" {
			status += " · " + p.Name
		}
		status += " · " + displayDate(i.Date)
		row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
		row.Children(func() {
			homeText(c, status, homeBody).SingleLine()
			hoverCopyAction(c, row, "复制内容", func() { c.WriteClipboard(sessionCopyBody(s, *i)); a.copyFeedback("内容") }).Disabled(!(i.BodyLoaded || i.Detail != nil || i.Preview != ""))
		})
		knowledgeRecallLabel(c, *i)
		knowledgeBasisTag(c, *i)
		ui.Spacer(c)
		anchor := readingAction(c, "内容信息", readingInfo, func() {
			a.projectUI.InfoOpen = !a.projectUI.InfoOpen
			a.projectUI.InfoKey = key
			a.projectUI.MenuOpen = false
		})
		if a.projectUI.InfoKey != key {
			a.projectUI.InfoOpen = false
		}
		a.contentInformation(c, anchor, *i)
	})
	title := projectDisplayTitle(*i)
	if title != "" {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			t := homeText(c, title, homeHeading).FontSize(fsTitle).LineHeight(1.35).MinWidth(0)
			if !a.projectUI.Expanded[key] {
				t.MaxLines(3)
			}
			if i.Session != nil {
				if idLabel := sessionIdentity(i.ID); idLabel != title {
					homeText(c, idLabel, homeMeta)
				}
			}
			if len([]rune(title)) > 80 {
				label := "展开完整标题"
				if a.projectUI.Expanded[key] {
					label = "收起标题"
				}
				projectLink(c, label, func() {
					if a.projectUI.Expanded == nil {
						a.projectUI.Expanded = map[string]bool{}
					}
					a.projectUI.Expanded[key] = !a.projectUI.Expanded[key]
				})
			}
		})
	}
	if s.Error != "" {
		homeText(c, "内容未刷新 · "+s.Error, homeMeta)
	}
	if paged {
		a.sessionHistoryControls(c, s, *i)
	}
}

func (a *App) projectDetailTail(c *ui.Context, s *pane, i *Item) {
	knowledgeBasis(c, *i)
	if i.Source != "" || i.SourceDocument != "" {
		ui.Row(c).FillWidth().Margin(sp4, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Padding(sp4, 0, 0, 0).Gap(sp3).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, v4IconDoc).FontSize(16).TextColor(tok.Text3).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, "出处 · "+readingSourceLabel(*i)).Font("system-ui").FontSize(fsFootnote).FontWeight(550).TextColor(tok.Text1).MaxLines(2)
				sub := "官方关联材料"
				if i.Source != "" {
					sub = sessionIdentity(i.Source)
				}
				homeText(c, sub, homeMeta).SingleLine()
				a.itemSessionDriftNote(c, *i)
			})
			label := "查看来源会话"
			if i.SourceDocument != "" {
				label = "查看来源文档"
			}
			loadingSource := s.Reading && s.ReadingSource
			if loadingSource {
				label = "正在读取…"
			}
			v4OutlineButton(c, label, nil, func() { a.projectUI.InfoOpen = false; a.source(s) }).Disabled(loadingSource)
		})
	} else if i.Kind == "知识" {
		homeText(c, "官方未提供可定位的会话出处", homeMeta).Margin(sp3, 0, 0, 0)
	}
}
func (a *App) copyFeedback(name string) {
	a.projectUI.Copied = name
	a.projectUI.CopyUntil = time.Now().Add(3 * time.Second)
}
func (a *App) contentInformation(c *ui.Context, anchor ui.Element, i Item) {
	_, h := c.Size()
	fields := itemPresentation(i).TechnicalFields
	if len(fields) == 0 {
		fields = []ReadingField{{"数据组 ID", i.Dataset}, {"对象 ID", i.ID}}
	}
	count := 0
	for _, f := range fields {
		if f.Value != "" {
			count++
		}
	}
	height := min(float32(400), h-160, float32(64+44*count))
	ui.Popover(c, anchor, &a.projectUI.InfoOpen, func() {
		ui.Row(c).Padding(10, 18).Shrink(0).AlignItems(ui.Center).Children(func() {
			homeText(c, "内容信息", homeHeading).FontSize(14)
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, "已复制", homeMeta)
			}
			readingAction(c, "关闭内容信息", readingClose, func() { a.projectUI.InfoOpen = false })
		})
		ui.Scroll(c.Key("content-info-fields-"+i.key())).Grow(1).MinHeight(0).TrackScroll(&a.projectUI.InfoScroll).Padding(0, 18, 14, 18).Gap(6).Children(func() {
			for _, field := range fields {
				f := field
				if f.Value == "" {
					continue
				}
				ui.Row(c).MinHeight(38).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						homeText(c, f.Name, homeMeta).FontSize(10)
						value := f.Value
						if strings.Contains(f.Name, "时间") {
							if t, ok := officialTime(f.Value); ok {
								value = t.In(time.Local).Format("2006-01-02 15:04:05 -07:00")
							}
						} else if r := []rune(value); len(r) > 32 {
							value = string(r[:16]) + "…" + string(r[len(r)-12:])
						}
						row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
						row.Children(func() {
							homeText(c, value, homeBody).FontSize(11).MinWidth(0).SingleLine().TextColor(homeInk).Tooltip(f.Value)
							hoverCopyAction(c, row, "复制"+f.Name, func() { c.WriteClipboard(f.Value); a.copyFeedback(f.Name) })
						})
					})
				})
			}
		})
	}).Modal().Role(ui.RoleDialog).Width(360).Height(height).Padding(0).Transition(projectOverlayMotion())
}
<<<<<<< HEAD
=======
func (a *App) projectReadingBody(c *ui.Context, s *pane, i *Item) {
	if (i.Document != nil && !i.BodyLoaded && s.Reading) || (!i.BodyLoaded && i.Detail == nil && i.Preview == "" && i.Body == "" && s.Reading) {
		v4SkeletonGroup(c, 3, 2, false, false)
		return
	}
	presentation := itemPresentation(*i)
	mdOpts := a.v4MarkdownOptions(fsReading)
	if i.Detail != nil {
		paged := a.sessionHistoryControls(c, s, *i)
		if paged && s.SessionSubagents && len(i.Subagents) > 0 {
			a.subagentsListView(c, s, *i)
			return
		}
		if !paged {
			homeText(c, sessionCoverage(*i.Detail), homeBody).FontSize(fsFootnote)
		}
		if !paged && (len(i.Detail.QAs) < i.Detail.Count || len(i.Detail.Traces) < i.Detail.ToolCount) {
			homeText(c, "当前是最近记录，可读取按时间排列的完整会话。", homeMeta)
			projectLink(c, "读取完整会话", func() { a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
		}
		if len(i.Detail.QAs) == 0 && (!paged || !s.SessionTools) {
			message := "此会话尚无已保存的问答；下方仅为已保存的工具记录。"
			if paged {
				message = "此会话尚无已保存的问答，可切换到工具记录查看。"
			}
			homeText(c, message, homeMeta)
		}
		if paged && s.SessionTools && i.Detail.ToolCount == 0 {
			homeText(c, "此会话尚无已保存的工具记录。", homeMeta)
		}
		qas := i.Detail.QAs
		traces := i.Detail.Traces
		if paged {
			if s.SessionTools {
				qas = nil
			} else {
				traces = nil
			}
		}
		promptRole := "用户"
		if i.Session != nil && i.Session.IsSubagent {
			promptRole = "上游消息"
		}
		for _, q := range qas {
			ui.Column(c).FillWidth().Padding(11, 14).Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Gap(6).Children(func() {
				ui.Text(c, promptRole).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3)
				markdown.View(c.Key("q-"+q.ID), q.Question, mdOpts)
				if q.QuestionTruncated {
					ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
				}
			})
			sourceAgentLabel(c, *i)
			markdown.View(c.Key("a-"+q.ID), q.Answer, mdOpts)
			if q.AnswerTruncated {
				ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
			}
		}
		for _, trace := range traces {
			t := trace
			key := i.key() + "/" + t.ID
			expanded := s.Tools[key]
			statusCol := tok.OK
			if strings.Contains(strings.ToLower(t.Status), "err") || strings.Contains(strings.ToLower(t.Status), "fail") || t.Error != "" {
				statusCol = tok.Danger
			}
			ui.Column(c).FillWidth().Border(1, tok.Border).Radius(radius).Background(tok.BgElevated).Children(func() {
				head := ui.ButtonBase(c.Key("tool-head-"+key)).Role(ui.RoleButton).Label(t.Name + " · " + t.Status).
					Cursor(ui.CursorPointer).FillWidth().Padding(sp2, sp3).Gap(sp2).AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0).Radius(radius)
				head.Background(homeHoverColor(head, "tool-bg", ui.Transparent, tok.Hover))
				head.Children(func() {
					ico := v4IconChev
					if expanded {
						ico = v4IconDown
					}
					ui.Icon(c, ico).FontSize(12).TextColor(tok.Text3).Shrink(0)
					ui.Box(c).Size(6, 6).Radius(3).Background(statusCol).Shrink(0)
					ui.Text(c, t.Name).Font("SF Mono").FontSize(fsFootnote).TextColor(tok.Text1).SingleLine().Grow(1).MinWidth(0)
					if t.Status != "" {
						homeText(c, t.Status, homeMeta).SingleLine().Shrink(0)
					}
				}).OnClick(func() { s.Tools[key] = !s.Tools[key] })
				if expanded {
					ui.Column(c).FillWidth().Padding(0, sp3, sp3, sp3).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
						markdown.View(c.Key("tool-"+key), "```json\n"+string(t.Params)+"\n```\n\n```json\n"+string(t.Result)+"\n```\n\n"+t.Error, mdOpts)
					})
				}
			})
		}
		if paged {
			a.sessionLoadMore(c, s, *i)
		}
	} else {
		markdown.View(c.Key("body-"+i.key()), presentation.VisibleBody, mdOpts)
		if i.BodyTruncated {
			ui.Text(c, "正文超过 8 MiB，已截断显示").FontSize(fsCaption).TextColor(tok.Text3)
		}
		if presentation.Unrecognized {
			ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
		}
	}
}

>>>>>>> aa8b7eb (perf(desktop): land P1 v2 cursor APIs, body fastpath, reading-state sync, sliding window, and 3-lane search)
func isRawRecordKind(kind string) bool {
	return kind == "原始记录" || kind == "会话" || kind == "文档"
}

func (a *App) switchProjectKind(kind string) {
	if a.page != "知识" {
		a.navigateKind("知识", a.project, kind)
		return
	}
	if a.kind == kind {
		return
	}
	oldKind := a.kind
	old := a.state()
	a.cancelTitles(old)
	if old.cancel != nil {
		old.cancel()
	}
	if old.readCancel != nil {
		old.readCancel()
	}
	old.Loading = false
	old.Reading = false
	a.epoch++
	a.kind = kind
	a.projectUI.InfoOpen = false
	if kind == "管理" {
		if p, ok := a.current(); ok {
			a.access.OpenProject = p.ID
		}
		return
	}
	n := a.state()
	// Each filter keeps its reading position, but its metadata snapshot can be
	// older than another filter's. Revalidate on entry while retaining content.
	if kind == "知识" {
		if n.Loaded && len(n.Items) > 0 {
			a.motionRuns.projectPage++
			a.motionRuns.projectList++
		} else {
			a.motionRuns.projectPage = 0
			a.motionRuns.projectList = 0
		}
		a.loadKnowledgeFeed(n)
		return
	}
	if isRawRecordKind(kind) {
		if isRawRecordKind(oldKind) {
			// Sub-filter switch within Raw Records keeps tabs/toolbar fixed and
			// seeds from old.RawCatalog so switching never flashes a skeleton.
			if !n.Loaded && len(old.RawCatalog) > 0 {
				seed := make([]Item, 0, len(old.RawCatalog))
				for _, item := range old.RawCatalog {
					if kind == "原始记录" || item.Kind == kind {
						seed = append(seed, item)
					}
				}
				end := min(30, len(seed))
				n.RawCatalog = seed
				n.Items = append([]Item(nil), seed[:end]...)
				n.More = end < len(seed)
				n.Loaded = true
				if n.Selected == nil && len(n.Items) > 0 {
					a.selectItem(n, n.Items[0])
				}
			}
			if n.Loaded && len(n.Items) > 0 {
				a.motionRuns.rawList++
			} else {
				a.motionRuns.rawList = 0
			}
		} else {
			if n.Loaded && len(n.Items) > 0 {
				a.motionRuns.rawPage++
				a.motionRuns.rawList++
			} else {
				a.motionRuns.rawPage = 0
				a.motionRuns.rawList = 0
			}
		}
	}
	a.load(n)
}

func projectListStatus(s *pane) string {
	if !s.Loaded {
		if s.Error != "" && !s.Loading {
			return "尚未载入"
		}
		return ""
	}
	return fmt.Sprintf("已载入 %d 条", len(s.Items))
}

func projectLink(c *ui.Context, label string, fn func()) ui.Element {
	return homeLink(c, label, fn).AlignSelf(ui.Start).Justify(ui.Start)
}
