package simpledesktop

import (
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestOverviewRefreshAutomaticAndPreservesView(t *testing.T) {
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	a := New(Config{ServiceURL: coldStartTestURL, Projects: []Project{{ID: "p", Dataset: "p"}}}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 30)
	a.Update = func(fn func()) { updates <- fn }
	v := &a.overviewState
	v.ScopeKey = a.overviewScope()
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
	a.overview.RefreshAt = now.Add(-time.Second)
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
	a.overview.RefreshAt = now.Add(-overviewRefreshInterval)
	a.SetWindowActive(false)
	a.SetWindowActive(true)
	drainReading(t, updates, func() bool { return v.Loading })
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
	if !tt.HasText("服务 · 已连接") {
		t.Fatal("connection status removed with decorative footer")
	}
}

func TestOnWindowFocusDebouncesAsyncIntegrationRefresh(t *testing.T) {
	requests := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	defer close(release)
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/generation" {
			t.Errorf("unexpected focus request %s %s", r.Method, r.URL.Path)
		}
		requests <- struct{}{}
		<-release
		w.Write([]byte("{}"))
	})
	a := New(Config{GenerationSetupOnly: true, ProjectControl: &ProjectControlConfig{
		URL: coldStartTestURL, ManagementTokenFile: p1TestTokenFile(t),
	}}, nil)
	a.Client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 8)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "设置"
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	a.focusRefreshNow = func() time.Time { return now }
	a.windowInactive = true
	a.OnWindowFocus()
	if a.windowInactive || !a.integrations.Loading || a.integrations.Request != 1 {
		t.Fatal("focus did not start an asynchronous refresh")
	}
	waitRequest := func() {
		t.Helper()
		select {
		case <-requests:
			release <- struct{}{}
		case <-time.After(3 * time.Second):
			t.Fatal("focus did not send an integration refresh")
		}
		drainReading(t, updates, func() bool { return !a.integrations.Loading })
	}
	waitRequest()
	now = now.Add(focusRefreshDebounce - time.Nanosecond)
	a.OnWindowFocus()
	if a.integrations.Request != 1 {
		t.Fatal("focus inside the debounce window refreshed")
	}
	now = now.Add(time.Nanosecond)
	a.OnWindowFocus()
	if a.integrations.Request != 2 {
		t.Fatal("focus at the debounce boundary did not refresh")
	}
	waitRequest()
}

func TestOnWindowFocusRefreshesIdleSetupEnvironment(t *testing.T) {
	a := New(Config{ServiceEnvironmentFile: t.TempDir() + "/missing.json"}, nil)
	updates := make(chan func(), 4)
	a.Update = func(fn func()) { updates <- fn }
	a.initialSetup = true
	a.environment.Busy = true
	now := time.Now()
	a.focusRefreshNow = func() time.Time { return now }
	a.OnWindowFocus()
	if a.environment.Loading {
		t.Fatal("focus interrupted the setup worker")
	}
	a.environment.Busy = false
	now = now.Add(focusRefreshDebounce)
	a.OnWindowFocus()
	if !a.environment.Loading || a.environment.Loaded {
		t.Fatal("environment read did not run asynchronously")
	}
	drainReading(t, updates, func() bool { return !a.environment.Loading })
	if !a.environment.Loaded || a.environment.Error == "" {
		t.Fatal("environment refresh did not report the fixture's missing file")
	}
}
