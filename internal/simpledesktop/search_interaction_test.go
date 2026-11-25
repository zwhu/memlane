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
	clickProjectUI(t, tt, "A knowledge result")
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
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/search/recent":
			if r.URL.Query().Get("limit") != "20" {
				t.Error("recent search must request a bounded list")
			}
			var resp v2SearchRecentResponse
			for n := 7; n >= 5; n-- {
				resp.KnowledgeItems = append(resp.KnowledgeItems, v2KnowledgeItem{DocID: fmt.Sprintf("k%d", n), DatasetID: "ds", Title: fmt.Sprintf("Recent knowledge %d", n)})
				resp.SessionItems = append(resp.SessionItems, v2SearchSessionItem{SessionID: fmt.Sprintf("r%d", n), DatasetID: "ds", Label: fmt.Sprintf("Recent record %d", n)})
			}
			resp.KnowledgeItems = append(resp.KnowledgeItems, v2KnowledgeItem{DocID: "foreign", DatasetID: "unrelated"}, resp.KnowledgeItems[0])
			resp.SessionItems = append(resp.SessionItems, v2SearchSessionItem{SessionID: "foreign", DatasetID: "unrelated"}, resp.SessionItems[0])
			json.NewEncoder(w).Encode(resp)
		case "/v1/search/metadata":
			json.NewEncoder(w).Encode(v2SearchMetadataResponse{})
		case "/v1/projects/p/session-drift", "/v1/knowledge/recalls":
			http.NotFound(w, r)
		default:
			t.Error("unexpected search request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := projectUIFixture()
	a.Config.ServiceURL = server.URL
	a.Config.ProjectControl = readingControlConfig(t, server.URL)
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
		doc := Document{ID: "document", Dataset: "ds", Name: "Official source.md", Created: "2026-11-12T10:00:00Z"}
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/datasets/ds/documents/document/body":
			body := "Full official document body."
			json.NewEncoder(w).Encode(v2DocumentBodyResponse{ID: doc.ID, DatasetID: doc.Dataset, Body: &body})
		case "/v1/datasets/ds/documents/document":
			json.NewEncoder(w).Encode(documentLookupV2Response{Document: doc})
		case "/v1/projects/p/raw-records":
			json.NewEncoder(w).Encode(v2RawRecordsResponse{DatasetID: "ds", CacheReady: true,
				Items: []v2RawRecordItem{{ItemKind: "document", Document: &doc}}})
		case "/v1/search/recent":
			json.NewEncoder(w).Encode(v2SearchRecentResponse{KnowledgeItems: []v2KnowledgeItem{{DocID: "k", DatasetID: "ds", Title: "Readable title"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := projectUIFixture()
	a.Config.ServiceURL = server.URL
	a.Config.ProjectControl = readingControlConfig(t, server.URL)
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
	control, _ := a.accessClient()
	rows, err := searchRecentItemsV2(context.Background(), control, []Project{{ID: "p", Dataset: "ds"}}, "p", "", "")
	if err != nil || len(rows) != 1 || rows[0].Title != "Readable title" || rows[0].BodyLoaded {
		t.Fatal("recent catalog should remain metadata-only", rows, err)
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

func TestSearchPaletteV4ProjectScopeChipAndPersistence(t *testing.T) {
	a := projectUIFixture()
	// Opening from a project page with empty query defaults "仅当前项目" to active.
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
	if !tt.HasText("仅当前项目") || !tt.HasText("共 2 项") {
		t.Fatalf("V4 palette controls missing: %v", tt.Texts())
	}
	if tt.HasText("查看全部结果") || tt.HasText("检索结果页") {
		t.Fatalf("removed search results page controls remain in ⌘K footer: %v", tt.Texts())
	}
	// Verify ~120px top offset regardless of window height.
	if panel, ok := tt.Find("搜索记忆与原始记录弹框"); !ok || panel.Y < 110 || panel.Y > 130 {
		t.Fatalf("expected ⌘K panel positioned ~120px from top, got ok=%v rect=%+v", ok, panel)
	}
	// Raw UUID must not leak as the row title.
	if tt.HasText("123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("bare UUID leaked in search palette row")
	}

	// Clicking v4Segmented keeps ↑↓ working via overlay shortcuts, and ⌘↵ no longer navigates to 检索结果.
	clickProjectUI(t, tt, "搜索内容类型")
	tt.Key(0, ui.KeyDown)
	if !a.searchUI.KeyboardSelection || a.searchUI.Selection != 0 {
		t.Fatalf("↑↓ keyboard selection broke after clicking segmented filter: kb=%v sel=%d", a.searchUI.KeyboardSelection, a.searchUI.Selection)
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if a.page == "检索结果" || !a.searchUI.Open {
		t.Fatalf("⌘↵ should not open removed 检索结果 page: page=%q open=%v", a.page, a.searchUI.Open)
	}

	// Closing and reopening ⌘K preserves last non-empty query, scope, type, rows, selection, and scroll.
	a.searchUI.Scroll.Y = 48
	a.closeUnifiedSearch()
	a.navigate("总览", 0)
	a.OpenSearch()
	if !a.searchUI.Open || a.searchUI.Query != "sandbox" || a.searchUI.Scope != a.Config.Projects[0].ID || a.searchUI.Type != "知识" || !a.searchUI.KeyboardSelection || a.searchUI.Selection != 0 || a.searchUI.Scroll.Y != 48 || len(a.searchUI.Rows) != 2 {
		t.Fatalf("reopening ⌘K lost last search context: %+v", a.searchUI)
	}

	// Clearing query resets cached search; subsequent reopen on overview defaults to global scope.
	tt.Frame()
	clickProjectUI(t, tt, "清空关键词")
	if a.searchUI.Query != "" {
		t.Fatalf("clear button did not empty query: %q", a.searchUI.Query)
	}
	a.closeUnifiedSearch()
	a.OpenSearch()
	if a.searchUI.Scope != "" {
		t.Fatalf("expected global scope when reopened with empty query on overview, got %q", a.searchUI.Scope)
	}
}

func TestSearchPaletteFromProjectManageTabAndCommonMemoryJump(t *testing.T) {
	a := projectUIFixture()
	a.Config.Common = []Project{{ID: "common-user", Name: "通用记忆", Dataset: "ds-common"}}
	// Confirm searchCatalog("") includes a.Config.Common alongside projects.
	catalog := a.searchCatalog("")
	hasCommon := false
	for _, p := range catalog {
		if p.Dataset == "ds-common" {
			hasCommon = true
			break
		}
	}
	if !hasCommon {
		t.Fatalf("searchCatalog(\"\") missing common memory dataset: %+v", catalog)
	}

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

	// Open ⌘K from project "管理" tab and jump directly to a common memory hit via Enter.
	a.navigateKind("知识", 1, "管理")
	a.OpenSearch()
	a.searchUI.Scope = ""
	a.searchUI.Query = "sandbox"
	commonHit := Item{ID: "cm-1", Dataset: "ds-common", Kind: "知识片段", Title: "Common sandbox preference", Body: "Always use explicit opt-in.", Preview: "Always use explicit opt-in.", BodyLoaded: true, SearchMatch: "关键词匹配", Date: "2026-11-20"}
	a.searchUI.Rows = []Item{commonHit}
	a.searchUI.Loaded = true
	a.searchUI.Loading = false
	a.searchUI.Submitted = a.searchSignature()

	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("清空") {
		t.Fatal("distinct clear text button missing when query is non-empty")
	}
	tt.Key(0, ui.KeyDown)
	a.searchSubmitOrOpen()
	if a.searchUI.Open || a.page != "通用知识" || a.project != 0 || a.state().Selected == nil || a.state().Selected.ID != "cm-1" {
		t.Fatalf("Enter on common memory hit did not open 通用知识 and select item: open=%v page=%q proj=%d sel=%+v", a.searchUI.Open, a.page, a.project, a.state().Selected)
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

func TestSearchSpaceCollapsedWrapperStrippingAndNoKnowledgeTitleDuplication(t *testing.T) {
	raw := "Lesson-Type: state State-Key: desktop/window Session learning (session codex_s1) Session ID: codex_s1 Window stays 1200x820 by default."
	item := Item{
		ID:          "chunk-collapsed",
		Dataset:     "ds",
		Kind:        "知识片段",
		Title:       "检索片段",
		Body:        raw,
		Preview:     raw,
		SearchMatch: "关键词匹配",
	}
	title, preview := searchRowText(item)
	if title != "Window stays 1200x820 by default." || preview != "" || item.Body != raw {
		t.Fatalf("space-collapsed wrapper leaked or source mutated: title=%q preview=%q body=%q", title, preview, item.Body)
	}

	// Knowledge hit with multi-sentence body and non-empty query must not duplicate "title — preview".
	longBody := "First sentence explains the rule. Second sentence mentions sandbox opt-in details."
	knowledgeHit := Item{
		ID:          "chunk-know",
		Dataset:     "ds",
		Kind:        "知识片段",
		Title:       "First sentence explains the rule.",
		Body:        longBody,
		Preview:     longBody,
		SearchMatch: "关键词匹配",
	}
	primary, secondary := searchItemPrimaryText(knowledgeHit, "sandbox")
	if secondary != "" || !strings.Contains(primary, "sandbox") || strings.Contains(primary, " — ") {
		t.Fatalf("knowledge hit duplicated title and preview: primary=%q secondary=%q", primary, secondary)
	}
}
