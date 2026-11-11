package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func browsingContextApp() *App {
	a := New("test", nil)
	a.page, a.scope = "项目", "alpha"
	a.browsing.Project = "alpha"
	a.browsing.Graph = records.CogneeGraph{Project: "alpha", Entities: []records.CogneeEntity{{ID: "entity", Name: "Object"}}, Mentions: []records.CogneeMention{{Entity: "entity", CogneeGraphSource: records.CogneeGraphSource{Memory: "knowledge", Revision: 2}}}}
	a.selectedID = "knowledge"
	a.selectedItem = &MemoryItem{ID: "knowledge", Dataset: "alpha", Revision: 3, Text: "Current knowledge", State: "active"}
	return a
}

func TestEntityKnowledgeSourceReturnKeepsExplorerContext(t *testing.T) {
	a := browsingContextApp()
	a.detailScroll.Y = 290
	a.browseEntity("entity")
	a.browsing.EntityList.Y, a.browsing.EntityBody.Y = 80, 420
	a.browseMemory("knowledge")
	if a.browsing.View != "memories" || a.browsing.Explorer == nil {
		t.Fatal("explorer return context missing")
	}
	item := MemoryItem{ID: "knowledge", Dataset: "alpha", Revision: 3, Text: "Current knowledge", State: "active"}
	a.selectedItem = &item
	tt := ui.NewTester(func(c *ui.Context) { a.browsingKnowledgeReturn(c, item) }, 720, 220)
	if !tt.HasText("关联材料引用 v2；这里显示当前知识 v3。原文按当前知识的出处核验。") {
		t.Fatal("old graph version was described as current")
	}
	a.detailScroll.Y = 140
	a.openSourceReader(item, records.MergeTarget{Memory: "raw", Revision: 1})
	if a.reader.Query.Owner != "knowledge" || a.reader.Query.OwnerRevision != 3 {
		t.Fatal("source reader lost current owner version")
	}
	a.returnFromSourceReader()
	if a.reader.Open || a.detailScroll.Y != 140 || a.browsing.Explorer == nil {
		t.Fatal("source return lost knowledge context")
	}
	a.returnToBrowsingExplorer()
	if a.browsing.View != "entities" || a.browsing.Entity != "entity" || a.browsing.EntityList.Y != 80 || a.browsing.EntityBody.Y != 420 {
		t.Fatal("entity position not restored")
	}
	a.returnToBrowsingKnowledge()
	if a.selectedID != "knowledge" || a.detailScroll.Y != 290 || a.browsing.View != "memories" {
		t.Fatal("original knowledge position not restored")
	}
}

func TestGraphReturnSurvivesSelectionRefresh(t *testing.T) {
	a := browsingContextApp()
	a.browsing.View, a.browsing.Entity, a.browsing.Relation = "relations", "entity", "edge"
	a.browsing.GraphScroll.Y, a.browsing.GraphBody.Y = 30, 180
	a.browseMemory("knowledge")
	a.onSelect = func(string) { a.browsing.Entity, a.browsing.Relation = "", "" }
	a.returnToBrowsingExplorer()
	if a.browsing.View != "relations" || a.browsing.Relation != "edge" || a.browsing.Entity != "entity" || a.browsing.GraphBody.Y != 180 {
		t.Fatal("refresh removed graph return context")
	}
}

func TestBrowsingProjectChangeRetiresOldReturnAndRestoresOwnPosition(t *testing.T) {
	a := browsingContextApp()
	a.browseEntity("entity")
	a.browsing.EntityList.Y, a.browsing.EntityBody.Y = 70, 190
	a.browsing.Search = "Object"
	a.browsing.Dataset = "alpha-dataset"
	a.browsing.GraphScroll.X = 25
	a.saveBrowsing()
	a.scope = "beta"
	a.browseProjectChanged()
	if a.browsing.Knowledge != nil || a.browsing.Explorer != nil || a.browsing.Entity != "" || a.browsing.EntityBody.Y != 0 {
		t.Fatal("old project return or position leaked")
	}
	a.browsing.Graph = records.CogneeGraph{Project: "alpha"}
	a.browsing.View = "entities"
	a.selectedID = "beta-knowledge"
	a.browseMemory("knowledge")
	if a.selectedID != "beta-knowledge" || a.browsing.Error == "" {
		t.Fatal("stale graph navigated another project")
	}
	a.scope = "alpha"
	a.browseProjectChanged()
	if a.browsing.View != "entities" || a.browsing.Entity != "entity" || a.browsing.EntityBody.Y != 190 || a.browsing.EntityList.Y != 70 || a.browsing.Search != "Object" || a.browsing.Dataset != "alpha-dataset" || a.browsing.GraphScroll.X != 25 {
		t.Fatal("project explorer position did not restore")
	}
}

func TestContentNavigationControlsFitOrdinaryAndNarrowWindows(t *testing.T) {
	for _, width := range []int{1280, 800} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := browsingContextApp()
			a.visualFixture = true
			a.projects = []ProjectItem{{ID: "alpha", Name: "测试项目"}}
			a.selectedItem.Title = "保持项目上下文的知识"
			a.items = []MemoryItem{*a.selectedItem}
			a.drafts[a.selectedID] = Draft{Title: "未保存标题", Text: "未保存正文", Revision: 3}
			tt := ui.NewTester(a.View, width, 720)
			within := func(label string) {
				t.Helper()
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > 721 {
					t.Fatalf("control %q outside %dpx window: %+v found=%v", label, width, r, ok)
				}
			}
			within("继续编辑")
			writeScreenshot(t, tt, fmt.Sprintf("content-%d-draft.png", width))
			a.browseEntity("entity")
			tt.Frame()
			within("返回知识")
			within("在记忆视图打开")
			writeScreenshot(t, tt, fmt.Sprintf("content-%d-entity.png", width))
			if err := tt.Click("在记忆视图打开"); err != nil {
				t.Fatal(err)
			}
			a.selectedItem = &a.items[0]
			tt.Frame()
			within("返回实体")
			writeScreenshot(t, tt, fmt.Sprintf("content-%d-associated-knowledge.png", width))
		})
	}
}
