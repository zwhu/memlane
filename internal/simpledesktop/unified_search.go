package simpledesktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type unifiedSearchState struct {
	Open, Focus, Loading, Loaded         bool
	Query, Scope, Type, Error, Submitted string
	Rows                                 []Item
	Selection                            int
	Scroll                               ui.ScrollState
	Reveal                               bool
	request                              uint64
	cancel                               context.CancelFunc
}

func (a *App) searchCatalog(scope string) []Project {
	if scope == "@common" {
		return append([]Project(nil), a.Config.Common...)
	}
	if scope == "" {
		return overviewProjects(a.Config)
	}
	for _, p := range a.Config.Projects {
		if p.ID == scope {
			return []Project{p}
		}
	}
	return nil
}
func (a *App) searchSignature() string {
	s := &a.searchUI
	v := a.readingIdentity + "/" + strings.TrimSpace(s.Query) + "/" + s.Scope + "/" + s.Type
	for _, p := range a.searchCatalog(s.Scope) {
		v += "/" + p.ID + ":" + p.Dataset
	}
	return v
}
func (a *App) openUnifiedSearch(restore bool) {
	s := &a.searchUI
	if s.Open {
		s.Focus = true
		return
	}
	a.projectUI.InfoOpen = false
	a.projectUI.MenuOpen = false
	if !restore {
		scope := ""
		if a.page == "知识" || a.page == "图谱" {
			if p, ok := a.current(); ok {
				scope = p.ID
			}
		} else if a.page == "通用知识" {
			scope = "@common"
		}
		if s.Scope != scope {
			a.closeUnifiedSearch()
			*s = unifiedSearchState{Scope: scope, Type: "全部", request: s.request}
		}
	}
	if s.Type == "" {
		s.Type = "全部"
	}
	wasOpen := s.Open
	s.Open = true
	s.Focus = wasOpen
}
func (a *App) closeUnifiedSearch() {
	s := &a.searchUI
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.request++
	s.Open = false
	s.Loading = false
}
func (a *App) invalidateUnifiedSearch(dataset string) {
	s := &a.searchUI
	a.closeUnifiedSearch()
	if dataset == "" {
		s.Rows = nil
		s.Loaded = false
		s.Submitted = ""
		return
	}
	rows := s.Rows[:0]
	for _, i := range s.Rows {
		if i.Dataset != dataset {
			rows = append(rows, i)
		}
	}
	s.Rows = rows
	s.Submitted = ""
}
func (a *App) submitUnifiedSearch() {
	s := &a.searchUI
	query := strings.TrimSpace(s.Query)
	if query == "" {
		if s.cancel != nil {
			s.cancel()
		}
		s.request++
		s.Rows = nil
		s.Submitted = ""
		s.Error = ""
		s.Loading = false
		s.Loaded = false
		return
	}
	if s.Loading && s.Submitted == a.searchSignature() {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request := s.request
	signature := a.searchSignature()
	s.Submitted = signature
	s.Loading = true
	s.Error = ""
	s.Selection = 0
	projects := a.searchCatalog(s.Scope)
	filter := ""
	if s.Type == "知识" {
		filter = "知识"
	} else if s.Type == "原始记录" {
		filter = "原始材料"
	}
	client := a.Client
	identity := a.readingIdentity
	go func() {
		defer cancel()
		rows, err := client.search(ctx, projects, query, filter)
		a.Update(func() {
			if request != s.request || signature != a.searchSignature() || identity != a.readingIdentity {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = err.Error()
				if readingUnavailable(err) {
					a.InvalidateReading("")
				}
				return
			}
			// Preserve official identity; stable grouping does not invent document ids.
			out := []Item{}
			seen := map[string]bool{}
			for _, knowledge := range []bool{true, false} {
				for _, i := range rows {
					if strings.Contains(i.Kind, "知识") == knowledge && !seen[i.key()] {
						out = append(out, i)
						seen[i.key()] = true
					}
				}
			}
			s.Rows = out
			s.Reveal = true
			s.Loaded = true
			s.Scroll.Y = 0
		})
	}()
}
func (a *App) searchSubmitOrOpen() {
	s := &a.searchUI
	if !s.Loading && s.Loaded && s.Error == "" && s.Submitted == a.searchSignature() && len(s.Rows) > 0 {
		a.openSearchResult(s.Rows[min(s.Selection, len(s.Rows)-1)])
		return
	}
	a.submitUnifiedSearch()
}
func (a *App) openSearchResult(i Item) {
	if i.ReadingState == "unavailable" {
		return
	}
	origin := a.readingLocation()
	if a.searchOrigin != nil {
		origin = a.searchOrigin
	}
	index := -1
	found := false
	for n, p := range a.Config.Projects {
		if p.Dataset == i.Dataset {
			index = n
			found = true
			break
		}
	}
	if !found {
		for _, p := range a.Config.Common {
			if p.Dataset == i.Dataset {
				found = true
				break
			}
		}
	}
	if !found {
		a.searchUI.Error = "结果范围已失效，请重新搜索"
		return
	}
	a.closeUnifiedSearch()
	a.navigate("检索结果", index)
	a.searchOrigin = origin
	a.selectItem(a.state(), i)
}
func (a *App) returnToSearch() {
	v := a.searchOrigin
	a.restoreReadingLocation(v)
	a.openUnifiedSearch(true)
}
func (a *App) unifiedSearchTrigger(c *ui.Context) {
	ui.Button(c, "").Key("unified-search-trigger").Label("搜索知识与原始记录").Width(264).Height(32).Padding(0, 11).Gap(10).Background(ui.Hex("#f6f8fa")).Border(1, homeBorder).Radius(7).Children(func() {
		ui.Icon(c, homeSearch).FontSize(16).TextColor(homeMuted)
		homeText(c, "搜索知识与原始记录", homeMeta).FontSize(12)
		ui.Spacer(c)
		homeText(c, "⌘ K", homeMeta)
	}).OnClick(a.OpenSearch)
}
func (a *App) searchDialog(c *ui.Context) {
	s := &a.searchUI
	if !s.Open {
		return
	}
	w, h := c.Size()
	ui.Modal(c.Key("unified-search-modal"), &s.Open, func() {
		ui.Row(c).Height(76).Shrink(0).Padding(0, 20).Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, homeSearch).FontSize(23).TextColor(homeLinkInk)
			input := ui.TextInputBase(c.Key("unified-query"), &s.Query).Label("搜索关键词").Placeholder("输入关键词，按 Enter 搜索").Grow(1).MinWidth(0).FontSize(20).OnSubmit(a.searchSubmitOrOpen)
			if input.Changed() {
				if s.cancel != nil {
					s.cancel()
				}
				s.request++
				s.Loading = false
				s.Error = ""
				if strings.TrimSpace(s.Query) == "" {
					s.Rows = nil
					s.Loaded = false
					s.Submitted = ""
				}
			}
			if s.Focus {
				input.Focus()
				s.Focus = false
			}
			if input.Shortcut(0, ui.KeyDown) && len(s.Rows) > 0 {
				s.Selection = (s.Selection + 1) % len(s.Rows)
				s.Reveal = true
			}
			if input.Shortcut(0, ui.KeyUp) && len(s.Rows) > 0 {
				s.Selection = (s.Selection + len(s.Rows) - 1) % len(s.Rows)
				s.Reveal = true
			}
			projects := a.Config.Projects
			options := []string{"全部项目与通用知识", "通用知识"}
			options = append(options, projectLabels(projects)...)
			selected := options[0]
			if s.Scope == "@common" {
				selected = options[1]
			}
			for n, p := range projects {
				if p.ID == s.Scope {
					selected = options[n+2]
				}
			}
			ui.Select(c, &selected, options).Label("搜索范围").Width(155).FontSize(11).OnChange(func() {
				scope := ""
				if selected == options[1] {
					scope = "@common"
				}
				for n, label := range options[2:] {
					if selected == label {
						scope = projects[n].ID
					}
				}
				s.Scope = scope
				a.cancelChangedSearch()
			})
			readingAction(c, "关闭搜索", readingClose, func() { a.closeUnifiedSearch() })
		})
		ui.Column(c).Shrink(0).Padding(8, 22).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
			status := "输入关键词后按 Enter 检索官方内容"
			if s.Loading {
				status = "正在搜索…"
			} else if s.Error != "" {
				status = "搜索失败 · " + s.Error + " · 按 Enter 重试"
			} else if s.Loaded {
				status = fmt.Sprintf("已返回 %d 条", len(s.Rows))
				if s.Submitted != a.searchSignature() {
					status = "条件已更改 · 按 Enter 搜索"
				}
			}
			homeText(c, status, homeMeta).MaxLines(2)
		})
		ui.Scroll(c.Key("unified-results")).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(8, 10).Gap(4).Children(func() {
			if len(s.Rows) == 0 {
				message := "在当前范围搜索知识和原始记录"
				if s.Loaded && !s.Loading && s.Error == "" {
					message = "当前范围没有匹配结果，可切换全部项目或清除筛选"
				}
				homeText(c, message, homeBody).TextColor(homeMuted).Margin(16)
			}
			group := ""
			for n, row := range s.Rows {
				i := row
				index := n
				g := "原始记录"
				if strings.Contains(i.Kind, "知识") {
					g = "知识"
				}
				if g != group {
					homeText(c, g, homeMeta).Margin(8, 12)
					group = g
				}
				b := ui.Button(c, "").Key("search-hit-"+i.key()).Label(i.Title).FillWidth().MinWidth(0).Justify(ui.Start).Padding(12, 14).BorderWidth(0).Radius(9).Gap(14).Background(ui.Transparent)
				if s.Selection == n {
					b.Background(ui.Hex("#edf3f9"))
					if s.Reveal {
						b.ScrollIntoView()
					}
				}
				b.Children(func() {
					ui.Icon(c, homeDocument).FontSize(19).TextColor(homeLinkInk)
					ui.Column(c).Grow(1).MinWidth(0).Gap(7).Children(func() {
						homeText(c, i.Title, homeTitle).FontSize(14).MaxLines(2)
						homeText(c, i.Preview, homeBody).FontSize(12).TextColor(homeMuted).MaxLines(2)
						project := "范围未知"
						for _, p := range overviewProjects(a.Config) {
							if p.Dataset == i.Dataset {
								project = p.Name
							}
						}
						homeText(c, project+" · "+g, homeMeta)
					})
				}).Disabled(s.Loading || s.Error != "" || s.Submitted != a.searchSignature() || i.ReadingState == "unavailable").OnClick(func() { s.Selection = index; a.openSearchResult(i) })
			}
		})
		s.Reveal = false
		ui.Row(c).Height(62).Shrink(0).Padding(0, 20).Gap(16).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
			for _, kind := range []string{"全部", "知识", "原始记录"} {
				k := kind
				b := ui.Button(c, "").Label("筛选"+k).Padding(8, 12).Background(ui.Transparent).BorderWidth(0).Radius(16)
				if k == s.Type {
					b.Background(ui.Hex("#e7edf3"))
				}
				b.Children(func() { homeText(c, k, homeAction) }).OnClick(func() { s.Type = k; a.cancelChangedSearch() })
			}
			ui.Spacer(c)
			homeText(c, "↑↓ 选择  ↵ 搜索/打开  esc 关闭", homeMeta)
		})
	}).Width(min(float32(760), w-80)).Height(min(float32(596), h-160)).Padding(0).Gap(0).Label("搜索知识与原始记录弹框").Transition(projectOverlayMotion())
	if !s.Open {
		a.closeUnifiedSearch()
	}
}
func (a *App) cancelChangedSearch() {
	s := &a.searchUI
	if s.cancel != nil {
		s.cancel()
	}
	s.request++
	s.Loading = false
	s.Focus = true
	s.Error = ""
}
