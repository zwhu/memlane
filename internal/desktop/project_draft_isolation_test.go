package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestNewDraftKeepsProjectAndScopeOnReturn(t *testing.T) {
	a := New("test", nil)
	a.projects = []ProjectItem{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta"}}
	a.navigate("项目", "alpha")
	a.newMemory()
	a.draftTitle, a.draft = "Alpha decision", "Only Alpha may inherit this text"
	a.navigate("项目", "beta")
	a.newMemory()
	if a.draft != "" || a.draftTitle != "" || a.draftScope != "Beta" {
		t.Fatalf("another project inherited draft: title=%q text=%q scope=%q", a.draftTitle, a.draft, a.draftScope)
	}
	a.draftTitle, a.draft = "Beta decision", "Only Beta"
	a.navigate("项目", "alpha")
	if !a.editing || a.detailMode != "new" || a.draftTitle != "Alpha decision" || a.draft != "Only Alpha may inherit this text" || a.draftScope != "Alpha" {
		t.Fatalf("Alpha draft was not restored: editing=%v mode=%s title=%q text=%q scope=%q", a.editing, a.detailMode, a.draftTitle, a.draft, a.draftScope)
	}
	a.navigate("项目", "beta")
	if a.draft != "Only Beta" || a.draftScope != "Beta" {
		t.Fatalf("Beta draft lost: %+v", a.drafts)
	}
}

func TestGlobalDraftDoesNotEnterProject(t *testing.T) {
	a := New("test", nil)
	a.newMemory()
	a.draft = "Cross-project reference"
	globalKey := a.newDraftKey()
	a.navigate("项目", "alpha")
	a.newMemory()
	if a.draft != "" || a.newDraftKey() == globalKey {
		t.Fatal("global draft leaked into project")
	}
	a.navigate("通用知识", "")
	a.newMemory()
	if a.draft != "Cross-project reference" {
		t.Fatal("global draft failed to survive route return")
	}
}

func TestReadingRouteRestoresKindAndBodyPosition(t *testing.T) {
	a := New("test", nil)
	a.navigate("项目", "alpha")
	a.recordKind, a.selectedID, a.detailScroll.Y = "raw", "alpha-raw", 340
	a.navigate("项目", "beta")
	if a.recordKind == "raw" || a.selectedID != "" || a.detailScroll.Y != 0 {
		t.Fatal("new project inherited old content type, selection or scroll")
	}
	a.selectedID, a.detailScroll.Y = "beta-knowledge", 120
	a.navigate("项目", "alpha")
	if a.recordKind != "raw" || a.selectedID != "alpha-raw" || a.detailScroll.Y != 340 {
		t.Fatal("return did not restore Alpha reading context")
	}
	a.navigate("项目", "beta")
	if a.recordKind == "raw" || a.selectedID != "beta-knowledge" || a.detailScroll.Y != 120 {
		t.Fatal("return did not restore Beta reading context")
	}
}

func TestKnowledgeDraftNoticeResumesWithoutChangingEffectiveKnowledge(t *testing.T) {
	a := New("test", nil)
	item := MemoryItem{ID: "knowledge", Title: "Saved title", Text: "Saved content", State: "active", Revision: 3}
	a.selectedID, a.selectedItem = item.ID, &item
	a.drafts[item.ID] = Draft{Title: "Draft title", Text: "Unsaved content", Revision: 2}
	tt := ui.NewTester(func(c *ui.Context) { a.knowledgeDraftNotice(c, item) }, 640, 300)
	if !tt.HasText("有未保存草稿") {
		t.Fatal("draft return path hidden")
	}
	if err := tt.Click("继续编辑"); err != nil {
		t.Fatal(err)
	}
	if !a.editing || a.draft != "Unsaved content" || a.drafts[item.ID].Revision != 2 || a.current().Text != "Saved content" {
		t.Fatal("resuming draft changed its version base or effective knowledge")
	}
}
