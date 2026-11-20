package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// textButton is the shared, lightweight secondary link action. ButtonBase retains
// native focus, keyboard activation, disabled state and accessibility behavior.
func textButton(c *ui.Context, label string, fn func()) ui.Element {
	title := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), "›"))
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).Height(24).MinHeight(24).
		Padding(0, 4).Gap(4).Radius(radiusSmall).FontSize(fsFootnote).TextColor(tok.Accent).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	b.Background(homeHoverColor(b, "link-bg", ui.Transparent, tok.AccentSofter))
	b.Children(func() {
		ui.Text(c, title).SingleLine()
		ui.Icon(c, homeArrow).FontSize(11).Shrink(0)
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}
