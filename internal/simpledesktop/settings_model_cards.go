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

func (a *App) generationSummaryCards(c *ui.Context) {
	s := &a.integrations
	settingsListSurface(c, func() {
		settingsListRow(c, func() {
			homeText(c, "当前使用", homeBody).FontSize(13).FontWeight(500).TextColor(homeInk).Width(100)
			generationAgentMark(c, s.Generation, s.Generation.Active)
			label := generationLabel(s.Generation, s.Generation.Active)
			homeText(c, label, homeBody).FontSize(13).TextColor(homeInk).Grow(1).MinWidth(0).SingleLine().Tooltip(label)
			if s.Generation.HandoffAvailable {
				textButton(c, "更换", func() { a.openGenerationHandoff(nil) }).Disabled(s.Busy || s.HandoffOperation != "")
			}
		})
		if s.Generation.Next != nil || s.Generation.NextError != "" || s.Generation.StartupError != "" {
			settingsListDivider(c)
			settingsListRow(c, func() {
				homeText(c, "下次启动", homeBody).FontSize(13).FontWeight(500).TextColor(homeInk).Width(100)
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					label := "沿用当前模型"
					if s.Generation.Next != nil {
						label = generationLabel(s.Generation, s.Generation.Next)
					}
					homeText(c, label, homeBody).FontSize(13).TextColor(homeInk).SingleLine().Tooltip(label)
					if s.Generation.NextError != "" || s.Generation.StartupError != "" {
						homeText(c, "上次切换未完成，当前模型仍在使用。", homeMeta)
					}
				})
			})
		}
	})
	a.generationHandoffStatus(c)
}

func (a *App) generationProfileCards(c *ui.Context) {
	s := &a.integrations
	groups := generationProfileGroups(s.Generation)
	if s.GenerationModeTab == "" {
		s.GenerationModeTab = "agent_bridge"
		if len(groups) > 0 && groups[0].Key != "agent" {
			s.GenerationModeTab = "provider"
		}
		if row, ok := s.Generation.row(s.Generation.Active); ok {
			s.GenerationModeTab = row.Profile.Mode
		}
	}
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			homeText(c, "处理方式", homeHeading).FontSize(15).FontWeight(500)
			ui.Spacer(c)
			textButton(c, "添加配置", nil).Disabled(s.Busy || s.SaveOperation != "").OnClick(func() {
				profile := GenerationProfile{Mode: "provider", Provider: "openai"}
				if s.GenerationModeTab == "agent_bridge" {
					profile = GenerationProfile{Mode: "agent_bridge", BridgeKind: "codex"}
				}
				s.editProfile("api-"+integrationOperation(), GenerationRow{Profile: profile})
				s.MaxOutputTokens, s.Saved, s.SavedSignature, s.Error, s.Message = "", nil, "", "", ""
			})
		})
		selected := 0
		if s.GenerationModeTab == "provider" {
			selected = 1
		}
		ui.Tabs(c, &selected, "Agent 桥接", "Provider").Label("记忆处理方式配置").OnChange(func() {
			if selected == 0 {
				s.GenerationModeTab = "agent_bridge"
			} else {
				s.GenerationModeTab = "provider"
			}
		})
		if len(groups) == 0 {
			homeText(c, "尚未添加配置", homeMeta)
		}
		for _, group := range groups {
			if (group.Key == "agent") != (s.GenerationModeTab == "agent_bridge") {
				continue
			}
			if group.Key != "agent" {
				homeText(c, group.Title, homeHeading).FontSize(15).FontWeight(500)
			}
			settingsListSurface(c.Key(group.Key), func() {
				for index, item := range group.Items {
					id, row := item.ID, item.Row
					ref := &GenerationRef{ID: id, Revision: row.Revision}
					if index > 0 {
						settingsListDivider(c)
					}
					settingsListRow(c.Key("model-profile-"+id), func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
							homeText(c, item.Title, homeBody).FontSize(13).FontWeight(500).TextColor(homeInk).SingleLine().Tooltip(item.Title)
							ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
								if item.Detail != "" {
									homeText(c, item.Detail, homeMeta)
								}
								state, color := "未验证", homeMuted
								if s.Generation.validated(ref) {
									state, color = "已验证", ui.Hex("#4d7a66")
								}
								if active := s.Generation.Active; active != nil && *active == *ref {
									state, color = "当前使用", ui.Hex("#4d7a66")
								} else if next := s.Generation.Next; next != nil && *next == *ref {
									state = "下次启动使用"
								}
								homeText(c, state, homeMeta).TextColor(color)
							})
						})
						textButton(c, "配置", nil).Label("配置模型：" + id).Disabled(s.Busy || s.SaveOperation != "").OnClick(func() { s.editProfile(id, row); s.Error, s.Message = "", "" })
						if row.Profile.Mode == "provider" {
							reason := s.generationDeleteBlock(id)
							textButton(c, "删除", func() { s.DeleteProfile = ref; s.DeleteOpen = true; s.DeleteError = "" }).Label("删除模型：" + id).Disabled(s.Busy || s.SaveOperation != "" || reason != "").Tooltip(reason)
						}
					})
				}
			})
		}
	})
}
