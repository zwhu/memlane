package simpledesktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func perfFixture(t *testing.T) *App {
	t.Helper()
	a := overviewFixture(t)
	tokenFile := filepath.Join(t.TempDir(), "official.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("k", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a.Config.TokenFile = tokenFile
	a.Client.Config.TokenFile = tokenFile
	a.syncReadingIdentity()
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.access.Queues = map[string]*projectQueueState{}
	baseIdentity := a.cachedBaseReadingIdentity()
	a.access.Directory.Projects = make([]AccessProject, 0, len(a.Config.Projects))
	for _, p := range a.Config.Projects {
		ap := AccessProject{ID: p.ID, Name: p.Name, Dataset: p.Dataset}
		a.access.Directory.Projects = append(a.access.Directory.Projects, ap)
		a.access.Queues[baseIdentity+"/"+p.ID+"/"+p.Dataset] = &projectQueueState{
			Loaded:           true,
			Next:             time.Now().Add(time.Hour),
			localStateLoaded: true,
		}
	}
	now := time.Now()
	items := make([]Item, 0, 200)
	for n := 0; n < 200; n++ {
		day := now.AddDate(0, 0, -(n % 90)).Format("2006-01-02")
		d := Document{
			ID:      fmt.Sprintf("perf-%d", n),
			Dataset: a.Config.Projects[0].Dataset,
			Created: day + "T08:00:00Z",
			Metadata: map[string]any{
				"node_set":     []any{"session_learnings"},
				"lesson_type":  "code_location",
				"session_date": day,
			},
		}
		body := fmt.Sprintf("`internal/simpledesktop/perf_%d.go` 与 `engine/python/module_%d.py` 在 **批量预读** 时保持行高稳定。", n, n)
		items = append(items, Item{
			ID:           d.ID,
			Dataset:      d.Dataset,
			Kind:         "知识",
			Title:        body,
			Body:         body,
			BodyLoaded:   true,
			ReadingState: "ready",
			Date:         day,
			Document:     &d,
			RecallStats:  &KnowledgeRecallStats{Count: int64(n % 12)},
		})
	}
	a.overviewState.Loaded = true
	a.overviewState.RawLoaded = true
	a.overviewState.Snapshot = overviewSnapshot{Knowledge: items, Raw: 83}
	a.overviewState.ActivityLoaded = true
	a.overviewState.Events = make([]overviewEvent, 0, 30)
	for n := 0; n < 30; n++ {
		a.overviewState.Events = append(a.overviewState.Events, overviewEvent{
			Key:     fmt.Sprintf("ev-%d", n),
			Title:   "Gemini App 自动召回",
			Project: a.Config.Projects[0].Name,
			State:   "已完成",
			At:      now.Add(-time.Duration(n) * time.Minute),
		})
	}
	a.overviewState.HistoryLoaded = true
	a.overviewState.History = []overviewEvent{
		{Key: "search-1", Title: "roadmap", Project: "手动搜索", State: "搜索记录", At: now},
	}
	a.navigate("知识", 0)
	s := a.state()
	s.Loaded = true
	s.Knowledge.Initialized = true
	s.Knowledge.CountsLoaded = true
	s.Knowledge.Catalog = items
	a.reorderKnowledge(s)
	s.Selected = &s.Items[0]
	a.navigate("总览", 0)
	return a
}

// TestPerformanceBudgetsFrameTimeAndHeapStability enforces runtime UI frame
// time (< 16ms median at 1200x820) and bounded heap growth across rapid page
// and appearance switches.
func TestPerformanceBudgetsFrameTimeAndHeapStability(t *testing.T) {
	a := perfFixture(t)
	tt := ui.NewTester(a.View, 1200, 820)
	// Warm up font atlas and layout caches.
	for n := 0; n < 4; n++ {
		tt.Frame()
	}

	measureMedian := func(label string, frames int) time.Duration {
		samples := make([]time.Duration, 0, frames)
		for n := 0; n < frames; n++ {
			start := time.Now()
			tt.Frame()
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		median := samples[len(samples)/2]
		if median > 16*time.Millisecond {
			t.Errorf("%s median frame build %v exceeds 16ms (60fps) budget", label, median)
		}
		return median
	}

	measureMedian("overview", 15)

	a.navigate("知识", 0)
	tt.Frame()
	measureMedian("project-three-pane", 15)

	a.overviewState.SearchHistory = true
	a.navigate("总览", 0)
	tt.Frame()
	measureMedian("overview-search-history", 10)

	// Rapid page + appearance switch stress: heap must not leak unboundedly.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for n := 0; n < 20; n++ {
		if n%2 == 0 {
			a.setAppearancePreference(appearanceDark)
			a.navigate("知识", 0)
		} else {
			a.setAppearancePreference(appearanceLight)
			a.navigate("总览", 0)
		}
		tt.Frame()
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	var delta int64
	if after.HeapAlloc > before.HeapAlloc {
		delta = int64(after.HeapAlloc - before.HeapAlloc)
	}
	const maxHeapGrowth = 25 * 1024 * 1024
	if delta > maxHeapGrowth {
		t.Fatalf("heap grew by %.1f MiB across 20 page/theme switches (budget <= 25 MiB)", float64(delta)/(1024*1024))
	}
}

// TestColdStartOverviewPerformanceBudget enforces the cold-start Overview
// latency & frame-time budget at realistic daily scale (467 memories, 86
// sessions, 50 activities) via the single-call /v1/overview/bootstrap path.
func TestColdStartOverviewPerformanceBudget(t *testing.T) {
	now := time.Now()
	const (
		totalKnowledge = 467
		totalSessions  = 86
		totalRuns      = 50
	)
	docs := make([]Document, 0, totalKnowledge)
	previews := make(map[string]string, 30)
	recallCounts := make(map[string]*KnowledgeRecallStats, 30)
	for n := 0; n < totalKnowledge; n++ {
		day := now.AddDate(0, 0, -(n / 5)).Format("2006-01-02")
		id := fmt.Sprintf("cold-%04d", n)
		docs = append(docs, Document{
			ID:      id,
			Dataset: "ds-main",
			Name:    fmt.Sprintf("text_%032x", n),
			Created: day + "T08:00:00Z",
			Metadata: map[string]any{
				"node_set":          []any{"session_learnings"},
				"lesson_type":       "code_location",
				"first_source_date": day,
			},
		})
		if n < 30 {
			previews["ds-main/"+id] = fmt.Sprintf("`internal/simpledesktop/overview_bootstrap.go` 单次只读聚合第 %d 条标题预览，冷启动零撑开。", n)
			recallCounts["ds-main/"+id] = &KnowledgeRecallStats{Count: int64(n % 9), StartedAt: "2026-11-01T00:00:00Z"}
		}
	}
	sessions := make([]Session, 0, totalSessions)
	for n := 0; n < totalSessions; n++ {
		sessions = append(sessions, Session{ID: fmt.Sprintf("s-%d", n), User: "u1", Dataset: "ds-main"})
	}
	runs := make([]activityRow, 0, totalRuns)
	for n := 0; n < totalRuns; n++ {
		runs = append(runs, activityRow{
			ID:        fmt.Sprintf("r-%d", n),
			Run:       fmt.Sprintf("run-%d", n),
			Dataset:   "ds-main",
			Operation: "improve",
			Outcome:   "succeeded",
			Created:   now.Add(-time.Duration(n) * time.Minute).UTC().Format(time.RFC3339),
		})
	}
	payload := overviewBootstrapResponse{
		Documents:       docs,
		Sessions:        sessions,
		Previews:        previews,
		PipelineRuns:    runs,
		RecallStartedAt: "2026-11-01T00:00:00Z",
		RecallCounts:    recallCounts,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/overview/bootstrap") {
			bootstrapCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(encoded)
			return
		}
		http.Error(w, "unexpected endpoint: "+r.URL.Path, 500)
	}))
	defer server.Close()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "manager.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	officialTokenFile := filepath.Join(dir, "official.token")
	if err := os.WriteFile(officialTokenFile, []byte(strings.Repeat("k", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ServiceURL:      server.URL,
		TokenFile:       officialTokenFile,
		ClientStateFile: filepath.Join(dir, "state.json"),
		ProjectControl:  &ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile},
		Projects:        []Project{{ID: "p1", Name: "memlane", Dataset: "ds-main"}},
	}

	tStart := time.Now()
	a := New(cfg, nil)
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.access.Directory.Projects = []AccessProject{{ID: "p1", Name: "memlane", Dataset: "ds-main"}}
	a.access.Queues = map[string]*projectQueueState{
		a.cachedBaseReadingIdentity() + "/p1/ds-main": {
			Loaded:           true,
			Next:             time.Now().Add(time.Hour),
			localStateLoaded: true,
		},
	}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	drainReading(t, updates, func() bool { return a.overviewState.Loaded && a.overviewState.ActivityLoaded })

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	coldReady := time.Since(tStart)

	if bootstrapCalls != 1 {
		t.Fatalf("expected 1 single bootstrap HTTP call, got %d", bootstrapCalls)
	}
	if coldReady > 150*time.Millisecond {
		t.Errorf("cold-start Overview ready time %v exceeds 150ms budget", coldReady)
	}
	if len(a.overviewState.Snapshot.Knowledge) != totalKnowledge || a.overviewState.Snapshot.Raw != totalSessions {
		t.Fatalf("unexpected cold-start snapshot: knowledge=%d raw=%d", len(a.overviewState.Snapshot.Knowledge), a.overviewState.Snapshot.Raw)
	}
	readyTop := 0
	for _, item := range a.overviewState.Snapshot.Knowledge[:20] {
		if item.ReadingState == "ready" {
			readyTop++
		}
	}
	if readyTop < 20 {
		t.Errorf("cold-start top-20 titles not atomically ready: %d/20", readyTop)
	}

	// Measure 15 frames during active Page Rise + Wave Grow + Stagger Cascade.
	frames := make([]time.Duration, 0, 15)
	for n := 0; n < 15; n++ {
		f0 := time.Now()
		tt.Frame()
		frames = append(frames, time.Since(f0))
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	if median := frames[len(frames)/2]; median > 12*time.Millisecond {
		t.Errorf("cold-start animated Overview median frame %v exceeds 12ms budget", median)
	}
	if worst := frames[len(frames)-1]; worst > 25*time.Millisecond {
		t.Errorf("cold-start animated Overview worst frame %v exceeds 25ms budget", worst)
	}
}

func TestKnowledgeUnreadToggleKeepsFirstItemVisible(t *testing.T) {
	a := perfFixture(t)
	a.navigate("知识", 0)
	s := a.state()
	p := a.Config.Projects[0]
	identity := a.cachedBaseReadingIdentity()
	if a.access.Queues == nil {
		a.access.Queues = map[string]*projectQueueState{}
	}
	readMap := map[string]bool{}
	for n, item := range s.Knowledge.Catalog {
		readMap[item.ID] = n < 5 // first 5 read, rest unread
	}
	a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset] = &projectQueueState{
		KnowledgeRead: knowledgeReadState{Version: 1, Initialized: true, Read: readMap},
	}
	a.reorderKnowledge(s)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	for _, unread := range []bool{true, false} {
		s.Knowledge.UnreadOnly = unread
		a.reorderKnowledge(s)
		tt.Frame()
		tt.Frame()
		if len(s.Items) == 0 {
			t.Fatalf("unread=%v produced empty list", unread)
		}
		if s.ListScroll.Y > 1 {
			t.Fatalf("unread=%v left list scrolled down: scrollY=%v", unread, s.ListScroll.Y)
		}
		first, _ := s.WidgetList.Visible()
		if first != 0 {
			t.Fatalf("unread=%v top visible row=%d, expected 0", unread, first)
		}
	}
}

func TestSwitchProjectToOverviewIsPaintOnlyAndZeroTitleStorm(t *testing.T) {
	a := overviewFixture(t)
	now := time.Now()
	items := make([]Item, 0, 200)
	for n := 0; n < 200; n++ {
		day := now.AddDate(0, 0, -(n % 90)).Format("2006-01-02")
		d := Document{
			ID:      fmt.Sprintf("perf-%d", n),
			Dataset: a.Config.Projects[0].Dataset,
			Created: day + "T08:00:00Z",
			Metadata: map[string]any{
				"node_set":     []any{"session_learnings"},
				"lesson_type":  "code_location",
				"session_date": day,
			},
		}
		if readableDocumentTitle(d) != "" {
			t.Fatalf("expected opaque fixture filename for item %d", n)
		}
		body := fmt.Sprintf("`internal/simpledesktop/perf_%d.go` 在返回总览时保持行高稳定。", n)
		items = append(items, Item{
			ID:           d.ID,
			Dataset:      d.Dataset,
			Kind:         "知识",
			Title:        body,
			Body:         body,
			BodyLoaded:   true,
			ReadingState: "ready",
			Date:         day,
			Document:     &d,
			RecallStats:  &KnowledgeRecallStats{Count: int64(n % 12)},
		})
	}
	events := make([]overviewEvent, 0, 30)
	for n := 0; n < 30; n++ {
		events = append(events, overviewEvent{
			Key:     fmt.Sprintf("ev-%d", n),
			Title:   "Gemini App 自动召回",
			Project: a.Config.Projects[0].Name,
			State:   "已完成",
			At:      now.Add(-time.Duration(n) * time.Minute),
		})
	}
	a.overviewState.scope = a.overviewScope()
	a.overviewState.Loaded = true
	a.overviewState.RawLoaded = true
	a.overviewState.Snapshot = overviewSnapshot{Knowledge: items, Raw: 83}
	a.overviewState.ActivityLoaded = true
	a.overviewState.Events = events
	a.overviewRefreshAt = now.Add(time.Hour)

	a.Config.Projects[1].Name = "dcim-fe"
	a.navigate("知识", 1)
	dcimPane := a.state()
	dcimPane.Loaded = true
	dcimPane.Loading = false
	dcimPane.Knowledge.Initialized = true
	dcimPane.Knowledge.CountsLoaded = true
	dcimPane.Items = nil
	a.navigate("总览", 0)

	var builds int
	var updateCalls atomic.Int64
	a.Update = func(func()) {
		updateCalls.Add(1)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		a.View(c)
	}, 1200, 820)
	tt.Frame()
	if len(a.overviewState.cachedBuckets) == 0 || len(a.overviewState.cachedHeatmapCells) == 0 {
		t.Fatalf("expected overview heatmap caches to be populated on initial render")
	}

	// Switch to empty project dcim-fe and confirm v4EmptyState animates on paint-only path.
	if err := tt.Click("dcim-fe"); err != nil {
		t.Fatalf("click dcim-fe: %v", err)
	}
	tt.Frame()
	builds = 0
	tt.Advance(300 * time.Millisecond)
	if !tt.FramePending() {
		t.Fatalf("expected empty project breathing animation to keep running")
	}
	if builds != 0 {
		t.Fatalf("empty project breathing animation triggered %d App.View rebuilds, want 0", builds)
	}

	// Switch back to Overview via sidebar click: no title goroutine/Update storm, Overview
	// animations launch cleanly and advance purely on paint-only frames until settling.
	prevReq := a.recentRequest
	updateCalls.Store(0)
	if err := tt.Click("总览"); err != nil {
		t.Fatalf("click overview: %v", err)
	}
	if a.page != "总览" {
		t.Fatalf("expected page 总览, got %q", a.page)
	}
	if a.recentRequest != prevReq || updateCalls.Load() != 0 {
		t.Fatalf("returning to overview restarted ready titles: req %d->%d updates=%d", prevReq, a.recentRequest, updateCalls.Load())
	}
	if a.overviewState.heatmapGrowStart.IsZero() {
		t.Fatalf("expected heatmapGrowStart to latch on frame 1")
	}
	imgStart := tt.Image()

	builds = 0
	tt.Advance(150 * time.Millisecond)
	if !tt.FramePending() {
		t.Fatalf("expected Overview enter/wave animations to remain active at 150ms")
	}
	if builds != 0 {
		t.Fatalf("Overview enter animation triggered %d App.View rebuilds in first 150ms, want 0", builds)
	}
	imgMid := tt.Image()

	tt.Advance(850 * time.Millisecond)
	if builds != 0 {
		t.Fatalf("Overview enter animation triggered %d App.View rebuilds across 1000ms, want 0", builds)
	}
	if tt.FramePending() {
		t.Fatalf("Overview still requesting frames after 1000ms settle (idle CPU not zero)")
	}
	imgEnd := tt.Image()
	if bytes.Equal(imgStart.Pix, imgMid.Pix) {
		t.Fatalf("Overview animation pixels did not change between 0ms and 150ms (animation broken/static)")
	}
	if bytes.Equal(imgMid.Pix, imgEnd.Pix) {
		t.Fatalf("Overview animation pixels did not progress between 150ms and 1000ms")
	}

	// Heatmap Cell Tap must also animate purely on paint frames and settle cleanly.
	a.selectOverviewDay(time.Now().Format("2006-01-02"))
	tt.Frame()
	builds = 0
	tt.Advance(150 * time.Millisecond)
	if !tt.FramePending() {
		t.Fatalf("expected Cell Tap animation to be active at 150ms")
	}
	if builds != 0 {
		t.Fatalf("Cell Tap animation triggered %d App.View rebuilds at 150ms, want 0", builds)
	}
	tt.Advance(600 * time.Millisecond)
	if builds != 0 {
		t.Fatalf("Cell Tap animation triggered %d App.View rebuilds to settle, want 0", builds)
	}
	if tt.FramePending() {
		t.Fatalf("Cell Tap animation still pending after 750ms")
	}

	// ReduceMotion must settle both empty state and Overview immediately.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("dcim-fe"); err != nil {
		t.Fatalf("click dcim-fe under ReduceMotion: %v", err)
	}
	tt.Frame()
	if tt.FramePending() {
		t.Fatalf("ReduceMotion empty project still requesting frames")
	}
	if err := tt.Click("总览"); err != nil {
		t.Fatalf("click overview under ReduceMotion: %v", err)
	}
	if tt.FramePending() {
		t.Fatalf("ReduceMotion Overview still requesting frames")
	}
}
