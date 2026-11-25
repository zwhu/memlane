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

func newFrameBudgetTester(t *testing.T, view func(*ui.Context), width, height int) (*ui.Tester, func() time.Duration) {
	t.Helper()
	const refresh = time.Second / 60
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		// Force a full view build at each display tick, even for paint-only motion.
		c.After(refresh)
		view(c)
	}, width, height)
	return tt, func() time.Duration {
		t.Helper()
		before := builds
		start := time.Now()
		// Frame() repeatedly rebuilds at a frozen clock while animation is active.
		tt.Advance(refresh)
		elapsed := time.Since(start)
		if builds-before != 1 {
			t.Fatalf("expected one view build per frame sample, got %d", builds-before)
		}
		return elapsed
	}
}

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
			Loaded: true,
			Next:   time.Now().Add(time.Hour),
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
	tt, measureFrame := newFrameBudgetTester(t, a.View, 1200, 820)
	// Warm up font atlas and layout caches.
	for n := 0; n < 4; n++ {
		tt.Frame()
	}

	measureMedian := func(label string, frames int) time.Duration {
		samples := make([]time.Duration, 0, frames)
		for n := 0; n < frames; n++ {
			samples = append(samples, measureFrame())
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
	buckets := make(map[string]int)
	recent := make([]v2KnowledgeItem, 0, 20)
	for n := 0; n < totalKnowledge; n++ {
		day := now.AddDate(0, 0, -(n / 5)).Format("2006-01-02")
		buckets[day]++
		if n < 20 {
			recent = append(recent, v2KnowledgeItem{
				DocID: fmt.Sprintf("cold-%04d", n), DatasetID: "ds-main", ProjectID: "p1",
				Title: fmt.Sprintf("冷启动聚合第 %d 条", n), PreviewText: "正文预览",
				Kind: "knowledge", EffectiveDate: day, CreatedAt: day + "T08:00:00Z",
			})
		}
	}
	dayBuckets := make([]v2OverviewDayBucket, 0, len(buckets))
	for day, count := range buckets {
		dayBuckets = append(dayBuckets, v2OverviewDayBucket{Day: day, Count: count})
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
	payload := v2OverviewBootstrapResponse{
		Generation:          1,
		TotalKnowledgeCount: totalKnowledge,
		DayBuckets:          dayBuckets,
		RecentItems:         recent,
		Projects:            []v2OverviewProjectSummary{{ProjectID: "p1", DatasetID: "ds-main", SessionCount: totalSessions}},
		PipelineRuns:        runs,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
			return
		}
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
			Loaded: true,
			Next:   time.Now().Add(time.Hour),
		},
	}
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	drainReading(t, updates, func() bool { return a.overviewState.Loaded && a.overviewState.ActivityLoaded })

	_, measureFrame := newFrameBudgetTester(t, a.View, 1200, 820)
	coldReady := time.Since(tStart)

	if bootstrapCalls != 1 {
		t.Fatalf("expected 1 single bootstrap HTTP call, got %d", bootstrapCalls)
	}
	if coldReady > 150*time.Millisecond {
		t.Errorf("cold-start Overview ready time %v exceeds 150ms budget", coldReady)
	}
	if a.overviewState.V2TotalKnowledge != totalKnowledge || a.overviewState.Snapshot.Raw != totalSessions {
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
		frames = append(frames, measureFrame())
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
	for n := range s.Knowledge.Catalog {
		s.Knowledge.Catalog[n].ServerUnreadKnown = true
		s.Knowledge.Catalog[n].ServerUnread = n >= 5
	}
	a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset] = &projectQueueState{
		UnreadState: UnreadState{ServerUnreadKnown: true, ServerUnreadCount: len(s.Knowledge.Catalog) - 5},
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
	a.overviewState.ScopeKey = a.overviewScope()
	a.overviewState.Loaded = true
	a.overviewState.RawLoaded = true
	a.overviewState.Snapshot = overviewSnapshot{Knowledge: items, Raw: 83}
	a.overviewState.ActivityLoaded = true
	a.overviewState.Events = events
	a.overview.RefreshAt = now.Add(time.Hour)

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
	if len(a.overviewState.BucketCache) == 0 || len(a.overviewState.HeatmapCache) == 0 {
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
	if a.overviewState.HeatmapGrowStart.IsZero() {
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

// TestScale50kDesktopSLABudgets enforces the §6.2 50k-scale desktop SLA gates
// across /v1/* cursor APIs, <=300 sliding window pagination, 256 KiB profile
// virtualization, multi-project tray flyout caching, Light/Dark frame times
// (<12ms/<16ms median, <25ms peak), cold-start Overview (<150ms), and bounded
// heap delta (<=25 MiB) with zero legacy full-catalog fallbacks.
func TestScale50kDesktopSLABudgets(t *testing.T) {
	now := time.Now()
	const (
		scaleKnowledgeCount = 50000
		scaleProjectCount   = 8
	)

	dayBuckets := make([]v2OverviewDayBucket, 0, 365)
	for d := 0; d < 365; d++ {
		dayBuckets = append(dayBuckets, v2OverviewDayBucket{
			Day:   now.AddDate(0, 0, -d).Format("2006-01-02"),
			Count: (scaleKnowledgeCount / 365) + (d % 5),
		})
	}
	projects := make([]Project, 0, scaleProjectCount)
	accessProjects := make([]AccessProject, 0, scaleProjectCount)
	projectSummaries := make([]v2OverviewProjectSummary, 0, scaleProjectCount)
	for i := 0; i < scaleProjectCount; i++ {
		pid := fmt.Sprintf("p-50k-%d", i)
		ds := fmt.Sprintf("ds-50k-%d", i)
		name := fmt.Sprintf("workspace-%d", i)
		projects = append(projects, Project{ID: pid, Name: name, Path: "/synthetic/" + name, Dataset: ds})
		accessProjects = append(accessProjects, AccessProject{ID: pid, Name: name, Dataset: ds, Status: "verified"})
		kCount := 2000
		if i == 0 {
			kCount = 36000
		}
		projectSummaries = append(projectSummaries, v2OverviewProjectSummary{
			ProjectID:      pid,
			DatasetID:      ds,
			KnowledgeCount: kCount,
			RawDocCount:    1250,
			UnreadCount:    (i + 1) * 3,
			CatalogVersion: "cv-50k-1",
		})
	}
	recentItems := make([]v2KnowledgeItem, 0, 20)
	for i := 0; i < 20; i++ {
		day := now.AddDate(0, 0, -(i % 7)).Format("2006-01-02")
		recentItems = append(recentItems, v2KnowledgeItem{
			DocID:            fmt.Sprintf("k50k-%05d", i),
			DatasetID:        "ds-50k-0",
			ProjectID:        "p-50k-0",
			Title:            fmt.Sprintf("50k 规模总览预览 #%d", i),
			EffectiveDate:    day,
			CreatedAt:        day + "T08:00:00Z",
			UpdatedAt:        day + "T08:00:00Z",
			ContentHash:      fmt.Sprintf("h-%05d", i),
			Kind:             "knowledge",
			Category:         "项目约定",
			LessonType:       "convention",
			SessionID:        fmt.Sprintf("codex_s50k-%d", i),
			RecallCount:      int64(i % 9),
			IsUnread:         i < 3,
			PreviewText:      fmt.Sprintf("`internal/simpledesktop/v2_client.go` 第 %d 条 50k 规模零正文预览摘要。", i),
			PreviewTruncated: true,
		})
	}
	runs := make([]activityRow, 0, 50)
	for i := 0; i < 50; i++ {
		runs = append(runs, activityRow{
			ID:        fmt.Sprintf("run-50k-%d", i),
			Run:       fmt.Sprintf("r-%d", i),
			Dataset:   "ds-50k-0",
			Operation: "improve",
			Outcome:   "succeeded",
			Created:   now.Add(-time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
		})
	}
	overviewPayload, err := json.Marshal(v2OverviewBootstrapResponse{
		Generation:          50000,
		LocalTZName:         "Asia/Shanghai",
		TotalKnowledgeCount: scaleKnowledgeCount,
		DayBuckets:          dayBuckets,
		Projects:            projectSummaries,
		RecentItems:         recentItems,
		PipelineRuns:        runs,
		RecallStartedAt:     "2026-10-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(overviewPayload) >= 128*1024 {
		t.Fatalf("50k overview v2 payload %d bytes exceeds 128 KiB budget", len(overviewPayload))
	}

	var overviewV2Calls, v1BootstrapCalls, legacyDataCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{
					v2FeatureCatalogIndex,
					v2FeatureReadingState,
					v2FeatureLocalBodyFastpath,
					v2FeatureOverview,
					v2FeatureKnowledgeCursor,
					v2FeatureRawRecords,
					v2FeatureSearch,
					v2FeatureMessageSummary,
					v2FeatureProvenanceBackfill,
				},
			})
		case r.URL.Path == "/v1/overview/bootstrap":
			overviewV2Calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(overviewPayload)
		case r.URL.Path == "/v1/projects/p-50k-0/knowledge":
			pageIdx := 0
			if cursor := r.URL.Query().Get("cursor"); cursor != "" {
				_, _ = fmt.Sscanf(cursor, "p50k-%d", &pageIdx)
			}
			items := make([]v2KnowledgeItem, 0, 30)
			for i := 0; i < 30; i++ {
				id := fmt.Sprintf("k50k-%05d", pageIdx*30+i)
				items = append(items, v2KnowledgeItem{
					DocID:            id,
					DatasetID:        "ds-50k-0",
					ProjectID:        "p-50k-0",
					Title:            "50k 记忆 " + id,
					EffectiveDate:    now.Format("2006-01-02"),
					CreatedAt:        now.UTC().Format(time.RFC3339),
					ContentHash:      "ch-" + id,
					Kind:             "knowledge",
					Category:         "踩坑经验",
					LessonType:       "pitfall",
					SessionID:        "codex_s50k-0",
					RecallCount:      int64(i % 7),
					PreviewText:      fmt.Sprintf("第 %d 页第 %d 条受控截断预览文本，用于 50k 滑动窗口渲染测试。", pageIdx, i),
					PreviewTruncated: true,
				})
			}
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID:      "p-50k-0",
				DatasetID:      "ds-50k-0",
				Generation:     50000,
				CatalogVersion: "cv-50k-1",
				Sort:           "date",
				TotalFiltered:  36000,
				UnreadCount:    3,
				CategoryCounts: map[string]int{"踩坑经验": 20000, "项目约定": 16000},
				Items:          items,
				NextCursor:     fmt.Sprintf("p50k-%d", pageIdx+1),
				HasMore:        pageIdx < 20,
			})
		case strings.HasSuffix(r.URL.Path, "/body") && strings.HasPrefix(r.URL.Path, "/v1/datasets/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/datasets/"), "/")
			dsID, docID := "ds-50k-0", "k50k-00000"
			if len(parts) >= 4 {
				dsID, docID = parts[0], parts[2]
			}
			bodyText := "# Session learning (session codex_s50k-0)\n\n50k 规模正文直读快路径内容。"
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID:        docID,
				DatasetID: dsID,
				Body:      &bodyText,
				MIMEType:  "text/markdown",
				Source:    "local_fastpath",
			})
		case r.URL.Path == "/v1/projects/p-50k-0/raw-records":
			items := make([]v2RawRecordItem, 0, 30)
			for i := 0; i < 30; i++ {
				sid := fmt.Sprintf("codex_raw-%04d", i)
				items = append(items, v2RawRecordItem{
					ItemKind: "session",
					SortTSNS: int64(100000 - i),
					Session: &v2RawSessionEntry{
						Session:  Session{ID: sid, Dataset: "ds-50k-0", User: "u1", Agent: "codex", Updated: now.UTC().Format(time.RFC3339)},
						Label:    "50k 原始会话 " + sid,
						MsgCount: 5,
					},
				})
			}
			_ = json.NewEncoder(w).Encode(v2RawRecordsResponse{
				ProjectID:  "p-50k-0",
				DatasetID:  "ds-50k-0",
				Kind:       "all",
				CacheReady: true,
				Items:      items,
				HasMore:    true,
				NextCursor: "raw-1",
			})
		case r.URL.Path == "/v1/raw-records/session-history":
			_ = json.NewEncoder(w).Encode(SessionHistoryPage{
				Paging: true, Limit: 20,
				SessionDetail: SessionDetail{
					Session: Session{ID: r.URL.Query().Get("session_id"), Dataset: "ds-50k-0", User: "u1", Updated: now.UTC().Format(time.RFC3339)},
					Label:   "50k 原始会话详情",
				},
			})
		case r.URL.Path == "/v1/reading-state/mark-read":
			_ = json.NewEncoder(w).Encode(v2ReadingStateMarkReadResponse{ScopeHash: "s1", Marked: 1, UnreadCount: 2, ReadHorizonAtNS: now.UTC().Format(time.RFC3339Nano)})
		case r.URL.Path == "/v1/provenance/backfill-legacy-dates":
			_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{Status: "absent"})
		case strings.HasPrefix(r.URL.Path, "/v1/projects/") && strings.HasSuffix(r.URL.Path, "/message-summary"):
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID:               "p-50k-0",
				DatasetID:               "ds-50k-0",
				KnowledgeCatalogVersion: "cv-50k-1",
				UnreadCount:             3,
			})
		case r.URL.Path == "/v1/user-profiles":
			_ = json.NewEncoder(w).Encode(userProfilesDirectory{
				Profiles: []userProfile{{
					ID: "prof-50k", Kind: "codex", Name: "Codex", Status: "ready", Method: "manual",
					Updated: float64(now.Unix()), Document: &userProfileDocument{Dataset: "ds-prof", ID: "doc-prof-50k"},
				}},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/overview/bootstrap"):
			v1BootstrapCalls.Add(1)
			http.Error(w, "unexpected v1 bootstrap", 500)
		case strings.Contains(r.URL.Path, "/api/v1/datasets/"):
			legacyDataCalls.Add(1)
			http.Error(w, "unexpected legacy dataset scan", 500)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "manager.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ServiceURL:      server.URL,
		ClientStateFile: filepath.Join(dir, "state.json"),
		ProjectControl:  &ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile},
		Projects:        projects,
	}

	// 1. Cold-start Overview via /v1/overview/bootstrap (<150ms budget).
	tStart := time.Now()
	a := New(cfg, nil)
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.access.Directory = AccessDirectory{Projects: accessProjects}
	a.service = "已连接"
	updates := make(chan func(), 256)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(func() { a.cancelAndResetPanes() })

	a.loadOverview()
	drainReading(t, updates, func() bool { return a.overviewState.Loaded && a.overviewState.ActivityLoaded })

	tt, measureFrame := newFrameBudgetTester(t, a.View, 1200, 820)
	coldReady := time.Since(tStart)

	if overviewV2Calls.Load() != 1 || v1BootstrapCalls.Load() != 0 || legacyDataCalls.Load() != 0 {
		t.Fatalf("expected overviewV2=1 v1=0 legacy=0, got v2=%d v1=%d legacy=%d", overviewV2Calls.Load(), v1BootstrapCalls.Load(), legacyDataCalls.Load())
	}
	if !a.overviewState.V2Mode || a.overviewState.V2TotalKnowledge != scaleKnowledgeCount || len(a.overviewState.Snapshot.Knowledge) != 20 {
		t.Fatalf("unexpected 50k overview v2 state: mode=%v total=%d recent=%d", a.overviewState.V2Mode, a.overviewState.V2TotalKnowledge, len(a.overviewState.Snapshot.Knowledge))
	}
	if !raceDetectorEnabled && coldReady > 150*time.Millisecond {
		t.Errorf("50k cold-start Overview ready time %v exceeds 150ms budget", coldReady)
	}

	// 2. Navigate to Project Knowledge and paginate deeply past 300 items (15 pages = 450 items -> capped at 300).
	a.navigate("知识", 0)
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading && !s.Reading })
	s.Selected = nil // allow full sliding-window eviction
	for page := 1; page < 15; page++ {
		a.appendKnowledgeBatch(s)
		drainReading(t, updates, func() bool { return !s.Knowledge.Appending })
	}
	if len(s.Items) != maxActiveListItems || len(s.Knowledge.Catalog) != 0 {
		t.Fatalf("expected 50k sliding window=%d and empty client catalog, got items=%d catalog=%d", maxActiveListItems, len(s.Items), len(s.Knowledge.Catalog))
	}

	// 3. Prepare 256 KiB virtualized User Profile body.
	var profileBuf strings.Builder
	for i := 0; i < 640; i++ {
		fmt.Fprintf(&profileBuf, "## 50k 画像章节 %03d\n\n用户长期偏好简洁可靠的系统设计与严格的自动化性能门禁，避免在 UI 渲染热路径引入全量扫描或同步落盘。\n\n", i)
	}
	profileBody := profileBuf.String()
	a.userProfiles.Loaded = true
	a.userProfiles.Next = time.Now().Add(time.Hour)
	a.userProfiles.ReadAt = time.Now()
	a.userProfiles.Directory = userProfilesDirectory{
		Profiles: []userProfile{{
			ID:       "prof-50k",
			Kind:     "codex",
			Name:     "Codex",
			Status:   "ready",
			Method:   "manual",
			Updated:  float64(now.Unix()),
			Document: &userProfileDocument{Dataset: "ds-prof", ID: "doc-prof-50k"},
		}},
	}
	a.userProfiles.Selected = "prof-50k"
	a.userProfiles.Body = profileBody
	a.userProfiles.BodyKey = "prof-50k/ds-prof/doc-prof-50k"

	// 4. Frame-time SLA checks across Light and Dark themes (Overview <12ms, Project/Profile <16ms, peak <25ms).
	measureFrames := func(label string, frames int, medianBudget, peakBudget time.Duration) {
		// Warm up layout/font caches for current page & theme.
		for w := 0; w < 3; w++ {
			measureFrame()
		}
		samples := make([]time.Duration, 0, frames)
		for n := 0; n < frames; n++ {
			samples = append(samples, measureFrame())
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		if !raceDetectorEnabled {
			if median := samples[len(samples)/2]; median > medianBudget {
				t.Errorf("%s median frame %v exceeds %v budget", label, median, medianBudget)
			}
			if peak := samples[len(samples)-1]; peak > peakBudget {
				t.Errorf("%s peak frame %v exceeds %v budget", label, peak, peakBudget)
			}
		}
	}

	for _, appearance := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(appearance)
		a.navigate("总览", 0)
		measureFrames("50k-overview-"+appearance, 15, 12*time.Millisecond, 25*time.Millisecond)

		a.navigate("知识", 0)
		s = a.state()
		drainReading(t, updates, func() bool { return s.Loaded && !s.Loading })
		measureFrames("50k-project-knowledge-"+appearance, 15, 16*time.Millisecond, 25*time.Millisecond)

		a.navigate("用户画像", 0)
		measureFrames("50k-profile-256k-"+appearance, 15, 16*time.Millisecond, 25*time.Millisecond)
		first, last := a.userProfiles.ReaderList.Visible()
		_, profileChunks := profileCachedMarkdownChunks(&a.userProfiles)
		if len(profileChunks) < 20 || (last-first+1) >= len(profileChunks) {
			t.Fatalf("[%s] 256 KiB profile was not virtualized: chunks=%d visible=%d..%d", appearance, len(profileChunks), first, last)
		}
	}

	// 5. Multi-project Tray Flyout caching & frame budget (8 projects, 20 frames -> 1 computation).
	a.trayCache = trayFlyoutCache{}
	_, measureTrayFrame := newFrameBudgetTester(t, a.TrayFlyoutView, 300, 440)
	trayFrames := make([]time.Duration, 0, 20)
	for n := 0; n < 20; n++ {
		trayFrames = append(trayFrames, measureTrayFrame())
	}
	sort.Slice(trayFrames, func(i, j int) bool { return trayFrames[i] < trayFrames[j] })
	if a.trayCache.summaryComputations != 1 {
		t.Fatalf("expected tray summary cached across 20 frames (1 computation), got %d", a.trayCache.summaryComputations)
	}
	if !raceDetectorEnabled && trayFrames[len(trayFrames)/2] > 12*time.Millisecond {
		t.Errorf("50k multi-project tray median frame %v exceeds 12ms budget", trayFrames[len(trayFrames)/2])
	}

	// 6. Page switches + deep pagination + Light/Dark toggles heap delta <= 25 MiB.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for step := 0; step < 20; step++ {
		if step%2 == 0 {
			a.setAppearancePreference(appearanceDark)
		} else {
			a.setAppearancePreference(appearanceLight)
		}
		switch step % 4 {
		case 0:
			a.navigate("总览", 0)
		case 1:
			a.navigate("知识", 0)
			cur := a.state()
			drainReading(t, updates, func() bool { return cur.Loaded && !cur.Loading })
			cur.Selected = nil
			for p := 0; p < 3 && cur.More; p++ {
				a.appendKnowledgeBatch(cur)
				drainReading(t, updates, func() bool { return !cur.Knowledge.Appending })
			}
		case 2:
			a.navigateKind("知识", 0, "原始记录")
			cur := a.state()
			drainReading(t, updates, func() bool { return cur.Loaded && !cur.Loading })
		case 3:
			a.navigate("用户画像", 0)
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
		t.Fatalf("50k scale page/scroll/theme switch heap grew by %.1f MiB (budget <= 25 MiB)", float64(delta)/(1024*1024))
	}
	if legacyDataCalls.Load() != 0 {
		t.Fatalf("50k scale SLA run triggered %d forbidden legacy dataset scans", legacyDataCalls.Load())
	}
}
