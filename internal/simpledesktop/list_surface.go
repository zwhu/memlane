package simpledesktop

import "github.com/egoist/mygo/ui"

// List surfaces have one treatment across the overview and settings.
func listSurface(c *ui.Context, content func()) {
	ui.Column(c).FillWidth().Gap(0).Padding(8, 20).Background(homeBackground).Border(1, homeBorder).Radius(10).Children(content)
}
