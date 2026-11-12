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
	ProcessingDetailsOpen                                                     bool
	projectLabelKey                                                           string
	AdvancedOpen, PathsOpen, MaterialsOpen                                    bool
	Progress                                                                  *OrganizationProgressUI
	OpenReviewItem                                                            string
	PreviewID, Focus                                                          string
	RestoreFocus, CancelWanted, AbandonWanted                                 bool
	FilterOpen, TasksLoaded                                                   bool
	FilterOrigin, FilterProject, FilterProjectLabel, FilterTime, ListIdentity string
	TasksTotal                                                                int
	RejectReasons                                                             map[string]*OrganizationRejectDraft
	ReviewItem                                                                string
	ResultScroll                                                              float32
	ReviewScroll                                                              map[string]*ui.ScrollState
	ReviewFocus                                                               map[string]string
	DiagnosticOpen, ExplanationOpen                                           bool
	Sources                                                                   map[string]MemoryItem
	SourceErrors                                                              map[string]string
	SourcesReadFailed                                                         bool
	SourceErrorCodes                                                          map[string]string
	MoveAuthorizations                                                        map[string]string
	DeletionImpact                                                            *OrganizationDeletionImpactUI
	Epoch                                                                     string
	Processing                                                                records.ProcessingConfiguration
	ProcessingLoaded, ProcessingLoading                                       bool
	ProcessingError                                                           string
	ProcessingSequence                                                        uint64
	ExperienceDrafts                                                          map[string]*ExperienceReviewDraft
	GlobalMode                                                                bool
	Purpose                                                                   string
	ReviewMode                                                                bool
	Destinations                                                              map[string]bool
	Decisions                                                                 map[string]records.OrganizationReview
	Client                                                                    serviceipc.OrganizationClient
	Tasks                                                                     []records.OrganizationSummary
	Selected, Next, Project, ProjectLabel, Kind, Cursor, Error, Notice        string
	Task                                                                      *records.OrganizationTask
	Candidates                                                                []MemoryItem
	Chosen                                                                    map[string]MemoryItem
	Order                                                                     []string
	Start                                                                     *records.OrganizationStart
	Resume                                                                    *records.OrganizationResume
	Results                                                                   map[string]MemoryItem
	New, Busy, Loading                                                        bool
	Sequence                                                                  int64
	Scroll                                                                    ui.ScrollState
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
	a.beginOrganizationFor(a.scope, nil)
}
func (a *App) beginOrganizationFor(project string, chosen []MemoryItem) {
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
	a.materialContextBarrier(func() {
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
		s.OpenReviewItem = ""
		s.Sources = nil
		s.SourceErrors = nil
		s.SourcesReadFailed = false
		s.SourceErrorCodes = nil
		s.MoveAuthorizations = nil
		s.DeletionImpact = nil
		s.Kind = "knowledge"
		s.GlobalMode = false
		s.Purpose = ""
		s.Project = ""
		s.ProjectLabel = ""
		s.Scroll.Y = 0
		s.PreviewID = uuid.NewString()
		s.Focus, s.RestoreFocus, s.CancelWanted = "", false, false
		s.Sequence++
		for _, scope := range a.scopes {
			if scope.Kind == "project" && scope.ID == project {
				s.Project = scope.ID
				s.ProjectLabel = a.scopeLabel(scope.ID)
			}
		}
		for _, item := range chosen {
			if item.Dataset == s.Project && item.State == "active" && item.Revision > 0 {
				if _, exists := s.Chosen[item.ID]; exists {
					continue
				}
				s.Chosen[item.ID] = item
				s.Order = append(s.Order, item.ID)
			}
		}
		if len(chosen) > 0 {
			s.Notice = fmt.Sprintf("已选择 %d 份当前可读取的相关材料。请核对范围和版本，再明确开始；尚未调用模型。", len(s.Chosen))
		} else if chosen != nil {
			s.Notice = "尚无可沿用的已核验材料。请先核对项目授权，再重新选择；尚未调用模型。"
		}
		a.refreshOrganization(false, false)
		a.refreshOrganizationProcessing()
	})
}
func (a *App) beginOrganizationOrMerge() {
	if a.organizationMode {
		a.beginOrganization()
	} else {
		a.beginMerge()
	}
}
func (a *App) selectOrganization(id string) { a.selectOrganizationAt(id, "") }

func (a *App) selectOrganizationAt(id, item string) {
	s := &a.organization
	if s.Busy {
		return
	}
	if s.Start != nil {
		s.Error = "原启动请求尚未取得回执。请先返回原请求，核对或明确放弃后再切换任务。"
		return
	}
	a.materialContextBarrier(func() {
		a.organizationMode = true
		a.navigate("整理记录", "")
		s.PreviewID = ""
		s.Focus, s.RestoreFocus, s.CancelWanted = "", false, false
		s.Selected = id
		s.Task = nil
		s.New = false
		s.Notice = ""
		s.Resume = nil
		s.ReviewItem = ""
		s.OpenReviewItem = item
		s.Sources = nil
		s.SourceErrors = nil
		s.SourcesReadFailed = false
		s.SourceErrorCodes = nil
		s.MoveAuthorizations = nil
		s.DeletionImpact = nil
		s.Scroll.Y = 0
		s.Sequence++
		a.refreshOrganization(false, false)
	})
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
	progress, progressRoot, progressEpoch := s.Progress, a.library.Root, a.libraryEpoch
	selected, project, isNew, kind := s.Selected, s.Project, s.New, s.Kind
	needsPreview := s.PreviewID == "" && selected != "" && !isNew
	hasPersistence := a.materialPersistence != nil
	if kind == "" {
		kind = "knowledge"
	}
	q := a.organizationListQuery(time.Now())
	identity := records.MergeDigest(q)
	if more {
		q.Before = s.Next
	}
	candidateCursor := ""
	if candidatesMore {
		candidateCursor = s.Cursor
	}
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		page, err := client.List(ctx, q)
		var task records.OrganizationTask
		var preview records.OrganizationPreviewState
		var candidates records.Page
		results := map[string]MemoryItem{}
		sources := map[string]MemoryItem{}
		sourceErrors := map[string]string{}
		sourceErrorCodes := map[string]string{}
		if err == nil && selected != "" && !isNew {
			task, err = client.Get(ctx, selected)
			if err == nil && needsPreview && hasPersistence {
				preview, err = client.MaterialPreviewFor(ctx, selected)
			}
		}
		if err == nil && isNew && project != "" {
			candidates, err = listOrganizationMaterials(ctx, library.List, project, candidateCursor)
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
					sourceErrorCodes[input.Memory] = sourceReaderErrorCode(e)
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
				s.SourcesReadFailed = true
				return
			}
			s.Error = ""
			if s.Start != nil && s.New {
				s.Error = "原启动回执尚未取得；材料与请求已固定。请核对原回执，或明确重试／放弃原请求。"
			}
			if s.ListIdentity != identity {
				s.Tasks, s.Next = nil, ""
			}
			s.ListIdentity, s.TasksLoaded, s.TasksTotal = identity, true, page.Total
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
					if needsPreview && s.PreviewID == "" {
						if preview.Preview != nil {
							preview.Task = &task
							preview.Inputs = task.Snapshot.Inputs
							a.adoptMaterialPreview(preview)
						} else {
							s.PreviewID = uuid.NewString()
						}
					}
					s.Task = &task
					if id := s.OpenReviewItem; id != "" {
						s.OpenReviewItem = ""
						for _, entry := range task.Entries {
							if entry.ID == id {
								a.openOrganizationEntry(id)
								break
							}
						}
					}
					s.Results = results
					a.adoptOrganizationProgress(progress, progressRoot, progressEpoch, task, results)
					s.Sources = sources
					s.SourceErrors = sourceErrors
					s.SourcesReadFailed = false
					s.SourceErrorCodes = sourceErrorCodes
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
			a.persistMaterialPreview(nil)
		})
	}()
}
func (a *App) organizationServiceAvailable() bool {
	return a.serviceState == "ready" && a.runtimeStatus.Phase == "ready" && a.libraryError == ""
}
func (a *App) startOrganization() {
	s := &a.organization
	if s.Busy || s.AbandonWanted || len(s.Chosen) == 0 || s.Project == "" || a.libraryContext == nil {
		return
	}
	if !a.organizationServiceAvailable() {
		s.Error = "记忆服务尚未就绪，材料与原请求保留；恢复服务后可明确重试。"
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
	a.organizationActionAfter(fn, start, nil)
}
func (a *App) organizationActionAfter(fn func(context.Context) (records.OrganizationTask, error), start bool, after func()) {
	s := &a.organization
	if s.Busy || a.libraryContext == nil {
		return
	}
	s.Busy = true
	s.Notice = "正在提交…"
	s.Error = ""
	s.Sequence++
	epoch, life := s.Epoch, a.libraryContext
	perform := func(err error) {
		if err != nil {
			s.Busy = false
			s.Notice = ""
			s.Error = "原请求尚未保存，操作未发送；草稿保留，请重试。"
			return
		}
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
					if a.retireUnconfirmedLegacyMove(err) {
						s.Error = "原移动确认未提交。请核对来源与目标项目，并明确授权范围变化后重新确认。"
						a.persistMaterialPreview(nil)
						return
					}
					s.Error = "操作回执未取得，原请求保留；请核对任务后重试：" + err.Error()
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
				if task.Cancelled {
					s.CancelWanted = false
				}
				if after != nil {
					after()
				}
				s.Notice = "任务已持久保存，产出与索引状态分别更新。"
				s.Sequence++
				a.refreshOrganization(false, false)
				a.refresh(false)
				a.persistMaterialPreview(nil)
			})
		}()
	}
	a.persistMaterialPreview(perform)
}
func (a *App) cancelOrganization() {
	s := &a.organization
	if s.Task == nil {
		return
	}
	id := s.Task.ID
	s.CancelWanted = true
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
	a.organizationActionAfter(func(ctx context.Context) (records.OrganizationTask, error) { return client.Resume(ctx, d) }, false, func() {
		if s.Resume != nil && s.Resume.OperationID == d.OperationID {
			s.Resume = nil
		}
	})
}
func (a *App) organizationList(c *ui.Context) {
	s := &a.organization
	if len(s.Tasks) == 0 {
		if s.Error != "" {
			ui.Text(c, "整理记录读取未完成，保留当前任务与筛选条件。").FontSize(11)
			quietButton(c, "重试读取整理记录").Key("organization-list-retry").Disabled(s.Loading || s.Busy).OnClick(func() { a.refreshOrganization(false, false) })
		} else if !s.TasksLoaded && !a.visualFixture {
			ui.Text(c, "正在读取整理记录…").FontSize(11)
		} else if s.FilterProject != "" || s.FilterOrigin != "" && s.FilterOrigin != "不限来源" || s.FilterTime == "今天" {
			a.empty(c, "没有匹配的整理记录", "当前任务和审核上下文保留，可清空筛选条件。")
			if !s.FilterOpen {
				quietButton(c, "清空条件").Key("organization-list-clear-empty").Disabled(s.Busy || s.Loading).OnClick(a.clearOrganizationFilters)
			}
		} else {
			a.empty(c, "暂无整理记录", "选择本次材料和处理方式开始整理，所有产出保留出处。")
		}
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
				ui.Text(c, a.scopeLabel(t.Project)+" · "+organizationSummaryExecutor(t)).FontSize(design.Small).TextColor(design.Muted)
				status := func() string {
					if t.State == "completed" {
						return "执行已完成"
					}
					return organizationStatus(t.State)
				}()
				created, _ := time.Parse("2006-01-02 15:04:05", t.CreatedAt)
				if created.IsZero() {
					created, _ = time.Parse(time.RFC3339Nano, t.CreatedAt)
				}
				ui.Text(c, organizationOriginLabel(t.Origin)+" · "+created.Local().Format("2006-01-02 15:04")+" · "+status).FontSize(design.Small).TextColor(design.Muted)
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
		quietButton(c, "加载更多记录").Key("organization-list-more").Disabled(s.Loading || s.Busy).OnClick(func() { a.refreshOrganization(true, false) })
	}
}
func (a *App) organizationChooser(c *ui.Context) {
	s := &a.organization
	if s.Start != nil {
		ui.Text(c, "核对原整理请求").FontSize(design.Title).Bold()
		a.organizationFrozenChooser(c)
		return
	}
	ui.Text(c, "主动整理").FontSize(10).TextColor(ui.Hex("#99aabd"))
	ui.Text(c, "选好范围，再开始整理").FontSize(23).FontWeight(550).TextColor(ui.Hex("#3c556d")).LineHeight(1.5)
	ui.Text(c, "使用当前已启用的处理方式与账号额度；新增摘要和标签可自动保存，已有知识的修改先生成建议。").FontSize(12).TextColor(ui.Hex("#93a7bb")).LineHeight(1.9)
	if s.Error != "" {
		ui.Text(c, s.Error).FontSize(design.Small)
		quietButton(c, "重试读取整理记录").Disabled(s.Busy || s.Loading).OnClick(func() { a.refreshOrganization(false, false) })
	}
	options := []string{}
	projects := map[string]string{}
	projectLabel := ""
	projectLabelKey := s.Project
	for _, v := range a.scopes {
		if v.Kind == "project" {
			label := a.scopeLabel(v.ID)
			options = append(options, label)
			projects[label] = v.ID
			projectLabelKey += "\x00" + v.ID + "\x00" + label
			if v.ID == s.Project {
				projectLabel = label
			}
		}
	}
	// Preview identity may arrive before scopes. Only that exact project ID
	// supplies its display label; a name never rebinds a restored request.
	if s.projectLabelKey != projectLabelKey {
		s.ProjectLabel = projectLabel
		s.projectLabelKey = projectLabelKey
	}
	ui.Text(c, "本次项目").FontSize(11).TextColor(design.Muted)
	ui.Select(c.Key("organization-project"), &s.ProjectLabel, options).Label("整理项目").Height(42).Background(ui.Hex("#fcfdff")).Border(1, ui.Hex("#dce6f2")).Radius(6).Disabled(s.Busy || s.Start != nil).OnChange(func() {
		project := projects[s.ProjectLabel]
		if project == "" || project == s.Project {
			s.ProjectLabel = projectLabel
			return
		}
		s.Project = project
		s.Chosen = map[string]MemoryItem{}
		s.Destinations = map[string]bool{}
		s.Order = nil
		s.Candidates = nil
		s.Cursor = ""
		s.Start = nil
		s.Sequence++
		a.refreshOrganization(false, false)
	})
	ui.Text(c, "整理执行者").FontSize(11).TextColor(design.Muted)
	status := "尚未取得当前记忆处理方式。"
	if s.ProcessingLoading {
		status = "正在读取记忆处理方式…"
	} else if s.ProcessingError == "" && s.ProcessingLoaded {
		if v := s.Processing.Current.Revision; v != nil {
			status = processingExecutorName(v.Spec)
		} else {
			status = "尚未启用记忆处理方式。"
		}
	}
	ui.Row(c).Gap(8).MinHeight(42).Padding(10, 11).Background(ui.Hex("#fcfdff")).Border(1, ui.Hex("#dce6f2")).Radius(6).Children(func() {
		ui.Text(c, status).Key("organization-processing-executor").FontSize(design.Small).Grow(1).MinWidth(0)
		quietButton(c, "更多").Key("organization-processing-details").Disabled(s.Busy).OnClick(func() { s.ProcessingDetailsOpen = !s.ProcessingDetailsOpen })
	})
	if s.ProcessingError != "" {
		ui.Text(c, s.ProcessingError).FontSize(11)
	} else if s.ProcessingLoaded && !s.ProcessingLoading && s.Processing.Current.Revision != nil && !organizationProcessingAvailable(s.Processing) {
		ui.Text(c, "当前方式尚未接通本次整理；请核对记忆处理状态。").FontSize(11)
	}
	if s.ProcessingDetailsOpen || s.ProcessingError != "" || !s.ProcessingLoaded {
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c, "读取当前处理方式").Key("organization-processing-refresh").Disabled(s.Busy || s.ProcessingLoading).OnClick(a.refreshOrganizationProcessing)
			quietButton(c, "前往记忆处理").Key("organization-processing-settings").Disabled(s.Busy).OnClick(func() { a.navigate("设置", ""); a.settingsSection = "processing"; a.refreshProcessing() })
		})
	}
	if s.Project == "" {
		ui.Text(c, "请选择已登记项目。").TextColor(design.Muted)
		return
	}
	ui.Divider(c)
	ui.Text(c, "本次材料").FontSize(12).FontWeight(500).TextColor(ui.Hex("#6e88a2"))
	for _, v := range s.Candidates {
		_, selected := s.Chosen[v.ID]
		ui.Checkbox(c.Key("organization-choose-"+v.ID), &selected, fmt.Sprintf("%s · v%d", v.Title, v.Revision)).Disabled(s.Busy || s.Start != nil || (!selected && len(s.Chosen) >= 16)).OnChange(func() {
			if !selected {
				delete(s.Chosen, v.ID)
				ids := []string{}
				for _, id := range s.Order {
					if id != v.ID {
						ids = append(ids, id)
					}
				}
				s.Order = ids
			} else {
				if s.Chosen == nil {
					s.Chosen = map[string]MemoryItem{}
				}
				s.Chosen[v.ID] = v
				s.Order = append(s.Order, v.ID)
			}
			s.Start = nil
		})
	}
	if s.Cursor != "" {
		quietButton(c, "加载更多记忆材料").Disabled(s.Loading).OnClick(func() { a.refreshOrganization(false, true) })
	}
	ui.Divider(c)
	ui.Text(c, fmt.Sprintf("已选 %d / 16", len(s.Chosen))).Bold()
	ui.Collapsible(c, "查看已选材料顺序", &s.MaterialsOpen, func() {
		for _, id := range s.Order {
			v := s.Chosen[id]
			ui.Text(c, fmt.Sprintf("%s · v%d", v.Title, v.Revision)).FontSize(design.Small)
		}
	})
	if s.ReviewMode || s.GlobalMode {
		policy := "已开启：修改建议"
		if s.GlobalMode && s.ReviewMode {
			policy += "、通用经验候选"
		} else if s.GlobalMode {
			policy = "已开启：通用经验候选"
		}
		ui.Text(c, policy).FontSize(design.Small).TextColor(design.Muted)
	}
	ui.Collapsible(c, "更多整理选项", &s.AdvancedOpen, func() {
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
				}
			}
		}

		ui.Collapsible(c, "查看项目位置", &s.PathsOpen, func() {
			for _, scope := range a.scopes {
				if scope.Kind == "project" {
					ui.Text(c, a.scopeLabel(scope.ID)).FontSize(design.Small)
					ui.Text(c, scope.Path).FontSize(design.Small).TextColor(design.Muted).Selectable()
				}
			}
		})
	})
}
func (a *App) organizationChooserHeader(c *ui.Context) {
	s := &a.organization
	if s.New && s.Start == nil {
		quietButton(c, "取消").Key("organization-chooser-cancel").Disabled(s.Busy || s.AbandonWanted).OnClick(a.returnFromOrganizationChooser)
	}
}
func (a *App) returnFromOrganizationChooser() {
	s := &a.organization
	if !s.New || s.Start != nil || s.Busy || s.AbandonWanted {
		return
	}
	if s.PreviewID == "" {
		s.PreviewID = uuid.NewString()
	}
	id, project, epoch, libraryEpoch := s.PreviewID, s.Project, s.Epoch, a.libraryEpoch
	a.materialContextBarrier(func() {
		if a.libraryEpoch != libraryEpoch || s.Epoch != epoch || s.PreviewID != id || !s.New || s.Start != nil {
			return
		}
		if s.Project != project {
			return
		}
		s.New = false
		a.navigate("整理记录", "")
	})
}

func (a *App) organizationFrozenChooser(c *ui.Context) {
	s := &a.organization
	ui.Text(c, "原整理请求与材料已固定").Bold()
	project := a.scopeLabel(s.Start.Project)
	if project == "" {
		project = "原项目（待核对）"
	}
	ui.Text(c, "本次项目 · "+project).FontSize(design.Small)
	executor := "原处理方式（待核对）"
	if auth := s.Start.Processing; auth != nil {
		if v := s.Processing.Current.Revision; v != nil && v.ID == auth.Revision {
			executor = processingExecutorName(v.Spec)
		}
	}
	ui.Text(c, "整理执行者 · "+executor).FontSize(design.Small)
	policy := "仅新增摘要与标签"
	if s.Start.Review {
		policy = "已有知识的修改先生成建议"
	}
	if s.Start.Global {
		policy += " · 包含通用经验候选"
	}
	ui.Text(c, policy).FontSize(design.Small).TextColor(design.Muted)
	ui.Text(c, "核对原请求后可重试；重试沿用同一次提交，不另建整理任务。").FontSize(design.Small).TextColor(design.Muted)
	if s.Error != "" {
		ui.Text(c, s.Error).FontSize(design.Small)
	}
	ui.Row(c).Gap(8).Children(func() {
		quietButton(c, "核对原请求回执").Key("organization-start-receipt").Disabled(s.Busy).OnClick(a.readMaterialStartReceipt)
		quietButton(c, "放弃原请求，保留材料").Key("organization-abandon-start").Disabled(s.Busy).OnClick(a.abandonMaterialStart)
	})
	ui.Text(c, fmt.Sprintf("本次固定材料 · %d 条", len(s.Start.Inputs))).Bold()
	for _, input := range s.Start.Inputs {
		v := s.Chosen[input.Memory]
		title := v.Title
		if title == "" {
			title = "原材料"
		}
		ui.Text(c, fmt.Sprintf("%s · v%d", title, input.Revision)).FontSize(design.Small)
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
		if confirm && !a.organizationSourcesCurrent(t, e) {
			s.Error = "来源版本或权限尚未核验，不能提交新的确认。"
			return
		}
		if confirm && e.Item.Action == "move" && !a.organizationMoveAuthorized(t, e) {
			s.Error = "请先明确授权来源与目标项目的范围变化。"
			return
		}
		if confirm && e.Item.Action == "delete" && !a.organizationDeletionImpactCurrent(t, e) {
			s.Error = "请先核对当前版本的直接引用影响，再确认删除。"
			return
		}
		d = records.OrganizationReview{Task: t.ID, Item: e.ID, Digest: records.MergeDigest(e.Item), Receipt: uuid.NewString(), Confirm: confirm}
		d.MoveScopeAccepted = confirm && e.Item.Action == "move"
		if !confirm {
			if draft := s.RejectReasons[e.ID]; draft != nil {
				d.RejectionReason = draft.Reason
			}
		}
		s.Decisions[key] = d
	}
	client := s.Client
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) { return client.Review(ctx, d) }, false)
}

// Resolve every current-version navigation against the service. A moved
// record's frozen scope remains history and cannot choose its current route.
func (a *App) openOrganizationMemory(id string) { a.openOrganizationMemoryMode(id, false) }
func (a *App) openOrganizationMemoryMode(id string, history bool) {
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
			if history {
				a.openKnowledgeHistoryFor(displayVersion(v))
			} else {
				a.openMergeMemory(id, v.Dataset, v.Origin)
			}
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
