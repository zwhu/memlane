package desktop

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

func TestReadWindowsPreserveCompleteUnicodeSource(t *testing.T) {
	for _, body := range []string{"", strings.Repeat("a", readWindowBytes), strings.Repeat("来源中文🙂\n", 50000)} {
		w := newTextWindow(body)
		var recovered strings.Builder
		for i := 1; i < len(w.Offsets); i++ {
			part := w.Body[w.Offsets[i-1]:w.Offsets[i]]
			if !utf8.ValidString(part) || len(part) > readWindowBytes+utf8.UTFMax-1 {
				t.Fatalf("invalid or unbounded read window: %d bytes", len(part))
			}
			recovered.WriteString(part)
		}
		if recovered.String() != body {
			t.Fatal("read windows changed or omitted source bytes")
		}
	}
}

func TestListPreviewBoundsLargeUnicodeText(t *testing.T) {
	body := strings.Repeat("中文🙂 ", 50000)
	if got := preview(body); utf8.RuneCountInString(got) != 241 || !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Fatal("list preview still renders full body or splits Unicode")
	}
	if got := preview("项目\n  约定"); got != "项目 约定" {
		t.Fatalf("short preview changed: %q", got)
	}
}

func TestLongTextNavigationAndFullClipboard(t *testing.T) {
	a := &App{}
	body := strings.Repeat("中", 6000) + "最后一段原文"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() { ui.Text(c, a.readableText(c, "test", body)).Selectable() })
	}, 1200, 780)
	w := a.textWindows["test"]
	for page := 1; page < len(w.Offsets)-1; page++ {
		if err := tt.Click("下一段"); err != nil {
			t.Fatal(err)
		}
		if w.Page != page {
			t.Fatal("next read window did not advance")
		}
	}
	if err := tt.Click("复制全文"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != body {
		t.Fatal("copy returned only the visible page")
	}
	if err := tt.Click("上一段"); err != nil || w.Page != len(w.Offsets)-3 {
		t.Fatalf("previous read window failed: %v", err)
	}
}
