package desktop

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func TestReadingFiltersPreserveRawSourceIdentityWithRenamedLabels(t *testing.T) {
	old, current := "用户：原文复制到全局", "用户：原文复制到通用知识"
	sources, labels := readingSourceOptions([]string{old, current, old}, "")
	if len(sources) != 3 || len(labels) != 3 {
		t.Fatal("directory failed to deduplicate raw identities", sources, labels)
	}
	seen := map[string]bool{}
	for i, raw := range sources {
		if seen[labels[i]] {
			t.Fatal("source option labels collide", labels)
		}
		seen[labels[i]] = true
		if strings.Contains(labels[i], "复制到全局") {
			t.Fatal("source label restored obsolete terminology")
		}
		if raw == old && labels[i] != current+" · 旧记录" {
			t.Fatal("legacy source not distinguished", labels[i])
		}
	}
	a := New("test", nil)
	a.page = "搜索"
	a.searchQuery = "snapshot"
	a.recordKind = "knowledge"
	a.applySearchResults([]records.Version{{Memory: "legacy", Dataset: "project", State: "active", Origin: "knowledge", Source: old}, {Memory: "current", Dataset: "project", State: "active", Origin: "knowledge", Source: current}})
	if a.searchHits[0].RawSource != old || a.searchHits[0].Source != current {
		t.Fatal("display conversion destroyed provenance key")
	}
	for _, tc := range []struct{ source, id string }{{old, "legacy"}, {current, "current"}} {
		a.readingFilters.Source = tc.source
		a.applySearchKind()
		if len(a.items) != 1 || a.items[0].ID != tc.id {
			t.Fatal("source alias merged independent records", a.items)
		}
	}
	_, collisionLabels := readingSourceOptions([]string{old, current, current + " · 旧记录"}, "")
	unique := map[string]bool{}
	for _, label := range collisionLabels {
		if unique[label] {
			t.Fatal("custom source collided with legacy marker", collisionLabels)
		}
		unique[label] = true
	}
}

func TestReadingFiltersUseCurrentVersionTimeAndOnlySubmittedSearchSnapshot(t *testing.T) {
	a := New("test", nil)
	a.page = "搜索"
	a.searchQuery = "confirmed query"
	a.query = a.searchQuery
	a.recordKind = "knowledge"
	a.scopes = []records.Scope{{ID: "project", Kind: "project"}, {ID: "preference", Kind: "preference"}}
	a.applySearchResults([]records.Version{
		{Memory: "updated", Dataset: "project", Source: "pi", Origin: "knowledge", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-11-11T08:00:00Z"},
		{Memory: "old", Dataset: "project", Source: "pi", Origin: "knowledge", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Memory: "preference", Dataset: "preference", Source: "pi", Origin: "knowledge", UpdatedAt: "2026-11-11T08:00:00Z"},
		{Memory: "raw", Dataset: "project", Source: "capture", Origin: "raw", UpdatedAt: "2026-11-11T08:00:00Z"},
	})
	a.readingFilters = ReadingFilterUI{Open: true, Scope: "project", Source: "pi", Category: "project", Time: "今天", SinceMS: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC).UnixMilli()}
	a.nextCursor = "outdated-page"
	a.readingFiltersChanged()
	if len(a.items) != 1 || a.items[0].ID != "updated" || len(a.searchHits) != 4 || a.searchQuery != "confirmed query" || a.nextCursor != "" || *a.readingKnowledgeTotal != 3 || *a.readingRawTotal != 1 {
		t.Fatal("snapshot filters changed query/type totals or used original creation time", a.items)
	}
	tt := ui.NewTester(a.View, 800, 720)
	if !tt.HasText("筛选本次搜索结果（最多 30 条）") {
		t.Fatal("snapshot-only search filtering scope hidden")
	}
	a.readingFilters.Source = "absent"
	a.readingFiltersChanged()
	if len(a.items) != 0 || a.projectKnowledgeEmpty() {
		t.Fatal("empty ranked filter became project initialization")
	}
}

func TestReadingFiltersReturnRestoresScopeSourceTimeAndPaginationIdentity(t *testing.T) {
	a := New("test", nil)
	a.visualFixture = true
	a.navigate("项目", "alpha")
	first := ReadingFilterUI{Open: true, Source: "pi", Category: "project", Time: "今天", SinceMS: 1234, Scroll: ui.ScrollState{Y: 90}}
	a.readingFilters = first
	a.navigate("项目", "beta")
	if a.readingFilters.active() {
		t.Fatal("new project inherited filters")
	}
	a.readingFilters = ReadingFilterUI{Source: "codex", Time: "最近 7 天", SinceMS: 5678}
	a.navigate("搜索", "")
	global := ReadingFilterUI{Open: true, Scope: "alpha", Source: "pi", Category: "project", Time: "最近 30 天", SinceMS: 9876}
	a.readingFilters = global
	a.nextCursor = "old-global-page"
	a.navigate("项目", "alpha")
	if !reflect.DeepEqual(a.readingFilters, first) || a.nextCursor != "" {
		t.Fatal("project return lost own filters or reused cursor", a.readingFilters)
	}
	q := a.listQuery()
	if q.Source != "pi" || q.ScopeKind != "project" || q.UpdatedSinceMS != 1234 || !reflect.DeepEqual(q.Datasets, []string{"alpha"}) {
		t.Fatal("restored filters not sent to list", q)
	}
	a.navigate("搜索", "")
	if !reflect.DeepEqual(a.readingFilters, global) || !reflect.DeepEqual(a.listQuery().Datasets, []string{"alpha"}) {
		t.Fatal("global scope filter did not survive project reading")
	}
	a.navigate("项目", "beta")
	if a.readingFilters.Source != "codex" || a.readingFilters.SinceMS != 5678 {
		t.Fatal("Beta filters replaced by Alpha or search")
	}
}

func TestReadingFiltersEmptyMatchDoesNotOfferProjectOrganization(t *testing.T) {
	for _, filter := range []ReadingFilterUI{{Source: "missing"}, {Scope: "project"}, {Category: "project"}, {SinceMS: 1}} {
		a := New("test", nil)
		a.visualFixture = true
		a.page, a.scope = "项目", "project"
		a.readingCountsRoute = "项目:project"
		knowledge, raw := 0, 2
		a.readingKnowledgeTotal, a.readingRawTotal = &knowledge, &raw
		a.readingFilters = filter
		if a.projectKnowledgeEmpty() || a.projectKnowledgePreviewVisible() {
			t.Fatal("filter zero was treated as true empty project", filter)
		}
		tt := ui.NewTester(a.View, 800, 720)
		if _, shown := tt.Find("整理项目"); shown {
			t.Fatal("filter zero showed organization main action")
		}
	}
}

func TestReadingFiltersNarrowScrollablePanelLeavesListUsable(t *testing.T) {
	for _, height := range []int{720, 620} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			a := New("test", nil)
			a.visualFixture = true
			a.page, a.scope = "项目", "project"
			a.listSort = "最近更新"
			a.projects = []ProjectItem{{ID: "project", Name: "合成项目"}}
			a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "合成项目"}}
			a.readingCountsRoute = "项目:project"
			knowledge, raw := 1, 0
			a.readingKnowledgeTotal, a.readingRawTotal = &knowledge, &raw
			item := MemoryItem{ID: "item", Dataset: "project", Title: "筛选后仍可阅读的知识", Text: "Synthetic narrow reading item", Origin: "knowledge", State: "active", Source: "pi", RawSource: "pi", Revision: 1}
			a.items = []MemoryItem{item}
			a.listTotal = 1
			a.selectedID = item.ID
			a.selectedItem = &item
			a.readingFilters.Open = true
			a.readingSourcesRoute = "项目:project"
			a.readingSources = []string{"pi", "codex"}
			tt := ui.NewTester(a.View, 800, height)
			if !tt.HasText("1 条知识") {
				t.Fatal("fixture or visible filtered total disagrees with list")
			}
			listTitle, ok := tt.Find(item.Title)
			if !ok || listTitle.X >= 428 || listTitle.Y+listTitle.H > float32(height) || height < 720 && a.readingFilters.Scroll.MaxY <= 0 {
				t.Fatalf("expanded panel obscured list: %+v visible=%v panelMaxY=%f", listTitle, ok, a.readingFilters.Scroll.MaxY)
			}
			writeScreenshot(t, tt, fmt.Sprintf("filters-800x%d-open.png", height))
			a.readingFilters.Scroll.Y = a.readingFilters.Scroll.MaxY
			tt.Frame()
			clear, ok := tt.Find("清空条件")
			if !ok || clear.Y+clear.H > float32(height) {
				t.Fatal("filter bottom action unreachable", clear)
			}
			listTitle, ok = tt.Find(item.Title)
			if !ok || listTitle.X >= 428 || listTitle.Y+listTitle.H > float32(height) {
				t.Fatal("scrolling panel hid list")
			}
			if err := tt.Click(item.Title); err != nil {
				t.Fatal(err)
			}
			if a.selectedID != "item" {
				t.Fatal("visible list not selectable")
			}
			writeScreenshot(t, tt, fmt.Sprintf("filters-800x%d-bottom.png", height))
		})
	}
}

func TestReadingFiltersNewLibraryRetiresOldSourceDirectory(t *testing.T) {
	a := New("test", nil)
	a.firstLaunch.Detecting = true
	a.readingSources = []string{"old-private-source"}
	a.readingSourcesRoute = "项目:old"
	a.readingSourcesTruncated = true
	a.readingFilters = ReadingFilterUI{Source: "old-private-source"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ConfigureLibrary(ctx, t.TempDir(), func(func()) {})
	if len(a.readingSources) != 0 || a.readingSourcesRoute != "" || a.readingSourcesTruncated || a.readingFilters.active() {
		t.Fatal("new library exposed stale filter metadata")
	}
}

func TestReadingFiltersDropdownsKeepScopeAndLegacySourceKeys(t *testing.T) {
	a := New("test", nil)
	a.page = "搜索"
	a.searchQuery = "submitted snapshot"
	a.recordKind = "knowledge"
	a.readingFilters.Open = true
	alpha, beta := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	a.scopes = []records.Scope{{ID: alpha, Name: "同名项目", Kind: "project"}, {ID: beta, Name: "同名项目", Kind: "project"}}
	old, current := "用户：原文复制到全局", "用户：原文复制到通用知识"
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	a.applySearchResults([]records.Version{{Memory: "old", Dataset: alpha, Source: old, Origin: "knowledge", UpdatedAt: stamp}, {Memory: "current", Dataset: alpha, Source: current, Origin: "knowledge", UpdatedAt: stamp}, {Memory: "foreign", Dataset: beta, Source: "pi", Origin: "knowledge", UpdatedAt: stamp}})
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Gap(8).Children(func() { a.readingFilterControls(c) }) }, 800, 620)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("不限范围")
	click("同名项目 · 11111111")
	if a.readingFilters.Scope != alpha || len(a.items) != 2 {
		t.Fatal("same-name project dropdown chose wrong identity")
	}
	click("全部来源")
	click(current + " · 旧记录")
	if a.readingFilters.Source != old || len(a.items) != 1 || a.items[0].ID != "old" {
		t.Fatal("source dropdown stored display name instead of raw key")
	}
	a.readingFilters.Scroll.Y = a.readingFilters.Scroll.MaxY
	tt.Frame()
	click("全部类别")
	click("项目约定")
	click("不限时间")
	click("今天")
	if a.readingFilters.Scope != alpha || a.readingFilters.Source != old || a.readingFilters.Category != "project" || a.readingFilters.Time != "今天" || a.readingFilters.SinceMS <= 0 || len(a.items) != 1 {
		t.Fatal("time/category selection lost previous scope/source identity", a.readingFilters)
	}
}

func TestReadingFiltersNewRouteDoesNotReuseOldSourceTruncationBanner(t *testing.T) {
	a := New("test", nil)
	a.page, a.scope = "项目", "new-project"
	a.readingFilters.Open = true
	a.readingSourcesRoute = "项目:old-project"
	a.readingSources = []string{"old-source"}
	a.readingSourcesTruncated = true
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.readingFilterControls(c) }) }, 800, 720)
	if tt.HasText("来源目录仅列出前 1000 种") || tt.HasText("old-source") {
		t.Fatal("new route reused previous source catalogue metadata")
	}
}
