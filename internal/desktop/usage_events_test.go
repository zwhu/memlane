package desktop

// Private Unix IPC fixtures verify presentation/lifecycle only; durable event
// queries are separately covered by real SQLite tests in store.
import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestUsageEventsPrivatePageFailureAndTargetEpoch(t *testing.T) {
	var mode atomic.Int32
	requests := make(chan records.UsageEventsQuery, 10)
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "usage_events" || r.UsageEventsQuery == nil {
			return serviceipc.Response{Error: "unexpected operation"}
		}
		q := *r.UsageEventsQuery
		requests <- q
		if mode.Load() == 1 {
			return serviceipc.Response{Code: "unavailable", Error: "do-not-display-provider-body"}
		}
		page := records.UsageEventsPage{Memory: q.Memory, Dataset: q.Dataset, CurrentRevision: q.ExpectedRevision, Channel: q.Channel, CoverageStartedMS: 1800000000000, RetentionDays: 90, SnapshotMS: 1800000001000}
		event := records.UsageEvent{ID: "returned-event", Memory: q.Memory, Dataset: q.Dataset, Revision: 1, Channel: q.Channel, Kind: "return", ConnectionID: "connection", Generation: 3}
		page.Events = []records.UsageEvent{event}
		page.HasMore = true
		page.NextCursor = "opaque-next"
		if q.Cursor != "" {
			event.ID, event.Kind = "read-event", "read"
			page.Events = append(page.Events, event)
			page.HasMore = false
			page.NextCursor = ""
		}
		if mode.Load() == 2 {
			page.Channel = "verification"
		}
		return serviceipc.Response{OK: true, Result: page}
	})
	v := MemoryItem{ID: "knowledge", Dataset: "project", Revision: 2}
	a.bindUsageEvents(v)
	awaitProcessingDispatch(t, dispatch)
	first := <-requests
	if first.Memory != v.ID || first.Dataset != v.Dataset || first.ExpectedRevision != 2 || first.Channel != "daily" || first.Limit != 20 {
		t.Fatal("query target or independent channel missing", first)
	}
	if !a.usageEvents.Loaded || len(a.usageEvents.Items) != 1 || a.usageEvents.Items[0].Revision != 1 {
		t.Fatal("observed version rewritten", a.usageEvents)
	}
	a.selectedID = "preserved-selection"
	a.detailScroll.Y = 77
	mode.Store(1)
	a.refreshUsageEvents(true)
	awaitProcessingDispatch(t, dispatch)
	<-requests
	if a.usageEvents.Error == "" || strings.Contains(a.usageEvents.Error, "do-not-display") || len(a.usageEvents.Items) != 1 || !a.usageEvents.RetryMore {
		t.Fatal("failed page cleared real facts or leaked response", a.usageEvents)
	}
	mode.Store(0)
	a.refreshUsageEvents(a.usageEvents.RetryMore)
	awaitProcessingDispatch(t, dispatch)
	q := <-requests
	if q.Cursor != "opaque-next" || len(a.usageEvents.Items) != 2 || a.selectedID != "preserved-selection" || a.detailScroll.Y != 77 {
		t.Fatal("retry pagination lost cursor/selection/scroll", a.usageEvents)
	}
	mode.Store(2)
	a.refreshUsageEvents(false)
	awaitProcessingDispatch(t, dispatch)
	<-requests
	if a.usageEvents.Error == "" || len(a.usageEvents.Items) != 2 {
		t.Fatal("wrong channel reply replaced daily facts")
	}
	mode.Store(0)
	a.refreshUsageEvents(false)
	// A completed response for a previous pane/library epoch is discarded.
	a.usageEvents = UsageEventsUI{Root: a.library.Root, Memory: "other-knowledge", Revision: 4, Sequence: a.usageEvents.Sequence + 1, Channel: "verification", Loaded: true}
	awaitProcessingDispatch(t, dispatch)
	<-requests
	if a.usageEvents.Memory != "other-knowledge" || a.usageEvents.Channel != "verification" || len(a.usageEvents.Items) != 0 {
		t.Fatal("late reply crossed target epoch", a.usageEvents)
	}
}
