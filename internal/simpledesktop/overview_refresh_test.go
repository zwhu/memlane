package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOverviewRefreshAutomaticAndPreservesView(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "p"}}}, nil)
	updates := make(chan func(), 30)
	a.Update = func(fn func()) { updates <- fn }
	v := &a.overviewState
	v.scope = a.overviewScope()
	v.Loaded = true
	v.RawLoaded = true
	v.ActivityLoaded = true
	v.Day = "2026-11-13"
	v.Offset = 3
	v.Scroll.Y = 81
	v.ActivityScroll.Y = 29
	v.ActivityExpanded = true
	v.ExpandedEvent = "kept"
	v.Snapshot.Raw = 17
	now := time.Now()
	a.overviewRefreshAt = now.Add(-time.Second)
	a.windowInactive = true
	if a.refreshOverviewIfDue(now) {
		t.Fatal("background window polled")
	}
	a.windowInactive = false
	a.searchUI.Open = true
	if a.refreshOverviewIfDue(now) {
		t.Fatal("search overlay triggered refresh")
	}
	a.searchUI.Open = false
	if !a.refreshOverviewIfDue(now) || a.refreshOverviewIfDue(now) {
		t.Fatal("due refresh missing or overlapped")
	}
	drainReading(t, updates, func() bool { return !v.Loading && !v.ActivityLoading })
	if v.Day != "2026-11-13" || v.Offset != 3 || v.Scroll.Y != 81 || v.ActivityScroll.Y != 29 || !v.ActivityExpanded || v.ExpandedEvent != "kept" || v.Snapshot.Raw != 17 {
		t.Fatal("automatic refresh lost valid content or reading state")
	}
	if a.refreshOverviewIfDue(time.Now()) {
		t.Fatal("failure ignored poll interval")
	}
	a.overviewRefreshAt = now.Add(-overviewRefreshInterval)
	a.SetWindowActive(false)
	a.SetWindowActive(true)
	if !v.Loading {
		t.Fatal("foreground did not refresh stale homepage")
	}
	drainReading(t, updates, func() bool { return !v.Loading && !v.ActivityLoading })
}
func TestOverviewHasNoRoutineRefreshOrDecorativeFooter(t *testing.T) {
	a := overviewFixture(t)
	tt := ui.NewTester(a.overviewShell, 1200, 820)
	if tt.HasText("刷新总览") || tt.HasText("刷新动态") || tt.HasText("Cognee 保存与处理") {
		t.Fatal("manual update or decorative footer remains")
	}
	if !tt.HasText("本机服务 · 已连接") {
		t.Fatal("connection status removed with decorative footer")
	}
}
