package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) officialController() *official.Controller {
	if a.firstLaunch.Controller == nil {
		return nil
	}
	return a.firstLaunch.Controller.Official
}
func (a *App) loadOfficialAgent() {
	c := a.officialController()
	if c == nil {
		return
	}
	s := &a.cognee
	s.Agent = nil
	s.TaskPrompt = ""
	s.TaskOutput = ""
	s.AgentStage = ""
	if d, err := c.LoadAgent(s.Project); err == nil {
		s.Agent = &d
		s.AgentExecutable = d.Executable
		s.AuthDirectory = d.AuthDirectory
		return
	}
	var paths map[string]string
	if raw, err := os.ReadFile(filepath.Join(a.firstLaunch.Controller.Config.Root, "agent-paths.json")); err == nil && len(raw) < 65536 {
		_ = json.Unmarshal(raw, &paths)
		s.AgentExecutable = paths["codex"]
	}
	if s.AgentExecutable == "" {
		for _, found := range a.firstLaunch.State.Agents {
			if found.Agent == "codex" && found.Path != "" && found.ConnectionError() == nil {
				s.AgentExecutable = found.Path
				break
			}
		}
	}
	if s.AuthDirectory == "" {
		s.AuthDirectory = os.Getenv("MEMLANE_CODEX_AUTH_DIRECTORY")
		if s.AuthDirectory == "" && a.agents.Home != "" {
			s.AuthDirectory = filepath.Join(a.agents.Home, ".codex")
		}
	}
}
func (a *App) prepareOfficialAgent() {
	s := &a.cognee
	c := a.officialController()
	if c == nil || s.Busy || s.Project == "" {
		return
	}
	var workspace string
	if s.FirstTask {
		workspace = s.Workspace
	}
	for _, project := range a.scopes {
		if project.ID == s.Project {
			workspace = project.Path
		}
	}
	d := official.AgentScope{Project: s.Project, Workspace: workspace, Executable: s.AgentExecutable, AuthDirectory: s.AuthDirectory}
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	s.AgentStage = "正在验证实际 Agent 和准备项目独立插件…"
	ctx, cancel := context.WithTimeout(a.libraryContext, 2*time.Minute)
	s.Cancel = cancel
	go func() {
		defer cancel()
		installed, err := c.PrepareAgent(ctx, d)
		a.dispatch(func() {
			if s.Sequence != sequence || s.Project != d.Project {
				return
			}
			s.Busy = false
			s.Cancel = nil
			if err != nil {
				s.AgentStage = "接入未完成：" + err.Error()
				return
			}
			s.Agent = &installed
			s.AgentStage = "官方插件已安装与配置。首次正常任务和保存尚未验证。"
		})
	}()
}
func (a *App) runOfficialAgent() {
	s := &a.cognee
	c := a.officialController()
	if c == nil || s.Busy || s.Agent == nil || s.TaskPrompt == "" {
		return
	}
	d, prompt := *s.Agent, s.TaskPrompt
	confirmed := s.Connection
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	s.TaskOutput = ""
	s.AgentStage = "真实 Agent 正在此项目执行已确认任务；官方插件仅采集本轮对话与工具轨迹…"
	ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Minute)
	s.Cancel = cancel
	go func() {
		defer cancel()
		var result official.AgentResult
		var err error
		if confirmed != nil {
			connections, e := a.library.Connections(ctx)
			err = e
			active := false
			for _, connection := range connections {
				if connection.ID == confirmed.ID && connection.Enabled && connection.Generation == confirmed.Generation {
					active = true
				}
			}
			if err == nil && !active {
				err = fmt.Errorf("本轮接入已撤销或改变，请返回 Agent 重新连接")
			}
		}
		if err == nil {
			result, err = c.RunAgent(ctx, d, prompt, func(message string) {
				a.dispatch(func() {
					if s.Sequence == sequence && s.Project == d.Project {
						s.TaskOutput = message
					}
				})
			})
		}
		a.dispatch(func() {
			if s.Sequence != sequence || s.Project != d.Project {
				return
			}
			s.Busy = false
			s.Cancel = nil
			s.Session = result.Session
			if err != nil {
				s.AgentStage = fmt.Sprintf("任务未完成（退出 %d）：%s。官方服务可能已提交部分材料，尚未导入 Memlane。", result.ExitCode, err)
				return
			}
			s.AgentStage = "正常任务已结束。请完成官方持久同步，再接收具体会话材料。"
			if s.FirstTask {
				a.finishOfficialSession()
			}
		})
	}()
}
func (a *App) finishOfficialSession() {
	s := &a.cognee
	c := a.officialController()
	if c == nil || s.Busy || s.Agent == nil || s.Session == "" {
		return
	}
	d, session := *s.Agent, s.Session
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	s.AgentStage = "等待官方结束同步与提炼回执…"
	ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Minute)
	s.Cancel = cancel
	projectName := a.scopeLabel(s.Project)
	confirmed := s.Connection
	go func() {
		defer cancel()
		err := c.SyncSession(ctx, d, session)
		var dataset, token string
		if err == nil {
			dataset, token, err = c.Dataset(ctx, d)
		}
		var binding records.CogneeBinding
		if err == nil {
			bindings, e := a.library.CogneeBindings(ctx, d.Project)
			err = e
			for _, b := range bindings {
				if b.Dataset == dataset {
					binding = b
					break
				}
			}
			if err == nil && binding.Operation == "" {
				connections, e := a.library.Connections(ctx)
				err = e
				var connection records.Connection
				for _, existing := range connections {
					if confirmed != nil && existing.ID == confirmed.ID && existing.Generation == confirmed.Generation && existing.Enabled {
						connection = existing
						break
					}
					if confirmed == nil && existing.Agent == "codex" && existing.Project == d.Project && existing.Enabled {
						connection = existing
						break
					}
				}
				if err == nil && confirmed != nil && connection.ID == "" {
					err = &records.AccessError{Code: "disconnected", Message: "本轮接入已撤销或改变，请重新连接；官方材料独立保留"}
				}
				if err == nil && connection.ID == "" {
					connection, err = a.library.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "官方 Cognee · " + projectName, Project: d.Project, AllowedScopes: []string{d.Project}})
				}
				if err == nil {
					state, e := c.ReadState()
					err = e
					if err == nil {
						binding, err = a.library.AuthorizeCognee(ctx, records.CogneeAuthorization{Binding: records.CogneeBinding{Operation: uuid.NewString(), Dataset: dataset, Project: d.Project, Endpoint: state.Endpoint, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: d.Workspace}, Credential: token})
					}
				}
			}
		}
		if err == nil {
			connections, e := a.library.Connections(ctx)
			err = e
			active := false
			for _, connection := range connections {
				if connection.ID == binding.ConnectionID && connection.Enabled && connection.Generation == binding.Generation {
					active = true
				}
			}
			if err == nil && !active {
				err = &records.AccessError{Code: "disconnected", Message: "Memlane 接入已撤销；官方提交独立保留，不继续接收该范围"}
			}
		}
		token = ""
		cancelled := ctx.Err() != nil
		a.dispatch(func() {
			if s.Sequence != sequence || s.Project != d.Project {
				return
			}
			s.Busy = false
			s.Cancel = nil
			if cancelled {
				s.AgentStage = "本轮操作已取消；官方服务已提交的材料保留，未继续导入 Memlane。"
				return
			}
			if err != nil {
				s.AgentStage = "官方同步或授权未完成：" + err.Error()
				return
			}
			s.Binding = &binding
			s.Authorized = true
			s.Bindings = []records.CogneeBinding{binding}
			s.Dataset = dataset
			s.Endpoint = binding.Endpoint
			s.AgentStage = "官方结束同步已返回；服务与项目 dataset 已正式授权。接下来读取完整持久材料进入 Memlane。"
			a.syncCognee()
		})
	}()
}
func (a *App) cogneeAgent(c *ui.Context) {
	s := &a.cognee
	controller := a.officialController()
	if controller == nil {
		return
	}
	ui.Divider(c)
	ui.Text(c, "首次官方 Agent 使用").Bold()
	ui.Text(c, "只在所选真实项目执行你确认的任务。此独立配置启用官方采集；已有聊天不导入，日常 Agent 配置不修改。").FontSize(design.Small).LineHeight(1.7)
	if s.Agent == nil {
		ui.TextInput(c.Key("official-agent-executable"), &s.AgentExecutable).Placeholder("已安装的 Codex 可执行文件").Disabled(s.Busy)
		ui.TextInput(c.Key("official-agent-auth"), &s.AuthDirectory).Placeholder("当前 Codex 登录与模型配置目录（.codex）").Disabled(s.Busy)
		ui.Text(c, "确认后安装官方 1.7.7 插件到此 App 的项目独立目录；复用该 Agent 的模型选择，登录仅在执行任务期间原样提供给 Agent。").FontSize(design.Small).LineHeight(1.7)
		actionButton(c, "确认本轮范围并准备官方插件", nil).Key("official-agent-prepare").Disabled(s.Busy || s.Project == "" || a.officialPreparation.Status.Phase != "ready").OnClick(a.prepareOfficialAgent)
	} else {
		meta(c, "真实 Agent", s.Agent.Version)
		meta(c, "沿用模型", s.Agent.Model)
		meta(c, "项目范围", a.scopeLabel(s.Project))
		ui.TextArea(c.Key("official-agent-task"), &s.TaskPrompt).Placeholder("输入本轮正常任务；只有该项目与本轮会话被采集").Height(130).Disabled(s.Busy)
		actionButton(c, "确认采集并运行此任务", nil).Key("official-agent-run").Disabled(s.Busy || s.TaskPrompt == "").OnClick(a.runOfficialAgent)
		actionButton(c, "官方结束同步并接收材料", nil).Key("official-agent-sync").Disabled(s.Busy || s.Session == "").OnClick(a.finishOfficialSession)
	}
	if s.Busy {
		quietButton(c, "取消本轮操作").Key("official-agent-cancel").OnClick(a.cancelOfficialOperation)
	}
	if s.AgentStage != "" {
		ui.Text(c, s.AgentStage).Key("official-agent-result").Selectable().LineHeight(1.7)
	}
	if s.TaskOutput != "" {
		ui.Text(c, s.TaskOutput).Selectable().LineHeight(1.7)
	}
	ui.Divider(c)
}

func (a *App) cancelOfficialOperation() {
	s := &a.cognee
	if s.Cancel != nil {
		s.Cancel()
	}
	s.AgentStage = "正在取消本轮操作；官方已提交材料不会被删除。"
	if s.Binding != nil {
		id := s.Binding.ConnectionID
		go func() {
			ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
			defer cancel()
			err := a.library.Disconnect(ctx, id)
			a.dispatch(func() {
				if s.Binding == nil || s.Binding.ConnectionID != id {
					return
				}
				if err != nil {
					s.Message = "取消后授权撤销未确认：" + err.Error()
					return
				}
				s.Message = "Memlane 项目接入已撤销；迟到同步按授权边界拒绝。官方服务上已提交的材料仍独立保留。"
				s.Authorized = false
				s.Receipt = nil
			})
		}()
	}
}
