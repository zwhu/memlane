package settings

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// Language switching is intentionally closed; the backend default is zh-CN.
func languageSettingsCard(tok *kitui.Tokens, c *ui.Context) {
	kitui.Row(c, tok, kitui.RowOptions{
		Key:   "settings-language-row",
		Title: "记忆语言",
		Desc:  "中文提炼指令在模型请求出口自动注入，不修改上游 Cognee 源码",
		Value: "简体中文",
	})
}
