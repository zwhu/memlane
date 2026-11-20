package simpledesktop

import "github.com/egoist/mygo/ui"

// List surfaces in V4 use dividers and whitespace instead of nested bordered cards.
func listSurface(c *ui.Context, content func()) {
	ui.Column(c).FillWidth().Gap(0).Background(tok.Bg).Border(1, tok.Border).Radius(radius).Padding(sp2, sp4).Children(content)
}
