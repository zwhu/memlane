package simpledesktop

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

func sameActionPixels(a, b image.Image, r ui.Rect) bool {
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}

func TestInlineCopyHoverFocusGeometryAndClipboard(t *testing.T) {
	const raw = "/synthetic/project/path"
	disabled := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
			row.Children(func() {
				homeText(c, raw, homePath).SingleLine().MinWidth(0)
				hoverCopyAction(c, row, "复制项目路径", func() { c.WriteClipboard(raw) }).Disabled(disabled)
			})
		})
	}, 600, 100)
	text, _ := tt.Find(raw)
	button, ok := tt.Find("复制项目路径")
	if !ok || button.W != 22 || button.H != 22 || button.X-text.X-text.W > 5 {
		t.Fatalf("copy is not a small inline action: text=%v copy=%v", text, button)
	}
	hidden := tt.Image()
	tt.Move(text.X+2, text.Y+2)
	tt.Frame()
	if sameActionPixels(hidden, tt.Image(), button) {
		t.Fatal("hover did not reveal the copy icon")
	}
	if after, _ := tt.Find(raw); text != after {
		t.Fatal("hover moved text")
	}
	if err := tt.Click("复制项目路径"); err != nil || tt.Clipboard() != raw {
		t.Fatalf("copy changed the original value: %v", err)
	}
	// A fresh tester gives keyboard focus without pointer hover.
	tt = ui.NewTester(func(c *ui.Context) {
		copyableText(c, "短显示", raw, "复制字段", homeMeta, func() { c.WriteClipboard(raw) })
	}, 300, 80)
	button, _ = tt.Find("复制字段")
	hidden = tt.Image()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("复制字段") || sameActionPixels(hidden, tt.Image(), button) {
		t.Fatal("keyboard focus did not reveal the copy action")
	}
	tt.Key(0, ui.KeyEnter)
	if tt.Clipboard() != raw {
		t.Fatal("keyboard copied the shortened display instead of raw value")
	}
}

func TestRefreshIconKeepsLabelKeyboardAndDisabledState(t *testing.T) {
	count, disabled := 0, false
	tt := ui.NewTester(func(c *ui.Context) {
		refreshAction(c, "刷新待处理文档", func() { count++ }).Disabled(disabled)
	}, 160, 80)
	r, ok := tt.Find("刷新待处理文档")
	if !ok || r.W != 22 || r.H != 22 {
		t.Fatal("refresh icon or accessible label missing", r)
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	if count != 1 {
		t.Fatal("refresh lost keyboard activation")
	}
	disabled = true
	tt.Frame()
	tt.Click("刷新待处理文档")
	if count != 1 {
		t.Fatal("disabled refresh ran")
	}
}

func TestRefreshIconLoadingKeepsGeometryAndDisablesClicks(t *testing.T) {
	count, loading := 0, true
	tt := ui.NewTester(func(c *ui.Context) {
		refreshActionLoading(c, "重新检测", loading, func() { count++ })
	}, 160, 80)
	r, ok := tt.Find("重新检测")
	if !ok || r.W != 22 || r.H != 22 {
		t.Fatal("loading refresh icon or accessible label missing", r)
	}
	tt.Click("重新检测")
	if count != 0 {
		t.Fatal("loading refresh triggered callback")
	}
	loading = false
	tt.Frame()
	tt.Click("重新检测")
	if count != 1 {
		t.Fatal("idle refresh did not trigger callback")
	}
}
