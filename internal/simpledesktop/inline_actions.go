package simpledesktop

import "github.com/egoist/mygo/ui"

// Use the SDK button for focus, disabled state, tooltips and activation.
func smallIconAction(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return ui.Button(c, "").Label(label).Tooltip(label).Cursor(ui.CursorPointer).
		Width(22).MinWidth(22).Height(22).MinHeight(22).Shrink(0).
		Padding(3).Gap(0).Radius(4).BorderWidth(0).Background(ui.Transparent).
		Children(func() { ui.Icon(c, icon).FontSize(14).TextColor(homeLinkInk) }).OnClick(fn)
}

// Keep the small hit target in the layout so revealing it never moves text.
// Keyboard focus reveals the action too, without requiring pointer input.
func hoverCopyAction(c *ui.Context, row ui.Element, label string, fn func()) ui.Element {
	b := smallIconAction(c, label, readingCopy, fn)
	if !row.Hovered() && !row.FocusWithin() {
		b.Opacity(0)
	}
	return b
}

func copyableText(c *ui.Context, display, raw, label string, role homeTextRole, fn func()) ui.Element {
	row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
	return row.Children(func() {
		homeText(c, display, role).MinWidth(0).SingleLine().Tooltip(raw)
		hoverCopyAction(c, row, label, fn)
	})
}

func refreshAction(c *ui.Context, label string, fn func()) ui.Element {
	return smallIconAction(c, label, homeRefresh, fn)
}

func refreshActionLoading(c *ui.Context, label string, loading bool, fn func()) ui.Element {
	if !loading {
		return refreshAction(c, label, fn)
	}
	return ui.Button(c, "").Label(label).Tooltip("正在"+label).Disabled(true).
		Width(22).MinWidth(22).Height(22).MinHeight(22).Shrink(0).
		Padding(3).Gap(0).Radius(4).BorderWidth(0).Background(ui.Transparent).
		Children(func() { ui.Spinner(c).Size(14, 14).TextColor(homeLinkInk) })
}
