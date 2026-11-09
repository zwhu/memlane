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

type ProjectBindingOperation struct {
	Project string
	Active  bool
	Error   string
	Kind    string
	Task    string
	Draft   records.ProjectTaskStart
	Result  *records.ProjectTask
}

type ProjectOrganizerUI struct {
	Client                       serviceipc.ProjectTaskClient
	Open, Loaded, Loading        bool
	Choosing, IncludeHistory     bool
	ReturnEditing, ReturnFocus   bool
	ReturnMode                   string
	Project, SelectedTask, Error string
	OperationID                  string
	Sequence                     uint64
	Binding                      records.ProjectBinding
	Connections                  []records.Connection
	Sessions                     []records.ProjectSessionSelection
	Task                         *records.ProjectTask
	Operation                    *ProjectBindingOperation
	Scroll                       ui.ScrollState
	Motion                       projectStageMotion
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
		Open: true, Sequence: prior.Sequence + 1, Operation: prior.Operation,
		OperationID: uuid.NewString(), ReturnMode: a.detailMode, ReturnEditing: a.editing}
	if prior.Operation != nil && prior.Operation.Project == project {
		if prior.Operation.Active && prior.Operation.Kind == "start" {
			a.projectOrganizer.OperationID = prior.Operation.Draft.OperationID
		}
		if task == "" && prior.Operation.Result != nil && projectTaskRunning(prior.Operation.Result) {
			a.projectOrganizer.SelectedTask = prior.Operation.Result.ID
		}
	}
	if task != "" && prior.Task != nil && prior.Task.ID == task {
		a.projectOrganizer.Task = prior.Task
	}
	if prior.Project == project {
		a.projectOrganizer.Motion = prior.Motion
	}
	a.projectOrganizer.Motion.Reenter = true
	a.editing, a.detailMode = false, "view"
	a.browsing.View = "memories"
	a.refreshProjectOrganizer()
}

func (a *App) closeProjectOrganizer() {
	s := &a.projectOrganizer
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
	if !s.Open || s.Loading || a.libraryContext == nil || a.dispatch == nil || s.Operation != nil && s.Operation.Active && s.Operation.Project == s.Project {
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
			page, e := client.List(ctx, records.MergeListQuery{Project: project, ActiveOnly: true, Limit: 1})
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
			if current.Binding.Revision != binding.Revision || current.Binding.Generation != binding.Generation {
				current.IncludeHistory, current.Sessions = false, nil
			}
			current.Binding, current.Connections, current.Task = binding, connections, task
			if task != nil {
				current.SelectedTask = task.ID
			}
			current.Error, current.Loaded = "", true
			if op := current.Operation; op != nil && op.Project == project && op.Error != "" {
				if op.Kind == "start" && task == nil || op.Kind == "cancel" && projectTaskRunning(task) && task.State != "cancelling" {
					current.Error = op.Error
				}
			}
		})
	}()
}

func (a *App) beginProjectTask(d records.ProjectTaskStart) {
	s := &a.projectOrganizer
	if !s.Open || !s.Loaded || s.Loading || s.Error != "" || s.Project != d.Project || s.Binding.Revision != d.BindingRevision || s.Binding.State != "available" || s.Binding.Agent != "codex" || projectTaskRunning(s.Task) || s.Operation != nil && s.Operation.Active || a.libraryContext == nil || a.dispatch == nil {
		return
	}
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
				if current.Open && current.Project == d.Project {
					current.Error = operation.Error
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
				current.IncludeHistory, current.Sessions = false, nil
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
		switch task.State {
		case "queued", "starting":
			phase = 1
		case "organizing":
			phase = 2
		case "saving", "indexing":
			phase = 3
		case "completed":
			phase = 4
		}
	}
	ui.Row(c).Key("project-organizer-steps").Justify(ui.Center).Gap(12).Padding(16, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		for i, label := range []string{"启动 Agent", "整理项目", "存入知识"} {
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
	busy := projectTaskRunning(s.Task) || s.Operation != nil && s.Operation.Active
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
				// Historical reads stay unavailable until a scoped reader is attached.
				history := s.IncludeHistory
				if frozen {
					history = s.Task.Snapshot.IncludeHistory
				}
				ui.Checkbox(c.Key("project-organizer-history"), &history, "同时整理历史会话").Disabled(true)
				historyText := "当前绑定 Agent 的历史会话暂不可读取。"
				if frozen && !history {
					historyText = "本次仅整理当前仓库，未包含历史会话。"
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
			if s.Task != nil && s.Task.State == "completed" {
				ui.Spacer(c)
				actionButton(c, "查看项目知识", nil).Key("project-organizer-view").Background(design.Ink).TextColor(ui.Hex("#ffffff")).OnClick(a.returnFromProjectOrganizer)
				return
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "会消耗 Token").FontSize(10).Bold()
				ui.Text(c, "使用绑定 Agent 当前模型，用量随整理内容变化。").FontSize(10).TextColor(design.Muted)
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
				quietButton(c, "重新整理").Key("project-organizer-reset").Disabled(busy || s.Loading || s.Error != "").OnClick(func() {
					s.Task, s.SelectedTask, s.Choosing = nil, "", false
					s.OperationID = uuid.NewString()
					s.IncludeHistory, s.Sessions = false, nil
					s.Motion.Reenter = true
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
			actionButton(c, label, nil).Key("project-organizer-start").Background(design.Ink).TextColor(ui.Hex("#ffffff")).Disabled(busy || !s.Loaded || s.Loading || s.Binding.State != "available" || s.Binding.Agent != "codex" || s.Error != "" || a.startProjectTask == nil).OnClick(func() {
				if a.startProjectTask != nil && s.Binding.State == "available" {
					a.startProjectTask(records.ProjectTaskStart{OperationID: s.OperationID, Project: s.Project, BindingRevision: s.Binding.Revision, IncludeHistory: s.IncludeHistory, Sessions: s.Sessions})
				}
			})
		})
	})
}
