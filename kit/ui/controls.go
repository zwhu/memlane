package ui

import (
	"strings"

	mygoui "github.com/egoist/mygo/ui"
)

// SmallButton renders a compact 24px V4 outlined button for inline row/header actions.
func SmallButton(c *mygoui.Context, tok *Tokens, label string, icon *mygoui.SVG, fn func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(24).Padding(0, Sp2).Gap(5).
		Radius(Radius).Border(1, tok.BorderStrong).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "sbtn-bg", mygoui.Transparent, tok.Hover))
	ButtonPress(b, 0.96)
	b.Children(func() {
		if icon != nil {
			mygoui.Icon(c, icon).FontSize(12).TextColor(tok.Text2).Shrink(0)
		}
		mygoui.Text(c, label).Font("system-ui").FontSize(FSCaption).FontWeight(500).TextColor(tok.Text1).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// SmallPrimaryButton renders a compact 24px V4 accent primary button.
func SmallPrimaryButton(c *mygoui.Context, tok *Tokens, label string, fn func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(24).Padding(0, Sp2).Gap(5).
		Radius(Radius).BorderWidth(0).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "spbtn-bg", tok.Accent, tok.AccentHover))
	ButtonPress(b, 0.96)
	b.Children(func() {
		mygoui.Text(c, label).Font("system-ui").FontSize(FSCaption).FontWeight(500).TextColor(tok.OnAccent).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// DangerButton renders a solid 28px V4 destructive primary button for confirmation dialogs.
func DangerButton(c *mygoui.Context, tok *Tokens, label string, fn func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(28).Padding(0, Sp3).Gap(6).
		Radius(Radius).BorderWidth(0).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "dbtn-bg", tok.Danger, tok.Danger.Alpha(0.88)))
	ButtonPress(b, 0.96)
	b.Children(func() {
		mygoui.Text(c, label).Font("system-ui").FontSize(FSFootnote).FontWeight(500).TextColor(tok.OnAccent).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// DangerOutlineButton renders a danger-outlined button for inline delete actions.
func DangerOutlineButton(c *mygoui.Context, tok *Tokens, label string, small bool, fn func()) mygoui.Element {
	h, padX, fs := float32(28), Sp3, FSFootnote
	if small {
		h, padX, fs = 24, Sp2, FSCaption
	}
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(h).Padding(0, padX).Gap(5).
		Radius(Radius).Border(1, tok.Danger).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "dobtn-bg", mygoui.Transparent, tok.Danger.Alpha(0.10)))
	ButtonPress(b, 0.96)
	b.Children(func() {
		mygoui.Text(c, label).Font("system-ui").FontSize(fs).FontWeight(500).TextColor(tok.Danger).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// GhostButton renders a borderless V4 action button with subtle hover tint.
func GhostButton(c *mygoui.Context, tok *Tokens, label string, icon *mygoui.SVG, fn func()) mygoui.Element {
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(28).Padding(0, Sp2).Gap(5).
		Radius(Radius).BorderWidth(0).AlignItems(mygoui.Center).Shrink(0)
	b.Background(HoverColor(b, "gbtn-bg", mygoui.Transparent, tok.Hover))
	ButtonPress(b, 0.96)
	txtCol := tok.Text2
	if b.Hovered() {
		txtCol = tok.Text1
	}
	b.Children(func() {
		if icon != nil {
			mygoui.Icon(c, icon).FontSize(14).TextColor(txtCol).Shrink(0)
		}
		mygoui.Text(c, label).Font("system-ui").FontSize(FSFootnote).FontWeight(500).TextColor(txtCol).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// StatusDot draws a 6px semantic status dot ("ok", "warn", "fail", "run", or neutral).
func StatusDot(c *mygoui.Context, tok *Tokens, state string) mygoui.Element {
	col := tok.Text4
	switch state {
	case "ok", "ready":
		col = tok.OK
	case "warn", "warning":
		col = tok.Warning
	case "fail", "error", "degraded", "exited":
		col = tok.Danger
	case "run", "running":
		col = tok.Accent
	}
	return mygoui.Box(c).Size(6, 6).Radius(3).Background(col).Shrink(0)
}

// Badge renders a compact 20px V4 status pill ("ok"/"accent", "warn", "danger", or neutral).
func Badge(c *mygoui.Context, tok *Tokens, label, tone string) mygoui.Element {
	bg, fg := tok.Fill2, tok.Text2
	switch tone {
	case "ok", "accent":
		bg, fg = tok.AccentSoft, tok.Accent
	case "warn":
		bg, fg = tok.Fill2, tok.Warning
	case "danger":
		bg, fg = tok.Danger.Alpha(0.12), tok.Danger
	}
	return mygoui.Row(c).
		Height(20).Padding(0, 7).Radius(RadiusSmall).
		Background(bg).AlignItems(mygoui.Center).Justify(mygoui.Center).Shrink(0).
		Children(func() {
			mygoui.Text(c, label).
				Font("system-ui").FontSize(FSCaption).FontWeight(500).FontFeatures("tnum").
				TextColor(fg).SingleLine()
		})
}

// MenuItem describes an entry for BuildMenu.
type MenuItem struct {
	Label     string
	Checked   bool
	Disabled  bool
	Separator bool
	OnSelect  func()
}

// BuildMenu populates a MyGo menu with unified V4 menu item semantics.
func BuildMenu(m *mygoui.Menu, items []MenuItem) {
	for _, it := range items {
		if it.Separator {
			m.Separator()
			continue
		}
		item := m.Item(it.Label)
		if it.Checked {
			item.Checked(true)
		}
		if it.Disabled {
			item.Disabled(true)
		}
		if item.Chosen() && it.OnSelect != nil {
			it.OnSelect()
		}
	}
}

// SelectOption represents a choice in Select.
type SelectOption struct {
	Value string
	Label string
}

// Select renders a V4 outlined dropdown trigger backed by a V4 popover menu.
func Select(c *mygoui.Context, tok *Tokens, key, label, value string, options []SelectOption, disabled, errState bool, onSelect func(value string)) mygoui.Element {
	display := value
	for _, opt := range options {
		if opt.Value == value {
			display = opt.Label
			break
		}
	}
	borderCol := tok.BorderStrong
	if errState {
		borderCol = tok.Danger
	}
	btn := mygoui.ButtonBase(c.Key(key)).Role(mygoui.RoleButton).Label(label).
		Cursor(mygoui.CursorPointer).Height(26).MinWidth(96).Padding(0, Sp2).Gap(6).
		Radius(Radius).Border(1, borderCol).AlignItems(mygoui.Center).Justify(mygoui.SpaceBetween).
		Shrink(0).Disabled(disabled)
	btn.Background(HoverColor(btn, "v4-sel-bg", mygoui.Transparent, tok.Hover))
	ButtonPress(btn, 0.97)
	btn.Children(func() {
		mygoui.Text(c, display).Font("system-ui").FontSize(FSFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
		mygoui.Icon(c, IconDown).FontSize(11).TextColor(tok.Text3).Shrink(0)
	})
	if !disabled {
		btn.PopoverMenuButton(func(m *mygoui.Menu) {
			items := make([]MenuItem, 0, len(options))
			for _, opt := range options {
				v := opt.Value
				items = append(items, MenuItem{
					Label:   opt.Label,
					Checked: v == value,
					OnSelect: func() {
						if v != value && onSelect != nil {
							onSelect(v)
						}
					},
				})
			}
			BuildMenu(m, items)
		})
	}
	return btn
}

// SkeletonRow renders a placeholder within a grouped list.
func SkeletonRow(c *mygoui.Context, tok *Tokens, lines int, avatar, rightPill bool) mygoui.Element {
	double := lines > 1
	minH := float32(44)
	padV := Sp2
	if double {
		minH = 54
		padV = 10
	}
	return mygoui.Row(c).FillWidth().MinHeight(minH).Padding(padV, Sp1).Gap(Sp3).
		AlignItems(mygoui.Center).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		if avatar {
			SkeletonBar(c, tok, 20, 20, RadiusSmall)
		}
		mygoui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			SkeletonBar(c, tok, 160, 11, RadiusSmall)
			if lines > 1 {
				SkeletonBar(c, tok, 260, 9, RadiusSmall)
			}
		})
		if rightPill {
			SkeletonBar(c, tok, 72, 20, Radius)
		}
	})
}

// SkeletonGroup renders count V4 skeleton rows.
func SkeletonGroup(c *mygoui.Context, tok *Tokens, count, lines int, avatar, rightPill bool) mygoui.Element {
	return mygoui.Column(c).FillWidth().Gap(0).Children(func() {
		for i := 0; i < count; i++ {
			SkeletonRow(c, tok, lines, avatar, rightPill)
		}
	})
}

// TextButton uses ButtonBase for native focus, keyboard activation and accessibility.
func TextButton(c *mygoui.Context, tok *Tokens, label string, fn func()) mygoui.Element {
	title := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), "›"))
	b := mygoui.ButtonBase(c).Role(mygoui.RoleButton).Label(label).Height(24).MinHeight(24).
		Padding(0, 4).Gap(4).Radius(RadiusSmall).FontSize(FSFootnote).TextColor(tok.Accent).
		AlignItems(mygoui.Center).Cursor(mygoui.CursorPointer)
	b.Background(HoverColor(b, "link-bg", mygoui.Transparent, tok.AccentSofter))
	b.Children(func() {
		mygoui.Text(c, title).SingleLine()
		mygoui.Icon(c, IconChev).FontSize(11).Shrink(0)
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

func IconButton(c *mygoui.Context, tok *Tokens, label string, icon *mygoui.SVG, fn func()) mygoui.Element {
	b := mygoui.Button(c, "").Cursor(mygoui.CursorPointer).Label(label).Tooltip(label).Radius(Radius).Children(func() { mygoui.Icon(c, icon).FontSize(14).TextColor(tok.Text2) }).OnClick(fn)
	ButtonPress(b, 0.96)
	return b
}

// Use the SDK button for focus, disabled state, tooltips and activation.
func SmallIconAction(c *mygoui.Context, tok *Tokens, label string, icon *mygoui.SVG, fn func()) mygoui.Element {
	return mygoui.Button(c, "").Label(label).Tooltip(label).Cursor(mygoui.CursorPointer).
		Width(22).MinWidth(22).Height(22).MinHeight(22).Shrink(0).
		Padding(3).Gap(0).Radius(4).BorderWidth(0).Background(mygoui.Transparent).
		Children(func() { mygoui.Icon(c, icon).FontSize(14).TextColor(tok.Accent) }).OnClick(fn)
}

// Keep the small hit target in the layout so revealing it never moves text.
// Keyboard focus reveals the action too, without requiring pointer input.
func HoverCopyAction(c *mygoui.Context, tok *Tokens, row mygoui.Element, icon *mygoui.SVG, label string, fn func()) mygoui.Element {
	b := SmallIconAction(c, tok, label, icon, fn)
	if !row.Hovered() && !row.FocusWithin() {
		b.Opacity(0)
	}
	return b
}

func CopyableText(c *mygoui.Context, tok *Tokens, display, raw, label string, role TextRole, copyIcon *mygoui.SVG, fn func()) mygoui.Element {
	row := mygoui.Row(c).MinWidth(0).AlignItems(mygoui.Center).Gap(4)
	return row.Children(func() {
		Text(c, tok, display, role).MinWidth(0).SingleLine().Tooltip(raw)
		HoverCopyAction(c, tok, row, copyIcon, label, fn)
	})
}

func RefreshAction(c *mygoui.Context, tok *Tokens, label string, fn func()) mygoui.Element {
	return SmallIconAction(c, tok, label, IconRefresh24, fn)
}

func RefreshActionLoading(c *mygoui.Context, tok *Tokens, label string, loading bool, fn func()) mygoui.Element {
	if !loading {
		return RefreshAction(c, tok, label, fn)
	}
	return mygoui.Button(c, "").Label(label).Tooltip("正在" + label).Disabled(true).
		Width(22).MinWidth(22).Height(22).MinHeight(22).Shrink(0).
		Padding(3).Gap(0).Radius(4).BorderWidth(0).Background(mygoui.Transparent).
		Children(func() { mygoui.Spinner(c).Size(14, 14).TextColor(tok.Accent) })
}
