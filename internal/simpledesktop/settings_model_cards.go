package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func generationBridgeLabel(kind string) string {
	switch kind {
	case "pi":
		return "pi"
	case "traex":
		return "TraeX"
	default:
		return "Codex"
	}
}

func generationDefaultModel(g GenerationDirectory, kind string) string {
	if actual, ok := g.AgentDefaults[kind]; ok && actual.Model != "" {
		parts := []string{actual.Model}
		if actual.ReasoningEffort != "" {
			parts = append(parts, actual.ReasoningEffort)
		}
		if actual.ThinkingLevel != "" {
			parts = append(parts, actual.ThinkingLevel)
		}
		if actual.ServiceTier != "" {
			parts = append(parts, actual.ServiceTier)
		}
		return strings.Join(parts, " / ")
	}
	if profile, ok := g.AgentBridges[kind]; ok && profile.Model != "" {
		return profile.Model
	}
	return "未读取到当前模型"
}

func generationAgentMark(c *ui.Context, g GenerationDirectory, ref *GenerationRef) {
	if row, ok := g.row(ref); ok && row.Profile.Mode == "agent_bridge" {
		agentLogo(c, agentBrandIcon(row.Profile.BridgeKind), 18)
	}
}

// Current runtime is a single compact line, never a duplicate configuration card.
func (a *App) generationCurrentStatus(c *ui.Context) {
	s := &a.integrations
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		homeText(c, "当前使用", homeMeta)
		generationAgentMark(c, s.Generation, s.Generation.Active)
		label := generationLabel(s.Generation, s.Generation.Active)
		homeText(c, label, homeMeta).Grow(1).MinWidth(0).SingleLine().Tooltip(label)
	})
}

func (a *App) generationProfileCards(c *ui.Context, editable ...bool) {
	edit := len(editable) == 0 || editable[0]
	s := &a.integrations
	groups := generationWorkspaceGroups(s.Generation, s.DraftMode)
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		if edit {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				title := "选择模型"
				if s.DraftMode == "agent_bridge" {
					title = "选择 Agent"
				}
				homeText(c, title, homeHeading).FontSize(15).FontWeight(500)
				ui.Spacer(c)
				if edit {
					textButton(c, "添加配置", nil).Disabled(s.Busy || s.SaveOperation != "").OnClick(func() {
						a.newGenerationProfile(s.DraftMode)
					})
				}
			})
		}
		if len(groups) == 0 {
			homeText(c, "尚未添加配置", homeMeta)
		}
		for _, group := range groups {
			if group.Key != "agent" {
				homeText(c, group.Title, homeMeta).Margin(10, 0, 2, 0)
			}
			surface := settingsListSurface
			if !edit {
				surface = func(c *ui.Context, content func()) { ui.Column(c).FillWidth().Gap(0).Children(content) }
			}
			surface(c.Key(group.Key), func() {
				for index, item := range group.Items {
					id, row := item.ID, item.Row
					ref := &GenerationRef{ID: id, Revision: row.Revision}
					if index > 0 {
						settingsListDivider(c)
					}
					settingsListRow(c.Key("model-profile-"+id), func() {
						if item.Icon != "" {
							ui.Box(c).Size(30, 30).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() { agentLogo(c, agentBrandIcon(item.Icon), 23) })
							homeText(c, item.Title, homeBody).Width(90).Shrink(0).FontSize(13).FontWeight(500).TextColor(homeInk).SingleLine()
							homeText(c, item.Detail, homeMeta).Grow(1).MinWidth(0).SingleLine().Tooltip(item.Detail)
							if !s.connectedProcessingAgent(item.Icon) {
								homeText(c, "未连接", homeMeta).SingleLine()
							}
						} else {
							homeText(c, item.Title, homeBody).Grow(1).MinWidth(0).FontSize(13).TextColor(homeInk).SingleLine().Tooltip(item.Title)
							state, color := "未验证", homeMuted
							if s.Generation.validated(ref) {
								state, color = "已验证", ui.Hex("#4d7a66")
							}
							if active := s.Generation.Active; active != nil && *active == *ref {
								state, color = "当前使用", ui.Hex("#4d7a66")
							}
							homeText(c, state, homeMeta).TextColor(color).SingleLine()
						}
						choice := s.ProfileID
						ui.Radio(c, &choice, id, "").Label("配置模型：" + id).Shrink(0).Disabled(s.Busy || (item.Icon != "" && !s.connectedProcessingAgent(item.Icon))).OnChange(func() { a.selectGenerationProfile(id, row) })

						if edit && row.Profile.Mode == "provider" {
							reason := s.generationDeleteBlock(id)
							textButton(c, "删除", func() { s.DeleteProfile = ref; s.DeleteOpen = true; s.DeleteError = "" }).Label("删除模型：" + id).Disabled(s.Busy || s.SaveOperation != "" || reason != "").Tooltip(reason)
						}
					})
				}
			})
		}
	})
}
