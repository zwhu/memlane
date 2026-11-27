package ui

import (
	"fmt"
	"strings"

	mygoui "github.com/egoist/mygo/ui"
)

const (
	PathOverview = `<rect x="3" y="3" width="6" height="6" rx="1.5"/><rect x="11" y="3" width="6" height="6" rx="1.5"/><rect x="3" y="11" width="6" height="6" rx="1.5"/><rect x="11" y="11" width="6" height="6" rx="1.5"/>`
	PathCommon   = `<path d="M5 3.5h10a1 1 0 0 1 1 1v12l-6-3.2-6 3.2v-12a1 1 0 0 1 1-1z"/>`
	PathProfile  = `<circle cx="10" cy="7" r="3.25"/><path d="M4 16.5c.8-2.9 3.2-4.5 6-4.5s5.2 1.6 6 4.5z"/>`
	PathFolder   = `<path d="M2.75 5.5a1.5 1.5 0 0 1 1.5-1.5h3.4l1.6 1.75h6.5a1.5 1.5 0 0 1 1.5 1.5v7.25a1.5 1.5 0 0 1-1.5 1.5H4.25a1.5 1.5 0 0 1-1.5-1.5z"/>`
)

func SolidSVG(color, paths string) *mygoui.SVG {
	return mygoui.MustParseSVG([]byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="%s" stroke="%s" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><g transform="translate(2 2)">%s</g></svg>`,
		color, color, paths,
	)))
}

func HomeSVG(paths string) *mygoui.SVG {
	return mygoui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

func MonoIcon(paths string) *mygoui.SVG {
	return HomeSVG(paths)
}

func IconSVG(paths string) *mygoui.SVG {
	return mygoui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><g transform="translate(2 2)">` + paths + `</g></svg>`))
}

// IconFrame draws an SVG icon centered inside a fixed transparent square frame
// (defaults to IconBox = 24x24 with IconGlyph = 18px glyph) so rows of icons
// share one visual center line regardless of path aspect ratio.
func IconFrame(c *mygoui.Context, icon *mygoui.SVG, glyphSize float32, color mygoui.Color) mygoui.Element {
	if glyphSize <= 0 {
		glyphSize = IconGlyph
	}
	return mygoui.Box(c).Size(IconBox, IconBox).Shrink(0).
		AlignItems(mygoui.Center).Justify(mygoui.Center).
		Background(mygoui.Transparent).
		Children(func() {
			mygoui.Icon(c, icon).FontSize(glyphSize).TextColor(color).Shrink(0)
		})
}

var (
	IconOverview = IconSVG(PathOverview)
	IconCommon   = IconSVG(PathCommon)
	IconProfile  = IconSVG(PathProfile)
	IconFolder   = IconSVG(PathFolder)
	IconSettings = IconSVG(`<circle cx="10" cy="10" r="2.5"/><path d="M10 3.25a1.2 1.2 0 0 1 1.15.88l.23.82a5.2 5.2 0 0 1 1.25.52l.74-.41a1.2 1.2 0 0 1 1.43.2l.82.82a1.2 1.2 0 0 1 .2 1.43l-.41.74c.23.4.4.82.52 1.25l.82.23a1.2 1.2 0 0 1 .88 1.15v1.16a1.2 1.2 0 0 1-.88 1.15l-.82.23a5.2 5.2 0 0 1-.52 1.25l.41.74a1.2 1.2 0 0 1-.2 1.43l-.82.82a1.2 1.2 0 0 1-1.43.2l-.74-.41a5.2 5.2 0 0 1-1.25.52l-.23.82a1.2 1.2 0 0 1-1.15.88H9.42a1.2 1.2 0 0 1-1.15-.88l-.23-.82a5.2 5.2 0 0 1-1.25-.52l-.74.41a1.2 1.2 0 0 1-1.43-.2l-.82-.82a1.2 1.2 0 0 1-.2-1.43l.41-.74a5.2 5.2 0 0 1-.52-1.25l-.82-.23a1.2 1.2 0 0 1-.88-1.15V9.42a1.2 1.2 0 0 1 .88-1.15l.82-.23c.12-.43.29-.85.52-1.25l-.41-.74a1.2 1.2 0 0 1 .2-1.43l.82-.82a1.2 1.2 0 0 1 1.43-.2l.74.41c.4-.23.82-.4 1.25-.52l.23-.82a1.2 1.2 0 0 1 1.15-.88z" transform="translate(-0.2 -0.2) scale(0.96)"/>`)
	IconSearch   = IconSVG(`<circle cx="9" cy="9" r="5.25"/><path d="M13 13l3.5 3.5"/>`)
	IconChev     = IconSVG(`<path d="M8 5l5 5-5 5"/>`)
	IconDown     = IconSVG(`<path d="M5 8l5 5 5-5"/>`)
	IconDoc      = IconSVG(`<path d="M5.5 2.75h6l3.5 3.5v11h-9.5z M11.5 2.75v3.5H15 M8 10h5M8 13h4"/>`)
	IconCopy     = IconSVG(`<rect x="7" y="7" width="9.5" height="9.5" rx="1.5"/><path d="M13 7V4.75a1.5 1.5 0 0 0-1.5-1.5H4.75a1.5 1.5 0 0 0-1.5 1.5v6.75a1.5 1.5 0 0 0 1.5 1.5H7"/>`)
	IconClose    = IconSVG(`<path d="M5 5l10 10M15 5L5 15"/>`)
	IconRecall   = IconSVG(`<path d="M4 10a6 6 0 1 0 1.8-4.3M4 3.5v3h3"/>`)
	IconInfo     = IconSVG(`<circle cx="10" cy="10" r="7"/><path d="M10 9v5M10 6.5v.01"/>`)
	IconCheck    = IconSVG(`<path d="M4.5 10.5l3.5 3.5 7.5-8"/>`)
	IconRefresh  = IconSVG(`<path d="M16 10a6 6 0 1 1-1.6-4.1M16 4v3.5h-3.5"/>`)
	IconPlus     = IconSVG(`<path d="M10 4.5v11M4.5 10h11"/>`)
)

// CategoryChip draws a neutral category tag (V4 never color-codes categories).
func CategoryChip(c *mygoui.Context, tok *Tokens, label, tooltip string) mygoui.Element {
	return mygoui.Text(c, label).
		Font(FontUI).FontSize(FSCaption).FontWeight(400).
		SingleLine().Shrink(0).Tooltip(tooltip).
		Padding(Sp1, Sp2).Radius(RadiusSmall).
		Background(tok.Fill2).TextColor(tok.Text2)
}

// AccentChip draws a calm accent-tinted pill for subagent and parent-child badges.
func AccentChip(c *mygoui.Context, tok *Tokens, label, tip string) mygoui.Element {
	el := mygoui.Text(c, label).
		Font(FontUI).FontSize(FSCaption).FontWeight(500).
		SingleLine().Shrink(0).
		Padding(Sp1, Sp2).Radius(RadiusSmall).
		Background(tok.AccentSoft).TextColor(tok.Accent)
	if tip != "" {
		el.Tooltip(tip)
	}
	return el
}

// RecallChip draws the V4 recall counter: accent-soft pill with icon when > 0,
// subtle muted text when 0 or unavailable.
func RecallChip(c *mygoui.Context, tok *Tokens, label, tip string, count int) mygoui.Element {
	if count > 0 {
		return mygoui.Row(c).AlignItems(mygoui.Center).Gap(Sp1).Padding(Sp1, Sp2).Radius(RadiusSmall).Background(tok.AccentSoft).Tooltip(tip).Shrink(0).Children(func() {
			mygoui.Icon(c, IconRecall).FontSize(12).TextColor(tok.Accent).Shrink(0)
			mygoui.Text(c, label).Font(FontUI).FontSize(FSCaption).FontWeight(500).FontFeatures("tnum").TextColor(tok.Accent).SingleLine()
		})
	}
	return mygoui.Text(c, label).Font(FontUI).FontSize(FSCaption).FontWeight(400).FontFeatures("tnum").TextColor(tok.Text4).SingleLine().Tooltip(tip).Shrink(0)
}

// UnreadDot draws the 6px accent indicator dot for unread knowledge.
func UnreadDot(c *mygoui.Context, tok *Tokens, label, tooltip string) mygoui.Element {
	return mygoui.Box(c).Label(label).Size(6, 6).Radius(3).Background(tok.Accent).Shrink(0).Tooltip(tooltip)
}

// Switch renders a compact V4 toggle switch (26x16) with a bouncy spring knob.
func Switch(c *mygoui.Context, tok *Tokens, key string, on *bool, label string, onChange func()) mygoui.Element {
	btn := mygoui.ButtonBase(c.Key(key)).Role(mygoui.RoleSwitch).Label(label).
		Cursor(mygoui.CursorPointer).Height(28).Padding(0, 0).Gap(Sp2).
		AlignItems(mygoui.Center).Background(mygoui.Transparent).BorderWidth(0).Shrink(0)
	target := float32(0)
	if *on {
		target = 10
	}
	knobX := btn.Spring("knob-x", target, Bouncy.Spring)
	trackTarget := tok.BorderStrong
	if *on {
		trackTarget = tok.Accent
	}
	trackColor := btn.AnimateColor("track-bg", trackTarget, Fast)
	textColor := tok.Text2
	if *on {
		textColor = tok.Text1
	}
	btn.Children(func() {
		mygoui.Box(c).Size(26, 16).Radius(8).Background(trackColor).Shrink(0).DrawOver(func(p *mygoui.Painter, r mygoui.Rect) {
			p.Fill(mygoui.Rect{X: r.X + 2 + knobX, Y: r.Y + 2, W: 12, H: 12}, tok.OnAccent, 6)
		})
		if label != "" {
			mygoui.Text(c, label).Font(FontUI).FontSize(FSFootnote).FontWeight(400).TextColor(textColor).SingleLine()
		}
	}).OnClick(func() {
		*on = !*on
		if onChange != nil {
			onChange()
		}
	})
	return btn
}

// DropdownButton renders a V4 filter/sort trigger button (height 28, Radius 6).
func DropdownButton(c *mygoui.Context, tok *Tokens, key, label, prefix, value string, active bool) mygoui.Element {
	b := mygoui.ButtonBase(c.Key(key)).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(28).Padding(0, Sp2).Gap(Sp1).
		Radius(Radius).AlignItems(mygoui.Center).Shrink(0)
	borderColor := tok.BorderStrong
	bgNormal := mygoui.Transparent
	valColor := tok.Text1
	if active {
		borderColor = tok.Accent
		bgNormal = tok.AccentSofter
		valColor = tok.Accent
	}
	b.Border(1, borderColor).Background(HoverColor(b, "dd-bg", bgNormal, tok.Hover))
	ButtonPress(b, 0.97)
	b.Children(func() {
		if prefix != "" {
			mygoui.Text(c, prefix).Font(FontUI).FontSize(FSFootnote).FontWeight(400).TextColor(tok.Text3).SingleLine()
		}
		mygoui.Text(c, value).Font(FontUI).FontSize(FSFootnote).FontWeight(400).TextColor(valColor).SingleLine()
		mygoui.Icon(c, IconDown).FontSize(12).TextColor(tok.Text3).Shrink(0)
	})
	return b
}

// OutlineButton renders the standard V4 secondary action button (height 28, Radius 6).
func OutlineButton(c *mygoui.Context, tok *Tokens, label string, icon *mygoui.SVG, fn func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(28).Padding(0, Sp3).Gap(Sp2).
		Radius(Radius).Border(1, tok.BorderStrong).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "btn-bg", mygoui.Transparent, tok.Hover))
	ButtonPress(b, 0.96)
	b.Children(func() {
		if icon != nil {
			mygoui.Icon(c, icon).FontSize(14).TextColor(tok.Text2).Shrink(0)
		}
		mygoui.Text(c, label).Font(FontUI).FontSize(FSFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// PrimaryButton renders the V4 accent primary button (height 28, Radius 6).
func PrimaryButton(c *mygoui.Context, tok *Tokens, label string, fn func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(28).Padding(0, Sp3).Gap(Sp2).
		Radius(Radius).BorderWidth(0).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "pbtn-bg", tok.Accent, tok.AccentHover))
	ButtonPress(b, 0.96)
	b.Children(func() {
		mygoui.Text(c, label).Font(FontUI).FontSize(FSFootnote).FontWeight(500).TextColor(tok.OnAccent).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// UnderlineTabs renders underline tabs with a paint-only AnimatePose 2px accent bar.
func UnderlineTabs(c *mygoui.Context, tok *Tokens, key, label string, selected *int, labels []string, onChange func()) mygoui.Element {
	tabH := ListHeaderH - 1
	parts := mygoui.TabsBase(c.Key(key), selected, len(labels))
	list := parts.List.Label(label).Height(tabH).Gap(Sp5).AlignItems(mygoui.Stretch).Shrink(0)
	var activeTab mygoui.Element
	var bar mygoui.Element
	list.Children(func() {
		bar = mygoui.Box(c.Key(key + "-bar")).Absolute().Bottom(0).Left(0).Height(2).Radius(1).Background(tok.Accent).PassThrough()
		for idx, text := range labels {
			on := idx == *selected
			tab := parts.Tab(idx).Cursor(mygoui.CursorPointer).Height(tabH).Padding(0, 0).AlignItems(mygoui.Center).FocusRing(false)
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
				mygoui.Text(c, text).Font(FontUI).FontSize(FSBody).FontWeight(weight).TextColor(color).SingleLine()
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
	bar.Width(targetW).AnimatePose(mygoui.Pose{X: targetX, Scale: 1}, Snappy)
	if onChange != nil {
		list.OnChange(onChange)
	}
	return list
}

// Segmented renders a compact segmented control with a paint-only AnimatePose thumb
// layered strictly behind the segment labels.
func Segmented(c *mygoui.Context, tok *Tokens, key, label string, selected *int, labels []string, onChange func()) mygoui.Element {
	host := mygoui.Row(c.Key(key + "-host")).Radius(Radius).Background(tok.Fill2).AlignItems(mygoui.Start).Shrink(0)
	var thumb mygoui.Element
	var track mygoui.Element
	var activeSeg mygoui.Element
	host.Children(func() {
		// Normal-flow 0x0 layer declared before track paints strictly behind segment text.
		mygoui.Box(c.Key(key+"-bg")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
			thumb = mygoui.Box(c.Key(key+"-thumb")).Margin(Sp1, 0, 0, 0).Height(24).Radius(RadiusSmall).Background(tok.SegThumb).PassThrough()
			if !tok.Dark {
				thumb.Border(1, tok.Border)
			}
		})
		parts := mygoui.SegmentedBase(c.Key(key), selected, len(labels))
		track = parts.Track.Label(label).Padding(Sp1).Radius(Radius).Background(mygoui.Transparent).AlignItems(mygoui.Center).Shrink(0)
		track.Children(func() {
			for idx, text := range labels {
				on := idx == *selected
				seg := parts.Segment(idx).Cursor(mygoui.CursorPointer).Height(24).Padding(0, Sp3).Radius(RadiusSmall).Background(mygoui.Transparent).BorderWidth(0)
				color := tok.Text2
				weight := 400
				if on {
					color = tok.Text1
					weight = 500
					activeSeg = seg
				}
				seg.Children(func() {
					mygoui.Text(c, text).Font(FontUI).FontSize(FSFootnote).FontWeight(weight).TextColor(color).SingleLine()
				})
			}
		})
	})
	trackBox := track.Bounds()
	segBox := activeSeg.Bounds()
	targetX := segBox.X - trackBox.X
	targetW := segBox.W
	if targetW <= 0 {
		targetX, targetW = Sp1, 44
	}
	thumb.Width(targetW).AnimatePose(mygoui.Pose{X: targetX, Scale: 1}, Snappy)
	if onChange != nil {
		track.OnChange(onChange)
	}
	return host
}

func CountBadge(c *mygoui.Context, tok *Tokens, label string, active bool) mygoui.Element {
	bg, fg := tok.Fill2, tok.Text2
	if active {
		bg, fg = tok.Accent, tok.OnAccent
	}
	return mygoui.Row(c).
		MinWidth(20).Height(20).Padding(0, Sp2).Radius(10).
		Background(bg).AlignItems(mygoui.Center).Justify(mygoui.Center).Shrink(0).
		Children(func() {
			mygoui.Text(c, label).
				Font(FontUI).FontSize(FSCaption).FontWeight(600).FontFeatures("tnum").
				TextColor(fg).SingleLine()
		})
}

// FormatBadgeCount formats unread counts for sidebar badges.
func FormatBadgeCount(count int) string {
	if count > 99 {
		return "99+"
	}
	return fmt.Sprint(count)
}

// StripInlineBackticks removes markdown backtick wrappers from single/multi-line
// list previews while leaving raw bodies, labels and clipboard copy untouched.
func StripInlineBackticks(s string) string {
	return strings.ReplaceAll(s, "`", "")
}

var (
	IconRefresh24 = HomeSVG(`<path d="M20 9a8 8 0 1 0 0 6M20 3v6h-6"/>`)
	IconCopy24    = HomeSVG(`<path d="M8 8h12v13H8zM4 16V3h12"/>`)
)
