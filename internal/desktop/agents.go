package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Workspace                                                       string
	SelectedPaths                                                   map[string]string
	InstallErrors                                                   map[string]string
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
					if a.page == "Agent" && !a.agents.Busy {
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
		for _, c := range connections {
			if i, e := agents.LoadInstallation(s.Root, c.ID); e == nil {
				installs[c.ID] = i
				if e = agents.CheckInstallation(i); e != nil {
					installErrors[c.ID] = e.Error()
				}
			}
		}
		if s.Context.Err() != nil {
			return
		}
		s.Dispatch(func() {
			s.Loading = false
			s.Found = found
			if err == nil {
				s.Connections = connections
				s.Installs = installs
				s.InstallErrors = installErrors
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
			if connection != nil {
				if i, ok := a.agents.Installs[connection.ID]; ok {
					d.Path = i.Options.AgentPath
					if i.AgentVersion != "" {
						d.Version = i.AgentVersion
					}
				}
			}
			return d, connection
		}
	}
	return agents.Discovery{}, connection
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
		s.ProjectLabel = "选择本地项目"
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
	if d.Path == "" {
		return "待连接", "未找到安装；可填写已知可执行文件路径。"
	}
	if d.Error != "" {
		return "异常", d.Error
	}
	if c == nil {
		return "已发现", "选择项目和允许范围，连接后在 Agent 中重载。"
	}
	if !c.Enabled {
		return "待连接", "已断开；其他 Agent 配置和项目记忆保留。"
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
	options := []string{"选择本地项目"}
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
		ui.TextInput(c.Key("agent-path"), &s.Path).Label("Agent 路径").Placeholder("可执行文件的绝对路径").Disabled(s.Busy || conn != nil)
		if conn == nil {
			quietButton(c.Key("probe-agent"), "检查所选路径").Disabled(s.Busy || s.Path == "").OnClick(a.probeAgentPath)
		}
		ui.Divider(c)
		ui.Text(c, "项目与检索范围").Bold()
		ui.Select(c.Key("agent-project"), &s.ProjectLabel, a.agentProjectOptions()).Label("连接项目").Disabled(s.Busy || (conn != nil && d.Agent == "codex")).OnChange(func() {
			s.Project = ""
			for _, p := range a.projects {
				if a.scopeLabel(p.ID) == s.ProjectLabel {
					s.Project = p.ID
					s.Workspace = p.Path
					break
				}
			}
		})
		ui.TextInput(c.Key("agent-workspace"), &s.Workspace).Label("工作区路径").Placeholder("所选仓库或 worktree 的绝对路径").Disabled(s.Busy || (conn != nil && d.Agent == "codex"))
		if d.Agent == "pi" {
			ui.Text(c, "pi 使用当前会话项目。这里选择验证项目，并授权它可以访问的范围。").FontSize(design.Small).TextColor(design.Muted)
		} else {
			ui.Text(c, "Codex 使用所选项目的配置；worktree 共用该仓库记忆。").FontSize(design.Small).TextColor(design.Muted)
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
				actionButton(c.Key("connect-agent"), "连接", nil).Disabled(s.Busy || s.Project == "" || s.Path == "").OnClick(a.connectAgent)
			} else {
				actionButton(c.Key("apply-agent-scopes"), "应用范围", nil).Disabled(s.Busy).OnClick(a.connectAgent)
				actionButton(c.Key("verify-agent"), "验证读写", nil).Disabled(s.Busy).OnClick(a.verifyAgent)
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy).OnClick(a.disconnectAgent)
			}
			if s.Cancel != nil {
				quietButton(c, "取消验证").OnClick(s.Cancel)
			}
		})
		if conn != nil {
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
		if d.Agent == "pi" {
			ui.Text(c, "验证模型（留空使用 Agent 默认模型）").FontSize(design.Small).TextColor(design.Muted)
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c.Key("agent-provider"), &s.Provider).Placeholder("provider").Grow(1).Disabled(s.Busy)
				ui.TextInput(c.Key("agent-model"), &s.Model).Placeholder("model").Grow(1).Disabled(s.Busy)
			})
		}
		ui.Divider(c)
		ui.Text(c, "初始化记忆库").Bold()
		ui.Text(c, "复制 prompt 的入口已预留，具体内容将在独立设计中完成。").FontSize(design.Small).TextColor(design.Muted)
		actionButton(c, "复制初始化 prompt", nil).Disabled(true).AlignSelf(ui.Start)
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
	if prior != nil {
		draft.ID = prior.ID
	}
	if d.Agent == "codex" {
		draft.Project = s.Project
	}
	project := s.Project
	opts := agents.InstallOptions{ClientRoot: s.Root, ServiceRoot: a.library.Root, Bridge: s.Bridge, AgentPath: s.Path, Workspace: workspace, PiDirectory: s.PiDir, Project: project}
	s.Busy = true
	s.Notice = "正在连接…"
	go func() {
		ctx, stop := context.WithTimeout(s.Context, 20*time.Second)
		defer stop()
		probe := agents.Probe(ctx, d.Agent, opts.AgentPath)
		var conn records.Connection
		var err error
		if probe.Error != "" {
			err = errors.New(probe.Error)
		} else {
			err = a.library.CheckWorkspace(ctx, project, workspace)
			if err == nil {
				conn, err = a.library.ConfigureConnection(ctx, draft)
			}
		}
		if err == nil {
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
	s.Busy = true
	go func() {
		ctx, stop := context.WithTimeout(s.Context, 20*time.Second)
		defer stop()
		err := a.library.Disconnect(ctx, id)
		revoked := err == nil
		if err == nil {
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
	marker := "memlaneConnectionCheck" + strings.ReplaceAll(conn.ID, "-", "") + fmt.Sprint(conn.Generation)
	params, _ := json.Marshal(map[string]any{"operation_id": "connection-check-" + fmt.Sprint(conn.Generation), "text": "Memlane synthetic connection verification " + marker, "origin": "raw", "source": map[string]any{"fragment": "Memlane connection verification; synthetic only"}})
	prompt := "这是 Memlane 合成连接验证。仅使用 Memlane memory tools，不读取文件或运行命令。先 memory_save 保存：" + string(params) + "。取得 persisted 回执后，用 memory_status 等待索引完成，再 memory_search 查询 " + marker + "。失败明确报告，不能宣称成功。"
	ctx, cancel := context.WithCancel(s.Context)
	s.Cancel = cancel
	s.Busy = true
	s.Notice = "正在让真实 Agent 验证读写…"
	options := agents.RunOptions{Prompt: prompt, Provider: s.Provider, Model: s.Model, PiAuthDirectory: os.Getenv("MEMLANE_PI_AUTH_DIRECTORY")}
	go func() {
		defer cancel()
		execution, err := agents.Run(ctx, d, i, options)
		if err == nil {
			var memoryID string
			var searchable bool
			var status bool
			for _, call := range execution.Calls {
				if !call.Response.OK {
					continue
				}
				switch call.Tool {
				case "memory_save":
					var receipt records.SaveReceipt
					if json.Unmarshal(call.Response.Result, &receipt) == nil && receipt.Persisted && strings.Contains(receipt.Record.Text, marker) {
						memoryID = receipt.Record.Memory
					}
				case "memory_status":
					var v records.Version
					if json.Unmarshal(call.Response.Result, &v) == nil && v.Memory == memoryID && v.IndexState == "indexed" {
						status = true
					}
				case "memory_search":
					var results struct {
						Hits []records.SearchHit `json:"hits"`
					}
					if json.Unmarshal(call.Response.Result, &results) == nil {
						for _, hit := range results.Hits {
							if hit.Version.Memory == memoryID && strings.Contains(hit.Version.Text, marker) {
								searchable = true
							}
						}
					}
				}
			}
			if memoryID == "" || !status || !searchable {
				err = errors.New("真实 Agent 未完成保存、索引状态和检索回执，连接尚不可用")
			}
		}
		failure := ""
		if err != nil {
			failure = err.Error()
		}
		persistErr := agents.SaveVerification(s.Root, conn.ID, conn.Generation, failure)
		if err == nil {
			err = persistErr
		}
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
