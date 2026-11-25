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
		return "尚未确认"
	}
	if row.Profile.Mode == "agent_bridge" {
		return "本机 Agent CLI · " + generationBridgeLabel(row.Profile.BridgeKind) + " · " + generationDefaultModel(g, row.Profile.BridgeKind)
	}
	provider := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama"}[row.Profile.Provider]
	return "模型 API · " + provider + " · " + row.Profile.Model
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
	a.generationPendingOperations(c)
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
	if s.hasPendingGeneration() {
		settingsCard(tok, c, "待核对操作", "重开应用后保留原操作；核对只读取回执，不重新提交请求。", func() {
			for _, pending := range s.PendingOperations {
				if pending.connection() {
					continue
				}
				o := pending
				label := map[string]string{"save": "保存配置", "next": "下次启动设置", "handoffs": "更换提炼模型", "validations": "模型验证", "delete": "删除配置"}[o.Kind]
				ui.Row(c.Key("pending-generation-" + o.Operation)).Gap(12).Children(func() {
					kitui.Text(c, tok, label+" · "+o.Profile, kitui.RoleMeta).Grow(1)
					kitui.TextButton(c, tok, "核对原操作", nil).Label("核对原操作：" + o.Operation).Disabled(s.Busy).OnClick(func() { a.reviewPendingGeneration(o) })
				})
			}
		})
	}
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
	if s.ValidationOperation != "" {
		label := generationValidationStageLabel(s.Validation)
		if generationValidationActive(s.Validation) {
			c.After(time.Second)
			elapsed := generationValidationElapsed(time.Now(), s.ValidationStartedAt, s.Validation)
			label += fmt.Sprintf(" · 已等待 %d 秒", elapsed/time.Second)
			if notice := generationValidationSlowNotice(elapsed, s.Validation); notice != "" {
				kitui.Text(c, tok, notice, kitui.RoleMeta).LineHeight(1.6)
			}
		}
		if !s.cleanSaved() {
			label = "已保存配置的原验证：" + label
		}
		kitui.Text(c, tok, label, kitui.RoleMeta)
		if message := generationValidationMessage(s.Validation.ErrorCode); message != "" {
			kitui.Text(c, tok, message, kitui.RoleMeta).LineHeight(1.6)
		}
		if diagnostic := generationValidationDiagnosticLine(s.Validation); diagnostic != "" {
			kitui.Text(c, tok, diagnostic, kitui.RoleMeta).LineHeight(1.6)
		}
		nextStep := generationValidationNextStep(s.Validation)
		if nextStep != "" {
			kitui.Text(c, tok, nextStep, kitui.RoleMeta).LineHeight(1.6)
		}
		if s.Validation.ModeEvidence == "official_adapter_request" {
			mode := map[string]string{"json_schema": "本次结构化请求：Schema", "json_schema_non_strict": "本次结构化请求：非严格 Schema", "json_object": "本次结构化请求：JSON 对象"}[s.Validation.StructuredMode]
			if mode != "" {
				kitui.Text(c, tok, mode, kitui.RoleMeta).LineHeight(1.6)
			}
		}
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			kitui.SmallButton(c, tok, "核对原验证", kitui.IconRefresh, func() { a.pollGenerationValidation(s.ValidationOperation) }).Disabled(s.Busy)
			if nextStep != "" {
				kitui.SmallButton(c, tok, "重新验证", kitui.IconRefresh, a.retryGenerationValidation).Disabled(!s.canRetryGenerationValidation())
			}
			if generationValidationActive(s.Validation) {
				kitui.DangerOutlineButton(c, tok, "停止验证", true, a.cancelGenerationValidation).Disabled(s.Busy)
			}
		})
	}
}

func generationValidationMessage(code string) string {
	if code == "" {
		return ""
	}
	if message, ok := map[string]string{
		"dns_resolution_failed":                "无法解析模型服务地址。",
		"provider_connection_unconfirmed":      "未能确认与模型服务的连接。",
		"provider_unavailable":                 "模型服务暂时不可用。",
		"probe_process_failed":                 "验证进程提前退出，结果未确认。",
		"generation_bridge_changed_revalidate": "Agent 的模型或配置在验证期间发生变化，请重新验证。",
		"validation_interrupted":               "验证已停止，结果未确认。",
		"validation_owner_interrupted":         "验证期间服务中断，结果未确认。",
		"validation_cleanup_unconfirmed":       "暂时无法确认验证进程已退出。",
		"validation_not_recorded":              "服务端未找到该次验证记录，可能尚未成功提交，请重新验证。",
		"pi_timeout":                           "Pi 模型请求超时。",
		"pi_model_failed":                      "Pi 模型请求失败。",
		"codex_timeout":                        "Codex 模型请求超时。",
		"codex_connection_failed":              "Codex 无法连接模型服务。",
		"codex_model_failed":                   "Codex 模型请求失败。",
		"antigravity_timeout":                  "Antigravity 模型请求超时。",
		"antigravity_model_failed":             "Antigravity 模型请求失败。",
		"traex_timeout":                        "TraeX 模型请求超时。",
		"traex_connection_failed":              "TraeX 无法连接模型服务。",
		"traex_model_failed":                   "TraeX 模型请求失败。",
		"authentication_failed":                "API 密钥无效，请检查凭据。",
		"permission_denied":                    "当前凭据没有访问此模型的权限。",
		"payment_required":                     "供应商额度或余额不足。",
		"quota_exhausted":                      "供应商可用额度已耗尽。",
		"rate_or_quota_limit":                  "供应商限制了请求或可用额度，请检查后再验证。",
		"provider_refusal":                     "模型拒绝了验证材料，本次验证未通过。",
		"completion_truncated":                 "模型输出被截断，本次验证未通过。",
		"schema_validation_failed":             "模型返回内容不符合所需的数据结构。",
		"native_schema_verification_failed":    "模型接口未通过结构化输出能力检测。",
		"structured_output_unsupported":        "此模型或接口不支持所需的结构化输出。",
		"unsupported_model_parameters":         "此模型或接口不支持所需的请求参数。",
		"plain_output_mismatch":                "普通回答未通过固定样本校验。",
		"extraction_quality_failed":            "抽取内容未通过固定事实校验。",
		"provider_timeout":                     "供应商请求超时，结果未确认；请核对原验证。",
		"validation_timeout":                   "验证等待超时，结果未确认；请核对原验证。",
		"provider_endpoint_or_model_not_found": "未找到配置的模型或 API 地址。",
		"provider_bad_request":                 "供应商拒绝了请求，请检查配置。",
		"provider_redirect_rejected":           "API 地址返回了重定向，已停止请求；请核对并明确配置最终地址。",
		"unexpected_model_destination":         "验证目标与已保存配置不一致，已停止请求。",
		"fallback_configuration_rejected":      "验证配置包含其他模型回退，已停止请求。",
	}[code]; ok {
		return message
	}
	return "本次验证未确认，请核对原结果。"
}
