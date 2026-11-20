package simpledesktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func v4IconSVG(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

var (
	v4IconOverview = v4IconSVG(v4PathOverview)
	v4IconCommon   = v4IconSVG(v4PathCommon)
	v4IconProfile  = v4IconSVG(v4PathProfile)
	v4IconFolder   = v4IconSVG(v4PathFolder)
	v4IconSettings = v4IconSVG(`<circle cx="10" cy="10" r="2.5"/><path d="M10 3.25a1.2 1.2 0 0 1 1.15.88l.23.82a5.2 5.2 0 0 1 1.25.52l.74-.41a1.2 1.2 0 0 1 1.43.2l.82.82a1.2 1.2 0 0 1 .2 1.43l-.41.74c.23.4.4.82.52 1.25l.82.23a1.2 1.2 0 0 1 .88 1.15v1.16a1.2 1.2 0 0 1-.88 1.15l-.82.23a5.2 5.2 0 0 1-.52 1.25l.41.74a1.2 1.2 0 0 1-.2 1.43l-.82.82a1.2 1.2 0 0 1-1.43.2l-.74-.41a5.2 5.2 0 0 1-1.25.52l-.23.82a1.2 1.2 0 0 1-1.15.88H9.42a1.2 1.2 0 0 1-1.15-.88l-.23-.82a5.2 5.2 0 0 1-1.25-.52l-.74.41a1.2 1.2 0 0 1-1.43-.2l-.82-.82a1.2 1.2 0 0 1-.2-1.43l.41-.74a5.2 5.2 0 0 1-.52-1.25l-.82-.23a1.2 1.2 0 0 1-.88-1.15V9.42a1.2 1.2 0 0 1 .88-1.15l.82-.23c.12-.43.29-.85.52-1.25l-.41-.74a1.2 1.2 0 0 1 .2-1.43l.82-.82a1.2 1.2 0 0 1 1.43-.2l.74.41c.4-.23.82-.4 1.25-.52l.23-.82a1.2 1.2 0 0 1 1.15-.88z" transform="translate(-0.2 -0.2) scale(0.96)"/>`)
	v4IconSearch   = v4IconSVG(`<circle cx="9" cy="9" r="5.25"/><path d="M13 13l3.5 3.5"/>`)
	v4IconChev     = v4IconSVG(`<path d="M8 5l5 5-5 5"/>`)
	v4IconDown     = v4IconSVG(`<path d="M5 8l5 5 5-5"/>`)
	v4IconDoc      = v4IconSVG(`<path d="M5.5 2.75h6l3.5 3.5v11h-9.5z M11.5 2.75v3.5H15 M8 10h5M8 13h4"/>`)
	v4IconCopy     = v4IconSVG(`<rect x="7" y="7" width="9.5" height="9.5" rx="1.5"/><path d="M13 7V4.75a1.5 1.5 0 0 0-1.5-1.5H4.75a1.5 1.5 0 0 0-1.5 1.5v6.75a1.5 1.5 0 0 0 1.5 1.5H7"/>`)
	v4IconClose    = v4IconSVG(`<path d="M5 5l10 10M15 5L5 15"/>`)
	v4IconRecall   = v4IconSVG(`<path d="M4 10a6 6 0 1 0 1.8-4.3M4 3.5v3h3"/>`)
	v4IconInfo     = v4IconSVG(`<circle cx="10" cy="10" r="7"/><path d="M10 9v5M10 6.5v.01"/>`)
)

// v4CategoryChip draws a neutral category tag (V4 never color-codes categories).
func v4CategoryChip(c *ui.Context, label string) ui.Element {
	return ui.Text(c, label).
		Font("system-ui").FontSize(fsCaption).FontWeight(400).
		SingleLine().Shrink(0).Tooltip("记忆点分类："+label).
		Padding(2, 6).Radius(radiusSmall).
		Background(tok.Fill2).TextColor(tok.Text2)
}

// v4RecallChip draws the V4 recall counter: accent-soft pill with icon when > 0,
// subtle muted text when 0 or unavailable.
func v4RecallChip(c *ui.Context, i Item) {
	label, tip := knowledgeCardRecall(i)
	if i.RecallStats != nil && i.RecallStats.Count > 0 {
		ui.Row(c).AlignItems(ui.Center).Gap(3).Padding(2, 6).Radius(radiusSmall).Background(tok.AccentSoft).Tooltip(tip).Shrink(0).Children(func() {
			ui.Icon(c, v4IconRecall).FontSize(10).TextColor(tok.Accent).Shrink(0)
			ui.Text(c, label).Font("system-ui").FontSize(fsCaption).FontWeight(500).FontFeatures("tnum").TextColor(tok.Accent).SingleLine()
		})
		return
	}
	ui.Text(c, label).Font("system-ui").FontSize(fsCaption).FontWeight(400).FontFeatures("tnum").TextColor(tok.Text4).SingleLine().Tooltip(tip).Shrink(0)
}

// v4UnreadDot draws the 6px accent indicator dot for unread knowledge.
func v4UnreadDot(c *ui.Context) ui.Element {
	return ui.Box(c).Label("未读标记").Size(6, 6).Radius(3).Background(tok.Accent).Shrink(0).Tooltip("未读")
}

// v4Switch renders a compact V4 toggle switch (26x16) with a bouncy spring knob.
func v4Switch(c *ui.Context, key string, on *bool, label string, onChange func()) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Role(ui.RoleSwitch).Label(label).
		Cursor(ui.CursorPointer).Height(26).Padding(0, 2).Gap(6).
		AlignItems(ui.Center).Background(ui.Transparent).BorderWidth(0).Shrink(0)
	target := float32(0)
	if *on {
		target = 10
	}
	knobX := btn.Spring("knob-x", target, v4Bouncy.Spring)
	trackTarget := tok.BorderStrong
	if *on {
		trackTarget = tok.Accent
	}
	trackColor := btn.AnimateColor("track-bg", trackTarget, v4Fast)
	textColor := tok.Text2
	if *on {
		textColor = tok.Text1
	}
	btn.Children(func() {
		ui.Box(c).Size(26, 16).Radius(8).Background(trackColor).Shrink(0).DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + 2 + knobX, Y: r.Y + 2, W: 12, H: 12}, tok.OnAccent, 6)
		})
		ui.Text(c, label).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(textColor).SingleLine()
	}).OnClick(func() {
		*on = !*on
		if onChange != nil {
			onChange()
		}
	})
	return btn
}

// v4DropdownButton renders a V4 filter/sort trigger button (height 26, radius 6).
func v4DropdownButton(c *ui.Context, key, label, prefix, value string, active bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(26).Padding(0, sp2).Gap(5).
		Radius(radius).AlignItems(ui.Center).Shrink(0)
	borderColor := tok.BorderStrong
	bgNormal := ui.Transparent
	valColor := tok.Text1
	if active {
		borderColor = tok.Accent
		bgNormal = tok.AccentSofter
		valColor = tok.Accent
	}
	b.Border(1, borderColor).Background(homeHoverColor(b, "dd-bg", bgNormal, tok.Hover))
	v4ButtonPress(b, 0.97)
	b.Children(func() {
		if prefix != "" {
			ui.Text(c, prefix).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text3).SingleLine()
		}
		ui.Text(c, value).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(valColor).SingleLine()
		ui.Icon(c, v4IconDown).FontSize(11).TextColor(tok.Text3).Shrink(0)
	})
	return b
}

// v4OutlineButton renders the standard V4 secondary action button (height 28, radius 6).
func v4OutlineButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(28).Padding(0, sp3).Gap(6).
		Radius(radius).Border(1, tok.BorderStrong).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "btn-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(b, 0.96)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(14).TextColor(tok.Text2).Shrink(0)
		}
		ui.Text(c, label).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4PrimaryButton renders the V4 accent primary button (height 28, radius 6).
func v4PrimaryButton(c *ui.Context, label string, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(28).Padding(0, sp3).Gap(6).
		Radius(radius).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "pbtn-bg", tok.Accent, tok.AccentHover))
	v4ButtonPress(b, 0.96)
	b.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.OnAccent).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4UnderlineTabs renders underline tabs with a snappy spring-sliding 2px accent bar.
func v4UnderlineTabs(c *ui.Context, key, label string, selected *int, labels []string, onChange func()) ui.Element {
	parts := ui.TabsBase(c.Key(key), selected, len(labels))
	list := parts.List.Label(label).Height(36).Gap(sp5).AlignItems(ui.Stretch).Shrink(0)
	var activeTab ui.Element
	list.Children(func() {
		for idx, text := range labels {
			on := idx == *selected
			tab := parts.Tab(idx).Cursor(ui.CursorPointer).Height(36).Padding(0, 1).AlignItems(ui.Center).FocusRing(false)
			color := tok.Text2
			weight := 400
			if on {
				color = tok.Text1
				weight = 550
				activeTab = tab
			} else if tab.Hovered() {
				color = tok.Text1
			}
			tab.Children(func() {
				ui.Text(c, text).Font("system-ui").FontSize(fsBody).FontWeight(weight).TextColor(color).SingleLine()
			})
		}
	})
	listBox := list.Bounds()
	tabBox := activeTab.Bounds()
	targetX := tabBox.X - listBox.X
	targetW := tabBox.W
	if targetW <= 0 {
		targetX, targetW = 0, 28
	}
	barX := list.Spring("tab-bar-x", targetX, v4Snappy.Spring)
	barW := list.Spring("tab-bar-w", targetW, v4Snappy.Spring)
	list.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if barW > 0 {
			p.Fill(ui.Rect{X: r.X + barX, Y: r.Y + r.H - 2, W: barW, H: 2}, tok.Accent, 1)
		}
	})
	if onChange != nil {
		list.OnChange(onChange)
	}
	return list
}

// v4Segmented renders a compact segmented control with a spring-sliding thumb.
func v4Segmented(c *ui.Context, key, label string, selected *int, labels []string, onChange func()) ui.Element {
	parts := ui.SegmentedBase(c.Key(key), selected, len(labels))
	track := parts.Track.Label(label).Padding(2).Radius(radius).Background(tok.Fill2).AlignItems(ui.Center).Shrink(0)
	var activeSeg ui.Element
	track.Children(func() {
		for idx, text := range labels {
			on := idx == *selected
			seg := parts.Segment(idx).Cursor(ui.CursorPointer).Height(22).Padding(0, 10).Radius(radiusSmall).Background(ui.Transparent).BorderWidth(0)
			color := tok.Text2
			weight := 400
			if on {
				color = tok.Text1
				weight = 500
				activeSeg = seg
			}
			seg.Children(func() {
				ui.Text(c, text).Font("system-ui").FontSize(fsFootnote).FontWeight(weight).TextColor(color).SingleLine()
			})
		}
	})
	trackBox := track.Bounds()
	segBox := activeSeg.Bounds()
	targetX := segBox.X - trackBox.X
	targetW := segBox.W
	if targetW <= 0 {
		targetX, targetW = 2, 44
	}
	thumbX := track.Spring("seg-x", targetX, v4Snappy.Spring)
	thumbW := track.Spring("seg-w", targetW, v4Snappy.Spring)
	track.Draw(func(p *ui.Painter, r ui.Rect) {
		if thumbW > 0 {
			rect := ui.Rect{X: r.X + thumbX, Y: r.Y + 2, W: thumbW, H: r.H - 4}
			p.Fill(rect, tok.SegThumb, radiusSmall)
			if !tok.Dark {
				p.Stroke(rect, tok.Border, radiusSmall, 1)
			}
		}
	})
	if onChange != nil {
		track.OnChange(onChange)
	}
	return track
}

func v4CountBadge(c *ui.Context, label string, active bool) ui.Element {
	bg, fg := tok.Fill2, tok.Text2
	if active {
		bg, fg = tok.Accent, tok.OnAccent
	}
	return ui.Text(c, label).
		Font("system-ui").FontSize(fsCaption).FontWeight(600).FontFeatures("tnum").
		MinWidth(20).Height(18).Padding(0, 6).Radius(9).
		Background(bg).TextColor(fg).TextAlign(ui.Center).SingleLine()
}

// formatBadgeCount formats unread counts for sidebar badges.
func formatBadgeCount(count int) string {
	if count > 99 {
		return "99+"
	}
	return fmt.Sprint(count)
}

// stripInlineBackticks removes markdown backtick wrappers from single/multi-line
// list previews while leaving raw bodies, labels and clipboard copy untouched.
func stripInlineBackticks(s string) string {
	return strings.ReplaceAll(s, "`", "")
}
