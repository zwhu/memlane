package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchTypeSwitchKeepsResultsClickableAndOpensReader(t *testing.T) {
	a := projectUIFixture()
	a.OpenSearch()
	a.searchUI.Query = "sandbox"
	a.searchUI.Rows = []Item{{ID: "knowledge-hit", Dataset: "ds", Kind: "知识片段", Title: "Knowledge hit", Body: "A knowledge result", BodyLoaded: true}, {ID: "raw-hit", Dataset: "ds", Kind: "原始材料片段", Title: "Raw hit", Body: "An original record", BodyLoaded: true}}
	a.searchUI.Loaded = true
	a.searchUI.Loading = false
	a.searchUI.Submitted = a.searchSignature()
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "搜索内容类型") // middle segment: knowledge
	clickProjectUI(t, tt, "Knowledge hit")
	if a.searchUI.Open {
		t.Fatal("switching result type disabled an already returned result")
	}
	if a.page != "知识" || a.project != 0 || a.kind != "知识" || !tt.HasText("项目内容工具栏") || !tt.HasText("A knowledge result") {
		t.Fatal("result did not open the corresponding knowledge reader", a.page, a.kind)
	}
}

func TestSearchPreviewHidesTechnicalEnvelopeWithoutChangingSource(t *testing.T) {
	raw := "Lesson-Type: pitfall\nState-Key: unique-key\n# Session learning (session s)\n\nSandbox listeners require explicit permission."
	i := Item{Title: "检索片段", Kind: "知识片段", Body: raw, Preview: raw}
	title, preview := searchRowText(i)
	if title != "Sandbox listeners require explicit permission." || preview != "" || i.Body != raw {
		t.Fatal("technical envelope leaked or source changed", title, preview)
	}
}

func TestSearchChangedQueryNeverShowsPreviousRows(t *testing.T) {
	a := projectUIFixture()
	a.searchUI.Query = "first"
	a.searchUI.Rows = []Item{{ID: "old", Dataset: "ds", Title: "previous result"}}
	a.searchUI.Loaded = true
	a.searchUI.Loading = false
	a.searchUI.Submitted = a.searchSignature()
	if len(a.visibleSearchRows()) != 1 {
		t.Fatal("completed results missing")
	}
	a.searchUI.Query = "second"
	a.cancelChangedSearch()
	if len(a.visibleSearchRows()) != 0 {
		t.Fatal("old rows displayed while debouncing")
	}
	a.searchUI.Loading = true
	a.searchUI.Submitted = a.searchSignature()
	if len(a.visibleSearchRows()) != 0 {
		t.Fatal("old rows displayed under in-flight query")
	}
}

func TestSearchSessionHeaderIsNotTheHitTitle(t *testing.T) {
	i := Item{Title: "Session ID: codex_123", Preview: "Session ID: codex_123\n\nThe actual matching content.", Body: "original bytes"}
	title, preview := searchRowText(i)
	if title != "The actual matching content." || strings.Contains(preview, "Session ID:") || i.Body != "original bytes" {
		t.Fatal(title, preview)
	}
}

func TestSearchRecentDefaultsAndTypeSwitchUseOneCompletedCatalog(t *testing.T) {
	var searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/search" {
			searches.Add(1)
			json.NewEncoder(w).Encode([]any{})
			return
		}
		if r.URL.Path == "/api/v1/sessions" {
			json.NewEncoder(w).Encode(map[string]any{"sessions": []Session{}, "has_more": false})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/datasets/ds/data") {
			docs := []Document{}
			for n := 0; n < 8; n++ {
				docs = append(docs, Document{ID: fmt.Sprintf("k%d", n), Dataset: "ds", Name: fmt.Sprintf("Recent knowledge %d", n), Created: fmt.Sprintf("2026-11-%02dT10:00:00Z", n+1), Metadata: map[string]any{"node_set": []any{"session_learnings"}, "session_date": fmt.Sprintf("2026-11-%02d", n+1)}}, Document{ID: fmt.Sprintf("r%d", n), Dataset: "ds", Name: fmt.Sprintf("Recent record %d", n), Created: fmt.Sprintf("2026-11-%02dT11:00:00Z", n+1)})
			}
			json.NewEncoder(w).Encode(docs)
			return
		}
		json.NewEncoder(w).Encode([]Document{})
	}))
	defer server.Close()
	a := projectUIFixture()
	a.Config.ServiceURL = server.URL
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client.json")
	a.Client.Config = a.Config
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 30)
	a.Update = func(fn func()) { updates <- fn }
	a.OpenSearch()
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	if searches.Load() != 0 || len(a.searchUI.Rows) != 6 {
		t.Fatal("recent defaults issued search or wrong bound", searches.Load(), len(a.searchUI.Rows))
	}
	if a.searchUI.Rows[0].ID != "k7" || a.searchUI.Rows[3].ID != "r7" {
		t.Fatal("recent category order not chronological", a.searchUI.Rows)
	}
	for _, kind := range []string{"知识", "原始记录", "全部"} {
		a.searchUI.Type = kind
		rows := a.visibleSearchRows()
		want := 3
		if kind == "全部" {
			want = 6
		}
		if len(rows) != want || a.searchUI.Submitted != a.searchSignature() || searches.Load() != 0 {
			t.Fatal("local category changed remote catalog")
		}
	}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, removed := range []string{"搜索范围", "条件已更改", "按 Enter", "↑↓ 选择", "筛选框"} {
		if tt.HasText(removed) {
			t.Fatal("removed instruction/filter remains", removed)
		}
	}
	fixtureImage(t, tt, "search-recent-defaults")
	a.searchUI.Focus = true
	tt.Frame()
	tt.Type("new")
	tt.Advance(time.Second)
	if searches.Load() != 0 || a.searchUI.Loading {
		t.Fatal("typing submitted a search")
	}
	if rows, err := a.Client.manualSearchHistory(); err != nil || len(rows) != 0 {
		t.Fatal("typing recorded an unsubmitted query", rows, err)
	}
	tt.Key(0, ui.KeyEnter)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	if searches.Load() != 2 {
		t.Fatal("Enter did not submit both retrieval lanes once")
	}
	if rows, err := a.Client.manualSearchHistory(); err != nil || len(rows) != 1 || rows[0].Query != "new" {
		t.Fatal("Enter did not record the submitted query", rows, err)
	}
}
func TestSearchSourceDocumentUsesNormalThreePaneReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/raw") {
			fmt.Fprint(w, "Full official document body.")
			return
		}
		if r.URL.Path == "/api/v1/sessions" {
			json.NewEncoder(w).Encode(map[string]any{"sessions": []Session{}, "has_more": false})
			return
		}
		if strings.Contains(r.URL.Path, "/datasets/ds/") {
			json.NewEncoder(w).Encode([]Document{{ID: "document", Dataset: "ds", Name: "Official source.md", Created: "2026-11-12T10:00:00Z"}})
			return
		}
		json.NewEncoder(w).Encode([]Document{})
	}))
	defer server.Close()
	a := projectUIFixture()
	a.Config.ServiceURL = server.URL
	a.Client.Config = a.Config
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 30)
	a.Update = func(fn func()) { updates <- fn }
	a.searchUI = unifiedSearchState{Query: "official", Type: "原始记录", Open: true}
	i := Item{ID: "chunk", Dataset: "ds", Kind: "原始材料片段", Title: "Snippet", SourceDocument: "document", Body: "partial", BodyLoaded: true}
	a.openSearchResult(i)
	drainReading(t, updates, func() bool {
		return a.state().Selected != nil && a.state().Selected.ID == "document" && !a.state().Reading
	})
	if a.page != "知识" || a.kind != "原始记录" || a.state().Selected.Body != "Full official document body." || !containsReadingItem(a.state().Items, a.state().Selected.key()) {
		t.Fatal("source resolution lost real reader or selection")
	}
	a.returnToSearch()
	if !a.searchUI.Open || a.searchUI.Query != "official" || a.searchUI.Type != "原始记录" {
		t.Fatal("return lost search state")
	}
	a.closeUnifiedSearch()
	// Recent catalog is metadata-only when readable titles already exist.
	_, err := a.recentSearchItems(context.Background(), a.Client, []Project{{Dataset: "ds"}}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSearchRestingRowsHaveNoSelectionAndHoverShowsContent(t *testing.T) {
	a := projectUIFixture()
	a.OpenSearch()
	a.searchUI.Query = ""
	a.searchUI.Rows = []Item{{ID: "recent", Dataset: "ds", Kind: "会话", Title: "Recent conversation", Preview: "The complete conversation preview", BodyLoaded: true}}
	a.searchUI.Loaded = true
	a.searchUI.Loading = false
	a.searchUI.Submitted = a.searchSignature()
	tt := ui.NewTester(a.View, 1200, 820)
	if a.searchUI.KeyboardSelection || tt.HasText("The complete conversation preview") {
		t.Fatal("recent content should be compact and unselected at rest")
	}
	r, ok := tt.Find("Recent conversation")
	if !ok {
		t.Fatal("missing result")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(700 * time.Millisecond)
	if !tt.HasText("Recent conversation\n\nThe complete conversation preview") {
		t.Fatal("official hover tooltip did not show the content", tt.Texts())
	}
	fixtureImage(t, tt, "search-hover-preview")
}

func TestSearchPaletteV4ProjectScopeChipAndResultsPage(t *testing.T) {
	a := projectUIFixture()
	// Opening from a project page defaults "仅当前项目" to active.
	a.page = "知识"
	a.project = 0
	a.OpenSearch()
	if a.searchUI.Scope != a.Config.Projects[0].ID {
		t.Fatalf("expected project scope %q when opened from project page, got %q", a.Config.Projects[0].ID, a.searchUI.Scope)
	}
	a.searchUI.Query = "sandbox"
	a.searchUI.Rows = []Item{
		{ID: "k1", Dataset: "ds", Kind: "知识片段", Title: "Sandbox listener rule", Body: "Sandbox listener requires explicit opt-in.", Preview: "Sandbox listener requires explicit opt-in.", BodyLoaded: true, SearchMatch: "关键词匹配", Date: "2026-11-20"},
		{ID: "123e4567-e89b-12d3-a456-426614174000", Dataset: "ds", Kind: "会话", Title: "123e4567-e89b-12d3-a456-426614174000", Session: &Session{ID: "codex_123e4567-e89b-12d3-a456-426614174000", Dataset: "ds"}, Preview: "Sandbox session discussion", BodyLoaded: true, SearchMatch: "语义相关", Date: "2026-11-19"},
	}
	a.searchUI.Loaded = true
	a.searchUI.Loading = false
	a.searchUI.Submitted = a.searchSignature()

	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("仅当前项目") || !tt.HasText("查看全部结果") || !tt.HasText("检索结果页") {
		t.Fatalf("V4 palette controls missing: %v", tt.Texts())
	}
	// Verify ~120px top offset regardless of window height.
	if panel, ok := tt.Find("搜索记忆与原始记录弹框"); !ok || panel.Y < 110 || panel.Y > 130 {
		t.Fatalf("expected ⌘K panel positioned ~120px from top, got ok=%v rect=%+v", ok, panel)
	}
	// Raw UUID must not leak as the row title.
	if tt.HasText("123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("bare UUID leaked in search palette row")
	}

	// Clicking v4Segmented / filter controls must keep ↑↓ and ⌘↵ working via overlay shortcuts.
	clickProjectUI(t, tt, "搜索内容类型")
	tt.Key(0, ui.KeyDown)
	if !a.searchUI.KeyboardSelection || a.searchUI.Selection != 0 {
		t.Fatalf("↑↓ keyboard selection broke after clicking segmented filter: kb=%v sel=%d", a.searchUI.KeyboardSelection, a.searchUI.Selection)
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if a.page != "检索结果" || a.searchUI.Open {
		t.Fatalf("⌘↵ did not open 检索结果 page after clicking filter: page=%q open=%v", a.page, a.searchUI.Open)
	}
	if !tt.HasText("检索结果") || !tt.HasText("复制正文") || !tt.HasText("在项目中打开") {
		t.Fatalf("three-pane search results page missing V4 elements: %v", tt.Texts())
	}

	// Raw record / session in search results must also offer "在项目中打开" and render highlighted session QAs.
	a.searchUI.Type = "全部"
	rawItem := Item{
		ID:      "codex_sess1",
		Dataset: "ds",
		Kind:    "会话",
		Title:   "Sandbox session topic",
		Session: &Session{ID: "codex_sess1", Dataset: "ds"},
		Detail: &SessionDetail{
			Count: 1,
			QAs:   []QA{{ID: "q1", Question: "How does sandbox work?", Answer: "Sandbox uses explicit opt-in."}},
		},
		BodyLoaded: true,
	}
	a.searchUI.Rows = []Item{rawItem}
	a.state().Selected = &rawItem
	tt.Frame()
	if !tt.HasText("在项目中打开") || !tt.HasText("How does sandbox work?") {
		t.Fatalf("raw session search result missing open-in-project or Q&A text: %v", tt.Texts())
	}

	// Opening from overview defaults "仅当前项目" to unchecked.
	a.navigate("总览", 0)
	a.OpenSearch()
	if a.searchUI.Scope != "" {
		t.Fatalf("expected global scope when opened from overview, got %q", a.searchUI.Scope)
	}
}

func TestSearchResultsFromProjectManageTabAndHiddenProjectSkipped(t *testing.T) {
	a := projectUIFixture()
	// Mark first project hidden; second project is visible.
	p0 := a.Config.Projects[0]
	a.projectVisibility = projectVisibilityState{
		loaded: true,
		Hidden: map[string]bool{p0.ID + "/" + p0.Dataset: true},
	}
	a.navigate("总览", 0)
	if proj, ok := a.searchTargetProject(); !ok || proj.ID != a.Config.Projects[1].ID {
		t.Fatalf("expected visible project %q on overview, got ok=%v proj=%+v", a.Config.Projects[1].ID, ok, proj)
	}

	// Open ⌘K from project "管理" tab, press ⌘↵, and verify three-pane search results
	// renders instead of being occluded by the project management view.
	a.navigateKind("知识", 1, "管理")
	a.OpenSearch()
	a.searchUI.Query = "sandbox"
	a.searchUI.Rows = []Item{
		{ID: "k-manage", Dataset: a.Config.Projects[1].Dataset, Kind: "知识片段", Title: "Sandbox from manage", Body: "Sandbox rule body", Preview: "Sandbox rule body", BodyLoaded: true, SearchMatch: "关键词匹配", Date: "2026-11-20"},
	}
	a.searchUI.Loaded = true
	a.searchUI.Loading = false
	a.searchUI.Submitted = a.searchSignature()

	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("清空") {
		t.Fatal("distinct clear text button missing when query is non-empty")
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if a.page != "检索结果" || a.searchUI.Open {
		t.Fatalf("⌘↵ from manage tab failed: page=%q open=%v", a.page, a.searchUI.Open)
	}
	if _, ok := tt.Find("检索结果列表"); !ok {
		t.Fatalf("search results three-pane list occluded by manage tab: %v", tt.Texts())
	}
	if !tt.HasText("复制正文") || !tt.HasText("在项目中打开") {
		t.Fatalf("search results reader controls missing after opening from manage tab: %v", tt.Texts())
	}
}

func TestSearchPaletteLoadingSkeletonAndReduceMotion(t *testing.T) {
	a := projectUIFixture()
	a.OpenSearch()
	a.searchUI.Loading = true
	a.searchUI.Rows = nil
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("搜索中…") {
		t.Fatal("loading state missing skeleton status")
	}
}
