package simpledesktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func connectionUnavailable(row AgentConnection) bool {
	switch row.Status {
	case "configured_pending_new_session", "prepared", "requires_review", "activation_unknown", "preparation_unknown", "staging_unknown", "agent_creation_unconfirmed":
		return false
	case "integration_pending", "component_unavailable":
		return true
	}
	return !row.Detected
}

func (s *integrationState) connectionSelectionAction() (string, []string) {
	var prepare, activate []string
	for _, row := range s.Connections.Connections {
		if !s.Selected[row.ID] || s.pendingConnection(row.ID) != nil {
			continue
		}
		if connectionActionAllowed(row, "activate") {
			activate = append(activate, row.ID)
		} else if connectionActionAllowed(row, "prepare") {
			prepare = append(prepare, row.ID)
		}
	}
	if len(prepare) > 0 {
		return "prepare", prepare
	}
	return "activate", activate
}

func (a *App) agentConnectionSettings(c *ui.Context) {
	s := &a.integrations
	a.refreshAgentReceiptsIfDue(time.Now())
	ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			homeText(c, "Agent 连接", homeHeading).FontSize(16)
			homeText(c, "连接后，自动保存会话并使用项目记忆。", homeMeta)
		})
		textButton(c, "重新检测", nil).Disabled(s.Busy || s.Loading).OnClick(a.RefreshIntegrations)
	})
	if s.Loading {
		homeText(c, "正在检测…", homeMeta)
	}
	if !s.Loaded {
		homeText(c, "正在读取连接状态…", homeMeta)
		if s.Error != "" {
			homeText(c, s.Error, homeMeta)
			commonButton(c, "重试", false, a.RefreshIntegrations)
		}
		return
	}
	if s.Selected == nil {
		s.Selected = map[string]bool{}
	}
	var available, unavailable []AgentConnection
	for _, row := range s.Connections.Connections {
		if connectionUnavailable(row) {
			unavailable = append(unavailable, row)
		} else {
			available = append(available, row)
		}
	}
	a.agentConnectionGrid(c, available)
	if len(unavailable) > 0 {
		label := fmt.Sprintf("其他 Agent（%d）", len(unavailable))
		if s.ShowUnavailable {
			label = "收起其他 Agent"
		}
		projectLink(c, label, func() { s.ShowUnavailable = !s.ShowUnavailable })
		if s.ShowUnavailable {
			a.agentConnectionGrid(c, unavailable)
		}
	}
	for _, pending := range s.PendingOperations {
		if !pending.connection() {
			continue
		}
		o := pending
		name := "Agent"
		for _, row := range s.Connections.Connections {
			if row.ID == o.Profile {
				name = connectionName(row.Kind)
				break
			}
		}
		ui.Row(c.Key("pending-connection-" + o.Operation)).Gap(12).Children(func() {
			homeText(c, name+" 的连接结果尚未确认", homeMeta).Grow(1)
			textButton(c, "检查连接", nil).Label("检查连接：" + name + " " + o.Operation).Disabled(s.Busy).OnClick(func() { a.reviewConnectionOperation(o) })
		})
	}
	action, ids := s.connectionSelectionAction()
	selectionCount := 0
	for _, row := range s.Connections.Connections {
		if s.Selected[row.ID] && s.pendingConnection(row.ID) == nil && (connectionActionAllowed(row, "prepare") || connectionActionAllowed(row, "activate")) {
			selectionCount++
		}
	}
	if len(ids) > 0 || s.Busy {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			label := fmt.Sprintf("连接所选（%d）", selectionCount)
			if action == "activate" {
				label = fmt.Sprintf("确认连接（%d）", len(ids))
			}
			if s.Busy {
				label = "正在配置连接…"
			}
			ui.Button(c, label).Disabled(s.Busy || len(ids) == 0).OnClick(func() { a.connectAgents(action, ids) })
			if s.BatchStop != nil {
				textButton(c, "取消剩余连接", nil).OnClick(func() { s.BatchStop.Store(true); s.Message = "当前项完成后停止，已完成的连接会保留" })
			}
		})
	}
	if s.Message != "" {
		homeText(c, s.Message, homeMeta)
	}
	if s.Error != "" {
		homeText(c, s.Error, homeMeta)
	}
}

func (a *App) agentConnectionRow(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	name := connectionName(row.Kind)
	ui.Column(c.Key("agent-connection-"+row.ID)).FillWidth().Grow(1).MinHeight(84).Gap(8).Padding(12).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Icon(c, agentBrandIcon(row.Kind)).FontSize(24).TextColor(homeInk).Shrink(0)
			homeText(c, name, homeBody).TextColor(homeInk).FontSize(13).Grow(1)
			allowed := connectionActionAllowed(row, "prepare") || connectionActionAllowed(row, "activate")
			if allowed {
				selected := s.Selected[row.ID]
				ui.Checkbox(c.Key("select-agent-"+row.ID), &selected, "").Label("选择 " + name).Disabled(s.Busy || s.pendingConnection(row.ID) != nil).OnChange(func() { s.Selected[row.ID] = selected })
			}
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			status := homeText(c, connectionStatus(row), homeMeta)
			if row.Status == "configured_pending_new_session" && row.NewSessions > 0 {
				status.TextColor(ui.Hex("#51806b"))
			}
			if row.Kind == "cursor" {
				status.Tooltip("会话开始时或调用工具后加载记忆；仅对话的回合可能延迟加载。")
			}
			ui.Spacer(c)
			if row.Profile != "" {
				if s.EnvironmentOpen == nil {
					s.EnvironmentOpen = map[string]bool{}
				}
				label := "连接环境"
				if s.EnvironmentOpen[row.ID] {
					label = "收起环境"
				}
				projectLink(c, label, func() { s.EnvironmentOpen[row.ID] = !s.EnvironmentOpen[row.ID] }).Label("查看或更换连接环境")
			}
			if row.Status == "activation_unknown" || row.Status == "preparation_unknown" || row.Status == "requires_review" || row.Status == "staging_unknown" || row.Status == "agent_creation_unconfirmed" {
				textButton(c, "检查连接", nil).Label("检查连接：" + name).Disabled(s.Busy).OnClick(func() { a.connectAgents("review", []string{row.ID}) })
			}
		})
		if row.Status == "prepared" && row.Preview != nil {
			ui.Column(c).FillWidth().Padding(12, 16).Gap(6).Radius(6).Background(ui.Hex("#f5f8fb")).Children(func() {
				homeText(c, "将为 "+name+" 添加 Memlane 连接，确认后生效。", homeMeta)
				if len(row.Preview.DisabledPlugins) > 0 {
					homeText(c, fmt.Sprintf("将替换 %d 个旧 Cognee 连接，其他插件和登录设置会保留。", len(row.Preview.DisabledPlugins)), homeMeta)
				}
				homeText(c, "连接后，重新打开 "+name+" 并新建会话。", homeMeta)
			})
		} else if row.Status == "configured_pending_new_session" && row.NewSessions == 0 {
			homeText(c, "在所选环境中重新打开 "+name+" 并新建会话，收到回执后确认连接。", homeMeta).Margin(0, 28)
			if row.ReceiptOverdue {
				homeText(c, "尚未收到这个环境的会话回执。请核对环境、插件启用与宿主信任状态，再重新检测。", homeMeta).Margin(0, 28)
			}
		}
		if row.Profile != "" {
			a.agentEnvironmentDetails(c, row)
		}
		if row.Status == "environment_receipt_unconfirmed" {
			homeText(c, "已收到会话，但加载的插件环境尚未核实。请检查插件版本与所选环境。", homeMeta)
		}
		if message := s.ItemMessages[row.ID]; message != "" {
			homeText(c, message, homeMeta).Margin(0, 28).LineHeight(1.6)
		}
	})
}

func (a *App) agentEnvironmentDetails(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	if s.EnvironmentOpen == nil {
		s.EnvironmentOpen = map[string]bool{}
	}
	if row.EnvironmentIssue != "" {
		message := "请选择实际使用的 Agent 配置目录。"
		if row.EnvironmentIssue == "verification_environment" {
			message = "当前指向验收环境，日常接入已阻止。请选择实际使用的环境。"
		}
		homeText(c, message, homeMeta).Margin(0, 28)
	}
	if !s.EnvironmentOpen[row.ID] {
		return
	}
	ui.Column(c.Key("environment-"+row.ID)).FillWidth().Padding(10, 16).Gap(8).Children(func() {
		homeText(c, "当前目标："+row.Profile, homeMeta)
		if row.LastReceiptAt > 0 {
			homeText(c, "最近会话回执已收到", homeMeta)
		}
		if row.EnvironmentVerified {
			homeText(c, "实际加载环境已核对 · 插件 "+row.PluginVersion, homeMeta)
		}
		for _, candidate := range row.Environments {
			option := candidate
			label := "自定义环境"
			if option.Source == "default" {
				label = "默认环境"
			} else if option.Source == "environment" {
				label = "启动环境指定"
			}
			if option.Verification {
				label = "验收环境 · 不可用于日常连接"
			}
			homeText(c, label+"："+option.Profile, homeMeta)
			if option.Exists && !option.Verification {
				ui.Button(c, "使用这个环境").Label("使用环境：" + option.Profile).Disabled(s.Busy || s.Loading).OnClick(func() {
					a.selectAgentEnvironment(row, option.Profile)
				})
			}
		}
		if a.ChooseProjectFolder != nil {
			textButton(c, "选择其他配置目录", nil).Disabled(s.Busy || s.Loading).OnClick(func() {
				path, err := a.ChooseProjectFolder()
				if err == nil && path != "" {
					a.selectAgentEnvironment(row, path)
				}
			})
		}
		homeText(c, "选择只更改接入目标；安装前仍需确认连接，已有登录和其他插件保留。", homeMeta)
	})
}

func generationLabel(g GenerationDirectory, ref *GenerationRef) string {
	row, ok := g.row(ref)
	if !ok {
		return "尚未确认"
	}
	if row.Profile.Mode == "agent_bridge" {
		return "Codex 订阅 · " + row.Profile.Model
	}
	provider := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama"}[row.Profile.Provider]
	return provider + " · " + row.Profile.Model
}
func (a *App) generationSetupPage(c *ui.Context) {
	ui.Column(c).Fill().Padding(28).Gap(20).Background(ui.Hex("#f8fafc")).Children(func() {
		homeText(c, "配置记忆处理", homeHeading).FontSize(22)
		homeText(c, "先保存配置并验证，再选择下次启动使用。完成后重新启动后台服务，所选配置才会生效。", homeMeta).LineHeight(1.7)
		ui.Scroll(c.Key("generation-first-setup")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(900).Gap(20).Children(func() { a.generationSettings(c) })
		})
	})
}

func (a *App) generationSettings(c *ui.Context) {
	s := &a.integrations
	a.generationSummaryCards(c)
	a.globalToolGraphSettings(c)
	if !s.Loaded {
		homeText(c, "记忆处理配置尚未载入", homeMeta)
		commonButton(c, "读取记忆处理配置", false, a.RefreshIntegrations)
		return
	}
	if s.hasPendingGeneration() {
		settingsCard(c, "待核对操作", "重开应用后保留原操作；核对只读取回执，不重新提交请求。", func() {
			for _, pending := range s.PendingOperations {
				if pending.connection() {
					continue
				}
				o := pending
				label := map[string]string{"save": "保存配置", "next": "下次启动设置", "validations": "模型验证"}[o.Kind]
				ui.Row(c.Key("pending-generation-" + o.Operation)).Gap(12).Children(func() {
					homeText(c, label+" · "+o.Profile, homeMeta).Grow(1)
					textButton(c, "核对原操作", nil).Label("核对原操作：" + o.Operation).Disabled(s.Busy).OnClick(func() { a.reviewPendingGeneration(o) })
				})
			}
		})
	}
	a.generationProfileCards(c)
	if !s.Editor {
		return
	}
	settingsCard(c, "配置记忆处理模型", "填写供应商提供的实际模型 ID 和 API 地址。更换生成模型不更换向量模型。", func() {
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
			ui.Field(c, "模型 ID", func() {
				ui.TextInput(fieldKey("model"), &s.Model).Label("处理模型 ID").Placeholder("实际模型 ID").FillWidth().Disabled(locked)
			})
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
					homeText(c, "已保存密钥；不会回填显示", homeMeta)
				}
			}
			if s.draftProfile().Provider == "ollama" {
				homeText(c, "填写Ollama 地址，例如 http://127.0.0.1:11434，无需 API Key；模型需已在服务中可用。", homeMeta)
			}
		})
		ui.Row(c).Gap(10).Children(func() {
			validationLabel := "验证（会消耗额度）"
			if s.draftProfile().Provider == "ollama" {
				validationLabel = "验证（调用模型）"
			}
			ui.Button(c, "保存配置").Disabled(locked || s.pendingGenerationProfile(s.ProfileID)).OnClick(a.saveGeneration)
			if s.SaveOperation != "" {
				textButton(c, "核对保存结果", nil).Disabled(s.Busy).OnClick(a.reviewGenerationSave)
			}
			ui.Button(c, validationLabel).Disabled(s.Busy || !s.cleanSaved() || s.Validation.Status == "running" || s.Validation.Status == "queued" || s.pendingGenerationProfile(s.ProfileID)).OnClick(func() { a.generationAction("validations") })
			ui.Button(c, "下次启动使用").Disabled(s.Busy || s.NextOperation != "" || s.pendingGenerationProfile(s.ProfileID) || !s.cleanSaved() || !s.Generation.validated(s.Saved)).OnClick(func() { a.generationAction("next") })
			if s.NextOperation != "" {
				textButton(c, "核对待应用设置", nil).Disabled(s.Busy).OnClick(a.reviewGenerationNext)
			}
		})
		if !s.cleanSaved() && s.SaveOperation == "" {
			homeText(c, "配置有未保存修改，保存后才能验证与设为下次启动使用。", homeMeta)
		}
		if s.ValidationOperation != "" {
			label := map[string]string{"queued": "验证已登记", "running": "正在验证", "passed": "验证通过", "failed": "验证未通过", "result_unknown": "验证结果未确认"}[s.Validation.Status]
			if !s.cleanSaved() {
				label = "已保存配置的原验证：" + label
			}
			homeText(c, label, homeMeta)
			if message := generationValidationMessage(s.Validation.ErrorCode); message != "" {
				homeText(c, message, homeMeta).LineHeight(1.6)
			}
			if s.Validation.ModeEvidence == "official_adapter_request" {
				mode := map[string]string{"json_schema": "本次结构化请求：Schema", "json_schema_non_strict": "本次结构化请求：非严格 Schema", "json_object": "本次结构化请求：JSON 对象"}[s.Validation.StructuredMode]
				if mode != "" {
					homeText(c, mode, homeMeta).LineHeight(1.6)
				}
			}
			textButton(c, "核对原验证", nil).Disabled(s.Busy).OnClick(func() { a.pollGenerationValidation(s.ValidationOperation) })
			if s.Validation.Status == "queued" || s.Validation.Status == "running" {
				textButton(c, "停止验证", nil).Disabled(s.Busy).OnClick(a.cancelGenerationValidation)
			}
		}
		if s.Message != "" {
			homeText(c, s.Message, homeMeta).LineHeight(1.7)
		}
		if s.Error != "" {
			homeText(c, s.Error, homeMeta).LineHeight(1.7)
		}
	})
}

func generationValidationMessage(code string) string {
	if code == "" {
		return ""
	}
	if message, ok := map[string]string{
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
