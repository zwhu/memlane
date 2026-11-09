package desktop

import (
	"context"
	"fmt"
	"math"
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
			current.Error, current.Loaded = "", true
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
	if !s.Open || !s.Loaded || s.Error != "" || !projectConnectionAllowed(connection, s.Project) || a.libraryContext == nil || s.Operation != nil && s.Operation.Active {
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
		if a.projectOrganizer.ReturnFocus && a.projectOrganizer.Project == a.scope {
			button.Focus()
			a.projectOrganizer.ReturnFocus = false
		}
		ui.Text(c, "可选操作 · 会消耗模型 Token").FontSize(10).TextColor(design.Muted)
	})
}

// The static geometry is shared by empty/configuration views. Stage motion is
// attached only after the real task owner supplies lifecycle snapshots.
func projectKnowledgeStage(c *ui.Context) {
	ui.Box(c).Key("project-knowledge-stage").Height(245).Grow(1).MinHeight(150).Draw(func(p *ui.Painter, r ui.Rect) {
		scale := min(r.W/560, r.H/280)
		cx, cy := r.X+r.W*.47, r.Y+r.H*.53
		for i := 0; i < 24; i++ {
			angle, radius := float64(i)*2.39996, 34+math.Sqrt(float64(i))*15
			x := cx + float32(math.Cos(angle)*radius)*scale
			y := cy + float32(math.Sin(angle)*radius*.66)*scale
			w, depth := 14*scale, 5*scale
			p.Fill(ui.Rect{X: x, Y: y, W: w, H: w}, ui.Hex("#b5cadc"), scale)
			top := (&ui.Path{}).MoveTo(x, y).LineTo(x+depth, y-depth).LineTo(x+w+depth, y-depth).LineTo(x+w, y).Close()
			side := (&ui.Path{}).MoveTo(x+w, y).LineTo(x+w+depth, y-depth).LineTo(x+w+depth, y+w-depth).LineTo(x+w, y+w).Close()
			p.FillPath(top, ui.Hex("#dce7f1"))
			p.FillPath(side, ui.Hex("#7995ae"))
		}
	})
}

func (a *App) projectOrganizerContent(c *ui.Context) {
	s := &a.projectOrganizer
	busy := projectTaskRunning(s.Task) || s.Operation != nil && s.Operation.Active
	ui.Column(c).Key("project-organizer-layout").Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("project-organizer-body")).Grow(1).MinHeight(0).Padding(24).Gap(16).Children(func() {
			ui.Text(c, "让 Agent 了解这个项目").FontSize(design.Title).Bold()
			ui.Text(c, "整理项目约定、架构与关键模块，并存入当前项目。").FontSize(11).TextColor(design.Muted)
			if s.Loading && !s.Loaded {
				ui.Text(c, "正在读取项目绑定与访问状态…").FontSize(11)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(11)
				quietButton(c, "重新读取").Key("project-organizer-retry").Disabled(s.Loading).OnClick(a.refreshProjectOrganizer)
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
					ui.Text(c, name).Key("project-organizer-agent").FontSize(12).Bold()
					ui.Text(c, state).Key("project-organizer-access-state").FontSize(10).TextColor(design.Muted)
				})
				label := "选择 Agent"
				if s.Binding.ConnectionID != "" {
					label = "更换"
				}
				quietButton(c, label).Key("project-organizer-choose").Disabled(busy || !s.Loaded || s.Error != "").OnClick(func() { s.Choosing = !s.Choosing })
			})
			if s.Choosing {
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
				ui.Checkbox(c.Key("project-organizer-history"), &s.IncludeHistory, "同时整理历史会话").Disabled(true)
				ui.Text(c, "当前绑定 Agent 的历史会话暂不可读取。").FontSize(10).TextColor(design.Muted)
			})
			projectKnowledgeStage(c)
			if s.Task != nil {
				ui.Text(c, projectTaskStatus(s.Task.State)).Key("project-organizer-task-state").FontSize(11)
				ui.Text(c, "固定范围："+a.scopeLabel(s.Task.Snapshot.Project)+" · "+projectAgentName(s.Task.Snapshot.Executor.Agent)).FontSize(10).TextColor(design.Muted)
			} else {
				ui.Text(c, "① 启动 Agent　② 整理项目　③ 存入知识").FontSize(10).TextColor(design.Muted)
			}
		})
		ui.Row(c).Shrink(0).Padding(14, 24).Gap(14).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "会消耗 Token").FontSize(10).Bold()
				ui.Text(c, "使用绑定 Agent 当前模型，用量随整理内容变化。").FontSize(10).TextColor(design.Muted)
				if a.startProjectTask == nil {
					ui.Text(c, "项目整理暂不可用。").Key("project-organizer-unavailable").FontSize(10).TextColor(design.Muted)
				}
			})
			label := "先绑定 Agent"
			if s.Binding.State == "available" {
				label = "使用 " + projectAgentName(s.Binding.Agent) + " 整理"
			}
			actionButton(c, label, nil).Key("project-organizer-start").Disabled(busy || s.Binding.State != "available" || s.Error != "" || a.startProjectTask == nil).OnClick(func() {
				if a.startProjectTask != nil && s.Binding.State == "available" {
					a.startProjectTask(records.ProjectTaskStart{OperationID: s.OperationID, Project: s.Project, BindingRevision: s.Binding.Revision, IncludeHistory: s.IncludeHistory, Sessions: s.Sessions})
				}
			})
		})
	})
}
