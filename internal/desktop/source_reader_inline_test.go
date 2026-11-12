package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestSourceReaderInlineHighlightPreservesLineGeometry(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := sourceReadingFrame()
		a.guidanceRoot = t.TempDir()
		body := "前缀 inline命中 后缀。\n第二行保持原换行。"
		a.reader.Window = newTextWindow(body)
		a.reader.Result.Record.Text = body
		tt := ui.NewTester(a.View, width, 820)
		before, ok := tt.Find(body)
		if !ok {
			t.Fatal("original absent")
		}
		a.reader.Search = "inline命中"
		a.searchSource()
		tt.Frame()
		tt.Frame()
		tt.Frame()
		after, ok := tt.Find(body)
		if !ok {
			t.Fatal("rich text lost exact original bytes")
		}
		if before.H != after.H {
			t.Fatalf("highlight changed line layout: %v -> %v", before, after)
		}
		hit, ok := tt.Find("原文当前匹配")
		if !ok || hit.H >= after.H {
			t.Fatalf("not inline glyph bounds: hit=%v body=%v", hit, after)
		}
		if a.reader.Window.Body != body {
			t.Fatal("source changed")
		}
		writeScreenshot(t, tt, fmt.Sprintf("inline-%d.png", width))
	}
}
func TestSourceReaderLongParagraphNextRevealsActualMatch(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := sourceReadingFrame()
		a.guidanceRoot = t.TempDir()
		// A single paragraph, within one existing 8192-byte window. The second
		// match is far below the viewport; there are no newline anchors to fake it.
		body := "MATCH位置" + strings.Repeat("这是原文连续长段落，保留所有字节和自然换行。", 100) + "MATCH位置末尾"
		a.reader.Window = newTextWindow(body)
		a.reader.Result.Record.Text = body
		if len(a.reader.Window.Offsets) != 2 {
			t.Fatal("fixture crossed read window")
		}
		a.reader.Search = "MATCH位置"
		a.searchSource()
		height := 820
		if width == 800 {
			height = 620
		}
		tt := ui.NewTester(a.View, width, height)
		tt.Frame()
		if err := tt.Click("下一处"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Frame()
		tt.Frame()
		hit, ok := tt.Find("原文当前匹配")
		if !ok {
			t.Fatal("match absent")
		}
		footer, footerOK := tt.Find("当前知识与选中结果保持")
		if a.reader.Match != 1 || a.reader.Scroll.Y <= 0 || hit.Y < 200 || !footerOK || hit.Y+hit.H > footer.Y-8 {
			t.Fatalf("actual next glyph not visible: %v scroll=%v match=%d", hit, a.reader.Scroll.Y, a.reader.Match)
		}
		if a.reader.Window.Body != body {
			t.Fatal("body mutated")
		}
		writeScreenshot(t, tt, fmt.Sprintf("long-next-%d.png", width))
		// Wrap back and revisit a previously laid out match: stale inline bounds
		// must not be used after locateSourceOffset resets the scroll.
		for i := 0; i < 2; i++ {
			if err := tt.Click("下一处"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Frame()
			tt.Frame()
		}
		hit, ok = tt.Find("原文当前匹配")
		if !ok || hit.Y < 200 || hit.Y+hit.H > footer.Y-8 {
			t.Fatalf("revisited match outside viewport: %v", hit)
		}
	}
}

func TestSourceReaderInlineNextAcrossWindow(t *testing.T) {
	a := sourceReadingFrame()
	a.guidanceRoot = t.TempDir()
	body := "跨页目标" + strings.Repeat("保持原文。", 650) + "跨页目标终点"
	a.reader.Window = newTextWindow(body)
	a.reader.Result.Record.Text = body
	a.reader.Search = "跨页目标"
	a.searchSource()
	tt := ui.NewTester(a.View, 800, 820)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("下一处"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	tt.Frame()
	hit, ok := tt.Find("原文当前匹配")
	footer, _ := tt.Find("当前知识与选中结果保持")
	if a.reader.Window.Page == 0 || !ok || hit.Y < 200 || hit.Y+hit.H > footer.Y-8 {
		t.Fatalf("next window match not visible: page=%d hit=%v", a.reader.Window.Page, hit)
	}
	if a.reader.Window.Body != body || a.reader.FocusOffset != strings.LastIndex(body, "跨页目标") {
		t.Fatal("source offset changed")
	}
}
