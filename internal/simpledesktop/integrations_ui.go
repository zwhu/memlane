package simpledesktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func isBetaSupportedAgentKind(kind string) bool {
	switch kind {
	case "codex", "antigravity", "pi", "traex":
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
	switch row.Status {
	case "configured_pending_new_session", "prepared", "requires_review", "activation_unknown", "preparation_unknown", "staging_unknown", "agent_creation_unconfirmed":
		return false
	case "integration_pending", "component_unavailable":
		return true
	}
	return !row.Detected
}

func (a *App) agentConnectionSettings(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(0).Children(func() { a.agentConnectionSettingsContent(c) })
}
func (a *App) agentConnectionSettingsContent(c *ui.Context) {
	s := &a.integrations
	a.refreshAgentReceiptsIfDue(time.Now())
	v4Group(c, "Agent 连接", "", func() {
		v4SmallButton(c, "重新检测", v4IconRefresh, a.RefreshConnections).Disabled(s.Busy || s.Loading)
	}, func() {
		if !s.Loaded {
			if s.Error != "" {
				v4Row(c, v4RowOptions{
					Title: "连接状态暂时不可用",
					Desc:  s.Error,
					Control: func() {
						v4SmallButton(c, "重试", v4IconRefresh, a.RefreshIntegrations)
					},
				})
			} else {
				v4SkeletonGroup(c, 3, 2, true, true)
			}
			return
		}
		if s.Selected == nil {
			s.Selected = map[string]bool{}
		}
		var available, unavailable []AgentConnection
		for _, row := range s.Connections.Connections {
			if !isAgentVisibleInBeta(row, s.pendingConnection(row.ID)) {
				continue
			}
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
			v4Row(c, v4RowOptions{
				Key:         "agents-toggle-unavailable",
				Title:       label,
				Clickable:   true,
				HideChevron: true,
				Control: func() {
					ico := v4IconChev
					if s.ShowUnavailable {
						ico = v4IconDown
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
			for _, row := range s.Connections.Connections {
				if row.ID == o.Profile {
					name = connectionName(row.Kind)
					break
				}
			}
			v4Row(c, v4RowOptions{
				Key:   "pending-connection-" + o.Operation,
				Title: name + " 的连接结果尚未确认",
				Leading: func() {
					agentLogo(c, agentBrandIconFor("", name), 16)
				},
				Control: func() {
					v4SmallButton(c, "检查连接", nil, func() { a.reviewConnectionOperation(o) }).Label("检查连接：" + name + " " + o.Operation).Disabled(s.Busy)
				},
			})
		}
		if s.Message != "" {
			ui.Row(c).FillWidth().Padding(sp2, sp1).Children(func() { homeText(c, s.Message, homeMeta) })
		}
		if s.Error != "" {
			ui.Row(c).FillWidth().Padding(sp2, sp1).Children(func() { homeText(c, s.Error, homeMeta).TextColor(tok.Danger) })
		}
	})
}

func (a *App) agentConnectionRow(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	name := connectionName(row.Kind)
	if s.EnvironmentOpen == nil {
		s.EnvironmentOpen = map[string]bool{}
	}
	pending := s.pendingConnection(row.ID)
	allowed := pending == nil && (connectionActionAllowed(row, "prepare") || connectionActionAllowed(row, "activate"))
	version := row.HostVersion
	if version == "" {
		version = s.HostVersions[row.Kind]
	}
	if version == "" {
		version = "—"
	}
	status := connectionStatus(row)
	color := tok.Text3
	if (captureConnection(row) && row.Capture != nil && row.Capture.State == "healthy") ||
		(!captureConnection(row) && row.Status == "configured_pending_new_session" && row.NewSessions > 0) {
		color = tok.OK
	}
	if pending != nil {
		status = "连接结果待确认"
		if pending.Kind == "connection_allow_capture" || pending.Kind == "connection_defer_capture" {
			status = "自动采集设置待确认"
		}
		if s.Busy {
			status = "正在连接…"
			if pending.Kind == "connection_allow_capture" || pending.Kind == "connection_defer_capture" {
				status = "正在设置自动采集…"
			}
		}
	}
	ui.Column(c.Key("agent-connection-" + row.ID)).FillWidth().Gap(0).Children(func() {
		v4Row(c, v4RowOptions{
			Key:   "agent-row-" + row.ID,
			Title: name,
			TitleExtra: func() {
				ui.Text(c, version).Font("ui-monospace").FontSize(fsCaption).TextColor(tok.Text3).Label(name + " 版本：" + version).SingleLine()
			},
			DescExtra: func() {
				homeText(c, status, homeMeta).TextColor(color).Tooltip(captureSavedTip(row))
			},
			Leading: func() {
				ui.Box(c).Size(24, 24).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Image(c, agentOriginalIcon(row.Kind)).Size(20, 20).Fit(ui.Contain).TextColor(tok.Text1)
				})
			},
			Control: func() {
				if row.Profile != "" || row.EnvironmentIssue != "" || len(row.Environments) > 0 {
					label := "配置目录"
					if s.EnvironmentOpen[row.ID] {
						label = "收起目录"
					}
					v4SmallButton(c, label, nil, func() { s.EnvironmentOpen[row.ID] = !s.EnvironmentOpen[row.ID] }).Label("查看配置目录：" + name).Disabled(s.Busy)
				}
				switch {
				case pending != nil:
					v4SmallButton(c, "核对连接", nil, func() { a.reviewConnectionOperation(*pending) }).Label("核对连接：" + name).Disabled(s.Busy)
				case captureConnection(row):
					a.captureConnectionAction(c, row)
				case row.Status == "activation_unknown" || row.Status == "preparation_unknown" || row.Status == "requires_review" || row.Status == "staging_unknown" || row.Status == "agent_creation_unconfirmed":
					v4SmallButton(c, "核对连接", nil, func() { a.connectAgents("review", []string{row.ID}) }).Label("核对连接：" + name).Disabled(s.Busy)
				case allowed:
					action, label := "prepare", "连接"
					if connectionActionAllowed(row, "activate") {
						action, label = "activate", "确认连接"
					}
					v4SmallPrimaryButton(c, label, func() { a.connectAgents(action, []string{row.ID}) }).Label(label + " " + name).Disabled(s.Busy || s.Loading)
				}
			},
		})
		if row.Status == "prepared" && row.Preview != nil {
			ui.Column(c).FillWidth().Padding(sp2, sp1, sp3, 34).Gap(5).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
				homeText(c, "将为 "+name+" 添加 Memlane 连接，确认后生效。", homeMeta)
				if len(row.Preview.DisabledPlugins) > 0 {
					homeText(c, fmt.Sprintf("将替换 %d 个旧 Cognee 连接。", len(row.Preview.DisabledPlugins)), homeMeta)
				}
			})
		} else if captureConnection(row) {
			a.captureConnectionDetails(c, row)
		} else if row.Status == "configured_pending_new_session" && row.NewSessions == 0 {
			tip := "请重新打开 " + name + " 并新建会话，Memlane 会自动检查连接。"
			ui.Row(c).FillWidth().Padding(sp1, sp1, sp2, 34).Children(func() { homeText(c, tip, homeMeta).LineHeight(1.5) })
		} else if pending == nil {
			if notice := connectionReviewNotice(name, row); notice != "" {
				ui.Row(c).FillWidth().Padding(sp1, sp1, sp2, 34).Children(func() { homeText(c, notice, homeMeta).LineHeight(1.5) })
			}
		}
		if row.Profile != "" || row.EnvironmentIssue != "" || len(row.Environments) > 0 {
			a.agentEnvironmentDetails(c, row)
		}
		if row.Status == "environment_receipt_unconfirmed" {
			ui.Row(c).FillWidth().Padding(sp1, sp1, sp2, 34).Children(func() { homeText(c, "已检测到会话，但还不能确认使用的是哪个配置目录。", homeMeta) })
		}
		if message := s.ItemMessages[row.ID]; message != "" {
			ui.Row(c).FillWidth().Padding(sp1, sp1, sp2, 34).Children(func() { homeText(c, message, homeMeta).LineHeight(1.5) })
		}
	})
}

func (a *App) agentEnvironmentDetails(c *ui.Context, row AgentConnection) {
	s := &a.integrations
	if row.EnvironmentIssue != "" {
		message := "请选择你日常使用的配置目录。"
		if row.EnvironmentIssue == "verification_environment" {
			message = "当前是测试目录，请换成日常使用的配置目录。"
		}
		homeText(c, message, homeMeta).Margin(0, 0, 0, 42)
	}
	if !s.EnvironmentOpen[row.ID] {
		return
	}
	// Inset inside the measured width. FillWidth plus an external left margin
	// made the expanded directory escape the shared list surface.
	ui.Column(c.Key("environment-"+row.ID)).Label("Agent目录详情："+connectionName(row.Kind)).FillWidth().Padding(0, 0, 12, 46).Gap(6).Children(func() {
		if row.Profile != "" {
			homeText(c, "配置目录："+row.Profile, homeMeta).FillWidth().MinWidth(0).SingleLine().Tooltip(row.Profile)
		}
		if row.EnvironmentVerified {
			homeText(c, "插件已确认 · "+row.PluginVersion, homeMeta)
		} else if row.LastReceiptAt > 0 {
			homeText(c, "检测到会话活动，尚未确认来自此目录。", homeMeta)
		}
		for _, candidate := range row.Environments {
			option := candidate
			if option.Profile == row.Profile {
				continue
			}
			label := "其他目录"
			if option.Source == "default" {
				label = "默认目录"
			}
			if option.Source == "environment" {
				label = "启动时指定的目录"
			}
			if option.Verification {
				label = "测试目录 · 不可用于日常连接"
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				homeText(c, label+"："+option.Profile, homeMeta).Grow(1).MinWidth(0)
				if option.Exists && !option.Verification {
					textButton(c, "使用此目录", func() { a.selectAgentEnvironment(row, option.Profile) }).Label("使用目录：" + option.Profile).Disabled(s.Busy || s.Loading)
				}
			})
		}
		if a.ChooseProjectFolder != nil {
			textButton(c, "更换目录", func() {
				path, err := a.ChooseProjectFolder()
				if err == nil && path != "" {
					a.selectAgentEnvironment(row, path)
				}
			}).Disabled(s.Busy || s.Loading).AlignSelf(ui.Start)
		}
	})
}

func generationLabel(g GenerationDirectory, ref *GenerationRef) string {
	row, ok := g.row(ref)
	if !ok {
		return "尚未确认"
	}
	if row.Profile.Mode == "agent_bridge" {
		return "已连接的 Agent · " + generationBridgeLabel(row.Profile.BridgeKind) + " · " + generationDefaultModel(g, row.Profile.BridgeKind)
	}
	provider := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容", "ollama": "Ollama"}[row.Profile.Provider]
	return "模型 API · " + provider + " · " + row.Profile.Model
}
func (a *App) generationSetupPage(c *ui.Context) {
	ui.Column(c).Fill().Padding(28).Gap(20).Background(tok.Bg).Children(func() {
		homeText(c, "配置记忆提炼", homeHeading).FontSize(22)
		homeText(c, "选择提炼记忆的 Agent 或模型。", homeMeta).LineHeight(1.7)
		ui.Scroll(c.Key("generation-first-setup")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(900).Gap(20).Children(func() { a.generationSettings(c) })
		})
	})
}

func (a *App) generationSettings(c *ui.Context) {
	a.generationConfiguration(c, false)
}

func (a *App) generationConfiguration(c *ui.Context, showToolGraph bool) {
	a.generationConfigurationFor(c, true, true)
	if showToolGraph {
		a.globalToolGraphSettings(c)
	}
}

func (a *App) generationConfigurationFor(c *ui.Context, edit, enable bool) {
	s := &a.integrations
	if !s.Loaded {
		homeText(c, "记忆提炼配置尚未载入", homeMeta)
		commonButton(c, "读取记忆提炼配置", false, a.RefreshIntegrations)
		return
	}
	a.generationPendingOperations(c)
	a.generationWorkspaceFor(c, edit, enable)
	if edit {
		a.generationDeleteModal(c)
	}
}

func (a *App) generationPendingOperations(c *ui.Context) {
	s := &a.integrations
	if s.hasPendingGeneration() {
		settingsCard(c, "待核对操作", "重开应用后保留原操作；核对只读取回执，不重新提交请求。", func() {
			for _, pending := range s.PendingOperations {
				if pending.connection() {
					continue
				}
				o := pending
				label := map[string]string{"save": "保存配置", "next": "下次启动设置", "handoffs": "更换提炼模型", "validations": "模型验证", "delete": "删除配置"}[o.Kind]
				ui.Row(c.Key("pending-generation-" + o.Operation)).Gap(12).Children(func() {
					homeText(c, label+" · "+o.Profile, homeMeta).Grow(1)
					textButton(c, "核对原操作", nil).Label("核对原操作：" + o.Operation).Disabled(s.Busy).OnClick(func() { a.reviewPendingGeneration(o) })
				})
			}
		})
	}
}

func (a *App) generationEditorPanel(c *ui.Context) {
	s := &a.integrations
	if !s.Editor {
		if s.APIKey != "" {
			s.APIKey = ""
			s.KeyEpoch++
		}
		return
	}
	settingsListSurface(c.Key("generation-editor-"+s.ProfileID), func() { a.generationEditorContent(c) })
}

func (a *App) generationEditorContent(c *ui.Context) {
	s := &a.integrations
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		if !s.GenerationProviderDialog {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				title := "Provider 配置"
				if s.DraftMode == "agent_bridge" {
					title = generationBridgeLabel(s.BridgeKind) + " 配置"
				}
				homeText(c, title, homeHeading).FontSize(15).FontWeight(500)
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
						homeText(c, generationDefaultModel(s.Generation, s.BridgeKind), homeBody)
						homeText(c, "模型与思考设置跟随 Agent。", homeMeta)
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
						homeText(c, "已保存密钥；不会回填显示", homeMeta)
					}
				}
				if s.draftProfile().Provider == "ollama" {
					homeText(c, "填写Ollama 地址，例如 http://127.0.0.1:11434，无需 API Key；模型需已在服务中可用。", homeMeta)
				}
			})
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				validationLabel := "验证（会消耗额度）"
				if s.draftProfile().Provider == "ollama" {
					validationLabel = "验证（调用模型）"
				}
				_, defaultAvailable := s.Generation.AgentBridges[s.BridgeKind]
				v4PrimaryButton(c, "保存配置", a.saveGeneration).Disabled(locked || s.pendingGenerationProfile(s.ProfileID) || (s.DraftMode == "agent_bridge" && !defaultAvailable))
				if s.SaveOperation != "" {
					v4SmallButton(c, "核对保存结果", v4IconRefresh, a.reviewGenerationSave).Disabled(s.Busy)
				}
				v4OutlineButton(c, validationLabel, nil, func() { a.generationAction("validations") }).Disabled(s.Busy || !s.cleanSaved() || s.Validation.Status == "running" || s.Validation.Status == "queued" || s.pendingGenerationProfile(s.ProfileID))
				if s.NextOperation != "" {
					v4SmallButton(c, "核对待应用设置", v4IconRefresh, a.reviewGenerationNext).Disabled(s.Busy)
				}
				v4GhostButton(c, "取消修改", nil, a.discardGenerationDraft).Disabled(s.Busy || s.SaveOperation != "")
			})
			if !s.cleanSaved() && s.SaveOperation == "" {
				homeText(c, "配置有未保存修改。", homeMeta)
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
				ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
					v4SmallButton(c, "核对原验证", v4IconRefresh, func() { a.pollGenerationValidation(s.ValidationOperation) }).Disabled(s.Busy)
					if s.Validation.Status == "queued" || s.Validation.Status == "running" {
						v4DangerOutlineButton(c, "停止验证", true, a.cancelGenerationValidation).Disabled(s.Busy)
					}
				})
			}
			if s.Message != "" {
				homeText(c, s.Message, homeMeta).LineHeight(1.7)
			}
			if s.Error != "" {
				homeText(c, s.Error, homeMeta).LineHeight(1.7)
			}
		})
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
