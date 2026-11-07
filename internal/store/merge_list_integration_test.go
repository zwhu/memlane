package store

// Tests use real disposable SQLite and explicitly synthetic proposal payloads.
import (
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestMergeListPagingProvenanceAndExpiry(t *testing.T) {
	s, _ := mergeDB(t)
	x, r, b := mergeFixture(t, s)
	completeFixture(t, s, x, b)
	other, _, _ := mergeFixture(t, s)
	page, e := s.MergeList(mergeCtx, records.MergeListQuery{Project: x.Project, Limit: 1})
	mustMerge(t, e)
	if len(page.Items) != 1 || page.Items[0].ID != x.ID || page.Items[0].ReviewState != "pending" {
		t.Fatal(page)
	}
	_, e = s.Apply(mergeCtx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: x.Snapshot.Inputs[0].Memory, Dataset: x.Project, ExpectedRevision: 1, Text: "changed source"})
	mustMerge(t, e)
	page, e = s.MergeList(mergeCtx, records.MergeListQuery{Memory: x.Snapshot.Inputs[0].Memory})
	mustMerge(t, e)
	if len(page.Items) != 1 || page.Items[0].ReviewState != "expired" {
		t.Fatal(page)
	}
	all, e := s.MergeList(mergeCtx, records.MergeListQuery{Limit: 1})
	mustMerge(t, e)
	if len(all.Items) != 1 || all.Next == "" {
		t.Fatal(all)
	}
	tail, e := s.MergeList(mergeCtx, records.MergeListQuery{Limit: 1, Before: all.Next})
	mustMerge(t, e)
	if len(tail.Items) != 1 || tail.Items[0].ID == all.Items[0].ID || tail.Next != "" {
		t.Fatal(tail)
	}
	_, e = s.MergeList(mergeCtx, records.MergeListQuery{Project: x.Project, Before: other.ID})
	if e == nil {
		t.Fatal("cross project cursor allowed")
	}
	_, e = s.MergeList(mergeCtx, records.MergeListQuery{Limit: 101})
	if e == nil {
		t.Fatal("unbounded list")
	}
	_, e = s.MergeList(mergeCtx, records.MergeListQuery{Project: uuid.NewString()})
	if e == nil {
		t.Fatal("unknown project")
	}
	// Apply a different fresh task and find it from both the new result and each archive.
	fresh, decision, payload := mergeFixture(t, s)
	completeFixture(t, s, fresh, payload)
	applied, e := s.ReviewMerge(mergeCtx, decision, true)
	mustMerge(t, e)
	for _, id := range []string{applied.Receipt.Memory, fresh.Snapshot.Inputs[0].Memory, fresh.Snapshot.Inputs[1].Memory} {
		related, e := s.MergeList(mergeCtx, records.MergeListQuery{Memory: id})
		mustMerge(t, e)
		if len(related.Items) != 1 || related.Items[0].ID != fresh.ID || related.Items[0].ReviewState != "applied" {
			t.Fatal(related)
		}
	}
	// Listing never approves an expired proposal.
	_, e = s.ReviewMerge(mergeCtx, r, true)
	if e == nil {
		t.Fatal("expired proposal approved")
	}
	result, e := s.MergeTask(mergeCtx, x.ID)
	mustMerge(t, e)
	if result.ReviewState != "expired" || result.Receipt != nil {
		t.Fatal(result)
	}
}
