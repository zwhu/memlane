package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type OrganizationUI struct {
	ReviewItem                                                         string
	ResultScroll                                                       float32
	ReviewScroll                                                       map[string]*ui.ScrollState
	DiagnosticOpen, ExplanationOpen                                    bool
	Sources                                                            map[string]MemoryItem
	SourceErrors                                                       map[string]string
	Epoch                                                              string
	Processing                                                         records.ProcessingConfiguration
	ProcessingLoaded, ProcessingLoading                                bool
	ProcessingError                                                    string
	ProcessingSequence                                                 uint64
	ExperienceDrafts                                                   map[string]*ExperienceReviewDraft
	GlobalMode                                                         bool
	Purpose                                                            string
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
	if s.Start != nil {
		a.organizationMode = true
		a.navigate("整理记录", "")
		s.New = true
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
	s.ReviewItem = ""
	s.Sources = nil
	s.SourceErrors = nil
	s.Kind = "knowledge"
	s.GlobalMode = false
	s.Purpose = ""
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
	a.refreshOrganizationProcessing()
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
	s.ReviewItem = ""
	s.Sources = nil
	s.SourceErrors = nil
	s.Scroll.Y = 0
	s.Sequence++
	a.refreshOrganization(false, false)
}
func (a *App) refreshOrganization(more, candidatesMore bool) {
	s := &a.organization
	if a.libraryContext == nil || a.libraryContext.Err() != nil || s.Loading || s.Busy || a.visualFixture {
		return
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	epoch, life, client, library := s.Epoch, a.libraryContext, s.Client, a.library
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
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		page, err := client.List(ctx, q)
		var task records.OrganizationTask
		var candidates records.Page
		results := map[string]MemoryItem{}
		sources := map[string]MemoryItem{}
		sourceErrors := map[string]string{}
		if err == nil && selected != "" && !isNew {
			task, err = client.Get(ctx, selected)
		}
		if err == nil && isNew && project != "" {
			candidates, err = library.List(ctx, query)
		}
		if err == nil {
			for _, entry := range task.Entries {
				if entry.Receipt != nil && entry.Receipt.Memory != "" {
					v, e := library.Get(ctx, entry.Receipt.Memory)
					if e != nil {
						err = e
						break
					}
					results[v.Memory] = displayVersion(v)
				}
			}
		}
		if err == nil {
			for _, input := range task.Snapshot.Inputs {
				v, e := library.Get(ctx, input.Memory)
				if e != nil {
					sourceErrors[input.Memory] = "当前来源读取未完成，暂不能确认新的改动。"
				} else {
					sources[input.Memory] = displayVersion(v)
				}
			}
		}
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.organization.Epoch != epoch {
				return
			}
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
					s.Sources = sources
					s.SourceErrors = sourceErrors
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
		if s.ProcessingLoading || !s.ProcessingLoaded || !organizationProcessingAvailable(s.Processing) {
			s.Error = "请先在记忆处理中明确启用已验证的 pi 方式，再开始本次整理。"
			return
		}
		d := records.OrganizationStart{OperationID: uuid.NewString(), Project: s.Project, Review: s.ReviewMode || s.GlobalMode, Global: s.GlobalMode, Purpose: s.Purpose}
		d.Processing = &records.ProcessingTaskAuthorization{Revision: s.Processing.Current.Revision.ID, Generation: s.Processing.Current.Generation}
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
	client := s.Client
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return client.Start(ctx, d) }, true)
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
	epoch, life := s.Epoch, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 10*time.Second)
		defer stop()
		task, err := fn(ctx)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.organization.Epoch != epoch {
				return
			}
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
	client := s.Client
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return client.Cancel(ctx, id) }, false)
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
	client := s.Client
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return client.Resume(ctx, d) }, false)
}
func (a *App) organizationList(c *ui.Context) {
	s := &a.organization
	if len(s.Tasks) == 0 {
		a.empty(c, "暂无整理记录", "选择本次材料和处理方式开始整理，所有产出保留出处。")
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
		quietButton(c, "更多整理记录").Disabled(s.Loading).OnClick(func() { a.refreshOrganization(true, false) })
	}
}
func (a *App) organizationChooser(c *ui.Context) {
	s := &a.organization
	ui.Text(c, "选好范围，再开始整理").FontSize(design.Title).Bold()
	ui.Checkbox(c.Key("organization-global-mode"), &s.GlobalMode, "发现通用经验候选，预览确认后保存").Disabled(s.Busy || s.Start != nil).OnChange(func() { s.Start = nil })
	ui.Checkbox(c.Key("organization-review-mode"), &s.ReviewMode, "同时提出修改建议，逐项确认后生效").Disabled(s.Busy || s.Start != nil).OnChange(func() { s.Start = nil; s.Destinations = map[string]bool{} })
	if s.ReviewMode {
		ui.Text(c, "可建议移入的项目（可不选；授权不读取其他项目知识）").FontSize(design.Small)
		for _, scope := range a.scopes {
			if scope.Kind == "project" && scope.ID != s.Project {
				allowed := s.Destinations[scope.ID]
				ui.Checkbox(c.Key("organization-destination-"+scope.ID), &allowed, a.scopeLabel(scope.ID)).Disabled(s.Busy || s.Start != nil).OnChange(func() {
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
	ui.Text(c, "选择一个项目内的知识或原始记录。使用已启用的记忆处理方式与账号额度；新增摘要和标签可自动保存，已有知识的修改先生成建议。").LineHeight(1.7)
	ui.Column(c).Gap(8).Padding(12).Border(1, design.Border).Radius(5).Children(func() {
		ui.Text(c, "整理执行者").FontSize(11).TextColor(design.Muted)
		if s.Start != nil && s.Start.Processing != nil {
			ui.Text(c, "本次处理方式已固定；重试沿用原请求。").Key("organization-processing-fixed")
		} else if s.ProcessingLoading {
			ui.Text(c, "正在读取记忆处理方式…")
		} else if v := s.Processing.Current.Revision; v != nil {
			ui.Text(c, processingExecutorName(v.Spec)).Key("organization-processing-executor")
			if !organizationProcessingAvailable(s.Processing) {
				ui.Text(c, "当前方式尚未接通本次整理；请选择已验证并启用的 pi 方式。").FontSize(11)
			}
		} else {
			ui.Text(c, "尚未启用记忆处理方式。")
		}
		if s.ProcessingError != "" {
			ui.Text(c, s.ProcessingError).FontSize(11)
		}
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c, "读取当前处理方式").Key("organization-processing-refresh").Disabled(s.Busy || s.Start != nil || s.ProcessingLoading).OnClick(a.refreshOrganizationProcessing)
			quietButton(c, "前往记忆处理").Key("organization-processing-settings").Disabled(s.Busy || s.Start != nil).OnClick(func() { a.navigate("设置", ""); a.settingsSection = "processing"; a.refreshProcessing() })
		})
	})
	options := []string{}
	for _, v := range a.scopes {
		if v.Kind == "project" {
			options = append(options, a.scopeLabel(v.ID))
		}
	}
	ui.Select(c.Key("organization-project"), &s.ProjectLabel, options).Label("整理项目").Disabled(s.Busy || s.Start != nil).OnChange(func() {
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
			ui.Button(c, label).Key("organization-kind-" + kind).Disabled(s.Busy || s.Start != nil).OnClick(func() {
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
		ui.Button(c.Key("organization-choose-"+v.ID), label).Justify(ui.Start).Disabled(s.Busy || s.Start != nil || (!selected && len(s.Chosen) >= 16)).OnClick(func() {
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
}
func (a *App) organizationDetail(c *ui.Context) { a.materialOrganizationDetail(c) }

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
	case "global_experience":
		return "通用经验候选"
	}
	return action
}
func (a *App) organizationReviewPreview(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	if e.Item.Action == "global_experience" {
		a.experienceReviewPreview(c, t, e)
		return
	}
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
	if e.Item.Action == "global_experience" {
		meta(c, "保存范围", a.scopeLabel(e.Item.Destination))
		ui.Text(c, "适用条件").Bold()
		ui.Text(c, e.Item.Conditions).Selectable().LineHeight(1.7)
		ui.Text(c, "确认后新增通用经验，原项目知识与来源版本保留；项目明确约定优先。").FontSize(design.Small)
	}
	if e.Item.Action == "move" {
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
	client := s.Client
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return client.Review(ctx, d) }, false)
}

// Resolve every current-version navigation against the service. A moved
// record's frozen scope remains history and cannot choose its current route.
func (a *App) openOrganizationMemory(id string) {
	s := &a.organization
	if s.Busy || a.libraryContext == nil {
		return
	}
	selected := s.Selected
	epoch, life, library := s.Epoch, a.libraryContext, a.library
	s.Busy = true
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		v, err := library.Get(ctx, id)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.organization.Epoch != epoch {
				return
			}
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
	if t.Snapshot.Schema == records.OrganizationSchema || t.State != "completed" {
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
