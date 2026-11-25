package app

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

//go:embed mark.svg
var markBytes []byte
var mark = ui.MustParseSVG(markBytes)
var folder = monoIcon(`<path d="M3 7h6l2-3h4l2 3h4v14H3z"/>`)
var navIcons = map[string]*ui.SVG{
	"总览":   monoIcon(`<rect x="3" y="3" width="6" height="6" rx="1"/><rect x="15" y="3" width="6" height="6" rx="1"/><rect x="3" y="15" width="6" height="6" rx="1"/><rect x="15" y="15" width="6" height="6" rx="1"/>`),
	"搜索":   monoIcon(`<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6"/>`),
	"通用知识": monoIcon(`<circle cx="12" cy="7" r="4"/><path d="M4 22v-4a8 8 0 0 1 16 0v4"/>`),
}

func monoIcon(paths string) *ui.SVG {
	return kitui.MonoIcon(paths)
}
