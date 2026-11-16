package simpledesktop

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestOverviewSearchHistoryOnlyRecordsEditedDialogInput(t *testing.T) {
	a := overviewFixture(t)
	a.searchUI.Query = "agent search must not appear"
	a.rememberManualSearch()
	rows, err := a.Client.overviewSearchHistory(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("untyped query recorded: %+v %v", rows, err)
	}
	for _, query := range []string{"first search", "second search", "first search"} {
		a.searchUI.Query, a.searchUI.HistoryDirty = query, true
		a.rememberManualSearch()
	}
	reopened := New(a.Config, nil)
	rows, err = reopened.Client.overviewSearchHistory(context.Background())
	if err != nil || len(rows) != 2 || rows[0].Title != "first search" || rows[1].Title != "second search" || rows[0].At.IsZero() {
		t.Fatalf("persisted history incorrect: %+v %v", rows, err)
	}
	reopened.searchUI.Query, reopened.searchUI.HistoryDirty = "  ", true
	reopened.rememberManualSearch()
	rows, err = reopened.Client.overviewSearchHistory(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatal("empty query recorded")
	}
	other := reopened.Client
	other.Config.ServiceURL = "http://another-instance"
	rows, err = other.overviewSearchHistory(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatal("history crossed service identities")
	}
}

func TestOverviewSearchHistoryRenderingAndExpansion(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {1440, 900}} {
		a := overviewFixture(t)
		v := &a.overviewState
		v.SearchHistory, v.HistoryLoaded = true, true
		for _, title := range []string{"question one", "question two", "question three", "older fourth question"} {
			v.History = append(v.History, overviewEvent{Key: title, Title: title, Project: "全局", State: "搜索记录", At: time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local)})
		}
		tt := ui.NewTester(a.overviewShell, size[0], size[1])
		v.Scroll.Y = 120
		tt.Frame()
		if !tt.HasText("最近搜索") || !tt.HasText("question one") || !tt.HasText("older fourth question") || !tt.HasText("2026/01/02 03:04") {
			t.Fatal("history type, questions, or historical date missing")
		}
		if tt.HasText("查看搜索历史") {
			t.Fatal("obsolete expansion visible")
		}
		fixtureImage(t, tt, "search-history")
	}
}

func TestOverviewHistoryEmptyStateStaysInsidePanel(t *testing.T) {
	a := overviewFixture(t)
	a.overviewState.SearchHistory = true
	a.overviewState.HistoryLoaded = true
	tt := ui.NewTester(a.overviewShell, 1200, 820)
	tt.Frame()
	panel, ok := tt.Find("近期动态内容")
	if !ok {
		t.Fatal("missing activity panel")
	}
	message, ok := tt.Find("暂无搜索历史")
	if !ok {
		t.Fatal("missing empty state")
	}
	if message.Y < panel.Y || message.Y+message.H > panel.Y+panel.H {
		t.Fatalf("empty message outside panel: message=%+v panel=%+v", message, panel)
	}
	delta := message.Y + message.H/2 - (panel.Y + panel.H/2)
	if delta < -35 || delta > 35 {
		t.Fatalf("empty message not centered in panel: delta=%v", delta)
	}
}

func TestOverviewSearchHistoryDiscardsUnsubmittedTypingOnClose(t *testing.T) {
	a := overviewFixture(t)
	a.searchUI.Open, a.searchUI.Focus, a.searchUI.Loaded = true, true, true
	tt := ui.NewTester(a.searchDialog, 1200, 820)
	tt.Frame()
	tt.Type("manual dialog question")
	if err := tt.Click("关闭搜索"); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Client.overviewSearchHistory(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("unsubmitted dialog input was persisted: %+v %v", rows, err)
	}
}
