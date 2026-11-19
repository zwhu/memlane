package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	"math"
	"testing"
	"time"
)

func TestSessionPreloadKeepsListRowGeometry(t *testing.T) {
	a := projectUIFixture()
	a.kind = "原始记录"
	s := a.state()
	session := Session{ID: "codex_first-session"}
	next := Session{ID: "codex_second-session"}
	s.Items = []Item{{ID: session.ID, Kind: "会话", Title: session.ID, Session: &session}, {ID: next.ID, Kind: "会话", Title: next.ID, Session: &next}}
	s.Selected = nil
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.itemList(c, s, false) }) }, 300, 820)
	before, ok := tt.Find(sessionIdentity(next.ID))
	if !ok {
		t.Fatal("second row missing")
	}
	s.Items[0].Title = "An asynchronously loaded preview that wraps over two lines in the list"
	s.Items[0].SessionSummary = "已保存 2 轮问答 · 3 条工具记录"
	tt.Frame()
	after, _ := tt.Find(sessionIdentity(next.ID))
	if math.Abs(float64(before.X-after.X)) > .5 || math.Abs(float64(before.Y-after.Y)) > .5 || math.Abs(float64(before.H-after.H)) > .5 {
		t.Fatalf("title hydration shifts following row: before=%+v after=%+v", before, after)
	}
}

func TestOverviewLoadingCompletionKeepsHeatmapPosition(t *testing.T) {
	a := overviewFixture(t)
	a.Update = nil
	v := &a.overviewState
	v.Loaded, v.RawLoaded, v.Loading = false, false, true
	tt := ui.NewTester(a.View, 1512, 949)
	now := time.Now()
	heading := now.AddDate(0, 0, -364).Format("2006.01") + " — " + now.Format("2006.01")
	before, ok := tt.Find(heading)
	if !ok {
		t.Fatal("calendar heading missing")
	}
	v.Loaded, v.RawLoaded, v.Loading = true, true, false
	tt.Frame()
	after, _ := tt.Find(heading)
	if math.Abs(float64(before.X-after.X)) > .5 || math.Abs(float64(before.Y-after.Y)) > .5 || math.Abs(float64(before.H-after.H)) > .5 {
		t.Fatalf("statistics loading completion shifts heatmap: before=%+v after=%+v", before, after)
	}
}
