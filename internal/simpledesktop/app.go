package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/internal/bootstrap"
)

var ink = ui.Hex("#29343e")
var muted = ui.Hex("#8798aa")
var border = ui.Hex("#e8ecf0")
var blue = ui.Hex("#e7eff9")

type pane struct {
	Items                          []Item
	Selected                       *Item
	Offset                         int
	More, Loaded, Loading, Reading bool
	Error                          string
	Query                          string
	ListScroll                     ui.ScrollState
	Knowledge                      knowledgeFeedState
	RawCatalog                     []Item
	WidgetList                     ui.ListState
	ListSelection                  int
	DetailScroll                   map[string]*ui.ScrollState
	Tools                          map[string]bool
	History                        []Item
	HomeAnchor                     *Item
	HomeOffset                     int
	HomeCategory                   string
	Locating                       bool
	RevealItem                     string
	SourcePage                     *SessionHistoryPage
	SourceReturn                   *readingLocation
	SessionTools                   bool
	SessionLoadMaxY                float32
	request                        uint64
	readRequest                    uint64
	readCancel                     context.CancelFunc
	cancel                         context.CancelFunc
	titleCancel                    context.CancelFunc
	titleCtx                       context.Context
	titleRequest                   uint64
	Technical                      map[string]bool
}
type App struct {
	searchController
	graphController
	readingController
	overviewController

	userProfiles         userProfilesState
	environment          environmentSetupState
	initialSetup         bool
	integrations         integrationState
	layout               appLayoutState
	projectUI            projectReadingState
	settingsOrigin       *readingLocation
	access               projectAccessState
	commonEdit           commonEditorState
	commonPage           commonKnowledgePageState
	commonGeneration     uint64
	editProject          *Project
	editIndex            int
	settingsMessage      string
	ChooseProjectFolder  func() (string, error)
	ChooseDiagnosticPath func() (string, error)
	OpenExistingAgent    func(string) error
	projectCreate        projectCreateState
	Config               Config
	Client               Client
	Update               func(func())
	page                 string
	project              int
	kind                 string
	epoch                uint64
	service, completion  string
	monitor              serviceMonitorState
	configError          string
	md                   markdown.Options
	projectMD            *markdown.Options
}

func New(c Config, err error) *App {
	a := &App{Config: c, Client: Client{Config: c}, page: "总览", kind: "知识", service: "尚未检查", completion: "尚未检查"}
	if c.GenerationSetupOnly {
		a.page = "设置"
		a.access.SettingsSection = "Agent 与模型"
	}
	a.initReading(a.Client)
	if err != nil {
		a.configError = err.Error()
		a.initialSetup = c.ConfigFile == "" || (c.FirstInstallDefault && errors.Is(err, os.ErrNotExist))
	}
	if err == nil && c.FirstInstall != nil {
		complete, stateError := bootstrap.FirstInitializationComplete(c.ConfigFile)
		a.initialSetup = !complete
		if stateError != nil {
			a.environment.Error = bootstrap.RedactInstallerText(stateError.Error())
		}
		if a.initialSetup {
			step, stepError := bootstrap.FirstWizardStep(c.ConfigFile)
			a.environment.InitialStep = step
			if stepError != nil {
				a.environment.Error = bootstrap.RedactInstallerText(stepError.Error())
			}
		}
	}
	a.initSearch()
	a.initGraph(c.GraphAssets)
	a.md = markdown.Options{Theme: &markdown.Theme{FontSize: 12, LineHeight: 1.9, Text: ink, Muted: muted, CodeSize: 11.0 / 12, CodeBlockBackground: ui.Hex("#f5f8fb"), CodeBlockBorder: border}, CodeBlock: func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Background(ui.Hex("#f5f8fb")).Border(1, border).Radius(6).Children(func() {
			row := ui.Row(c).Padding(6, 12).Gap(4).AlignItems(ui.Center)
			row.Children(func() {
				ui.Text(c, strings.ToUpper(b.Language)).FontSize(10).TextColor(muted)
				hoverCopyAction(c, row, "复制代码", func() { c.WriteClipboard(b.Code()) })
			})
			b.Body(c).Label("代码横向滚动")
		})
	}}
	return a
}
func (a *App) Start() {
	if a.initialSetup {
		if a.Config.FirstInstall != nil && !a.environment.RuntimeStarted {
			a.beginFirstRuntime(a.environment.InitialStep)
			return
		}
		if a.environment.InitialStep >= 2 {
			a.RefreshIntegrations()
			return
		}
		a.refreshEnvironment()
		return
	}
	if a.Config.FirstInstall != nil && !a.environment.RuntimeStarted {
		a.beginFirstRuntime(0)
		return
	}
	if a.Config.GenerationSetupOnly {
		a.RefreshIntegrations()
		return
	}
	a.monitor.PollEnabled = true
	a.health()
	a.loadRecent()
	a.restoreCodeState()
	a.RefreshProjectDirectory()
	a.RefreshIntegrations()
}
func (a *App) health() {
	a.refreshServiceMonitor()
}
func (a *App) loadRecent()      { a.loadOverview() }
func (a *App) routeKey() string { return a.currentRoute().Key() }
func (a *App) state() *pane {
	return a.paneFor(a.routeKey())
}
func (a *App) projects() []Project {
	if a.page == "通用知识" {
		return a.Config.Common
	}
	return a.Config.Projects
}
func (a *App) current() (Project, bool) {
	ps := a.projects()
	if a.project >= 0 && a.project < len(ps) {
		return ps[a.project], true
	}
	return Project{}, false
}
func (a *App) navigate(page string, index int) { a.navigateKind(page, index, "") }
func (a *App) navigateKind(page string, index int, kind string) {
	a.cancelOverviewRecall()
	a.commonPage.MenuOpen = false
	a.projectUI.InfoOpen = false
	a.projectUI.MenuOpen = false
	a.closeUnifiedSearch()
	a.clearSearchOrigin()
	if page != "设置" {
		a.settingsOrigin = nil
	}
	if a.page == "总览" && page != "总览" {
		a.leaveOverview(a.panes)
	}
	if page == "总览" {
		a.enterOverview()
	}
	a.cancelRecentTitles()
	a.epoch++
	a.graph.clear()
	for _, p := range a.panes {
		p.Knowledge.RecallFilter = nil
		p.HomeAnchor, p.Locating, p.RevealItem = nil, false, ""
		a.cancelTitles(p)
		if p.readCancel != nil {
			p.readCancel()
		}
		if p.cancel != nil {
			p.cancel()
			p.Loading = false
			p.Reading = false
		}
	}
	a.page = page
	if page == "用户画像" {
		a.refreshUserProfiles()
	}
	if page == "设置" {
		a.RefreshIntegrations()
	}
	a.project = index
	if page == "总览" {
		a.loadOverview()
	}
	if page == "知识" {
		a.kind = "知识"
		if kind != "" {
			a.kind = kind
		}
	}
	if page == "通用知识" {
		a.kind = "知识"
	}
	if page != "总览" && page != "设置" && page != "图谱" && page != "检索结果" && page != "用户画像" {
		s := a.state()
		if page == "知识" && a.kind == "知识" {
			a.loadKnowledgeFeed(s)
		} else {
			a.load(s)
		}
	}
}
func (a *App) load(s *pane) {
	a.syncReadingIdentity()
	if a.page == "知识" && a.kind == "知识" && s.Knowledge.Initialized && s.HomeAnchor == nil && s.Query == "" {
		a.loadKnowledgeFeed(s)
		return
	}
	if s.HomeAnchor != nil && len(s.History) == 0 {
		if a.page == "通用知识" && s.HomeCategory != a.commonCategoryFilter() {
			s.HomeAnchor = nil
		}
	}
	if s.HomeAnchor != nil && len(s.History) == 0 {
		if s.Offset == s.HomeOffset && (s.Selected == nil || s.Selected.key() == s.HomeAnchor.key()) {
			a.locateOverviewItem(s, *s.HomeAnchor)
			return
		}
		s.HomeAnchor = nil
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch := s.request, a.epoch
	refreshing := s.Loaded
	s.Loading = true
	s.Error = ""
	p, ok := a.current()
	page, kind, offset, q := a.page, a.kind, s.Offset, s.Query
	rawFeed := page == "知识" && (kind == "原始记录" || kind == "会话" || kind == "文档") && q == ""
	rawCount := max(30, len(s.Items))
	category := a.commonCategoryFilter()
	filter := a.searchType
	if page != "搜索" {
		if kind == "知识" {
			filter = "知识"
		} else {
			filter = "原始材料"
		}
	}
	ps := a.projects()
	if page == "搜索" {
		ps = searchProjects(a.Config.Projects, a.searchProject)
	}
	client := a.Client
	recallFilter := map[string]bool{}
	for id, yes := range s.Knowledge.RecallFilter {
		recallFilter[id] = yes
	}
	go func() {
		defer cancel()
		var rows []Item
		var rawCatalog []Item
		var more bool
		var err error
		if page == "搜索" || q != "" {
			if q == "" {
				rows = []Item{}
			} else {
				if page != "搜索" {
					ps = []Project{p}
				}
				rows, err = client.search(ctx, ps, q, filter)
				if page == "通用知识" {
					filtered := []Item{}
					for _, row := range rows {
						if row.CommonCategory != "" && (category == "" || row.CommonCategory == category) {
							filtered = append(filtered, row)
						}
					}
					rows = filtered
				}
			}
		} else if !ok {
			rows = []Item{}
		} else if p.Dataset == "" {
			err = fmt.Errorf("项目正在登记，内容归属尚未确认")
		} else if page == "通用知识" {
			if len(recallFilter) == 0 {
				rows, more, err = client.commonDocuments(ctx, p, offset, category)
			} else {
				var all []Item
				all, err = client.commonDocumentItems(ctx, p, category)
				for _, row := range all {
					if recallFilter[row.ID] {
						rows = append(rows, row)
					}
				}
				start := min(offset, len(rows))
				end := min(start+30, len(rows))
				more = end < len(rows)
				rows = rows[start:end]
			}
		} else if page == "知识" && (kind == "原始记录" || kind == "会话" || kind == "文档") {
			rawCatalog, err = client.projectRecordCatalog(ctx, p, kind)
			end := min(rawCount, len(rawCatalog))
			rows = append([]Item{}, rawCatalog[:end]...)
			more = end < len(rawCatalog)
		} else if kind == "会话" {
			rows, more, err = client.sessions(ctx, p, offset)
		} else {
			rows, more, err = client.documents(ctx, p, offset, kind == "文档")
		}
		a.Update(func() {
			if a.epoch != epoch || s.request != request {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = err.Error()
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
				}
				return
			}
			if a.searchPane == s && a.searchAnchor != nil && !containsReadingItem(rows, a.searchAnchor.key()) {
				rows = append([]Item{*a.searchAnchor}, rows...)
			}
			if s.SourceReturn != nil && sourceSession(s) && !containsReadingItem(rows, s.Selected.key()) {
				rows = append([]Item{*s.Selected}, rows...)
			}
			if refreshing {
				preserveResolvedReadingTitles(rows, s.Items)
			}
			s.Items = rows
			if rawFeed {
				s.RawCatalog = rawCatalog
				s.Offset = 0
			}
			s.More = more
			s.Loaded = true
			a.startTitles(s)
			if len(s.History) > 0 {
				return
			}
			if s.Selected != nil {
				for _, i := range rows {
					if i.key() == s.Selected.key() {
						if refreshing {
							a.selectItem(s, i)
						}
						return
					}
				}
				if page != "搜索" && q == "" {
					s.Selected = nil
					s.Error = "选中内容已不在当前官方页面，请重新选择"
				}
			}
		})
	}()
}
func (a *App) selectItem(s *pane, i Item) {
	if a.page == "知识" && a.kind == "知识" {
		s.Knowledge.Detail = true
	}
	if s.Locating {
		if s.cancel != nil {
			s.cancel()
		}
		s.request++
		s.Loading, s.Locating = false, false
	}
	if s.HomeAnchor != nil && s.HomeAnchor.key() != i.key() {
		s.HomeAnchor = nil
	}
	s.SourcePage, s.SessionTools, s.SessionLoadMaxY = nil, false, 0
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch := s.readRequest, a.epoch
	generation := a.readingGeneration(s)
	s.Reading = true
	s.History = nil
	s.SourceReturn = nil
	a.resetOverviewFloor(s)
	s.Error = ""
	if s.Selected == nil || s.Selected.key() != i.key() || keyForReading("", *s.Selected, generation) != keyForReading("", i, generation) {
		s.Selected = &i
	}
	client, titles := a.Client, a.titles
	control, hasControl := a.accessClient()
	revision := a.readingRevisions[i.key()]
	go func() {
		defer cancel()
		var result Item
		var sessionPage *SessionHistoryPage
		var err error
		if i.Document != nil {
			result, err = titles.read(ctx, client, i, generation, true, false)
		} else if i.Session != nil {
			result, sessionPage, err = client.readSessionHistory(ctx, i)
		} else {
			result, err = client.read(ctx, i)
		}
		if err == nil && result.Kind == "知识" && hasControl {
			statsCtx, statsCancel := context.WithTimeout(ctx, 2*time.Second)
			result.RecallStats, _ = control.knowledgeRecall(statsCtx, result)
			statsCancel()
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.readRequest || revision != a.readingRevisions[i.key()] {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = err.Error()
				a.handleReadingFailure(i, err)
				return
			}
			s.Selected = &result
			s.SourcePage = sessionPage
			a.publishOverviewTitle(result)
			for idx, row := range s.Items {
				if row.key() == result.key() {
					s.Items[idx].Title = result.Title
				}
			}
		})
	}()
}
func (a *App) source(s *pane) {
	if s.Selected == nil || s.Selected.Source == "" && s.Selected.SourceDocument == "" {
		return
	}
	original := *s.Selected
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch := s.readRequest, a.epoch
	s.Reading = true
	client := a.Client
	titles, generation := a.titles, a.readingGeneration(s)
	go func() {
		defer cancel()
		var i Item
		var sessionPage *SessionHistoryPage
		var err error
		if original.SourceDocument != "" {
			i, err = client.documentInfoByID(ctx, original.Dataset, original.SourceDocument)
			if err == nil {
				i, err = titles.read(ctx, client, i, generation, true, false)
			}
		} else {
			i, sessionPage, err = client.sourceWithHistory(ctx, original)
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.readRequest {
				return
			}
			if !a.syncReadingIdentity() {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = err.Error()
				if i.ID != "" {
					a.handleReadingFailure(i, err)
				} else if status, ok := httpStatusCode(err); ok && status == 401 {
					a.InvalidateReading("")
				}
				return
			}
			s.History = append(s.History, original)
			if i.Session != nil && a.page == "知识" && a.kind == "知识" && a.state() == s {
				s.History = s.History[:len(s.History)-1]
				a.openSourceRecord(s, original, i, sessionPage)
				return
			}
			s.Selected = &i
			if a.page == "知识" && a.kind == "知识" {
				s.Knowledge.Detail = true
			}
			s.SourcePage, s.SessionTools = sessionPage, false
		})
	}()
}
func button(c *ui.Context, label string, fn func()) {
	ui.Button(c, label).FontSize(12).Padding(8, 12).Background(ui.Transparent).BorderWidth(0).Justify(ui.Start).OnClick(fn)
}
func (a *App) View(c *ui.Context) {
	if a.initialSetup {
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.initialEnvironmentPage(c) })
		return
	}
	if a.Config.GenerationSetupOnly {
		ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() { a.generationSetupPage(c) })
		return
	}
	ui.Themed(c, homeTheme).Fill().FontWeight(400).Children(func() {
		a.viewContent(c)
		a.knowledgeDialog(c)
		a.searchDialog(c)
		a.projectCreateDialog(c)
		a.allProjectsDialog(c)
		a.toolGraphDialog(c)
		if a.page == "用户画像" {
			a.userProfileImportDialog(c)
		}
	})
}
func (a *App) viewContent(c *ui.Context) {
	c.After(2 * time.Second)
	a.syncReadingIdentity()
	if time.Now().After(a.access.Next) {
		a.RefreshProjectDirectory()
	}
	if a.page == "总览" && a.Update != nil {
		if a.Config.ProjectControl != nil && !a.access.Loaded {
			if a.access.DirectoryError != "" {
				a.overviewState.Loading, a.overviewState.ActivityLoading = false, false
				a.overviewState.Error = a.access.DirectoryError
			}
		} else if a.overviewState.scope != a.overviewScope() {
			a.loadOverview()
		}
	}
	a.refreshOverviewIfDue(time.Now())
	a.mainShell(c)
}
func (a *App) mainShell(c *ui.Context) {
	ui.Row(c.Key("main-shell")).Fill().AlignItems(ui.Stretch).Background(shellBackground).Children(func() {
		a.navigation(c)
		ui.Column(c).Grow(1).MinWidth(0).Padding(0, 10, 10, 0).Children(func() {
			a.projectHeader(c)
			ui.Column(c.Key("workspace")).Grow(1).MinHeight(0).FillWidth().Background(ui.Hex("#ffffff")).Border(1, shellBorder).Radius(14).Clip().Children(func() {
				if a.configError != "" {
					ui.Text(c, "运行配置不可用："+a.configError).Margin(24).TextColor(ink)
					return
				}
				// The graph owns its navigation row; reading tabs live in the list pane.
				if a.page == "图谱" {
					a.projectNavigation(c)
				}
				switch a.page {
				case "用户画像":
					a.userProfilesPage(c)
				case "总览":
					a.home(c)
				case "设置":
					a.settings(c)
				case "图谱":
					a.graphView(c)
				case "通用知识":
					a.commonKnowledgePage(c)
				case "知识", "检索结果":
					a.projectReading(c)
				default:
					a.reading(c)
				}
			})
		})
	})
}
func (a *App) navigation(c *ui.Context) { a.overviewNavigation(c) }
func (a *App) home(c *ui.Context)       { a.overviewHome(c) }
func (a *App) settings(c *ui.Context) {
	if a.settingsOrigin != nil && a.access.OpenProject != "" {
		a.projectSettingsPage(c)
		return
	}
	a.globalSettingsPage(c)
}
func (a *App) reading(c *ui.Context) {
	s := a.state()
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(316).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(border).Children(func() {
			ui.Column(c).Padding(24, 18, 16, 18).Gap(12).Children(func() {
				label := "项目内容"
				if a.page == "通用知识" {
					label = "通用记忆"
				}
				ui.Text(c, label).FontSize(10).TextColor(muted)
				if a.page != "搜索" {
					ui.Row(c).Gap(3).Children(func() {
						if a.page != "通用知识" {
							button(c, "图谱", func() { a.navigate("图谱", a.project); a.loadGraph() })
						}
						for _, k := range []string{"知识", "会话", "文档"} {
							if a.page == "通用知识" && k != "知识" {
								continue
							}
							kind := k
							label := kind
							if k == "会话" {
								label = "原始记录"
							}
							button(c, memoryKindLabel(label), func() {
								a.cancelTitles(s)
								a.epoch++
								if s.cancel != nil {
									s.cancel()
								}
								a.kind = kind
								next := a.state()
								if !next.Loaded {
									a.load(next)
								}
							})
						}
					})
				} else {
					catalog := append([]Project(nil), a.Config.Projects...)
					options := []string{"全部项目"}
					options = append(options, projectLabels(catalog)...)
					selected := options[0]
					for index, p := range a.Config.Projects {
						if p.ID == a.searchProject {
							selected = options[index+1]
							break
						}
					}
					ui.Select(c, &selected, options).Width(270).OnChange(func() {
						if selected == options[0] {
							a.searchProject = ""
						}
						for index, label := range options[1:] {
							if label == selected {
								a.searchProject = catalog[index].ID
								break
							}
						}
						s.Offset = 0
						a.load(s)
					})
					ui.Select(c, &a.searchType, []string{"全部类型", "知识", "原始材料"}).Width(270).OnChange(func() { s.Offset = 0; a.load(s) })
					ui.Text(c, "最多30个官方检索片段；类型按返回元数据筛选").FontSize(10).TextColor(muted)
				}
				if a.page == "通用知识" {
					a.commonToolbar(c)
					options := []string{}
					for _, p := range a.Config.Common {
						options = append(options, p.Name)
					}
					if len(options) > 0 {
						selected := options[min(a.project, len(options)-1)]
						ui.Select(c, &selected, options).Width(270).OnChange(func() {
							for idx, name := range options {
								if name == selected {
									a.navigate("通用知识", idx)
								}
							}
						})
					}
				}

				ui.Row(c).Children(func() {
					ui.Text(c, fmt.Sprintf("已载入 %d 项", len(s.Items))).FontSize(10).TextColor(muted)
					ui.Spacer(c)
					refreshAction(c, "刷新列表", func() { a.load(s) })
				})
				if s.Loading {
					ui.Text(c, "正在读取…").FontSize(10).TextColor(muted)
				}
				if s.Error != "" {
					ui.Text(c, s.Error).FontSize(11).TextColor(muted)
				}
			})
			ui.Scroll(c.Key("list-"+a.routeKey())).Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(0, 10).Gap(4).Children(func() {
				if s.Loaded && len(s.Items) == 0 {
					label := "当前页面没有内容"
					if a.page == "搜索" && s.Query == "" {
						label = "输入关键词搜索官方内容"
					}
					if a.page == "通用知识" && len(a.Config.Common) == 0 {
						label = "官方当前暂不支持个人偏好与通用经验的独立阅读。后续可使用独立的通用记忆数据组，供所有 Agent 读取。"
					}
					if s.Query != "" {
						label = "没有匹配结果"
					}
					if s.Loading {
						label = "正在读取…"
					} else if s.Error != "" {
						label = "内容读取失败，请刷新"
					}
					ui.Text(c, label).Margin(12).FontSize(12).TextColor(muted)
				}
				for _, row := range s.Items {
					i := row
					b := ui.Button(c, "").Key("item-"+i.key()).Justify(ui.Start).Padding(14, 12).Background(ui.Transparent).BorderWidth(0).Radius(6).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
							ui.Text(c, i.Title).MinWidth(0).FontSize(13).Bold().SingleLine().TextColor(ink)
							ui.Text(c, memoryKindLabel(i.Kind)+" · "+displayDate(i.Date)).MinWidth(0).FontSize(10).TextColor(muted).SingleLine()
							if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
								button(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
							}
						})
					}).OnClick(func() { a.selectItem(s, i) })
					if s.Selected != nil && s.Selected.key() == i.key() {
						b.Background(ui.Hex("#edf3fa"))
					}
				}
			})
			ui.Row(c).Height(48).Shrink(0).Padding(0, 12).BorderWidth(1, 0, 0, 0).BorderColor(border).Children(func() {
				ui.Button(c, "‹").Disabled(s.Offset == 0 || s.Loading || s.Query != "").OnClick(func() { s.Offset = max(0, s.Offset-30); a.load(s) })
				ui.Text(c, fmt.Sprintf("%d", s.Offset/30+1)).FontSize(11)
				ui.Button(c, "›").Disabled(!s.More || s.Loading || s.Query != "").OnClick(func() { s.Offset += 30; a.load(s) })
			})
		})
		if a.page == "通用知识" && a.commonEdit.Open {
			a.commonEditor(c)
		} else {
			a.detail(c, s)
		}
	})
}
func (a *App) detail(c *ui.Context, s *pane) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		if s.Selected == nil {
			ui.Text(c, "选择一项内容开始阅读").FontSize(14).TextColor(muted).Margin(30)
			return
		}
		i := s.Selected
		if len(s.History) > 0 {
			ui.Row(c).Height(42).Shrink(0).Padding(0, 22).Children(func() {
				button(c, "‹ 返回阅读", func() { a.returnFromSource(s) })
			})
		}
		scroll := s.DetailScroll[i.key()]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[i.key()] = scroll
		}
		ui.Scroll(c.Key("detail-"+i.key())).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(28, 30).Gap(18).Children(func() {
			ui.Text(c, memoryKindLabel(i.Kind)+" / 只读").FontSize(10).TextColor(muted)
			presentation := itemPresentation(*i)
			ui.Text(c, presentation.DisplayTitle).FontSize(21).Bold().TextColor(ink)
			ui.Text(c, displayDate(i.Date)).FontSize(10).TextColor(muted)
			a.commonActions(c, *i)
			if s.Reading {
				ui.Text(c, "正在读取…").FontSize(10).TextColor(muted)
			}
			if i.Detail != nil {
				ui.Text(c, fmt.Sprintf("官方已载入 %d/%d 条问答；工具记录独立列示", len(i.Detail.QAs), i.Detail.Count)).FontSize(10).TextColor(muted)
				for _, q := range i.Detail.QAs {
					ui.Column(c).Padding(11, 14).Background(blue).Border(1, border).Radius(6).Gap(6).Children(func() {
						ui.Text(c, "用户").FontSize(10).TextColor(muted)
						markdown.View(c.Key("q-"+q.ID), q.Question, &a.md)
						if q.QuestionTruncated {
							ui.Text(c, "官方内容已截断").FontSize(10).TextColor(muted)
						}
					})
					sourceAgentLabel(c, *i)
					markdown.View(c.Key("a-"+q.ID), q.Answer, &a.md)
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
						button(c, memoryKindLabel(label), func() { s.Tools[key] = !s.Tools[key] })
						if s.Tools[key] {
							markdown.View(c.Key("tool-"+key), "```json\n"+string(t.Params)+"\n```\n\n```json\n"+string(t.Result)+"\n```\n\n"+t.Error, &a.md)
						}
					})
				}
			} else {
				markdown.View(c.Key("body-"+i.key()), presentation.VisibleBody, &a.md)
				if presentation.Unrecognized {
					ui.Text(c, "原文格式未识别，保留原文").FontSize(10).TextColor(muted)
				}
			}
			if i.Source != "" || i.SourceDocument != "" {
				ui.Column(c).Padding(16).Border(1, border).Radius(8).Gap(8).Children(func() {
					ui.Text(c, "出处 · 官方关联材料").FontSize(12).Bold()
					ui.Text(c, readingSourceLabel(*i)).FontSize(10).TextColor(muted)
					button(c, "查看原始记录 →", func() { a.source(s) })
				})
			} else if i.Kind == "知识" && (i.Document == nil || commonCategory(*i.Document) == "") {
				ui.Text(c, "官方未提供可定位的会话出处").FontSize(10).TextColor(muted)
			}
			if s.Technical == nil {
				s.Technical = map[string]bool{}
			}
			label := "› 技术详情"
			if s.Technical[i.key()] {
				label = "⌄ 技术详情"
			}
			button(c, memoryKindLabel(label), func() { s.Technical[i.key()] = !s.Technical[i.key()] })
			if s.Technical[i.key()] {
				fields := presentation.TechnicalFields
				if len(fields) == 0 {
					fields = []ReadingField{{"数据组 ID", i.Dataset}, {"对象 ID", i.ID}}
				}
				for _, field := range fields {
					f := field
					if f.Value != "" {
						copyableText(c, f.Name+"："+f.Value, f.Value, "复制"+f.Name, homeMeta, func() { c.WriteClipboard(f.Value) })
					}
				}
				if i.BodyLoaded || i.Detail != nil || i.Preview != "" {
					copyableText(c, "原文", i.Body, "复制原文", homeMeta, func() { c.WriteClipboard(i.Body) })
				}
			}
		})
		ui.Row(c).Height(48).Shrink(0).Padding(0, 24).BorderWidth(1, 0, 0, 0).BorderColor(border).Children(func() {
			row := ui.Row(c).AlignItems(ui.Center).Gap(4)
			row.Children(func() {
				ui.Text(c, "只读 · 内容由 Cognee 提供").FontSize(10).TextColor(muted)
				if i.BodyLoaded || i.Detail != nil || i.Preview != "" {
					label := "复制已载入内容"
					if i.Kind == "知识" {
						label = "复制记忆内容"
					}
					hoverCopyAction(c, row, label, func() { c.WriteClipboard(itemPresentation(*i).VisibleBody) })
				}
			})
		})
	})
}

func displayDate(date string) string {
	if day, ok := sourceCalendarDate(date); ok {
		return day
	}
	if date == "" {
		return "时间未知"
	}
	if t, ok := officialTime(date); ok {
		return t.In(time.Local).Format("2006-01-02 15:04")
	}
	return "时间未知"
}

func (a *App) OpenSearch() {
	if !a.projectCreate.Open && !a.toolGraph.Open {
		a.openUnifiedSearch(false)
	}
}
func (a *App) projectEditor(c *ui.Context) {
	if a.settingsMessage != "" {
		ui.Text(c, a.settingsMessage).FontSize(11).TextColor(muted)
	}
	p := a.editProject
	if p == nil {
		return
	}
	ui.Column(c).Padding(18).Border(1, border).Radius(8).Gap(10).Children(func() {
		ui.Text(c, "关联已有官方数据组").FontSize(14).Bold()
		for _, field := range []struct {
			Label string
			Value *string
		}{{"项目标识", &p.ID}, {"显示名称", &p.Name}, {"仓库路径", &p.Path}, {"记忆数据组 ID", &p.Dataset}, {"代码数据组 ID（可选）", &p.CodeDataset}} {
			ui.Text(c, field.Label).FontSize(10).TextColor(muted)
			ui.TextInput(c.Key("project-field-"+field.Label), field.Value).Height(32).Label(field.Label)
		}
		ui.Row(c).Gap(8).Children(func() {
			button(c, "保存关联", func() {
				if p.ID == "" || p.Name == "" || p.Dataset == "" {
					a.settingsMessage = "请填写项目标识、名称与已有官方数据组 ID"
					return
				}
				for idx, existing := range a.Config.Projects {
					if idx != a.editIndex && existing.ID == p.ID {
						a.settingsMessage = "项目标识已存在"
						return
					}
				}
				before := append([]Project(nil), a.Config.Projects...)
				if a.editIndex < 0 {
					a.Config.Projects = append(a.Config.Projects, *p)
				} else {
					a.Config.Projects[a.editIndex] = *p
				}
				if e := a.saveProjects(); e != nil {
					a.Config.Projects = before
					a.settingsMessage = e.Error()
					return
				}
				a.epoch++
				a.InvalidateReading("")
				a.cancelAndResetPanes()
				a.editProject = nil
				a.settingsMessage = "关联已保存；服务与模型配置通过重启后台生效"
				a.loadRecent()
			})
			button(c, "取消", func() { a.editProject = nil })
		})
	})
}

func (a *App) returnFromSource(s *pane) {
	if s.SourceReturn != nil {
		location := s.SourceReturn
		s.SourceReturn, s.History = nil, nil
		a.restoreReadingLocation(location)
		return
	}
	if len(s.History) == 0 {
		return
	}
	last := len(s.History) - 1
	original := s.History[last]
	history := append([]Item(nil), s.History[:last]...)
	floor := a.overviewSourceFloor[s]
	a.selectItem(s, original)
	s.History = history
	a.clampOverviewFloor(s, floor, len(history))
}
