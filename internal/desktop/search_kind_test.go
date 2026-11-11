package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestEmptyGlobalSearchBrowsesSavedContentWithoutRankedQuery(t *testing.T) {
	listed := false
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "scopes":
			return serviceipc.Response{OK: true, Result: []records.Scope{}}
		case "list":
			listed = true
			if r.ListQuery.Date != "2026-11-11" || r.ListQuery.Kind != "knowledge" {
				return serviceipc.Response{Error: "date/type lost"}
			}
			return serviceipc.Response{OK: true, Result: records.Page{Items: []records.Version{{Memory: "saved", Title: "尚未完成索引的知识", Origin: "knowledge", State: "active"}}, Total: 1}}
		case "activities":
			return serviceipc.Response{OK: true, Result: records.ActivitySummary{}}
		default:
			return serviceipc.Response{Error: "unexpected query " + r.Op}
		}
	})
	a.libraryContext = context.Background()
	a.page, a.query, a.searchQuery, a.activityDate, a.recordKind = "搜索", "  ", "old-ranked", "2026-11-11", "knowledge"
	a.searchMemory()
	awaitProcessingDispatch(t, updates)
	if !listed || a.page != "搜索" || a.searchQuery != "" || len(a.items) != 1 || a.items[0].ID != "saved" || a.listTotal != 1 {
		t.Fatal("empty global query restored the retired route or hid saved content", a.items, a.libraryError)
	}
}

func TestSearchTabsKeepQueryAndSeparateKnowledgeFromRaw(t *testing.T) {
	a := New("test", nil)
	a.page = "搜索"
	a.query = "request_id"
	a.searchQuery = a.query
	a.applySearchResults([]records.Version{{Memory: "p", Origin: "knowledge", Text: "project", State: "active"}, {Memory: "r", Origin: "raw", Text: "raw", State: "active"}, {Memory: "g", Origin: "knowledge", Text: "common", State: "active"}})
	tt := ui.NewTester(a.View, 1200, 820)
	if len(a.items) != 2 || a.listTotal != 2 || !tt.HasText("知识 2") || !tt.HasText("原始记录 1") {
		t.Fatal("mixed search results labelled as knowledge", a.items)
	}
	a.selectedID = "p"
	if err := tt.Click("原始记录 1"); err != nil {
		t.Fatal(err)
	}
	if len(a.items) != 1 || a.items[0].ID != "r" || a.listTotal != 1 || a.query != "request_id" || a.searchQuery != "request_id" {
		t.Fatal("raw tab lost query or contains knowledge")
	}
	a.selectedID = "r"
	if err := tt.Click("知识 2"); err != nil {
		t.Fatal(err)
	}
	if len(a.items) != 2 || a.selectedID != "p" {
		t.Fatal("knowledge tab lost prior selection")
	}
	a.applySearchResults(nil)
	tt.Frame()
	if len(a.items) != 0 || a.listTotal != 0 || *a.readingKnowledgeTotal != 0 || *a.readingRawTotal != 0 {
		t.Fatal("empty result retained earlier hits")
	}
}
