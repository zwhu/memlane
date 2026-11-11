package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type homeActivityUI struct {
	Delivery                                      AgentDeliveryUI
	Return, ReturnFull                            bool
	Detail                                        homeActivityDetail
	Scroll                                        ui.ScrollState
	OverviewScroll                                ui.ScrollState
	Review                                        homeReviewUI
	Cursor                                        string
	Back                                          []string
	BackRequested                                 bool
	Full, Loaded, Loading                         bool
	Sequence                                      uint64
	Error, Project, Kind, ProjectLabel, KindLabel string
	Items                                         []records.HomeActivity
	Next                                          string
	Total                                         int
	CheckedAt                                     time.Time
}

func (a *App) refreshHomeActivity(more bool) {
	s := &a.homeActivity
	if a.libraryContext == nil || a.dispatch == nil || s.Loading {
		return
	}
	if more && s.Next == "" {
		return
	}
	limit := 4
	if s.Full {
		limit = 8
	}
	q := records.HomeActivityQuery{Project: s.Project, Kind: s.Kind, Limit: limit, Before: s.Cursor}
	backward := s.BackRequested
	s.BackRequested = false
	if backward && len(s.Back) > 0 {
		q.Before = s.Back[len(s.Back)-1]
	}
	if more {
		q.Before = s.Next
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	owner, library := a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(owner, 5*time.Second)
		defer cancel()
		page, err := library.HomeActivities(ctx, q)
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != seq || a.library.Root != library.Root {
				return
			}
			s.Loading = false
			s.CheckedAt = time.Now()
			if err != nil {
				s.Error = "动态读取失败；保留上次已读取记录。"
				return
			}
			s.Error = ""
			s.Loaded = true
			s.Total = page.Total
			s.Next = page.Next
			if more {
				s.Back = append(s.Back, s.Cursor)
			} else if backward && len(s.Back) > 0 {
				s.Back = s.Back[:len(s.Back)-1]
			}
			s.Cursor = q.Before
			s.Items = page.Items
			if s.Full && len(s.Items) > 0 {
				found := false
				for _, event := range s.Items {
					if event.ID == s.Detail.Event.ID {
						found = true
						break
					}
				}
				if !found {
					a.selectHomeActivity(s.Items[0])
				}
			}
		})
	}()
}

func (a *App) changeHomeActivity(full bool, project, kind string) {
	a.homeActivity.Delivery.Full = false
	s := &a.homeActivity
	s.Sequence++
	s.Loading = false
	s.Loaded = false
	s.Error = ""
	s.Full = full
	if project == "" {
		s.ProjectLabel = "全部项目"
	}
	if kind == "" {
		s.KindLabel = "全部类型"
	}
	s.Project = project
	s.Kind = kind
	s.Items = nil
	s.Next = ""
	s.Cursor = ""
	s.Back = nil
	s.BackRequested = false
	s.CheckedAt = time.Time{}
	s.Detail.Sequence++
	s.Detail.Event = records.HomeActivity{}
	a.refreshHomeActivity(false)
}

func (a *App) homeActivityPanel(c *ui.Context) {
	s := &a.homeActivity
	c.After(5 * time.Second)
	if !s.Loading && (s.CheckedAt.IsZero() || !s.Full && time.Since(s.CheckedAt) > 5*time.Second) {
		a.refreshHomeActivity(false)
	}
	ui.Row(c).Wrap().AlignItems(ui.Center).Gap(8).Margin(0, 0, 6, 0).Children(func() {
		title := "最近动态"
		if s.Full {
			title = "全部动态"
		}
		ui.Text(c, title).Bold()
		if s.Loaded && s.Full {
			ui.Text(c, fmt.Sprintf("%d 条", s.Total)).FontSize(10).TextColor(design.Muted)
		}
		ui.Spacer(c)
		a.agentDeliveryEntry(c)
		if s.Full {
			quietButton(c, "返回最近动态").OnClick(func() { a.changeHomeActivity(false, "", "") })
		} else if s.Loaded && s.Total > 4 {
			quietButton(c, fmt.Sprintf("查看全部 %d 条", s.Total)).OnClick(func() { a.changeHomeActivity(true, "", "") })
		}
		quietButton(c, "刷新动态").Disabled(s.Loading).OnClick(func() { a.refreshHomeActivity(false) })
	})
	if s.Full {
		a.homeActivityFilters(c)
	}
	ui.Divider(c)
	if s.Error != "" {
		ui.Text(c, s.Error).FontSize(design.Small).TextColor(design.Muted).Margin(10, 0)
	}
	if len(s.Items) == 0 {
		label := "暂无动态"
		if s.Loading {
			label = "正在读取动态"
		} else if !s.Loaded && s.Error == "" {
			label = "等待本地记忆服务"
		}
		ui.Column(c).Gap(8).Padding(18, 0).Children(func() {
			ui.Text(c, label).FontSize(design.Small).TextColor(design.Muted)
			if s.Loaded && s.Project == "" && s.Kind == "" {
				description := "保存知识后，实际保存、读取与采用记录会显示在这里。"
				if a.overviewRaw != nil && *a.overviewRaw > 0 && a.overviewKnowledge != nil && *a.overviewKnowledge == 0 {
					description = "已有原始记录，尚未形成知识；可以从项目查看原始记录，整理可随时跳过。"
				}
				ui.Text(c, description).FontSize(design.Small).TextColor(design.Muted)
				quietButton(c, "新增知识").AlignSelf(ui.Start).OnClick(a.newMemory)
			}
		})
	}
	for _, event := range s.Items {
		a.homeActivityRow(c, event)
	}
	if s.Full {
		ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
			ui.Text(c, fmt.Sprintf("第 %d 页 · 每页 8 条", len(s.Back)+1)).FontSize(10).TextColor(design.Muted)
			if len(s.Back) > 0 {
				quietButton(c, "上一页动态").Disabled(s.Loading).OnClick(func() {
					s.BackRequested = true
					a.refreshHomeActivity(false)
				})
			}
			if s.Next != "" {
				quietButton(c, "下一页动态").Disabled(s.Loading).OnClick(func() { a.refreshHomeActivity(true) })
			}
		})
	}
}

func (a *App) homeActivityFilters(c *ui.Context) {
	s := &a.homeActivity
	projects := []string{"全部项目"}
	projectIDs := []string{""}
	for _, p := range a.projects {
		projects = append(projects, p.Name)
		projectIDs = append(projectIDs, p.ID)
	}
	if s.ProjectLabel == "" {
		s.ProjectLabel = "全部项目"
	}
	if s.KindLabel == "" {
		s.KindLabel = "全部类型"
	}
	kinds := []string{"全部类型", "保存", "编辑", "恢复", "删除", "读取", "检索返回", "明确采用"}
	kindIDs := []string{"", "save", "edit", "restore", "delete", "read", "return", "adopt"}
	ui.Row(c).Gap(8).Margin(0, 0, 12, 0).Children(func() {
		ui.Select(c.Key("home-activity-project"), &s.ProjectLabel, projects).Label("动态项目筛选").OnChange(func() {
			for i, label := range projects {
				if label == s.ProjectLabel {
					a.changeHomeActivity(true, projectIDs[i], s.Kind)
					break
				}
			}
		})
		ui.Select(c.Key("home-activity-kind"), &s.KindLabel, kinds).Label("动态类型筛选").OnChange(func() {
			for i, label := range kinds {
				if label == s.KindLabel {
					a.changeHomeActivity(true, s.Project, kindIDs[i])
					break
				}
			}
		})
	})
}

func homeActivityVerb(kind string) string {
	switch kind {
	case "save":
		return "保存了"
	case "edit":
		return "编辑了"
	case "restore":
		return "恢复了"
	case "delete":
		return "删除了"
	case "read":
		return "读取了"
	case "return":
		return "检索返回了"
	case "adopt":
		return "明确采用了"
	case "merge":
		return "合并了"
	case "archive":
		return "归档了"
	default:
		return "更新了"
	}
}

func (a *App) homeActivityRow(c *ui.Context, e records.HomeActivity) {
	if a.homeActivity.Full {
		a.homeActivityListRow(c, e)
		return
	}
	actor := sourceDisplayName(e.Agent)
	if actor == "" && e.Channel == "daily" {
		actor = "Agent"
	}
	kind := "知识"
	if e.Origin == "raw" {
		kind = "原始记录"
	}
	label := actor + " " + homeActivityVerb(e.Kind) + kind + " " + e.Title
	if actor == "" {
		label = homeActivityVerb(e.Kind) + kind + " " + e.Title
	}
	ui.Button(c.Key("home-event-"+e.ID), "").Label(label).Justify(ui.Start).MinHeight(42).Padding(5, 4).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Background(ui.Transparent).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon := icons["全部记忆"]
			if e.Kind == "read" || e.Kind == "return" || e.Kind == "adopt" {
				icon = icons["history"]
			}
			ui.Icon(c, icon).FontSize(12).TextColor(design.Muted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, label).FontSize(design.Small).SingleLine().MinWidth(0)
				scope := e.ScopeName
				if e.ScopeKind == "experience" {
					scope = "通用经验"
				} else if e.ScopeKind == "preference" {
					scope = "个人偏好"
				}
				ui.Text(c, fmt.Sprintf("%s · v%d", scope, e.Revision)).FontSize(10).TextColor(design.Muted).SingleLine()
			})
			ui.Text(c, homeTimestamp(time.UnixMilli(e.OccurredMS))).FontSize(10).TextColor(design.Muted).Shrink(0)
		})
	}).OnClick(func() {
		if a.homeActivity.Full {
			a.selectHomeActivity(e)
		} else {
			a.openHomeActivityCurrent(e)
		}
	})
}

func (a *App) openHomeActivityCurrent(e records.HomeActivity) {
	a.homeActivity.Return, a.homeActivity.ReturnFull = true, a.homeActivity.Full
	a.homeActivity.Review.Return = false
	dataset, scopeKind := e.CurrentDataset, e.CurrentScopeKind
	if dataset == "" {
		dataset, scopeKind = e.Dataset, e.ScopeKind
	}
	page, scope := "项目", dataset
	if scopeKind == "experience" || scopeKind == "preference" {
		page, scope = "通用知识", ""
	}
	a.navigate(page, scope)
	// An explicit record target takes precedence over the project's restored
	// entity/graph view. Keep ordinary project navigation restoration intact.
	if page == "项目" {
		a.browsing.View = "memories"
		a.browsing.Knowledge, a.browsing.Explorer = nil, nil
		a.browsing.Relation = ""
	}
	a.recordKind = "knowledge"
	if e.Origin == "raw" {
		a.recordKind = "raw"
	}
	a.activityDate = ""
	a.selectedID = e.Memory
	a.selectedItem = nil
	a.detailMode = "view"
	a.editing = false
	a.detailScroll.Y = 0
	// The sidebar opens current knowledge; the timeline retains the observed
	// version. No browse navigation is reported as an Agent adoption.
	a.message = fmt.Sprintf("动态记录 v%d；当前正文与历史以实际读取结果为准。", e.Revision)
	if a.onSelect != nil {
		a.onSelect(e.Memory)
	}
}
