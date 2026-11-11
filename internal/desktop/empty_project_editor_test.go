package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestEmptyProjectDoesNotHideManualMemoryDraft(t *testing.T) {
	a := projectMenuFrame()
	a.readingSearch, a.readingFilter = "", ""
	a.selectedID, a.selectedItem = "", nil
	zero, one := 0, 1
	a.readingCountsRoute = "项目:a"
	a.readingKnowledgeTotal, a.readingRawTotal = &zero, &one
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("让项目知识逐步积累") {
		t.Fatal("empty project preview missing")
	}
	if err := tt.Click("新增记忆"); err != nil {
		t.Fatal(err)
	}
	if !a.editing || !tt.HasText("记忆内容") || tt.HasText("让项目知识逐步积累") {
		t.Fatal("empty project hid the manual editor")
	}
	if err := tt.Click("记忆内容"); err != nil {
		t.Fatal(err)
	}
	tt.Type("合成项目的手动知识草稿")
	if a.draft != "合成项目的手动知识草稿" || a.scope != "a" || a.draftScope != "项目A" {
		t.Fatal("draft lost project ownership", a.draftScope)
	}
	a.navigate("总览", "")
	a.navigate("项目", "a")
	a.newMemory()
	tt.Frame()
	if a.draft != "合成项目的手动知识草稿" || !tt.HasText("记忆内容") {
		t.Fatal("draft was lost on return")
	}
}
