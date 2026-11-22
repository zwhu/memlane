package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func intPtr(v int) *int       { return &v }

func p1TempToken(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(strings.Repeat("k", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReviewFixFailClosedCapability401NoOfficialFallback verifies GO-P0-01 / CC-P0-05:
// control-plane 401/403 on /v1/capabilities, /v1/overview/bootstrap, or
// /v1/projects/{id}/message-summary must fail closed with zero fallback to
// official /api/v1/* and must never cache supported=false.
func TestReviewFixFailClosedCapability401NoOfficialFallback(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()

	var officialCalls atomic.Int32
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		officialCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer official.Close()

	var capCalls atomic.Int32
	capStatus := atomic.Int32{}
	capStatus.Store(http.StatusUnauthorized)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			capCalls.Add(1)
			w.WriteHeader(int(capStatus.Load()))
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		case "/v1/overview/bootstrap", "/v1/projects/p1/message-summary":
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, official.URL, control.URL, token)
	ctx := context.Background()

	// 1. Direct Client O(1) methods must fail closed on capability 401 with zero official hits.
	doc := Document{ID: "doc-1", Dataset: "ds-1"}
	if _, err := a.Client.read(ctx, Item{ID: "doc-1", Dataset: "ds-1", Kind: "知识", Document: &doc}); !isControlAuthDenied(err) {
		t.Fatalf("Client.read must fail closed on 401, got %v", err)
	}
	if _, err := a.Client.source(ctx, Item{ID: "doc-1", Dataset: "ds-1", Source: "s-1"}); !isControlAuthDenied(err) {
		t.Fatalf("Client.source must fail closed on 401, got %v", err)
	}
	if _, err := a.Client.documentInfoByID(ctx, "ds-1", "doc-1"); !isControlAuthDenied(err) {
		t.Fatalf("Client.documentInfoByID must fail closed on 401, got %v", err)
	}
	if _, err := a.Client.codeActivity(ctx, Project{ID: "p1", Dataset: "ds-1", CodeDataset: "code-1"}); !isControlAuthDenied(err) {
		t.Fatalf("Client.codeActivity must fail closed on 401, got %v", err)
	}
	if capCalls.Load() < 4 {
		t.Fatalf("401 must NOT be cached as unsupported, got capCalls=%d", capCalls.Load())
	}

	// 2. loadOverview, loadKnowledgeFeed, loadProjectQueue, runUnifiedSearch must also fail closed.
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading })
	if a.overviewState.Error == "" {
		t.Fatalf("expected fail-closed overview error on 401")
	}

	a.navigate("知识", 0)
	s := a.state()
	drainReading(t, updates, func() bool { return !s.Loading })
	if s.Error == "" {
		t.Fatalf("expected fail-closed knowledge error on 401")
	}

	q := a.projectQueueStateFor(a.access.Directory.Projects[0])
	a.loadProjectQueue(q, a.access.Directory.Projects[0])
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.DocumentError == "" {
		t.Fatalf("expected fail-closed queue DocumentError on 401")
	}

	a.searchUI.Query = "memory"
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })

	// 3. Also test v1 bootstrap / message-summary 403 when /v1/capabilities returns 404.
	capStatus.Store(http.StatusNotFound)
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading })
	a.loadProjectQueue(q, a.access.Directory.Projects[0])
	drainReading(t, updates, func() bool { return !q.Loading })

	if officialCalls.Load() != 0 {
		t.Fatalf("FAIL-CLOSED VIOLATION: official /api/v1/* received %d requests after control-plane 401/403", officialCalls.Load())
	}
}

// TestReviewFixInvalidateReadingFencesInFlightCallbacks verifies GO-P0-02:
// in-flight overview, day, knowledge, queue, and search callbacks arriving after
// InvalidateReading must NOT repopulate revoked content or state under -race.
func TestReviewFixInvalidateReadingFencesInFlightCallbacks(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()

	release := make(chan struct{})
	var started atomic.Int32
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1", "v2"},
				Features: []string{
					v2FeatureOverview, v2FeatureKnowledgeCursor,
					v2FeatureMessageSummary, v2FeatureSearch,
				},
			})
			return
		}
		started.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		switch r.URL.Path {
		case "/v2/overview/bootstrap":
			_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
				Generation:          1,
				TotalKnowledgeCount: 99,
				RecentItems: []v2KnowledgeItem{
					{DocID: "revoked-1", DatasetID: "ds-1", ProjectID: "p1", Title: "Secret", PreviewText: "Top Secret", Kind: "knowledge"},
				},
			})
		case "/v2/projects/p1/knowledge":
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID: "p1", DatasetID: "ds-1", TotalFiltered: 1, UnreadCount: 7,
				Items: []v2KnowledgeItem{
					{DocID: "revoked-1", DatasetID: "ds-1", ProjectID: "p1", Title: "Secret", PreviewText: "Top Secret", Kind: "knowledge"},
				},
			})
		case "/v2/projects/p1/message-summary":
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID: "p1", DatasetID: "ds-1", UnreadCount: 7,
				UnreadPreview: []v2UnreadPreviewItem{{DocID: "revoked-1", Title: "Secret"}},
				PendingDocuments: []projectMessageSummaryDocument{
					{ID: "pending-1", DatasetID: "ds-1", Title: "Secret Doc"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)

	// Seed pre-revocation sensitive state.
	a.page, a.kind = "知识", "知识"
	s := a.state()
	anchor := Item{ID: "anchor-1", Dataset: "ds-1", Kind: "知识", Body: "secret"}
	s.HomeAnchor = &anchor
	s.SubagentParent = &Item{ID: "parent-1", Dataset: "ds-1", Kind: "会话", Detail: &SessionDetail{Count: 1}}
	a.searchAnchor = &anchor
	a.searchPane = s
	q := a.projectQueueStateFor(a.access.Directory.Projects[0])
	q.UnreadKnowledge = []Item{anchor}
	q.ServerUnreadKnown = true
	q.ServerUnreadCount = 5
	q.Documents = []Item{{ID: "pending-0", Dataset: "ds-1", Kind: "文档"}}

	// Launch concurrent in-flight requests.
	a.loadOverview()
	a.loadKnowledgeFeed(s)
	a.loadProjectQueue(q, a.access.Directory.Projects[0])

	deadline := time.Now().Add(3 * time.Second)
	for started.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Revoke access while all three requests are in flight!
	a.InvalidateReading("")
	close(release)

	// Drain any queued update closures.
	time.Sleep(80 * time.Millisecond)
	for {
		select {
		case fn := <-updates:
			fn()
		default:
			goto checked
		}
	}
checked:
	if len(a.overviewState.Snapshot.Knowledge) != 0 || a.overviewState.V2TotalKnowledge != 0 {
		t.Fatalf("overview repopulated after InvalidateReading: %+v", a.overviewState.Snapshot.Knowledge)
	}
	if len(s.Items) != 0 || s.HomeAnchor != nil || s.SubagentParent != nil || a.searchAnchor != nil {
		t.Fatalf("pane leaked state after InvalidateReading: items=%d home=%v sub=%v search=%v", len(s.Items), s.HomeAnchor, s.SubagentParent, a.searchAnchor)
	}
	if q.ServerUnreadKnown || q.ServerUnreadCount != 0 || len(q.UnreadKnowledge) != 0 || len(q.Documents) != 0 {
		t.Fatalf("queue repopulated after InvalidateReading: known=%v cnt=%d unread=%d docs=%d", q.ServerUnreadKnown, q.ServerUnreadCount, len(q.UnreadKnowledge), len(q.Documents))
	}
}

// TestReviewFixReadingStateBatch200StateRevAndNoLegacyFile verifies GO-P0-03 / GO-P1-02:
// 1. mark-read batches strictly cap at <=200 doc_ids per POST.
// 2. Out-of-order state_rev and unread_count_known=false never regress unread count.
// 3. FlushPendingState drains pending async mark-read writes within bounded timeout.
func TestReviewFixReadingStateBatch200StateRevAndNoLegacyFile(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()

	var mu sync.Mutex
	var batchSizes []int
	var callIdx int

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1", "v2"},
				Features:    []string{v2FeatureReadingState},
			})
		case "/v2/reading-state/mark-read":
			var req v2ReadingStateMarkReadRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			batchSizes = append(batchSizes, len(req.DocIDs))
			callIdx++
			cur := callIdx
			mu.Unlock()
			if len(req.DocIDs) > 200 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "batch_too_large"})
				return
			}
			// Simulate: chunk 1 -> rev=10 count=55 known=true;
			// chunk 2 -> rev=11 count=0 known=false (unsynced catalog must not clobber!);
			// chunk 3 -> stale rev=5 count=99 known=true (must be ignored!).
			resp := v2ReadingStateMarkReadResponse{ScopeHash: "sh-1", Marked: len(req.DocIDs)}
			switch cur {
			case 1:
				resp.UnreadCount = 55
				resp.UnreadCountKnown = boolPtr(true)
				resp.StateRev = 10
			case 2:
				resp.UnreadCount = 0
				resp.UnreadCountKnown = boolPtr(false)
				resp.StateRev = 11
			default:
				resp.UnreadCount = 99
				resp.UnreadCountKnown = boolPtr(true)
				resp.StateRev = 5
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.ServerUnreadKnown = true
	q.ServerUnreadCount = 300
	ctrl, _ := a.accessClient()
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})

	// Hold worker via single bulk enqueue of 255 IDs -> must chunk into 200 + 55.
	w := a.ensureKnowledgeWriter()
	w.mu.Lock()
	ids := make([]string, 255)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%03d", i)
	}
	w.jobs = append(w.jobs, &knowledgeReadMarkJob{
		key: "k1", queue: q, update: a.Update, control: &ctrl,
		readingIdentity: readingID, projectID: p.ID, datasetID: p.Dataset,
		app: a, authEpoch: a.authEpoch, docIDs: ids,
	})
	w.running = true
	go w.drainLoop()
	w.mu.Unlock()

	a.FlushPendingState(2 * time.Second)
	for {
		select {
		case fn := <-updates:
			fn()
		default:
			goto afterFirst
		}
	}
afterFirst:
	mu.Lock()
	sizes := append([]int(nil), batchSizes...)
	mu.Unlock()
	if len(sizes) != 2 || sizes[0] != 200 || sizes[1] != 55 {
		t.Fatalf("expected chunked batch sizes [200 55], got %v", sizes)
	}
	// Chunk 2 returned unread_count_known=false so count must remain 300 (not clobbered to 0) and rev=11!
	if q.ServerUnreadStateRev != 11 || q.ServerUnreadCount != 300 {
		t.Fatalf("unread_count_known=false clobbered count: rev=%d count=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}

	// Send a third mark-read that receives stale state_rev=5 with count=99 -> must NOT overwrite rev=11!
	a.enqueueKnowledgeReadMark("", "doc-extra", q, &ctrl, readingID, p.ID, p.Dataset)
	a.FlushPendingState(2 * time.Second)
	for {
		select {
		case fn := <-updates:
			fn()
		default:
			goto afterSecond
		}
	}
afterSecond:
	if q.ServerUnreadStateRev != 11 || q.ServerUnreadCount != 300 {
		t.Fatalf("stale state_rev=5 regressed unread state: rev=%d count=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}
}

// TestReviewFixOverviewRawCountKPIAndV2DayVirtualization verifies CC-P0-02 and GO-P1-04:
// 1. Overview Raw KPI includes session_count, common.raw_doc_count, and total_raw_count.
// 2. Overview V2 Day view renders via virtualized ui.List in Light & Dark modes with 0 body reads.
func TestReviewFixOverviewRawCountKPIAndV2DayVirtualization(t *testing.T) {
	projects := []Project{
		{ID: "p1", Name: "memlane", Dataset: "ds-1"},
		{ID: "common", Name: "通用记忆", Dataset: "ds-common"},
	}

	// Case A: explicit total_raw_count preferred when all server datasets allowed.
	snap1, _, _, _ := buildOverviewFromV2Bootstrap(projects, v2OverviewBootstrapResponse{
		TotalRawCount: intPtr(42),
		Projects:      []v2OverviewProjectSummary{{ProjectID: "p1", DatasetID: "ds-1", RawDocCount: 5, SessionCount: 38}},
		Common:        &v2OverviewCommonSummary{DatasetID: "ds-common", RawDocCount: 3},
	}, 50)
	if snap1.Raw != 42 {
		t.Fatalf("expected deduped total_raw_count=42, got %d", snap1.Raw)
	}

	// Case B: fallback sum when total_raw_count omitted includes RawDocCount + SessionCount + Common.RawDocCount.
	snap2, _, _, _ := buildOverviewFromV2Bootstrap(projects, v2OverviewBootstrapResponse{
		Projects: []v2OverviewProjectSummary{{ProjectID: "p1", DatasetID: "ds-1", RawDocCount: 5, SessionCount: 30}},
		Common:   &v2OverviewCommonSummary{DatasetID: "ds-common", RawDocCount: 3},
	}, 50)
	if snap2.Raw != 38 {
		t.Fatalf("expected summed raw count 38, got %d", snap2.Raw)
	}

	// Case C: V2 Day virtualization + zero secondary /raw reads in Light & Dark modes.
	var bodyHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyHits.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a := New(Config{ServiceURL: srv.URL, Projects: projects[:1]}, nil)
	a.overviewState.scope = a.overviewScope()
	a.overviewRefreshAt = time.Now().Add(time.Hour)
	a.overviewState.Loaded = true
	a.overviewState.V2Mode = true
	a.overviewState.Day = "2026-11-21"
	a.overviewState.V2DayBuckets = map[string]int{"2026-11-21": 250}
	dayItems := make([]Item, 120)
	for i := range dayItems {
		doc := Document{ID: fmt.Sprintf("day-%03d", i), Dataset: "ds-1", FirstSourceDate: "2026-11-21"}
		dayItems[i] = Item{
			ID: doc.ID, Dataset: "ds-1", Kind: "知识",
			Title: fmt.Sprintf("当日记忆 %03d", i), Preview: "已有摘要",
			ReadingState: "ready", Document: &doc,
		}
	}
	a.overviewState.DayItems = dayItems
	a.overviewState.DayHasMore = true
	for _, pref := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(pref)
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		if !tt.HasText("当日记忆 000") || !tt.HasText("加载更多") {
			t.Fatalf("[%s] virtualized v2 day list did not render expected rows/load-more", pref)
		}
	}
	if bodyHits.Load() != 0 {
		t.Fatalf("expected 0 secondary body HTTP calls for v2 overview rows, got %d", bodyHits.Load())
	}
}

// TestReviewFixAnchorPositioningAndBidirectionalWindow300 verifies CC-P0-04 / GO-P1-03 / CC-P1-06:
// 1. locateOverviewItem passes anchor_doc_id and rejects 404 without prepending stale item.
// 2. Bidirectional <=300 sliding window supports downward append AND upward prepend.
// 3. 400 invalid_cursor resets to page 1 under the SAME filter without switching to legacy mode.
func TestReviewFixAnchorPositioningAndBidirectionalWindow300(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()

	var sawAnchor atomic.Value
	var sawPrevDir atomic.Bool
	var invalidCursorOnce atomic.Bool
	invalidCursorOnce.Store(true)
	var resetFilterCategory atomic.Value

	makeV2Item := func(idx int) v2KnowledgeItem {
		return v2KnowledgeItem{
			DocID: fmt.Sprintf("k-%04d", idx), DatasetID: "ds-1", ProjectID: "p1",
			Title: fmt.Sprintf("记忆 %04d", idx), EffectiveDate: "2026-11-21",
			Kind: "knowledge", Category: "踩坑经验", LessonType: "pitfall",
			PreviewText: fmt.Sprintf("摘要 %04d", idx),
		}
	}

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1", "v2"},
				Features:    []string{v2FeatureKnowledgeCursor, v2FeatureLocalBodyFastpath},
			})
		case "/v2/projects/p1/knowledge":
			q := r.URL.Query()
			anchor := q.Get("anchor_doc_id")
			dir := q.Get("direction")
			cursor := q.Get("cursor")
			if anchor == "missing-doc" {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "document_not_found"})
				return
			}
			if cursor == "expired-cursor" && invalidCursorOnce.Swap(false) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_cursor"})
				return
			}
			if cursor == "" && q.Get("category") != "" {
				resetFilterCategory.Store(q.Get("category"))
			}
			if anchor != "" && dir == "" {
				sawAnchor.Store(anchor)
				items := make([]v2KnowledgeItem, 0, 30)
				for i := 50; i < 80; i++ {
					items = append(items, makeV2Item(i))
				}
				_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
					ProjectID: "p1", DatasetID: "ds-1", TotalFiltered: 450,
					Items: items, PrevCursor: "prev-50", HasPrev: true, NextCursor: "next-80", HasMore: true,
				})
				return
			}
			if dir == "prev" {
				sawPrevDir.Store(true)
				items := make([]v2KnowledgeItem, 0, 30)
				for i := 20; i < 50; i++ {
					items = append(items, makeV2Item(i))
				}
				_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
					ProjectID: "p1", DatasetID: "ds-1", TotalFiltered: 450,
					Items: items, PrevCursor: "prev-20", HasPrev: true, NextCursor: "next-50", HasMore: true,
				})
				return
			}
			items := make([]v2KnowledgeItem, 0, 30)
			for i := 0; i < 30; i++ {
				items = append(items, makeV2Item(i))
			}
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID: "p1", DatasetID: "ds-1", TotalFiltered: 450,
				Items: items, NextCursor: "next-30", HasMore: true,
			})
		case "/v2/datasets/ds-1/documents/k-0065/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "k-0065", DatasetID: "ds-1", Body: strPtr("锚点记忆全文 65"), Source: "local_fastpath",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)
	a.page, a.kind = "知识", "知识"
	s := a.state()

	// 1. Anchor positioning fetches exact neighborhood containing k-0065 with PrevCursor/HasPrev.
	targetDoc := Document{ID: "k-0065", Dataset: "ds-1"}
	a.locateOverviewItem(s, Item{ID: "k-0065", Dataset: "ds-1", Kind: "知识", Document: &targetDoc})
	drainReading(t, updates, func() bool { return !s.Loading && !s.Locating && s.Loaded && !s.Reading })
	if got, _ := sawAnchor.Load().(string); got != "k-0065" {
		t.Fatalf("expected anchor_doc_id=k-0065, got %q", got)
	}
	if !s.Knowledge.HasPrev || s.Knowledge.PrevCursor != "prev-50" || s.Selected == nil || s.Selected.ID != "k-0065" {
		t.Fatalf("unexpected anchored pane state: hasPrev=%v prev=%q sel=%v", s.Knowledge.HasPrev, s.Knowledge.PrevCursor, s.Selected)
	}

	// 2. Anchor 404 surfaces explicit error without fabricating a row.
	missingDoc := Document{ID: "missing-doc", Dataset: "ds-1"}
	a.locateOverviewItem(s, Item{ID: "missing-doc", Dataset: "ds-1", Kind: "知识", Document: &missingDoc})
	drainReading(t, updates, func() bool { return !s.Loading && !s.Locating })
	if s.Error == "" {
		t.Fatalf("expected explicit error when anchor_doc_id returns 404")
	}

	// 3. Bidirectional sliding window: prependKnowledgeBatch from 300 items prepends head and trims tail to <=300.
	s.Error = ""
	s.Items = make([]Item, 300)
	for i := 0; i < 300; i++ {
		s.Items[i] = makeV2Item(50 + i).toItem("")
	}
	s.ListSelection = 10
	s.Knowledge.V2Mode = true
	s.Knowledge.HasPrev = true
	s.Knowledge.PrevCursor = "prev-50"
	a.prependKnowledgeBatch(s)
	drainReading(t, updates, func() bool { return !s.Knowledge.Prepending })
	if !sawPrevDir.Load() || len(s.Items) != maxActiveListItems || s.Items[0].ID != "k-0020" || !s.More || s.ListSelection != 40 {
		t.Fatalf("bidirectional prepend failed: prevDir=%v len=%d head=%s more=%v sel=%d", sawPrevDir.Load(), len(s.Items), s.Items[0].ID, s.More, s.ListSelection)
	}

	// 4. 400 invalid_cursor resets to page 1 under the SAME category filter without leaving V2Mode.
	s.Knowledge.CategoryFilter = "踩坑经验"
	s.Knowledge.NextCursor = "expired-cursor"
	s.More = true
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, func() bool { return !s.Knowledge.Appending && !s.Loading && resetFilterCategory.Load() != nil })
	if !s.Knowledge.V2Mode || resetFilterCategory.Load() != "踩坑经验" {
		t.Fatalf("invalid_cursor must stay in V2Mode and reload same filter, v2=%v cat=%v", s.Knowledge.V2Mode, resetFilterCategory.Load())
	}
}

// TestReviewFixBodyValidationSessionUserIDAndPendingDocs verifies GO-P1-01, GO-P1-05, GO-P1-06.
func TestReviewFixBodyValidationSessionUserIDAndPendingDocs(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()

	var sawChildrenUserID atomic.Value
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1", "v2"},
				Features:    []string{v2FeatureLocalBodyFastpath, v2FeatureRawRecords},
			})
		case "/v2/datasets/ds-1/documents/doc-null/body":
			_, _ = w.Write([]byte(`{"id":"doc-null","dataset_id":"ds-1","body":null}`))
		case "/v2/datasets/ds-1/documents/doc-mismatch/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "other-id", DatasetID: "ds-1", Body: strPtr("wrong doc"),
			})
		case "/v2/datasets/ds-1/documents/doc-trunc/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "doc-trunc", DatasetID: "ds-1", Body: strPtr("truncated content"), Truncated: true, ContentHash: "h-trunc",
			})
		case "/v2/projects/p1/raw-records/sessions/shared-sid/children":
			sawChildrenUserID.Store(r.URL.Query().Get("user_id"))
			_ = json.NewEncoder(w).Encode(v2SessionChildrenResponse{
				ProjectID: "p1", DatasetID: "ds-1", ParentSessionID: "shared-sid", TotalCount: 1,
				Items: []rawSessionEntry{{Session: Session{ID: "child-1", Dataset: "ds-1", User: "user-B", IsSubagent: true}, Label: "子任务"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)
	ctx := context.Background()

	// 1. Body null & mismatched ID must fail and NEVER set BodyLoaded=true.
	dNull := Document{ID: "doc-null", Dataset: "ds-1"}
	resNull, errNull := a.Client.read(ctx, Item{ID: "doc-null", Dataset: "ds-1", Kind: "知识", Document: &dNull})
	if errNull == nil || resNull.BodyLoaded {
		t.Fatalf("body=null must fail without BodyLoaded=true: err=%v loaded=%v", errNull, resNull.BodyLoaded)
	}
	dMis := Document{ID: "doc-mismatch", Dataset: "ds-1"}
	resMis, errMis := a.Client.read(ctx, Item{ID: "doc-mismatch", Dataset: "ds-1", Kind: "知识", Document: &dMis})
	if errMis == nil || resMis.BodyLoaded {
		t.Fatalf("mismatched id must fail without BodyLoaded=true: err=%v loaded=%v", errMis, resMis.BodyLoaded)
	}

	// 2. ReadingTitles cache hits preserve BodyTruncated=true.
	dTrunc := Document{ID: "doc-trunc", Dataset: "ds-1", ContentHash: "h-trunc"}
	itemTrunc := Item{ID: "doc-trunc", Dataset: "ds-1", Kind: "知识", Document: &dTrunc}
	firstRead, err := a.titles.read(ctx, a.Client, itemTrunc, "g1", true, true)
	if err != nil || !firstRead.BodyTruncated {
		t.Fatalf("first read expected BodyTruncated=true, got err=%v trunc=%v", err, firstRead.BodyTruncated)
	}
	cachedRead, err := a.titles.read(ctx, a.Client, itemTrunc, "g1", false, true)
	if err != nil || !cachedRead.BodyTruncated {
		t.Fatalf("titles.read cache hit dropped BodyTruncated: err=%v trunc=%v", err, cachedRead.BodyTruncated)
	}
	cachedFast, ok := a.titles.cachedItem(a.Client, itemTrunc, "g1")
	if !ok || !cachedFast.BodyTruncated {
		t.Fatalf("titles.cachedItem dropped BodyTruncated: ok=%v trunc=%v", ok, cachedFast.BodyTruncated)
	}

	// 3. Session Item.key() distinguishes same session_id across different user_id owners, and loadMoreSubagents sends user_id.
	sA := Item{ID: "shared-sid", Dataset: "ds-1", Kind: "会话", Session: &Session{ID: "shared-sid", Dataset: "ds-1", User: "user-A"}}
	sB := Item{ID: "shared-sid", Dataset: "ds-1", Kind: "会话", Session: &Session{ID: "shared-sid", Dataset: "ds-1", User: "user-B"}}
	if sA.key() == sB.key() {
		t.Fatalf("sessions with distinct user_id collided on key %q", sA.key())
	}
	a.page, a.kind = "知识", "原始记录"
	pane := a.state()
	pane.Items = []Item{sA, sB}
	pane.Selected = &sB
	a.loadMoreSubagents(pane, sB)
	drainReading(t, updates, func() bool { return !pane.Selected.SubagentsLoading && len(pane.Selected.Subagents) == 1 })
	if got, _ := sawChildrenUserID.Load().(string); got != "user-B" {
		t.Fatalf("expected loadMoreSubagents to pass user_id=user-B, got %q", got)
	}

	// 4. V2 pending_documents without nested document object still produces readable Document + renders total PendingDocumentsCount.
	summary := v2MessageSummaryResponse{
		ProjectID:             "p1",
		DatasetID:             "ds-1",
		PendingDocumentsCount: 42,
		PendingDocuments: []projectMessageSummaryDocument{
			{ID: "pend-1", DatasetID: "ds-1", Name: "design.md", Title: "design.md", Date: "2026-11-21", Kind: "文档", CreatedAt: "2026-11-21T08:00:00Z"},
		},
	}
	pendingItems := summary.pendingItems("ds-1")
	if len(pendingItems) != 1 || pendingItems[0].Document == nil || pendingItems[0].Document.ID != "pend-1" || pendingItems[0].Document.Dataset != "ds-1" {
		t.Fatalf("pendingItems failed to synthesize readable Document: %+v", pendingItems)
	}
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded = true
	q.Documents = pendingItems
	q.PendingDocumentsCount = 42
	for _, pref := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(pref)
		a.page, a.kind = "知识", "管理"
		a.access.OpenProject = p.ID
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		if !tt.HasText("42 份文档") {
			t.Fatalf("[%s] expected PendingDocumentsCount=42 in queue progress header", pref)
		}
	}
}

// TestReviewFixChunkedProvenanceBackfillAndUnconfirmedRetention verifies GO-P1-07 / CC-P1-04.
func TestReviewFixChunkedProvenanceBackfillAndUnconfirmedRetention(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()
	provenanceBackfillTracker.mu.Lock()
	clear(provenanceBackfillTracker.completed)
	provenanceBackfillTracker.mu.Unlock()

	var mu sync.Mutex
	var chunkCounts []int
	var maxBodyBytes int
	mode := atomic.Int32{}
	mode.Store(0) // 0: same-machine partial; 1: custom-dir chunked with chunk 1 returning partial; 2: settled migrated

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1", "v2"},
				Features:    []string{v2FeatureProvenanceBackfill},
			})
		case "/v2/provenance/backfill-legacy-dates":
			raw, _ := io.ReadAll(r.Body)
			var req v2ProvenanceBackfillRequest
			_ = json.Unmarshal(raw, &req)
			if len(req.Days) == 0 {
				if mode.Load() == 0 {
					_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{
						Status: "partial", Imported: 10, Unmapped: 2, HasUnconfirmedProjects: true,
					})
					return
				}
				_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{Status: "absent"})
				return
			}
			mu.Lock()
			chunkCounts = append(chunkCounts, len(req.Days))
			chunkIndex := len(chunkCounts)
			if len(raw) > maxBodyBytes {
				maxBodyBytes = len(raw)
			}
			mu.Unlock()
			if mode.Load() == 1 {
				// Return Python's exact "partial" on chunk 1 while subsequent chunks return "migrated":
				// client MUST NOT abort on chunk 1, MUST upload all remaining chunks, and MUST NOT archive.
				status := "migrated"
				unmapped := 0
				unconfirmed := false
				if chunkIndex == 1 {
					status = "partial"
					unmapped = 1
					unconfirmed = true
				}
				_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{
					Status: status, Imported: len(req.Days) - unmapped, Unmapped: unmapped, HasUnconfirmedProjects: unconfirmed,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{
				Status: "migrated", Imported: len(req.Days), Unmapped: 0, HasUnconfirmedProjects: false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	dir := t.TempDir()
	token := p1TempToken(t)
	cfg := Config{
		ServiceURL:      control.URL,
		TokenFile:       token,
		ClientStateFile: filepath.Join(dir, "client-state.json"),
		ProjectControl:  &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token},
	}
	client := Client{Config: cfg}
	identity, err := readingIdentity(client)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath, ok := knowledgeSourceDatesPath(cfg, identity)
	if !ok {
		t.Fatal("missing legacy path")
	}
	days := make(map[string]string, 1250)
	for i := 0; i < 1250; i++ {
		h := fmt.Sprintf("%x", sha256.Sum256([]byte("ds-1/key-"+strconv.Itoa(i))))
		days[h] = "2026-11-10"
	}
	payload, _ := json.Marshal(map[string]any{"schema_version": 1, "days": days})
	if err := os.WriteFile(legacyPath, payload, 0600); err != nil {
		t.Fatal(err)
	}

	// Pass 0: same-machine response returns status="partial" -> no error, not settled, file untouched.
	settled, err := client.backfillLegacyKnowledgeDatesV2(context.Background())
	if err != nil || settled {
		t.Fatalf("same-machine partial pass expected settled=false err=nil, got settled=%v err=%v", settled, err)
	}

	// Pass 1: chunk 1 returns status="partial" -> MUST continue uploading ALL remaining chunks,
	// return settled=false err=nil, and MUST keep legacy file intact without renaming to .migrated.bak.
	mode.Store(1)
	settled, err = client.backfillLegacyKnowledgeDatesV2(context.Background())
	if err != nil || settled {
		t.Fatalf("chunked partial pass expected settled=false err=nil, got settled=%v err=%v", settled, err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy file must NOT be renamed while any chunk returned partial: %v", err)
	}
	mu.Lock()
	totalUploaded := 0
	for _, cnt := range chunkCounts {
		totalUploaded += cnt
		if cnt > 500 {
			t.Fatalf("chunk exceeded 500 entries: %d", cnt)
		}
	}
	if len(chunkCounts) < 3 || totalUploaded != 1250 || maxBodyBytes >= 64*1024 {
		t.Fatalf("expected all 1250 entries across >=3 chunks (<64 KiB each) despite chunk 1 partial, got chunks=%v total=%d maxBytes=%d", chunkCounts, totalUploaded, maxBodyBytes)
	}
	mu.Unlock()

	// Pass 2: projects confirmed & unmapped=0 -> MUST rename to .migrated.bak and mark settled.
	mode.Store(2)
	settled, err = client.backfillLegacyKnowledgeDatesV2(context.Background())
	if err != nil || !settled {
		t.Fatalf("confirmed pass expected settled=true err=nil, got settled=%v err=%v", settled, err)
	}
	if _, err := os.Stat(legacyPath + ".migrated.bak"); err != nil {
		t.Fatalf("expected legacy file renamed to .migrated.bak: %v", err)
	}
}

// TestReviewFixColdStoreTruncationMaxDelayAndWriteErrors verifies GO-P1-08 & GO-P1-09.
func TestReviewFixColdStoreTruncationMaxDelayAndWriteErrors(t *testing.T) {
	// 1. Snapshot exceeding maxBytes trims LRU tail and preserves MRU head.
	snap := persistedColdPendingSnapshot{SchemaVersion: coldPendingWatchSchemaVersion}
	for i := 0; i < 200; i++ {
		snap.Standard = append(snap.Standard, persistedColdPendingRecord{
			Key:             coldPendingKey{AgentID: "a1", HostSessionKey: fmt.Sprintf("sess-%03d", i), Dataset: "ds-1"},
			Signature:       "sig-" + strconv.Itoa(i),
			SessionInactive: true,
			ManifestPath:    "/tmp/manifest-" + strings.Repeat("x", 200),
		})
	}
	bounded, err := marshalBoundedColdPendingSnapshot(snap, 4096)
	if err != nil {
		t.Fatalf("marshalBoundedColdPendingSnapshot failed: %v", err)
	}
	if len(bounded) > 4096 {
		t.Fatalf("bounded snapshot exceeded limit: %d > 4096", len(bounded))
	}
	var decoded persistedColdPendingSnapshot
	if err := json.Unmarshal(bounded, &decoded); err != nil {
		t.Fatalf("invalid bounded json: %v", err)
	}
	if len(decoded.Standard) == 0 || decoded.Standard[0].Key.HostSessionKey != "sess-000" {
		t.Fatalf("expected MRU prefix preserved, got len=%d first=%+v", len(decoded.Standard), decoded.Standard)
	}

	// 2. Continuous mutations cannot starve flush beyond maxDelay.
	dir := t.TempDir()
	storePath := filepath.Join(dir, "cold-pending-watch.json")
	prevDebounce, prevMax := coldPendingStore.debounce, coldPendingStore.maxDelay
	coldPendingStore.debounce = 500 * time.Millisecond
	coldPendingStore.maxDelay = 80 * time.Millisecond
	t.Cleanup(func() {
		waitColdPendingStoreFlush()
		configureColdPendingStore("")
		resetColdPendingCachesForTest()
		coldPendingStore.debounce = prevDebounce
		coldPendingStore.maxDelay = prevMax
	})
	resetColdPendingCachesForTest()
	configureColdPendingStore(storePath)
	startWrites := coldPendingStore.writeCount

	for i := 0; i < 6; i++ {
		scheduleColdPendingFlush()
		time.Sleep(25 * time.Millisecond)
	}
	waitColdPendingStoreFlush()
	if coldPendingStore.writeCount <= startWrites {
		t.Fatalf("continuous mutations starved flush past maxDelay: writes=%d start=%d", coldPendingStore.writeCount, startWrites)
	}

	// 3. Write error records lastWriteErr and does NOT increment writeCount.
	blockedPath := filepath.Join(dir, "as-file")
	if err := os.WriteFile(blockedPath, []byte("not-a-dir"), 0600); err != nil {
		t.Fatal(err)
	}
	badStorePath := filepath.Join(blockedPath, "cold-pending-watch.json")
	configureColdPendingStore(badStorePath)
	beforeErrWrites := coldPendingStore.writeCount
	coldPendingStore.mu.Lock()
	coldPendingStore.dirty = true
	coldPendingStore.mu.Unlock()
	flushColdPendingStoreNow()
	coldPendingStore.mu.Lock()
	gotErr := coldPendingStore.lastWriteErr
	afterErrWrites := coldPendingStore.writeCount
	coldPendingStore.mu.Unlock()
	if gotErr == nil || afterErrWrites != beforeErrWrites {
		t.Fatalf("write error must set lastWriteErr and not increment writeCount: err=%v before=%d after=%d", gotErr, beforeErrWrites, afterErrWrites)
	}
}

// TestReviewFixSearchEmptyScopeIdentityAndMonotonicMerge verifies GO-P1-10.
func TestReviewFixSearchEmptyScopeIdentityAndMonotonicMerge(t *testing.T) {
	controlCapabilitiesCache.mu.Lock()
	clear(controlCapabilitiesCache.entries)
	controlCapabilitiesCache.mu.Unlock()

	var recentIdentity, metaIdentity atomic.Value
	var searchCalls atomic.Int32
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1", "v2"},
				Features:    []string{v2FeatureSearch},
			})
		case "/v2/search/recent":
			searchCalls.Add(1)
			recentIdentity.Store(r.Header.Get(v2HeaderReadingIdentity))
			_ = json.NewEncoder(w).Encode(v2SearchRecentResponse{
				KnowledgeItems: []v2KnowledgeItem{
					{DocID: "r-1", DatasetID: "ds-1", ProjectID: "p1", Title: "最近记忆 1", Kind: "knowledge", PreviewText: "最近摘要", IsUnread: true},
				},
			})
		case "/v2/search/metadata":
			searchCalls.Add(1)
			metaIdentity.Store(r.Header.Get(v2HeaderReadingIdentity))
			_ = json.NewEncoder(w).Encode(v2SearchMetadataResponse{
				Query: r.URL.Query().Get("q"),
				Items: []v2KnowledgeItem{
					{DocID: "m-1", DatasetID: "ds-1", ProjectID: "p1", Title: "命中记忆 1", Kind: "knowledge", PreviewText: "命中内容"},
				},
			})
		case "/api/v1/search":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)

	// 1. Deleted/invalid scope -> len(projects) == 0 -> returns 0 items with ZERO backend calls!
	a.searchUI.Scope = "deleted-project-id"
	a.searchUI.Query = "anything"
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading })
	if len(a.searchUI.Rows) != 0 || searchCalls.Load() != 0 {
		t.Fatalf("invalid project scope must return empty without hitting backend: rows=%d calls=%d", len(a.searchUI.Rows), searchCalls.Load())
	}

	// 2. Valid empty query -> sends X-Memlane-Reading-Identity to /v2/search/recent.
	a.searchUI.Scope = ""
	a.searchUI.Query = ""
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if got, _ := recentIdentity.Load().(string); got == "" {
		t.Fatalf("expected X-Memlane-Reading-Identity on /v2/search/recent")
	}

	// 3. Valid keyword query -> sends X-Memlane-Reading-Identity to /v2/search/metadata.
	a.searchUI.Query = "命中"
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if got, _ := metaIdentity.Load().(string); got == "" {
		t.Fatalf("expected X-Memlane-Reading-Identity on /v2/search/metadata")
	}
}
