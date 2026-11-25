package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func smallIconAction(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return kitui.SmallIconAction(c, &tok.Tokens, label, icon, fn)
}

func hoverCopyAction(c *ui.Context, row ui.Element, label string, fn func()) ui.Element {
	return kitui.HoverCopyAction(c, &tok.Tokens, row, readingCopy, label, fn)
}

func copyableText(c *ui.Context, display, raw, label string, role homeTextRole, fn func()) ui.Element {
	return kitui.CopyableText(c, &tok.Tokens, display, raw, label, role, readingCopy, fn)
}

func refreshAction(c *ui.Context, label string, fn func()) ui.Element {
	return kitui.RefreshAction(c, &tok.Tokens, label, fn)
}

func refreshActionLoading(c *ui.Context, label string, loading bool, fn func()) ui.Element {
	return kitui.RefreshActionLoading(c, &tok.Tokens, label, loading, fn)
}
