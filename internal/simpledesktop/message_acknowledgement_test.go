package simpledesktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func acknowledgementFixture(t *testing.T) (*App, *projectQueueState, AccessProject) {
	t.Helper()
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	q.Sessions, q.Documents = q.Sessions[1:], nil
	q.Backlog = &backlogJob{Operation: "task-one", Status: "paused", Reason: "backlog_result_unconfirmed", ProcessingFailed: 1}
	return a, q, a.access.Directory.Projects[0]
}

func TestMessageAcknowledgementClosesOnlyChosenReminderAndSurvivesReopen(t *testing.T) {
	a, q, p := acknowledgementFixture(t)
	before := q.Sessions[0].View.Status
	job := *q.Backlog
	if !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("fixture has no notification")
	}
	_, notices := a.messageNotices(q, p)
	a.acknowledgeNotice(q, p, notices[0], false)
	if !q.noticeAcknowledged(notices[0].Key) || !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("one acknowledgement cleared other issues")
	}
	for _, n := range notices[1:] {
		a.acknowledgeNotice(q, p, n, false)
	}
	if q.NoticeAcknowledgementError != "" || q.unacknowledgedNotification(time.Now()) {
		t.Fatal("all acknowledgements did not clear notification", q.NoticeAcknowledgementError)
	}
	if !reflect.DeepEqual(before, q.Sessions[0].View.Status) || job != *q.Backlog {
		t.Fatal("acknowledgement changed delivery or task")
	}
	q.Checks[q.Sessions[0].Agent.ID+"/"+q.Sessions[0].Binding.HostSessionKey] = pendingCheckState{Status: "unconfirmed", Reason: "original_receipt_unavailable", CheckedAt: 999999}
	q.Backlog.Saved++
	if q.unacknowledgedNotification(time.Now()) {
		t.Fatal("polling time or task progress re-alerted")
	}
	a.access.Queues = nil
	reopened := a.projectQueueStateFor(p)
	reopened.Sessions, reopened.Backlog = q.Sessions, q.Backlog
	if reopened.unacknowledgedNotification(time.Now()) {
		t.Fatal("reopening lost acknowledged state")
	}
	path, _ := a.noticeAcknowledgementPath(p)
	b, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(b), q.Sessions[0].Binding.HostSessionKey) || strings.Contains(string(b), "task-one") {
		t.Fatal("state contains source identities", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state is not private")
	}
	a.acknowledgeNotice(reopened, p, notices[0], true)
	if !reopened.unacknowledgedNotification(time.Now()) {
		t.Fatal("restore reminder did not re-alert")
	}
}

func TestAcknowledgementRealertsForSameCountNewMessagesAndTask(t *testing.T) {
	a, q, p := acknowledgementFixture(t)
	q.Backlog = nil
	s := &q.Sessions[0].View.Status
	s.UnknownEntryIDs = []string{noticeHash("first")}
	_, notices := a.messageNotices(q, p)
	a.acknowledgeNotice(q, p, notices[0], false)
	s.Signature = "changed by unrelated known unsent messages"
	if q.unacknowledgedNotification(time.Now()) {
		t.Fatal("unrelated queue changes re-alerted same identified unknown")
	}
	s.UnknownEntryIDs = []string{noticeHash("second")}
	if !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("same count new messages inherited acknowledgement")
	}
	q.Backlog = &backlogJob{Operation: "one", Status: "paused", Reason: "unexpected"}
	a.acknowledgeNotice(q, p, messageNotice{Key: "backlog"}, false)
	q.Backlog.Operation = "two"
	if q.noticeAcknowledged("backlog") {
		t.Fatal("new task inherited old decision")
	}
}

func TestAcknowledgementFailureAndSourceIsolationKeepReminder(t *testing.T) {
	a, q, p := acknowledgementFixture(t)
	_, notices := a.messageNotices(q, p)
	path, _ := a.noticeAcknowledgementPath(p)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	a.acknowledgeNotice(q, p, notices[0], false)
	if q.NoticeAcknowledgementError == "" || q.noticeAcknowledged(notices[0].Key) || !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("failed state write hid reminder")
	}
	os.Remove(path)
	a.acknowledgeNotice(q, p, notices[0], false)
	p.Dataset = "another"
	rows, message := a.readNoticeAcknowledgements(p)
	if message != "" || len(rows) != 0 {
		t.Fatal("dataset inherited acknowledgement")
	}
	p.Dataset = "ds"
	a.Client.Config.ServiceURL = "http://127.0.0.1:65432"
	rows, message = a.readNoticeAcknowledgements(p)
	if message != "" || len(rows) != 0 {
		t.Fatal("different service inherited acknowledgement")
	}
}

func TestReadFailureRetainsKnownUnknownAndAcknowledgementActions(t *testing.T) {
	a, q, p := acknowledgementFixture(t)
	q.Backlog = nil
	v := q.Sessions[0].View
	v.Loaded, v.ReadError = false, "unavailable"
	_, notices := a.messageNotices(q, p)
	if len(notices) != 2 || !strings.HasSuffix(notices[1].Key, "/unknown") {
		t.Fatal("read failure hid retained unknown and its action", notices)
	}
	for _, n := range notices {
		a.acknowledgeNotice(q, p, n, false)
	}
	q.observeNotificationFailures(time.Now().Add(-3 * time.Minute))
	q.observeNotificationFailures(time.Now().Add(-time.Minute))
	q.observeNotificationFailures(time.Now())
	if q.unacknowledgedNotification(time.Now()) {
		t.Fatal("acknowledged read failure or retained unknown re-alerted")
	}
	v.ReadError = "another cause"
	if !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("changed read failure did not re-alert")
	}
	a.clearRecoveredNoticeAcknowledgements(q, p, []string{notices[0].Key})
	v.ReadError = "unavailable"
	if q.noticeAcknowledged(notices[0].Key) {
		t.Fatal("new episode inherited earlier acknowledgement")
	}
}

func TestMessageActionsRemainVisibleAndAcknowledgementShowsReceipt(t *testing.T) {
	a, q, p := acknowledgementFixture(t)
	q.Backlog = nil
	q.Loaded = true
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
	}, 1200, 1000)
	if !tt.HasText("知道了，不再提醒") || !tt.HasText("查看详情") {
		t.Fatal("missing explicit reminder action")
	}
	fixtureImage(t, tt, "message-actions-before")
	clickProjectUI(t, tt, "知道了，不再提醒")
	if !tt.HasText("已知晓的历史提示（1）") || tt.HasText("1 条消息未收到采集结果") {
		t.Fatal("acknowledged issue did not leave the current reminder list")
	}
	clickProjectUI(t, tt, "已知晓的历史提示（1）")
	if !tt.HasText("已知晓 · 不再提醒") || !tt.HasText("恢复提醒") || !tt.HasText("1 条消息未收到采集结果") || q.unacknowledgedNotification(time.Now()) {
		t.Fatal("acknowledgement lost issue facts or failed to remove reminder")
	}
	fixtureImage(t, tt, "message-actions-acknowledged")
	clickProjectUI(t, tt, "恢复提醒")
	if !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("restore action did not work")
	}
	access := "maintain"
	p.Overrides[q.Sessions[0].Agent.ID] = ProjectOverride{Access: &access}
	q.Sessions[0].View.observe(PendingMaterials{Pending: 2, Signature: "new"})
	tt.Frame()
	if !tt.HasText("开始采集") || !tt.HasText("暂不处理") {
		t.Fatal("safe unsent queue lacks explicit choices")
	}
	clickProjectUI(t, tt, "暂不处理")
	if !tt.HasText("已知晓的历史提示（1）") {
		t.Fatal("deferred issue has no history entry")
	}
	if !tt.HasText("暂不处理 · 不再提醒") || q.Sessions[0].View.Status.Pending != 2 {
		t.Fatal("defer action consumed messages")
	}
}

func (q *projectQueueState) unacknowledgedNotification(_ time.Time) bool {
	keys, ackErr := (&App{}).unacknowledgedNotificationKeys(q, AccessProject{})
	return ackErr || len(keys) > 0
}

func TestAcknowledgementAlertTargetsUseDisplayedAnomalies(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		setup     func(*projectQueueState, *AccessProject)
	}{
		{"processing failure", "processing/op1", func(q *projectQueueState, _ *AccessProject) {
			q.ProcessingNotices = []processingNotice{{Operation: "op1", Status: 500}}
		}},
		{"policy sync", "policy-sync", func(_ *projectQueueState, p *AccessProject) {
			p.Status = "unconfirmed"
		}},
		{"review failure", "review-failed", func(q *projectQueueState, _ *AccessProject) {
			q.Backlog = &backlogJob{ReviewFailed: 1}
		}},
		{"processing read", "processing-read", func(q *projectQueueState, _ *AccessProject) {
			q.ProcessingReadError = "提炼状态读取失败"
			q.ProcessingNotices = []processingNotice{{Operation: "op1", Status: 500}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := settingsFixture()
			a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
			p := &a.access.Directory.Projects[0]
			q := a.projectQueueStateFor(*p)
			q.Loaded = true
			tc.setup(q, p)
			keys, ackErr := a.unacknowledgedNotificationKeys(q, *p)
			if len(keys) != 1 || !keys[tc.key] || ackErr {
				t.Fatalf("unexpected alert keys: %v, error=%t", keys, ackErr)
			}
			a.openProjectQueueStatus(0)
			ui.NewTester(a.View, 1200, 820)
			if q.LocatedNoticeKey != tc.key {
				t.Fatalf("tray navigation highlighted %q, want %q", q.LocatedNoticeKey, tc.key)
			}
			a.acknowledgeNotice(q, *p, messageNotice{Key: tc.key}, false)
			keys, ackErr = a.unacknowledgedNotificationKeys(q, *p)
			if len(keys) > 0 || ackErr {
				t.Fatalf("acknowledged notice still targets: %v, error=%t", keys, ackErr)
			}
		})
	}
}

func TestAcknowledgementAlertTargetsIgnoreOrdinaryRetryProgress(t *testing.T) {
	a := settingsFixture()
	a.access.Directory.Defaults.Access = "maintain"
	p := a.access.Directory.Projects[0]
	p.Overrides = nil
	q := &projectQueueState{
		AutomaticSave: true,
		Sessions: []queuedSession{{
			Agent: AccessAgent{ID: "agent"}, Binding: ProjectBinding{HostSessionKey: "session"},
			View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1, RetryableUnknown: 1}},
		}},
	}
	check := func(wantTag string) {
		t.Helper()
		needs, info := a.messageNotices(q, p)
		anomalies, progress := partitionInfoNotices(info)
		if len(progress) != 1 || progress[0].Tag != wantTag {
			t.Fatalf("expected %s notice, got %+v", wantTag, progress)
		}
		if keys, ackErr := a.unacknowledgedNotificationKeys(q, p); len(keys) != 0 || ackErr {
			t.Fatalf("retry progress alerted: %v, error=%t", keys, ackErr)
		}
		if key, ackErr := a.pickProjectQueueAlertTarget(q, p, needs, anomalies); key != "" || ackErr {
			t.Fatalf("retry progress became a jump target: %q, error=%t", key, ackErr)
		}
	}
	check("等待重试")
	q.Checks = map[string]pendingCheckState{"agent/session": {Status: "checking"}}
	check("正在核对")
	q.Sessions = nil
	q.Backlog = &backlogJob{Status: "paused", Reason: "capture_retry_pending", Automatic: true}
	check("自动重试")

	// Nil queue state must not panic and still surfaces directory policy-sync unconfirmed state.
	if keys, ackErr := a.unacknowledgedNotificationKeys(nil, p); len(keys) != 0 || ackErr {
		t.Fatalf("unexpected keys for nil queue on verified project: %v, err=%t", keys, ackErr)
	}
	unconfirmed := p
	unconfirmed.Status = "unconfirmed"
	if keys, ackErr := a.unacknowledgedNotificationKeys(nil, unconfirmed); !keys["policy-sync"] || len(keys) != 1 || ackErr {
		t.Fatalf("expected policy-sync key for nil queue on unconfirmed project: %v, err=%t", keys, ackErr)
	}
}
