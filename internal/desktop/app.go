// Package desktop owns the native UI; the service remains the source of truth.
package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type App struct {
	cognee                                                           CogneeUI
	officialPreparation                                              OfficialPreparationUI
	firstLaunch                                                      FirstLaunchUI
	browsing                                                         BrowsingUI
	reader                                                           SourceReaderUI
	restoreBackup                                                    func(context.Context, string, string) (records.RestoreReceipt, error)
	reloadAfterBackup                                                func()
	backups                                                          BackupUI
	indexRebuild                                                     records.IndexRebuild
	indexRebuildScope, indexRebuildError, indexRebuildOperation      string
	indexRebuildConfirm, indexRebuildBusy, indexRebuildLoading       bool
	indexRebuildSequence                                             int64
	code                                                             CodeUI
	initialization                                                   InitializationUI
	guidanceRoot, guidanceError                                      string
	guidanceHidden                                                   bool
	listSort, usageNavigation, usageBrowseError, usageImportantError string
	usageBrowsed                                                     map[string]bool
	importanceBusy                                                   bool
	organization                                                     OrganizationUI
	schedules                                                        ScheduleUI
	experience                                                       ExperienceUI
	organizationMode                                                 bool
	organizationSources                                              []records.OrganizationSource
	organizationSourcesFor                                           string
	organizationSourcesRevision                                      int64
	organizationSourcesError                                         string
	relatedFor, relatedError                                         string
	relatedMerges                                                    []records.MergeSummary
	merges                                                           MergeUI
	agents                                                           AgentUI
	processing                                                       ProcessingUI
	activityLoaded                                                   bool
	overviewRaw, overviewKnowledge                                   *int
	overviewUpdated                                                  time.Time
	homeDate                                                         string
	homeRecentLoaded                                                 bool
	homeActiveTask                                                   homeTask
	pendingMutations                                                 map[string]records.Mutation
	saving                                                           bool
	restoreExpected                                                  int64
	conflictCurrent                                                  *MemoryItem
	retryingIndex                                                    bool
	searchSequence                                                   int64
	searching                                                        bool
	searchQuery, searchError                                         string

	serviceState                           string
	routeStates                            map[string]PaneState
	memoryRoute                            string
	library                                serviceipc.Library
	libraryContext                         context.Context
	dispatch                               func(func())
	scopes                                 []records.Scope
	selectedItem                           *MemoryItem
	recent                                 []MemoryItem
	loadSequence                           int64
	loading                                bool
	libraryError                           string
	nextCursor                             string
	listTotal                              int
	imported                               int
	recordKind                             string
	projectName, projectPath, projectError string
	projectBusy                            bool
	onProject, onMore                      func()

	page            string
	settingsSection string
	scope           string
	selectedID      string
	detailMode      string
	collapsed       bool
	editing         bool
	draft           string
	draftTitle      string
	draftConditions string
	draftScope      string
	drafts          map[string]Draft
	items           []MemoryItem
	projects        []ProjectItem
	history         []MemoryHistory
	activities      map[string]int
	activityDate    string
	historyRevision int64
	listScroll      map[string]*ui.ScrollState
	detailScroll    ui.ScrollState
	textWindows     map[string]*textWindow
	visualFixture   bool
	zoom            float32
	version         string
	updateText      string
	checking        bool
	check           func()
	runtimeStatus   RuntimeStatus
	prepareRuntime  func()
	cancelRuntime   func()
	stopRuntime     func()
	resumeRuntime   func()
	keepService     bool
	keepChanged     func(bool)
	installUpdate   func()
	query           string
	message         string
	onSearch        func()
	onSave          func()
	onRestore       func(int64)
	onSelect        func(string)
	onNavigate      func()
}

func New(version string, check func()) *App {
	return &App{page: "总览", settingsSection: "environment", detailMode: "view", zoom: 1, version: version, check: check, keepService: true,
		processing: ProcessingUI{Mode: "agent", Drafts: map[string]records.ProcessingSpec{}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{}}},
		browsing:   BrowsingUI{View: "memories", Preferences: BrowsePreferences{Selections: map[string]BrowseSelection{}}},
		updateText: "此版本尚未配置更新源", pendingMutations: map[string]records.Mutation{}, routeStates: map[string]PaneState{}, drafts: map[string]Draft{}, listScroll: map[string]*ui.ScrollState{}, activities: map[string]int{}}
}
func (a *App) SetRuntimeStatus(status RuntimeStatus) {
	a.runtimeStatus = status
	a.firstLaunchRuntime(status)
}
func (a *App) ConfigureRuntime(prepare, cancel, stop, resume func(), keep bool, changed func(bool)) {
	a.prepareRuntime, a.cancelRuntime, a.stopRuntime, a.resumeRuntime = prepare, cancel, stop, resume
	a.keepService, a.keepChanged = keep, changed
}
func (a *App) SetInstallUpdate(install func()) { a.installUpdate = install }
func (a *App) SetUpdateStatus(message string, checking bool) {
	a.updateText, a.checking = message, checking
}

func (a *App) View(c *ui.Context) {
	theme := *ui.LightTheme()
	theme.Spacing, theme.FontSize, theme.Radius = 3, design.Body, 5
	theme.Accent, theme.Text, theme.TextMuted, theme.Border = design.Ink, design.Ink, design.Muted, design.Border
	c.SetTheme(&theme)
	c.SetZoom(a.zoom)
	if a.firstLaunch.Open {
		a.firstLaunchView(c)
		return
	}
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.page == "设置" {
			a.settingsSidebar(c)
		} else {
			a.sidebar(c)
		}
		ui.Column(c).Grow(1).MinWidth(0).Background(ui.Hex("#ffffff")).Children(func() {
			if a.reader.Open {
				a.sourceReader(c)
				return
			}
			a.toolbar(c)
			if a.page == "项目" && a.scope != "" && a.detailMode == "view" {
				a.projectExplorer(c)
			} else if a.page == "官方记忆" {
				a.cogneeSetupPage(c)
			} else if a.page == "总览" {
				a.home(c)
			} else if a.page == "设置" {
				a.settingsShell(c)
			} else {
				a.details(c)
			}
		})
	})
}
func (a *App) sidebar(c *ui.Context) {
	width := design.Sidebar
	if a.collapsed {
		width = 112
	}
	background := design.SidebarFill
	if c.Vibrancy() {
		background = background.Alpha(.85)
	}
	ui.Column(c).Key("navigation").Width(width).Shrink(0).Background(background).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
		bar := c.TitleBar()
		ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(0, 20, 0, max(bar.Left+8, 12)).DragWindow().Children(func() {
			if !a.collapsed {
				ui.Spacer(c)
				iconButton(c, "切换侧边栏", icons["sidebar"]).OnClick(func() { a.collapsed = true })
			}
		})
		ui.Scroll(c).Grow(1).Padding(18, 10).Gap(6).Children(func() {
			if a.collapsed {
				ui.Button(c, "展开").Label("切换侧边栏").OnClick(func() { a.collapsed = false })
			}
			for _, page := range []string{"总览", "全部记忆", "全局记忆", "整理记录"} {
				a.nav(c, page, "", icons[page])
			}
			ui.Row(c).Margin(22, 12, 8, 12).Children(func() {
				ui.Text(c, "项目").FontSize(design.Small).TextColor(design.Muted)
				ui.Spacer(c)
				iconButton(c, "添加项目", icons["plus"]).OnClick(func() { a.navigate("项目", ""); a.detailMode = "project" })
			})
			for _, project := range a.projects {
				a.nav(c, project.Name, project.ID, icons["项目"])
			}
			ui.Button(c, "").Label("项目").Padding(8, 12).BorderWidth(0).Background(ui.Transparent).Justify(ui.Start).Gap(10).Children(func() {
				ui.Icon(c, icons["plus"]).FontSize(design.Icon).TextColor(design.Muted)
				ui.Text(c, "添加项目").TextColor(design.Muted)
			}).OnClick(func() { a.navigate("项目", ""); a.detailMode = "project" })
		})
		ui.Column(c).Padding(10).Gap(10).Shrink(0).Children(func() {
			a.nav(c, "设置", "", icons["设置"])
			status := "服务未连接"
			switch a.serviceState {
			case "ready":
				status = "服务运行中"
			case "preparing":
				status = "引擎准备中"
			case "recovering":
				status = "索引恢复中"
			case "failed":
				status = "索引服务异常"
			}
			if a.serviceState == "" && a.runtimeStatus.Phase == "ready" {
				status = "服务运行中"
			} else if a.runtimeStatus.Phase == "stopped" {
				status = "服务已停止"
			}
			if a.libraryError != "" && a.activityLoaded && a.runtimeStatus.Phase != "stopped" {
				status = "服务暂不可用"
			}
			ui.Text(c, "· "+status).Label(status).FontSize(design.Small).TextColor(design.Muted).Padding(0, 12)
		})
	})
}
func (a *App) nav(c *ui.Context, label, scope string, icon *ui.SVG) {
	button := ui.Button(c.Key("nav-"+label+scope), "").Label(label).Height(34).Padding(8, 12).Radius(5).Justify(ui.Start).Gap(10).BorderWidth(0).Background(ui.Transparent).Children(func() {
		if !a.collapsed {
			ui.Icon(c, icon).FontSize(design.Icon).TextColor(design.Muted)
		}
		ui.Text(c, label).SingleLine().MinWidth(0)
	})
	selected := a.page == label && a.scope == scope || scope != "" && a.scope == scope || label == "设置" && a.page == "整理计划"
	if selected {
		button.Background(design.Selected)
	} else if button.Hovered() {
		button.Background(ui.Hex("#dfe9f4"))
	}
	button.OnClick(func() {
		if scope != "" {
			a.navigate("项目", scope)
		} else {
			a.navigate(label, scope)
		}
	})
}
func (a *App) toolbar(c *ui.Context) {
	ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(10, 24).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).DragWindow().Children(func() {
		if a.page == "设置" {
			ui.Text(c, "设置 / "+a.settingsSectionLabel()).FontSize(design.Small).TextColor(design.Muted)
			ui.Spacer(c)
			ui.Text(c, a.processingToolbarStatus()).FontSize(design.Small).TextColor(design.Muted)
			return
		}
		icon := icons[a.page]
		if a.page == "整理计划" {
			icon = icons["整理记录"]
		}
		if icon == nil {
			icon = icons["项目"]
		}
		ui.Icon(c, icon).FontSize(design.Icon).TextColor(design.Muted)
		ui.Text(c, a.pageTitle()).Bold()
		ui.Spacer(c)
		ui.Row(c).Width(244).Height(29).Padding(4, 9).Gap(6).Radius(5).Border(1, design.Border).Background(design.Surface).Children(func() {
			ui.Icon(c, icons["search"]).FontSize(13).TextColor(design.Muted)
			ui.TextInput(c.Key("memory-search"), &a.query).Label("搜索记忆").Placeholder(a.searchPlaceholder()).Grow(1).MinWidth(0).BorderWidth(0).Background(ui.Transparent).OnSubmit(func() {
				if a.onSearch != nil {
					a.onSearch()
				}
			})
		})
		quietButton(c, "搜索").OnClick(func() {
			if a.onSearch != nil {
				a.onSearch()
			}
		})
		actionButton(c, "新增记忆", icons["plus"]).OnClick(a.newMemory)
	})
}
func (a *App) pageTitle() string {
	if a.page == "项目" && a.scope != "" {
		for _, p := range a.projects {
			if p.ID == a.scope {
				return p.Name
			}
		}
	}
	return a.page
}
func (a *App) home(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Key("overview").Grow(1).MinHeight(0).Padding(28, 30, 24, 30).Gap(20).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).Children(func() {
					ui.Text(c, "记忆动态").FontSize(design.Heading).Bold()
					ui.Spacer(c)
					a.homeOrganizationButton(c)
				})
				ui.Text(c, "最近积累的知识，以及需要你确认的改动。").FontSize(design.Small).TextColor(design.Muted)
			})
			a.homeTotals(c)
			if a.libraryError != "" {
				ui.Row(c).Gap(10).Children(func() {
					label := "数据暂不可用"
					if !a.overviewUpdated.IsZero() {
						label = "保留上次数据 · 更新于 " + a.overviewUpdated.Format("15:04")
					}
					ui.Text(c, label).FontSize(design.Small).TextColor(design.Muted)
					quietButton(c, "重试读取").OnClick(func() { a.refresh(false) })
				})
			}
			a.heatmap(c)
			ui.Row(c).AlignItems(ui.Stretch).Gap(28).Children(func() {
				ui.Column(c).Grow(1.85).MinWidth(0).Gap(0).Children(func() {
					ui.Row(c).Children(func() {
						label := "最近新增"
						if a.homeDate != "" {
							label = a.homeDate + " 新增知识"
						}
						ui.Text(c, label).Bold().Margin(0, 0, 12, 0)
						ui.Spacer(c)
						if a.homeDate != "" {
							quietButton(c, "回到最近新增").OnClick(func() { a.selectHomeDate("") })
						}
						quietButton(c, "查看全部").OnClick(func() { a.navigate("全部记忆", "") })
					})
					ui.Divider(c)
					if len(a.recent) == 0 && !a.visualFixture {
						title, description := "还没有知识", "保存一条知识，或在项目中整理已有记录。"
						if a.libraryError != "" {
							title, description = "知识暂不可用", "请重试读取，或选择其他日期。"
						} else if a.loading || (a.homeDate != "" && !a.homeRecentLoaded) {
							title, description = "正在读取知识", "等待所选日期的实际记录。"
						} else if !a.activityLoaded {
							title = "等待本地记忆服务"
							description = "活动和最近新增将在服务连接后显示。"
						} else if a.homeDate != "" {
							title, description = "这一天没有新增知识", "可以选择其他日期，或回到最近新增。"
						} else if a.overviewRaw != nil && *a.overviewRaw > 0 {
							title, description = "已有原始记录，尚未形成知识", "可以查看原始记录，或在项目中整理。"
						} else if a.overviewKnowledge != nil && *a.overviewKnowledge > 0 {
							title, description = "暂无日常新增知识", "已有知识可以从全部记忆查看；导入数量单独统计。"
						}
						a.empty(c, title, description)
						actionButton(c, "记录第一条记忆", icons["plus"]).AlignSelf(ui.Start).OnClick(a.newMemory)
						if a.agents.Context != nil {
							ui.Row(c).Gap(12).Margin(12, 0, 0, 0).Children(func() {
								quietButton(c, "连接 Agent").OnClick(func() { a.openAgentSettings() })
								quietButton(c, "添加本地项目").OnClick(func() { a.navigate("项目", "") })
							})
						}
					}
					recent := a.recent
					if a.visualFixture {
						recent = a.items
					}
					for i, item := range recent {
						if i == 4 {
							break
						}
						a.memoryRow(c, item, false)
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(12).Children(func() { a.mergeHome(c) })
			})
		})
		a.homePlanFooter(c)
	})
}
func (a *App) heatmap(c *ui.Context) {
	today := time.Now()
	start := today.AddDate(-1, 0, 1)
	start = start.AddDate(0, 0, -int(start.Weekday()))
	total := 0
	for _, count := range a.activities {
		total += count
	}
	ui.Column(c).Key("activity-heatmap").Border(1, design.Border).Radius(8).Padding(20).Gap(16).Children(func() {
		ui.Row(c).Children(func() {
			label := fmt.Sprintf("过去一年新增 %d 条知识", total)
			if !a.activityLoaded && !a.visualFixture {
				label = "每日记忆 · 等待本地活动记录"
			}
			ui.Text(c, label)
			ui.Spacer(c)
			ui.Text(c, start.Format("2006.01")+" — "+today.Format("2006.01")).FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Row(c).Gap(6).Children(func() {
			ui.Column(c).Width(26).Gap(4).Padding(22, 0, 0, 0).Children(func() {
				for day := 0; day < 7; day++ {
					label := ""
					if day == 1 {
						label = "一"
					}
					if day == 3 {
						label = "三"
					}
					if day == 5 {
						label = "五"
					}
					ui.Text(c, label).Height(12).FontSize(10).TextColor(design.Muted)
				}
			})
			ui.Row(c).Grow(1).Gap(4).Children(func() {
				for week := 0; week < 53; week++ {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						date := start.AddDate(0, 0, week*7)
						label := ""
						if week == 0 || date.Month() != date.AddDate(0, 0, -7).Month() {
							label = fmt.Sprintf("%d月", date.Month())
						}
						ui.Text(c, label).Height(18).FontSize(10).TextColor(design.Muted).NoWrap()
						for day := 0; day < 7; day++ {
							d := date.AddDate(0, 0, day)
							key := d.Format("2006-01-02")
							count := a.activities[key]
							level := 0
							if count > 0 {
								level = min(4, (count+1)/2)
							}
							cell := ui.Button(c.Key("day-"+key), "").Label(fmt.Sprintf("%s 新增 %d 条", key, count)).Height(12).MinWidth(0).Padding(0).BorderWidth(0).Radius(2).Background(design.Heat[level]).Tooltip(fmt.Sprintf("%s · 新增 %d 条", key, count)).Disabled(d.After(today) || (!a.activityLoaded && !a.visualFixture))
							if key == a.homeDate {
								cell.Border(1, design.Muted)
							}
							cell.OnClick(func() {
								if a.homeDate == key {
									a.selectHomeDate("")
								} else {
									a.selectHomeDate(key)
								}
							})
						}
					})
				}
			})
		})
		ui.Row(c).Gap(4).Children(func() {
			label := fmt.Sprintf("今天新增 %d 条", a.activities[today.Format("2006-01-02")])
			if !a.activityLoaded && !a.visualFixture {
				label = "尚未读取活动"
			}
			ui.Text(c, label).FontSize(design.Small).TextColor(design.Muted)
			if a.imported > 0 {
				ui.Text(c, fmt.Sprintf("· 另外导入 %d 条", a.imported)).FontSize(design.Small).TextColor(design.Muted)
			}
			ui.Spacer(c)
			ui.Text(c, "少").FontSize(10).TextColor(design.Muted)
			for _, color := range design.Heat {
				ui.Box(c).Size(8, 8).Radius(1).Background(color)
			}
			ui.Text(c, "多").FontSize(10).TextColor(design.Muted)
		})
	})
}
func (a *App) details(c *ui.Context) {
	width := design.List
	// At the minimum window size the detail remains useful rather than being squeezed by a fixed list.
	windowWidth, _ := c.Size()
	if windowWidth < 1100 {
		width = 320
	}
	ui.Row(c).Key("details-layout").Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Key("item-list").Width(width).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Column(c).Padding(28, 24, 18, 24).Gap(12).Shrink(0).Children(func() {
				if !a.isMemoryPage() {
					ui.Text(c, a.pageTitle()).FontSize(design.Heading).Bold()
				}
				if a.page == "搜索" {
					if a.searching {
						ui.Text(c, "正在搜索当前可检索版本…").FontSize(design.Small).TextColor(design.Muted)
					} else if a.searchError != "" {
						ui.Text(c, a.searchError).FontSize(design.Small).TextColor(design.Muted)
						quietButton(c, "重试搜索").OnClick(a.searchMemory)
					} else {
						ui.Text(c, fmt.Sprintf("“%s” · %d 条当前版本", a.searchQuery, len(a.items))).FontSize(design.Small).TextColor(design.Muted)
					}
				}
				if a.libraryError != "" {
					ui.Text(c, a.libraryError).FontSize(design.Small).TextColor(design.Muted)
					quietButton(c, "重试读取").OnClick(func() { a.refresh(false) })
				}
				if a.page == "代码图谱" {
					actionButton(c.Key("code-new"), "开启工作区", icons["plus"]).Disabled(a.code.Busy).OnClick(a.newCode)
				} else if a.page == "初始化" {
					actionButton(c, "新建初始化批次", nil).Key("initialization-new").Disabled(a.initialization.Busy).OnClick(func() { a.openInitialization("") })
				} else if a.page == "使用指引" {
					ui.Text(c, "步骤状态来自实际组件、登记与连接回执。").FontSize(design.Small).TextColor(design.Muted)
				} else if a.page == "整理计划" {
					ui.Text(c, "按项目设置频率，明确启用后才运行。").FontSize(design.Small).TextColor(design.Muted)
					actionButton(c, "新建整理计划", nil).Key("schedule-new").Disabled(a.schedules.Busy).OnClick(a.newSchedule)
				} else if a.page == "设置" {
					ui.Text(c, "引擎、模型与客户端分别管理。").FontSize(design.Small).TextColor(design.Muted)
				} else if a.page == "Agent" {
					ui.Text(c, "共享当前项目的记忆，保留来源与版本。").FontSize(design.Small).TextColor(design.Muted)
					quietButton(c, "重新发现").Disabled(a.agents.Busy).OnClick(func() { a.refreshAgents(true) })
				} else if a.page == "整理记录" {
					ui.Row(c).Gap(8).Children(func() {
						ui.Button(c, "合并审核").Key("organization-tab-merge").OnClick(func() { a.organizationMode = false })
						ui.Button(c, "摘要与标签").Key("organization-tab-summary").OnClick(func() { a.organizationMode = true; a.refreshOrganization(false, false) })
					})
					actionButton(c, "立即整理", icons["全部记忆"]).Disabled(a.merges.Busy).OnClick(a.beginOrganizationOrMerge)
				} else if a.isMemoryPage() {
					actionButton(c, "立即整理", icons["全部记忆"]).AlignSelf(ui.End).OnClick(a.beginOrganizationOrMerge)
					kind := "知识"
					if a.recordKind == "raw" {
						kind = "原始记录"
					}
					ui.Text(c, fmt.Sprintf("%d 条%s · 保留来源与修改记录", a.listTotal, kind)).FontSize(design.Small).TextColor(design.Muted)
					ui.Row(c).Gap(6).Children(func() {
						ui.Button(c, "知识").Height(28).Padding(5, 10).OnClick(func() { a.recordKind = "knowledge"; a.nextCursor = ""; a.refresh(false) })
						ui.Button(c, "原始记录").Height(28).Padding(5, 10).OnClick(func() { a.recordKind = "raw"; a.nextCursor = ""; a.refresh(false) })
					})
					if a.page != "搜索" {
						ui.Select(c.Key("memory-sort"), &a.listSort, []string{"最近更新", "常用", "重要优先"}).OnChange(func() { a.nextCursor = ""; a.items = nil; a.refresh(false) })
						ui.Text(c, usageSortExplanation(a.listSort)).FontSize(design.Small).TextColor(design.Muted)
					}
					if a.activityDate != "" {
						quietButton(c, a.activityDate+" · 清除日期筛选").OnClick(func() {
							a.activityDate = ""
							if a.onNavigate != nil {
								a.onNavigate()
							}
						})
					}
				}
			})
			key := a.page + ":" + a.scope
			if a.listScroll[key] == nil {
				a.listScroll[key] = &ui.ScrollState{}
			}
			ui.Scroll(c.Key("list-"+key)).Grow(1).TrackScroll(a.listScroll[key]).Padding(0, 15, 18, 15).Children(func() {
				if a.isMemoryPage() {
					if len(a.items) == 0 {
						if a.page == "搜索" {
							message := "没有搜索结果。未完成索引的记忆暂不参与搜索。"
							if a.searching {
								message = "索引查询进行中。"
							}
							if a.searchError != "" {
								message = "搜索未完成，请重试。"
							}
							a.empty(c, "", message)
						} else {
							a.empty(c, "列表为空", "保存的记忆会出现在这里。")
						}
					}
					for _, item := range a.items {
						a.memoryRow(c, item, true)
					}
					if a.nextCursor != "" {
						quietButton(c, "加载更多").Disabled(a.loading || a.onMore == nil).OnClick(func() {
							if a.onMore != nil {
								a.onMore()
							}
						})
					}
				} else if a.page == "整理记录" {
					if a.organizationMode {
						a.organizationList(c)
					} else {
						a.mergeList(c)
					}
				} else if a.page == "代码图谱" {
					a.codeList(c)
				} else if a.page == "Agent" {
					a.agentList(c)
				} else if a.page == "初始化" {
					a.initializationList(c)
				} else if a.page == "整理计划" {
					a.scheduleList(c)
				} else {
					a.empty(c, "", a.pageDescription())
				}
			})
		})
		ui.Column(c).Key("detail-pane").Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Height(55).Shrink(0).Padding(0, 22).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				label := "知识详情"
				raw := a.recordKind == "raw"
				if item := a.current(); item != nil {
					raw = item.Origin == "raw"
				}
				if raw {
					label = "原始记录"
				}
				if a.editing {
					label = "新增记忆"
					if a.detailMode == "edit" {
						label = "编辑记忆"
					}
				} else if a.detailMode == "history" {
					label = "历史记录"
				} else if a.detailMode == "restore" {
					label = "恢复版本"
				} else if !a.isMemoryPage() {
					label = a.pageTitle()
				}
				ui.Text(c, label)
				ui.Spacer(c)
				if a.isMemoryPage() && a.current() != nil && !a.editing && a.detailMode == "view" {
					iconButton(c, "编辑", icons["edit"]).Disabled(a.current().State != "active").OnClick(a.editMemory)
					iconButton(c, "历史记录", icons["history"]).OnClick(func() { a.detailMode = "history" })
				}
			})
			if a.page == "代码图谱" {
				a.codeDetail(c)
			} else if a.page == "使用指引" {
				a.guidanceDetail(c)
			} else if a.page == "初始化" {
				a.initializationDetail(c)
			} else if a.page == "整理计划" {
				a.scheduleDetail(c)
			} else if a.page == "整理记录" {
				if a.organizationMode {
					a.organizationDetail(c)
				} else {
					a.mergeDetail(c)
				}
			} else if a.page == "Agent" {
				a.agentDetail(c)
			} else if a.page == "官方记忆" {
				a.cogneeDetail(c)
			} else if a.page == "项目" && a.scope == "" {
				a.projectEditor(c)
			} else if a.page == "设置" {
				ui.Scroll(c).Grow(1).Padding(24).Gap(18).Children(func() { a.settings(c) })
			} else if a.editing && a.isMemoryPage() {
				a.editor(c)
			} else if a.current() != nil && a.isMemoryPage() {
				a.memoryDetail(c)
			} else {
				ui.Scroll(c).Grow(1).Padding(24).Gap(16).Children(func() { a.empty(c, a.emptyTitle(), a.pageDescription()) })
			}
		})
	})
}
func (a *App) isMemoryPage() bool {
	return a.page == "全部记忆" || a.page == "全局记忆" || a.page == "项目" || a.page == "搜索"
}
func (a *App) current() *MemoryItem {
	if a.selectedItem != nil && a.selectedItem.ID == a.selectedID {
		return a.selectedItem
	}
	for i := range a.items {
		if a.items[i].ID == a.selectedID {
			return &a.items[i]
		}
	}
	return nil
}
func (a *App) memoryRow(c *ui.Context, item MemoryItem, inList bool) {
	b := ui.Button(c.Key("memory-"+item.ID), "").Label(item.Title).Justify(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Background(ui.Transparent).Padding(12, 10).Radius(5).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, item.Title).FontWeight(600).SingleLine().Grow(1).MinWidth(0)
				category := item.Category
				if item.State == "deleted" {
					category = "已删除"
				}
				ui.Text(c, category).FontSize(10).TextColor(design.Muted).Shrink(0)
			})
			rowPreview := preview(item.Text)
			if item.Origin == "raw" && item.SourceDetail != nil && (item.SourceDetail.Official != nil || item.SourceDetail.Native != nil) {
				rowPreview = "原文已保留 · 打开阅读器核验当前来源与授权"
			}
			ui.Text(c, rowPreview).FontSize(design.Small).TextColor(design.Muted).SingleLine()
			ui.Row(c).Children(func() {
				ui.Text(c, item.Source+" · "+item.ScopeName).FontSize(10).TextColor(design.Muted).Grow(1).SingleLine().MinWidth(0)
				ui.Text(c, item.CreatedAt.Format("15:04")).FontSize(10).TextColor(design.Muted)
			})
		})
	})
	if inList && item.ID == a.selectedID {
		b.Background(design.ListSelected).BorderWidth(0)
	} else if b.Hovered() {
		b.Background(design.Surface)
	}
	b.OnClick(func() {
		a.rememberDraft()
		a.selectedID = item.ID
		a.message = ""
		a.conflictCurrent = nil
		a.editing = false
		a.detailMode = "view"
		a.detailScroll.Y = 0
		if a.page == "总览" {
			a.page = "全部记忆"
			a.recordKind, a.activityDate = "knowledge", ""
		}
		if a.onSelect != nil {
			a.onSelect(item.ID)
		}
	})
}
func (a *App) editor(c *ui.Context) {
	ui.Column(c).Grow(1).Padding(24).Gap(16).Children(func() {
		ui.TextInput(c.Key("memory-title"), &a.draftTitle).Disabled(a.saving).Label("记忆标题").Placeholder("给这条记忆一个简短标题").FontSize(design.Title).Height(36)
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "范围").FontSize(design.Small).TextColor(design.Muted)
			ui.Select(c.Key("draft-scope"), &a.draftScope, a.scopeOptions()).Label("记忆范围").Disabled(a.detailMode == "edit" || a.saving)
		})
		ui.TextArea(c.Key("memory-draft"), &a.draft).Disabled(a.saving).Label("记忆内容").Placeholder("记录一条项目经验…").Grow(1).MinHeight(80).LineHeight(1.7)
		if item := a.current(); a.detailMode == "edit" && item != nil && item.Experience != nil {
			ui.TextArea(c.Key("experience-conditions"), &a.draftConditions).Disabled(a.saving).Label("适用条件").Height(80).LineHeight(1.6)
		}
		message := "内容暂存于当前窗口，尚未保存。"
		if a.onSave == nil {
			message = "记忆服务尚未就绪，暂不能保存。"
		}
		if a.message != "" {
			message = a.message
		}
		ui.Text(c, message).FontSize(design.Small).TextColor(design.Muted)
		if a.conflictCurrent != nil {
			ui.Text(c, fmt.Sprintf("最新 v%d：%s", a.conflictCurrent.Revision, a.conflictCurrent.Text)).MaxLines(3).FontSize(design.Small).Selectable()
			quietButton(c, "按最新版本继续编辑").AlignSelf(ui.Start).OnClick(a.continueFromLatest)
		}
		ui.Row(c).Gap(8).Children(func() {
			actionButton(c, "返回", nil).OnClick(func() { a.rememberDraft(); a.editing = false; a.detailMode = "view" })
			actionButton(c, "保存", nil).Disabled(a.saving || a.onSave == nil || strings.TrimSpace(a.draft) == "").OnClick(func() {
				if a.onSave != nil {
					a.onSave()
				}
			})
		})
	})
}
func (a *App) memoryDetail(c *ui.Context) {
	item := a.current()
	ui.Scroll(c).Grow(1).TrackScroll(&a.detailScroll).Padding(24).Gap(22).Children(func() {
		if a.detailMode == "history" || a.detailMode == "restore" {
			a.historyView(c, *item)
			return
		}
		a.recordDisplayedMemory(*item)
		ui.Text(c, item.Title).FontSize(design.Title).Bold()
		if a.page == "项目" && a.scope == item.Dataset {
			entities := a.memoryEntities(*item)
			if len(entities) > 0 {
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, "这条知识涉及的对象").FontSize(design.Small).Bold()
					for start := 0; start < len(entities); start += 2 {
						ui.Row(c).Gap(8).Children(func() {
							for _, entity := range entities[start:min(start+2, len(entities))] {
								name := []rune(entity.Name)
								label := string(name[:min(28, len(name))])
								if len(name) > 28 {
									label += "…"
								}
								quietButton(c, label).Key("browse-memory-entity-"+entity.ID).Tooltip(entity.Name).Padding(5, 8).Background(design.Surface).Grow(1).MinWidth(0).Height(28).OnClick(func() { a.browseEntity(entity.ID) })
							}
						})
					}
				})
			}
			if a.browsing.Error != "" {
				ui.Text(c, a.browsing.Error).FontSize(design.Small).TextColor(design.Muted)
			}
		}
		if item.Origin == "raw" {
			quietButton(c, "阅读完整原始记录").Key("source-reader-open").OnClick(func() { a.openSourceReader(*item, records.MergeTarget{Memory: item.ID, Revision: item.Revision}) })
		}
		if item.Origin == "raw" && item.SourceDetail != nil && (item.SourceDetail.Official != nil || item.SourceDetail.Native != nil) {
			ui.Text(c, "原文副本已持久保留。阅读器会核验当前项目、来源版本和官方授权；来源变化或接入撤销后停止读取。").FontSize(design.Small).LineHeight(1.8)
		} else {
			ui.Text(c, a.readableText(c, "current-"+item.ID+fmt.Sprint(item.Revision), item.Text)).Selectable().LineHeight(1.8)
		}
		if item.Experience != nil {
			ui.Text(c, "适用条件").Bold()
			conditions := item.Experience.Conditions
			if conditions == "" {
				conditions = "原文复制，尚未填写通用适用条件；使用时核对项目明确约定。"
			}
			ui.Text(c, conditions).Selectable().LineHeight(1.7)
			ui.Text(c, "项目明确约定优先于全局建议。").FontSize(design.Small).TextColor(design.Muted)
		}
		a.experienceCopyPreview(c, *item)
		a.usageDetail(c, *item)
		ui.Column(c).Gap(10).Children(func() {
			meta(c, "范围", item.ScopeName)
			meta(c, "来源", item.Source)
			meta(c, "创建", item.CreatedAt.Format("2006-01-02 15:04"))
			operation := operationLabel(item.Operation)
			if item.Derived != nil && item.Experience == nil && item.Operation == "save" {
				operation = "自动摘要"
			}
			if item.Operation == "save" && strings.Contains(item.Source, "用户确认合并") {
				operation = "确认整理新增"
			}
			if item.Operation == "save" && (item.Source == "codex" || item.Source == "pi") {
				operation = "Agent 保存"
			}
			if item.Operation == "save" && item.Origin == "imported" && item.Revision == 1 {
				operation = "导入"
			}
			meta(c, "版本", fmt.Sprintf("v%d · %s", item.Revision, operation))
			meta(c, "保存", "已保存")
			if item.Experience != nil {
				meta(c, "形态", experienceKindLabel(item.Experience.Kind))
			} else if item.Derived != nil {
				meta(c, "形态", "派生摘要 · "+derivedLabel(item.Derived.State))
			}
			if len(item.Tags) > 0 {
				meta(c, "标签", fmt.Sprint(item.Tags))
			}
			index := a.availableIndexLabel(item.IndexState)
			if item.Derived != nil && item.Derived.State == "stale" {
				index = "摘要来源已变化 · 不参与检索"
			}
			if item.State == "archived" {
				index = "已归档 · 不参与检索"
			}
			meta(c, "索引", index)
			if a.message != "" {
				ui.Text(c, a.message).FontSize(design.Small).TextColor(design.Muted)
			}
			if item.IndexState != "indexed" && a.libraryContext != nil && (item.Derived == nil || item.Derived.State != "stale") {
				quietButton(c, "重试索引").Disabled(a.retryingIndex).AlignSelf(ui.Start).OnClick(a.retryIndex)
			}
		})
		if a.relatedFor == item.ID {
			if a.relatedError != "" {
				ui.Text(c, a.relatedError).FontSize(design.Small).TextColor(design.Muted)
			}
			for _, task := range a.relatedMerges {
				quietButton(c, "相关整理 · "+mergeStatus(task.State, task.ReviewState)).Key("memory-merge-" + task.ID).OnClick(func() { a.selectMerge(task.ID) })
			}
		}
		ui.Divider(c)
		ui.Column(c).Gap(12).Children(func() {
			sourceTitle := "原始出处"
			if item.Derived != nil {
				sourceTitle = "生成时正文 · 派生转述"
			}
			if item.Experience != nil {
				sourceTitle = "保存时全局正文"
			}
			ui.Text(c, sourceTitle).Bold()
			ui.Text(c, a.readableText(c, "original-"+item.ID, item.Original)).Selectable().LineHeight(1.7).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 2).BorderColor(design.Muted)
			ui.Text(c, item.Source+" · 已保留来源").FontSize(design.Small).TextColor(design.Muted)
			if item.SourceDetail != nil {
				if item.SourceDetail.Fragment != "" && item.SourceDetail.Official == nil {
					ui.Text(c, item.SourceDetail.Fragment).Selectable().FontSize(design.Small).LineHeight(1.7)
				}
				for _, reference := range item.SourceDetail.References {
					ui.Text(c, reference).Selectable().FontSize(design.Small).TextColor(design.Muted)
				}
			}
		})
		if item.Derived != nil && a.organizationSourcesFor == item.ID {
			ui.Divider(c)
			ui.Text(c, "冻结依据与具体版本").Bold()
			ui.Text(c, "摘要与同源转述不构成新证据，来源版本和历史保留。").FontSize(design.Small).TextColor(design.Muted)
			if a.organizationSourcesError != "" {
				ui.Text(c, a.organizationSourcesError).TextColor(design.Muted)
			}
			for _, source := range a.organizationSources {
				ui.Text(c, fmt.Sprintf("%s · v%d", source.Input.Title, source.Revision)).Bold()
				if source.Changed {
					ui.Text(c, fmt.Sprintf("来源已变化 · 当前 v%d；冻结依据与已确认正文保留。", source.CurrentRevision)).FontSize(design.Small).TextColor(design.Muted)
				}
				ui.Text(c, source.Quote).Selectable().LineHeight(1.7)
				quietButton(c, "查看摘要依据").Key("derived-source-" + source.Memory).OnClick(func() { a.openMergeMemory(source.Memory, source.Project) })
				if source.Input.Origin == "raw" && source.Project == item.Dataset {
					quietButton(c, "阅读完整原始记录").Key("source-reader-open-" + source.Memory).OnClick(func() {
						a.openSourceReader(*item, records.MergeTarget{Memory: source.Memory, Revision: source.Revision})
					})
				}
			}
		}

		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, item.Category).FontSize(10).Padding(4, 6).Background(design.Surface)
			state := "当前有效"
			if item.Derived != nil && item.Derived.State == "stale" {
				state = "摘要依据过期"
			}
			if item.State == "archived" {
				state = archivedLabel(item.State)
			}
			if item.State == "deleted" {
				state = "已删除 · 保留历史"
			}
			ui.Text(c, state).FontSize(10).Padding(4, 6).Background(design.Surface)
		})
	})
	if a.detailMode == "view" {
		ui.Row(c).Shrink(0).Padding(18, 24, 24, 24).Gap(8).Children(func() {
			actionButton(c, "编辑", icons["edit"]).Disabled(item.State != "active").OnClick(a.editMemory)
			actionButton(c, "历史记录", icons["history"]).OnClick(func() { a.detailMode = "history"; a.detailScroll.Y = 0 })
			if a.isProjectKnowledge(*item) {
				quietButton(c, "复制到全局…").Key("experience-copy-preview").Disabled(a.experience.Busy).OnClick(func() { a.beginExperienceCopy(*item) })
				quietButton(c, "AI 提炼为全局…").Key("experience-distill").Disabled(a.organization.Busy).OnClick(func() { a.beginExperienceDistill(*item) })
			}
			if a.organization.Selected != "" {
				quietButton(c, "返回摘要任务").OnClick(func() { a.selectOrganization(a.organization.Selected) })
			}
			if a.merges.Selected != "" {
				quietButton(c, "返回整理任务").OnClick(func() { a.selectMerge(a.merges.Selected) })
			}
		})
	}
}
func (a *App) historyView(c *ui.Context, item MemoryItem) {
	ui.Text(c, item.Title).FontSize(design.Title).Bold()
	if item.Origin == "raw" && item.SourceDetail != nil && (item.SourceDetail.Official != nil || item.SourceDetail.Native != nil) {
		ui.Text(c, "官方原始记录保持原样。请返回详情，通过阅读器核验具体来源和当前授权后阅读。").LineHeight(1.7)
		quietButton(c, "返回详情").OnClick(func() { a.detailMode = "view" })
		return
	}
	if a.detailMode == "restore" {
		ui.Text(c, fmt.Sprintf("将 v%d 的正文恢复为新的当前版本", a.historyRevision)).Bold()
		if a.message != "" {
			ui.Text(c, a.message).FontSize(design.Small).TextColor(design.Muted)
		}
		ui.Text(c, "恢复会新增版本，保留当前正文和全部历史。确认前不会改动已有知识。").TextColor(design.Muted).LineHeight(1.6)
		for _, h := range a.history {
			if h.Revision == a.historyRevision {
				if h.Title != "" {
					ui.Text(c, h.Title).Bold()
				}
				ui.Text(c, a.readableText(c, fmt.Sprintf("restore-%s-%d", item.ID, h.Revision), h.Text)).Selectable().LineHeight(1.7).Padding(14).Background(design.Surface)
				if item.Experience != nil {
					meta(c, "恢复适用条件", h.Conditions)
				}
			}
		}
		ui.Row(c).Gap(8).Children(func() {
			actionButton(c, "返回历史", nil).OnClick(func() { a.detailMode = "history" })
			actionButton(c, "确认恢复", nil).Disabled(a.saving || a.onRestore == nil).OnClick(func() {
				if a.onRestore != nil {
					a.onRestore(a.historyRevision)
				}
			})
		})
		return
	}
	quietButton(c, "返回详情").AlignSelf(ui.Start).OnClick(func() { a.detailMode = "view" })
	if len(a.history) == 0 {
		a.empty(c, "尚无历史记录", "保存后可在这里查看每个版本和修改差异。")
	}
	for i, h := range a.history {
		ui.Column(c.Key(fmt.Sprintf("history-%d", h.Revision))).Border(1, design.Border).Radius(6).Padding(14).Gap(10).Children(func() {
			ui.Row(c).Children(func() {
				operation := operationLabel(h.Operation)
				if h.Operation == "save" && (item.Source == "codex" || item.Source == "pi") {
					operation = "Agent 保存"
				}
				if h.Operation == "save" && item.Origin == "imported" && h.Revision == 1 {
					operation = "导入"
				}
				ui.Text(c, fmt.Sprintf("v%d · %s", h.Revision, operation)).Bold()
				ui.Spacer(c)
				ui.Text(c, h.CreatedAt.Format("01-02 15:04")).FontSize(design.Small).TextColor(design.Muted)
			})
			if h.Title != "" {
				ui.Text(c, h.Title).Bold()
			}
			ui.Text(c, a.readableText(c, fmt.Sprintf("history-%s-%d", item.ID, h.Revision), h.Text)).Selectable().LineHeight(1.7)
			if item.Experience != nil {
				meta(c, "适用条件", h.Conditions)
			}
			if i+1 < len(a.history) {
				ui.Text(c, "相较上一版本").FontSize(design.Small).TextColor(design.Muted)
				ui.Text(c, a.readableText(c, fmt.Sprintf("diff-%s-%d", item.ID, h.Revision), lineDiff(a.history[i+1].Text, h.Text))).FontSize(design.Small).Selectable().LineHeight(1.6).Background(design.Surface).Padding(10)
			}
			if h.Revision != item.Revision {
				quietButton(c, fmt.Sprintf("恢复 v%d", h.Revision)).AlignSelf(ui.Start).OnClick(func() {
					a.historyRevision = h.Revision
					a.restoreExpected = item.Revision
					a.message = ""
					a.detailMode = "restore"
					a.detailScroll.Y = 0
				})
			} else {
				ui.Text(c, "当前版本").FontSize(design.Small).TextColor(design.Muted)
			}
		})
	}
}
func (a *App) empty(c *ui.Context, title, description string) {
	ui.Column(c).Padding(14, 10).Gap(10).Children(func() {
		if title != "" {
			ui.Text(c, title).FontWeight(600)
		}
		ui.Text(c, description).FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
	})
}
func (a *App) emptyTitle() string {
	if a.page == "Agent" {
		return "尚未连接 Agent"
	}
	if a.page == "整理记录" {
		return "暂无整理记录"
	}
	if a.page == "项目" && a.scope == "" {
		return "添加本地项目"
	}
	return "选择一条记忆"
}
func (a *App) pageDescription() string {
	switch a.page {
	case "Agent":
		return "选择已发现的 Codex、pi、TRAE CLI 或 TRAE IDE，连接并管理允许访问的项目。"
	case "整理记录":
		return "选择同一项目内两条知识生成建议，确认后合并并保留来源与历史。"
	case "项目":
		if a.scope == "" {
			return "一个项目对应一个本地代码仓库。登记后多个 Agent 可以共享项目记忆。"
		}
	}
	return "从列表选择记忆查看正文、来源与历史，或新增一条记忆。"
}

func (a *App) settings(c *ui.Context) {
	quietButton(c, "官方 Cognee 接入与同步").Key("cognee-settings").OnClick(a.openCognee)
	quietButton(c, "恢复设置指引").Key("guidance-restore").OnClick(func() { a.hideGuidance(false); a.navigate("使用指引", "") })
	quietButton(c, "初始化记录").Key("initialization-settings").OnClick(func() { a.openInitialization("") })
	quietButton(c, "整理计划").Key("schedule-settings").OnClick(func() { a.navigate("整理计划", "") })
	if a.firstLaunch.Controller != nil {
		quietButton(c, "本机环境准备").Key("first-launch-resume").OnClick(a.openFirstLaunch)
	}
	ui.Text(c, "本地组件与更新").FontSize(20).Bold()
	if a.prepareRuntime == nil {
		ui.Text(c, "本地引擎：待准备").Bold()
		ui.Text(c, "引擎与模型将独立下载；当前尚无可安装的组件版本。").TextColor(c.Theme().TextMuted)
	} else {
		ui.Text(c, a.runtimeStatus.Message).Selectable()
		if a.runtimeStatus.EngineBytes > 0 && a.runtimeStatus.ModelBytes > 0 {
			ui.Text(c, fmt.Sprintf("引擎 %.2f MB · 模型 %.2f MB", float64(a.runtimeStatus.EngineBytes)/1e6, float64(a.runtimeStatus.ModelBytes)/1e6)).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		if a.runtimeStatus.Total > 0 {
			ui.Text(c, fmt.Sprintf("已准备 %.2f / %.2f MB", float64(a.runtimeStatus.Downloaded)/1e6, float64(a.runtimeStatus.Total)/1e6)).TextColor(c.Theme().TextMuted)
			ui.Progress(c, float64(a.runtimeStatus.Downloaded)/float64(a.runtimeStatus.Total))
		}
		busy := a.backups.RestoreLocked || a.backups.Busy || a.runtimeStatus.Phase == "checking" || a.runtimeStatus.Phase == "downloading" || a.runtimeStatus.Phase == "installing" || a.runtimeStatus.Phase == "installed" || a.runtimeStatus.Phase == "preparing" || a.runtimeStatus.Phase == "verifying"
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "准备 / 重试").Disabled(busy).OnClick(a.prepareRuntime)
			if busy {
				ui.Button(c, "取消准备").OnClick(a.cancelRuntime)
			}
			if a.runtimeStatus.Phase == "ready" {
				ui.Button(c, "停止服务").Disabled(busy).OnClick(a.stopRuntime)
			}
			if a.runtimeStatus.Phase == "stopped" {
				ui.Button(c, "启动服务").Disabled(busy).OnClick(a.resumeRuntime)
			}
		})
		previous := a.keepService
		ui.Checkbox(c, &a.keepService, "退出 App 后继续运行记忆服务")
		if previous != a.keepService && a.keepChanged != nil {
			a.keepChanged(a.keepService)
		}
	}
	ui.Divider(c)
	a.indexRecoverySettings(c)
	a.backupSettings(c)
	ui.Divider(c)
	ui.Text(c, "客户端 "+a.version).Bold()
	ui.Text(c, a.updateText).Selectable().TextColor(c.Theme().TextMuted)
	ui.Button(c, "检查更新").Disabled(a.checking || a.check == nil).OnClick(func() {
		if a.check != nil {
			a.check()
		}
	})
	if a.installUpdate != nil {
		ui.Button(c, "安装更新并重新打开").Disabled(a.checking).OnClick(a.installUpdate)
	}
	ui.Divider(c)
	ui.Text(c, "界面缩放").Bold()
	ui.Row(c).Gap(10).Children(func() {
		ui.Button(c, "缩小").Disabled(a.zoom <= 0.8).OnClick(func() { a.zoom = max(float32(0.8), a.zoom-0.1) })
		ui.Text(c, fmt.Sprintf("%.0f%%", a.zoom*100)).Width(48)
		ui.Button(c, "放大").Disabled(a.zoom >= 1.4).OnClick(func() { a.zoom = min(float32(1.4), a.zoom+0.1) })
	})
}

// UpdateTimeout bounds a manual check so a stalled feed cannot block shutdown.
const UpdateTimeout = 20 * time.Second
