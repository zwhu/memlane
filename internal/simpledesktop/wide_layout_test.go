package simpledesktop

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestHeatmapUsesAvailableWidth(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for _, width := range []int{900, 1200, 1600} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := overviewFixture(t)
			a.overviewState.Snapshot.Knowledge = []Item{{Document: &Document{Created: now.Format(time.RFC3339)}}}
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.overviewHeatmap(c, now) }) }, width, 600)
			day, ok := tt.Find("2026-11-17 · 新增 1 条记忆")
			if !ok {
				t.Fatal("last calendar cell missing")
			}
			if day.X+day.W > float32(width) {
				t.Fatal("calendar overflows")
			}
			today, ok := tt.Find("今天新增 1 条")
			if !ok || today.X <= day.X+day.W {
				t.Fatalf("V4 heatmap right summary misplaced: day=%+v today=%+v", day, today)
			}
		})
	}
}

func TestReaderInfoFollowsBodyWidth(t *testing.T) {
	for _, width := range []int{700, 1100, 1500} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := projectUIFixture()
			tt := ui.NewTester(func(c *ui.Context) { a.projectDetail(c, a.state()) }, width, 820)
			info, ok := tt.Find("内容信息")
			if !ok {
				t.Fatal("information action missing")
			}
			right := sp10 + min(float32(width)-2*sp10, readerMaxW)
			if math.Abs(float64(info.X+info.W-right)) > 2 {
				t.Fatalf("information action detached from body edge: %+v expected right %v", info, right)
			}
		})
	}
}
