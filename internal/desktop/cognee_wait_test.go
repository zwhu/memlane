package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

// Transport lifecycle regression, not evidence of official persistence.
func TestAwaitCogneeMaterialRequiresPersistenceAndCompletedLearning(t *testing.T) {
	query := records.CogneeSync{Project: "approved", Session: "actual"}
	steps := []records.CogneeSyncReceipt{
		{OfficialState: "awaiting_official_sync"},
		{OfficialState: "persisted", Raw: make([]records.SaveReceipt, 9)},
		{OfficialState: "persisted", Raw: make([]records.SaveReceipt, 9), PendingDocuments: 1},
		{OfficialState: "persisted", Raw: make([]records.SaveReceipt, 9), Knowledge: make([]records.SaveReceipt, 1)},
	}
	reads := 0
	got, err := awaitCogneeMaterial(context.Background(), query, func(_ context.Context, q records.CogneeSync) (records.CogneeSyncReceipt, error) {
		if q != query {
			t.Fatal("scope changed")
		}
		v := steps[reads]
		reads++
		return v, nil
	}, 0, nil)
	if err != nil || reads != 4 || len(got.Knowledge) != 1 {
		t.Fatalf("premature completion: reads=%d receipt=%+v err=%v", reads, got, err)
	}
}

func TestAwaitCogneeMaterialCancellationStopsFurtherReadsAndReturnsReceipt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	got, err := awaitCogneeMaterial(ctx, records.CogneeSync{}, func(context.Context, records.CogneeSync) (records.CogneeSyncReceipt, error) {
		reads++
		return records.CogneeSyncReceipt{OfficialState: "persisted", PendingDocuments: 1, Raw: make([]records.SaveReceipt, 9)}, nil
	}, 0, func(records.CogneeSyncReceipt) { cancel() })
	if !errors.Is(err, context.Canceled) || reads != 1 || len(got.Raw) != 9 {
		t.Fatalf("late read or lost receipt: reads=%d receipt=%+v err=%v", reads, got, err)
	}
}

func TestAwaitCogneeMaterialPendingStopsAfterFiniteReads(t *testing.T) {
	reads := 0
	got, err := awaitCogneeMaterial(context.Background(), records.CogneeSync{}, func(context.Context, records.CogneeSync) (records.CogneeSyncReceipt, error) {
		reads++
		return records.CogneeSyncReceipt{OfficialState: "awaiting_official_sync"}, nil
	}, 0, nil)
	if err != nil || reads != 30 || got.OfficialState != "awaiting_official_sync" {
		t.Fatalf("unbounded wait: %d %v", reads, err)
	}
}
