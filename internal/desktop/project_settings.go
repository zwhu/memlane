package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

type ProjectSettingsUI struct {
	Open, Loading, ReturnEditing bool
	Owner                        records.Scope
	ReturnMode                   string
	Sequence                     uint64
	Connections                  []records.Connection
	Error, Notice                string
	Scroll                       ui.ScrollState
}

func (a *App) openProjectSettings(id string) {
	a.closeProjectOrganizer()
	var owner records.Scope
	for _, p := range a.projects {
		if p.ID == id {
			owner = records.Scope{ID: p.ID, Name: p.Name, Path: p.Path, Kind: "project"}
			break
		}
	}
	if owner.ID == "" {
		return
	}
	if a.page != "项目" || a.scope != id {
		a.navigate("项目", id)
	} else {
		a.closeProjectSettings()
	}
	a.rememberDraft()
	prior := a.projectSettings
	a.projectSettings = ProjectSettingsUI{Open: true, Owner: owner, ReturnMode: a.detailMode, ReturnEditing: a.editing, Sequence: prior.Sequence + 1}
	a.editing, a.detailMode = false, "view"
	a.refreshProjectSettings()
}

func (a *App) closeProjectSettings() {
	s := &a.projectSettings
	if s.Open && a.page == "项目" && a.scope == s.Owner.ID {
		a.editing, a.detailMode = s.ReturnEditing, s.ReturnMode
	}
	s.Open, s.Loading = false, false
	s.Sequence++
}

func (a *App) refreshProjectSettings() {
	s := &a.projectSettings
	if !s.Open || s.Loading {
		return
	}
	if a.libraryContext == nil || a.dispatch == nil {
		s.Connections = append([]records.Connection(nil), a.agents.Connections...)
		return
	}
	s.Sequence++
	sequence, id := s.Sequence, s.Owner.ID
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	s.Loading, s.Error = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(life, 5*time.Second)
		defer cancel()
		scopes, err := client.Scopes(ctx)
		var owner records.Scope
		for _, p := range scopes {
			if p.ID == id && p.Kind == "project" {
				owner = p
				break
			}
		}
		if err == nil && owner.ID == "" {
			err = fmt.Errorf("项目已不可用，请重新选择")
		}
		var connections []records.Connection
		if err == nil {
			connections, err = client.Connections(ctx)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch {
				return
			}
			if !s.Open || s.Sequence != sequence || s.Owner.ID != id {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = "项目设置读取未完成：请确认记忆服务可用后重试。"
				return
			}
			s.Owner, s.Connections = owner, connections
		})
	}()
}

func (a *App) projectConnectionState(c records.Connection, id string) string {
	if !c.Enabled {
		return "已断开"
	}
	i, ok := a.agents.Installs[c.ID]
	if !ok || i.Connection.Generation != c.Generation || a.agents.InstallErrors[c.ID] != "" {
		return "待重载与验证"
	}
	if c.Agent == "codex" && i.Options.ComputerLevel {
		for _, d := range a.agents.Found {
			if d.Agent != c.Agent {
				continue
			}
			d = a.connectionDiscovery(d, &c)
			workspace := ""
			for _, scope := range a.scopes {
				if scope.ID == id && scope.Kind == "project" {
					workspace = scope.Path
					break
				}
			}
			if workspace == "" {
				for _, project := range a.projects {
					if project.ID == id {
						workspace = project.Path
						break
					}
				}
			}
			if agents.ComputerConnectionError(d) == nil && currentComputerProtocol(i, c) && agents.CodexProjectVerification(i, id, workspace, c.Generation) {
				return "项目链路已验证"
			}
		}
		return "待重载与验证"
	}
	if i.VerifiedGeneration != c.Generation || i.VerifiedAt == "" {
		return "待重载与验证"
	}
	for _, d := range a.agents.Found {
		if d.Agent == c.Agent {
			d = a.connectionDiscovery(d, &c)
			if d.ConnectionError() == nil && d.Version == i.AgentVersion {
				return "已连接"
			}
		}
	}
	return "需重新验证"
}

func (a *App) projectSettingsContent(c *ui.Context) {
	s := &a.projectSettings
	ui.Scroll(c).Key("project-settings-content").Grow(1).TrackScroll(&s.Scroll).Padding(24, 34).Gap(20).Children(func() {
		ui.Text(c, "本地项目").FontSize(10).TextColor(design.Muted)
		ui.Text(c, s.Owner.Name).Key("project-settings-name").FontSize(23).Bold()
		ui.Text(c, "配置对整个项目生效，同一仓库的工作目录共用。").FontSize(12).TextColor(design.Muted).LineHeight(1.9)
		ui.Column(c).Padding(16, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Gap(10).Children(func() {
			ui.Text(c, "仓库位置").FontSize(11).Bold().TextColor(design.Muted)
			ui.Row(c).Padding(13, 14).Gap(11).Radius(7).Border(1, design.Border).Background(design.Surface).Children(func() {
				ui.Icon(c, icons["项目"]).FontSize(13).Shrink(0).TextColor(design.Muted)
				ui.Text(c, s.Owner.Path).Key("project-settings-path").Selectable().FontSize(11).Grow(1).MinWidth(0)
				quietButton(c, "复制").OnClick(func() { c.WriteClipboard(s.Owner.Path) })
			})
		})
		ui.Column(c).Padding(16, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Gap(10).Children(func() {
			ui.Text(c, "Agent 连接").FontSize(11).Bold().TextColor(design.Muted)
			ui.Text(c, "所有 Agent 均可读写项目知识与原始记录。").FontSize(11).TextColor(design.Muted)
			if s.Loading {
				ui.Text(c, "正在读取连接状态…").FontSize(11)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(11).TextColor(design.Muted)
				quietButton(c, "重新读取项目设置").OnClick(a.refreshProjectSettings)
			}
			for _, conn := range s.Connections {
				ui.Row(c).Key("project-connection-"+conn.ID).Padding(10, 0).Gap(10).AlignItems(ui.Center).Children(func() {
					name := conn.Name
					if name == "" {
						name = conn.Agent
					}
					ui.Text(c, name).FontSize(12).Grow(1)
					ui.Text(c, a.projectConnectionState(conn, s.Owner.ID)).FontSize(10).TextColor(design.Muted)
					if conn.Agent == "codex" && conn.Enabled && a.agents.Installs[conn.ID].Options.ComputerLevel {
						quietButton(c, "重载与验证").OnClick(func() { a.openProjectConnectionVerification(conn, s.Owner) })
					}
				})
			}
			if !s.Loading && len(s.Connections) == 0 {
				quietButton(c, "前往 Agent 连接").OnClick(func() { a.navigate("设置", ""); a.settingsSection = "agents" })
			}
		})
		ui.Column(c).Padding(16, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Gap(10).Children(func() {
			ui.Text(c, "使用范围").FontSize(11).Bold().TextColor(design.Muted)
			ui.Text(c, "默认按当前项目与通用知识检索，也可选择其他项目。").FontSize(11).LineHeight(1.8).TextColor(design.Muted)
			ui.Text(c, "当前项目的明确约定优先于通用知识。").Padding(5, 0, 5, 13).BorderWidth(0, 0, 0, 2).BorderColor(design.Border).FontSize(11).TextColor(design.Muted)
		})
	})
}

// The project row is an explicit selection; do not infer a verification workspace
// from another open project or a connection's historical default.
func (a *App) openProjectConnectionVerification(conn records.Connection, owner records.Scope) {
	if !conn.Enabled || owner.Path == "" {
		return
	}
	a.openAgentSettings()
	a.selectAgent("connection-" + conn.ID)
	a.agents.Project, a.agents.Workspace = owner.ID, owner.Path
	a.agents.ProjectLabel = a.scopeLabel(owner.ID)
}
