package simpledesktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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
	if notice.Title != "积压消息采集已暂停" || !strings.Contains(notice.Detail, "触发来源未记录") {
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
	if !q.DetailDialogOpen || !tt.HasText("会话：synthetic-second") || q.Sessions[0].View.Status.Pending != before.Pending || q.Sessions[0].View.Status.Unknown != before.Unknown {
		t.Fatal("details mutated message status or failed to open dialog")
	}
	clickProjectUI(t, tt, "关闭")
	if q.DetailDialogOpen {
		t.Fatal("detail dialog did not close")
	}
	q.Sessions[0].View.observe(PendingMaterials{Signature: "confirmed"})
	tt.Frame()
	if tt.HasText("1 条消息未收到采集结果") || !tt.HasText("自动采集已暂停") {
		t.Fatal("confirmed recovery did not transition to healthy single-row state")
	}
	q.AutomaticSave = true
	tt.Frame()
	if !tt.HasText("自动采集已开启 · 正常") {
		t.Fatal("healthy automatic-save state missing")
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
	if !tt.HasText("结果未确认") || !tt.HasText("待采集") || !tt.HasText("显示其余 15 条") {
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

func TestProcessingNoticeDetailDialogShowsDiagnostics(t *testing.T) {
	a, q := projectQueueFixture()
	p := a.access.Directory.Projects[0]
	q.Sessions = nil
	q.Documents = nil
	q.Loaded = true
	q.AutomaticSave = true
	q.ProcessingNotices = []processingNotice{{
		Operation:   "op-504",
		Request:     "req-504",
		At:          "2026-11-21T07:51:57Z",
		RecoveredAt: "2026-11-21T09:00:24Z",
		Status:      504,
		Code:        "codex_timeout",
		Backend:     "codex",
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
	}, 1200, 1000)
	for _, want := range []string{"系统异常", "记忆提炼调用失败", "提炼异常", "查看详情", "知道了，不再提醒"} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected text %q", want)
		}
	}
	for _, legacy := range []string{"当前没有需要你处理的事项", "状态与异常提示", "重新读取状态"} {
		if tt.HasText(legacy) {
			t.Fatalf("unexpected legacy text %q remained", legacy)
		}
	}
	clickProjectUI(t, tt, "查看详情")
	if !q.DetailDialogOpen {
		t.Fatal("expected detail dialog to open")
	}
	for _, want := range []string{"codex_timeout", "诊断信息", "复制诊断", "刷新状态"} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected detail dialog text %q", want)
		}
	}
	if tt.HasText("op-504") {
		t.Fatal("technical diagnostics exposed before expanding")
	}
	clickProjectUI(t, tt, "诊断信息")
	if !tt.HasText("op-504") || !tt.HasText("req-504") {
		t.Fatal("expanded diagnostics missing operation_id or request_id")
	}
	clickProjectUI(t, tt, "关闭")
	if q.DetailDialogOpen {
		t.Fatal("expected detail dialog to close")
	}
}

func TestProcessingNoticeMovesToResolvedHistoryWithoutClosingDiagnostics(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			a, q := projectQueueFixture()
			a.Config.ConfigFile = filepath.Join(t.TempDir(), "runtime.json")
			a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
			a.initDesktopPreferences()
			a.setAppearancePreference(appearance)
			p := a.access.Directory.Projects[0]
			q.Sessions = nil
			q.Documents = nil
			q.Loaded = true
			q.ProcessingNotices = []processingNotice{{
				Operation: "original", Request: "original-request", At: "2026-11-21T18:55:28Z",
				Status: 502, Code: "codex_stream_disconnected", Backend: "codex",
			}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
			}, 1200, 1000)
			if !tt.HasText("1 项待处理") {
				t.Fatal("unconfirmed original operation must appear as pending")
			}
			clickProjectUI(t, tt, "查看详情")
			clickProjectUI(t, tt, "诊断信息")
			q.ProcessingNotices[0].ResolvedAt = "2026-11-21T18:57:18Z"
			tt.Frame()
			if !q.DetailDialogOpen || !tt.HasText("original-request") || !tt.HasText("resolved_at") {
				t.Fatal("open diagnostics must show the same operation and the completion receipt")
			}
			if _, count := a.countProjectMessageAlerts(p); count != 0 {
				t.Fatalf("confirmed operation still counts as a project anomaly: %d", count)
			}
			clickProjectUI(t, tt, "关闭")
			if tt.HasText("1 项待处理") || !tt.HasText("已恢复的提炼调用（1）") {
				t.Fatal("confirmed operation did not leave pending and enter history")
			}
			clickProjectUI(t, tt, "已恢复的提炼调用（1）")
			if tt.HasText("恢复提醒") || tt.HasText("知道了，不再提醒") {
				t.Fatal("automatically resolved operation was offered a manual acknowledgement")
			}
			clickProjectUI(t, tt, "查看详情")
			if !q.DetailDialogOpen {
				t.Fatal("resolved operation no longer opens diagnostics")
			}
			clickProjectUI(t, tt, "关闭")
			q.ProcessingNotices = append(q.ProcessingNotices, processingNotice{
				Operation: "older", At: "2026-11-21T17:00:00Z", Status: 502, Code: "codex_stream_disconnected", Backend: "codex",
			})
			tt.Frame()
			if !tt.HasText("1 项待处理") || !tt.HasText("已恢复的提炼调用（1）") {
				t.Fatal("closed history must not mask an older unresolved operation")
			}
		})
	}
}

func TestProcessingNoticeUsesConfirmedFailureReceipt(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions = nil
	q.Documents = nil
	q.Loaded = true
	p := a.access.Directory.Projects[0]
	now := time.Now().UTC()
	q.ProcessingNotices = []processingNotice{{
		Operation: "failed-operation", Request: "failed-request", At: now.Add(-2 * time.Minute).Format(time.RFC3339),
		FailedAt: now.Add(-time.Minute).Format(time.RFC3339), Status: 502, Code: "codex_stream_disconnected", Backend: "codex",
	}}
	tt := ui.NewTester(func(c *ui.Context) { a.projectQueueSection(c, p) }, 1200, 820)
	if !tt.HasText("记忆提炼失败") || !tt.HasText("结果为失败") || tt.HasText("该次提炼结果仍需核对") || !tt.HasText("1 项待处理") {
		t.Fatalf("confirmed failure was not presented as a terminal anomaly: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "查看详情")
	clickProjectUI(t, tt, "诊断信息")
	if !tt.HasText("failed_at") || !tt.HasText("failed-request") {
		t.Fatal("diagnostics lost the failure receipt or request ID")
	}
}

func TestMessageNoticesSupportsAllAgentBridgeBackends(t *testing.T) {
	a, q := projectQueueFixture()
	p := a.access.Directory.Projects[0]
	q.Sessions = nil
	q.Documents = nil
	q.Loaded = true
	now := time.Now().UTC().Format(time.RFC3339)
	cases := []struct {
		backend    string
		code       string
		status     int
		wantSource string
		wantDetail string
	}{
		{"pi", "pi_authentication_required", 409, "Pi · 记忆提炼", "Pi 登录认证失败"},
		{"antigravity", "antigravity_account_location_unavailable", 403, "Antigravity · 记忆提炼", "地区或网络出口不可用"},
		{"traex", "traex_quota_exceeded", 429, "TRAE CLI · 记忆提炼", "TRAE CLI 可用额度不足"},
	}
	for _, tc := range cases {
		q.ProcessingNotices = []processingNotice{{
			Operation: "op-" + tc.backend,
			Request:   "req-" + tc.backend,
			At:        now,
			Status:    tc.status,
			Code:      tc.code,
			Backend:   tc.backend,
		}}
		_, info := a.messageNotices(q, p)
		if len(info) != 1 {
			t.Fatalf("%s: expected 1 notice, got %d", tc.backend, len(info))
		}
		if !strings.HasPrefix(info[0].Source, tc.wantSource) {
			t.Fatalf("%s: expected source prefix %q, got %q", tc.backend, tc.wantSource, info[0].Source)
		}
		if !strings.Contains(info[0].Detail, tc.wantDetail) {
			t.Fatalf("%s: expected detail containing %q, got %q", tc.backend, tc.wantDetail, info[0].Detail)
		}
	}
}

func TestRetryStatesAreProgressAndPolicyUnconfirmedSurfaces(t *testing.T) {
	a, q := projectQueueFixture()
	p := a.access.Directory.Projects[0]
	q.Sessions = nil
	q.Documents = nil
	q.Loaded = true
	q.AutomaticSave = true

	// 1. partitionInfoNotices classifies retry/checking tags as progress, not anomalies.
	sample := []messageNotice{
		{Key: "retry", Tag: "等待重试"},
		{Key: "auto-retry", Tag: "自动重试"},
		{Key: "checking-1", Tag: "正在核对"},
		{Key: "checking-2", Tag: "正在检查"},
		{Key: "real-err", Tag: "系统异常"},
	}
	anomalies, progress := partitionInfoNotices(sample)
	if len(anomalies) != 1 || anomalies[0].Key != "real-err" || len(progress) != 4 {
		t.Fatalf("unexpected partition: anomalies=%+v progress=%+v", anomalies, progress)
	}
	for _, n := range progress {
		if !n.isProgressNotice() || !n.isActiveProgressNotice() {
			t.Fatalf("expected active progress notice for tag %q", n.Tag)
		}
	}

	// 2. Retryable unknown on healthy project counts as 0 project alerts.
	q.Sessions = []queuedSession{{
		Agent:   a.access.Directory.Agents[0],
		Binding: ProjectBinding{ProjectID: p.ID, AgentID: a.access.Directory.Agents[0].ID, HostSessionKey: "s-retry"},
		View:    &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1, RetryableUnknown: 1}},
	}}
	maintain := "maintain"
	p.Overrides[a.access.Directory.Agents[0].ID] = ProjectOverride{Access: &maintain}
	actionCnt, errCnt := a.countProjectMessageAlerts(p)
	if actionCnt != 0 || errCnt != 0 {
		t.Fatalf("healthy 等待重试 should not count as alert: action=%d err=%d", actionCnt, errCnt)
	}

	// 3. Project ACL unconfirmed surfaces policy-sync anomaly with 核对权限 and pauses retry badge.
	p.Status = "unconfirmed"
	needs, info := a.messageNotices(q, p)
	if len(needs) != 0 {
		t.Fatalf("expected no user-fault needs, got %+v", needs)
	}
	var policyNotice, retryNotice *messageNotice
	for i := range info {
		switch info[i].Key {
		case "policy-sync":
			policyNotice = &info[i]
		default:
			if strings.HasSuffix(info[i].Key, "/retry") {
				retryNotice = &info[i]
			}
		}
	}
	if policyNotice == nil || policyNotice.Action != "reconcile-policy" || policyNotice.Tag != "状态未确认" {
		t.Fatalf("missing policy-sync notice: %+v", info)
	}
	if retryNotice == nil || retryNotice.Tag != "状态未确认" {
		t.Fatalf("expected unconfirmed policy to suppress 等待重试 claim: %+v", retryNotice)
	}
	a.access.Directory.Projects[0] = p
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
	}, 1200, 1000)
	if !tt.HasText("项目权限同步尚未确认") || !tt.HasText("核对权限") {
		t.Fatalf("expected policy-sync notice and 核对权限 action in UI: %v", tt.Texts())
	}
}
