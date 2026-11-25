package app

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func v4IconSVG(paths string) *ui.SVG {
	return kitui.IconSVG(paths)
}

var (
	v4IconOverview = kitui.IconOverview
	v4IconCommon   = kitui.IconCommon
	v4IconProfile  = kitui.IconProfile
	v4IconFolder   = kitui.IconFolder
	v4IconSettings = kitui.IconSettings
	v4IconSearch   = kitui.IconSearch
	v4IconChev     = kitui.IconChev
	v4IconDown     = kitui.IconDown
	v4IconDoc      = kitui.IconDoc
	v4IconCopy     = kitui.IconCopy
	v4IconClose    = kitui.IconClose
	v4IconRecall   = kitui.IconRecall
	v4IconInfo     = kitui.IconInfo
	v4IconCheck    = kitui.IconCheck
	v4IconRefresh  = kitui.IconRefresh
	v4IconPlus     = kitui.IconPlus
)

func v4CategoryChip(c *ui.Context, label string) ui.Element {
	return kitui.CategoryChip(c, &tok.Tokens, label, "记忆点分类："+label)
}

func v4SubagentChip(c *ui.Context, label, tip string) ui.Element {
	return kitui.AccentChip(c, &tok.Tokens, label, tip)
}

func v4UnreadDot(c *ui.Context) ui.Element {
	return kitui.UnreadDot(c, &tok.Tokens, "未读标记", "未读")
}

func v4Switch(c *ui.Context, key string, on *bool, label string, onChange func()) ui.Element {
	return kitui.Switch(c, &tok.Tokens, key, on, label, onChange)
}

func v4DropdownButton(c *ui.Context, key, label, prefix, value string, active bool) ui.Element {
	return kitui.DropdownButton(c, &tok.Tokens, key, label, prefix, value, active)
}

func v4OutlineButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return kitui.OutlineButton(c, &tok.Tokens, label, icon, fn)
}

func v4PrimaryButton(c *ui.Context, label string, fn func()) ui.Element {
	return kitui.PrimaryButton(c, &tok.Tokens, label, fn)
}

func v4UnderlineTabs(c *ui.Context, key, label string, selected *int, labels []string, onChange func()) ui.Element {
	return kitui.UnderlineTabs(c, &tok.Tokens, key, label, selected, labels, onChange)
}

func v4Segmented(c *ui.Context, key, label string, selected *int, labels []string, onChange func()) ui.Element {
	return kitui.Segmented(c, &tok.Tokens, key, label, selected, labels, onChange)
}

func v4CountBadge(c *ui.Context, label string, active bool) ui.Element {
	return kitui.CountBadge(c, &tok.Tokens, label, active)
}

func formatBadgeCount(count int) string {
	return kitui.FormatBadgeCount(count)
}

func stripInlineBackticks(s string) string {
	return kitui.StripInlineBackticks(s)
}

func v4RecallChip(c *ui.Context, i Item) {
	label, tip := knowledgeCardRecall(i)
	count := 0
	if i.RecallStats != nil {
		count = int(i.RecallStats.Count)
	}
	kitui.RecallChip(c, &tok.Tokens, label, tip, count)
}
