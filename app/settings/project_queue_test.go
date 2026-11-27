package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
)

func TestProjectQueueUsesOfficialPendingDocumentsAndPreservesSummaryErrors(t *testing.T) {
	var completed, missing bool
	server := newTestHTTP(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: ReadingContract})
		case "/v1/projects/p/message-summary":
			if r.Method != http.MethodGet {
				t.Error("queue enumeration wrote data")
			}
			resp := ProjectMessageSummary{ProjectID: "p", DatasetID: "ds", Activity: []activityRow{{Outcome: "succeeded"}}}
			if missing {
				resp.Errors = map[string]string{"documents": "文档列表已变化，请重新读取"}
			} else if !completed {
				resp.PendingDocumentsCount = 1
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Error("unexpected queue request", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	a := newTestHost(Config{ServiceURL: server.URL}, nil)
	a.client.HTTP = server.client
	in := projectQueueFetchInput{client: a.client, project: AccessProject{ID: "p", Dataset: "ds"}, control: readingControlConfig(t, server.URL)}
	fetch := func() projectQueueSnapshot {
		var snap projectQueueSnapshot
		a.fetchProjectQueueSummary(context.Background(), in, &snap)
		return snap
	}
	snap := fetch()
	if snap.documentErr != nil || snap.pendingDocumentsCount != 1 {
		t.Fatalf("official pending document was lost: %+v", snap)
	}
	completed = true
	snap = fetch()
	if snap.documentErr != nil || snap.pendingDocumentsCount != 0 {
		t.Fatal("completed document kept pending", snap)
	}
	missing = true
	if snap = fetch(); snap.documentErr == nil {
		t.Fatal("missing metadata faked successful empty list")
	}
	in.control = nil
	if snap = fetch(); snap.documentErr == nil || snap.activityErr == nil {
		t.Fatal("queue did not report the unavailable control plane", snap)
	}
}

func projectQueueFixture() (*testHost, *ProjectQueueState) {
	a := settingsFixture()
	a.page = "设置"
	a.access.OpenProject = "p"
	p := a.access.Directory.Projects[0]
	q := a.projectQueue(p)
	q.BacklogLoaded = true
	q.Loaded, q.InboxPending, q.InboxFailed = true, 2, 1
	return a, q
}

func TestProjectQueueActionsStayCloseToTheirText(t *testing.T) {
	for _, width := range []int{1200, 1512, 2400} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, _ := projectQueueFixture()
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, a.access.Directory.Projects[0]) })
			}, width, 1200)
			details, ok := tt.Find("一键重试")
			if !ok || details.X+details.W > 900 {
				t.Fatal("retry drifted outside bounded V4 container", details)
			}
			if tt.HasText("归档此异常") || tt.HasText("继续采集") {
				t.Fatal("ambiguous recovery controls remain")
			}
			fixtureImage(t, tt, fmt.Sprintf("message-status-%d", width))
		})
	}
}

func TestProjectQueueBothTypesAndCompactPermissionFeedback(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a, q := projectQueueFixture()
			tt := ui.NewTester(a.View, wh[0], wh[1])
			for _, label := range []string{"Agent 访问权限", "消息与采集状态", "采集队列有 1 项失败", "有 2 条原始记录等待提炼", "一键重试"} {
				if !tt.HasText(label) {
					t.Fatal("missing queue distinction", label)
				}
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-both", wh[0], wh[1]))
			a.access.CheckboxPending = map[string]*ProjectCheckboxPending{"p/agent": {Project: "p", Agent: "agent", Field: "access", Value: "read", Revision: 4}}
			a.access.Busy = true
			tt.Frame()
			if !tt.HasText("保存中…") || tt.HasText("重新核对") || tt.HasText("修改结果待确认") {
				t.Fatal("normal saving looks like unknown failure")
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-saving", wh[0], wh[1]))
			a.access.Busy = false
			tt.Frame()
			if !tt.HasText("尚未确认保存") || !tt.HasText("重新核对") {
				t.Fatal("unknown save has no compact recovery")
			}
			link, ok := tt.Find("重新核对")
			if !ok || link.W > 120 {
				t.Fatal("recovery expanded into a full-width button")
			}
			a.access.CheckboxPending = nil
			q.InboxPending, q.InboxFailed = 0, 0
			tt.Frame()
			if !tt.HasText("自动采集已开启 · 正常") {
				t.Fatal("successful healthy collapsed row missing")
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-empty", wh[0], wh[1]))
			q.Loaded = false
			q.Loading = true
			q.Next = time.Now().Add(time.Hour)
			tt.Frame()
			if tt.HasText("0 条记录") || tt.HasText("0 份文档") {
				t.Fatal("loading faked zero counts")
			}
		})
	}
}

func TestProjectQueueReconcileActivity(t *testing.T) {
	p := AccessProject{ID: "one", Name: "Project", Dataset: "ds-1"}
	const at = "2026-11-24T08:00:00Z"
	snap := projectQueueSnapshot{activity: []activityRow{
		{ID: "earlier", Run: "extract", Dataset: p.Dataset, Operation: "cognify", Created: at},
		{ID: "finished", Run: "extract", Dataset: p.Dataset, Operation: "cognify", Created: at, Outcome: "succeeded"},
		{ID: "unrelated", Run: "extract", Dataset: "ds-2", Operation: "memify", Created: "2026-11-24T09:00:00Z"},
		{ID: "global", Run: "extract", Operation: "memify", Created: "2026-11-24T10:00:00Z"},
		{ID: "ignored", Dataset: p.Dataset, Operation: "search", Created: at},
	}}
	before := append([]activityRow(nil), snap.activity...)
	rec := reconcileProjectQueueState(p, snap)
	if rec.ActivityError != "" || len(rec.Activity) != 1 || rec.Activity[0].Title != "提炼记忆" ||
		rec.Activity[0].Project != p.Name || rec.Activity[0].Record.ID != "finished" || !slices.Equal(snap.activity, before) {
		t.Fatalf("incorrect activity reconciliation: %+v", rec.Activity)
	}
}

func TestProjectQueueApplyReadErrorsAndRecovery(t *testing.T) {
	a := newTestHost(Config{}, nil)
	p := AccessProject{ID: "one", Dataset: "ds-1"}
	backlog := &BacklogJob{ProcessingFailed: 1}
	q := &ProjectQueueState{Loading: true, Backlog: backlog, BacklogLoaded: true}
	meta := projectQueueApplyMeta{baseIdentity: a.cachedBaseReadingIdentity()}
	snap := projectQueueSnapshot{v2SummaryOK: true, summaryBacklogErr: "unavailable", documentErr: fmt.Errorf("failed")}
	rec := reconcileProjectQueueState(p, snap)
	if rec.UpdateBacklog {
		t.Fatalf("partial failures made the backlog eligible for replacement: %+v", rec)
	}
	a.applyProjectQueueResult(q, p, meta, snap, rec)
	if q.Loading || !q.Loaded || q.BacklogError == "" || q.DocumentError == "" || q.Backlog != backlog || !q.BacklogLoaded {
		t.Fatal("failed read discarded an existing queue snapshot")
	}
	snap.documentErr = nil
	snap.summaryBacklogErr = ""
	a.applyProjectQueueResult(q, p, meta, snap, reconcileProjectQueueState(p, snap))
	if q.BacklogError != "" || q.DocumentError != "" || q.Backlog != nil {
		t.Fatal("successful read did not replace queue snapshots and clear errors")
	}
}

func TestProjectQueueApplyAuthorizationFences(t *testing.T) {
	p := AccessProject{ID: "one", Dataset: "ds-1"}
	q := &ProjectQueueState{Request: 7, Loading: true, Activity: []overviewEvent{{Key: "existing"}}, Documents: []Item{{ID: "private"}}}
	other := &ProjectQueueState{Documents: []Item{{ID: "other"}}}
	a := newTestHost(Config{}, nil)
	a.authVersion = 3
	a.access.Queues = map[string]*ProjectQueueState{"one/ds-1": q, "two/ds-2": other}
	snap := projectQueueSnapshot{authDenied: true, documentErr: &ControlResponseError{Status: 403, Code: "document_path_denied"}}
	rec := reconcileProjectQueueState(p, snap)
	a.applyProjectQueueResult(q, p, projectQueueApplyMeta{request: 6}, snap, rec)
	if !q.Loading || a.authVersion != 3 || len(q.Documents) != 1 {
		t.Fatal("stale request crossed the request fence")
	}
	// A current request's authorization denial must win over any stale epoch.
	a.applyProjectQueueResult(q, p, projectQueueApplyMeta{request: 7, authEpoch: 2}, snap, rec)
	if a.authVersion != 4 || q.Loading || q.Request != 8 || q.Documents != nil || q.Activity != nil ||
		q.DocumentError != "内容路径受限，无法读取" || len(other.Documents) != 1 {
		t.Fatal("authorization denial failed to withdraw only the affected dataset")
	}
}

func TestProjectInboxShowsFailureAndRetriesThroughGateway(t *testing.T) {
	a, q := projectQueueFixture()
	p := a.access.Directory.Projects[0]
	q.InboxPending, q.InboxFailed = 0, 0
	q.BacklogNext = time.Now().Add(time.Hour)
	const (
		token  = "0123456789012345678901234567890123456789"
		reason = "提炼服务连接超时"
	)
	retryCalls := make(chan struct{}, 4)
	identity, err := readingIdentity(a.client)
	if err != nil {
		t.Fatal(err)
	}
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			fmt.Fprint(w, `{"reading_contract":"catalog-cursor-v1","features":{"async_capture":true}}`)
		case "/api/reading-state":
			fmt.Fprint(w, `{"projects":[{"project_id":"p","dataset_id":"ds","unread_count":2,"state_rev":1}]}`)
		case "/api/projects/p":
			if r.Method != http.MethodGet {
				t.Errorf("project read used %s", r.Method)
			}
			if len(retryCalls) < 2 {
				fmt.Fprintf(w, `{"id":"p","dataset_id":"ds","inbox_pending":3,"inbox_failed":2,"last_error":%q}`, reason)
			} else {
				fmt.Fprint(w, `{"id":"p","dataset_id":"ds","inbox_pending":5,"inbox_failed":0,"last_error":""}`)
			}
		case "/api/projects/p/retry":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+token ||
				r.Header.Get("X-Memlane-Management") != token || r.Header.Get("X-Memlane-Reading-Identity") != identity {
				t.Error("retry did not send the expected method and identity")
			}
			retryCalls <- struct{}{}
			if len(retryCalls) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":"engine_unavailable","message":"采集服务暂不可用"}`)
				return
			}
			fmt.Fprint(w, `{"project_id":"p","retried_count":2,"item_ids":["first","second"]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	a.config.ProjectControl = readingControlConfig(t, "http://127.0.0.1:16529")
	a.client.HTTP = &http.Client{Transport: transport}
	updates := make(chan func(), 10)
	a.dispatch = func(fn func()) { updates <- fn }

	a.LoadProjectQueue(q, p)
	drainReading(t, updates, func() bool { return !q.Loading })
	if q.InboxPending != 3 || q.InboxFailed != 2 || q.LastError != reason || q.DocumentError != "" || !q.ServerUnreadKnown || q.ServerUnreadCount != 2 {
		t.Fatalf("queue did not load the gateway failure: pending=%d failed=%d reason=%q", q.InboxPending, q.InboxFailed, q.LastError)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.projectQueueSection(c, p) }, 1200, 820)
	for _, label := range []string{"采集队列有 2 项失败", reason, "一键重试", "自动采集"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q: %v", label, tt.Texts())
		}
	}
	clickProjectUI(t, tt, "一键重试")
	if !q.InboxRetryBusy {
		t.Fatal("retry did not enter busy state")
	}
	a.RetryProjectInbox(q, p)
	drainReading(t, updates, func() bool { return !q.InboxRetryBusy })
	tt.Frame()
	if len(retryCalls) != 1 || !tt.HasText(reason) || !strings.Contains(q.InboxRetryError, "采集服务暂不可用") || !tt.HasText(q.InboxRetryError) {
		t.Fatalf("failed retry did not retain the original reason and show the retry error: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "一键重试")
	if !q.InboxRetryBusy || q.InboxRetryError != "" {
		t.Fatal("another retry did not clear the previous retry error")
	}
	drainReading(t, updates, func() bool { return !q.InboxRetryBusy && !q.Loading })
	tt.Frame()
	if len(retryCalls) != 2 || q.InboxPending != 5 || q.InboxFailed != 0 || q.LastError != "" ||
		q.InboxRetryError != "" || tt.HasText(reason) || tt.HasText("一键重试") {
		t.Fatalf("successful retry did not refresh the failure row: pending=%d failed=%d texts=%v", q.InboxPending, q.InboxFailed, tt.Texts())
	}
}

func TestProjectQueueActiveInboxTightensNextIntervalAndVisibleStatusRefreshesOpenProject(t *testing.T) {
	a := newTestHost(Config{}, nil)
	p := AccessProject{ID: "p-active", Name: "Active", Dataset: "ds-active"}
	a.access.Loaded = true
	a.access.Next = time.Now().Add(time.Hour)
	a.access.Directory.Projects = []AccessProject{
		{ID: "p-0", Dataset: "ds-0"},
		{ID: "p-1", Dataset: "ds-1"},
		{ID: "p-2", Dataset: "ds-2"},
		{ID: "p-3", Dataset: "ds-3"},
		p,
	}
	a.access.OpenProject = p.ID
	a.client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	updates := make(chan func(), 16)
	a.dispatch = func(fn func()) { updates <- fn }

	// Idle queue gets 30s Next; transitioning 0 -> >0 in applyProjectQueueResult tightens Next to <= 5s.
	q := a.ProjectQueue(p)
	q.Loaded = true
	q.Next = time.Now().Add(30 * time.Second)
	q.Loading = true
	q.Request = 1
	meta := projectQueueApplyMeta{request: 1, baseIdentity: a.cachedBaseReadingIdentity()}
	snap := projectQueueSnapshot{gateway: true, inbox: &api.ProjectView{ID: p.ID, InboxPending: 2}}
	a.applyProjectQueueResult(q, p, meta, snap, reconcileProjectQueueState(p, snap))
	if q.InboxPending != 2 {
		t.Fatalf("expected InboxPending=2, got %d", q.InboxPending)
	}
	if until := time.Until(q.Next); until <= 0 || until > projectQueueActiveInterval+time.Second {
		t.Fatalf("applyProjectQueueResult did not tighten Next for pending inbox: %v", until)
	}

	// Mark the first 4 projects fresh; only the open project is due.
	for i := 0; i < 4; i++ {
		other := a.ProjectQueue(a.access.Directory.Projects[i])
		other.Loaded = true
		other.Next = time.Now().Add(time.Hour)
	}
	q.Next = time.Now().Add(-time.Second)
	a.RefreshVisibleStatus(time.Now())
	if !q.Loading {
		t.Fatal("RefreshVisibleStatus did not refresh the due currently-open project queue")
	}
	if until := time.Until(q.Next); until <= 0 || until > projectQueueActiveInterval+time.Second {
		t.Fatalf("LoadProjectQueue with InboxPending>0 did not use 5s interval: %v", until)
	}
	drainReading(t, updates, func() bool { return !q.Loading })
}

func TestProjectQueueGatewayUnreadPreviewAndSummaryDateNormalization(t *testing.T) {
	createdTS := "2026-11-21T12:30:00Z"
	parsed, err := time.Parse(time.RFC3339, createdTS)
	if err != nil {
		t.Fatal(err)
	}
	wantLocalDay := parsed.In(time.Local).Format("2006-01-02")

	var sawKnowledgeQuery string
	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			fmt.Fprint(w, `{"reading_contract":"catalog-cursor-v1","features":{"async_capture":true}}`)
		case "/api/reading-state":
			fmt.Fprint(w, `{"projects":[{"project_id":"p","dataset_id":"ds","unread_count":12,"state_rev":7}]}`)
		case "/api/knowledge":
			sawKnowledgeQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(api.KnowledgePage{
				ProjectID:      "p",
				DatasetID:      "ds",
				UnreadCount:    12,
				UnreadStateRev: 7,
				Items: []api.CatalogItem{
					{DocID: "k-1", DatasetID: "ds", Kind: "knowledge", Title: "可读标题", EffectiveDate: "2026-11-20", IsUnread: true},
					{DocumentID: "k-2", DatasetID: "ds", Kind: "direct_memory", Title: "text_0123456789abcdef0123456789abcdef", DocName: "text_0123456789abcdef0123456789abcdef", EffectiveDate: "2026-11-21", IsUnread: true},
					{DocID: "k-3", DatasetID: "ds", Kind: "knowledge", Title: "", DocName: "user_context-0123456789abcdef0123456789abcdef.txt", CreatedAt: createdTS, IsUnread: true},
					{DocID: "foreign", DatasetID: "other-ds", Kind: "knowledge", Title: "不应该出现", EffectiveDate: "2026-11-21", IsUnread: true},
				},
			})
		case "/api/projects/p":
			fmt.Fprint(w, `{"id":"p","dataset_id":"ds","catalog_version":"cv-7"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	a := newTestHost(Config{ProjectControl: readingControlConfig(t, "http://127.0.0.1:16531")}, nil)
	a.client.HTTP = &http.Client{Transport: transport}
	p := AccessProject{ID: "p", Dataset: "ds"}
	in := projectQueueFetchInput{client: a.client, project: p, control: a.config.ProjectControl}
	snap := a.fetchProjectQueueSnapshot(context.Background(), in)
	rec := reconcileProjectQueueState(p, snap)

	if !strings.Contains(sawKnowledgeQuery, "unread_only=true") || !strings.Contains(sawKnowledgeQuery, "limit=10") {
		t.Fatalf("unexpected /api/knowledge query: %q", sawKnowledgeQuery)
	}
	if rec.ServerUnreadCount != 12 || rec.ServerUnreadStateRev != 7 || !rec.UnreadHasMore {
		t.Fatalf("unexpected unread summary: %+v", rec)
	}
	if len(rec.UnreadKnowledge) != 3 {
		t.Fatalf("expected 3 scoped unread preview items, got %d: %+v", len(rec.UnreadKnowledge), rec.UnreadKnowledge)
	}
	if rec.UnreadKnowledge[0].ID != "k-1" || rec.UnreadKnowledge[0].Title != "可读标题" || rec.UnreadKnowledge[0].Date != "2026-11-20" || rec.UnreadKnowledge[0].Kind != "知识" || !rec.UnreadKnowledge[0].ServerUnread {
		t.Fatalf("unexpected first unread item: %+v", rec.UnreadKnowledge[0])
	}
	if rec.UnreadKnowledge[1].ID != "k-2" || rec.UnreadKnowledge[1].Title != "记忆 · 2026-11-21" || rec.UnreadKnowledge[1].Date != "2026-11-21" {
		t.Fatalf("machine name was not replaced with date placeholder: %+v", rec.UnreadKnowledge[1])
	}
	if rec.UnreadKnowledge[2].ID != "k-3" || rec.UnreadKnowledge[2].Date != wantLocalDay || rec.UnreadKnowledge[2].Title != "记忆 · "+wantLocalDay {
		t.Fatalf("created_at fallback did not normalize to YYYY-MM-DD: %+v", rec.UnreadKnowledge[2])
	}

	summary := ProjectMessageSummary{
		ProjectID: "p",
		DatasetID: "ds",
		UnreadPreview: []UnreadPreviewItem{
			{DocID: "s-1", Title: "", EffectiveDate: "", CreatedAt: createdTS},
			{DocID: "s-2", Title: "text_0123456789abcdef0123456789abcdef", EffectiveDate: "2026-11-22"},
		},
	}
	items := summary.unreadPreviewItems("ds")
	if len(items) != 2 {
		t.Fatalf("expected 2 summary preview items, got %d", len(items))
	}
	if items[0].Date != wantLocalDay || items[0].Title != "记忆 · "+wantLocalDay {
		t.Fatalf("summary fallback leaked raw timestamp or dropped date placeholder: %+v", items[0])
	}
	if items[1].Date != "2026-11-22" || items[1].Title != "记忆 · 2026-11-22" {
		t.Fatalf("summary machine title or calendar date placeholder failed: %+v", items[1])
	}
}
