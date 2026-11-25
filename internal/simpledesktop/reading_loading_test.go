package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPolicyRevisionWithdrawsCommonBodyWithoutClaimingDenial(t *testing.T) {
	a := New(Config{ServiceURL: "http://127.0.0.1:60000"}, nil)
	a.syncReadingIdentity()
	a.commonEdit = commonEditorState{Open: true, Submitted: true, SaveVerified: true,
		Existing: &Document{ID: "saved", Dataset: "common"}, Target: Project{Dataset: "common"},
		Draft:   CommonDraft{Title: "Saved title", Body: "Previous body"},
		Receipt: CommonWriteReceipt{DataID: "saved", PipelineRunID: "original-operation"}}
	a.SetReadingPolicyRevision("unrelated-project-revision")
	if a.commonEdit.Open || a.commonEdit.Existing != nil || a.commonEdit.Draft.Body != "" {
		t.Fatal("configuration change retained old editable content")
	}
	if !a.commonEdit.Submitted || !a.commonEdit.SaveVerified || a.commonEdit.Receipt.PipelineRunID != "original-operation" {
		t.Fatal("configuration change lost the original save receipt")
	}
	if !strings.Contains(a.commonEdit.Message, "配置已变化") || strings.Contains(a.commonEdit.Message, "无权") {
		t.Fatal("an identity revision was reported as a confirmed denial")
	}
}

func drainReading(t *testing.T, updates chan func(), ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case fn := <-updates:
			fn()
		case <-deadline.C:
			t.Fatal("reading update deadline")
		}
	}
}
func TestReadingPaneAutomaticBoundedAndContinuous(t *testing.T) {
	var rawCalls atomic.Int32
	var deny atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/raw") {
			rawCalls.Add(1)
			if deny.Load() {
				w.WriteHeader(403)
				return
			}
			fmt.Fprint(w, "# Session learning (session s)\n\nReadable knowledge. More content.")
			return
		}
		if req.URL.Query().Get("limit") != "31" {
			t.Errorf("catalog unbounded: %s", req.URL.RawQuery)
		}
		rows := []Document{}
		for n := 0; n < 31; n++ {
			d := lessonDocument()
			d.ID = fmt.Sprint(n)
			d.ContentHash = "v"
			rows = append(rows, d)
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL, Projects: []Project{{ID: "p", Dataset: "dataset"}}}, nil)
	updates := make(chan func(), 100)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "知识"
	s := a.state()
	s.ListScroll.Y = 45
	s.Technical["saved"] = true
	s.Tools["tool"] = true
	a.load(s)
	drainReading(t, updates, func() bool {
		if !s.Loaded {
			return false
		}
		for _, i := range s.Items {
			if i.ReadingState != "ready" {
				return false
			}
		}
		return true
	})
	if len(s.Items) != 30 || rawCalls.Load() != 30 || !s.More || s.ListScroll.Y != 45 || !s.Technical["saved"] || !s.Tools["tool"] {
		t.Fatalf("auto title altered continuity/count: %d calls%d", len(s.Items), rawCalls.Load())
	}
	for _, i := range s.Items {
		if i.Title != "Readable knowledge." || i.Body != "" {
			t.Fatal("title incomplete or raw copied into row")
		}
	}
	a.selectItem(s, s.Items[0])
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected == nil || !s.Selected.BodyLoaded || itemPresentation(*s.Selected).VisibleBody == s.Selected.Body {
		t.Fatal("detail/visible copy missing")
	}
	s.DetailScroll[s.Selected.key()] = &ui.ScrollState{Y: 80}
	selectedKey := s.Selected.key()
	a.load(s)
	drainReading(t, updates, func() bool { return !s.Loading && !s.Reading })
	if s.Selected == nil || s.Selected.key() != selectedKey || s.DetailScroll[selectedKey].Y != 80 {
		t.Fatal("refresh lost reader")
	}
	deny.Store(true)
	a.selectItem(s, *s.Selected)
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected != nil || s.Items[0].ReadingState != "unavailable" || len(s.Items) != 30 || s.Items[1].ReadingState == "unavailable" {
		t.Fatal("denial retained body/copy or affected unrelated rows")
	}
	a.SetReadingPolicyRevision("revoked")
	if s.Selected != nil || s.Loaded {
		t.Fatal("policy revision not invalidated")
	}
}
func TestReadingLateReplyDoesNotCrossPaneOrRefillCache(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, "# Session learning (session s)\n\nOld content.")
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	updates := make(chan func(), 20)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Items = []Item{readingTestItem("old", "v")}
	s.Items[0].Title = "知识"
	a.startTitles(s)
	<-entered
	a.epoch++
	a.cancelTitles(s)
	s.Items = []Item{readingTestItem("new", "v")}
	s.Items[0].Title = "New page"
	close(release)
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("no canceled completion")
	}
	for len(updates) > 0 {
		fn := <-updates
		fn()
	}
	if s.Items[0].Title != "New page" {
		t.Fatal("late result replaced page")
	}
	a.titles.mu.Lock()
	count := len(a.titles.cache)
	a.titles.mu.Unlock()
	if count != 0 {
		t.Fatal("canceled result refilled cache")
	}
}
func TestReadingVersionlessRefreshAndOversize(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, strings.Repeat("x", 256*1024+1))
	}))
	defer server.Close()
	r := newReadingTitles()
	c := Client{Config: Config{ServiceURL: server.URL}}
	i := readingTestItem("d", "")
	i.Document.Updated = ""
	if keyForReading("s", i, "1") == keyForReading("s", i, "2") {
		t.Fatal("versionless reused across catalogs")
	}
	for n := 0; n < 2; n++ {
		if _, e := r.read(context.Background(), c, i, "1", false, true); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("oversize cached")
	}
}

func TestReadingTimeoutKeepsConfirmedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { <-req.Context().Done() }))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	a.titles.timeout = 5 * time.Millisecond
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	i := readingTestItem("d", "v")
	i.Body = "# Session learning (session s)\n\nConfirmed content."
	i.BodyLoaded = true
	s := a.state()
	s.Items = []Item{i}
	s.Selected = &i
	a.selectItem(s, i)
	drainReading(t, updates, func() bool { return !s.Reading })
	if s.Selected == nil || s.Selected.Body != i.Body || !s.Selected.BodyLoaded || s.Error == "" || len(s.Items) != 1 {
		t.Fatal("timeout erased valid body or claimed success")
	}
}

func TestRevocationAndIdentityChangeClearAllMemoryCopiesGraphAndProfile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "official.token")
	if err := os.WriteFile(tokenFile, []byte("initial-secret-token\n"), 0600); err != nil {
		t.Fatal(err)
	}

	makePopulatedApp := func() (*App, *pane, Item, Item) {
		a := New(Config{
			ServiceURL: "http://127.0.0.1:61255",
			TokenFile:  tokenFile,
			Projects:   []Project{{ID: "p1", Name: "memlane", Dataset: "ds1"}, {ID: "p2", Name: "other", Dataset: "ds2"}},
		}, nil)
		a.Update = func(func()) {}
		a.syncReadingIdentity()

		parentSess := Session{ID: "parent-sess", Dataset: "ds1", User: "u1"}
		childSess := Session{ID: "child-sess", Dataset: "ds1", User: "u1", IsSubagent: true, ParentSessionID: "parent-sess"}
		childItem := Item{
			ID: "child-sess", Dataset: "ds1", Kind: "会话", Title: "child",
			Body: "secret child body", BodyLoaded: true, Preview: "child preview",
			Session: &childSess,
			Detail:  &SessionDetail{Session: childSess, Count: 1, QAs: []QA{{ID: "q-c", Question: "cq", Answer: "ca"}}},
		}
		parentItem := Item{
			ID: "parent-sess", Dataset: "ds1", Kind: "会话", Title: "parent",
			Body: "secret parent body", BodyLoaded: true, Preview: "parent preview",
			Session:   &parentSess,
			Detail:    &SessionDetail{Session: parentSess, Count: 1, QAs: []QA{{ID: "q-p", Question: "pq", Answer: "pa"}}},
			Subagents: []Item{childItem},
		}
		otherDoc := Document{ID: "doc-ds2", Dataset: "ds2", Name: "Keep.md"}
		otherItem := Item{
			ID: "doc-ds2", Dataset: "ds2", Kind: "知识", Title: "Keep.md",
			Body: "ds2 body", BodyLoaded: true, Preview: "ds2 preview",
			Document: &otherDoc,
		}

		a.page = "知识"
		s := a.state()
		parentCopy := parentItem
		childCopy := childItem
		s.Selected = &childCopy
		s.SourcePage = &SessionHistoryPage{SessionDetail: *childItem.Detail, Paging: true}
		s.SubagentParent = &parentCopy
		s.SubagentParentPage = &SessionHistoryPage{SessionDetail: *parentItem.Detail, Paging: true}
		homeAnchor := parentItem
		s.HomeAnchor = &homeAnchor
		s.Items = []Item{parentItem, otherItem}
		s.RawCatalog = []Item{parentItem}
		s.Knowledge.Catalog = []Item{parentItem, otherItem}
		s.Knowledge.Measurements = map[string]knowledgeCardMeasurement{
			parentItem.key(): {Body: "secret card body"},
			otherItem.key():  {Body: "ds2 card body"},
		}

		anchor := parentItem
		a.searchAnchor = &anchor
		a.searchPane = s
		a.searchOrigin = &readingLocation{Page: "总览"}
		a.searchUI.Rows = []Item{parentItem, otherItem}

		// Overview snapshot contains both a nil-Document row (must not panic!) and a loaded body row.
		a.overviewState.Snapshot.Knowledge = []Item{
			{ID: parentItem.ID, Dataset: "ds1", Kind: "会话", Title: "no-doc-row", Body: "secret overview body", BodyLoaded: true, Preview: "ov preview", Document: nil},
			otherItem,
		}

		// Graph canvas + selection.
		a.graph = &GraphHost{
			project: "p1",
			dataset: "ds1",
			epoch:   3,
			frame:   map[string]any{"dataset": "ds1"},
			nodes:   map[string]json.RawMessage{"n1": json.RawMessage(`{"id":"n1","dataset":"ds1"}`)},
		}
		a.graphSelection = `{"id":"n1","dataset":"ds1"}`
		a.graphLoading = true

		// User profile cached body.
		a.userProfiles = userProfilesState{
			Loaded:         true,
			Next:           time.Now().Add(time.Hour),
			Selected:       "codex",
			Body:           "# Secret Profile Body",
			BodyKey:        "codex/ml-user-profiles/prof-1",
			ReadAt:         time.Now(),
			ReadGeneration: 5,
			Directory: userProfilesDirectory{
				Profiles: []userProfile{{
					ID: "codex", Kind: "codex", Name: "Codex", Status: "ready",
					Document: &userProfileDocument{Dataset: "ml-user-profiles", ID: "prof-1"},
				}},
			},
		}
		return a, s, parentItem, otherItem
	}

	// 1. Object-level revocation (unavailableReadingItem) clears parent subagent cache, item Detail/Preview/Subagents,
	//    HomeAnchor, searchAnchor, and overview body WITHOUT panicking on nil Document.
	a, s, parentItem, _ := makePopulatedApp()
	a.unavailableReadingItem(parentItem)
	if s.SubagentParent != nil || s.SubagentParentPage != nil {
		t.Fatal("unavailableReadingItem retained SubagentParent or SubagentParentPage")
	}
	if s.HomeAnchor != nil || a.searchAnchor != nil {
		t.Fatal("unavailableReadingItem retained HomeAnchor or searchAnchor")
	}
	if s.Items[0].Body != "" || s.Items[0].BodyLoaded || s.Items[0].Detail != nil || s.Items[0].Preview != "" || len(s.Items[0].Subagents) != 0 {
		t.Fatalf("unavailableReadingItem retained item content copies: %+v", s.Items[0])
	}
	if a.overviewState.Snapshot.Knowledge[0].Body != "" || a.overviewState.Snapshot.Knowledge[0].BodyLoaded {
		t.Fatalf("unavailableReadingItem retained overview knowledge body: %+v", a.overviewState.Snapshot.Knowledge[0])
	}

	// 2. Dataset-level InvalidateReading("ds1") clears all ds1 state & graph while preserving ds2.
	a, s, _, _ = makePopulatedApp()
	a.InvalidateReading("ds1")
	if s.SubagentParent != nil || s.SubagentParentPage != nil || s.HomeAnchor != nil || a.searchAnchor != nil {
		t.Fatal("InvalidateReading(ds1) retained parent/anchor pointers")
	}
	if s.Items[0].Detail != nil || s.Items[0].Preview != "" || len(s.Items[0].Subagents) != 0 || s.Items[0].Body != "" {
		t.Fatalf("InvalidateReading(ds1) retained ds1 item copies: %+v", s.Items[0])
	}
	if s.Items[1].Body != "ds2 body" || !s.Items[1].BodyLoaded {
		t.Fatal("InvalidateReading(ds1) unexpectedly cleared unrelated ds2 item")
	}
	if len(s.Knowledge.Catalog) != 1 || s.Knowledge.Catalog[0].Dataset != "ds2" {
		t.Fatalf("InvalidateReading(ds1) did not strip ds1 from Knowledge.Catalog: %+v", s.Knowledge.Catalog)
	}
	if a.graph.frame != nil || a.graph.nodes != nil || a.graph.epoch <= 3 || a.graphSelection != "" || a.graphLoading {
		t.Fatalf("InvalidateReading(ds1) did not clear graph canvas/selection: frame=%v nodes=%v epoch=%d sel=%q", a.graph.frame, a.graph.nodes, a.graph.epoch, a.graphSelection)
	}
	if a.overviewState.Snapshot.Knowledge[0].Body != "" || a.overviewState.Snapshot.Knowledge[0].BodyLoaded {
		t.Fatal("InvalidateReading(ds1) retained overview body")
	}

	// 3. Global revocation / identity change via SetReadingPolicyRevision clears EVERYTHING including userProfiles.Body/BodyKey
	//    and immediately disables the "复制正文" copy button.
	a, s, _, _ = makePopulatedApp()
	prevGen := a.userProfiles.ReadGeneration
	a.SetReadingPolicyRevision("revoked-v2")
	if a.userProfiles.Body != "" || a.userProfiles.BodyKey != "" || a.userProfiles.ReadGeneration <= prevGen {
		t.Fatalf("identity change retained user profile body/key: body=%q key=%q gen=%d", a.userProfiles.Body, a.userProfiles.BodyKey, a.userProfiles.ReadGeneration)
	}
	if a.graph.frame != nil || a.graph.nodes != nil || a.graphSelection != "" {
		t.Fatal("identity change retained graph canvas or selection")
	}
	if s.SubagentParent != nil || s.SubagentParentPage != nil || s.HomeAnchor != nil || len(s.Knowledge.Catalog) != 0 || a.searchAnchor != nil {
		t.Fatal("identity change retained pane/search/catalog state")
	}
	for n, row := range s.Items {
		if row.Body != "" || row.BodyLoaded || row.Detail != nil || row.Preview != "" || len(row.Subagents) != 0 {
			t.Fatalf("identity change retained item %d content: %+v", n, row)
		}
	}
	// Verify "复制正文" button on 用户画像 page is disabled after revocation.
	a.page = "用户画像"
	tt := ui.NewTester(a.View, 1200, 820)
	_ = tt.Click("复制正文")
	if tt.Clipboard() != "" {
		t.Fatalf("revoked user profile body was still copyable: %q", tt.Clipboard())
	}
}

func TestRevocationCancelsInFlightOverviewAndDropsLateCallbacks(t *testing.T) {
	bootstrapResp := v2OverviewBootstrapResponse{
		Generation: 1,
		RecentItems: []v2KnowledgeItem{{
			DocID: "doc-secret", DatasetID: "ds1", ProjectID: "p1", Title: "confidential",
			Kind: "knowledge", PreviewText: "SECRET_REVOKED_PREVIEW: confidential memory preview content",
		}},
		PipelineRuns: []activityRow{{
			ID: "run-1", Run: "run-1", Dataset: "ds1", Operation: "improve", Outcome: "completed",
			Created: "2026-11-21T10:05:00Z",
		}},
	}

	// 1. In-flight /v1/overview/bootstrap is cancelled immediately on InvalidateReading("ds1").
	t.Run("bootstrap_cancelled_in_flight", func(t *testing.T) {
		started := make(chan struct{}, 1)
		cancelled := make(chan struct{}, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/capabilities" {
				_ = json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/overview/bootstrap") {
				select {
				case started <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				select {
				case cancelled <- struct{}{}:
				default:
				}
				return
			}
			w.WriteHeader(404)
		}))
		defer server.Close()

		dir := t.TempDir()
		token := filepath.Join(dir, "control.token")
		if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
			t.Fatal(err)
		}
		a := New(Config{
			ServiceURL:     server.URL,
			Projects:       []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
			ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token},
		}, nil)
		a.access.Loaded = true
		updates := make(chan func(), 20)
		a.Update = func(fn func()) { updates <- fn }

		a.loadOverview()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("bootstrap request never started")
		}
		genBefore := a.overviewState.generation
		actGenBefore := a.overviewState.activityGeneration

		a.InvalidateReading("ds1")

		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("InvalidateReading did not cancel in-flight bootstrap HTTP context")
		}
		if a.overviewState.generation <= genBefore || a.overviewState.activityGeneration <= actGenBefore {
			t.Fatalf("InvalidateReading did not bump overview generations: gen %d->%d act %d->%d",
				genBefore, a.overviewState.generation, actGenBefore, a.overviewState.activityGeneration)
		}
		if a.overviewState.Loading || a.overviewState.ActivityLoading {
			t.Fatal("InvalidateReading left overview loading flags true")
		}
	})

	// 2. Already-queued /v1/overview/bootstrap callback is dropped after identity/policy revocation.
	t.Run("bootstrap_queued_callback_dropped_after_policy_revision", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/capabilities" {
				_ = json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/overview/bootstrap") {
				_ = json.NewEncoder(w).Encode(bootstrapResp)
				return
			}
			w.WriteHeader(404)
		}))
		defer server.Close()

		dir := t.TempDir()
		token := filepath.Join(dir, "control.token")
		if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
			t.Fatal(err)
		}
		a := New(Config{
			ServiceURL:     server.URL,
			Projects:       []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
			ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token},
		}, nil)
		a.access.Loaded = true
		updates := make(chan func(), 20)
		a.Update = func(fn func()) { updates <- fn }

		a.loadOverview()
		var queued func()
		select {
		case queued = <-updates:
		case <-time.After(2 * time.Second):
			t.Fatal("bootstrap callback not queued")
		}

		// Revocation occurs before the queued UI callback runs.
		a.SetReadingPolicyRevision("revoked-v2")
		queued()

		if a.overviewState.Loaded || len(a.overviewState.Snapshot.Knowledge) != 0 || len(a.overviewState.Events) != 0 {
			t.Fatalf("stale bootstrap callback repopulated overview after revocation: loaded=%v knowledge=%d events=%d",
				a.overviewState.Loaded, len(a.overviewState.Snapshot.Knowledge), len(a.overviewState.Events))
		}
	})

	// 3. Bootstrap and activity queued callbacks are dropped after dataset invalidation.
	t.Run("bootstrap_and_activity_callbacks_dropped_after_invalidation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v1/capabilities":
				json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
			case r.URL.Path == "/v1/overview/bootstrap":
				json.NewEncoder(w).Encode(bootstrapResp)
			case strings.Contains(r.URL.Path, "/pipeline-runs"):
				_ = json.NewEncoder(w).Encode(bootstrapResp.PipelineRuns)
			default:
				_ = json.NewEncoder(w).Encode([]any{})
			}
		}))
		defer server.Close()

		a := New(Config{
			ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL),
			Projects: []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
		}, nil)
		a.access.Loaded = true
		updates := make(chan func(), 20)
		a.Update = func(fn func()) { updates <- fn }

		a.loadOverview()
		var cb1, cb2 func()
		select {
		case cb1 = <-updates:
		case <-time.After(2 * time.Second):
			t.Fatal("bootstrap callback not queued")
		}
		a.startOverviewActivity()
		select {
		case cb2 = <-updates:
		case <-time.After(2 * time.Second):
			t.Fatal("activity callback not queued")
		}

		a.InvalidateReading("ds1")
		cb1()
		cb2()

		if a.overviewState.Loaded || len(a.overviewState.Snapshot.Knowledge) != 0 || len(a.overviewState.Events) != 0 {
			t.Fatalf("stale bootstrap/activity callbacks repopulated overview after InvalidateReading: loaded=%v knowledge=%d events=%d",
				a.overviewState.Loaded, len(a.overviewState.Snapshot.Knowledge), len(a.overviewState.Events))
		}
	})

	// 4. Token rotation on disk during in-flight overview bootstrap is rejected by callback identity guard.
	t.Run("token_rotation_on_disk_drops_in_flight_overview", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/capabilities" {
				_ = json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/overview/bootstrap") {
				_ = json.NewEncoder(w).Encode(bootstrapResp)
				return
			}
			w.WriteHeader(404)
		}))
		defer server.Close()

		dir := t.TempDir()
		controlToken := filepath.Join(dir, "control.token")
		officialToken := filepath.Join(dir, "official.key")
		if err := os.WriteFile(controlToken, []byte(strings.Repeat("m", 40)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(officialToken, []byte("token-v1-secret"), 0600); err != nil {
			t.Fatal(err)
		}
		a := New(Config{
			ServiceURL:     server.URL,
			TokenFile:      officialToken,
			Projects:       []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
			ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: controlToken},
		}, nil)
		a.access.Loaded = true
		updates := make(chan func(), 20)
		a.Update = func(fn func()) { updates <- fn }

		a.loadOverview()
		var queued func()
		select {
		case queued = <-updates:
		case <-time.After(2 * time.Second):
			t.Fatal("bootstrap callback not queued")
		}

		// Rotate credential file on disk (different size + mtime) without calling InvalidateReading manually.
		if err := os.WriteFile(officialToken, []byte("token-v2-rotated-credential"), 0600); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(2 * time.Second)
		_ = os.Chtimes(officialToken, future, future)

		queued()

		if a.overviewState.Loaded || len(a.overviewState.Snapshot.Knowledge) != 0 || len(a.overviewState.Events) != 0 {
			t.Fatalf("overview callback committed after on-disk token rotation: loaded=%v knowledge=%d events=%d",
				a.overviewState.Loaded, len(a.overviewState.Snapshot.Knowledge), len(a.overviewState.Events))
		}
	})

	// 5. Natural DeadlineExceeded clears Loading/ActivityLoading and reports timeout instead of locking periodic refresh.
	t.Run("deadline_exceeded_clears_loading_and_reports_error", func(t *testing.T) {
		origLoad, origAct := overviewLoadTimeout, overviewActivityTimeout
		overviewLoadTimeout = 40 * time.Millisecond
		overviewActivityTimeout = 40 * time.Millisecond
		defer func() {
			overviewLoadTimeout = origLoad
			overviewActivityTimeout = origAct
		}()

		unblock := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
		}))
		defer func() { close(unblock); server.Close() }()

		// 5a. Fast-path /v1/overview/bootstrap timeout clears both Loading and ActivityLoading.
		dir := t.TempDir()
		token := filepath.Join(dir, "control.token")
		if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
			t.Fatal(err)
		}
		a := New(Config{
			ServiceURL:     server.URL,
			Projects:       []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
			ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token},
		}, nil)
		a.access.Loaded = true
		updates := make(chan func(), 20)
		a.Update = func(fn func()) { updates <- fn }
		a.loadOverview()
		drainReading(t, updates, func() bool { return !a.overviewState.Loading && !a.overviewState.ActivityLoading })
		if a.overviewState.Error == "" || a.overviewState.ActivityError == "" {
			t.Fatalf("bootstrap timeout cleared loading without recording error: err=%q actErr=%q",
				a.overviewState.Error, a.overviewState.ActivityError)
		}

		// 5b. Missing control plane settles both loading states with an explicit error.
		b := New(Config{
			ServiceURL: server.URL, ProjectControl: &ProjectControlConfig{},
			Projects: []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
		}, nil)
		updatesB := make(chan func(), 20)
		b.Update = func(fn func()) { updatesB <- fn }
		b.loadOverview()
		drainReading(t, updatesB, func() bool { return !b.overviewState.Loading && !b.overviewState.ActivityLoading })
		if !strings.Contains(b.overviewState.Error, "控制面服务不可用") || b.overviewState.ActivityError != b.overviewState.Error {
			t.Fatalf("missing control plane did not settle the overview: err=%q actErr=%q",
				b.overviewState.Error, b.overviewState.ActivityError)
		}
	})

	// 6. HTTP 401 withdraws overview data while preserving scope and error so subsequent App.View frames do not loop.
	t.Run("http_401_preserves_scope_and_prevents_per_frame_storm", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/capabilities" {
				json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
				return
			}
			calls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		a := New(Config{
			ServiceURL: server.URL, ProjectControl: readingControlConfig(t, server.URL),
			Projects: []Project{{ID: "p1", Name: "P1", Dataset: "ds1"}},
		}, nil)
		a.access.Loaded = true
		a.page = "总览"
		a.overviewState.scope = a.overviewScope()
		a.overviewState.Loaded = true
		a.overviewState.Snapshot.Knowledge = []Item{{ID: "old", Dataset: "ds1", Body: "secret", BodyLoaded: true}}
		a.overviewState.Events = []overviewEvent{{Key: "ev-old", Record: activityRow{Dataset: "ds1"}}}
		updates := make(chan func(), 20)
		a.Update = func(fn func()) { updates <- fn }

		a.loadOverview()
		drainReading(t, updates, func() bool { return !a.overviewState.Loading && !a.overviewState.ActivityLoading })

		if calls.Load() != 1 {
			t.Fatal("bootstrap did not observe the HTTP 401")
		}
		if a.overviewState.scope != a.overviewScope() {
			t.Fatalf("401 cleared overview scope: got %q want %q", a.overviewState.scope, a.overviewScope())
		}
		if a.overviewState.Error == "" && a.overviewState.ActivityError == "" {
			t.Fatal("401 did not preserve visible error state")
		}
		if (len(a.overviewState.Snapshot.Knowledge) > 0 && a.overviewState.Snapshot.Knowledge[0].Body != "") || len(a.overviewState.Events) != 0 {
			t.Fatal("401 failed to withdraw overview bodies/events")
		}
		genAfter401 := a.overviewState.generation

		// Render multiple frames: scope matches so App.View must NOT re-trigger loadOverview().
		tt := ui.NewTester(a.View, 1200, 820)
		for i := 0; i < 4; i++ {
			tt.Frame()
		}
		if a.overviewState.generation != genAfter401 || a.overviewState.Loading || a.overviewState.ActivityLoading {
			t.Fatalf("401 triggered per-frame request storm: gen %d -> %d loading=%v/%v calls=%d",
				genAfter401, a.overviewState.generation, a.overviewState.Loading, a.overviewState.ActivityLoading, calls.Load())
		}
	})

	// 7. Single-dataset InvalidateReading filters revoked dataset events (record/failure/recall) and clears matching expanded detail.
	t.Run("dataset_invalidation_filters_overview_events", func(t *testing.T) {
		a := New(Config{ServiceURL: "http://127.0.0.1:60000"}, nil)
		a.overviewState.Events = []overviewEvent{
			{Key: "ev-ds1-rec", Record: activityRow{Dataset: "ds1"}},
			{Key: "ev-ds1-fail", Record: activityRow{Dataset: ""}, Failures: []activityRow{{Dataset: "ds1"}}},
			{Key: "ev-ds1-recall", Recall: &RecallEvent{ID: "r1", Hits: []RecallHit{{Dataset: "ds1", Document: "d1"}}}},
			{Key: "ev-ds2-keep", Record: activityRow{Dataset: "ds2"}},
		}
		a.overviewState.ExpandedEvent = "ev-ds1-fail"
		a.overviewState.ExpandedReport = "secret failure report"
		a.overviewState.ExpandedDetail = &failureDetailModel{}

		a.InvalidateReading("ds1")

		if len(a.overviewState.Events) != 1 || a.overviewState.Events[0].Key != "ev-ds2-keep" {
			t.Fatalf("dataset invalidation left revoked events or dropped unrelated ds2 event: %+v", a.overviewState.Events)
		}
		if a.overviewState.ExpandedEvent != "" || a.overviewState.ExpandedReport != "" || a.overviewState.ExpandedDetail != nil {
			t.Fatal("dataset invalidation retained expanded detail of revoked event")
		}
	})
}

func TestInvalidateReadingUnifiedHooks(t *testing.T) {
	wantOrder := []string{"common-editor", "search", "project-ui", "user-profiles", "graph", "reading", "overview", "project-queues"}
	t.Run("zero value", func(t *testing.T) {
		a := &App{}
		a.InvalidateReading("ds-1")
		a.InvalidateReading("")
		if a.authEpoch != 2 || len(a.readingInvalidators) != len(wantOrder) {
			t.Fatal("zero-value App did not install and run its hooks")
		}
	})

	a := New(Config{}, nil)
	var names []string
	for _, entry := range a.readingInvalidators {
		names = append(names, entry.name)
	}
	if !slices.Equal(names, wantOrder) {
		t.Fatalf("built-in hook order = %v, want %v", names, wantOrder)
	}

	first := Item{Dataset: "ds-1", ID: "one", Body: "private body", BodyLoaded: true, Preview: "preview"}
	second := Item{Dataset: "ds-2", ID: "two", Body: "other body", BodyLoaded: true}
	newPane := func(item Item) *pane {
		return &pane{
			Items: []Item{item}, Selected: &item, HomeAnchor: &item, SubagentParent: &item,
			SubagentParentPage: &SessionHistoryPage{}, SessionSubagents: true, SourcePage: &SessionHistoryPage{Dataset: item.Dataset},
			History: []Item{first, second}, RawCatalog: []Item{first, second},
			DetailLists: map[string]*ui.ListState{"ds-1/one": {}, "ds-2/two": {}},
			Knowledge: knowledgeFeedState{
				Catalog: []Item{first, second}, Measurements: map[string]knowledgeCardMeasurement{"ds-1/one": {}, "ds-2/two": {}},
				Appending: true, Prepending: true, NextCursor: "next", PrevCursor: "prev", HasPrev: true,
			},
			Loading: true, Locating: true, Reading: true, RawAppending: true, RawNextCursor: "raw",
			CommonNextCursor: "next", CommonPrevCursor: "prev", CommonHasPrev: true,
			CommonNav: []v2KnowledgePageOpts{{Cursor: "page"}}, CommonCursors: []string{"page"}, CommonPageIndex: 1,
		}
	}
	p1, p2, queueReader := newPane(first), newPane(second), newPane(first)
	a.panes["first"], a.panes["alias"], a.panes["second"] = p1, p1, p2
	newQueue := func(reader *pane) *projectQueueState {
		return &projectQueueState{
			Reader: reader, Loading: true, UnreadKnowledge: []Item{first}, UnreadHasMore: true,
			ServerUnreadKnown: true, ServerUnreadCount: 4, ServerUnreadStateRev: 7,
			Activity: []overviewEvent{{Key: "event"}}, ProcessingNotices: []processingNotice{{Code: "notice"}},
			ProcessingIssues: []processingIssue{{Reason: "issue"}}, Documents: []Item{first}, PendingDocumentsCount: 1,
		}
	}
	q1, q2, qAlias := newQueue(p1), newQueue(p2), newQueue(queueReader)
	a.access.Queues = map[string]*projectQueueState{
		"project/ds-1": q1, "project/ds-2": q2, "alias/ds-1": qAlias, "other/ds-2": {Reader: queueReader},
	}

	a.commonEdit = commonEditorState{Open: true, Existing: &Document{Dataset: first.Dataset}, Draft: CommonDraft{Body: first.Body},
		Submitted: true, Receipt: CommonWriteReceipt{DataID: "keep"}}
	a.searchUI = unifiedSearchState{Open: true, Loading: true, Rows: []Item{first, second}, Submitted: "search"}
	a.searchOrigin, a.searchAnchor, a.searchPane = &readingLocation{}, &second, p2
	a.projectUI.InfoOpen = true
	a.userProfiles = userProfilesState{Body: first.Body, BodyKey: "profile/ds-1/one", Reading: true}
	a.graph, a.graphSelection, a.pendingGraphLoad, a.graphLoading = &GraphHost{dataset: second.Dataset}, "node", &graphLoad{}, true
	a.recentScope, a.recent = "recent", []Item{second}
	for _, item := range []Item{first, second} {
		a.titles.cache[readingKey{Dataset: item.Dataset, ID: item.ID}] = readingCacheEntry{item: item}
		a.titles.bytes += len(item.Body)
		a.detailChunks.getOrSplit(item.Dataset, item.ID, "body", detailContentKey{}, func() []string { return []string{item.Body} })
	}
	ev1 := overviewEvent{Key: "first", Record: activityRow{Dataset: first.Dataset}}
	ev2 := overviewEvent{Key: "second", Record: activityRow{Dataset: second.Dataset}}
	recall := &RecallEvent{Hits: []RecallHit{{Dataset: first.Dataset}, {Dataset: second.Dataset}}}
	mixed := overviewEvent{Key: "mixed", Failures: []activityRow{ev1.Record, ev2.Record}, Recall: recall}
	v := &a.overviewState
	*v = overviewViewState{
		scope: "scope", Loaded: true, RawLoaded: true, Loading: true, ActivityLoading: true, DayLoading: true,
		DayItems: []Item{first, second}, DayNextCursor: "next", DayHasMore: true, DayTotal: 2,
		Events: []overviewEvent{ev1, mixed, ev2}, History: []overviewEvent{ev1, ev2}, HistoryLoaded: true,
		ExpandedEvent: "mixed", ExpandedReport: "report", ExpandedDetail: &failureDetailModel{},
		cachedGroupedEvents: []overviewEvent{mixed}, Snapshot: overviewSnapshot{Knowledge: []Item{first, second}},
	}
	cancelCount := 0
	cancel := func() { cancelCount++ }
	a.recentCancel, p1.cancel, p1.readCancel, p1.titleCancel = cancel, cancel, cancel, cancel
	v.cancel, v.activityCancel, v.dayCancel, v.recallCancel, a.searchUI.cancel = cancel, cancel, cancel, cancel, cancel

	var customCalls []string
	a.registerReadingInvalidator("probe", readingInvalidatorFunc(func(string) { t.Fatal("replaced hook was called") }))
	a.registerReadingInvalidator("last", readingInvalidatorFunc(func(dataset string) { customCalls = append(customCalls, "last:"+dataset) }))
	a.registerReadingInvalidator("probe", readingInvalidatorFunc(func(dataset string) {
		customCalls = append(customCalls, "probe:"+dataset)
		if a.authEpoch != 1 && dataset == first.Dataset || a.authEpoch != 2 && dataset == "" ||
			a.projectUI.InfoOpen || q1.Documents != nil || p1.Selected != nil {
			t.Error("custom hook ran before the fence or a built-in hook")
		}
	}))

	a.InvalidateReading(first.Dataset)
	if !slices.Equal(customCalls, []string{"probe:ds-1", "last:ds-1"}) || cancelCount != 9 {
		t.Fatalf("scoped invalidation calls = %v, cancellations = %d", customCalls, cancelCount)
	}
	if a.commonEdit.Open || a.commonEdit.Existing != nil || a.commonEdit.Draft.Body != "" ||
		!a.commonEdit.Submitted || a.commonEdit.Receipt.DataID != "keep" ||
		a.userProfiles.Reading || a.userProfiles.Body != "" || a.userProfiles.ReadGeneration != 1 {
		t.Fatal("scoped invalidation did not withdraw editor/profile content and preserve the save receipt")
	}
	if a.searchUI.Open || a.searchUI.Loading || a.searchUI.request != 1 || len(a.searchUI.Rows) != 1 ||
		a.searchUI.Rows[0].ID != second.ID || a.searchAnchor != &second ||
		a.pendingGraphLoad != nil || a.graphLoading || a.graphSelection != "node" || a.graphLoadRequest != 1 || a.graph.epoch != 0 {
		t.Fatal("scoped invalidation changed unrelated search/graph content or left graphLoading stuck")
	}
	if len(a.titles.cache) != 1 || a.titles.cache[readingKey{Dataset: second.Dataset, ID: second.ID}].item.Body != second.Body ||
		len(a.detailChunks.entries) != 1 || a.detailChunks.entries[0].dataset != second.Dataset ||
		a.recentScope != "" || a.recentCancel != nil || a.recentRequest != 1 || len(a.recent) != 1 {
		t.Fatal("scoped invalidation did not preserve unrelated cached reading state")
	}
	if p1.request != 1 || p1.readRequest != 1 || p1.titleRequest != 1 ||
		p1.cancel != nil || p1.readCancel != nil || p1.titleCancel != nil ||
		p1.Loading || p1.Locating || p1.Reading || p1.RawAppending || p1.Knowledge.Appending || p1.Knowledge.Prepending ||
		p1.RawNextCursor != "" || p1.Knowledge.NextCursor != "" || p1.Knowledge.PrevCursor != "" || p1.Knowledge.HasPrev ||
		p1.CommonNextCursor != "" || p1.CommonPrevCursor != "" || p1.CommonHasPrev ||
		p1.CommonNav != nil || p1.CommonCursors != nil || p1.CommonPageIndex != 0 ||
		p1.Selected != nil || p1.HomeAnchor != nil || p1.SubagentParent != nil || p1.SubagentParentPage != nil || p1.SessionSubagents ||
		p1.SourcePage != nil || p1.Error != "内容已失效或无权读取，请刷新" || p1.Items[0].ReadingState != "unavailable" ||
		len(p1.History) != 1 || p1.History[0].ID != second.ID || len(p1.DetailLists) != 1 || p1.DetailLists["ds-2/two"] == nil ||
		len(p1.Knowledge.Measurements) != 1 || len(p1.Knowledge.Catalog) != 1 || p1.Knowledge.Catalog[0].ID != second.ID ||
		len(p1.RawCatalog) != 1 || p1.RawCatalog[0].ID != second.ID {
		t.Fatal("scoped invalidation did not reset the matched pane exactly once")
	}
	if p2.request != 0 || p2.readRequest != 0 || p2.titleRequest != 1 || p2.Loading || p2.Reading || p2.Selected == nil ||
		p2.Items[0].Body != second.Body || queueReader.request != 1 || queueReader.readRequest != 1 || queueReader.titleRequest != 1 {
		t.Fatal("scoped invalidation changed unrelated pane data, left loading stuck, or failed to deduplicate the queue reader")
	}
	if v.snapshotVersion != 1 || v.generation != 1 || v.activityGeneration != 1 || v.dayRequest != 1 ||
		v.cancel != nil || v.activityCancel != nil || v.dayCancel != nil || v.recallCancel != nil ||
		v.Loading || v.ActivityLoading || v.DayLoading || v.cachedGroupedEvents != nil ||
		len(v.DayItems) != 1 || v.DayItems[0].ID != second.ID || v.DayNextCursor != "" || v.DayHasMore || v.DayTotal != 2 ||
		v.ExpandedEvent != "" || v.ExpandedReport != "" || v.ExpandedDetail != nil ||
		len(v.Events) != 2 || v.Events[0].Key != mixed.Key || len(v.Events[0].Failures) != 1 ||
		v.Events[0].Failures[0].Dataset != second.Dataset || len(v.Events[0].Recall.Hits) != 1 ||
		v.Events[0].Recall.Hits[0].Dataset != second.Dataset || len(recall.Hits) != 2 ||
		v.Events[1].Key != ev2.Key || len(v.History) != 1 || v.History[0].Key != ev2.Key ||
		v.Snapshot.Knowledge[0].BodyLoaded || v.Snapshot.Knowledge[1].Body != second.Body {
		t.Fatal("scoped invalidation did not filter overview data and fence loaders")
	}
	checkQueueReset := func(q *projectQueueState, version uint64) {
		t.Helper()
		if q.Loading || q.Request != version || q.StateVersion != version ||
			q.UnreadKnowledge != nil || q.UnreadHasMore || q.ServerUnreadKnown || q.ServerUnreadCount != 0 || q.ServerUnreadStateRev != 0 ||
			q.Activity != nil || q.ProcessingNotices != nil || q.ProcessingIssues != nil || q.Documents != nil || q.PendingDocumentsCount != 0 {
			t.Fatalf("queue did not reset at generation %d", version)
		}
	}
	checkQueueReset(q1, 1)
	checkQueueReset(qAlias, 1)
	if !q2.Loading || q2.Request != 0 || q2.StateVersion != 0 || q2.ServerUnreadCount != 4 || len(q2.Documents) != 1 {
		t.Fatal("unrelated queue changed")
	}

	a.commonEdit.Open, a.commonEdit.Existing = true, &Document{Dataset: second.Dataset}
	a.userProfiles.Body, a.userProfiles.BodyKey = second.Body, "profile/ds-2/two"
	a.projectUI.InfoOpen = true
	a.InvalidateReading("")
	if !slices.Equal(customCalls, []string{"probe:ds-1", "last:ds-1", "probe:", "last:"}) {
		t.Fatalf("global invalidation hook order = %v", customCalls)
	}
	if a.commonEdit.Open || a.commonEdit.Existing != nil || a.userProfiles.Body != "" || a.userProfiles.ReadGeneration != 2 ||
		a.searchOrigin != nil || a.searchAnchor != nil || a.searchPane != nil || a.searchUI.Rows != nil ||
		a.graph.epoch != 1 || a.graphSelection != "" || a.graphLoading || a.graphLoadRequest != 2 ||
		len(a.titles.cache) != 0 || len(a.detailChunks.entries) != 0 || a.recent != nil || a.recentRequest != 2 {
		t.Fatal("global invalidation retained controller state")
	}
	if p1.request != 2 || p1.readRequest != 2 || p1.titleRequest != 2 ||
		queueReader.request != 2 || queueReader.readRequest != 2 || queueReader.titleRequest != 2 ||
		p2.request != 1 || p2.readRequest != 1 || p2.titleRequest != 2 || p2.Selected != nil ||
		p2.Items[0].BodyLoaded || p2.RawCatalog != nil || p2.Knowledge.Catalog != nil ||
		len(p2.History) != 0 || len(p2.DetailLists) != 0 || len(p2.Knowledge.Measurements) != 0 {
		t.Fatal("global invalidation did not reset all unique panes")
	}
	if v.snapshotVersion != 2 || v.generation != 2 || v.activityGeneration != 2 || v.dayRequest != 2 || v.scope != "" ||
		v.Loaded || v.RawLoaded || v.HistoryLoaded || v.DayItems != nil || v.DayTotal != 0 || v.Events != nil ||
		v.History != nil || v.Snapshot.Knowledge[1].BodyLoaded {
		t.Fatal("global invalidation retained overview data")
	}
	checkQueueReset(q1, 2)
	checkQueueReset(qAlias, 2)
	checkQueueReset(q2, 1)

	t.Run("evicted expanded failure detail and unrelated graphLoading", func(t *testing.T) {
		app := New(Config{}, nil)
		evDS1 := overviewEvent{
			Key: "ev-ds1", Title: "提炼记忆", Project: "Proj1",
			Record:   activityRow{Dataset: "ds-1", Operation: "cognify", Outcome: "failed"},
			Failures: []activityRow{{ID: "f1", Dataset: "ds-1", Operation: "cognify", ErrorClass: "TimeoutError"}},
		}
		evDS2 := overviewEvent{
			Key: "ev-ds2", Title: "提炼记忆", Project: "Proj2",
			Record:   activityRow{Dataset: "ds-2", Operation: "cognify", Outcome: "failed"},
			Failures: []activityRow{{ID: "f2", Dataset: "ds-2", Operation: "cognify", ErrorClass: "TimeoutError"}},
		}
		// 1. Open ds-1 failure detail, then evict it from v.Events before revoking ds-1.
		app.openOverviewFailureDetail(evDS1, time.UTC)
		app.overviewState.CopiedEvent = evDS1.Key
		app.overviewState.Events = []overviewEvent{evDS2}
		app.graph = &GraphHost{dataset: "ds-2"}
		app.graphSelection = "node-ds2"
		app.graphLoading = true

		app.InvalidateReading("ds-1")
		if app.overviewState.ExpandedEvent != "" || app.overviewState.ExpandedReport != "" ||
			app.overviewState.ExpandedDetail != nil || app.overviewState.CopiedEvent != "" {
			t.Fatal("revoking ds-1 retained evicted failure dialog or copy report")
		}
		if app.graphLoading || app.graphLoadRequest != 1 || app.graph.dataset != "ds-2" ||
			app.graph.epoch != 0 || app.graphSelection != "node-ds2" {
			t.Fatal("revoking ds-1 left ds-2 graphLoading stuck or cleared ds-2 graph canvas")
		}

		// 2. Open ds-2 failure detail, evict it from v.Events, then revoke ds-1: ds-2 detail stays open.
		app.openOverviewFailureDetail(evDS2, time.UTC)
		app.overviewState.Events = nil
		app.InvalidateReading("ds-1")
		if app.overviewState.ExpandedEvent != evDS2.Key || app.overviewState.ExpandedDetail == nil || app.overviewState.ExpandedReport == "" {
			t.Fatal("revoking ds-1 unexpectedly closed evicted ds-2 failure dialog")
		}
		// Revoking ds-2 clears it immediately.
		app.InvalidateReading("ds-2")
		if app.overviewState.ExpandedEvent != "" || app.overviewState.ExpandedDetail != nil || app.overviewState.ExpandedReport != "" {
			t.Fatal("revoking ds-2 did not close evicted ds-2 failure dialog")
		}
	})

	t.Run("session title excerpt cleared and empty/unrelated pane loading unfrozen", func(t *testing.T) {
		app := New(Config{}, nil)
		sessItem := Item{
			ID: "sess-1", Dataset: "ds-1", Kind: "会话",
			Title: "secret first question excerpt", ParentTitle: "secret parent question",
			Session: &Session{ID: "sess-1", Dataset: "ds-1"}, ReadingState: "ready",
		}
		pSess := app.paneFor("知识/0/会话")
		pSess.Items = []Item{sessItem}
		pEmpty := app.paneFor("知识/1/知识")
		pEmpty.Loading = true
		pEmpty.Knowledge.Appending = true
		pOther := app.paneFor("知识/2/知识")
		otherItem := Item{ID: "k-2", Dataset: "ds-2", Kind: "知识", Title: "keep-ds2", Body: "keep-body"}
		pOther.Items = []Item{otherItem}
		pOther.Selected = &otherItem
		pOther.Loading = true
		pOther.Reading = true
		otherCancelled := false
		pOther.cancel = func() { otherCancelled = true }

		app.InvalidateReading("ds-1")
		if pSess.Items[0].Title != "" || pSess.Items[0].ParentTitle != "" || pSess.Items[0].ReadingState != "unavailable" {
			t.Fatalf("revoked session retained title excerpt: %+v", pSess.Items[0])
		}
		if pEmpty.Loading || pEmpty.Knowledge.Appending {
			t.Fatal("empty pane stayed stuck in loading after dataset invalidation")
		}
		if !otherCancelled || pOther.Loading || pOther.Reading || pOther.Selected == nil || pOther.Items[0].Title != "keep-ds2" {
			t.Fatal("unrelated pane stayed stuck loading or lost its already-loaded ds-2 data")
		}
	})
}
