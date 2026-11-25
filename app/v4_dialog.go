package app

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const v4DialogSmall = kitui.DialogSmall
const v4DialogLarge = kitui.DialogLarge

type v4DialogOptions = kitui.DialogOptions

func v4Dialog(c *ui.Context, open *bool, opts v4DialogOptions) ui.Element {
	return kitui.Dialog(c, &tok.Tokens, open, opts)
}
