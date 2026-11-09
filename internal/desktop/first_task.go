package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

// The first task retains the host-confirmed connection. A workspace is an
// authorization input; it is not an empty project registration.
func (a *App) openFirstTask(connection records.Connection) {
	if a.cognee.Cancel != nil {
		a.cognee.Cancel()
	}
	project := connection.Project
	if project == "" {
		project = connection.PendingProject
	}
	a.cognee = CogneeUI{FirstTask: true, Project: project, Workspace: connection.TrustedWorkspace, Connection: &connection, Sequence: a.cognee.Sequence + 1}
	if a.cognee.Workspace == "" {
		for _, scope := range a.scopes {
			if scope.ID == project {
				a.cognee.Workspace = scope.Path
			}
		}
	}
	a.loadOfficialAgent()
	a.navigate("官方记忆", "")
	if project != "" {
		a.refreshCogneeBindings()
	}
}

func (a *App) confirmFirstTaskWorkspace() {
	s := &a.cognee
	if s.Busy || s.Connection == nil || strings.TrimSpace(s.Workspace) == "" {
		return
	}
	connection := *s.Connection
	workspace := strings.TrimSpace(s.Workspace)
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	s.AgentStage = "正在核对本轮任务的真实仓库范围…"
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 20*time.Second)
		defer cancel()
		var err error
		if connection.Project != "" {
			err = a.library.CheckWorkspace(ctx, connection.Project, workspace)
		} else {
			connection, err = a.library.ConfigureConnection(ctx, records.ConnectionDraft{ID: connection.ID, Agent: connection.Agent, Name: connection.Name, AllowedScopes: connection.AllowedScopes, AutoProject: true, Workspace: workspace})
		}
		a.dispatch(func() {
			if s.Sequence != sequence {
				return
			}
			s.Busy = false
			if err != nil {
				s.AgentStage = "工作目录未确认：" + err.Error()
				return
			}
			s.Connection = &connection
			s.Workspace = workspace
			if connection.TrustedWorkspace != "" {
				s.Workspace = connection.TrustedWorkspace
			}
			s.Project = connection.Project
			if s.Project == "" {
				s.Project = connection.PendingProject
			}
			a.loadOfficialAgent()
			s.AgentStage = "工作目录与接入范围已确认。接下来准备官方环境和插件；只有有效材料到达后才建立项目。"
		})
	}()
}

func (a *App) firstTaskPage(c *ui.Context) {
	s := &a.cognee
	ui.Scroll(c.Key("first-task-page")).Grow(1).Padding(24).Gap(18).Children(func() {
		ui.Text(c, "让 Codex 完成第一项任务").FontSize(design.Title).Bold()
		ui.Text(c, "确认本轮工作目录和采集范围，使用官方插件记录这项真实任务，再查看产生的记忆与出处。").LineHeight(1.7)
		if s.Connection == nil || !s.Connection.Enabled {
			a.empty(c, "接入尚未确认", "返回 Agent 页面重新连接后继续。")
			return
		}
		if s.Project == "" {
			ui.Text(c, "1 · 本轮任务的工作目录").Bold()
			ui.Text(c, "项目选择已跳过。这里仅授权实际任务所在的仓库，连接和准备不会创建空项目。").FontSize(design.Small).LineHeight(1.7)
			ui.TextInput(c.Key("official-agent-workspace"), &s.Workspace).Label("任务工作目录").Placeholder("本地 Git 仓库或 worktree 的绝对路径").Disabled(s.Busy)
			actionButton(c, "确认本轮工作目录", nil).Key("official-workspace-confirm").Disabled(s.Busy || strings.TrimSpace(s.Workspace) == "").OnClick(a.confirmFirstTaskWorkspace)
			if s.AgentStage != "" {
				ui.Text(c, s.AgentStage).Key("official-agent-result").Selectable().LineHeight(1.7)
			}
			return
		}
		meta(c, "本轮工作目录", s.Workspace)
		if name := a.scopeLabel(s.Project); name != "" {
			meta(c, "项目", name)
		} else {
			ui.Text(c, "尚未建立项目；首次有效材料到达后按该仓库建立或复用。").FontSize(design.Small).TextColor(design.Muted)
		}
		if a.officialPreparation.Status.Phase != "ready" {
			ui.Text(c, "2 · 验证提炼 Agent").Bold()
			a.cogneePreparation(c)
			return
		}
		a.cogneeAgent(c)
		if s.Receipt != nil {
			ui.Divider(c)
			ui.Text(c, "本次产生的记忆").Bold()
			ui.Text(c, fmt.Sprintf("%d 份采集分段 · %d 条知识", len(s.Receipt.Raw), len(s.Receipt.Knowledge))).Key("first-task-memory-counts")
			if s.Receipt.SessionOriginal != nil {
				ui.Text(c, "完整会话原文已保留，可以从知识的出处查看。").FontSize(design.Small)
			}
			for _, item := range s.Receipt.Knowledge {
				memory := item.Record.Memory
				quietButton(c, item.Record.Title).Key("first-task-memory-" + memory).OnClick(func() {
					a.navigate("项目", s.Project)
					a.browseView("memories")
					a.browseMemory(memory)
				})
			}
			if len(s.Receipt.Raw) > 0 {
				actionButton(c, "查看记忆与出处", nil).Key("cognee-browse").Disabled(s.Busy).OnClick(func() { a.navigate("项目", s.Project); a.browseView("memories") })
			}
		}
		if s.Message != "" {
			ui.Text(c, s.Message).Key("cognee-result").Selectable().LineHeight(1.7)
		}
		quietButton(c, "返回 Agent").Key("first-task-back").Disabled(s.Busy || a.officialPreparation.Busy).OnClick(func() { a.navigate("Agent", ""); a.refreshAgents(false) })
	})
}

// Attach current-knowledge reads only after an actual import. The official
// plugin remains responsible for collection; its historical graph is not
// silently rewritten when the user edits a Memlane version.
func (a *App) enableFirstTaskCurrentKnowledge(ctx context.Context, agent official.AgentScope, binding records.CogneeBinding) error {
	connections, err := a.library.Connections(ctx)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if connection.ID == binding.ConnectionID && connection.Enabled && connection.Generation == binding.Generation && connection.Project == agent.Project {
			bridge := a.agents.Bridge
			if bridge == "" {
				return fmt.Errorf("当前知识读取程序尚未配置")
			}
			return a.officialController().EnableCurrentKnowledge(ctx, agent, connection, filepath.Clean(a.library.Root), bridge)
		}
	}
	return fmt.Errorf("接入范围已变化，当前知识读取未启用")
}
