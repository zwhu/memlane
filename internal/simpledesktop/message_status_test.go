package simpledesktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
	"time"
)

func TestMessageStatusClassificationUsesObservedFacts(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		access       string
		user         bool
	}{
		{"permission denied", "", "read", true},
		{"waiting with explicit save action", "", "maintain", true},
		{"permission unconfirmed", "policy_unconfirmed", "maintain", false},
		{"service interrupted", "owner_interrupted", "maintain", false},
		{"connection changed", "backlog_installation_changed", "maintain", false},
		{"user paused", "user_stopped", "maintain", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, q := projectQueueFixture()
			q.Sessions = q.Sessions[:1]
			q.Documents = nil
			p := a.access.Directory.Projects[0]
			p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &tc.access}
			if tc.reason != "" {
				q.Backlog = &backlogJob{Status: "paused", Reason: tc.reason}
			}
			needs, info := a.messageNotices(q, p)
			if (len(needs) > 0) != tc.user {
				t.Fatalf("wrong user responsibility: needs=%+v info=%+v", needs, info)
			}
			for _, n := range append(needs, info...) {
				if strings.Contains(n.Detail+n.Followup, "网络") {
					t.Fatal("guessed network failure")
				}
			}
		})
	}
}

func TestPausedBacklogDoesNotAttributeLegacyStopToUser(t *testing.T) {
	a, q := projectQueueFixture()
	q.Backlog = &backlogJob{Status: "stopped", Reason: "user_stopped", Automatic: true}
	needs, _ := a.messageNotices(q, a.access.Directory.Projects[0])
	var notice messageNotice
	for _, n := range needs {
		if n.Key == "backlog" {
			notice = n
		}
	}
	if notice.Title != "积压消息保存已暂停" || !strings.Contains(notice.Detail, "触发来源未记录") {
		t.Fatalf("legacy stop was attributed to user: %+v", needs)
	}
	if notice.Action != "resume" {
		t.Fatal("lost explicit resume action")
	}
}
func TestUnknownDeliveryIsNeverAUserDecisionOrInferredFromJob(t *testing.T) {
	for _, status := range []string{"", "checking", "unconfirmed"} {
		a, q := projectQueueFixture()
		q.Sessions = q.Sessions[1:]
		q.Documents = nil
		q.Backlog = &backlogJob{Status: "running", Automatic: true}
		r := q.Sessions[0]
		q.Checks[r.Agent.ID+"/"+r.Binding.HostSessionKey] = pendingCheckState{Status: status, Reason: "original_receipt_unavailable"}
		needs, info := a.messageNotices(q, a.access.Directory.Projects[0])
		if len(needs) != 0 || len(info) != 1 {
			t.Fatal(needs, info)
		}
		n := info[0]
		if (n.Tag == "正在检查") != (status == "checking") {
			t.Fatal("job fabricated per-message check", n)
		}
		if status == "unconfirmed" && !strings.Contains(n.Followup, "目前无法自动恢复") {
			t.Fatal("unrecoverable delivery was hidden")
		}
		if n.Action == "resume" || n.Action == "replay" {
			t.Fatal("unknown may be resent")
		}
	}
}

func TestNumberedRetryIsSeparateFromLegacyMissingReceipts(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions = q.Sessions[1:]
	q.Documents = nil
	q.AutomaticSave = true
	r := &q.Sessions[0]
	r.View.Status.Pending, r.View.Status.Unknown, r.View.Status.RetryableUnknown = 3, 3, 2
	p := a.access.Directory.Projects[0]
	access := "maintain"
	p.Overrides[r.Agent.ID] = ProjectOverride{Access: &access}
	q.Checks[r.Agent.ID+"/"+r.Binding.HostSessionKey] = pendingCheckState{Status: "unconfirmed", Reason: "original_receipt_unavailable"}
	needs, info := a.messageNotices(q, p)
	if len(needs) != 0 || len(info) != 2 || info[0].Tag != "等待重试" || !strings.Contains(info[1].Title, "1 条消息") {
		t.Fatal(needs, info)
	}
	if strings.Contains(info[0].Followup, "无法自动恢复") || !strings.Contains(info[1].Followup, "无法自动恢复") {
		t.Fatal("new and legacy recovery were conflated", info)
	}
	q.Loaded = true
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
	}, 1200, 1000)
	fixtureImage(t, tt, "capture-retry-and-legacy")
	q.Backlog = &backlogJob{Status: "stopped", Reason: "user_stopped"}
	_, info = a.messageNotices(q, p)
	if info[0].Tag != "等待恢复" || !strings.Contains(info[0].Followup, "恢复后才会") {
		t.Fatal("claimed retry while paused", info)
	}
}

func TestRetryLabelRequiresObservedTaskState(t *testing.T) {
	for _, status := range []string{"paused", "stopped", "interrupted", "stopping", ""} {
		t.Run(status, func(t *testing.T) {
			a, q := projectQueueFixture()
			q.Sessions, q.Documents = q.Sessions[1:], nil
			q.Sessions[0].View.Status.RetryableUnknown = 1
			p := a.access.Directory.Projects[0]
			access := "maintain"
			p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &access}
			if status != "" {
				q.Backlog = &backlogJob{Status: status, Reason: "backlog_snapshot_unconfirmed"}
			}
			_, info := a.messageNotices(q, p)
			want := "等待恢复"
			if status == "" {
				want = "等待核对"
			}
			if len(info) == 0 || info[0].Tag != want {
				t.Fatalf("retry capability claimed actual execution: %+v", info)
			}
		})
	}
}
func TestNoticeRecoveryAndDetailsDoNotChangeQueue(t *testing.T) {
	a, q := projectQueueFixture()
	q.Loaded = true
	q.Sessions = q.Sessions[1:]
	q.Documents = nil
	before := q.Sessions[0].View.Status
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, a.access.Directory.Projects[0]) })
	}, 1200, 1000)
	clickProjectUI(t, tt, "查看详情")
	if !tt.HasText("会话：synthetic-second") || q.Sessions[0].View.Status.Pending != before.Pending || q.Sessions[0].View.Status.Unknown != before.Unknown {
		t.Fatal("details mutated message status")
	}
	q.Sessions[0].View.observe(PendingMaterials{Signature: "confirmed"})
	tt.Frame()
	if tt.HasText("1 条消息未收到保存结果") || !tt.HasText("当前没有消息积压或已检测到的异常") {
		t.Fatal("confirmed recovery did not remove notice")
	}
}
func TestMessageStatusVisualScenarios(t *testing.T) {
	for _, width := range []int{900, 1500} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, q := projectQueueFixture()
			q.Loaded = true
			q.Documents = nil
			q.AutomaticSave = true
			read := "read"
			p := a.access.Directory.Projects[0]
			p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &read}
			q.Sessions = append(q.Sessions, queuedSession{Agent: AccessAgent{Name: "Codex"}, Binding: ProjectBinding{HostSessionKey: "synthetic-unread"}, View: &pendingBufferView{ReadCode: "official_session_unconfirmed"}})
			q.Checks[q.Sessions[1].Agent.ID+"/synthetic-second"] = pendingCheckState{Status: "unconfirmed", Reason: "original_receipt_unavailable", CheckedAt: float64(time.Now().Unix())}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
			}, width, 760)
			if !tt.HasText("需要授权") || !tt.HasText("系统异常") || !tt.HasText("读取异常") {
				t.Fatal("missing classified states")
			}
			fixtureImage(t, tt, fmt.Sprintf("message-status-mixed-%d", width))
		})
	}
}

func TestPreviouslyDeniedQueueRequiresExplicitReplayWithoutUnknown(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions = q.Sessions[:1]
	p := a.access.Directory.Projects[0]
	access := "maintain"
	p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &access}
	s := &q.Sessions[0].View.Status
	s.Blocked = &struct {
		Reason string `json:"reason"`
	}{Reason: "write_denied"}
	needs, _ := a.messageNotices(q, p)
	if len(needs) != 1 || needs[0].Action != "replay" {
		t.Fatal("restored permission silently resumed or had no explicit action", needs)
	}
	s.Unknown = 1
	needs, info := a.messageNotices(q, p)
	for _, n := range append(needs, info...) {
		if n.Action == "replay" {
			t.Fatal("mixed unknown queue offered unsafe replay")
		}
	}
}
func TestMessageDetailsAreWithdrawnWhenQueueChanges(t *testing.T) {
	v := pendingBufferView{Loaded: true, Status: PendingMaterials{Signature: "old"}, DetailsLoaded: true, Details: []pendingMessageDetail{{Summary: "private"}}}
	v.observe(PendingMaterials{Signature: "new"})
	if v.DetailsLoaded || len(v.Details) > 0 {
		t.Fatal("old message preview survived queue change")
	}
}

func TestDuplicateMessageBodiesRemainIndividuallyVisible(t *testing.T) {
	a, q := projectQueueFixture()
	q.Loaded = true
	q.Sessions = q.Sessions[1:]
	q.Documents = nil
	v := q.Sessions[0].View
	v.CheckDetailsOpen = true
	v.DetailsLoaded = true
	v.Details = []pendingMessageDetail{{ID: "same", Summary: "first duplicate"}, {ID: "same", Summary: "second duplicate"}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, a.access.Directory.Projects[0]) })
	}, 1200, 1000)
	if !tt.HasText("first duplicate") || !tt.HasText("second duplicate") {
		t.Fatal("duplicate message previews were merged")
	}
}

func TestMessageDetailsSeparatePreviewAndDiagnostics(t *testing.T) {
	a, q := projectQueueFixture()
	row := q.Sessions[0]
	v := row.View
	v.DetailsLoaded = true
	v.ReadCode = "capture_retry_pending"
	v.Status.Pending = 269
	for i := 0; i < 20; i++ {
		v.Details = append(v.Details, pendingMessageDetail{ID: fmt.Sprintf("synthetic-id-%02d", i), Kind: "工具记录", Summary: fmt.Sprintf("Bash · synthetic message %02d", i), Unknown: i == 0})
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(900).Padding(20).Children(func() { a.messageNoticeDetails(c, q, a.access.Directory.Projects[0], row) })
	}, 940, 1000)
	if tt.HasText("诊断代码：capture_retry_pending") || tt.HasText("消息标识：synthetic-id-00") || tt.HasText("Bash · synthetic message 05") {
		t.Fatal("default view exposes diagnostic noise or more than five previews")
	}
	if !tt.HasText("结果未确认") || !tt.HasText("待保存") || !tt.HasText("显示其余 15 条") {
		t.Fatal("missing message state distinction or progressive disclosure")
	}
	fixtureImage(t, tt, "message-details-readable")
	clickProjectUI(t, tt, "诊断信息")
	if !tt.HasText("诊断代码：capture_retry_pending") || !tt.HasText("消息标识：synthetic-id-00") {
		t.Fatal("technical details are no longer available")
	}
	clickProjectUI(t, tt, "收起诊断信息")
	clickProjectUI(t, tt, "显示其余 15 条")
	if !tt.HasText("Bash · synthetic message 19") || v.Status.Pending != 269 {
		t.Fatal("showing additional previews lost messages or changed delivery state")
	}
	v.observe(PendingMaterials{Signature: "new-snapshot"})
	if v.AllMessagesOpen || v.ExpandedMessages != nil || v.DetailsLoaded {
		t.Fatal("old preview state survived a new queue snapshot")
	}
}

func TestNumberedPausedQueueCanResumeWithoutClaimingAutomaticRetry(t *testing.T) {
	for _, reason := range []string{"user_stopped", "capture_retry_pending"} {
		a, q := projectQueueFixture()
		q.Sessions = q.Sessions[1:]
		q.Documents = nil
		q.Sessions[0].View.Status.Pending = 1
		q.Sessions[0].View.Status.Unknown = 1
		q.Sessions[0].View.Status.RetryableUnknown = 1
		p := a.access.Directory.Projects[0]
		access := "maintain"
		p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &access}
		q.Backlog = &backlogJob{Status: "paused", Reason: reason, Automatic: false}
		needs, _ := a.messageNotices(q, p)
		found := false
		for _, n := range needs {
			if n.Key == "backlog" {
				found = n.Action == "resume" && n.Tag != "自动重试"
			}
		}
		if !found {
			t.Fatalf("numbered manual queue has no explicit recovery: %+v", needs)
		}
		q.Sessions[0].View.Status.RetryableUnknown = 0
		needs, _ = a.messageNotices(q, p)
		for _, n := range needs {
			if n.Action == "resume" {
				t.Fatal("legacy unnumbered unknown became recoverable")
			}
		}
	}
}
func TestOfficial403CanOnlyReplayAfterPermissionRestored(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions = q.Sessions[:1]
	q.Documents = nil
	q.Sessions[0].View.Status.Blocked = &struct {
		Reason string `json:"reason"`
	}{Reason: "official_http_403"}
	p := a.access.Directory.Projects[0]
	access := "maintain"
	p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &access}
	needs, _ := a.messageNotices(q, p)
	found := false
	for _, n := range needs {
		found = found || n.Action == "replay"
	}
	if !found {
		t.Fatal("known403 cannot be explicitly replayed", needs)
	}
	access = "read"
	p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &access}
	needs, _ = a.messageNotices(q, p)
	for _, n := range needs {
		if n.Action == "replay" {
			t.Fatal("403 replay bypassed current permission")
		}
	}
}
