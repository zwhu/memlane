package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type AgentUI struct {
	Sequence                                                        uint64
	Workspace                                                       string
	SelectedPaths                                                   map[string]string
	InstallErrors                                                   map[string]string
	IDEStates                                                       map[string]string
	OfficialConnections                                             map[string]bool
	Context                                                         context.Context
	Dispatch                                                        func(func())
	Root, Home, Bridge, PiDir                                       string
	Found                                                           []agents.Discovery
	Connections                                                     []records.Connection
	Installs                                                        map[string]agents.Installation
	Selection, Path, Project, ProjectLabel, Provider, Model, Notice string
	Allowed                                                         map[string]bool
	Busy, Loading                                                   bool
	Cancel                                                          context.CancelFunc
}

func (a *App) ConfigureAgents(ctx context.Context, root, home, bridge, piDir string, dispatch func(func())) {
	a.agents = AgentUI{Context: ctx, Dispatch: dispatch, Root: root, Home: home, Bridge: bridge, PiDir: piDir, Allowed: map[string]bool{}, Installs: map[string]agents.Installation{}}
	a.agents.SelectedPaths = map[string]string{}
	if saved, err := os.ReadFile(filepath.Join(root, "agent-paths.json")); err == nil && len(saved) < 65536 {
		_ = json.Unmarshal(saved, &a.agents.SelectedPaths)
	}
	a.refreshAgents(true)
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dispatch(func() {
					if (a.page == "Agent" || a.page == "使用指引" || a.page == "初始化") && !a.agents.Busy {
						a.refreshAgents(false)
					}
				})
			}
		}
	}()
}

func (a *App) refreshAgents(discover bool) {
	s := &a.agents
	if s.Context == nil || s.Loading || s.Busy {
		return
	}
	s.Loading = true
	sequence := s.Sequence
	prior := append([]agents.Discovery{}, s.Found...)
	selectedPaths := map[string]string{}
	for agent, path := range s.SelectedPaths {
		selectedPaths[agent] = path
	}
	go func() {
		ctx, stop := context.WithTimeout(s.Context, 20*time.Second)
		defer stop()
		found := prior
		if discover {
			found = agents.Discover(ctx, s.Home, selectedPaths)
		}
		connections, err := a.library.Connections(ctx)
		installs := map[string]agents.Installation{}
		installErrors := map[string]string{}
		ideStates := map[string]string{}
		officialConnections := map[string]bool{}
		for _, c := range connections {
			if i, e := agents.LoadInstallation(s.Root, c.ID); e == nil {
				installs[c.ID] = i
				if e = agents.CheckInstallation(i); e != nil {
					installErrors[c.ID] = e.Error()
				} else if c.Agent == "trae" && c.Enabled && i.Connection.Generation == c.Generation && i.IDEChallenge != nil && i.IDEReceipt == nil {
					receipt, e := a.library.CheckIDEVerification(ctx, *i.IDEChallenge)
					if e != nil {
						ideStates[c.ID] = e.Error()
					} else {
						ideStates[c.ID] = receipt.State
						if receipt.Complete {
							if e = agents.FinishIDEVerification(s.Root, *i.IDEChallenge, &receipt); e != nil {
								ideStates[c.ID] = e.Error()
							} else {
								updated, e := agents.LoadInstallation(s.Root, c.ID)
								if e == nil {
									installs[c.ID] = updated
								}
							}
						}
					}
				}
			}
			if c.Agent == "codex" && c.Project != "" {
				var receipt struct {
					Connection string `json:"connection"`
					Generation int64  `json:"generation"`
					Project    string `json:"project"`
				}
				path := filepath.Join(s.Root, "official", "agents", c.Project, "current-knowledge.json")
				if data, e := os.ReadFile(path); e == nil && len(data) < 65536 && json.Unmarshal(data, &receipt) == nil {
					officialConnections[c.ID] = receipt.Connection == c.ID && receipt.Generation == c.Generation && receipt.Project == c.Project
				}
			}
		}
		if s.Context.Err() != nil {
			return
		}
		s.Dispatch(func() {
			s.Loading = false
			if s.Sequence != sequence {
				a.refreshAgents(false)
				return
			}
			s.Found = found
			if err == nil {
				s.Connections = connections
				s.Installs = installs
				s.InstallErrors = installErrors
				s.IDEStates = ideStates
				s.OfficialConnections = officialConnections
			}
			if s.Selection == "" && len(found) > 0 {
				a.selectAgent("agent-" + found[0].Agent)
			}
		})
	}()
}

func (a *App) selectedAgent() (agents.Discovery, *records.Connection) {
	key := strings.TrimPrefix(a.agents.Selection, "agent-")
	var connection *records.Connection
	for index := range a.agents.Connections {
		c := &a.agents.Connections[index]
		if a.agents.Selection == "connection-"+c.ID {
			connection = c
			key = c.Agent
			break
		}
	}
	for _, d := range a.agents.Found {
		if d.Agent == key {
			return a.connectionDiscovery(d, connection), connection
		}
	}
	return agents.Discovery{}, connection
}

func (a *App) connectionDiscovery(d agents.Discovery, c *records.Connection) agents.Discovery {
	if c != nil {
		if i, ok := a.agents.Installs[c.ID]; ok && d.Path != i.Options.AgentPath {
			return agents.Discovery{Agent: d.Agent, Name: d.Name, Path: i.Options.AgentPath,
				Error: "此连接的安装路径与当前探测不同；请在 Agent 安装项检查该路径后重新发现。"}
		}
	}
	// Installation receipts describe the previous version. Preserve the current
	// discovery at this path for both list rows and connection details.
	return d
}

func (a *App) selectAgent(key string) {
	s := &a.agents
	if key == "agent-pi" {
		for _, connection := range s.Connections {
			if connection.Agent == "pi" && connection.Enabled {
				if installed, ok := s.Installs[connection.ID]; ok && installed.Options.PiDirectory == s.PiDir {
					key = "connection-" + connection.ID
					break
				}
			}
		}
	}
	s.Selection = key
	s.Notice = ""
	d, c := a.selectedAgent()
	s.Path = d.Path
	s.Allowed = map[string]bool{}
	s.Project = ""
	s.Workspace = ""
	if c != nil {
		for _, id := range c.AllowedScopes {
			s.Allowed[id] = true
		}
		s.Project = c.Project
		s.Workspace = c.TrustedWorkspace
		if i, ok := s.Installs[c.ID]; ok {
			s.Workspace = i.Options.Workspace
			if i.Options.Project != "" {
				s.Project = i.Options.Project
			}
			for _, project := range a.projects {
				if project.Path == i.Options.Workspace {
					s.Project = project.ID
				}
			}
		}
	} else {
		for _, scope := range a.scopes {
			if scope.Kind == "preference" || scope.Kind == "experience" {
				s.Allowed[scope.ID] = true
			}
		}
		for _, p := range a.projects {
			if p.ID == a.scope {
				s.Project = p.ID
			}
		}
	}
	s.ProjectLabel = a.scopeLabel(s.Project)
	if s.ProjectLabel == "" {
		s.ProjectLabel = "暂不选择项目"
	}
	if s.Workspace == "" {
		for _, p := range a.projects {
			if p.ID == s.Project {
				s.Workspace = p.Path
			}
		}
	}
}

func (a *App) agentStatus(d agents.Discovery, c *records.Connection) (string, string) {
	d = a.connectionDiscovery(d, c)
	if d.Path == "" {
		return "未找到安装", "可填写已知可执行文件路径。"
	}
	if d.Error != "" {
		return "异常", d.Error
	}
	if d.CapabilityError != "" {
		if c != nil && c.Enabled {
			return "待适配", d.CapabilityError + " 现有连接配置保留，暂不能验证当前版本。"
		}
		return "已发现", d.CapabilityError
	}
	if c == nil {
		return "已发现", "确认接入范围；项目可以跳过，再由 App 引导首次任务。"
	}
	if !c.Enabled {
		return "待连接", "已断开；其他 Agent 配置和项目记忆保留。"
	}
	if a.agents.OfficialConnections[c.ID] {
		if a.serviceState != "ready" {
			return "异常", "官方接入已确认，当前记忆服务不可用；请明确启动服务后继续。"
		}
		return "已接入", "官方任务材料已进入 Memlane，后续任务先读取当前有效知识；可继续任务或查看记忆与出处。"
	}
	if c.PendingProject != "" || (c.Project == "" && c.TrustedWorkspace == "") {
		if c.Agent == "codex" {
			return "已连接 · 待首次任务", "接入身份已确认。下一步确认任务工作目录、准备官方插件并完成首次真实任务；尚未产生项目记忆。"
		}
	}
	i, ok := a.agents.Installs[c.ID]
	if !ok {
		return "异常", "配置尚未安装完成，请重新连接。"
	}
	if message := a.agents.InstallErrors[c.ID]; message != "" {
		return "异常", message
	}
	if i.Connection.Generation != c.Generation {
		return "待重载", "范围已变更，请应用配置并重载 Agent。"
	}
	if i.VerificationError != "" {
		return "异常", i.VerificationError
	}
	if c.Agent == "trae" && i.IDEChallenge != nil && i.IDEReceipt == nil {
		message := "到此项目的TRAE IDE重载MCP，执行验证prompt；正在等待真实工具回执。"
		if status := a.agents.IDEStates[c.ID]; status != "" && status != "waiting" {
			message += " 当前回执状态：" + status
		}
		return "待验证", message
	}
	if i.VerifiedGeneration != c.Generation {
		return "待重载", "配置已安装。重载 Agent 后验证真实读写。"
	}
	if a.serviceState != "ready" {
		return "异常", "连接已验证，记忆服务或索引当前不可用。请从设置恢复。"
	}
	return "可用", "真实 Agent 已通过保存、状态与检索；范围由 Memlane 校验。"
}

func (a *App) agentList(c *ui.Context) {
	s := &a.agents
	for _, d := range s.Found {
		state, _ := a.agentStatus(d, nil)
		a.agentRow(c, "agent-"+d.Agent, d.Name, d.Version, state)
		for _, conn := range s.Connections {
			if conn.Agent == d.Agent {
				state, _ := a.agentStatus(d, &conn)
				name := conn.Name
				if conn.Project != "" {
					name = d.Name + " · " + a.scopeLabel(conn.Project)
				}
				a.agentRow(c, "connection-"+conn.ID, name, "已配置的记忆连接", state)
			}
		}
	}
	if len(s.Found) == 0 {
		a.empty(c, "正在发现 Agent", "只查看已知安装位置和 PATH。")
	}
}

func (a *App) agentRow(c *ui.Context, key, title, subtitle, state string) {
	b := ui.Button(c.Key(key), "").Label(title).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Background(ui.Transparent).Padding(14, 10).Justify(ui.Start).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, title).Bold().Grow(1).MinWidth(0).SingleLine()
				ui.Text(c, state).FontSize(10).TextColor(design.Muted)
			})
			ui.Text(c, subtitle).FontSize(design.Small).TextColor(design.Muted).SingleLine()
		})
	})
	if a.agents.Selection == key {
		b.Background(design.ListSelected).BorderWidth(0)
	}
	b.Disabled(a.agents.Busy).OnClick(func() { a.selectAgent(key) })
}

func (a *App) agentProjectOptions() []string {
	options := []string{"暂不选择项目"}
	for _, p := range a.projects {
		options = append(options, a.scopeLabel(p.ID))
	}
	return options
}

func (a *App) agentDetail(c *ui.Context) {
	s := &a.agents
	d, conn := a.selectedAgent()
	ui.Scroll(c.Key("agent-detail")).Grow(1).Padding(24).Gap(18).Children(func() {
		if d.Agent == "" {
			a.empty(c, "选择 Agent", "选择已发现的安装或连接。")
			return
		}
		state, description := a.agentStatus(d, conn)
		ui.Row(c).Children(func() {
			ui.Text(c, d.Name).FontSize(design.Title).Bold()
			ui.Spacer(c)
			ui.Text(c, state).FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Text(c, description).FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
		meta(c, "版本", d.Version)
		if d.Channel != "" {
			meta(c, "渠道", d.Channel)
		}
		ui.TextInput(c.Key("agent-path"), &s.Path).Label("Agent 路径").Placeholder("可执行文件的绝对路径").Disabled(s.Busy || conn != nil)
		if conn == nil {
			quietButton(c.Key("probe-agent"), "检查所选路径").Disabled(s.Busy || s.Path == "").OnClick(a.probeAgentPath)
		}
		ui.Divider(c)
		ui.Text(c, "项目与检索范围").Bold()
		ui.Select(c.Key("agent-project"), &s.ProjectLabel, a.agentProjectOptions()).Label("项目（可跳过）").Disabled(s.Busy || (conn != nil && d.Agent != "pi")).OnChange(func() {
			s.Project = ""
			s.Workspace = ""
			for _, p := range a.projects {
				if a.scopeLabel(p.ID) == s.ProjectLabel {
					s.Project = p.ID
					s.Workspace = p.Path
					break
				}
			}
		})
		ui.Text(c, "未选择项目时，连接不会创建空项目。首项任务确认工作目录，有有效记忆后按真实仓库建立或复用项目。").FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
		ui.TextInput(c.Key("agent-workspace"), &s.Workspace).Label("任务工作目录").Placeholder("可以在首次任务时再选择").Disabled(s.Busy || (conn != nil && d.Agent != "pi"))
		if d.Agent == "pi" {
			ui.Text(c, "pi 使用当前会话项目。这里选择验证项目，并授权它可以访问的范围。").FontSize(design.Small).TextColor(design.Muted)
		} else if d.Agent == "trae" {
			ui.Text(c, "TRAE IDE 使用所选项目的 .trae/mcp.json；在该项目中启用或重载 MCP 后执行验证prompt。").FontSize(design.Small).TextColor(design.Muted)
		} else {
			ui.Text(c, "MCP CLI 使用所选项目的配置；worktree 共用该仓库记忆。").FontSize(design.Small).TextColor(design.Muted)
		}
		for _, scope := range a.scopes {
			if scope.Kind != "project" && scope.Kind != "preference" && scope.Kind != "experience" {
				continue
			}
			allowed := s.Allowed[scope.ID] || scope.ID == s.Project
			ui.Checkbox(c.Key("allow-"+scope.ID), &allowed, scope.Name).Disabled(s.Busy || scope.ID == s.Project).OnChange(func() { s.Allowed[scope.ID] = allowed })
		}
		ui.Text(c, "默认当前项目＋允许的全局范围。其他获批项目仍需在 Agent 中明确指定。").FontSize(design.Small).TextColor(design.Muted)
		if s.Notice != "" {
			ui.Text(c, s.Notice).Selectable().FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
		}
		ui.Row(c).Gap(10).Children(func() {
			if conn == nil || !conn.Enabled {
				actionButton(c.Key("connect-agent"), "连接", nil).Disabled(s.Busy || s.Path == "" || d.ConnectionError() != nil).OnClick(a.connectAgent)
			} else if !s.OfficialConnections[conn.ID] && conn.PendingProject == "" && !(d.Agent == "codex" && conn.Project == "") {
				actionButton(c.Key("apply-agent-scopes"), "应用范围", nil).Disabled(s.Busy || d.ConnectionError() != nil).OnClick(a.connectAgent)
				verifyLabel := "验证读写"
				if d.Agent == "trae" {
					verifyLabel = "准备验证prompt"
				}
				actionButton(c.Key("verify-agent"), verifyLabel, nil).Disabled(s.Busy || d.ConnectionError() != nil).OnClick(a.verifyAgent)
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy).OnClick(a.disconnectAgent)
			}
			if conn != nil && conn.Enabled && (s.OfficialConnections[conn.ID] || conn.PendingProject != "" || (d.Agent == "codex" && conn.Project == "")) {
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy).OnClick(a.disconnectAgent)
			}
			if s.Cancel != nil {
				quietButton(c, "取消验证").OnClick(s.Cancel)
			}
		})
		if conn != nil {
			if d.Agent == "codex" && a.officialController() != nil {
				actionButton(c, "开始首次任务", nil).Key("agent-first-task").Disabled(s.Busy || !conn.Enabled).OnClick(func() { a.openFirstTask(*conn) })
			}
			if i, ok := s.Installs[conn.ID]; ok {
				meta(c, "配置", i.ConfigPath)
				if i.BackupPath != "" {
					meta(c, "备份", i.BackupPath)
				}
				if i.VerifiedAt != "" {
					meta(c, "验证", i.VerifiedAt)
				}
			}
		}
		if d.Agent == "trae" {
			ui.Text(c, "在 IDE 中使用自己的登录与模型。复制prompt仅开始等待回执；Memlane不会启动IDE模型。").FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
			if conn != nil {
				if i, ok := s.Installs[conn.ID]; ok && i.IDEChallenge != nil {
					d := *i.IDEChallenge
					ui.Text(c, agents.IDEVerificationPrompt(d)).Selectable().FontSize(design.Small).LineHeight(1.6)
					ui.Row(c).Gap(10).Children(func() {
						quietButton(c, "复制验证prompt").Key("ide-copy-prompt").OnClick(func() { c.Services().WriteClipboard(agents.IDEVerificationPrompt(d)) })
						if i.IDEReceipt == nil {
							quietButton(c, "取消等待").Key("ide-cancel-verification").Disabled(s.Busy).OnClick(func() { a.cancelIDEVerification(d) })
						}
					})
				}
			}
		} else {
			ui.Text(c, "验证模型（留空使用 Agent 默认模型）").FontSize(design.Small).TextColor(design.Muted)
			ui.Row(c).Gap(8).Children(func() {
				if d.Agent == "pi" {
					ui.TextInput(c.Key("agent-provider"), &s.Provider).Placeholder("provider").Grow(1).Disabled(s.Busy)
				}
				ui.TextInput(c.Key("agent-model"), &s.Model).Placeholder("model").Grow(1).Disabled(s.Busy)
			})
		}
		ui.Divider(c)
		ui.Text(c, "初始化记忆库").Bold()
		ui.Text(c, "选择少量项目文件后复制prompt，实际提交与索引状态由初始化记录显示。").FontSize(design.Small).TextColor(design.Muted)
		key := ""
		if conn != nil {
			key = conn.ID
		}
		actionButton(c, "初始化项目记忆", nil).Key("agent-initialization").Disabled(conn == nil || !conn.Enabled).OnClick(func() { a.openInitialization(key) })
	})
}

func (a *App) probeAgentPath() {
	s := &a.agents
	d, _ := a.selectedAgent()
	path := s.Path
	s.Busy = true
	go func() {
		probe := agents.Probe(s.Context, d.Agent, path)
		s.Dispatch(func() {
			s.Busy = false
			for index := range s.Found {
				if s.Found[index].Agent == d.Agent {
					s.Found[index] = probe
				}
			}
			s.Notice = "路径已检查"
			if probe.Error != "" {
				s.Notice = probe.Error
			} else {
				if probe.CapabilityError != "" {
					s.Notice = probe.CapabilityError
				}
				if s.SelectedPaths == nil {
					s.SelectedPaths = map[string]string{}
				}
				s.SelectedPaths[d.Agent] = path
				if err := os.MkdirAll(s.Root, 0700); err == nil {
					if err = serviceipc.WriteJSON(filepath.Join(s.Root, "agent-paths.json"), s.SelectedPaths); err != nil {
						s.Notice = "所选路径未保存：" + err.Error()
					}
				}
			}
		})
	}()
}

func (a *App) connectAgent() {
	s := &a.agents
	if s.Context == nil || s.Busy {
		return
	}
	d, prior := a.selectedAgent()
	allowed := []string{}
	for _, scope := range a.scopes {
		if s.Allowed[scope.ID] || scope.ID == s.Project {
			allowed = append(allowed, scope.ID)
		}
	}
	workspace := s.Workspace
	draft := records.ConnectionDraft{Agent: d.Agent, Name: d.Name + " · " + a.scopeLabel(s.Project), AllowedScopes: allowed}
	deferred := s.Project == ""
	if deferred {
		draft.Name = d.Name + " · 首次接入"
		draft.AutoProject, draft.Workspace = true, workspace
	}
	if prior != nil {
		draft.ID = prior.ID
	}
	if d.Agent != "pi" {
		draft.Project = s.Project
	}
	project := s.Project
	opts := agents.InstallOptions{ClientRoot: s.Root, ServiceRoot: a.library.Root, Bridge: s.Bridge, AgentPath: s.Path, Workspace: workspace, PiDirectory: s.PiDir, Project: project}
	s.Sequence++
	s.Busy = true
	s.Notice = "正在连接…"
	go func() {
		ctx, stop := context.WithTimeout(s.Context, 20*time.Second)
		defer stop()
		probe := agents.Probe(ctx, d.Agent, opts.AgentPath)
		var conn records.Connection
		var err error
		if probe.ConnectionError() != nil {
			err = probe.ConnectionError()
		} else {
			if !deferred {
				err = a.library.CheckWorkspace(ctx, project, workspace)
			}
			if err == nil {
				conn, err = a.library.ConfigureConnection(ctx, draft)
			}
		}
		if err == nil {
			if prior != nil && prior.ID != conn.ID {
				err = agents.Uninstall(ctx, s.Root, prior.ID)
			}
		}
		if err == nil && !(deferred && d.Agent == "codex") {
			_, err = agents.Install(ctx, conn, opts)
		}
		if s.Context.Err() != nil {
			return
		}
		s.Dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Notice = "连接失败：" + err.Error()
			} else {
				s.Selection = "connection-" + conn.ID
				if s.SelectedPaths == nil {
					s.SelectedPaths = map[string]string{}
				}
				s.SelectedPaths[d.Agent] = opts.AgentPath
				_ = serviceipc.WriteJSON(filepath.Join(s.Root, "agent-paths.json"), s.SelectedPaths)
				s.Notice = "配置已安装，请重载 Agent 后验证读写。"
				if deferred && d.Agent == "codex" {
					s.Notice = "Agent 接入身份已确认。继续确认本轮任务工作目录与官方采集；连接没有创建项目。"
					a.openFirstTask(conn)
				}
			}
			a.refreshAgents(false)
		})
	}()
}

func (a *App) disconnectAgent() {
	s := &a.agents
	_, conn := a.selectedAgent()
	if conn == nil || s.Busy {
		return
	}
	id := conn.ID
	_, hasInstallation := s.Installs[id]
	s.Sequence++
	s.Busy = true
	go func() {
		ctx, stop := context.WithTimeout(s.Context, 20*time.Second)
		defer stop()
		err := a.library.Disconnect(ctx, id)
		revoked := err == nil
		if err == nil && hasInstallation {
			err = agents.Uninstall(ctx, s.Root, id)
		}
		if s.Context.Err() != nil {
			return
		}
		s.Dispatch(func() {
			s.Busy = false
			s.Notice = "已断开；其他配置与记忆保留。"
			if err != nil {
				if revoked {
					s.Notice = "连接已撤销；配置清理需要处理：" + err.Error()
				} else {
					s.Notice = "断开未完成，请从设置明确启动服务后重试：" + err.Error()
				}
			}
			a.refreshAgents(false)
		})
	}()
}

func (a *App) verifyAgent() {
	s := &a.agents
	d, conn := a.selectedAgent()
	if conn == nil || s.Busy {
		return
	}
	i, ok := s.Installs[conn.ID]
	if !ok {
		s.Notice = "请先完成连接"
		return
	}
	if d.Agent == "trae" {
		id, generation := conn.ID, conn.Generation
		s.Sequence++
		s.Busy = true
		go func() {
			_, err := agents.BeginIDEVerification(s.Root, id, generation)
			s.Dispatch(func() {
				s.Busy = false
				s.Notice = "验证prompt已准备；到此项目的IDE执行，等待真实工具回执。"
				if err != nil {
					s.Notice = "准备失败：" + err.Error()
				}
				a.refreshAgents(false)
			})
		}()
		return
	}
	if s.Provider == "" && d.Agent == "pi" {
		var settings struct {
			Provider string `json:"defaultProvider"`
			Model    string `json:"defaultModel"`
		}
		if data, err := os.ReadFile(filepath.Join(s.PiDir, "settings.json")); err == nil && json.Unmarshal(data, &settings) == nil {
			s.Provider = settings.Provider
			s.Model = settings.Model
		}
	}
	ctx, cancel := context.WithCancel(s.Context)
	s.Cancel = cancel
	s.Busy = true
	s.Notice = "正在让真实 Agent 验证读写…"
	options := agents.RunOptions{Provider: s.Provider, Model: s.Model, PiAuthDirectory: os.Getenv("MEMLANE_PI_AUTH_DIRECTORY"), TraeAuthHome: os.Getenv("MEMLANE_TRAE_AUTH_DIRECTORY")}
	go func() {
		defer cancel()
		_, err := agents.VerifyConnection(ctx, d, i, options)
		if s.Context.Err() != nil {
			return
		}
		s.Dispatch(func() {
			s.Busy = false
			s.Cancel = nil
			s.Notice = "真实 Agent 读写验证通过。"
			if err != nil {
				s.Notice = "验证失败：" + err.Error()
			}
			a.refreshAgents(false)
		})
	}()
}

func (a *App) cancelIDEVerification(d records.IDEVerification) {
	s := &a.agents
	if s.Busy {
		return
	}
	s.Sequence++
	s.Busy = true
	go func() {
		err := agents.FinishIDEVerification(s.Root, d, nil)
		s.Dispatch(func() {
			s.Busy = false
			s.Notice = "已取消等待；旧回执不能通过后续验证。IDE会话由用户控制。"
			if err != nil {
				s.Notice = "取消失败：" + err.Error()
			}
			a.refreshAgents(false)
		})
	}()
}
