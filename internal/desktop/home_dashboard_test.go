package desktop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func TestHomeActivityOpensCurrentOwnerAndPreservesRecordType(t *testing.T) {
	for _, origin := range []string{"knowledge", "raw"} {
		t.Run(origin, func(t *testing.T) {
			a := New("test", nil)
			a.activityLoaded = true
			a.homeActivity.Loaded = true
			a.homeActivity.CheckedAt = time.Now()
			a.homeActivity.Items = []records.HomeActivity{{ID: "observed-event", Kind: "read", Channel: "daily", Agent: "pi", Memory: "memory", Dataset: "original-project", ScopeName: "原项目", CurrentDataset: "current-project", CurrentScopeKind: "project", Revision: 1, Title: "实际读取标题", Origin: origin, OccurredMS: time.Now().UnixMilli()}}
			a.homeActivity.Review.Loaded = true
			a.homeActivity.Review.CheckedAt = time.Now()
			selected := ""
			a.onSelect = func(id string) { selected = id }
			tt := ui.NewTester(a.View, 1000, 820)
			label := "pi 读取了知识 实际读取标题"
			if origin == "raw" {
				label = "pi 读取了原始记录 实际读取标题"
			}
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			if a.page != "项目" || a.scope != "current-project" || selected != "memory" || a.recordKind != origin {
				t.Fatalf("event lost identity/type: %s %s %s %s", a.page, a.scope, selected, a.recordKind)
			}
		})
	}
}

func TestHomeDateAllKnowledgeUsesGlobalSearchAndKeepsDate(t *testing.T) {
	a := New("test", nil)
	a.activityLoaded = true
	a.homeRecentLoaded = true
	a.homeDate = "2026-11-10"
	tt := ui.NewTester(a.View, 1000, 820)
	if err := tt.Click("查看全部知识"); err != nil {
		t.Fatal(err)
	}
	if a.page != "搜索" || a.activityDate != "2026-11-10" || a.recordKind != "knowledge" || a.searchQuery != "" {
		t.Fatal("date browse restored obsolete navigation or lost range")
	}
}

func TestHomeBusyOverviewKeepsProjectEntryAboveFixedFooter(t *testing.T) {
	a := New("test", nil)
	a.activityLoaded = true
	a.homeActivity.Loaded, a.homeActivity.Review.Loaded = true, true
	a.homeActivity.CheckedAt, a.homeActivity.Review.CheckedAt = time.Now(), time.Now()
	raw, knowledge := 2, 7
	a.overviewRaw, a.overviewKnowledge = &raw, &knowledge
	a.projects = []ProjectItem{{ID: "project-one", Name: "项目一"}, {ID: "project-two", Name: "项目二"}}
	a.projectOverviews = map[string]*ProjectOverview{"project-one": {Ready: true, Knowledge: 4, CheckedAt: time.Now()}, "project-two": {Ready: true, Knowledge: 3, CheckedAt: time.Now()}}
	for i := 0; i < 4; i++ {
		a.homeActivity.Items = append(a.homeActivity.Items, records.HomeActivity{ID: fmt.Sprint("event", i), Kind: "read", Channel: "daily", Agent: "pi", Memory: fmt.Sprint("memory", i), Dataset: "project-one", ScopeName: "项目一", Revision: 1, Title: fmt.Sprint("实际知识", i), Origin: "knowledge", OccurredMS: time.Now().UnixMilli()})
	}
	for i := 0; i < 3; i++ {
		a.homeActivity.Review.Items = append(a.homeActivity.Review.Items, records.HomeReview{ID: fmt.Sprint("review", i), Task: fmt.Sprint("task", i), Title: fmt.Sprint("建议", i), ScopeName: "项目一", Action: "merge"})
	}
	a.homeActivity.Total, a.homeActivity.Review.Total = 36, 14
	tt := ui.NewTester(a.View, 1200, 820)
	project, ok := tt.Find("项目中的知识：项目一")
	if !ok {
		t.Fatal("project entry missing")
	}
	footer, ok := tt.Find("管理计划")
	if !ok {
		t.Fatal("fixed footer missing")
	}
	t.Logf("project=%+v footer=%+v", project, footer)
	if project.Y+project.H > 820-42 {
		t.Fatalf("busy home hides first project behind fixed footer: project=%+v footer=%+v", project, footer)
	}
	if !tt.HasText("首次使用日期未知") {
		t.Fatal("density change hid unknown first-use semantics")
	}
}

// ConfigureLibrary installs the actual navigation/selection callbacks. A retired
// test context keeps this UI regression entirely local, with no live IPC owner.
func homeNavigationCallbacks(t *testing.T, view string) *App {
	t.Helper()
	a := New("test", nil)
	a.visualFixture = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ConfigureLibrary(ctx, t.TempDir(), func(func()) {})
	a.libraryContext = nil
	if a.onNavigate == nil || a.onSelect == nil {
		t.Fatal("production callbacks absent")
	}
	a.page = "总览"
	a.browsing.Preferences.Selections["target-project"] = BrowseSelection{View: view, Memory: "old-memory", Entity: "old-entity", Relation: "old-edge", Dataset: "old-graph-dataset", EntityListY: 72, EntityBodyY: 190, GraphY: 33, GraphBodyY: 140}
	return a
}
func TestHomeExactRecordOverridesRestoredExplorerWithLibraryCallbacks(t *testing.T) {
	for _, view := range []string{"entities", "relations"} {
		for _, entry := range []string{"dated-knowledge", "activity-knowledge", "activity-raw"} {
			t.Run(view+"/"+entry, func(t *testing.T) {
				a := homeNavigationCallbacks(t, view)
				target := MemoryItem{ID: "target-memory", Dataset: "target-project", Title: "Explicit home target", Text: "Exact target body", Origin: "knowledge", Revision: 3, State: "active"}
				kind := "knowledge"
				if entry == "activity-raw" {
					target.Origin = "raw"
					kind = "raw"
				}
				navigated, selected := false, false
				realNavigate, realSelect := a.onNavigate, a.onSelect
				a.onNavigate = func() {
					if a.browsing.View != view || a.selectedID != "old-memory" {
						t.Fatal("fixture did not exercise real project preference restoration")
					}
					navigated = true
					realNavigate()
				}
				a.onSelect = func(id string) {
					if !navigated || id != target.ID || a.selectedID != target.ID || a.browsing.View != "memories" || a.browsing.Knowledge != nil || a.browsing.Explorer != nil || a.browsing.Relation != "" {
						t.Fatal("restored explorer superseded explicit target before selection")
					}
					realSelect(id)
					a.selectedItem = &target // The corresponding read snapshot, never old graph text.
					a.items = []MemoryItem{target}
					selected = true
				}
				a.homeDate = "2026-11-11"
				a.detailScroll.Y = 250
				if entry == "dated-knowledge" {
					tt := ui.NewTester(func(c *ui.Context) { a.homeKnowledgeRow(c, target) }, 1200, 200)
					if err := tt.Click(target.Title); err != nil {
						t.Fatal(err)
					}
				} else {
					a.openHomeActivityCurrent(records.HomeActivity{Memory: target.ID, Dataset: "prior-owner", CurrentDataset: target.Dataset, CurrentScopeKind: "project", Origin: target.Origin, Revision: 1})
				}
				if !selected || a.page != "项目" || a.scope != target.Dataset || a.recordKind != kind || a.detailMode != "view" || a.editing || a.detailScroll.Y != 0 || a.activityDate != "" {
					t.Fatal("direct record identity/type or reading position lost")
				}
				for _, width := range []int{1200, 800} {
					tt := ui.NewTester(a.View, width, 720)
					if !tt.HasText(target.Title) || !tt.HasText(target.Text) || tt.HasText("项目实体") {
						t.Fatal("target selected but old explorer still rendered", width)
					}
				}
			})
		}
	}
}
func TestOrdinaryProjectNavigationStillRestoresExplorerWithLibraryCallbacks(t *testing.T) {
	for _, view := range []string{"entities", "relations"} {
		a := homeNavigationCallbacks(t, view)
		a.navigate("项目", "target-project")
		if a.browsing.View != view || a.browsing.Entity != "old-entity" || a.browsing.Relation != "old-edge" || a.selectedID != "old-memory" || a.browsing.Dataset != "old-graph-dataset" || a.browsing.EntityList.Y != 72 || a.browsing.EntityBody.Y != 190 || a.browsing.GraphScroll.Y != 33 || a.browsing.GraphBody.Y != 140 {
			t.Fatal("home-specific fix changed ordinary project restoration", view)
		}
	}
}
