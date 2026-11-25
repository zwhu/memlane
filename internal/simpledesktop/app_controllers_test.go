package simpledesktop

import "testing"

func TestAppControllerInitializationAndResets(t *testing.T) {
	a := New(Config{}, nil)
	if a.searchController.searchType != "全部类型" {
		t.Fatalf("searchController.searchType = %q, want 全部类型", a.searchController.searchType)
	}
	if a.graphController.Mode != "实体关系" {
		t.Fatalf("graphController.Mode = %q, want 实体关系", a.graphController.Mode)
	}
	if a.readingController.panes == nil || a.readingController.titles == nil {
		t.Fatal("readingController panes and titles must be initialized")
	}

	// Verify searchController origin lifecycle and cancellation.
	var resolveCancelled bool
	p := a.paneFor("知识/0/知识")
	a.searchOrigin = &readingLocation{Page: "总览"}
	a.searchPane = p
	a.searchAnchor = &Item{ID: "hit"}
	a.searchResolveCancel = func() { resolveCancelled = true }
	if !a.hasSearchOriginFor(p) {
		t.Fatal("hasSearchOriginFor should match active search pane")
	}
	a.clearSearchOrigin()
	if !resolveCancelled || a.searchOrigin != nil || a.searchPane != nil || a.searchAnchor != nil || a.hasSearchOriginFor(p) {
		t.Fatal("clearSearchOrigin did not cancel and clear state")
	}

	// Verify graphController blocking & reset semantics.
	if a.codeIndexBlocked() {
		t.Fatal("fresh graphController should not block code indexing")
	}
	a.graphController.CodeBusy = true
	if !a.codeIndexBlocked() {
		t.Fatal("codeBusy should block code indexing")
	}
	a.graphController.CodeBusy, a.graphController.CodeUnknown = false, true
	if !a.codeIndexBlocked() {
		t.Fatal("codeUnknown should block code indexing")
	}
	a.graphController.CodeUnknown, a.graphController.CodeRemoteActive = false, true
	if !a.codeIndexBlocked() {
		t.Fatal("codeRemoteActive should block code indexing")
	}
	a.graphController.Selection, a.graphController.Error, a.graphController.Loading = "node-1", "err", true
	a.resetGraphView("代码结构")
	if a.graphController.Mode != "代码结构" || a.graphController.Selection != "" || a.graphController.Error != "" || a.graphController.Loading {
		t.Fatal("resetGraphView did not reset graph state")
	}

	// Verify readingController pane cancellation and recent-title cancellation.
	var paneCancelled, readCancelled, recentCancelled bool
	p.cancel = func() { paneCancelled = true }
	p.readCancel = func() { readCancelled = true }
	a.recentCancel = func() { recentCancelled = true }
	beforeReq := a.recentRequest
	a.cancelRecentTitles()
	if !recentCancelled || a.recentRequest != beforeReq+1 {
		t.Fatal("cancelRecentTitles did not cancel and advance request")
	}
	a.cancelAndResetPanes()
	if !paneCancelled || !readCancelled || len(a.panes) != 0 {
		t.Fatal("cancelAndResetPanes did not cancel and reset panes")
	}

	// Verify overviewController transitions and floor clamping.
	var recallCancelled, overviewCancelled bool
	a.overviewState.RecallCancel = func() { recallCancelled = true }
	a.overviewState.BootstrapCancel = func() { overviewCancelled = true }
	a.overviewState.Loading = true
	p2 := a.paneFor("知识/0/知识")
	p2.History = []Item{{ID: "1"}, {ID: "2"}}
	a.cancelOverviewRecall()
	if !recallCancelled || a.overviewState.RecallCancel != nil {
		t.Fatal("cancelOverviewRecall did not cancel and clear recallCancel")
	}
	a.leaveOverview(a.panes)
	if !overviewCancelled || a.overviewState.Loading || !a.fromOverview || a.overviewSourceFloor[p2] != 2 {
		t.Fatal("leaveOverview did not record pane history floor")
	}
	a.clampOverviewFloor(p2, 2, 1)
	if a.overviewSourceFloor[p2] != 1 {
		t.Fatalf("clampOverviewFloor = %d, want 1", a.overviewSourceFloor[p2])
	}
	a.resetOverviewFloor(p2)
	if a.overviewSourceFloor[p2] != 0 {
		t.Fatalf("resetOverviewFloor = %d, want 0", a.overviewSourceFloor[p2])
	}
	a.enterOverview()
	if a.fromOverview || a.overviewSourceFloor != nil {
		t.Fatal("enterOverview did not clear overview return state")
	}
}
