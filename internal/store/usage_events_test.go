package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestUsageEventsRealRowsStableSameTimePagesAndIdempotency(t *testing.T) {
	s, c, v := usageFixture(t)
	ctx := context.Background()
	var replay records.UsageObservation
	for index := 0; index < 53; index++ {
		observation := usageObservation(c, v, "read")
		observation.OperationID = fmt.Sprintf("retained-event-%03d", index)
		_, err := s.RecordUsage(ctx, observation)
		mustMerge(t, err)
		replay = observation
	}
	_, err := s.RecordUsage(ctx, replay)
	mustMerge(t, err)
	// Explicit deterministic timestamp fixture exercises SQL tie ordering;
	// observations themselves were accepted by the real transactional recorder.
	observed := time.Now().Add(-time.Second).UnixMilli()
	_, err = s.DB.Exec(`UPDATE usage_events SET observed_ms=?`, observed)
	mustMerge(t, err)
	query := records.UsageEventsQuery{Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: v.Revision, Limit: 20}
	first, err := s.UsageEvents(ctx, query)
	mustMerge(t, err)
	if len(first.Events) != 20 || !first.HasMore || first.NextCursor == "" || first.Events[0].ID != "retained-event-052" {
		t.Fatal("deterministic first page missing", first)
	}
	// Even a newer event with the same timestamp and a lower lexical ID may
	// not enter this pagination snapshot: its actual rowid is above high water.
	newObservation := usageObservation(c, v, "read")
	newObservation.OperationID = "new-after-snapshot"
	_, err = s.RecordUsage(ctx, newObservation)
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE usage_events SET observed_ms=? WHERE id=?`, observed, newObservation.OperationID)
	mustMerge(t, err)
	seen := map[string]bool{}
	page := first
	for {
		for _, event := range page.Events {
			if seen[event.ID] || event.ID == newObservation.OperationID || event.Memory != v.Memory || event.Dataset != v.Dataset || event.Revision != v.Revision || event.ConnectionID != c.ID || event.Generation != c.Generation || event.Agent != "pi" || event.Channel != "daily" || event.Kind != "read" {
				t.Fatal("duplicated or fabricated event provenance", event)
			}
			seen[event.ID] = true
		}
		if !page.HasMore {
			break
		}
		query.Cursor = page.NextCursor
		page, err = s.UsageEvents(ctx, query)
		mustMerge(t, err)
		if page.SnapshotMS != first.SnapshotMS {
			t.Fatal("snapshot changed")
		}
	}
	if len(seen) != 53 {
		t.Fatal("pagination skipped or replay recounted event", len(seen))
	}
	for _, change := range []func(*records.UsageEventsQuery){func(q *records.UsageEventsQuery) { q.Memory = uuid.NewString() }, func(q *records.UsageEventsQuery) { q.Channel = "verification" }, func(q *records.UsageEventsQuery) { q.Kind = "return" }, func(q *records.UsageEventsQuery) { q.Limit = 10 }, func(q *records.UsageEventsQuery) { q.Cursor = "X" + q.Cursor }} {
		bad := query
		bad.Cursor = first.NextCursor
		change(&bad)
		if _, err = s.UsageEvents(ctx, bad); err == nil {
			t.Fatal("cursor moved to another target/filter")
		}
	}
	path := strings.TrimSuffix(s.ownership.Name(), ".owner.lock")
	mustMerge(t, s.Close())
	reopened, err := Open(path)
	mustMerge(t, err)
	defer reopened.Close()
	query.Cursor = first.NextCursor
	if _, err = reopened.UsageEvents(ctx, query); err == nil {
		t.Fatal("cursor survived a different database owner epoch")
	}
}

func TestUsageEventsChannelsVersionsExposureAndUnknownCoverage(t *testing.T) {
	s, c, v := usageFixture(t)
	ctx := context.Background()
	read := usageObservation(c, v, "read")
	receipt, err := s.RecordUsage(ctx, read)
	mustMerge(t, err)
	adopt := usageObservation(c, v, "adopt")
	adopt.Exposure = receipt.ID
	_, err = s.RecordUsage(ctx, adopt)
	mustMerge(t, err)
	for _, channel := range []string{"app", "verification", "background"} {
		o := usageObservation(c, v, "read")
		o.Channel = channel
		if channel == "app" {
			o.Kind = "browse"
			o.ConnectionID = ""
			o.Generation = 0
		}
		if channel == "background" {
			o.ConnectionID = ""
			o.Generation = 0
		}
		_, err = s.RecordUsage(ctx, o)
		mustMerge(t, err)
	}
	v2, err := s.Apply(ctx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: v.Revision, Text: "synthetic edited version"})
	mustMerge(t, err)
	_, err = s.RecordUsage(ctx, usageObservation(c, v2, "read"))
	mustMerge(t, err)
	_, err = s.DB.Exec(`UPDATE usage_coverage SET started_ms=0`)
	mustMerge(t, err)
	page, err := s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: v2.Revision})
	mustMerge(t, err)
	if len(page.Events) != 3 || page.Channel != "daily" || page.CoverageStartedMS != 0 || page.RetentionDays != 90 || page.CurrentRevision != v2.Revision {
		t.Fatal("daily mixed channels or invented coverage", page)
	}
	versions := map[int64]bool{}
	foundExposure := false
	for _, event := range page.Events {
		versions[event.Revision] = true
		if event.Kind == "adopt" {
			foundExposure = event.Exposure == receipt.ID
		}
	}
	if !versions[v.Revision] || !versions[v2.Revision] || !foundExposure {
		t.Fatal("historical version/exposure lost")
	}
	for _, channel := range []string{"app", "verification", "background"} {
		filtered, err := s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Channel: channel})
		mustMerge(t, err)
		if len(filtered.Events) != 1 || filtered.Events[0].Channel != channel {
			t.Fatal("channel not independent", filtered)
		}
	}
	var coverage int64
	mustMerge(t, s.DB.QueryRow(`SELECT started_ms FROM usage_coverage WHERE id=1`).Scan(&coverage))
	if coverage != 0 {
		t.Fatal("read migrated unknown coverage")
	}
	missing, err := s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Kind: "return"})
	mustMerge(t, err)
	if !reflect.DeepEqual(missing.Events, []records.UsageEvent{}) || missing.HasMore {
		t.Fatal("known empty page was not explicit empty array")
	}
	for _, limit := range []int{-1, 51} {
		if _, err = s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Limit: limit}); err == nil {
			t.Fatal("invalid bound accepted")
		}
	}
}

func TestUsageEventsCurrentScopeFenceAndRetentionDoNotMutateHistory(t *testing.T) {
	s, c, v := usageFixture(t)
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		_, err := s.RecordUsage(ctx, usageObservation(c, v, "read"))
		mustMerge(t, err)
	}
	page, err := s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Limit: 1})
	mustMerge(t, err)
	if !page.HasMore {
		t.Fatal("fixture pagination missing")
	}
	_, err = s.DB.Exec(`UPDATE usage_events SET observed_ms=?`, time.Now().Add(-91*24*time.Hour).UnixMilli())
	mustMerge(t, err)
	empty, err := s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory})
	mustMerge(t, err)
	if len(empty.Events) != 0 {
		t.Fatal("expired event returned")
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM usage_events`).Scan(&count))
	if count != 3 {
		t.Fatal("read performed retention cleanup")
	}
	other := uuid.NewString()
	_, err = s.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','synthetic moved scope')`, other)
	mustMerge(t, err)
	// This explicit SQL scope-fence fixture follows the existing Usage tests;
	// it is not evidence of a user-facing move operation.
	_, err = s.DB.Exec(`UPDATE memories SET dataset=? WHERE id=?`, other, v.Memory)
	mustMerge(t, err)
	if _, err = s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Cursor: page.NextCursor, Limit: 1}); err == nil {
		t.Fatal("old scope cursor survived move")
	}
	if _, err = s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Dataset: v.Dataset}); err == nil {
		t.Fatal("old detail target received another scope")
	}
	empty, err = s.UsageEvents(ctx, records.UsageEventsQuery{Memory: v.Memory, Dataset: other})
	mustMerge(t, err)
	if len(empty.Events) != 0 {
		t.Fatal("old project events leaked across move")
	}
}
