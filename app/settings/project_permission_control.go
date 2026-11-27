package settings

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

// v4TriAccessOptions defines the three-tier project access choices.
var v4TriAccessOptions = []kitui.SelectOption{
	{Value: "none", Label: "不可访问"},
	{Value: "read", Label: "只读"},
	{Value: "maintain", Label: "读写"},
}

// v4TriAccessState holds the presentation state for a three-tier access dropdown.
type v4TriAccessState struct {
	Value    string
	Saving   bool
	Error    string
	Disabled bool
}

// v4TriAccessSelect renders a Q5 three-tier permission dropdown with an inline
// saving spinner and danger rollback error message.
func v4TriAccessSelect(tok *kitui.Tokens, c *ui.Context, key, agentName string, state v4TriAccessState, onSelect func(nextValue string)) ui.Element {
	return ui.Column(c.Key(key + "-wrap")).AlignItems(ui.End).Gap(4).Shrink(0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(sp2).Shrink(0).Children(func() {
			if state.Saving {
				ui.Row(c).AlignItems(ui.Center).Gap(4).Shrink(0).Children(func() {
					kitui.Spinner(c, tok)
					ui.Text(c, "保存中…").Font(kitui.FontUI).FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
				})
			}
			label := "访问权限"
			if agentName != "" {
				label = agentName + " 访问权限"
			}
			kitui.Select(c, tok, key, label, state.Value, v4TriAccessOptions, state.Disabled || state.Saving, state.Error != "", onSelect)
		})
		if state.Error != "" {
			ui.Text(c, state.Error).Font(kitui.FontUI).FontSize(fsCaption).FontWeight(400).TextColor(tok.Danger).LineHeight(1.35)
		}
	})
}
