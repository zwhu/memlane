package reading

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// CopyBody returns the text eligible for the reader's copy action.
func CopyBody(body string, loaded, unavailable bool) (string, bool) {
	if !loaded || unavailable {
		return "", false
	}
	return body, true
}

func CatalogUpdateBanner(c *ui.Context, tokens *kitui.Tokens, renderRefresh func()) {
	ui.Row(c.Key("knowledge-new-catalog-banner")).FillWidth().Padding(kitui.Sp1, kitui.Sp4).
		Gap(kitui.Sp2).AlignItems(ui.Center).Background(tokens.AccentSofter).Shrink(0).Children(func() {
		ui.Text(c, "有新记忆可用").Font(kitui.FontUI).FontSize(kitui.FSCaption).FontWeight(500).TextColor(tokens.Text1).SingleLine()
		ui.Spacer(c)
		renderRefresh()
	})
}
