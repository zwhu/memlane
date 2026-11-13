package simpledesktop

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"
)

func (a *App) agentConnectionSettings(c *ui.Context) {
	s := &a.integrations
	if !s.Loaded {
		homeText(c, "正在读取本机 Agent 接入配置", homeMeta)
		if s.Error != "" {
			homeText(c, s.Error, homeMeta)
			commonButton(c, "重新读取接入配置", false, a.RefreshIntegrations)
		}
		return
	}
	for _, item := range s.Connections.Connections {
		row := item
		ui.Column(c.Key("agent-connection-"+row.ID)).FillWidth().Gap(8).Padding(14).Border(1, homeBorder).Radius(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
				selected := s.Selected[row.ID]
				configured := row.Status == "configured_pending_new_session"
				missing := row.Kind != "codex" && !row.Detected
				ui.Checkbox(c.Key("select-agent-"+row.ID), &selected, connectionName(row.Kind)).Disabled(s.Busy || configured || missing).OnChange(func() { s.Selected[row.ID] = selected })
				ui.Spacer(c)
				homeText(c, connectionStatus(row), homeMeta)
			})
			if row.Status == "prepared" && row.Preview != nil {
				homeText(c, "已准备独立身份与安装产物。启用后需在 Agent 中完成信任、重载并新建会话。", homeMeta).LineHeight(1.6)
				if len(row.Preview.DisabledPlugins) > 0 {
					homeText(c, "将停用已识别的旧 Cognee 接入；其他插件保留。", homeMeta)
				}
			}
			if row.Kind == "cursor" {
				homeText(c, "召回在会话开始或本轮首个非 Read 工具后注入；无工具回合暂不支持当轮注入。", homeMeta).LineHeight(1.6)
			}
			if message := s.ItemMessages[row.ID]; message != "" {
				homeText(c, message, homeMeta).LineHeight(1.6)
			}
			if row.Status == "activation_unknown" || row.Status == "preparation_unknown" || row.Status == "requires_review" || row.Status == "staging_unknown" {
				ui.Button(c, "核对 "+connectionName(row.Kind)+" 原操作").Disabled(s.Busy).OnClick(func() { a.connectAgents("review", []string{row.ID}) })
			}
		})
	}
	selected := func(action string) []string {
		var ids []string
		for _, row := range s.Connections.Connections {
			if !s.Selected[row.ID] || row.Status == "configured_pending_new_session" {
				continue
			}
			if action == "activate" && row.Status != "prepared" {
				continue
			}
			if action == "prepare" && (row.Status == "prepared" || row.Status == "activation_unknown" || row.Status == "preparation_unknown") {
				continue
			}
			ids = append(ids, row.ID)
		}
		return ids
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Button(c, "准备所选 Agent").Disabled(s.Busy || len(selected("prepare")) == 0).OnClick(func() { a.connectAgents("prepare", selected("prepare")) })
		ui.Button(c, "启用已准备项").Disabled(s.Busy || len(selected("activate")) == 0).OnClick(func() { a.connectAgents("activate", selected("activate")) })
		if s.BatchStop != nil {
			ui.Button(c, "取消剩余项").OnClick(func() { s.BatchStop.Store(true); s.Message = "当前项完成后停止，保留已成功项" })
		}
	})
	if s.Message != "" {
		homeText(c, s.Message, homeMeta)
	}
	if s.Error != "" {
		homeText(c, s.Error, homeMeta)
	}
}

func generationLabel(g GenerationDirectory, ref *GenerationRef) string {
	row, ok := g.row(ref)
	if !ok {
		return "尚未确认"
	}
	if row.Profile.Mode == "agent_bridge" {
		return "Codex 订阅 · " + row.Profile.Model
	}
	provider := map[string]string{"openai": "OpenAI", "anthropic": "Anthropic", "gemini": "Gemini", "custom": "OpenAI 兼容"}[row.Profile.Provider]
	return provider + " · " + row.Profile.Model
}
func (a *App) generationSettings(c *ui.Context) {
	s := &a.integrations
	settingsCard(c, "当前知识处理", "Agent 接入和知识处理模型独立配置。切换 Provider 不改变项目权限。", func() {
		homeText(c, generationLabel(s.Generation, s.Generation.Active), homeHeading).FontSize(14)
		if s.Generation.Next != nil {
			homeText(c, "下次启动使用："+generationLabel(s.Generation, s.Generation.Next), homeMeta).LineHeight(1.7)
		}
		if s.Generation.NextError != "" || s.Generation.StartupError != "" {
			homeText(c, "上次候选启动未完成，当前配置与待应用项已保留。", homeMeta)
		}
	})
	if !s.Loaded {
		homeText(c, "知识处理配置尚未载入", homeMeta)
		commonButton(c, "读取知识处理配置", false, a.RefreshIntegrations)
		return
	}
	if len(s.PendingOperations) > 0 {
		settingsCard(c, "待核对操作", "重开应用后保留原操作；核对只读取回执，不重新提交请求。", func() {
			for _, pending := range s.PendingOperations {
				o := pending
				label := map[string]string{"save": "保存配置", "next": "下次启动设置", "validations": "模型验证"}[o.Kind]
				ui.Row(c.Key("pending-generation-" + o.Operation)).Gap(12).Children(func() {
					homeText(c, label+" · "+o.Profile, homeMeta).Grow(1)
					ui.Button(c, "核对原操作").Label("核对原操作：" + o.Operation).Disabled(s.Busy).OnClick(func() { a.reviewPendingGeneration(o) })
				})
			}
		})
	}
	settingsCard(c, "已保存配置", "保存、验证与下次启动使用分别操作。验证会调用所选供应商并消耗额度。", func() {
		ids := make([]string, 0, len(s.Generation.Profiles))
		for id := range s.Generation.Profiles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			rows := s.Generation.Profiles[id]
			if len(rows) == 0 {
				continue
			}
			row := rows[len(rows)-1]
			identifier := id
			ui.Row(c.Key("generation-profile-" + id)).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				homeText(c, generationLabel(s.Generation, &GenerationRef{ID: id, Revision: row.Revision}), homeBody).Grow(1).SingleLine()
				ui.Button(c, "配置").Label("配置模型：" + id).Disabled(s.Busy || s.SaveOperation != "").OnClick(func() { s.editProfile(identifier, row); s.Error = ""; s.Message = "" })
			})
		}
		ui.Button(c, "添加 Provider").Disabled(s.Busy || s.SaveOperation != "").OnClick(func() {
			s.editProfile("api-"+integrationOperation(), GenerationRow{Profile: GenerationProfile{Mode: "provider", Provider: "openai"}})
			s.Saved = nil
			s.SavedSignature = ""
			s.Error = ""
			s.Message = ""
		})
	})
	if !s.Editor {
		return
	}
	settingsCard(c, "配置知识处理模型", "填写供应商提供的实际模型 ID 和 API 地址。更换生成模型不更换向量模型。", func() {
		locked := s.Busy || s.SaveOperation != ""
		fieldKey := func(name string) *ui.Context {
			return c.Key(fmt.Sprintf("generation-%s-%s-%d", name, s.ProfileID, s.KeyEpoch))
		}
		ui.Form(c, func() {
			if s.DraftMode == "provider" {
				ui.Field(c, "供应商协议", func() {
					ui.Select(fieldKey("provider"), &s.ProviderLabel, []string{"OpenAI", "Anthropic", "Gemini", "OpenAI 兼容"}).FillWidth().Disabled(locked)
				})
				ui.Field(c, "API 地址", func() {
					ui.TextInput(fieldKey("endpoint"), &s.Endpoint).Label("Provider API 地址").Placeholder("https://…").FillWidth().Disabled(locked)
				})
			}
			ui.Field(c, "模型 ID", func() {
				ui.TextInput(fieldKey("model"), &s.Model).Label("处理模型 ID").Placeholder("实际模型 ID").FillWidth().Disabled(locked)
			})
			if s.DraftMode == "provider" {
				ui.Field(c, "API Key", func() {
					ui.TextInput(c.Key(fmt.Sprintf("generation-key-%d", s.KeyEpoch)), &s.APIKey).Label("Provider API Key").Placeholder("已保存时留空保留").Password().FillWidth().Disabled(locked)
				})
				if s.CredentialConfigured {
					homeText(c, "已保存密钥；不会回填显示", homeMeta)
				}
			}
		})
		ui.Row(c).Gap(10).Children(func() {
			ui.Button(c, "保存配置").Disabled(locked || s.pendingGenerationProfile(s.ProfileID)).OnClick(a.saveGeneration)
			if s.SaveOperation != "" {
				ui.Button(c, "核对保存结果").Disabled(s.Busy).OnClick(a.reviewGenerationSave)
			}
			ui.Button(c, "验证（会消耗额度）").Disabled(s.Busy || !s.cleanSaved() || s.Validation.Status == "running" || s.Validation.Status == "queued" || s.pendingGenerationProfile(s.ProfileID)).OnClick(func() { a.generationAction("validations") })
			ui.Button(c, "下次启动使用").Disabled(s.Busy || s.NextOperation != "" || s.pendingGenerationProfile(s.ProfileID) || !s.cleanSaved() || !s.Generation.validated(s.Saved)).OnClick(func() { a.generationAction("next") })
			if s.NextOperation != "" {
				ui.Button(c, "核对待应用设置").Disabled(s.Busy).OnClick(a.reviewGenerationNext)
			}
		})
		if !s.cleanSaved() && s.SaveOperation == "" {
			homeText(c, "配置有未保存修改，保存后才能验证与设为下次启动使用。", homeMeta)
		}
		if s.ValidationOperation != "" {
			label := map[string]string{"queued": "验证已登记", "running": "正在验证", "passed": "验证通过", "failed": "验证未通过", "result_unknown": "验证结果未确认"}[s.Validation.Status]
			homeText(c, label, homeMeta)
			ui.Button(c, "核对原验证").Disabled(s.Busy).OnClick(func() { a.pollGenerationValidation(s.ValidationOperation) })
			if s.Validation.Status == "queued" || s.Validation.Status == "running" {
				ui.Button(c, "停止本机验证").Disabled(s.Busy).OnClick(a.cancelGenerationValidation)
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
