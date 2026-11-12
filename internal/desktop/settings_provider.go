package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type settingsSupplierDraft struct{ Endpoint, Protocol, Secret string }

func (a *App) selectSettingsSupplier(id string) {
	s, h := &a.processing, &a.processing.HTTP
	if h.SettingsDrafts == nil {
		h.SettingsDrafts = map[string]settingsSupplierDraft{}
	}
	if h.Editing != "" {
		h.SettingsDrafts[h.Editing] = settingsSupplierDraft{h.Endpoint, h.Protocol, h.Secret}
	}
	h.Editing = id
	h.Secret, h.Error = "", ""
	for _, template := range processingSupplierTemplates {
		if template.ID == id {
			h.Endpoint, h.Protocol = template.Endpoint, template.Protocol
		}
	}
	profile, configured := h.Profiles[id]
	// A saved processing revision is authoritative while supplier metadata is
	// loading. Never replace its custom address/protocol with a catalog default.
	if !configured {
		if saved := s.Drafts["provider"]; saved.Provider == id && saved.Endpoint != "" {
			profile, configured = saved, true
		}
	}
	if configured {
		h.Endpoint, h.Protocol = profile.Endpoint, profile.Protocol
	}
	if pending, ok := h.SettingsDrafts[id]; ok {
		h.Endpoint, h.Protocol, h.Secret = pending.Endpoint, pending.Protocol, pending.Secret
	}
	if s.Drafts["provider"].Provider != id {
		if !configured {
			profile = records.ProcessingSpec{Mode: "provider", Provider: id, Endpoint: h.Endpoint, Protocol: h.Protocol}
		}
		s.Drafts["provider"] = profile
	}
}

func (a *App) settingsProviderForm(c *ui.Context) {
	s, h := &a.processing, &a.processing.HTTP
	if h.Editing == "" && s.draft().Provider != "" {
		a.selectSettingsSupplier(s.draft().Provider)
	}
	ui.Text(c, "供应商配置").FontSize(design.Small).TextColor(design.Muted)
	labels := []string{"请选择供应商"}
	ids := map[string]string{}
	selected := labels[0]
	for _, template := range processingSupplierTemplates {
		label := template.Name
		if _, ok := h.Profiles[template.ID]; ok {
			label += " · 已保存"
		}
		labels = append(labels, label)
		ids[label] = template.ID
		if h.Editing == template.ID {
			selected = label
		}
	}
	before := selected
	ui.Select(c.Key("settings-provider-choice"), &selected, labels).Label("选择供应商配置").Disabled(h.CredentialBusy)
	if selected != before {
		if id := ids[selected]; id != "" {
			a.selectSettingsSupplier(id)
		}
	}
	ui.Text(c, "服务地址").FontSize(design.Small).TextColor(design.Muted)
	ui.TextInput(c.Key("processing-provider-endpoint"), &h.Endpoint).Label("供应商 API 地址").Height(38).Disabled(h.CredentialBusy || h.Editing == "")
	ui.Text(c, "API Key").FontSize(design.Small).TextColor(design.Muted)
	placeholder := "输入此供应商的独立密钥"
	if profile, ok := h.Profiles[h.Editing]; ok && profile.Endpoint == h.Endpoint && profile.Protocol == h.Protocol {
		placeholder = "已有独立凭据；留空保留"
	}
	ui.TextInput(c.Key("processing-provider-secret"), &h.Secret).Password().Label("供应商独立 API Key").Placeholder(placeholder).Height(38).Disabled(h.CredentialBusy || h.Editing == "")
	quietButton(c, "保存供应商配置").Disabled(!a.canSaveProcessingSupplier()).AlignSelf(ui.Start).OnClick(a.saveProcessingSupplier)
	ui.Collapsible(c, "接口设置", &a.settingsLayout.ProviderAdvanced, func() {
		ui.Text(c, "接口格式").FontSize(design.Small).TextColor(design.Muted)
		ui.Select(c.Key("processing-provider-protocol"), &h.Protocol, []string{"openai-chat", "openai-responses", "anthropic-messages"}).Label("供应商接口格式").Disabled(h.CredentialBusy || h.Editing == "")
	})
	ui.Text(c, "模型").FontSize(design.Small).TextColor(design.Muted)
	catalog := s.catalog("provider")
	choices := []string{"请选择已读取的模型"}
	choice := choices[0]
	for _, model := range append(append([]records.ProcessingModel{}, catalog.Catalog.Models...), catalog.Manual...) {
		if model.Provider == h.Editing {
			choices = append(choices, model.ID)
		}
	}
	draft := s.draft()
	if draft.Provider == h.Editing && draft.Model != "" {
		choice = draft.Model
		found := false
		for _, id := range choices {
			found = found || id == choice
		}
		if !found {
			choices = append(choices, choice)
		}
	}
	previous := choice
	ui.Select(c.Key("settings-provider-model"), &choice, choices).Label("处理模型").Disabled(h.CredentialBusy || h.Editing == "")
	if choice != previous {
		a.chooseProcessingHTTPModel("provider", h.Editing, choice)
	}
	ui.Row(c).Gap(12).Children(func() {
		quietButton(c, "读取模型").Disabled(catalog.Loading || len(h.Profiles) == 0).OnClick(func() { a.readProcessingModels("provider", false) })
		quietButton(c, "添加模型 ID").Disabled(len(h.Profiles) == 0).OnClick(func() { h.ManualSupplier = h.Editing; h.View = "manual" })
		quietButton(c, "浏览模型目录").OnClick(func() { h.View = "catalog" })
	})
	if !catalog.Loaded {
		ui.Text(c, "先配置供应商并读取模型目录").FontSize(design.Small).TextColor(design.Muted)
	}
	if catalog.Error != "" {
		ui.Text(c, catalog.Error).FontSize(design.Small).TextColor(design.Muted)
	}
	settingsNotice(c, "供应商配置可复用。修改地址、模型或凭据后需要重新验证。")
}
