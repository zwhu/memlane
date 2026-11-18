package simpledesktop

import "github.com/egoist/mygo/ui"

// Language switching is intentionally closed; the backend default is zh-CN.
func languageSettingsCard(c *ui.Context) {
	settingsListSurface(c, func() {
		settingsListRow(c, func() {
			homeText(c, "记忆语言", homeBody).FontSize(13).FontWeight(500).TextColor(homeInk).Width(100)
			homeText(c, "简体中文", homeBody).FontSize(13).TextColor(homeInk)
		})
	})
}
