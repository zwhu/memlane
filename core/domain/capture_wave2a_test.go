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
	must(t, s.UpsertProject(ctx, Project{ID: "p1", Name: "Project 1", DatasetID: "ds-1"}))

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
	must(t, s.UpsertProject(ctx, Project{ID: "p", Name: "Project", DatasetID: "ds"}))

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
	for worker := range 6 {
		must(t, s.UpsertProject(ctx, Project{
			ID:        fmt.Sprintf("p-%d", worker),
			Name:      fmt.Sprintf("Project %d", worker),
			DatasetID: fmt.Sprintf("ds-%d", worker),
		}))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for worker := range 6 {
		wg.Go(func() {
			pid := fmt.Sprintf("p-%d", worker)
			ds := fmt.Sprintf("ds-%d", worker)
			for i := range 15 {
				tag := fmt.Sprintf("w%d-%d", worker, i)
				if _, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
					Receipt: CaptureReceipt{
						CaptureKey: "ck-" + tag, UserID: "u", DatasetID: ds,
						SessionID: "s-" + tag, MessageID: "m-" + tag, PayloadSHA256: "sha-" + tag, EntryType: "capture_trace",
					},
					Inbox: CaptureInboxItem{
						ID: "in-" + tag, ProjectID: pid, DatasetID: ds, AgentID: "agent",
						SessionID: "s-" + tag, MessageID: "m-" + tag, CaptureKey: "ck-" + tag, Kind: "capture_trace",
						PayloadJSON: `{"content":{"origin_function":"Read"}}`,
					},
					SessionBinding: &SessionBinding{SessionKey: "bind-" + tag, ProjectID: pid, UpdatedAt: float64(i)},
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
				if err := s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: ds, DocID: "doc-" + tag, Kind: "knowledge"}); err != nil {
					errs <- err
					return
				}
				if err := s.DeleteCatalogDoc(ctx, ds, "doc-"+tag); err != nil {
					errs <- err
					return
				}
				if _, _, err := s.EnqueueSessionFlushJob(ctx, pid, ds, "s-"+tag); err != nil {
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
	must(t, s.UpsertProject(ctx, Project{ID: "p1", Name: "Project 1", DatasetID: "ds-1"}))

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
	must(t, s.UpsertProject(ctx, Project{ID: "p-order", Name: "Order Project", DatasetID: "ds-order"}))
	jobA, _, err := s.EnqueueSessionFlushJob(ctx, "p-order", "ds-order", "sess-a")
	must(t, err)
	jobB, _, err := s.EnqueueSessionFlushJob(ctx, "p-order", "ds-order", "sess-b")
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
	_, _, err = s.EnqueueSessionFlushJob(ctx, "p-order", "ds-order", "sess-a")
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

func TestSchemaV5ToV6MigrationPreservesRowsAndAcceptsCancelledStatus(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v5.sqlite3")
	db, err := OpenPrivateSQLite(path)
	must(t, err)
	v5Schema := strings.ReplaceAll(schemaDDL,
		"CHECK(status IN ('pending','processing','succeeded','failed','cancelled_project_removed'))",
		"CHECK(status IN ('pending','processing','succeeded','failed'))")
	must(t, execSQL(ctx, db, v5Schema+`
		INSERT INTO schema_meta VALUES(1,5,'created','updated');
		INSERT INTO projects(id,name,dataset_id) VALUES('p1','Project 1','ds-1');
		INSERT INTO capture_inbox(id,project_id,dataset_id,agent_id,session_id,message_id,capture_key,kind,payload_json,status,last_error,last_error_at,attempts,created_at,updated_at)
			VALUES('in-keep','p1','ds-1','agent','s1','m1','ck1','capture_qa','{"k":"v"}','pending','','',0,'c1','u1');
		INSERT INTO session_flush_jobs(id,project_id,dataset_id,session_id,status,last_error,last_error_at,attempts,rerun_requested,created_at,updated_at)
			VALUES('fl-keep','p1','ds-1','s1','failed','boom','err-at',2,0,'c2','u2');
	`))
	must(t, db.Close())

	for range 2 {
		store, err := OpenStore(path)
		must(t, err)
		version, err := store.SchemaVersion()
		must(t, err)
		if version != 6 {
			t.Fatalf("migrated version = %d, want 6", version)
		}
		item, err := store.GetInboxItem(ctx, "in-keep")
		must(t, err)
		if item.PayloadJSON != `{"k":"v"}` || item.Status != "pending" {
			t.Fatalf("preserved inbox item altered: %+v", item)
		}
		flush, err := store.GetSessionFlushJob(ctx, "ds-1", "s1")
		must(t, err)
		if flush.ID != "fl-keep" || flush.Status != "failed" || flush.LastError != "boom" || flush.LastErrorAt != "err-at" || flush.Attempts != 2 {
			t.Fatalf("preserved flush job altered: %+v", flush)
		}
		must(t, store.Close())
	}

	s, err := OpenStore(path)
	must(t, err)
	defer func() { must(t, s.Close()) }()
	must(t, s.DeleteProject(ctx, "p1"))

	item, err := s.GetInboxItem(ctx, "in-keep")
	must(t, err)
	if item.Status != "cancelled_project_removed" {
		t.Fatalf("inbox status after delete = %q, want cancelled_project_removed", item.Status)
	}
	flush, err := s.GetSessionFlushJob(ctx, "ds-1", "s1")
	must(t, err)
	if flush.Status != "cancelled_project_removed" {
		t.Fatalf("flush status after delete = %q, want cancelled_project_removed", flush.Status)
	}
}

func TestDeleteProjectAtomicCleanupAndProtectedGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 1. Protected project cannot be deleted.
	must(t, s.UpsertProject(ctx, Project{ID: "ml-user-profiles", Name: "ml-user-profiles", DatasetID: "prof-ds", DatasetName: "ml-user-profiles"}))
	wantError(t, s.DeleteProject(ctx, "ml-user-profiles"), ErrForbidden)

	// 2. Normal project with catalog, session relations, ACL, bindings, aliases, and mixed inbox/flush statuses.
	aliasDir := t.TempDir()
	must(t, s.UpsertProject(ctx, Project{
		ID: "p-del", Name: "Delete Me", DatasetID: "ds-del", CodeDatasetID: "ds-code",
		Aliases: []string{aliasDir},
	}))
	must(t, s.UpsertAgent(ctx, Agent{ID: "agent-1", Name: "Agent 1"}))
	maintain := AccessMaintain
	must(t, s.SetACLOverride(ctx, ACLOverride{TargetKind: "project", TargetID: "p-del", AgentID: "agent-1", Access: &maintain}))
	must(t, s.UpsertSessionBinding(ctx, `["agent-1", "s1"]`, "p-del", 100))
	must(t, s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: "ds-del", DocID: "d1", Kind: "knowledge", IsKnowledge: true}))
	must(t, s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: "ds-code", DocID: "c1", Kind: "raw_doc"}))
	must(t, s.UpsertCatalogSession(ctx, CatalogSession{DatasetID: "ds-del", UserID: "u1", SessionID: "s1"}))
	must(t, s.UpsertCatalogSyncState(ctx, CatalogSyncState{DatasetID: "ds-del", CatalogVersion: "v1"}))
	must(t, s.UpsertSessionRelation(ctx, SessionRelation{DatasetID: "ds-del", SessionID: "s1", ParentSessionID: "root"}))

	// Enqueue 3 inbox items: pending, processing, failed; plus 2 flush jobs: pending and processing.
	for _, id := range []string{"in-pend", "in-proc", "in-fail"} {
		_, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
			Receipt: CaptureReceipt{CaptureKey: "ck-" + id, UserID: "u1", DatasetID: "ds-del", SessionID: "s-" + id, MessageID: id, PayloadSHA256: "sha-" + id, EntryType: "capture_qa"},
			Inbox:   CaptureInboxItem{ID: id, ProjectID: "p-del", DatasetID: "ds-del", AgentID: "agent-1", SessionID: "s-" + id, MessageID: id, CaptureKey: "ck-" + id, Kind: "capture_qa", PayloadJSON: `{}`},
		})
		must(t, err)
	}
	// Claim in-pend (make it failed), claim in-proc (leave processing), leave in-fail pending? Let's set statuses precisely.
	c1, err := s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.MarkInboxFailed(ctx, c1.ID, "err"))
	c2, err := s.ClaimNextInboxItem(ctx)
	must(t, err)
	// c2 stays processing, 3rd item stays pending.

	fl1, _, err := s.EnqueueSessionFlushJob(ctx, "p-del", "ds-del", "sf-proc")
	must(t, err)
	_, err = s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-del"})
	must(t, err)
	fl2, _, err := s.EnqueueSessionFlushJob(ctx, "p-del", "ds-del", "sf-pend")
	must(t, err)

	// DeleteProject atomically cleans everything except processing jobs.
	must(t, s.DeleteProject(ctx, "p-del"))

	_, err = s.GetProject(ctx, "p-del")
	wantError(t, err, ErrNotFound)
	_, err = s.GetSessionBinding(ctx, `["agent-1", "s1"]`)
	wantError(t, err, ErrNotFound)
	overrides, err := s.ListACLOverrides(ctx, "project", "p-del")
	must(t, err)
	if len(overrides) != 0 {
		t.Fatalf("ACL overrides leaked: %+v", overrides)
	}
	rels, err := s.ListSessionRelations(ctx, "ds-del")
	must(t, err)
	if len(rels) != 0 {
		t.Fatalf("session_relations leaked: %+v", rels)
	}
	var docsCount, sessCount, syncCount int
	must(t, s.DB().QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM catalog_docs WHERE dataset_id IN ('ds-del','ds-code')),
		(SELECT count(*) FROM catalog_sessions WHERE dataset_id='ds-del'),
		(SELECT count(*) FROM catalog_sync_state WHERE dataset_id='ds-del')`).Scan(&docsCount, &sessCount, &syncCount))
	if docsCount != 0 || sessCount != 0 || syncCount != 0 {
		t.Fatalf("catalog residue after delete: docs=%d sess=%d sync=%d", docsCount, sessCount, syncCount)
	}

	gotFailed, err := s.GetInboxItem(ctx, c1.ID)
	must(t, err)
	gotProc, err := s.GetInboxItem(ctx, c2.ID)
	must(t, err)
	gotPend, err := s.GetInboxItem(ctx, "in-fail")
	must(t, err)
	if gotFailed.Status != "cancelled_project_removed" || gotPend.Status != "cancelled_project_removed" || gotProc.Status != "processing" {
		t.Fatalf("unexpected inbox statuses after delete: failed=%s pend=%s proc=%s", gotFailed.Status, gotPend.Status, gotProc.Status)
	}
	gotFl1, err := s.GetSessionFlushJob(ctx, "ds-del", "sf-proc")
	must(t, err)
	gotFl2, err := s.GetSessionFlushJob(ctx, "ds-del", "sf-pend")
	must(t, err)
	if gotFl1.ID != fl1.ID || gotFl1.Status != "processing" || gotFl2.ID != fl2.ID || gotFl2.Status != "cancelled_project_removed" {
		t.Fatalf("unexpected flush statuses after delete: fl1=%+v fl2=%+v", gotFl1, gotFl2)
	}

	// 3. Enqueueing for a removed project is rejected inside WithTx.
	_, err = s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: CaptureReceipt{CaptureKey: "ck-after", UserID: "u1", DatasetID: "ds-del", SessionID: "s-after", MessageID: "m-after", PayloadSHA256: "sha-after", EntryType: "capture_qa"},
		Inbox:   CaptureInboxItem{ID: "in-after", ProjectID: "p-del", DatasetID: "ds-del", AgentID: "agent-1", SessionID: "s-after", MessageID: "m-after", CaptureKey: "ck-after", Kind: "capture_qa", PayloadJSON: `{}`},
	})
	wantError(t, err, ErrNotFound)
	_, _, err = s.EnqueueSessionFlushJob(ctx, "p-del", "ds-del", "sf-after")
	wantError(t, err, ErrNotFound)
}

func TestDeletedProjectProcessingTasksNeverRequeueOrClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	must(t, s.UpsertProject(ctx, Project{ID: "p-gone", Name: "Gone", DatasetID: "ds-gone"}))

	// Seed 2 processing inbox items (one for RequeueInboxItem, one for RecoverStaleProcessingBefore).
	for _, id := range []string{"in-requeue", "in-stale"} {
		_, err := s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
			Receipt: CaptureReceipt{CaptureKey: "ck-" + id, UserID: "u", DatasetID: "ds-gone", SessionID: "s-" + id, MessageID: id, PayloadSHA256: "sha-" + id, EntryType: "capture_qa"},
			Inbox:   CaptureInboxItem{ID: id, ProjectID: "p-gone", DatasetID: "ds-gone", AgentID: "agent", SessionID: "s-" + id, MessageID: id, CaptureKey: "ck-" + id, Kind: "capture_qa", PayloadJSON: `{}`},
		})
		must(t, err)
		_, err = s.ClaimNextInboxItem(ctx)
		must(t, err)
	}

	// Seed 4 processing flush jobs (with rerun_requested=1) to test DeleteProject, MarkSucceeded, MarkFailed, Requeue, and RecoverStale.
	flSucc, _, err := s.EnqueueSessionFlushJob(ctx, "p-gone", "ds-gone", "sf-succ")
	must(t, err)
	flFail, _, err := s.EnqueueSessionFlushJob(ctx, "p-gone", "ds-gone", "sf-fail")
	must(t, err)
	flReq, _, err := s.EnqueueSessionFlushJob(ctx, "p-gone", "ds-gone", "sf-req")
	must(t, err)
	flStale, _, err := s.EnqueueSessionFlushJob(ctx, "p-gone", "ds-gone", "sf-stale")
	must(t, err)
	for range 4 {
		_, err = s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-gone"})
		must(t, err)
	}
	// Trigger rerun_requested=1 while processing.
	for _, sid := range []string{"sf-succ", "sf-fail", "sf-req", "sf-stale"} {
		_, _, err = s.EnqueueSessionFlushJob(ctx, "p-gone", "ds-gone", sid)
		must(t, err)
	}

	// DeleteProject must clear rerun_requested=0 immediately.
	must(t, s.DeleteProject(ctx, "p-gone"))
	for _, sid := range []string{"sf-succ", "sf-fail", "sf-req", "sf-stale"} {
		got, err := s.GetSessionFlushJob(ctx, "ds-gone", sid)
		must(t, err)
		if got.RerunRequested {
			t.Fatalf("DeleteProject did not clear rerun_requested on %s: %+v", sid, got)
		}
	}

	// RequeueInboxItem -> cancelled_project_removed, never pending/claimable.
	must(t, s.RequeueInboxItem(ctx, "in-requeue"))
	gotInReq, err := s.GetInboxItem(ctx, "in-requeue")
	must(t, err)
	if gotInReq.Status != "cancelled_project_removed" {
		t.Fatalf("RequeueInboxItem status = %q, want cancelled_project_removed", gotInReq.Status)
	}

	// MarkSessionFlushSucceeded -> requeued=false, cancelled_project_removed.
	requeued, err := s.MarkSessionFlushSucceeded(ctx, flSucc.ID)
	must(t, err)
	if requeued {
		t.Fatal("MarkSessionFlushSucceeded must not requeue deleted project flush")
	}
	gotSucc, err := s.GetSessionFlushJob(ctx, "ds-gone", "sf-succ")
	must(t, err)
	if gotSucc.Status != "cancelled_project_removed" {
		t.Fatalf("MarkSessionFlushSucceeded status = %q", gotSucc.Status)
	}

	// MarkSessionFlushFailed -> requeued=false, cancelled_project_removed.
	requeued, err = s.MarkSessionFlushFailed(ctx, flFail.ID, "err")
	must(t, err)
	if requeued {
		t.Fatal("MarkSessionFlushFailed must not requeue deleted project flush")
	}
	gotFail, err := s.GetSessionFlushJob(ctx, "ds-gone", "sf-fail")
	must(t, err)
	if gotFail.Status != "cancelled_project_removed" {
		t.Fatalf("MarkSessionFlushFailed status = %q", gotFail.Status)
	}

	// RequeueSessionFlush -> cancelled_project_removed.
	must(t, s.RequeueSessionFlush(ctx, flReq.ID))
	gotFlReq, err := s.GetSessionFlushJob(ctx, "ds-gone", "sf-req")
	must(t, err)
	if gotFlReq.Status != "cancelled_project_removed" {
		t.Fatalf("RequeueSessionFlush status = %q", gotFlReq.Status)
	}

	// RecoverStaleProcessingBefore -> cancels orphaned processing rows (not recovered to pending).
	recIn, recFl, err := s.RecoverStaleProcessingBefore(ctx, "")
	must(t, err)
	if recIn != 0 || recFl != 0 {
		t.Fatalf("RecoverStaleProcessingBefore recovered deleted project rows: (%d, %d)", recIn, recFl)
	}
	gotStaleIn, err := s.GetInboxItem(ctx, "in-stale")
	must(t, err)
	gotStaleFl, err := s.GetSessionFlushJob(ctx, "ds-gone", "sf-stale")
	must(t, err)
	if gotStaleIn.Status != "cancelled_project_removed" || gotStaleFl.ID != flStale.ID || gotStaleFl.Status != "cancelled_project_removed" {
		t.Fatalf("stale orphan statuses: inbox=%s flush=%s", gotStaleIn.Status, gotStaleFl.Status)
	}

	// Even if a pending row somehow existed in DB for a removed project, ClaimNext* must never claim it.
	must(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `UPDATE capture_inbox SET status='pending' WHERE id='in-requeue'`)
	}))
	_, err = s.ClaimNextInboxItem(ctx)
	wantError(t, err, ErrNotFound)
	must(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `UPDATE session_flush_jobs SET status='pending' WHERE id=?`, flReq.ID)
	}))
	_, err = s.ClaimNextSessionFlushExcludingDatasets(ctx, nil)
	wantError(t, err, ErrNotFound)
}

func TestDeleteProjectNormalizedUUIDCatalogCleanupAndSharedProtection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const (
		unformattedDS   = "11111111222233334444555566667777"
		canonicalDS     = "11111111-2222-3333-4444-555566667777"
		sharedUpperDS   = "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
		sharedCompactDS = "aaaaaaaabbbbccccddddeeeeeeeeeeee"
	)
	must(t, s.UpsertProject(ctx, Project{ID: "p-mix", Name: "Mix", DatasetID: unformattedDS, CodeDatasetID: sharedUpperDS}))
	must(t, s.UpsertProject(ctx, Project{ID: "p-keep", Name: "Keep", DatasetID: sharedCompactDS}))

	must(t, s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: canonicalDS, DocID: "d-del", Kind: "knowledge", IsKnowledge: true}))
	must(t, s.UpsertCatalogSession(ctx, CatalogSession{DatasetID: canonicalDS, UserID: "u1", SessionID: "s-del"}))
	must(t, s.UpsertCatalogSyncState(ctx, CatalogSyncState{DatasetID: canonicalDS, CatalogVersion: "v1"}))
	must(t, s.UpsertSessionRelation(ctx, SessionRelation{DatasetID: canonicalDS, SessionID: "s-del", ParentSessionID: "root"}))

	must(t, s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: sharedCompactDS, DocID: "d-shared", Kind: "knowledge", IsKnowledge: true}))

	must(t, s.DeleteProject(ctx, "p-mix"))

	var delDocs, delSess, delSync, delRel, sharedDocs int
	must(t, s.DB().QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM catalog_docs WHERE LOWER(REPLACE(dataset_id,'-',''))=?1),
		(SELECT count(*) FROM catalog_sessions WHERE LOWER(REPLACE(dataset_id,'-',''))=?1),
		(SELECT count(*) FROM catalog_sync_state WHERE LOWER(REPLACE(dataset_id,'-',''))=?1),
		(SELECT count(*) FROM session_relations WHERE LOWER(REPLACE(dataset_id,'-',''))=?1),
		(SELECT count(*) FROM catalog_docs WHERE LOWER(REPLACE(dataset_id,'-',''))=?2)`,
		unformattedDS, sharedCompactDS).Scan(&delDocs, &delSess, &delSync, &delRel, &sharedDocs))
	if delDocs != 0 || delSess != 0 || delSync != 0 || delRel != 0 {
		t.Fatalf("unformatted UUID project left catalog residue: docs=%d sess=%d sync=%d rel=%d", delDocs, delSess, delSync, delRel)
	}
	if sharedDocs != 1 {
		t.Fatalf("shared dataset catalog doc was wrongly deleted across UUID formats: sharedDocs=%d", sharedDocs)
	}
}

func TestReconnectedProjectFlushOwnershipHandoverAndOrphanInboxNonBlocking(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 1. 3B-R2-01: Old project A has a processing flush; A is removed and B reconnects same dataset;
	// B calls EnqueueSessionFlushJob -> takes ownership of project_id and sets rerun_requested=1 ->
	// when A's in-flight flush completes, it atomically requeues to pending for B instead of cancelling!
	must(t, s.UpsertProject(ctx, Project{ID: "p-a", Name: "A", DatasetID: "ds-reconn"}))
	fl, _, err := s.EnqueueSessionFlushJob(ctx, "p-a", "ds-reconn", "sess-handover")
	must(t, err)
	_, err = s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-reconn"})
	must(t, err)

	must(t, s.DeleteProject(ctx, "p-a"))
	must(t, s.UpsertProject(ctx, Project{ID: "p-b", Name: "B", DatasetID: "ds-reconn"}))

	handed, queued, err := s.EnqueueSessionFlushJob(ctx, "p-b", "ds-reconn", "sess-handover")
	must(t, err)
	if queued || handed.ProjectID != "p-b" || !handed.RerunRequested || handed.Status != "processing" {
		t.Fatalf("flush ownership handover failed: job=%+v queued=%v", handed, queued)
	}
	requeued, err := s.MarkSessionFlushSucceeded(ctx, fl.ID)
	must(t, err)
	if !requeued {
		t.Fatal("expected flush to requeue for active reconnected project B")
	}
	claimedByB, err := s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-reconn"})
	must(t, err)
	if claimedByB.ProjectID != "p-b" || claimedByB.ID != fl.ID {
		t.Fatalf("requeued flush not claimed for project B: %+v", claimedByB)
	}

	// 2. 3B-R2-02: Removed project's in-flight inbox failure marks cancelled_project_removed (not failed)
	// and never blocks reconnected project B's flush job.
	must(t, s.UpsertProject(ctx, Project{ID: "p-old", Name: "Old", DatasetID: "ds-unblock"}))
	_, err = s.ReserveAndEnqueueCapture(ctx, EnqueueCaptureInput{
		Receipt: CaptureReceipt{CaptureKey: "ck-old", UserID: "u", DatasetID: "ds-unblock", SessionID: "sess-unblock", MessageID: "m1", PayloadSHA256: "sha1", EntryType: "capture_qa"},
		Inbox:   CaptureInboxItem{ID: "in-old", ProjectID: "p-old", DatasetID: "ds-unblock", AgentID: "agent", SessionID: "sess-unblock", MessageID: "m1", CaptureKey: "ck-old", Kind: "capture_qa", PayloadJSON: `{}`},
	})
	must(t, err)
	_, err = s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.DeleteProject(ctx, "p-old"))
	must(t, s.MarkInboxFailed(ctx, "in-old", "engine error"))
	gotOld, err := s.GetInboxItem(ctx, "in-old")
	must(t, err)
	if gotOld.Status != "cancelled_project_removed" {
		t.Fatalf("removed project MarkInboxFailed status = %q, want cancelled_project_removed", gotOld.Status)
	}

	// Even if a legacy failed row from p-old existed, B's flush must not be blocked.
	must(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `UPDATE capture_inbox SET status='failed' WHERE id='in-old'`)
	}))
	must(t, s.UpsertProject(ctx, Project{ID: "p-new", Name: "New", DatasetID: "ds-unblock"}))
	flNew, _, err := s.EnqueueSessionFlushJob(ctx, "p-new", "ds-unblock", "sess-unblock")
	must(t, err)
	claimedNew, err := s.ClaimNextSessionFlushInDatasets(ctx, []string{"ds-unblock"})
	must(t, err)
	if claimedNew.ID != flNew.ID {
		t.Fatalf("orphan removed-project inbox row blocked new project flush: got %+v", claimedNew)
	}
}

func TestSchemaV5ToV6NormalizesBraceAndUrnUUIDs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v5-uuid.sqlite3")
	db, err := OpenPrivateSQLite(path)
	must(t, err)
	v5Schema := strings.ReplaceAll(schemaDDL,
		"CHECK(status IN ('pending','processing','succeeded','failed','cancelled_project_removed'))",
		"CHECK(status IN ('pending','processing','succeeded','failed'))")
	must(t, execSQL(ctx, db, v5Schema+`
		INSERT INTO schema_meta VALUES(1,5,'created','updated');
		INSERT INTO projects(id,name,dataset_id,code_dataset_id) VALUES('p1','P1','{11111111-2222-3333-4444-555566667777}','urn:uuid:AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE');
		INSERT INTO common_config(singleton,id,name,dataset_id) VALUES(1,'common','Common','{99999999-8888-7777-6666-555544443333}');
	`))
	must(t, db.Close())

	s, err := OpenStore(path)
	must(t, err)
	defer func() { must(t, s.Close()) }()
	p, err := s.GetProject(ctx, "p1")
	must(t, err)
	if p.DatasetID != "11111111-2222-3333-4444-555566667777" || p.CodeDatasetID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("v5->v6 did not normalize project UUIDs: %+v", p)
	}
	c, err := s.GetCommonConfig(ctx)
	must(t, err)
	if c.DatasetID != "99999999-8888-7777-6666-555544443333" {
		t.Fatalf("v5->v6 did not normalize common_config UUID: %+v", c)
	}
}
