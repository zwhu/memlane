package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func textButton(c *ui.Context, label string, fn func()) ui.Element {
	return kitui.TextButton(c, &tok.Tokens, label, fn)
}
