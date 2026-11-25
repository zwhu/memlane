package settings

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// Management edits saved candidates. It never chooses the running processor.
func (a *Controller) generationManagement(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(0).Children(func() { a.generationManagementContent(c) })
}

func (a *Controller) generationManagementContent(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if !s.generationReady() {
		kitui.Group(c, tok, "模型配置", "", nil, func() { a.generationLoadPrompt(c) })
		return
	}
	a.generationPendingOperations(c)

	groups := generationWorkspaceGroups(s.Generation, "provider")
	kitui.Group(c, tok, "模型配置", "", func() {
		kitui.SmallButton(c, tok, "添加配置", kitui.IconPlus, func() {
			a.newGenerationProfile("provider")
			s.GenerationProviderDialog = true
		}).Disabled(s.Busy)
	}, func() {
		if len(groups) == 0 {
			kitui.Row(c, tok, kitui.RowOptions{
				Title: "尚未添加 Provider",
				Desc:  "点击右上角「添加配置」接入 OpenAI 兼容或本地 Ollama 服务",
			})
		}
		for _, group := range groups {
			for _, item := range group.Items {
				id, row := item.ID, item.Row
				ref := &GenerationRef{ID: id, Revision: row.Revision}
				isActive := s.Generation.Active != nil && *s.Generation.Active == *ref
				state, tone := "未验证", ""
				if s.Generation.validated(ref) {
					state, tone = "已验证", "ok"
				}
				if isActive {
					state, tone = "当前使用", "ok"
				}
				desc := group.Title
				if item.Detail != "" {
					desc = group.Title + " · " + item.Detail
				}
				reason := s.generationDeleteBlock(id)
				kitui.Row(c, tok, kitui.RowOptions{
					Key:      "mgmt-provider-" + id,
					Title:    item.Title,
					Desc:     desc,
					DescMono: true,
					Control: func() {
						kitui.Badge(c, tok, state, tone)
						kitui.SmallButton(c, tok, "配置", nil, func() {
							a.selectGenerationProfile(id, row)
							s.GenerationProviderDialog = true
						}).Label("配置模型：" + id).Disabled(s.Busy)
						kitui.DangerOutlineButton(c, tok, "删除", true, func() {
							s.DeleteProfile = ref
							s.DeleteOpen = true
							s.DeleteError = ""
						}).Label("删除模型：" + id).Disabled(s.Busy || s.SaveOperation != "" || reason != "").Tooltip(reason)
					},
				})
			}
		}
	})
	a.generationProviderDialog(c)
	a.generationDeleteModal(c)
}

func (a *Controller) generationProviderDialog(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if !s.GenerationProviderDialog || !s.Editor || s.DraftMode != "provider" {
		return
	}
	kitui.Dialog(c, tok, &s.GenerationProviderDialog, kitui.DialogOptions{
		Key:         "provider-dialog-" + s.ProfileID,
		Title:       "Provider 配置",
		CloseLabel:  "关闭 Provider 配置",
		Width:       kitui.DialogLarge,
		ScrollBody:  true,
		HideCancel:  true,
		HideConfirm: true,
		Hint:        "保存配置不会自动启用此模型",
		OnCancel: func() {
			s.APIKey = ""
			s.KeyEpoch++
		},
		Body: func() {
			a.generationEditorContent(c)
		},
	})
	if !s.GenerationProviderDialog {
		s.APIKey = ""
		s.KeyEpoch++
	}
}
