package simpledesktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func recallSortFixture() (*App, *pane) {
	a := New(Config{Projects: []Project{{ID: "project", Name: "项目", Dataset: "dataset"}}}, nil)
	a.page, a.kind = "知识", "知识"
	s := a.state()
	s.Loaded = true
	s.Knowledge.Initialized = true
	s.Knowledge.CountsLoaded = true
	for n := 0; n < 45; n++ {
		i := Item{ID: fmt.Sprint(n), Dataset: "dataset", Kind: "知识", Date: fmt.Sprintf("2026-11-%02d", 27-n%26), Body: fmt.Sprintf("记忆正文 %d", n), BodyLoaded: true, ReadingState: "ready", RecallStats: &KnowledgeRecallStats{Count: 0, StartedAt: "2026-11-01"}}
		if n == 44 {
			i.RecallStats.Count = 9
		}
		s.Knowledge.Catalog = append(s.Knowledge.Catalog, i)
	}
	a.reorderKnowledge(s)
	return a, s
}

func TestRecallSortIncludesUndisplayedCatalogAndStableTimeTies(t *testing.T) {
	a, s := recallSortFixture()
	if len(s.Items) != knowledgeBatch {
		t.Fatal("fixture did not paginate")
	}
	s.Knowledge.SortByRecalls = true
	a.reorderKnowledge(s)
	if s.Items[0].ID != "44" || !s.More {
		t.Fatal("sorted only displayed batch")
	}
	s.ListScroll.Y = 120
	s.Knowledge.SortByRecalls = false
	a.reorderKnowledge(s)
	if s.ListScroll.Y != 0 || s.Items[0].Date != "2026-11-27" {
		t.Fatal("time order or scroll reset failed")
	}
	s.Knowledge.RecallFilter = map[string]bool{"44": true, "40": true}
	a.reorderKnowledge(s)
	if len(s.Items) != 2 || s.More {
		t.Fatal("recall event filter leaked unrelated memories")
	}
}

func TestRecallActivityFiltersExactReceiptsWithoutRunningRetrieval(t *testing.T) {
	a, s := recallSortFixture()
	a.page = "总览"
	event := RecallEvent{ID: "event", AgentID: "codex", Hits: []RecallHit{{"dataset", "44"}, {"dataset", "40"}}}
	tt := ui.NewTester(func(c *ui.Context) { a.recallActivityAction(c, event) }, 400, 100)
	clickProjectUI(t, tt, "查看 2 条记忆")
	if a.page != "知识" || len(s.Items) != 2 || !s.Knowledge.RecallFilter["44"] {
		t.Fatal("activity failed to locate exact cards")
	}
	for _, i := range s.Items {
		if i.ID != "44" && i.ID != "40" {
			t.Fatal("unrelated card present")
		}
	}
	a.navigate("知识", 0)
	if len(s.Knowledge.RecallFilter) != 0 {
		t.Fatal("temporary recall filter survived normal navigation")
	}
}

func TestRecallActivityReplacesGenericSuccessWithExactReceiptIDs(t *testing.T) {
	rows := []overviewEvent{{Key: "raw", State: "已完成", Record: activityRow{Operation: "recall"}}, {Key: "failure", State: "失败", Record: activityRow{Operation: "recall"}}}
	receipts := []RecallEvent{{ID: "one", AgentID: "a", AgentKind: "codex", Channel: "automatic", Received: "2026-11-18T01:00:00Z", Hits: []RecallHit{{"dataset", "44"}}}, {ID: "one", AgentID: "b", AgentKind: "pi", Received: "2026-11-18T01:00:00Z", Hits: []RecallHit{{"dataset", "40"}}}}
	merged, _ := mergeRecallActivity(rows, receipts, []Project{{Name: "项目", Dataset: "dataset"}}, 50)
	if len(merged) != 3 {
		t.Fatal("duplicated raw successful calls or dropped failed calls")
	}
	keys := map[string]bool{}
	for _, row := range merged {
		if keys[row.Key] {
			t.Fatal("receipt identities merged")
		}
		keys[row.Key] = true
	}
	if keys["raw"] {
		t.Fatal("request success counted as a delivered recall")
	}
}

func TestKnowledgeSortAndRecallBadgeUI(t *testing.T) {
	a, s := recallSortFixture()
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.knowledgePage(c, s) }) }, 1200, 820)
	clickProjectUI(t, tt, "按召回次数")
	if s.Items[0].ID != "44" || !tt.HasText("召回 9 次") {
		t.Fatal("sort selection did not reorder actual cards")
	}
	fixtureImage(t, tt, "recall-sorted-cards")
}
