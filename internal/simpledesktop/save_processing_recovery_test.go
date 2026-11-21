package simpledesktop

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
