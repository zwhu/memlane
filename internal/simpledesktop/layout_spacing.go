package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const (
	pageInset     = kitui.PageInset
	pageTopInset  = kitui.PageTopInset
	paneInset     = kitui.PaneInset
	moduleInset   = kitui.ModuleInset
	sectionGap    = kitui.SectionGap
	controlGap    = kitui.ControlGap
	listItemInset = kitui.ListItemInset
)

func paneHeading(c *ui.Context, title string, actions func()) ui.Element {
	return kitui.PaneHeading(c, &tok.Tokens, title, actions)
}
