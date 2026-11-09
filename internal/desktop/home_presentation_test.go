package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestHomeDateSelectionKeepsGlobalTotalsAndSurvivesNavigation(t *testing.T) {
	a := New("test", nil)
	raw, knowledge := 17, 3
	a.overviewRaw, a.overviewKnowledge = &raw, &knowledge
	a.activityLoaded = true
	date := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	a.activities[date] = 1
	a.recent = []MemoryItem{{ID: "old-range", Title: "旧日期知识"}}
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("可选设置指引") || !tt.HasText("首次使用日期未知") {
		t.Fatal("home fabricated first use or repeated onboarding")
	}
	if err := tt.Click(date + " 新增 1 条"); err != nil {
		t.Fatal(err)
	}
	if a.page != "总览" || a.homeDate != date || a.listQuery().Date != date || !tt.HasText("17") || !tt.HasText("3") {
		t.Fatal("date selection changed global totals or left home")
	}
	if tt.HasText("旧日期知识") {
		t.Fatal("switching date reused another range before a successful read")
	}
	a.libraryError = "explicit read failure"
	if tt.HasText("这一天没有新增知识") {
		t.Fatal("failed date read was presented as empty")
	}
	a.libraryError = ""
	a.navigate("全部记忆", "")
	a.navigate("总览", "")
	if a.homeDate != date {
		t.Fatal("home date was lost on detail navigation")
	}
	if err := tt.Click(date + " 新增 1 条"); err != nil {
		t.Fatal(err)
	}
	if a.homeDate != "" || a.page != "总览" {
		t.Fatal("selecting the same date did not clear only the date filter")
	}
	for _, size := range [][2]int{{1200, 820}, {980, 620}} {
		tt.SetSize(size[0], size[1])
		node, ok := tt.Find("管理计划")
		if !ok || node.Y+node.H > float32(size[1]) {
			t.Fatal("scroll content displaced fixed plan footer")
		}
	}
}

func TestHomeCompletionRequiresFinalReceiptAndDoesNotReplay(t *testing.T) {
	for _, terminal := range []string{"", "failed", "cancelled", "interrupted", "completed"} {
		t.Run(terminal, func(t *testing.T) {
			a := New("test", nil)
			now := time.Now()
			a.observeHomeTask(homeTask{ID: "task", Kind: "organization", State: "running"}, homeTask{}, now)
			a.observeHomeTask(homeTask{}, homeTask{ID: "task", Kind: "organization", State: terminal}, now)
			if !a.homeCompletedAt.IsZero() != (terminal == "completed") {
				t.Fatal("absent, failed or cancelled task fabricated completion")
			}
			when := a.homeCompletedAt
			a.observeHomeTask(homeTask{}, homeTask{ID: "task", Kind: "organization", State: terminal}, now.Add(time.Second))
			if a.homeCompletedAt != when {
				t.Fatal("polling replayed completion feedback")
			}
		})
	}
}

func TestHomeRunningBlocksMoveAtCurrentEpochAndCompletionIsBounded(t *testing.T) {
	epoch := float64(time.Now().UnixMilli()) / 1000
	moved := false
	for i := 0; i < 9; i++ {
		x, y, _, _ := homeBlockPose(i, epoch, "running")
		nextX, nextY, _, _ := homeBlockPose(i, epoch+.2, "running")
		if x != nextX || y != nextY {
			moved = true
		}
	}
	if !moved {
		t.Fatal("epoch precision froze the running glyph")
	}
	a := New("test", nil)
	a.serviceState = "ready"
	a.homeCompletedAt = time.Now()
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("整理完成") {
		t.Fatal("real final receipt feedback is missing")
	}
	a.homeCompletedAt = time.Now().Add(-2 * time.Second)
	tt = ui.NewTester(a.View, 1200, 820)
	if tt.HasText("整理完成") {
		t.Fatal("expired completion feedback remained")
	}
	a.homeMotion = homeButtonMotion{Hover: true, HoverAt: time.Now()}
	a.navigate("设置", "")
	if a.homeMotion.Hover || !a.homeMotion.HoverAt.IsZero() {
		t.Fatal("hidden hover state replayed on reentry")
	}
}
