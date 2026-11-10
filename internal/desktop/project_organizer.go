package desktop

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type ProjectBindingOperation struct {
	FailureCode string
	Project     string
	Active      bool
	Error       string
	Kind        string
	Task        string
	Draft       records.ProjectTaskStart
	Recovery    records.IndexRebuildStart
	Result      *records.ProjectTask
}

type ProjectOrganizerUI struct {
	WatchCancel                             context.CancelFunc
	WatchSequence                           uint64
	HistoryLoading, HistoryLoaded           bool
	HistorySequence                         uint64
	HistoryCancel                           context.CancelFunc
	HistoryWorkspaceID, HistoryError        string
	HistoryUnavailable, HistoryUnattributed int
	Client                                  serviceipc.ProjectTaskClient
	Open, Loaded, Loading                   bool
	Choosing, IncludeHistory                bool
	ReturnEditing, ReturnFocus              bool
	ReturnMode                              string
	Project, SelectedTask, Error            string
	OperationID                             string
	Sequence                                uint64
	Binding                                 records.ProjectBinding
	Connections                             []records.Connection
	Sessions                                []records.ProjectSessionSelection
	Task                                    *records.ProjectTask
	Operation                               *ProjectBindingOperation
	Scroll                                  ui.ScrollState
	Motion                                  projectStageMotion
}

func (a *App) projectKnowledgeEmpty() bool {
	return a.page == "项目" && a.scope != "" && (a.recordKind == "" || a.recordKind == "knowledge") &&
		!a.loading && a.libraryError == "" && a.readingSearch == "" && a.readingFilter == "" &&
		a.readingCountsRoute == a.page+":"+a.scope && a.readingKnowledgeTotal != nil && a.readingRawTotal != nil && *a.readingKnowledgeTotal == 0
}

func (a *App) openProjectOrganizer(project, task string) {
	a.closeProjectOrganizer()
	if a.page != "项目" || a.scope != project {
		a.navigate("项目", project)
	}
	a.closeProjectSettings()
	a.closeKnowledgeHistory()
	a.closeSourceReader()
	a.rememberDraft()
	prior := a.projectOrganizer
	a.projectOrganizer = ProjectOrganizerUI{Client: prior.Client, Project: project, SelectedTask: task,
		WatchSequence: prior.WatchSequence + 1,
		Open:          true, Sequence: prior.Sequence + 1, Operation: prior.Operation,
		HistorySequence: prior.HistorySequence + 1,
		OperationID:     uuid.NewString(), ReturnMode: a.detailMode, ReturnEditing: a.editing}
	if prior.Operation != nil && prior.Operation.Project == project {
		if prior.Operation.Active && prior.Operation.Kind == "start" {
			a.projectOrganizer.OperationID = prior.Operation.Draft.OperationID
		}
	}
	if task != "" && prior.Task != nil && prior.Task.ID == task {
		a.projectOrganizer.Task = prior.Task
	}
	if prior.Project == project {
		a.projectOrganizer.Motion = prior.Motion
		if task == "" && prior.Task == nil {
			a.projectOrganizer.IncludeHistory = prior.IncludeHistory
			a.projectOrganizer.Sessions = append([]records.ProjectSessionSelection(nil), prior.Sessions...)
			a.projectOrganizer.HistoryLoaded = prior.HistoryLoaded
			a.projectOrganizer.HistoryWorkspaceID = prior.HistoryWorkspaceID
			a.projectOrganizer.HistoryError = prior.HistoryError
			a.projectOrganizer.HistoryUnavailable = prior.HistoryUnavailable
			a.projectOrganizer.HistoryUnattributed = prior.HistoryUnattributed
			a.projectOrganizer.Binding = prior.Binding
		}
	}
	a.projectOrganizer.Motion.Reenter = true
	a.editing, a.detailMode = false, "view"
	a.browsing.View = "memories"
	a.refreshProjectOrganizer()
}

func (a *App) closeProjectOrganizer() {
	s := &a.projectOrganizer
	s.stopWatch()
	if s.HistoryCancel != nil {
		s.HistoryCancel()
		s.HistoryCancel = nil
		s.HistoryLoading = false
		s.HistorySequence++
	}
	if s.Open && a.page == "项目" && a.scope == s.Project {
		a.editing, a.detailMode = s.ReturnEditing, s.ReturnMode
	}
	s.Open, s.Loading, s.ReturnFocus = false, false, false
	s.Motion.Reenter = true
	s.Sequence++
}

func (a *App) returnFromProjectOrganizer() {
	a.closeProjectOrganizer()
	a.projectOrganizer.ReturnFocus = true
}

func (a *App) refreshProjectOrganizer() {
	s := &a.projectOrganizer
	if a.windowHidden || !s.Open || s.Loading || a.libraryContext == nil || a.dispatch == nil || s.Operation != nil && s.Operation.Active && s.Operation.Project == s.Project {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence, project, selected := s.Sequence, s.Project, s.SelectedTask
	ownerContext, client, library := a.libraryContext, s.Client, a.library
	go func() {
		ctx, cancel := context.WithTimeout(ownerContext, 5*time.Second)
		defer cancel()
		binding, err := client.Binding(ctx, project)
		var connections []records.Connection
		var task *records.ProjectTask
		if err == nil {
			connections, err = library.Connections(ctx)
		}
		if err == nil && selected != "" {
			value, e := client.Get(ctx, selected)
			err = e
			if err == nil && value.Snapshot.Project != project {
				err = fmt.Errorf("整理任务不属于当前项目")
			}
			if err == nil {
				task = &value
			}
		}
		if err == nil && selected == "" {
			page, e := client.List(ctx, records.MergeListQuery{Project: project, ActiveOnly: true, IncludeIndexRecovery: true, Limit: 1})
			err = e
			if err == nil && len(page.Items) > 0 {
				value, e := client.Get(ctx, page.Items[0].ID)
				err = e
				if err == nil && value.Snapshot.Project != project {
					err = fmt.Errorf("整理任务不属于当前项目")
				}
				if err == nil {
					task = &value
				}
			}
		}
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			current := &a.projectOrganizer
			if !current.Open || current.Sequence != sequence || current.Project != project || current.SelectedTask != selected {
				return
			}
			current.Loading = false
			if err != nil {
				current.Error = "项目整理状态读取未完成，已加载内容保留。"
				return
			}
			if current.Binding != binding {
				current.clearHistory()
			}
			current.Binding, current.Connections, current.Task = binding, connections, task
			if task != nil {
				current.SelectedTask = task.ID
			}
			current.Error, current.Loaded = "", true
			a.ensureProjectTaskWatch()
			if op := current.Operation; op != nil && op.Project == project && op.Error != "" {
				if op.Kind == "start" && task == nil || op.Kind == "cancel" && projectTaskRunning(task) && task.State != "cancelling" || op.Kind == "recover" && (task == nil || task.IndexRecovery == nil || task.IndexRecovery.ID != op.Recovery.OperationID) {
					if op.FailureCode != "history_changed" || current.HistoryError == "" {
						current.Error = op.Error
					}
				}
			}
		})
	}()
}

func (a *App) beginProjectTask(d records.ProjectTaskStart) {
	s := &a.projectOrganizer
	if !s.Open || !s.Loaded || s.Error != "" || s.Project != d.Project || s.Binding.Revision != d.BindingRevision || s.Binding.State != "available" || s.Binding.Agent != "codex" || projectTaskRunning(s.Task) || s.Operation != nil && s.Operation.Active || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	if d.IncludeHistory && (!s.HistoryLoaded || s.HistoryLoading || s.HistoryError != "" || len(s.Sessions) == 0 || d.HistoryWorkspaceID != s.HistoryWorkspaceID || !reflect.DeepEqual(d.Sessions, s.Sessions)) {
		return
	}
	d.Sessions = append([]records.ProjectSessionSelection(nil), d.Sessions...)
	operation := &ProjectBindingOperation{Project: d.Project, Kind: "start", Active: true, Draft: d}
	s.Operation = operation
	ownerContext, client := a.libraryContext, s.Client
	go func() {
		ctx, cancel := context.WithTimeout(ownerContext, 30*time.Second)
		defer cancel()
		task, err := client.Start(ctx, d)
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			operation.Active = false
			current := &a.projectOrganizer
			if current.Operation != operation {
				return
			}
			if err != nil {
				operation.Error = "项目整理启动未确认，请重新读取当前任务后再试。"
				var remote *serviceipc.ServiceError
				if errors.As(err, &remote) {
					operation.FailureCode = remote.Code
				}
				if operation.FailureCode == "history_changed" {
					operation.Error = "所选历史会话已变化，请重新选择后再开始。"
					if current.Project == d.Project && current.Task == nil {
						current.HistoryLoaded = false
						current.HistoryError = "所选历史会话已变化，请重新读取历史选择。"
					}
				}
				if current.Open && current.Project == d.Project {
					if operation.FailureCode == "history_changed" {
						current.Error = ""
					} else {
						current.Error = operation.Error
					}
					a.refreshProjectOrganizer()
				}
				return
			}
			operation.Result = &task
			operation.Task = task.ID
			if current.Project == d.Project && (current.SelectedTask == "" || current.SelectedTask == task.ID) {
				current.SelectedTask, current.Task, current.Error = task.ID, &task, ""
				current.Sequence++
				current.Loading = false
				a.ensureProjectTaskWatch()
			}
			if overview := a.projectOverview(d.Project); overview != nil {
				overview.Task = task.ID
				overview.CheckedAt = time.Time{}
			}
		})
	}()
}

func (a *App) stopProjectTask() {
	s := &a.projectOrganizer
	if !s.Open || s.Task == nil || !projectTaskRunning(s.Task) || s.Task.State == "cancelling" || s.Operation != nil && s.Operation.Active || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	project, id, attempt := s.Project, s.Task.ID, s.Task.Attempt
	operation := &ProjectBindingOperation{Project: project, Task: id, Kind: "cancel", Active: true}
	s.Operation = operation
	ownerContext, client := a.libraryContext, s.Client
	go func() {
		ctx, cancel := context.WithTimeout(ownerContext, 10*time.Second)
		defer cancel()
		task, err := client.Cancel(ctx, id)
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			operation.Active = false
			current := &a.projectOrganizer
			if current.Operation != operation {
				return
			}
			if err != nil {
				operation.Error = "停止请求未确认，请重新读取整理状态。"
				if current.Project == project && current.SelectedTask == id {
					current.Error = operation.Error
				}
				return
			}
			operation.Result = &task
			if current.Project == project && current.SelectedTask == id && task.Attempt == attempt {
				if current.Task != nil && !projectTaskRunning(current.Task) && projectTaskRunning(&task) {
					return
				}
				current.Task = &task
				current.Error = ""
				current.Sequence++
				current.Loading = false
			}
		})
	}()
}

func projectConnectionAllowed(c records.Connection, project string) bool {
	if !c.Enabled || c.LastSeen == "" {
		return false
	}
	for _, id := range c.AllowedScopes {
		if id == project {
			return true
		}
	}
	return false
}

func (a *App) recheckProjectOrganizer() {
	s := &a.projectOrganizer
	if !s.Open || s.Loading || s.Operation != nil && s.Operation.Active {
		return
	}
	if s.Operation != nil && s.Operation.Project == s.Project {
		s.Operation.Error = ""
	}
	s.Error = ""
	a.refreshProjectOrganizer()
}

func projectAgentName(agent string) string {
	switch agent {
	case "codex":
		return "Codex"
	case "trae":
		return "TRAE"
	case "traex":
		return "traex"
	case "pi":
		return "pi"
	}
	return agent
}

func (a *App) bindProjectOrganizer(connection records.Connection) {
	s := &a.projectOrganizer
	if !s.Open || !s.Loaded || s.Error != "" || projectTaskRunning(s.Task) || !projectConnectionAllowed(connection, s.Project) || a.libraryContext == nil || s.Operation != nil && s.Operation.Active {
		return
	}
	d := records.ProjectBindingChange{Project: s.Project, ConnectionID: connection.ID, ExpectedGeneration: connection.Generation, ExpectedRevision: s.Binding.Revision}
	operation := &ProjectBindingOperation{Project: s.Project, Active: true}
	s.Operation = operation
	sequence, client := s.Sequence, s.Client
	ownerContext := a.libraryContext
	go func() {
		ctx, cancel := context.WithTimeout(ownerContext, 10*time.Second)
		defer cancel()
		binding, err := client.Bind(ctx, d)
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			operation.Active = false
			if err != nil {
				operation.Error = "绑定未保存，请重新核对连接和项目访问范围。"
			}
			current := &a.projectOrganizer
			if !current.Open || current.Project != d.Project || current.Sequence != sequence {
				return
			}
			if err == nil {
				current.Binding, current.Choosing = binding, false
				current.clearHistory()
			}
			current.Error = operation.Error
		})
	}()
}

func projectTaskRunning(task *records.ProjectTask) bool {
	if task == nil {
		return false
	}
	switch task.State {
	case "queued", "starting", "organizing", "saving", "indexing", "cancelling":
		return true
	}
	return false
}

func projectIndexRecoveryRunning(task *records.ProjectTask) bool {
	return task != nil && task.IndexRecovery != nil && task.IndexRecovery.State == "running"
}

func projectIndexRecoverable(task *records.ProjectTask) bool {
	if task == nil || projectTaskRunning(task) || projectIndexRecoveryRunning(task) || task.Error == "backup_restored" ||
		task.Receipt == nil || !task.Receipt.Persisted || task.Receipt.IndexFailed+task.Receipt.SourceIndexFailed == 0 {
		return false
	}
	if task.IndexRecovery != nil && task.IndexRecovery.State == "expired" {
		for _, item := range task.IndexRecovery.Items {
			if item.State == "failed" && item.Error != "backup_restored" {
				return true
			}
		}
		return false
	}
	return true
}

func projectTaskVisualState(task *records.ProjectTask) string {
	if task.IndexRecovery != nil {
		switch task.IndexRecovery.State {
		case "running":
			return "indexing"
		case "completed":
			if task.Receipt != nil && task.Receipt.Indexed == len(task.Receipt.Added) && task.Receipt.SourceIndexed == len(task.Receipt.Sources) {
				return "completed"
			}
			return "failed"
		case "failed", "partial_failed", "expired":
			return "failed"
		}
	}
	return task.State
}

func (a *App) recoverProjectIndexes() {
	s := &a.projectOrganizer
	if !s.Open || !s.Loaded || s.Error != "" || !projectIndexRecoverable(s.Task) || s.Operation != nil && s.Operation.Active || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	d := records.IndexRebuildStart{OperationID: uuid.NewString(), ProjectTask: s.Task.ID, Attempt: s.Task.Attempt}
	if prior := s.Operation; prior != nil && prior.Kind == "recover" && prior.Task == s.Task.ID && prior.Recovery.Attempt == s.Task.Attempt && prior.Result == nil {
		d = prior.Recovery
	}
	op := &ProjectBindingOperation{Project: s.Project, Task: s.Task.ID, Kind: "recover", Active: true, Recovery: d}
	s.Operation = op
	ownerContext, client := a.libraryContext, s.Client
	go func() {
		ctx, cancel := context.WithTimeout(ownerContext, 15*time.Second)
		defer cancel()
		recovery, err := client.RecoverIndexes(ctx, d)
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			op.Active = false
			current := &a.projectOrganizer
			if current.Operation != op {
				return
			}
			if err != nil {
				op.Error = "索引恢复未确认，请重新读取当前任务后再试。"
			}
			if current.Project != op.Project || current.Task == nil || current.Task.ID != d.ProjectTask || current.Task.Attempt != d.Attempt {
				return
			}
			current.Error = op.Error
			if err == nil {
				current.Task.IndexRecovery = &recovery
				op.Result = current.Task
				current.Sequence++
				current.Loading = false
			}
			a.refreshProjectOrganizer()
		})
	}()
}

func projectTaskStatus(state string) string {
	switch state {
	case "queued":
		return "等待启动 Agent"
	case "starting":
		return "正在启动 Agent"
	case "organizing":
		return "项目整理中"
	case "saving":
		return "正在保存知识"
	case "indexing":
		return "正在建立索引"
	case "completed":
		return "整理已完成"
	case "failed":
		return "整理失败"
	case "cancelling":
		return "停止整理中"
	case "cancelled":
		return "已停止整理"
	case "interrupted":
		return "整理已中断"
	}
	return "整理状态未报告"
}

func (a *App) projectEmptyKnowledge(c *ui.Context) {
	ui.Column(c).Key("project-knowledge-empty").Grow(1).AlignItems(ui.Center).Padding(34, 8).Gap(12).Children(func() {
		ui.Icon(c, icons["项目"]).FontSize(32).TextColor(design.Muted).Margin(16, 0)
		ui.Text(c, "还没有项目知识").FontSize(12).Bold()
		ui.Text(c, "你可以直接开始新会话，让知识逐步积累。").FontSize(10).TextColor(design.Muted)
		button := actionButton(c, "整理项目", nil).Key("project-organize-open").Margin(9, 0).Background(design.Ink).TextColor(ui.Hex("#ffffff")).OnClick(func() { a.openProjectOrganizer(a.scope, "") })
		a.projectPreviewInviting = button.Hovered() || button.FocusVisible()
		if a.projectOrganizer.ReturnFocus && a.projectOrganizer.Project == a.scope {
			button.Focus()
			a.projectOrganizer.ReturnFocus = false
		}
		ui.Text(c, "可选操作 · 会消耗模型 Token").FontSize(10).TextColor(design.Muted)
	})
}

func projectOrganizerHeading(task *records.ProjectTask) (string, string) {
	if task == nil {
		return "让 Agent 了解这个项目", "整理项目约定、架构与关键模块，并存入当前项目。"
	}
	if task.State == "organizing" && task.Snapshot.IncludeHistory {
		return "正在整理项目与历史会话", "范围已固定，已有知识保留，可随时查看知识或停止整理。"
	}
	if task.IndexRecovery != nil {
		switch task.IndexRecovery.State {
		case "running":
			return "正在恢复索引", "只处理本次已保存的知识与原文，不重新整理。"
		case "completed":
			if projectTaskVisualState(task) == "completed" {
				return "已保存内容的索引已恢复", "知识与原文版本保留，可以继续查看和使用。"
			}
			return "已保存内容的版本已变化", "恢复回执保留；当前版本的索引状态以知识详情为准。"
		case "expired":
			return "部分内容版本已变化", "旧版本不再恢复索引，当前知识与原文仍保留。"
		default:
			return "索引恢复未完成", "已保存正文仍可阅读，可以重试失败索引。"
		}
	}
	if projectIndexRecoverable(task) {
		return "知识已保存，索引未完成", "可先阅读正文，或恢复本次已保存内容的索引。"
	}
	if task.State == "failed" && task.Snapshot.IncludeHistory {
		if task.Execution == nil || task.Execution.HistoryContext == nil || task.Execution.HistoryContext.Read != len(task.Snapshot.Sessions) || task.Execution.HistoryContext.Sent != len(task.Snapshot.Sessions) {
			return "历史会话整理未完成", "本次选定的历史范围未完成，已保存知识保留，可重新选择后再整理。"
		}
	}
	switch task.State {
	case "completed":
		if task.Receipt != nil && task.Receipt.Persisted && len(task.Receipt.Added) == 0 {
			return "本次没有新增知识", "已有知识与出处保留，可回到项目继续使用。"
		}
		return "项目知识已经就绪", "可以回到项目，查看知识与对应出处。"
	case "failed":
		return "项目整理未完成", "已保存的知识保留，可查看结果或重新整理。"
	case "cancelled", "interrupted":
		return projectTaskStatus(task.State), "已保存的知识保留；重新整理会创建新的任务。"
	case "cancelling":
		return "正在停止整理", "等待 Agent 退出和服务确认，已保存的知识保留。"
	}
	return projectTaskStatus(task.State), "范围已固定，可随时查看知识或停止整理。"
}

func projectProgressSteps(c *ui.Context, task *records.ProjectTask) {
	phase := 0
	if task != nil {
		switch projectTaskVisualState(task) {
		case "queued", "starting":
			phase = 1
		case "organizing":
			phase = 2
		case "saving", "indexing":
			phase = 3
		case "completed":
			phase = 4
		case "failed":
			if task.Execution != nil && task.Execution.Terminal && task.Receipt != nil && task.Receipt.Persisted {
				phase = 3
			}
		}
	}
	ui.Row(c).Key("project-organizer-steps").Justify(ui.Center).Gap(12).Padding(16, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		organizeLabel := "整理项目"
		if task != nil && task.Snapshot.IncludeHistory {
			organizeLabel = "项目与历史会话"
		}
		for i, label := range []string{"启动 Agent", organizeLabel, "存入知识"} {
			mark, color := fmt.Sprint(i+1), design.Muted
			if phase > i+1 {
				mark, color = "✓", ui.Hex("#7493ad")
			} else if phase == i+1 {
				color = ui.Hex("#476c8e")
			}
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mark).Size(17, 17).Radius(9).Border(1, design.Border).FontSize(9).TextColor(color).TextAlign(ui.Center)
				ui.Text(c, label).FontSize(10).TextColor(color)
			})
		}
	})
}

func (a *App) projectOrganizerContent(c *ui.Context) {
	s := &a.projectOrganizer
	busy := projectTaskRunning(s.Task) || projectIndexRecoveryRunning(s.Task) || s.Operation != nil && s.Operation.Active
	frozen := s.Task != nil
	heading, description := projectOrganizerHeading(s.Task)
	padding, gap := float32(24), float32(16)
	width, height := c.Size()
	if width < 1100 || height < 740 {
		padding, gap = 12, 8
	}
	ui.Column(c).Key("project-organizer-layout").Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("project-organizer-body")).Grow(1).MinHeight(0).Padding(padding).Gap(gap).Children(func() {
			ui.Text(c, heading).Key("project-organizer-task-state").FontSize(design.Title).Bold()
			ui.Text(c, description).FontSize(11).TextColor(design.Muted)
			if s.Loading && !s.Loaded {
				ui.Text(c, "正在读取项目绑定与访问状态…").FontSize(11)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(11)
				quietButton(c, "重新读取").Key("project-organizer-retry").Disabled(s.Loading || s.Operation != nil && s.Operation.Active).OnClick(a.recheckProjectOrganizer)
			}
			ui.Row(c).Padding(14).Gap(10).Radius(6).Border(1, design.Border).Children(func() {
				ui.Icon(c, icons["Agent"]).FontSize(20).TextColor(design.Muted)
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					name, state := "尚未绑定 Agent", "选择已连接且获准访问本项目的 Agent"
					if s.Binding.ConnectionID != "" {
						name = projectAgentName(s.Binding.Agent)
						state = "连接或范围已变化，请重新验证后绑定"
						if s.Binding.State == "available" {
							state = "已连接 · 当前项目可用"
						}
					}
					if frozen {
						name = projectAgentName(s.Task.Snapshot.Executor.Agent)
						state = "固定范围：" + a.scopeLabel(s.Task.Snapshot.Project) + " · " + s.Task.Snapshot.Executor.Model
					}
					ui.Text(c, name).Key("project-organizer-agent").FontSize(12).Bold()
					ui.Text(c, state).Key("project-organizer-access-state").FontSize(10).TextColor(design.Muted)
				})
				label := "选择 Agent"
				if s.Binding.ConnectionID != "" {
					label = "更换"
				}
				if !frozen {
					quietButton(c, label).Key("project-organizer-choose").Disabled(busy || !s.Loaded || s.Error != "").OnClick(func() { s.Choosing = !s.Choosing })
				}
			})
			if s.Choosing && !frozen {
				for _, connection := range s.Connections {
					allowed := projectConnectionAllowed(connection, s.Project)
					ui.Column(c).Gap(5).Children(func() {
						quietButton(c, projectAgentName(connection.Agent)+" · "+connection.Name).Key("project-organizer-" + connection.ID).Disabled(busy || !allowed || connection.ID == s.Binding.ConnectionID && s.Binding.State == "available").OnClick(func() { a.bindProjectOrganizer(connection) })
						if !allowed {
							ui.Text(c, "需要先验证连接并授权当前项目。").FontSize(10).TextColor(design.Muted)
						}
					})
				}
				quietButton(c, "前往 Agent 连接").OnClick(func() { a.navigate("设置", ""); a.settingsSection = "agents" })
			}
			ui.Column(c).Padding(12).Gap(6).Radius(6).Border(1, design.Border).Children(func() {
				history := s.IncludeHistory
				if frozen {
					history = s.Task.Snapshot.IncludeHistory
				}
				ui.Checkbox(c.Key("project-organizer-history"), &history, "同时整理历史会话").Disabled(frozen || busy || !s.Loaded || s.Binding.State != "available" || s.Binding.Agent != "codex" || a.startProjectTask == nil).OnChange(func() { a.chooseProjectHistory(history) })
				historyText := "当前绑定 Agent 的历史会话暂不可读取。"
				if !frozen && s.Binding.State == "available" && s.Binding.Agent == "codex" && a.startProjectTask != nil {
					historyText = "仅整理 Codex 在当前项目的已完成会话，保留原始出处。"
				}
				if frozen && !history {
					historyText = "本次仅整理当前仓库，未包含历史会话。"
				}
				if frozen && history {
					historyText = fmt.Sprintf("本次固定选择 %d 个历史会话，历史会话会增加用量。", len(s.Task.Snapshot.Sessions))
				}
				if !frozen && history {
					if s.HistoryLoading {
						historyText = "正在读取当前项目的历史会话…"
					} else if s.HistoryLoaded {
						historyText = fmt.Sprintf("已选择 %d 个已完成会话 · 历史会话会增加用量。", len(s.Sessions))
						if s.HistoryUnavailable > 0 || s.HistoryUnattributed > 0 {
							ui.Text(c, fmt.Sprintf("%d 项当前项目会话暂不可读取，%d 项无法确认归属，均未纳入。", s.HistoryUnavailable, s.HistoryUnattributed)).FontSize(10).TextColor(design.Muted)
						}
						if len(s.Sessions) == 0 {
							historyText = "没有可读取的已完成会话，可取消历史选择后仅整理仓库。"
						}
					}
					if s.HistoryError != "" {
						ui.Text(c, s.HistoryError).Key("project-history-error").FontSize(10)
						quietButton(c, "重新读取历史会话").Key("project-history-reload").Disabled(s.HistoryLoading).OnClick(func() { a.chooseProjectHistory(true) })
					}
				}
				ui.Text(c, historyText).FontSize(10).TextColor(design.Muted)
			})
			a.projectStage(c)
			projectProgressSteps(c, s.Task)
			if s.Task != nil {
				if receipt := s.Task.Receipt; receipt != nil && receipt.Persisted {
					ui.Text(c, fmt.Sprintf("已保存 %d 条知识 · %d 条已建立索引", len(receipt.Added), receipt.Indexed)).Key("project-organizer-result").FontSize(10).TextColor(design.Muted)
					if receipt.SaveFailed > 0 || receipt.IndexFailed > 0 || receipt.SourceIndexFailed > 0 {
						ui.Text(c, "部分保存或索引未完成，已保存正文仍可阅读。").FontSize(10).TextColor(design.Muted)
					}
				}
			}
		})
		ui.Row(c).Shrink(0).Padding(14, 24).Gap(14).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			if projectIndexRecoveryRunning(s.Task) || projectIndexRecoverable(s.Task) || s.Task != nil && s.Task.IndexRecovery != nil {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, "知识与原文已保存").FontSize(10).Bold()
					ui.Text(c, "恢复索引不调用模型，不消耗整理 Token。").FontSize(10).TextColor(design.Muted)
				})
				if projectIndexRecoverable(s.Task) {
					actionButton(c, "恢复索引", nil).Key("project-organizer-index-recover").Disabled(busy || !s.Loaded || s.Error != "").OnClick(a.recoverProjectIndexes)
				} else if projectIndexRecoveryRunning(s.Task) {
					actionButton(c, "正在恢复索引…", nil).Key("project-organizer-index-recover").Disabled(true)
				} else {
					actionButton(c, "查看项目知识", nil).Key("project-organizer-view").OnClick(a.returnFromProjectOrganizer)
				}
				return
			}
			if s.Task != nil && s.Task.State == "completed" {
				ui.Spacer(c)
				actionButton(c, "查看项目知识", nil).Key("project-organizer-view").Background(design.Ink).TextColor(ui.Hex("#ffffff")).OnClick(a.returnFromProjectOrganizer)
				return
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "会消耗 Token").FontSize(10).Bold()
				ui.Text(c, "使用绑定 Agent 当前模型，用量随整理内容变化。").FontSize(10).TextColor(design.Muted)
				if s.IncludeHistory || frozen && s.Task.Snapshot.IncludeHistory {
					ui.Text(c, "历史会话会增加用量。").FontSize(10).TextColor(design.Muted)
				}
				if a.startProjectTask == nil {
					ui.Text(c, "项目整理暂不可用。").Key("project-organizer-unavailable").FontSize(10).TextColor(design.Muted)
				}
				if s.Binding.State == "available" && s.Binding.Agent != "codex" && !frozen {
					ui.Text(c, "当前 Agent 的项目整理暂不可用。").FontSize(10).TextColor(design.Muted)
				}
			})
			if projectTaskRunning(s.Task) {
				label := "停止整理"
				if s.Task.State == "cancelling" || s.Operation != nil && s.Operation.Active && s.Operation.Kind == "cancel" {
					label = "停止整理中…"
				}
				actionButton(c, label, nil).Key("project-organizer-stop").Disabled(s.Task.State == "cancelling" || s.Operation != nil && s.Operation.Active).OnClick(a.stopProjectTask)
				return
			}
			if frozen {
				quietButton(c, "重新整理").Key("project-organizer-reset").Disabled(busy || !s.Loaded || s.Error != "").OnClick(func() {
					s.stopWatch()
					s.Sequence++
					s.Loading = false
					s.Task, s.SelectedTask, s.Choosing = nil, "", false
					s.Operation = nil
					s.OperationID = uuid.NewString()
					s.clearHistory()
					s.Motion.Reenter = true
					a.refreshProjectOrganizer()
				})
				return
			}
			label := "先绑定 Agent"
			if s.Binding.State == "available" {
				label = "使用 " + projectAgentName(s.Binding.Agent) + " 整理"
			}
			if s.Operation != nil && s.Operation.Active && s.Operation.Kind == "start" {
				label = "正在确认启动…"
			}
			actionButton(c, label, nil).Key("project-organizer-start").Background(design.Ink).TextColor(ui.Hex("#ffffff")).Disabled(busy || !s.Loaded || s.Binding.State != "available" || s.Binding.Agent != "codex" || s.Error != "" || a.startProjectTask == nil || s.IncludeHistory && (!s.HistoryLoaded || s.HistoryLoading || s.HistoryError != "" || len(s.Sessions) == 0)).OnClick(func() {
				if a.startProjectTask != nil && s.Binding.State == "available" {
					a.startProjectTask(records.ProjectTaskStart{OperationID: s.OperationID, Project: s.Project, BindingRevision: s.Binding.Revision, IncludeHistory: s.IncludeHistory, Sessions: s.Sessions, HistoryWorkspaceID: s.HistoryWorkspaceID})
				}
			})
		})
	})
}
