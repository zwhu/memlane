package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Expectations come from full-design-reading.js formHTML, not the old editor.
func TestMVPEditorDesignActionsAndExistingKnowledgeScope(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := projectMenuFrame()
		a.readingSearch, a.readingFilter = "", ""
		a.editMemory()
		a.onSave = func() {}
		tt := ui.NewTester(a.View, width, 820)
		writeScreenshot(t, tt, fmt.Sprintf("mvp-editor-%d.png", width))
		if tt.HasText("记忆范围") || tt.HasText("保存位置") {
			t.Fatal("H02: existing knowledge must not show a scope selector")
		}
		back, ok := tt.Find("保留草稿返回")
		if !ok {
			t.Fatal("H02: missing header draft return")
		}
		save, ok := tt.Find("保存知识")
		if !ok || back.Y >= save.Y || back.Y > 120 {
			t.Fatal("H02: return belongs in the header, save in the footer")
		}
		pixel := tt.Image().RGBAAt(int(save.X+8), int(save.Y+8))
		if pixel.R > 100 || pixel.G > 100 || pixel.B > 100 {
			t.Fatal("H02: save must use the dark primary style")
		}
		if err := tt.Click("保留草稿返回"); err != nil {
			t.Fatal(err)
		}
		if a.editing || a.selectedID != "knowledge-a" || a.drafts["knowledge-a"].Text == "" {
			t.Fatal("H02: return lost the selected knowledge or draft")
		}
	}
}
