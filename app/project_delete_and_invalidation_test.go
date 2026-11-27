package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
)

func TestProjectDetailDeleteDialogFailureAndSuccessSync(t *testing.T) {
	var failDelete atomic.Bool
	failDelete.Store(true)
	var deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/knowledge/ds/k" {
			deleteCalls.Add(1)
			if failDelete.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "delete_failed", "message": "后端删除失败"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	a := projectUIFixture()
	a.Config.ProjectControl = readingControlConfig(t, server.URL)
	a.Client.Config = a.Config
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 32)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Knowledge.Detail = true

	// Seed unread, overview totals & heatmap, and recent list.
	day, ok := overviewItemDate(s.Items[0], time.Local)
	if !ok {
		day = "2026-11-12"
	}
	s.Items[0].Date = day
	s.Selected.Date = day
	s.Knowledge.Catalog = []Item{s.Items[0]}
	a.recent = []Item{s.Items[0], {ID: "other-doc", Dataset: "other", Kind: "知识"}}
	q := &ProjectQueueState{
		UnreadState: UnreadState{
			ServerUnreadKnown: true,
			ServerUnreadCount: 3,
			UnreadPreview:     []Item{s.Items[0]},
		},
	}
	a.access.Queues = map[string]*ProjectQueueState{"p/ds": q}
	a.overviewState.V2Mode = true
	a.overviewState.V2TotalKnowledge = 10
	a.overviewState.V2DayBuckets = map[string]int{day: 2}
	a.overviewState.Snapshot.Knowledge = []Item{s.Items[0]}
	a.overviewState.DayItems = []Item{s.Items[0]}
	a.overviewState.DayTotal = 1

	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("Cognee 提供 · 只读") {
		t.Fatal("project knowledge detail footer must not claim read-only")
	}
	if !tt.HasText("Cognee 提供") {
		t.Fatal("project knowledge detail footer missing")
	}

	// Open confirmation dialog and verify agreed wording.
	clickProjectUI(t, tt, "删除记忆")
	if !tt.HasText("产生这条记忆的原始对话会保留。") || !tt.HasText("删除『Development runtime configur…』？") {
		t.Fatalf("confirmation dialog wording missing: %v", tt.Texts())
	}

	// 1. Failing delete must keep item selected and in list, and display error.
	authBefore := a.authEpoch
	clickProjectUI(t, tt, "确认删除记忆")
	drainReading(t, updates, func() bool { return !a.projectUI.DeleteBusy })
	if deleteCalls.Load() != 1 || a.projectUI.DeleteError == "" {
		t.Fatalf("expected failed delete with error, calls=%d err=%q", deleteCalls.Load(), a.projectUI.DeleteError)
	}
	if s.Selected == nil || len(s.Items) != 1 || a.overviewState.V2TotalKnowledge != 10 || q.ServerUnreadCount != 3 || a.authEpoch != authBefore {
		t.Fatal("failed delete mutated UI state")
	}

	// 2. Succeeding delete must remove item, sync unread + overview + heatmap, and advance authEpoch.
	failDelete.Store(false)
	tt.Frame()
	clickProjectUI(t, tt, "确认删除记忆")
	drainReading(t, updates, func() bool { return !a.projectUI.DeleteBusy && a.projectUI.DeleteTarget == nil })
	if deleteCalls.Load() != 2 {
		t.Fatalf("expected 2 delete calls, got %d", deleteCalls.Load())
	}
	if s.Selected != nil || len(s.Items) != 0 || len(s.Knowledge.Catalog) != 0 {
		t.Fatalf("deleted item remained in project pane: selected=%v items=%d catalog=%d", s.Selected, len(s.Items), len(s.Knowledge.Catalog))
	}
	if q.ServerUnreadCount != 2 || len(q.UnreadPreview) != 0 {
		t.Fatalf("unread state not synced after delete: count=%d preview=%d", q.ServerUnreadCount, len(q.UnreadPreview))
	}
	if a.overviewState.V2TotalKnowledge != 9 || a.overviewState.V2DayBuckets[day] != 1 || a.overviewState.DayTotal != 0 || len(a.overviewState.DayItems) != 0 {
		t.Fatalf("overview state not synced after delete: total=%d buckets=%v dayTotal=%d", a.overviewState.V2TotalKnowledge, a.overviewState.V2DayBuckets, a.overviewState.DayTotal)
	}
	if len(a.recent) != 1 || a.recent[0].ID != "other-doc" {
		t.Fatalf("recent list not synced after delete: %+v", a.recent)
	}
	if a.authEpoch <= authBefore || a.unreadAuthEpoch.Load() != a.authEpoch {
		t.Fatalf("authEpoch did not advance: before=%d after=%d unread=%d", authBefore, a.authEpoch, a.unreadAuthEpoch.Load())
	}
}

func TestCommonDeleteSyncsOverviewHeatmapUnreadAndAuthEpoch(t *testing.T) {
	var deletedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/common/") {
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	commonDoc := Document{
		ID: "cm-1", Dataset: "common-ds", Name: "pref.md", Created: "2026-11-20T10:00:00Z",
		Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"},
	}
	a := New(Config{
		ServiceURL:     server.URL,
		ProjectControl: readingControlConfig(t, server.URL),
		Common:         []Project{{ID: "common", Name: "通用记忆", Dataset: "common-ds"}},
	}, nil)
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "通用知识"
	a.project = 0
	item := Item{ID: commonDoc.ID, Dataset: commonDoc.Dataset, Kind: "知识", Date: "2026-11-20", Document: &commonDoc, BodyLoaded: true}
	s := a.state()
	s.Items = []Item{item}
	s.Selected = &item
	a.overviewState.V2Mode = true
	a.overviewState.V2TotalKnowledge = 5
	a.overviewState.V2DayBuckets = map[string]int{"2026-11-20": 1}
	a.overviewState.DayItems = []Item{item}
	a.overviewState.DayTotal = 1
	q := &ProjectQueueState{UnreadState: UnreadState{ServerUnreadKnown: true, ServerUnreadCount: 1, UnreadPreview: []Item{item}}}
	a.access.Queues = map[string]*ProjectQueueState{"common/common-ds": q}

	authBefore := a.authEpoch
	a.deleteCommonKnowledge(commonDoc)
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })

	if deletedPath != "/api/common/cm-1" {
		t.Fatalf("expected gateway DELETE /api/common/cm-1, got %q", deletedPath)
	}
	if a.overviewState.V2TotalKnowledge != 4 || len(a.overviewState.V2DayBuckets) != 0 || a.overviewState.DayTotal != 0 {
		t.Fatalf("overview stats not synced after common delete: total=%d buckets=%v dayTotal=%d",
			a.overviewState.V2TotalKnowledge, a.overviewState.V2DayBuckets, a.overviewState.DayTotal)
	}
	if q.ServerUnreadCount != 0 || len(q.UnreadPreview) != 0 {
		t.Fatalf("unread state not synced after common delete: count=%d preview=%d", q.ServerUnreadCount, len(q.UnreadPreview))
	}
	if a.authEpoch <= authBefore || a.unreadAuthEpoch.Load() != a.authEpoch {
		t.Fatalf("authEpoch did not advance on common delete: before=%d after=%d", authBefore, a.authEpoch)
	}
}

func TestReadingInvalidationClosesAllThreeLeaks(t *testing.T) {
	a := New(Config{}, nil)
	a.Update = func(func()) {}

	// Leak 1: commonEdit.Delete confirmation and new draft (Existing == nil)
	a.commonEdit = commonEditorState{
		Open:            true,
		Target:          Project{Dataset: "ds-1"},
		Draft:           CommonDraft{Title: "New Draft", Body: "Draft Body", Category: "preference"},
		Delete:          &Document{ID: "d1", Dataset: "ds-1"},
		DeleteTarget:    Project{Dataset: "ds-1"},
		DeleteSubmitted: false,
	}

	// Leak 2: overview single-dataset invalidation clears V2DayBuckets, V2TotalKnowledge, DayTotal, Snapshot.Raw
	a.overviewState.V2Mode = true
	a.overviewState.V2TotalKnowledge = 12
	a.overviewState.V2DayBuckets = map[string]int{"2026-11-20": 4}
	a.overviewState.Snapshot.Raw = 33
	a.overviewState.RawLoaded = true
	a.overviewState.DayItems = []Item{{ID: "k1", Dataset: "ds-1", Kind: "知识"}, {ID: "k2", Dataset: "ds-2", Kind: "知识"}}
	a.overviewState.DayTotal = 2

	// Leak 3: userProfiles metadata/drafts/in-flight cancel AND readingController.recent single-dataset filter
	var profileRefreshCanceled, profileReadCanceled bool
	a.userProfiles = userProfilesState{
		Loaded:        true,
		Loading:       true,
		Reading:       true,
		Selected:      "codex",
		Body:          "# secret",
		BodyKey:       "codex/ds-1/doc-1",
		Open:          true,
		DeleteOpen:    true,
		Draft:         "unsaved draft",
		DraftSource:   "codex",
		Drafts:        map[string]string{"codex": "unsaved draft"},
		Message:       "status",
		Next:          time.Now().Add(time.Hour),
		RefreshCancel: func() { profileRefreshCanceled = true },
		ReadCancel:    func() { profileReadCanceled = true },
		Directory: userProfilesDirectory{
			Prompt: "prompt",
			Profiles: []userProfile{
				{ID: "codex", Name: "Codex", Status: "ready", ContentVersion: "v1", Document: &userProfileDocument{Dataset: "ds-1", ID: "doc-1"}},
				{ID: "other", Name: "Other", Status: "ready", ContentVersion: "v2", Document: &userProfileDocument{Dataset: "ds-2", ID: "doc-2"}},
			},
		},
	}
	a.recent = []Item{
		{ID: "r1", Dataset: "ds-1", Kind: "知识"},
		{ID: "r2", Dataset: "ds-2", Kind: "知识"},
	}
	a.projectUI.DeleteTarget = &Item{ID: "k1", Dataset: "ds-1", Kind: "知识"}
	a.projectUI.DeleteError = "stale error"

	a.InvalidateReading("ds-1")

	// Assert Leak 1 closed
	if a.commonEdit.Delete != nil || a.commonEdit.Open || a.commonEdit.Draft != (CommonDraft{}) {
		t.Fatalf("leak 1 not closed: commonEdit=%+v", a.commonEdit)
	}
	// Assert Leak 2 closed (including P2: Loaded=false so other projects don't show fake "0 memories")
	if a.overviewState.V2DayBuckets != nil || a.overviewState.V2TotalKnowledge != 0 ||
		a.overviewState.Snapshot.Raw != 0 || a.overviewState.RawLoaded || a.overviewState.Loaded || a.overviewState.DayTotal != 1 {
		t.Fatalf("leak 2 not closed: buckets=%v total=%d raw=%d rawLoaded=%v loaded=%v dayTotal=%d",
			a.overviewState.V2DayBuckets, a.overviewState.V2TotalKnowledge, a.overviewState.Snapshot.Raw, a.overviewState.RawLoaded, a.overviewState.Loaded, a.overviewState.DayTotal)
	}
	// Assert Leak 3 closed
	if !profileRefreshCanceled || !profileReadCanceled || a.userProfiles.Loading || a.userProfiles.Reading ||
		a.userProfiles.Loaded || a.userProfiles.Open || a.userProfiles.DeleteOpen ||
		a.userProfiles.Draft != "" || a.userProfiles.Drafts != nil || a.userProfiles.Selected != "" ||
		a.userProfiles.Directory.Profiles[0].Document != nil || a.userProfiles.Directory.Profiles[0].ContentVersion != "" ||
		a.userProfiles.Directory.Profiles[1].Document == nil {
		t.Fatalf("leak 3 userProfiles not closed: refreshCanceled=%v readCanceled=%v loaded=%v open=%v deleteOpen=%v draft=%q selected=%q",
			profileRefreshCanceled, profileReadCanceled, a.userProfiles.Loaded, a.userProfiles.Open, a.userProfiles.DeleteOpen, a.userProfiles.Draft, a.userProfiles.Selected)
	}
	if len(a.recent) != 1 || a.recent[0].Dataset != "ds-2" {
		t.Fatalf("leak 3 recent not filtered for ds-1: %+v", a.recent)
	}
	if a.projectUI.DeleteTarget != nil || a.projectUI.DeleteError != "" {
		t.Fatalf("project delete dialog state not cleared on invalidation: %+v", a.projectUI)
	}
}

func TestDeleteSyncFencesOtherLoadingPaneAndAvoidsUnreadDoubleDecrement(t *testing.T) {
	// R1-04 + R1-05: Deleting on project A must reset loading on project B (never stuck)
	// and must compute unread status at completion time (not double-decrement if already marked read).
	a := projectUIFixture()
	a.Update = func(func()) {}
	paneA := a.state()
	deletedItem := paneA.Items[0]

	// Switch user to project B which is currently Loading=true with an in-flight cancel func.
	a.project = 1
	paneB := a.state()
	paneB.Loading = true
	paneBCanceled := false
	paneB.cancel = func() { paneBCanceled = true }

	// Mark deletedItem as already read before delete completes (simulating concurrent mark-read).
	qA := &ProjectQueueState{
		UnreadState: UnreadState{
			ServerUnreadKnown: true,
			ServerUnreadCount: 2,
			ReadIDs:           map[string]bool{deletedItem.ID: true},
		},
	}
	a.access.Queues = map[string]*ProjectQueueState{"p/ds": qA}

	// Also seed in-flight overview bootstrap & activity loads to verify R1-04 overview reset.
	a.overviewState.Loading = true
	a.overviewState.ActivityLoading = true
	overviewBootstrapCanceled := false
	overviewActivityCanceled := false
	a.overviewState.BootstrapCancel = func() { overviewBootstrapCanceled = true }
	a.overviewState.ActivityCancel = func() { overviewActivityCanceled = true }
	genBefore := a.overviewState.Generation
	actRunBefore := a.overviewState.ActivityRun

	a.applyKnowledgeDocumentDeleted(deletedItem, true)

	if !paneBCanceled {
		t.Fatal("R1-04: in-flight load on visible pane B was not canceled")
	}
	if !overviewBootstrapCanceled || !overviewActivityCanceled ||
		a.overviewState.Generation <= genBefore+1 || a.overviewState.ActivityRun <= actRunBefore {
		t.Fatalf("R1-04: overview in-flight loading not reset and restarted on authEpoch++: bootCancel=%v actCancel=%v gen=%d/%d actRun=%d/%d",
			overviewBootstrapCanceled, overviewActivityCanceled,
			a.overviewState.Generation, genBefore, a.overviewState.ActivityRun, actRunBefore)
	}
	if qA.ServerUnreadCount != 2 {
		t.Fatalf("R1-05: already-read item double-decremented unread count to %d", qA.ServerUnreadCount)
	}
}

func TestProjectDelete403ShowsErrorAfterInvalidation(t *testing.T) {
	// R1-08: 403 on project delete must both invalidate reading AND show error on the page.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "forbidden", "message": "无权删除此记忆"})
	}))
	defer server.Close()

	a := projectUIFixture()
	a.Config.ProjectControl = readingControlConfig(t, server.URL)
	a.Client.Config = a.Config
	a.readingIdentity, _ = readingIdentity(a.Client)
	a.syncReadingIdentity()
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	s := a.state()
	s.Knowledge.Detail = true
	target := *s.Selected
	a.projectUI.DeleteTarget = &target

	a.deleteProjectKnowledge(s, target)
	drainReading(t, updates, func() bool { return !a.projectUI.DeleteBusy })

	if s.Selected != nil {
		t.Fatal("403 should have withdrawn selected item")
	}
	if !strings.Contains(s.Error, "删除失败") {
		t.Fatalf("R1-08: expected visible page error after 403 invalidation, got %q", s.Error)
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("删除失败") {
		t.Fatalf("R1-08: error message not rendered in empty state after 403: %v", tt.Texts())
	}
}

func TestCredentialRotationAbortsConfirmAndDeletesWithoutPanic(t *testing.T) {
	// R2-02 + R2-03: After credential/endpoint rotation:
	// 1. confirmCommonDeletion must NOT panic when syncReadingIdentity clears e.Delete.
	// 2. deleteCommonKnowledge and deleteProjectKnowledge must NOT issue DELETE.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	commonDoc := Document{
		ID: "cm-rot", Dataset: "common-ds", Name: "pref.md",
		Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"},
	}
	a := New(Config{
		ServiceURL:     server.URL,
		ProjectControl: readingControlConfig(t, server.URL),
		Projects:       []Project{{ID: "p", Name: "memlane", Dataset: "ds"}},
		Common:         []Project{{ID: "common", Name: "通用记忆", Dataset: "common-ds"}},
	}, nil)
	a.Update = func(func()) {}
	a.syncReadingIdentity()

	// Case 1: confirmCommonDeletion after rotation must not panic and must not send requests.
	a.commonEdit.DeleteSubmitted = true
	a.commonEdit.Delete = &commonDoc
	a.commonEdit.DeleteTarget = Project{Dataset: "common-ds"}
	a.Client.Config.ProjectControl.URL = "http://127.0.0.1:19991" // rotate endpoint/identity
	a.confirmCommonDeletion()
	if a.commonEdit.Busy || a.commonEdit.Delete != nil || requests.Load() != 0 {
		t.Fatalf("R2-02: confirmCommonDeletion did not abort safely on rotation: busy=%v del=%v reqs=%d", a.commonEdit.Busy, a.commonEdit.Delete, requests.Load())
	}

	// Case 2: deleteCommonKnowledge after rotation must not send DELETE.
	a.page = "通用知识"
	a.Client.Config.ProjectControl.URL = "http://127.0.0.1:19992"
	a.deleteCommonKnowledge(commonDoc)
	if a.commonEdit.Busy || requests.Load() != 0 {
		t.Fatalf("R2-03: deleteCommonKnowledge sent DELETE after rotation: busy=%v reqs=%d", a.commonEdit.Busy, requests.Load())
	}

	// Case 3: deleteProjectKnowledge after rotation must not send DELETE.
	a.page, a.kind = "知识", "知识"
	projDoc := Document{ID: "k-rot", Dataset: "ds", Name: "note.md"}
	target := Item{ID: "k-rot", Dataset: "ds", Kind: "知识", Document: &projDoc}
	a.projectUI.DeleteTarget = &target
	a.Client.Config.ProjectControl.URL = "http://127.0.0.1:19993"
	a.deleteProjectKnowledge(a.state(), target)
	if a.projectUI.DeleteBusy || a.projectUI.DeleteTarget != nil || requests.Load() != 0 {
		t.Fatalf("R2-03: deleteProjectKnowledge sent DELETE after rotation: busy=%v target=%v reqs=%d", a.projectUI.DeleteBusy, a.projectUI.DeleteTarget, requests.Load())
	}
}

func TestCommonDeleteAuthEpochFenceAndReconcileNoDoubleDecrement(t *testing.T) {
	// R1-06 + R1-09:
	// 1. Stale delete callback after authEpoch change must not mutate overview stats.
	// 2. Reconciliation (allowOptimisticStats=false) must not double-decrement V2TotalKnowledge.
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			close(started)
			<-release
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	commonDoc := Document{
		ID: "cm-fence", Dataset: "common-ds", Name: "pref.md", Created: "2026-11-20T10:00:00Z",
		Metadata: map[string]any{"memlane_kind": "common_knowledge", "category": "preference"},
	}
	a := New(Config{
		ServiceURL:     server.URL,
		ProjectControl: readingControlConfig(t, server.URL),
		Common:         []Project{{ID: "common", Name: "通用记忆", Dataset: "common-ds"}},
	}, nil)
	updates := make(chan func(), 16)
	a.Update = func(fn func()) { updates <- fn }
	a.page = "通用知识"
	a.overviewState.V2Mode = true
	a.overviewState.V2TotalKnowledge = 5

	a.deleteCommonKnowledge(commonDoc)
	<-started
	// Revoke / advance authEpoch while delete is in flight.
	a.authEpoch++
	close(release)
	drainReading(t, updates, func() bool { return !a.commonEdit.Busy })

	if a.overviewState.V2TotalKnowledge != 5 {
		t.Fatalf("R1-06: stale delete callback mutated overview total to %d", a.overviewState.V2TotalKnowledge)
	}

	// R1-09: reconciliation path (allowOptimisticStats=false) must not decrement V2TotalKnowledge.
	a.applyKnowledgeDocumentDeleted(Item{ID: commonDoc.ID, Dataset: commonDoc.Dataset, Kind: "知识", Date: "2026-11-20"}, false)
	if a.overviewState.V2TotalKnowledge != 5 {
		t.Fatalf("R1-09: reconciliation double-decremented overview total to %d", a.overviewState.V2TotalKnowledge)
	}
}

func TestSubmittedCommonDraftVerifiesBySHA256AfterWithdrawal(t *testing.T) {
	// R1-07: After withdrawal clears Draft plaintext, confirmGatewayCommon verifies via ExpectedSHA256.
	draft := CommonDraft{Title: "Saved Title", Body: "Saved Body", Category: "preference"}
	formatted, err := draft.FormatDocument()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/common/doc-sha" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"dataset_id":      "common-ds",
				"doc_id":          "doc-sha",
				"document_id":     "doc-sha",
				"content":         formatted,
				"pipeline_status": "completed",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	receipt := CommonWriteReceipt{
		Gateway:          true,
		DatasetID:        "common-ds",
		DataID:           "doc-sha",
		ExpectedSHA256:   sha256SumTrimmed(formatted),
		HasExpectedSHA:   true,
		ExpectedCategory: "preference",
	}
	// Pass an empty Draft (as after withdrawCommonEditor).
	doc, msg, err := confirmGatewayCommon(t.Context(), client, Project{Dataset: "common-ds"}, receipt, CommonDraft{})
	if err != nil || doc.ID != "doc-sha" || !strings.Contains(msg, "已核对") {
		t.Fatalf("R1-07: expected SHA-256 verification to succeed after draft withdrawal: doc=%+v msg=%q err=%v", doc, msg, err)
	}
}

func sha256SumTrimmed(s string) [32]byte {
	return reading.BaselineSHA256(strings.TrimSuffix(s, "\n"))
}

