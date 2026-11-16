package simpledesktop

import "github.com/egoist/mygo/ui"

// Language switching is intentionally closed; the backend default is zh-CN.
func languageSettingsCard(c *ui.Context) {
	settingsCard(c, "语言", "新生成的记忆使用默认语言，代码和文件名称保持原样。", func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
			homeText(c, "默认语言", homeHeading)
			ui.Spacer(c)
			homeText(c, "简体中文", homeHeading)
		})
		homeText(c, "暂不支持切换语言", homeMeta)
	})
}
