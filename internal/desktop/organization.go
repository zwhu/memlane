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

type OrganizationUI struct {
	ReviewMode                                                         bool
	Destinations                                                       map[string]bool
	Decisions                                                          map[string]records.OrganizationReview
	Client                                                             serviceipc.OrganizationClient
	Tasks                                                              []records.OrganizationSummary
	Selected, Next, Project, ProjectLabel, Kind, Cursor, Error, Notice string
	Task                                                               *records.OrganizationTask
	Candidates                                                         []MemoryItem
	Chosen                                                             map[string]MemoryItem
	Order                                                              []string
	Start                                                              *records.OrganizationStart
	Resume                                                             *records.OrganizationResume
	Results                                                            map[string]MemoryItem
	New, Busy, Loading                                                 bool
	Sequence                                                           int64
	Scroll                                                             ui.ScrollState
}

func organizationStatus(state string) string {
	switch state {
	case "queued":
		return "等待整理"
	case "running":
		return "正在生成摘要与标签"
	case "applying":
		return "正在保存整理结果"
	case "completed":
		return "整理已完成"
	case "partial_failed":
		return "部分未应用"
	case "failed":
		return "整理失败"
	case "cancelled":
		return "整理已取消"
	case "interrupted":
		return "整理已中断"
	}
	return state
}
func (a *App) beginOrganization() {
	s := &a.organization
	if s.Busy {
		return
	}
	project := a.scope
	a.organizationMode = true
	a.navigate("整理记录", "")
	s.New = true
	s.Selected = ""
	s.Task = nil
	s.Error = ""
	s.Notice = ""
	s.Candidates = nil
	s.Cursor = ""
	s.Chosen = map[string]MemoryItem{}
	s.Order = nil
	s.Start = nil
	s.Resume = nil
	s.Destinations = map[string]bool{}
	s.Decisions = map[string]records.OrganizationReview{}
	s.Kind = "knowledge"
	s.Project = ""
	s.ProjectLabel = ""
	s.Scroll.Y = 0
	s.Sequence++
	for _, scope := range a.scopes {
		if scope.Kind == "project" && scope.ID == project {
			s.Project = scope.ID
			s.ProjectLabel = a.scopeLabel(scope.ID)
		}
	}
	a.refreshOrganization(false, false)
}
func (a *App) beginOrganizationOrMerge() {
	if a.organizationMode {
		a.beginOrganization()
	} else {
		a.beginMerge()
	}
}
func (a *App) selectOrganization(id string) {
	s := &a.organization
	if s.Busy {
		return
	}
	a.organizationMode = true
	a.navigate("整理记录", "")
	s.Selected = id
	s.Task = nil
	s.New = false
	s.Notice = ""
	s.Resume = nil
	s.Scroll.Y = 0
	s.Sequence++
	a.refreshOrganization(false, false)
}
func (a *App) refreshOrganization(more, candidatesMore bool) {
	s := &a.organization
	if a.libraryContext == nil || a.libraryContext.Err() != nil || s.Loading || a.visualFixture {
		return
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	selected, project, isNew, kind := s.Selected, s.Project, s.New, s.Kind
	if kind == "" {
		kind = "knowledge"
	}
	q := records.MergeListQuery{Limit: 30}
	if more {
		q.Before = s.Next
	}
	query := records.ListQuery{Datasets: []string{project}, Limit: 100, Kind: kind}
	if candidatesMore {
		query.Cursor = s.Cursor
	}
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer stop()
		page, err := s.Client.List(ctx, q)
		var task records.OrganizationTask
		var candidates records.Page
		results := map[string]MemoryItem{}
		if err == nil && selected != "" && !isNew {
			task, err = s.Client.Get(ctx, selected)
		}
		if err == nil && isNew && project != "" {
			candidates, err = a.library.List(ctx, query)
		}
		if err == nil {
			for _, entry := range task.Entries {
				if entry.Receipt != nil && entry.Receipt.Memory != "" {
					v, e := a.library.Get(ctx, entry.Receipt.Memory)
					if e != nil {
						err = e
						break
					}
					results[v.Memory] = displayVersion(v)
				}
			}
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Loading = false
			if s.Sequence != seq {
				a.refreshOrganization(false, false)
				return
			}
			if err != nil {
				s.Error = "整理读取失败，已加载内容保留，请重试。"
				return
			}
			s.Error = ""
			if more {
				s.Tasks = append(s.Tasks, page.Items...)
			} else if len(s.Tasks) <= 30 {
				s.Tasks = page.Items
			} else {
				seen := map[string]bool{}
				items := append([]records.OrganizationSummary{}, page.Items...)
				for _, v := range items {
					seen[v.ID] = true
				}
				for _, v := range s.Tasks {
					if !seen[v.ID] {
						items = append(items, v)
					}
				}
				s.Tasks = items
			}
			if more || len(s.Tasks) <= 30 {
				s.Next = page.Next
			}
			if s.Selected == selected && s.New == isNew && s.Project == project && s.Kind == kind {
				if !isNew && selected != "" {
					s.Task = &task
					s.Results = results
					for i := range s.Tasks {
						if s.Tasks[i].ID == task.ID {
							s.Tasks[i].State = task.State
						}
					}
				}
				if isNew {
					if candidatesMore {
						s.Candidates = append(s.Candidates, displayVersions(candidates.Items)...)
						s.Cursor = candidates.NextCursor
					} else if len(s.Candidates) <= 100 {
						s.Candidates = displayVersions(candidates.Items)
						s.Cursor = candidates.NextCursor
					}
				}
			}
		})
	}()
}
func (a *App) startOrganization() {
	s := &a.organization
	if s.Busy || len(s.Chosen) == 0 || s.Project == "" || a.libraryContext == nil {
		return
	}
	if s.Start == nil {
		d := records.OrganizationStart{OperationID: uuid.NewString(), Project: s.Project, Review: s.ReviewMode}
		if s.ReviewMode {
			for _, scope := range a.scopes {
				if s.Destinations[scope.ID] {
					d.Destinations = append(d.Destinations, scope.ID)
				}
			}
		}
		for _, id := range s.Order {
			v := s.Chosen[id]
			d.Inputs = append(d.Inputs, records.MergeTarget{Memory: id, Revision: v.Revision})
		}
		s.Start = &d
	}
	d := *s.Start
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return s.Client.Start(ctx, d) }, true)
}
func (a *App) organizationAction(fn func(context.Context) (records.OrganizationTask, error), start bool) {
	s := &a.organization
	if s.Busy || a.libraryContext == nil {
		return
	}
	s.Busy = true
	s.Notice = "正在提交…"
	s.Error = ""
	s.Sequence++
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer stop()
		task, err := fn(ctx)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Notice = ""
				s.Error = "操作未成功，请核对任务后重试：" + err.Error()
				a.refreshOrganization(false, false)
				return
			}
			if start {
				s.Selected = task.ID
				s.New = false
				s.Start = nil
			}
			if s.Selected == task.ID {
				s.Task = &task
			}
			if s.Resume != nil && s.Resume.Task == task.ID {
				s.Resume = nil
			}
			s.Notice = "任务已持久保存，产出与索引状态分别更新。"
			s.Sequence++
			a.refreshOrganization(false, false)
			a.refresh(false)
		})
	}()
}
func (a *App) cancelOrganization() {
	s := &a.organization
	if s.Task == nil {
		return
	}
	id := s.Task.ID
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return s.Client.Cancel(ctx, id) }, false)
}
func (a *App) resumeOrganization() {
	s := &a.organization
	if s.Task == nil {
		return
	}
	if s.Resume == nil {
		s.Resume = &records.OrganizationResume{Task: s.Task.ID, OperationID: uuid.NewString(), ExpectedAttempt: s.Task.Attempt}
	}
	d := *s.Resume
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return s.Client.Resume(ctx, d) }, false)
}
func (a *App) organizationList(c *ui.Context) {
	s := &a.organization
	if len(s.Tasks) == 0 {
		a.empty(c, "暂无摘要与标签记录", "选择项目材料开始整理，所有产出保留出处。")
		return
	}
	for _, t := range s.Tasks {
		title := t.Title
		if title == "" {
			title = "项目材料整理"
		}
		b := ui.Button(c.Key("organization-task-"+t.ID), "").Label(title).BorderWidth(0).Padding(12).Justify(ui.Start).Children(func() {
			ui.Column(c).Gap(7).Children(func() {
				ui.Text(c, title).Bold()
				ui.Text(c, a.scopeLabel(t.Project)+" · "+func() string {
					if t.State == "completed" {
						return "执行已完成"
					}
					return organizationStatus(t.State)
				}()).FontSize(design.Small).TextColor(design.Muted)
				created, _ := time.Parse("2006-01-02 15:04:05", t.CreatedAt)
				ui.Text(c, created.Local().Format("2006-01-02 15:04")).FontSize(design.Small).TextColor(design.Muted)
			})
		})
		if t.ID == s.Selected && !s.New {
			b.Background(design.ListSelected)
		} else {
			b.Background(ui.Transparent)
		}
		b.OnClick(func() { a.selectOrganization(t.ID) })
	}
	if s.Next != "" {
		quietButton(c, "更多摘要记录").Disabled(s.Loading).OnClick(func() { a.refreshOrganization(true, false) })
	}
}
func (a *App) organizationChooser(c *ui.Context) {
	s := &a.organization
	ui.Text(c, "项目材料整理").FontSize(design.Title).Bold()
	ui.Checkbox(c.Key("organization-review-mode"), &s.ReviewMode, "同时提出修改建议，逐项确认后生效").Disabled(s.Busy).OnChange(func() { s.Start = nil; s.Destinations = map[string]bool{} })
	if s.ReviewMode {
		ui.Text(c, "可建议移入的项目（可不选；授权不读取其他项目知识）").FontSize(design.Small)
		for _, scope := range a.scopes {
			if scope.Kind == "project" && scope.ID != s.Project {
				allowed := s.Destinations[scope.ID]
				ui.Checkbox(c.Key("organization-destination-"+scope.ID), &allowed, a.scopeLabel(scope.ID)).Disabled(s.Busy).OnChange(func() {
					if s.Destinations == nil {
						s.Destinations = map[string]bool{}
					}
					s.Destinations[scope.ID] = allowed
					s.Start = nil
				})
				ui.Text(c, scope.Path).FontSize(design.Small).TextColor(design.Muted)
			}
		}
	}
	ui.Text(c, "选择一个项目内的知识或原始材料。新增摘要和标签可自动保存；已有正文、范围和历史保留。材料将交给 traex 当前模型处理。").LineHeight(1.7)
	options := []string{}
	for _, v := range a.scopes {
		if v.Kind == "project" {
			options = append(options, a.scopeLabel(v.ID))
		}
	}
	ui.Select(c.Key("organization-project"), &s.ProjectLabel, options).Label("整理项目").Disabled(s.Busy).OnChange(func() {
		s.Project = a.datasetForLabel(s.ProjectLabel)
		s.Chosen = map[string]MemoryItem{}
		s.Destinations = map[string]bool{}
		s.Order = nil
		s.Candidates = nil
		s.Cursor = ""
		s.Start = nil
		s.Sequence++
		a.refreshOrganization(false, false)
	})
	if s.Project == "" {
		ui.Text(c, "请选择已登记项目。").TextColor(design.Muted)
		return
	}
	ui.Row(c).Gap(8).Children(func() {
		for _, kind := range []string{"knowledge", "raw"} {
			label := "知识"
			if kind == "raw" {
				label = "原始记录"
			}
			ui.Button(c, label).Key("organization-kind-" + kind).Disabled(s.Busy).OnClick(func() {
				s.Kind = kind
				s.Candidates = nil
				s.Cursor = ""
				s.Sequence++
				a.refreshOrganization(false, false)
			})
		}
	})
	for _, v := range s.Candidates {
		_, selected := s.Chosen[v.ID]
		label := "选择 " + v.Title
		if selected {
			label = "已选 " + v.Title
		}
		ui.Button(c.Key("organization-choose-"+v.ID), label).Justify(ui.Start).Disabled(s.Busy || (!selected && len(s.Chosen) >= 16)).OnClick(func() {
			if selected {
				delete(s.Chosen, v.ID)
				ids := []string{}
				for _, id := range s.Order {
					if id != v.ID {
						ids = append(ids, id)
					}
				}
				s.Order = ids
			} else {
				s.Chosen[v.ID] = v
				s.Order = append(s.Order, v.ID)
			}
			s.Start = nil
		})
		ui.Text(c, fmt.Sprintf("v%d · %s", v.Revision, preview(v.Text))).FontSize(design.Small).TextColor(design.Muted)
	}
	if s.Cursor != "" {
		quietButton(c, "更多授权材料").Disabled(s.Loading).OnClick(func() { a.refreshOrganization(false, true) })
	}
	ui.Divider(c)
	ui.Text(c, fmt.Sprintf("已选 %d / 16", len(s.Chosen))).Bold()
	for _, id := range s.Order {
		v := s.Chosen[id]
		ui.Text(c, fmt.Sprintf("%s · v%d", v.Title, v.Revision)).FontSize(design.Small)
	}
	actionButton(c, "开始摘要与标签", nil).Key("organization-start").Disabled(s.Busy || s.Project == "" || len(s.Chosen) == 0 || s.Error != "").OnClick(a.startOrganization)
}
func (a *App) organizationDetail(c *ui.Context) {
	s := &a.organization
	ui.Scroll(c.Key("organization-detail-scroll")).Grow(1).TrackScroll(&s.Scroll).Padding(24).Gap(16).Children(func() {
		if s.Error != "" {
			ui.Text(c, s.Error).TextColor(design.Muted)
			quietButton(c, "重试整理读取").OnClick(func() { a.refreshOrganization(false, false) })
		}
		if s.Notice != "" {
			ui.Text(c, s.Notice).FontSize(design.Small).TextColor(design.Muted)
		}
		if s.New {
			a.organizationChooser(c)
			return
		}
		if s.Task == nil {
			a.empty(c, "查看摘要与标签", "选择任务或开始整理。")
			return
		}
		task := s.Task
		ui.Text(c, organizationTaskStatus(*task)).FontSize(design.Title).Bold()
		meta(c, "任务", task.ID)
		meta(c, "项目", a.scopeLabel(task.Project))
		executor := "traex"
		if task.AttemptKind == "apply" {
			executor = "应用已有结果"
		}
		meta(c, "执行", fmt.Sprintf("%s · 执行记录 %d", executor, task.Snapshot.Attempt))
		if task.Error != "" {
			meta(c, "错误", task.Error)
		}
		if task.Result != nil {
			ui.Text(c, strings.ReplaceAll(task.Result.Explanation, "destinations 授权列表", "已授权的移入项目列表")).LineHeight(1.7)
			if len(task.Entries) == 0 {
				ui.Text(c, "本次没有可应用的建议，已有知识保持不变。").Bold()
			}
		}
		applied, failed := 0, 0
		for _, e := range task.Entries {
			if e.State == "applied" {
				applied++
			} else {
				failed++
			}
		}
		if task.Result != nil {
			if task.Snapshot.Schema == records.OrganizationReviewSchema {
				confirmed, pending := 0, 0
				for _, e := range task.Entries {
					if e.State == "review_pending" {
						pending++
					}
					if e.State == "applied" && (e.Item.Action == "duplicate" || e.Item.Action == "conflict") {
						confirmed++
					}
				}
				ui.Text(c, fmt.Sprintf("已应用 %d 项 · 已核对 %d 项 · 待审核 %d 项 · 其他未应用 %d 项", applied-confirmed, confirmed, pending, failed-pending)).FontSize(design.Small).TextColor(design.Muted)
			} else {
				ui.Text(c, fmt.Sprintf("已应用 %d 项 · 未应用 %d 项", applied, failed)).FontSize(design.Small).TextColor(design.Muted)
			}
		}
		for _, entry := range task.Entries {
			ui.Column(c.Key("organization-entry-"+entry.ID)).Border(1, design.Border).Radius(5).Padding(14).Gap(10).Children(func() {
				label := organizationActionLabel(entry.Item.Action)
				entryLabel := organizationEntryLabel(entry.State)
				if entry.State == "applied" && (entry.Item.Action == "duplicate" || entry.Item.Action == "conflict") {
					entryLabel = "已核对"
				}
				ui.Text(c, label+" · "+entryLabel).Bold()
				if entry.Item.Action == "summary" {
					ui.Text(c, entry.Item.Title).Bold()
					ui.Text(c, entry.Item.Text).Selectable().LineHeight(1.7)
					ui.Text(c, "派生摘要保留引用依据，不构成新的独立证据。").FontSize(design.Small).TextColor(design.Muted)
				} else if entry.Item.Action == "add_tags" {
					ui.Text(c, fmt.Sprint(entry.Item.Tags)).Selectable()
				} else {
					a.organizationReviewPreview(c, *task, entry)
				}
				if entry.Error != "" {
					ui.Text(c, entry.Error).FontSize(design.Small).TextColor(design.Muted)
				}
				for _, ref := range entry.Item.Sources {
					ui.Text(c, fmt.Sprintf("依据 %s · v%d", ref.Memory, ref.Revision)).FontSize(design.Small).TextColor(design.Muted)
					ui.Text(c, ref.Quote).Selectable().LineHeight(1.6)
				}
				if entry.Receipt != nil && entry.Receipt.Memory != "" {
					v, ok := s.Results[entry.Receipt.Memory]
					if ok {
						meta(c, "当前", fmt.Sprintf("v%d · %s", v.Revision, a.organizationResultLabel(v)))
						if v.Derived != nil && v.Derived.State == "stale" {
							ui.Text(c, "摘要来源已变化，已排除检索。").Bold()
						}
					}
					if entry.Item.Action == "add_tags" {
						meta(c, "本次新增", fmt.Sprint(entry.Receipt.AddedTags))
					}
					quietButton(c, "查看产出或标签目标").Key("organization-open-" + entry.ID).OnClick(func() {
						a.openOrganizationMemory(entry.Receipt.Memory)
					})
				}
			})
		}
		ui.Divider(c)
		ui.Text(c, "冻结材料与具体版本").Bold()
		for _, v := range task.Snapshot.Inputs {
			ui.Column(c).Border(1, design.Border).Padding(14).Gap(10).Children(func() {
				ui.Text(c, fmt.Sprintf("%s · v%d", v.Title, v.Revision)).Bold()
				ui.Text(c, v.Text).Selectable().LineHeight(1.7)
				meta(c, "来源", v.Source)
				meta(c, "捕获", v.CapturedAt)
				quietButton(c, "查看材料当前版本").Key("organization-source-" + v.Memory).OnClick(func() { a.openOrganizationMemory(v.Memory) })
			})
		}
	})
	ui.Row(c).Shrink(0).Padding(14, 24, 20, 24).Gap(8).Children(func() {
		t := s.Task
		if s.New || t == nil {
			return
		}
		hasReview := false
		for _, e := range t.Entries {
			if e.State == "review_pending" {
				hasReview = true
			}
		}
		if hasReview || t.State == "queued" || t.State == "running" || t.State == "applying" {
			label := "取消摘要整理"
			if t.Snapshot.Schema == records.OrganizationReviewSchema {
				label = "取消未完成项"
			}
			actionButton(c, label, nil).Key("organization-cancel").Disabled(s.Busy).OnClick(a.cancelOrganization)
		}
		if t.Result != nil && (t.State == "partial_failed" || t.State == "interrupted") {
			actionButton(c, "继续应用已有结果", nil).Key("organization-resume").Disabled(s.Busy).OnClick(a.resumeOrganization)
		}
		if t.State == "failed" || t.State == "cancelled" || t.State == "interrupted" || t.State == "partial_failed" {
			quietButton(c, "重新选择并整理").Disabled(s.Busy).OnClick(a.beginOrganization)
		}
	})
}

func organizationEntryLabel(state string) string {
	switch state {
	case "review_pending":
		return "待审核"
	case "rejected":
		return "已拒绝"
	case "pending":
		return "等待保存"
	case "applied":
		return "已应用"
	case "expired":
		return "来源已变化"
	case "failed":
		return "应用失败"
	case "cancelled":
		return "已取消"
	}
	return state
}
func derivedLabel(state string) string {
	switch state {
	case "valid":
		return "自动产生"
	case "stale":
		return "来源已变化"
	case "user_confirmed":
		return "用户确认修订"
	}
	return state
}

func (a *App) organizationResultLabel(v MemoryItem) string {
	if v.State == "deleted" {
		return "已删除 · 保留历史"
	}
	if v.State == "archived" {
		return archivedLabel(v.State)
	}
	if v.Derived != nil && v.Derived.State == "stale" {
		return "摘要依据过期 · 已排除检索"
	}
	return a.availableIndexLabel(v.IndexState)
}

func organizationActionLabel(action string) string {
	switch action {
	case "summary":
		return "新增摘要"
	case "add_tags":
		return "添加标签"
	case "merge":
		return "合并知识"
	case "overwrite":
		return "覆盖正文"
	case "delete":
		return "删除知识"
	case "move":
		return "移动项目"
	case "duplicate":
		return "重复发现"
	case "conflict":
		return "冲突发现"
	}
	return action
}
func (a *App) organizationReviewPreview(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	ui.Text(c, e.Item.Reason).Selectable().LineHeight(1.7)
	if e.Item.Target != "" {
		for _, v := range t.Snapshot.Inputs {
			if v.Memory == e.Item.Target {
				meta(c, "目标", fmt.Sprintf("%s · v%d", v.Title, v.Revision))
				ui.Text(c, "修改前").Bold()
				ui.Text(c, v.Text).Selectable().LineHeight(1.6)
			}
		}
	}
	if e.Item.Text != "" {
		ui.Text(c, "建议正文").Bold()
		ui.Text(c, e.Item.Title).Bold()
		ui.Text(c, e.Item.Text).Selectable().LineHeight(1.7)
	}
	if e.Item.Action == "merge" {
		ui.Text(c, "确认后新建合并知识，归档全部下列引用知识；历史保留。").FontSize(design.Small)
	}
	if e.Item.Action == "delete" {
		ui.Text(c, "确认后逻辑删除，历史保留；恢复产生新版本。").FontSize(design.Small)
	}
	if e.Item.Destination != "" {
		meta(c, "移入项目", a.scopeLabel(e.Item.Destination))
		ui.Text(c, "正文保留；原项目检索立即排除。").FontSize(design.Small)
	}
	if e.Item.Action == "duplicate" || e.Item.Action == "conflict" {
		ui.Text(c, "确认仅记录已核对，不裁决或修改知识。").FontSize(design.Small)
	}
	if e.State == "review_pending" {
		ui.Row(c).Gap(8).Children(func() {
			label := "确认应用此项"
			if e.Item.Action == "duplicate" || e.Item.Action == "conflict" {
				label = "记录已核对此项"
			}
			actionButton(c, label, nil).Key("organization-confirm-" + e.ID).Disabled(a.organization.Busy).OnClick(func() { a.reviewOrganizationEntry(t, e, true) })
			quietButton(c, "拒绝此项").Key("organization-reject-" + e.ID).Disabled(a.organization.Busy).OnClick(func() { a.reviewOrganizationEntry(t, e, false) })
		})
	}
}
func (a *App) reviewOrganizationEntry(t records.OrganizationTask, e records.OrganizationEntry, confirm bool) {
	s := &a.organization
	if s.Decisions == nil {
		s.Decisions = map[string]records.OrganizationReview{}
	}
	key := e.ID + fmt.Sprint(confirm)
	d, ok := s.Decisions[key]
	if !ok {
		d = records.OrganizationReview{Task: t.ID, Item: e.ID, Digest: records.MergeDigest(e.Item), Receipt: uuid.NewString(), Confirm: confirm}
		s.Decisions[key] = d
	}
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return s.Client.Review(ctx, d) }, false)
}

// Resolve every current-version navigation against the service. A moved
// record's frozen scope remains history and cannot choose its current route.
func (a *App) openOrganizationMemory(id string) {
	s := &a.organization
	if s.Busy || a.libraryContext == nil {
		return
	}
	selected := s.Selected
	s.Busy = true
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer stop()
		v, err := a.library.Get(ctx, id)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if s.Selected != selected || a.page != "整理记录" || !a.organizationMode {
				return
			}
			if err != nil {
				s.Error = "当前版本读取失败，请重试；任务内容已保留。"
				return
			}
			a.openMergeMemory(id, v.Dataset)
		})
	}()
}

// Execution completion and approval are separate facts. The v2 detail heading
// names the unresolved decision without changing the persistent task state.
func organizationTaskStatus(t records.OrganizationTask) string {
	if t.Snapshot.Schema != records.OrganizationReviewSchema || t.State != "completed" {
		return organizationStatus(t.State)
	}
	pending, expired, rejected := 0, 0, 0
	for _, e := range t.Entries {
		switch e.State {
		case "review_pending":
			pending++
		case "expired":
			expired++
		case "rejected":
			rejected++
		}
	}
	if pending > 0 {
		return fmt.Sprintf("等待审核 · %d 项", pending)
	}
	if expired > 0 {
		return "执行已完成 · 有过期建议"
	}
	if rejected > 0 {
		return "执行已完成 · 已记录拒绝"
	}
	return "整理已完成"
}
