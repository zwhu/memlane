package overview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type testFence struct{ auth uint64 }

func (*testFence) Epoch() uint64           { return 1 }
func (f *testFence) AuthEpoch() uint64     { return f.auth }
func (*testFence) ReadingIdentity() string { return "reading-id" }
func (f *testFence) InvalidateReading()    { f.auth++ }

type testTheme struct{ tokens *kitui.Tokens }

func (t testTheme) Tokens() *kitui.Tokens { return t.tokens }
func (testTheme) ReduceMotion() bool      { return false }

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func overviewTestClient(t *testing.T, handler http.HandlerFunc) *api.Client {
	t.Helper()
	// Exercise real HTTP where the runner permits loopback sockets.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	cfg := api.Config{ReadingIdentity: "reading-id"}
	switch {
	case err == nil:
		srv := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
		srv.Start()
		t.Cleanup(srv.Close)
		cfg.BaseURL, cfg.HTTPClient = srv.URL, srv.Client()
	case errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES):
		t.Logf("loopback listener unavailable (%v); using httptest recorder transport", err)
		cfg.BaseURL = "http://127.0.0.1"
		cfg.HTTPClient = &http.Client{Transport: handlerTransport{handler}}
	default:
		t.Fatal(err)
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func applyNext(t *testing.T, queued <-chan func()) {
	t.Helper()
	select {
	case apply := <-queued:
		apply()
	case <-time.After(3 * time.Second):
		t.Fatal("request did not publish")
	}
}

func overviewDeps(client *api.Client) (Deps, *testFence, chan func()) {
	queued := make(chan func(), 8)
	fence := &testFence{}
	deps := Deps{
		Client: client, Fence: fence, Dispatcher: shell.DispatchFunc(func(fn func()) { queued <- fn }),
		Projects: func() []shell.ProjectRef {
			return []shell.ProjectRef{{ID: "project", DatasetID: "ds", Name: "Project"}}
		},
	}
	return deps, fence, queued
}

func TestBootstrapAndSearchHistoryFromAPI(t *testing.T) {
	now := time.Now()
	day := now.Format("2006-01-02")
	at := now.UTC().Format(time.RFC3339Nano)
	var bootstrapCalls, historyCalls atomic.Int32
	client := overviewTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("X-Memlane-Reading-Identity") != "reading-id" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/api/overview/bootstrap":
			bootstrapCalls.Add(1)
			if r.URL.Query().Get("days") != "365" {
				t.Errorf("unexpected bootstrap query: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(api.OverviewSnapshot{
				TotalKnowledge: 271, TotalRaw: 473, DayBuckets: []api.DayBucket{{Day: day, Count: 3}},
				RecentItems: []api.CatalogItem{{DocID: "doc", DatasetID: "ds", Title: "Memory title", EffectiveDate: day}},
				RecallEvents: []api.OverviewRecall{{AgentKind: "codex", AgentID: "agent", EventID: "recall", Channel: "automatic",
					ReceivedAt: at, Hits: []api.OverviewRecallHit{{DatasetID: "ds", DocumentID: "doc"}}}},
				Searches: []api.SearchHistoryEntry{{Query: "initial user search", SearchedAt: at}},
			})
		case "/api/search/recent":
			historyCalls.Add(1)
			json.NewEncoder(w).Encode(api.RecentSearchResponse{RecentSearches: []api.SearchHistoryEntry{
				{Query: "new user search", SearchedAt: at},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	})
	deps, _, queued := overviewDeps(client)
	var selected string
	deps.OpenRecalled = func(event RecallEvent, dataset string) { selected = event.ID + "/" + dataset }
	c := New(deps)
	c.Load()
	applyNext(t, queued)
	v := c.State
	if bootstrapCalls.Load() != 1 || historyCalls.Load() != 0 || !v.Loaded || !v.ActivityLoaded || !v.HistoryLoaded ||
		v.V2TotalKnowledge != 271 || v.Snapshot.Raw != 473 || c.CachedBuckets(now)[day] != 3 ||
		len(v.Snapshot.Knowledge) != 1 || v.Snapshot.Knowledge[0].ID != "doc" ||
		len(v.Events) != 1 || v.Events[0].Recall == nil || len(v.History) != 1 ||
		v.History[0].Title != "initial user search" || v.History[0].Recall != nil {
		t.Fatalf("unexpected single-shot hydration: %+v", v)
	}
	tester := ui.NewTester(c.RenderHome, 1040, 820)
	tester.Advance(time.Second)
	if err := tester.Click("Codex 自动召回 · Project · 1 条记忆"); err != nil {
		t.Fatal(err)
	}
	if selected != "recall/ds" {
		t.Fatalf("recall callback = %q", selected)
	}
	c.SetSearchHistory(true)
	applyNext(t, queued)
	if historyCalls.Load() != 1 || bootstrapCalls.Load() != 1 || v.History[0].Title != "new user search" || v.Events[0].Recall == nil {
		t.Fatal("search history refresh crossed into agent recall")
	}
}

func TestDayClickPaginationAndInvalidCursor(t *testing.T) {
	now := time.Now()
	day := now.Format("2006-01-02")
	var firstPages atomic.Int32
	client := overviewTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/overview/bootstrap":
			json.NewEncoder(w).Encode(api.OverviewSnapshot{TotalKnowledge: 390, DayBuckets: []api.DayBucket{{Day: day, Count: 390}}})
		case "/api/overview/day":
			q := r.URL.Query()
			if q.Get("day") != day || q.Get("limit") != "30" {
				t.Errorf("unexpected day request: %s", r.URL)
			}
			page := 0
			cursor := q.Get("cursor")
			if cursor == "" {
				firstPages.Add(1)
			} else if _, err := fmt.Sscan(cursor, &page); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"invalid_cursor"}`)
				return
			}
			items := make([]api.CatalogItem, 30)
			for n := range items {
				id := fmt.Sprintf("doc-%03d", page*30+n)
				items[n] = api.CatalogItem{DocID: id, Title: id, DatasetID: "ds", EffectiveDate: day}
			}
			json.NewEncoder(w).Encode(api.KnowledgePage{TotalCount: 390, Items: items, NextCursor: fmt.Sprint(page + 1), HasMore: true})
		default:
			http.NotFound(w, r)
		}
	})
	deps, _, queued := overviewDeps(client)
	c := New(deps)
	c.Load()
	applyNext(t, queued)
	tester := ui.NewTester(c.RenderHome, 1040, 820)
	tester.Advance(time.Second)
	if err := tester.Click(day + " · 新增 390 条记忆"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, queued)
	v := c.State
	if v.Day != day || v.DayTotal != 390 || len(v.DayItems) != 30 || v.DayNextCursor != "1" {
		t.Fatalf("initial day page: %+v", v)
	}
	for n := 0; n < 10; n++ {
		c.LoadDay(day, true)
		applyNext(t, queued)
	}
	if len(v.DayItems) != maxDayItems || v.DayItems[0].ID != "doc-030" || v.DayItems[len(v.DayItems)-1].ID != "doc-329" {
		t.Fatalf("incorrect sliding window: len=%d", len(v.DayItems))
	}
	v.DayNextCursor = "invalid"
	c.LoadDay(day, true)
	applyNext(t, queued)
	applyNext(t, queued)
	if firstPages.Load() != 2 || len(v.DayItems) != 30 || v.DayItems[0].ID != "doc-000" || v.DayError != "" {
		t.Fatal("invalid cursor did not recover to the first page")
	}
}

func TestFenceDropsQueuedResultsAndWithdrawsOnDenial(t *testing.T) {
	var deny atomic.Bool
	client := overviewTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if deny.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(api.OverviewSnapshot{TotalRaw: 10, RecentItems: []api.CatalogItem{{DocID: "private"}}})
	})
	deps, fence, queued := overviewDeps(client)
	c := New(deps)
	c.Load()
	fence.InvalidateReading()
	applyNext(t, queued)
	if c.State.Loaded || len(c.State.Snapshot.Knowledge) != 0 {
		t.Fatal("revoked callback populated overview")
	}
	c.Load()
	applyNext(t, queued)
	if !c.State.Loaded {
		t.Fatal("fresh request did not publish")
	}
	deny.Store(true)
	c.Load()
	applyNext(t, queued)
	if fence.auth != 2 || c.State.Loaded || c.State.Loading || len(c.State.Snapshot.Knowledge) != 0 || c.State.Error == "" {
		t.Fatal("403 did not invalidate scope and clear cached data")
	}
}

func TestFailureDetailRedactsServiceAndLogs(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	if err := os.Mkdir(logs, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "official.log"), []byte(`2026-11-13T03:34:38Z [ERROR] BrokenPipeError
File "/runtime/main.py", line 22, in run
File "/runtime/main.py", line 22, in run private-trailing-payload
Authorization: Bearer secret-log-token
Question: private-session-body
`), 0600); err != nil {
		t.Fatal(err)
	}
	event := Event{Key: "run-id", Title: "召回记忆", State: "失败", Failures: []ActivityRow{{
		Run: "run-id", Operation: "recall", ErrorClass: "BrokenPipeError", Created: "2026-11-13T03:34:38Z",
		Origin: "Authorization: Bearer private-row-token",
	}}}
	cfg := DiagnosticSource{OfficialData: root, ServiceURL: "http://user:private-password@127.0.0.1:9999/?token=query-token"}
	detail := BuildFailureDetail(event, cfg, time.UTC)
	if !detail.HasLogCandidate || detail.ReasonLabel != "连接管道已断开" || detail.FailureCount != 1 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	report := FailureReport(event, cfg)
	for _, expected := range []string{"run-id", "127.0.0.1:9999", "main.py", "BrokenPipeError"} {
		if !strings.Contains(report, expected) {
			t.Errorf("report missing %s", expected)
		}
	}
	for _, secret := range []string{"private-password", "query-token", "secret-log-token", "private-session-body", "private-trailing-payload", "private-row-token"} {
		if strings.Contains(report, secret) {
			t.Errorf("report includes %s", secret)
		}
	}
}

func TestHeatmapCanvasFrameBudget(t *testing.T) {
	now := time.Now()
	for _, appearance := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		t.Run(appearance.name, func(t *testing.T) {
			c := New(Deps{Theme: testTheme{kitui.TokensFor(appearance.dark)}})
			v := c.State
			v.Loaded, v.RawLoaded, v.V2Mode, v.V2TotalKnowledge = true, true, true, 730
			v.V2DayBuckets = make(map[string]int)
			for offset := 0; offset < 365; offset++ {
				v.V2DayBuckets[now.AddDate(0, 0, -offset).Format("2006-01-02")] = 2
			}
			const sampleTick = 500 * time.Millisecond
			builds := 0
			tester := ui.NewTester(func(ctx *ui.Context) {
				builds++
				ctx.After(sampleTick)
				c.RenderHeatmap(ctx, now)
			}, 1040, 250)
			for range 4 {
				tester.Frame()
			}
			if len(v.HeatmapCache) != 365 {
				t.Fatalf("paint cache contains %d cells", len(v.HeatmapCache))
			}
			if _, ok := tester.Find("记忆分布热力图"); !ok {
				t.Fatal("canvas missing")
			}
			if _, ok := tester.Find(now.Format("2006-01-02") + " · 新增 2 条记忆"); !ok {
				t.Fatal("accessible today button missing")
			}
			if _, ok := tester.Find(now.AddDate(0, 0, -1).Format("2006-01-02") + " · 新增 2 条记忆"); ok {
				t.Fatal("historical cells should be painted rather than individual controls")
			}
			c.StartEntrance(false)
			tester.Frame()
			if !tester.FramePending() {
				t.Fatal("wave did not request a paint frame")
			}
			tester.Advance(time.Second)
			samples := make([]time.Duration, 15)
			for i := range samples {
				before, start := builds, time.Now()
				tester.Advance(sampleTick)
				samples[i] = time.Since(start)
				if builds-before != 1 {
					t.Fatalf("expected one full frame per sample, got %d", builds-before)
				}
			}
			slices.Sort(samples)
			median := samples[len(samples)/2]
			t.Logf("median frame %v", median)
			if median >= 16*time.Millisecond {
				t.Fatalf("median exceeds 16ms: %v", median)
			}
		})
	}
}
