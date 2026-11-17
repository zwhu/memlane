package simpledesktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type projectReadingState struct {
	MenuOpen, InfoOpen bool
	SidebarMenuOpen    bool
	SidebarProject     string
	InfoKey            string
	Expanded           map[string]bool
	Copied             string
	CopyUntil          time.Time
	InfoScroll         ui.ScrollState
}
type readingLocation struct {
	Page, Kind, ProjectID string
	Index                 int
	FromOverview          bool
	Floors                map[*pane]int
	SettingsParent        *readingLocation
	SettingsProject       string
	FeedDetail            bool
}

func (a *App) readingLocation() *readingLocation {
	p, _ := a.current()
	location := &readingLocation{Page: a.page, Kind: a.kind, ProjectID: p.ID, Index: a.project, FromOverview: a.fromOverview, Floors: a.overviewSourceFloor}
	location.FeedDetail = a.state().Knowledge.Detail
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
	b := ui.Button(c, "").Cursor(ui.CursorPointer).Label(label).Tooltip(label).MinWidth(28).MinHeight(28).Padding(5).Gap(6)
	return b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(17).TextColor(homeLinkInk)
		}
	}).OnClick(fn)
}
func (a *App) projectHeader(c *ui.Context) {
	appToolbar(c, "页面工具栏", func() {
		var back func()
		label := "返回总览"
		if a.searchOrigin != nil && a.searchPane == a.state() {
			if len(a.state().History) > 0 {
				back = func() { a.returnFromSource(a.state()) }
				label = "返回搜索结果"
			} else {
				back = a.returnToSearch
				label = "返回搜索"
			}
		} else if a.page == "检索结果" {
			if len(a.state().History) > 0 {
				back = func() { a.returnFromSource(a.state()) }
				label = "返回检索结果"
			} else {
				back = a.returnToSearch
				label = "返回搜索"
			}
		} else if (a.page == "知识" || a.page == "通用知识") && len(a.state().History) > a.overviewSourceFloor[a.state()] {
			back = func() { a.returnFromSource(a.state()) }
			label = "返回记忆"
		} else if a.page == "设置" && a.settingsOrigin != nil {
			back = func() { v := a.settingsOrigin; a.restoreReadingLocation(v) }
			label = "返回项目"
			switch a.settingsOrigin.Page {
			case "通用知识", "总览", "设置", "检索结果":
				label = "返回" + memoryKindLabel(a.settingsOrigin.Page)
			}
		}
		if back != nil {
			readingAction(c, label, readingBack, back)
		}
		if _, ok := a.current(); ok && (a.page == "知识" || a.page == "图谱" || (a.page == "检索结果" && a.project >= 0)) {
			ui.Button(c, "").Cursor(ui.CursorPointer).Label("项目设置").Height(30).Padding(0).Gap(6).BorderWidth(0).Background(ui.Transparent).TextColor(homeNavInk).Children(func() {
				ui.Icon(c, homeSettings).FontSize(15).TextColor(homeIconInk)
				ui.Text(c, "项目设置").FontSize(12)
			}).OnClick(func() { a.openProjectSettings(a.project) })
		}
		ui.Spacer(c)
		if a.page == "通用知识" {
			anchor := readingAction(c, "通用记忆菜单", readingMore, func() { a.commonPage.MenuOpen = !a.commonPage.MenuOpen })
			ui.Popover(c, anchor, &a.commonPage.MenuOpen, func() {
				ui.Column(c).Width(288).Padding(12).Gap(12).Children(func() {
					if a.Config.ProjectControl == nil {
						homeText(c, "尚未配置记忆库存储服务", homeMeta)
					} else {
						a.commonDatasetSettings(c)
					}
				})
			})
		}

		a.unifiedSearchTrigger(c)
	}).Height(48).Padding(0, 12).Background(shellBackground).BorderWidth(0).DragWindow()
}

// One stable tab host, with the reading divider continuing to the toolbar.
func (a *App) projectNavigation(c *ui.Context) {
	if a.layout.ProjectListWidth == 0 {
		a.layout.ProjectListWidth = 300
	}
	ui.Row(c.Key("project-view-navigation")).Shrink(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(a.layout.ProjectListWidth).Shrink(0).Padding(pageTopInset, pageInset, 0, pageInset).Children(func() { a.projectTabs(c) })
		line := homeBorder
		if a.page == "图谱" || (a.page == "知识" && a.kind == "知识") {
			line = ui.Transparent
		}
		ui.Column(c).Width(1).Shrink(0).Background(line)
		ui.Spacer(c)
	})
}

func (a *App) projectTabs(c *ui.Context) {
	labels := []string{"记忆", "原始记录", "图谱"}
	selected := 0
	if a.page == "图谱" {
		selected = 2
	} else if a.kind != "知识" {
		selected = 1
	}
	ui.Tabs(c, &selected, labels...).Label("项目视图").Margin(0, -12).FillWidth().OnChange(func() {
		a.projectUI.InfoOpen = false
		if selected == 2 {
			a.navigate("图谱", a.project)
			a.loadGraph()
			return
		}
		a.switchProjectKind([]string{"知识", "原始记录", "图谱"}[selected])
	})
}

func (a *App) projectReading(c *ui.Context) {
	s := a.state()
	if a.page == "检索结果" {
		a.projectDetail(c, s)
		return
	}
	if a.kind == "知识" {
		a.knowledgePage(c, s)
		return
	}
	readingSplit(c, "project-reading-split", &a.layout.ProjectListWidth, func() {
		ui.Column(c).Fill().Children(func() {
			ui.Column(c.Key("project-view-navigation")).Padding(pageTopInset, pageInset, 0, pageInset).Shrink(0).Children(func() { a.projectTabs(c) })
			ui.Column(c).Padding(controlGap, pageInset, controlGap, pageInset).Gap(controlGap).Shrink(0).Children(func() {
				if a.kind != "知识" {
					labels := []string{"全部", "会话", "文档"}
					selected := 0
					if a.kind == "会话" {
						selected = 1
					} else if a.kind == "文档" {
						selected = 2
					}
					ui.Segmented(c, &selected, labels...).Label("原始记录筛选").FillWidth().OnChange(func() {
						kind := labels[selected]
						if selected == 0 {
							kind = "原始记录"
						}
						a.switchProjectKind(kind)
					})
				}
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					label := projectListStatus(s)
					if s.Loading && s.Loaded {
						label += " · 更新中"
					}
					homeText(c, label, homeMeta)
					ui.Spacer(c)
					refreshAction(c, "刷新列表", func() { a.load(s) })
				})
				if s.Error != "" {
					homeText(c, "未刷新 · "+s.Error, homeMeta).MaxLines(3)
					projectLink(c, "重试读取", func() { a.load(s) })
				}
			})
			a.rawRecordsAutoLoad(s)
			a.itemList(c, s, false)
			appFooter(c, "原始记录加载状态").Padding(0, pageInset).Children(func() {
				homeText(c, projectListStatus(s), homeMeta)
				ui.Spacer(c)
				if s.Loaded && !s.Loading && s.Error == "" {
					if s.More {
						homeText(c, "向下滚动加载更多", homeMeta)
					} else {
						homeText(c, "已加载全部", homeMeta)
					}
				}
			})
		})
	}, func() { a.projectDetail(c, s) })
}
func projectDisplayTitle(i Item) string {
	if i.Session != nil {
		return sessionIdentity(i.ID)
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
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if s.Selected == nil {
			if s.Locating {
				homeText(c, "正在定位记忆…", homeMeta).Margin(paneInset)
			} else {
				a.emptyReadingBackground(c)
			}
			appFooter(c, "项目阅读栏").Padding(0, paneInset)
			return
		}
		i := s.Selected
		key := i.key()
		appToolbar(c, "项目内容工具栏", func() {
			status := memoryKindLabel(i.Kind) + " · " + displayDate(i.Date)
			if s.Reading {
				status += " · 正在读取…"
			}
			row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
			row.Children(func() {
				homeText(c, status, homeMeta).SingleLine()
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
		}).Height(44)
		scrollKey := key
		if i.Session != nil && s.SessionTools {
			scrollKey += "/tools"
		}
		scroll := s.DetailScroll[scrollKey]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[scrollKey] = scroll
		}
		ui.Scroll(c.Key("project-detail-"+scrollKey)).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(controlGap, paneInset, paneInset, paneInset).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(820).Gap(12).Children(func() {
				title := projectDisplayTitle(*i)
				if title != "" {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						t := homeText(c, title, homeHeading).FontSize(22).LineHeight(1.41).MinWidth(0)
						if !a.projectUI.Expanded[key] {
							t.MaxLines(3)
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
				a.projectReadingBody(c, s, i)
				knowledgeBasis(c, *i)
				if i.Source != "" || i.SourceDocument != "" {
					ui.Row(c).FillWidth().BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Padding(20, 0, 0, 0).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, homeDocument).FontSize(18).TextColor(homeMuted)
						ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
							homeText(c, "出处 · "+readingSourceLabel(*i), homeBody).FontSize(12).MaxLines(2)
							homeText(c, "官方关联材料", homeMeta)
						})
						label := "查看来源会话  ›"
						if i.SourceDocument != "" {
							label = "查看来源文档  ›"
						}
						projectLink(c, label, func() { a.projectUI.InfoOpen = false; a.source(s) })
					})
				} else if i.Kind == "知识" {
					homeText(c, "官方未提供可定位的会话出处", homeMeta)
				}
			})
		})
		appFooter(c, "项目阅读栏").Padding(0, paneInset).Children(func() {
			homeText(c, "Cognee 提供 · 只读", homeMeta)
			if time.Now().Before(a.projectUI.CopyUntil) {
				ui.Spacer(c)
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
		})
	})
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
func (a *App) projectReadingBody(c *ui.Context, s *pane, i *Item) {
	presentation := itemPresentation(*i)
	if a.projectMD == nil {
		theme := markdown.Theme{}
		if a.md.Theme != nil {
			theme = *a.md.Theme
		}
		theme.FontSize = 13
		a.projectMD = &markdown.Options{Theme: &theme, CodeBlock: a.md.CodeBlock}
	}
	if i.Detail != nil {
		if sourceSession(s) {
			a.sourceContext(c, s)
		}
		paged := a.sessionHistoryControls(c, s, *i)
		homeText(c, sessionCoverage(*i.Detail), homeBody).FontSize(12)
		homeText(c, "同一 session 的连续记录；新消息更新此会话，不新增列表项。", homeMeta)
		if i.Title != "" && i.Title != i.ID && i.Title != "来源会话" {
			homeText(c, "已保存首条："+i.Title, homeMeta).MaxLines(2)
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
		for _, q := range qas {
			ui.Column(c).Padding(11, 14).Background(blue).Border(1, border).Radius(6).Gap(6).Children(func() {
				ui.Text(c, "用户").FontSize(10).TextColor(muted)
				markdown.View(c.Key("q-"+q.ID), q.Question, a.projectMD)
				if q.QuestionTruncated {
					ui.Text(c, "官方内容已截断").FontSize(10).TextColor(muted)
				}
			})
			ui.Text(c, "Agent").FontSize(10).TextColor(muted)
			markdown.View(c.Key("a-"+q.ID), q.Answer, a.projectMD)
			if q.AnswerTruncated {
				ui.Text(c, "官方内容已截断").FontSize(10).TextColor(muted)
			}
		}
		for _, trace := range traces {
			t := trace
			key := i.key() + "/" + t.ID
			label := "› " + t.Name + " · " + t.Status
			if s.Tools[key] {
				label = "⌄ " + t.Name + " · " + t.Status
			}
			ui.Column(c).Border(1, border).Radius(6).Padding(8).Children(func() {
				button(c, label, func() { s.Tools[key] = !s.Tools[key] })
				if s.Tools[key] {
					markdown.View(c.Key("tool-"+key), "```json\n"+string(t.Params)+"\n```\n\n```json\n"+string(t.Result)+"\n```\n\n"+t.Error, a.projectMD)
				}
			})
		}
		if paged {
			a.sessionLoadMore(c, s, *i)
		}
	} else {
		markdown.View(c.Key("body-"+i.key()), presentation.VisibleBody, a.projectMD)
		if presentation.Unrecognized {
			ui.Text(c, "原文格式未识别，保留原文").FontSize(10).TextColor(muted)
		}
	}
}

func (a *App) switchProjectKind(kind string) {
	if a.page != "知识" {
		a.navigateKind("知识", a.project, kind)
		return
	}
	if a.kind == kind {
		return
	}
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
	n := a.state()
	// Each filter keeps its reading position, but its metadata snapshot can be
	// older than another filter's. Revalidate on entry while retaining content.
	if kind == "知识" {
		a.loadKnowledgeFeed(n)
	} else {
		a.load(n)
	}
}

func projectListStatus(s *pane) string {
	if !s.Loaded {
		if s.Error != "" && !s.Loading {
			return "尚未载入"
		}
		return "正在读取"
	}
	return fmt.Sprintf("已载入 %d 条", len(s.Items))
}

func projectLink(c *ui.Context, label string, fn func()) ui.Element {
	return homeLink(c, label, fn).AlignSelf(ui.Start).Justify(ui.Start)
}
