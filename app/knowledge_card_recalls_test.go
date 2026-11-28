package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestKnowledgeCardRecallsUseExistingReceiptsAndDistinguishUnknown(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/knowledge/recalls" {
			t.Error("card statistics invoked a write or retrieval")
		}
		if r.URL.Query().Get("document_id") == "failed" {
			w.WriteHeader(503)
			return
		}
		count := int64(7)
		if r.URL.Query().Get("document_id") == "zero" {
			count = 0
		}
		json.NewEncoder(w).Encode(KnowledgeRecallStats{Count: count, StartedAt: "2026-11-14T02:00:00Z"})
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.page, a.kind = "知识", "知识"
	a.syncReadingIdentity()
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Loaded = true
	s.Items = []Item{{ID: "hit", Dataset: "ds", Kind: "知识"}, {ID: "zero", Dataset: "ds", Kind: "知识"}, {ID: "failed", Dataset: "ds", Kind: "知识"}}
	a.loadKnowledgeCardRecalls(s)
	drainReading(t, updates, func() bool { return s.Knowledge.RecallPending == 0 })
	for n, want := range []string{"召回 7", "召回 0", "召回 —"} {
		label, tip := knowledgeCardRecall(s.Items[n])
		if n == 2 {
			if tip != "已记录的检索命中次数尚未读到，请稍后重试。" {
				t.Fatal("missing default-value explanation")
			}
		} else {
			wantTip := "已记录的检索命中次数（仅统计已落账的 Agent 检索命中，人工阅读不计入；统计写入失败不阻断召回）。自 " + displayDate("2026-11-14T02:00:00Z") + " 起记录，旧历史不补算。"
			if tip != wantTip {
				t.Fatalf("card %d: tooltip lost full specification: got %q want %q", n, tip, wantTip)
			}
		}
		if label != want {
			t.Fatalf("card %d: %s, want %s", n, label, want)
		}
	}
	a.loadKnowledgeCardRecalls(s)
	if calls.Load() != 3 {
		t.Fatal("repaint retried every statistic")
	}
	s.Items = append(s.Items, Item{ID: "new-card", Dataset: "ds", Kind: "知识"})
	a.loadKnowledgeCardRecalls(s)
	drainReading(t, updates, func() bool { return s.Knowledge.RecallPending == 0 })
	if calls.Load() != 4 {
		t.Fatal("new batch reloaded previously checked statistics")
	}
	a.InvalidateReading("ds")
	if s.Items[0].RecallStats != nil {
		t.Fatal("invalidated card retained statistics")
	}
}

func TestCatalogCountRefreshMaintainsCurrentRecallSort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/knowledge/recall-counts" {
			t.Error("unexpected endpoint", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"started_at": "2026-11-14T02:00:00Z", "items": []map[string]any{{"document_id": "a", "count": 6}, {"document_id": "b", "count": 7}}})
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600)
	a := New(Config{Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.page, a.kind = "知识", "知识"
	a.syncReadingIdentity()
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Loaded = true
	s.Knowledge.SortByRecalls = true
	s.Knowledge.Catalog = []Item{{ID: "a", Dataset: "ds", Kind: "知识", Body: "retained a", BodyLoaded: true, RecallStats: &KnowledgeRecallStats{Count: 6}}, {ID: "b", Dataset: "ds", Kind: "知识", Body: "retained b", BodyLoaded: true, RecallStats: &KnowledgeRecallStats{Count: 5}}}
	s.Items = append([]Item(nil), s.Knowledge.Catalog...)
	a.loadKnowledgeCatalogRecalls(s)
	drainReading(t, updates, func() bool { return s.Knowledge.RecallPending == 0 })
	if s.Items[0].ID != "b" || s.Knowledge.Catalog[0].ID != "b" || s.Items[1].Body != "retained a" {
		t.Fatal("refreshed order/body lost", s.Items)
	}
}
func TestOverviewStatsBatchByDatasetAndCancelOnLeave(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		calls := atomic.Int32{}
		started := make(chan struct{}, 4)
		ended := make(chan struct{}, 4)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Path != "/v1/knowledge/recall-counts" {
				t.Error("overview used per-document endpoint", r.URL.Path)
			}
			started <- struct{}{}
			if cancelled {
				<-r.Context().Done()
				ended <- struct{}{}
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"started_at": "2026-11-14T02:00:00Z", "items": []any{}})
		}))
		token := filepath.Join(t.TempDir(), "token")
		os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600)
		a := New(Config{Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
		a.page = "总览"
		updates := make(chan func(), 10)
		a.Update = func(fn func()) { updates <- fn }
		a.overviewState.Generation = 1
		for i := 0; i < 80; i++ {
			a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: fmt.Sprint(i), Dataset: "ds", Kind: "知识"})
		}
		a.loadOverviewRecallStats(1)
		if cancelled {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("batch never started")
			}
			a.navigate("图谱", 0)
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("leaving overview did not cancel request")
			}
		} else {
			drainReading(t, updates, func() bool { return a.overviewState.Snapshot.Knowledge[79].RecallStats != nil })
		}
		if calls.Load() != 1 {
			t.Fatal("overview expanded requests with row count", calls.Load())
		}
		if a.overviewState.RecallCancel != nil {
			a.overviewState.RecallCancel()
		}
		server.Close()
	}
}

func TestLeavingOverviewBeforeDirectoryCallbackCannotStartStats(t *testing.T) {
	stats := atomic.Int32{}
	releaseActivity := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/activity/pipeline-runs" {
			<-releaseActivity
			json.NewEncoder(w).Encode([]any{})
			return
		}
		if r.URL.Path == "/v1/knowledge/recall-events" {
			json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "more": false})
			return
		}
		if r.URL.Path == "/v1/knowledge/recall-counts" {
			stats.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"started_at": "2026-11-14T02:00:00Z", "items": []any{}})
			return
		}
		if r.URL.Path == "/api/v1/datasets/ds/data" {
			json.NewEncoder(w).Encode([]Document{{ID: "a", Dataset: "ds", Created: "2026-11-16T23:00:00", Metadata: map[string]any{"node_set": []any{"session_learnings"}}}})
			return
		}
		if r.URL.Path == "/api/v1/sessions" {
			json.NewEncoder(w).Encode(map[string]any{"sessions": []any{}, "has_more": false})
			return
		}
		json.NewEncoder(w).Encode([]any{})
	}))
	defer func() { close(releaseActivity); server.Close() }()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600)
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "ds"}}, ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}, nil)
	a.page = "总览"
	a.access.Loaded = true
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	a.loadOverview()
	var late func()
	select {
	case late = <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("directory callback not queued")
	}
	a.navigate("图谱", 0)
	late()
	time.Sleep(30 * time.Millisecond)
	if stats.Load() != 0 {
		t.Fatal("late overview callback started stats after leaving", stats.Load())
	}
}

func TestKnowledgeCardRecallShortLabelAndFullTooltip(t *testing.T) {
	item := Item{
		ID:      "k1",
		Dataset: "ds",
		Kind:    "知识",
		RecallStats: &KnowledgeRecallStats{
			Count:          4,
			StartedAt:      "2026-11-14T02:00:00Z",
			LastRecalledAt: "2026-11-21T08:30:00Z",
		},
	}
	label, tip := knowledgeCardRecall(item)
	if label != "召回 4" {
		t.Fatalf("got label %q, want %q", label, "召回 4")
	}
	for _, want := range []string{
		"已记录的检索命中次数",
		"仅统计已落账的 Agent 检索命中",
		"人工阅读不计入",
		"自 " + displayDate("2026-11-14T02:00:00Z") + " 起记录",
		"最近命中：" + displayDate("2026-11-21T08:30:00Z"),
	} {
		if !strings.Contains(tip, want) {
			t.Fatalf("tooltip %q missing %q", tip, want)
		}
	}
	item.RecallStats = &KnowledgeRecallStats{Count: 0, StartedAt: "2026-11-14T02:00:00Z"}
	if label, tip := knowledgeCardRecall(item); label != "召回 0" || !strings.Contains(tip, "已记录的检索命中次数") {
		t.Fatalf("zero count got label=%q tip=%q", label, tip)
	}
	item.RecallStats = nil
	if label, tip := knowledgeCardRecall(item); label != "召回 —" || tip != "已记录的检索命中次数尚未读到，请稍后重试。" {
		t.Fatalf("nil stats got label=%q tip=%q", label, tip)
	}
}

func TestKnowledgeCardMetadataRowFitsNarrowAndDefaultPane(t *testing.T) {
	makeApp := func(listWidth float32) *App {
		a := projectUIFixture()
		a.layout.ProjectListWidth = listWidth
		a.page, a.kind = "知识", "知识"
		docHit := Document{
			ID:      "hit-location",
			Dataset: "ds",
			Name:    "定位召回与分类渲染",
			Created: "2026-11-21T09:15:00Z",
			Metadata: map[string]any{
				"node_set":    []any{"session_learnings"},
				"session_id":  "codex:s1",
				"lesson_type": "location",
			},
		}
		docZero := Document{
			ID:      "zero-long-cat",
			Dataset: "ds",
			Name:    "超长自定义分类不挤出日期",
			Created: "2026-11-20T10:00:00Z",
			Metadata: map[string]any{
				"node_set":    []any{"session_learnings"},
				"session_id":  "codex:s2",
				"lesson_type": "very_long_unrecognized_category_identifier_name",
			},
		}
		docMissing := Document{
			ID:      "missing-pitfall",
			Dataset: "ds",
			Name:    "缺失召回统计态",
			Created: "2026-11-19T10:00:00Z",
			Metadata: map[string]any{
				"node_set":    []any{"session_learnings"},
				"session_id":  "pi:s3",
				"lesson_type": "pitfall",
			},
		}
		items := []Item{
			{
				ID: docHit.ID, Dataset: "ds", Kind: "知识",
				Title: docHit.Name, Date: docHit.Created, Document: &docHit,
				BodyLoaded: true, Body: "左侧卡片在窄栏下保持紧凑召回标签、分类标签与日期完整显示，不被裁切。",
				ServerUnread: true, ServerUnreadKnown: true,
				RecallStats: &KnowledgeRecallStats{Count: 4, StartedAt: "2026-11-14T00:00:00Z", LastRecalledAt: "2026-11-21T09:15:00Z"},
			},
			{
				ID: docZero.ID, Dataset: "ds", Kind: "知识",
				Title: docZero.Name, Date: docZero.Created, Document: &docZero,
				BodyLoaded: true, Body: "即使出现极长分类标签，分类胶囊也会在左半区内优雅省略，绝不把右侧日期挤出边界。",
				ServerUnread: true, ServerUnreadKnown: true,
				RecallStats: &KnowledgeRecallStats{Count: 0, StartedAt: "2026-11-14T00:00:00Z"},
			},
			{
				ID: docMissing.ID, Dataset: "ds", Kind: "知识",
				Title: docMissing.Name, Date: docMissing.Created, Document: &docMissing,
				BodyLoaded: true, Body: "统计尚未读取完成时显示简洁占位，不撑宽卡片底部元数据行。",
			},
		}
		s := a.state()
		s.Loaded = true
		s.Knowledge.Initialized = true
		s.Knowledge.CountsLoaded = true
		s.Knowledge.SortByRecalls = true
		s.Knowledge.Catalog = items
		s.Items = items
		s.Selected = &items[0]
		return a
	}

	for _, listW := range []float32{360, 300} {
		t.Run(fmt.Sprintf("pane_%0.0f", listW), func(t *testing.T) {
			a := makeApp(listW)
			tt := ui.NewTester(a.View, 1200, 820)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			tt.Advance(time.Second)
			tt.Frame()

			paneRect, ok := tt.Find("项目列表头部")
			if !ok {
				t.Fatal("list pane header missing")
			}
			paneRight := paneRect.X + paneRect.W

			recallRect, ok := tt.Find("召回 4")
			if !ok {
				t.Fatal("compact recall label '召回 4' missing")
			}
			dateHitRect, ok := tt.Find("2026-11-21")
			if !ok {
				t.Fatal("date '2026-11-21' missing")
			}
			dateLongRect, ok := tt.Find("2026-11-20")
			if !ok {
				t.Fatal("date '2026-11-20' missing for extra-long category card")
			}
			catRect, ok := tt.Find("代码位置与关联")
			if !ok {
				t.Fatal("category chip '代码位置与关联' missing")
			}
			sortRect, ok := tt.Find("按召回次数")
			if !ok {
				t.Fatal("short sort label '按召回次数' missing")
			}

			if dateHitRect.W < 55 {
				t.Fatalf("date '2026-11-21' unexpectedly narrow/truncated: %+v", dateHitRect)
			}
			if dateHitRect.X+dateHitRect.W > paneRight+0.5 {
				t.Fatalf("date '2026-11-21' overflowed list pane right edge %.1f: %+v", paneRight, dateHitRect)
			}
			if dateLongRect.W < 55 || dateLongRect.X+dateLongRect.W > paneRight+0.5 {
				t.Fatalf("date '2026-11-20' clipped or overflowed with long category (paneRight=%.1f): %+v", paneRight, dateLongRect)
			}
			if recallRect.X+recallRect.W > dateHitRect.X-4 {
				t.Fatalf("recall chip %+v squeezed/overlapped date %+v", recallRect, dateHitRect)
			}
			if catRect.X+catRect.W > dateHitRect.X-4 {
				t.Fatalf("category chip %+v squeezed/overlapped date %+v", catRect, dateHitRect)
			}
			if sortRect.X+sortRect.W > paneRight+0.5 {
				t.Fatalf("sort dropdown %+v overflowed list pane right edge %.1f", sortRect, paneRight)
			}

			// Verify filter toolbar still keeps sort trigger inside pane even with longest category selected.
			a.state().Knowledge.CategoryFilter = "代码位置与关联"
			a.reorderKnowledge(a.state())
			tt.Frame()
			sortBtnRect, ok := tt.Find("记忆排序")
			if !ok {
				t.Fatal("sort dropdown button missing after selecting long category")
			}
			if sortBtnRect.X+sortBtnRect.W > paneRight+0.5 {
				t.Fatalf("sort dropdown button %+v overflowed paneRight %.1f with long category filter", sortBtnRect, paneRight)
			}
		})
	}
}
