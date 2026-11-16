package simpledesktop

import "github.com/egoist/mygo/ui"

// Static placeholders follow the final row geometry without suggesting an empty result.
func overviewLoadingRows(c *ui.Context, label string, compact bool) {
	ui.Column(c).FillWidth().Label(label).Padding(8, 14).Gap(0).Children(func() {
		for n := 0; n < 4; n++ {
			ui.Row(c).FillWidth().Padding(13, 0).Gap(13).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(28, 33).Shrink(0).Radius(6).Background(ui.Hex("#edf2f6"))
				ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
					width := float32(230)
					if compact {
						width = 140
					}
					if n%2 == 1 {
						width *= .78
					}
					ui.Box(c).Width(width).MaxWidth(width).Height(11).Radius(4).Background(ui.Hex("#edf2f6"))
					ui.Box(c).Width(width * .58).Height(8).Radius(3).Background(ui.Hex("#f1f5f8"))
				})
			})
		}
	})
}
