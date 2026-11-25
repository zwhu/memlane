package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func projectUIFixture() *App {
	a := New(Config{Projects: []Project{{ID: "p", Name: "memlane", Path: "/synthetic/memlane", Dataset: "ds"}, {ID: "same", Name: "memlane", Path: "/synthetic/other/memlane", Dataset: "other"}}}, nil)
	a.Update = func(func()) {}
	a.access.Next = time.Now().Add(time.Hour)
	a.page = "知识"
	a.monitor.EngineStatus = "已连接"
	d := Document{ID: "k", Dataset: "ds", Name: "Development runtime configuration", Created: "2026-11-12T16:56:00Z", Metadata: map[string]any{"node_set": []any{"session_learnings:s"}, "session_id": "s"}}
	i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: "docs/development.md documents config/daily-runtime.example.json fields, credential separation, shared official identity, and source-development startup, shutdown, and window reopening through scripts/dev.py.", Date: d.Created, Document: &d, BodyLoaded: true, Body: strings.Repeat("A readable paragraph about local development and credentials.\n\n", 18), Source: "s"}
	s := a.state()
	s.Knowledge.Initialized = true
	s.Items = []Item{i}
	s.Selected = &i
	s.Loaded = true
	return a
}
func clickProjectUI(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if r, ok := tt.Find(label); ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}
func TestProjectReadingModalFocusCopyAndIndependentGeometry(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := projectUIFixture()
			a.state().Knowledge.Detail = true
			tt := ui.NewTester(a.View, wh[0], wh[1])
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-reading", wh[0], wh[1]))
			if tt.HasText("搜索当前内容") || tt.HasText("技术详情") {
				t.Fatal("old search/technical layout remains")
			}
			before, ok := tt.Find("Development runtime configuration")
			if !ok {
				t.Fatal("reader title missing")
			}
			clickProjectUI(t, tt, "内容信息")
			if !tt.HasText("复制文档 ID") || tt.HasText("复制诊断信息") {
				t.Fatal("information panel did not open")
			}
			clickProjectUI(t, tt, "复制文档 ID")
			if tt.Clipboard() != "k" {
				t.Fatal("field copy changed value")
			}
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-information", wh[0], wh[1]))
			tt.Key(0, ui.KeyEscape)
			after, _ := tt.Find("Development runtime configuration")
			if before != after {
				t.Fatal("information panel moved reading geometry")
			}
			clickProjectUI(t, tt, "搜索记忆与原始记录")
			if a.page != "知识" || !tt.Focused("搜索关键词") {
				t.Fatal("opening search navigated or did not focus input")
			}
			tt.Key(0, ui.KeyTab)
			if tt.Focused("总览") {
				t.Fatal("dialog let focus escape")
			}
			tt.Key(0, ui.KeyEscape)
			if !a.searchUI.Open { // Escape may first dismiss a Select popup; either way do not close the reader.
				if !tt.Focused("搜索记忆与原始记录") {
					t.Fatal("search did not return focus")
				}
			} else {
				tt.Key(0, ui.KeyEscape)
			}
			if a.searchUI.Open || a.page != "知识" {
				t.Fatal("escape did not restore reader")
			}
			clickProjectUI(t, tt, "搜索记忆与原始记录")
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-search-empty", wh[0], wh[1]))
			tt.ClickAt(225, 80)
			if a.searchUI.Open {
				t.Fatal("outside click did not close search")
			}
		})
	}
}
func TestUnifiedSearchRequestsAndNestedReturn(t *testing.T) {
	oldStarted := make(chan struct{})
	oldRelease := make(chan struct{})
	var requests atomic.Int32
	var started sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			json.NewEncoder(w).Encode([]Document{})
			return
		}
		requests.Add(1)
		var in struct {
			Query    string   `json:"query"`
			Datasets []string `json:"dataset_ids"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.Datasets) != 2 || in.Datasets[0] != "ds" || in.Datasets[1] != "other" {
			t.Errorf("global search catalog changed: %v", in.Datasets)
		}
		if in.Query == "old" {
			started.Do(func() { close(oldStarted) })
			<-oldRelease
		}
		if in.Query == "failure" {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"dataset_id": "ds", "search_result": []any{map[string]any{"id": in.Query, "text": "A real official snippet", "document_name": in.Query, "document_id": "source"}}}})
	}))
	defer server.Close()
	var release sync.Once
	defer release.Do(func() { close(oldRelease) })
	a := projectUIFixture()
	a.Config.ServiceURL = server.URL
	a.Client.Config = a.Config
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 30)
	a.Update = func(fn func()) { updates <- fn }
	a.kind = "文档"
	origin := a.state()
	origin.Loaded = true
	origin.ListScroll.Y = 42
	a.OpenSearch()
	if a.searchUI.Scope != a.Config.Projects[0].ID {
		t.Fatalf("expected project scope by default from project page, got %q", a.searchUI.Scope)
	}
	// Toggle off "仅当前项目" to verify the global search catalog.
	a.searchUI.Scope = ""
	if requests.Load() != 0 {
		t.Fatal("opening search issued request")
	}
	a.submitUnifiedSearch()
	if requests.Load() != 0 {
		t.Fatal("empty word issued request")
	}
	a.searchUI.Query = "old"
	a.submitUnifiedSearch()
	select {
	case <-oldStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("old request did not reach fixture")
	}
	a.searchUI.Query = "new"
	a.submitUnifiedSearch()
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	release.Do(func() { close(oldRelease) })
	// The old completion is cancelled before it can overwrite this generation.
	if len(a.searchUI.Rows) != 1 || a.searchUI.Rows[0].ID != "new" {
		t.Fatal("latest request missing")
	}
	a.openSearchResult(a.searchUI.Rows[0])
	drainReading(t, updates, func() bool { return !a.state().Reading })
	if a.page != "知识" || a.searchOrigin == nil {
		t.Fatal("result origin missing")
	}
	a.state().History = []Item{*a.state().Selected}
	a.state().Selected = &Item{ID: "source", Dataset: "ds", Kind: "文档", BodyLoaded: true}
	a.returnFromSource(a.state())
	drainReading(t, updates, func() bool { return !a.state().Reading })
	if len(a.state().History) != 0 {
		t.Fatal("source return did not unwind")
	}
	a.returnToSearch()
	if a.page != "知识" || a.kind != "文档" || a.state() != origin || origin.ListScroll.Y != 42 || !a.searchUI.Open || a.searchUI.Query != "new" {
		t.Fatal("search return lost original route, list or query")
	}
	a.searchUI.Query = "failure"
	a.submitUnifiedSearch()
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	if a.searchUI.Error == "" || len(a.searchUI.Rows) != 0 {
		t.Fatal("failed new query exposed previous query results")
	}
	a.InvalidateReading("ds")
	if len(a.searchUI.Rows) != 0 || a.searchUI.Open {
		t.Fatal("withdrawal retained searchable content")
	}
}
func TestRawRecordsCursorAppendsMergedPageWithoutDuplicates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/projects/p/raw-records":
			q := r.URL.Query()
			if q.Get("kind") != "all" || q.Get("limit") != "30" {
				t.Error("unexpected raw records request", q)
			}
			resp := v2RawRecordsResponse{DatasetID: "ds", CacheReady: true}
			session := v2RawRecordItem{ItemKind: "session", Session: &v2RawSessionEntry{Session: Session{ID: "s", Dataset: "ds"}, Label: "Session"}}
			start, end := 0, 29
			switch q.Get("cursor") {
			case "":
				resp.Items = append(resp.Items, session)
				resp.HasMore, resp.NextCursor = true, "merged-30"
			case "merged-30":
				start, end = 29, 35
				resp.Items = append(resp.Items, session) // The adjacent page overlaps the session.
			default:
				t.Error("unexpected cursor", q)
			}
			for n := start; n < end; n++ {
				resp.Items = append(resp.Items, v2RawRecordItem{ItemKind: "document", Document: &Document{ID: fmt.Sprint(n), Dataset: "ds", Name: "raw.md"}})
			}
			json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL), Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	a.page, a.kind = "知识", "原始记录"
	s := a.state()
	s.Locating = true // Exercise list paging without selecting a detail.
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading })
	if s.Error != "" || !s.More || len(s.Items) != 30 || s.Items[0].ID != "s" {
		t.Fatalf("first merged page: %v %v %s", len(s.Items), s.More, s.Error)
	}
	a.appendRawRecords(s)
	drainReading(t, updates, func() bool { return !s.RawAppending })
	if s.Error != "" || s.More || len(s.Items) != 36 {
		t.Fatalf("second merged page: %d %v %s", len(s.Items), s.More, s.Error)
	}
	seen := map[string]bool{}
	for _, i := range s.Items {
		if seen[i.Key()] || i.Dataset != "ds" {
			t.Fatal("duplicated or foreign merged item", i)
		}
		seen[i.Key()] = true
	}
}

func TestProjectLongTitleSearchKeyboardAndStateFixtures(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := projectUIFixture()
			s := a.state()
			s.Knowledge.Detail = true
			i := s.Items[0]
			i.Document.Name = i.Title
			i.Body = "A readable local development guide.\n\n## Startup\n\nUse the existing launcher to reopen the application.\n\n```sh\npython3 scripts/dev.py open\n```"
			s.Items[0] = i
			s.Selected = &i
			for n := 2; n < 13; n++ {
				a.Config.Projects = append(a.Config.Projects, Project{ID: fmt.Sprint(n), Name: strings.Repeat("长项目名称", 4), Path: fmt.Sprintf("/synthetic/team-%d/project", n), Dataset: fmt.Sprint(n)})
			}
			tt := ui.NewTester(a.View, wh[0], wh[1])
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-long-title", wh[0], wh[1]))
			titleBefore := projectDisplayTitle(i)
			if titleBefore != i.Title {
				t.Fatal("official title was rewritten")
			}
			clickProjectUI(t, tt, "展开完整标题")
			if !a.projectUI.Expanded[i.Key()] || projectDisplayTitle(i) != titleBefore {
				t.Fatal("expansion altered title")
			}
			clickProjectUI(t, tt, "收起标题")
			clickProjectUI(t, tt, "搜索记忆与原始记录")
			a.searchUI.Query = "凭据配置"
			a.searchUI.Rows = []Item{{ID: "s1", Dataset: "ds", Kind: "知识片段", Title: i.Title, Preview: "credential separation, shared official identity…", BodyLoaded: true}, {ID: "s2", Dataset: "ds", Kind: "知识片段", Title: "客户端认证约定", Preview: "从 token_file 读取 X-Api-Key", BodyLoaded: true}, {ID: "s3", Dataset: "ds", Kind: "原始材料片段", Title: "开发环境与凭据配置", Preview: "启动参数与窗口重开", BodyLoaded: true}}
			a.searchUI.Loaded = true
			a.searchUI.Loading = false
			a.searchUI.Submitted = a.searchSignature()
			tt.Frame()
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-search-results", wh[0], wh[1]))
			tt.Key(0, ui.KeyDown)
			if a.searchUI.Selection != 0 || !a.searchUI.KeyboardSelection {
				t.Fatal("first down key did not select the first result")
			}
			tt.Key(0, ui.KeyDown)
			if a.searchUI.Selection != 1 {
				t.Fatal("down key did not select next result")
			}
			clickProjectUI(t, tt, "搜索内容类型")
			if a.searchUI.Submitted != a.searchSignature() {
				t.Fatal("local type switch invalidated completed results")
			}
			tt.Key(0, ui.KeyEscape)
			if a.searchUI.Open {
				t.Fatal("escape left search open")
			}
			s.Selected = nil
			s.Items = nil
			s.Loaded = true
			tt.Frame()
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-empty", wh[0], wh[1]))
			s.Loading = true
			s.Loaded = false
			tt.Frame()
			fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-loading", wh[0], wh[1]))
			if tt.HasText("已载入 0 条") {
				t.Fatal("initial loading showed false zero")
			}
		})
	}
}

func TestProjectKindSwitchCancelsInFlightAndKeepsPane(t *testing.T) {
	a := projectUIFixture()
	original := a.state()
	original.Loading = true
	original.Reading = true
	cancelledList, cancelledRead := false, false
	original.cancel = func() { cancelledList = true }
	original.readCancel = func() { cancelledRead = true }
	a.kind = "文档"
	docs := a.state()
	docs.Loaded = true
	docs.ListScroll.Y = 91
	a.kind = "知识"
	a.switchProjectKind("文档")
	if !cancelledList || !cancelledRead || original.Loading || original.Reading || a.state() != docs || docs.ListScroll.Y != 91 {
		t.Fatal("kind switch orphaned a pending pane or lost position")
	}
	a.switchProjectKind("知识")
	if a.state() != original || original.Selected == nil {
		t.Fatal("return to knowledge discarded selection")
	}
}
func TestProjectCodeGraphEmptyDoesNotStartWork(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		a := projectUIFixture()
		a.page = "图谱"
		a.graphController.Mode = "代码结构"
		a.graphController.CodeStatus = "官方代码索引状态：DATASET_PROCESSING_COMPLETED · historical-run"
		tt := ui.NewTester(a.View, wh[0], wh[1])
		if !tt.HasText("代码图谱尚未开启") || a.graphController.CodeBusy || a.graphController.Loading {
			t.Fatal("disabled code graph was not an idle empty state")
		}
		if tt.HasText("更新代码图谱") || tt.HasText("刷新图谱") || tt.HasText(a.graphController.CodeStatus) {
			t.Fatal("disabled code graph exposes irrelevant toolbar actions")
		}
		if _, ok := tt.Find("开启代码图谱"); !ok {
			t.Fatal("empty state has no explicit enable action")
		}
		fixtureImage(t, tt, fmt.Sprintf("project-%dx%d-graph-disabled", wh[0], wh[1]))
		a.graphController.Error = "代码设置尚未确认，请先核对原操作"
		tt.Frame()
		if !tt.HasText(a.graphController.Error) {
			t.Fatal("empty state hid the failed enable result")
		}
	}
}

func TestProjectTitlePreparationKeepsFullSentenceWithoutRawRows(t *testing.T) {
	title := strings.Repeat("Long technical sentence ", 15) + "ends here."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "# Session learning (session s)\n\n"+title)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	a.page = "知识"
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	d := lessonDocument()
	s := a.state()
	s.Items = []Item{{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &d}}
	a.startTitles(s)
	drainReading(t, updates, func() bool { return s.Items[0].ReadingState != "pending" })
	if s.Items[0].Title != title || s.Items[0].Body != "" {
		t.Fatal("project title truncated before visual layout, or raw copied into row")
	}
}

func TestSessionTitlesPreloadBeforeSelectionAndReuseTitleOnlyCache(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		json.NewEncoder(w).Encode(SessionHistoryPage{Paging: true, Limit: 20, SessionDetail: SessionDetail{Session: Session{ID: "codex_session", Dataset: "dataset", User: "owner"}, Count: 1, ToolCount: 1, QAs: []QA{{Question: "自动载入会话的可读标题", Answer: "Private answer"}}, Traces: []Trace{{ID: "private-tool"}}}})
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	a.page = "知识"
	a.kind = "原始记录"
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	session := Session{ID: "codex_session", Dataset: "dataset", User: "owner"}
	s := a.state()
	s.Items = []Item{{ID: session.ID, Dataset: session.Dataset, Kind: "会话", Title: session.ID, Session: &session}}
	for round := 0; round < 2; round++ {
		// Wait for this round's callback, including the title-cache hit.
		s.Items[0].ReadingState = ""
		a.startTitles(s)
		drainReading(t, updates, func() bool { return s.Items[0].ReadingState != "pending" })
		if s.Selected != nil || s.Items[0].Title != "自动载入会话的可读标题" || s.Items[0].Body != "" || s.Items[0].Detail != nil {
			t.Fatal("title requires selection or raw leaked into list")
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("cached title fetched again: %d", reads.Load())
	}
	a.titles.mu.Lock()
	defer a.titles.mu.Unlock()
	for _, entry := range a.titles.cache {
		if entry.item.Detail != nil || entry.item.Body != "" {
			t.Fatal("session preheat retained answers or tools")
		}
	}
}
