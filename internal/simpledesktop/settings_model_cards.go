package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func generationBridgeLabel(kind string) string {
	switch kind {
	case "pi":
		return "pi CLI"
	case "traex":
		return "TraeX CLI"
	case "antigravity":
		return "Gemini CLI"
	default:
		return "Codex CLI"
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
		if kind == "antigravity" && actual.ConfigurationSource == "agy_cli_settings" {
			parts = append(parts, "agy CLI 默认配置")
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

func v4SelectionIndicator(c *ui.Context, selected bool) {
	if selected {
		ui.Box(c).Size(18, 18).Radius(9).Background(tok.AccentSoft).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, v4IconCheck).FontSize(12).TextColor(tok.Accent)
		})
		return
	}
	ui.Box(c).Size(18, 18).Radius(9).Border(1, tok.BorderStrong).Background(ui.Transparent).Shrink(0)
}

func (a *App) generationProfileCards(c *ui.Context, editable ...bool) {
	edit := len(editable) == 0 || editable[0]
	s := &a.integrations
	groups := generationWorkspaceGroups(s.Generation, s.DraftMode)
	ui.Column(c).FillWidth().Gap(0).Children(func() {
		if edit {
			title := "选择模型"
			if s.DraftMode == "agent_bridge" {
				title = "选择已连接的 Agent"
			}
			settingsSectionHeader(c, title, nil, func() {
				v4SmallButton(c, "添加配置", v4IconPlus, func() {
					a.newGenerationProfile(s.DraftMode)
				}).Disabled(s.Busy || s.SaveOperation != "")
			})
		}
		if len(groups) == 0 {
			v4Row(c, v4RowOptions{
				Title: "尚未添加配置",
				Desc:  "可前往「Agent 与模型」连接 Agent 或添加模型 Provider。",
			})
		}
		for _, group := range groups {
			if group.Key != "agent" {
				ui.Row(c).FillWidth().Padding(sp2, sp1, sp1, sp1).Children(func() {
					ui.Text(c, group.Title).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3).SingleLine()
				})
			}
			ui.Column(c.Key(group.Key)).FillWidth().Gap(0).Children(func() {
				for _, item := range group.Items {
					id, row := item.ID, item.Row
					ref := &GenerationRef{ID: id, Revision: row.Revision}
					isActive := s.Generation.Active != nil && *s.Generation.Active == *ref
					isSelected := s.ProfileID == id
					if item.Icon != "" {
						iconKind := item.Icon
						connected := s.connectedProcessingAgent(iconKind)
						v4Row(c, v4RowOptions{
							Key:         "model-profile-" + id,
							Title:       item.Title,
							Desc:        item.Detail,
							Label:       "配置模型：" + id,
							Clickable:   true,
							HideChevron: true,
							Disabled:    s.Busy || !connected,
							OnClick:     func() { a.selectGenerationProfile(id, row) },
							Leading: func() {
								ui.Box(c).Size(24, 24).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
									agentLogo(c, agentBrandIcon(iconKind), 20)
								})
							},
							Control: func() {
								if isActive {
									v4Badge(c, "使用中", "ok")
								} else if !connected {
									homeText(c, "CLI 未绑定", homeMeta).SingleLine()
								}
								v4SelectionIndicator(c, isSelected)
							},
						})
					} else {
						state, tone := "未验证", ""
						if s.Generation.validated(ref) {
							state, tone = "已验证", "ok"
						}
						if isActive {
							if edit {
								state, tone = "当前使用", "ok"
							} else {
								state, tone = "使用中", "ok"
							}
						}
						if !edit {
							v4Row(c, v4RowOptions{
								Key:         "model-profile-" + id,
								Title:       item.Title,
								Desc:        item.Detail,
								DescMono:    item.Detail != "",
								Label:       "配置模型：" + id,
								Clickable:   true,
								HideChevron: true,
								Disabled:    s.Busy,
								OnClick:     func() { a.selectGenerationProfile(id, row) },
								Control: func() {
									v4Badge(c, state, tone)
									v4SelectionIndicator(c, isSelected)
								},
							})
							continue
						}
						v4Row(c, v4RowOptions{
							Key:      "model-profile-" + id,
							Title:    item.Title,
							Desc:     item.Detail,
							DescMono: item.Detail != "",
							Control: func() {
								v4Badge(c, state, tone)
								selectLabel := "选择"
								var selectIcon *ui.SVG
								if isSelected {
									selectLabel = "已选"
									selectIcon = v4IconCheck
								}
								v4SmallButton(c, selectLabel, selectIcon, func() { a.selectGenerationProfile(id, row) }).
									Label("配置模型：" + id).Disabled(s.Busy)
								if row.Profile.Mode == "provider" {
									reason := s.generationDeleteBlock(id)
									v4DangerOutlineButton(c, "删除", true, func() {
										s.DeleteProfile = ref
										s.DeleteOpen = true
										s.DeleteError = ""
									}).Label("删除模型：" + id).Disabled(s.Busy || s.SaveOperation != "" || reason != "").Tooltip(reason)
								}
							},
						})
					}
				}
			})
		}
	})
}
