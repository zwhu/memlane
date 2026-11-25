package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type v4RowOptions = kitui.RowOptions

func v4GroupWrap(c *ui.Context, content func()) ui.Element {
	return kitui.GroupWrap(c, content)
}

func v4Group(c *ui.Context, title, desc string, actions func(), rows func()) ui.Element {
	return kitui.Group(c, &tok.Tokens, title, desc, actions, rows)
}

func v4Row(c *ui.Context, opts v4RowOptions) ui.Element {
	return kitui.Row(c, &tok.Tokens, opts)
}
