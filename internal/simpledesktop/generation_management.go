package simpledesktop

import "github.com/egoist/mygo/ui"

// Management edits saved candidates. It never chooses the running processor.
func (a *App) generationManagement(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(12).Children(func() { a.generationManagementContent(c) })
}
func (a *App) generationManagementContent(c *ui.Context) {
	s := &a.integrations
	if !s.Loaded {
		homeText(c, "记忆处理配置尚未载入", homeMeta)
		textButton(c, "读取记忆处理配置", a.RefreshIntegrations)
		return
	}
	a.generationPendingOperations(c)

	settingsSectionHeader(c, "模型配置", nil, func() {
		textButton(c, "添加配置", func() { a.newGenerationProfile("provider"); s.GenerationProviderDialog = true }).Disabled(s.Busy)
	})
	groups := generationWorkspaceGroups(s.Generation, "provider")
	if len(groups) == 0 {
		homeText(c, "尚未添加 Provider", homeMeta)
	}
	for _, group := range groups {
		settingsListSurface(c, func() {
			homeText(c, group.Title, homeMeta).Margin(8, 0, 4, 0)
			for index, item := range group.Items {
				if index > 0 {
					settingsListDivider(c)
				}
				settingsListRow(c, func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						homeText(c, item.Title, homeBody).FontSize(13).TextColor(homeInk).SingleLine().Tooltip(item.Title)
						if item.Detail != "" {
							homeText(c, item.Detail, homeMeta)
						}
					})
					textButton(c, "配置", func() { a.selectGenerationProfile(item.ID, item.Row); s.GenerationProviderDialog = true }).Label("配置模型：" + item.ID).Disabled(s.Busy)
					ref := &GenerationRef{ID: item.ID, Revision: item.Row.Revision}
					reason := s.generationDeleteBlock(item.ID)
					textButton(c, "删除", func() { s.DeleteProfile = ref; s.DeleteOpen = true; s.DeleteError = "" }).Label("删除模型：" + item.ID).Disabled(s.Busy || s.SaveOperation != "" || reason != "").Tooltip(reason)
				})
			}
		})
	}
	a.generationProviderDialog(c)
	a.generationDeleteModal(c)
}

func (a *App) generationProviderDialog(c *ui.Context) {
	s := &a.integrations
	if !s.GenerationProviderDialog || !s.Editor || s.DraftMode != "provider" {
		return
	}
	_, height := c.Size()
	modal := ui.Modal(c.Key("provider-dialog-"+s.ProfileID), &s.GenerationProviderDialog, func() {
		ui.Scroll(c.Key("provider-dialog-fields")).Width(600).MaxHeight(height - 180).Children(func() { a.generationEditorContent(c) })
	})
	if modal.Dismissed() || !s.GenerationProviderDialog {
		s.APIKey = ""
		s.KeyEpoch++
	}
}
