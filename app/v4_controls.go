package app

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type v4MenuItem = kitui.MenuItem
type v4SelectOption = kitui.SelectOption

// v4TriAccessOptions defines the three-tier project access choices.
var v4TriAccessOptions = []v4SelectOption{
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
func v4TriAccessSelect(c *ui.Context, key, agentName string, state v4TriAccessState, onSelect func(nextValue string)) ui.Element {
	return ui.Column(c.Key(key + "-wrap")).AlignItems(ui.End).Gap(4).Shrink(0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(sp2).Shrink(0).Children(func() {
			if state.Saving {
				ui.Row(c).AlignItems(ui.Center).Gap(4).Shrink(0).Children(func() {
					kitui.Spinner(c, &tok.Tokens)
					ui.Text(c, "保存中…").Font(fontUI).FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
				})
			}
			label := "访问权限"
			if agentName != "" {
				label = agentName + " 访问权限"
			}
			kitui.Select(c, &tok.Tokens, key, label, state.Value, v4TriAccessOptions, state.Disabled || state.Saving, state.Error != "", onSelect)
		})
		if state.Error != "" {
			ui.Text(c, state.Error).Font(fontUI).FontSize(fsCaption).FontWeight(400).TextColor(tok.Danger).LineHeight(1.35)
		}
	})
}

func v4SmallButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return kitui.SmallButton(c, &tok.Tokens, label, icon, fn)
}

func v4SmallPrimaryButton(c *ui.Context, label string, fn func()) ui.Element {
	return kitui.SmallPrimaryButton(c, &tok.Tokens, label, fn)
}

func v4DangerButton(c *ui.Context, label string, fn func()) ui.Element {
	return kitui.DangerButton(c, &tok.Tokens, label, fn)
}

func v4DangerOutlineButton(c *ui.Context, label string, small bool, fn func()) ui.Element {
	return kitui.DangerOutlineButton(c, &tok.Tokens, label, small, fn)
}

func v4GhostButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return kitui.GhostButton(c, &tok.Tokens, label, icon, fn)
}

func v4StatusDot(c *ui.Context, state string) ui.Element {
	return kitui.StatusDot(c, &tok.Tokens, state)
}

func v4Badge(c *ui.Context, label, tone string) ui.Element {
	return kitui.Badge(c, &tok.Tokens, label, tone)
}

func v4BuildMenu(m *ui.Menu, items []v4MenuItem) {
	kitui.BuildMenu(m, items)
}

func v4Select(c *ui.Context, key, label, value string, options []v4SelectOption, disabled, errState bool, onSelect func(value string)) ui.Element {
	return kitui.Select(c, &tok.Tokens, key, label, value, options, disabled, errState, onSelect)
}

func v4SkeletonRow(c *ui.Context, lines int, avatar, rightPill bool) ui.Element {
	return kitui.SkeletonRow(c, &tok.Tokens, lines, avatar, rightPill)
}

func v4SkeletonGroup(c *ui.Context, count, lines int, avatar, rightPill bool) ui.Element {
	return kitui.SkeletonGroup(c, &tok.Tokens, count, lines, avatar, rightPill)
}
