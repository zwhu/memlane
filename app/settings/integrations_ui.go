package settings

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func isBetaSupportedAgentKind(kind string) bool {
	switch kind {
	case "codex", "antigravity", "pi", "traex", "trae_ide":
		return true
	default:
		return false
	}
}

func isAgentVisibleInBeta(row AgentConnection, pending *generationUIOperation) bool {
	if isBetaSupportedAgentKind(row.Kind) || pending != nil {
		return true
	}
	switch row.Status {
	case "configured_pending_new_session", "prepared", "requires_review",
		"activation_unknown", "preparation_unknown", "staging_unknown",
		"agent_creation_unconfirmed":
		return true
	}
	return false
}

func connectionUnavailable(row AgentConnection) bool {
	if needsTraeHooksRepair(row) {
		return false
	}
	switch row.Status {
	case "configured_pending_new_session", "prepared", "requires_review", "activation_unknown", "preparation_unknown", "staging_unknown", "agent_creation_unconfirmed":
		return false
	case "integration_pending", "component_unavailable":
		return true
	}
	return !row.Detected
}

func (a *Controller) agentConnectionSettings(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(0).Children(func() { a.agentConnectionSettingsContent(c) })
}

func (a *Controller) agentConnectionSettingsContent(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	a.refreshAgentReceiptsIfDue(time.Now())
	title := "Agent 连接"
	if a.initialSetup {
		title = "Agent 接入"
	}
	kitui.Group(c, tok, title, "", func() {
		kitui.SmallButton(c, tok, "重新检测", kitui.IconRefresh, a.RefreshConnections).Disabled(s.Busy || s.Loading)
	}, func() {
		connectionsError := s.ConnectionsError
		if !s.Loaded && connectionsError == "" {
			connectionsError = s.Error
		}
		if connectionsError != "" && len(s.Connections.Connections) == 0 {
			kitui.Row(c, tok, kitui.RowOptions{
				Title: "连接状态暂时不可用",
				Desc:  connectionsError,
				Control: func() {
					kitui.SmallButton(c, tok, "重试", kitui.IconRefresh, a.RefreshIntegrations).Disabled(s.Busy || s.Loading)
				},
			})
		} else if !s.Loaded {
			kitui.SkeletonGroup(c, tok, 3, 2, true, true)
			return
		}
		if s.Selected == nil {
			s.Selected = map[string]bool{}
		}
		var pinned, available, unavailable []AgentConnection
		for _, row := range s.Connections.Connections {
			if !isAgentVisibleInBeta(row, s.pendingConnection(row.ID)) {
				continue
			}
			if slices.Contains(s.PinnedAgentKinds, row.Kind) {
				pinned = append(pinned, row)
			} else if connectionUnavailable(row) && s.pendingConnection(row.ID) == nil {
				unavailable = append(unavailable, row)
			} else {
				available = append(available, row)
			}
		}
		a.agentConnectionGrid(c, append(pinned, available...))
		if len(unavailable) > 0 {
			label := fmt.Sprintf("其他 Agent（%d）", len(unavailable))
			if s.ShowUnavailable {
				label = "收起其他 Agent"
			}
			kitui.Row(c, tok, kitui.RowOptions{
				Key:         "agents-toggle-unavailable",
				Title:       label,
				Clickable:   true,
				HideChevron: true,
				Control: func() {
					ico := kitui.IconChev
					if s.ShowUnavailable {
						ico = kitui.IconDown
					}
					ui.Icon(c, ico).FontSize(14).TextColor(tok.Text3).Shrink(0)
				},
				OnClick: func() { s.ShowUnavailable = !s.ShowUnavailable },
			})
			if s.ShowUnavailable {
				a.agentConnectionGrid(c, unavailable)
			}
		}
		for _, pending := range s.PendingOperations {
			if !pending.connection() {
				continue
			}
			found := false
			for _, row := range s.Connections.Connections {
				if row.ID == pending.Profile {
					found = true
					break
				}
			}
			if found {
				continue
			}
			o := pending
			name := "Agent"
			ui.Column(c.Key("pending-connection-item-" + o.Operation)).FillWidth().Gap(0).Children(func() {
				kitui.Row(c, tok, kitui.RowOptions{
					Key:   "pending-connection-" + o.Operation,
					Title: name + " 的连接结果尚未确认",
					Leading: func() {
						agentLogo(tok, c, AgentBrandIconFor("", name), 16)
					},
					Control: func() {
						kitui.SmallButton(c, tok, "检查连接", nil, func() { a.reviewConnectionOperation(o) }).Label("检查连接：" + name + " " + o.Operation).Disabled(s.Busy)
					},
				})
				a.pendingConnectionReconcileDetails(c, o, "")
			})
		}
		if s.Message != "" && s.MessageScope != "generation" {
			ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() { kitui.Text(c, tok, s.Message, kitui.RoleMeta) })
		}
		if s.Error != "" && (s.Error != connectionsError || len(s.Connections.Connections) != 0) {
			ui.Row(c).FillWidth().Padding(sp2, sp4).Children(func() { kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger) })
		}
	})
}
