package store

import (
	"errors"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestMoveReviewNeedsSeparateConsentAndFreezesItInReceipt(t *testing.T) {
	s, _ := mergeDB(t)
	task, d := reviewOrganizationFixture(t, s, "move")
	d.MoveScopeAccepted = false
	before := sqlRows(t, s.DB, "versions")
	_, err := s.ReviewOrganization(mergeCtx, d)
	var access *records.AccessError
	if !errors.As(err, &access) || access.Code != "move_scope_confirmation_required" {
		t.Fatalf("missing consent accepted or incorrectly classified: %v", err)
	}
	if len(sqlRows(t, s.DB, "versions")) != len(before) || organizationCount(t, s, `SELECT count(*) FROM organization_decisions`) != 0 {
		t.Fatal("failed consent changed knowledge or recorded a decision")
	}
	d.MoveScopeAccepted = true
	applied, err := s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, err)
	if applied.Entries[0].Receipt == nil || !applied.Entries[0].Receipt.MoveScopeAccepted {
		t.Fatal("receipt lost separate scope consent")
	}
	_, err = s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, err)
	changed := d
	changed.MoveScopeAccepted = false
	if _, err = s.ReviewOrganization(mergeCtx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("same receipt accepted a changed consent payload", err)
	}
	v, err := s.Current(mergeCtx, task.Entries[0].Item.Target)
	mustMerge(t, err)
	if v.Revision != 2 || v.Dataset != task.Snapshot.Destinations[0].ID {
		t.Fatal("replay moved knowledge again or changed destination", v)
	}
}

func TestMoveReviewCommittedLegacyReceiptRemainsReadable(t *testing.T) {
	s, _ := mergeDB(t)
	_, d := reviewOrganizationFixture(t, s, "move")
	_, err := s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, err)
	// A pre-contract receipt has no new consent field. Replaying it may only
	// return that known decision, never run the move a second time.
	_, err = s.DB.Exec(`UPDATE organization_decisions SET receipt=json_remove(receipt,'$.move_scope_accepted') WHERE id=?`, d.Receipt)
	mustMerge(t, err)
	d.MoveScopeAccepted = false
	before := organizationCount(t, s, `SELECT count(*) FROM versions`)
	_, err = s.ReviewOrganization(mergeCtx, d)
	mustMerge(t, err)
	if organizationCount(t, s, `SELECT count(*) FROM versions`) != before {
		t.Fatal("legacy receipt replay mutated knowledge")
	}
}
