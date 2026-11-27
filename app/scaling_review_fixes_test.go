package app

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
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/settings"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func intPtr(v int) *int       { return &v }

func p1TempToken(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("synthetic-management-test-token-"+p), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReviewFixFailClosedCapability401NoOfficialFallback verifies GO-P0-01 / CC-P0-05:
// control-plane 401/403 on /v1/capabilities, /v1/overview/bootstrap, or
// /v1/projects/{id}/message-summary must fail closed with zero fallback to
// official /api/v1/* and must never cache supported=false.
func TestReviewFixFailClosedCapability401NoOfficialFallback(t *testing.T) {

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
	if _, err := a.Client.read(ctx, Item{ID: "doc-1", Dataset: "ds-1", Kind: "知识", Document: &doc}); !settings.IsControlAuthDenied(err) {
		t.Fatalf("Client.read must fail closed on 401, got %v", err)
	}
	if _, err := a.Client.source(ctx, Item{ID: "doc-1", Dataset: "ds-1", Source: "s-1"}); !settings.IsControlAuthDenied(err) {
		t.Fatalf("Client.source must fail closed on 401, got %v", err)
	}
	if _, err := a.Client.documentInfoByID(ctx, "ds-1", "doc-1"); !settings.IsControlAuthDenied(err) {
		t.Fatalf("Client.documentInfoByID must fail closed on 401, got %v", err)
	}
	if _, err := a.Client.codeActivity(ctx, Project{ID: "p1", Dataset: "ds-1", CodeDataset: "code-1"}); !settings.IsControlAuthDenied(err) {
		t.Fatalf("Client.codeActivity must fail closed on 401, got %v", err)
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

	if capCalls.Load() < 3 {
		t.Fatalf("shared contract checks must not cache a 401, got %d probes", capCalls.Load())
	}

	// 3. Also test v1 bootstrap / message-summary 403 when /v1/capabilities returns 404.
	capStatus.Store(http.StatusNotFound)
	a.loadOverview()
	drainReading(t, updates, func() bool { return !a.overviewState.Loading })
	a.loadProjectQueue(q, a.access.Directory.Projects[0])
	drainReading(t, updates, func() bool { return !q.Loading })

	// 4. R2-GO-P0-01: HTTP 500 on /v1/capabilities MUST fail closed (never cache false, zero official fallback).
	if err := os.WriteFile(token, []byte("synthetic-replacement-token-"+token), 0600); err != nil {
		t.Fatal(err)
	}
	previousCalls := capCalls.Load()
	capStatus.Store(http.StatusInternalServerError)
	if control, _ := a.accessClient(); control.requireReadingContract(ctx) == nil || capCalls.Load() != previousCalls+1 {
		t.Fatalf("HTTP 500 on /v1/capabilities must fail closed, got nil")
	}

	// 5. R2-GO-P0-01: Unreadable/missing token file MUST fail closed with zero official fallback.
	badClient := a.Client
	badCtrl := *a.Config.ProjectControl
	badCtrl.ManagementTokenFile = filepath.Join(t.TempDir(), "does-not-exist.token")
	badClient.Config.ProjectControl = &badCtrl
	if _, err := badClient.documentInfoByID(ctx, "ds-1", "doc-1"); err == nil {
		t.Fatalf("missing management token file must fail closed, got nil")
	}

	// 6. R2-GO-P0-01: Rotating token content at the SAME file path MUST bust controlCapabilitiesCache.
	rotatedTokenFile := filepath.Join(t.TempDir(), "rotated.token")
	goodTok := strings.Repeat("a", 40)
	revokedTok := strings.Repeat("b", 40)
	if err := os.WriteFile(rotatedTokenFile, []byte(goodTok), 0600); err != nil {
		t.Fatal(err)
	}
	var rotCapCalls atomic.Int32
	rotSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			rotCapCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+goodTok {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureOverview},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer rotSrv.Close()

	rotClient := ProjectControlClient{Config: ProjectControlConfig{URL: rotSrv.URL, ManagementTokenFile: rotatedTokenFile}}
	has1, err := rotClient.probeFeature(ctx, v2FeatureOverview)
	if err != nil || !has1 || rotCapCalls.Load() != 1 {
		t.Fatalf("initial token probe expected true/nil/1, got has=%v err=%v calls=%d", has1, err, rotCapCalls.Load())
	}
	// Cached hit with same token content.
	has2, err := rotClient.probeFeature(ctx, v2FeatureOverview)
	if err != nil || !has2 || rotCapCalls.Load() != 1 {
		t.Fatalf("cached probe expected true/nil/1, got has=%v err=%v calls=%d", has2, err, rotCapCalls.Load())
	}
	// Overwrite same path with revoked token -> cache key digest changes -> MUST re-probe and fail closed 401!
	if err := os.WriteFile(rotatedTokenFile, []byte(revokedTok), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := rotClient.probeFeature(ctx, v2FeatureOverview); !settings.IsControlAuthDenied(err) {
		t.Fatalf("rotated revoked token at same path must bust cache and return 401, got err=%v calls=%d", err, rotCapCalls.Load())
	}
	if rotCapCalls.Load() != 2 {
		t.Fatalf("expected second /v1/capabilities probe after token rotation, got %d", rotCapCalls.Load())
	}

	if officialCalls.Load() != 0 {
		t.Fatalf("FAIL-CLOSED VIOLATION: official /api/v1/* received %d requests after control-plane errors", officialCalls.Load())
	}
}

// TestReviewFixInvalidateReadingFencesInFlightCallbacks verifies GO-P0-02:
// in-flight overview, day, knowledge, queue, and search callbacks arriving after
// InvalidateReading must NOT repopulate revoked content or state under -race.
func TestReviewFixInvalidateReadingFencesInFlightCallbacks(t *testing.T) {

	release := make(chan struct{})
	inFlight := make(chan string, 3)
	var handlersWG sync.WaitGroup
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{
					v2FeatureOverview, v2FeatureKnowledgeCursor,
					v2FeatureMessageSummary, v2FeatureSearch,
				},
			})
			return
		case "/v1/overview/bootstrap", "/v1/projects/p1/knowledge", "/v1/projects/p1/message-summary":
			handlersWG.Add(1)
			defer handlersWG.Done()
			inFlight <- r.URL.Path
		default:
			http.NotFound(w, r)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		switch r.URL.Path {
		case "/v1/overview/bootstrap":
			_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
				Generation:          1,
				TotalKnowledgeCount: 99,
				RecentItems: []v2KnowledgeItem{
					{DocID: "revoked-1", DatasetID: "ds-1", ProjectID: "p1", Title: "Secret", PreviewText: "Top Secret", Kind: "knowledge"},
				},
			})
		case "/v1/projects/p1/knowledge":
			_ = json.NewEncoder(w).Encode(v2ProjectKnowledgeResponse{
				ProjectID: "p1", DatasetID: "ds-1", TotalFiltered: 1, UnreadCount: 7,
				Items: []v2KnowledgeItem{
					{DocID: "revoked-1", DatasetID: "ds-1", ProjectID: "p1", Title: "Secret", PreviewText: "Top Secret", Kind: "knowledge"},
				},
			})
		case "/v1/projects/p1/message-summary":
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID: "p1", DatasetID: "ds-1", UnreadCount: 7,
				UnreadPreview:         []v2UnreadPreviewItem{{DocID: "revoked-1", Title: "Secret"}},
				PendingDocumentsCount: 1,
			})
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
	q.UnreadPreview = []Item{anchor}
	q.ServerUnreadKnown = true
	q.ServerUnreadCount = 5
	q.Documents = []Item{{ID: "pending-0", Dataset: "ds-1", Kind: "文档"}}

	// Launch concurrent in-flight requests.
	a.loadOverview()
	a.loadKnowledgeFeed(s)
	a.loadProjectQueue(q, a.access.Directory.Projects[0])

	seenInFlight := map[string]bool{}
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for len(seenInFlight) < 3 {
		select {
		case path := <-inFlight:
			seenInFlight[path] = true
		case <-timeout.C:
			close(release)
			t.Fatalf("expected all 3 data requests in flight before InvalidateReading, got %v", seenInFlight)
		}
	}

	// Revoke access while all three requests are genuinely in flight!
	a.InvalidateReading("")
	close(release)
	handlersWG.Wait()

	// Both loadKnowledgeFeed and loadProjectQueue always enqueue an update callback
	// (loadOverview skips a.Update when canceled before decoding). Wait for both
	// guaranteed callbacks, then drain any optional third callback before asserting.
	drainDeadline := time.NewTimer(2 * time.Second)
	defer drainDeadline.Stop()
	for i := 0; i < 2; i++ {
		select {
		case fn := <-updates:
			fn()
		case <-drainDeadline.C:
			t.Fatalf("timed out waiting for in-flight callbacks to enqueue after release (received %d/2)", i)
		}
	}
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
	if q.ServerUnreadKnown || q.ServerUnreadCount != 0 || len(q.UnreadPreview) != 0 || len(q.Documents) != 0 {
		t.Fatalf("queue repopulated after InvalidateReading: known=%v cnt=%d unread=%d docs=%d", q.ServerUnreadKnown, q.ServerUnreadCount, len(q.UnreadPreview), len(q.Documents))
	}
}

// TestReviewFixReadingStateBatch200StateRevAndNoLegacyFile verifies GO-P0-03 / GO-P1-02:
// 1. mark-read batches strictly cap at <=200 doc_ids per POST.
// 2. Out-of-order state_rev and unread_count_known=false never regress unread count.
// 3. FlushPendingState drains pending async mark-read writes within bounded timeout.
func TestReviewFixReadingStateBatch200StateRevAndNoLegacyFile(t *testing.T) {

	var mu sync.Mutex
	var batchSizes []int
	var callIdx int

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureReadingState},
			})
		case "/v1/reading-state/mark-read":
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
	ids := make([]string, 255)
	for n := range ids {
		ids[n] = fmt.Sprintf("doc-%03d", n)
	}
	a.enqueueKnowledgeReadMarks(ids, q, p)

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
	a.enqueueKnowledgeReadMark("doc-extra", q, p)
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
	// ui.NewTester shares MyGo's global scene.Atlas; keep UI raster tests sequential.
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
	a.overviewState.ScopeKey = a.overviewScope()
	a.overview.RefreshAt = time.Now().Add(time.Hour)
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

	var sawAnchor atomic.Value
	var sawPrevDir atomic.Bool
	var sawFrontTrimAnchor atomic.Value
	var invalidCursorOnce atomic.Bool
	invalidCursorOnce.Store(true)
	var resetFilterCategory atomic.Value
	var commonCallsMu sync.Mutex
	var commonCallLog []string

	makeV2Item := func(idx int) v2KnowledgeItem {
		return v2KnowledgeItem{
			DocID: fmt.Sprintf("k-%04d", idx), DatasetID: "ds-1", ProjectID: "p1",
			Title: fmt.Sprintf("记忆 %04d", idx), EffectiveDate: "2026-11-21",
			Kind: "knowledge", Category: "踩坑经验", LessonType: "pitfall",
			PreviewText: fmt.Sprintf("摘要 %04d", idx),
		}
	}
	makeCommonV2Item := func(idx int) v2KnowledgeItem {
		return v2KnowledgeItem{
			DocID: fmt.Sprintf("c-%04d", idx), DatasetID: "ds-common", ProjectID: "common-1",
			Title: fmt.Sprintf("通用记忆 %04d", idx), EffectiveDate: "2026-11-21",
			Kind: "knowledge", CommonCategory: "通用约定", Category: "通用约定",
			PreviewText: fmt.Sprintf("通用摘要 %04d", idx),
		}
	}

	enforceContract1 := func(w http.ResponseWriter, anchor, dir, cursor string) bool {
		if anchor != "" && cursor != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_cursor"})
			return false
		}
		if dir == "prev" && cursor == "" && anchor == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_cursor"})
			return false
		}
		return true
	}

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureKnowledgeCursor, v2FeatureCommonKnowledgeCursor, v2FeatureLocalBodyFastpath},
			})
		case "/v1/projects/p1/knowledge":
			q := r.URL.Query()
			anchor := q.Get("anchor_doc_id")
			dir := q.Get("direction")
			cursor := q.Get("cursor")
			if !enforceContract1(w, anchor, dir, cursor) {
				return
			}
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
				if anchor != "" {
					sawFrontTrimAnchor.Store(anchor)
				}
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
		case "/v1/common/knowledge":
			q := r.URL.Query()
			anchor := q.Get("anchor_doc_id")
			dir := q.Get("direction")
			cursor := q.Get("cursor")
			if !enforceContract1(w, anchor, dir, cursor) {
				return
			}
			commonCallsMu.Lock()
			commonCallLog = append(commonCallLog, fmt.Sprintf("dir=%s|cursor=%s|anchor=%s", dir, cursor, anchor))
			commonCallsMu.Unlock()
			switch {
			case anchor == "c-0065" && dir == "":
				items := make([]v2KnowledgeItem, 0, 30)
				for i := 50; i < 80; i++ {
					items = append(items, makeCommonV2Item(i))
				}
				_ = json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{
					DatasetID: "ds-common", TotalFiltered: 200,
					Items: items, PrevCursor: "c-prev-50", HasPrev: true, NextCursor: "c-next-80", HasMore: true,
				})
			case cursor == "c-next-80":
				items := make([]v2KnowledgeItem, 0, 30)
				for i := 80; i < 110; i++ {
					items = append(items, makeCommonV2Item(i))
				}
				_ = json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{
					DatasetID: "ds-common", TotalFiltered: 200,
					Items: items, PrevCursor: "c-prev-80", HasPrev: true, NextCursor: "c-next-110", HasMore: true,
				})
			case dir == "prev" && cursor == "c-prev-50":
				items := make([]v2KnowledgeItem, 0, 30)
				for i := 20; i < 50; i++ {
					items = append(items, makeCommonV2Item(i))
				}
				_ = json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{
					DatasetID: "ds-common", TotalFiltered: 200,
					Items: items, PrevCursor: "c-prev-20", HasPrev: true, NextCursor: "c-next-50", HasMore: true,
				})
			default:
				items := make([]v2KnowledgeItem, 0, 30)
				for i := 0; i < 30; i++ {
					items = append(items, makeCommonV2Item(i))
				}
				_ = json.NewEncoder(w).Encode(v2CommonKnowledgeResponse{
					DatasetID: "ds-common", TotalFiltered: 200,
					Items: items, NextCursor: "c-next-30", HasMore: true,
				})
			}
		case "/v1/datasets/ds-1/documents/k-0065/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "k-0065", DatasetID: "ds-1", Body: strPtr("锚点记忆全文 65"), Source: "local_fastpath",
			})
		case "/v1/datasets/ds-common/documents/c-0065/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "c-0065", DatasetID: "ds-common", Body: strPtr("通用锚点记忆全文 65"), Source: "local_fastpath",
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

	// 3. Bidirectional sliding window: prependKnowledgeBatch after front-trim (PrevCursor=="")
	// MUST pass direction=prev&anchor_doc_id=<head.ID> (without cursor) per Contract #1.
	s.Error = ""
	s.Items = make([]Item, 300)
	for i := 0; i < 300; i++ {
		s.Items[i] = makeV2Item(50 + i).toItem("")
	}
	s.ListSelection = 10
	s.Knowledge.V2Mode = true
	s.Knowledge.HasPrev = true
	s.Knowledge.PrevCursor = "" // Simulates front-trimmed window!
	a.prependKnowledgeBatch(s)
	drainReading(t, updates, func() bool { return !s.Knowledge.Prepending })
	if !sawPrevDir.Load() || sawFrontTrimAnchor.Load() != "k-0050" || len(s.Items) != maxActiveListItems || s.Items[0].ID != "k-0020" || !s.More || s.ListSelection != 40 {
		t.Fatalf("front-trim bidirectional prepend failed: prevDir=%v anchor=%v len=%d head=%s more=%v sel=%d", sawPrevDir.Load(), sawFrontTrimAnchor.Load(), len(s.Items), s.Items[0].ID, s.More, s.ListSelection)
	}

	// 4. R2-GO-P1-04: V2 item with non-empty Title and EMPTY Preview must NOT block knowledgePage rendering.
	s.Items[0].Preview = ""
	s.Knowledge.Detail = false
	a.setAppearancePreference(appearanceLight)
	ttV2 := ui.NewTester(a.View, 1200, 820)
	ttV2.Frame()
	if !ttV2.HasText("记忆 0020") {
		t.Fatalf("V2 knowledge item with empty Preview failed to render in knowledgePage")
	}

	// 5. 400 invalid_cursor resets to page 1 under the SAME category filter without leaving V2Mode.
	s.Knowledge.CategoryFilter = "踩坑经验"
	s.Knowledge.NextCursor = "expired-cursor"
	s.More = true
	a.appendKnowledgeBatch(s)
	drainReading(t, updates, func() bool { return !s.Knowledge.Appending && !s.Loading && resetFilterCategory.Load() != nil })
	if !s.Knowledge.V2Mode || resetFilterCategory.Load() != "踩坑经验" {
		t.Fatalf("invalid_cursor must stay in V2Mode and reload same filter, v2=%v cat=%v", s.Knowledge.V2Mode, resetFilterCategory.Load())
	}

	// 6. CC2-P0-03: Common Knowledge deep-page anchor supports bidirectional paging:
	// locate(c-0065) -> next (80..109) -> prev (back to anchor 50..79, NOT page 1!) -> prev (20..49 via PrevCursor).
	a.page, a.kind, a.project = "通用知识", "知识", 0
	cs := a.state()
	commonTargetDoc := Document{ID: "c-0065", Dataset: "ds-common"}
	a.locateOverviewItem(cs, Item{ID: "c-0065", Dataset: "ds-common", Kind: "知识", CommonCategory: "通用约定", Document: &commonTargetDoc})
	drainReading(t, updates, func() bool { return !cs.Loading && !cs.Locating && cs.Loaded && !cs.Reading })
	if !cs.CommonV2Mode || !cs.CommonHasPrev || cs.CommonPrevCursor != "c-prev-50" || cs.Selected == nil || cs.Selected.ID != "c-0065" {
		t.Fatalf("unexpected common anchor state: v2=%v hasPrev=%v prev=%q sel=%v", cs.CommonV2Mode, cs.CommonHasPrev, cs.CommonPrevCursor, cs.Selected)
	}
	for _, pref := range []string{appearanceLight, appearanceDark} {
		a.setAppearancePreference(pref)
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		if !tt.HasText("通用摘要 0065") || !tt.HasText("通用锚点记忆全文 65") || !tt.HasText("上一页") || !tt.HasText("下一页") {
			t.Fatalf("[%s] common anchor view missing expected controls/item", pref)
		}
	}
	// Step next -> 80..109.
	a.stepCommonKnowledgeV2(cs, 1)
	drainReading(t, updates, func() bool { return !cs.Loading })
	if len(cs.Items) == 0 || cs.Items[0].ID != "c-0080" {
		t.Fatalf("common next page expected c-0080, got %+v", cs.Items)
	}
	// Step prev -> MUST return to anchor window 50..79 (NOT page 1!).
	a.stepCommonKnowledgeV2(cs, -1)
	drainReading(t, updates, func() bool { return !cs.Loading })
	if len(cs.Items) == 0 || cs.Items[0].ID != "c-0050" || !cs.CommonHasPrev || cs.CommonPrevCursor != "c-prev-50" {
		t.Fatalf("common prev from next expected anchor window c-0050, got head=%v hasPrev=%v prev=%q", cs.Items[0].ID, cs.CommonHasPrev, cs.CommonPrevCursor)
	}
	// Step prev AGAIN from CommonPageIndex==0 with CommonHasPrev==true -> MUST send direction=prev&cursor=c-prev-50 (20..49)!
	a.stepCommonKnowledgeV2(cs, -1)
	drainReading(t, updates, func() bool { return !cs.Loading })
	if len(cs.Items) == 0 || cs.Items[0].ID != "c-0020" {
		t.Fatalf("common prev past anchor expected c-0020, got %+v", cs.Items)
	}
	commonCallsMu.Lock()
	gotLog := append([]string(nil), commonCallLog...)
	commonCallsMu.Unlock()
	wantLog := []string{
		"dir=|cursor=|anchor=c-0065",
		"dir=next|cursor=c-next-80|anchor=",
		"dir=|cursor=|anchor=c-0065",
		"dir=prev|cursor=c-prev-50|anchor=",
	}
	if strings.Join(gotLog, ",") != strings.Join(wantLog, ",") {
		t.Fatalf("unexpected common bidirectional call sequence:\n got: %v\nwant: %v", gotLog, wantLog)
	}
}

// TestReviewFixBodyValidationSessionUserIDAndPendingDocs verifies GO-P1-01, GO-P1-05, GO-P1-06.
func TestReviewFixBodyValidationSessionUserIDAndPendingDocs(t *testing.T) {

	var sawChildrenUserID atomic.Value
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureLocalBodyFastpath, v2FeatureRawRecords},
			})
		case "/v1/datasets/ds-1/documents/doc-null/body":
			_, _ = w.Write([]byte(`{"id":"doc-null","dataset_id":"ds-1","body":null}`))
		case "/v1/datasets/ds-1/documents/doc-mismatch/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "other-id", DatasetID: "ds-1", Body: strPtr("wrong doc"),
			})
		case "/v1/datasets/ds-1/documents/doc-trunc/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: "doc-trunc", DatasetID: "ds-1", Body: strPtr("truncated content"), Truncated: true, ContentHash: "h-trunc",
			})
		case "/v1/projects/p1/raw-records/sessions/shared-sid/children":
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

	// 3. Session Item.Key() distinguishes same session_id across different user_id owners, and loadMoreSubagents sends user_id.
	sA := Item{ID: "shared-sid", Dataset: "ds-1", Kind: "会话", Session: &Session{ID: "shared-sid", Dataset: "ds-1", User: "user-A"}}
	sB := Item{ID: "shared-sid", Dataset: "ds-1", Kind: "会话", Session: &Session{ID: "shared-sid", Dataset: "ds-1", User: "user-B"}}
	if sA.Key() == sB.Key() {
		t.Fatalf("sessions with distinct user_id collided on key %q", sA.Key())
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

	// Summary counts drive tray progress even when document rows are absent.
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded, q.PendingDocumentsCount = true, 42
	progress := a.settingsController.TrayQueueSummary().Progress
	if len(progress) != 1 || progress[0].Count != 42 {
		t.Fatalf("pending document count was not preserved: %+v", progress)
	}
}

// TestReviewFixChunkedProvenanceBackfillAndUnconfirmedRetention verifies GO-P1-07 / CC-P1-04.
func TestReviewFixChunkedProvenanceBackfillAndUnconfirmedRetention(t *testing.T) {
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
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureProvenanceBackfill},
			})
		case "/v1/provenance/backfill-legacy-dates":
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

// TestReviewFixSearchEmptyScopeIdentityAndMonotonicMerge verifies GO-P1-10.
func TestReviewFixSearchEmptyScopeIdentityAndMonotonicMerge(t *testing.T) {

	var recentIdentity, metaIdentity atomic.Value
	var searchCalls atomic.Int32
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureSearch},
			})
		case "/v1/search/recent":
			searchCalls.Add(1)
			recentIdentity.Store(r.Header.Get(v2HeaderReadingIdentity))
			_ = json.NewEncoder(w).Encode(v2SearchRecentResponse{
				KnowledgeItems: []v2KnowledgeItem{
					{DocID: "r-1", DatasetID: "ds-1", ProjectID: "p1", Title: "最近记忆 1", Kind: "knowledge", PreviewText: "最近摘要", IsUnread: true},
				},
			})
		case "/v1/search/metadata":
			searchCalls.Add(1)
			metaIdentity.Store(r.Header.Get(v2HeaderReadingIdentity))
			_ = json.NewEncoder(w).Encode(v2SearchMetadataResponse{
				Query: r.URL.Query().Get("q"),
				Items: []v2KnowledgeItem{
					{DocID: "m-1", DatasetID: "ds-1", ProjectID: "p1", Title: "命中记忆 1", Kind: "knowledge", PreviewText: "命中内容"},
				},
			})
		case "/api/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": []any{}, "semantic_hits": []any{}})
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

	// 2. Valid empty query -> sends X-Memlane-Reading-Identity to /v1/search/recent.
	a.searchUI.Scope = ""
	a.searchUI.Query = ""
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if got, _ := recentIdentity.Load().(string); got == "" {
		t.Fatalf("expected X-Memlane-Reading-Identity on /v1/search/recent")
	}

	// 3. Valid keyword query -> sends X-Memlane-Reading-Identity to /v1/search/metadata.
	a.searchUI.Query = "命中"
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if got, _ := metaIdentity.Load().(string); got == "" {
		t.Fatalf("expected X-Memlane-Reading-Identity on /v1/search/metadata")
	}
}

// TestReviewFixR2InvalidateQueuedCallbacksAndSubagents verifies R2-GO-P0-02 & R2-GO-P2-01:
//  1. Callbacks ALREADY queued in updates channel before InvalidateReading cannot repopulate
//     overview activity, DayItems, knowledge, queue, or openSubagentSession.
//  2. Per-dataset InvalidateReading("ds-1") removes ds-1 DayItems while retaining ds-2.
//  3. project_queue.go 401 triggers InvalidateReading clearing cached UnreadKnowledge/Documents.
//  4. loadMoreSubagents clears Subagents/cursor on 401/403 and clears dead cursor on 400 invalid_cursor.
func TestReviewFixR2InvalidateQueuedCallbacksAndSubagents(t *testing.T) {

	var queueStatus atomic.Int32
	queueStatus.Store(http.StatusOK)
	var childStatus atomic.Int32
	childStatus.Store(http.StatusOK)

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{
					v2FeatureOverview, v2FeatureKnowledgeCursor,
					v2FeatureMessageSummary, v2FeatureRawRecords,
				},
			})
		case "/api/v1/search/history":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "sh-1", "text": "secret query", "createdAt": "2026-11-21T10:00:00Z"},
			})
		case "/v1/projects/p1/message-summary":
			if st := int(queueStatus.Load()); st != http.StatusOK {
				w.WriteHeader(st)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID: "p1", DatasetID: "ds-1", UnreadCount: 3, UnreadStateRev: 2,
				UnreadPreview: []v2UnreadPreviewItem{{DocID: "u-1", Title: "未读 1"}},
			})
		case "/v1/raw-records/session-history":
			_ = json.NewEncoder(w).Encode(SessionHistoryPage{
				Paging: true, Limit: 20,
				SessionDetail: SessionDetail{
					Session: Session{ID: "sub-1", Dataset: "ds-1", User: "u1", IsSubagent: true},
					Label:   "子代理会话",
				},
			})
		case "/v1/projects/p1/raw-records/sessions/parent-1/children":
			switch int(childStatus.Load()) {
			case http.StatusUnauthorized:
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			case http.StatusBadRequest:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_cursor"})
			default:
				_ = json.NewEncoder(w).Encode(v2SessionChildrenResponse{
					ProjectID: "p1", DatasetID: "ds-1", ParentSessionID: "parent-1", TotalCount: 2,
					Items:      []rawSessionEntry{{Session: Session{ID: "sub-1", Dataset: "ds-1", User: "u1", IsSubagent: true}, Label: "子 1"}},
					NextCursor: "dead-cur", HasMore: true,
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)
	a.Config.Projects[0].CodeDataset = "code-1"

	// Seed DayItems from two datasets; InvalidateReading("ds-1") must remove only ds-1.
	a.overviewState.DayItems = []Item{
		{ID: "d1", Dataset: "ds-1", Kind: "知识", Title: "Secret ds-1"},
		{ID: "d2", Dataset: "ds-2", Kind: "知识", Title: "Keep ds-2"},
	}

	// Launch overview activity + openSubagentSession and wait until BOTH have queued
	// their a.Update(fn) callbacks in `updates` BEFORE calling InvalidateReading!
	a.page, a.kind = "知识", "原始记录"
	s := a.state()
	parent := Item{ID: "parent-1", Dataset: "ds-1", Kind: "会话", Session: &Session{ID: "parent-1", Dataset: "ds-1", User: "u1"}}
	sub := Item{ID: "sub-1", Dataset: "ds-1", Kind: "会话", Session: &Session{ID: "sub-1", Dataset: "ds-1", User: "u1", IsSubagent: true}}
	s.Selected = &parent

	a.overviewState.SearchHistory = true
	a.startOverviewActivity()
	a.openSubagentSession(s, parent, sub)

	deadline := time.Now().Add(3 * time.Second)
	for len(updates) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(updates) < 2 {
		t.Fatalf("expected >=2 queued callbacks before InvalidateReading, got %d", len(updates))
	}

	// Now revoke ds-1 while those 200-OK callbacks are ALREADY sitting in `updates`!
	a.InvalidateReading("ds-1")

	// Per-dataset DayItems filtering must drop ds-1 and retain ds-2.
	if len(a.overviewState.DayItems) != 1 || a.overviewState.DayItems[0].Dataset != "ds-2" {
		t.Fatalf("InvalidateReading(ds-1) did not cleanly filter DayItems: %+v", a.overviewState.DayItems)
	}

	// Drain the already-queued callbacks: NONE may repopulate revoked state!
	for {
		select {
		case fn := <-updates:
			fn()
		default:
			goto drainedQueued
		}
	}
drainedQueued:
	if len(a.overviewState.History) != 0 || len(a.overviewState.Events) != 0 {
		t.Fatalf("queued startOverviewActivity callback repopulated activity after InvalidateReading: history=%+v events=%+v", a.overviewState.History, a.overviewState.Events)
	}
	if s.Selected != nil && s.Selected.ID == "sub-1" {
		t.Fatalf("queued openSubagentSession callback repopulated revoked subagent after InvalidateReading")
	}

	// 3. project_queue.go 401 on message-summary must trigger InvalidateReading clearing UnreadKnowledge.
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.UnreadPreview = []Item{{ID: "old-u", Dataset: "ds-1", Kind: "知识"}}
	q.ServerUnreadKnown = true
	q.ServerUnreadCount = 4
	queueStatus.Store(http.StatusUnauthorized)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if len(q.UnreadPreview) != 0 || q.ServerUnreadKnown || q.ServerUnreadCount != 0 {
		t.Fatalf("project_queue 401 failed to invalidate cached unread state: known=%v cnt=%d items=%d", q.ServerUnreadKnown, q.ServerUnreadCount, len(q.UnreadPreview))
	}

	// 4a. loadMoreSubagents 400 invalid_cursor clears dead cursor.
	parentLoaded := parent
	parentLoaded.Subagents = []Item{sub}
	parentLoaded.SubagentsHasMore = true
	parentLoaded.SubagentsNextCursor = "dead-cur"
	s.Items = []Item{parentLoaded}
	s.Selected = &parentLoaded
	childStatus.Store(http.StatusBadRequest)
	a.loadMoreSubagents(s, parentLoaded)
	drainReading(t, updates, func() bool { return s.Selected == nil || !s.Selected.SubagentsLoading })
	if s.Selected == nil || s.Selected.SubagentsNextCursor != "" || s.Error == "" {
		t.Fatalf("loadMoreSubagents 400 invalid_cursor must clear cursor and surface error: sel=%+v err=%q", s.Selected, s.Error)
	}

	// 4b. loadMoreSubagents 401 clears Selected AND scrubs cached Subagents/cursor/count on pane item.
	parentLoaded.Subagents = []Item{sub}
	parentLoaded.SubagentsHasMore = true
	parentLoaded.SubagentsNextCursor = "cur-2"
	parentLoaded.SubagentTotalCount = 5
	s.Items = []Item{parentLoaded}
	s.Selected = &parentLoaded
	childStatus.Store(http.StatusUnauthorized)
	a.loadMoreSubagents(s, parentLoaded)
	drainReading(t, updates, func() bool { return s.Selected == nil || !s.Selected.SubagentsLoading })
	if s.Selected != nil || len(s.Items) != 1 || s.Items[0].ReadingState != "unavailable" || len(s.Items[0].Subagents) != 0 || s.Items[0].SubagentsHasMore || s.Items[0].SubagentsNextCursor != "" || s.Items[0].SubagentTotalCount != 0 {
		t.Fatalf("loadMoreSubagents 401 must clear Selected and scrub item Subagents, got sel=%v item=%+v", s.Selected, s.Items[0])
	}
}

// TestReviewFixR2UnreadStateRevMonotonicFlushAndMarkAllRace verifies R2-GO-P1-02, R2-GO-P1-03, R2-GO-P1-08:
// 1. GET snapshots with stale unread_state_rev cannot overwrite newer ServerUnreadCount.
// 2. FlushPendingState returns error when mark-read or cold-store write fails.
// 3. Concurrent clearProjectNotificationsAsync runs cleanly with knowledge writer serialization.
func TestReviewFixR2UnreadStateRevMonotonicFlushAndMarkAllRace(t *testing.T) {

	var markFail atomic.Bool
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureReadingState, v2FeatureMessageSummary},
			})
		case "/v1/reading-state/mark-read":
			if markFail.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
				return
			}
			_ = json.NewEncoder(w).Encode(v2ReadingStateMarkReadResponse{ScopeHash: "sh", Marked: 1, UnreadCount: 2, StateRev: 10})
		case "/v1/reading-state/mark-all-read":
			_ = json.NewEncoder(w).Encode(v2ReadingStateMarkAllReadResponse{ScopeHash: "sh", UnreadCount: 0, StateRev: 11})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)

	// 1. Monotonic unread_state_rev snapshot protection.
	if !applyServerUnreadSnapshot(q, 5, 10, false) || q.ServerUnreadCount != 5 || q.ServerUnreadStateRev != 10 {
		t.Fatalf("expected rev=10 count=5 applied, got rev=%d cnt=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}
	if applyServerUnreadSnapshot(q, 99, 8, false) || q.ServerUnreadCount != 5 {
		t.Fatalf("stale rev=8 overwrote count: rev=%d cnt=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}
	if applyServerUnreadSnapshot(q, 88, 0, false) || q.ServerUnreadCount != 5 {
		t.Fatalf("rev=0 snapshot overwrote tracked rev=10: rev=%d cnt=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}
	if applyServerUnreadSnapshot(q, 4, 10, true) || q.ServerUnreadCount != 5 {
		t.Fatalf("same-rev snapshot while writer pending overwrote count: rev=%d cnt=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}
	if !applyServerUnreadSnapshot(q, 3, 12, false) || q.ServerUnreadCount != 3 || q.ServerUnreadStateRev != 12 {
		t.Fatalf("newer rev=12 failed to apply: rev=%d cnt=%d", q.ServerUnreadStateRev, q.ServerUnreadCount)
	}

	// 2. FlushPendingState returns error on unsaved mark-read failure, and retries cleanly on next flush.
	markFail.Store(true)
	a.enqueueKnowledgeReadMark("doc-fail", q, p)
	if err := a.FlushPendingState(2 * time.Second); err == nil {
		t.Fatalf("FlushPendingState must return error when mark-read failed")
	}
	markFail.Store(false)
	if err := a.FlushPendingState(2 * time.Second); err != nil {
		t.Fatalf("FlushPendingState retry after recovery must succeed, got %v", err)
	}

	// 3. Double-click concurrent clearProjectNotificationsAsync under -race.
	// Flush waits for writes, but their UI updates can still be buffered.
	for len(updates) > 0 {
		(<-updates)()
	}
	q.Loaded = true
	version := q.StateVersion
	a.clearProjectNotificationsAsync(q, p)
	a.clearProjectNotificationsAsync(q, p)
	drainReading(t, updates, func() bool { return q.StateVersion >= version+2 })
	if q.UnreadState.Error != "" {
		t.Fatalf("concurrent clearProjectNotificationsAsync failed: read=%q", q.UnreadState.Error)
	}
}

// TestReviewFixR2ProvenanceReadErrorMidModifyAndCommonSearch verifies R2-GO-P1-07, R2-GO-P1-10, R2-GO-P1-01.
func TestReviewFixR2ProvenanceReadErrorMidModifyAndCommonSearch(t *testing.T) {
	provenanceBackfillTracker.mu.Lock()
	clear(provenanceBackfillTracker.completed)
	provenanceBackfillTracker.mu.Unlock()

	var recentDatasetID, metaDatasetID atomic.Value
	var modifyFileDuringBackfill atomic.Bool
	var legacyPath string
	var absentUnconfirmed atomic.Bool

	canonicalUUID := "01234567-89ab-cdef-0123-456789abcdef"
	hexUUID := "0123456789abcdef0123456789abcdef"

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{
					v2FeatureProvenanceBackfill, v2FeatureSearch, v2FeatureLocalBodyFastpath,
				},
			})
		case "/v1/provenance/backfill-legacy-dates":
			var req v2ProvenanceBackfillRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if len(req.Days) == 0 {
				_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{
					Status:                 "absent",
					HasUnconfirmedProjects: absentUnconfirmed.Load(),
				})
				return
			}
			if modifyFileDuringBackfill.Swap(false) && legacyPath != "" {
				// Mutate local file mid-upload -> client must detect stat change and skip rename!
				_ = os.WriteFile(legacyPath, []byte(`{"schema_version":1,"days":{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":"2026-11-22","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb":"2026-11-22"}}`), 0600)
			}
			_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{Status: "migrated", Imported: len(req.Days)})
		case "/v1/search/recent":
			recentDatasetID.Store(r.URL.Query().Get("dataset_id"))
			_ = json.NewEncoder(w).Encode(v2SearchRecentResponse{
				KnowledgeItems: []v2KnowledgeItem{{DocID: "c-1", DatasetID: "ds-common", Title: "通用最近", Kind: "knowledge"}},
			})
		case "/v1/search/metadata":
			metaDatasetID.Store(r.URL.Query().Get("dataset_id"))
			_ = json.NewEncoder(w).Encode(v2SearchMetadataResponse{
				Query: r.URL.Query().Get("q"),
				Items: []v2KnowledgeItem{{DocID: "c-2", DatasetID: "ds-common", Title: "通用命中", Kind: "knowledge"}},
			})
		case "/api/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": []any{}, "semantic_hits": []any{}})
		case "/v1/datasets/" + canonicalUUID + "/documents/" + canonicalUUID + "/body":
			_ = json.NewEncoder(w).Encode(v2DocumentBodyResponse{
				ID: hexUUID, DatasetID: hexUUID, Body: strPtr("canonical uuid body"), Source: "local_fastpath",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	token := p1TempToken(t)
	a, updates := p1TestApp(t, control.URL, control.URL, token)

	// 1. R2-GO-P1-07: corrupt local dates file must return error and NOT settle.
	identity, err := readingIdentity(a.Client)
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	legacyPath, ok = knowledgeSourceDatesPath(a.Config, identity)
	if !ok {
		t.Fatal("expected legacy knowledge source dates path")
	}
	if err := os.WriteFile(legacyPath, []byte("{bad-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if settled, bErr := a.Client.backfillLegacyKnowledgeDatesV2(context.Background()); bErr == nil || settled {
		t.Fatalf("corrupt local dates file must error and not settle: settled=%v err=%v", settled, bErr)
	}

	// 2. R2-GO-P1-07: absent + has_unconfirmed_projects=true must NOT settle even without local file.
	_ = os.Remove(legacyPath)
	absentUnconfirmed.Store(true)
	if settled, bErr := a.Client.backfillLegacyKnowledgeDatesV2(context.Background()); bErr != nil || settled {
		t.Fatalf("absent with has_unconfirmed_projects=true must not settle: settled=%v err=%v", settled, bErr)
	}
	absentUnconfirmed.Store(false)

	// 3. R2-GO-P1-07: file modified mid-upload must NOT rename to .migrated.bak and must NOT settle!
	if err := os.WriteFile(legacyPath, []byte(`{"schema_version":1,"days":{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":"2026-11-21"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	modifyFileDuringBackfill.Store(true)
	if settled, bErr := a.Client.backfillLegacyKnowledgeDatesV2(context.Background()); bErr != nil || settled {
		t.Fatalf("mid-upload file modification must defer rename and return settled=false, got settled=%v err=%v", settled, bErr)
	}
	if _, statErr := os.Stat(legacyPath); statErr != nil {
		t.Fatalf("modified local dates file was unexpectedly renamed/removed: %v", statErr)
	}
	if _, statErr := os.Stat(legacyPath + ".migrated.bak"); !os.IsNotExist(statErr) {
		t.Fatalf("expected no .migrated.bak when file changed mid-upload")
	}

	// 4. R2-GO-P1-10: @common search scope sends dataset_id=ds-common to BOTH recent and metadata endpoints.
	a.searchUI.Scope = "@common"
	a.searchUI.Query = ""
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if got, _ := recentDatasetID.Load().(string); got != "ds-common" {
		t.Fatalf("@common recent search expected dataset_id=ds-common, got %q", got)
	}
	a.searchUI.Query = "通用"
	a.runUnifiedSearch(false)
	drainReading(t, updates, func() bool { return !a.searchUI.Loading && a.searchUI.Loaded })
	if got, _ := metaDatasetID.Load().(string); got != "ds-common" {
		t.Fatalf("@common metadata search expected dataset_id=ds-common, got %q", got)
	}

	// 5. R2-GO-P1-01: canonical UUID matches hyphenless hex UUID in /v1/.../body response.
	docUUID := Document{ID: canonicalUUID, Dataset: canonicalUUID}
	readItem, readErr := a.Client.read(context.Background(), Item{ID: canonicalUUID, Dataset: canonicalUUID, Kind: "知识", Document: &docUUID})
	if readErr != nil || !readItem.BodyLoaded || readItem.Body != "canonical uuid body" {
		t.Fatalf("canonical UUID matching failed: err=%v loaded=%v body=%q", readErr, readItem.BodyLoaded, readItem.Body)
	}
}

// TestReviewFixR3ConvergenceClosure verifies all 4 items of #64 §二 (R3 Codex findings):
//  1. P0-3: project_queue 403 on processing-notices returns immediately without writing concurrent
//     official Documents/UnreadKnowledge; InvalidateReading withdraws commonEdit and clears Events/History.
//  2. P1-1: partial InvalidateReading(ds-1) never leaves unrelated project ds-2 q.Loading stuck true.
//  3. P1-2: pending projects do not block overview for verified projects.
//  4. P1-5/P1-6: stale mark-all-read state_rev never wipes newer unread preview; equal-length same-mtime
//     token file replacement immediately busts controlCapabilitiesCache.
func TestReviewFixR3ConvergenceClosure(t *testing.T) {

	// --- 1. P0-3: Legacy summary 404 + processing-notices 403 + concurrent official 200 OK ---
	var denyActivity atomic.Bool
	officialP0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/processing-status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"id": "pend-doc-1", "completed": false},
			}})
		case strings.Contains(r.URL.Path, "/data"):
			_ = json.NewEncoder(w).Encode([]Document{
				{ID: "pend-doc-1", Dataset: "ds-1", Name: "secret-pending.md", Created: "2026-11-21T10:00:00Z"},
				{ID: "know-doc-1", Dataset: "ds-1", Name: "k.md", Created: "2026-11-21T10:00:00Z", Metadata: map[string]any{"memlane_kind": "knowledge"}},
			})
		case strings.Contains(r.URL.Path, "/pipeline-runs"):
			if denyActivity.Load() {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer officialP0.Close()

	controlP0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities", "/v1/projects/p1/message-summary":
			http.NotFound(w, r)
		case "/v1/projects/p1/processing-notices":
			if denyActivity.Load() {
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controlP0.Close()

	tokenP0 := p1TempToken(t)
	a0, updates0 := p1TestApp(t, officialP0.URL, controlP0.URL, tokenP0)
	p1 := a0.access.Directory.Projects[0]
	q1 := a0.projectQueueStateFor(p1)
	a0.loadProjectQueue(q1, p1)
	drainReading(t, updates0, func() bool { return !q1.Loading })
	if len(q1.Documents) != 0 || len(q1.UnreadPreview) != 0 || q1.DocumentError == "" {
		t.Fatalf("P0-3 VIOLATION: 403 processing-notices wrote concurrent official docs/unread: docs=%d unread=%d err=%q", len(q1.Documents), len(q1.UnreadPreview), q1.DocumentError)
	}

	// A successful parallel document response must also be discarded on activity 403.
	denyActivity.Store(true)
	q1.DocumentError = ""
	q1.Activity = []overviewEvent{{Title: "cached activity"}}
	a0.loadProjectQueue(q1, p1)
	drainReading(t, updates0, func() bool { return !q1.Loading })
	if len(q1.Activity) != 0 || len(q1.Documents) != 0 || len(q1.UnreadPreview) != 0 || q1.DocumentError == "" {
		t.Fatalf("activity denial leaked queue data: activity=%d docs=%d unread=%d err=%q",
			len(q1.Activity), len(q1.Documents), len(q1.UnreadPreview), q1.DocumentError)
	}

	// --- 1b. P0-3: InvalidateReading withdraws commonEdit and clears Events/History ---
	existingCommon := Document{ID: "c-edit-1", Dataset: "ds-common", Name: "c.md"}
	a0.commonEdit = commonEditorState{
		Open:     true,
		Target:   Project{ID: "common-1", Dataset: "ds-common"},
		Existing: &existingCommon,
		Draft:    CommonDraft{Title: "机密通用草稿", Body: "敏感正文"},
	}
	a0.overviewState.Events = []overviewEvent{
		{Key: "ev-ds1", Title: "ds-1 活动", Record: activityRow{Dataset: "ds-1"}},
		{Key: "ev-ds2", Title: "ds-2 活动", Record: activityRow{Dataset: "ds-2"}},
		{Key: "ev-rec1", Title: "ds-1 召回", Recall: &RecallEvent{ID: "r1", Hits: []RecallHit{{Dataset: "ds-1", Document: "k1"}}}},
	}
	a0.overviewState.History = []overviewEvent{{Key: "h-1", Title: "搜索历史 1"}}
	a0.overviewState.HistoryLoaded = true
	a0.overviewState.ActivityLoaded = true
	a0.overviewState.GroupedEvents = a0.overviewState.Events

	// Partial invalidate ds-1: drops ds-1 event + ds-1 recall, keeps ds-2 event and ds-common editor.
	a0.InvalidateReading("ds-1")
	if len(a0.overviewState.Events) != 1 || a0.overviewState.Events[0].Key != "ev-ds2" || a0.overviewState.GroupedEvents != nil {
		t.Fatalf("InvalidateReading(ds-1) did not cleanly filter Events: %+v", a0.overviewState.Events)
	}
	if !a0.commonEdit.Open || a0.commonEdit.Existing == nil {
		t.Fatalf("InvalidateReading(ds-1) unexpectedly closed unrelated ds-common editor")
	}
	// Partial invalidate ds-common: withdraws ds-common editor immediately.
	a0.InvalidateReading("ds-common")
	if a0.commonEdit.Open || a0.commonEdit.Existing != nil || a0.commonEdit.Draft.Body != "" {
		t.Fatalf("InvalidateReading(ds-common) failed to withdraw commonEdit: %+v", a0.commonEdit)
	}
	// Global invalidate "": clears all remaining Events and History.
	a0.InvalidateReading("")
	if len(a0.overviewState.Events) != 0 || len(a0.overviewState.History) != 0 || a0.overviewState.HistoryLoaded || a0.overviewState.ActivityLoaded {
		t.Fatalf("InvalidateReading(\"\") leaked Events/History: events=%d history=%d", len(a0.overviewState.Events), len(a0.overviewState.History))
	}

	// --- 2. P1-1: Partial InvalidateReading(ds-1) must NOT stall unrelated project ds-2 q.Loading ---
	p2Started := make(chan struct{}, 1)
	p2Release := make(chan struct{})
	var p2Calls atomic.Int32
	controlP1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureMessageSummary},
			})
		case "/v1/projects/p2/message-summary":
			if p2Calls.Add(1) == 1 {
				select {
				case p2Started <- struct{}{}:
				default:
				}
				<-p2Release
			}
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID: "p2", DatasetID: "ds-2", UnreadCount: 2, UnreadStateRev: 1,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controlP1.Close()

	tokenP1 := p1TempToken(t)
	a1, updates1 := p1TestApp(t, controlP1.URL, controlP1.URL, tokenP1)
	p2 := AccessProject{ID: "p2", Name: "other", Dataset: "ds-2", Status: "verified"}
	a1.access.Directory.Projects = append(a1.access.Directory.Projects, p2)
	q2 := a1.projectQueueStateFor(p2)
	a1.loadProjectQueue(q2, p2)
	<-p2Started
	// Revoke unrelated ds-1 while p2 is mid-flight!
	a1.InvalidateReading("ds-1")
	close(p2Release)
	drainReading(t, updates1, func() bool { return !q2.Loading })
	if q2.Loading {
		t.Fatalf("P1-1 VIOLATION: unrelated project p2 q.Loading stuck true after InvalidateReading(ds-1)")
	}
	// Verify p2 can immediately be polled again and loads cleanly.
	a1.loadProjectQueue(q2, p2)
	drainReading(t, updates1, func() bool { return !q2.Loading && q2.ServerUnreadKnown })
	if q2.ServerUnreadCount != 2 {
		t.Fatalf("p2 follow-up poll failed after recovery: known=%v count=%d", q2.ServerUnreadKnown, q2.ServerUnreadCount)
	}

	// --- 3. P1-2: transitional project B does not block overview for verified A ---
	var overviewHits atomic.Int32
	controlP2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureOverview, v2FeatureReadingState},
			})
		case "/v1/overview/bootstrap":
			overviewHits.Add(1)
			_ = json.NewEncoder(w).Encode(v2OverviewBootstrapResponse{
				Generation: 1, TotalKnowledgeCount: 10,
				Projects: []v2OverviewProjectSummary{{ProjectID: "p1", DatasetID: "ds-1", KnowledgeCount: 10, UnreadCount: 1, UnreadStateRev: 1}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controlP2.Close()

	tokenP2 := p1TempToken(t)
	a2, updates2 := p1TestApp(t, controlP2.URL, controlP2.URL, tokenP2)
	a2.access.Directory.Projects = append(a2.access.Directory.Projects, AccessProject{
		ID: "p-unconfirmed", Name: "unconfirmed", Dataset: "ds-pending", Status: "pending",
	})
	a2.loadOverview()
	drainReading(t, updates2, func() bool { return !a2.overviewState.Loading })
	if a2.overviewState.Error != "" || overviewHits.Load() != 1 {
		t.Fatalf("P1-2 VIOLATION: unconfirmed project blocked verified overview load: err=%q hits=%d", a2.overviewState.Error, overviewHits.Load())
	}

	// An empty directory leaves only the fallback config, which has no verified status.
	aConfig, configUpdates := p1TestApp(t, controlP2.URL, controlP2.URL, tokenP2)
	aConfig.access.Directory = AccessDirectory{}
	aConfig.Config.Projects = append(aConfig.Config.Projects, Project{ID: "p-unconfirmed", Dataset: "ds-pending"})
	aConfig.loadOverview()
	drainReading(t, configUpdates, func() bool { return !aConfig.overviewState.Loading })

	if overviewHits.Load() != 2 {
		t.Fatal("config fallback did not load overview")
	}
	// --- 4a. P1-5: stale mark-all-read (state_rev=5) must NOT clear newer unread preview (state_rev=10) ---
	controlP5 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureReadingState},
			})
		case "/v1/reading-state/mark-all-read":
			_ = json.NewEncoder(w).Encode(v2ReadingStateMarkAllReadResponse{
				ScopeHash: "sh-5", UnreadCount: 0, UnreadCountKnown: boolPtr(true), StateRev: 5,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controlP5.Close()

	tokenP5 := p1TempToken(t)
	a5, updates5 := p1TestApp(t, controlP5.URL, controlP5.URL, tokenP5)
	p5 := a5.access.Directory.Projects[0]
	q5 := a5.projectQueueStateFor(p5)
	q5.Loaded = true
	q5.ServerUnreadKnown = true
	q5.ServerUnreadStateRev = 10
	q5.ServerUnreadCount = 3
	q5.UnreadHasMore = true
	q5.UnreadPreview = []Item{{ID: "newer-unread", Dataset: "ds-1", Kind: "知识", Title: "新到达未读"}}
	a5.clearProjectNotificationsAsync(q5, p5)
	drainReading(t, updates5, func() bool { return q5.StateVersion > 0 })
	if q5.ServerUnreadStateRev != 10 || q5.ServerUnreadCount != 3 || len(q5.UnreadPreview) != 1 || !q5.UnreadHasMore {
		t.Fatalf("P1-5 VIOLATION: stale mark-all-read rev=5 clobbered newer unread rev=10: rev=%d cnt=%d unread=%d more=%v",
			q5.ServerUnreadStateRev, q5.ServerUnreadCount, len(q5.UnreadPreview), q5.UnreadHasMore)
	}

	// --- 4b. P1-6: equal-length, identical-mtime token replacement immediately busts capabilities cache ---
	sameMtimeTokFile := filepath.Join(t.TempDir(), "same-mtime.token")
	tokA := strings.Repeat("x", 40)
	tokB := strings.Repeat("y", 40)
	fixedTime := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(sameMtimeTokFile, []byte(tokA), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sameMtimeTokFile, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	var capReqs atomic.Int32
	srvP6 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			capReqs.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+tokA {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureOverview},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srvP6.Close()

	ctrlP6 := ProjectControlClient{Config: ProjectControlConfig{URL: srvP6.URL, ManagementTokenFile: sameMtimeTokFile}}
	has, err := ctrlP6.probeFeature(context.Background(), v2FeatureOverview)
	if err != nil || !has || capReqs.Load() != 1 {
		t.Fatalf("initial probe failed: has=%v err=%v calls=%d", has, err, capReqs.Load())
	}
	// Replace file with equal-length tokB AND pin exact same mtime!
	if err := os.WriteFile(sameMtimeTokFile, []byte(tokB), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sameMtimeTokFile, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrlP6.probeFeature(context.Background(), v2FeatureOverview); !settings.IsControlAuthDenied(err) || capReqs.Load() != 2 {
		t.Fatalf("P1-6 VIOLATION: equal-length same-mtime token swap reused stale capabilities cache: err=%v calls=%d", err, capReqs.Load())
	}
}

type queueTestTransport http.HandlerFunc

func (handler queueTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w.Result(), nil
}

func TestReviewFixQueueReadingAuthorizationIsolation(t *testing.T) {
	var denySummary atomic.Bool
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureReadingState, v2FeatureMessageSummary},
			})
		case "/v1/projects/p1/message-summary":
			if denySummary.Load() {
				http.Error(w, `{"error":"project_unconfirmed"}`, http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(v2MessageSummaryResponse{
				ProjectID: "p1", DatasetID: "ds-1", UnreadCount: 3, UnreadStateRev: 1,
			})
		default:
			http.NotFound(w, r)
		}
	})

	const endpoint = "http://127.0.0.1:16129"
	a, updates := p1TestApp(t, endpoint, endpoint, p1TempToken(t))
	a.Client.HTTP = &http.Client{Transport: transport}
	a.access.Directory.Projects = append(a.access.Directory.Projects, AccessProject{
		ID: "stale", Dataset: "ds-stale", Status: "verified",
	})
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	a.loadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.DocumentError != "" || !q.ServerUnreadKnown || q.ServerUnreadCount != 3 {
		t.Fatalf("unrelated project blocked queue: err=%q known=%t count=%d",
			q.DocumentError, q.ServerUnreadKnown, q.ServerUnreadCount)
	}

	denySummary.Store(true)
	q.Activity = []overviewEvent{{Title: "cached activity"}}
	q.Documents = []Item{{ID: "cached document"}}
	q.UnreadPreview = []Item{{ID: "cached unread"}}
	a.loadProjectQueue(q, p)
	a.InvalidateReading("ds-stale")
	drainReading(t, updates, func() bool { return !q.Loading })
	if len(q.Activity) != 0 || len(q.Documents) != 0 || len(q.UnreadPreview) != 0 ||
		q.ServerUnreadKnown || q.ServerUnreadCount != 0 || q.DocumentError == "" {
		t.Fatalf("403 after unrelated invalidation left stale queue state: %+v", q)
	}
}

func TestReviewFixR4MarkAllReceiptPreservesSameRevisionUnread(t *testing.T) {
	t.Parallel()
	a := &App{}
	q := &ProjectQueueState{UnreadState: UnreadState{ServerUnreadStateRev: 5}}
	before := q.ServerUnreadSnapshot
	if !applyServerUnreadSnapshot(q, 1, 5, false) {
		t.Fatal("expected same-revision unread snapshot to be accepted")
	}
	q.UnreadPreview = []Item{{ID: "late"}}

	a.applyMarkAllReadReceipt(q, "ds-1", api.MarkReadResponse{ReadingStateView: api.ReadingStateView{StateRev: 5}}, before, q.KnowledgeCatalogVersion)
	if q.ServerUnreadCount != 1 || len(q.UnreadPreview) != 1 || q.UnreadPreview[0].ID != "late" {
		t.Fatal("delayed mark-all cleared a new unread at the same reading revision")
	}
}
