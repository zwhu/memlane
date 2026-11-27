package settings

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func generationLabel(g GenerationDirectory, ref *GenerationRef) string {
	row, ok := g.row(ref)
	if !ok {
		return "尚未配置"
	}
	if row.Profile.Mode == "agent_bridge" {
		return generationBridgeLabel(row.Profile.BridgeKind) + " · " + generationDefaultModel(g, row.Profile.BridgeKind)
	}
	provider := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama"}[row.Profile.Provider]
	return provider + " · " + row.Profile.Model
}

func (a *Controller) generationSetupPage(c *ui.Context) {
	tok := a.tokens()
	ui.Column(c).Fill().Padding(28).Gap(20).Background(tok.Bg).Children(func() {
		kitui.Text(c, tok, "配置记忆提炼", kitui.RoleHeading).FontSize(22)
		kitui.Text(c, tok, "选择提炼记忆的 Agent 或模型。", kitui.RoleMeta).LineHeight(1.7)
		ui.Scroll(c.Key("generation-first-setup")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(900).Gap(20).Children(func() { a.generationSettings(c) })
		})
	})
}

func (a *Controller) generationSettings(c *ui.Context) {
	a.generationConfiguration(c, false)
}

func (a *Controller) generationConfiguration(c *ui.Context, showToolGraph bool) {
	a.generationConfigurationFor(c, true, true)
	if showToolGraph {
		a.globalToolGraphSettings(c)
	}
}

func (a *Controller) generationConfigurationFor(c *ui.Context, edit, enable bool) {
	s := &a.integrations
	if !s.generationReady() {
		a.generationLoadPrompt(c)
		return
	}
	if edit {
		a.generationPendingOperations(c)
	}
	a.generationWorkspaceFor(c, edit, enable)
	if edit {
		a.generationDeleteModal(c)
	}
}

func (a *Controller) generationLoadPrompt(c *ui.Context) {
	tok := a.tokens()
	kitui.Text(c, tok, "记忆提炼配置尚未载入", kitui.RoleMeta)
	if a.integrations.GenerationError != "" {
		kitui.Text(c, tok, a.integrations.GenerationError, kitui.RoleMeta).TextColor(tok.Danger)
	}
	kitui.TextButton(c, tok, "读取记忆提炼配置", a.RefreshIntegrations)
}

func (a *Controller) generationPendingOperations(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	var visible []generationUIOperation
	for _, pending := range s.PendingOperations {
		if pending.connection() || pending.Kind == "validations" {
			continue
		}
		visible = append(visible, pending)
	}
	if len(visible) == 0 {
		return
	}
	settingsCard(tok, c, "待核对操作", "上次操作尚未确认，点击核对最新状态。", func() {
		for _, o := range visible {
			op := o
			label := map[string]string{"save": "保存配置", "next": "下次启动设置", "handoffs": "切换提炼模型", "delete": "删除配置"}[op.Kind]
			ui.Row(c.Key("pending-generation-" + op.Operation)).Gap(12).Children(func() {
				kitui.Text(c, tok, label+" · "+op.Profile, kitui.RoleMeta).Grow(1)
				kitui.TextButton(c, tok, "核对原操作", nil).Label("核对原操作：" + op.Operation).Disabled(s.Busy).OnClick(func() { a.reviewPendingGeneration(op) })
			})
		}
	})
}

func (a *Controller) generationEditorPanel(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if !s.Editor {
		if s.APIKey != "" {
			s.APIKey = ""
			s.KeyEpoch++
		}
		return
	}
	settingsListSurface(tok, c.Key("generation-editor-"+s.ProfileID), func() { a.generationEditorContent(c) })
}

func (a *Controller) generationEditorContent(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		if !s.GenerationProviderDialog {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				title := "Provider 配置"
				if s.DraftMode == "agent_bridge" {
					title = generationBridgeLabel(s.BridgeKind) + " 配置"
				}
				kitui.Text(c, tok, title, kitui.RoleHeading).FontSize(15).FontWeight(500)
			})
		}
		ui.Column(c.Key("generation-editor-fields-" + s.ProfileID)).FillWidth().Gap(14).Children(func() {
			locked := s.Busy || s.SaveOperation != ""
			fieldKey := func(name string) *ui.Context {
				return c.Key(fmt.Sprintf("generation-%s-%s-%d", name, s.ProfileID, s.KeyEpoch))
			}
			ui.Form(c, func() {
				if s.DraftMode == "provider" {
					ui.Field(c, "供应商协议", func() {
						ui.Select(fieldKey("provider"), &s.ProviderLabel, []string{"OpenAI", "Anthropic", "Gemini", "OpenAI 兼容", "Ollama"}).FillWidth().Disabled(locked)
					})
					ui.Field(c, "API 地址", func() {
						ui.TextInput(fieldKey("endpoint"), &s.Endpoint).Label("Provider API 地址").Placeholder("https://…").FillWidth().Disabled(locked)
					})
				}
				if s.DraftMode == "agent_bridge" {
					ui.Field(c, "Agent 当前模型", func() {
						kitui.Text(c, tok, generationDefaultModel(s.Generation, s.BridgeKind), kitui.RoleBody)
						kitui.Text(c, tok, "模型与思考设置跟随 Agent。", kitui.RoleMeta)
					})
				} else {
					ui.Field(c, "模型 ID", func() {
						ui.TextInput(fieldKey("model"), &s.Model).Label("提炼模型 ID").Placeholder("实际模型 ID").FillWidth().Disabled(locked)
					})
				}
				if s.DraftMode == "provider" {
					ui.Field(c, "采样温度（可选）", func() {
						ui.TextInput(fieldKey("temperature"), &s.Temperature).Label("模型采样温度").Placeholder("例如 0.6").FillWidth().Disabled(locked)
					})
					ui.Field(c, "最大输出 Token（可选）", func() {
						ui.TextInput(fieldKey("output-tokens"), &s.MaxOutputTokens).Label("模型最大输出 Token").Placeholder("例如 16384").FillWidth().Disabled(locked)
					})
				}
				if s.DraftMode == "provider" && s.draftProfile().Provider != "ollama" {
					ui.Field(c, "API Key", func() {
						ui.TextInput(c.Key(fmt.Sprintf("generation-key-%d", s.KeyEpoch)), &s.APIKey).Label("Provider API Key").Placeholder("已保存时留空保留").Password().FillWidth().Disabled(locked)
					})
					if s.CredentialConfigured {
						kitui.Text(c, tok, "已保存密钥；不会回填显示", kitui.RoleMeta)
					}
				}
				if s.draftProfile().Provider == "ollama" {
					kitui.Text(c, tok, "填写Ollama 地址，例如 http://127.0.0.1:11434，无需 API Key；模型需已在服务中可用。", kitui.RoleMeta)
				}
			})
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				validationLabel := "验证（会消耗额度）"
				if s.draftProfile().Provider == "ollama" {
					validationLabel = "验证（调用模型）"
				}
				_, defaultAvailable := s.Generation.AgentBridges[s.BridgeKind]
				saveLabel := "保存配置"
				if a.initialSetup {
					saveLabel = "保存草稿"
				}
				kitui.PrimaryButton(c, tok, saveLabel, a.saveGeneration).Disabled(locked || s.pendingGenerationProfile(s.ProfileID) || (s.DraftMode == "agent_bridge" && !defaultAvailable))
				if s.SaveOperation != "" {
					kitui.SmallButton(c, tok, "核对保存结果", kitui.IconRefresh, a.reviewGenerationSave).Disabled(s.Busy)
				}
				kitui.OutlineButton(c, tok, validationLabel, nil, func() { a.generationAction("validations") }).Disabled(s.Busy || !s.cleanSaved() || s.Validation.Status == "running" || s.Validation.Status == "queued" || s.pendingGenerationProfile(s.ProfileID))
				if s.NextOperation != "" {
					kitui.SmallButton(c, tok, "核对待应用设置", kitui.IconRefresh, a.reviewGenerationNext).Disabled(s.Busy)
				}
				kitui.GhostButton(c, tok, "取消修改", nil, a.discardGenerationDraft).Disabled(s.Busy || s.SaveOperation != "")
			})
			if !s.cleanSaved() && s.SaveOperation == "" {
				kitui.Text(c, tok, "配置有未保存修改。", kitui.RoleMeta)
			}
			a.generationValidationStatus(c)
			if s.Message != "" {
				kitui.Text(c, tok, s.Message, kitui.RoleMeta).LineHeight(1.7)
			}
			if s.Error != "" {
				kitui.Text(c, tok, s.Error, kitui.RoleMeta).LineHeight(1.7)
			}
		})
	})
}

func (a *Controller) generationValidationStatus(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if s.ValidationOperation == "" {
		return
	}
	active := generationValidationActive(s.Validation)
	switch {
	case active:
		c.After(time.Second)
		elapsed := generationValidationElapsed(time.Now(), s.ValidationStartedAt, s.Validation)
		label := generationValidationStageLabel(s.Validation) + fmt.Sprintf(" · 已等待 %d 秒", elapsed/time.Second)
		if !s.cleanSaved() {
			label = "已保存配置的原验证：" + label
		}
		kitui.Text(c, tok, label, kitui.RoleMeta)
		if notice := generationValidationSlowNotice(elapsed, s.Validation); notice != "" {
			kitui.Text(c, tok, notice, kitui.RoleMeta).LineHeight(1.5)
		}
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			kitui.DangerOutlineButton(c, tok, "停止验证", true, a.cancelGenerationValidation).Disabled(s.Busy)
		})
	case s.Validation.Status == "passed":
		label := "验证通过"
		if !s.cleanSaved() {
			label = "已保存配置的原验证：" + label
		}
		kitui.Text(c, tok, label, kitui.RoleMeta)
	default:
		reason := generationValidationMessage(s.Validation.ErrorCode)
		if reason == "" {
			reason = "验证未通过，请检查配置后重试。"
		}
		if !s.cleanSaved() {
			reason = "已保存配置的原验证：" + reason
		}
		kitui.Text(c, tok, reason, kitui.RoleMeta).TextColor(tok.Danger).LineHeight(1.5)
		if diagnostic := generationValidationDiagnosticLine(s.Validation); diagnostic != "" {
			kitui.Text(c, tok, diagnostic, kitui.RoleMeta).LineHeight(1.5)
		}
		if s.Validation.ModeEvidence == "official_adapter_request" {
			mode := map[string]string{"json_schema": "本次结构化请求：Schema", "json_schema_non_strict": "本次结构化请求：非严格 Schema", "json_object": "本次结构化请求：JSON 对象"}[s.Validation.StructuredMode]
			if mode != "" {
				kitui.Text(c, tok, mode, kitui.RoleMeta).LineHeight(1.5)
			}
		}
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			kitui.SmallButton(c, tok, "重新验证", kitui.IconRefresh, a.retryGenerationValidation).Disabled(!s.canRetryGenerationValidation())
		})
	}
}

func generationValidationMessage(code string) string {
	if code == "" {
		return ""
	}
	if message, ok := map[string]string{
		"dns_resolution_failed":                "无法解析模型服务地址。",
		"provider_connection_unconfirmed":      "无法连接到模型服务，请检查地址和网络。",
		"provider_unavailable":                 "模型服务暂时不可用。",
		"probe_process_failed":                 "验证进程异常退出，请重试。",
		"probe_result_unconfirmed":             "验证未完成，请重试。",
		"generation_bridge_changed_revalidate": "Agent 模型配置已变更，请重新验证。",
		"validation_interrupted":               "验证已停止。",
		"validation_owner_interrupted":         "验证期间服务中断，请重试。",
		"validation_cleanup_unconfirmed":       "验证进程尚未完全退出，请稍后重试。",
		"validation_not_recorded":              "未找到该次验证记录，请重新验证。",
		"pi_timeout":                           "pi 请求超时。",
		"pi_model_failed":                      "pi 模型请求失败。",
		"codex_timeout":                        "Codex 请求超时。",
		"codex_connection_failed":              "Codex 无法连接模型服务。",
		"codex_model_failed":                   "Codex 模型请求失败。",
		"antigravity_timeout":                  "Gemini CLI 请求超时。",
		"antigravity_model_failed":             "Gemini CLI 模型请求失败。",
		"traex_timeout":                        "TRAE CLI 请求超时。",
		"traex_connection_failed":              "TRAE CLI 无法连接模型服务。",
		"traex_model_failed":                   "TRAE CLI 模型请求失败。",
		"authentication_failed":                "API 密钥无效，请检查凭据。",
		"permission_denied":                    "当前凭据没有访问此模型的权限。",
		"payment_required":                     "供应商额度或余额不足。",
		"quota_exhausted":                      "供应商可用额度已耗尽。",
		"rate_or_quota_limit":                  "触发限流或额度不足，请稍后重试。",
		"provider_refusal":                     "模型拒绝了验证请求。",
		"completion_truncated":                 "模型输出被截断，验证未通过。",
		"schema_validation_failed":             "模型返回内容不符合结构化格式要求。",
		"native_schema_verification_failed":    "模型接口未通过结构化输出检测。",
		"structured_output_unsupported":        "此模型不支持所需的结构化输出。",
		"unsupported_model_parameters":         "此模型不支持当前请求参数。",
		"plain_output_mismatch":                "基础对话校验未通过。",
		"extraction_quality_failed":            "结构化抽取校验未通过。",
		"provider_timeout":                     "模型响应超时，请重试。",
		"validation_timeout":                   "验证超时，请重试。",
		"provider_endpoint_or_model_not_found": "未找到配置的模型或 API 地址。",
		"provider_bad_request":                 "模型服务拒绝了请求，请检查配置。",
		"provider_redirect_rejected":           "API 地址返回了重定向，请填写最终地址。",
		"unexpected_model_destination":         "请求目标与已保存配置不一致。",
		"fallback_configuration_rejected":      "配置包含不支持的回退模型。",
	}[code]; ok {
		return message
	}
	return "验证未完成，请重试。"
}
