package simpledesktop

import (
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func perfFixture(t *testing.T) *App {
	t.Helper()
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
