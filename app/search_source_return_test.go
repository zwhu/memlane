package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func searchSourceFixture() (*App, *pane) {
	a := projectUIFixture()
	a.kind = "知识"
	a.readingIdentity, _ = readingIdentity(a.Client)
	s := a.state()
	knowledge := *s.Selected
	a.searchUI.Query, a.searchUI.Type = "development", "知识"
	a.searchUI.Rows, a.searchUI.Loaded = []Item{knowledge}, true
	a.searchUI.Scroll.Y = 123
	a.searchUI.Submitted = a.searchSignature()
	a.openSearchResult(knowledge)
	origin := a.state()
	session := Session{ID: "s", Dataset: "ds", User: "owner"}
	a.openSourceRecord(origin, knowledge, Item{ID: session.ID, Dataset: session.Dataset, Kind: "会话", Session: &session}, &SessionHistoryPage{})
	return a, origin
}

func TestSourceRecordReturnRestoresSearchControlsAndQuery(t *testing.T) {
	a, origin := searchSourceFixture()
	a.returnFromSource(a.state())
	a.closeKnowledgeDetail(a.state())
	tt := ui.NewTester(func(c *ui.Context) { a.projectHeader(c) }, 1200, 820)
	if a.state() != origin || !tt.HasText("返回搜索") {
		t.Fatal("source return removed the search return control")
	}
	clickProjectUI(t, tt, "返回搜索")
	if !a.searchUI.Open || a.searchUI.Query != "development" || a.searchUI.Type != "知识" || a.searchUI.Scroll.Y != 123 || len(a.searchUI.Rows) != 1 {
		t.Fatal("return lost the same search query, filter, position or results")
	}
}

func TestSourceSearchContextDoesNotSurviveIdentityChangeOrOrdinaryNavigation(t *testing.T) {
	a, _ := searchSourceFixture()
	location := a.state().SourceReturn
	a.Client.Config.ServiceURL = "http://127.0.0.1:1"
	a.restoreReadingLocation(location)
	if a.searchOrigin != nil || a.searchPane != nil || a.searchAnchor != nil {
		t.Fatal("source return restored a search from the previous identity")
	}
	a, _ = searchSourceFixture()
	a.returnFromSource(a.state())
	a.navigate("知识", 1)
	if a.searchOrigin != nil || a.searchPane != nil || a.searchAnchor != nil {
		t.Fatal("ordinary navigation retained the previous search context")
	}
}
