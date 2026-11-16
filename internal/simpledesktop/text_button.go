package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// textButton is the shared, lightweight secondary action. ButtonBase retains
// native focus, keyboard activation, disabled state and accessibility behavior.
func textButton(c *ui.Context, label string, fn func()) ui.Element {
	title := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), "›"))
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).Height(24).MinHeight(24).
		Padding(0, 2).Gap(4).Radius(4).FontSize(12).TextColor(homeLinkInk).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	b.Background(ui.Transparent)
	b.Children(func() {
		ui.Text(c, title).SingleLine()
		ui.Icon(c, homeArrow).FontSize(12).Shrink(0)
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}
