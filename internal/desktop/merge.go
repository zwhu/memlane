package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type MergeUI struct {
	Client                                                              serviceipc.MergeClient
	Tasks                                                               []records.MergeSummary
	Next, Selected, ProjectLabel, Project, Notice, Error, CandidateNext string
	Task                                                                *records.MergeTask
	Result                                                              *MemoryItem
	Candidates                                                          []MemoryItem
	Chosen                                                              map[string]records.MergeTarget
	Start                                                               *records.MergeStart
	Reviews                                                             map[string]records.MergeReview
	Loading, Busy, New                                                  bool
	Sequence                                                            int64
	Scroll                                                              ui.ScrollState
}

func mergeStatus(state, review string) string {
	switch review {
	case "pending":
		return "待确认"
	case "applied":
		return "已应用"
	case "rejected":
		return "已拒绝"
	case "expired":
		return "已过期 · 请重新生成建议"
	case "cancelled":
		return "已取消"
	}
	switch state {
	case "queued":
		return "等待执行"
	case "running":
		return "正在整理"
	case "completed":
		return "建议已保存"
	case "failed":
		return "整理失败"
	case "cancelled":
		return "已取消"
	case "interrupted":
		return "执行中断 · 需重新整理"
	}
	return ""
}
func (a *App) beginMerge() {
	a.organizationMode = false
	s := &a.merges
	if s.Busy {
		return
	}
	project := a.scope
	a.navigate("整理记录", "")
	s.New = true
	s.Error = ""
	s.Notice = ""
	s.Task = nil
	s.Result = nil
	s.Selected = ""
	s.Chosen = map[string]records.MergeTarget{}
	s.Candidates = nil
	s.CandidateNext = ""
	s.Start = nil
	s.Project = ""
	s.ProjectLabel = ""
	for _, p := range a.projects {
		if p.ID == project {
			s.Project = p.ID
			s.ProjectLabel = a.scopeLabel(p.ID)
		}
	}
	s.Scroll.Y = 0
	a.refreshMerge(false)
}
func (a *App) mergeProjectChanged() {
	s := &a.merges
	s.Project = a.datasetForLabel(s.ProjectLabel)
	s.Candidates = nil
	s.CandidateNext = ""
	s.Chosen = map[string]records.MergeTarget{}
	s.Start = nil
	s.Error = ""
	s.Scroll.Y = 0
	a.refreshMerge(false)
}
func (a *App) refreshMerge(more bool) {
	s := &a.merges
	if a.libraryContext == nil || a.libraryContext.Err() != nil || s.Loading || a.visualFixture {
		return
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	selected, project, isNew := s.Selected, s.Project, s.New
	q := records.MergeListQuery{Limit: 30}
	if more {
		q.Before = s.Next
	}
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer stop()
		page, err := s.Client.List(ctx, q)
		var task records.MergeTask
		var result records.Version
		var candidates records.Page
		if err == nil && selected != "" && !isNew {
			task, err = s.Client.Get(ctx, selected)
		}
		if err == nil && task.Receipt != nil {
			result, err = a.library.Get(ctx, task.Receipt.Memory)
		}
		if err == nil && isNew && project != "" {
			candidates, err = a.library.List(ctx, records.ListQuery{Datasets: []string{project}, Kind: "knowledge", Limit: 100})
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Loading = false
			if s.Sequence != seq {
				a.refreshMerge(false)
				return
			}
			if err != nil {
				s.Error = "整理记录读取失败；已加载内容保留，请重试。"
				return
			}
			s.Error = ""
			if more {
				s.Tasks = append(s.Tasks, page.Items...)
			} else {
				if len(s.Tasks) <= 30 {
					s.Tasks = page.Items
				} else {
					loaded := map[string]bool{}
					tasks := append([]records.MergeSummary{}, page.Items...)
					for _, v := range tasks {
						loaded[v.ID] = true
					}
					for _, v := range s.Tasks {
						if !loaded[v.ID] {
							tasks = append(tasks, v)
						}
					}
					s.Tasks = tasks
				}
			}
			if more || len(s.Tasks) <= 30 {
				s.Next = page.Next
			}
			if selected == s.Selected && isNew == s.New && project == s.Project {
				if selected != "" && !isNew {
					s.Task = &task
					for i := range s.Tasks {
						if s.Tasks[i].ID == task.ID {
							s.Tasks[i].State = task.State
							s.Tasks[i].ReviewState = task.ReviewState
						}
					}
					s.Result = nil
					if result.Memory != "" {
						r := displayVersion(result)
						s.Result = &r
					}
				}
				if isNew {
					if len(s.Candidates) <= 100 {
						s.Candidates = displayVersions(candidates.Items)
						s.CandidateNext = candidates.NextCursor
					}
				}
			}
		})
	}()
}
func (a *App) selectMerge(id string) {
	a.organizationMode = false
	s := &a.merges
	if s.Busy {
		return
	}
	a.navigate("整理记录", "")
	s.Sequence++
	s.Selected = id
	s.New = false
	s.Task = nil
	s.Result = nil
	s.Notice = ""
	s.Scroll.Y = 0
	a.refreshMerge(false)
}
func (a *App) startMerge() {
	s := &a.merges
	if s.Busy || a.libraryContext == nil || s.Project == "" || len(s.Chosen) != 2 {
		return
	}
	if s.Start == nil {
		d := records.MergeStart{OperationID: uuid.NewString(), Project: s.Project}
		// Preserve user-visible order rather than map iteration order.
		for _, v := range s.Candidates {
			if target, ok := s.Chosen[v.ID]; ok {
				d.Inputs = append(d.Inputs, target)
			}
		}
		if len(d.Inputs) != 2 {
			s.Error = "所选来源已变化，请重新选择。"
			return
		}
		s.Start = &d
	}
	d := *s.Start
	a.mergeAction(func(ctx context.Context) (records.MergeTask, error) { return s.Client.Start(ctx, d) }, true)
}
func (a *App) reviewMerge(confirm bool) {
	s := &a.merges
	if s.Busy || s.Task == nil || s.Task.Proposal == nil || s.Task.ReviewState != "pending" {
		return
	}
	t := s.Task
	key := t.ID + fmt.Sprint(confirm)
	r, ok := s.Reviews[key]
	if !ok {
		r = records.MergeReview{Task: t.ID, Proposal: t.Proposal.ID, Digest: t.ResultDigest, Receipt: uuid.NewString()}
		s.Reviews[key] = r
	}
	a.mergeAction(func(ctx context.Context) (records.MergeTask, error) {
		if confirm {
			return s.Client.Confirm(ctx, r)
		}
		return s.Client.Reject(ctx, r)
	}, false)
}
func (a *App) cancelMerge() {
	s := &a.merges
	if s.Task == nil || s.Busy {
		return
	}
	id := s.Task.ID
	a.mergeAction(func(ctx context.Context) (records.MergeTask, error) { return s.Client.Cancel(ctx, id) }, false)
}
func (a *App) mergeAction(fn func(context.Context) (records.MergeTask, error), start bool) {
	s := &a.merges
	if s.Busy || a.libraryContext == nil {
		return
	}
	s.Busy = true
	s.Error = ""
	s.Notice = "正在提交…"
	s.Sequence++
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer stop()
		t, err := fn(ctx)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Notice = ""
				s.Error = "操作未成功：" + err.Error() + "。请核对任务状态后重试。"
				a.refreshMerge(false)
				return
			}
			s.Notice = "已持久保存，任务与索引状态分别更新。"
			if start {
				s.New = false
				s.Selected = t.ID
				s.Start = nil
				s.Chosen = map[string]records.MergeTarget{}
			}
			if s.Selected == t.ID {
				s.Task = &t
			}
			s.Sequence++
			a.refreshMerge(false)
			a.refresh(false)
		})
	}()
}
func (a *App) mergeList(c *ui.Context) {
	s := &a.merges
	if len(s.Tasks) == 0 {
		a.empty(c, "暂无整理记录", "选择同一项目内两条有效知识开始整理。")
		return
	}
	for _, t := range s.Tasks {
		title := t.Title
		if title == "" {
			title = "两条知识整理"
		}
		b := ui.Button(c.Key("merge-task-"+t.ID), "").Label(title).Justify(ui.Start).BorderWidth(0).Padding(12).Gap(7).Children(func() {
			ui.Column(c).MinWidth(0).Gap(7).Children(func() {
				ui.Text(c, title).Bold()
				ui.Text(c, a.scopeLabel(t.Project)+" · "+mergeStatus(t.State, t.ReviewState)).FontSize(design.Small).TextColor(design.Muted)
				created, _ := time.Parse("2006-01-02 15:04:05", t.CreatedAt)
				ui.Text(c, created.Local().Format("2006-01-02 15:04")).FontSize(design.Small).TextColor(design.Muted)
			})
		})
		if t.ID == s.Selected && !s.New {
			b.Background(design.ListSelected)
		} else {
			b.Background(ui.Transparent)
		}
		b.OnClick(func() { a.selectMerge(t.ID) })
	}
	if s.Next != "" {
		quietButton(c, "更多整理记录").Disabled(s.Loading).OnClick(func() { a.refreshMerge(true) })
	}
}
func (a *App) mergeDetail(c *ui.Context) {
	s := &a.merges
	ui.Scroll(c.Key("merge-detail-scroll")).Grow(1).TrackScroll(&s.Scroll).Padding(24).Gap(16).Children(func() {
		if s.Error != "" {
			ui.Text(c, s.Error).TextColor(design.Muted)
			quietButton(c, "重试整理记录").OnClick(func() { a.refreshMerge(false) })
		}
		if s.Notice != "" {
			ui.Text(c, s.Notice).FontSize(design.Small).TextColor(design.Muted)
		}
		if s.New {
			a.mergeChooser(c)
			return
		}
		if s.Task == nil {
			a.empty(c, "查看整理记录", "选择一个任务，或开始新的整理。")
			return
		}
		t := s.Task
		ui.Text(c, mergeStatus(t.State, t.ReviewState)).FontSize(design.Title).Bold()
		meta(c, "任务", t.ID)
		meta(c, "项目", a.scopeLabel(t.Project))
		meta(c, "执行", fmt.Sprintf("traex · attempt %d", t.Snapshot.Attempt))
		if t.Error != "" {
			meta(c, "错误", t.Error)
		}
		if t.ReviewState == "pending" {
			ui.Text(c, "确认前已有知识保持不变。确认会新建合并知识，并归档两条来源；历史和出处保留。").LineHeight(1.7)
		}
		if t.ReviewState == "expired" {
			ui.Text(c, "该建议已失效，整项不能应用。请核对当前知识后重新生成建议。").LineHeight(1.7)
		}
		if t.State == "interrupted" {
			ui.Text(c, "服务中断，旧执行结果不会应用。重新整理需再次选择当前版本。").LineHeight(1.7)
		}
		if t.Proposal != nil {
			ui.Divider(c)
			ui.Text(c, "建议正文").Bold()
			ui.Text(c, t.Proposal.Title).Bold()
			ui.Text(c, t.Proposal.Text).Selectable().LineHeight(1.7)
		}
		ui.Divider(c)
		ui.Text(c, "冻结来源与具体版本").Bold()
		for i, v := range t.Snapshot.Inputs {
			ui.Column(c.Key("merge-source-"+v.Memory)).Padding(14).Border(1, design.Border).Radius(5).Gap(10).Children(func() {
				ui.Text(c, fmt.Sprintf("%s · v%d", v.Title, v.Revision)).Bold()
				ui.Text(c, v.Text).Selectable().LineHeight(1.7)
				meta(c, "来源", v.Source)
				meta(c, "捕获", v.CapturedAt)
				if v.SourceDetail != nil {
					if v.SourceDetail.Fragment != "" {
						ui.Text(c, v.SourceDetail.Fragment).Selectable().FontSize(design.Small)
					}
					for _, ref := range v.SourceDetail.References {
						ui.Text(c, ref).Selectable().FontSize(design.Small)
					}
				}
				if t.Proposal != nil && i < len(t.Proposal.Sources) {
					ui.Text(c, "引用依据").FontSize(design.Small).Bold()
					ui.Text(c, t.Proposal.Sources[i].Quote).Selectable().LineHeight(1.6)
					ui.Text(c, "相较此来源的正文改动").FontSize(design.Small).Bold()
					ui.Text(c, lineDiff(v.Text, t.Proposal.Text)).Selectable().LineHeight(1.6).FontSize(design.Small).Padding(10).Background(design.Surface)
				}
				quietButton(c, "查看来源当前版本").Key("merge-open-source-" + v.Memory).OnClick(func() { a.openMergeMemory(v.Memory, t.Project) })
				quietButton(c, fmt.Sprintf("查看来源 v%d 历史", v.Revision)).Key("merge-history-" + v.Memory).OnClick(func() { a.openMergeMemory(v.Memory, t.Project); a.detailMode = "history" })
			})
		}
		if t.Receipt != nil {
			ui.Divider(c)
			ui.Text(c, "应用结果").Bold()
			meta(c, "提交", fmt.Sprintf("合并 v%d · %s", t.Receipt.Revision, t.Receipt.Memory))
			if s.Result != nil {
				label := a.availableIndexLabel(s.Result.IndexState)
				if s.Result.State == "archived" {
					label = "已归档 · 不参与检索"
				} else if s.Result.State == "deleted" {
					label = "已删除 · 不参与检索"
				}
				meta(c, "当前", fmt.Sprintf("v%d · %s", s.Result.Revision, label))
			}
			quietButton(c, "查看合并知识").Key("merge-open-result").OnClick(func() { a.openMergeMemory(t.Receipt.Memory, t.Project) })
			ui.Text(c, "来源已归档。恢复单条来源会生成新版本，并保留本次合并。").FontSize(design.Small).LineHeight(1.6).TextColor(design.Muted)
		}
	})
	// Review remains visible while scrolling through long sources.
	ui.Row(c).Shrink(0).Padding(14, 24, 20, 24).Gap(8).Children(func() {
		t := s.Task
		if !s.New && t != nil {
			if t.ReviewState == "pending" {
				actionButton(c, "确认合并", nil).Key("merge-confirm").Disabled(s.Busy || s.Loading || s.Error != "").OnClick(func() { a.reviewMerge(true) })
				actionButton(c, "拒绝建议", nil).Key("merge-reject").Disabled(s.Busy || s.Loading || s.Error != "").OnClick(func() { a.reviewMerge(false) })
			}
			if t.State == "queued" || t.State == "running" || t.ReviewState == "pending" {
				actionButton(c, "取消整理", nil).Key("merge-cancel").Disabled(s.Busy).OnClick(a.cancelMerge)
			}
			if t.State == "failed" || t.State == "interrupted" || t.State == "cancelled" || t.ReviewState == "expired" {
				quietButton(c, "重新选择并整理").Disabled(s.Busy).OnClick(a.beginMerge)
			}
		}
	})
}
func (a *App) mergeChooser(c *ui.Context) {
	s := &a.merges
	ui.Text(c, "立即整理").FontSize(design.Title).Bold()
	ui.Text(c, "选择同一项目内两条有效知识。traex 只生成建议，确认后才合并；所选正文会交给该 Agent 当前模型处理。").LineHeight(1.7)
	options := []string{}
	for _, p := range a.projects {
		options = append(options, a.scopeLabel(p.ID))
	}
	ui.Select(c.Key("merge-project"), &s.ProjectLabel, options).Label("整理项目").Disabled(s.Busy).OnChange(a.mergeProjectChanged)
	meta(c, "执行者", "traex · 使用已有认证与模型选择")
	if s.Project == "" {
		ui.Text(c, "请选择已登记项目。").TextColor(design.Muted)
		return
	}
	for _, v := range s.Candidates {
		if v.State != "active" {
			continue
		}
		_, chosen := s.Chosen[v.ID]
		label := "选择 " + v.Title
		if chosen {
			label = "已选 " + v.Title
		}
		ui.Button(c.Key("merge-choose-"+v.ID), label).Justify(ui.Start).Disabled(s.Busy || (!chosen && len(s.Chosen) >= 2)).OnClick(func() {
			if chosen {
				delete(s.Chosen, v.ID)
			} else {
				s.Chosen[v.ID] = records.MergeTarget{Memory: v.ID, Revision: v.Revision}
			}
			s.Start = nil
		})
		ui.Text(c, fmt.Sprintf("v%d · %s", v.Revision, preview(v.Text))).FontSize(design.Small).TextColor(design.Muted)
	}
	if s.CandidateNext != "" {
		quietButton(c, "更多项目知识").Disabled(s.Loading).OnClick(a.moreMergeCandidates)
	}
	ui.Text(c, fmt.Sprintf("已选 %d / 2", len(s.Chosen))).FontSize(design.Small)
	actionButton(c, "开始整理", nil).Key("merge-start").Disabled(s.Busy || len(s.Chosen) != 2 || s.Error != "").OnClick(a.startMerge)
}
func (a *App) openMergeMemory(id, project string) {
	page, scope := "项目", project
	for _, registered := range a.scopes {
		if registered.ID == project && registered.Kind != "project" {
			page, scope = "全局记忆", ""
			break
		}
	}
	a.navigate(page, scope)
	a.selectedID = id
	a.selectedItem = nil
	a.history = nil
	a.detailMode = "view"
	a.detailScroll.Y = 0
	a.refresh(false)
}
func (a *App) mergeHome(c *ui.Context) {
	pending := 0
	for _, t := range a.merges.Tasks {
		if t.ReviewState == "pending" {
			pending++
		}
	}
	ui.Row(c).Children(func() {
		ui.Text(c, "最近记录 · 待确认").Bold()
		ui.Text(c, fmt.Sprint(pending)).FontSize(10).Padding(2, 6).Background(design.Surface).TextColor(design.Muted)
	})
	shown := 0
	for _, t := range a.merges.Tasks {
		if t.ReviewState != "pending" || shown >= 3 {
			continue
		}
		shown++
		ui.Button(c.Key("merge-home-"+t.ID), "").Label(t.Title).Padding(16).Border(1, design.Border).Radius(7).Background(ui.Hex("#fafbfc")).Children(func() {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, t.Title).Bold()
				ui.Text(c, a.scopeLabel(t.Project)+" · 确认前知识保持不变").FontSize(design.Small).TextColor(design.Muted)
			})
		}).OnClick(func() { a.selectMerge(t.ID) })
	}
	if shown == 0 {
		ui.Column(c).Padding(16).Border(1, design.Border).Radius(7).Background(ui.Hex("#fafbfc")).Gap(10).Children(func() {
			ui.Text(c, "暂无待确认建议").Bold()
			ui.Text(c, "选择项目知识开始整理；已有知识仍可手动编辑。").FontSize(design.Small).LineHeight(1.6).TextColor(design.Muted)
		})
	}
	quietButton(c, "查看全部整理记录").OnClick(func() { a.navigate("整理记录", "") })
}

// Avoid claiming that a successfully saved archive is current searchable knowledge.
func archivedLabel(state string) string {
	if strings.TrimSpace(state) == "archived" {
		return "已归档 · 保留来源与历史"
	}
	return ""
}

func (a *App) moreMergeCandidates() {
	s := &a.merges
	if s.Loading || s.Busy || s.CandidateNext == "" || a.libraryContext == nil {
		return
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	project := s.Project
	q := records.ListQuery{Datasets: []string{project}, Kind: "knowledge", Limit: 100, Cursor: s.CandidateNext}
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer stop()
		page, err := a.library.List(ctx, q)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Loading = false
			if seq != s.Sequence || project != s.Project {
				a.refreshMerge(false)
				return
			}
			if err != nil {
				s.Error = "知识读取失败，请重试。"
				return
			}
			s.Candidates = append(s.Candidates, displayVersions(page.Items)...)
			s.CandidateNext = page.NextCursor
		})
	}()
}
