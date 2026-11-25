package app

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func listSurface(c *ui.Context, content func()) {
	kitui.ListSurface(c, &tok.Tokens, content)
}
