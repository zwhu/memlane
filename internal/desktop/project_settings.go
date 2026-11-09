package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

type ProjectAccessOperation struct {
	Active                           bool
	Owner, Connection, Error, Notice string
}

type ProjectSettingsUI struct {
	Open, Loading, ReturnEditing bool
	Owner                        records.Scope
	ReturnMode                   string
	Sequence                     uint64
	Connections                  []records.Connection
	Editing                      string
	Allowed                      bool
	Generation                   int64
	Error, Notice                string
	Scroll                       ui.ScrollState
	Operation                    *ProjectAccessOperation
}

func (a *App) openProjectSettings(id string) {
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
	a.projectSettings = ProjectSettingsUI{Open: true, Owner: owner, ReturnMode: a.detailMode, ReturnEditing: a.editing, Sequence: prior.Sequence + 1, Operation: prior.Operation}
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
	s.Loading, s.Error = true, ""
	s.Editing = ""
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		scopes, err := a.library.Scopes(ctx)
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
			connections, err = a.library.Connections(ctx)
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
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

func connectionAllows(c records.Connection, project string) bool {
	for _, scope := range c.AllowedScopes {
		if scope == project {
			return true
		}
	}
	return false
}

func (a *App) projectAccessState(c records.Connection, id string) string {
	if !c.Enabled {
		return "已断开"
	}
	if !connectionAllows(c, id) {
		return "未授权本项目"
	}
	i, ok := a.agents.Installs[c.ID]
	if !ok || i.Connection.Generation != c.Generation || i.VerifiedGeneration != c.Generation || i.VerifiedAt == "" || a.agents.InstallErrors[c.ID] != "" {
		return "已授权，待重载与验证"
	}
	for _, d := range a.agents.Found {
		if d.Agent == c.Agent {
			d = a.connectionDiscovery(d, &c)
			if d.ConnectionError() == nil && d.Version == i.AgentVersion {
				return "可访问本项目"
			}
		}
	}
	return "已授权，需重新验证"
}

func (a *App) editProjectAccess(c records.Connection) {
	s := &a.projectSettings
	if s.Operation != nil && s.Operation.Active {
		return
	}
	s.Editing, s.Generation, s.Allowed = c.ID, c.Generation, connectionAllows(c, s.Owner.ID)
	s.Notice = ""
}

func (a *App) saveProjectAccess() {
	s := &a.projectSettings
	if !s.Open || s.Editing == "" || a.libraryContext == nil || a.dispatch == nil || (s.Operation != nil && s.Operation.Active) {
		return
	}
	d := records.ConnectionScopeChange{ConnectionID: s.Editing, ExpectedGeneration: s.Generation, Project: s.Owner.ID, Allowed: s.Allowed}
	sequence := s.Sequence
	op := &ProjectAccessOperation{Active: true, Owner: s.Owner.ID, Connection: s.Editing}
	s.Operation = op
	i, installed := a.agents.Installs[d.ConnectionID]
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 25*time.Second)
		defer cancel()
		conn, err := a.library.ChangeProjectAccess(ctx, d)
		accepted := err == nil
		if err == nil && installed && i.Connection.Generation != conn.Generation {
			_, err = agents.Install(ctx, conn, i.Options)
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			op.Active = false
			if accepted && conn.ID != "" && conn.Generation != d.ExpectedGeneration {
				op.Notice = "授权已保存；请重载 Agent 后重新验证。"
			}
			if err != nil {
				op.Error = "范围保存未全部完成，请重新读取后重试。"
			} else if op.Notice == "" {
				op.Notice = "项目授权已确认。"
			}
			a.refreshAgents(false)
			if !s.Open || s.Sequence != sequence || s.Owner.ID != d.Project {
				return
			}
			s.Notice, s.Error = op.Notice, op.Error
			if err == nil {
				s.Editing = ""
			}
			if conn.ID != "" {
				for index := range s.Connections {
					if s.Connections[index].ID == conn.ID {
						s.Connections[index] = conn
						if accepted {
							s.Generation = conn.Generation
						}
					}
				}
			}
		})
	}()
}

func (a *App) projectSettingsContent(c *ui.Context) {
	s := &a.projectSettings
	busy := s.Operation != nil && s.Operation.Active
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
			ui.Text(c, "Agent 访问").FontSize(11).Bold().TextColor(design.Muted)
			ui.Text(c, "访问权限覆盖本项目的知识与原始记录。").FontSize(11).TextColor(design.Muted)
			if s.Loading {
				ui.Text(c, "正在读取项目访问状态…").FontSize(11)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(11).TextColor(design.Muted)
				quietButton(c, "重新读取项目设置").Disabled(busy).OnClick(a.refreshProjectSettings)
			}
			if !s.Loading && s.Error == "" && len(s.Connections) == 0 {
				ui.Text(c, "尚未配置 Agent 连接。").FontSize(11).TextColor(design.Muted)
				quietButton(c, "前往 Agent 连接").OnClick(func() { a.navigate("设置", ""); a.settingsSection = "agents" })
			}
			for _, conn := range s.Connections {
				ui.Row(c).Key("project-access-"+conn.ID).Padding(10, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
					name := conn.Name
					if name == "" {
						name = conn.Agent
					}
					icon := icons[conn.Agent]
					if icon == nil {
						icon = icons["Agent"]
					}
					ui.Icon(c, icon).FontSize(22).Shrink(0).TextColor(design.Muted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Text(c, name).FontSize(12).MaxLines(2)
						ui.Text(c, conn.Agent).FontSize(10).TextColor(design.Muted)
					})
					ui.Text(c, a.projectAccessState(conn, s.Owner.ID)).Key("project-access-state-" + conn.ID).FontSize(10).TextColor(design.Muted)
					quietButton(c, "管理范围").Key("project-access-edit-" + conn.ID).Disabled(busy || !conn.Enabled).OnClick(func() { a.editProjectAccess(conn) })
				})
				if s.Editing == conn.ID {
					ui.Column(c).Key("project-access-editor").Padding(12).Gap(10).Background(design.Surface).Radius(7).Children(func() {
						ui.Text(c, conn.Name+" · "+s.Owner.Name).FontSize(12).Bold()
						bound := conn.Project == s.Owner.ID || conn.PendingProject == s.Owner.ID
						ui.Checkbox(c.Key("project-access-allowed"), &s.Allowed, "允许访问本项目的知识与原始记录").Disabled(busy || bound)
						if bound {
							ui.Text(c, "该连接与本项目绑定。撤销访问请先在 Agent 连接中断开。").FontSize(11).TextColor(design.Muted)
						}
						ui.Text(c, "这里只修改本项目的授权；其他项目和全局范围保留。").FontSize(11).TextColor(design.Muted)
						ui.Row(c).Gap(10).Children(func() {
							quietButton(c, "取消").Key("project-access-cancel").Disabled(busy).OnClick(func() { s.Editing = "" })
							quietButton(c, "保存范围").Key("project-access-save").Disabled(busy || a.libraryContext == nil || bound).OnClick(a.saveProjectAccess)
						})
					})
				}
			}
			if s.Notice != "" {
				ui.Text(c, s.Notice).FontSize(11).TextColor(design.Muted)
			}
			if busy {
				ui.Text(c, "正在保存项目授权与配置…").FontSize(11).TextColor(design.Muted)
			}
			if strings.HasPrefix(s.Notice, "授权已保存") {
				quietButton(c, "前往 Agent 连接验证").OnClick(func() { a.navigate("设置", ""); a.settingsSection = "agents" })
			}
		})
		ui.Column(c).Padding(16, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Gap(10).Children(func() {
			ui.Text(c, "使用范围").FontSize(11).Bold().TextColor(design.Muted)
			ui.Text(c, "默认使用当前项目与允许的全局记忆。其他项目需要明确指定。").FontSize(11).LineHeight(1.8).TextColor(design.Muted)
			ui.Text(c, "当前项目的明确约定优先于全局建议。").Padding(5, 0, 5, 13).BorderWidth(0, 0, 0, 2).BorderColor(design.Border).FontSize(11).TextColor(design.Muted)
		})
	})
}
