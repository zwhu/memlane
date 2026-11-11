package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

func TestUsageCoverageDoesNotTurnUnknownIntoZeroOr1970(t *testing.T) {
	for _, u := range []*records.UsageInfo{nil, {}, {StartedMS: 0, WindowDays: 30, RetentionDays: 90}} {
		label := usageCoverageText(u)
		if !strings.Contains(label, "未知") || strings.Contains(label, "1970") {
			t.Fatal("unknown coverage invented", label)
		}
	}
	if usageCountText(false, 0) != "未知" || usageCountText(true, 0) != "0 次" || usageCountText(true, -1) != "未知" {
		t.Fatal("zero and unknown conflated")
	}
	covered := usageCoverageText(&records.UsageInfo{StartedMS: 1800000000000, WindowDays: 30, RetentionDays: 90})
	for _, fact := range []string{"近30天", "保留90天", "各版本", "此前未知"} {
		if !strings.Contains(covered, fact) {
			t.Fatal("coverage fact missing", covered)
		}
	}
	missing := usageCoverageText(&records.UsageInfo{StartedMS: 1800000000000})
	if strings.Contains(missing, "近30天") || strings.Contains(missing, "保留90天") {
		t.Fatal("missing range invented", missing)
	}
}

func TestUsageSummaryThreeFactsAndSeparateObservationSources(t *testing.T) {
	for _, width := range []int{650, 320} {
		a := New("usage-contract", nil)
		v := MemoryItem{ID: "synthetic-knowledge", Dataset: "synthetic-project", Revision: 2, State: "active", Usage: &records.UsageInfo{StartedMS: 1800000000000, WindowDays: 30, RetentionDays: 90, Daily: records.UsageCounts{Returns: 0, Reads: 7, Adoptions: 1}, Verification: records.UsageCounts{Reads: 99}}}
		tt := ui.NewTester(func(c *ui.Context) { a.usageDetail(c, v) }, width, 760)
		for _, text := range []string{"检索返回", "成功读取", "报告采用", "0 次", "7 次", "1 次", "连接验证 · 单独统计"} {
			if !tt.HasText(text) {
				t.Fatal("usage fact/source omitted", width, text)
			}
		}
		if !tt.HasText("次数未采集 · 冻结来源见整理记录") {
			t.Fatal("unknown background presented as zero")
		}
	}
}

func TestUsageEventsPreserveExactFactsAndDoNotInferAdoption(t *testing.T) {
	first := usageEventFact{ID: "actual-return-receipt", Memory: "knowledge-id", Dataset: "scope-id", Revision: 1, Kind: "return", Channel: "daily", Connection: "connection-id", Generation: 3}
	second := first
	second.ID = "actual-read-receipt"
	second.Kind = "read"
	adopt := first
	adopt.ID = "actual-adopt-receipt"
	adopt.Kind = "adopt"
	adopt.Exposure = second.ID
	page := mergeUsageEventFacts([]usageEventFact{first, second}, []usageEventFact{second, adopt})
	if len(page) != 3 || page[2].Exposure != second.ID || page[0].Revision != 1 {
		t.Fatal("pagination fabricated or rewrote facts", page)
	}
	lines := strings.Join(usageEventReceiptLines(adopt), "\n")
	for _, fact := range []string{adopt.ID, adopt.Memory, "v1", adopt.Dataset, adopt.Connection, "代次3", second.ID} {
		if !strings.Contains(lines, fact) {
			t.Fatal("exact receipt identity omitted", lines)
		}
	}
	if usageEventAction("return") != "检索返回" || usageEventAction("read") != "成功读取" || usageEventAction("adopt") != "报告采用" {
		t.Fatal("event meanings conflated")
	}
	if usageEventChannel("verification") == usageEventChannel("daily") {
		t.Fatal("verification presented as daily")
	}
	if !strings.Contains(usageEventEmptyText("daily", 0), "未知") || strings.Contains(usageEventEmptyText("daily", 0), "暂无观测事件") {
		t.Fatal("unknown coverage presented as known zero")
	}
}

func TestUsageReceiptAndOriginalReturnKeepReadingAndHomeContext(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {980, 620}} {
		a := sourceReadingFrame()
		a.readingUsageOpen = true
		a.detailScroll.Y = 80
		a.homeActivity.Return = true
		v := a.current()
		v.Usage = &records.UsageInfo{StartedMS: 1800000000000, WindowDays: 30, RetentionDays: 90}
		event := usageEventFact{ID: "durable-read-id", Memory: v.ID, Dataset: v.Dataset, Revision: 1, Kind: "read", Channel: "daily", Connection: "real-connection-id", Generation: 3, ObservedMS: 1800000000000}
		a.usageEvents = UsageEventsUI{Root: a.library.Root, Memory: v.ID, Dataset: v.Dataset, Revision: v.Revision, Channel: "daily", Loaded: true, Items: []usageEventFact{event}, Expanded: map[string]bool{event.ID: true}}
		tt := ui.NewTester(a.View, size[0], size[1])
		if err := tt.Click("返回知识"); err != nil {
			t.Fatal(err)
		}
		if a.selectedID != v.ID || a.detailScroll.Y != 80 || !a.homeActivity.Return || !a.usageEvents.Expanded[event.ID] {
			t.Fatal("source return changed reading/home/receipt context")
		}
		for _, text := range []string{"成功读取 · v1", "事件：durable-read-id", "连接：real-connection-id · 代次3"} {
			if !tt.HasText(text) {
				t.Fatal("actual receipt or original version absent after return", size, text)
			}
		}
	}
}
