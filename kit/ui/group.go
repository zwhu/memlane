package ui

import (
	"strings"

	mygoui "github.com/egoist/mygo/ui"
)

// GroupWrap centers configuration groups within the V4 680px reading column.
func GroupWrap(c *mygoui.Context, content func()) mygoui.Element {
	return mygoui.Column(c).FillWidth().MaxWidth(ReaderMaxW).AlignSelf(mygoui.Center).Gap(Sp6).Children(content)
}

// Group renders a cardless V4 grouped list section: caption header on top,
// hairline top border, and rows separated by hairline bottom borders.
func Group(c *mygoui.Context, tok *Tokens, title, desc string, actions func(), rows func()) mygoui.Element {
	return mygoui.Column(c).FillWidth().Gap(0).Children(func() {
		if title != "" || desc != "" || actions != nil {
			mygoui.Row(c).FillWidth().Padding(0, Sp1, Sp2, Sp1).Gap(Sp2).AlignItems(mygoui.Center).Children(func() {
				if title != "" {
					mygoui.Text(c, title).Font("system-ui").FontSize(FSCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
				}
				if desc != "" && actions == nil {
					mygoui.Spacer(c)
					mygoui.Text(c, desc).Font("system-ui").FontSize(FSCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
				} else if desc != "" {
					mygoui.Text(c, desc).Font("system-ui").FontSize(FSCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
					mygoui.Spacer(c)
				} else if actions != nil {
					mygoui.Spacer(c)
				}
				if actions != nil {
					mygoui.Row(c).AlignItems(mygoui.Center).Gap(Sp2).Shrink(0).Children(actions)
				}
			})
		}
		mygoui.Column(c).FillWidth().Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(rows)
	})
}

// RowOptions configures a single hairline-separated row inside Group.
type RowOptions struct {
	Key         string
	Title       string
	TitleExtra  func()
	Desc        string
	DescMono    bool
	DescExtra   func()
	Error       string
	Value       string
	ValueMono   bool
	ValueColor  mygoui.Color
	Leading     func()
	Control     func()
	Clickable   bool
	Chevron     bool
	HideChevron bool
	Disabled    bool
	NoBorder    bool
	Label       string
	Tooltip     string
	OnClick     func()
}

// Row renders a 32px single-line or 52px double-line V4 settings row.
func Row(c *mygoui.Context, tok *Tokens, opts RowOptions) mygoui.Element {
	ctx := c
	if opts.Key != "" {
		ctx = c.Key(opts.Key)
	}
	double := opts.Desc != "" || opts.DescExtra != nil || opts.Error != ""
	minH := RowHeight1
	padV := Sp2
	colGap := float32(2)
	if double {
		minH = 52
		padV = 10
		colGap = 3
		if (opts.Desc != "" && (len([]rune(opts.Desc)) > 20 || strings.Contains(opts.Desc, "\n"))) || opts.Error != "" {
			padV = Sp3
		}
	}
	showChevron := (opts.Clickable || opts.Chevron) && !opts.HideChevron

	buildContent := func(hovered bool) {
		if opts.Leading != nil {
			mygoui.Row(c).Shrink(0).AlignItems(mygoui.Center).Justify(mygoui.Center).Children(opts.Leading)
		}
		mygoui.Column(c).Grow(1).MinWidth(0).Gap(colGap).Children(func() {
			if opts.Title != "" || opts.TitleExtra != nil {
				mygoui.Row(c).FillWidth().AlignItems(mygoui.Center).Gap(Sp2).Children(func() {
					if opts.Title != "" {
						mygoui.Text(c, opts.Title).Font("system-ui").FontSize(FSBody).FontWeight(500).TextColor(tok.Text1).LineHeight(1.4)
					}
					if opts.TitleExtra != nil {
						opts.TitleExtra()
					}
				})
			}
			if opts.Desc != "" {
				t := mygoui.Text(c, opts.Desc).FillWidth().MinWidth(0).FontSize(FSFootnote).FontWeight(400).TextColor(tok.Text3).LineHeight(1.45)
				if opts.DescMono {
					t.Font("ui-monospace").FontSize(FSCaption).FontFeatures("tnum")
				} else {
					t.Font("system-ui")
				}
			}
			if opts.DescExtra != nil {
				opts.DescExtra()
			}
			if opts.Error != "" {
				mygoui.Text(c, opts.Error).FillWidth().MinWidth(0).Font("system-ui").FontSize(FSCaption).FontWeight(400).TextColor(tok.Danger).LineHeight(1.4)
			}
		})
		if opts.Value != "" || opts.Control != nil || showChevron {
			mygoui.Row(c).Shrink(0).AlignItems(mygoui.Center).Gap(Sp2).Children(func() {
				if opts.Value != "" {
					vc := tok.Text2
					if opts.ValueColor.A > 0 {
						vc = opts.ValueColor
					}
					vt := mygoui.Text(c, opts.Value).FontSize(FSFootnote).FontWeight(400).FontFeatures("tnum").TextColor(vc).SingleLine()
					if opts.ValueMono {
						vt.Font("ui-monospace").FontSize(FSCaption)
					} else {
						vt.Font("system-ui")
					}
				}
				if opts.Control != nil {
					opts.Control()
				}
				if showChevron {
					chevColor := tok.Text4
					if hovered {
						chevColor = tok.Text2
					}
					mygoui.Icon(c, IconChev).FontSize(14).TextColor(chevColor).Shrink(0)
				}
			})
		}
	}

	if opts.Clickable {
		label := opts.Label
		if label == "" {
			label = opts.Title
		}
		btn := mygoui.ButtonBase(ctx).Role(mygoui.RoleButton).Label(label).
			FillWidth().MinHeight(minH).Padding(padV, Sp2).Gap(Sp3).
			AlignItems(mygoui.Center).Justify(mygoui.Start).Radius(Radius).
			Cursor(mygoui.CursorPointer).Disabled(opts.Disabled)
		if opts.Tooltip != "" {
			btn.Tooltip(opts.Tooltip)
		}
		hovered := btn.Hovered()
		if !opts.NoBorder {
			borderCol := tok.Border
			if hovered {
				borderCol = mygoui.Transparent
			}
			btn.BorderWidth(0, 0, 1, 0).BorderColor(borderCol)
		}
		btn.Background(HoverColor(btn, "v4-row-bg", mygoui.Transparent, tok.Hover))
		ButtonPress(btn, 0.995)
		btn.Children(func() { buildContent(hovered) })
		if opts.OnClick != nil {
			btn.OnClick(opts.OnClick)
		}
		return btn
	}

	row := mygoui.Row(ctx).FillWidth().MinHeight(minH).Padding(padV, Sp1).Gap(Sp3).
		AlignItems(mygoui.Center)
	if !opts.NoBorder {
		row.BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
	}
	if opts.Label != "" {
		row.Label(opts.Label)
	}
	if opts.Tooltip != "" {
		row.Tooltip(opts.Tooltip)
	}
	row.Children(func() { buildContent(false) })
	return row
}
