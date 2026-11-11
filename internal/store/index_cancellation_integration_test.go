package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func indexCancelFixture(t *testing.T) (*Store, string, []records.Version) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	mustMerge(t, err)
	t.Cleanup(func() { s.Close() })
	var scope string
	mustMerge(t, s.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope))
	items := []records.Version{}
	for _, text := range []string{"synthetic first index", "synthetic second index"} {
		v, err := s.Apply(context.Background(), Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: text})
		mustMerge(t, err)
		mustMerge(t, s.MarkIndexed(context.Background(), v.Record, engine.Receipt{Generation: "before", ChunkIDs: []string{uuid.NewString()}}))
		items = append(items, v)
	}
	return s, scope, items
}
func indexReceipt() engine.Receipt {
	return engine.Receipt{Generation: "explicit index test", ChunkIDs: []string{uuid.NewString()}}
}

func TestIndexRebuildCancelLateReceiptsAndRetryExactUnfinished(t *testing.T) {
	ctx := context.Background()
	s, scope, items := indexCancelFixture(t)
	versions := sqlRows(t, s.DB, "versions")
	activities := sqlRows(t, s.DB, "activities")
	task, err := s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope})
	mustMerge(t, err)
	completed, err := s.ClaimIndex(ctx, items[0].Memory, 1)
	mustMerge(t, err)
	mustMerge(t, s.MarkIndexed(completed, items[0].Record, indexReceipt()))
	late, err := s.ClaimIndex(ctx, items[1].Memory, 1)
	mustMerge(t, err)
	cancelled, err := s.CancelIndexRebuild(ctx, task.ID)
	mustMerge(t, err)
	if cancelled.State != "cancelled" {
		t.Fatal(cancelled)
	}
	if err = s.MarkIndexed(late, items[1].Record, indexReceipt()); !errors.Is(err, ErrConflict) {
		t.Fatal("late cancelled success accepted", err)
	}
	if err = s.IndexFailed(late, items[1].Record, errors.New("late worker failure")); !errors.Is(err, ErrConflict) {
		t.Fatal("late cancelled failure accepted", err)
	}
	retryStart := records.IndexRebuildStart{OperationID: uuid.NewString(), RetryOf: task.ID}
	retry, err := s.StartIndexRebuild(ctx, retryStart)
	mustMerge(t, err)
	if len(retry.Items) != 1 || retry.Items[0].Memory != items[1].Memory {
		t.Fatal("retry included completed or unrelated versions", retry)
	}
	current, err := s.ClaimIndex(ctx, items[1].Memory, 1)
	mustMerge(t, err)
	if err = s.MarkIndexed(late, items[1].Record, indexReceipt()); !errors.Is(err, ErrConflict) {
		t.Fatal("old attempt resurrected after retry", err)
	}
	mustMerge(t, s.MarkIndexed(current, items[1].Record, indexReceipt()))
	replay, err := s.StartIndexRebuild(ctx, retryStart)
	mustMerge(t, err)
	if replay.State != "completed" {
		t.Fatal(replay)
	}
	original, err := s.IndexRebuild(ctx, task.ID)
	mustMerge(t, err)
	if original.State != "cancelled" {
		t.Fatal("retry rewrote historical cancellation", original)
	}
	if !reflect.DeepEqual(versions, sqlRows(t, s.DB, "versions")) || !reflect.DeepEqual(activities, sqlRows(t, s.DB, "activities")) {
		t.Fatal("index maintenance changed knowledge/history")
	}
}

func TestIndexRebuildCancelPreservesOrdinaryAndSharedJobs(t *testing.T) {
	ctx := context.Background()
	s, scope, items := indexCancelFixture(t)
	mustMerge(t, s.Requeue(ctx, items[0].Memory, 1)) // independent ordinary save/retry
	ordinary, err := s.ClaimIndex(ctx, items[0].Memory, 1)
	mustMerge(t, err)
	first, err := s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope})
	mustMerge(t, err)
	shared, err := s.ClaimIndex(ctx, items[1].Memory, 1)
	mustMerge(t, err)
	second, err := s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope})
	mustMerge(t, err)
	_, err = s.CancelIndexRebuild(ctx, first.ID)
	mustMerge(t, err)
	mustMerge(t, s.MarkIndexed(ordinary, items[0].Record, indexReceipt()))
	mustMerge(t, s.MarkIndexed(shared, items[1].Record, indexReceipt()))
	other, err := s.IndexRebuild(ctx, second.ID)
	mustMerge(t, err)
	if other.State != "completed" {
		t.Fatal("cancelled shared job used by another rebuild", other)
	}
	original, err := s.IndexRebuild(ctx, first.ID)
	mustMerge(t, err)
	if original.State != "cancelled" {
		t.Fatal(original)
	}
}

func TestIndexRebuildSingleRetryAndRestartRetireOldAttempt(t *testing.T) {
	ctx := context.Background()
	s, scope, items := indexCancelFixture(t)
	task, err := s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope})
	mustMerge(t, err)
	old, err := s.ClaimIndex(ctx, items[0].Memory, 1)
	mustMerge(t, err)
	_, err = s.CancelIndexRebuild(ctx, task.ID)
	mustMerge(t, err)
	mustMerge(t, s.Requeue(ctx, items[0].Memory, 1))
	current, err := s.ClaimIndex(ctx, items[0].Memory, 1)
	mustMerge(t, err)
	if err = s.MarkIndexed(old, items[0].Record, indexReceipt()); !errors.Is(err, ErrConflict) {
		t.Fatal("old attempt overwrote single retry", err)
	}
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	mustMerge(t, s.Close())
	reopened, err := Open(path)
	mustMerge(t, err)
	defer reopened.Close()
	restarted, err := reopened.ClaimIndex(ctx, items[0].Memory, 1)
	mustMerge(t, err)
	if err = reopened.MarkIndexed(current, items[0].Record, indexReceipt()); !errors.Is(err, ErrConflict) {
		t.Fatal("pre-restart token remained live", err)
	}
	mustMerge(t, reopened.MarkIndexed(restarted, items[0].Record, indexReceipt()))
}

func TestIndexRebuildRestoreClearsAttemptOwnersAndCancelledJobs(t *testing.T) {
	ctx := context.Background()
	s, scope, items := indexCancelFixture(t)
	task, err := s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope})
	mustMerge(t, err)
	_, err = s.ClaimIndex(ctx, items[0].Memory, 1)
	mustMerge(t, err)
	_, err = s.CancelIndexRebuild(ctx, task.ID)
	mustMerge(t, err)
	_, snapshot, err := s.snapshot(ctx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	prepared[18], prepared[19] = 1, 1
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	db, err := sql.Open("sqlite", path)
	mustMerge(t, err)
	defer db.Close()
	var owners, cancelled int
	mustMerge(t, db.QueryRow(`SELECT count(*) FROM index_attempts`).Scan(&owners))
	mustMerge(t, db.QueryRow(`SELECT count(*) FROM jobs WHERE state='cancelled'`).Scan(&cancelled))
	if owners != 0 || cancelled != 0 {
		t.Fatal("restore retained old operational authority", owners, cancelled)
	}
	var pending int
	mustMerge(t, db.QueryRow(`SELECT count(*) FROM jobs WHERE state='pending'`).Scan(&pending))
	if pending != len(items) {
		t.Fatal("restore did not rebuild all current versions", pending)
	}
}
