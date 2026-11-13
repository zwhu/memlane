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
	QueueBack          *readingLocation
	QueueProject       string
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
}

func (a *App) readingLocation() *readingLocation {
	p, _ := a.current()
	location := &readingLocation{Page: a.page, Kind: a.kind, ProjectID: p.ID, Index: a.project, FromOverview: a.fromOverview, Floors: a.overviewSourceFloor}
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
	b := ui.Button(c, "").Label(label).Tooltip(label).MinWidth(28).MinHeight(28).Padding(5).Gap(6)
	return b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(17).TextColor(homeLinkInk)
		}
		if label == "复制内容" {
			homeText(c, label, homeAction)
		}
	}).OnClick(fn)
}
func (a *App) projectHeader(c *ui.Context) {
	appToolbar(c, "页面工具栏", func() {
		var back func()
		label := "返回总览"
		if a.page == "检索结果" {
			if len(a.state().History) > 0 {
				back = func() { a.returnFromSource(a.state()) }
				label = "返回检索结果"
			} else {
				back = a.returnToSearch
				label = "返回搜索"
			}
		} else if (a.page == "知识" || a.page == "通用知识") && len(a.state().History) > a.overviewSourceFloor[a.state()] {
			back = func() { a.returnFromSource(a.state()) }
			label = "返回知识"
		} else if a.page == "知识" && a.projectUI.QueueProject != "" {
			label = "返回项目设置"
			back = func() {
				origin, id := a.projectUI.QueueBack, a.projectUI.QueueProject
				index := a.project
				a.navigate("设置", index)
				a.access.OpenProject = id
				a.settingsOrigin = origin
			}
		} else if a.page == "设置" && a.settingsOrigin != nil {
			back = func() { v := a.settingsOrigin; a.restoreReadingLocation(v) }
			label = "返回项目"
		}
		if back != nil {
			readingAction(c, label, readingBack, back)
		}
		title := a.page
		if a.page == "设置" && a.settingsOrigin != nil {
			for _, p := range a.Config.Projects {
				if p.ID == a.access.OpenProject {
					title = p.Name + " · 设置"
					break
				}
			}
		}
		if a.page == "检索结果" && a.project < 0 {
			title = "通用知识 · 搜索结果"
		}
		if p, ok := a.current(); ok && (a.page == "知识" || a.page == "图谱" || a.page == "检索结果") {
			title = p.Name
		}
		homeText(c, title, homeTopTitle).FontSize(15).FontWeight(600).MaxWidth(280).SingleLine().Tooltip(title)
		ui.Spacer(c)
		a.unifiedSearchTrigger(c)
		if a.page == "通用知识" {
			anchor := readingAction(c, "通用知识菜单", readingMore, func() { a.commonPage.MenuOpen = !a.commonPage.MenuOpen })
			ui.Popover(c, anchor, &a.commonPage.MenuOpen, func() {
				ui.Column(c).Width(288).Padding(12).Gap(12).Children(func() {
					if a.Config.ProjectControl == nil {
						homeText(c, "尚未配置知识库存储服务", homeMeta)
					} else {
						a.commonDatasetSettings(c)
					}
				})
			})
		}
		if p, ok := a.current(); ok && (a.page == "知识" || a.page == "图谱") {

			readingAction(c, "项目菜单", readingMore, func() {}).PopoverMenuButton(func(m *ui.Menu) {
				m.Item(p.Name).Disabled(true)
				m.Separator()
				if m.Item("复制项目路径").Chosen() {
					c.WriteClipboard(p.Path)
				}
			})
		}
	}).BorderWidth(0, 0, 1, 0).BorderColor(homeBorder).Padding(0, 18, 0, 12).DragWindow()
}
func (a *App) projectTabs(c *ui.Context) {
	labels := []string{"知识", "原始记录", "图谱"}
	selected := 0
	if a.page == "图谱" {
		selected = 2
	} else if a.kind != "知识" {
		selected = 1
	}
	ui.Tabs(c, &selected, labels...).Label("项目视图").FillWidth().OnChange(func() {
		a.projectUI.InfoOpen = false
		if selected == 2 {
			a.navigate("图谱", a.project)
			a.loadGraph()
			return
		}
		a.switchProjectKind(labels[selected])
	})
}

func (a *App) projectReading(c *ui.Context) {
	s := a.state()
	if a.page == "检索结果" {
		a.projectDetail(c, s)
		return
	}
	readingSplit(c, "project-reading-split", &a.layout.ProjectListWidth, func() {
		ui.Column(c).Fill().Children(func() {
			ui.Column(c).Padding(24, 18, 5, 18).Gap(12).Shrink(0).Children(func() {
				a.projectTabs(c)
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
					homeText(c, label, homeMeta)
					ui.Spacer(c)
					readingAction(c, "刷新列表", homeRefresh, func() { a.load(s) })
				})
				if s.Loading {
					homeText(c, "正在读取…", homeMeta)
				}
				if s.Error != "" {
					homeText(c, "未刷新 · "+s.Error, homeMeta).MaxLines(3)
					projectLink(c, "重试读取", func() { a.load(s) })
				}
			})
			a.itemList(c, s, false)
			appFooter(c, "项目分页栏").Padding(0, 12).Children(func() {
				homeText(c, projectListStatus(s), homeMeta)
				ui.Spacer(c)
				readingAction(c, "上一页", readingBack, func() { s.Offset = max(0, s.Offset-30); a.load(s) }).Disabled(s.Offset == 0 || s.Loading)
				homeText(c, fmt.Sprint(s.Offset/30+1), homeMeta)
				readingAction(c, "下一页", homeArrow, func() { s.Offset += 30; a.load(s) }).Disabled(!s.More || s.Loading)
			})
		})
	}, func() { a.projectDetail(c, s) })
}
func projectDisplayTitle(i Item) string {
	if i.Document != nil && readableDocumentTitle(*i.Document) == "" && i.BodyLoaded {
		body := itemPresentation(i).VisibleBody
		if t := firstReadingSentenceLimit(body, len([]rune(body))); t != "" {
			return t
		}
	}
	return itemPresentation(i).DisplayTitle
}
func (a *App) projectDetail(c *ui.Context, s *pane) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if s.Selected == nil {
			a.emptyReadingBackground(c)
			appFooter(c, "项目阅读栏").Padding(0, 32)
			return
		}
		i := s.Selected
		key := i.key()
		appToolbar(c, "项目内容工具栏", func() {
			homeText(c, i.Kind+" · "+displayDate(i.Date), homeMeta).SingleLine()
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
			readingAction(c, "复制内容", readingCopy, func() { c.WriteClipboard(itemPresentation(*i).VisibleBody); a.copyFeedback("内容") }).Disabled(!(i.BodyLoaded || i.Detail != nil || i.Preview != ""))
		})
		scroll := s.DetailScroll[key]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[key] = scroll
		}
		ui.Scroll(c.Key("project-detail-"+key)).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(10, 32, 32, 32).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(820).Gap(20).Children(func() {
				title := projectDisplayTitle(*i)
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
				if s.Reading {
					homeText(c, "正在读取…", homeMeta)
				}
				if s.Error != "" {
					homeText(c, "内容未刷新 · "+s.Error, homeMeta)
				}
				a.projectReadingBody(c, s, i)
				if i.Source != "" || i.SourceDocument != "" {
					ui.Row(c).FillWidth().BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Padding(20, 0, 0, 0).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, homeDocument).FontSize(18).TextColor(homeMuted)
						ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
							homeText(c, "出处 · "+readingSourceLabel(*i), homeBody).FontSize(12).MaxLines(2)
							homeText(c, "官方关联材料", homeMeta)
						})
						projectLink(c, "查看原始记录  ›", func() { a.projectUI.InfoOpen = false; a.source(s) })
					})
				} else if i.Kind == "知识" {
					homeText(c, "官方未提供可定位的会话出处", homeMeta)
				}
			})
		})
		appFooter(c, "项目阅读栏").Padding(0, 32).Children(func() {
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
	height := min(float32(560), h-160, float32(148+62*count))
	ui.Popover(c, anchor, &a.projectUI.InfoOpen, func() {
		ui.Row(c).Padding(14, 20).Shrink(0).AlignItems(ui.Center).Children(func() {
			homeText(c, "内容信息", homeHeading).FontSize(15)
			ui.Spacer(c)
			readingAction(c, "关闭内容信息", readingClose, func() { a.projectUI.InfoOpen = false })
		})
		ui.Scroll(c.Key("content-info-fields-"+i.key())).Grow(1).MinHeight(0).TrackScroll(&a.projectUI.InfoScroll).Padding(0, 20).Gap(8).Children(func() {
			for _, field := range fields {
				f := field
				if f.Value == "" {
					continue
				}
				ui.Row(c).MinHeight(54).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						homeText(c, f.Name, homeMeta)
						value := f.Value
						if strings.Contains(f.Name, "时间") {
							if t, ok := officialTime(f.Value); ok {
								value = t.In(time.Local).Format("2006-01-02 15:04:05 -07:00")
							}
						}
						homeText(c, value, homeBody).FontSize(11).TextColor(homeInk).Tooltip(f.Value)
					})
					readingAction(c, "复制"+f.Name, readingCopy, func() { c.WriteClipboard(f.Value); a.copyFeedback(f.Name) })
				})
			}
		})
		ui.Row(c).Shrink(0).MinHeight(60).Padding(12, 20).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
			projectLink(c, "复制诊断信息", func() { c.WriteClipboard(readingDiagnostic(fields)); a.copyFeedback("诊断信息") })
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
		})
	}).Modal().Role(ui.RoleDialog).Width(450).Height(height).Padding(0).Transition(projectOverlayMotion())
}
func readingDiagnostic(fields []ReadingField) string {
	var b strings.Builder
	for _, f := range fields {
		if f.Value != "" {
			fmt.Fprintf(&b, "%s: %s\n", f.Name, f.Value)
		}
	}
	return b.String()
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
		ui.Text(c, fmt.Sprintf("官方已载入 %d/%d 条问答；工具记录独立列示", len(i.Detail.QAs), i.Detail.Count)).FontSize(10).TextColor(muted)
		for _, q := range i.Detail.QAs {
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
		for _, trace := range i.Detail.Traces {
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
	if !n.Loaded {
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
