package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// A bounded read window prevents a single large paragraph from monopolizing
// native text layout. Offsets refer to the unchanged UTF-8 source string.
const readWindowBytes = 8192

type textWindow struct {
	Body    string
	Offsets []int
	Page    int
}

func newTextWindow(body string) *textWindow {
	w := &textWindow{Body: body, Offsets: []int{0}}
	for offset := range body {
		if offset-w.Offsets[len(w.Offsets)-1] >= readWindowBytes {
			w.Offsets = append(w.Offsets, offset)
		}
	}
	w.Offsets = append(w.Offsets, len(body))
	return w
}

func (a *App) readableText(c *ui.Context, key, body string) string {
	if len(body) <= readWindowBytes {
		return body
	}
	if a.textWindows == nil {
		a.textWindows = map[string]*textWindow{}
	}
	w := a.textWindows[key]
	if w == nil || w.Body != body {
		w = newTextWindow(body)
		a.textWindows[key] = w
	}
	pages := len(w.Offsets) - 1
	w.Page = min(max(w.Page, 0), pages-1)
	ui.Row(c.Key("read-" + key)).Gap(8).Children(func() {
		ui.Text(c, fmt.Sprintf("长文分段显示 · 第 %d / %d 段", w.Page+1, pages)).FontSize(design.Small).TextColor(design.Muted)
		quietButton(c, "上一段").Key("read-" + key + "-previous").Disabled(w.Page == 0).OnClick(func() { w.Page-- })
		quietButton(c, "下一段").Key("read-" + key + "-next").Disabled(w.Page == pages-1).OnClick(func() { w.Page++ })
		quietButton(c, "复制全文").Key("read-" + key + "-copy").OnClick(func() { c.Services().WriteClipboard(body) })
	})
	return body[w.Offsets[w.Page]:w.Offsets[w.Page+1]]
}
