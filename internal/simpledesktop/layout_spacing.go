package simpledesktop

import "github.com/egoist/mygo/ui"

// Insets belong to their container level; do not reuse overview styling for pane toolbars.
const (
	pageInset     float32 = 32
	pageTopInset  float32 = 24
	paneInset     float32 = 24
	moduleInset   float32 = 16
	sectionGap    float32 = 24
	controlGap    float32 = 16
	listItemInset float32 = 8
)

func paneHeading(c *ui.Context, title string, actions func()) ui.Element {
	return ui.Row(c).Height(32).Shrink(0).AlignItems(ui.Center).Children(func() {
		homeText(c, title, homeHeading).FontSize(14).FontWeight(600)
		ui.Spacer(c)
		actions()
	})
}
