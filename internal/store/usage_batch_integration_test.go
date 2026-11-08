package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

// A SQL failure in the second channel previously left daily success statistics
// from a search that returned an error. This exercises a real SQLite rollback.
func TestMixedUsageSecondChannelFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	daily := usageObservation(c, v, "return")
	verification := usageObservation(c, v, "return")
	verification.Channel = "verification"
	batch := []records.UsageObservation{daily, verification}
	_, err := s.DB.Exec(`CREATE TRIGGER synthetic_second_channel_failure BEFORE INSERT ON usage_events WHEN NEW.channel='verification' BEGIN SELECT RAISE(ABORT,'synthetic second channel storage failure'); END`)
	mustMerge(t, err)
	if receipts, err := s.RecordUsageBatch(ctx, batch); err == nil || receipts != nil {
		t.Fatal("partial successful receipt after injected failure", receipts, err)
	}
	for _, table := range []string{"usage_operations", "usage_events"} {
		var count int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		if count != 0 {
			t.Fatalf("%s retained partial failed return: %d", table, count)
		}
	}
	current, err := s.Connection(ctx, c.ID)
	mustMerge(t, err)
	if current.LastSeen != "" {
		t.Fatal("failed batch marked connection active", current)
	}
	_, err = s.DB.Exec(`DROP TRIGGER synthetic_second_channel_failure`)
	mustMerge(t, err)
	receipts, err := s.RecordUsageBatch(ctx, batch)
	mustMerge(t, err)
	replay, err := s.RecordUsageBatch(ctx, batch)
	mustMerge(t, err)
	if len(receipts) != 2 || !reflect.DeepEqual(receipts, replay) {
		t.Fatal("batch replay changed full receipts", receipts, replay)
	}
	usage, err := s.Usage(ctx, v.Memory)
	mustMerge(t, err)
	if usage.Daily.Returns != 1 || usage.Verification.Returns != 1 {
		t.Fatal("batch channels or replay counts", usage)
	}
	mustMerge(t, s.Disconnect(ctx, c.ID))
	if _, err = s.RecordUsageBatch(ctx, batch); err == nil {
		t.Fatal("revoked batch replay succeeded")
	}
}
