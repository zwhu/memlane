package simpledesktop

import "github.com/egoist/mygo/ui"

// v4GroupWrap centers configuration groups within the V4 680px reading column.
func v4GroupWrap(c *ui.Context, content func()) ui.Element {
	return ui.Column(c).FillWidth().MaxWidth(readerMaxW).AlignSelf(ui.Center).Gap(sp6).Children(content)
}

// v4Group renders a cardless V4 grouped list section: caption header on top,
// hairline top border, and rows separated by hairline bottom borders.
func v4Group(c *ui.Context, title, desc string, actions func(), rows func()) ui.Element {
	return ui.Column(c).FillWidth().Gap(0).Children(func() {
		if title != "" || desc != "" || actions != nil {
			ui.Row(c).FillWidth().Padding(0, sp1, sp2, sp1).Gap(sp2).AlignItems(ui.Center).Children(func() {
				if title != "" {
					ui.Text(c, title).Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
				}
				if desc != "" && actions == nil {
					ui.Spacer(c)
					ui.Text(c, desc).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
				} else if desc != "" {
					ui.Text(c, desc).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Text3).SingleLine()
					ui.Spacer(c)
				} else if actions != nil {
					ui.Spacer(c)
				}
				if actions != nil {
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Shrink(0).Children(actions)
				}
			})
		}
		ui.Column(c).FillWidth().Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(rows)
	})
}

// v4RowOptions configures a single hairline-separated row inside v4Group.
type v4RowOptions struct {
	Key         string
	Title       string
	TitleExtra  func()
	Desc        string
	DescMono    bool
	DescExtra   func()
	Error       string
	Value       string
	ValueMono   bool
	ValueColor  ui.Color
	Leading     func()
	Control     func()
	Clickable   bool
	Chevron     bool
	HideChevron bool
	Disabled    bool
	Label       string
	Tooltip     string
	OnClick     func()
}

// v4Row renders a 32px single-line or 52px double-line V4 settings row.
func v4Row(c *ui.Context, opts v4RowOptions) ui.Element {
	ctx := c
	if opts.Key != "" {
		ctx = c.Key(opts.Key)
	}
	double := opts.Desc != "" || opts.DescExtra != nil || opts.Error != ""
	minH := rowHeight1
	padV := sp2
	if double {
		minH = 52
		padV = 10
	}
	showChevron := (opts.Clickable || opts.Chevron) && !opts.HideChevron

	buildContent := func(hovered bool) {
		if opts.Leading != nil {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(opts.Leading)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			if opts.Title != "" || opts.TitleExtra != nil {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
					if opts.Title != "" {
						ui.Text(c, opts.Title).Font("system-ui").FontSize(fsBody).FontWeight(500).TextColor(tok.Text1).LineHeight(1.4)
					}
					if opts.TitleExtra != nil {
						opts.TitleExtra()
					}
				})
			}
			if opts.Desc != "" {
				t := ui.Text(c, opts.Desc).FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text3).LineHeight(1.45)
				if opts.DescMono {
					t.Font("ui-monospace").FontSize(fsCaption).FontFeatures("tnum")
				} else {
					t.Font("system-ui")
				}
			}
			if opts.DescExtra != nil {
				opts.DescExtra()
			}
			if opts.Error != "" {
				ui.Text(c, opts.Error).Font("system-ui").FontSize(fsCaption).FontWeight(400).TextColor(tok.Danger).LineHeight(1.4)
			}
		})
		if opts.Value != "" || opts.Control != nil || showChevron {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
				if opts.Value != "" {
					vc := tok.Text2
					if opts.ValueColor.A > 0 {
						vc = opts.ValueColor
					}
					vt := ui.Text(c, opts.Value).FontSize(fsFootnote).FontWeight(400).FontFeatures("tnum").TextColor(vc).SingleLine()
					if opts.ValueMono {
						vt.Font("ui-monospace").FontSize(fsCaption)
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
					ui.Icon(c, v4IconChev).FontSize(14).TextColor(chevColor).Shrink(0)
				}
			})
		}
	}

	if opts.Clickable {
		label := opts.Label
		if label == "" {
			label = opts.Title
		}
		btn := ui.ButtonBase(ctx).Role(ui.RoleButton).Label(label).
			FillWidth().MinHeight(minH).Padding(padV, sp2).Gap(sp3).
			AlignItems(ui.Center).Justify(ui.Start).Radius(radius).
			Cursor(ui.CursorPointer).Disabled(opts.Disabled)
		if opts.Tooltip != "" {
			btn.Tooltip(opts.Tooltip)
		}
		hovered := btn.Hovered()
		borderCol := tok.Border
		if hovered {
			borderCol = ui.Transparent
		}
		btn.BorderWidth(0, 0, 1, 0).BorderColor(borderCol)
		btn.Background(homeHoverColor(btn, "v4-row-bg", ui.Transparent, tok.Hover))
		v4ButtonPress(btn, 0.995)
		btn.Children(func() { buildContent(hovered) })
		if opts.OnClick != nil {
			btn.OnClick(opts.OnClick)
		}
		return btn
	}

	row := ui.Row(ctx).FillWidth().MinHeight(minH).Padding(padV, sp1).Gap(sp3).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
	if opts.Label != "" {
		row.Label(opts.Label)
	}
	if opts.Tooltip != "" {
		row.Tooltip(opts.Tooltip)
	}
	row.Children(func() { buildContent(false) })
	return row
}
