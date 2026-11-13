package simpledesktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
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
	DetailScroll                   map[string]*ui.ScrollState
	Tools                          map[string]bool
	History                        []Item
	request                        uint64
	readRequest                    uint64
	readCancel                     context.CancelFunc
	cancel                         context.CancelFunc
}
type App struct {
	overviewState   overviewViewState
	searchType      string
	editProject     *Project
	editIndex       int
	settingsMessage string
	focusSearch     bool

	graph                                             *GraphHost
	graphError, graphMode, graphSelection, codeStatus string
	graphLoading, codeBusy                            bool
	codeUnknown                                       bool
	codeRemoteActive                                  bool

	Config              Config
	Client              Client
	Update              func(func())
	page                string
	project             int
	kind                string
	panes               map[string]*pane
	epoch               uint64
	service, completion string
	configError         string
	md                  markdown.Options
	searchProject       string
	recent              []Item
	homeError           string
}

func New(c Config, err error) *App {
	a := &App{Config: c, Client: Client{Config: c}, page: "总览", kind: "知识", panes: map[string]*pane{}, service: "尚未检查", completion: "尚未检查", searchProject: "全部项目"}
	if err != nil {
		a.configError = err.Error()
	}
	a.searchType = "全部类型"
	a.graphMode = "实体关系"
	if c.GraphAssets != "" {
		a.graph, err = RegisterGraph(c.GraphAssets)
		if err != nil {
			a.graphError = err.Error()
		}
	}
	a.md = markdown.Options{Theme: &markdown.Theme{FontSize: 12, LineHeight: 1.9, Text: ink, Muted: muted, CodeSize: 11.0 / 12, CodeBlockBackground: ui.Hex("#f5f8fb"), CodeBlockBorder: border}, CodeBlock: func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Background(ui.Hex("#f5f8fb")).Border(1, border).Radius(6).Children(func() {
			ui.Row(c).Padding(6, 12).Children(func() {
				ui.Text(c, strings.ToUpper(b.Language)).FontSize(10).TextColor(muted)
				ui.Spacer(c)
				ui.Button(c, "复制代码").OnClick(func() { c.WriteClipboard(b.Code()) })
			})
			b.Body(c).Label("代码横向滚动")
		})
	}}
	return a
}
func (a *App) Start() { a.health(); a.loadRecent(); a.restoreCodeState() }
func (a *App) health() {
	client := a.Client
	completionURL := a.Config.CompletionURL
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.request(ctx, "GET", "/health", nil)
		state := "已连接"
		if err != nil {
			state = err.Error()
		}
		completion := "未配置"
		if completionURL != "" {
			cc := Client{Config: Config{ServiceURL: completionURL}}
			_, e := cc.request(ctx, "GET", "/health", nil)
			completion = "端点已连接（登录状态以实际请求为准）"
			if e != nil {
				completion = e.Error()
			}
		}
		a.Update(func() { a.service = state; a.completion = completion })
	}()
}
func (a *App) loadRecent()      { a.loadOverview() }
func (a *App) routeKey() string { return fmt.Sprintf("%s/%d/%s", a.page, a.project, a.kind) }
func (a *App) state() *pane {
	k := a.routeKey()
	s := a.panes[k]
	if s == nil {
		s = &pane{DetailScroll: map[string]*ui.ScrollState{}, Tools: map[string]bool{}}
		a.panes[k] = s
	}
	return s
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
func (a *App) navigate(page string, index int) {
	a.epoch++
	a.graph.clear()
	for _, p := range a.panes {
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
	a.project = index
	if page == "总览" {
		a.loadOverview()
	}
	if page == "知识" {
		a.kind = "知识"
	}
	if page == "通用知识" {
		a.kind = "知识"
	}
	if page != "总览" && page != "设置" && page != "图谱" {
		s := a.state()
		if !s.Loaded {
			a.load(s)
		}
	}
}
func (a *App) load(s *pane) {
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
		ps = a.Config.Projects
		if a.searchProject != "全部项目" {
			ps = nil
			for _, p := range a.Config.Projects {
				if p.Name == a.searchProject {
					ps = append(ps, p)
				}
			}
		}
	}
	go func() {
		defer cancel()
		var rows []Item
		var more bool
		var err error
		if page == "搜索" || q != "" {
			if q == "" {
				rows = []Item{}
			} else {
				if page != "搜索" {
					ps = []Project{p}
				}
				rows, err = a.Client.search(ctx, ps, q, filter)
			}
		} else if !ok {
			rows = []Item{}
		} else if kind == "会话" {
			rows, more, err = a.Client.sessions(ctx, p, offset)
		} else {
			rows, more, err = a.Client.documents(ctx, p, offset, kind == "文档")
		}
		a.Update(func() {
			if a.epoch != epoch || s.request != request {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = err.Error()
				return
			}
			s.Items = rows
			s.More = more
			s.Loaded = true
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
	if s.readCancel != nil {
		s.readCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.readCancel = cancel
	s.readRequest++
	request, epoch := s.readRequest, a.epoch
	s.Reading = true
	s.History = nil
	s.Error = ""
	if s.Selected == nil || s.Selected.key() != i.key() {
		s.Selected = &i
	}
	go func() {
		defer cancel()
		result, err := a.Client.read(ctx, i)
		a.Update(func() {
			if epoch != a.epoch || request != s.readRequest {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = err.Error()
				if strings.Contains(err.Error(), "HTTP 404") {
					s.Selected = nil
				}
				return
			}
			s.Selected = &result
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
	go func() {
		defer cancel()
		var i Item
		var err error
		if original.SourceDocument != "" {
			i, err = a.Client.documentByID(ctx, original.Dataset, original.SourceDocument)
		} else {
			i, err = a.Client.source(ctx, original)
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.readRequest {
				return
			}
			s.Reading = false
			if err != nil {
				s.Error = err.Error()
				return
			}
			s.History = append(s.History, original)
			s.Selected = &i
		})
	}()
}
func button(c *ui.Context, label string, fn func()) {
	ui.Button(c, label).FontSize(12).Padding(8, 12).Background(ui.Transparent).BorderWidth(0).Justify(ui.Start).OnClick(fn)
}
func (a *App) View(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(ui.Hex("#ffffff")).Children(func() {
		a.navigation(c)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Height(56).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(border).Padding(0, 28).DragWindow().Children(func() {
				title := a.page
				if p, ok := a.current(); ok && a.page == "知识" {
					title = p.Name
				}
				ui.Text(c, title).FontSize(14).Bold().TextColor(ink)
				ui.Spacer(c)
				button(c, "搜索知识", a.OpenSearch)
			})
			if a.configError != "" {
				ui.Text(c, "运行配置不可用："+a.configError).Margin(24).TextColor(ink)
				return
			}
			switch a.page {
			case "总览":
				a.home(c)
			case "设置":
				a.settings(c)
			case "图谱":
				a.graphView(c)
			default:
				a.reading(c)
			}
		})
	})
}
func (a *App) navigation(c *ui.Context) {
	ui.Column(c).Width(196).Shrink(0).Background(blue).Children(func() {
		ui.Row(c).Height(72).Padding(30, 24, 0, 24).DragWindow().Children(func() {
			ui.Icon(c, mark).FontSize(19).TextColor(ui.Hex("#60758a"))
			ui.Text(c, "Memlane").Margin(0, 10).FontSize(14).Bold().TextColor(ui.Hex("#60758a"))
		})
		ui.Column(c).Padding(12).Gap(6).Children(func() {
			for _, page := range []string{"总览", "搜索", "通用知识"} {
				label := page
				nav := ui.Button(c, "").Label(label).Children(func() {
					ui.Icon(c, navIcons[label]).FontSize(16).TextColor(muted)
					ui.Text(c, label).Margin(0, 12).FontSize(13).TextColor(ink)
				}).Key("nav-"+page).Height(38).Justify(ui.Start).Padding(8, 16).Background(ui.Transparent).BorderWidth(0).Radius(6).FontSize(13).OnClick(func() { a.navigate(label, 0) })
				if a.page == page {
					nav.Background(ui.Hex("#d5e1ef"))
				}
			}
		})
		ui.Text(c, "项目").FontSize(10).TextColor(muted).Margin(22, 24, 8, 24)
		ui.Scroll(c).Grow(1).Padding(0, 12).Gap(6).Children(func() {
			for idx, p := range a.Config.Projects {
				index := idx
				nav := ui.Button(c, "").Label(p.Name).Children(func() {
					ui.Icon(c, folder).FontSize(16).TextColor(muted)
					ui.Text(c, p.Name).Margin(0, 12).FontSize(13).TextColor(ink)
				}).Key("project-"+p.ID).Height(38).Justify(ui.Start).Padding(8, 16).Background(ui.Transparent).BorderWidth(0).Radius(6).OnClick(func() { a.navigate("知识", index) })
				if a.page == "知识" && a.project == index {
					nav.Background(ui.Hex("#d5e1ef"))
				}
			}
		})
		button(c, "设置", func() { a.navigate("设置", 0) })
		ui.Text(c, "服务状态 · "+a.service).FontSize(10).TextColor(muted).Margin(12, 20, 18, 20).MaxLines(2)
	})
}
func (a *App) home(c *ui.Context) { a.overviewHome(c) }
func (a *App) settings(c *ui.Context) {
	ui.Scroll(c).Grow(1).Padding(30, 32).Gap(22).Children(func() {
		ui.Text(c, "服务与连接").FontSize(21).Bold()
		ui.Text(c, "Cognee 官方服务\n"+a.Config.ServiceURL+"\n"+a.service).FontSize(12).LineHeight(1.9)
		ui.Text(c, "Codex 订阅推理\n模型："+a.Config.Model+"\n"+a.completion).FontSize(12).LineHeight(1.9)
		button(c, "刷新连接状态", a.health)
		ui.Text(c, "项目关联").FontSize(16).Bold()
		for idx, p := range a.Config.Projects {
			index := idx
			button(c, p.Name+" · 修改关联", func() { copy := a.Config.Projects[index]; a.editProject = &copy; a.editIndex = index })
			ui.Text(c, p.Name+"\n"+p.Path+"\n知识数据组："+p.Dataset).FontSize(12).LineHeight(1.8)
		}
		button(c, "关联已有官方项目", func() { a.editProject = &Project{}; a.editIndex = -1 })
		a.projectEditor(c)
		ui.Text(c, "官方插件连接状态：以宿主实际加载回执为准").FontSize(11).TextColor(muted)
		ui.Text(c, "开发入口\npython3 scripts/dev.py\n关闭窗口后官方服务与模型端点继续运行。重新打开窗口使用启动器入口；明确停止启动器才收尾后台。\n配置更改后停止并重新启动后台生效。电脑重启后手动启动。\n向量模型由 Cognee 官方配置管理。").FontSize(12).LineHeight(1.9)
	})
}
func (a *App) reading(c *ui.Context) {
	s := a.state()
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(316).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(border).Children(func() {
			ui.Column(c).Padding(24, 18, 16, 18).Gap(12).Children(func() {
				ui.Text(c, "项目内容").FontSize(10).TextColor(muted)
				if a.page != "搜索" {
					ui.Row(c).Gap(3).Children(func() {
						if a.page != "通用知识" {
							button(c, "图谱", func() { a.navigate("图谱", a.project); a.loadGraph() })
						}
						for _, k := range []string{"知识", "会话", "文档"} {
							kind := k
							label := kind
							if k == "会话" {
								label = "原始记录"
							}
							button(c, label, func() {
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
					options := []string{"全部项目"}
					for _, p := range a.Config.Projects {
						options = append(options, p.Name)
					}
					ui.Select(c, &a.searchProject, options).Width(270).OnChange(func() { s.Offset = 0; a.load(s) })
					ui.Select(c, &a.searchType, []string{"全部类型", "知识", "原始材料"}).Width(270).OnChange(func() { s.Offset = 0; a.load(s) })
					ui.Text(c, "最多30个官方检索片段；类型按返回元数据筛选").FontSize(10).TextColor(muted)
				}
				if a.page == "通用知识" {
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
				input := ui.TextInput(c.Key("query-"+a.routeKey()), &s.Query).Placeholder("搜索当前内容").Label("官方内容搜索").Height(36).OnSubmit(func() { s.Offset = 0; a.load(s) })
				if a.focusSearch {
					input.Focus()
					a.focusSearch = false
				}
				ui.Row(c).Children(func() {
					ui.Text(c, fmt.Sprintf("已载入 %d 项", len(s.Items))).FontSize(10).TextColor(muted)
					ui.Spacer(c)
					button(c, "刷新", func() { a.load(s) })
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
						label = "官方当前暂不支持个人偏好与通用经验的独立阅读。后续可使用独立的通用知识数据组，供所有 Agent 读取。"
					}
					if s.Query != "" {
						label = "没有匹配结果"
					}
					ui.Text(c, label).Margin(12).FontSize(12).TextColor(muted)
				}
				for _, row := range s.Items {
					i := row
					b := ui.Button(c, "").Key("item-"+i.key()).Justify(ui.Start).Padding(14, 12).Background(ui.Transparent).BorderWidth(0).Radius(6).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
							ui.Text(c, i.Title).MinWidth(0).FontSize(13).Bold().SingleLine().TextColor(ink)
							ui.Text(c, i.Kind+" · "+displayDate(i.Date)).MinWidth(0).FontSize(10).TextColor(muted).SingleLine()
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
		a.detail(c, s)
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
			ui.Text(c, i.Kind+" / 只读").FontSize(10).TextColor(muted)
			ui.Text(c, i.Title).FontSize(21).Bold().TextColor(ink)
			ui.Text(c, i.Dataset+" · "+i.ID).FontSize(10).TextColor(muted)
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
					ui.Text(c, "Agent").FontSize(10).TextColor(muted)
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
						button(c, label, func() { s.Tools[key] = !s.Tools[key] })
						if s.Tools[key] {
							markdown.View(c.Key("tool-"+key), "```json\n"+string(t.Params)+"\n```\n\n```json\n"+string(t.Result)+"\n```\n\n"+t.Error, &a.md)
						}
					})
				}
			} else {
				markdown.View(c.Key("body-"+i.key()), i.Body, &a.md)
			}
			if i.Source != "" || i.SourceDocument != "" {
				ui.Column(c).Padding(16).Border(1, border).Radius(8).Gap(8).Children(func() {
					ui.Text(c, "出处 · 官方关联材料").FontSize(12).Bold()
					ui.Text(c, i.Source+i.SourceDocument).FontSize(10).TextColor(muted)
					button(c, "查看原始记录 →", func() { a.source(s) })
				})
			} else if i.Kind == "知识" {
				ui.Text(c, "官方未提供可定位的会话出处").FontSize(10).TextColor(muted)
			}
		})
		ui.Row(c).Height(48).Shrink(0).Padding(0, 24).BorderWidth(1, 0, 0, 0).BorderColor(border).Children(func() {
			ui.Text(c, "只读 · 内容由 Cognee 提供").FontSize(10).TextColor(muted)
			ui.Spacer(c)
			button(c, "复制已载入内容", func() { c.WriteClipboard(i.Body) })
		})
	})
}

func displayDate(date string) string {
	if date == "" {
		return "时间未知"
	}
	return date
}

func (a *App) OpenSearch() { a.navigate("搜索", 0); a.focusSearch = true }
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
		}{{"项目标识", &p.ID}, {"显示名称", &p.Name}, {"仓库路径", &p.Path}, {"知识数据组 ID", &p.Dataset}, {"代码数据组 ID（可选）", &p.CodeDataset}} {
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
				for _, state := range a.panes {
					if state.cancel != nil {
						state.cancel()
					}
					if state.readCancel != nil {
						state.readCancel()
					}
				}
				a.panes = map[string]*pane{}
				a.editProject = nil
				a.settingsMessage = "关联已保存；服务与模型配置通过重启后台生效"
				a.loadRecent()
			})
			button(c, "取消", func() { a.editProject = nil })
		})
	})
}

func (a *App) returnFromSource(s *pane) {
	if len(s.History) == 0 {
		return
	}
	last := len(s.History) - 1
	original := s.History[last]
	history := append([]Item(nil), s.History[:last]...)
	a.selectItem(s, original)
	s.History = history
}
