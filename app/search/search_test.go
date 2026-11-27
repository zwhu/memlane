package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type testFence struct {
	epoch, authEpoch uint64
	identity         string
}

func (f *testFence) Epoch() uint64           { return f.epoch }
func (f *testFence) AuthEpoch() uint64       { return f.authEpoch }
func (f *testFence) ReadingIdentity() string { return f.identity }
func (f *testFence) InvalidateReading()      { f.authEpoch++ }

type testRouter struct{ target shell.ItemTarget }

func (*testRouter) NavigateOverview()                        {}
func (*testRouter) NavigateProject(string)                   {}
func (*testRouter) NavigateCommon()                          {}
func (*testRouter) NavigateProfiles()                        {}
func (*testRouter) NavigateSettings(string)                  {}
func (r *testRouter) OpenItem(target shell.ItemTarget) error { r.target = target; return nil }

type testTheme struct{ dark bool }

func (t testTheme) Tokens() *kitui.Tokens { return kitui.TokensFor(t.dark) }
func (testTheme) ReduceMotion() bool      { return true }

type searchTransport func(*http.Request) (*http.Response, error)

func (f searchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func searchAPI(t *testing.T, handler http.Handler) *api.Client {
	t.Helper()
	cfg := api.Config{BaseURL: "http://127.0.0.1", ReadingIdentity: "test-reader"}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		_ = listener.Close()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		cfg.BaseURL, cfg.HTTPClient = server.URL, server.Client()
	} else {
		t.Logf("loopback listener unavailable, using recorder transport: %v", err)
		cfg.HTTPClient = &http.Client{Transport: searchTransport(func(req *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			return w.Result(), nil
		})}
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func awaitSearch[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("search callback stalled")
		var zero T
		return zero
	}
}

func searchProjects(scope string) []shell.ProjectRef {
	p := shell.ProjectRef{ID: "p", Name: "Memlane", DatasetID: "ds"}
	switch scope {
	case "p":
		return []shell.ProjectRef{p}
	case "":
		return []shell.ProjectRef{p, {ID: "other", DatasetID: "other-ds"}}
	default:
		return nil
	}
}

func TestSearchExplicitEnterRecentAndContext(t *testing.T) {
	var searches atomic.Int32
	requests := make(chan api.SearchRequest, 8)
	client := searchAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Memlane-Reading-Identity") != "test-reader" {
			t.Error("reading identity missing")
		}
		switch r.URL.Path {
		case "/api/search/recent":
			fmt.Fprint(w, `{"recent_items":[{"dataset_id":"ds","doc_id":"recent","kind":"knowledge","title":"Recent"}]}`)
		case "/api/search":
			searches.Add(1)
			var input api.SearchRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if r.Method != http.MethodPost || input.Query != "wal" || !input.Semantic || input.Filter != "all" {
				t.Errorf("wrong search request %+v", input)
			}
			requests <- input
			fmt.Fprint(w, `{"ok":true,"items":[{"dataset_id":"ds","doc_id":"doc","kind":"knowledge","title":"WAL notes"},{"dataset_id":"ds","session_id":"s","user_id":"u","kind":"session","title":"Session"},{"dataset_id":"denied","doc_id":"secret","kind":"knowledge"}],"semantic_hits":[{"dataset_id":"ds","document_id":"doc2","title":"Related","snippet":"Another result"}]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	updates := make(chan func(), 8)
	router, fence := &testRouter{}, &testFence{identity: "test-reader"}
	p := searchProjects("p")[0]
	restoreCount := 0
	c := New(Deps{Client: client, Dispatcher: shell.DispatchFunc(func(fn func()) { updates <- fn }), Router: router,
		Fence: fence, Catalog: searchProjects, OnRestoreOrigin: func() { restoreCount++ }})
	c.Open(false, "graph", p, true)
	awaitSearch(t, updates)()
	if c.State.Scope != "p" || len(c.VisibleRows()) != 1 || c.VisibleRows()[0].ID != "recent" {
		t.Fatal("fresh graph search did not show scoped recent results")
	}
	c.State.Query = "wal"
	c.CancelChanged()
	if len(c.VisibleRows()) != 0 || c.State.Loading || searches.Load() != 0 {
		t.Fatal("query editing fetched or exposed old rows")
	}
	c.ToggleCurrentProject(p.ID)
	if c.State.Scope != "" || searches.Load() != 0 {
		t.Fatal("scope change submitted uncommitted query")
	}
	c.ToggleCurrentProject(p.ID)
	c.SubmitOrOpen()
	awaitSearch(t, updates)()
	input := awaitSearch(t, requests)
	if input.ProjectID != "p" || input.DatasetID != "ds" || len(c.VisibleRows()) != 3 {
		t.Fatalf("search scope or result mapping changed: %+v %+v", input, c.VisibleRows())
	}
	c.SetType("原始记录")
	visible := c.VisibleRows()
	if len(visible) != 1 || visible[0].ID != "s" || visible[0].UserID != "u" {
		t.Fatalf("type filter or session identity lost: %+v", visible)
	}
	c.State.KeyboardSelection, c.State.Scroll.Y = true, 48
	c.SubmitOrOpen()
	if router.target.ItemID != "s" || c.State.Open || searches.Load() != 1 {
		t.Fatalf("Enter did not open the selected row: %+v", router.target)
	}
	c.ReturnToSearch()
	if restoreCount != 1 || !c.State.Open || c.State.Scroll.Y != 48 || c.State.Query != "wal" ||
		c.State.Scope != p.ID || c.State.Type != "原始记录" || !c.State.KeyboardSelection || len(c.VisibleRows()) != 1 {
		t.Fatalf("search context changed: %+v", c.State)
	}
}

func TestInFlightSearchIsCanceledWhenQueryChanges(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	updates := make(chan func(), 4)
	c := New(Deps{Catalog: searchProjects, Dispatcher: shell.DispatchFunc(func(fn func()) { updates <- fn })})
	c.Fetch = func(string) Fetcher[ResultItem, struct{}] {
		return func(ctx context.Context, query string, _ struct{}, publish func(Publication[ResultItem, struct{}])) {
			close(started)
			<-ctx.Done()
			close(canceled)
			publish(Publication[ResultItem, struct{}]{Rows: []ResultItem{{ID: query}}, Done: true, Loaded: true})
		}
	}
	c.State.Query = "old"
	c.Submit()
	awaitSearch(t, started)
	c.State.Query = "new"
	c.CancelChanged()
	awaitSearch(t, canceled)
	awaitSearch(t, updates)()
	if c.State.Loading || c.State.Submitted != "" || len(c.State.Rows) != 0 {
		t.Fatal("canceled results crossed request fence")
	}
}

func TestSearchReadingFenceAndAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		change func(*testFence)
	}{
		{"auth epoch", 200, func(f *testFence) { f.authEpoch++ }},
		{"reading identity", 200, func(f *testFence) { f.identity = "new-reader" }},
		{"unauthorized", 401, nil},
		{"forbidden", 403, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := searchAPI(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					fmt.Fprint(w, `{"ok":true,"items":[{"dataset_id":"ds","doc_id":"stale","kind":"knowledge"}]}`)
					return
				}
				fmt.Fprint(w, `{"error":"reading_denied","message":"denied"}`)
			}))
			updates := make(chan func(), 2)
			f := &testFence{identity: "reader"}
			c := New(Deps{Client: client, Catalog: searchProjects, Fence: f, Dispatcher: shell.DispatchFunc(func(fn func()) { updates <- fn })})
			c.State.Open, c.State.Query = true, "wal"
			c.Submit()
			finish := awaitSearch(t, updates)
			if tc.change != nil {
				tc.change(f)
			}
			finish()
			if len(c.State.Rows) != 0 || c.State.Loading {
				t.Fatal("stale or denied search exposed its rows")
			}
			if tc.change == nil && (c.State.Open || f.authEpoch != 1) {
				t.Fatal("authorization rejection did not invalidate reading")
			}
		})
	}
}

func TestSearchText(t *testing.T) {
	wrapped := "Lesson-Type: fix\nState-Key: test\n# Session learning (session uuid)\nSession ID: 32\nWAL fix"
	if got := StripWrapperPrefixes(wrapped); got != "WAL fix" {
		t.Fatal(got)
	}
	if !LooksLikeBareUUID("b416c2c6-7d13-4ef0-87c4-3b2662aa0a01") || LooksLikeBareUUID("readable document") {
		t.Fatal("UUID recognition changed")
	}
	if !KeywordMatch("WAL", "日志", "wal 日志") || KeywordMatch("WAL", "日志", "wal missing") || KeywordMatch("WAL", "", "") {
		t.Fatal("keyword matching changed")
	}
	preview := strings.Repeat("界", 90) + " WAL " + strings.Repeat("体", 150)
	if excerpt := HitExcerpt(preview, "wal"); !strings.HasPrefix(excerpt, "…") || !strings.HasSuffix(excerpt, "…") || !strings.Contains(excerpt, "WAL") {
		t.Fatal(excerpt)
	}
	for _, row := range []struct{ text, term, want string }{{"No WAL yet", "wal", "WAL"}, {"Use K now", "k", "K"}, {"κόσμος", "Σ", "σς"}, {"搜索 搜索词", "搜索词", "搜索词"}} {
		spans := HighlightSpans(ui.Span{Text: row.text}, []string{row.term}, ui.Hex("#ffeebb"))
		var found, joined string
		for _, span := range spans {
			joined += span.Text
			if span.Radius > 0 {
				found += span.Text
			}
		}
		if joined != row.text || found != row.want {
			t.Fatalf("highlighting %q produced %q / %q", row.text, joined, found)
		}
	}
}

func TestSearchPaletteLightDarkAndControls(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := searchProjects("p")[0]
		c := New(Deps{Theme: testTheme{dark}, TargetProject: func() (shell.ProjectRef, bool) { return p, true }, Catalog: searchProjects})
		c.State.Open = true
		c.State.Focus = true
		tt := ui.NewTester(c.RenderDialog, 1200, 820)
		if box, ok := tt.Find("搜索记忆与原始记录弹框"); !ok || box.W != 640 {
			t.Fatalf("dark=%v palette geometry: %+v", dark, box)
		}
		for _, label := range []string{"全部", "记忆", "原始记录", "仅当前项目", "Esc"} {
			if !tt.HasText(label) {
				if _, ok := tt.Find(label); !ok {
					t.Errorf("dark=%v missing %q", dark, label)
				}
			}
		}
		if err := tt.Click("仅当前项目"); err != nil || c.State.Scope != p.ID {
			t.Fatalf("scope chip failed: %q %v", c.State.Scope, err)
		}
		if err := tt.Click("记忆"); err != nil || c.State.Type != "知识" {
			t.Fatalf("segmented filter failed: %q %v", c.State.Type, err)
		}
	}
}

func TestMapCatalogItemKindNormalizationAndMachineNameFiltering(t *testing.T) {
	allowed := map[string]bool{"ds": true}
	resp := api.SearchResponse{
		Items: []api.SearchItem{
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-upper", Kind: "  Knowledge ", Title: "Upper knowledge"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-common", Kind: "common", Title: "Common note"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-direct", Kind: "direct_memory", Title: "Direct save"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-uctx", Kind: "USER_CONTEXT", Title: "User context"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-mem", Kind: "memory", Title: "Memory note"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-cat", Kind: "", CommonCategory: "约定", Title: "Common by category"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "d-raw", Kind: "raw_doc", Title: "", DocName: "README.md"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "d-rawdoc", Kind: " RAW_DOCUMENT ", Title: "", DocName: "text_0123456789abcdef0123456789abcdef.md"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-machine-title", Kind: "knowledge", Title: "text_0123456789abcdef0123456789abcdef", DocName: "text_0123456789abcdef0123456789abcdef"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", DocID: "k-machine-fallback", Kind: "direct_memory", Title: "user_context-0123456789abcdef0123456789abcdef.txt", DocLabel: "Readable label", DocName: "user_context-0123456789abcdef0123456789abcdef.txt"}},
			{CatalogItem: api.CatalogItem{DatasetID: "ds", SessionID: "sess-1", Kind: " RAW_SESSION ", Title: "b416c2c6-7d13-4ef0-87c4-3b2662aa0a01"}},
		},
		SemanticHits: []api.SearchHit{
			{DatasetID: "ds", DocumentID: "sem-1", Title: "text_0123456789abcdef0123456789abcdef", Snippet: "WAL checkpoint tuning"},
		},
	}

	got := MapSearchResponse(resp, "WAL", allowed)
	byID := make(map[string]ResultItem, len(got))
	for _, item := range got {
		byID[item.ID] = item
	}

	for _, id := range []string{"k-upper", "k-common", "k-direct", "k-uctx", "k-mem", "k-cat", "k-machine-title", "k-machine-fallback"} {
		item, ok := byID[id]
		if !ok || item.Kind != "知识" || !item.knowledge() {
			t.Fatalf("item %s did not normalize to 知识: %+v", id, item)
		}
	}
	if byID["d-raw"].Kind != "文档" || byID["d-raw"].Title != "README.md" {
		t.Fatalf("raw_doc mapping failed: %+v", byID["d-raw"])
	}
	if byID["d-rawdoc"].Kind != "文档" || byID["d-rawdoc"].Title != "" {
		t.Fatalf("raw_document leaked machine name or wrong kind: %+v", byID["d-rawdoc"])
	}
	if byID["k-machine-title"].Title != "" {
		t.Fatalf("machine title leaked into ResultItem: %+v", byID["k-machine-title"])
	}
	if title, _ := resultText(byID["k-machine-title"], ""); title != "记忆" {
		t.Fatalf("palette fallback leaked machine name: %q", title)
	}
	if byID["k-machine-fallback"].Title != "Readable label" {
		t.Fatalf("readable DocLabel fallback lost: %+v", byID["k-machine-fallback"])
	}
	if byID["sess-1"].Kind != "会话" || !byID["sess-1"].IsSession || byID["sess-1"].Title != "" {
		t.Fatalf("raw_session normalization failed: %+v", byID["sess-1"])
	}
	if byID["sem-1"].Title != "" {
		t.Fatalf("semantic hit leaked machine name: %+v", byID["sem-1"])
	}
	if title, _ := resultText(byID["sem-1"], "WAL"); title != "WAL checkpoint tuning" {
		t.Fatalf("semantic hit fallback did not use snippet: %q", title)
	}
}
