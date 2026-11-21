package simpledesktop

import "github.com/egoist/mygo/ui"

// v4SmallButton renders a compact 24px V4 outlined button for inline row/header actions.
func v4SmallButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Gap(5).
		Radius(radius).Border(1, tok.BorderStrong).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "sbtn-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(b, 0.96)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(12).TextColor(tok.Text2).Shrink(0)
		}
		ui.Text(c, label).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text1).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4SmallPrimaryButton renders a compact 24px V4 accent primary button.
func v4SmallPrimaryButton(c *ui.Context, label string, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Gap(5).
		Radius(radius).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "spbtn-bg", tok.Accent, tok.AccentHover))
	v4ButtonPress(b, 0.96)
	b.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.OnAccent).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4DangerButton renders a solid 28px V4 destructive primary button for confirmation dialogs.
func v4DangerButton(c *ui.Context, label string, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(28).Padding(0, sp3).Gap(6).
		Radius(radius).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "dbtn-bg", tok.Danger, tok.Danger.Alpha(0.88)))
	v4ButtonPress(b, 0.96)
	b.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.OnAccent).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4DangerOutlineButton renders a danger-outlined button for inline delete actions.
func v4DangerOutlineButton(c *ui.Context, label string, small bool, fn func()) ui.Element {
	h, padX, fs := float32(28), sp3, fsFootnote
	if small {
		h, padX, fs = 24, sp2, fsCaption
	}
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(h).Padding(0, padX).Gap(5).
		Radius(radius).Border(1, tok.Danger).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "dobtn-bg", ui.Transparent, tok.Danger.Alpha(0.10)))
	v4ButtonPress(b, 0.96)
	b.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fs).FontWeight(500).TextColor(tok.Danger).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4GhostButton renders a borderless V4 action button with subtle hover tint.
func v4GhostButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	b := ui.ButtonBase(c).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(28).Padding(0, sp2).Gap(5).
		Radius(radius).BorderWidth(0).AlignItems(ui.Center).Shrink(0)
	b.Background(homeHoverColor(b, "gbtn-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(b, 0.96)
	txtCol := tok.Text2
	if b.Hovered() {
		txtCol = tok.Text1
	}
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(14).TextColor(txtCol).Shrink(0)
		}
		ui.Text(c, label).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(txtCol).SingleLine()
	})
	if fn != nil {
		b.OnClick(fn)
	}
	return b
}

// v4StatusDot draws a 6px semantic status dot ("ok", "warn", "fail", "run", or neutral).
func v4StatusDot(c *ui.Context, state string) ui.Element {
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
	return ui.Box(c).Size(6, 6).Radius(3).Background(col).Shrink(0)
}

// v4Badge renders a compact 20px V4 status pill ("ok"/"accent", "warn", "danger", or neutral).
func v4Badge(c *ui.Context, label, tone string) ui.Element {
	bg, fg := tok.Fill2, tok.Text2
	switch tone {
	case "ok", "accent":
		bg, fg = tok.AccentSoft, tok.Accent
	case "warn":
		bg, fg = tok.Fill2, tok.Warning
	case "danger":
		bg, fg = tok.Danger.Alpha(0.12), tok.Danger
	}
	return ui.Text(c, label).
		Font("system-ui").FontSize(fsCaption).FontWeight(500).FontFeatures("tnum").
		Height(20).Padding(0, 7).Radius(radiusSmall).
		Background(bg).TextColor(fg).SingleLine().Shrink(0)
}

// v4MenuItem describes an entry for v4BuildMenu.
type v4MenuItem struct {
	Label     string
	Checked   bool
	Disabled  bool
	Separator bool
	OnSelect  func()
}

// v4BuildMenu populates a MyGo ui.Menu with unified V4 menu item semantics.
func v4BuildMenu(m *ui.Menu, items []v4MenuItem) {
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

// v4SelectOption represents a choice in v4Select.
type v4SelectOption struct {
	Value string
	Label string
}

// v4Select renders a V4 outlined dropdown trigger backed by a V4 popover menu.
func v4Select(c *ui.Context, key, label, value string, options []v4SelectOption, disabled, errState bool, onSelect func(value string)) ui.Element {
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
	btn := ui.ButtonBase(c.Key(key)).Role(ui.RoleButton).Label(label).
		Cursor(ui.CursorPointer).Height(26).MinWidth(96).Padding(0, sp2).Gap(6).
		Radius(radius).Border(1, borderCol).AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Shrink(0).Disabled(disabled)
	btn.Background(homeHoverColor(btn, "v4-sel-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(btn, 0.97)
	btn.Children(func() {
		ui.Text(c, display).Font("system-ui").FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text1).SingleLine()
		ui.Icon(c, v4IconDown).FontSize(11).TextColor(tok.Text3).Shrink(0)
	})
	if !disabled {
		btn.PopoverMenuButton(func(m *ui.Menu) {
			items := make([]v4MenuItem, 0, len(options))
			for _, opt := range options {
				v := opt.Value
				items = append(items, v4MenuItem{
					Label:   opt.Label,
					Checked: v == value,
					OnSelect: func() {
						if v != value && onSelect != nil {
							onSelect(v)
						}
					},
				})
			}
			v4BuildMenu(m, items)
		})
	}
	return btn
}

// v4TriAccessOptions defines the three-tier project access choices (Q5).
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
				ui.Row(c).AlignItems(ui.Center).Gap(5).Shrink(0).Children(func() {
					v4Spinner(c)
					ui.Text(c, "保存中…").Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
				})
			}
			label := "访问权限"
			if agentName != "" {
				label = agentName + " 访问权限"
			}
			v4Select(c, key, label, state.Value, v4TriAccessOptions, state.Disabled || state.Saving, state.Error != "", onSelect)
		})
		if state.Error != "" {
			ui.Text(c, state.Error).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Danger).LineHeight(1.35)
		}
	})
}

// v4SkeletonRow renders a single V4 skeleton placeholder row inside a grouped list.
func v4SkeletonRow(c *ui.Context, lines int, avatar, rightPill bool) ui.Element {
	double := lines > 1
	minH := float32(44)
	padV := sp2
	if double {
		minH = 54
		padV = 10
	}
	return ui.Row(c).FillWidth().MinHeight(minH).Padding(padV, sp1).Gap(sp3).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		if avatar {
			v4SkeletonBar(c, 20, 20, radiusSmall)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			v4SkeletonBar(c, 160, 11, radiusSmall)
			if lines > 1 {
				v4SkeletonBar(c, 260, 9, radiusSmall)
			}
		})
		if rightPill {
			v4SkeletonBar(c, 72, 20, radius)
		}
	})
}

// v4SkeletonGroup renders count V4 skeleton rows.
func v4SkeletonGroup(c *ui.Context, count, lines int, avatar, rightPill bool) ui.Element {
	return ui.Column(c).FillWidth().Gap(0).Children(func() {
		for i := 0; i < count; i++ {
			v4SkeletonRow(c, lines, avatar, rightPill)
		}
	})
}
