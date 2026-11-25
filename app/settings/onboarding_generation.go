package settings

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

var onboardingKeyIcon = kitui.IconSVG(`<circle cx="7" cy="7" r="4"/><path d="m10 10 7 7m-2.5-2.5 2.5-2.5"/>`)

func (a *Controller) prepareOnboardingProcessDraft() {
	s := &a.integrations
	if !s.Editor && s.generationReady() && !s.Busy {
		if cli := a.firstAvailableOnboardingCLI(); cli != "" {
			a.chooseOnboardingGeneration("agent_bridge", cli)
		} else {
			a.chooseOnboardingGeneration("provider", "")
		}
	}
}

func (a *Controller) onboardingProcess(c *ui.Context) {
	tok := a.tokens()
	s, g := &a.integrations, &a.onboardingGeneration
	g.inputFocused = false
	onboardingHeading(tok, c, onboardingProcess, "选择如何提炼记忆", "Memlane 需要一个大模型，把采集到的对话整理成可召回的记忆。以后可在「设置 › 记忆提炼」随时切换。")
	if a.environment.Error != "" {
		kitui.Text(c, tok, a.environment.Error, kitui.RoleMeta).TextColor(tok.Danger)
	}
	locked := s.Busy || g.phase == onboardingActivating && g.failure != ""
	ui.Row(c.Key("onboarding-generation-method")).Role(ui.RoleRadioGroup).FillWidth().Gap(sp3).Margin(sp3, 0, 0, 0).Children(func() {
		a.onboardingGenerationCard(c, "用已连接的 Agent", "复用 Codex、pi 等 CLI 的账号与额度，无需 API Key。",
			onboardingTerminalIcon, s.DraftMode == "agent_bridge", a.firstAvailableOnboardingCLI() != "", locked, func() {
				a.chooseOnboardingGeneration("agent_bridge", a.firstAvailableOnboardingCLI())
			})
		a.onboardingGenerationCard(c, "直接调用模型 API", "填写 OpenAI 兼容接口或本地 Ollama，由 Memlane 直接调用。",
			onboardingKeyIcon, s.DraftMode == "provider", false, locked, func() {
				a.chooseOnboardingGeneration("provider", "")
			})
	})
	switch s.DraftMode {
	case "agent_bridge":
		a.onboardingGenerationCLIs(c, locked)
	case "provider":
		a.onboardingGenerationAPI(c, locked)
	}
	if !s.generationReady() {
		label := "正在读取记忆提炼配置…"
		if s.GenerationError != "" {
			label = s.GenerationError
		}
		kitui.Text(c, tok, label, kitui.RoleMeta)
		kitui.SmallButton(c, tok, "重新检测", kitui.IconRefresh, a.RefreshIntegrations).Disabled(s.Loading || locked)
	}
	for _, op := range s.PendingOperations {
		if op.connection() {
			continue
		}
		ui.Row(c.Key("onboarding-generation-operation-" + op.Operation)).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
			kitui.Text(c, tok, "配置操作结果待确认", kitui.RoleMeta).Grow(1)
			kitui.SmallButton(c, tok, "核对原操作", kitui.IconRefresh, func() {
				a.reviewPendingGeneration(op)
			}).Disabled(s.Busy)
		})
	}
	if s.hasPendingGeneration() && s.Error != "" {
		kitui.Text(c, tok, s.Error, kitui.RoleMeta).TextColor(tok.Danger)
	}
	a.onboardingGenerationProgress(c)
}

func (a *Controller) onboardingGenerationCard(c *ui.Context, title, desc string, icon *ui.SVG, selected, recommended, disabled bool, choose func()) {
	tok := a.tokens()
	bg, stroke, iconBackground, iconColor := tok.Bg, tok.Border, tok.Fill2, tok.Text2
	if selected {
		bg, stroke, iconBackground, iconColor = tok.AccentSofter, tok.Accent, tok.AccentSoft, tok.Accent
	}
	b := ui.ButtonBase(c.Key(title)).Role(ui.RoleRadio).Label(title).Selected(selected).Disabled(disabled).
		Grow(1).Basis(0).MinWidth(0).MinHeight(110).Padding(sp3).
		Border(1, stroke).Radius(8).Cursor(ui.CursorPointer).OnClick(choose)
	if disabled {
		b.Background(bg)
	} else {
		b.Background(kitui.HoverColor(b, "onboarding-generation-card-bg", bg, tok.Hover))
	}
	kitui.ButtonPress(b, 0.99)
	b.Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Start).Children(func() {
				ui.Row(c).Size(30, 30).Radius(8).Background(iconBackground).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c, icon).FontSize(16).TextColor(iconColor)
				})
				ui.Spacer(c)
				onboardingGenerationRadio(tok, c, selected)
			})
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				kitui.Text(c, tok, title, kitui.RoleBody).FontWeight(600)
				if recommended {
					kitui.Badge(c, tok, "推荐", "accent")
				}
			})
			ui.Text(c, desc).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.55)
		})
	})
}

func onboardingGenerationRadio(tok *kitui.Tokens, c *ui.Context, selected bool) {
	bg, stroke := ui.Transparent, tok.BorderStrong
	if selected {
		bg, stroke = tok.Accent, tok.Accent
	}
	ui.Row(c).Size(16, 16).Shrink(0).Radius(8).Border(1, stroke).Background(bg).
		AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		if selected {
			ui.Icon(c, kitui.IconCheck).FontSize(12).TextColor(tok.OnAccent)
		}
	})
}

func (a *Controller) firstAvailableOnboardingCLI() string {
	for _, cli := range bootstrap.RecommendedProcessingCLIs() {
		if profile, ok := a.integrations.Generation.AgentBridges[cli.Kind]; ok && profile.Model != "" {
			return cli.Kind
		}
	}
	return ""
}

func (a *Controller) chooseOnboardingGeneration(mode, kind string) {
	s, g := &a.integrations, &a.onboardingGeneration
	if s.Busy || (s.Editor && s.DraftMode == mode && (mode == "provider" || s.BridgeKind == kind)) {
		return
	}
	g.failure, g.phase, g.validation = "", onboardingGenerationIdle, GenerationValidation{}
	if mode == "provider" {
		// Keep the draft when switching sources within the wizard.
		if id := s.GenerationModeSelection[mode]; id != "" {
			if d, found := s.GenerationDrafts[id]; found {
				a.selectGenerationProfile(id, GenerationRow{Profile: d.Profile})
				return
			}
		}
	}
	for _, group := range generationWorkspaceGroups(s.Generation, mode) {
		for _, item := range group.Items {
			if mode == "provider" || kind == item.Icon {
				a.selectGenerationProfile(item.ID, item.Row)
				return
			}
		}
	}
	if mode == "provider" {
		a.selectGenerationProfile("api-"+integrationOperation(), GenerationRow{Profile: GenerationProfile{
			Mode: "provider", Provider: "custom", Endpoint: "https://api.openai.com/v1", Model: "gpt-4.1-mini",
		}})
		return
	}
	a.selectGenerationProfile("agent-"+kind, GenerationRow{Profile: GenerationProfile{Mode: mode, BridgeKind: kind}})
}

func (a *Controller) onboardingGenerationCLIs(c *ui.Context, locked bool) {
	tok := a.tokens()
	s := &a.integrations
	hasReady := a.firstAvailableOnboardingCLI() != ""
	hasCLI := hasReady || a.hasDetectedProcessingCLI()
	title := "用哪个 Agent 提炼"
	if !hasCLI {
		title = "未检测到可用于提炼的本机 CLI"
	}
	kitui.Group(c, tok, title, "", func() {
		kitui.GhostButton(c, tok, "重新检测", kitui.IconRefresh, func() {
			a.refreshLocalHosts()
			a.RefreshIntegrations()
		}).Disabled(locked || s.Loading)
	}, func() {
		for _, cli := range bootstrap.RecommendedProcessingCLIs() {
			profile, ready := s.Generation.AgentBridges[cli.Kind]
			ready = ready && profile.Model != ""
			if !ready && !a.isDetectedProcessingCLI(cli.Kind) {
				continue
			}
			selected := ready && s.BridgeKind == cli.Kind
			login := map[string]string{"codex": "codex login", "pi": "pi", "traex": "traecli login", "antigravity": "agy"}[cli.Kind]
			desc := "尚未登录 · " + cli.LoginHint
			if ready {
				desc = generationDefaultModel(s.Generation, cli.Kind) + " · 已登录"
			}
			title := cli.DisplayName
			var titleExtra func()
			if !ready {
				title = ""
				titleExtra = func() {
					ui.Text(c, cli.DisplayName).FontSize(fsBody).FontWeight(500).TextColor(tok.Text3)
				}
			}
			kitui.Row(c, tok, kitui.RowOptions{
				Key: "onboarding-cli-" + cli.Kind, Title: title, TitleExtra: titleExtra, Desc: desc,
				Clickable: ready && !locked, HideChevron: true, Label: "使用 " + cli.DisplayName + " 提炼",
				OnClick: func() { a.chooseOnboardingGeneration("agent_bridge", cli.Kind) },
				Leading: func() {
					ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
						onboardingGenerationRadio(tok, c, selected)
						icon := ui.Image(c, agentOriginalIcon(cli.Kind)).Size(24, 24).Fit(ui.Contain)
						if !ready {
							icon.Opacity(0.45)
						}
					})
				},
				Control: func() {
					if selected {
						kitui.Badge(c, tok, "将使用", "accent")
					} else if !ready {
						kitui.SmallButton(c, tok, "复制登录命令", kitui.IconCopy, func() {
							c.WriteClipboard(login)
							a.showOnboardingToast("已复制 " + cli.DisplayName + " 登录命令")
						}).Label("复制 " + cli.DisplayName + " 登录命令")
					}
				},
			})
		}
		if hasReady {
			kitui.Text(c, tok, "提炼使用该 Agent 当前登录的账号与默认模型，会消耗它的额度。", kitui.RoleMeta)
		} else {
			ui.Row(c).FillWidth().Gap(sp1).AlignItems(ui.Center).Children(func() {
				kitui.Text(c, tok, "安装任一 CLI 并登录，即可复用它的账号额度。", kitui.RoleMeta)
				kitui.TextButton(c, tok, "改用模型 API", nil).OnClick(func() { a.chooseOnboardingGeneration("provider", "") })
			})
			a.recommendedProcessingCLIRows(c, &s.Message)
		}
	})
	if s.Message != "" {
		kitui.Text(c, tok, s.Message, kitui.RoleMeta)
	}
}

func (a *Controller) onboardingGenerationAPI(c *ui.Context, locked bool) {
	tok := a.tokens()
	s, g := &a.integrations, &a.onboardingGeneration
	halfWidth := (float32(onboardingWidth) - sp3) / 2
	field := func(width float32, label, key string, value *string, placeholder string, password bool) {
		ui.Column(c.Key("onboarding-api-field-" + key)).Width(width).Gap(5).Children(func() {
			ui.Text(c, label).FontSize(fsFootnote).TextColor(tok.Text2)
			inputLabel := label
			if key == "model" {
				inputLabel = "模型 ID"
			}
			input := ui.TextInput(c.Key(fmt.Sprintf("%s-%d", key, s.KeyEpoch)), value).
				Label(inputLabel).Placeholder(placeholder).FillWidth().Height(32).Font("ui-monospace").FontSize(fsFootnote).Disabled(locked).
				OnSubmit(func() {})
			if key == "model" && g.focusModel {
				input.Focus()
				g.focusModel = false
			}
			if password {
				input.Password()
			}
			g.inputFocused = g.inputFocused || input.Focused()
		})
	}
	ui.Column(c.Key("onboarding-generation-api")).FillWidth().Gap(sp3).Children(func() {
		kitui.Text(c, tok, "模型接口", kitui.RoleMeta).FontWeight(600)
		ui.Row(c).FillWidth().Gap(sp3).Children(func() {
			ui.Column(c).Width(halfWidth).Gap(5).Children(func() {
				ui.Text(c, "服务类型").FontSize(fsFootnote).TextColor(tok.Text2)
				selector := ui.Select(c.Key("onboarding-api-protocol"), &s.ProviderLabel, []string{"OpenAI 兼容", "OpenAI", "Anthropic", "Gemini", "Ollama"}).
					Label("服务类型").FillWidth().Height(32).Disabled(locked)
				selector.OnChange(func() {
					// A credential from another protocol must be deliberately entered again.
					s.CredentialVersion, s.APIKey = "", ""
					s.CredentialConfigured = false
					if s.ProviderLabel == "Ollama" {
						s.Endpoint = "http://127.0.0.1:11434"
					}
				})
			})
			field(halfWidth, "模型", "model", &s.Model, "实际模型 ID", false)
		})
		field(onboardingWidth, "接口地址", "endpoint", &s.Endpoint, "https://…", false)
		if s.ProviderLabel != "Ollama" {
			placeholder := "只保存在本机受管凭据中"
			if s.CredentialConfigured {
				placeholder = "已保存时留空保留"
			}
			field(onboardingWidth, "API Key", "key", &s.APIKey, placeholder, true)
		}
		disclosure := kitui.IconChev
		if g.advanced {
			disclosure = kitui.IconDown
		}
		kitui.GhostButton(c, tok, "高级参数（采样温度、最大输出 Token，留空即不发送）", disclosure, func() {
			g.advanced = !g.advanced
		}).AlignSelf(ui.Start)
		if g.advanced {
			ui.Row(c).FillWidth().Gap(sp3).Children(func() {
				field(halfWidth, "采样温度", "temperature", &s.Temperature, "未设置", false)
				field(halfWidth, "最大输出 Token", "output-tokens", &s.MaxOutputTokens, "未设置", false)
			})
			if err := s.generationParameterError(); err != nil {
				kitui.Text(c, tok, err.Error(), kitui.RoleMeta).TextColor(tok.Danger)
			}
		}
	})
}

func (a *Controller) onboardingProcessAction() (string, bool, func()) {
	s, g := &a.integrations, &a.onboardingGeneration
	if s.Busy && g.phase != onboardingGenerationIdle {
		return "正在验证并启用…", true, nil
	}
	blocked := !a.environment.RuntimeStarted || a.environment.staleRuntimeOwner() || a.environment.Busy ||
		s.Busy || s.Loading || !a.canDispatch() || !s.generationReady() ||
		a.environment.RuntimePreparation.ConfigPath == "" || s.hasPendingGeneration() || generationValidationActive(s.Validation)
	label := "验证并启用"
	if g.failure != "" {
		label = "重新验证"
		if g.phase == onboardingActivating && g.activationRef != nil {
			return "核对启用结果", blocked, a.runOnboardingGeneration
		}
	}
	profile := s.draftProfile()
	eligible := profile.Mode == "agent_bridge" && s.availableProcessingBridge(profile.BridgeKind) && profile.Model != ""
	if profile.Mode == "provider" {
		eligible = profile.Model != "" && profile.Endpoint != "" && s.generationParameterError() == nil &&
			(profile.Provider == "ollama" || strings.TrimSpace(s.APIKey) != "" || s.CredentialConfigured)
	}
	return label, blocked || !eligible, a.runOnboardingGeneration
}

func (a *Controller) onboardingProcessEditAction() func() {
	g := &a.onboardingGeneration
	if g.failure == "" || g.phase == onboardingActivating || a.integrations.Busy || a.integrations.hasPendingGeneration() {
		return nil
	}
	return func() {
		g.failure, g.phase, g.validation = "", onboardingGenerationIdle, GenerationValidation{}
		g.focusModel = a.integrations.DraftMode == "provider"
	}
}

func (a *Controller) onboardingGenerationProgress(c *ui.Context) {
	tok := a.tokens()
	g := &a.onboardingGeneration
	if g.phase == onboardingGenerationIdle {
		g.shownPhase, g.shownFailure, g.shownSlow = g.phase, "", false
		return
	}
	elapsed := g.validationElapsed
	if g.phase == onboardingValidating && g.failure == "" {
		c.After(time.Second)
		elapsed = time.Since(g.validationStarted)
	}
	slow := g.phase == onboardingValidating && g.failure == "" && elapsed >= 15*time.Second
	progress := ui.Column(c.Key("onboarding-generation-progress")).FillWidth().Radius(8).Background(tok.Fill1).Padding(sp4).Gap(sp1).
		Role(ui.RoleStatus).Children(func() {
		a.onboardingGenerationProgressRow(c, onboardingSaving, "保存配置", "")
		timing := ""
		if g.phase >= onboardingValidating {
			timing = fmt.Sprintf("%.1f 秒", elapsed.Seconds())
		}
		a.onboardingGenerationProgressRow(c, onboardingValidating, "发送一次简短测试请求", timing)
		if slow {
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				kitui.Text(c, tok, "响应较慢… 模型或网络仍在处理。", kitui.RoleMeta).TextColor(tok.Warning)
				kitui.SmallButton(c, tok, "停止验证", nil, a.stopOnboardingValidation).Disabled(g.stopping)
			})
		}
		a.onboardingGenerationProgressRow(c, onboardingSelecting, "启用并启动记忆提炼", "")
		if g.failure == "" {
			return
		}
		kitui.Text(c, tok, g.failure, kitui.RoleMeta).TextColor(tok.Danger)
		if diagnostic := generationValidationDiagnosticLine(g.validation); diagnostic != "" {
			kitui.Text(c, tok, diagnostic, kitui.RoleMeta)
		}
		if suggestion := generationValidationNextStep(g.validation); suggestion != "" {
			kitui.Text(c, tok, suggestion, kitui.RoleMeta)
		} else if g.phase > onboardingSaving && g.phase < onboardingActivating {
			kitui.Text(c, tok, "已保留当前保存的配置，可以核对模型权限、凭据和服务状态后重试。", kitui.RoleMeta)
		} else if g.phase == onboardingActivating {
			kitui.Text(c, tok, "已保留配置与启用回执，可核对原启用结果。", kitui.RoleMeta)
		} else {
			kitui.Text(c, tok, "请检查填写的模型、接口地址和凭据。", kitui.RoleMeta)
		}
	})
	if g.shownPhase != g.phase || g.shownFailure != g.failure || slow && !g.shownSlow {
		g.shownPhase, g.shownFailure, g.shownSlow = g.phase, g.failure, slow
		progress.ScrollIntoView()
	}
}

func (a *Controller) onboardingGenerationProgressRow(c *ui.Context, phase onboardingGenerationPhase, label, metric string) {
	tok := a.tokens()
	g := &a.onboardingGeneration
	active := g.phase == phase || (phase == onboardingSelecting && g.phase == onboardingActivating)
	ui.Row(c.Key(label)).FillWidth().MinHeight(30).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Size(18, 18).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			switch {
			case active && g.failure != "":
				ui.Icon(c, kitui.IconClose).FontSize(14).TextColor(tok.Danger)
			case active:
				kitui.Spinner(c, tok)
			case g.phase > phase:
				ui.Icon(c, kitui.IconCheck).FontSize(14).TextColor(tok.OK)
			default:
				kitui.StatusDot(c, tok, "")
			}
		})
		kitui.Text(c, tok, label, kitui.RoleBody).Grow(1)
		if metric != "" {
			ui.Text(c, metric).FontSize(fsFootnote).FontFeatures("tnum").TextColor(tok.Text3)
		}
	})
}
