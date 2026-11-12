package desktop

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

// Display string values directly, rather than JSON-escaping a command's
// newlines. Composite values remain JSON and retain exact numeric literals.
func (a *App) conversationTraceField(c *ui.Context, s *ConversationUI, trace, field string, raw json.RawMessage) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		ui.Text(c, "未提供").FontSize(11).TextColor(design.Muted)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		ui.Text(c, "暂无法显示此字段").FontSize(11).TextColor(design.Muted)
		return
	}
	if object, ok := value.(map[string]any); ok && field == "参数" && len(object) > 0 {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			ui.Text(c, key).FontSize(10).TextColor(design.Muted)
			a.conversationTraceValue(c, s, records.MergeDigest([]string{trace, field, key}), object[key])
		}
		return
	}
	a.conversationTraceValue(c, s, records.MergeDigest([]string{trace, field}), value)
}

func (a *App) conversationTraceValue(c *ui.Context, s *ConversationUI, key string, value any) {
	text, ok := value.(string)
	if !ok {
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if encoder.Encode(value) != nil {
			return
		}
		text = strings.TrimSuffix(buf.String(), "\n")
	}
	box := ui.ScrollHorizontal(c.Key("trace-value-"+key)).Label("工具字段横向滚动").Direction(ui.LeftToRight).MinWidth(0).Padding(10).Background(ui.Hex("#ffffff")).Border(1, design.Border).Radius(4)
	if view := s.Views[s.Selected]; view != nil {
		if view.TraceScroll == nil {
			view.TraceScroll = map[string]*ui.ScrollState{}
		}
		if view.TraceScroll[key] == nil {
			view.TraceScroll[key] = &ui.ScrollState{}
		}
		box.TrackScroll(view.TraceScroll[key])
	}
	box.Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(11).TextColor(design.Ink).LineHeight(1.7).NoWrap().Shrink(0)
	})
}
