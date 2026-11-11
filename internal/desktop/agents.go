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
	CaptureStatus                                                   map[string]*agents.CodexCaptureStatus
	CaptureErrors                                                   map[string]string
	Batch                                                           AgentBatchUI
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

func protocolOnlyConnection(c *records.Connection) bool {
	return c != nil && (c.Agent == "pi" || c.Agent == "codex") && c.Project == "" && !c.AutoProject && c.PendingProject == "" && c.TrustedWorkspace == "" && len(c.AllowedScopes) == 0
}

func (a *App) ConfigureAgents(ctx context.Context, root, home, bridge, piDir string, dispatch func(func())) {
	a.agents = AgentUI{Context: ctx, Dispatch: dispatch, Root: root, Home: home, Bridge: bridge, PiDir: piDir, Allowed: map[string]bool{}, Installs: map[string]agents.Installation{}}
	a.agents.SelectedPaths = map[string]string{}
	a.agents.Batch.Selected = map[string]bool{}
	a.loadAgentBatchIntent()
	if saved, err := os.ReadFile(filepath.Join(root, "agent-paths.json")); err == nil && len(saved) < 65536 {
		_ = json.Unmarshal(saved, &a.agents.SelectedPaths)
	}
	a.refreshAgents(true)
	a.refreshAgentBatch()
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dispatch(func() {
					if (a.page == "Agent" || (a.firstLaunch.Open && a.firstLaunchStep() == "agents") || (a.page == "设置" && a.settingsSection == "agents") || a.page == "使用指引" || a.page == "初始化") && !a.agents.Busy {
						a.refreshAgents(false)
						a.refreshAgentBatch()
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
	life, client, dispatch, epoch := s.Context, a.library, s.Dispatch, a.libraryEpoch
	root, home := s.Root, s.Home
	prior := append([]agents.Discovery{}, s.Found...)
	selectedPaths := map[string]string{}
	for agent, path := range s.SelectedPaths {
		selectedPaths[agent] = path
	}
	go func() {
		ctx, stop := context.WithTimeout(life, 20*time.Second)
		defer stop()
		found := prior
		if discover {
			found = agents.Discover(ctx, home, selectedPaths)
		}
		connections, err := client.Connections(ctx)
		installs := map[string]agents.Installation{}
		captureStatus := map[string]*agents.CodexCaptureStatus{}
		captureErrors := map[string]string{}
		installErrors := map[string]string{}
		ideStates := map[string]string{}
		officialConnections := map[string]bool{}
		for _, c := range connections {
			if i, e := agents.LoadInstallation(root, c.ID); e == nil {
				installs[c.ID] = i
				if c.Agent == "codex" && i.Options.ComputerLevel {
					status, e := agents.ReadCodexCaptureStatus(root, c.ID)
					if e != nil {
						captureErrors[c.ID] = "采集回执暂时无法核对；不会据此显示采集中。"
					} else {
						captureStatus[c.ID] = status
					}
				}
				if e = agents.CheckInstallation(i); e != nil {
					installErrors[c.ID] = e.Error()
				} else if c.Agent == "trae" && c.Enabled && i.Connection.Generation == c.Generation && i.IDEChallenge != nil && i.IDEReceipt == nil {
					receipt, e := client.CheckIDEVerification(ctx, *i.IDEChallenge)
					if e != nil {
						ideStates[c.ID] = e.Error()
					} else {
						ideStates[c.ID] = receipt.State
						if receipt.Complete {
							if e = agents.FinishIDEVerification(root, *i.IDEChallenge, &receipt); e != nil {
								ideStates[c.ID] = e.Error()
							} else {
								updated, e := agents.LoadInstallation(root, c.ID)
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
				path := filepath.Join(root, "official", "agents", c.Project, "current-knowledge.json")
				if data, e := os.ReadFile(path); e == nil && len(data) < 65536 && json.Unmarshal(data, &receipt) == nil {
					officialConnections[c.ID] = receipt.Connection == c.ID && receipt.Generation == c.Generation && receipt.Project == c.Project
				}
			}
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch {
				return
			}
			s.Loading = false
			if s.Sequence != sequence {
				a.refreshAgents(false)
				return
			}
			s.Found = found
			if err == nil {
				s.Connections = connections
				s.Installs = installs
				s.CaptureStatus = captureStatus
				s.CaptureErrors = captureErrors
				s.InstallErrors = installErrors
				s.IDEStates = ideStates
				s.OfficialConnections = officialConnections
			}
			if s.Selection == "" && len(found) > 0 && a.page == "Agent" {
				a.selectAgent("agent-" + found[0].Agent)
			}
			if a.firstLaunch.Open && a.firstLaunchStep() == "processing" {
				a.refreshProcessing()
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

func currentComputerProtocol(i agents.Installation, c records.Connection) bool {
	proof, ok := agents.ConnectionVerification(i, c.Generation)
	return ok && strings.HasPrefix(proof, "protocol:") && i.ProtocolScopeDigest == records.MergeDigest([]any{c.Project, c.AllowedScopes})
}

// Match the displayed protocol proof to both the installed and current grant
// snapshot; a legacy read/write token is not an authorization metadata proof.
func currentPiProtocol(i agents.Installation, c records.Connection) bool {
	return agents.PiProtocolConnectionEligible(c) && i.Connection.ID == c.ID &&
		i.Connection.Generation == c.Generation && i.ProtocolGeneration == c.Generation &&
		i.ProtocolVerifiedAt != "" && i.ProtocolError == "" &&
		i.ProtocolScopeDigest == records.MergeDigest([]any{c.Project, c.AllowedScopes}) &&
		records.MergeDigest([]any{i.Connection.Project, i.Connection.AllowedScopes}) == i.ProtocolScopeDigest
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
	if !protocolOnlyConnection(c) && (c.PendingProject != "" || (c.Project == "" && c.TrustedWorkspace == "")) {
		if c.Agent == "codex" && !a.agents.Installs[c.ID].Options.ComputerLevel {
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
	if c.Agent == "codex" && i.Options.ComputerLevel && agents.ComputerConnectionError(d) != nil {
		return "待适配", "Codex 所需连接能力暂不可用，请重新检测并验证连接。"
	}
	if i.Connection.Generation != c.Generation {
		return "待重载", "范围已变更，请应用配置并重载 Agent。"
	}
	if c.Agent == "pi" && agents.PiProtocolConnectionEligible(*c) && !protocolOnlyConnection(c) {
		if i.ProtocolError != "" {
			return "异常", i.ProtocolError
		}
		if !currentPiProtocol(i, *c) {
			return "待验证", "授权已更新；请重载 Agent 后核对当前连接与授权元数据。"
		}
		if a.serviceState != "ready" {
			return "异常", "连接与授权元数据已核对，记忆服务当前不可用。"
		}
		return "已连接", "本机连接与当前授权元数据已核对；模型、记忆读写和项目工作目录访问仍需独立验证。"
	}
	if protocolOnlyConnection(c) {
		if c.Agent == "codex" && !i.Options.ComputerLevel {
			return "待适配", "原项目级配置不是电脑级连接，请使用电脑级连接入口。"
		}
		if i.ProtocolError != "" {
			return "异常", i.ProtocolError
		}
		if i.ProtocolGeneration != c.Generation || i.ProtocolVerifiedAt == "" || c.Agent == "codex" && !currentComputerProtocol(i, *c) {
			return "待验证", "配置已安装，请验证连接；项目与通用知识尚未授权。"
		}
		if a.serviceState != "ready" {
			if a.agents.Batch.ServiceReachable && a.serviceState != "stopped" && a.runtimeStatus.Phase != "stopped" {
				return "已连接", "本机连接协议已验证；记忆环境尚未就绪，项目与通用知识尚未授权。"
			}
			return "异常", "连接协议已验证，记忆服务当前不可用。"
		}
		return "已连接", "本机连接协议已验证；项目与通用知识尚未授权。"
	}
	if c.Agent == "codex" && i.Options.ComputerLevel {
		if currentComputerProtocol(i, *c) {
			if a.serviceState != "ready" {
				return "异常", "连接协议已验证，记忆服务当前不可用。"
			}
			return "已连接", "本机连接与当前授权范围协议已验证；项目读写与模型能力独立验证。"
		}
		return "待验证", "项目授权已保存；请重载 Agent，项目访问能力尚待独立验证。"
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

func (a *App) computerVerificationNeedsProject(conn *records.Connection) bool {
	if conn == nil {
		return false
	}
	for _, scope := range a.scopes {
		if scope.Kind == "project" && connectionAllows(*conn, scope.ID) {
			return true
		}
	}
	for _, project := range a.projects {
		if connectionAllows(*conn, project.ID) {
			return true
		}
	}
	return false
}

func (a *App) agentDetail(c *ui.Context) {
	if a.page == "设置" && a.settingsSection == "agents" {
		a.agentSettingsConnectionDetail(c)
		return
	}
	s := &a.agents
	d, conn := a.selectedAgent()
	computer := conn != nil && d.Agent == "codex" && s.Installs[conn.ID].Options.ComputerLevel
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
		if computer {
			ui.Text(c, "电脑级连接").Bold()
			ui.Text(c, "此连接不绑定项目或首次任务。项目访问请在对应项目的设置中明确授权；连接验证不表示模型或项目读写能力已通过。").FontSize(design.Small).TextColor(design.Muted)
			if a.computerVerificationNeedsProject(conn) {
				options := []string{"选择已授权项目用于验证"}
				for _, project := range a.projects {
					if connectionAllows(*conn, project.ID) {
						options = append(options, a.scopeLabel(project.ID))
					}
				}
				ui.Select(c.Key("codex-verification-project"), &s.ProjectLabel, options).Label("验证项目").Disabled(s.Busy).OnChange(func() {
					s.Project, s.Workspace = "", ""
					for _, project := range a.projects {
						if connectionAllows(*conn, project.ID) && a.scopeLabel(project.ID) == s.ProjectLabel {
							s.Project, s.Workspace = project.ID, project.Path
							break
						}
					}
				})
			}

		} else {
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
				ui.Checkbox(c.Key("allow-"+scope.ID), &allowed, a.scopeLabel(scope.ID)).Disabled(s.Busy || scope.ID == s.Project).OnChange(func() { s.Allowed[scope.ID] = allowed })
			}
			ui.Text(c, "默认当前项目＋已授权的通用知识范围。其他获批项目仍需在 Agent 中明确指定。").FontSize(design.Small).TextColor(design.Muted)
		}
		if s.Notice != "" {
			ui.Text(c, s.Notice).Selectable().FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
		}
		ui.Row(c).Gap(10).Children(func() {
			if computer && conn.Enabled {
				actionButton(c.Key("verify-agent"), "已重载，验证连接", nil).Disabled(s.Busy || d.ConnectionError() != nil || a.computerVerificationNeedsProject(conn) && s.Workspace == "").OnClick(a.verifyAgent)
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy).OnClick(a.disconnectAgent)
			} else if computer {
				quietButton(c, "选择电脑级重新连接").Key("reconnect-computer-agent").Disabled(s.Busy || s.Batch.frozen() || !a.machineConnectionEligible(d)).OnClick(func() {
					if s.Batch.Selected == nil {
						s.Batch.Selected = map[string]bool{}
					}
					s.Batch.Selected[d.Agent] = true
					s.Selection = ""
				})
			} else if conn == nil || !conn.Enabled {
				actionButton(c.Key("connect-agent"), "连接", nil).Disabled(s.Busy || s.Path == "" || d.ConnectionError() != nil).OnClick(a.connectAgent)
			} else if !s.OfficialConnections[conn.ID] && conn.PendingProject == "" && !(d.Agent == "codex" && conn.Project == "") {
				actionButton(c.Key("apply-agent-scopes"), "应用范围", nil).Disabled(s.Busy || d.ConnectionError() != nil).OnClick(a.connectAgent)
				verifyLabel := "验证读写"
				if protocolOnlyConnection(conn) {
					verifyLabel = "验证连接"
				}
				if d.Agent == "trae" {
					verifyLabel = "准备验证prompt"
				}
				actionButton(c.Key("verify-agent"), verifyLabel, nil).Disabled(s.Busy || d.ConnectionError() != nil).OnClick(a.verifyAgent)
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy).OnClick(a.disconnectAgent)
			}
			if !computer && conn != nil && conn.Enabled && (s.OfficialConnections[conn.ID] || conn.PendingProject != "" || (d.Agent == "codex" && conn.Project == "")) {
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy).OnClick(a.disconnectAgent)
			}
			if s.Cancel != nil {
				quietButton(c, "取消验证").OnClick(s.Cancel)
			}
		})
		if conn != nil {
			if !computer && d.Agent == "codex" && a.officialController() != nil {
				actionButton(c, "开始首次任务", nil).Key("agent-first-task").Disabled(s.Busy || !conn.Enabled).OnClick(func() { a.openFirstTask(*conn) })
			}
			if i, ok := s.Installs[conn.ID]; ok {
				meta(c, "配置", i.ConfigPath)
				if i.BackupPath != "" {
					meta(c, "备份", i.BackupPath)
				}
				if !computer && i.VerifiedAt != "" && i.VerifiedGeneration == conn.Generation {
					meta(c, "验证", i.VerifiedAt)
				}
				if i.ProtocolVerifiedAt != "" && i.ProtocolGeneration == conn.Generation && i.ProtocolError == "" && (!computer || currentComputerProtocol(i, *conn)) {
					meta(c, "连接验证", i.ProtocolVerifiedAt)
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
		} else if !protocolOnlyConnection(conn) {
			ui.Text(c, "验证模型（留空使用 Agent 默认模型）").FontSize(design.Small).TextColor(design.Muted)
			ui.Row(c).Gap(8).Children(func() {
				if d.Agent == "pi" {
					ui.TextInput(c.Key("agent-provider"), &s.Provider).Placeholder("provider").Grow(1).Disabled(s.Busy)
				}
				ui.TextInput(c.Key("agent-model"), &s.Model).Placeholder("model").Grow(1).Disabled(s.Busy)
			})
		}
		ui.Divider(c)
		actionButton(c, "前往项目知识", nil).Key("agent-project-knowledge").Disabled(conn == nil || !conn.Enabled || conn.Project == "").OnClick(func() { a.navigate("项目", conn.Project) })
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
	if d.Agent == "codex" {
		opts.CodexDirectory = os.Getenv("CODEX_HOME")
		if opts.CodexDirectory == "" {
			opts.CodexDirectory = filepath.Join(s.Home, ".codex")
		}
	}
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
	if conn == nil || !conn.Enabled || s.Busy {
		return
	}
	if a.page == "设置" && a.settingsSection == "agents" {
		if reason := a.settingsProtocolVerificationReason(d, conn); reason != "" {
			s.Notice = reason
			return
		}
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
	if i.Connection.Generation != conn.Generation {
		s.Notice = "当前授权配置尚未应用，请重新读取连接并应用后重载。"
		return
	}
	computer := d.Agent == "codex" && i.Options.ComputerLevel
	settingsPi := a.page == "设置" && a.settingsSection == "agents" && agents.PiProtocolConnectionEligible(*conn)
	protocolOnly := protocolOnlyConnection(conn) || computer || settingsPi
	if computer && a.computerVerificationNeedsProject(conn) && (s.Workspace == "" || !connectionAllows(*conn, s.Project)) {
		s.Notice = "请明确选择一个已授权项目，用它的工作目录验证当前连接。"
		return
	}
	if !protocolOnly && s.Provider == "" && d.Agent == "pi" {
		var settings struct {
			Provider string `json:"defaultProvider"`
			Model    string `json:"defaultModel"`
		}
		if data, err := os.ReadFile(filepath.Join(s.PiDir, "settings.json")); err == nil && json.Unmarshal(data, &settings) == nil {
			s.Provider = settings.Provider
			s.Model = settings.Model
		}
	}
	life, dispatch, epoch := s.Context, s.Dispatch, a.libraryEpoch
	if life == nil || dispatch == nil {
		s.Notice = "连接服务尚未准备，请重新读取连接。"
		return
	}
	ctx, cancel := context.WithCancel(life)
	s.Cancel = cancel
	s.Busy = true
	s.Notice = "正在让真实 Agent 验证读写…"
	if protocolOnly {
		s.Notice = "正在验证本机连接…"
		if settingsPi {
			s.Notice = "正在核对本机连接及当前授权元数据…"
		}
	}
	workspace, granted := s.Workspace, len(conn.AllowedScopes) > 0
	if settingsPi {
		workspace = ""
	}
	if computer && !a.computerVerificationNeedsProject(conn) {
		workspace = ""
	}
	options := agents.RunOptions{Provider: s.Provider, Model: s.Model, PiAuthDirectory: os.Getenv("MEMLANE_PI_AUTH_DIRECTORY"), TraeAuthHome: os.Getenv("MEMLANE_TRAE_AUTH_DIRECTORY")}
	go func() {
		defer cancel()
		var err error
		if protocolOnly {
			_, err = agents.VerifyProtocolConnection(ctx, d, i, agents.RunOptions{Workspace: workspace})
		} else {
			_, err = agents.VerifyConnection(ctx, d, i, options)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch {
				return
			}
			s.Busy = false
			s.Cancel = nil
			s.Notice = "真实 Agent 读写验证通过。"
			if protocolOnly {
				s.Notice = "本机连接验证通过；项目与通用知识尚未授权。"
				if granted {
					s.Notice = "本机连接与当前授权范围协议验证通过；不代表模型或项目读写验证。"
				}
				if settingsPi {
					s.Notice = "本机连接及当前授权元数据握手通过；不代表模型、记忆读写或项目工作目录访问已验证。"
				}
			}
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

// The connection settings never turn a protocol check into a model-backed
// read/write task, including when an old connection already carries grants.
func (a *App) settingsProtocolVerificationReason(d agents.Discovery, conn *records.Connection) string {
	if conn == nil || !conn.Enabled {
		return "请先通过上方卡片连接 Agent。"
	}
	installation, ok := a.agents.Installs[conn.ID]
	if !ok {
		return "连接配置尚未安装完成；原记录保留，请重新读取连接。"
	}
	if installation.Connection.Generation != conn.Generation {
		return "当前连接配置尚未应用，请重新读取连接并核对重载状态。"
	}
	if d.ConnectionError() != nil || agents.ComputerConnectionError(d) != nil {
		return "当前安装版本尚不能在这里执行电脑级连接验证；原配置保留。"
	}
	switch conn.Agent {
	case "pi":
		if !agents.PiProtocolConnectionEligible(*conn) {
			return "此连接尚未形成可核对的授权配置；原记录保留，请先在项目设置完成归属与授权。"
		}
	case "codex":
		if !installation.Options.ComputerLevel {
			return "这是原项目级连接，不能将其标为电脑级连接；原配置保留。"
		}
		if a.computerVerificationNeedsProject(conn) && (a.agents.Workspace == "" || !connectionAllows(*conn, a.agents.Project)) {
			return "此连接的验证需要项目上下文，当前页面暂不能单独验证；原配置与授权保留，此处不会调用模型。"
		}
	default:
		return "此接入暂不支持这里的电脑级连接验证；原配置保留。"
	}
	return ""
}

func (a *App) agentSettingsConnectionDetail(c *ui.Context) {
	s := &a.agents
	d, conn := a.selectedAgent()
	ui.Column(c).Key("connection-management").Padding(20).Gap(12).Children(func() {
		if conn == nil {
			ui.Text(c, "请通过上方卡片多选连接 Agent。").FontSize(design.Small).TextColor(design.Muted)
			return
		}
		name := conn.Name
		if name == "" {
			name = conn.Agent
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, name).Bold().Grow(1).MinWidth(0)
			ui.Text(c, a.agentConnectionRecordState(*conn)).FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Text(c, "连接配置与项目访问分别管理。访问授权请前往对应项目设置；连接验证不代表模型或项目读写能力已通过。").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6)
		if conn.Agent == "codex" {
			a.codexCaptureSettings(c, *conn)
		}
		if d.Version != "" {
			meta(c, "版本", d.Version)
		}
		if installed, ok := s.Installs[conn.ID]; ok {
			level := "现有配置（保留）"
			if conn.Agent == "pi" && installed.Options.PiDirectory != "" || conn.Agent == "codex" && installed.Options.ComputerLevel {
				level = "这台电脑"
			}
			meta(c, "连接层级", level)
			if installed.ConfigPath != "" {
				meta(c, "配置位置", installed.ConfigPath)
			}
			if installed.ProtocolGeneration == conn.Generation && installed.ProtocolVerifiedAt != "" && installed.ProtocolError == "" && (protocolOnlyConnection(conn) || conn.Agent == "pi" && currentPiProtocol(installed, *conn) || conn.Agent == "codex" && currentComputerProtocol(installed, *conn)) {
				meta(c, "连接验证", installed.ProtocolVerifiedAt)
			}
		}
		reason := a.settingsProtocolVerificationReason(d, conn)
		if conn.Enabled && reason != "" {
			ui.Text(c, reason).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6)
		}
		if s.Notice != "" {
			ui.Text(c, s.Notice).Selectable().FontSize(design.Small).TextColor(design.Muted)
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			quietButton(c, "重新读取连接").Key("connection-read-again").Disabled(s.Busy || s.Context == nil).OnClick(func() { a.refreshAgents(false) })
			if conn.Enabled {
				actionButton(c.Key("verify-agent"), "已重载，验证连接", nil).Disabled(s.Busy || s.Context == nil || reason != "").OnClick(a.verifyAgent)
				actionButton(c.Key("disconnect-agent"), "断开", nil).Disabled(s.Busy || s.Context == nil).OnClick(a.disconnectAgent)
			} else {
				quietButton(c, "选择电脑级重新连接").Key("reconnect-computer-agent").Disabled(s.Busy || s.Batch.frozen() || !a.machineConnectionEligible(d)).OnClick(func() {
					if s.Batch.Selected == nil {
						s.Batch.Selected = map[string]bool{}
					}
					s.Batch.Selected[d.Agent] = true
					s.Selection = ""
				})
			}
			if s.Cancel != nil {
				quietButton(c, "取消验证").OnClick(s.Cancel)
			}
		})
	})
}
