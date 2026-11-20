package simpledesktop

import "github.com/egoist/mygo/ui"

// Static placeholders match the final 2-line/3-line row geometry without
// suggesting an empty result or shifting layout on swap.
func overviewLoadingRows(c *ui.Context, label string, compact bool) {
	ui.Column(c).FillWidth().Label(label).Padding(sp1, sp2).Gap(0).Children(func() {
		count := 5
		if compact {
			count = 4
		}
		for n := 0; n < count; n++ {
			row := ui.Row(c).FillWidth().Padding(sp2+2, 0).Gap(sp3).AlignItems(ui.Center)
			if compact {
				row.MinHeight(52)
			} else {
				row.MinHeight(68)
			}
			row.Children(func() {
				if compact {
					ui.Box(c).Size(6, 6).Shrink(0).Radius(3).Background(tok.Fill2)
				} else {
					ui.Box(c).Size(22, 22).Shrink(0).Radius(radius).Background(tok.Fill2)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					width := float32(360)
					if compact {
						width = 150
					}
					if n%2 == 1 {
						width *= .82
					}
					ui.Box(c).Width(width).MaxWidth(width).Height(12).Radius(radiusSmall).Background(tok.Fill2)
					if !compact {
						ui.Box(c).Width(width * .72).MaxWidth(width * .72).Height(12).Radius(radiusSmall).Background(tok.Fill2)
					}
					ui.Box(c).Width(width * .45).Height(9).Radius(3).Background(tok.Fill1)
				})
			})
		}
	})
}
