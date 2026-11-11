package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type homeReviewUI struct {
	DecisionStamp                                           string
	Return, ReturnFull                                      bool
	Scroll                                                  ui.ScrollState
	Selected                                                *records.HomeReview
	Cursor                                                  string
	Back                                                    []string
	BackRequested                                           bool
	Full, Loaded, Loading                                   bool
	Sequence                                                uint64
	Error, Project, Action, ProjectLabel, ActionLabel, Next string
	Items                                                   []records.HomeReview
	Total                                                   int
	CheckedAt                                               time.Time
}

func (a *App) refreshHomeReviews(more bool) {
	s := &a.homeActivity.Review
	if a.libraryContext == nil || a.dispatch == nil || s.Loading {
		return
	}
	limit := 3
	if s.Full {
		limit = 8
	}
	q := records.HomeReviewQuery{Limit: limit, Project: s.Project, Action: s.Action, Before: s.Cursor}
	backward := s.BackRequested
	s.BackRequested = false
	if backward && len(s.Back) > 0 {
		q.Before = s.Back[len(s.Back)-1]
	}
	if more {
		q.Before = s.Next
		if q.Before == "" {
			return
		}
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	owner, library := a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(owner, 5*time.Second)
		defer cancel()
		page, err := library.HomeReviews(ctx, q)
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
				s.Error = "待确认读取失败；请刷新队列。"
				return
			}
			s.Loaded = true
			s.Error = ""
			s.Next = page.Next
			s.Total = page.Total
			if more {
				s.Back = append(s.Back, s.Cursor)
			} else if backward && len(s.Back) > 0 {
				s.Back = s.Back[:len(s.Back)-1]
			}
			s.Cursor = q.Before
			s.Items = page.Items
			if s.Full && len(s.Items) > 0 {
				found := false
				for _, item := range s.Items {
					if s.Selected != nil && s.Selected.ID == item.ID {
						found = true
						break
					}
				}
				if !found && (s.DecisionStamp == "" || more || backward) {
					a.selectHomeReview(s.Items[0])
				}
			}
		})
	}()
}

func (a *App) changeHomeReviews(full bool, project, action string) {
	s := &a.homeActivity.Review
	wasFull := s.Full
	s.Full = full
	if project == "" {
		s.ProjectLabel = "全部项目"
	}
	if action == "" {
		s.ActionLabel = "全部建议"
	}
	s.Project = project
	s.Action = action
	s.Sequence++
	s.Loading = false
	s.Loaded = false
	s.Items = nil
	s.Next = ""
	s.Cursor = ""
	s.Back = nil
	s.BackRequested = false
	s.Error = ""
	s.CheckedAt = time.Time{}
	s.Selected = nil
	s.DecisionStamp = ""

	if full && a.page != "整理记录" {
		a.navigate("整理记录", "")
	} else if !full && wasFull {
		a.navigate("总览", "")
	}
	a.refreshHomeReviews(false)
}

func (a *App) homeReviewPanel(c *ui.Context) {
	s := &a.homeActivity.Review
	if s.Full && s.Selected != nil && !s.Loading {
		state := ""
		item := s.Selected
		if item.Owner == "merge" && a.merges.Task != nil && a.merges.Task.ID == item.Task {
			state = a.merges.Task.ReviewState
		}
		if item.Owner == "organization" && a.organization.Task != nil && a.organization.Task.ID == item.Task {
			for _, entry := range a.organization.Task.Entries {
				if entry.ID == item.ID {
					state = entry.State
					break
				}
			}
		}
		stamp := item.ID + ":" + state
		if state != "" && state != "pending" && state != "review_pending" && s.DecisionStamp != stamp {
			s.DecisionStamp = stamp
			a.refreshHomeReviews(false)
		}
	}
	if !s.Loading && (s.CheckedAt.IsZero() || !s.Full && time.Since(s.CheckedAt) > 5*time.Second) {
		a.refreshHomeReviews(false)
	}
	c.After(5 * time.Second)
	ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
		title := "待确认"
		if s.Full {
			title = "全部待确认"
		}
		ui.Text(c, title).Bold()
		if s.Loaded {
			ui.Text(c, fmt.Sprint(s.Total)).FontSize(10).Padding(2, 6).Background(design.Surface).TextColor(design.Muted)
		}
		ui.Spacer(c)
		if s.Full {
			quietButton(c, "刷新建议").Disabled(s.Loading).OnClick(func() { a.changeHomeReviews(s.Full, s.Project, s.Action) })
		}
		if s.Full {
			quietButton(c, "返回首页动态").OnClick(func() { a.changeHomeReviews(false, "", "") })
		} else {
			quietButton(c, "查看建议").OnClick(func() { a.changeHomeReviews(true, "", "") })
		}
	})
	if s.Full {
		a.homeReviewFilters(c)
	}
	if s.Error != "" {
		ui.Text(c, s.Error).FontSize(design.Small).TextColor(design.Muted)
	}
	if len(s.Items) == 0 {
		label := "暂无待确认建议"
		if s.Loading && !s.Loaded {
			label = "正在读取建议"
		} else if !s.Loaded {
			label = "待确认状态未知"
		}
		ui.Text(c, label).FontSize(design.Small).TextColor(design.Muted).Padding(16, 12)
	}
	for _, item := range s.Items {
		title := item.Title
		if title == "" {
			title = organizationActionLabel(item.Action)
		}
		button := ui.Button(c.Key("home-review-"+item.ID), "").Label(title).Padding(12).Justify(ui.Start).Border(1, design.Border).Radius(7).Background(ui.Hex("#fafbfc")).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Text(c, title).FontWeight(600).SingleLine().MinWidth(0)
					ui.Text(c, item.ScopeName+" · "+organizationActionLabel(item.Action)).FontSize(10).TextColor(design.Muted).SingleLine()
				})
				ui.Text(c, "›").TextColor(design.Muted)
			})
		})
		if s.Full && s.Selected != nil && s.Selected.ID == item.ID {
			button.Background(design.ListSelected)
		}
		button.OnClick(func() {
			if s.Full {
				a.selectHomeReview(item)
			} else {
				a.openHomeReview(item)
			}
		})
	}
	if s.Full {
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, fmt.Sprintf("第 %d 页 · 每页 8 项", len(s.Back)+1)).FontSize(10).TextColor(design.Muted)
			if len(s.Back) > 0 {
				quietButton(c, "上一页建议").Disabled(s.Loading).OnClick(func() { s.BackRequested = true; a.refreshHomeReviews(false) })
			}
			if s.Next != "" {
				quietButton(c, "下一页建议").Disabled(s.Loading).OnClick(func() { a.refreshHomeReviews(true) })
			}
		})
	}
}

func (a *App) homeReviewFilters(c *ui.Context) {
	s := &a.homeActivity.Review
	projects := []string{"全部项目"}
	ids := []string{""}
	for _, p := range a.projects {
		projects = append(projects, p.Name)
		ids = append(ids, p.ID)
	}
	if s.ProjectLabel == "" {
		s.ProjectLabel = "全部项目"
	}
	if s.ActionLabel == "" {
		s.ActionLabel = "全部建议"
	}
	actions := []string{"全部建议", "合并", "覆盖", "删除", "移动", "重复", "冲突", "通用经验"}
	values := []string{"", "merge", "overwrite", "delete", "move", "duplicate", "conflict", "global_experience"}
	ui.Row(c).Gap(8).Children(func() {
		ui.Select(c.Key("home-review-project"), &s.ProjectLabel, projects).Label("建议项目筛选").OnChange(func() {
			for i, label := range projects {
				if label == s.ProjectLabel {
					a.changeHomeReviews(true, ids[i], s.Action)
					break
				}
			}
		})
		ui.Select(c.Key("home-review-action"), &s.ActionLabel, actions).Label("建议类型筛选").OnChange(func() {
			for i, label := range actions {
				if label == s.ActionLabel {
					a.changeHomeReviews(true, s.Project, values[i])
					break
				}
			}
		})
	})
}

func (a *App) openHomeReview(item records.HomeReview) {
	a.homeActivity.Review.Return, a.homeActivity.Review.ReturnFull = true, a.homeActivity.Review.Full
	a.homeActivity.Return = false
	if item.Owner == "merge" {
		a.selectMerge(item.Task)
	} else {
		a.selectOrganizationAt(item.Task, item.ID)
	}
}
