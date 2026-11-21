package simpledesktop

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestRejectedMessageDoesNotBecomeSendableOrHideHealthyPending(t *testing.T) {
	a, q := projectQueueFixture()
	a.Config.ClientStateFile = filepath.Join(t.TempDir(), "client-state.json")
	q.Sessions, q.Documents = q.Sessions[:1], nil
	row := q.Sessions[0]
	row.View.Status = PendingMaterials{Pending: 3, Unknown: 1, Signature: "current"}
	q.BufferReviews = []bufferReview{{AgentID: row.Agent.ID, Host: row.Binding.HostSessionKey, Signature: "current", Rejected: 2, RejectedUnsent: 1, RejectedUnknown: 1}}
	q.Backlog = &backlogJob{Status: "watching"}
	if queueSafeCount(q) != 1 || queueRecoverableCount(q) != 1 {
		t.Fatal("rejected entry was treated as safe to resume")
	}
	_, notices := a.messageNotices(q, a.access.Directory.Projects[0])
	var rejected messageNotice
	for _, n := range notices {
		if strings.HasSuffix(n.Key, "/rejected") {
			rejected = n
		}
		if strings.HasSuffix(n.Key, "/unknown") {
			t.Fatal("a known rejection was mislabeled as legacy receipt loss")
		}
	}
	if rejected.Title != "2 条消息采集受阻" || !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("known rejected records have no visible reason or notification", notices)
	}
	a.acknowledgeNotice(q, a.access.Directory.Projects[0], rejected, false)
	if q.unacknowledgedNotification(time.Now()) {
		t.Fatal("acknowledged rejection still notified as a separate unknown write")
	}
}

func TestFilteredBufferUsesMatchingSnapshotAndKeepsUnknownSeparate(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions = q.Sessions[:1]
	row := q.Sessions[0]
	row.View.Status = PendingMaterials{Pending: 4, Unknown: 1, Signature: "snapshot"}
	q.BufferReviews = []bufferReview{{AgentID: row.Agent.ID, Host: row.Binding.HostSessionKey, Signature: "snapshot", Filtered: 2}}
	_, info := a.messageNotices(q, a.access.Directory.Projects[0])
	filtered := false
	for _, n := range info {
		filtered = filtered || strings.HasSuffix(n.Key, "/filtered") && strings.Contains(n.Title, "2 条")
	}
	if !filtered || queueSafeCount(q) != 1 || queueRecoverableCount(q) != 1 {
		t.Fatal("filtered retained messages were counted as sendable", info)
	}
	row.View.Status.Signature = "changed"
	if q.filteredPending(row) != 0 || queueSafeCount(q) != 3 {
		t.Fatal("old filter result applied to another queue snapshot")
	}
}

func TestPartialAndActiveProcessingAreNotFailuresOrUnknownWrites(t *testing.T) {
	a, q := projectQueueFixture()
	q.Sessions, q.Documents = nil, nil
	q.Backlog = &backlogJob{Status: "watching", Operation: "original", ProcessingPartial: 1, ProcessingActive: 1}
	_, info := a.messageNotices(q, a.access.Directory.Projects[0])
	if len(info) != 2 || info[0].Key != "processing-partial" || info[1].Key != "processing-active" || q.unacknowledgedNotification(time.Now()) {
		t.Fatal("partial/active processing was presented as failure", info)
	}
	q.Backlog.ProcessingFailed = 1
	if !q.unacknowledgedNotification(time.Now()) {
		t.Fatal("actual unconfirmed submit was hidden")
	}
}

func TestProcessingFailureDetailShowsIssuesAndActions(t *testing.T) {
	a, q := projectQueueFixture()
	p := a.access.Directory.Projects[0]
	q.Sessions, q.Documents = nil, nil
	q.Loaded = true
	q.AutomaticSave = true
	q.Backlog = &backlogJob{Status: "watching", Operation: "original", ProcessingFailed: 1}
	q.ProcessingIssues = []processingIssue{{
		AgentID:         a.access.Directory.Agents[0].ID,
		Host:            "host-abcdef123456",
		SessionID:       "codex_host-abcdef123456",
		Processing:      "unknown",
		Reason:          "owner_interrupted",
		AttemptedAt:     1795266456,
		ReconcileStatus: "failed",
		ReconciledAt:    1795270000,
	}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().MaxWidth(900).Children(func() { a.projectQueueSection(c, p) })
	}, 1200, 1000)
	if !tt.HasText("部分消息的记忆提炼未确认完成") {
		t.Fatalf("missing processing-failed card: %v", tt.Texts())
	}
	clickProjectUI(t, tt, "查看详情")
	for _, want := range []string{"相关会话", "核对提炼状态", "重新提炼", "后台服务重启或停止打断了本次提交", "诊断信息"} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected detail text %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("codex_host-abcdef123456") {
		t.Fatal("technical session id exposed before expanding diagnostics")
	}
	clickProjectUI(t, tt, "诊断信息")
	if !tt.HasText("codex_host-abcdef123456") {
		t.Fatalf("expanded diagnostics missing session_id: %v", tt.Texts())
	}
}
