package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestContentExplanationPreservesReadingAndReturnsFocus(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := projectMenuFrame()
		a.readingSearch, a.readingFilter = "", ""
		owner := a.selectedID
		tt := ui.NewTester(a.View, width, 820)
		if err := tt.Click("记忆是怎么来的？"); err != nil {
			t.Fatal(err)
		}
		if !a.contentExplanation.Open || !tt.HasText("留下过程，积累知识。") || a.selectedID != owner || a.scope != "a" {
			t.Fatal("content explanation replaced the reading context")
		}
		writeScreenshot(t, tt, fmt.Sprintf("content-explanation-%d.png", width))
		if err := tt.Click("举个例子"); err != nil || !a.contentExplanation.Example {
			t.Fatal("example diagram unavailable", err)
		}
		if err := tt.Click("与 Cognee 的关系"); err != nil || !a.contentExplanation.CogneeOpen {
			t.Fatal("Cognee explanation unavailable", err)
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if a.contentExplanation.Open || a.selectedID != owner || !tt.Focused("记忆是怎么来的？") {
			t.Fatal("closing explanation lost reading or return focus")
		}
	}
}
