package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestNewFromRankedSearchReturnsToSearchWithRealNavigationCallbacks(t *testing.T) {
	a := homeNavigationCallbacks(t, "entities")
	a.manualDrafts.Loaded = true
	navigations := 0
	realNavigate := a.onNavigate
	a.onNavigate = func() { navigations++; realNavigate() }
	a.query = "HarborCourier"
	a.searchQuery = "HarborCourier"
	a.navigate("搜索", "")
	a.recordKind = "raw"
	a.readingSearch = "正文筛选"
	a.readingFilter = "全部"
	a.activityDate = "2026-11-11"
	a.detailScroll.Y = 123
	owner := MemoryItem{ID: "search-owner", Dataset: "project-a", Revision: 1, Title: "HarborCourier", Text: "synthetic search record", Origin: "knowledge"}
	a.items = []MemoryItem{owner}
	a.selectedID = owner.ID
	a.selectedItem = &owner
	a.detailMode = "view"
	if navigations == 0 || a.page != "搜索" {
		t.Fatal("fixture missed real navigation callback")
	}
	a.newMemory()
	if !a.editing || a.detailMode != "new" {
		t.Fatal("fixture did not enter new editor")
	}
	if a.page != "搜索" {
		t.Errorf("new from search restored forbidden route %q; query=%q submitted=%q", a.page, a.query, a.searchQuery)
	}
	a.draftTitle = "未保存合成草稿"
	a.draft = "保留草稿，不写服务"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Grow(1).Children(func() {
			a.memoryEditorReturn(c)
			a.editor(c)
		})
	}, 800, 640)
	if e := tt.Click("保留草稿返回"); e != nil {
		t.Fatal(e)
	}
	if a.page != "搜索" || a.query != "HarborCourier" || a.searchQuery != "HarborCourier" {
		t.Errorf("return lost ranked-search owner page=%q query=%q", a.page, a.query)
	}
	if a.recordKind != "raw" || a.activityDate != "2026-11-11" || a.readingSearch != "正文筛选" || a.readingFilter != "全部" || a.detailScroll.Y != 123 {
		t.Fatal("return changed reading filters/date/type/scroll")
	}
	if a.editing || a.detailMode != "view" {
		t.Fatal("return did not close editor")
	}
	if a.selectedID != owner.ID || a.drafts[a.newDraftKey()].Text != "保留草稿，不写服务" {
		t.Fatal("selection or unsaved draft lost")
	}
}

func TestNewMemoryKeepsLegalScopeAndHomeUsesSearchCallback(t *testing.T) {
	for _, page := range []string{"搜索", "项目", "通用知识", "总览"} {
		t.Run(page, func(t *testing.T) {
			a := homeNavigationCallbacks(t, "relations")
			a.manualDrafts.Loaded = true
			scope := "target-project"
			if page == "通用知识" || page == "总览" {
				scope = ""
			}
			a.navigate(page, scope)
			a.recordKind = "raw"
			a.activityDate = "2026-11-11"
			a.detailScroll.Y = 77
			a.query = "HarborCourier"
			a.searchQuery = a.query
			calls := 0
			real := a.onNavigate
			a.onNavigate = func() { calls++; real() }
			a.newMemory()
			expected := page
			if page == "总览" {
				expected = "搜索"
				if calls != 1 {
					t.Fatal("home did not navigate using production callback")
				}
			} else if calls != 0 {
				t.Fatal("existing reading route unnecessarily refreshed")
			}
			if a.page != expected || a.scope != scope || !a.editing || a.detailMode != "new" {
				t.Fatal("draft replaced legal scope/route")
			}
			if page != "总览" && (a.recordKind != "raw" || a.activityDate != "2026-11-11" || a.detailScroll.Y != 77) {
				t.Fatal("legal reading context changed")
			}
			if a.query != "HarborCourier" || a.searchQuery != a.query {
				t.Fatal("query lost")
			}
		})
	}
}
