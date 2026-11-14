package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchTypeSwitchKeepsResultsClickableAndOpensThreePanes(t *testing.T) {
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
	if a.page != "知识" || a.project != 0 || a.kind != "知识" || !tt.HasText("项目分页栏") {
		t.Fatal("result did not open the corresponding three-pane reader", a.page, a.kind)
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
	a.searchUI.Due = time.Time{}
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
				docs = append(docs, Document{ID: fmt.Sprintf("k%d", n), Dataset: "ds", Name: fmt.Sprintf("Recent knowledge %d", n), Created: fmt.Sprintf("2026-11-%02dT10:00:00Z", n+1), Metadata: map[string]any{"node_set": []any{"session_learnings"}}}, Document{ID: fmt.Sprintf("r%d", n), Dataset: "ds", Name: fmt.Sprintf("Recent record %d", n), Created: fmt.Sprintf("2026-11-%02dT11:00:00Z", n+1)})
			}
			json.NewEncoder(w).Encode(docs)
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
	a.searchUI.Query = "new"
	a.cancelChangedSearch()
	if a.searchUI.Due.IsZero() {
		t.Fatal("typing did not schedule search")
	}
	a.searchUI.Due = time.Now().Add(-time.Second)
	tt.Frame()
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	if searches.Load() != 2 {
		t.Fatal("debounce did not submit both retrieval lanes once")
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
