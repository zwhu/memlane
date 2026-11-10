package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestOrganizationRejectionReasonIsFrozenReceiptAndLeavesKnowledge(t *testing.T) {
	for _, action := range []string{"merge", "overwrite", "delete", "move", "duplicate", "conflict", "global_experience"} {
		t.Run(action, func(t *testing.T) {
			s, _ := mergeDB(t)
			var task records.OrganizationTask
			var d records.OrganizationReview
			if action == "global_experience" {
				task, d = globalExperienceFixture(t, s)
			} else {
				task, d = reviewOrganizationFixture(t, s, action)
			}
			d.Confirm, d.Edit, d.RejectionReason = false, nil, "暂时保留现有约定；待补充独立依据。"
			before := sqlRows(t, s.DB, "versions")
			done, err := s.ReviewOrganization(mergeCtx, d)
			mustMerge(t, err)
			if done.Entries[0].State != "rejected" || done.Entries[0].Receipt == nil || done.Entries[0].Receipt.RejectionReason != d.RejectionReason || done.Entries[0].Receipt.Memory != "" {
				t.Fatal("rejection receipt missing", done)
			}
			if !reflect.DeepEqual(task.Entries[0].Item, done.Entries[0].Item) || !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
				t.Fatal("rejection changed model proposal or knowledge")
			}
			read, err := s.OrganizationTask(mergeCtx, task.ID)
			mustMerge(t, err)
			if !reflect.DeepEqual(read.Entries[0].Receipt, done.Entries[0].Receipt) {
				t.Fatal("read lost decision receipt")
			}
			_, err = s.ReviewOrganization(mergeCtx, d)
			mustMerge(t, err)
			d.RejectionReason = "changed reason"
			if _, err = s.ReviewOrganization(mergeCtx, d); !errors.Is(err, ErrConflict) {
				t.Fatal("receipt accepted a different reason", err)
			}
			if !reflect.DeepEqual(before, sqlRows(t, s.DB, "versions")) {
				t.Fatal("replay changed knowledge")
			}
		})
	}
}

func TestOrganizationRejectReasonValidationBeforeDecision(t *testing.T) {
	for _, tc := range []struct {
		reason  string
		confirm bool
	}{{strings.Repeat("a", 1025), false}, {string([]byte{0xff}), false}, {"reason belongs only to rejection", true}} {
		s, _ := mergeDB(t)
		_, d := reviewOrganizationFixture(t, s, "merge")
		d.RejectionReason, d.Confirm = tc.reason, tc.confirm
		if _, err := s.ReviewOrganization(mergeCtx, d); err == nil {
			t.Fatal("invalid reason accepted")
		}
		if organizationCount(t, s, "SELECT count(*) FROM organization_decisions") != 0 {
			t.Fatal("invalid reason persisted a decision")
		}
	}
}
