package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

func TestTrayFlyoutCachesSummaryAndTodayCountAcrossFrames(t *testing.T) {
	dir := t.TempDir()
	a := settingsFixture()
	a.Config.ConfigFile = filepath.Join(dir, "runtime.json")
	a.initDesktopPreferences()
	a.service = "已连接"
	a.monitor.State = "ready"

	now := time.Now()
	nowRFC := now.Format(time.RFC3339)
	a.overviewState.Loaded = true
	a.overviewState.Generation = 1
	a.overviewState.Snapshot = overviewSnapshot{
		Knowledge: []Item{
			{ID: "k1", Dataset: "ds", Kind: "知识", Date: nowRFC, Document: &Document{ID: "k1", Created: nowRFC}},
			{ID: "k2", Dataset: "ds", Kind: "知识", Date: nowRFC, Document: &Document{ID: "k2", Created: nowRFC}},
		},
	}

	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.InboxFailed = 1

	// Warm both tray status level and full flyout body: must share a single summary computation.
	_ = a.trayStatusLevel()
	tt := ui.NewTester(a.TrayFlyoutView, 300, 480)
	for i := 0; i < 8; i++ {
		_ = a.trayStatusLevel()
		_, _, _ = a.TrayIconState()
		_ = a.trayProjectAlerts()
		_ = a.trayProjectProgressItems()
		_ = a.trayTodayMemoryCount(now)
		tt.Frame()
	}
	if a.trayCache.summaryComputations != 1 {
		t.Fatalf("expected single cached summary computation across trayStatusLevel + flyout frames, got %d", a.trayCache.summaryComputations)
	}
	if a.trayCache.todayComputations != 1 {
		t.Fatalf("expected single cached todayCount computation across frames, got %d", a.trayCache.todayComputations)
	}

	// Mutating queue state invalidates the cached summary immediately.
	q.InboxFailed = 0
	if level := a.trayStatusLevel(); level != "ok" {
		t.Fatalf("expected trayStatusLevel=ok after observe cleared anomaly, got %q", level)
	}
	if a.trayCache.summaryComputations != 2 {
		t.Fatalf("expected summary cache invalidation after observe, got %d computations", a.trayCache.summaryComputations)
	}

	// Advancing overview generation invalidates todayCount cache.
	a.overviewState.Generation++
	a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge,
		Item{ID: "k3", Dataset: "ds", Kind: "知识", Date: nowRFC, Document: &Document{ID: "k3", Created: nowRFC}})
	if got := a.trayTodayMemoryCount(now); got != 3 {
		t.Fatalf("expected updated today count=3, got %d", got)
	}
	if a.trayCache.todayComputations != 2 {
		t.Fatalf("expected today cache invalidation on generation bump, got %d", a.trayCache.todayComputations)
	}
}

func TestUserProfileLargeMarkdownVirtualizedRenderingLightAndDark(t *testing.T) {
	t.Cleanup(func() { applyAppearance(false) })

	// Build an ~80 KiB Markdown profile with headings, prose, lists, and fenced code blocks.
	var sb strings.Builder
	sb.WriteString("# 大篇幅用户画像基线\n\n")
	for sec := 0; sec < 60; sec++ {
		fmt.Fprintf(&sb, "## 第 %d 节：工程实践与协作习惯\n\n", sec)
		fmt.Fprintf(&sb, "- [2026-11-20] - 保持零侵入外置扩展与严格权限边界，段落索引 %d。\n", sec)
		sb.WriteString(strings.Repeat("这是一段用于验证 256 KiB 画像分块虚拟化渲染性能与正确性的长中文说明文本。", 12))
		sb.WriteString("\n\n```go\n")
		fmt.Fprintf(&sb, "func section%d() {\n\t// blank line inside fence must not split chunk\n\n\tprintln(%d)\n}\n", sec, sec)
		sb.WriteString("```\n\n")
	}
	largeBody := sb.String()
	if len(largeBody) < 40*1024 {
		t.Fatalf("expected large profile body > 40KiB, got %d", len(largeBody))
	}

	chunks := splitProfileMarkdownChunks(largeBody)
	if len(chunks) < 10 {
		t.Fatalf("expected large profile split into multiple chunks, got %d", len(chunks))
	}
	for idx, ch := range chunks {
		// Every chunk must have balanced ``` fences (never split inside a code block).
		if count := strings.Count(ch, "```"); count%2 != 0 {
			t.Fatalf("chunk %d split inside a fenced code block (``` count=%d):\n%s", idx, count, ch)
		}
	}

	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "light", dark: false},
		{name: "dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := profileUIFixture(t)
			if tc.dark {
				a.setAppearancePreference(appearanceDark)
			} else {
				a.setAppearancePreference(appearanceLight)
			}
			a.userProfiles.Body = largeBody
			a.userProfiles.BodyKey = "codex/synthetic/large-" + tc.name
			tt := ui.NewTester(a.View, 1200, 820)
			tt.SetDark(tc.dark)
			// Warm up 2 frames.
			tt.Frame()
			tt.Frame()

			if !tt.HasText("Codex 对你的了解") || !tt.HasText("大篇幅用户画像基线") {
				t.Fatalf("[%s] virtualized large profile missing header/title text", tc.name)
			}
			firstVis, lastVis := a.userProfiles.ReaderList.Visible()
			if lastVis < firstVis || lastVis >= len(chunks) {
				t.Fatalf("[%s] expected virtualized subset of %d chunks, got visible=[%d,%d]", tc.name, len(chunks), firstVis, lastVis)
			}

			// Measure 10 frames and verify virtualized rendering stays well within 16ms budget.
			samples := make([]time.Duration, 0, 10)
			for n := 0; n < 10; n++ {
				start := time.Now()
				tt.Frame()
				samples = append(samples, time.Since(start))
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			if !raceDetectorEnabled {
				if median := samples[len(samples)/2]; median > 16*time.Millisecond {
					t.Fatalf("[%s] large virtualized profile median frame %v exceeds 16ms budget", tc.name, median)
				}
			}

			// Verbatim copy still copies the entire raw body.
			clickProjectUI(t, tt, "复制正文")
			if tt.Clipboard() != largeBody {
				t.Fatalf("[%s] verbatim copy truncated or altered large body (got %d bytes want %d)", tc.name, len(tt.Clipboard()), len(largeBody))
			}
		})
	}
}

func TestStartTitlesCapsAt30EvenWithRawCatalogAndInvalidateReadingClearsPreviewAndMetadata(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"session_id":"s","dataset_id":"ds","user_id":"u","history_paging":true,"page_limit":20,"qas":[{"qa_id":"q1","question":"Q","answer":"A"}],"msg_count":1}`)
	}))
	defer server.Close()

	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "ds"}}}, nil)
	updates := make(chan func(), 256)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "知识"
	a.kind = "原始记录"
	s := a.state()

	const total = 120
	items := make([]Item, 0, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("sess-%03d", i)
		sess := Session{ID: id, Dataset: "ds", User: "u", Updated: "2026-11-21T08:00:00Z"}
		items = append(items, Item{ID: id, Dataset: "ds", Kind: "会话", Title: id, Session: &sess})
	}
	s.Items = items
	s.RawCatalog = append([]Item(nil), items...)

	a.startTitles(s)
	// Wait until the bounded <=30 goroutines complete.
	drainReading(t, updates, func() bool {
		done := 0
		for _, it := range s.Items {
			if it.ReadingState == "ready" || it.ReadingState == "failed" || it.ReadingState == "unavailable" {
				done++
			}
		}
		return done >= maxTitlePreheatWindow
	})
	// Brief settle to catch any rogue extra requests beyond 30.
	time.Sleep(30 * time.Millisecond)
	if got := hits.Load(); got > maxTitlePreheatWindow {
		t.Fatalf("startTitles exceeded 30-item preheat cap with RawCatalog != nil: got %d requests", got)
	}

	// Verify InvalidateReading clears Item.Preview, overviewState.Snapshot.Knowledge[n].Preview, and Document.Metadata.
	d1 := Document{ID: "doc-1", Dataset: "ds", Name: "doc1.md", Metadata: map[string]any{"why_learned": "secret", "node_set": []any{"session_learnings"}}}
	d2 := Document{ID: "doc-2", Dataset: "other", Name: "doc2.md", Metadata: map[string]any{"why_learned": "keep"}}
	s.Items = []Item{
		{ID: "doc-1", Dataset: "ds", Kind: "知识", Preview: "sensitive preview", Body: "full body", BodyLoaded: true, Document: &d1},
		{ID: "doc-2", Dataset: "other", Kind: "知识", Preview: "keep preview", Body: "keep body", BodyLoaded: true, Document: &d2},
	}
	s.Knowledge.Catalog = []Item{
		{ID: "doc-1", Dataset: "ds", Kind: "知识", Preview: "catalog preview", Body: "catalog body", BodyLoaded: true, Document: &Document{ID: "doc-1", Dataset: "ds", Name: "doc1.md", Metadata: map[string]any{"secret": "x"}}},
	}
	a.overviewState.Snapshot.Knowledge = []Item{
		{ID: "doc-1", Dataset: "ds", Kind: "知识", Preview: "overview preview", Body: "overview body", BodyLoaded: true, Document: &Document{ID: "doc-1", Dataset: "ds", Name: "doc1.md", Metadata: map[string]any{"secret": "y"}}},
		{ID: "doc-2", Dataset: "other", Kind: "知识", Preview: "other preview", Body: "other body", BodyLoaded: true, Document: &Document{ID: "doc-2", Dataset: "other", Name: "doc2.md", Metadata: map[string]any{"secret": "z"}}},
	}

	a.InvalidateReading("ds")
	if s.Items[0].Preview != "" || s.Items[0].Body != "" || s.Items[0].BodyLoaded || s.Items[0].Document.Metadata != nil {
		t.Fatalf("InvalidateReading leaked s.Items fields: %+v doc=%+v", s.Items[0], s.Items[0].Document)
	}
	if len(s.Knowledge.Catalog) != 0 {
		t.Fatalf("InvalidateReading retained revoked rows in s.Knowledge.Catalog: %+v", s.Knowledge.Catalog)
	}
	if a.overviewState.Snapshot.Knowledge[0].Preview != "" || a.overviewState.Snapshot.Knowledge[0].Body != "" || a.overviewState.Snapshot.Knowledge[0].Document.Metadata != nil {
		t.Fatalf("InvalidateReading leaked overview knowledge fields: %+v doc=%+v", a.overviewState.Snapshot.Knowledge[0], a.overviewState.Snapshot.Knowledge[0].Document)
	}
	if s.Items[1].Preview != "keep preview" || s.Items[1].Document.Metadata == nil || a.overviewState.Snapshot.Knowledge[1].Preview != "other preview" {
		t.Fatalf("InvalidateReading(ds) unexpectedly cleared unrelated dataset")
	}
}

func TestMarkKnowledgeReadAsyncCoalescedThroughGateway(t *testing.T) {
	const url = "http://127.0.0.1:16141"
	a, updates := p1TestApp(t, url, url, p1TempToken(t))
	project := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(project)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	batches := make(chan []string, 3)
	a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/reading-state/mark-read" {
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var req api.MarkReadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		batches <- req.DocIDs
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{ReadingStateView: api.ReadingStateView{StateRev: int64(calls.Load())}})
	})}
	mark := func(id string, loaded, truncated bool) {
		doc := unreadDoc(id)
		a.markKnowledgeRead(Item{ID: id, Dataset: project.Dataset, Kind: "知识", Document: &doc, BodyLoaded: loaded, BodyTruncated: truncated})
	}
	mark("unloaded", false, false)
	mark("truncated", true, true)
	if len(q.ReadIDs) != 0 {
		t.Fatal("partial body changed unread state")
	}
	mark("first", true, false)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("mark-read did not start")
	}
	mark("second", true, false)
	mark("third", true, false)
	if !q.ReadIDs["third"] {
		t.Fatal("optimistic update did not happen on the UI thread")
	}
	close(release)
	a.waitKnowledgeReadFlush()
	for len(updates) > 0 {
		(<-updates)()
	}
	first, second := <-batches, <-batches
	if calls.Load() != 2 || len(first) != 1 || len(second) != 2 || q.ServerUnreadStateRev != 2 {
		t.Fatalf("unexpected batches %v %v, revision %d", first, second, q.ServerUnreadStateRev)
	}
}

func TestManagedPointLookupsUseOnlyUnifiedRoutes(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "manager.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("t", 40)), 0600); err != nil {
		t.Fatal(err)
	}

	t.Run("point reads and fail-closed errors", func(t *testing.T) {
		var legacyCalls atomic.Int32
		var capCalls atomic.Int32
		var docStatus atomic.Int32
		docStatus.Store(200)

		control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/capabilities":
				capCalls.Add(1)
				fmt.Fprint(w, `{"api_versions":["v1"],"reading_contract":"catalog-cursor-v1","features":["document_lookup_v2","session_lookup_v2","code_activity_v2","tool_graph_batched_v2"]}`)
			case "/v1/datasets/ds-1/documents/doc-1":
				switch docStatus.Load() {
				case 403:
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"error":"project_unconfirmed"}`)
				case 404:
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":"document_not_found"}`)
				default:
					fmt.Fprint(w, `{"id":"doc-1","datasetId":"ds-1","name":"lesson.md","mimeType":"text/markdown","externalMetadata":{"node_set":["session_learnings"],"lesson_type":"pitfall","session_id":"sess-1"},"updatedAt":"2026-11-21T08:00:00Z","createdAt":"2026-11-20T08:00:00Z","contentHash":"abc","effective_date":"2026-11-19","category":"踩坑经验","common_category":"","session_id":"sess-1","recall_count":4}`)
				}
			case "/v1/datasets/ds-1/sessions/sess-1":
				fmt.Fprint(w, `{"session_id":"sess-1","dataset_id":"ds-1","user_id":"user-1","agent_id":"codex","last_activity_at":"2026-11-21T09:00:00Z","started_at":"2026-11-21T08:00:00Z","status":"active"}`)
			case "/v1/projects/proj-1/code-activity":
				fmt.Fprint(w, `{"project_id":"proj-1","code_dataset_id":"code-ds-1","dataset_created_at":"2026-11-20T00:00:00Z","active":true,"status":"running","run_id":"run-42","latest_created_at":"2026-11-21T09:00:00Z","latest_started_at":"2026-11-21T09:00:00Z","latest_ended_at":""}`)
			default:
				http.NotFound(w, r)
			}
		}))
		defer control.Close()

		official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			legacyCalls.Add(1)
			http.Error(w, "legacy endpoint must not be called when v2 succeeds or fails closed", 500)
		}))
		defer official.Close()

		client := Client{
			Config: Config{
				ServiceURL:      official.URL,
				ClientStateFile: filepath.Join(dir, "state.json"),
				ProjectControl:  &ProjectControlConfig{URL: control.URL, ManagementTokenFile: tokenFile},
			},
		}
		ctx := context.Background()

		// 1. O(1) document lookup
		docItem, err := client.documentInfoByID(ctx, "ds-1", "doc-1")
		if err != nil || docItem.ID != "doc-1" || docItem.Kind != "知识" || docItem.Date != "2026-11-19" || docItem.Source != "sess-1" || docItem.RecallStats == nil || docItem.RecallStats.Count != 4 {
			t.Fatalf("unexpected v2 document lookup: item=%+v err=%v", docItem, err)
		}
		// 2. O(1) session lookup
		sessItem, err := client.source(ctx, Item{Dataset: "ds-1", Source: "sess-1"})
		if err != nil || sessItem.ID != "sess-1" || sessItem.Session == nil || sessItem.Session.User != "user-1" || sessItem.Session.Agent != "codex" {
			t.Fatalf("unexpected v2 session lookup: item=%+v err=%v", sessItem, err)
		}
		// 3. O(1) code activity lookup
		act, err := client.codeActivity(ctx, Project{ID: "proj-1", CodeDataset: "code-ds-1"})
		if err != nil || !act.Active || act.Status != "running" || act.Run != "run-42" {
			t.Fatalf("unexpected v2 code activity: state=%+v err=%v", act, err)
		}
		// Non-colliding routes use only their own response and authorization.
		if capCalls.Load() != 0 {
			t.Fatalf("point reads issued %d capability probes", capCalls.Load())
		}

		// 4. 403 project_unconfirmed fails closed without calling legacy.
		docStatus.Store(403)
		if _, err := client.documentInfoByID(ctx, "ds-1", "doc-1"); !isControlAuthDenied(err) {
			t.Fatalf("expected 403 fail-closed error, got %v", err)
		}

		// 5. 404 document_not_found returns immediately without calling legacy pagination.
		docStatus.Store(404)
		if _, err := client.documentInfoByID(ctx, "ds-1", "doc-1"); err == nil || !strings.Contains(err.Error(), "官方未提供") {
			t.Fatalf("expected not-found error on document_not_found, got %v", err)
		}

		if legacyCalls.Load() != 0 {
			t.Fatalf("legacy official endpoints were unexpectedly hit %d times", legacyCalls.Load())
		}
	})

	t.Run("missing managed endpoints never scan official lists", func(t *testing.T) {
		var officialReads atomic.Int32
		control := httptest.NewServer(http.HandlerFunc(http.NotFound))
		defer control.Close()
		official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			officialReads.Add(1)
			http.NotFound(w, r)
		}))
		defer official.Close()
		client := Client{Config: Config{
			ServiceURL:     official.URL,
			ProjectControl: &ProjectControlConfig{URL: control.URL, ManagementTokenFile: tokenFile},
		}}
		ctx := context.Background()
		if _, err := client.documentInfoByID(ctx, "ds", "doc"); !isControlNotFound(err) {
			t.Fatalf("document lookup: %v", err)
		}
		if _, err := client.source(ctx, Item{Dataset: "ds", Source: "session"}); !isControlNotFound(err) {
			t.Fatalf("session lookup: %v", err)
		}
		if _, err := client.codeActivity(ctx, Project{ID: "project", CodeDataset: "code"}); !isControlNotFound(err) {
			t.Fatalf("code activity: %v", err)
		}
		if officialReads.Load() != 0 {
			t.Fatalf("unified reads fell back to %d official requests", officialReads.Load())
		}
	})
}
