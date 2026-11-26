package settings

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func generationBridgeLabel(kind string) string {
	switch kind {
	case "pi":
		return "pi CLI"
	case "traex":
		return "TRAE CLI"
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

func generationAgentMark(tok *kitui.Tokens, c *ui.Context, g GenerationDirectory, ref *GenerationRef) {
	if row, ok := g.row(ref); ok && row.Profile.Mode == "agent_bridge" {
		agentLogo(tok, c, agentBrandIcon(row.Profile.BridgeKind), 18)
	}
}

func v4SelectionIndicator(tok *kitui.Tokens, c *ui.Context, selected bool) {
	if selected {
		ui.Box(c).Size(18, 18).Radius(9).Background(tok.AccentSoft).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, kitui.IconCheck).FontSize(12).TextColor(tok.Accent)
		})
		return
	}
	ui.Box(c).Size(18, 18).Radius(9).Border(1, tok.BorderStrong).Background(ui.Transparent).Shrink(0)
}

func (a *Controller) generationProfileCards(c *ui.Context, editable ...bool) {
	tok := a.tokens()
	edit := len(editable) == 0 || editable[0]
	s := &a.integrations
	if s.DraftMode == "agent_bridge" && !s.hasAvailableProcessingBridge() {
		a.emptyProcessingCLIGuide(c)
		return
	}
	groups := generationWorkspaceGroups(s.Generation, s.DraftMode)
	ui.Column(c).FillWidth().Gap(0).Children(func() {
		if edit {
			title := "选择模型"
			if s.DraftMode == "agent_bridge" {
				title = "选择本机 Agent CLI"
			}
			settingsSectionHeader(tok, c, title, nil, func() {
				kitui.SmallButton(c, tok, "添加配置", kitui.IconPlus, func() {
					a.newGenerationProfile(s.DraftMode)
				}).Disabled(s.Busy || s.SaveOperation != "")
			})
		}
		if len(groups) == 0 {
			kitui.Row(c, tok, kitui.RowOptions{
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
						available := s.availableProcessingBridge(iconKind)
						kitui.Row(c, tok, kitui.RowOptions{
							Key:         "model-profile-" + id,
							Title:       item.Title,
							Desc:        item.Detail,
							Label:       "配置模型：" + id,
							Clickable:   true,
							HideChevron: true,
							Disabled:    s.Busy || !available,
							OnClick:     func() { a.selectGenerationProfile(id, row) },
							Leading: func() {
								ui.Box(c).Size(24, 24).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
									agentLogo(tok, c, agentBrandIcon(iconKind), 20)
								})
							},
							Control: func() {
								if isActive {
									kitui.Badge(c, tok, "使用中", "ok")
								} else if !available {
									status := "未检测到 CLI"
									if a.isDetectedProcessingCLI(iconKind) {
										status = "待登录配置"
									}
									kitui.Text(c, tok, status, kitui.RoleMeta).SingleLine()
								} else if s.Generation.validated(ref) {
									kitui.Badge(c, tok, "已验证", "ok")
								} else {
									kitui.Badge(c, tok, "未验证", "")
								}
								v4SelectionIndicator(tok, c, isSelected)
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
							kitui.Row(c, tok, kitui.RowOptions{
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
									kitui.Badge(c, tok, state, tone)
									v4SelectionIndicator(tok, c, isSelected)
								},
							})
							continue
						}
						kitui.Row(c, tok, kitui.RowOptions{
							Key:      "model-profile-" + id,
							Title:    item.Title,
							Desc:     item.Detail,
							DescMono: item.Detail != "",
							Control: func() {
								kitui.Badge(c, tok, state, tone)
								selectLabel := "选择"
								var selectIcon *ui.SVG
								if isSelected {
									selectLabel = "已选"
									selectIcon = kitui.IconCheck
								}
								kitui.SmallButton(c, tok, selectLabel, selectIcon, func() { a.selectGenerationProfile(id, row) }).
									Label("配置模型：" + id).Disabled(s.Busy)
								if row.Profile.Mode == "provider" {
									reason := s.generationDeleteBlock(id)
									kitui.DangerOutlineButton(c, tok, "删除", true, func() {
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

func (a *Controller) preferredExistingHostName() string {
	for _, row := range a.integrations.Connections.Connections {
		if row.Detected || a.integrations.Selected[row.ID] || captureConnection(row) {
			if name := takeoverAgentName(row.Kind); name != "" {
				return name
			}
		}
	}
	for _, host := range a.environment.Hosts {
		if host.Detected && host.DisplayName != "" {
			return host.DisplayName
		}
	}
	for _, kind := range a.environment.SelectedAgents {
		if name := takeoverAgentName(kind); name != "" {
			return name
		}
	}
	return "当前 Agent"
}

func (a *Controller) emptyProcessingCLIGuide(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	unconfigured := a.detectedUnconfiguredProcessingCLIs()
	heading := "未检测到可用于记忆提炼的本地 CLI 工具"
	desc := "把对话自动整理成记忆需要一个后台提炼工具。你可以安装下面任意一个 CLI（复用已有账号额度），或改用直接调用模型 API / Ollama。"
	recheck := "已装好，重新检测"
	if len(unconfigured) > 0 {
		names := make([]string, 0, len(unconfigured))
		for _, cli := range unconfigured {
			names = append(names, cli.DisplayName)
		}
		heading = "已检测到本机 CLI，请先完成登录与默认模型配置"
		if a.initialSetup {
			desc = fmt.Sprintf("已检测到 %s，但尚未读到默认模型配置。请先在终端完成登录并选择默认模型后点击「已登录，重新检测」；也可以改用模型 API / Ollama，或先进入 Memlane 稍后配置。", strings.Join(names, "、"))
		} else {
			desc = fmt.Sprintf("已检测到 %s，但尚未读到默认模型配置。请先在终端完成登录并选择默认模型后点击「已登录，重新检测」，也可以改用模型 API / Ollama。", strings.Join(names, "、"))
		}
		recheck = "已登录，重新检测"
	} else if a.initialSetup {
		desc += "也可以点击「暂不配置提炼，先进入 Memlane」，稍后再配置。"
	}
	ui.Column(c.Key("empty-processing-cli-guide")).FillWidth().Margin(sp2, 0, sp2, 0).Padding(sp3).Gap(sp2).
		Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Children(func() {
		kitui.Text(c, tok, heading, kitui.RoleHeading)
		kitui.Text(c, tok, desc, kitui.RoleMeta).LineHeight(1.5)
		if len(unconfigured) == 0 {
			a.recommendedProcessingCLIRows(c, &s.Message)
		}
		for _, cli := range unconfigured {
			hint := strings.TrimPrefix(cli.LoginHint, "安装后")
			hint = strings.TrimPrefix(hint, "安装并")
			opts := kitui.RowOptions{Key: "detected-unconfigured-cli-" + cli.Kind, Title: cli.DisplayName, Desc: hint}
			if cli.DocsURL != "" {
				opts.Control = func() { a.processingCLIDocsButton(c, cli, &s.Message) }
			}
			kitui.Row(c, tok, opts)
		}
		ui.Row(c).FillWidth().Gap(sp2).Children(func() {
			kitui.SmallButton(c, tok, recheck, kitui.IconRefresh, a.RefreshIntegrations).Disabled(s.Busy || s.Loading)
			if len(unconfigured) == 0 {
				host := a.preferredExistingHostName()
				kitui.SmallButton(c, tok, "复制指令，让 "+host+" 帮我安装 CLI", kitui.IconCopy, func() {
					c.WriteClipboard(agentTakeoverInstallCLIPrompt(host))
					s.Message = "已复制 CLI 安装指令"
				})
			}
			kitui.SmallButton(c, tok, "改用模型 API / Ollama", nil, func() { a.chooseGenerationSource("provider") }).Disabled(s.Busy)
		})
		if s.Message != "" {
			kitui.Text(c, tok, s.Message, kitui.RoleMeta)
		}
	})
}

func (a *Controller) recommendedProcessingCLIRows(c *ui.Context, message *string) {
	tok := a.tokens()
	for _, cli := range bootstrap.RecommendedProcessingCLIs() {
		kitui.Row(c, tok, kitui.RowOptions{
			Key: "recommended-cli-" + cli.Kind, Title: cli.DisplayName, Desc: cli.LoginHint,
			DescExtra: func() {
				if cli.InstallCommand != "" {
					ui.Text(c, cli.InstallCommand).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text2).LineHeight(1.45)
				}
			},
			Control: func() {
				if cli.InstallCommand != "" {
					kitui.SmallButton(c, tok, "复制命令", kitui.IconCopy, func() {
						c.WriteClipboard(cli.InstallCommand)
						*message = "已复制 " + cli.DisplayName + " 安装命令"
					}).Label("复制命令 " + cli.DisplayName)
				}
				if cli.DocsURL != "" {
					a.processingCLIDocsButton(c, cli, message)
				}
			},
		})
	}
}

func (a *Controller) processingCLIDocsButton(c *ui.Context, cli bootstrap.RecommendedProcessingCLI, message *string) {
	tok := a.tokens()
	kitui.SmallButton(c, tok, "查看文档", nil, func() {
		if a.host.OpenExternalURL != nil {
			if err := a.host.OpenExternalURL(cli.DocsURL); err != nil {
				*message = bootstrap.RedactInstallerText(err.Error())
			}
		}
	}).Label("查看文档 " + cli.DisplayName).Disabled(a.host.OpenExternalURL == nil)
}
