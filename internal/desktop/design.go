package desktop

import (
	"embed"
	"github.com/egoist/mygo/ui"
)

//go:embed assets/*.svg
var environmentAssets embed.FS

func environmentSVG(name string) *ui.SVG {
	raw, err := environmentAssets.ReadFile("assets/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(raw)
}

// Tokens are measured against the selected 1200 x 820 content area. The
// navigation is 212 DIP, the memory list 428 DIP, leaving 560 DIP for details.
var design = struct {
	Sidebar, List, Toolbar, Body, Small, Heading, Title, Icon        float32
	SidebarFill, Selected, ListSelected, Border, Ink, Muted, Surface ui.Color
	Heat                                                             [5]ui.Color
}{
	212, 428, 54, 12, 11, 20, 20, 15,
	ui.Hex("#e8f0fa"), ui.Hex("#d6e2ef"), ui.Hex("#eef1f5"),
	ui.Hex("#e9eaed"), ui.Hex("#30353a"), ui.Hex("#8a9098"), ui.Hex("#f4f5f7"),
	[5]ui.Color{ui.Hex("#f0f2f5"), ui.Hex("#e0eaf5"), ui.Hex("#c3d5e9"), ui.Hex("#9bb8d7"), ui.Hex("#688db9")},
}

var icons = map[string]*ui.SVG{
	"sqlite":  environmentSVG("sqlite"),
	"lancedb": environmentSVG("lancedb"),
	"model":   svg(`<rect x="5" y="5" width="14" height="14" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M8 2v3m8-3v3M8 19v3m8-3v3M2 8h3m-3 8h3M19 8h3m-3 8h3"/>`),
	"brand":   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><g fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round"><path d="M72 30 A30 30 0 1 0 79 58"/><path d="M64 36 A20 20 0 1 0 69 55"/><path d="M56 43 A10 10 0 1 0 59 53"/></g><circle cx="78" cy="36" r="4" fill="currentColor"/></svg>`)),
	"pi":      ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M5 3h12v4h4v10h-4V7H9v4h8v4H9v6H5z"/></svg>`)),
	"codex":   svg(`<rect x="2" y="4" width="20" height="16" rx="6"/><path d="m7 9 3 3-3 3m6 0h4"/>`),
	"总览":      svg(`<rect x="3" y="3" width="6" height="6" rx="1"/><rect x="15" y="3" width="6" height="6" rx="1"/><rect x="3" y="15" width="6" height="6" rx="1"/><rect x="15" y="15" width="6" height="6" rx="1"/>`),
	"全部记忆":    svg(`<path d="m3 7 9-5 9 5-9 5-9-5zm0 5 9 5 9-5M3 17l9 5 9-5"/>`),
	"通用知识":    svg(`<circle cx="12" cy="7" r="4"/><path d="M4 21v-3a8 8 0 0 1 16 0v3z"/>`),
	"Agent":   svg(`<rect x="2" y="4" width="20" height="16" rx="3"/><path d="m6 9 3 3-3 3m6 0h5"/>`),
	"整理记录":    svg(`<circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>`),
	"项目":      svg(`<path d="M3 6h7l2 3h9v12H3z"/>`),
	"设置":      svg(`<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="3"/><path d="M12 1v3m0 16v3M1 12h3m16 0h3"/>`),
	"sidebar": svg(`<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/>`),
	"search":  svg(`<circle cx="10" cy="10" r="6"/><path d="m15 15 5 5"/>`),
	"edit":    svg(`<path d="m4 16 12-12 4 4-12 12-5 1zM13 7l4 4"/>`),
	"history": svg(`<circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>`),
	"plus":    svg(`<path d="M12 5v14M5 12h14"/>`),
}

func svg(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}
func actionButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	return ui.Button(c, "").Label(label).Height(30).Padding(6, 12).Gap(7).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(13).TextColor(design.Ink)
		}
		ui.Text(c, label).SingleLine()
	})
}
func iconButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	return ui.Button(c, "").Label(label).Tooltip(label).Size(24, 24).Padding(4).BorderWidth(0).Background(ui.Transparent).Children(func() { ui.Icon(c, icon).FontSize(15).TextColor(design.Muted) })
}
func quietButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).FontSize(design.Small).TextColor(design.Muted).Padding(3, 0).BorderWidth(0).Background(ui.Transparent)
}
func meta(c *ui.Context, label, value string) {
	ui.Row(c).AlignItems(ui.Start).Gap(12).Children(func() {
		ui.Text(c, label).Width(56).FontSize(design.Small).TextColor(design.Muted)
		ui.Text(c, value).FontSize(design.Small).Grow(1).MinWidth(0).Selectable()
	})
}

func init() { icons["搜索"] = icons["search"] }
