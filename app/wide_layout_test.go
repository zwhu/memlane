package app

import (
	"fmt"
	"math"
	"path/filepath"
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
			tt.Advance(600 * time.Millisecond)
			tt.Frame()
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

func TestThreePaneUnifiedGeometry(t *testing.T) {
	type pageProbe struct {
		name      string
		makeApp   func(t *testing.T) *App
		listHead  string
		readerBar string
		footers   []string
	}
	pages := []pageProbe{
		{
			name: "project-knowledge",
			makeApp: func(t *testing.T) *App {
				a := projectUIFixture()
				a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client.json")
				return a
			},
			listHead:  "项目列表头部",
			readerBar: "项目内容工具栏",
			footers:   []string{"侧栏状态栏", "记忆加载状态", "项目阅读栏"},
		},
		{
			name: "common-knowledge",
			makeApp: func(t *testing.T) *App {
				a := commonUIFixture()
				a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client.json")
				return a
			},
			listHead:  "通用记忆列表头部",
			readerBar: "通用记忆内容工具栏",
			footers:   []string{"侧栏状态栏", "通用记忆分页栏", "通用记忆阅读栏"},
		},
		{
			name: "user-profiles",
			makeApp: func(t *testing.T) *App {
				a := profileUIFixture(t)
				a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client.json")
				return a
			},
			listHead:  "用户画像来源头部",
			readerBar: "用户画像内容工具栏",
			footers:   []string{"侧栏状态栏", "用户画像来源状态", "用户画像操作栏"},
		},
	}

	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprintf("%dx%d", wh[0], wh[1]), func(t *testing.T) {
			var wantDividerX, wantListHeadY, wantListHeadH, wantReaderBarY float32
			for idx, pg := range pages {
				a := pg.makeApp(t)
				a.setAppearancePreference(appearanceLight)
				tt := ui.NewTester(a.View, wh[0], wh[1])
				tt.Advance(600 * time.Millisecond)
				tt.Frame()

				tb, ok := tt.Find("页面工具栏")
				if !ok || math.Abs(float64(tb.H-titlebarH)) > 1 {
					t.Fatalf("%s titlebar geometry mismatch: found=%v rect=%+v", pg.name, ok, tb)
				}
				div, ok := tt.Find("Divider")
				if !ok {
					t.Fatalf("%s missing split divider", pg.name)
				}
				lh, ok := tt.Find(pg.listHead)
				if !ok || math.Abs(float64(lh.Y-titlebarH)) > 1 || math.Abs(float64(lh.H-listHeaderH)) > 1 {
					t.Fatalf("%s list header geometry mismatch: found=%v rect=%+v", pg.name, ok, lh)
				}
				rb, ok := tt.Find(pg.readerBar)
				if !ok {
					t.Fatalf("%s missing reader meta bar %q", pg.name, pg.readerBar)
				}
				if math.Abs(float64(rb.Y-(titlebarH+sp6))) > 2 {
					t.Fatalf("%s reader meta bar top mismatch: got %v want ~%v", pg.name, rb.Y, titlebarH+sp6)
				}
				for _, fl := range pg.footers {
					fr, ok := tt.Find(fl)
					if !ok {
						t.Fatalf("%s missing footer %q", pg.name, fl)
					}
					if math.Abs(float64(fr.H-appFooterHeight)) > 1 || math.Abs(float64(fr.Y+fr.H-float32(wh[1]))) > 1 {
						t.Fatalf("%s footer %q misaligned: %+v", pg.name, fl, fr)
					}
				}
				if idx == 0 {
					wantDividerX = div.X
					wantListHeadY = lh.Y
					wantListHeadH = lh.H
					wantReaderBarY = rb.Y
				} else {
					if math.Abs(float64(div.X-wantDividerX)) > 1 {
						t.Fatalf("%s split divider X=%v != baseline %v", pg.name, div.X, wantDividerX)
					}
					if math.Abs(float64(lh.Y-wantListHeadY)) > 1 || math.Abs(float64(lh.H-wantListHeadH)) > 1 {
						t.Fatalf("%s list header %+v != baseline Y=%v H=%v", pg.name, lh, wantListHeadY, wantListHeadH)
					}
					if math.Abs(float64(rb.Y-wantReaderBarY)) > 2 {
						t.Fatalf("%s reader meta bar Y=%v != baseline %v", pg.name, rb.Y, wantReaderBarY)
					}
				}
				if wh[0] == 1200 && wh[1] == 820 {
					fixtureImage(t, tt, pg.name+"-light")
					a.setAppearancePreference(appearanceDark)
					tt.Advance(600 * time.Millisecond)
					tt.Frame()
					fixtureImage(t, tt, pg.name+"-dark")
				}
			}

			// Verify inline common editor header matches the 36px list header height too.
			ca := commonUIFixture()
			ca.Config.ClientStateFile = filepath.Join(t.TempDir(), "client.json")
			ca.beginCommonDraft(nil)
			tt := ui.NewTester(ca.View, wh[0], wh[1])
			tt.Advance(600 * time.Millisecond)
			tt.Frame()
			ed, ok := tt.Find("通用记忆编辑工具栏")
			if !ok || math.Abs(float64(ed.Y-titlebarH)) > 1 || math.Abs(float64(ed.H-listHeaderH)) > 1 {
				t.Fatalf("common editor header geometry mismatch: found=%v rect=%+v", ok, ed)
			}
			ef, ok := tt.Find("通用记忆编辑栏")
			if !ok || math.Abs(float64(ef.H-appFooterHeight)) > 1 || math.Abs(float64(ef.Y+ef.H-float32(wh[1]))) > 1 {
				t.Fatalf("common editor footer misaligned: found=%v rect=%+v", ok, ef)
			}

			// Verify project raw-records three-pane aligns identically as well.
			ra := projectUIFixture()
			ra.Config.ClientStateFile = filepath.Join(t.TempDir(), "client.json")
			ra.kind = "原始记录"
			rs := ra.state()
			rs.Loaded = true
			ri := Item{ID: "s1", Dataset: "ds", Kind: "会话", Title: "Raw session", Date: "2026-11-13T10:00:00Z", Session: &Session{ID: "s1", Dataset: "ds"}}
			rs.Items = []Item{ri}
			rs.Selected = &ri
			rtt := ui.NewTester(ra.View, wh[0], wh[1])
			rtt.Advance(600 * time.Millisecond)
			rtt.Frame()
			rdiv, ok := rtt.Find("Divider")
			if !ok || math.Abs(float64(rdiv.X-wantDividerX)) > 1 {
				t.Fatalf("project raw-records divider mismatch: found=%v rect=%+v wantX=%v", ok, rdiv, wantDividerX)
			}
			rlh, ok := rtt.Find("项目列表头部")
			if !ok || math.Abs(float64(rlh.Y-wantListHeadY)) > 1 || math.Abs(float64(rlh.H-wantListHeadH)) > 1 {
				t.Fatalf("project raw-records header mismatch: found=%v rect=%+v", ok, rlh)
			}
			rrb, ok := rtt.Find("项目内容工具栏")
			if !ok || math.Abs(float64(rrb.Y-wantReaderBarY)) > 2 {
				t.Fatalf("project raw-records reader meta bar mismatch: found=%v rect=%+v wantY=%v", ok, rrb, wantReaderBarY)
			}
		})
	}
}
