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
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestProjectQueueUsesOfficialPendingDocumentsAndPreservesSummaryErrors(t *testing.T) {
	var completed, missing bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(ControlCapabilities{ReadingContract: readingContract})
		case "/v1/projects/p/message-summary":
			if r.Method != http.MethodGet {
				t.Error("queue enumeration wrote data")
			}
			resp := v2MessageSummaryResponse{ProjectID: "p", DatasetID: "ds", Activity: []activityRow{{Outcome: "succeeded"}}}
			if missing {
				resp.Errors = map[string]string{"documents": "文档列表已变化，请重新读取"}
			} else if !completed {
				resp.PendingDocumentsCount = 1
				resp.PendingDocuments = []projectMessageSummaryDocument{{ID: "raw", Name: "design.md"}}
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Error("unexpected queue request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := New(Config{ServiceURL: server.URL}, nil)
	in := projectQueueFetchInput{client: a.Client, project: AccessProject{ID: "p", Dataset: "ds"}, control: readingControlConfig(t, server.URL), stateDir: t.TempDir()}
	fetch := func() projectQueueSnapshot {
		var snap projectQueueSnapshot
		a.fetchProjectQueueSummary(context.Background(), in, &snap)
		return snap
	}
	snap := fetch()
	if snap.documentErr != nil || len(snap.documents) != 1 || snap.documents[0].ID != "raw" || snap.pendingDocumentsCount != 1 {
		t.Fatalf("official pending document was lost: %+v", snap)
	}
	completed = true
	snap = fetch()
	if snap.documentErr != nil || len(snap.documents) != 0 {
		t.Fatal("completed document kept pending", snap)
	}
	missing = true
	if snap = fetch(); snap.documentErr == nil {
		t.Fatal("missing metadata faked successful empty list")
	}
	in.control = nil
	if snap = fetch(); snap.documentErr == nil || snap.noticeErr == nil || snap.activityErr == nil {
		t.Fatal("queue did not report the unavailable control plane", snap)
	}
}
func projectQueueFixture() (*App, *projectQueueState) {
	a := settingsFixture()
	a.page = "设置"
	a.access.OpenProject = "p"
	a.settingsOrigin = &readingLocation{Page: "知识", ProjectID: "p"}
	p := a.access.Directory.Projects[0]
	q := a.projectQueue(p)
	q.BacklogLoaded = true
	agent := a.access.Directory.Agents[0]
	q.Sessions = []queuedSession{
		{Agent: agent, Binding: ProjectBinding{ProjectID: "p", AgentID: agent.ID, HostSessionKey: "synthetic-first"}, View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 2}}},
		{Agent: agent, Binding: ProjectBinding{ProjectID: "p", AgentID: agent.ID, HostSessionKey: "synthetic-second"}, View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}}},
	}
	q.Checks = map[string]pendingCheckState{agent.ID + "/synthetic-second": {Status: "unconfirmed", Reason: "original_receipt_unavailable"}}
	for n, name := range []string{"项目界面设计与交互规范.md", "本地服务启动与故障排查.md"} {
		d := Document{ID: fmt.Sprint(n), Dataset: "ds", Name: name, Created: "2026-11-13T16:56:00Z"}
		q.Documents = append(q.Documents, Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: name, Date: d.Created, Document: &d})
	}
	return a, q
}

func TestQueuedDocumentReaderDoesNotEnumerateAnotherCatalogAndRestoresScroll(t *testing.T) {
	raw := "Session ID: synthetic-session\nQuestion: 如何启动本地服务？\nAnswer: 使用项目开发启动器。"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/raw") {
			t.Errorf("queue reader requested an unrelated catalog or mutation: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, raw)
	}))
	defer server.Close()
	a, _ := projectQueueFixture()
	a.Config.ServiceURL = server.URL
	a.Client = Client{Config: a.Config}
	a.readingIdentity, _ = readingIdentity(a.Client)
	updates := make(chan func(), 10)
	a.Update = nil
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Loaded, q.Next, q.BacklogNext = true, time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	d := Document{ID: "source", Dataset: "ds", Name: "text_0123456789abcdef0123456789abcdef"}
	item := Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: d.Name, Document: &d}
	q.Documents = []Item{item}
	for n := 0; n < 12; n++ {
		q.Sessions = append(q.Sessions, queuedSession{Agent: AccessAgent{Name: fmt.Sprint("Agent ", n)}, View: &pendingBufferView{}})
	}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.projectSettingsPage(c) }) }, 1200, 820)
	tt.Scroll(1000, 700, 0, 10000)
	before := q.Scroll.Y
	if before == 0 {
		t.Fatal("fixture did not reach a scrolled queue")
	}
	a.Update = func(fn func()) { updates <- fn }
	clickProjectUI(t, tt, "查看")
	reader := q.Reader
	drainReading(t, updates, func() bool { return !reader.Reading })
	a.Update = nil
	tt.Frame()
	if a.page != "设置" || reader.Error != "" || reader.Selected == nil || !reader.Selected.BodyLoaded || !tt.HasText("如何启动本地服务？") || !tt.HasText("刷新提炼状态") {
		t.Fatal("pending detail lost its body, title, context or next read action")
	}
	if tt.HasText("已载入 0 条") || tt.HasText("选中内容已不在当前官方页面，请重新选择") || tt.HasText("Session ID: synthetic-session") {
		t.Fatal("queue reader leaked catalog errors or technical wrapper")
	}
	fixtureImage(t, tt, "queue-document-detail")
	clickProjectUI(t, tt, "返回待处理内容")
	if q.Reader != nil || q.Scroll.Y != before || a.access.OpenProject != p.ID {
		t.Fatal("return lost project or scroll position", q.Scroll.Y, before)
	}
}

func TestProjectQueueActionsStayCloseToTheirText(t *testing.T) {
	for _, width := range []int{1200, 1512, 2400} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, _ := projectQueueFixture()
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, a.access.Directory.Projects[0]) })
			}, width, 1200)
			details, ok := tt.Find("查看详情")
			if !ok || details.X+details.W > 900 {
				t.Fatal("details drifted outside bounded V4 container", details)
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
			for _, label := range []string{"Agent 访问权限", "消息与采集状态", "需要你处理", "系统异常", "进行中与待提炼", "2 条消息等待采集", "1 条消息未收到采集结果", "2 份文档"} {
				if !tt.HasText(label) {
					t.Fatal("missing queue distinction", label)
				}
			}
			fixtureImage(t, tt, fmt.Sprintf("project-settings-%dx%d-both", wh[0], wh[1]))
			a.access.CheckboxPending = map[string]*projectCheckboxPending{"p/agent": {Project: "p", Agent: "agent", Field: "access", Value: "read", Revision: 4}}
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
			q.Sessions = nil
			q.Documents = nil
			q.AutomaticSave = true
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

func TestInProgressNoticesRenderInProgressBucketLightAndDark(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a, q := projectQueueFixture()
			a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
			a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
			a.initDesktopPreferences()
			a.setAppearancePreference(appearance)
			p := a.access.Directory.Projects[0]
			maintain := "maintain"
			p.Overrides[a.access.Directory.Agents[0].ID] = ProjectOverride{Access: &maintain}
			a.access.Directory.Projects[0] = p

			// Pure in-progress state: 1 session "待采集" + 1 backlog "处理中".
			q.Loaded = true
			q.AutomaticSave = true
			q.Documents = nil
			q.Sessions = q.Sessions[:1]
			q.Backlog = &backlogJob{Status: "watching", ProcessingActive: 1}

			tt := ui.NewTester(a.View, 1200, 820)
			for _, want := range []string{
				"自动采集已开启 · 正常",
				"进行中与待提炼",
				"2 条消息等待采集",
				"待采集",
				"正在提交记忆提炼",
				"处理中",
				"查看详情",
			} {
				if !tt.HasText(want) {
					t.Fatalf("[%s] missing expected progress UI text %q; got %v", appearance, want, tt.Texts())
				}
			}
			for _, unwanted := range []string{"系统异常", "2 项待处理", "1 项待处理", "知道了，不再提醒", "暂不处理"} {
				if tt.HasText(unwanted) {
					t.Fatalf("[%s] unexpected alarming text %q in pure progress state; got %v", appearance, unwanted, tt.Texts())
				}
			}

			// Queue section must stay stably pinned at the top above 项目信息 even without alerts.
			infoRect, infoOK := tt.Find("项目信息")
			queueRect, queueOK := tt.Find("消息与采集状态")
			if !infoOK || !queueOK || queueRect.Y >= infoRect.Y {
				t.Fatalf("[%s] expected 消息与采集状态 pinned above 项目信息; info=%+v(%v) queue=%+v(%v)", appearance, infoRect, infoOK, queueRect, queueOK)
			}

			// Clicking 查看详情 on a progress notice opens the detail dialog without error hint.
			clickProjectUI(t, tt, "查看详情")
			if !q.DetailDialogOpen || !tt.HasText("后台按队列顺序自动处理，切换页面不会中断") {
				t.Fatalf("[%s] failed to open progress detail dialog cleanly; texts=%v", appearance, tt.Texts())
			}
			clickProjectUI(t, tt, "关闭")
			tt.Frame()

			// Mixed state: add 1 real unknown-outcome anomaly + 2 queued documents.
			d1 := Document{ID: "d1", Dataset: "ds", Name: "设计草稿.md", Created: "2026-11-13T16:56:00Z"}
			d2 := Document{ID: "d2", Dataset: "ds", Name: "会议纪要.md", Created: "2026-11-13T16:56:00Z"}
			q.Documents = []Item{
				{ID: d1.ID, Dataset: d1.Dataset, Kind: "文档", Title: d1.Name, Date: d1.Created, Document: &d1},
				{ID: d2.ID, Dataset: d2.Dataset, Kind: "文档", Title: d2.Name, Date: d2.Created, Document: &d2},
			}
			q.Sessions = append(q.Sessions, queuedSession{
				Agent:   a.access.Directory.Agents[0],
				Binding: ProjectBinding{ProjectID: "p", AgentID: a.access.Directory.Agents[0].ID, HostSessionKey: "synthetic-err"},
				View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}},
			})
			tt.Frame()
			for _, want := range []string{"1 项待处理", "系统异常", "1 条消息未收到采集结果", "知道了，不再提醒", "进行中与待提炼", "2 份文档", "2 条消息等待采集", "正在提交记忆提炼"} {
				if !tt.HasText(want) {
					t.Fatalf("[%s] mixed state missing %q; texts=%v", appearance, want, tt.Texts())
				}
			}
			if tt.HasText("3 项待处理") {
				t.Fatalf("[%s] mixed state must not count progress items in 待处理 header: %v", appearance, tt.Texts())
			}

			// Clearing notifications acknowledges only the real anomaly and leaves progress untouched.
			if err := a.clearProjectNotifications(q, p); err != nil {
				t.Fatalf("[%s] clearProjectNotifications failed: %v", appearance, err)
			}
			tt.Frame()
			if tt.HasText("系统异常") || !tt.HasText("已知晓的历史提示（1）") {
				t.Fatalf("[%s] expected only the 1 real anomaly in history; texts=%v", appearance, tt.Texts())
			}
			if !tt.HasText("2 条消息等待采集") || !tt.HasText("正在提交记忆提炼") {
				t.Fatalf("[%s] progress notices should remain visible after clearing notifications; texts=%v", appearance, tt.Texts())
			}

			// Transient read failure recovery via observe() clears any matching ReadError ghost message and keeps section pinned at top.
			q.Sessions[0].View.ReadError = "采集状态暂时无法读取"
			q.Sessions[0].View.Message = "采集状态暂时无法读取"
			q.Sessions[0].View.Loaded = false
			q.Sessions[0].View.observe(PendingMaterials{Pending: 0, Signature: "recovered"})
			q.Backlog = nil
			q.Documents = nil
			q.Sessions = q.Sessions[:1]
			tt.Frame()
			if tt.HasText("上次消息操作结果") || tt.HasText("采集状态暂时无法读取") {
				t.Fatalf("[%s] recovered read failure left ghost receipt in UI; texts=%v", appearance, tt.Texts())
			}
			infoAfter, _ := tt.Find("项目信息")
			queueAfter, _ := tt.Find("消息与采集状态")
			if queueAfter.Y >= infoAfter.Y {
				t.Fatalf("[%s] expected 消息与采集状态 to remain above 项目信息 after alerts clear; queue=%+v info=%+v", appearance, queueAfter, infoAfter)
			}
		})
	}
}

func TestProjectQueueAcknowledgementReadCannotOverwriteNewerDecision(t *testing.T) {
	for _, tc := range []string{"local acknowledgement", "recovered acknowledgement", "project menu write", "failed local mutation"} {
		t.Run(tc, func(t *testing.T) {
			a, q, p := acknowledgementFixture(t)
			a.Config.ProjectControl = nil
			a.Client.HTTP = &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			})}
			updates := make(chan func(), 4)
			a.Update = func(fn func()) { updates <- fn }
			n := messageNotice{Key: "processing-failed"}
			path, err := a.noticeAcknowledgementPath(p)
			if err != nil {
				t.Fatal(err)
			}
			if tc == "recovered acknowledgement" {
				a.acknowledgeNotice(q, p, n, false)
			}
			a.loadProjectQueue(q, p)
			var complete func()
			select {
			case complete = <-updates:
			case <-time.After(2 * time.Second):
				t.Fatal("queue did not finish reading")
			}
			wantAck, wantError := true, false
			switch tc {
			case "local acknowledgement":
				a.acknowledgeNotice(q, p, n, false)
			case "recovered acknowledgement":
				a.clearRecoveredNoticeAcknowledgements(q, p, []string{n.Key})
				wantAck = false
			case "project menu write":
				// The menu writes via the shared path writer without changing the queue epoch.
				rows, message, err := updateNoticeAcknowledgementsAtPath(path, func(rows map[string]noticeAcknowledgement) {
					rows[noticeHash(n.Key)] = noticeAcknowledgement{Fingerprint: q.noticeFingerprint(n.Key), At: time.Now()}
				})
				if err != nil || message != "" {
					t.Fatalf("menu write: %s %v", message, err)
				}
				q.NoticeAcknowledgements = rows
			case "failed local mutation":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				a.acknowledgeNotice(q, p, n, false)
				wantAck, wantError = false, true
			}
			complete()
			if q.Loading || q.noticeAcknowledged(n.Key) != wantAck || (q.NoticeAcknowledgementError != "") != wantError {
				t.Fatalf("queue callback replaced a newer decision: acks=%v error=%q loaded=%t", q.NoticeAcknowledgements, q.NoticeAcknowledgementError, q.Loaded)
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
	a := &App{}
	p := AccessProject{ID: "one", Dataset: "ds-1"}
	backlog := &backlogJob{ProcessingFailed: 1}
	check := pendingCheckState{Status: "confirmed"}
	q := &projectQueueState{
		Loading: true, Backlog: backlog, BacklogLoaded: true, AutomaticSave: true,
		ProcessingIssues: []processingIssue{{Reason: "existing"}}, BufferReviews: []bufferReview{{}},
		Checks:            map[string]pendingCheckState{"existing": check},
		ProcessingNotices: []processingNotice{{Code: "existing"}},
	}
	meta := projectQueueApplyMeta{baseIdentity: a.cachedBaseReadingIdentity()}
	snap := projectQueueSnapshot{
		v2SummaryOK: true, summaryBacklogErr: "unavailable",
		v2Summary: v2MessageSummaryResponse{BufferReviews: []bufferReview{}, Checks: []pendingCheckState{}},
		noticeErr: fmt.Errorf("failed"), notices: []processingNotice{{Code: "fresh"}},
	}
	rec := reconcileProjectQueueState(p, snap)
	if rec.UpdateNotices || rec.UpdateBacklog {
		t.Fatalf("partial failures made snapshots eligible for replacement: %+v", rec)
	}
	a.applyProjectQueueResult(q, p, meta, snap, rec)
	if q.Loading || !q.Loaded || q.ProcessingReadError != "记忆提炼异常记录暂时无法读取" ||
		len(q.ProcessingNotices) != 1 || q.ProcessingNotices[0].Code != "existing" ||
		q.BacklogError != "后台采集状态暂时无法读取；任务不会因此重复启动" || q.Backlog != backlog ||
		!q.BacklogLoaded || !q.AutomaticSave || len(q.BufferReviews) != 1 || q.Checks["existing"] != check ||
		len(q.ProcessingIssues) != 1 || q.ProcessingIssues[0].Reason != "existing" {
		t.Fatal("failed read discarded an existing queue snapshot")
	}
	recovered := reconcileRecoveredNoticeKeys(p, q, snap.v2SummaryOK, snap.summaryBacklogErr)
	if slices.Contains(recovered, "processing-read") || slices.Contains(recovered, "processing-failed") {
		t.Fatalf("failed reads were treated as recovered: %v", recovered)
	}

	snap.noticeErr = nil
	snap.summaryBacklogErr = ""
	a.applyProjectQueueResult(q, p, meta, snap, reconcileProjectQueueState(p, snap))
	if q.ProcessingReadError != "" || !slices.Equal(q.ProcessingNotices, snap.notices) ||
		q.BacklogError != "" || q.Backlog != nil || q.ProcessingIssues != nil || q.AutomaticSave ||
		len(q.BufferReviews) != 0 || len(q.Checks) != 0 {
		t.Fatal("successful read did not replace queue snapshots and clear errors")
	}
	recovered = reconcileRecoveredNoticeKeys(p, q, snap.v2SummaryOK, snap.summaryBacklogErr)
	if !slices.Contains(recovered, "processing-read") || !slices.Contains(recovered, "processing-failed") {
		t.Fatalf("successful reads did not recover notices: %v", recovered)
	}
}

func TestProjectQueueApplyAcknowledgementFences(t *testing.T) {
	a := &App{}
	p := AccessProject{}
	path := filepath.Join(t.TempDir(), "acks.json")
	key := noticeHash("unresolved")
	newer, older := noticeAcknowledgement{Fingerprint: noticeHash("new"), At: time.Now()},
		noticeAcknowledgement{Fingerprint: noticeHash("old"), At: time.Now()}
	if _, msg, err := updateNoticeAcknowledgementsAtPath(path, func(rows map[string]noticeAcknowledgement) { rows[key] = newer }); msg != "" || err != nil {
		t.Fatalf("could not seed a newer acknowledgement: %s %v", msg, err)
	}
	_, _, seq := readNoticeAcknowledgementsWithSeq(path)
	for _, tc := range []struct {
		name        string
		epoch, seq  uint64
		want        noticeAcknowledgement
		wantMessage string
	}{
		{"stale epoch", 1, seq, newer, "newer decision"},
		{"stale sequence", 2, seq - 1, newer, "newer decision"},
		{"current", 2, seq, older, "read result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &projectQueueState{noticeAckEpoch: 2, NoticeAcknowledgements: map[string]noticeAcknowledgement{key: newer}, NoticeAcknowledgementError: "newer decision"}
			meta := projectQueueApplyMeta{baseIdentity: a.cachedBaseReadingIdentity(), ackEpoch: tc.epoch}
			snap := projectQueueSnapshot{ackPath: path, noticeAckSeq: tc.seq,
				noticeAcks: map[string]noticeAcknowledgement{key: older}, noticeAckErr: "read result"}
			a.applyProjectQueueResult(q, p, meta, snap, reconcileProjectQueueState(p, snap))
			if q.NoticeAcknowledgements[key] != tc.want || q.NoticeAcknowledgementError != tc.wantMessage {
				t.Fatal("wrong acknowledgement snapshot was applied")
			}
		})
	}
}

func TestProjectQueueApplyAuthorizationFences(t *testing.T) {
	p := AccessProject{ID: "one", Dataset: "ds-1"}
	q := &projectQueueState{Request: 7, Loading: true, Activity: []overviewEvent{{Key: "existing"}}, Documents: []Item{{ID: "private"}}}
	other := &projectQueueState{Documents: []Item{{ID: "other"}}}
	a := &App{authEpoch: 3}
	a.access.Queues = map[string]*projectQueueState{"one/ds-1": q, "two/ds-2": other}
	snap := projectQueueSnapshot{authDenied: true, documentErr: &ControlResponseError{Status: 403, Code: "document_path_denied"}}
	rec := reconcileProjectQueueState(p, snap)
	a.applyProjectQueueResult(q, p, projectQueueApplyMeta{request: 6}, snap, rec)
	if !q.Loading || a.authEpoch != 3 || len(q.Documents) != 1 {
		t.Fatal("stale request crossed the request fence")
	}
	// A current request's authorization denial must win over any stale epoch.
	a.applyProjectQueueResult(q, p, projectQueueApplyMeta{request: 7, authEpoch: 2}, snap, rec)
	if a.authEpoch != 4 || q.Loading || q.Request != 8 || q.Documents != nil || q.Activity != nil ||
		q.DocumentError != "内容路径受限，无法读取" || len(other.Documents) != 1 {
		t.Fatal("authorization denial failed to withdraw only the affected dataset")
	}
}
