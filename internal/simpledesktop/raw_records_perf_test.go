package simpledesktop

import (
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
)

func TestRawRecordsColdStartAndSessionOpenPerformanceBudget(t *testing.T) {
	const (
		totalSessions = 120
		totalDocs     = 20
	)
	now := time.Now().UTC()
	docs := make([]Document, 0, totalDocs)
	for n := 0; n < totalDocs; n++ {
		ts := now.Add(-time.Duration(n*3) * time.Minute).Format(time.RFC3339)
		docs = append(docs, Document{
			ID:        fmt.Sprintf("raw-doc-%03d", n),
			Dataset:   "ds-main",
			Name:      fmt.Sprintf("notes_%03d.md", n),
			MIME:      "text/markdown",
			Updated:   ts,
			Created:   ts,
			Metadata:  map[string]any{"node_set": []any{"project_docs"}},
		})
	}
	sessions := make([]rawSessionEntry, 0, totalSessions)
	for n := 0; n < totalSessions; n++ {
		ts := now.Add(-time.Duration(n) * time.Minute).Format(time.RFC3339)
		sessions = append(sessions, rawSessionEntry{
			Session: Session{
				ID:      fmt.Sprintf("codex_sess-%03d", n),
				Dataset: "ds-main",
				User:    "11111111-1111-1111-1111-111111111111",
				Agent:   "codex",
				Updated: ts,
				Started: ts,
			},
			Label:     fmt.Sprintf("检查原始记录性能优化第 %d 条会话主题", n),
			MsgCount:  8,
			ToolCalls: 42,
		})
	}
	payload, err := json.Marshal(rawRecordsResponse{
		DatasetID:  "ds-main",
		CacheReady: true,
		Documents:  docs,
		Sessions:   sessions,
	})
	if err != nil {
		t.Fatal(err)
	}

	var rawRecordsCalls atomic.Int32
	var rawHistoryCalls atomic.Int32
	var legacyFallbackCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/capabilities":
			http.NotFound(w, r)
		case r.URL.Path == "/v1/raw-records":
			rawRecordsCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		case r.URL.Path == "/v1/raw-records/session-history":
			rawHistoryCalls.Add(1)
			sid := r.URL.Query().Get("session_id")
			page := SessionHistoryPage{
				Paging:      true,
				QAOffset:    0,
				TraceOffset: 0,
				Limit:       20,
				QAMore:      false,
				TraceMore:   true,
				SessionDetail: SessionDetail{
					Session: Session{
						ID:      sid,
						Dataset: "ds-main",
						User:    "11111111-1111-1111-1111-111111111111",
						Updated: now.Format(time.RFC3339),
					},
					Label:     "检查原始记录性能优化第 0 条会话主题",
					Count:     1,
					ToolCount: 25,
					QAs: []QA{
						{ID: "qa-1", Question: "为什么原始记录打开很慢？", Answer: "已通过只读 SQLite 聚合和分页查询加速。"},
					},
					Traces: func() []Trace {
						out := make([]Trace, 0, 20)
						for i := 0; i < 20; i++ {
							out = append(out, Trace{ID: fmt.Sprintf("tr-%d", i), Name: "Read", Status: "success"})
						}
						return out
					}(),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(page)
		default:
			legacyFallbackCalls.Add(1)
			http.Error(w, "unexpected fallback: "+r.URL.Path, 500)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "manager.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ServiceURL:     server.URL,
		ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: tokenFile},
		Projects:       []Project{{ID: "p1", Name: "memlane", Path: "/synthetic/memlane", Dataset: "ds-main"}},
	}
	a := New(cfg, nil)
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.service = "已连接"
	updates := make(chan func(), 64)
	a.Update = func(fn func()) { updates <- fn }
	t.Cleanup(func() { a.cancelAndResetPanes() })

	t0 := time.Now()
	a.navigateKind("知识", 0, "原始记录")
	s := a.state()
	drainReading(t, updates, func() bool { return s.Loaded && !s.Loading && !s.Reading })
	tt := ui.NewTester(a.View, 1200, 820)
	coldReady := time.Since(t0)

	if rawRecordsCalls.Load() != 1 {
		t.Fatalf("expected 1 fast raw-records call, got %d", rawRecordsCalls.Load())
	}
	if rawHistoryCalls.Load() != 1 {
		t.Fatalf("expected 1 fast history call for auto-selected item (0 N+1 title calls), got %d", rawHistoryCalls.Load())
	}
	if legacyFallbackCalls.Load() != 0 {
		t.Fatalf("unexpected legacy HTTP fallback calls: %d", legacyFallbackCalls.Load())
	}
	if coldReady > 150*time.Millisecond {
		t.Errorf("cold-start Raw Records ready time %v exceeds 150ms budget", coldReady)
	}
	if !s.Loaded || len(s.Items) != 30 || !s.More || s.Selected == nil || s.Selected.Detail == nil {
		t.Fatalf("raw records state incomplete: loaded=%v items=%d more=%v selected=%v", s.Loaded, len(s.Items), s.More, s.Selected != nil)
	}
	if s.Items[0].Title == s.Items[0].ID || s.Items[0].SessionSummary == "" {
		t.Fatalf("session list item did not hydrate title/summary atomically: %+v", s.Items[0])
	}

	frames := make([]time.Duration, 0, 15)
	for n := 0; n < 15; n++ {
		f0 := time.Now()
		tt.Frame()
		frames = append(frames, time.Since(f0))
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	if median := frames[len(frames)/2]; median > 12*time.Millisecond {
		t.Errorf("raw-records median frame %v exceeds 12ms budget", median)
	}
	if worst := frames[len(frames)-1]; worst > 25*time.Millisecond {
		t.Errorf("raw-records worst frame %v exceeds 25ms budget", worst)
	}
}

func setTestAppearance(t *testing.T, a *App, mode string) {
	t.Helper()
	a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	a.initDesktopPreferences()
	a.setAppearancePreference(mode)
	t.Cleanup(func() { applyAppearance(false) })
}

func TestRawRecordsUIParityAndMotionWithKnowledge(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a := projectUIFixture()
			setTestAppearance(t, a, appearance)
			a.page = "知识"
			a.kind = "原始记录"
			s := a.state()

			// 1. Cold-start loading state: 5 skeleton rows on left, no "选择一条原始记录" flash on right, no duplicate "已载入".
			s.Loaded = false
			s.Loading = true
			s.Items = nil
			s.Selected = nil
			tt := ui.NewTester(a.View, 1200, 820)
			if !tt.HasText("正在读取原始记录…") {
				t.Fatalf("[%s] missing 5-row skeleton placeholder matching knowledge view: %v", appearance, tt.Texts())
			}
			for _, unwanted := range []string{"选择一条原始记录", "已载入 0 项", "已载入 0 条", "更新中"} {
				if tt.HasText(unwanted) {
					t.Fatalf("[%s] raw records loading flashed %q: %v", appearance, unwanted, tt.Texts())
				}
			}

			// 2. Loaded state: V4 footer, KeepOffset enabled, Page Rise + Stagger Cascade + Doc Glide armed.
			now := time.Now().UTC().Format(time.RFC3339)
			sess := Session{ID: "codex_demo", Dataset: "ds", User: "u1", Updated: now}
			doc := Document{ID: "doc-1", Dataset: "ds", Name: "README.md", Updated: now, Created: now}
			allItems := []Item{
				{ID: sess.ID, Dataset: "ds", Kind: "会话", Title: "排查原始记录加载性能", SessionSummary: "已保存 3 轮问答 · 8 条工具记录", Date: now, Session: &sess, ReadingState: "ready", BodyLoaded: true, Detail: &SessionDetail{Session: sess, Count: 3, ToolCount: 8, QAs: []QA{{ID: "q1", Question: "Q", Answer: "A"}}}},
				{ID: doc.ID, Dataset: "ds", Kind: "文档", Title: doc.Name, Date: now, Document: &doc},
			}
			s.Loaded = true
			s.Loading = false
			s.RawCatalog = allItems
			s.Items = allItems
			s.Selected = &allItems[0]
			a.motionRuns.rawPage++
			a.motionRuns.rawList++
			tt.Frame()
			tt.Frame()
			if !s.WidgetList.KeepOffset {
				t.Fatalf("[%s] raw-records list must enable WidgetList.KeepOffset", appearance)
			}
			if a.motionRuns.rawPage == 0 || a.motionRuns.rawList == 0 || a.motionRuns.docEnter == 0 {
				t.Fatalf("[%s] expected Page Rise, Stagger Cascade, and Doc Glide runs armed: %+v", appearance, a.motionRuns)
			}
			if !tt.HasText("已载入 2 项") || !tt.HasText("共 2 条") {
				t.Fatalf("[%s] footer did not match knowledge V4 format: %v", appearance, tt.Texts())
			}

			// 3. Sub-filter switch (全部 -> 会话 -> 文档) keeps rawPage fixed and seeds immediately without skeleton flash.
			pageRunBefore := a.motionRuns.rawPage
			listRunBefore := a.motionRuns.rawList
			a.switchProjectKind("会话")
			tt.Frame()
			if a.motionRuns.rawPage != pageRunBefore || a.motionRuns.rawList != listRunBefore+1 {
				t.Fatalf("[%s] sub-filter switch should only replay rawList (%d->%d), keep rawPage (%d->%d)",
					appearance, listRunBefore, a.motionRuns.rawList, pageRunBefore, a.motionRuns.rawPage)
			}
			if tt.HasText("正在读取原始记录…") || len(a.state().Items) != 1 || a.state().Items[0].Kind != "会话" {
				t.Fatalf("[%s] sub-filter switch flashed skeleton or failed immediate seed", appearance)
			}

			// 4. Empty state renders v4EmptyState cleanly.
			a.switchProjectKind("原始记录")
			emptyPane := a.state()
			emptyPane.Items = nil
			emptyPane.RawCatalog = nil
			emptyPane.Selected = nil
			emptyPane.Loading = false
			emptyPane.Loaded = true
			tt.Frame()
			if !tt.HasText("此项目暂无原始记录") {
				t.Fatalf("[%s] empty raw records missing V4 empty state message: %v", appearance, tt.Texts())
			}

			// 5. ReduceMotion settles immediately without crashing or leaving frames unrendered.
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			tt.Frame()
		})
	}
}

func TestRawRecordsLargeDocumentVirtualizationAndPerfBudget(t *testing.T) {
	var docBuilder strings.Builder
	for sec := 0; sec < 80; sec++ {
		fmt.Fprintf(&docBuilder, "## Doc Section %03d", sec)
		if sec == 0 {
			docBuilder.WriteString(" TOP_MARKER")
		}
		docBuilder.WriteString("\n\n")
		for p := 0; p < 5; p++ {
			fmt.Fprintf(&docBuilder,
				"Section %03d paragraph %d documents `internal/simpledesktop/detail_virtualized.go` chunking, balanced fenced blocks, table preservation, and stationary header rendering across light and dark appearances without destroying the panel container.\n\n",
				sec, p,
			)
		}
		if sec%10 == 0 {
			docBuilder.WriteString("| Module | Responsibility | Status |\n| --- | --- | --- |\n| reader | virtualized list | active |\n| cache | bounded lru | active |\n\n")
		}
		if sec%4 == 0 {
			docBuilder.WriteString("```go\n")
			lineCount := 18
			if sec == 4 {
				lineCount = 180
			}
			for l := 0; l < lineCount; l++ {
				if sec == 4 && l == 179 {
					docBuilder.WriteString("func longBlockLine179() string { return \"ok\" }\n")
				} else {
					fmt.Fprintf(&docBuilder, "func section%03dStep%03d(v int) int { return v + %d }\n", sec, l, l)
				}
			}
			docBuilder.WriteString("```\n\n")
		}
	}
	body := docBuilder.String()
	if len(body) < 520*1024 {
		// Pad deterministically past 520 KB so benchmark matches real large raw records.
		padUnit := strings.Repeat("Large raw document padding line with `code` and **markdown** tokens for realistic layout stress testing.\n\n", 10)
		var big strings.Builder
		big.WriteString(body)
		for sec := 80; big.Len() < 540*1024; sec++ {
			fmt.Fprintf(&big, "## Extra Section %03d\n\n", sec)
			big.WriteString(padUnit)
		}
		body = big.String()
	}
	body += "\nEND_TAIL_MARKER_SECTION_079\n"
	if len(body) < 500*1024 {
		t.Fatalf("expected >500KB test document, got %d bytes", len(body))
	}

	chunks := splitMarkdownChunks(body)
	if len(chunks) <= 50 {
		t.Fatalf("expected >50 markdown chunks, got %d", len(chunks))
	}
	for idx, ch := range chunks {
		fences := 0
		for _, line := range strings.Split(ch, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fences++
			}
		}
		if fences%2 != 0 {
			t.Fatalf("chunk %d has unbalanced code fence (%d fence lines):\n%s", idx, fences, ch)
		}
	}
	joined := strings.Join(chunks, "\n")
	for _, marker := range []string{"TOP_MARKER", "func longBlockLine179()", "END_TAIL_MARKER_SECTION_079"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("chunking dropped expected marker %q", marker)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a := projectUIFixture()
			setTestAppearance(t, a, appearance)
			a.kind = "原始记录"
			s := a.state()
			doc := Document{ID: "large-doc", Dataset: "ds-main", Name: "large_architecture_spec.md", Updated: now, Created: now}
			item := Item{ID: doc.ID, Dataset: "ds-main", Kind: "文档", Title: doc.Name, Date: now, Document: &doc, BodyLoaded: true, Body: body}
			s.Loaded = true
			s.Items = []Item{item}
			s.RawCatalog = s.Items
			s.Selected = &item

			tt, measureFrame := newFrameBudgetTester(t, a.View, 1200, 820)
			tt.Advance(200 * time.Millisecond)
			tt.Frame()
			fixtureImage(t, tt, "raw-large-doc-"+appearance)
			if !tt.HasText("Doc Section 000 TOP_MARKER") {
				t.Fatalf("[%s] missing top marker in virtualized document", appearance)
			}
			if tt.HasText("END_TAIL_MARKER_SECTION_079") {
				t.Fatalf("[%s] tail marker should not be built while scrolled at top", appearance)
			}

			scroll := projectDetailScroll(s, item.key())
			frames := make([]time.Duration, 0, 15)
			for n := 0; n < 15; n++ {
				scroll.Y += 480
				frames = append(frames, measureFrame())
			}
			sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
			if med := frames[len(frames)/2]; med > 12*time.Millisecond {
				t.Errorf("[%s] large document scroll median frame %v exceeds 12ms budget", appearance, med)
			}
			if worst := frames[len(frames)-1]; worst > 25*time.Millisecond {
				t.Errorf("[%s] large document scroll worst frame %v exceeds 25ms budget", appearance, worst)
			}

			scroll.Y = scroll.MaxY
			tt.Frame()
			tt.Frame()
			if !tt.HasText("END_TAIL_MARKER_SECTION_079") {
				t.Fatalf("[%s] scrolling to bottom did not render tail marker (scrollY=%v maxY=%v)", appearance, scroll.Y, scroll.MaxY)
			}
			if tt.HasText("Doc Section 000 TOP_MARKER") {
				t.Fatalf("[%s] top marker should be unmounted when scrolled to bottom", appearance)
			}

			a.InvalidateReading("ds-main")
			if len(a.detailChunks.entries) != 0 || len(s.DetailLists) != 0 {
				t.Fatalf("[%s] InvalidateReading failed to clear chunk cache (%d) or detail lists (%d)",
					appearance, len(a.detailChunks.entries), len(s.DetailLists))
			}
		})
	}
}

func TestDetailChunkCacheHitSessionQATraceAndByteEviction(t *testing.T) {
	// 1. Direct cache hit & byte-budget eviction (oldest-stamp evicted first, oversize uncached).
	cache := &detailChunkCache{maxEntries: 16, maxBytes: 300}
	var splits int
	makeChunk := func(ch string, n int) func() []string {
		return func() []string {
			splits++
			return []string{strings.Repeat(ch, n)}
		}
	}
	c1 := cache.getOrSplit("ds", "id1", "s1", markdownContentKey("a"), makeChunk("a", 120))
	c1Again := cache.getOrSplit("ds", "id1", "s1", markdownContentKey("a"), makeChunk("a", 120))
	if splits != 1 || len(c1) != 1 || c1[0] != c1Again[0] {
		t.Fatalf("expected cache hit with 1 split, got splits=%d", splits)
	}
	// Insert s2 (120B, total 240B), then touch s1 so s2 becomes oldest, then insert s3 (120B -> evicts s2).
	cache.getOrSplit("ds", "id1", "s2", markdownContentKey("b"), makeChunk("b", 120))
	cache.getOrSplit("ds", "id1", "s1", markdownContentKey("a"), makeChunk("a", 120))
	cache.getOrSplit("ds", "id1", "s3", markdownContentKey("c"), makeChunk("c", 120))
	if cache.totalBytes > cache.maxBytes || len(cache.entries) != 2 {
		t.Fatalf("byte eviction violated cap: entries=%d totalBytes=%d maxBytes=%d", len(cache.entries), cache.totalBytes, cache.maxBytes)
	}
	// s1 should still hit (was refreshed); s2 should have been evicted.
	before := splits
	cache.getOrSplit("ds", "id1", "s1", markdownContentKey("a"), makeChunk("a", 120))
	if splits != before {
		t.Fatalf("recently used entry s1 was evicted prematurely (splits %d->%d)", before, splits)
	}
	// Oversize entry (> maxBytes) must be returned without exceeding cap or being stored.
	oversize := cache.getOrSplit("ds", "id1", "big", markdownContentKey("big"), makeChunk("z", 500))
	if len(oversize) != 1 || len(oversize[0]) != 500 || cache.totalBytes > cache.maxBytes {
		t.Fatalf("oversize entry broke cache bounds: totalBytes=%d maxBytes=%d", cache.totalBytes, cache.maxBytes)
	}
	cache.InvalidateObject("ds", "id1")
	if len(cache.entries) != 0 || cache.totalBytes != 0 {
		t.Fatalf("InvalidateObject left entries=%d totalBytes=%d", len(cache.entries), cache.totalBytes)
	}

	// 2. Session with 12 large QAs (24 slots) + 4 expanded traces fits without thrashing; short QAs stay uncached.
	a := projectUIFixture()
	s := a.state()
	largeText := strings.Repeat("Large session markdown paragraph with `code` and **bold** text.\n\n", 140)
	if !shouldVirtualizeMarkdown(largeText) {
		t.Fatal("test largeText must trigger virtualization")
	}
	qas := make([]QA, 0, 13)
	qas = append(qas, QA{ID: "short", Question: "short question", Answer: "short answer"})
	for n := 0; n < 12; n++ {
		qas = append(qas, QA{
			ID:       fmt.Sprintf("qa-%02d", n),
			Question: fmt.Sprintf("## Q%d\n\n%s", n, largeText),
			Answer:   fmt.Sprintf("## A%d\n\n%s", n, largeText),
		})
	}
	traces := make([]Trace, 0, 4)
	sess := Session{ID: "sess-cache", Dataset: "ds-main", User: "u1"}
	item := Item{
		ID:      sess.ID,
		Dataset: "ds-main",
		Kind:    "会话",
		Session: &sess,
		Detail:  &SessionDetail{Session: sess, Count: len(qas), ToolCount: 4, QAs: qas},
	}
	for n := 0; n < 4; n++ {
		tr := Trace{
			ID:     fmt.Sprintf("tr-%02d", n),
			Name:   "Bash",
			Status: "ok",
			Params: json.RawMessage(fmt.Sprintf(`{"cmd":"echo %d"}`, n)),
			Result: json.RawMessage(`{"out":"` + strings.Repeat("x", 4000) + `"}`),
		}
		traces = append(traces, tr)
		s.Tools[item.key()+"/"+tr.ID] = true
	}
	item.Detail.Traces = traces

	rows1 := buildSessionDetailRows(a, s, item, false)
	if len(rows1) == 0 {
		t.Fatal("expected session detail rows")
	}
	if len(a.detailChunks.entries) != 28 {
		t.Fatalf("expected 28 cached slots (24 large QA + 4 expanded traces, excluding short QA), got %d", len(a.detailChunks.entries))
	}
	clockBefore := a.detailChunks.clock
	rows2 := buildSessionDetailRows(a, s, item, false)
	if len(rows2) != len(rows1) || len(a.detailChunks.entries) != 28 {
		t.Fatalf("second build altered row/cache count: rows %d->%d entries=%d", len(rows1), len(rows2), len(a.detailChunks.entries))
	}
	if a.detailChunks.clock != clockBefore+28 {
		t.Fatalf("expected 28 pure cache hits advancing clock by 28, got %d->%d", clockBefore, a.detailChunks.clock)
	}
	// Mutating trace Error must invalidate that trace's content key and update output.
	item.Detail.Traces[0].Error = "trace error changed"
	rows3 := buildSessionDetailRows(a, s, item, false)
	foundUpdated := false
	for _, r := range rows3 {
		if r.kind == detailRowTraceBody && strings.Contains(r.text, "trace error changed") {
			foundUpdated = true
			break
		}
	}
	if !foundUpdated {
		t.Fatal("mutated trace content did not invalidate cached trace chunks")
	}
}
