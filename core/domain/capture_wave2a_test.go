package domain

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSchemaV4ToV5MigrationAndSessionFlushJobs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v4.sqlite3")
	db, err := OpenPrivateSQLite(path)
	must(t, err)
	v4Capture := strings.ReplaceAll(captureDDL, captureInboxV5IndexesDDL+sessionFlushDDL, "")
	v4Schema := strings.ReplaceAll(schemaDDL, captureDDL, v4Capture)
	must(t, execSQL(ctx, db, v4Schema+`
		INSERT INTO schema_meta VALUES(1,4,'created','updated');
		INSERT INTO projects(id,name,dataset_id) VALUES('p1','Project 1','ds-1');
	`))
	must(t, db.Close())

	for range 2 {
		store, err := OpenStore(path)
		must(t, err)
		version, err := store.SchemaVersion()
		must(t, err)
		if version != SchemaVersion {
			t.Fatalf("migrated version = %d, want %d", version, SchemaVersion)
		}
		must(t, store.Close())
	}

	s, err := OpenStore(path)
	must(t, err)
	defer func() { must(t, s.Close()) }()

	// Enqueue, dedupe, claim, fail, retry, succeed, re-enqueue.
	job, queued, err := s.EnqueueSessionFlushJob(ctx, "p1", "ds-1", "sess-1")
	must(t, err)
	if !queued || job.Status != "pending" || job.ProjectID != "p1" || !strings.HasPrefix(job.ID, "flush:") {
		t.Fatalf("first enqueue = (%+v, %v)", job, queued)
	}
	dup, queued, err := s.EnqueueSessionFlushJob(ctx, "p1", "ds-1", "sess-1")
	must(t, err)
	if queued || dup.ID != job.ID {
		t.Fatalf("duplicate pending enqueue = (%+v, %v)", dup, queued)
	}

	// An in-flight or failed inbox item for the same session must block claiming its flush job.
	inboxItem, err := s.EnqueueInboxItem(ctx, CaptureInboxItem{
		ID: "in-1", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
		SessionID: "sess-1", MessageID: "m1", Kind: "capture_trace", PayloadJSON: `{"content":{}}`,
	})
	must(t, err)
	_, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	wantError(t, err, ErrNotFound)

	// Mark inbox item failed: flush must still wait until failed item is retried & succeeded.
	_, err = s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.MarkInboxFailed(ctx, inboxItem.ID, "trace failed"))
	_, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	wantError(t, err, ErrNotFound)

	// Retry and succeed the inbox item; now flush job is claimable (unless dataset excluded).
	_, err = s.RetryInboxItem(ctx, inboxItem.ID)
	must(t, err)
	_, err = s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.MarkInboxSucceeded(ctx, inboxItem.ID))

	_, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, []string{"ds-1"})
	wantError(t, err, ErrNotFound)

	claimed, err := s.ClaimNextSessionFlushExcludingDatasets(ctx, []string{"ds-other"})
	must(t, err)
	if claimed.ID != job.ID || claimed.Status != "processing" || claimed.Attempts != 1 {
		t.Fatalf("claimed flush = %+v", claimed)
	}

	// RecoverStaleProcessing transitions processing -> pending.
	recInbox, recFlush, err := s.RecoverStaleProcessing(ctx)
	must(t, err)
	if recInbox != 0 || recFlush != 1 {
		t.Fatalf("recovered = (%d, %d), want (0, 1)", recInbox, recFlush)
	}
	claimed, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	must(t, err)
	failedRequeued, err := s.MarkSessionFlushFailed(ctx, claimed.ID, "summary failed")
	must(t, err)
	if failedRequeued {
		t.Fatal("expected failedRequeued=false without rerun_requested")
	}
	failed, err := s.GetSessionFlushJob(ctx, "ds-1", "sess-1")
	must(t, err)
	if failed.Status != "failed" || failed.LastError != "summary failed" || failed.LastErrorAt == "" {
		t.Fatalf("failed flush = %+v", failed)
	}

	retried, err := s.RetrySessionFlushJob(ctx, claimed.ID)
	must(t, err)
	if retried.Status != "pending" || retried.LastError != "" {
		t.Fatalf("retried flush = %+v", retried)
	}
	claimed, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	must(t, err)
	requeuedFlag, err := s.MarkSessionFlushSucceeded(ctx, claimed.ID)
	must(t, err)
	if requeuedFlag {
		t.Fatal("expected requeued=false when rerun was not requested")
	}

	// New session-end after succeeded re-enqueues cleanly.
	requeued, queued, err := s.EnqueueSessionFlushJob(ctx, "p1", "ds-1", "sess-1")
	must(t, err)
	if !queued || requeued.Status != "pending" || requeued.ID != job.ID {
		t.Fatalf("re-enqueued flush = (%+v, %v)", requeued, queued)
	}

	// Enqueue while processing sets rerun_requested=1 and re-queues atomically on completion.
	claimed, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	must(t, err)
	inFlight, queued, err := s.EnqueueSessionFlushJob(ctx, "p1", "ds-1", "sess-1")
	must(t, err)
	if queued || !inFlight.RerunRequested || inFlight.Status != "processing" {
		t.Fatalf("in-flight enqueue = (%+v, %v)", inFlight, queued)
	}
	requeuedFlag, err = s.MarkSessionFlushSucceeded(ctx, claimed.ID)
	must(t, err)
	if !requeuedFlag {
		t.Fatal("expected requeued=true when rerun_requested=1")
	}
	pendingAgain, err := s.GetSessionFlushJob(ctx, "ds-1", "sess-1")
	must(t, err)
	if pendingAgain.Status != "pending" || pendingAgain.RerunRequested {
		t.Fatalf("expected pending with rerun_requested=false, got %+v", pendingAgain)
	}
	claimed, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	must(t, err)
	requeuedFlag, err = s.MarkSessionFlushSucceeded(ctx, claimed.ID)
	must(t, err)
	if requeuedFlag {
		t.Fatal("expected final completion requeued=false")
	}
}

func TestReserveAndEnqueueCaptureAtomicAndNoFakeInboxID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	baseReceipt := CaptureReceipt{
		CaptureKey: "ck-1", UserID: "u1", DatasetID: "ds-1", SessionID: "s-1",
		MessageID: "m-1", PayloadSHA256: "sha-1", EntryType: "capture_qa",
	}
	baseInbox := CaptureInboxItem{
		ID: "inbox-1", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
		SessionID: "s-1", MessageID: "m-1", CaptureKey: "ck-1", Kind: "capture_qa",
		PayloadJSON: `{"content":{"question":"q","answer":"a"}}`,
	}

	// Self-parent relation failure must roll back receipt, binding, relation, and inbox atomically.
	_, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: baseReceipt,
		Inbox:   baseInbox,
		SessionBinding: &SessionBinding{
			SessionKey: `["agent", "s-1"]`, ProjectID: "p1", UpdatedAt: 100,
		},
		SessionRelation: &SessionRelation{
			DatasetID: "ds-1", SessionID: "s-1", ParentSessionID: "s-1",
		},
	})
	wantError(t, err, ErrInvalidArgument)
	_, err = s.GetCaptureReceipt(ctx, "ck-1")
	wantError(t, err, ErrNotFound)
	_, err = s.GetSessionBinding(ctx, `["agent", "s-1"]`)
	wantError(t, err, ErrNotFound)
	_, err = s.GetInboxItem(ctx, "inbox-1")
	wantError(t, err, ErrNotFound)

	// Valid single-transaction enqueue persists all four tables atomically.
	res, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: baseReceipt,
		Inbox:   baseInbox,
		SessionBinding: &SessionBinding{
			SessionKey: `["agent", "s-1"]`, ProjectID: "p1", UpdatedAt: 100,
		},
		SessionRelation: &SessionRelation{
			DatasetID: "ds-1", SessionID: "s-1", ParentSessionID: "parent-1", SubagentRole: "worker",
		},
	})
	must(t, err)
	if res.Deduplicated || !res.HasInboxItem || res.InboxItem.ID != "inbox-1" || res.InboxItem.Status != "pending" {
		t.Fatalf("unexpected enqueue result: %+v", res)
	}

	// Duplicate with matching scope/payload returns the REAL inbox item ID & status.
	dup, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: baseReceipt,
		Inbox: CaptureInboxItem{
			ID: "different-proposed-id", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
			SessionID: "s-1", MessageID: "m-1", CaptureKey: "ck-1", Kind: "capture_qa",
			PayloadJSON: `{"content":{"question":"q","answer":"a"}}`,
		},
	})
	must(t, err)
	if !dup.Deduplicated || !dup.HasInboxItem || dup.InboxItem.ID != "inbox-1" {
		t.Fatalf("duplicate must return real DB inbox_id, got: %+v", dup)
	}

	// Duplicate with changed payload returns ErrConflict.
	changed := baseReceipt
	changed.PayloadSHA256 = "sha-changed"
	_, err = s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{Receipt: changed, Inbox: baseInbox})
	wantError(t, err, ErrConflict)

	// Legacy v4 pending receipt without a capture_inbox row must be repaired atomically.
	legacyPending := CaptureReceipt{
		CaptureKey: "ck-legacy-pending", UserID: "u1", DatasetID: "ds-1", SessionID: "s-legacy-p",
		MessageID: "m-legacy-p", PayloadSHA256: "sha-legacy-p", EntryType: "capture_qa",
	}
	_, _, err = s.ReserveCaptureReceipt(ctx, legacyPending)
	must(t, err)
	repaired, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: legacyPending,
		Inbox: CaptureInboxItem{
			ID: "repaired-inbox-id", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
			SessionID: "s-legacy-p", MessageID: "m-legacy-p", CaptureKey: "ck-legacy-pending", Kind: "capture_qa",
			PayloadJSON: `{"content":{"question":"q","answer":"a"}}`,
		},
	})
	must(t, err)
	if repaired.Deduplicated || !repaired.HasInboxItem || repaired.InboxItem.ID != "repaired-inbox-id" {
		t.Fatalf("legacy pending receipt was not repaired: %+v", repaired)
	}

	// Legacy saved receipt without a corresponding capture_inbox row must NOT return a fake inbox_id.
	legacySaved := CaptureReceipt{
		CaptureKey: "ck-legacy-saved", UserID: "u1", DatasetID: "ds-1", SessionID: "s-legacy-s",
		MessageID: "m-legacy-s", PayloadSHA256: "sha-legacy-s", EntryType: "capture_qa",
	}
	_, _, err = s.ReserveCaptureReceipt(ctx, legacySaved)
	must(t, err)
	must(t, s.MarkCaptureReceiptSaved(ctx, legacySaved.CaptureKey, "saved-entry-1"))
	legacyDup, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: legacySaved,
		Inbox: CaptureInboxItem{
			ID: "fake-inbox-id", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
			SessionID: "s-legacy-s", MessageID: "m-legacy-s", CaptureKey: "ck-legacy-saved", Kind: "capture_qa",
			PayloadJSON: `{"content":{"question":"q","answer":"a"}}`,
		},
	})
	must(t, err)
	if !legacyDup.Deduplicated || legacyDup.HasInboxItem || legacyDup.InboxItem.ID != "" {
		t.Fatalf("legacy saved duplicate fabricated inbox_id: %+v", legacyDup)
	}

	// Failure at the final step inside the transaction (capture_inbox ID conflict with different payload)
	// must roll back earlier capture_receipts, session_bindings, and session_relations writes.
	_, err = s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: CaptureReceipt{
			CaptureKey: "ck-rollback-end", UserID: "u1", DatasetID: "ds-1", SessionID: "s-rollback",
			MessageID: "m-rollback", PayloadSHA256: "sha-rollback", EntryType: "capture_qa",
		},
		Inbox: CaptureInboxItem{
			ID: "inbox-1", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
			SessionID: "s-rollback", MessageID: "m-rollback", CaptureKey: "ck-rollback-end", Kind: "capture_qa",
			PayloadJSON: `{"content":{"question":"different","answer":"payload"}}`,
		},
		SessionBinding: &SessionBinding{
			SessionKey: `["agent", "s-rollback"]`, ProjectID: "p1", UpdatedAt: 200,
		},
		SessionRelation: &SessionRelation{
			DatasetID: "ds-1", SessionID: "s-rollback", ParentSessionID: "parent-rb", SubagentRole: "worker",
		},
	})
	wantError(t, err, ErrConflict)
	_, err = s.GetCaptureReceipt(ctx, "ck-rollback-end")
	wantError(t, err, ErrNotFound)
	_, err = s.GetSessionBinding(ctx, `["agent", "s-rollback"]`)
	wantError(t, err, ErrNotFound)
	rels, err := s.ListSessionRelations(ctx, "ds-1")
	must(t, err)
	for _, rel := range rels {
		if rel.SessionID == "s-rollback" {
			t.Fatalf("session relation leaked despite capture_inbox rollback: %+v", rel)
		}
	}
}

func TestPruneSucceededInboxPayloads(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"done-1", "pending-1"} {
		_, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
			Receipt: CaptureReceipt{
				CaptureKey: "ck-" + id, UserID: "u", DatasetID: "ds", SessionID: "s",
				MessageID: id, PayloadSHA256: "sha-" + id, EntryType: "capture_qa",
			},
			Inbox: CaptureInboxItem{
				ID: id, ProjectID: "p", DatasetID: "ds", AgentID: "agent",
				SessionID: "s", MessageID: id, CaptureKey: "ck-" + id, Kind: "capture_qa",
				PayloadJSON: `{"content":{"question":"large","answer":"payload"}}`,
			},
		})
		must(t, err)
	}
	claimed, err := s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.MarkCaptureReceiptSaved(ctx, claimed.CaptureKey, "entry-done-1"))
	must(t, s.MarkInboxSucceeded(ctx, claimed.ID))

	pruned, err := s.PruneSucceededInboxPayloads(ctx, "")
	must(t, err)
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	doneItem, err := s.GetInboxItem(ctx, claimed.ID)
	must(t, err)
	if doneItem.PayloadJSON != "{}" || doneItem.Status != "succeeded" {
		t.Fatalf("pruned item = %+v", doneItem)
	}
	receipt, err := s.GetCaptureReceipt(ctx, claimed.CaptureKey)
	must(t, err)
	if receipt.Status != "saved" || receipt.EntryID != "entry-done-1" {
		t.Fatalf("receipt altered by prune: %+v", receipt)
	}
	pendingItem, err := s.GetInboxItem(ctx, "pending-1")
	must(t, err)
	if pendingItem.PayloadJSON == "{}" {
		t.Fatalf("pending item payload was pruned: %+v", pendingItem)
	}
}

func TestConcurrentSQLiteWritesNoBusy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for worker := range 6 {
		wg.Go(func() {
			for i := range 15 {
				tag := fmt.Sprintf("w%d-%d", worker, i)
				if _, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
					Receipt: CaptureReceipt{
						CaptureKey: "ck-" + tag, UserID: "u", DatasetID: fmt.Sprintf("ds-%d", worker),
						SessionID: "s-" + tag, MessageID: "m-" + tag, PayloadSHA256: "sha-" + tag, EntryType: "capture_trace",
					},
					Inbox: CaptureInboxItem{
						ID: "in-" + tag, ProjectID: "p", DatasetID: fmt.Sprintf("ds-%d", worker), AgentID: "agent",
						SessionID: "s-" + tag, MessageID: "m-" + tag, CaptureKey: "ck-" + tag, Kind: "capture_trace",
						PayloadJSON: `{"content":{"origin_function":"Read"}}`,
					},
					SessionBinding: &SessionBinding{SessionKey: "bind-" + tag, ProjectID: "p", UpdatedAt: float64(i)},
				}); err != nil {
					errs <- err
					return
				}
				if err := s.RecordRecallQueryStat(ctx, "automatic", 1, ""); err != nil {
					errs <- err
					return
				}
				if err := s.UpsertUserProfile(ctx, UserProfile{InstanceID: "prof-" + tag, Status: "ready"}); err != nil {
					errs <- err
					return
				}
				if err := s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: fmt.Sprintf("ds-%d", worker), DocID: "doc-" + tag, Kind: "knowledge"}); err != nil {
					errs <- err
					return
				}
				if err := s.DeleteCatalogDoc(ctx, fmt.Sprintf("ds-%d", worker), "doc-"+tag); err != nil {
					errs <- err
					return
				}
				if _, _, err := s.EnqueueSessionFlushJob(ctx, "p", fmt.Sprintf("ds-%d", worker), "s-"+tag); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent SQLite write failed: %v", err)
	}
}

func TestRecoverStaleProcessingBeforeAndFlushFailedRerunAndQueueOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 1. RecoverStaleProcessingBefore must reset rows updated at/before cutoff and leave newer processing rows intact.
	_, err := s.EnqueueInboxItem(ctx, CaptureInboxItem{
		ID: "stale-in", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
		SessionID: "s1", MessageID: "m1", Kind: "capture_qa", PayloadJSON: `{"content":{}}`,
	})
	must(t, err)
	_, err = s.ClaimNextInboxItem(ctx)
	must(t, err)
	cutoff := NowUTCMicro()
	// Simulate a newer processing row created AFTER Start (e.g. MCP fast-path) with a future updated_at.
	_, err = s.EnqueueInboxItem(ctx, CaptureInboxItem{
		ID: "fresh-in", ProjectID: "p1", DatasetID: "ds-1", AgentID: "agent",
		SessionID: "s2", MessageID: "m2", Kind: "capture_qa", PayloadJSON: `{"content":{}}`,
	})
	must(t, err)
	_, err = s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `UPDATE capture_inbox SET updated_at='9999-12-31T23:59:59.000000+00:00' WHERE id='fresh-in'`)
	}))

	recInbox, recFlush, err := s.RecoverStaleProcessingBefore(ctx, cutoff)
	must(t, err)
	if recInbox != 1 || recFlush != 0 {
		t.Fatalf("RecoverStaleProcessingBefore = (%d, %d), want (1, 0)", recInbox, recFlush)
	}
	staleItem, err := s.GetInboxItem(ctx, "stale-in")
	must(t, err)
	freshItem, err := s.GetInboxItem(ctx, "fresh-in")
	must(t, err)
	if staleItem.Status != "pending" || freshItem.Status != "processing" {
		t.Fatalf("unexpected statuses: stale=%s fresh=%s", staleItem.Status, freshItem.Status)
	}

	// 2. MarkSessionFlushFailed with rerun_requested=1 must requeue to pending (not drop to failed).
	jobA, _, err := s.EnqueueSessionFlushJob(ctx, "p1", "ds-order", "sess-a")
	must(t, err)
	jobB, _, err := s.EnqueueSessionFlushJob(ctx, "p1", "ds-order", "sess-b")
	must(t, err)
	// Ensure deterministic updated_at ordering: sess-a earlier than sess-b.
	must(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := execSQL(ctx, tx, `UPDATE session_flush_jobs SET created_at='2026-01-01T00:00:01.000000+00:00',updated_at='2026-01-01T00:00:01.000000+00:00' WHERE id=?`, jobA.ID); err != nil {
			return err
		}
		return execSQL(ctx, tx, `UPDATE session_flush_jobs SET created_at='2026-01-01T00:00:02.000000+00:00',updated_at='2026-01-01T00:00:02.000000+00:00' WHERE id=?`, jobB.ID)
	}))

	claimed, err := s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-order"})
	must(t, err)
	if claimed.ID != jobA.ID {
		t.Fatalf("first claimed = %s, want %s", claimed.ID, jobA.ID)
	}
	// While sess-a is processing, a second /session-end arrives -> rerun_requested=1.
	_, _, err = s.EnqueueSessionFlushJob(ctx, "p1", "ds-order", "sess-a")
	must(t, err)
	// First flush fails -> must requeue sess-a to pending (at tail of updated_at), NOT leave it failed.
	requeued, err := s.MarkSessionFlushFailed(ctx, jobA.ID, "transient error")
	must(t, err)
	if !requeued {
		t.Fatal("expected MarkSessionFlushFailed to requeue when rerun_requested=1")
	}
	// 3. Requeued sess-a has newer updated_at, so sess-b MUST be claimed next (no starvation by sess-a's older created_at!).
	nextClaimed, err := s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-order"})
	must(t, err)
	if nextClaimed.ID != jobB.ID {
		t.Fatalf("expected sess-b (%s) next after sess-a requeue, got %s", jobB.ID, nextClaimed.ID)
	}
}
