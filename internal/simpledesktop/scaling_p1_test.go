package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func p1TestTokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manager.token")
	if err := os.WriteFile(p, []byte("synthetic-p1-management-token-0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func p1TestApp(t *testing.T, serviceURL, controlURL, tokenFile string) (*App, chan func()) {
	t.Helper()
	stateFile := filepath.Join(t.TempDir(), "client-state.json")
	cfg := Config{
		ServiceURL:      serviceURL,
		ClientStateFile: stateFile,
		ProjectControl:  &ProjectControlConfig{URL: controlURL, ManagementTokenFile: tokenFile},
		Projects:        []Project{{ID: "p1", Name: "memlane", Path: "/synthetic/memlane", Dataset: "ds-1"}},
		Common:          []Project{{ID: "common-1", Name: "通用记忆", Dataset: "ds-common"}},
	}
	a := New(cfg, nil)
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.access.Directory = AccessDirectory{
		Projects: []AccessProject{{ID: "p1", Name: "memlane", Dataset: "ds-1", Status: "verified"}},
		Common:   &AccessProject{ID: "common-1", Name: "通用记忆", Dataset: "ds-common", Status: "verified"},
	}
	a.service = "已连接"
	updates := make(chan func(), 256)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(func() { a.cancelAndResetPanes() })
	return a, updates
}

func TestScalingP1BodyFastpathAndPreviewDecoupling(t *testing.T) {
	token := p1TestTokenFile(t)
	var bodyFastCalls, officialRawCalls, markReadCalls atomic.Int32
	var markedDocIDs []string
	var markMu sync.Mutex

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureLocalBodyFastpath, v2FeatureKnowledgeCursor, v2FeatureReadingState},
			})
		case "/v1/projects/p1/knowledge":
			if r.Header.Get(v2HeaderReadingIdentity) == "" {
				t.Errorf("missing %s header on knowledge v2", v2HeaderReadingIdentity)
			}
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID:      "p1",
				DatasetID:      "ds-1",
				CatalogVersion: "v1",
				TotalFiltered:  3,
				UnreadCount:    2,
				CategoryCounts: map[string]int{"项目约定": 3},
				Items: []v2KnowledgeItem{
					{
						DocID:            "doc-1",
						DatasetID:        "ds-1",
						ProjectID:        "p1",
						Title:            "预览标题一",
						DocName:          "k1.md",
						EffectiveDate:    "2026-11-21",
						CreatedAt:        "2026-11-21T10:00:00Z",
						UpdatedAt:        "2026-11-21T10:00:00Z",
						ContentHash:      "hash-1",
						Kind:             "knowledge",
						Category:         "项目约定",
						LessonType:       "convention",
						SessionID:        "codex_s1",
						RecallCount:      3,
						IsUnread:         true,
						PreviewText:      "服务端截断预览文本，绝对不能当作完整正文或触发已读。",
						PreviewTruncated: true,
					},
					{
						DocID:            "doc-denied",
						DatasetID:        "ds-1",
						ProjectID:        "p1",
						Title:            "受限文档",
						EffectiveDate:    "2026-11-20",
						CreatedAt:        "2026-11-20T10:00:00Z",
						ContentHash:      "hash-denied",
						Kind:             "knowledge",
						Category:         "项目约定",
						LessonType:       "convention",
						PreviewText:      "受限预览",
						PreviewTruncated: true,
					},
					{
						DocID:            "doc-trunc",
						DatasetID:        "ds-1",
						ProjectID:        "p1",
						Title:            "超大截断文档",
						DocName:          "k-trunc.md",
						EffectiveDate:    "2026-11-19",
						CreatedAt:        "2026-11-19T10:00:00Z",
						ContentHash:      "hash-trunc",
						Kind:             "knowledge",
						Category:         "项目约定",
						LessonType:       "convention",
						IsUnread:         true,
						PreviewText:      "超大文档预览",
						PreviewTruncated: true,
					},
				},
			})
		case "/v1/datasets/ds-1/documents/doc-1/body":
			bodyFastCalls.Add(1)
			bodyText := "# Session learning (session codex_s1)\n\n这是完整的正文内容。"
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID:          "doc-1",
				DatasetID:   "ds-1",
				Body:        &bodyText,
				MIMEType:    "text/markdown",
				ContentHash: "hash-1",
				Truncated:   false,
				BytesTotal:  int64(len(bodyText)),
				Source:      "local_fastpath",
			})
		case "/v1/datasets/ds-1/documents/doc-trunc/body":
			bodyFastCalls.Add(1)
			bodyText := "# Session learning (session codex_s1)\n\n这是完整的 8MiB 截断标记测试正文内容。"
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID:          "doc-trunc",
				DatasetID:   "ds-1",
				Body:        &bodyText,
				MIMEType:    "text/markdown",
				ContentHash: "hash-trunc",
				Truncated:   true,
				BytesTotal:  9 * 1024 * 1024,
				Source:      "local_fastpath",
			})
		case "/v1/datasets/ds-1/documents/doc-denied/body":
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "document_path_denied"})
		case "/v1/reading-state/mark-read":
			markReadCalls.Add(1)
			var req v2ReadingStateMarkReadRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			markMu.Lock()
			markedDocIDs = append(markedDocIDs, req.DocIDs...)
			markMu.Unlock()
			_ = json.NewEncoder(w).Encode(v2ReadingStateMarkReadResponse{
				ScopeHash:   "h1",
				Marked:      len(req.DocIDs),
				UnreadCount: 0,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	cognee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/raw") {
			officialRawCalls.Add(1)
		}
		http.Error(w, "unexpected official call: "+r.URL.Path, http.StatusInternalServerError)
	}))
	defer cognee.Close()

	a, updates := p1TestApp(t, cognee.URL, control.URL, token)
	a.navigate("知识", 0)
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading && !s.Reading })

	if !s.Knowledge.V2Mode {
		t.Fatalf("expected V2Mode enabled on project knowledge pane")
	}
	if len(s.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(s.Items))
	}
	// Item 1 (unselected) must strictly keep Preview decoupled from Body / BodyLoaded!
	unselected := s.Items[1]
	if unselected.Preview == "" || unselected.Body != "" || unselected.BodyLoaded {
		t.Fatalf("preview/body decoupling violated on unselected item: preview=%q body=%q loaded=%v", unselected.Preview, unselected.Body, unselected.BodyLoaded)
	}
	// Flush any pending mark-read from auto-selected doc-1 first, then assert
	// trying to mark an unloaded item read is a strict NO-OP.
	a.waitKnowledgeReadFlush()
	beforeMarks := markReadCalls.Load()
	a.markKnowledgeRead(unselected)
	a.waitKnowledgeReadFlush()
	if markReadCalls.Load() != beforeMarks {
		t.Fatalf("markKnowledgeRead fired for BodyLoaded==false item!")
	}
	// Selected item 0 loaded via /v1/.../body fastpath (0 official /raw calls) and triggered mark-read.
	if bodyFastCalls.Load() != 1 || officialRawCalls.Load() != 0 {
		t.Fatalf("expected 1 fastpath body call and 0 official /raw calls, got fast=%d official=%d", bodyFastCalls.Load(), officialRawCalls.Load())
	}
	if s.Selected == nil || !s.Selected.BodyLoaded || s.Selected.BodyTruncated {
		t.Fatalf("expected selected item loaded & non-truncated via fastpath, got %+v", s.Selected)
	}
	a.waitKnowledgeReadFlush()
	drainReading(t, updates, func() bool { return true })
	markMu.Lock()
	gotMarked := append([]string(nil), markedDocIDs...)
	markMu.Unlock()
	if len(gotMarked) != 1 || gotMarked[0] != "doc-1" {
		t.Fatalf("expected mark-read for doc-1 after BodyLoaded=true && !BodyTruncated, got %v", gotMarked)
	}

	// Selecting a truncated (>8 MiB) item loads via fastpath, renders the truncation notice
	// in both Light and Dark modes, and MUST NOT trigger markKnowledgeRead!
	a.selectItem(s, s.Items[2])
	drainReading(t, updates, func() bool { return !s.Reading && s.Selected != nil && s.Selected.ID == "doc-trunc" })
	if s.Selected == nil || !s.Selected.BodyLoaded || !s.Selected.BodyTruncated {
		t.Fatalf("expected truncated item loaded with BodyTruncated=true, got %+v", s.Selected)
	}
	a.waitKnowledgeReadFlush()
	markMu.Lock()
	gotMarkedAfterTrunc := append([]string(nil), markedDocIDs...)
	markMu.Unlock()
	if len(gotMarkedAfterTrunc) != 1 || gotMarkedAfterTrunc[0] != "doc-1" {
		t.Fatalf("truncated body MUST NOT mark read, got marked=%v", gotMarkedAfterTrunc)
	}

	// Render both Light and Dark modes to verify preview card & truncation notice UI build cleanly.
	for _, pref := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(pref)
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		if !tt.HasText("正文超过 8 MiB，已截断显示") {
			t.Fatalf("[%s] missing 8MiB truncation notice in reader", pref)
		}
	}

	// Fail-Closed check: 403 document_path_denied must NEVER fall back to Cognee /raw.
	_, err := a.Client.read(context.Background(), unselected)
	if !isControlErrorCode(err, http.StatusForbidden, "document_path_denied") {
		t.Fatalf("expected 403 document_path_denied Fail-Closed error, got %v", err)
	}
	if officialRawCalls.Load() != 0 {
		t.Fatalf("403 document_path_denied leaked fallback to official /raw (%d calls)", officialRawCalls.Load())
	}
}

func TestScalingP1OverviewV2BootstrapAndDayDrilldown(t *testing.T) {
	token := p1TestTokenFile(t)
	today := time.Now().Format("2006-01-02")
	var dayCalls atomic.Int32
	var rebuildFail atomic.Bool

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureOverview},
			})
		case "/v1/overview/bootstrap":
			if rebuildFail.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "catalog_index_rebuilding"})
				return
			}
			if r.Header.Get(v2HeaderReadingIdentity) == "" {
				t.Errorf("missing reading identity header on /v1/overview/bootstrap")
			}
			if al := r.URL.Query().Get("activity_limit"); al != "50" {
				t.Errorf("expected activity_limit=50 (<=50), got %q", al)
			}
			_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
				Generation:          42,
				LocalTZName:         "Asia/Shanghai",
				TotalKnowledgeCount: 50000,
				DayBuckets: []v2OverviewDayBucket{
					{Day: today, Count: 17},
					{Day: "2026-11-20", Count: 35},
				},
				Projects: []v2OverviewProjectSummary{
					{ProjectID: "p1", DatasetID: "ds-1", KnowledgeCount: 50000, RawDocCount: 1200, UnreadCount: 14, CatalogVersion: "cv-9"},
				},
				RecentItems: []v2KnowledgeItem{
					{DocID: "r1", DatasetID: "ds-1", ProjectID: "p1", Title: "最近记忆一", EffectiveDate: today, CreatedAt: today + "T08:00:00Z", Kind: "knowledge", PreviewText: "摘要一", IsUnread: true, RecallCount: 2},
				},
				RecallStartedAt: "2026-10-01T00:00:00Z",
			})
		case "/v1/overview/day":
			dayCalls.Add(1)
			cursor := r.URL.Query().Get("cursor")
			if cursor == "" {
				_ = json.NewEncoder(w).Encode(v2OverviewDayResponse{
					Day:        r.URL.Query().Get("day"),
					Generation: 42,
					TotalCount: 35,
					Items: []v2KnowledgeItem{
						{DocID: "d-p1", DatasetID: "ds-1", ProjectID: "p1", Title: "某日第1条", EffectiveDate: "2026-11-20", Kind: "knowledge", PreviewText: "页1摘要"},
					},
					NextCursor: "day-c2",
					HasMore:    true,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(v2OverviewDayResponse{
				Day:        r.URL.Query().Get("day"),
				Generation: 42,
				TotalCount: 35,
				Items: []v2KnowledgeItem{
					{DocID: "d-p2", DatasetID: "ds-1", ProjectID: "p1", Title: "某日第2条", EffectiveDate: "2026-11-20", Kind: "knowledge", PreviewText: "页2摘要"},
				},
				NextCursor: "",
				HasMore:    false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	a, updates := p1TestApp(t, control.URL, control.URL, token)
	a.loadOverview()
	drainReading(t, updates, func() bool { return a.overviewState.Loaded && !a.overviewState.Loading })

	if !a.overviewState.V2Mode || a.overviewState.V2TotalKnowledge != 50000 || a.overviewState.Snapshot.Raw != 1200 {
		t.Fatalf("unexpected overview v2 state: v2=%v total=%d raw=%d", a.overviewState.V2Mode, a.overviewState.V2TotalKnowledge, a.overviewState.Snapshot.Raw)
	}
	if got := a.trayTodayMemoryCount(time.Now()); got != 17 {
		t.Fatalf("expected trayTodayMemoryCount=17 from V2DayBuckets, got %d", got)
	}
	if got := a.projectUnread(a.Config.Projects[0]); got != 14 {
		t.Fatalf("expected sidebar unread=14 from overview v2 project summary, got %d", got)
	}

	// Day drilldown page 1 + cursor page 2.
	a.selectOverviewDay("2026-11-20")
	drainReading(t, updates, func() bool { return !a.overviewState.DayLoading && len(a.overviewState.DayItems) == 1 })
	if a.overviewState.DayTotal != 35 || !a.overviewState.DayHasMore || a.overviewState.DayNextCursor != "day-c2" {
		t.Fatalf("unexpected day page 1 state: total=%d more=%v cursor=%q", a.overviewState.DayTotal, a.overviewState.DayHasMore, a.overviewState.DayNextCursor)
	}
	a.loadOverviewDayV2("2026-11-20", true)
	drainReading(t, updates, func() bool { return !a.overviewState.DayLoading && len(a.overviewState.DayItems) == 2 })
	if a.overviewState.DayHasMore || dayCalls.Load() != 2 {
		t.Fatalf("expected 2 day cursor calls and DayHasMore=false, got calls=%d more=%v", dayCalls.Load(), a.overviewState.DayHasMore)
	}

	// 503 catalog_index_rebuilding must Fail-Closed and NEVER fall back to /v1/overview/bootstrap.
	rebuildFail.Store(true)
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading })
	if !strings.Contains(a.overviewState.Error, "索引正在构建中") {
		t.Fatalf("expected rebuilding user error, got %q", a.overviewState.Error)
	}

}

func TestScalingP1ProjectKnowledgeCursorV2AndSlidingWindow(t *testing.T) {
	token := p1TestTokenFile(t)
	var sawRecallAgent, sawRecallEvent string
	var mu sync.Mutex

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureKnowledgeCursor, v2FeatureLocalBodyFastpath},
			})
		case "/v1/projects/p1/knowledge":
			q := r.URL.Query()
			mu.Lock()
			if q.Get("recall_agent_id") != "" {
				sawRecallAgent = q.Get("recall_agent_id")
				sawRecallEvent = q.Get("recall_event_id")
			}
			mu.Unlock()
			cursor := q.Get("cursor")
			if cursor == "bad-cursor" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_cursor"})
				return
			}
			pageIdx := 0
			if cursor != "" {
				_, _ = fmt.Sscanf(cursor, "p-%d", &pageIdx)
			}
			items := make([]v2KnowledgeItem, 0, 30)
			for i := 0; i < 30; i++ {
				id := fmt.Sprintf("k-%04d", pageIdx*30+i)
				items = append(items, v2KnowledgeItem{
					DocID:         id,
					DatasetID:     "ds-1",
					ProjectID:     "p1",
					Title:         "记忆 " + id,
					EffectiveDate: "2026-11-21",
					Kind:          "knowledge",
					Category:      "踩坑经验",
					LessonType:    "pitfall",
					PreviewText:   "预览内容 " + id,
					RecallCount:   int64(i),
				})
			}
			nextCursor := ""
			hasMore := pageIdx < 14
			if hasMore {
				nextCursor = fmt.Sprintf("p-%d", pageIdx+1)
			}
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID:      "p1",
				DatasetID:      "ds-1",
				CatalogVersion: "v10",
				Sort:           q.Get("sort"),
				TotalFiltered:  450,
				UnreadCount:    5,
				CategoryCounts: map[string]int{"踩坑经验": 300, "项目约定": 150},
				Items:          items,
				NextCursor:     nextCursor,
				HasMore:        hasMore,
			})
		case "/v1/datasets/ds-1/documents/k-0000/body":
			bodyText := "完整正文 k-0000"
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "k-0000", DatasetID: "ds-1", Body: &bodyText, Source: "local_fastpath",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	a, updates := p1TestApp(t, control.URL, control.URL, token)
	a.navigate("知识", 0)
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading && !s.Reading })

	if len(s.Knowledge.Catalog) != 0 {
		t.Fatalf("expected empty client Catalog in V2Mode, got %d", len(s.Knowledge.Catalog))
	}
	baseTotal, counts := a.knowledgeCategoryCounts(s)
	if baseTotal != 450 || counts["踩坑经验"] != 300 {
		t.Fatalf("unexpected server category counts: total=%d counts=%v", baseTotal, counts)
	}

	// Seed per-item measurement & scroll state for early items to verify eviction.
	s.Knowledge.Measurements = map[string]knowledgeCardMeasurement{s.Items[0].key(): {Height: 80}}
	s.Knowledge.Columns = map[string]int{s.Items[0].key(): 0}
	firstKey := s.Items[0].key()
	s.Selected = nil // allow eviction of firstKey when window slides past 300

	for page := 1; page < 15; page++ {
		a.appendKnowledgeBatch(s)
		drainReading(t, updates, func() bool { return !s.Knowledge.Appending })
	}
	if len(s.Items) != maxActiveListItems {
		t.Fatalf("expected sliding window cap %d, got %d", maxActiveListItems, len(s.Items))
	}
	if s.Items[len(s.Items)-1].ID != "k-0449" {
		t.Fatalf("expected tail item k-0449, got %s", s.Items[len(s.Items)-1].ID)
	}
	if _, stillMeasured := s.Knowledge.Measurements[firstKey]; stillMeasured {
		t.Fatalf("expected evicted item %s pruned from Measurements", firstKey)
	}

	// Verify 400 invalid_cursor explicitly resets to page 1 in V2Mode (never falling back to legacy).
	s.More = true
	s.Knowledge.NextCursor = "bad-cursor"
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, func() bool {
		return !s.Knowledge.Appending && !s.Loading && len(s.Items) == 30 && s.Items[0].ID == "k-0000"
	})
	if !s.Knowledge.V2Mode || s.Error != "" {
		t.Fatalf("expected invalid_cursor to reset cleanly to page 1 in V2Mode, v2=%v err=%q", s.Knowledge.V2Mode, s.Error)
	}

	// Verify openRecalledCards sends paired recall_agent_id + recall_event_id.
	a.openRecalledCards(RecallEvent{
		ID:      "ev-99",
		AgentID: "ag-codex",
		Hits:    []RecallHit{{Dataset: "ds-1", Document: "k-0000"}},
	}, "ds-1")
	s = a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading })
	mu.Lock()
	gotAgent, gotEvent := sawRecallAgent, sawRecallEvent
	mu.Unlock()
	if gotAgent != "ag-codex" || gotEvent != "ev-99" {
		t.Fatalf("expected paired recall_agent_id/recall_event_id, got agent=%q event=%q", gotAgent, gotEvent)
	}
}

func TestScalingP1CommonKnowledgeCursorV2AndO1DeleteVerify(t *testing.T) {
	token := p1TestTokenFile(t)
	var overviewDocsCalls, lookupCalls atomic.Int32
	deleted := false

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureCommonKnowledgeCursor, "document_lookup_v2"},
			})
		case "/v1/common/knowledge":
			cursor := r.URL.Query().Get("cursor")
			if cursor == "" {
				_ = json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{
					DatasetID:      "ds-common",
					CommonCategory: r.URL.Query().Get("common_category"),
					TotalFiltered:  2,
					Items: []v2KnowledgeItem{
						{DocID: "c-1", DatasetID: "ds-common", Title: "偏好1", CommonCategory: "preference", PreviewText: "偏好预览1", EffectiveDate: "2026-11-21"},
					},
					NextCursor: "c-page-2",
					HasMore:    true,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{
				DatasetID:      "ds-common",
				CommonCategory: r.URL.Query().Get("common_category"),
				TotalFiltered:  2,
				Items: []v2KnowledgeItem{
					{DocID: "c-2", DatasetID: "ds-common", Title: "偏好2", CommonCategory: "preference", PreviewText: "偏好预览2", EffectiveDate: "2026-11-20"},
				},
				NextCursor: "",
				HasMore:    false,
			})
		case "/v1/datasets/ds-common/documents/c-1":
			lookupCalls.Add(1)
			if deleted {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "document_not_found"})
				return
			}
			_ = json.NewEncoder(w).Encode(documentLookupV2Response{
				Document: Document{ID: "c-1", Dataset: "ds-common", Name: "c1.md"},
			})
		default:
			if strings.Contains(r.URL.Path, "/api/v1/datasets/") {
				overviewDocsCalls.Add(1)
			}
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	a, updates := p1TestApp(t, control.URL, control.URL, token)
	a.navigate("通用知识", 0)
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading })

	if !s.CommonV2Mode || len(s.Items) != 1 || s.Items[0].ID != "c-1" || s.Items[0].BodyLoaded {
		t.Fatalf("unexpected common v2 page 1: mode=%v items=%+v", s.CommonV2Mode, s.Items)
	}
	// Cursor forward to page 2.
	s.CommonCursors = []string{"", s.CommonNextCursor}
	s.CommonPageIndex = 1
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading && len(s.Items) == 1 && s.Items[0].ID == "c-2" })

	// O(1) delete verification must use /v1/datasets/ds-common/documents/c-1 (0 overviewDocuments calls).
	deleted = true
	a.commonEdit.DeleteSubmitted = true
	a.commonEdit.Delete = &Document{ID: "c-1", Dataset: "ds-common"}
	a.commonEdit.DeleteTarget = a.Config.Common[0]
	a.confirmCommonDeletion()
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })
	if lookupCalls.Load() != 1 || overviewDocsCalls.Load() != 0 {
		t.Fatalf("expected O(1) lookup=1 and overviewDocuments=0, got lookup=%d overview=%d", lookupCalls.Load(), overviewDocsCalls.Load())
	}
	if a.commonEdit.Delete != nil || !strings.Contains(a.commonEdit.Message, "已核对") {
		t.Fatalf("expected confirmed deletion, got msg=%q delete=%v", a.commonEdit.Message, a.commonEdit.Delete)
	}
}

func TestScalingP1ThreeLaneProgressiveSearchAndLexicalSoftTimeout(t *testing.T) {
	prevTimeout := lexicalSearchSoftTimeout
	lexicalSearchSoftTimeout = 80 * time.Millisecond
	t.Cleanup(func() { lexicalSearchSoftTimeout = prevTimeout })

	token := p1TestTokenFile(t)
	var recentV2Calls, metaCalls, semanticCalls, lexicalCalls, overviewDocsCalls atomic.Int32
	semanticGate := make(chan struct{})

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureSearch},
			})
		case "/v1/search/recent":
			recentV2Calls.Add(1)
			_ = json.NewEncoder(w).Encode(v2SearchRecentResponse{
				Generation: 1,
				KnowledgeItems: []v2KnowledgeItem{
					{DocID: "rec-k1", DatasetID: "ds-1", ProjectID: "p1", Title: "最近知识", EffectiveDate: "2026-11-21", Kind: "knowledge", PreviewText: "最近知识预览"},
				},
				SessionItems: []v2SearchSessionItem{
					{SessionID: "codex_rec1", DatasetID: "ds-1", ProjectID: "p1", Label: "最近会话主题", MsgCount: 4, ToolCalls: 2, LastActivityAt: "2026-11-21T09:00:00Z"},
				},
			})
		case "/v1/search/metadata":
			metaCalls.Add(1)
			_ = json.NewEncoder(w).Encode(v2SearchMetadataResponse{
				Query:      r.URL.Query().Get("q"),
				Generation: 1,
				Items: []v2KnowledgeItem{
					{DocID: "meta-1", DatasetID: "ds-1", ProjectID: "p1", Title: "WAL 模式配置", EffectiveDate: "2026-11-21", Kind: "knowledge", PreviewText: "SQLite WAL 配置要点", MatchField: "title"},
					{DocID: "meta-raw-1", DatasetID: "ds-1", ProjectID: "p1", Title: "WAL 原始文档", EffectiveDate: "2026-11-21", Kind: "raw_doc", PreviewText: "raw doc preview", MatchField: "doc_name"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	cognee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/data") {
			overviewDocsCalls.Add(1)
		}
		if r.URL.Path == "/api/v1/search" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["search_type"] == "CHUNKS_LEXICAL" {
				lexicalCalls.Add(1)
				select {
				case <-r.Context().Done():
					return
				case <-time.After(300 * time.Millisecond):
					_ = json.NewEncoder(w).Encode([]any{})
				}
				return
			}
			semanticCalls.Add(1)
			<-semanticGate
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"dataset_id": "ds-1",
					"search_result": []map[string]any{
						{"id": "sem-chunk-1", "text": "语义命中 WAL 相关讨论", "document_id": "doc-sem-1", "metadata": map[string]any{"node_set": []any{"session_learnings"}}},
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer cognee.Close()

	a, updates := p1TestApp(t, cognee.URL, control.URL, token)
	// 1. Empty query hits /v1/search/recent (0 overviewDocuments calls).
	a.openUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if recentV2Calls.Load() != 1 || overviewDocsCalls.Load() != 0 || len(a.visibleSearchRows()) != 2 {
		t.Fatalf("expected /v1/search/recent=1 overview=0 rows=2, got recent=%d overview=%d rows=%d", recentV2Calls.Load(), overviewDocsCalls.Load(), len(a.visibleSearchRows()))
	}

	// 2. Non-empty query renders Lane 1 (metadata) progressively while semantic & lexical are still running!
	a.searchUI.Query = "WAL"
	a.submitUnifiedSearch()
	drainReading(t, updates, func() bool { return len(a.visibleSearchRows()) >= 1 })
	if !a.searchUI.Loading {
		t.Fatalf("expected Loading=true while semantic & lexical lanes are still in flight")
	}
	if a.visibleSearchRows()[0].ID != "meta-1" {
		t.Fatalf("expected progressive Lane 1 metadata hit meta-1 first, got %+v", a.visibleSearchRows())
	}

	// Unblock semantic lane; lexical lane will exceed 80ms soft timeout and yield non-blocking hint.
	close(semanticGate)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	if metaCalls.Load() != 1 || semanticCalls.Load() != 1 || lexicalCalls.Load() != 1 {
		t.Fatalf("expected 3 search lanes fired, got meta=%d sem=%d lex=%d", metaCalls.Load(), semanticCalls.Load(), lexicalCalls.Load())
	}
	if len(a.visibleSearchRows()) < 3 {
		t.Fatalf("expected merged metadata (knowledge+raw_doc) + semantic rows >=3, got %d", len(a.visibleSearchRows()))
	}
	foundRawDoc := false
	for _, row := range a.visibleSearchRows() {
		if row.ID == "meta-raw-1" {
			foundRawDoc = true
			if row.Kind != "文档" {
				t.Fatalf("expected raw_doc mapped to Kind=文档, got %q", row.Kind)
			}
		}
	}
	if !foundRawDoc {
		t.Fatalf("missing raw_doc metadata search hit meta-raw-1")
	}
	if !a.searchUI.Lanes.KeywordTimedOut || !strings.Contains(a.searchUI.Error, "词法全文检索超时") {
		t.Fatalf("expected non-blocking lexical soft-timeout hint, got timedOut=%v err=%q", a.searchUI.Lanes.KeywordTimedOut, a.searchUI.Error)
	}
}

func TestScalingP1RawRecordsCursorV2AndSubagentChildrenPagination(t *testing.T) {
	token := p1TestTokenFile(t)
	var childCalls atomic.Int32

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureRawRecords},
			})
		case "/v1/projects/p1/raw-records":
			cursor := r.URL.Query().Get("cursor")
			pageIdx := 0
			if cursor != "" {
				_, _ = fmt.Sscanf(cursor, "raw-%d", &pageIdx)
			}
			items := make([]v2RawRecordItem, 0, 30)
			for i := 0; i < 30; i++ {
				sid := fmt.Sprintf("codex_s-%04d", pageIdx*30+i)
				entry := &v2RawSessionEntry{
					Session:  Session{ID: sid, Dataset: "ds-1", User: "u1", Agent: "codex", Updated: "2026-11-21T10:00:00Z"},
					Label:    "会话 " + sid,
					MsgCount: 3,
				}
				if pageIdx == 0 && i == 0 {
					entry.ChildCount = 7
					entry.ChildrenHasMore = true
					for cIdx := 0; cIdx < 5; cIdx++ {
						entry.ChildrenPreview = append(entry.ChildrenPreview, rawSessionEntry{
							Session: Session{ID: fmt.Sprintf("codex_sub-%d", cIdx), Dataset: "ds-1", IsSubagent: true, ParentSessionID: sid, Updated: "2026-11-21T10:00:00Z"},
							Label:   fmt.Sprintf("子会话 %d", cIdx),
						})
					}
				}
				items = append(items, v2RawRecordItem{ItemKind: "session", SortTSNS: int64(10000 - (pageIdx*30 + i)), Session: entry})
			}
			nextCursor := ""
			hasMore := pageIdx < 11
			if hasMore {
				nextCursor = fmt.Sprintf("raw-%d", pageIdx+1)
			}
			_ = json.NewEncoder(w).Encode(v2RawRecordsResponse{
				ProjectID:  "p1",
				DatasetID:  "ds-1",
				Kind:       "all",
				CacheReady: true,
				Items:      items,
				NextCursor: nextCursor,
				HasMore:    hasMore,
			})
		case "/v1/projects/p1/raw-records/sessions/codex_s-0000/children":
			childCalls.Add(1)
			_ = json.NewEncoder(w).Encode(v2SessionChildrenResponse{
				ProjectID:       "p1",
				DatasetID:       "ds-1",
				ParentSessionID: "codex_s-0000",
				TotalCount:      7,
				Items: []rawSessionEntry{
					{Session: Session{ID: "codex_sub-5", Dataset: "ds-1", IsSubagent: true, ParentSessionID: "codex_s-0000"}, Label: "子会话 5"},
					{Session: Session{ID: "codex_sub-6", Dataset: "ds-1", IsSubagent: true, ParentSessionID: "codex_s-0000"}, Label: "子会话 6"},
				},
				HasMore: false,
			})
		case "/v1/raw-records/session-history":
			_ = json.NewEncoder(w).Encode(SessionHistoryPage{
				Paging: true, Limit: 20,
				SessionDetail: SessionDetail{
					Session: Session{ID: "codex_s-0000", Dataset: "ds-1", User: "u1", Updated: "2026-11-21T10:00:00Z"},
					Label:   "会话 codex_s-0000",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	a, updates := p1TestApp(t, control.URL, control.URL, token)
	a.navigateKind("知识", 0, "原始记录")
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading && !s.Reading })

	if !s.RawV2Mode || len(s.Items) != 30 || len(s.Items[0].Subagents) != 5 || !s.Items[0].SubagentsHasMore {
		t.Fatalf("unexpected raw records v2 page 1: mode=%v len=%d sub=%d hasMore=%v", s.RawV2Mode, len(s.Items), len(s.Items[0].Subagents), s.Items[0].SubagentsHasMore)
	}
	// Load more subagent children via /children.
	a.loadMoreSubagents(s, *s.Selected)
	drainReading(t, updates, func() bool { return s.Selected != nil && !s.Selected.SubagentsLoading })
	if childCalls.Load() != 1 || len(s.Selected.Subagents) != 7 || s.Selected.SubagentsHasMore {
		t.Fatalf("expected 7 children after /children call, got calls=%d len=%d more=%v", childCalls.Load(), len(s.Selected.Subagents), s.Selected.SubagentsHasMore)
	}

	// Paginate 11 more pages (360 total) and assert <=300 sliding window.
	for page := 1; page < 12; page++ {
		a.appendRawRecords(s)
		drainReading(t, updates, func() bool { return !s.RawAppending })
	}
	if len(s.Items) != maxActiveListItems {
		t.Fatalf("expected raw records sliding window %d, got %d", maxActiveListItems, len(s.Items))
	}
}

func TestScalingP1ReadingStateMigrationUnreadPopupAndNonDisruptiveRefresh(t *testing.T) {
	token := p1TestTokenFile(t)
	var migrateLocalCalls, beginCalls, chunkCalls, commitCalls, markAllCalls atomic.Int32
	var stagedItems int
	var committedBatchID string
	var catalogVersion atomic.Value
	catalogVersion.Store("cv-1")
	var knowledgeV2Calls atomic.Int32
	var sawUnreadOnly atomic.Bool

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureReadingState, v2FeatureMessageSummary, v2FeatureKnowledgeCursor},
			})
		case "/v1/reading-state/migrate-local":
			migrateLocalCalls.Add(1)
			// Simulate custom ClientStateFile dir returning Python's exact no_legacy_file -> triggers 2-phase staging!
			_ = json.NewEncoder(w).Encode(v2ReadingStateMigrateResponse{
				Migrated: []v2ReadingStateMigratedScope{
					{ProjectID: "p1", DatasetID: "ds-1", Status: "no_legacy_file"},
				},
			})
		case "/v1/reading-state/import/begin":
			beginCalls.Add(1)
			var raw map[string]any
			_ = json.NewDecoder(r.Body).Decode(&raw)
			if len(raw) != 5 {
				t.Errorf("import/begin must send exact 5 keys, got %v", raw)
			}
			for _, k := range []string{"reading_identity", "project_id", "dataset_id", "initialized", "enabled_at"} {
				if _, ok := raw[k]; !ok {
					t.Errorf("import/begin missing key %q", k)
				}
			}
			_ = json.NewEncoder(w).Encode(v2ReadingStateImportBeginResponse{BatchID: "batch-1", ScopeHash: "sh-1"})
		case "/v1/reading-state/import/chunk":
			chunkCalls.Add(1)
			var req v2ReadingStateImportChunkRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.BatchID != "batch-1" {
				t.Errorf("import/chunk unexpected batch_id=%q", req.BatchID)
			}
			if len(req.Items) > 500 || len(req.Items) == 0 {
				t.Errorf("chunk size out of range (1..500): %d", len(req.Items))
			}
			stagedItems += len(req.Items)
			_ = json.NewEncoder(w).Encode(v2ReadingStateImportChunkResponse{BatchID: req.BatchID, Staged: len(req.Items)})
		case "/v1/reading-state/import/commit":
			commitCalls.Add(1)
			var req v2ReadingStateImportCommitRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			committedBatchID = req.BatchID
			if req.ExpectedItems == nil || *req.ExpectedItems != stagedItems {
				t.Errorf("import/commit expected_items=%v want=%d", req.ExpectedItems, stagedItems)
			}
			_ = json.NewEncoder(w).Encode(v2ReadingStateImportCommitResponse{ScopeHash: "sh-1", Committed: stagedItems, UnreadCount: 25})
		case "/v1/reading-state/mark-all-read":
			markAllCalls.Add(1)
			_ = json.NewEncoder(w).Encode(v2ReadingStateMarkAllReadResponse{ScopeHash: "sh-1", UnreadCount: 0, ReadHorizonAtNS: "2026-11-21T10:00:00.000000000Z"})
		case "/v1/projects/p1/message-summary":
			previews := make([]v2UnreadPreviewItem, 0, 10)
			for i := 0; i < 10; i++ {
				previews = append(previews, v2UnreadPreviewItem{
					DocID:         fmt.Sprintf("u-%d", i),
					Title:         fmt.Sprintf("未读预览 %d", i),
					EffectiveDate: "2026-11-21",
					CreatedAt:     "2026-11-21T10:00:00Z",
				})
			}
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID:               "p1",
				DatasetID:               "ds-1",
				AutomaticSave:           true,
				KnowledgeCatalogVersion: catalogVersion.Load().(string),
				UnreadCount:             25,
				UnreadPreview:           previews,
				UnreadHasMore:           true,
			})
		case "/v1/projects/p1/knowledge":
			knowledgeV2Calls.Add(1)
			if r.URL.Query().Get("unread_only") == "1" {
				sawUnreadOnly.Store(true)
			}
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID:      "p1",
				DatasetID:      "ds-1",
				CatalogVersion: catalogVersion.Load().(string),
				TotalFiltered:  25,
				UnreadCount:    25,
				Items: []v2KnowledgeItem{
					{DocID: "u-0", DatasetID: "ds-1", ProjectID: "p1", Title: "未读记忆 0", EffectiveDate: "2026-11-21", Kind: "knowledge", PreviewText: "未读摘要", IsUnread: true},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	a, updates := p1TestApp(t, control.URL, control.URL, token)
	p := a.access.Directory.Projects[0]

	// Seed a local 650-item knowledge-read-<hash>.json (using exact // hash path) to test 2-phase staging import.
	localPath, err := a.knowledgeReadPath(p)
	if err != nil {
		t.Fatal(err)
	}
	// Verify double-slash // hash compatibility!
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	expectedHash := fmt.Sprintf("knowledge-read-%x.json", sha256.Sum256([]byte(readingID+"/"+p.ID+"/"+p.Dataset)))
	if filepath.Base(localPath) != expectedHash || !strings.HasSuffix(readingID, "/") {
		t.Fatalf("double-slash hash mismatch: base=%s expected=%s id=%q", filepath.Base(localPath), expectedHash, readingID)
	}
	readMap := make(map[string]bool, 650)
	for i := 0; i < 650; i++ {
		readMap[fmt.Sprintf("old-%04d", i)] = i%10 != 0
	}
	if err := writeKnowledgeState(localPath, knowledgeReadState{Version: 1, Initialized: true, StartedAt: time.Now().UTC(), Read: readMap}); err != nil {
		t.Fatal(err)
	}

	q := a.projectQueueStateFor(p)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return q.Loaded && !q.Loading })

	// 1. Verify migrate-local + 2-phase staging (2 chunks: 500 + 150) executed once.
	if migrateLocalCalls.Load() != 1 || beginCalls.Load() != 1 || chunkCalls.Load() != 2 || commitCalls.Load() != 1 {
		t.Fatalf("unexpected migration calls: local=%d begin=%d chunk=%d commit=%d", migrateLocalCalls.Load(), beginCalls.Load(), chunkCalls.Load(), commitCalls.Load())
	}
	if committedBatchID != "batch-1" || stagedItems != 650 {
		t.Fatalf("unexpected commit totals: batch=%q staged=%d", committedBatchID, stagedItems)
	}
	if q.unreadCount() != 25 || !q.UnreadHasMore || len(q.UnreadKnowledge) != 10 {
		t.Fatalf("unexpected v2 message-summary unread state: count=%d more=%v preview=%d", q.unreadCount(), q.UnreadHasMore, len(q.UnreadKnowledge))
	}

	// 2. Verify sidebar unread badge (>10) popover opens and displays "查看全部 25 条未读记忆" closure!
	for _, pref := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(pref)
		a.projectUI.UnreadPopoverOpen = true
		a.projectUI.UnreadPopoverProject = "p1"
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		if !tt.HasText("查看全部 25 条未读记忆") {
			t.Fatalf("[%s] missing '查看全部 25 条未读记忆' jump button in unread popover", pref)
		}
	}
	a.openProjectUnreadFeed(0)
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading })
	if !s.Knowledge.UnreadOnly || !sawUnreadOnly.Load() {
		t.Fatalf("expected UnreadOnly=true and unread_only=1 server request")
	}

	// 3. Non-disruptive refresh Case A: user scrolled down -> banner appears, viewport stays fixed!
	s.ListScroll.Y = 320
	s.Knowledge.NewCatalogAvailable = false
	beforeCalls := knowledgeV2Calls.Load()
	catalogVersion.Store("cv-2")
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if !s.Knowledge.NewCatalogAvailable || knowledgeV2Calls.Load() != beforeCalls {
		t.Fatalf("scrolled view should show non-disruptive banner without auto-reloading: banner=%v calls=%d/%d", s.Knowledge.NewCatalogAvailable, knowledgeV2Calls.Load(), beforeCalls)
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("有新记忆可用") || !tt.HasText("点击刷新") {
		t.Fatalf("expected non-disruptive new-catalog banner in UI")
	}

	// 4. Non-disruptive refresh Case B: user at top & not deep-reading -> silently refreshes!
	s.ListScroll.Y = 0
	s.Knowledge.Detail = false
	s.Knowledge.NewCatalogAvailable = false
	catalogVersion.Store("cv-3")
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading && !s.Loading && knowledgeV2Calls.Load() > beforeCalls })
	if s.Knowledge.NewCatalogAvailable {
		t.Fatalf("top-of-list silent refresh should clear NewCatalogAvailable")
	}

	// 5. clearProjectNotifications calls POST /v1/reading-state/mark-all-read and zeroes unreadCount.
	if err := a.clearProjectNotifications(q, p); err != nil {
		t.Fatalf("clearProjectNotifications v2 failed: %v", err)
	}
	if markAllCalls.Load() != 1 || q.unreadCount() != 0 {
		t.Fatalf("expected mark-all-read=1 and unread=0, got calls=%d unread=%d", markAllCalls.Load(), q.unreadCount())
	}
}
