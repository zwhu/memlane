package domain

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openCatalogPublishTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "catalog_publish.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestPublishDatasetCatalogIncrementalIsolationAndMonotonicVersion(t *testing.T) {
	ctx := context.Background()
	store := openCatalogPublishTestStore(t)

	dsA := "11111111-1111-4111-8111-111111111111"
	dsB := "22222222-2222-4222-8222-222222222222"
	if err := store.UpsertProject(ctx, Project{ID: "p-a", Name: "A", DatasetID: dsA}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProject(ctx, Project{ID: "p-b", Name: "B", DatasetID: dsB}); err != nil {
		t.Fatal(err)
	}

	// Publish dataset B first.
	statsB, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: dsB,
		Exists:    true,
		Docs: []CatalogPublishDoc{{
			CatalogDoc: CatalogDoc{
				DocID: "doc-b1", Kind: "knowledge", IsKnowledge: true, DocLabel: "B Doc 1",
				EffectiveDate: "2026-11-20", DayDescKey: -20261120,
			},
		}},
		Sessions: []CatalogSession{{
			SessionID: "sess-b1", UserID: "u-b", Status: "active",
		}},
	})
	if err != nil || statsB.DocsProjected != 1 || statsB.SessionsProjected != 1 {
		t.Fatalf("publish B = %+v, %v", statsB, err)
	}
	stateB1, err := store.GetCatalogSyncState(ctx, dsB)
	if err != nil || stateB1.CatalogVersion != "1" {
		t.Fatalf("state B1 = %+v, %v", stateB1, err)
	}

	// Publish dataset A: must NOT touch dataset B.
	docA1 := CatalogPublishDoc{
		CatalogDoc: CatalogDoc{
			DocID: "doc-a1", Kind: "knowledge", IsKnowledge: true, DocLabel: "A Doc 1",
			EffectiveDate: "2026-11-21", DayDescKey: -20261121,
		},
	}
	if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: dsA,
		Exists:    true,
		Docs:      []CatalogPublishDoc{docA1},
	}); err != nil {
		t.Fatal(err)
	}
	stateA1, err := store.GetCatalogSyncState(ctx, dsA)
	if err != nil || stateA1.CatalogVersion != "1" {
		t.Fatalf("state A1 = %+v, %v", stateA1, err)
	}

	// Verify dataset B is completely untouched.
	docsB, err := store.ListCatalogDocs(ctx, dsB, "knowledge", 10)
	if err != nil || len(docsB) != 1 || docsB[0].DocID != "doc-b1" {
		t.Fatalf("dataset B docs altered = %+v, %v", docsB, err)
	}
	stateBAfter, err := store.GetCatalogSyncState(ctx, dsB)
	if err != nil || stateBAfter.CatalogVersion != "1" || stateBAfter.SyncedAt != stateB1.SyncedAt {
		t.Fatalf("dataset B sync state altered = %+v vs %+v", stateBAfter, stateB1)
	}

	// Republish dataset A with identical content -> CatalogVersion stays "1", doc_seq preserved.
	docsA1, _ := store.ListCatalogDocs(ctx, dsA, "knowledge", 10)
	if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: dsA,
		Exists:    true,
		Docs:      []CatalogPublishDoc{docA1},
	}); err != nil {
		t.Fatal(err)
	}
	stateAUnchanged, err := store.GetCatalogSyncState(ctx, dsA)
	if err != nil || stateAUnchanged.CatalogVersion != "1" {
		t.Fatalf("unchanged republish bumped version: %+v", stateAUnchanged)
	}
	docsAUnchanged, _ := store.ListCatalogDocs(ctx, dsA, "knowledge", 10)
	if docsAUnchanged[0].DocSeq != docsA1[0].DocSeq {
		t.Fatalf("doc_seq changed on republish: %d vs %d", docsAUnchanged[0].DocSeq, docsA1[0].DocSeq)
	}

	// Modify title of doc-a1 -> CatalogVersion bumps to "2".
	docA1.DocLabel = "A Doc 1 Updated"
	if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: dsA,
		Exists:    true,
		Docs:      []CatalogPublishDoc{docA1},
	}); err != nil {
		t.Fatal(err)
	}
	stateA2, err := store.GetCatalogSyncState(ctx, dsA)
	if err != nil || stateA2.CatalogVersion != "2" {
		t.Fatalf("changed publish version = %q, want 2", stateA2.CatalogVersion)
	}

	// PublishDatasetSessions only updates sessions, preserves catalog_docs, and bumps version to "3".
	if _, err := store.PublishDatasetSessions(ctx, dsA, []CatalogSession{
		{SessionID: "sess-a1", UserID: "u-a", Status: "active"},
	}); err != nil {
		t.Fatal(err)
	}
	stateA3, err := store.GetCatalogSyncState(ctx, dsA)
	if err != nil || stateA3.CatalogVersion != "3" || stateA3.SessionCount != 1 || stateA3.DocCount != 1 {
		t.Fatalf("after PublishDatasetSessions state = %+v", stateA3)
	}
	docsAAfterSess, _ := store.ListCatalogDocs(ctx, dsA, "knowledge", 10)
	if len(docsAAfterSess) != 1 || docsAAfterSess[0].DocLabel != "A Doc 1 Updated" {
		t.Fatalf("PublishDatasetSessions altered catalog_docs: %+v", docsAAfterSess)
	}

	// Identical PublishDatasetSessions keeps CatalogVersion at "3".
	if _, err := store.PublishDatasetSessions(ctx, dsA, []CatalogSession{
		{SessionID: "sess-a1", UserID: "u-a", Status: "active"},
	}); err != nil {
		t.Fatal(err)
	}
	stateA3Again, err := store.GetCatalogSyncState(ctx, dsA)
	if err != nil || stateA3Again.CatalogVersion != "3" {
		t.Fatalf("unchanged session publish bumped version: %+v", stateA3Again)
	}
}

func TestGate11StatePitfallReplacementDateRecallAndReadMigration(t *testing.T) {
	ctx := context.Background()
	store := openCatalogPublishTestStore(t)

	ds := "11111111-1111-4111-8111-111111111111"
	if err := store.UpsertProject(ctx, Project{ID: "proj-1", Name: "P1", DatasetID: ds}); err != nil {
		t.Fatal(err)
	}
	oldStateDoc := "aaaa1111-1111-4111-8111-111111111111"
	newStateDoc := "bbbb2222-2222-4222-8222-222222222222"
	oldPitfallDoc := "cccc3333-3333-4333-8333-333333333333"
	newPitfallDoc := "dddd4444-4444-4444-8444-444444444444"
	convDoc1 := "eeee5555-5555-4555-8555-555555555555"
	convDoc2 := "ffff6666-6666-4666-8666-666666666666"

	// Initial publish with state, pitfall, and convention docs.
	_, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: ds,
		Exists:    true,
		Docs: []CatalogPublishDoc{
			{
				CatalogDoc: CatalogDoc{
					DocID: oldStateDoc, Kind: "knowledge", IsKnowledge: true,
					LessonType: "state", StateKey: "build_status", DocLabel: "Old Build State",
					CreatedAtNS: "2026-10-01T10:00:00.000000000Z", EffectiveDate: "2026-10-01",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: oldStateDoc, LessonType: "state", StateKey: "build_status",
					FirstSourceDate: "2026-10-01", SourceDateStatus: "linked_qa", SourceQAIDs: []string{"qa-old"},
				},
			},
			{
				CatalogDoc: CatalogDoc{
					DocID: oldPitfallDoc, Kind: "knowledge", IsKnowledge: true,
					LessonType: "pitfall", StateKey: "wal_lock", DocLabel: "Old Pitfall",
					CreatedAtNS: "2026-10-05T10:00:00.000000000Z", EffectiveDate: "2026-10-05",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: oldPitfallDoc, LessonType: "pitfall", StateKey: "wal_lock",
					FirstSourceDate: "2026-10-05", SourceDateStatus: "session_date",
				},
			},
			{
				CatalogDoc: CatalogDoc{
					DocID: convDoc1, Kind: "knowledge", IsKnowledge: true,
					LessonType: "convention", StateKey: "", DocLabel: "Convention 1",
					CreatedAtNS: "2026-10-02T10:00:00.000000000Z", EffectiveDate: "2026-10-02",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: convDoc1, LessonType: "convention",
					FirstSourceDate: "2026-10-02", SourceDateStatus: "session_date",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Record recalls on oldStateDoc (explicit=2, auto=1) and convDoc1 (auto=1).
	for i, ch := range []string{"explicit", "explicit", "automatic"} {
		if _, err := store.RecordRecallEvent(ctx, RecallEventInput{
			AgentID: "agent", EventID: "ev-state-" + string(rune('a'+i)), Channel: ch,
			Hits: []RecallHit{{DatasetID: ds, DocumentID: oldStateDoc}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.RecordRecallEvent(ctx, RecallEventInput{
		AgentID: "agent", EventID: "ev-conv-1", Channel: "automatic",
		Hits: []RecallHit{{DatasetID: ds, DocumentID: convDoc1}},
	}); err != nil {
		t.Fatal(err)
	}

	// Set up read scope:
	// - oldStateDoc explicitly marked read (is_read=1)
	// - oldPitfallDoc implicitly read via horizon (2026-10-10 > 2026-10-05)
	// - convDoc1 explicitly marked unread (is_read=0)
	scopeHash := KnowledgeReadScopeHash("reader-1", "proj-1", ds)
	if err := store.UpsertReadScope(ctx, ReadScope{
		ScopeHash: scopeHash, ProjectID: "proj-1", DatasetID: ds,
		Initialized: true, EnabledAtNS: "2026-10-10T00:00:00.000000000Z",
		ReadHorizonAtNS: "2026-10-10T00:00:00.000000000Z", StateRev: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadItem(ctx, scopeHash, oldStateDoc, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadItem(ctx, scopeHash, convDoc1, false); err != nil {
		t.Fatal(err)
	}
	scopeBefore, err := store.GetReadScope(ctx, scopeHash)
	if err != nil {
		t.Fatal(err)
	}

	// Now publish replacement:
	// - oldStateDoc replaced by newStateDoc (newer date 2026-11-25)
	// - oldPitfallDoc replaced by newPitfallDoc (newer date 2026-11-26)
	// - convDoc1 coexists with convDoc2 (no migration)
	_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: ds,
		Exists:    true,
		Docs: []CatalogPublishDoc{
			{
				CatalogDoc: CatalogDoc{
					DocID: newStateDoc, Kind: "knowledge", IsKnowledge: true,
					LessonType: "state", StateKey: "build_status", DocLabel: "New Build State",
					CreatedAtNS: "2026-11-25T10:00:00.000000000Z", EffectiveDate: "2026-11-25",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: newStateDoc, LessonType: "state", StateKey: "build_status",
					FirstSourceDate: "2026-11-25", SourceDateStatus: "linked_qa", SourceQAIDs: []string{"qa-new"},
				},
			},
			{
				CatalogDoc: CatalogDoc{
					DocID: newPitfallDoc, Kind: "knowledge", IsKnowledge: true,
					LessonType: "pitfall", StateKey: "wal_lock", DocLabel: "New Pitfall",
					CreatedAtNS: "2026-11-26T10:00:00.000000000Z", EffectiveDate: "2026-11-26",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: newPitfallDoc, LessonType: "pitfall", StateKey: "wal_lock",
					FirstSourceDate: "2026-11-26", SourceDateStatus: "session_date",
				},
			},
			{
				CatalogDoc: CatalogDoc{
					DocID: convDoc1, Kind: "knowledge", IsKnowledge: true,
					LessonType: "convention", StateKey: "", DocLabel: "Convention 1",
					CreatedAtNS: "2026-10-02T10:00:00.000000000Z", EffectiveDate: "2026-10-02",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: convDoc1, LessonType: "convention",
					FirstSourceDate: "2026-10-02", SourceDateStatus: "session_date",
				},
			},
			{
				CatalogDoc: CatalogDoc{
					DocID: convDoc2, Kind: "knowledge", IsKnowledge: true,
					LessonType: "convention", StateKey: "", DocLabel: "Convention 2",
					CreatedAtNS: "2026-11-27T10:00:00.000000000Z", EffectiveDate: "2026-11-27",
				},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: convDoc2, LessonType: "convention",
					FirstSourceDate: "2026-11-27", SourceDateStatus: "session_date",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListCatalogDocs(ctx, ds, "knowledge", 20)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]CatalogDoc{}
	for _, d := range docs {
		byID[d.DocID] = d
	}

	// 1. Date must NOT invert back to 2026-10-01 / 2026-10-05; keyed_provenance must update.
	if got := byID[newStateDoc]; got.EffectiveDate != "2026-11-25" || got.RecallCount != 3 {
		t.Fatalf("newStateDoc = %+v, want date=2026-11-25 recall_count=3", got)
	}
	if got := byID[newPitfallDoc]; got.EffectiveDate != "2026-11-26" {
		t.Fatalf("newPitfallDoc = %+v, want date=2026-11-26", got)
	}
	kpState, err := store.GetKeyedProvenance(ctx, ds, "state", "build_status")
	if err != nil || kpState.FirstSourceDate != "2026-11-25" || kpState.SourceDateStatus != "linked_qa" {
		t.Fatalf("keyed_provenance(state) = %+v, %v", kpState, err)
	}
	kpPitfall, err := store.GetKeyedProvenance(ctx, ds, "pitfall", "wal_lock")
	if err != nil || kpPitfall.FirstSourceDate != "2026-11-26" {
		t.Fatalf("keyed_provenance(pitfall) = %+v, %v", kpPitfall, err)
	}

	// 2. Recall counts migrated to newStateDoc; oldStateDoc row deleted; historical recall_events intact.
	rcNew, err := store.GetRecallDocCount(ctx, ds, newStateDoc)
	if err != nil || rcNew.TotalHits != 3 || rcNew.ExplicitHits != 2 || rcNew.AutoHits != 1 {
		t.Fatalf("migrated recall_doc_counts = %+v, %v", rcNew, err)
	}
	if _, err := store.GetRecallDocCount(ctx, ds, oldStateDoc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old recall_doc_counts row should be deleted, got %v", err)
	}
	var eventCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM recall_events`).Scan(&eventCount); err != nil || eventCount != 4 {
		t.Fatalf("historical recall_events should remain intact (4), got %d, %v", eventCount, err)
	}

	// 3. Read states migrated (explicit + horizon-implicit), old rows deleted, state_rev bumped.
	riNewState, err := store.GetReadItem(ctx, scopeHash, newStateDoc)
	if err != nil || !riNewState.IsRead {
		t.Fatalf("migrated explicit read item(state) = %+v, %v", riNewState, err)
	}
	if _, err := store.GetReadItem(ctx, scopeHash, oldStateDoc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old ui_read_items row should be deleted, got %v", err)
	}
	riNewPitfall, err := store.GetReadItem(ctx, scopeHash, newPitfallDoc)
	if err != nil || !riNewPitfall.IsRead {
		t.Fatalf("migrated horizon-implicit read item(pitfall) = %+v, %v", riNewPitfall, err)
	}
	scopeAfter, err := store.GetReadScope(ctx, scopeHash)
	if err != nil || scopeAfter.StateRev <= scopeBefore.StateRev {
		t.Fatalf("state_rev should bump: before=%d after=%d", scopeBefore.StateRev, scopeAfter.StateRev)
	}

	// 4. Conventions coexist without migration.
	if byID[convDoc1].RecallCount != 1 || byID[convDoc2].RecallCount != 0 {
		t.Fatalf("convention recall counts unexpectedly migrated: c1=%d c2=%d", byID[convDoc1].RecallCount, byID[convDoc2].RecallCount)
	}
	if riConv1, err := store.GetReadItem(ctx, scopeHash, convDoc1); err != nil || riConv1.IsRead {
		t.Fatalf("convDoc1 read state altered: %+v, %v", riConv1, err)
	}
	if _, err := store.GetReadItem(ctx, scopeHash, convDoc2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("convDoc2 should not inherit read state: %v", err)
	}

	// 5. Re-scanning the SAME doc_id still locks its own earliest date.
	_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
		DatasetID: ds,
		Exists:    true,
		Docs: []CatalogPublishDoc{{
			CatalogDoc: CatalogDoc{
				DocID: newStateDoc, Kind: "knowledge", IsKnowledge: true,
				LessonType: "state", StateKey: "build_status", DocLabel: "New Build State",
				CreatedAtNS: "2026-11-28T10:00:00.000000000Z", EffectiveDate: "2026-11-28",
			},
			Provenance: &DocumentProvenance{
				DatasetID: ds, DocumentID: newStateDoc, LessonType: "state", StateKey: "build_status",
				FirstSourceDate: "2026-11-28", SourceDateStatus: "created_at",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rescanDocs, _ := store.ListCatalogDocs(ctx, ds, "knowledge", 10)
	if len(rescanDocs) != 1 || rescanDocs[0].EffectiveDate != "2026-11-25" {
		t.Fatalf("re-scan of same doc_id should keep earliest date 2026-11-25, got %+v", rescanDocs)
	}
}

func TestR1DomainFixes(t *testing.T) {
	ctx := context.Background()

	t.Run("R1-02_unmanaged_dataset_in_tx_cleans_up_without_ghost", func(t *testing.T) {
		store := openCatalogPublishTestStore(t)
		ds := "99999999-9999-4999-8999-999999999999"
		if err := store.UpsertProject(ctx, Project{ID: "p-rm", Name: "RM", DatasetID: ds}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{CatalogDoc: CatalogDoc{DocID: "d1", Kind: "knowledge", IsKnowledge: true}}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteProject(ctx, "p-rm"); err != nil {
			t.Fatal(err)
		}
		// All publish & state writers must detect unmanaged dataset inside tx, clean up, and return nil.
		if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{CatalogDoc: CatalogDoc{DocID: "ghost", Kind: "knowledge", IsKnowledge: true}}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishDatasetSessions(ctx, ds, []CatalogSession{{SessionID: "ghost-s", UserID: "u"}}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkCatalogPublishFailed(ctx, ds, "p-rm", "err"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkCatalogPublishPending(ctx, ds, "p-rm"); err != nil {
			t.Fatal(err)
		}
		if docs, _ := store.ListCatalogDocs(ctx, ds, "knowledge", 10); len(docs) != 0 {
			t.Fatalf("ghost docs resurrected: %+v", docs)
		}
		if _, err := store.GetCatalogPublishState(ctx, ds); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ghost catalog_publish_state remained: %v", err)
		}
	})

	t.Run("R1-05_v5_legacy_cross_date_self_heal_and_preserve_real_early_qa", func(t *testing.T) {
		store := openCatalogPublishTestStore(t)
		ds := "11111111-1111-4111-8111-111111111111"
		if err := store.UpsertProject(ctx, Project{ID: "p1", Name: "P1", DatasetID: ds}); err != nil {
			t.Fatal(err)
		}
		// Simulate v5 polluted record: doc-b received old doc-a's 2026-11-01 session_date (no own early linked_qa).
		_, err := store.DB().ExecContext(ctx, `
			INSERT INTO keyed_provenance VALUES(?,'state','k1','2026-11-01','session_date','2026-11-01T00:00:00Z');
			INSERT INTO document_provenance VALUES(?,'doc-b','state','k1','2026-11-01','session_date','[]','2026-11-01T00:00:00Z',1);
		`, ds, ds)
		if err != nil {
			t.Fatal(err)
		}
		// Rescan doc-b with authoritative 2026-11-20 linked_qa -> must self-heal to 2026-11-20!
		_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{
				CatalogDoc: CatalogDoc{DocID: "doc-b", Kind: "knowledge", IsKnowledge: true, LessonType: "state", StateKey: "k1"},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: "doc-b", LessonType: "state", StateKey: "k1",
					FirstSourceDate: "2026-11-20", SourceDateStatus: "linked_qa", SourceQAIDs: []string{"qa-b"},
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		docs, _ := store.ListCatalogDocs(ctx, ds, "knowledge", 10)
		if len(docs) != 1 || docs[0].EffectiveDate != "2026-11-20" {
			t.Fatalf("v5 cross-date did not self-heal: %+v", docs)
		}
		kp, _ := store.GetKeyedProvenance(ctx, ds, "state", "k1")
		if kp.FirstSourceDate != "2026-11-20" {
			t.Fatalf("keyed_provenance not healed: %+v", kp)
		}
		// Now rescan doc-b with non-authoritative later 2026-11-25 created_at -> must stay 2026-11-20!
		_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{
				CatalogDoc: CatalogDoc{DocID: "doc-b", Kind: "knowledge", IsKnowledge: true, LessonType: "state", StateKey: "k1"},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: "doc-b", LessonType: "state", StateKey: "k1",
					FirstSourceDate: "2026-11-25", SourceDateStatus: "created_at",
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		docs2, _ := store.ListCatalogDocs(ctx, ds, "knowledge", 10)
		if len(docs2) != 1 || docs2[0].EffectiveDate != "2026-11-20" {
			t.Fatalf("real early linked_qa was overwritten by non-authoritative fallback: %+v", docs2)
		}

		// 3A-R2-05: v5 polluted record with linked_qa and non-empty SourceQAIDs also self-heals on authoritative incoming evidence.
		_, err = store.DB().ExecContext(ctx, `
			INSERT INTO keyed_provenance VALUES(?,'pitfall','k2','2026-10-01','linked_qa','2026-10-01T00:00:00Z');
			INSERT INTO document_provenance VALUES(?,'doc-c','pitfall','k2','2026-10-01','linked_qa','["qa-old"]','2026-10-01T00:00:00Z',2);
		`, ds, ds)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{
				CatalogDoc: CatalogDoc{DocID: "doc-c", Kind: "knowledge", IsKnowledge: true, LessonType: "pitfall", StateKey: "k2"},
				Provenance: &DocumentProvenance{
					DatasetID: ds, DocumentID: "doc-c", LessonType: "pitfall", StateKey: "k2",
					FirstSourceDate: "2026-11-22", SourceDateStatus: "linked_qa", SourceQAIDs: []string{"qa-c"},
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		docs3, _ := store.ListCatalogDocs(ctx, ds, "knowledge", 10)
		if len(docs3) != 1 || docs3[0].EffectiveDate != "2026-11-22" {
			t.Fatalf("3A-R2-05 v5 linked_qa cross-date did not self-heal: %+v", docs3)
		}
	})

	t.Run("R1-06_07_08_09_uppercase_uuid_recall_redirects_atomic_read_and_version_bump", func(t *testing.T) {
		store := openCatalogPublishTestStore(t)
		ds := "11111111-2222-4333-8444-555555555555"
		docOld := "aaaa1111-2222-4333-8444-555555555555"
		docNew := "bbbb1111-2222-4333-8444-555555555555"
		if err := store.UpsertProject(ctx, Project{ID: "p1", Name: "P1", DatasetID: ds}); err != nil {
			t.Fatal(err)
		}
		// Seed initial docOld with invalid created_at (implicit unread).
		_, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{
				CatalogDoc: CatalogDoc{
					DocID: docOld, Kind: "knowledge", IsKnowledge: true,
					LessonType: "state", StateKey: "k-mig", CreatedAtInvalid: true,
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		// Seed uppercase UUID recall_doc_counts row (R1-06).
		_, err = store.DB().ExecContext(ctx,
			`INSERT INTO recall_doc_counts VALUES(?,?,2,1,1,'2026-11-20T00:00:00Z',1)`,
			"11111111-2222-4333-8444-555555555555", "AAAA1111-2222-4333-8444-555555555555")
		if err != nil {
			t.Fatal(err)
		}
		// Set up read scope with horizon > new doc's created_at_ns to test P2 invalid-date implicit unread preservation.
		scopeHash := KnowledgeReadScopeHash("reader", "p1", ds)
		if err := store.UpsertReadScope(ctx, ReadScope{
			ScopeHash: scopeHash, ProjectID: "p1", DatasetID: ds, Initialized: true,
			EnabledAtNS: "2026-11-27T00:00:00.000000000Z", ReadHorizonAtNS: "2026-11-27T00:00:00.000000000Z", StateRev: 1,
		}); err != nil {
			t.Fatal(err)
		}

		// Replace docOld -> docNew (new doc has created_at_ns <= horizon).
		docNewInput := CatalogPublishDoc{
			CatalogDoc: CatalogDoc{
				DocID: docNew, Kind: "knowledge", IsKnowledge: true,
				LessonType: "state", StateKey: "k-mig",
				CreatedAtRaw: "2026-11-26T00:00:00Z", CreatedAtNS: "2026-11-26T00:00:00.000000000Z",
			},
		}
		_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true, Docs: []CatalogPublishDoc{docNewInput},
		})
		if err != nil {
			t.Fatal(err)
		}
		// R1-06: uppercase UUID recall migrated to docNew (total=2).
		rc, err := store.GetRecallDocCount(ctx, ds, docNew)
		if err != nil || rc.TotalHits != 2 {
			t.Fatalf("uppercase UUID recall migration failed: %+v, %v", rc, err)
		}
		// P2: invalid-date implicit unread preserved as explicit is_read=0 so docNew is still unread.
		ri, err := store.GetReadItem(ctx, scopeHash, docNew)
		if err != nil || ri.IsRead {
			t.Fatalf("implicit unread not preserved across replacement: %+v, %v", ri, err)
		}

		// R1-07 & R1-09: in-flight recall against old docOld follows redirect to docNew and bumps CatalogVersion!
		stBeforeRecall, _ := store.GetCatalogSyncState(ctx, ds)
		if _, err := store.RecordRecallEvent(ctx, RecallEventInput{
			AgentID: "ag", EventID: "ev-inflight", Channel: "explicit",
			Hits: []RecallHit{{DatasetID: ds, DocumentID: docOld}},
		}); err != nil {
			t.Fatal(err)
		}
		rcAfter, _ := store.GetRecallDocCount(ctx, ds, docNew)
		if rcAfter.TotalHits != 3 {
			t.Fatalf("in-flight recall did not follow redirect to docNew: %+v", rcAfter)
		}
		stAfterRecall, _ := store.GetCatalogSyncState(ctx, ds)
		if stAfterRecall.CatalogVersion == stBeforeRecall.CatalogVersion {
			t.Fatalf("RecordRecallEvent did not bump CatalogVersion: %q", stAfterRecall.CatalogVersion)
		}
		// Subsequent unchanged PublishDatasetCatalog must NOT bump CatalogVersion a second time (R1-09).
		_, err = store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true, Docs: []CatalogPublishDoc{docNewInput},
		})
		if err != nil {
			t.Fatal(err)
		}
		stAfterNoop, _ := store.GetCatalogSyncState(ctx, ds)
		if stAfterNoop.CatalogVersion != stAfterRecall.CatalogVersion {
			t.Fatalf("unchanged publish double-bumped CatalogVersion after recall: %q -> %q",
				stAfterRecall.CatalogVersion, stAfterNoop.CatalogVersion)
		}

		// R1-08: MarkCatalogDocsRead using old docOld follows redirect atomically and marks docNew read.
		if err := store.MarkCatalogDocsRead(ctx, scopeHash, ds, []string{docOld}); err != nil {
			t.Fatalf("MarkCatalogDocsRead(docOld) failed: %v", err)
		}
		riAfterRead, err := store.GetReadItem(ctx, scopeHash, docNew)
		if err != nil || !riAfterRead.IsRead {
			t.Fatalf("MarkCatalogDocsRead did not mark redirected docNew read: %+v, %v", riAfterRead, err)
		}
	})

	t.Run("R2_01_02_03_06_07_surgical_edge_cases", func(t *testing.T) {
		store := openCatalogPublishTestStore(t)
		ds := "11111111-2222-4333-8444-555555555555"
		dsHex := "11111111222243338444555555555555"
		if err := store.UpsertProject(ctx, Project{ID: "p-r2", Name: "R2", DatasetID: ds}); err != nil {
			t.Fatal(err)
		}

		// 3A-R2-01: older snapshot must not overwrite newer pending state.
		olderSnapshotAt := "2026-01-01T00:00:00.000000+00:00"
		if err := store.MarkCatalogPublishPending(ctx, dsHex, "p-r2"); err != nil {
			t.Fatal(err)
		}
		// 3A-R2-03: non-hyphenated UUID was normalized to canonical ds.
		stPending, err := store.GetCatalogPublishState(ctx, ds)
		if err != nil || stPending.DatasetID != ds || stPending.Status != "pending" {
			t.Fatalf("normalized pending state = %+v, %v", stPending, err)
		}
		if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true, SnapshotAt: olderSnapshotAt,
			Docs: []CatalogPublishDoc{{CatalogDoc: CatalogDoc{DocID: "d-old", Kind: "knowledge", IsKnowledge: true, LessonType: "state", StateKey: "k-horizon", CreatedAtNS: "2026-11-28T00:00:00.000000000Z"}}},
		}); err != nil {
			t.Fatal(err)
		}
		stStillPending, err := store.GetCatalogPublishState(ctx, ds)
		if err != nil || stStillPending.Status != "pending" {
			t.Fatalf("older snapshot clobbered newer pending state: %+v, %v", stStillPending, err)
		}

		// 3A-R2-02: DeleteCatalogForDataset is a no-op when dataset is managed.
		if err := store.DeleteCatalogForDataset(ctx, ds); err != nil {
			t.Fatal(err)
		}
		if docs, _ := store.ListCatalogDocs(ctx, ds, "knowledge", 10); len(docs) != 1 {
			t.Fatalf("DeleteCatalogForDataset deleted managed dataset docs: %+v", docs)
		}

		// 3A-R2-07: old valid doc (> horizon, implicitly unread) replaced by new doc (<= horizon) must remain unread via explicit is_read=0.
		scopeHash := KnowledgeReadScopeHash("reader-r2", "p-r2", ds)
		if err := store.UpsertReadScope(ctx, ReadScope{
			ScopeHash: scopeHash, ProjectID: "p-r2", DatasetID: ds, Initialized: true,
			EnabledAtNS: "2026-11-27T00:00:00.000000000Z", ReadHorizonAtNS: "2026-11-27T00:00:00.000000000Z", StateRev: 1,
		}); err != nil {
			t.Fatal(err)
		}
		// 3A-R2-06: non-canonical variant row on newDocID itself gets merged into canonical (ds, d-new) on migration.
		newDoc := "cccccccc-2222-4333-8444-555555555555"
		if _, err := store.DB().ExecContext(ctx,
			`INSERT INTO recall_doc_counts VALUES(?,'d-old',1,1,0,'2026-11-20T00:00:00Z',1)`, ds); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(ctx,
			`INSERT INTO recall_doc_counts VALUES(?,?,2,0,2,'2026-11-21T00:00:00Z',2)`,
			dsHex, "CCCCCCCC-2222-4333-8444-555555555555"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishDatasetCatalog(ctx, CatalogPublishInput{
			DatasetID: ds, Exists: true,
			Docs: []CatalogPublishDoc{{CatalogDoc: CatalogDoc{DocID: newDoc, Kind: "knowledge", IsKnowledge: true, LessonType: "state", StateKey: "k-horizon", CreatedAtNS: "2026-11-26T00:00:00.000000000Z"}}},
		}); err != nil {
			t.Fatal(err)
		}
		ri, err := store.GetReadItem(ctx, scopeHash, newDoc)
		if err != nil || ri.IsRead {
			t.Fatalf("3A-R2-07 valid-date >horizon unread not preserved across <=horizon replacement: %+v, %v", ri, err)
		}
		rc, err := store.GetRecallDocCount(ctx, ds, newDoc)
		if err != nil || rc.TotalHits != 3 {
			t.Fatalf("3A-R2-06 non-canonical newDocID variant not consolidated: %+v, %v", rc, err)
		}
		var variantCount int
		if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM recall_doc_counts WHERE LOWER(REPLACE(document_id,'-',''))=?`, normalizedUUIDHex(newDoc)).Scan(&variantCount); err != nil || variantCount != 1 {
			t.Fatalf("expected single canonical recall_doc_counts row, got %d, %v", variantCount, err)
		}
	})
}
